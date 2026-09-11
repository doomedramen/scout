package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestMonitoringMigrationImportsLegacyTelemetryAndCutsOver(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	repository := store.NewSQL(db)
	backupData, err := repository.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := store.ParseBackup(backupData)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	deviceID := store.NewID()
	agentID := store.NewID()
	sampleID := "legacy-sample-001"
	observationID := "legacy-observation-001"
	value := 91.5
	backup.State.Devices[deviceID] = store.Device{
		ID: deviceID, DisplayName: "migration-target", Platform: "linux", Architecture: "amd64",
		Lifecycle: "enrolled", CreatedAt: now,
	}
	backup.State.Agents[agentID] = store.AgentIdentity{
		ID: agentID, DeviceID: deviceID, PublicKeyHash: "pubhash", CertSerial: "cert-001",
		CertificatePEM: "fixture-certificate", ExpiresAt: now.Add(time.Hour), InstalledVersion: "fixture",
	}
	receiptKey, err := json.Marshal([]string{agentID, "boot-001", "batch-001"})
	if err != nil {
		t.Fatal(err)
	}
	backup.State.BatchReceipts[string(receiptKey)] = "payload-hash-001"
	backup.State.Samples = append(backup.State.Samples, store.MetricSample{
		ID: sampleID, DeviceID: deviceID, AgentID: agentID, CollectorID: "host", EntityID: "host",
		Metric: "memory.used_percent", Labels: map[string]string{"scope": "host"}, Value: &value,
		Availability: store.FreshnessCurrent, Unit: "percent", ObservedAt: now, ReceivedAt: now.Add(time.Second),
	})
	backup.State.Observations[observationID] = store.Observation{
		ID: observationID, ReporterID: agentID, CollectorID: "host", SubjectID: deviceID, Kind: "fixture",
		Payload: map[string]any{"safe": true}, ObservedAt: now, ReceivedAt: now.Add(time.Second),
		ExpiresAt: now.Add(time.Hour), Confidence: 1,
	}
	legacyData, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Restore(ctx, legacyData); err != nil {
		t.Fatal(err)
	}

	initial, err := repository.MonitoringStorage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Phase != store.MonitoringStorageLegacy {
		t.Fatalf("unexpected initial phase: %+v", initial)
	}
	started, err := repository.BeginMonitoringMigration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if started.Phase != store.MonitoringStorageImporting || started.MigrationGeneration != 1 {
		t.Fatalf("unexpected started migration: %+v", started)
	}

	parity, err := repository.ImportLegacyTelemetry(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !parity.Complete || parity.LegacySamples != 1 || parity.NormalizedSamples != 1 || parity.LegacyReceipts != 1 || parity.NormalizedReceipts != 1 || parity.LegacyObservations != 1 || parity.NormalizedObservations != 1 {
		t.Fatalf("unexpected migration parity: %+v", parity)
	}

	cutover, err := repository.CutoverMonitoringStorage(ctx, started.MigrationGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if cutover.Phase != store.MonitoringStorageAuthoritative || cutover.StorageGeneration != started.MigrationGeneration {
		t.Fatalf("unexpected cutover state: %+v", cutover)
	}

	var sampleCount, receiptCount, seriesCount, currentCount, observationCount int
	for _, query := range []struct {
		name  string
		query string
		value *int
	}{
		{name: "samples", query: `SELECT COUNT(*) FROM metric_samples WHERE storage_generation = 1`, value: &sampleCount},
		{name: "receipts", query: `SELECT COUNT(*) FROM telemetry_receipts WHERE storage_generation = 1`, value: &receiptCount},
		{name: "series", query: `SELECT COUNT(*) FROM metric_series WHERE storage_generation = 1`, value: &seriesCount},
		{name: "current", query: `SELECT COUNT(*) FROM current_series WHERE storage_generation = 1`, value: &currentCount},
		{name: "observations", query: `SELECT COUNT(*) FROM observations WHERE storage_generation = 1`, value: &observationCount},
	} {
		if err := db.QueryRowContext(ctx, query.query).Scan(query.value); err != nil {
			t.Fatalf("count %s: %v", query.name, err)
		}
	}
	if sampleCount != 1 || receiptCount != 1 || seriesCount != 1 || currentCount != 1 || observationCount != 1 {
		t.Fatalf("unexpected normalized counts samples=%d receipts=%d series=%d current=%d observations=%d", sampleCount, receiptCount, seriesCount, currentCount, observationCount)
	}
	if _, err := repository.BeginMonitoringMigration(ctx); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("authoritative migration was restartable: %v", err)
	}
}
