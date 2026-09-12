#!/usr/bin/env bash
set -euo pipefail

bash -n ct/scout.sh
bash -n install/scout-install.sh

python3 -m json.tool json/scout.json >/dev/null

test_dir=$(mktemp -d)
cleanup() {
  status=$?
  rm -rf -- "$test_dir"
  exit "$status"
}
trap cleanup EXIT

mkdir -p "$test_dir/bin" "$test_dir/scout"
awk '/^cat .*\/usr\/bin\/update$/ { capture=1; next } capture && /^EOF$/ { exit } capture { print }' \
  install/scout-install.sh >"$test_dir/update"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'printf "%s\\n" "$*" >>"$SCOUT_TEST_LOG"' >"$test_dir/bin/docker"
chmod +x "$test_dir/update" "$test_dir/bin/docker"
SCOUT_INSTALL_DIR="$test_dir/scout" SCOUT_TEST_LOG="$test_dir/docker.log" PATH="$test_dir/bin:$PATH" \
  bash "$test_dir/update" >/dev/null
grep -qx 'compose pull' "$test_dir/docker.log"
grep -qx 'compose up -d --remove-orphans --wait' "$test_dir/docker.log"

grep -q 'function update_script()' ct/scout.sh
grep -q '_CS_DEFAULT_URL="https://raw.githubusercontent.com/doomedramen/scout/main"' ct/scout.sh
grep -q 'docker compose pull' ct/scout.sh
grep -q 'docker compose up -d --remove-orphans --wait' ct/scout.sh
grep -q "cat <<'EOF' >/usr/bin/update" install/scout-install.sh
grep -q 'docker compose pull' install/scout-install.sh
grep -q 'docker compose up -d --remove-orphans --wait' install/scout-install.sh
grep -q '"updateable": true' json/scout.json
grep -q '"script": "ct/scout.sh"' json/scout.json

echo "Proxmox helper contract passed. Live Proxmox VE and LXC installation remain unverified."
