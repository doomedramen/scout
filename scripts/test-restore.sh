#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

go test ./internal/store ./internal/telemetry -run 'Test(BackupRestore|TelemetryBackpressure)' -count=1
scripts/backup.sh --help >/dev/null
scripts/restore.sh --help >/dev/null

if [[ "${SCOUT_RESTORE_LAB:-0}" == "1" ]]; then
	: "${SCOUT_BACKUP_FILE:?SCOUT_BACKUP_FILE is required for restore lab mode}"
	: "${SCOUT_RESTORE_DATABASE_URL:?SCOUT_RESTORE_DATABASE_URL is required for restore lab mode}"
	: "${SCOUT_SECRET_KEY_FILE:?SCOUT_SECRET_KEY_FILE is required for restore lab mode}"
	: "${SCOUT_RESTORE_CONFIRM:?Set SCOUT_RESTORE_CONFIRM=YES for restore lab mode}"
	scripts/restore.sh
else
	echo "fixture restore passed; live PostgreSQL restore is opt-in via SCOUT_RESTORE_LAB=1"
fi
