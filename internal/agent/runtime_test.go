package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestRuntimeUsesConfiguredScanCapabilities(t *testing.T) {
	defaultRuntime, err := NewRuntime(Config{ServerURL: "http://scout.test", DataDir: filepath.Join(t.TempDir(), "default")})
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultRuntime.Config.ScanCapabilities; len(got.ScanProtocolVersions) != 1 || got.ScanProtocolVersions[0] != 1 || len(got.ScanTransports) != 1 || got.ScanTransports[0] != store.ScanTransportTCP {
		t.Fatalf("default scan capabilities = %+v", got)
	}

	configured := store.ScanCapabilities{ScanProtocolVersions: []int{1, 2}, ScanTransports: []string{store.ScanTransportTCP}}
	configuredRuntime, err := NewRuntime(Config{ServerURL: "http://scout.test", DataDir: filepath.Join(t.TempDir(), "configured"), ScanCapabilities: configured})
	if err != nil {
		t.Fatal(err)
	}
	if got := configuredRuntime.Config.ScanCapabilities; len(got.ScanProtocolVersions) != 2 || got.ScanProtocolVersions[1] != 2 {
		t.Fatalf("configured scan capabilities = %+v", got)
	}
}

func TestRuntimeRejectsInvalidScanCapabilities(t *testing.T) {
	_, err := NewRuntime(Config{ServerURL: "http://scout.test", DataDir: t.TempDir(), ScanCapabilities: store.ScanCapabilities{ScanProtocolVersions: []int{0}, ScanTransports: []string{store.ScanTransportTCP}}})
	if err == nil {
		t.Fatal("invalid scan capabilities were accepted")
	}
}

func TestSpoolIsBoundedAndEvictsOldest(t *testing.T) {
	spool := NewSpool(filepath.Join(t.TempDir(), "spool"), 10, time.Hour)
	if err := spool.Add([]byte("123456")); err != nil {
		t.Fatal(err)
	}
	if err := spool.Add([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	items, err := spool.Items()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || string(items[0].Data) != "abcdef" {
		t.Fatalf("unexpected spool items: %+v", items)
	}
	if spool.Dropped() != 1 {
		t.Fatalf("dropped=%d", spool.Dropped())
	}
}

func TestSpoolUsesPrivateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	spool := NewSpool(dir, 1024, time.Hour)
	if err := spool.Add([]byte(`{"sample":1}`)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("batch missing")
	}
	info, _ := entries[0].Info()
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestRemoveConsumedInvitationAcceptsReadOnlyMountErrors(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "invitation")
	if err := os.WriteFile(path, []byte("one-time"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(directory, 0o700)
	if err := removeConsumedInvitation(path); err != nil {
		t.Fatal(err)
	}
}
