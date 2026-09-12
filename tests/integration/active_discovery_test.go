package integration

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"scout.local/scout/internal/agent"
	"scout.local/scout/internal/control"
	"scout.local/scout/internal/discovery"
	"scout.local/scout/internal/store"
)

func TestServerAndAgentScansUseControlledListenersAndHonorExclusions(t *testing.T) {
	t.Run("server vantage", testServerVantageScan)
	t.Run("agent vantage", testAgentVantageScan)
}

func testServerVantageScan(t *testing.T) {
	ctx := context.Background()
	openListener, port := newProbeListener(t)
	application, err := control.NewApp(store.NewMemory(), nil, control.Config{SetupToken: "active-discovery-setup"})
	if err != nil {
		t.Fatal(err)
	}
	dialer := &recordingDialer{}
	application.Coordinator.Scanner = discovery.TCPScanner{Options: discovery.ProbeOptions{DialContext: dialer.DialContext}}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := server.Client()

	setup := integrationPostJSON(t, client, server.URL+"/api/v1/setup", map[string]any{"setupToken": "active-discovery-setup", "password": "ScoutAa1"}, nil, "")
	if setup.status != http.StatusCreated {
		t.Fatalf("setup: %d %s", setup.status, setup.body)
	}
	login := integrationPostJSON(t, client, server.URL+"/api/v1/sessions", map[string]any{"password": "ScoutAa1"}, nil, "")
	if login.status != http.StatusOK {
		t.Fatalf("login: %d %s", login.status, login.body)
	}
	var loginBody struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal([]byte(login.body), &loginBody); err != nil {
		t.Fatal(err)
	}
	if loginBody.CSRFToken == "" {
		t.Fatal("login omitted CSRF token")
	}

	siteResponse := integrationPostJSON(t, client, server.URL+"/api/v1/sites", map[string]any{"name": "active-server-lab"}, login.cookies, loginBody.CSRFToken)
	if siteResponse.status != http.StatusCreated {
		t.Fatalf("site: %d %s", siteResponse.status, siteResponse.body)
	}
	var site store.Site
	if err := json.Unmarshal([]byte(siteResponse.body), &site); err != nil {
		t.Fatal(err)
	}
	policyInput := activeScanPolicy(port, true, nil)
	scopeResponse := integrationPostJSON(t, client, server.URL+"/api/v1/scopes", map[string]any{
		"siteId":     site.ID,
		"ranges":     []string{"127.0.0.1", "192.0.2.2"},
		"exclusions": []string{"192.0.2.2"},
		"methods":    []string{"tcp"},
		"ports":      []int{port},
		"enabled":    true,
		"scanPolicy": policyInput,
	}, login.cookies, loginBody.CSRFToken)
	if scopeResponse.status != http.StatusCreated {
		t.Fatalf("scope: %d %s", scopeResponse.status, scopeResponse.body)
	}
	var scope struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
		Policy   struct {
			Revision int64 `json:"revision"`
		} `json:"scanPolicy"`
	}
	if err := json.Unmarshal([]byte(scopeResponse.body), &scope); err != nil {
		t.Fatal(err)
	}
	if scope.ID == "" || scope.Revision < 1 || scope.Policy.Revision < 1 {
		t.Fatalf("scope omitted revisions: %+v", scope)
	}

	headers := loginBody.CSRFToken
	runResponse := integrationPostJSONWithHeaders(t, client, server.URL+"/api/v1/scopes/"+scope.ID+"/scan-runs", map[string]any{
		"expectedRevision": scope.Policy.Revision,
		"scanner":          map[string]any{"kind": "server", "id": "control-server"},
	}, login.cookies, http.Header{"X-CSRF-Token": []string{headers}, "Idempotency-Key": []string{"server-loopback-run"}})
	if runResponse.status != http.StatusAccepted {
		t.Fatalf("run: %d %s", runResponse.status, runResponse.body)
	}
	var queued store.ScanRun
	if err := json.Unmarshal([]byte(runResponse.body), &queued); err != nil {
		t.Fatal(err)
	}
	if queued.ID == "" {
		t.Fatal("run omitted id")
	}

	completed, err := application.Coordinator.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed) != 1 || completed[0].State != store.ScanRunCompleted {
		t.Fatalf("server scan did not complete: %+v", completed)
	}
	assertOpenEvidence(t, application.Store, queued.ID, scope.ID, "server", "control-server", port)
	waitForProbeCount(t, openListener, 1)
	if got := dialer.excludedAttempts.Load(); got != 0 {
		t.Fatalf("excluded address received %d probe attempt(s)", got)
	}
	if got := openListener.bytes.Load(); got != 0 {
		t.Fatalf("server scanner sent %d application bytes", got)
	}
}

