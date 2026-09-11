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
{"format":"scout-postgres-custom","schemaVersion":${schema_version//[[:space:]]/},"keyFingerprint":"$key_fingerprint","createdAt":"$(date -u +%Y-%m-%dT%H:%M:%SZ)"}
EOF
chmod 0600 -- "$SCOUT_BACKUP_FILE" "$metadata_file"
echo "Scout backup created: $SCOUT_BACKUP_FILE"
