package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/control"
	"scout.local/scout/internal/discovery"
	"scout.local/scout/internal/enrollment"
	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

func TestCredentialBrokerIsTargetBoundAndWriteOnly(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	keyRing, err := secrets.NewKeyRing([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := keyRing.EncryptSecret("credential-1", "ssh", []byte("private-secret"))
	if err != nil {
		t.Fatal(err)
	}
	credential, err := repository.PutCredential(ctx, store.CredentialRef{ID: "credential-1", Kind: "ssh", AllowedUse: []string{"enrollment"}, Targets: []string{"192.0.2.10:22"}, Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce, WrappedDataKey: envelope.WrappedDataKey, KeyVersion: envelope.KeyVersion})
	if err != nil {
		t.Fatal(err)
	}
	broker := secrets.Broker{Store: repository, KeyRing: keyRing}
	secret, err := broker.RedeemForTarget(ctx, credential.ID, "enrollment", "192.0.2.10:22")
	if err != nil || string(secret) != "private-secret" {
		t.Fatalf("scoped redemption failed: %v %q", err, secret)
	}
	if _, err := broker.RedeemForTarget(ctx, credential.ID, "enrollment", "192.0.2.11:22"); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("wrong target was accepted: %v", err)
	}
	if _, err := broker.RedeemForTarget(ctx, credential.ID, "diagnostics", "192.0.2.10:22"); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("wrong operation was accepted: %v", err)
	}
	listed, err := repository.ListCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-secret") || strings.Contains(string(encoded), "ciphertext") {
		t.Fatalf("credential listing leaked secret material: %s", encoded)
	}
	if err := repository.RevokeCredential(ctx, credential.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.RedeemForTarget(ctx, credential.ID, "enrollment", "192.0.2.10:22"); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("revoked credential remained usable: %v", err)
	}
}

func TestTrustAndMissingAccessRequestsAreNormalizedAndDeduplicated(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	site, _ := repository.CreateSite(ctx, store.Site{Name: "access-lab"})
	device, _ := repository.CreateDevice(ctx, store.Device{DisplayName: "candidate", SiteID: site.ID})
	scope, err := repository.CreateScope(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, Ports: []int{22}, AllowedMethods: []string{"tcp"}, CredentialRef: "missing", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	access := enrollment.Access{Policy: &policy.Engine{Store: repository}, Store: repository}
	first, err := access.Evaluate(ctx, scope.ID, "192.0.2.10", "tcp", 22, device.ID)
	if err != nil || first.Eligible || first.Reason != enrollment.ReasonMissingCredentials {
		t.Fatalf("missing credential was not classified: %+v %v", first, err)
	}
	second, err := access.Evaluate(ctx, scope.ID, "192.0.2.10", "tcp", 22, device.ID)
	if err != nil || first.Request.ID != second.Request.ID {
		t.Fatalf("missing request was not deduplicated: first=%+v second=%+v err=%v", first.Request, second.Request, err)
	}
	trust, err := repository.PutTrust(ctx, store.TrustRecord{ScopeID: scope.ID, Host: "192.0.2.10", Endpoint: "192.0.2.10:22", Fingerprint: "SHA256:known"})
	if err != nil {
		t.Fatal(err)
	}
	_ = trust
	decision, err := enrollment.VerifyTrust(ctx, repository, scope.ID, "192.0.2.10", 22, "sha256:known")
	if err != nil || !decision.Allowed {
		t.Fatalf("normalized trusted endpoint was rejected: %+v %v", decision, err)
	}
	changed, err := enrollment.VerifyTrust(ctx, repository, scope.ID, "192.0.2.10", 22, "sha256:changed")
	if err != nil || changed.Allowed || changed.Reason != "host_trust_required" {
		t.Fatalf("changed host key was accepted: %+v %v", changed, err)
	}
}

func TestOrdinaryAgentCannotRedeemOwnerCredentials(t *testing.T) {
	app, err := newIntegrationApp()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/credentials", nil)
	request.Header.Set("Authorization", "Bearer ordinary-agent")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("agent bearer reached owner credential route: %d %s", response.Code, response.Body)
	}
}

