package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretEncryptionBindsContextAndDetectsTampering(t *testing.T) {
	ring, err := NewKeyRing(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := ring.EncryptSecret("cred-1", "ssh", []byte("private-value"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ring.DecryptSecret("cred-1", "ssh", envelope)
	if err != nil || string(plain) != "private-value" {
		t.Fatalf("decrypt: %v %q", err, plain)
	}
	if _, err := ring.DecryptSecret("cred-2", "ssh", envelope); err == nil {
		t.Fatal("context mismatch accepted")
	}
	envelope.Ciphertext[0] ^= 1
	if _, err := ring.DecryptSecret("cred-1", "ssh", envelope); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestSecretRotationKeepsOldVersionUntilKeyRetirement(t *testing.T) {
	first := bytes.Repeat([]byte{1}, 32)
	second := bytes.Repeat([]byte{2}, 32)
	ring, _ := NewKeyRing(first)
	old, err := ring.EncryptSecret("cred-1", "ssh", []byte("value"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ring.Rotate(second); err != nil {
		t.Fatal(err)
	}
	if got, err := ring.DecryptSecret("cred-1", "ssh", old); err != nil || string(got) != "value" {
		t.Fatalf("old version after rotation: %v", err)
	}
	ring.RemoveVersion(old.KeyVersion)
	if _, err := ring.DecryptSecret("cred-1", "ssh", old); !IsMissingKey(err) {
		t.Fatalf("expected missing key, got %v", err)
	}
}

func TestProvisionedKeyRequiresNarrowPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "master.key")
	key := bytes.Repeat([]byte{9}, 32)
	if err := WriteProvisionedKey(path, key); err != nil {
		t.Fatal(err)
	}
	ring, err := LoadKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if ring.CurrentVersion() != 1 {
		t.Fatal("unexpected version")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyFile(path); err == nil {
		t.Fatal("broad key permissions accepted")
	}
}
