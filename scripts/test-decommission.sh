#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

go test ./internal/store ./internal/enrollment ./internal/telemetry ./tests/integration -run 'Test(Decommission|Reenable|SecurityMatrix|RuntimeEnrollment|MemoryQueue|Credential)' -count=1

if [[ "${SCOUT_DECOMMISSION_LAB:-0}" == "1" ]]; then
	if [[ "${SCOUT_DECOMMISSION_LAB_CONFIRM:-}" != "YES" ]]; then
		echo "Set SCOUT_DECOMMISSION_LAB_CONFIRM=YES only for an owner-authorized disposable agent." >&2
		exit 2
	fi
	echo "Fixture decommission suite passed. No agent was revoked or uninstalled outside the in-memory test store."
	echo "A live run must separately verify offline behavior, rediscovery exclusion, confirmed uninstall, retained history, and explicit re-enable."
fi
