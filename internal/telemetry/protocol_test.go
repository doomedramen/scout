package telemetry

import (
	"math"
	"testing"
	"time"
)

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
