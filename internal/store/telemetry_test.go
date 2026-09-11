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
