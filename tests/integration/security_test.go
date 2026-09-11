package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/store"
	"scout.local/scout/internal/telemetry"
)

func TestSecurityMatrixRevocationStopsTelemetryAndReenableNeedsScope(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	site, err := repository.CreateSite(ctx, store.Site{Name: "security-matrix"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{DisplayName: "protected-host", SiteID: site.ID, Addresses: []string{"192.0.2.20"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	agent := store.AgentIdentity{ID: "security-agent", DeviceID: device.ID, AuthTokenHash: store.HashToken("security-token"), CertSerial: "security-serial", ExpiresAt: now.Add(time.Hour), InstalledVersion: "0.1.0"}
	if err := repository.CreateAgentIdentity(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DecommissionDevice(ctx, device.ID, "security matrix"); err != nil {
		t.Fatal(err)
	}
	value := 1.0
	batch := telemetry.Batch{ProtocolVersion: telemetry.ProtocolVersion, BootID: "boot", BatchID: "batch", ObservedAt: now, Collector: telemetry.CollectorRef{ID: "host", SchemaVersion: 1}, Samples: []telemetry.Sample{{EntityID: "host", Metric: "cpu.utilization", Value: &value, Availability: "current", Unit: "percent", ObservedAt: now}}}
	service := &telemetry.Service{Store: repository}
	if _, err := service.Ingest(ctx, agent.ID, batch, nil); !errors.Is(err, store.ErrRevoked) {
		t.Fatalf("revoked agent submitted telemetry: %v", err)
	}
	if _, err := repository.ReenableDevice(ctx, device.ID, 0); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("reenable without an enabled scope was accepted: %v", err)
	}
}
