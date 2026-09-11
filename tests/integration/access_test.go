package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scout.local/scout/internal/control"
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

func newIntegrationApp() (*control.App, error) {
	return control.NewApp(store.NewMemory(), nil, control.Config{SetupToken: "integration-setup-token", AgentRequireMTLS: true})
}
