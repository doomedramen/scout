#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

go test ./internal/updates ./tests/integration -run 'Test(Manifest|Installer|Download|SignedRelease|Rollout)' -count=1

if [[ "${SCOUT_UPDATES_LAB:-0}" == "1" ]]; then
	echo "Native updater interruption testing is opt-in and must run on a disposable Linux VM." >&2
	cat <<'EOF'
The fixture suite above verifies signatures, digest/platform/downgrade checks,
offline bundle import, range resume, journal recovery, readiness, and one
bounded rollback. A VM runner must additionally cut power or terminate the
process at each documented stage before recording live acceptance.
EOF
fi
