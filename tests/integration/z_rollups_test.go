package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/store"
	"scout.local/scout/internal/telemetry"
)

func TestRollupsUseLeasesGenerationsAndBoundedBackfill(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	repository := store.NewSQL(db)
	ensureAuthoritativeMonitoring(t, ctx, repository)
	device, err := repository.CreateDevice(ctx, store.Device{ID: store.NewID(), DisplayName: "rollup-target", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	agent := store.AgentIdentity{ID: store.NewID(), DeviceID: device.ID, CertSerial: "rollup-" + store.NewID(), PublicKeyHash: "rollup-public-key", CertificatePEM: "rollup-certificate", ExpiresAt: time.Now().UTC().Add(time.Hour), InstalledVersion: "fixture"}
	if err := repository.CreateAgentIdentity(ctx, agent); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC)
	first, second, fourth := 10.0, 20.0, 40.0
	initial := []store.MetricSample{
		{CollectorID: "host", EntityID: "host", Metric: "cpu.utilization", Unit: "percent", IntervalSeconds: 60, Value: &first, Availability: store.FreshnessCurrent, ObservedAt: base, ReceivedAt: base},
		{CollectorID: "host", EntityID: "host", Metric: "cpu.utilization", Unit: "percent", IntervalSeconds: 60, Value: &second, Availability: store.FreshnessCurrent, ObservedAt: base.Add(time.Minute), ReceivedAt: base.Add(time.Minute)},
		{CollectorID: "host", EntityID: "host", Metric: "cpu.utilization", Unit: "percent", IntervalSeconds: 60, Availability: store.FreshnessUnavailable, ObservedAt: base.Add(2 * time.Minute), ReceivedAt: base.Add(2 * time.Minute)},
		{CollectorID: "host", EntityID: "host", Metric: "cpu.utilization", Unit: "percent", IntervalSeconds: 60, Value: &fourth, Availability: store.FreshnessCurrent, ObservedAt: base.Add(3 * time.Minute), ReceivedAt: base.Add(3 * time.Minute)},
	}
	if _, err := repository.IngestBatch(ctx, agent.ID, "rollup-boot", "rollup-initial", "rollup-initial-hash", initial, nil, 0); err != nil {
		t.Fatal(err)
	}
	var seriesID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM metric_series WHERE device_id = $1 AND metric = 'cpu.utilization'`, device.ID).Scan(&seriesID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM rollup_work WHERE series_id <> $1`, seriesID); err != nil {
		t.Fatal(err)
	}

	childClaims, err := repository.ClaimRollupWork(ctx, "worker-a", 1, time.Minute)
	if err != nil || len(childClaims) != 1 {
		t.Fatalf("first rollup claim = %+v, err=%v", childClaims, err)
	}
	child := childClaims[0]
	if child.ResolutionSeconds != store.RollupResolutionFiveMinute {
		t.Fatalf("claim order did not prioritize five-minute work: %+v", child)
	}
	parentClaims, err := repository.ClaimRollupWork(ctx, "worker-b", 1, time.Minute)
	if err != nil || len(parentClaims) != 1 {
		t.Fatalf("second rollup claim = %+v, err=%v", parentClaims, err)
	}
	parent := parentClaims[0]
	if parent.ResolutionSeconds != store.RollupResolutionHourly || parent.SeriesID != child.SeriesID {
		t.Fatalf("second worker claimed the wrong work: %+v", parent)
	}
	if _, err := repository.ComputeRollupAggregate(ctx, parent); !errors.Is(err, store.ErrRollupSourcePending) {
		t.Fatalf("hourly rollup ignored pending child: %v", err)
	}
	if err := repository.FailRollupWork(ctx, parent, "source aggregate pending"); err != nil {
		t.Fatal(err)
	}

	late := 90.0
	if _, err := repository.IngestBatch(ctx, agent.ID, "rollup-boot", "rollup-late", "rollup-late-hash", []store.MetricSample{{CollectorID: "host", EntityID: "host", Metric: "cpu.utilization", Unit: "percent", IntervalSeconds: 60, Value: &late, Availability: store.FreshnessCurrent, ObservedAt: base.Add(4 * time.Minute), ReceivedAt: base.Add(4 * time.Minute)}}, nil, 0); err != nil {
		t.Fatal(err)
	}
	staleAggregate, err := repository.ComputeRollupAggregate(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteRollupWork(ctx, child, staleAggregate); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale generation was committed: %v", err)
	}

	currentClaims, err := repository.ClaimRollupWork(ctx, "worker-c", 1, time.Minute)
	if err != nil || len(currentClaims) != 1 {
		t.Fatalf("reclaimed child rollup = %+v, err=%v", currentClaims, err)
	}
	currentChild := currentClaims[0]
	currentAggregate, err := repository.ComputeRollupAggregate(ctx, currentChild)
	if err != nil {
		t.Fatal(err)
	}
	if currentAggregate.Count != 4 || currentAggregate.ExpectedCount != 5 || currentAggregate.CoveredSeconds != 240 || currentAggregate.Min == nil || *currentAggregate.Min != 10 || currentAggregate.Max == nil || *currentAggregate.Max != 90 {
		t.Fatalf("unexpected five-minute aggregate: %+v", currentAggregate)
	}
	if err := repository.CompleteRollupWork(ctx, currentChild, currentAggregate); err != nil {
		t.Fatal(err)
	}

	parentClaims, err = repository.ClaimRollupWork(ctx, "worker-d", 1, time.Minute)
	if err != nil || len(parentClaims) != 1 || parentClaims[0].ResolutionSeconds != store.RollupResolutionHourly {
		t.Fatalf("reclaimed hourly rollup = %+v, err=%v", parentClaims, err)
	}
	currentParent := parentClaims[0]
	hourly, err := repository.ComputeRollupAggregate(ctx, currentParent)
	if err != nil {
		t.Fatal(err)
	}
	if hourly.Count != 4 || hourly.ExpectedCount != 60 || hourly.CoveredSeconds != 240 || hourly.Sum == nil || *hourly.Sum != 160 || hourly.Min == nil || *hourly.Min != 10 || hourly.Max == nil || *hourly.Max != 90 {
		t.Fatalf("unexpected hourly aggregate: %+v", hourly)
	}
	if err := repository.CompleteRollupWork(ctx, currentParent, hourly); err != nil {
		t.Fatal(err)
	}
	if work, err := repository.ClaimRollupWork(ctx, "worker-e", 1, time.Minute); err != nil || len(work) != 0 {
		t.Fatalf("completed rollup work remained claimable: work=%+v err=%v", work, err)
	}

	backfilled, err := repository.BackfillRollupWork(ctx, base, base.Add(5*time.Minute), 1)
	if err != nil || backfilled != 2 {
		t.Fatalf("bounded backfill enqueued %d rows, err=%v", backfilled, err)
	}
	service := &telemetry.Service{Store: repository}
	firstRun, err := service.RunRollups(ctx, telemetry.RollupPolicy{Owner: "service-worker", Limit: 1000, Lease: time.Minute, MaxDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if firstRun.Claimed != 2 || firstRun.Completed != 1 || firstRun.Stale != 1 {
		t.Fatalf("generation fence was not exercised by worker: %+v", firstRun)
	}
	secondRun, err := service.RunRollups(ctx, telemetry.RollupPolicy{Owner: "service-worker", Limit: 1000, Lease: time.Minute, MaxDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if secondRun.Claimed != 1 || secondRun.Completed != 1 {
		t.Fatalf("queued hourly recomputation was not completed: %+v", secondRun)
	}

	var fiveMinuteRows, hourlyRows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_aggregates WHERE series_id = $1 AND resolution_seconds = $2`, seriesID, store.RollupResolutionFiveMinute).Scan(&fiveMinuteRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_aggregates WHERE series_id = $1 AND resolution_seconds = $2`, seriesID, store.RollupResolutionHourly).Scan(&hourlyRows); err != nil {
		t.Fatal(err)
	}
	if fiveMinuteRows != 1 || hourlyRows != 1 {
		t.Fatalf("rollup recomputation appended duplicate rows: fiveMinute=%d hourly=%d", fiveMinuteRows, hourlyRows)
	}
	if _, err := repository.ClaimRollupWork(ctx, "worker-f", store.MaxRollupWorkItems+1, time.Minute); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("oversized rollup claim was accepted: %v", err)
	}
	if _, err := repository.BackfillRollupWork(ctx, base.Add(time.Hour), base, 1); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("reversed rollup backfill range was accepted: %v", err)
	}
}
