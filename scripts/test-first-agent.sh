#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

go test ./tests/integration -run TestRuntimeEnrollmentPersistenceAndReporting -count=1

if [[ "${SCOUT_SYSTEMD_LAB:-0}" == "1" ]]; then
	if [[ "$(uname -s)" != "Linux" ]] || ! command -v systemctl >/dev/null 2>&1; then
		echo "SCOUT_SYSTEMD_LAB=1 requires a Linux host with systemd" >&2
		exit 2
	fi
	echo "The native systemd acceptance run is intentionally opt-in and must be executed against an owner-authorized disposable Linux host." >&2
	cat <<'EOF'
Provide the lab runner with a built Linux agent, a private invitation file, and
an HTTPS control endpoint. This repository test proves the same enrollment,
restart, heartbeat, freshness, and revocation boundaries without contacting a
real device.
EOF
fi
