package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
