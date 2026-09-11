#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

go test ./internal/policy ./internal/discovery ./internal/enrollment ./internal/store ./tests/integration -run 'Test(ExpandTargets|Probe|ReconcileSightings|DiscoveryIntegration|Candidates|Worker|Install|Uninstall|MemoryQueue)' -count=1

if [[ "${SCOUT_ENROLLMENT_LAB:-0}" == "1" ]]; then
	if [[ "${SCOUT_ENROLLMENT_LAB_CONFIRM:-}" != "YES" ]]; then
		echo "Set SCOUT_ENROLLMENT_LAB_CONFIRM=YES only after selecting an owner-authorized disposable lab." >&2
		exit 2
	fi
	echo "Fixture enrollment passed. A second-vantage lab still requires a separately reviewed runner; no target was contacted by this script."
	cat <<'EOF'
The live runner must provide two isolated vantage points, packet capture, a
private worker token, a disposable Linux target, and a signed test artifact.
Record excluded-target attempts, duplicate sightings, restart behavior, and
scope/credential revision fences before claiming live enrollment acceptance.
EOF
fi
