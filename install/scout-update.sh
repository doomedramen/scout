#!/usr/bin/env bash
set -euo pipefail

install_dir="${SCOUT_INSTALL_DIR:-/opt/scout}"
compose_file="$install_dir/compose.yaml"
compose_url="${SCOUT_COMPOSE_URL:-https://raw.githubusercontent.com/doomedramen/scout/main/compose.proxmox.yaml}"

if [[ ! -f "$compose_file" ]]; then
  echo "Scout update: $compose_file was not found" >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
  echo "Scout update: curl is required to refresh the Compose file" >&2
  exit 1
fi

cd "$install_dir"
candidate="$(mktemp "$install_dir/compose.yaml.next.XXXXXX")"
backup="$compose_file.previous"
cleanup() {
  rm -f -- "$candidate"
}
trap cleanup EXIT

curl --fail --location --proto '=https' --tlsv1.2 --silent --show-error --retry 3 \
  --output "$candidate" "$compose_url"
if ! docker compose -f "$candidate" config >/dev/null; then
  echo "Scout update: downloaded Compose file failed validation" >&2
  exit 1
fi

cp -p -- "$compose_file" "$backup"
mv -- "$candidate" "$compose_file"

if ! docker compose pull; then
  cp -p -- "$backup" "$compose_file"
  echo "Scout update: image pull failed; restored the previous Compose file" >&2
  exit 1
fi
if ! docker compose run --rm --no-deps --user 0 --cap-add CHOWN --cap-add FOWNER \
  --entrypoint /bin/sh server -c '
    chown -R node:node /var/lib/scout
    chmod 0700 /var/lib/scout
    if [ -e /var/lib/scout/wrapping-key ]; then
      chmod 0400 /var/lib/scout/wrapping-key
    fi
  '; then
  cp -p -- "$backup" "$compose_file"
  echo "Scout update: could not repair Scout data permissions; restored the previous Compose file" >&2
  exit 1
fi
if ! docker compose up -d --remove-orphans --wait; then
  cp -p -- "$backup" "$compose_file"
  echo "Scout update: service restart failed; restored the previous Compose file" >&2
  exit 1
fi

echo "Scout updated successfully."
