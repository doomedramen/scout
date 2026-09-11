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

	windowStart := observedAt.Truncate(15 * time.Second)
	historyQuery := MetricQuery{DeviceID: device.ID, Metric: "disk.read_rate", From: windowStart, To: windowStart.Add(15 * time.Second), MaxPoints: 2}
	series, err := s.QueryMetrics(ctx, historyQuery)
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
	historyQuery.EntityID = "disk-b"
	filtered, err := s.QueryMetrics(ctx, historyQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].EntityID != "disk-b" {
		t.Fatalf("entity filter returned the wrong series: %+v", filtered)
	}
}

func TestMetricHistoryReturnsBucketsAndQualityMetadata(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	s := NewMemory()
	s.SetClock(func() time.Time { return now })
	device, agent := createTelemetryFixture(t, ctx, s)

	base := now.Add(-2 * time.Hour).Truncate(time.Minute)
	values := []float64{10, 20, 40, 80}
	observations := []time.Duration{30 * time.Second, time.Minute, 90 * time.Second, 4 * time.Minute}
	samples := make([]MetricSample, 0, len(values))
	for index, value := range values {
		value := value
		samples = append(samples, MetricSample{
			CollectorID:     "host",
			EntityID:        "host",
			Metric:          "cpu.utilization",
			Unit:            "percent",
			IntervalSeconds: 60,
			Value:           &value,
			Availability:    FreshnessCurrent,
			ObservedAt:      base.Add(observations[index]),
		})
	}
	if _, err := s.IngestBatch(ctx, agent.ID, "history-boot", "history-batch", "history-hash", samples, nil, 0); err != nil {
		t.Fatal(err)
	}

	series, err := s.QueryMetrics(ctx, MetricQuery{
		DeviceID:  device.ID,
		Metric:    "cpu.utilization",
		From:      base.Add(30 * time.Second),
		To:        base.Add(4*time.Minute + 30*time.Second),
		MaxPoints: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 {
		t.Fatalf("expected one history series, got %+v", series)
	}
	item := series[0]
	if item.SeriesID == "" || item.EntityID != "host" || item.ResolutionSeconds != 60 || len(item.Points) != 5 {
		t.Fatalf("unexpected history shape: %+v", item)
	}
	if !item.Points[0].Partial || item.Points[0].Count != 1 || item.Points[0].Value == nil || *item.Points[0].Value != 10 || item.Points[0].Availability != FreshnessUnavailable {
		t.Fatalf("first partial bucket lost its inspectable statistics: %+v", item.Points[0])
	}
	if item.Points[1].Count != 2 || item.Points[1].Value == nil || *item.Points[1].Value != 30 || item.Points[1].Min == nil || *item.Points[1].Min != 20 || item.Points[1].Max == nil || *item.Points[1].Max != 40 || item.Points[1].Coverage != 1 {
		t.Fatalf("bucket aggregation metadata is incorrect: %+v", item.Points[1])
	}
	if item.Points[3].Count != 0 || item.Points[3].Value != nil || item.Points[3].Coverage != 0 || item.Points[3].Availability != FreshnessUnavailable || item.Points[3].Partial {
		t.Fatalf("empty interior bucket was not represented as unavailable: %+v", item.Points[3])
	}
	if !item.Points[4].Partial || item.Points[4].Count != 1 || item.Points[4].Value == nil || *item.Points[4].Value != 80 || item.Points[4].Coverage != 1 || item.Points[4].Availability != FreshnessUnavailable {
		t.Fatalf("last partial bucket metadata is incorrect: %+v", item.Points[4])
	}

	grouped, err := s.QueryMetrics(ctx, MetricQuery{
		DeviceID:  device.ID,
		Metric:    "cpu.utilization",
		From:      base,
		To:        base.Add(5 * time.Minute),
		MaxPoints: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped) != 1 || len(grouped[0].Points) > 3 || grouped[0].ResolutionSeconds%60 != 0 || grouped[0].ResolutionSeconds <= 60 {
		t.Fatalf("history did not group into bounded source multiples: %+v", grouped)
	}
}

func TestMetricHistoryExplicitSeriesIsStableAndBounded(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	s := NewMemory()
	s.SetClock(func() time.Time { return now })
	device, agent := createTelemetryFixture(t, ctx, s)
	value := 42.0
	observedAt := now.Add(-time.Minute).Truncate(time.Minute)
	if _, err := s.IngestBatch(ctx, agent.ID, "explicit-boot", "explicit-batch", "explicit-hash", []MetricSample{{
		CollectorID: "host", EntityID: "host", Metric: "memory.used_percent", Unit: "percent", Value: &value, Availability: FreshnessCurrent, ObservedAt: observedAt,
	}}, nil, 0); err != nil {
		t.Fatal(err)
	}

	known, err := s.QueryMetrics(ctx, MetricQuery{DeviceID: device.ID, Metric: "memory.used_percent", From: observedAt, To: observedAt.Add(time.Minute), MaxPoints: 2})
	if err != nil || len(known) != 1 {
		t.Fatalf("known series query failed: series=%+v err=%v", known, err)
	}
	knownID := known[0].SeriesID
	if knownID == "" {
		t.Fatal("known series did not expose a stable identifier")
	}

	unknown, err := s.QueryMetrics(ctx, MetricQuery{DeviceID: device.ID, SeriesIDs: []string{"00000000-0000-4000-8000-000000000000"}, From: observedAt, To: observedAt.Add(time.Minute), MaxPoints: 2})
	if err != nil || len(unknown) != 1 || unknown[0].SeriesID != "00000000-0000-4000-8000-000000000000" || unknown[0].Metric != "" || len(unknown[0].Points) != 0 {
		t.Fatalf("unknown explicit series was substituted or populated: series=%+v err=%v", unknown, err)
	}

	mismatched, err := s.QueryMetrics(ctx, MetricQuery{DeviceID: device.ID, SeriesIDs: []string{knownID}, Metric: "cpu.utilization", From: observedAt, To: observedAt.Add(time.Minute), MaxPoints: 2})
	if err != nil || len(mismatched) != 1 || mismatched[0].SeriesID != knownID || mismatched[0].Metric != "memory.used_percent" || len(mismatched[0].Points) != 0 {
		t.Fatalf("explicit series filter was not honored: series=%+v err=%v", mismatched, err)
	}

	if _, err := s.QueryMetrics(ctx, MetricQuery{DeviceID: device.ID, SeriesIDs: []string{knownID}, From: observedAt, To: observedAt.Add(time.Minute), MaxPoints: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("maxPoints=1 was accepted: %v", err)
	}
	tooMany := make([]string, MaxMetricHistorySeries+1)
	for index := range tooMany {
		tooMany[index] = NewID()
	}
	if _, err := s.QueryMetrics(ctx, MetricQuery{DeviceID: device.ID, SeriesIDs: tooMany, From: observedAt, To: observedAt.Add(time.Minute), MaxPoints: 2}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("more than %d explicit series were accepted: %v", MaxMetricHistorySeries, err)
	}
}

func createTelemetryFixture(t *testing.T, ctx context.Context, s *Store) (Device, AgentIdentity) {
	t.Helper()
	device, err := s.CreateDevice(ctx, Device{DisplayName: "history-target", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	agent := AgentIdentity{ID: NewID(), DeviceID: device.ID, ExpiresAt: s.Now().Add(time.Hour)}
	if err := s.CreateAgentIdentity(ctx, agent); err != nil {
		t.Fatal(err)
	}
	return device, agent
}
