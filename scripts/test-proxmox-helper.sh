#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

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
awk '/^cat .*\/opt\/scout\/compose\.yaml$/ { capture=1; next } capture && /^EOF$/ { exit } capture { print }' \
  install/scout-install.sh >"$test_dir/compose.install.yaml"
cmp "$test_dir/compose.install.yaml" "$repo_dir/compose.proxmox.yaml"
cp "$repo_dir/compose.quickstart.yaml" "$test_dir/scout/compose.yaml"
printf '%s\n' 'SCOUT_DB_PASSWORD=test-password' 'SCOUT_SETUP_TOKEN=test-token' >"$test_dir/scout/.env"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'if [[ "${SCOUT_FAIL_CONFIG:-0}" == "1" && "$*" == *" config" ]]; then exit 1; fi' \
  'if [[ "${SCOUT_FAIL_PULL:-0}" == "1" && "$*" == *" pull" ]]; then exit 1; fi' \
  'printf "%s\\n" "$*" >>"$SCOUT_TEST_LOG"' >"$test_dir/bin/docker"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'while (($#)); do' \
  '  if [[ "$1" == "--output" ]]; then cp "$SCOUT_TEST_COMPOSE_SOURCE" "$2"; exit 0; fi' \
  '  shift' \
  'done' \
  'exit 2' >"$test_dir/bin/curl"
chmod +x "$test_dir/update" "$test_dir/bin/docker" "$test_dir/bin/curl"
SCOUT_INSTALL_DIR="$test_dir/scout" SCOUT_TEST_LOG="$test_dir/docker.log" \
  SCOUT_TEST_COMPOSE_SOURCE="$repo_dir/compose.proxmox.yaml" PATH="$test_dir/bin:$PATH" \
  bash "$test_dir/update" >/dev/null
grep -q 'compose -f .* config' "$test_dir/docker.log"
grep -qx 'compose pull' "$test_dir/docker.log"
grep -qx 'compose up -d --remove-orphans --wait' "$test_dir/docker.log"
cmp "$test_dir/scout/compose.yaml" "$repo_dir/compose.proxmox.yaml"
cmp "$test_dir/scout/compose.yaml.previous" "$repo_dir/compose.quickstart.yaml"
grep -qx 'SCOUT_DB_PASSWORD=test-password' "$test_dir/scout/.env"
grep -qx 'SCOUT_SETUP_TOKEN=test-token' "$test_dir/scout/.env"

cp "$test_dir/scout/compose.yaml" "$test_dir/compose.before-config-failure.yaml"
if SCOUT_FAIL_CONFIG=1 SCOUT_INSTALL_DIR="$test_dir/scout" SCOUT_TEST_LOG="$test_dir/docker.log" \
  SCOUT_TEST_COMPOSE_SOURCE="$repo_dir/compose.proxmox.yaml" PATH="$test_dir/bin:$PATH" \
  bash "$test_dir/update" >/dev/null 2>&1; then
  echo "Compose validation failure unexpectedly succeeded" >&2
  exit 1
fi
cmp "$test_dir/scout/compose.yaml" "$test_dir/compose.before-config-failure.yaml"

cp "$test_dir/scout/compose.yaml" "$test_dir/compose.before-pull-failure.yaml"
if SCOUT_FAIL_PULL=1 SCOUT_INSTALL_DIR="$test_dir/scout" SCOUT_TEST_LOG="$test_dir/docker.log" \
  SCOUT_TEST_COMPOSE_SOURCE="$repo_dir/compose.proxmox.yaml" PATH="$test_dir/bin:$PATH" \
  bash "$test_dir/update" >/dev/null 2>&1; then
  echo "Compose pull failure unexpectedly succeeded" >&2
  exit 1
fi
cmp "$test_dir/scout/compose.yaml" "$test_dir/compose.before-pull-failure.yaml"

grep -q 'function update_script()' ct/scout.sh
grep -q '_CS_DEFAULT_URL="https://raw.githubusercontent.com/doomedramen/scout/main"' ct/scout.sh
grep -q 'compose.proxmox.yaml' ct/scout.sh
grep -q 'Downloaded Scout Compose file failed validation' ct/scout.sh
grep -q 'docker compose pull' ct/scout.sh
grep -q 'docker compose up -d --remove-orphans --wait' ct/scout.sh
grep -q "cat <<'EOF' >/usr/bin/update" install/scout-install.sh
grep -q 'compose.proxmox.yaml' install/scout-install.sh
grep -q 'downloaded Compose file failed validation' install/scout-install.sh
grep -q 'docker compose pull' install/scout-install.sh
grep -q 'docker compose up -d --remove-orphans --wait' install/scout-install.sh
grep -q '"updateable": true' json/scout.json
grep -q '"script": "ct/scout.sh"' json/scout.json

echo "Proxmox helper contract passed. Live Proxmox VE and LXC installation remain unverified."
