package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBatchReceiptKeyIsJSONSafeAndBackwardCompatible(t *testing.T) {
	key := batchReceiptKey("agent", "boot", "batch")
	if strings.ContainsRune(key, '\x00') {
		t.Fatal("batch receipt key contains a NUL byte")
	}
	encoded, err := json.Marshal(map[string]string{key: "payload-hash"})
	if err != nil {
		t.Fatalf("marshal batch receipt map: %v", err)
	}
	if strings.ContainsRune(string(encoded), '\x00') {
		t.Fatal("marshaled batch receipt map contains a NUL byte")
	}
	parsed, ok := parseBatchReceiptKey(key)
	if !ok || parsed.agentID != "agent" || parsed.bootID != "boot" || parsed.batchID != "batch" {
		t.Fatalf("failed to parse JSON batch receipt key: %+v, %t", parsed, ok)
	}
	parsed, ok = parseBatchReceiptKey("agent\x00boot\x00batch")
	if !ok || parsed.agentID != "agent" || parsed.bootID != "boot" || parsed.batchID != "batch" {
		t.Fatalf("failed to parse legacy batch receipt key: %+v, %t", parsed, ok)
	}
}

func TestTelemetryBackpressureIsVisibleAndRecoverable(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	site, _ := s.CreateSite(ctx, Site{Name: "telemetry-lab"})
	device, _ := s.CreateDevice(ctx, Device{DisplayName: "telemetry-target", SiteID: site.ID})
	agent := AgentIdentity{ID: NewID(), DeviceID: device.ID, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := s.CreateAgentIdentity(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWorkspace(ctx, func(state *WorkspaceState) error { state.MaxSamples = 1; return nil }); err != nil {
		t.Fatal(err)
	}
	value := 1.0
	sample := MetricSample{EntityID: "host", Metric: "test", Value: &value, Availability: FreshnessCurrent, Unit: "count", ObservedAt: time.Now().UTC()}
	if _, err := s.IngestBatch(ctx, agent.ID, "boot", "one", "hash-one", []MetricSample{sample}, nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestBatch(ctx, agent.ID, "boot", "two", "hash-two", []MetricSample{sample}, nil, 0); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("second batch was accepted past sample limit: %v", err)
	}
	status, err := s.TelemetryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Backpressure || status.Samples != 1 {
		t.Fatalf("unexpected telemetry status: %+v", status)
	}
	if err := s.SetTelemetryBackpressure(ctx, false); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryMetricQueryKeepsEntitySeriesSeparate(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	site, err := s.CreateSite(ctx, Site{Name: "series-lab"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := s.CreateDevice(ctx, Device{DisplayName: "series-target", SiteID: site.ID})
	if err != nil {
		t.Fatal(err)
	}
	agent := AgentIdentity{ID: NewID(), DeviceID: device.ID, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := s.CreateAgentIdentity(ctx, agent); err != nil {
		t.Fatal(err)
	}

	first, second := 1024.0, 2048.0
	observedAt := time.Now().UTC().Truncate(time.Second)
	_, err = s.IngestBatch(ctx, agent.ID, "boot", "entities", "entities-hash", []MetricSample{
		{EntityID: "disk-a", Metric: "disk.read_rate", Value: &first, Availability: FreshnessCurrent, Unit: "bytes_per_second", ObservedAt: observedAt},
		{EntityID: "disk-b", Metric: "disk.read_rate", Value: &second, Availability: FreshnessCurrent, Unit: "bytes_per_second", ObservedAt: observedAt.Add(time.Second)},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	series, err := s.QueryMetrics(ctx, MetricQuery{DeviceID: device.ID, Metric: "disk.read_rate"})
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 2 {
		t.Fatalf("expected one series per disk, got %d: %+v", len(series), series)
	}
	if series[0].EntityID != "disk-a" || series[1].EntityID != "disk-b" {
		t.Fatalf("unexpected series order or identity: %+v", series)
	}
	if len(series[0].Points) != 1 || series[0].Points[0].Value == nil || *series[0].Points[0].Value != first {
		t.Fatalf("disk-a history was mixed or lost: %+v", series[0])
	}
	if len(series[1].Points) != 1 || series[1].Points[0].Value == nil || *series[1].Points[0].Value != second {
		t.Fatalf("disk-b history was mixed or lost: %+v", series[1])
	}

	current, err := s.GetDevice(ctx, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.CurrentMetrics["disk-a:disk.read_rate"].Value == nil || current.CurrentMetrics["disk-b:disk.read_rate"].Value == nil {
		t.Fatalf("current metric projection lost entity identity: %+v", current.CurrentMetrics)
	}
	if current.MetricFreshness["disk-a:disk.read_rate"] != FreshnessCurrent || current.MetricFreshness["disk-b:disk.read_rate"] != FreshnessCurrent {
		t.Fatalf("current metric freshness lost entity identity: %+v", current.MetricFreshness)
	}
	filtered, err := s.QueryMetrics(ctx, MetricQuery{DeviceID: device.ID, Metric: "disk.read_rate", EntityID: "disk-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].EntityID != "disk-b" {
		t.Fatalf("entity filter returned the wrong series: %+v", filtered)
	}
}
