package integration

import (
	"context"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestSQLMetricHistoryUsesTiersAndEmptyBuckets(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	repository := store.NewSQL(db)
	ensureAuthoritativeMonitoring(t, ctx, repository)
	device, err := repository.CreateDevice(ctx, store.Device{ID: store.NewID(), DisplayName: "history-sql-target", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	agent := store.AgentIdentity{
		ID:               store.NewID(),
		DeviceID:         device.ID,
		CertSerial:       "history-sql-" + store.NewID(),
		PublicKeyHash:    "history-sql-public-key",
		CertificatePEM:   "history-sql-certificate",
		ExpiresAt:        time.Now().UTC().Add(time.Hour),
		InstalledVersion: "fixture",
	}
	if err := repository.CreateAgentIdentity(ctx, agent); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	rawBase := now.Truncate(time.Minute).Add(-5 * time.Minute)
	rawValues := []float64{10, 20, 40}
	rawTimes := []time.Time{rawBase, rawBase.Add(time.Minute), rawBase.Add(3 * time.Minute)}
	rawSamples := make([]store.MetricSample, 0, len(rawValues))
	for index, value := range rawValues {
		value := value
		rawSamples = append(rawSamples, store.MetricSample{
			CollectorID:     "host",
			EntityID:        "host",
			Metric:          "cpu.utilization",
			Unit:            "percent",
			IntervalSeconds: 60,
			Value:           &value,
			Availability:    store.FreshnessCurrent,
			ObservedAt:      rawTimes[index],
			ReceivedAt:      rawTimes[index],
		})
	}
	if _, err := repository.IngestBatch(ctx, agent.ID, "history-sql-boot", "history-sql-raw", "history-sql-raw-hash", rawSamples, nil, 0); err != nil {
		t.Fatal(err)
	}

	raw, err := repository.QueryMetrics(ctx, store.MetricQuery{
		DeviceID:  device.ID,
		Metric:    "cpu.utilization",
		From:      rawBase,
		To:        rawBase.Add(5 * time.Minute),
		MaxPoints: 10,
	})
	if err != nil || len(raw) != 1 {
		t.Fatalf("raw history query failed: series=%+v err=%v", raw, err)
	}
	if raw[0].ResolutionSeconds != 60 || len(raw[0].Points) != 5 || raw[0].Points[2].Count != 0 || raw[0].Points[2].Coverage != 0 || raw[0].Points[2].Availability != store.FreshnessUnavailable {
		t.Fatalf("raw history did not retain full empty bucket range: %+v", raw[0])
	}
	if raw[0].Points[0].Count != 1 || raw[0].Points[0].Value == nil || *raw[0].Points[0].Value != 10 || raw[0].Points[3].Count != 1 || raw[0].Points[3].Value == nil || *raw[0].Points[3].Value != 40 {
		t.Fatalf("raw history values were not preserved: %+v", raw[0])
	}

	oldBase := now.Add(-100 * 24 * time.Hour).Truncate(time.Hour)
	oldFirst, oldSecond := 50.0, 70.0
	oldSamples := []store.MetricSample{
		{CollectorID: "host", EntityID: "host", Metric: "memory.used_percent", Unit: "percent", IntervalSeconds: 60, Value: &oldFirst, Availability: store.FreshnessCurrent, ObservedAt: oldBase.Add(10 * time.Minute), ReceivedAt: oldBase.Add(10 * time.Minute)},
		{CollectorID: "host", EntityID: "host", Metric: "memory.used_percent", Unit: "percent", IntervalSeconds: 60, Value: &oldSecond, Availability: store.FreshnessCurrent, ObservedAt: oldBase.Add(11 * time.Minute), ReceivedAt: oldBase.Add(11 * time.Minute)},
	}
	if _, err := repository.IngestBatch(ctx, agent.ID, "history-sql-boot", "history-sql-old", "history-sql-old-hash", oldSamples, nil, 0); err != nil {
		t.Fatal(err)
	}
	var oldSeriesID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM metric_series WHERE device_id = $1 AND metric = 'memory.used_percent'`, device.ID).Scan(&oldSeriesID); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := db.QueryRowContext(ctx, `SELECT storage_generation FROM monitoring_storage_state WHERE singleton = true`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	sum, minimum, maximum := 120.0, 50.0, 70.0
	if _, err := db.ExecContext(ctx, `
		INSERT INTO metric_aggregates(series_id, resolution_seconds, bucket_start, count, sum, min, max, expected_count, covered_seconds, bucket_seconds, partial, generation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, false, $11)`, oldSeriesID, store.RollupResolutionHourly, oldBase, 2, sum, minimum, maximum, 60, 120, 3600, generation); err != nil {
		t.Fatal(err)
	}

	hourly, err := repository.QueryMetrics(ctx, store.MetricQuery{
		DeviceID:  device.ID,
		Metric:    "memory.used_percent",
		From:      oldBase,
		To:        oldBase.Add(2 * time.Hour),
		MaxPoints: 4,
	})
	if err != nil || len(hourly) != 1 {
		t.Fatalf("hourly history query failed: series=%+v err=%v", hourly, err)
	}
	if hourly[0].ResolutionSeconds != store.RollupResolutionHourly || len(hourly[0].Points) != 2 {
		t.Fatalf("old history did not select hourly tier: %+v", hourly[0])
	}
	point := hourly[0].Points[0]
	if point.Count != 2 || point.Value == nil || *point.Value != 60 || point.Min == nil || *point.Min != 50 || point.Max == nil || *point.Max != 70 || point.Coverage != 120.0/3600.0 || point.Partial {
		t.Fatalf("hourly aggregate statistics were not exposed: %+v", point)
	}
	if hourly[0].Points[1].Count != 0 || hourly[0].Points[1].Value != nil || hourly[0].Points[1].Availability != store.FreshnessUnavailable {
		t.Fatalf("hourly empty bucket was not exposed: %+v", hourly[0].Points[1])
	}

	unknown, err := repository.QueryMetrics(ctx, store.MetricQuery{
		DeviceID:  device.ID,
		SeriesIDs: []string{"00000000-0000-4000-8000-000000000000"},
		From:      rawBase,
		To:        rawBase.Add(5 * time.Minute),
		MaxPoints: 2,
	})
	if err != nil || len(unknown) != 1 || unknown[0].SeriesID != "00000000-0000-4000-8000-000000000000" || unknown[0].Metric != "" || len(unknown[0].Points) != 0 {
		t.Fatalf("SQL unknown explicit series was substituted or populated: series=%+v err=%v", unknown, err)
	}
}
