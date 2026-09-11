package updates

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"scout.local/scout/internal/store"
)

func signedFixture(t *testing.T, artifact []byte, generation int64) (Manifest, *TrustStore) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyID := KeyID(publicKey)
	manifest, err := SignManifest(Manifest{SchemaVersion: ManifestSchemaVersion, Version: "1.2.3", Generation: generation, Platform: "linux", Architecture: "amd64", Digest: ArtifactDigest(artifact), Bytes: int64(len(artifact)), ProtocolMin: 1, ProtocolMax: 1}, privateKey, keyID)
	if err != nil {
		t.Fatal(err)
	}
	trust := NewTrustStore()
	if err := trust.Add(keyID, publicKey); err != nil {
		t.Fatal(err)
	}
	return manifest, trust
}

func TestManifestVerificationRejectsTamperAndRevokedTrust(t *testing.T) {
	artifact := []byte("signed-agent")
	manifest, trust := signedFixture(t, artifact, 1)
	if err := manifest.Verify(artifact, trust); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(manifest.Verify([]byte("altered-agent"), trust), ErrTampered) {
		t.Fatal("altered artifact was accepted")
	}
	if err := trust.Revoke(manifest.KeyID); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(manifest.Verify(artifact, trust), store.ErrUnauthorized) {
		t.Fatal("revoked signing key was accepted")
	}
}

func TestManifestCompatibilityAndDowngradeChecks(t *testing.T) {
	manifest, _ := signedFixture(t, []byte("agent"), 4)
	if err := Compatible(manifest, "linux", "amd64", 1); err != nil {
		t.Fatal(err)
	}
	if err := Compatible(manifest, "linux", "arm64", 1); err == nil {
		t.Fatal("wrong architecture was accepted")
	}
	if !IsDowngrade(3, 4) || IsDowngrade(4, 4) {
		t.Fatal("generation downgrade check is incorrect")
	}
}

func TestInstallerRecoversInterruptedSwitchAndBoundsRollback(t *testing.T) {
	installer, err := NewInstaller(filepath.Join(t.TempDir(), "updater"))
	if err != nil {
		t.Fatal(err)
	}
	firstArtifact := []byte("agent-v1")
	first, _ := signedFixture(t, firstArtifact, 1)
	if err := installer.Stage(context.Background(), first, firstArtifact); err != nil {
		t.Fatal(err)
	}
	if err := installer.MarkReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	secondArtifact := []byte("agent-v2")
	second, _ := signedFixture(t, secondArtifact, 2)
	if err := installer.Stage(context.Background(), second, secondArtifact); err != nil {
		t.Fatal(err)
	}
	state, err := installer.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveGeneration != 1 || !state.Ready {
		t.Fatalf("interrupted switch was not rolled back: %+v", state)
	}
	if err := installer.Stage(context.Background(), second, secondArtifact); err != nil {
		t.Fatal(err)
	}
	if err := installer.MarkReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err = installer.Rollback(context.Background())
	if err != nil || state.ActiveGeneration != 1 {
		t.Fatalf("bounded rollback failed: %v %+v", err, state)
	}
	if _, err := installer.Rollback(context.Background()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second rollback was accepted: %v", err)
	}
}

func TestDownloadResumesPartialArtifactAndChecksDigest(t *testing.T) {
	artifact := []byte("0123456789")
	digest := ArtifactDigest(artifact)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(artifact[:4])
			return
		}
		start, _ := strconv.Atoi(rangeHeader[len("bytes=") : len(rangeHeader)-1])
		w.Header().Set("Content-Range", "bytes "+strconv.Itoa(start)+"-"+strconv.Itoa(len(artifact)-1)+"/"+strconv.Itoa(len(artifact)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(artifact[start:])
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "scout-agent")
	if err := Download(context.Background(), server.Client(), server.URL, destination, digest, int64(len(artifact)), "agent-token"); err == nil {
		t.Fatal("short initial download was accepted")
	}
	if err := Download(context.Background(), server.Client(), server.URL, destination, digest, int64(len(artifact)), "agent-token"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != string(artifact) {
		t.Fatalf("downloaded artifact=%q err=%v", data, err)
	}
	if requests != 2 {
		t.Fatalf("requests=%d want 2", requests)
	}
}
