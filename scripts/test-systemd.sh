#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

go test ./internal/collector/systemd ./internal/alerts -run 'Test(Adapter|SystemdFailed|MustRun|Service|Resolve)' -race -count=1

if [[ "${SCOUT_SYSTEMD_LAB:-0}" != "1" ]]; then
	cat <<'EOF'
Fixture systemd and service-alert checks passed. No host system bus was contacted.
Set SCOUT_SYSTEMD_LAB=1 and SCOUT_SYSTEMD_LAB_CONFIRM=YES only for an owner-authorized,
disposable Linux check. The optional check is read-only and does not create units.
EOF
	exit 0
fi

if [[ "${SCOUT_SYSTEMD_LAB_CONFIRM:-}" != "YES" ]]; then
	echo "Set SCOUT_SYSTEMD_LAB_CONFIRM=YES only for an owner-authorized disposable Linux check." >&2
	exit 2
fi
if [[ "$(uname -s)" != "Linux" ]]; then
	echo "The systemd lab check requires Linux." >&2
	exit 2
fi
if ! command -v systemctl >/dev/null 2>&1; then
	echo "systemctl is not installed; no live systemd evidence was collected." >&2
	exit 2
fi

systemd_version=$(systemctl --version | sed -n '1p')
runtime_state=$(systemctl is-system-running 2>/dev/null || true)
loaded_services=$(systemctl list-units --type=service --all --no-legend --no-pager | awk 'NF { count++ } END { print count + 0 }')
printf 'Read-only systemd preflight: %s; manager state: %s; loaded service rows: %s\n' "$systemd_version" "${runtime_state:-unknown}" "$loaded_services"
cat <<'EOF'
This preflight does not claim service-fixture compatibility. Record the exact
Linux distribution, architecture, systemd version, permission result, and a
controlled active/failed/inactive/transitional fixture run before advertising
live support. Never use this script to start, stop, reload, or mutate a unit.
EOF