func testAgentVantageScan(t *testing.T) {
	ctx := context.Background()
	openListener, port := newProbeListener(t)
	dialer := &recordingDialer{}
	repository := store.NewMemory()
	application, err := control.NewApp(repository, nil, control.Config{SetupToken: "active-agent-setup"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	site, err := repository.CreateSite(ctx, store.Site{Name: "active-agent-lab"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{SiteID: site.ID, DisplayName: "loopback-agent", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	invitation := "active-agent-invitation"
	if err := repository.ReserveInvitation(ctx, store.BootstrapInvitation{TokenHash: store.HashToken(invitation), DeviceID: device.ID, ExpiresAt: repository.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	invitationPath := filepath.Join(t.TempDir(), "invitation")
	if err := os.WriteFile(invitationPath, []byte(invitation+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := agent.NewRuntime(agent.Config{ServerURL: server.URL, InvitationFile: invitationPath, DataDir: t.TempDir(), HTTPClient: server.Client(), Interval: time.Second, ScanDialContext: dialer.DialContext})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	device, err = repository.GetDevice(ctx, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if device.AgentID == "" {
		t.Fatal("agent enrollment did not attach an identity")
	}
	agentIdentity, err := repository.Agent(ctx, device.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agentIdentity.Capabilities.ScanProtocolVersions) == 0 {
		t.Fatal("agent heartbeat did not persist scan capabilities")
	}

	scope, err := repository.CreateScopeWithScanPolicy(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"127.0.0.1", "192.0.2.2"}, Exclusions: []string{"192.0.2.2"}, AllowedMethods: []string{"tcp"}, Ports: []int{port}, Enabled: true}, activeScanPolicyValue(port, false, []string{device.AgentID}))
	if err != nil {
		t.Fatal(err)
	}
	scanPolicy, err := repository.ScanPolicy(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := repository.Now()
	queued, err := repository.CreateScanRun(ctx, store.ScanRun{ScopeID: scope.ID, ScopeRevision: scanPolicy.Revision, ScannerKind: "agent", ScannerID: agentIdentity.ID, Trigger: "owner", ScheduledAt: now, AssignmentExpiresAt: now.Add(time.Minute), PolicySnapshot: store.ScanPolicySnapshot{Ranges: scope.Ranges, Exclusions: scope.Exclusions, EntryPoints: scanPolicy.EntryPoints, Limits: scanPolicy.Limits}, TargetsPlanned: 1, AttemptsPlanned: 1})
	if err != nil {
		t.Fatal(err)
	}
	leased, err := repository.LeaseScanRun(ctx, queued.ID, agentIdentity.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.StartScanRun(ctx, queued.ID, agentIdentity.ID, leased.LeaseEpoch); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	completed := waitForScanRun(t, repository, queued.ID)
	if completed.State != store.ScanRunCompleted {
		t.Fatalf("agent scan did not complete: %+v", completed)
	}
	assertOpenEvidence(t, repository, queued.ID, scope.ID, "agent", agentIdentity.ID, port)
	waitForProbeCount(t, openListener, 1)
	if got := dialer.excludedAttempts.Load(); got != 0 {
		t.Fatalf("excluded address received %d probe attempt(s)", got)
	}
	if got := openListener.bytes.Load(); got != 0 {
		t.Fatalf("agent scanner sent %d application bytes", got)
	}
	metrics, err := repository.QueryMetrics(ctx, store.MetricQuery{DeviceID: device.ID, MaxPoints: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) == 0 {
		t.Fatal("agent scan interrupted ordinary telemetry")
	}
}

func activeScanPolicy(port int, serverEnabled bool, agentIDs []string) map[string]any {
	return map[string]any{
		"serverEnabled":   serverEnabled,
		"agentIds":        agentIDs,
		"scheduleSeconds": 60,
		"entryPoints": []map[string]any{{
			"id": "ssh-loopback", "name": "SSH loopback", "transport": "tcp", "port": port, "accessMethod": "ssh", "enabled": true,
		}},
		"limits": map[string]any{
			"probesPerSecond": 100, "concurrency": 1, "targetBudget": 2, "attemptBudget": 2,
			"timeoutMilliseconds": 250, "runDeadlineSeconds": 30, "resultPageSize": 10,
		},
	}
}

func activeScanPolicyValue(port int, serverEnabled bool, agentIDs []string) store.ScanPolicy {
	return store.ScanPolicy{
		ServerEnabled: serverEnabled, AgentIDs: agentIDs, ScheduleSeconds: 60,
		EntryPoints: []store.ScanEntryPoint{{ID: "ssh-loopback", Name: "SSH loopback", Transport: store.ScanTransportTCP, Port: port, AccessMethod: store.ScanAccessSSH, Enabled: true}},
		Limits:      store.ScanLimits{ProbesPerSecond: 100, Concurrency: 1, TargetBudget: 2, AttemptBudget: 2, TimeoutMilliseconds: 250, RunDeadlineSeconds: 30, ResultPageSize: 10},
	}
}

type integrationResponse struct {
	status  int
	body    string
	cookies []*http.Cookie
}

func integrationPostJSON(t *testing.T, client *http.Client, target string, value any, cookies []*http.Cookie, csrf string) integrationResponse {
	t.Helper()
	return integrationPostJSONWithHeaders(t, client, target, value, cookies, http.Header{"X-CSRF-Token": []string{csrf}})
}

func integrationPostJSONWithHeaders(t *testing.T, client *http.Client, target string, value any, cookies []*http.Cookie, headers http.Header) integrationResponse {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if len(cookies) > 0 {
		request.Header.Set("Cookie", cookies[0].String())
		for _, cookie := range cookies[1:] {
			request.Header.Add("Cookie", cookie.String())
		}
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return integrationResponse{status: response.StatusCode, body: string(data), cookies: response.Cookies()}
}

type probeListener struct {
	net.Listener
	connections atomic.Int32
	bytes       atomic.Int64
}

func newProbeListener(t *testing.T) (*probeListener, int) {
	t.Helper()
	openRaw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := openRaw.Addr().(*net.TCPAddr).Port
	open := &probeListener{Listener: openRaw}
	go open.accept()
	t.Cleanup(func() { _ = open.Close() })
	return open, port
}

type recordingDialer struct {
	excludedAttempts atomic.Int32
}

func (d *recordingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err == nil && host == "192.0.2.2" {
		d.excludedAttempts.Add(1)
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func (listener *probeListener) accept() {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		listener.connections.Add(1)
		_ = connection.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		data, _ := io.ReadAll(io.LimitReader(connection, 1024))
		listener.bytes.Add(int64(len(data)))
		_ = connection.Close()
	}
}

func waitForProbeCount(t *testing.T, listener *probeListener, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if listener.connections.Load() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("listener accepted %d connections, want at least %d", listener.connections.Load(), want)
}

func waitForScanRun(t *testing.T, repository *store.Store, id string) store.ScanRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := repository.GetScanRun(context.Background(), id)
		if err == nil && (run.State == store.ScanRunCompleted || run.State == store.ScanRunPartial || run.State == store.ScanRunFailed || run.State == store.ScanRunCancelled) {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	run, err := repository.GetScanRun(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("scan run remained active: %+v", run)
	return store.ScanRun{}
}

func assertOpenEvidence(t *testing.T, repository *store.Store, runID, scopeID, scannerKind, scannerID string, port int) {
	t.Helper()
	observations, err := repository.ListEntryPointObservations(context.Background(), store.EntryPointObservationQuery{RunID: runID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations.Items) != 1 {
		t.Fatalf("got %d observations, want one non-excluded observation: %+v", len(observations.Items), observations.Items)
	}
	observation := observations.Items[0]
	if observation.ScopeID != scopeID || observation.ScannerKind != scannerKind || observation.ScannerID != scannerID || observation.Address != "127.0.0.1" || observation.Port != port || observation.Outcome != "open" || !observation.Actionable {
		t.Fatalf("unexpected open observation: %+v", observation)
	}
	candidates, err := repository.ListCandidates(context.Background(), scopeID, "needs_credentials")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Address != "127.0.0.1" {
		t.Fatalf("unexpected candidate projection: %+v", candidates)
	}
	requests, err := repository.ListAccessRequestsForCandidate(context.Background(), candidates[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].ReasonCode != "missing_credentials" {
		t.Fatalf("unexpected access requests: %+v", requests)
	}
}
