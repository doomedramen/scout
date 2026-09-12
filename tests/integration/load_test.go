package integration

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestSyntheticLoad100Devices24Hours(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	const (
		deviceCount       = 100
		hours             = 24
		hoursPerBatch     = 6
		numericSeries     = 40
		serviceStateCount = 50
	)
	expectedSamples := deviceCount * hours * numericSeries
	expectedObservations := deviceCount * hours * serviceStateCount
	if _, err := repository.SetWorkspace(ctx, func(state *store.WorkspaceState) error {
		state.MaxSamples = expectedSamples + 1
		state.RetentionHours = 24
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	site, err := repository.CreateSite(ctx, store.Site{Name: "synthetic-load"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(-365 * 24 * time.Hour)
	started := time.Now()
	devices := make([]store.Device, 0, deviceCount)
	for deviceIndex := 0; deviceIndex < deviceCount; deviceIndex++ {
		device, createErr := repository.CreateDevice(ctx, store.Device{DisplayName: fmt.Sprintf("load-device-%03d", deviceIndex), SiteID: site.ID, Platform: "linux", Architecture: "amd64"})
		if createErr != nil {
			t.Fatal(createErr)
		}
		agent := store.AgentIdentity{ID: "load-agent-" + store.NewID(), DeviceID: device.ID, CertSerial: "load-serial-" + store.NewID(), ExpiresAt: base.Add(48 * time.Hour), InstalledVersion: "0.1.0"}
		if createErr := repository.CreateAgentIdentity(ctx, agent); createErr != nil {
			t.Fatal(createErr)
		}
		devices = append(devices, device)
		for batchStart := 0; batchStart < hours; batchStart += hoursPerBatch {
			batchEnd := batchStart + hoursPerBatch
			if batchEnd > hours {
				batchEnd = hours
			}
			samples := make([]store.MetricSample, 0, numericSeries*(batchEnd-batchStart))
			observations := make([]store.Observation, 0, serviceStateCount*(batchEnd-batchStart))
			for hour := batchStart; hour < batchEnd; hour++ {
				observed := base.Add(time.Duration(hour) * time.Hour)
				for seriesIndex := 0; seriesIndex < numericSeries; seriesIndex++ {
					value := float64((deviceIndex+seriesIndex+hour)%100) + 0.25
					samples = append(samples, store.MetricSample{
						DeviceID: device.ID, AgentID: agent.ID, CollectorID: "host", EntityID: fmt.Sprintf("series-%02d", seriesIndex),
						Metric: fmt.Sprintf("load.metric.%02d", seriesIndex), Value: &value, Availability: store.FreshnessCurrent,
						Unit: "count", IntervalSeconds: 3600, ObservedAt: observed, ReceivedAt: observed,
					})
				}
				for serviceIndex := 0; serviceIndex < serviceStateCount; serviceIndex++ {
					observations = append(observations, store.Observation{
						ReporterID: agent.ID, CollectorID: "systemd", SubjectID: fmt.Sprintf("service-%02d", serviceIndex),
						Kind: "service.state", Payload: map[string]any{"state": "active", "required": serviceIndex%2 == 0},
						ObservedAt: observed, ReceivedAt: observed, ExpiresAt: observed.Add(2 * time.Hour), Confidence: 1,
					})
				}
			}
			if _, ingestErr := repository.IngestBatch(ctx, agent.ID, "boot-"+device.ID, fmt.Sprintf("batch-%02d", batchStart), fmt.Sprintf("hash-%s-%02d", device.ID, batchStart), samples, observations, 0); ingestErr != nil {
				t.Fatal(ingestErr)
			}
		}
	}
	status, err := repository.TelemetryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Samples != expectedSamples || status.Backpressure {
		t.Fatalf("synthetic ingestion exceeded expected bounds: %+v", status)
	}
	monitoring, err := repository.MonitoringStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observations, err := repository.ListObservations(ctx)
	if err != nil || len(observations) != expectedObservations {
		t.Fatalf("synthetic service-state observations = %d, want %d (err=%v)", len(observations), expectedObservations, err)
	}
	queryLatencies := make([]time.Duration, 0, len(devices))
	for _, device := range devices {
		queryStarted := time.Now()
		series, queryErr := repository.QueryMetrics(ctx, store.MetricQuery{
			DeviceID: device.ID, Metric: "load.metric.00", EntityID: "series-00", From: base,
			To: base.Add(time.Duration(hours) * time.Hour), MaxPoints: 24,
		})
		queryLatencies = append(queryLatencies, time.Since(queryStarted))
		if queryErr != nil || len(series) != 1 || series[0].ResolutionSeconds != store.RollupResolutionHourly || len(series[0].Points) != hours {
			t.Fatalf("year-tier history query failed for %s: series=%+v err=%v", device.ID, series, queryErr)
		}
	}
	sort.Slice(queryLatencies, func(left, right int) bool { return queryLatencies[left] < queryLatencies[right] })
	p95Index := (len(queryLatencies)*95+99)/100 - 1
	if p95Index < 0 {
		p95Index = 0
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	t.Logf("synthetic expanded workload: devices=%d numeric_series=%d service_states=%d hours=%d samples=%d observations=%d evaluation_queue=%d rollup_queue=%d evaluation_lag=%.0fs ingest=%s query_p95=%s heap_alloc=%d bytes; not a live capacity claim", deviceCount, numericSeries, serviceStateCount, hours, status.Samples, len(observations), monitoring.Queues.Evaluation, monitoring.Queues.Rollup, monitoring.EvaluationLagSeconds, time.Since(started).Round(time.Millisecond), queryLatencies[p95Index].Round(time.Microsecond), memory.HeapAlloc)
}

func mustListDevices(t *testing.T, repository *store.Store) []store.Device {
	t.Helper()
	items, err := repository.ListDevices(context.Background(), store.DeviceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return items
}
