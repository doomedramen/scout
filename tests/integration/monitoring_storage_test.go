package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestMonitoringStorageFailureFixtures(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	repository := store.NewSQL(db)
	initialStatus, err := repository.MonitoringStorage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initialStatus.Phase != store.MonitoringStorageLegacy {
		t.Skipf("monitoring storage is already %s; fixture requires a fresh legacy database", initialStatus.Phase)
	}
	var existingDevices, existingSamples int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices`).Scan(&existingDevices); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples`).Scan(&existingSamples); err != nil {
		t.Fatal(err)
	}
	if existingDevices != 0 || existingSamples != 0 {
		t.Skipf("fixture requires empty SQL telemetry tables: devices=%d samples=%d", existingDevices, existingSamples)
	}
	initialBackup, err := repository.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deviceID := store.NewID()
	agentID := store.NewID()
	cleanupMonitoringFixture(t, db, repository, initialBackup, deviceID, agentID)

	backup, err := store.ParseBackup(initialBackup)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 11, 14, 0, 0, 0, time.UTC)
	backup.State.Devices = map[string]store.Device{deviceID: {
		ID: deviceID, DisplayName: "monitoring-storage-fixture", Platform: "linux", Architecture: "amd64", Lifecycle: "enrolled", CreatedAt: now,
	}}
	backup.State.Agents = map[string]store.AgentIdentity{agentID: {
		ID: agentID, DeviceID: deviceID, PublicKeyHash: "fixture-public-key", CertSerial: "fixture-" + agentID, CertificatePEM: "fixture-certificate", ExpiresAt: now.Add(time.Hour), InstalledVersion: "fixture",
	}}
	value := 42.0
	backup.State.Samples = []store.MetricSample{{
		ID: "monitoring-storage-sample", DeviceID: deviceID, AgentID: agentID, CollectorID: "host", EntityID: "host", Metric: "cpu.utilization", Unit: "percent", Value: &value, Availability: store.FreshnessCurrent, ObservedAt: now, ReceivedAt: now,
	}}
	receiptKey, err := json.Marshal([]string{agentID, "fixture-boot", "fixture-batch"})
	if err != nil {
		t.Fatal(err)
	}
	backup.State.BatchReceipts = map[string]string{string(receiptKey): "fixture-payload-hash"}
	backup.State.Observations = map[string]store.Observation{}
	backup.State.Workspace.MaxSamples = 0
	backup.State.Workspace.TelemetryBudgetBytes = store.DefaultTelemetryBudgetBytes
	backup.State.Workspace.TelemetryBackpressure = false
	legacyData, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Restore(ctx, legacyData); err != nil {
		t.Fatal(err)
	}

	started, err := repository.BeginMonitoringMigration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	installMigrationPauseTrigger(t, db)
	interruptedCtx, interrupt := context.WithCancel(ctx)
	go func() {
		// The trigger pauses the first normalized sample insert. This cancellation
		// lands inside a real migration transaction, not before it starts.
		time.Sleep(50 * time.Millisecond)
		interrupt()
	}()
	if _, err := repository.ImportLegacyTelemetry(interruptedCtx, 1); err == nil {
		t.Fatal("interrupted migration unexpectedly completed")
	}
	dropMigrationPauseTrigger(t, db, ctx)
	var nextIndex int64
	var completed bool
	if err := db.QueryRowContext(ctx, `SELECT next_index, completed FROM monitoring_migration_checkpoints WHERE migration_generation = $1 AND stream = 'samples'`, started.MigrationGeneration).Scan(&nextIndex, &completed); err != nil {
		t.Fatal(err)
	}
	if nextIndex != 0 || completed {
		t.Fatalf("interrupted migration advanced checkpoint: next=%d completed=%t", nextIndex, completed)
	}
	var normalizedAfterInterrupt int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples WHERE storage_generation = $1`, started.MigrationGeneration).Scan(&normalizedAfterInterrupt); err != nil {
		t.Fatal(err)
	}
	if normalizedAfterInterrupt != 0 {
		t.Fatalf("interrupted migration committed %d sample rows", normalizedAfterInterrupt)
	}
	if _, err := repository.ImportLegacyTelemetry(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CutoverMonitoringStorage(ctx, started.MigrationGeneration); err != nil {
		t.Fatal(err)
	}

	valid := store.MetricSample{CollectorID: "host", EntityID: "host", Metric: "memory.used_percent", Unit: "percent", Value: &value, Availability: store.FreshnessCurrent, ObservedAt: now}
	invalid := store.MetricSample{CollectorID: "host", EntityID: "broken", Unit: "percent", Value: &value, Availability: store.FreshnessCurrent, ObservedAt: now}
	var beforeRollback int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples WHERE device_id = $1`, deviceID).Scan(&beforeRollback); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.IngestBatch(ctx, agentID, "fixture-boot", "rollback-batch", "rollback-hash", []store.MetricSample{valid, invalid}, nil, 0); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("invalid SQL batch did not fail with validation error: %v", err)
	}
	var afterRollback int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples WHERE device_id = $1`, deviceID).Scan(&afterRollback); err != nil {
		t.Fatal(err)
	}
	if afterRollback != beforeRollback {
		t.Fatalf("invalid SQL batch left rows behind: before=%d after=%d", beforeRollback, afterRollback)
	}

	replaySample := valid
	replaySample.EntityID = "replay"
	firstReplay, err := repository.IngestBatch(ctx, agentID, "fixture-boot", "replay-batch", "replay-hash", []store.MetricSample{replaySample}, nil, 0)
	if err != nil || firstReplay.Duplicate {
		t.Fatalf("first replay fixture batch was not accepted: result=%+v err=%v", firstReplay, err)
	}
	duplicateReplay, err := repository.IngestBatch(ctx, agentID, "fixture-boot", "replay-batch", "replay-hash", []store.MetricSample{replaySample}, nil, 0)
	if err != nil || !duplicateReplay.Duplicate {
		t.Fatalf("duplicate replay was not idempotent: result=%+v err=%v", duplicateReplay, err)
	}
	if _, err := repository.IngestBatch(ctx, agentID, "fixture-boot", "replay-batch", "replay-hash-conflict", []store.MetricSample{replaySample}, nil, 0); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("conflicting replay was accepted: %v", err)
	}

	status, err := repository.TelemetryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.UsedBytes <= 0 || status.BudgetBytes != store.DefaultTelemetryBudgetBytes {
		t.Fatalf("SQL telemetry status omitted measured budget: %+v", status)
	}
	if _, err := repository.SetWorkspace(ctx, func(state *store.WorkspaceState) error {
		state.MaxSamples = 0
		state.TelemetryBudgetBytes = status.UsedBytes + 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.IngestBatch(ctx, agentID, "fixture-boot", "budget-batch", "budget-hash", []store.MetricSample{valid}, nil, 0); !errors.Is(err, store.ErrBackpressure) {
		t.Fatalf("SQL disk budget admitted batch: %v", err)
	}
	limited, err := repository.TelemetryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !limited.Backpressure || limited.Samples != status.Samples || limited.UsedBytes != status.UsedBytes {
		t.Fatalf("disk budget changed telemetry unexpectedly: before=%+v after=%+v", status, limited)
	}
	if _, err := repository.SetWorkspace(ctx, func(state *store.WorkspaceState) error {
		state.TelemetryBudgetBytes = store.DefaultTelemetryBudgetBytes
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetTelemetryBackpressure(ctx, false); err != nil {
		t.Fatal(err)
	}

	var group sync.WaitGroup
	errorsCh := make(chan error, 2)
	for index := 0; index < 2; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			item := valid
			suffix := fmt.Sprintf("%c", 'a'+index)
			item.EntityID = "concurrent-" + suffix
			_, ingestErr := repository.IngestBatch(ctx, agentID, "fixture-boot", "concurrent-batch-"+suffix, "concurrent-hash-"+suffix, []store.MetricSample{item}, nil, 0)
			errorsCh <- ingestErr
		}(index)
	}
	group.Wait()
	close(errorsCh)
	for ingestErr := range errorsCh {
		if ingestErr != nil {
			t.Fatalf("concurrent SQL writer failed: %v", ingestErr)
		}
	}
	var finalCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_samples WHERE device_id = $1`, deviceID).Scan(&finalCount); err != nil {
		t.Fatal(err)
	}
	if finalCount != beforeRollback+3 {
		t.Fatalf("concurrent SQL writers stored %d samples, want %d", finalCount, beforeRollback+3)
	}
}

