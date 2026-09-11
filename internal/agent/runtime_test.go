package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
