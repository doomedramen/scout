#!/usr/bin/env bash

# Copyright (c) 2021-2026 community-scripts ORG
# Author: doomedramen
# License: MIT | https://github.com/community-scripts/ProxmoxVED/raw/main/LICENSE
# Source: https://github.com/doomedramen/scout

source /dev/stdin <<<"$FUNCTIONS_FILE_PATH"
color
verb_ip6
catch_errors
setting_up_container
network_check
update_os

msg_info "Installing Dependencies"
$STD apt install -y curl openssl
msg_ok "Installed Dependencies"

setup_docker

msg_info "Configuring Scout"
install -d -m 0750 /opt/scout
SCOUT_DB_PASSWORD=$(openssl rand -hex 24)
SCOUT_SETUP_TOKEN=$(openssl rand -hex 24)
cat <<EOF >/opt/scout/.env
SCOUT_DB_PASSWORD=${SCOUT_DB_PASSWORD}
SCOUT_SETUP_TOKEN=${SCOUT_SETUP_TOKEN}
SCOUT_BIND_ADDRESS=0.0.0.0
EOF
chmod 600 /opt/scout/.env

cat <<'EOF' >/opt/scout/compose.yaml
name: scout

services:
  postgres:
    image: postgres:17-alpine
    restart: unless-stopped
    stop_grace_period: 1m
    environment:
      POSTGRES_USER: scout
      POSTGRES_DB: scout
      POSTGRES_PASSWORD: ${SCOUT_DB_PASSWORD:-scout-local-only}
    volumes:
      - scout-postgres:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U scout -d scout"]
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 10s

  server:
    image: ${SCOUT_IMAGE:-ghcr.io/doomedramen/scout:latest}
    pull_policy: always
    restart: unless-stopped
    stop_grace_period: 10s
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      SCOUT_PRODUCTION: "false"
      SCOUT_LISTEN: "127.0.0.1:8081"
      SCOUT_DATABASE_URL: postgres://scout:${SCOUT_DB_PASSWORD:-scout-local-only}@postgres:5432/scout?sslmode=disable
      SCOUT_SETUP_TOKEN: ${SCOUT_SETUP_TOKEN:-local-only-change-me}
      SCOUT_SECRET_KEY_FILE: /var/lib/scout/wrapping-key
      SCOUT_API_ORIGIN: http://127.0.0.1:8081
      SCOUT_PUBLIC_ORIGIN: ${SCOUT_PUBLIC_ORIGIN:-http://127.0.0.1:${SCOUT_PORT:-8080}}
      SCOUT_AUTO_ENROLLMENT: ${SCOUT_AUTO_ENROLLMENT:-true}
    ports:
      - "${SCOUT_BIND_ADDRESS:-0.0.0.0}:${SCOUT_PORT:-8080}:8080"
    volumes:
      - scout-server-data:/var/lib/scout
    read_only: true
    tmpfs:
      - /tmp:size=16m,mode=1777
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL

volumes:
  scout-postgres:
  scout-server-data:
EOF
msg_ok "Configured Scout"

msg_info "Starting Scout"
cd /opt/scout
$STD docker compose up -d --wait
msg_ok "Started Scout"

motd_ssh
customize

cat <<'EOF' >/usr/bin/update
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
if ! docker compose up -d --remove-orphans --wait; then
  cp -p -- "$backup" "$compose_file"
  echo "Scout update: service restart failed; restored the previous Compose file" >&2
  exit 1
fi

echo "Scout updated successfully."
EOF
chmod 0755 /usr/bin/update

cleanup_lxc
