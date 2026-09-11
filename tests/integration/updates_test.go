package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scout.local/scout/internal/store"
	"scout.local/scout/internal/updates"
)

func TestSignedReleaseImportIsImmutableAndRevocable(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyID := updates.KeyID(publicKey)
	trust := updates.NewTrustStore()
	if err := trust.Add(keyID, publicKey); err != nil {
		t.Fatal(err)
	}
	artifact := []byte("linux-agent-release")
	manifest, err := updates.SignManifest(updates.Manifest{SchemaVersion: updates.ManifestSchemaVersion, Version: "0.2.0", Generation: 2, Platform: "linux", Architecture: "amd64", Digest: updates.ArtifactDigest(artifact), Bytes: int64(len(artifact)), ProtocolMin: 1, ProtocolMax: 1}, privateKey, keyID)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := updates.EncodeBundle(manifest, artifact)
	if err != nil {
		t.Fatal(err)
	}
	service := updates.NewReleaseService(repository, trust, filepath.Join(t.TempDir(), "releases"))
	release, err := service.ImportBundle(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.ImportBundle(ctx, bundle)
	if err != nil || duplicate.ID != release.ID {
		t.Fatalf("idempotent import failed: %v %+v", err, duplicate)
	}
	data, err := service.Artifact(ctx, release.ID, release.Digest)
	if err != nil || string(data) != string(artifact) {
		t.Fatalf("artifact read failed: %v %q", err, data)
	}
	if err := os.WriteFile(release.ImmutablePath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(func() error { _, readErr := service.Artifact(ctx, release.ID, release.Digest); return readErr }(), updates.ErrTampered) {
		t.Fatal("tampered immutable artifact was served")
	}
	if err := service.Revoke(ctx, release.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Artifact(ctx, release.ID, release.Digest); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("revoked release remained downloadable: %v", err)
	}
}

func TestRolloutPlannerHonorsCanariesWindowsAndExclusions(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	site, _ := repository.CreateSite(ctx, store.Site{Name: "updates-lab"})
	first, _ := repository.CreateDevice(ctx, store.Device{DisplayName: "first", SiteID: site.ID, Platform: "linux", Architecture: "amd64"})
	second, _ := repository.CreateDevice(ctx, store.Device{DisplayName: "second", SiteID: site.ID, Platform: "linux", Architecture: "amd64", Excluded: true})
	third, _ := repository.CreateDevice(ctx, store.Device{DisplayName: "third", SiteID: site.ID, Platform: "linux", Architecture: "arm64"})
	release := store.Release{ID: "release-1", ManifestHash: "manifest", Version: "0.2.0", Generation: 2, Platform: "linux", Architecture: "amd64", Digest: "sha256:release", Bytes: 10}
	if _, err := repository.PutRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	rollout := store.Rollout{ID: "rollout-1", ReleaseID: release.ID, Mode: "automatic", Targets: []string{first.ID, second.ID, third.ID}, Concurrency: 2, Canaries: 1, FailureThreshold: 2, WindowStart: &start, WindowEnd: &end}
	planned, err := updates.PlanAssignments(rollout, release, []store.Device{first, second, third}, start.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(planned) != 1 || planned[0].DeviceID != first.ID || planned[0].State != "canary" {
		t.Fatalf("unexpected rollout plan: %+v", planned)
	}
	outside, err := updates.PlanAssignments(rollout, release, []store.Device{first}, end.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(outside) != 0 {
		t.Fatalf("automatic rollout ignored maintenance window: %+v", outside)
	}
}
