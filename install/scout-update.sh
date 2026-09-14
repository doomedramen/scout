#!/usr/bin/env bash
set -euo pipefail

install_dir="${SCOUT_INSTALL_DIR:-${SCOUT_DIR:-/opt/scout}}"
helper="/usr/local/bin/scout"

read_env_value() {
  local key="${1:?key}" value
  [[ -f "$install_dir/.env" ]] || return 0
  value="$(sed -n "s/^${key}=//p" "$install_dir/.env" | tail -n 1)"
  value="${value#\"}"
  value="${value%\"}"
  value="${value#\'}"
  value="${value%\'}"
  printf '%s' "$value"
}

source_url="${SCOUT_SOURCE_URL:-}"
if [[ -z "$source_url" ]]; then
  source_url="$(read_env_value SCOUT_SOURCE_URL || true)"
fi
source_url="${source_url:-https://raw.githubusercontent.com/doomedramen/scout/main}"
source_url="${source_url%/}"
helper_url="${SCOUT_HELPER_URL:-$source_url/packaging/host/scout}"

[[ -d "$install_dir" ]] || {
  printf 'Scout update: %s was not found\n' "$install_dir" >&2
  exit 1
}
command -v curl >/dev/null 2>&1 || {
  printf 'Scout update: curl is required to refresh the helper\n' >&2
  exit 1
}

candidate="$(mktemp /tmp/scout-helper.XXXXXX)"
cleanup() {
  rm -f -- "$candidate"
}
trap cleanup EXIT

curl --fail --location --proto '=https' --tlsv1.2 --silent --show-error --retry 3 \
  --connect-timeout 10 --output "$candidate" "$helper_url"
[[ -s "$candidate" ]] || {
  printf 'Scout update: downloaded helper is empty\n' >&2
  exit 1
}
sh -n "$candidate" || {
  printf 'Scout update: downloaded helper failed shell validation\n' >&2
  exit 1
}
install -m 0755 "$candidate" "$helper"

SCOUT_DIR="$install_dir" exec "$helper" update "$@"
