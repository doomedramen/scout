package integration

import (
	"context"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestSyntheticLoad100Devices24Hours(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	if _, err := repository.SetWorkspace(ctx, func(state *store.WorkspaceState) error {
		state.MaxSamples = 10_000
		state.RetentionHours = 24
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	site, err := repository.CreateSite(ctx, store.Site{Name: "synthetic-load"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	started := time.Now()
	for deviceIndex := 0; deviceIndex < 100; deviceIndex++ {
		device, createErr := repository.CreateDevice(ctx, store.Device{DisplayName: "load-device", SiteID: site.ID, Platform: "linux", Architecture: "amd64"})
		if createErr != nil {
			t.Fatal(createErr)
		}
		agent := store.AgentIdentity{ID: "load-agent-" + store.NewID(), DeviceID: device.ID, CertSerial: "load-serial-" + store.NewID(), ExpiresAt: base.Add(48 * time.Hour), InstalledVersion: "0.1.0"}
		if createErr := repository.CreateAgentIdentity(ctx, agent); createErr != nil {
			t.Fatal(createErr)
		}
		for hour := 0; hour < 24; hour++ {
			value := float64(deviceIndex + hour)
			observed := base.Add(time.Duration(hour) * time.Hour)
			if _, ingestErr := repository.IngestBatch(ctx, agent.ID, "boot", "batch-"+store.NewID(), "hash-"+store.NewID(), []store.MetricSample{{DeviceID: device.ID, AgentID: agent.ID, CollectorID: "host", EntityID: "host", Metric: "cpu.utilization", Value: &value, Availability: store.FreshnessCurrent, Unit: "percent", ObservedAt: observed, ReceivedAt: observed}}, nil, 0); ingestErr != nil {
				t.Fatal(ingestErr)
			}
		}
	}
	status, err := repository.TelemetryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Samples != 2400 || status.Backpressure {
		t.Fatalf("synthetic ingestion exceeded expected bounds: %+v", status)
	}
	for _, device := range mustListDevices(t, repository) {
		series, queryErr := repository.QueryMetrics(ctx, store.MetricQuery{DeviceID: device.ID, MaxPoints: 12})
		if queryErr != nil || len(series) != 1 || len(series[0].Points) != 12 {
			t.Fatalf("bounded history query failed for %s: series=%+v err=%v", device.ID, series, queryErr)
		}
	}
	t.Logf("synthetic 100-device/24-hour fixture ingested and queried in %s; this is not a live capacity claim", time.Since(started).Round(time.Millisecond))
}

func mustListDevices(t *testing.T, repository *store.Store) []store.Device {
	t.Helper()
	items, err := repository.ListDevices(context.Background(), store.DeviceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return items
}
