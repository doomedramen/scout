#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" ]]; then
	cat <<'EOF'
Usage: SCOUT_BACKUP_FILE=... SCOUT_RESTORE_DATABASE_URL=... SCOUT_SECRET_KEY_FILE=... SCOUT_RESTORE_CONFIRM=YES scripts/restore.sh

Restores into a separate PostgreSQL destination in one transaction, verifies
the schema, and leaves restored authority in recovery mode. It never drops or
rewrites SCOUT_DATABASE_URL.
EOF
	exit 0
fi
if [[ $# -eq 0 ]]; then
	echo "SCOUT_BACKUP_FILE, SCOUT_RESTORE_DATABASE_URL, SCOUT_SECRET_KEY_FILE, and SCOUT_RESTORE_CONFIRM are required" >&2
	exit 2
fi

: "${SCOUT_BACKUP_FILE:?SCOUT_BACKUP_FILE is required}"
: "${SCOUT_RESTORE_DATABASE_URL:?SCOUT_RESTORE_DATABASE_URL is required}"
: "${SCOUT_SECRET_KEY_FILE:?SCOUT_SECRET_KEY_FILE is required}"
: "${SCOUT_RESTORE_CONFIRM:?Set SCOUT_RESTORE_CONFIRM=YES to restore}"
[[ "$SCOUT_RESTORE_CONFIRM" == "YES" ]] || { echo "restore confirmation must be YES" >&2; exit 2; }

command -v pg_restore >/dev/null 2>&1 || { echo "pg_restore is required" >&2; exit 2; }
command -v psql >/dev/null 2>&1 || { echo "psql is required" >&2; exit 2; }
[[ -f "$SCOUT_BACKUP_FILE" ]] || { echo "backup file is unavailable" >&2; exit 2; }
[[ -f "$SCOUT_BACKUP_FILE.json" ]] || { echo "backup metadata is unavailable" >&2; exit 2; }
[[ -f "$SCOUT_SECRET_KEY_FILE" ]] || { echo "secret key file is unavailable" >&2; exit 2; }
[[ "$(wc -c <"$SCOUT_SECRET_KEY_FILE")" -eq 32 ]] || { echo "secret key file must contain 32 bytes" >&2; exit 2; }

if [[ -n "${SCOUT_DATABASE_URL:-}" && "$SCOUT_RESTORE_DATABASE_URL" == "$SCOUT_DATABASE_URL" ]]; then
	echo "restore destination must be separate from SCOUT_DATABASE_URL" >&2
	exit 2
fi

key_fingerprint=$(sha256sum "$SCOUT_SECRET_KEY_FILE" 2>/dev/null | cut -c1-16 || shasum -a 256 "$SCOUT_SECRET_KEY_FILE" | cut -c1-16)
expected_fingerprint=$(sed -n 's/.*"keyFingerprint":"\([^"]*\)".*/\1/p' "$SCOUT_BACKUP_FILE.json")
[[ -n "$expected_fingerprint" && "$expected_fingerprint" == "$key_fingerprint" ]] || { echo "restore key does not match backup metadata" >&2; exit 2; }

expected_table_count=$(sed -n 's/.*"monitoringTableCount":\([0-9][0-9]*\).*/\1/p' "$SCOUT_BACKUP_FILE.json")
expected_monitoring_state=$(sed -n 's/.*"monitoringState":"\([^"]*\)".*/\1/p' "$SCOUT_BACKUP_FILE.json")
expected_checkpoint_count=$(sed -n 's/.*"migrationCheckpointCount":\([0-9][0-9]*\).*/\1/p' "$SCOUT_BACKUP_FILE.json")
expected_settings_revision=$(sed -n 's/.*"monitoringSettingsRevision":\([0-9][0-9]*\).*/\1/p' "$SCOUT_BACKUP_FILE.json")
[[ "$expected_table_count" =~ ^[0-9]+$ && "$expected_monitoring_state" =~ ^[0-9]+:[0-9]+:(legacy|importing|authoritative)$ && "$expected_checkpoint_count" =~ ^[0-9]+$ && "$expected_settings_revision" =~ ^[0-9]+$ ]] || { echo "backup metadata lacks 002 monitoring coverage" >&2; exit 2; }

pg_restore --clean --if-exists --no-owner --single-transaction --dbname="$SCOUT_RESTORE_DATABASE_URL" "$SCOUT_BACKUP_FILE"
psql "$SCOUT_RESTORE_DATABASE_URL" --set=ON_ERROR_STOP=1 --command "SELECT 1 FROM scout_schema_migrations LIMIT 1" >/dev/null
restored_table_count=$(psql "$SCOUT_RESTORE_DATABASE_URL" --tuples-only --no-align --command "SELECT COUNT(*) FROM pg_catalog.pg_tables WHERE schemaname='public' AND tablename IN ('monitoring_storage_state','monitoring_migration_checkpoints','metric_series','metric_samples','telemetry_receipts','telemetry_sample_ordinals','current_series','metric_aggregates','rollup_work','alert_rules','alert_overrides','alert_evaluations','incidents','incident_transitions','alert_work','notification_destinations','notification_deliveries','suppression_windows','suppression_episodes','monitoring_settings','retention_previews')" | tr -d '[:space:]')
restored_monitoring_state=$(psql "$SCOUT_RESTORE_DATABASE_URL" --tuples-only --no-align --command "SELECT storage_generation || ':' || migration_generation || ':' || phase FROM monitoring_storage_state WHERE singleton=true" | tr -d '[:space:]')
restored_checkpoint_count=$(psql "$SCOUT_RESTORE_DATABASE_URL" --tuples-only --no-align --command "SELECT COUNT(*) FROM monitoring_migration_checkpoints WHERE migration_generation=(SELECT migration_generation FROM monitoring_storage_state WHERE singleton=true)" | tr -d '[:space:]')
restored_settings_revision=$(psql "$SCOUT_RESTORE_DATABASE_URL" --tuples-only --no-align --command "SELECT revision FROM monitoring_settings WHERE singleton=true" | tr -d '[:space:]')
[[ "$restored_table_count" == "$expected_table_count" && "$restored_monitoring_state" == "$expected_monitoring_state" && "$restored_checkpoint_count" == "$expected_checkpoint_count" && "$restored_settings_revision" == "$expected_settings_revision" ]] || { echo "restored 002 monitoring coverage does not match backup metadata" >&2; exit 2; }
psql "$SCOUT_RESTORE_DATABASE_URL" --set=ON_ERROR_STOP=1 <<'SQL'
BEGIN;
UPDATE notification_deliveries
SET status='cancelled', next_attempt_at=NULL, lease_owner='', lease_until=NULL,
    safe_error='notification delivery cancelled during recovery', updated_at=now()
WHERE status IN ('queued', 'retry', 'sending');
UPDATE alert_evaluations
SET evidence_state='unknown', last_observed_at=NULL, last_received_at=NULL,
    pending_since=NULL, recovery_since=NULL, last_valid_at=NULL,
    trigger_consecutive=0, recovery_consecutive=0, updated_at=now();
UPDATE alert_work
SET dirty_generation=GREATEST(dirty_generation + 1, 1), lease_epoch=lease_epoch + 1,
    lease_owner='', lease_until=NULL, attempts=0, last_error='', updated_at=now();
INSERT INTO alert_work (lineage_id, entity_id, dirty_generation, lease_epoch,
    lease_owner, lease_until, attempts, last_error, created_at, updated_at)
SELECT lineage_id, entity_id, 1, 0, '', NULL, 0, '', now(), now()
FROM alert_evaluations
ON CONFLICT (lineage_id, entity_id) DO NOTHING;
UPDATE suppression_episodes
SET ended_at=now(), summary_batch_id=''
WHERE ended_at IS NULL;
UPDATE monitoring_settings
SET notifications_paused=true, revision=revision + 1, updated_at=now()
WHERE singleton=true;
UPDATE workspace_state
SET recovery_mode=true, enrollment_paused=true, updates_paused=true,
    state_json=jsonb_set(
      jsonb_set(
        jsonb_set(COALESCE(state_json, '{}'::jsonb), '{workspace,recoveryMode}', 'true'::jsonb, true),
        '{workspace,enrollmentPaused}', 'true'::jsonb, true),
      '{workspace,updatesPaused}', 'true'::jsonb, true),
    updated_at=now()
WHERE singleton=true;
COMMIT;
SQL
echo "Scout restore verified; restored authority is paused in recovery mode"
