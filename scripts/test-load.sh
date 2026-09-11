#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

go test ./tests/integration -run TestSyntheticLoad100Devices24Hours -count=1 -v

if [[ "${SCOUT_LOAD_LAB:-0}" == "1" ]]; then
	if [[ "${SCOUT_LOAD_LAB_CONFIRM:-}" != "YES" ]]; then
		echo "Set SCOUT_LOAD_LAB_CONFIRM=YES only before running a separately provisioned load environment." >&2
		exit 2
	fi
	echo "The repository run is synthetic and does not claim a 24-hour production result."
	echo "A live run must measure 100 devices for 24 hours, queue limits, disk pressure, retention sizing, and read latency before changing defaults."
fi