func installMigrationPauseTrigger(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `
CREATE OR REPLACE FUNCTION scout_test_pause_migration() RETURNS trigger
LANGUAGE plpgsql AS $function$
BEGIN
  PERFORM pg_sleep(1);
  RETURN NEW;
END
$function$;
CREATE TRIGGER scout_test_pause_migration_trigger
BEFORE INSERT ON metric_samples
FOR EACH ROW EXECUTE FUNCTION scout_test_pause_migration()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dropMigrationPauseTrigger(t, db, context.Background())
	})
}

func dropMigrationPauseTrigger(t *testing.T, db *sql.DB, ctx context.Context) {
	t.Helper()
	_, _ = db.ExecContext(ctx, `DROP TRIGGER IF EXISTS scout_test_pause_migration_trigger ON metric_samples`)
	_, _ = db.ExecContext(ctx, `DROP FUNCTION IF EXISTS scout_test_pause_migration()`)
}

func cleanupMonitoringFixture(t *testing.T, db *sql.DB, repository *store.Store, initialBackup []byte, deviceID, agentID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = db.ExecContext(ctx, `DELETE FROM telemetry_sample_ordinals WHERE agent_id = $1`, agentID)
		_, _ = db.ExecContext(ctx, `DELETE FROM telemetry_receipts WHERE agent_id = $1`, agentID)
		_, _ = db.ExecContext(ctx, `DELETE FROM current_series WHERE series_id IN (SELECT id FROM metric_series WHERE device_id = $1)`, deviceID)
		_, _ = db.ExecContext(ctx, `DELETE FROM metric_samples WHERE device_id = $1`, deviceID)
		_, _ = db.ExecContext(ctx, `DELETE FROM observations WHERE reporter_id = $1`, agentID)
		_, _ = db.ExecContext(ctx, `DELETE FROM rollup_work WHERE series_id IN (SELECT id FROM metric_series WHERE device_id = $1)`, deviceID)
		_, _ = db.ExecContext(ctx, `DELETE FROM metric_series WHERE device_id = $1`, deviceID)
		_, _ = db.ExecContext(ctx, `DELETE FROM agent_identities WHERE id = $1`, agentID)
		_, _ = db.ExecContext(ctx, `DELETE FROM devices WHERE id = $1`, deviceID)
		_, _ = db.ExecContext(ctx, `DELETE FROM monitoring_migration_checkpoints`)
		_, _ = db.ExecContext(ctx, `UPDATE monitoring_storage_state SET storage_generation = 0, migration_generation = 0, phase = 'legacy', legacy_state_hash = '', legacy_sample_count = 0, normalized_sample_count = 0, legacy_receipt_count = 0, normalized_receipt_count = 0, legacy_observation_count = 0, normalized_observation_count = 0, parity_checked_at = NULL, cutover_at = NULL, updated_at = now() WHERE singleton = true`)
		_ = repository.Restore(ctx, initialBackup)
		if backup, parseErr := store.ParseBackup(initialBackup); parseErr == nil {
			_, _ = repository.SetWorkspace(ctx, func(state *store.WorkspaceState) error {
				*state = backup.State.Workspace
				return nil
			})
		}
	})
}