func TestScanCandidateAccessReevaluationClassifiesFailuresAndQueuesOnce(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	now := time.Date(2026, 9, 12, 20, 0, 0, 0, time.UTC)
	repository.SetClock(func() time.Time { return now })
	site, err := repository.CreateSite(ctx, store.Site{Name: "reevaluation-lab"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := repository.CreateScope(ctx, store.Scope{
		SiteID:         site.ID,
		Ranges:         []string{"192.0.2.0/24"},
		AllowedMethods: []string{"tcp"},
		Ports:          []int{22},
		Enabled:        true,
	})
	if err != nil {
		t.Fatal(err)
	}

	service := &discovery.Service{Store: repository, Policy: &policy.Engine{Store: repository}, Now: repository.Now}
	candidates, err := service.ReconcileScanObservations(ctx, []store.EntryPointObservation{{
		ID:      "reevaluation-open",
		ScopeID: scope.ID, ScopeRevision: scope.Revision, ScannerKind: "server", ScannerID: "control-server",
		Address: "192.0.2.10", Transport: store.ScanTransportTCP, Port: 22, EntryPointID: "ssh-default",
		Outcome: "open", ObservedAt: now,
	}})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("seed candidate: %+v err=%v", candidates, err)
	}
	candidate := candidates[0]

	access := &enrollment.Access{Policy: &policy.Engine{Store: repository}, Store: repository, Now: repository.Now}
	result, err := access.ReevaluateCandidate(ctx, candidate.ID)
	if err != nil || result.Eligible || result.Reason != enrollment.ReasonMissingCredentials {
		t.Fatalf("missing credentials: %+v err=%v", result, err)
	}

	credential, err := repository.PutCredential(ctx, store.CredentialRef{
		ID: "reevaluation-credential", Kind: "ssh", AllowedUse: []string{"enrollment"}, Targets: []string{"192.0.2.10:22"},
		Ciphertext: []byte{1}, Nonce: []byte{2}, WrappedDataKey: []byte{3}, KeyVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	currentScope, err := repository.GetScope(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentScope.CredentialRef = credential.ID
	if _, err := repository.UpdateScope(ctx, scope.ID, currentScope.Revision, currentScope); err != nil {
		t.Fatal(err)
	}
	trust, err := repository.PutTrust(ctx, store.TrustRecord{ScopeID: scope.ID, Host: "192.0.2.10", Endpoint: "192.0.2.10:22", Fingerprint: "SHA256:known"})
	if err != nil {
		t.Fatal(err)
	}
	currentScope, err = repository.GetScope(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentScope.TrustRef = trust.ID
	if _, err := repository.UpdateScope(ctx, scope.ID, currentScope.Revision, currentScope); err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		name   string
		check  enrollment.AccessCheckResult
		state  string
		reason enrollment.Reason
	}{
		{name: "invalid authentication", check: enrollment.AccessCheckResult{HostTrusted: true, Privileged: true, ServerReachable: true}, state: "invalid_credentials", reason: enrollment.ReasonInvalidCredentials},
		{name: "insufficient privilege", check: enrollment.AccessCheckResult{Authenticated: true, HostTrusted: true, ServerReachable: true}, state: "needs_privilege", reason: enrollment.ReasonInsufficientPrivilege},
		{name: "host key mismatch", check: enrollment.AccessCheckResult{Authenticated: true, Privileged: true, ServerReachable: true}, state: "needs_host_trust", reason: enrollment.ReasonHostTrust},
		{name: "server connectivity", check: enrollment.AccessCheckResult{Authenticated: true, Privileged: true, HostTrusted: true}, state: "needs_server_connectivity", reason: enrollment.ReasonServerConnectivity},
	}
	for _, testCase := range checks {
		t.Run(testCase.name, func(t *testing.T) {
			access.Verifier = enrollment.AccessVerifierFunc(func(context.Context, enrollment.AccessCheckRequest) (enrollment.AccessCheckResult, error) {
				return testCase.check, nil
			})
			result, err := access.ReevaluateCandidate(ctx, candidate.ID)
			if err != nil || result.Eligible || result.Reason != testCase.reason {
				t.Fatalf("access result: %+v err=%v", result, err)
			}
			updated, err := repository.GetCandidate(ctx, candidate.ID)
			if err != nil || updated.State != testCase.state {
				t.Fatalf("candidate state: %+v err=%v", updated, err)
			}
		})
	}

	access.Verifier = enrollment.AccessVerifierFunc(func(context.Context, enrollment.AccessCheckRequest) (enrollment.AccessCheckResult, error) {
		return enrollment.AccessCheckResult{Authenticated: true, Privileged: true, HostTrusted: true, ServerReachable: true}, nil
	})
	result, err = access.ReevaluateCandidate(ctx, candidate.ID)
	if err != nil || !result.Eligible || result.Job.ID == "" {
		t.Fatalf("eligible candidate was not queued: %+v err=%v", result, err)
	}
	repeated, err := access.ReevaluateCandidate(ctx, candidate.ID)
	if err != nil || !repeated.Eligible || repeated.Job.ID != result.Job.ID {
		t.Fatalf("re-evaluation created a second job: first=%+v repeated=%+v err=%v", result, repeated, err)
	}
	jobs, err := repository.ListJobs(ctx, "enrollment", "", "")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("expected exactly one enrollment job: %+v err=%v", jobs, err)
	}
	devices, err := repository.ListDevices(ctx, store.DeviceFilter{SiteID: site.ID, Query: "192.0.2.10"})
	if err != nil || len(devices) != 1 {
		t.Fatalf("expected exactly one adopted target device: %+v err=%v", devices, err)
	}
	if devices[0].Addresses[0] != "192.0.2.10" {
		t.Fatalf("target address was not retained: %+v", devices[0])
	}
}

func TestCredentialAndTrustMutationsReevaluateScanCandidate(t *testing.T) {
	ctx := context.Background()
	application, err := control.NewApp(store.NewMemory(), nil, control.Config{SetupToken: "mutation-setup"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := server.Client()
	setup := integrationPostJSON(t, client, server.URL+"/api/v1/setup", map[string]any{"setupToken": "mutation-setup", "password": "ScoutAa1"}, nil, "")
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
	siteResponse := integrationPostJSON(t, client, server.URL+"/api/v1/sites", map[string]any{"name": "mutation-lab"}, login.cookies, loginBody.CSRFToken)
	if siteResponse.status != http.StatusCreated {
		t.Fatalf("site: %d %s", siteResponse.status, siteResponse.body)
	}
	var site store.Site
	if err := json.Unmarshal([]byte(siteResponse.body), &site); err != nil {
		t.Fatal(err)
	}
	scope, err := application.Store.CreateScope(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := application.Discovery.ReconcileScanObservations(ctx, []store.EntryPointObservation{{
		ID: "mutation-open", ScopeID: scope.ID, ScopeRevision: scope.Revision, ScannerKind: "server", ScannerID: "control-server",
		Address: "192.0.2.10", Transport: store.ScanTransportTCP, Port: 22, EntryPointID: "ssh-default", Outcome: "open", ObservedAt: application.Store.Now(),
	}})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("seed candidate: %+v err=%v", candidates, err)
	}
	candidateID := candidates[0].ID
	devices, err := application.Store.ListDevices(ctx, store.DeviceFilter{SiteID: site.ID})
	if err != nil || len(devices) != 0 {
		t.Fatalf("candidate was adopted before access was supplied: %+v err=%v", devices, err)
	}

	credentialResponse := integrationPostJSON(t, client, server.URL+"/api/v1/credentials", map[string]any{
		"kind": "ssh", "secret": "private-key-material", "allowedUse": []string{"enrollment"}, "targets": []string{"192.0.2.10:22"},
		"scopeId": scope.ID, "expectedScopeRevision": scope.Revision,
	}, login.cookies, loginBody.CSRFToken)
	if credentialResponse.status != http.StatusCreated {
		t.Fatalf("credential: %d %s", credentialResponse.status, credentialResponse.body)
	}
	var credentialBody map[string]any
	if err := json.Unmarshal([]byte(credentialResponse.body), &credentialBody); err != nil {
		t.Fatal(err)
	}
	if credentialBody["affectedCandidateCount"] != float64(1) || strings.Contains(credentialResponse.body, "private-key-material") {
		t.Fatalf("credential mutation response: %s", credentialResponse.body)
	}
	candidate, err := application.Store.GetCandidate(ctx, candidateID)
	if err != nil || candidate.State != "needs_host_trust" {
		t.Fatalf("credential did not advance candidate to trust: %+v err=%v", candidate, err)
	}

	trustResponse := integrationPostJSON(t, client, server.URL+"/api/v1/trust", map[string]any{
		"scopeId": scope.ID, "host": "192.0.2.10", "endpoint": "192.0.2.10:22", "fingerprint": "SHA256:known",
	}, login.cookies, loginBody.CSRFToken)
	if trustResponse.status != http.StatusCreated {
		t.Fatalf("trust: %d %s", trustResponse.status, trustResponse.body)
	}
	if !strings.Contains(trustResponse.body, `"affectedCandidateCount":1`) {
		t.Fatalf("trust mutation did not report the affected candidate: %s", trustResponse.body)
	}
	candidate, err = application.Store.GetCandidate(ctx, candidateID)
	if err != nil || candidate.State != "queued" || candidate.DeviceID == "" {
		t.Fatalf("trusted candidate was not queued: %+v err=%v", candidate, err)
	}
	j, err := application.Store.ListJobs(ctx, "enrollment", "", "")
	if err != nil || len(j) != 1 {
		t.Fatalf("expected one enrollment job: %+v err=%v", j, err)
	}
	if j[0].DeviceID != candidate.DeviceID || strings.Contains(trustResponse.body, "private-key-material") {
		t.Fatalf("unsafe enrollment handoff: candidate=%+v jobs=%+v trust=%s", candidate, j, trustResponse.body)
	}
}

func newIntegrationApp() (*control.App, error) {
	return control.NewApp(store.NewMemory(), nil, control.Config{SetupToken: "integration-setup-token", AgentRequireMTLS: true})
}
