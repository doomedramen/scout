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

pg_restore --clean --if-exists --no-owner --single-transaction --dbname="$SCOUT_RESTORE_DATABASE_URL" "$SCOUT_BACKUP_FILE"
psql "$SCOUT_RESTORE_DATABASE_URL" --set=ON_ERROR_STOP=1 --command "SELECT 1 FROM scout_schema_migrations LIMIT 1" >/dev/null
psql "$SCOUT_RESTORE_DATABASE_URL" --set=ON_ERROR_STOP=1 --command "UPDATE workspace_state SET recovery_mode=true, enrollment_paused=true, updates_paused=true, updated_at=now() WHERE singleton=true" >/dev/null
echo "Scout restore verified; restored authority is paused in recovery mode"
