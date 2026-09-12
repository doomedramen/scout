package discovery

import (
	"errors"
	"testing"

	"scout.local/scout/internal/store"
)

func TestDefaultScanCatalogIsSSHOnly(t *testing.T) {
	entries := DefaultScanCatalog()
	if len(entries) != 1 {
		t.Fatalf("default catalog entries=%d, want one", len(entries))
	}
	entry := entries[0]
	if entry.ID != "ssh-default" || entry.Name != "SSH" || entry.Transport != store.ScanTransportTCP || entry.Port != 22 || entry.AccessMethod != store.ScanAccessSSH || !entry.Enabled {
		t.Fatalf("unexpected default catalog entry: %+v", entry)
	}
}

func TestScanCatalogRejectsUnsafeOrAmbiguousEntries(t *testing.T) {
	cases := []store.ScanEntryPoint{
		{ID: "udp", Name: "UDP", Transport: "udp", Port: 53, Enabled: true},
		{ID: "bad-port", Name: "bad", Transport: store.ScanTransportTCP, Port: 0, Enabled: true},
		{ID: "unsupported", Name: "unsupported", Transport: store.ScanTransportTCP, Port: 22, AccessMethod: "telnet", Enabled: true},
		{ID: "dup-a", Name: "a", Transport: store.ScanTransportTCP, Port: 22, Enabled: true},
		{ID: "dup-b", Name: "b", Transport: store.ScanTransportTCP, Port: 22, Enabled: true},
	}
	for index, entries := range [][]store.ScanEntryPoint{cases[:1], cases[1:2], cases[2:3], cases[3:]} {
		if err := ValidateScanCatalog(entries); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("case %d accepted unsafe catalog: %v", index, err)
		}
	}
}

func TestScanCatalogKeepsObservationOnlyPortsTyped(t *testing.T) {
	entries := []store.ScanEntryPoint{{ID: "http-alt", Name: "HTTP alternate", Transport: store.ScanTransportTCP, Port: 8080, Enabled: true}}
	if err := ValidateScanCatalog(entries); err != nil {
		t.Fatal(err)
	}
	if entries[0].AccessMethod != "" {
		t.Fatalf("observation-only entry unexpectedly gained access method: %+v", entries[0])
	}
}
