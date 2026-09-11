package telemetry

import (
	"math"
	"testing"
	"time"

	"scout.local/scout/internal/collector"
)

func TestFromCollectorPreservesLegacyAndDiagnosticHostSchemas(t *testing.T) {
	now := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	legacy := FromCollector(collector.HostSnapshot{ObservedAt: now}, "boot", "legacy")
	if legacy.Collector.SchemaVersion != 1 {
		t.Fatalf("legacy host schema = %d, want 1", legacy.Collector.SchemaVersion)
	}
	diagnostic := FromCollector(collector.HostSnapshot{ObservedAt: now, SchemaVersion: collector.HostSchemaVersion}, "boot", "diagnostic")
	if diagnostic.Collector.SchemaVersion != collector.HostSchemaVersion {
		t.Fatalf("diagnostic host schema = %d, want %d", diagnostic.Collector.SchemaVersion, collector.HostSchemaVersion)
	}
	for _, batch := range []Batch{legacy, diagnostic} {
		if err := ValidateBatch(batch, now); err != nil {
			t.Fatalf("schema %d rejected: %v", batch.Collector.SchemaVersion, err)
		}
	}
}

func TestBatchValidationRejectsNonFiniteAndOversizeLabels(t *testing.T) {
	value := math.NaN()
	batch := Batch{ProtocolVersion: 1, BootID: "boot", BatchID: "batch", Collector: CollectorRef{ID: "host", SchemaVersion: 1}, Samples: []Sample{{EntityID: "host", Metric: "cpu", Unit: "percent", Value: &value, Availability: "current"}}}
	if err := ValidateBatch(batch, time.Now()); err == nil {
		t.Fatal("NaN accepted")
	}
	value = 1
	batch.Samples[0].Value = &value
	batch.Samples[0].Labels = map[string]string{"key": string(make([]byte, 257))}
	if err := ValidateBatch(batch, time.Now()); err == nil {
		t.Fatal("oversize label accepted")
	}
}
