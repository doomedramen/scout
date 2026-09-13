#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" ]]; then
	cat <<'EOF'
Usage: SCOUT_DATABASE_URL=... SCOUT_SECRET_KEY_FILE=... SCOUT_BACKUP_FILE=... scripts/backup.sh

Creates a PostgreSQL custom-format backup and a sidecar metadata file. The
wrapping key is required for restore but is never copied into the backup.
EOF
	exit 0
fi
if [[ $# -eq 0 ]]; then
	echo "SCOUT_DATABASE_URL, SCOUT_SECRET_KEY_FILE, and SCOUT_BACKUP_FILE are required" >&2
	exit 2
fi

: "${SCOUT_DATABASE_URL:?SCOUT_DATABASE_URL is required}"
: "${SCOUT_SECRET_KEY_FILE:?SCOUT_SECRET_KEY_FILE is required}"
: "${SCOUT_BACKUP_FILE:?SCOUT_BACKUP_FILE is required}"

command -v pg_dump >/dev/null 2>&1 || { echo "pg_dump is required" >&2; exit 2; }
command -v psql >/dev/null 2>&1 || { echo "psql is required" >&2; exit 2; }
[[ -f "$SCOUT_SECRET_KEY_FILE" ]] || { echo "secret key file is unavailable" >&2; exit 2; }
[[ "$(wc -c <"$SCOUT_SECRET_KEY_FILE")" -eq 32 ]] || { echo "secret key file must contain 32 bytes" >&2; exit 2; }

required_002_tables=(
	monitoring_storage_state monitoring_migration_checkpoints metric_series metric_samples
	telemetry_receipts telemetry_sample_ordinals current_series metric_aggregates rollup_work
	alert_rules alert_overrides alert_evaluations incidents incident_transitions alert_work
	notification_destinations notification_deliveries suppression_windows suppression_episodes
	monitoring_settings retention_previews
)
required_003_tables=(
	scan_policies scan_vantage_assignments scan_runs scan_run_leases scan_result_receipts
	scan_entry_point_observations scan_entry_point_current scan_candidate_extensions scan_access_request_keys
)
required_table_list=$(printf "'%s'," "${required_002_tables[@]}")
required_table_list=${required_table_list%,}
monitoring_table_count=$(psql "$SCOUT_DATABASE_URL" --tuples-only --no-align --command "SELECT COUNT(*) FROM pg_catalog.pg_tables WHERE schemaname='public' AND tablename IN (${required_table_list})" | tr -d '[:space:]')
if [[ ! "$monitoring_table_count" =~ ^[0-9]+$ || "$monitoring_table_count" -ne "${#required_002_tables[@]}" ]]; then
	echo "database is missing one or more 002 monitoring tables" >&2
	exit 2
fi
active_scanning_table_list=$(printf "'%s'," "${required_003_tables[@]}")
active_scanning_table_list=${active_scanning_table_list%,}
active_scanning_table_count=$(psql "$SCOUT_DATABASE_URL" --tuples-only --no-align --command "SELECT COUNT(*) FROM pg_catalog.pg_tables WHERE schemaname='public' AND tablename IN (${active_scanning_table_list})" | tr -d '[:space:]')
if [[ ! "$active_scanning_table_count" =~ ^[0-9]+$ || "$active_scanning_table_count" -ne "${#required_003_tables[@]}" ]]; then
	echo "database is missing one or more 003 active-scanning tables" >&2
	exit 2
fi
monitoring_state=$(psql "$SCOUT_DATABASE_URL" --tuples-only --no-align --command "SELECT storage_generation || ':' || migration_generation || ':' || phase FROM monitoring_storage_state WHERE singleton=true" | tr -d '[:space:]')
[[ "$monitoring_state" =~ ^[0-9]+:[0-9]+:(legacy|importing|authoritative)$ ]] || { echo "monitoring storage state is unavailable" >&2; exit 2; }
migration_checkpoint_count=$(psql "$SCOUT_DATABASE_URL" --tuples-only --no-align --command "SELECT COUNT(*) FROM monitoring_migration_checkpoints WHERE migration_generation=(SELECT migration_generation FROM monitoring_storage_state WHERE singleton=true)" | tr -d '[:space:]')
settings_revision=$(psql "$SCOUT_DATABASE_URL" --tuples-only --no-align --command "SELECT revision FROM monitoring_settings WHERE singleton=true" | tr -d '[:space:]')
[[ "$migration_checkpoint_count" =~ ^[0-9]+$ && "$settings_revision" =~ ^[0-9]+$ ]] || { echo "monitoring checkpoint/settings metadata is unavailable" >&2; exit 2; }

backup_dir=$(dirname -- "$SCOUT_BACKUP_FILE")
mkdir -p -- "$backup_dir"
temporary=$(mktemp "$SCOUT_BACKUP_FILE.tmp.XXXXXX")
metadata_file="$SCOUT_BACKUP_FILE.json"
cleanup() { rm -f -- "$temporary"; }
trap cleanup EXIT

pg_dump --format=custom --no-owner --file="$temporary" "$SCOUT_DATABASE_URL"
schema_version=$(psql "$SCOUT_DATABASE_URL" --tuples-only --no-align --command "SELECT COALESCE(MAX(version), 0) FROM scout_schema_migrations")
key_fingerprint=$(sha256sum "$SCOUT_SECRET_KEY_FILE" 2>/dev/null | cut -c1-16 || shasum -a 256 "$SCOUT_SECRET_KEY_FILE" | cut -c1-16)
mv -- "$temporary" "$SCOUT_BACKUP_FILE"
trap - EXIT

cat >"$metadata_file" <<EOF
{"format":"scout-postgres-custom","schemaVersion":${schema_version//[[:space:]]/},"keyFingerprint":"$key_fingerprint","createdAt":"$(date -u +%Y-%m-%dT%H:%M:%SZ)","monitoringTableCount":${#required_002_tables[@]},"activeScanningTableCount":${#required_003_tables[@]},"monitoringState":"$monitoring_state","migrationCheckpointCount":$migration_checkpoint_count,"monitoringSettingsRevision":$settings_revision}
EOF
chmod 0600 -- "$SCOUT_BACKUP_FILE" "$metadata_file"
echo "Scout backup created: $SCOUT_BACKUP_FILE"
