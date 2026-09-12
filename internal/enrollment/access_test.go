package enrollment

import (
	"context"
	"testing"

	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

func TestReevaluateScopeQueuesCandidateAfterPriorInvalidCredentials(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	site, err := repository.CreateSite(ctx, store.Site{Name: "access-retry-site"})
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
	keyRing, err := secrets.NewKeyRing(testKeyMaterial(1))
	if err != nil {
		t.Fatal(err)
	}
	trust, err := repository.PutTrust(ctx, store.TrustRecord{ScopeID: scope.ID, Host: "192.0.2.10", Endpoint: "192.0.2.10:22", Fingerprint: "SHA256:fixture"})
	if err != nil {
		t.Fatal(err)
	}
	oldEnvelope, err := keyRing.EncryptSecret("credential-old", "ssh", []byte("old-key"))
	if err != nil {
		t.Fatal(err)
	}
	oldCredential, err := repository.PutCredential(ctx, store.CredentialRef{
		ID: "credential-old", Kind: "ssh", AllowedUse: []string{"enrollment"}, Targets: []string{"192.0.2.10:22"},
		Ciphertext: oldEnvelope.Ciphertext, Nonce: oldEnvelope.Nonce, WrappedDataKey: oldEnvelope.WrappedDataKey, KeyVersion: oldEnvelope.KeyVersion,
		Metadata: map[string]string{"username": "fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	scope.CredentialRef = oldCredential.ID
	scope.TrustRef = trust.ID
	scope, err = repository.UpdateScope(ctx, scope.ID, scope.Revision, scope)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := repository.UpsertCandidate(ctx, store.Candidate{
		ScopeID: scope.ID, Address: "192.0.2.10", State: "discovered", CoverageState: "current",
		EntryPointIDs: []string{"ssh-default"}, PreferredAccessMethod: store.ScanAccessSSH,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpsertAccessRequest(ctx, store.AccessRequest{
		DeviceID: candidate.ID, CandidateID: candidate.ID, ScopeID: scope.ID, AccessMethod: store.ScanAccessSSH,
		Endpoint: "192.0.2.10:22", ReasonCode: "missing_credentials", State: "open",
		SafeDetails: map[string]string{"target": "192.0.2.10:22", "method": store.ScanAccessSSH},
	}); err != nil {
		t.Fatal(err)
	}
	access := &Access{Policy: &policy.Engine{Store: repository}, Store: repository, Now: repository.Now}
	first, err := access.ReevaluateCandidate(ctx, candidate.ID)
	if err != nil || !first.Eligible {
		t.Fatalf("initial enrollment queue: eligible=%v err=%v", first.Eligible, err)
	}
	claimed, err := repository.ClaimJob(ctx, "fixture-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ReportJob(ctx, claimed.ID, "fixture-worker", claimed.Epoch, "failed", map[string]string{"code": "invalid_credentials"}); err != nil {
		t.Fatal(err)
	}
	if err := access.RecordEnrollmentOutcome(ctx, candidate.ID, "invalid_credentials"); err != nil {
		t.Fatal(err)
	}

	newEnvelope, err := keyRing.EncryptSecret("credential-new", "ssh", []byte("new-key"))
	if err != nil {
		t.Fatal(err)
	}
	newCredential, err := repository.PutCredential(ctx, store.CredentialRef{
		ID: "credential-new", Kind: "ssh", AllowedUse: []string{"enrollment"}, Targets: []string{"192.0.2.10:22"},
		Ciphertext: newEnvelope.Ciphertext, Nonce: newEnvelope.Nonce, WrappedDataKey: newEnvelope.WrappedDataKey, KeyVersion: newEnvelope.KeyVersion,
		Metadata: map[string]string{"username": "fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	scope.CredentialRef = newCredential.ID
	scope, err = repository.UpdateScope(ctx, scope.ID, scope.Revision, scope)
	if err != nil {
		t.Fatal(err)
	}

	affected, err := access.ReevaluateScope(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	if affected != 1 {
		t.Fatalf("affected candidates = %d, want 1", affected)
	}
	updated, err := repository.GetCandidate(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != "queued" {
		t.Fatalf("candidate state = %q, want queued", updated.State)
	}
	jobs, err := repository.ListJobs(ctx, "enrollment", "", updated.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[1].State != "queued" {
		t.Fatalf("jobs after credential replacement = %+v, want failed and queued", jobs)
	}
}

func testKeyMaterial(seed byte) []byte {
	key := make([]byte, 32)
	for index := range key {
		key[index] = seed + byte(index)
	}
	return key
}
