#!/usr/bin/env bash
set -euo pipefail

bash -n ct/scout.sh
bash -n install/scout-install.sh

python3 -m json.tool json/scout.json >/dev/null

mock_helper=$(mktemp)
trap 'rm -f "$mock_helper"' EXIT
printf '%s\n' \
  '[[ "${var_os:-}" == "debian" ]] || exit 10' \
  'printf "dispatcher-ok\\n"' >"$mock_helper"
dispatcher_output=$(var_os=debian SCOUT_HELPER_URL="file://$mock_helper" bash install/scout-install.sh)
[[ "$dispatcher_output" == "dispatcher-ok" ]]

grep -q 'function update_script()' ct/scout.sh
grep -q '_CS_DEFAULT_URL="https://raw.githubusercontent.com/doomedramen/scout/main"' ct/scout.sh
grep -q 'docker compose pull' ct/scout.sh
grep -q 'docker compose up -d --remove-orphans --wait' ct/scout.sh
grep -q 'https://raw.githubusercontent.com/doomedramen/scout/main/ct/scout.sh' install/scout-install.sh
grep -q 'FUNCTIONS_FILE_PATH:-' install/scout-install.sh
grep -q '"updateable": true' json/scout.json
grep -q '"script": "ct/scout.sh"' json/scout.json

echo "Proxmox helper contract passed. Live Proxmox VE and LXC installation remain unverified."
