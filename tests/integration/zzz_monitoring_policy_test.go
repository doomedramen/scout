package integration

import (
	"context"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestSQLMonitoringPolicyPreventsUnaggregatedDeletion(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	repository := store.NewSQL(db)
	ensureAuthoritativeMonitoring(t, ctx, repository)

	deviceID := store.NewID()
	agentID := store.NewID()
	cleanup := func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM current_series WHERE series_id IN (SELECT id FROM metric_series WHERE device_id = $1)`, deviceID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM rollup_work WHERE series_id IN (SELECT id FROM metric_series WHERE device_id = $1)`, deviceID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM metric_aggregates WHERE series_id IN (SELECT id FROM metric_series WHERE device_id = $1)`, deviceID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM metric_samples WHERE device_id = $1`, deviceID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM metric_series WHERE device_id = $1`, deviceID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM telemetry_sample_ordinals WHERE agent_id = $1`, agentID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM telemetry_receipts WHERE agent_id = $1`, agentID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM agent_identities WHERE id = $1`, agentID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM devices WHERE id = $1`, deviceID)
	}
	t.Cleanup(cleanup)

	device, err := repository.CreateDevice(ctx, store.Device{ID: deviceID, DisplayName: "monitoring-policy-sql-target", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	agent := store.AgentIdentity{ID: agentID, DeviceID: device.ID, CertSerial: "monitoring-policy-" + agentID, PublicKeyHash: "monitoring-policy-public-key", CertificatePEM: "monitoring-policy-certificate", ExpiresAt: time.Now().UTC().Add(time.Hour), InstalledVersion: "fixture"}
	if err := repository.CreateAgentIdentity(ctx, agent); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	repository.SetClock(func() time.Time { return now })
	settings, err := repository.GetMonitoringSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Retention.RawDays < 2 {
		t.Skipf("shared integration database already has raw retention at %d days", settings.Retention.RawDays)
	}
	proposed := settings.Retention
	proposed.RawDays--
	oldObservedAt := now.Add(-time.Duration(proposed.RawDays)*24*time.Hour - 12*time.Hour)
	value := 42.0
	if _, err := repository.IngestBatch(ctx, agent.ID, "monitoring-policy-boot", "monitoring-policy-batch", "monitoring-policy-hash", []store.MetricSample{{
		CollectorID: "host", EntityID: "host", Metric: "cpu.utilization", Unit: "percent", IntervalSeconds: 60, Value: &value, Availability: store.FreshnessCurrent, ObservedAt: oldObservedAt, ReceivedAt: oldObservedAt,
	}}, nil, 0); err != nil {
		t.Fatal(err)
	}

	preview, err := repository.CreateRetentionPreview(ctx, store.RetentionPreviewInput{ExpectedRevision: settings.Revision, Retention: proposed, IdempotencyKey: "monitoring-policy-sql-preview"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.EstimatedRows != 1 || !preview.Irreversible {
		t.Fatalf("preview did not identify the newly affected raw row: %+v", preview)
	}
	if _, err := repository.UpdateMonitoringSettings(ctx, store.MonitoringSettingsPatch{ExpectedRevision: settings.Revision, Retention: &store.MonitoringRetentionPatch{RawDays: &proposed.RawDays}, RetentionPreviewID: preview.PreviewID}); err != nil {
		t.Fatal(err)
	}

	blocked, err := repository.RetainTelemetry(ctx, proposed, now)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.RawDeleted != 0 || blocked.RawDeferred < 1 {
		t.Fatalf("unaggregated raw telemetry was not protected: %+v", blocked)
	}

	var seriesID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM metric_series WHERE device_id = $1 AND metric = 'cpu.utilization'`, device.ID).Scan(&seriesID); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := db.QueryRowContext(ctx, `SELECT storage_generation FROM monitoring_storage_state WHERE singleton = true`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	fiveMinute := oldObservedAt.Truncate(5 * time.Minute)
	hourly := oldObservedAt.Truncate(time.Hour)
	for _, aggregate := range []struct {
		resolution int
		bucket     time.Time
		seconds    int
	}{
		{resolution: store.RollupResolutionFiveMinute, bucket: fiveMinute, seconds: 300},
		{resolution: store.RollupResolutionHourly, bucket: hourly, seconds: 3600},
	} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO metric_aggregates(series_id, resolution_seconds, bucket_start, count, sum, min, max, expected_count, covered_seconds, bucket_seconds, partial, generation)
			VALUES ($1,$2,$3,1,$4,$4,$4,1,60,$5,false,$6)`, seriesID, aggregate.resolution, aggregate.bucket, value, aggregate.seconds, generation); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM rollup_work WHERE series_id = $1`, seriesID); err != nil {
		t.Fatal(err)
	}

	completed, err := repository.RetainTelemetry(ctx, proposed, now)
	if err != nil {
		t.Fatal(err)
	}
	if completed.RawDeleted != 1 || completed.RawDeferred != blocked.RawDeferred-1 {
		t.Fatalf("eligible raw telemetry was not removed after rollups completed: %+v", completed)
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples WHERE device_id = $1`, device.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("retention left %d raw rows after completed rollups", remaining)
	}
	status, err := repository.MonitoringStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastSuccessfulJobs["retention"] == nil {
		t.Fatalf("retention status was not recorded: %+v", status.LastSuccessfulJobs)
	}
}
