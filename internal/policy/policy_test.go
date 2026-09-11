package policy

import (
	"context"
	"errors"
	"testing"

	"scout.local/scout/internal/store"
)

func TestScopeEvaluationIsLiteralScopedAndExclusionsWin(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	site, _ := s.CreateSite(ctx, store.Site{Name: "lab"})
	credential := store.CredentialRef{ID: store.NewID(), Kind: "ssh", Ciphertext: []byte{1}, Nonce: []byte{2}, WrappedDataKey: []byte{3}, AllowedUse: []string{"enrollment"}, Targets: []string{"10.0.0.0/24"}, KeyVersion: 1}
	s.PutCredential(ctx, credential)
	scope, err := s.CreateScope(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"10.0.0.0/24"}, Exclusions: []string{"10.0.0.9"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, CredentialRef: credential.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	engine := Engine{Store: s}
	decision, err := engine.Evaluate(ctx, scope.ID, "10.0.0.8", "tcp", 22)
	if err != nil || !decision.Allowed {
		t.Fatalf("eligible target: %v %+v", err, decision)
	}
	decision, err = engine.Evaluate(ctx, scope.ID, "10.0.0.9", "tcp", 22)
	if err != nil || decision.Allowed || decision.Reason != "excluded" {
		t.Fatalf("excluded target: %v %+v", err, decision)
	}
	decision, err = engine.Evaluate(ctx, scope.ID, "10.0.1.8", "tcp", 22)
	if err != nil || decision.Allowed || decision.Reason != "outside_scope" {
		t.Fatalf("out-of-scope target: %v %+v", err, decision)
	}
	_ = errors.Is
}
