#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

go test ./internal/collector/... ./internal/topology ./tests/integration -run 'Test(Adapter|Registry|Lease|Service|DiscoveryIntegration)' -count=1

if [[ "${SCOUT_COLLECTOR_LAB:-0}" == "1" ]]; then
	if [[ "${SCOUT_COLLECTOR_LAB_CONFIRM:-}" != "YES" ]]; then
		echo "Set SCOUT_COLLECTOR_LAB_CONFIRM=YES only for an owner-authorized disposable provider lab." >&2
		exit 2
	fi
	echo "Fixture collector suite passed. Live Docker/Proxmox endpoints were not contacted by this script."
	cat <<'EOF'
The live runner must record exact Docker Engine and Proxmox VE versions,
endpoint ACLs, token grants, host visibility, timeout behavior, and redaction
results. Do not place provider tokens in command arguments or repository files.
EOF
fi
