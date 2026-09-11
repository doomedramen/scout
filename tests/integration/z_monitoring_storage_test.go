package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func ensureAuthoritativeMonitoring(t *testing.T, ctx context.Context, repository *store.Store) store.MonitoringMigrationStatus {
	t.Helper()
	status, err := repository.MonitoringStorage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase == store.MonitoringStorageLegacy || status.Phase == store.MonitoringStorageImporting {
		started, beginErr := repository.BeginMonitoringMigration(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if _, importErr := repository.ImportLegacyTelemetry(ctx, 2); importErr != nil {
			t.Fatal(importErr)
		}
		status, err = repository.CutoverMonitoringStorage(ctx, started.MigrationGeneration)
		if err != nil {
			t.Fatal(err)
		}
	}
	if status.Phase != store.MonitoringStorageAuthoritative {
		t.Fatalf("monitoring storage did not become authoritative: %+v", status)
	}
	return status
}

func TestAuthoritativeTelemetryUsesSQLIdentityAndAtomicReplay(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	repository := store.NewSQL(db)
	status := ensureAuthoritativeMonitoring(t, ctx, repository)
	device, err := repository.CreateDevice(ctx, store.Device{ID: store.NewID(), DisplayName: "sql-telemetry-target", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	agent := store.AgentIdentity{ID: store.NewID(), DeviceID: device.ID, CertSerial: "sql-telemetry-" + store.NewID(), PublicKeyHash: "fixture-public-key", CertificatePEM: "fixture-certificate", ExpiresAt: time.Now().UTC().Add(time.Hour), InstalledVersion: "fixture"}
	if err := repository.CreateAgentIdentity(ctx, agent); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC)
	first := 10.0
	outOfOrder := 20.0
	secondEntity := 30.0
	observations := []store.Observation{{Kind: "fixture", SubjectID: device.ID, Payload: map[string]any{"source": "sql"}, ObservedAt: base, ReceivedAt: base}}
	samples := []store.MetricSample{
		{CollectorID: "host", EntityID: "disk-a", Metric: "disk.read_rate", Labels: map[string]string{"device": "a"}, Value: &first, Availability: store.FreshnessCurrent, Unit: "bytes_per_second", ObservedAt: base, ReceivedAt: base.Add(2 * time.Second)},
		{CollectorID: "host", EntityID: "disk-a", Metric: "disk.read_rate", Labels: map[string]string{"device": "a"}, Value: &outOfOrder, Availability: store.FreshnessCurrent, Unit: "bytes_per_second", ObservedAt: base.Add(-time.Minute), ReceivedAt: base.Add(3 * time.Second)},
		{CollectorID: "host", EntityID: "disk-b", Metric: "disk.read_rate", Labels: map[string]string{"device": "b"}, Value: &secondEntity, Availability: store.FreshnessCurrent, Unit: "bytes_per_second", ObservedAt: base.Add(time.Minute), ReceivedAt: base.Add(4 * time.Second)},
	}
	firstResult, err := repository.IngestBatch(ctx, agent.ID, "boot-sql", "batch-sql-1", "hash-sql-1", samples, observations, 2)
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.Duplicate || firstResult.AcceptedAt.IsZero() {
		t.Fatalf("unexpected first SQL ingestion result: %+v", firstResult)
	}
	duplicate, err := repository.IngestBatch(ctx, agent.ID, "boot-sql", "batch-sql-1", "hash-sql-1", samples, observations, 0)
	if err != nil || !duplicate.Duplicate || !duplicate.AcceptedAt.Equal(firstResult.AcceptedAt) {
		t.Fatalf("duplicate replay was not idempotent: result=%+v err=%v", duplicate, err)
	}
	if _, err := repository.IngestBatch(ctx, agent.ID, "boot-sql", "batch-sql-1", "hash-sql-conflict", samples, observations, 0); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("conflicting replay was accepted: %v", err)
	}
	var beforeRollback int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples WHERE device_id = $1`, device.ID).Scan(&beforeRollback); err != nil {
		t.Fatal(err)
	}
	invalidSamples := append([]store.MetricSample(nil), samples[:1]...)
	invalidSamples = append(invalidSamples, store.MetricSample{CollectorID: "host", EntityID: "broken", Unit: "count", Availability: store.FreshnessCurrent, ObservedAt: base})
	if _, err := repository.IngestBatch(ctx, agent.ID, "boot-sql", "batch-sql-invalid", "hash-sql-invalid", invalidSamples, nil, 0); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("invalid sample batch did not fail atomically: %v", err)
	}
	var afterRollback int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples WHERE device_id = $1`, device.ID).Scan(&afterRollback); err != nil {
		t.Fatal(err)
	}
	if afterRollback != beforeRollback {
		t.Fatalf("invalid batch left SQL rows behind: before=%d after=%d", beforeRollback, afterRollback)
	}

	series, err := repository.QueryMetrics(ctx, store.MetricQuery{DeviceID: device.ID, Metric: "disk.read_rate", MaxPoints: 600})
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 2 {
		t.Fatalf("full series identity was collapsed: %+v", series)
	}
	seriesByEntity := map[string]store.MetricSeries{}
	for _, item := range series {
		seriesByEntity[item.EntityID] = item
		if item.SeriesID == "" || len(item.Points) == 0 {
			t.Fatalf("SQL series metadata or points missing: %+v", item)
		}
	}
	if got := *seriesByEntity["disk-a"].Points[0].Value; got != outOfOrder || *seriesByEntity["disk-a"].Points[1].Value != first {
		t.Fatalf("out-of-order SQL history was not retained: %+v", seriesByEntity["disk-a"])
	}
	if got := *seriesByEntity["disk-b"].Points[0].Value; got != secondEntity {
		t.Fatalf("second entity history was not retained: %+v", seriesByEntity["disk-b"])
	}
	currentDevice, err := repository.GetDevice(ctx, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := currentDevice.CurrentMetrics["disk-a:disk.read_rate"].Value; got == nil || *got != first {
		t.Fatalf("device current projection did not use SQL current_series: %+v", currentDevice.CurrentMetrics)
	}

	for _, item := range series {
		var currentValue float64
		var currentAvailability string
		if err := db.QueryRowContext(ctx, `SELECT value, availability FROM current_series WHERE series_id = $1`, item.SeriesID).Scan(&currentValue, &currentAvailability); err != nil {
			t.Fatalf("read current series %s: %v", item.SeriesID, err)
		}
		want := secondEntity
		if item.EntityID == "disk-a" {
			want = first
		}
		if currentValue != want || currentAvailability != string(store.FreshnessCurrent) {
			t.Fatalf("current series %s picked %v/%s, want %v/current", item.EntityID, currentValue, currentAvailability, want)
		}
		var dirty int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rollup_work WHERE series_id = $1`, item.SeriesID).Scan(&dirty); err != nil {
			t.Fatalf("read dirty work %s: %v", item.SeriesID, err)
		}
		wantDirty := 2
		if item.EntityID == "disk-a" {
			wantDirty = 4
		}
		if dirty != wantDirty {
			t.Fatalf("expected %d bucket(s) of five-minute/hourly dirty work for %s, got %d", wantDirty, item.EntityID, dirty)
		}
	}
	var sampleCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples WHERE device_id = $1 AND storage_generation = $2`, device.ID, status.StorageGeneration).Scan(&sampleCount); err != nil {
		t.Fatal(err)
	}
	if sampleCount != 3 {
		t.Fatalf("unexpected SQL sample count: %d", sampleCount)
	}
	telemetryStatus, err := repository.TelemetryStatus(ctx)
	if err != nil || telemetryStatus.DroppedSamples < 2 {
		t.Fatalf("SQL telemetry status lost dropped count: %+v err=%v", telemetryStatus, err)
	}

	observationID := "sql-observation-001"
	if err := repository.StoreObservation(ctx, store.Observation{ID: observationID, ReporterID: agent.ID, CollectorID: "host", SubjectID: device.ID, Kind: "direct", Payload: map[string]any{"safe": true}, ObservedAt: base, ReceivedAt: base, ExpiresAt: base.Add(time.Hour), Confidence: 1}); err != nil {
		t.Fatal(err)
	}
	list, err := repository.ListObservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range list {
		if item.ID == observationID && item.Payload["safe"] == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("SQL observation was not readable: %+v", list)
	}

	backupData, err := repository.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := store.ParseBackup(backupData)
	if err != nil {
		t.Fatal(err)
	}
	if len(backup.State.Samples) != 0 || len(backup.State.BatchReceipts) != 0 || len(backup.State.Observations) != 0 {
		t.Fatalf("authoritative telemetry leaked back into legacy snapshot: samples=%d receipts=%d observations=%d", len(backup.State.Samples), len(backup.State.BatchReceipts), len(backup.State.Observations))
	}
	if _, err := repository.GetDevice(ctx, device.ID); err != nil {
		t.Fatalf("non-telemetry state was lost while stripping snapshot: %v", err)
	}
}
