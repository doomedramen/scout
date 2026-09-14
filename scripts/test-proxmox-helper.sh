#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

bash -n "$repo_dir/ct/scout.sh"
bash -n "$repo_dir/install/scout-install.sh"
bash -n "$repo_dir/install/scout-update.sh"
sh -n "$repo_dir/packaging/host/scout"
jq empty "$repo_dir/json/scout.json"

grep -q '_CS_DEFAULT_URL="https://raw.githubusercontent.com/doomedramen/scout/main"' \
  "$repo_dir/ct/scout.sh"
grep -q 'var_os="${var_os:-debian}"' "$repo_dir/ct/scout.sh"
grep -q 'var_version="${var_version:-13}"' "$repo_dir/ct/scout.sh"
grep -q 'var_unprivileged="${var_unprivileged:-1}"' "$repo_dir/ct/scout.sh"
grep -q 'install/scout-install.sh' "$repo_dir/ct/scout.sh"
grep -q 'packaging/host/scout' "$repo_dir/install/scout-install.sh"
grep -q 'install/scout-update.sh' "$repo_dir/install/scout-install.sh"
grep -q 'docker.io docker-compose-v2' "$repo_dir/install/scout-install.sh"
grep -q '"updateable": true' "$repo_dir/json/scout.json"
grep -q '"script": "ct/scout.sh"' "$repo_dir/json/scout.json"

test_dir=$(mktemp -d)
cleanup() {
  status=$?
  rm -rf -- "$test_dir"
  exit "$status"
}
trap cleanup EXIT

mkdir -p "$test_dir/bin" "$test_dir/scout"
cp "$repo_dir/compose.yaml" "$test_dir/scout/compose.yaml"
printf '%s\n' \
  'SCOUT_PORT=18080' \
  "SCOUT_COMPOSE_URL=$test_dir/source-compose.yaml" \
  >"$test_dir/scout/.env"
cp "$repo_dir/compose.yaml" "$test_dir/source-compose.yaml"

printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'if [[ "$1" == "compose" && "$2" == "version" ]]; then exit 0; fi' \
  'if [[ "$1" == "image" && "$2" == "inspect" ]]; then' \
  '  if [[ "$*" == *"RepoDigests"* ]]; then' \
  '    printf "%s\\n" "ghcr.io/doomedramen/scout@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"' \
  '  else' \
  '    printf "%s\\n" "sha256:scout-test-image"' \
  '  fi' \
  '  exit 0' \
  'fi' \
  'if [[ "$1" == "compose" && "$*" == *" config --images"* ]]; then' \
  '  printf "%s\\n" "ghcr.io/doomedramen/scout:edge"' \
  '  exit 0' \
  'fi' \
  'if [[ "${SCOUT_FAIL_CONFIG:-0}" == "1" && "$1" == "compose" && "$*" == *" config"* ]]; then exit 1; fi' \
  'if [[ "${SCOUT_FAIL_PULL:-0}" == "1" && "$1" == "compose" && "$*" == *" pull scout"* ]]; then exit 1; fi' \
  'if [[ "$1" == "compose" && "$*" == *" run "* && "$*" == *" authority"* ]]; then printf "%s\\n" false; fi' \
  'printf "%s\\n" "$*" >>"$SCOUT_TEST_LOG"' \
  >"$test_dir/bin/docker"

printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'output=' \
  'while (($#)); do' \
  '  case "$1" in' \
  '    --output|-o) output=$2; shift 2 ;;' \
  '    *) shift ;;' \
  '  esac' \
  'done' \
  '[[ -n "$output" ]] || exit 2' \
  'cp "$SCOUT_TEST_COMPOSE_SOURCE" "$output"' \
  >"$test_dir/bin/curl"
chmod 0755 "$test_dir/bin/docker" "$test_dir/bin/curl"

SCOUT_DIR="$test_dir/scout" \
  SCOUT_TEST_LOG="$test_dir/docker.log" \
  SCOUT_TEST_COMPOSE_SOURCE="$test_dir/source-compose.yaml" \
  PATH="$test_dir/bin:$PATH" \
  sh "$repo_dir/packaging/host/scout" update

grep -q 'compose .* config' "$test_dir/docker.log"
grep -qx 'compose .* pull scout' "$test_dir/docker.log"
grep -q 'compose .* run .* snapshot' "$test_dir/docker.log"
grep -q 'compose .* run .* pause' "$test_dir/docker.log"
grep -q 'compose .* run .* set-authority false' "$test_dir/docker.log"
cmp "$test_dir/scout/compose.yaml" "$test_dir/source-compose.yaml"

cp "$test_dir/scout/compose.yaml" "$test_dir/before-config-failure.yaml"
if SCOUT_FAIL_CONFIG=1 \
  SCOUT_DIR="$test_dir/scout" \
  SCOUT_TEST_LOG="$test_dir/docker.log" \
  SCOUT_TEST_COMPOSE_SOURCE="$test_dir/source-compose.yaml" \
  PATH="$test_dir/bin:$PATH" \
  sh "$repo_dir/packaging/host/scout" update >/dev/null 2>&1; then
  printf '%s\n' 'Compose validation failure unexpectedly succeeded' >&2
  exit 1
fi
cmp "$test_dir/scout/compose.yaml" "$test_dir/before-config-failure.yaml"

cp "$test_dir/scout/compose.yaml" "$test_dir/before-pull-failure.yaml"
if SCOUT_FAIL_PULL=1 \
  SCOUT_DIR="$test_dir/scout" \
  SCOUT_TEST_LOG="$test_dir/docker.log" \
  SCOUT_TEST_COMPOSE_SOURCE="$test_dir/source-compose.yaml" \
  PATH="$test_dir/bin:$PATH" \
  sh "$repo_dir/packaging/host/scout" update >/dev/null 2>&1; then
  printf '%s\n' 'Compose pull failure unexpectedly succeeded' >&2
  exit 1
fi
cmp "$test_dir/scout/compose.yaml" "$test_dir/before-pull-failure.yaml"

printf '%s\n' 'Proxmox helper contract passed. Live Proxmox VE and LXC installation remain unverified.'
