package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestAgentScanResultRouteUsesAuthenticatedAgentIdentity(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	now := time.Date(2026, 9, 12, 16, 0, 0, 0, time.UTC)
	repository.SetClock(func() time.Time { return now })
	site, err := repository.CreateSite(ctx, store.Site{Name: "agent-route"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := repository.CreateScope(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{SiteID: site.ID, DisplayName: "agent-route-device", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	const agentID = "agent-route-1"
	const agentToken = "agent-route-token"
	if err := repository.CreateAgentIdentity(ctx, store.AgentIdentity{ID: agentID, DeviceID: device.ID, AuthTokenHash: store.HashToken(agentToken), ExpiresAt: now.Add(time.Hour), Capabilities: store.ScanCapabilities{ScanProtocolVersions: []int{1}, ScanTransports: []string{store.ScanTransportTCP}}}); err != nil {
		t.Fatal(err)
	}
	scanPolicy, err := repository.ScanPolicy(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	scanPolicy.AgentIDs = []string{agentID}
	scanPolicy, err = repository.UpdateScanPolicy(ctx, scope.ID, scanPolicy.Revision, scanPolicy)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repository.CreateScanRun(ctx, store.ScanRun{ID: "agent-route-run", ScopeID: scope.ID, ScopeRevision: scanPolicy.Revision, ScannerKind: "agent", ScannerID: agentID, ScheduledAt: now.Add(-time.Minute), AssignmentExpiresAt: now.Add(time.Minute), PolicySnapshot: store.ScanPolicySnapshot{Ranges: scope.Ranges, Exclusions: scope.Exclusions, EntryPoints: scanPolicy.EntryPoints, Limits: scanPolicy.Limits}, TargetsPlanned: 1, AttemptsPlanned: 1})
	if err != nil {
		t.Fatal(err)
	}
	leased, err := repository.LeaseScanRun(ctx, run.ID, agentID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.StartScanRun(ctx, run.ID, agentID, leased.LeaseEpoch); err != nil {
		t.Fatal(err)
	}

	page := map[string]any{
		"protocolVersion": 1,
		"runId":           run.ID,
		"leaseEpoch":      leased.LeaseEpoch,
		"scopeRevision":   scanPolicy.Revision,
		"pageOrdinal":     0,
		"observedFrom":    now.Add(-time.Second),
		"observedTo":      now,
		"final":           true,
		"results": []map[string]any{{
			"address": "192.0.2.10", "entryPointId": scanPolicy.EntryPoints[0].ID, "transport": "tcp", "port": 22, "outcome": "open", "observedAt": now,
		}},
		"summary": map[string]any{"targetsPlanned": 1, "attemptsPlanned": 1, "attemptsCompleted": 1, "partialReason": nil},
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/v1/scan-results", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+agentToken)
	response := httptest.NewRecorder()
	application, err := NewApp(repository, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		current, _ := repository.GetScanRun(ctx, run.ID)
		t.Fatalf("scan result response: %d %s (run=%+v)", response.Code, response.Body, current)
	}
	stored, err := repository.GetScanRun(ctx, run.ID)
	if err != nil || stored.State != store.ScanRunCompleted {
		t.Fatalf("route did not finalize run: %+v err=%v", stored, err)
	}
}

func TestAgentDesiredStateOnlyIncludesAssignedCompatibleScan(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	now := time.Date(2026, 9, 12, 17, 0, 0, 0, time.UTC)
	repository.SetClock(func() time.Time { return now })
	site, err := repository.CreateSite(ctx, store.Site{Name: "desired-state-scan"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := repository.CreateScope(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, Exclusions: []string{"192.0.2.9"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{SiteID: site.ID, DisplayName: "desired-state-agent", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	const agentID = "desired-state-agent-1"
	const agentToken = "desired-state-token"
	if err := repository.CreateAgentIdentity(ctx, store.AgentIdentity{ID: agentID, DeviceID: device.ID, AuthTokenHash: store.HashToken(agentToken), ExpiresAt: now.Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.RecordHeartbeat(ctx, agentID, store.Heartbeat{BootID: "boot-1", Capabilities: store.ScanCapabilities{ScanProtocolVersions: []int{1}, ScanTransports: []string{store.ScanTransportTCP}}}); err != nil {
		t.Fatal(err)
	}
	scanPolicy, err := repository.ScanPolicy(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	scanPolicy.AgentIDs = []string{agentID}
	scanPolicy, err = repository.UpdateScanPolicy(ctx, scope.ID, scanPolicy.Revision, scanPolicy)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repository.CreateScanRun(ctx, store.ScanRun{ID: "desired-state-run", ScopeID: scope.ID, ScopeRevision: scanPolicy.Revision, ScannerKind: "agent", ScannerID: agentID, ScheduledAt: now, AssignmentExpiresAt: now.Add(5 * time.Minute), PolicySnapshot: store.ScanPolicySnapshot{Ranges: scope.Ranges, Exclusions: scope.Exclusions, EntryPoints: scanPolicy.EntryPoints, Limits: scanPolicy.Limits}, TargetsPlanned: 1, AttemptsPlanned: 1})
	if err != nil {
		t.Fatal(err)
	}
	leased, err := repository.LeaseScanRun(ctx, run.ID, agentID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.StartScanRun(ctx, run.ID, agentID, leased.LeaseEpoch); err != nil {
		t.Fatal(err)
	}
	application, err := NewApp(repository, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent/v1/desired-state", nil)
	request.Header.Set("Authorization", "Bearer "+agentToken)
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("desired state response: %d %s", response.Code, response.Body)
	}
	var desired struct {
		DiscoveryPolicy []store.Scope `json:"discoveryPolicy"`
		ScanAssignment  *struct {
			RunID         string                 `json:"runId"`
			ScopeID       string                 `json:"scopeId"`
			ScopeRevision int64                  `json:"scopeRevision"`
			LeaseEpoch    int64                  `json:"leaseEpoch"`
			Ranges        []string               `json:"ranges"`
			Exclusions    []string               `json:"exclusions"`
			EntryPoints   []store.ScanEntryPoint `json:"entryPoints"`
			Limits        store.ScanLimits       `json:"limits"`
		} `json:"scanAssignment"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &desired); err != nil {
		t.Fatal(err)
	}
	if len(desired.DiscoveryPolicy) != 0 || desired.ScanAssignment == nil || desired.ScanAssignment.RunID != run.ID || desired.ScanAssignment.ScopeID != scope.ID || desired.ScanAssignment.LeaseEpoch != leased.LeaseEpoch || len(desired.ScanAssignment.Ranges) != 1 || desired.ScanAssignment.EntryPoints[0].AccessMethod != store.ScanAccessSSH {
		t.Fatalf("unexpected compatible desired state: %+v", desired)
	}
	if strings.Contains(response.Body.String(), "credential") || strings.Contains(response.Body.String(), "trust") {
		t.Fatalf("scan assignment leaked access material: %s", response.Body)
	}

	if _, _, err := repository.RecordHeartbeat(ctx, agentID, store.Heartbeat{BootID: "boot-2"}); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/agent/v1/desired-state", nil)
	request.Header.Set("Authorization", "Bearer "+agentToken)
	response = httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"scanAssignment":null`) {
		t.Fatalf("unsupported agent received scan assignment: %d %s", response.Code, response.Body)
	}
}

func TestAgentScanResultsRejectAnAuthenticatedAgentWithoutRunAuthority(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	now := time.Date(2026, 9, 12, 17, 30, 0, 0, time.UTC)
	repository.SetClock(func() time.Time { return now })
	site, err := repository.CreateSite(ctx, store.Site{Name: "scan-route-authority"})
	if err != nil {
		t.Fatal(err)
	}
	firstDevice, err := repository.CreateDevice(ctx, store.Device{SiteID: site.ID, DisplayName: "assigned-agent", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	secondDevice, err := repository.CreateDevice(ctx, store.Device{SiteID: site.ID, DisplayName: "other-agent", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := store.ScanCapabilities{ScanProtocolVersions: []int{1}, ScanTransports: []string{store.ScanTransportTCP}}
	if err := repository.CreateAgentIdentity(ctx, store.AgentIdentity{ID: "assigned-agent-1", DeviceID: firstDevice.ID, AuthTokenHash: store.HashToken("assigned-token"), ExpiresAt: now.Add(time.Hour), Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateAgentIdentity(ctx, store.AgentIdentity{ID: "other-agent-1", DeviceID: secondDevice.ID, AuthTokenHash: store.HashToken("other-token"), ExpiresAt: now.Add(time.Hour), Capabilities: capabilities}); err != nil {
		t.Fatal(err)
	}
	scope, err := repository.CreateScope(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"192.0.2.10"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	scanPolicy, err := repository.ScanPolicy(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	scanPolicy.AgentIDs = []string{"assigned-agent-1"}
	scanPolicy, err = repository.UpdateScanPolicy(ctx, scope.ID, scanPolicy.Revision, scanPolicy)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repository.CreateScanRun(ctx, store.ScanRun{ID: "authority-run", ScopeID: scope.ID, ScopeRevision: scanPolicy.Revision, ScannerKind: "agent", ScannerID: "assigned-agent-1", ScheduledAt: now, AssignmentExpiresAt: now.Add(time.Minute), PolicySnapshot: store.ScanPolicySnapshot{Ranges: scope.Ranges, Exclusions: scope.Exclusions, EntryPoints: scanPolicy.EntryPoints, Limits: scanPolicy.Limits}, TargetsPlanned: 1, AttemptsPlanned: 1})
	if err != nil {
		t.Fatal(err)
	}
	leased, err := repository.LeaseScanRun(ctx, run.ID, "assigned-agent-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.StartScanRun(ctx, run.ID, "assigned-agent-1", leased.LeaseEpoch); err != nil {
		t.Fatal(err)
	}
	page := map[string]any{
		"protocolVersion": 1,
		"runId":           run.ID,
		"leaseEpoch":      leased.LeaseEpoch,
		"scopeRevision":   scanPolicy.Revision,
		"pageOrdinal":     0,
		"observedFrom":    now,
		"observedTo":      now,
		"final":           true,
		"results":         []map[string]any{},
		"summary":         map[string]any{"targetsPlanned": 1, "attemptsPlanned": 1, "attemptsCompleted": 0, "partialReason": "cancelled"},
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewApp(repository, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/v1/scan-results", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer other-token")
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unauthorized agent result response: %d %s", response.Code, response.Body)
	}
}

func TestAgentPauseAcknowledgementUsesAuthenticatedAgentIdentity(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	now := time.Date(2026, 9, 12, 18, 15, 0, 0, time.UTC)
	repository.SetClock(func() time.Time { return now })
	site, err := repository.CreateSite(ctx, store.Site{Name: "agent-pause-ack"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{SiteID: site.ID, DisplayName: "pause-agent", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	const agentID = "pause-agent-1"
	const agentToken = "pause-agent-token"
	if err := repository.CreateAgentIdentity(ctx, store.AgentIdentity{ID: agentID, DeviceID: device.ID, AuthTokenHash: store.HashToken(agentToken), ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SetWorkspace(ctx, func(state *store.WorkspaceState) error {
		state.DiscoveryPaused = true
		state.PauseRequested = true
		state.PausePending = true
		state.ExecutionHolders = []string{agentID}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	application, err := NewApp(repository, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/v1/pause-ack", strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer "+agentToken)
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("agent pause acknowledgement response: %d %s", response.Code, response.Body)
	}
	state, err := repository.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.PausePending || len(state.ExecutionHolders) != 0 {
		t.Fatalf("agent pause acknowledgement did not clear its holder: %+v", state)
	}
}
