package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

var monitoringRecoveryTables = []string{
	"monitoring_storage_state", "monitoring_migration_checkpoints", "metric_series", "metric_samples",
	"telemetry_receipts", "telemetry_sample_ordinals", "current_series", "metric_aggregates", "rollup_work",
	"alert_rules", "alert_overrides", "alert_evaluations", "incidents", "incident_transitions", "alert_work",
	"notification_destinations", "notification_deliveries", "suppression_windows", "suppression_episodes",
	"monitoring_settings", "retention_previews",
}

func TestMonitoringRecoveryPreservesDurableStateAndPreventsNoReplay(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	for _, table := range monitoringRecoveryTables {
		var present bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_tables WHERE schemaname='public' AND tablename=$1)`, table).Scan(&present); err != nil {
			t.Fatalf("check monitoring table %s: %v", table, err)
		}
		if !present {
			t.Fatalf("002 monitoring table %s is missing from the SQL schema", table)
		}
	}

	suffix := store.NewID()
	siteID := "recovery-site-" + suffix
	deviceID := "recovery-device-" + suffix
	agentID := "recovery-agent-" + suffix
	seriesID := "recovery-series-" + suffix
	sampleID := "recovery-sample-" + suffix
	destinationID := "recovery-destination-" + suffix
	ruleID := "recovery-rule-" + suffix
	overrideID := "recovery-override-" + suffix
	incidentID := "recovery-incident-" + suffix
	transitionID := "recovery-transition-" + suffix
	windowID := "recovery-window-" + suffix
	previewID := "recovery-preview-" + suffix
	checkpointStream := "samples"
	now := time.Now().UTC().Truncate(time.Microsecond)

	workspaceJSON := snapshotJSON(t, ctx, db, `SELECT row_to_json(workspace_state) FROM workspace_state WHERE singleton=true`)
	settingsJSON := snapshotJSON(t, ctx, db, `SELECT row_to_json(monitoring_settings) FROM monitoring_settings WHERE singleton=true`)
	var migrationGeneration int64
	if err := db.QueryRowContext(ctx, `SELECT migration_generation FROM monitoring_storage_state WHERE singleton=true`).Scan(&migrationGeneration); err != nil {
		t.Fatal(err)
	}
	var checkpointJSON []byte
	err := db.QueryRowContext(ctx, `SELECT row_to_json(monitoring_migration_checkpoints) FROM monitoring_migration_checkpoints WHERE migration_generation=$1 AND stream=$2`, migrationGeneration, checkpointStream).Scan(&checkpointJSON)
	if errors.Is(err, sql.ErrNoRows) {
		checkpointJSON = nil
	} else if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM retention_previews WHERE id=$1`, previewID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM suppression_episodes WHERE destination_id=$1`, destinationID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM suppression_windows WHERE id=$1`, windowID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM notification_deliveries WHERE destination_id=$1`, destinationID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM notification_destinations WHERE id=$1`, destinationID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM incident_transitions WHERE incident_id=$1`, incidentID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM incidents WHERE id=$1`, incidentID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM alert_work WHERE lineage_id=$1`, ruleID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM alert_evaluations WHERE lineage_id=$1`, ruleID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM alert_overrides WHERE id=$1`, overrideID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM alert_rules WHERE id=$1`, ruleID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM metric_aggregates WHERE series_id=$1`, seriesID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM rollup_work WHERE series_id=$1`, seriesID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM current_series WHERE series_id=$1`, seriesID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM telemetry_sample_ordinals WHERE agent_id=$1`, agentID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM telemetry_receipts WHERE agent_id=$1`, agentID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM metric_samples WHERE id=$1`, sampleID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM metric_series WHERE id=$1`, seriesID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM credential_refs WHERE id=$1`, "recovery-credential-"+suffix)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM agent_identities WHERE id=$1`, agentID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM devices WHERE id=$1`, deviceID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM sites WHERE id=$1`, siteID)
		if checkpointJSON == nil {
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM monitoring_migration_checkpoints WHERE migration_generation=$1 AND stream=$2`, migrationGeneration, checkpointStream)
		} else {
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM monitoring_migration_checkpoints WHERE migration_generation=$1 AND stream=$2`, migrationGeneration, checkpointStream)
			_, _ = db.ExecContext(cleanupCtx, `INSERT INTO monitoring_migration_checkpoints SELECT * FROM json_populate_record(NULL::monitoring_migration_checkpoints, $1::json)`, checkpointJSON)
		}
		_, _ = db.ExecContext(cleanupCtx, `UPDATE workspace_state AS current SET recovery_mode=restored.recovery_mode, enrollment_paused=restored.enrollment_paused, updates_paused=restored.updates_paused, schema_version=restored.schema_version, state_json=restored.state_json, updated_at=restored.updated_at FROM json_populate_record(NULL::workspace_state, $1::json) AS restored WHERE current.singleton=true`, workspaceJSON)
		_, _ = db.ExecContext(cleanupCtx, `UPDATE monitoring_settings AS current SET revision=restored.revision, defaults_version=restored.defaults_version, notifications_paused=restored.notifications_paused, raw_days=restored.raw_days, five_minute_days=restored.five_minute_days, hourly_days=restored.hourly_days, disk_budget_bytes=restored.disk_budget_bytes, migration_generation=restored.migration_generation, dropped_count=restored.dropped_count, truncated_count=restored.truncated_count, backpressure_count=restored.backpressure_count, active_admission_failures=restored.active_admission_failures, last_evaluation_at=restored.last_evaluation_at, last_rollup_at=restored.last_rollup_at, last_retention_at=restored.last_retention_at, updated_at=restored.updated_at FROM json_populate_record(NULL::monitoring_settings, $1::json) AS restored WHERE current.singleton=true`, settingsJSON)
	})

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sites(id, name, address_context, created_at) VALUES ($1, $2, $3, $4)`, siteID, "recovery fixture", "fixture", now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO devices(id, site_id, display_name, platform, architecture, lifecycle, created_at) VALUES ($1, $2, $3, 'linux', 'amd64', 'enrolled', $4)`, deviceID, siteID, "recovery hardware", now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_identities(id, device_id, public_key_hash, cert_serial, certificate_pem, expires_at, installed_version) VALUES ($1, $2, 'recovery-public-key', $3, 'recovery-certificate', $4, 'fixture')`, agentID, deviceID, "recovery-cert-"+suffix, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO credential_refs(id, kind, endpoint, allowed_use, targets, ciphertext, nonce, wrapped_data_key, key_version, metadata, revision) VALUES ($1, 'ssh', 'ssh://fixture', '["enrollment"]', $2, '\x01', '\x02', '\x03', 1, '{"source":"fixture"}', 2)`, "recovery-credential-"+suffix, fmt.Sprintf(`["%s"]`, deviceID)); err != nil {
		t.Fatal(err)
	}
	value := 42.5
	if _, err := tx.ExecContext(ctx, `INSERT INTO metric_series(id, device_id, collector_id, entity_id, metric, unit, labels, interval_seconds, first_seen, last_seen, storage_generation) VALUES ($1, $2, 'sensors', 'sensor-0', 'sensor.temperature', 'celsius', '{"identityStable":"true"}', 30, $3, $3, 0)`, seriesID, deviceID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO metric_samples(id, device_id, agent_id, collector_id, entity_id, metric, labels, value, availability, unit, observed_at, received_at, series_id, boot_id, batch_id, batch_ordinal, storage_generation, interval_seconds) VALUES ($1, $2, $3, 'sensors', 'sensor-0', 'sensor.temperature', '{"identityStable":"true"}', $4, 'current', 'celsius', $5, $5, $6, 'boot-recovery', 'batch-recovery', 0, 0, 30)`, sampleID, deviceID, agentID, value, now, seriesID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO telemetry_receipts(agent_id, boot_id, batch_id, payload_hash, accepted_at, storage_generation) VALUES ($1, 'boot-recovery', 'batch-recovery', 'hash-recovery', $2, 0)`, agentID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO telemetry_sample_ordinals(agent_id, boot_id, batch_id, batch_ordinal, sample_id, received_at) VALUES ($1, 'boot-recovery', 'batch-recovery', 0, $2, $3)`, agentID, sampleID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO current_series(series_id, sample_id, observed_at, received_at, value, availability, storage_generation) VALUES ($1, $2, $3, $3, $4, 'current', 0)`, seriesID, sampleID, now, value); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO metric_aggregates(series_id, resolution_seconds, bucket_start, count, sum, min, max, expected_count, covered_seconds, bucket_seconds, partial, generation) VALUES ($1, 300, $2, 1, $3, $3, $3, 1, 30, 300, false, 4)`, seriesID, now.Truncate(5*time.Minute), value); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO rollup_work(series_id, resolution_seconds, bucket_start, dirty_generation, lease_epoch, lease_owner, lease_until, attempts, last_error) VALUES ($1, 300, $2, 4, 2, 'old-rollup-worker', $3, 1, 'fixture')`, seriesID, now.Truncate(5*time.Minute), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO alert_rules(id, name, kind, metric, entity_id, operator, trigger_value, clear_value, trigger_seconds, clear_seconds, minimum_consecutive_samples, severity, target_kind, target_id, enabled, revision, created_at, updated_at) VALUES ($1, 'Recovery temperature rule', 'numeric', 'sensor.temperature', 'sensor-0', 'gt', 90, 80, 300, 120, 1, 'warning', 'device', $2, true, 2, $3, $3)`, ruleID, deviceID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO alert_overrides(id, lineage_id, target_kind, target_id, kind, metric, entity_id, operator, trigger_value, clear_value, trigger_seconds, clear_seconds, minimum_consecutive_samples, severity, enabled, revision, created_at, updated_at) VALUES ($1, $2, 'device', $3, 'numeric', 'sensor.temperature', 'sensor-0', 'gt', 91, 81, 300, 120, 1, 'critical', true, 1, $4, $4)`, overrideID, ruleID, deviceID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO alert_evaluations(lineage_id, entity_id, effective_revision, evidence_state, last_observed_at, last_received_at, pending_since, recovery_since, last_valid_at, trigger_consecutive, recovery_consecutive, incident_id, updated_at) VALUES ($1, 'sensor-0', 2, 'fresh', $2, $2, $2, $2, $2, 3, 2, $3, $2)`, ruleID, now, incidentID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO incidents(id, lineage_id, entity_id, device_id, rule_revision, rule_snapshot, severity, status, evidence_state, value, unit, source, opened_at, observed_at, evaluated_at, revision) VALUES ($1, $2, 'sensor-0', $3, 2, '{"metric":"sensor.temperature"}', 'critical', 'active', 'fresh', 95, 'celsius', 'sensors', $4, $4, $4, 1)`, incidentID, ruleID, deviceID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO incident_transitions(id, incident_id, sequence, kind, evidence_state, reason, occurred_at, rule_revision) VALUES ($1, $2, 1, 'triggered', 'fresh', 'fixture', $3, 2)`, transitionID, incidentID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO alert_work(lineage_id, entity_id, dirty_generation, lease_epoch, lease_owner, lease_until, attempts, last_error, created_at, updated_at) VALUES ($1, 'sensor-0', 3, 4, 'old-alert-worker', $2, 3, 'fixture', $3, $3)`, ruleID, now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_destinations(id, name, base_url, masked_topic, has_token, allow_plain_http, enabled, revision, secret_ciphertext, secret_nonce, secret_wrapped_data_key, secret_key_version, created_at, updated_at) VALUES ($1, 'Recovery receiver', 'https://ntfy.example.test', 're********r', true, false, true, 1, '\x01', '\x02', '\x03', 1, $2, $2)`, destinationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_deliveries(id, destination_id, destination_revision, transition_id, status, attempts, expires_at, payload, created_at, updated_at) VALUES ($1, $2, 1, $3, 'queued', 0, $4, '{"message":"stale"}', $5, $5)`, "recovery-delivery-"+suffix, destinationID, transitionID, now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO suppression_windows(id, name, target_kind, target_id, mode, starts_at, ends_at, enabled, revision, created_at, updated_at) VALUES ($1, 'Recovery window', 'device', $2, 'oneTime', $3, $4, true, 1, $5, $5)`, windowID, deviceID, now.Add(-time.Minute), now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO suppression_episodes(destination_id, entity_id, device_id, epoch, started_at, reason_bits, summary_batch_id) VALUES ($1, 'sensor-0', $2, 1, $3, '["maintenance"]', 'stale-summary')`, destinationID, deviceID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO retention_previews(id, idempotency_key, expected_revision, raw_days, five_minute_days, hourly_days, estimated_rows, irreversible, expires_at, created_at) SELECT $1, $2, revision, raw_days, five_minute_days, hourly_days, 1, true, $3, $4 FROM monitoring_settings WHERE singleton=true`, previewID, "recovery-preview-"+suffix, now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO monitoring_migration_checkpoints(migration_generation, stream, next_index, completed, updated_at) VALUES ($1, $2, 7, false, $3) ON CONFLICT (migration_generation, stream) DO UPDATE SET next_index=EXCLUDED.next_index, completed=EXCLUDED.completed, updated_at=EXCLUDED.updated_at`, migrationGeneration, checkpointStream, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	repository := store.NewSQL(db)
	started, err := repository.StartRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !started.RecoveryMode || !started.EnrollmentPaused || !started.UpdatesPaused || !started.NotificationsPaused {
		t.Fatalf("recovery did not pause all authority: %+v", started)
	}

	var preserved int
	for _, check := range []struct {
		name  string
		query string
		args  []any
	}{
		{"metric series", `SELECT COUNT(*) FROM metric_series WHERE id=$1`, []any{seriesID}},
		{"metric sample", `SELECT COUNT(*) FROM metric_samples WHERE id=$1`, []any{sampleID}},
		{"telemetry receipt", `SELECT COUNT(*) FROM telemetry_receipts WHERE agent_id=$1`, []any{agentID}},
		{"sample ordinal", `SELECT COUNT(*) FROM telemetry_sample_ordinals WHERE agent_id=$1`, []any{agentID}},
		{"current series", `SELECT COUNT(*) FROM current_series WHERE series_id=$1`, []any{seriesID}},
		{"aggregate", `SELECT COUNT(*) FROM metric_aggregates WHERE series_id=$1`, []any{seriesID}},
		{"rollup work", `SELECT COUNT(*) FROM rollup_work WHERE series_id=$1`, []any{seriesID}},
		{"alert rule", `SELECT COUNT(*) FROM alert_rules WHERE id=$1`, []any{ruleID}},
		{"alert override", `SELECT COUNT(*) FROM alert_overrides WHERE id=$1`, []any{overrideID}},
		{"incident", `SELECT COUNT(*) FROM incidents WHERE id=$1`, []any{incidentID}},
		{"incident transition", `SELECT COUNT(*) FROM incident_transitions WHERE id=$1`, []any{transitionID}},
		{"destination", `SELECT COUNT(*) FROM notification_destinations WHERE id=$1`, []any{destinationID}},
		{"window", `SELECT COUNT(*) FROM suppression_windows WHERE id=$1`, []any{windowID}},
		{"preview", `SELECT COUNT(*) FROM retention_previews WHERE id=$1`, []any{previewID}},
	} {
		if err := db.QueryRowContext(ctx, check.query, check.args...).Scan(&preserved); err != nil {
			t.Fatalf("check %s: %v", check.name, err)
		}
		if preserved != 1 {
			t.Fatalf("%s was not preserved after recovery: %d", check.name, preserved)
		}
	}
	var checkpointNext int64
	var checkpointCompleted bool
	if err := db.QueryRowContext(ctx, `SELECT next_index, completed FROM monitoring_migration_checkpoints WHERE migration_generation=$1 AND stream=$2`, migrationGeneration, checkpointStream).Scan(&checkpointNext, &checkpointCompleted); err != nil {
		t.Fatal(err)
	}
	if checkpointNext != 7 || checkpointCompleted {
		t.Fatalf("migration checkpoint changed during recovery: next=%d completed=%t", checkpointNext, checkpointCompleted)
	}

	var deliveryStatus, deliveryError string
	if err := db.QueryRowContext(ctx, `SELECT status, safe_error FROM notification_deliveries WHERE destination_id=$1`, destinationID).Scan(&deliveryStatus, &deliveryError); err != nil {
		t.Fatal(err)
	}
	if deliveryStatus != store.NotificationDeliveryCancelled || deliveryError == "" {
		t.Fatalf("stale notification was not cancelled: status=%s error=%q", deliveryStatus, deliveryError)
	}
	claims, err := repository.ClaimNotificationDeliveries(ctx, "recovery-worker", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("recovery claimed %d stale notification deliveries", len(claims))
	}

	var evidenceState string
	var triggerCount, recoveryCount int
	if err := db.QueryRowContext(ctx, `SELECT evidence_state, trigger_consecutive, recovery_consecutive FROM alert_evaluations WHERE lineage_id=$1`, ruleID).Scan(&evidenceState, &triggerCount, &recoveryCount); err != nil {
		t.Fatal(err)
	}
	if evidenceState != "unknown" || triggerCount != 0 || recoveryCount != 0 {
		t.Fatalf("alert evaluation was not reset for fresh post-restore evidence: state=%s trigger=%d recovery=%d", evidenceState, triggerCount, recoveryCount)
	}
	var episodeEnded bool
	var summaryBatchID string
	if err := db.QueryRowContext(ctx, `SELECT ended_at IS NOT NULL, summary_batch_id FROM suppression_episodes WHERE destination_id=$1`, destinationID).Scan(&episodeEnded, &summaryBatchID); err != nil {
		t.Fatal(err)
	}
	if episodeEnded || summaryBatchID != "stale-summary" {
		t.Fatalf("open suppression episode was not preserved during live recovery: ended=%t summary=%q", episodeEnded, summaryBatchID)
	}
}

func snapshotJSON(t *testing.T, ctx context.Context, db *sql.DB, query string) []byte {
	t.Helper()
	var raw []byte
	if err := db.QueryRowContext(ctx, query).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("snapshot is not valid JSON: %s", raw)
	}
	return raw
}
