#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

go test ./tests/integration -run TestRuntimeEnrollmentPersistenceAndReporting -count=1
bash -n scripts/install-agent.sh
scripts/install-agent.sh --help >/dev/null
if grep -q 'github.com/doomedramen/scout/releases' scripts/install-agent.sh; then
	echo "agent installer still depends on public release assets" >&2
	exit 1
fi
if ! grep -q '/api/v1/bootstrap/agent/' scripts/install-agent.sh; then
	echo "agent installer does not use the Scout bootstrap endpoint" >&2
	exit 1
fi
if ! grep -q 'SCOUT_OTI' scripts/install-agent.sh; then
	echo "agent installer does not support the one-command invitation flow" >&2
	exit 1
fi
if ! grep -q '__SCOUT_SERVER_URL__' scripts/install-agent.sh; then
	echo "agent installer does not expose the server URL placeholder" >&2
	exit 1
fi
if grep -q 'run this installer with sudo or as root' scripts/install-agent.sh; then
	echo "agent installer still requires the caller to prepend sudo" >&2
	exit 1
fi
if grep -q 'scout.example.invalid\|/usr/local/bin/scout-agent' packaging/linux/agent.service; then
	echo "agent service still contains a placeholder installation path" >&2
	exit 1
fi
if ! grep -q 'EnvironmentFile=-/etc/scout/agent.env' packaging/linux/agent.service; then
	echo "agent service does not load the installer environment" >&2
	exit 1
fi

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
