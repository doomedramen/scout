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
$STD apt install -y openssl
msg_ok "Installed Dependencies"

setup_docker

msg_info "Configuring Scout"
install -d -m 0750 /opt/scout
SCOUT_DB_PASSWORD=$(openssl rand -hex 24)
SCOUT_SETUP_TOKEN=$(openssl rand -hex 24)
cat <<EOF >/opt/scout/.env
SCOUT_DB_PASSWORD=${SCOUT_DB_PASSWORD}
SCOUT_SETUP_TOKEN=${SCOUT_SETUP_TOKEN}
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
      POSTGRES_PASSWORD: ${SCOUT_DB_PASSWORD}
    volumes:
      - scout-postgres:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U scout -d scout"]
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 10s

  server:
    image: ghcr.io/doomedramen/scout:latest
    restart: unless-stopped
    stop_grace_period: 10s
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      SCOUT_PRODUCTION: "false"
      SCOUT_LISTEN: "0.0.0.0:8080"
      SCOUT_DATABASE_URL: postgres://scout:${SCOUT_DB_PASSWORD}@postgres:5432/scout?sslmode=disable
      SCOUT_SETUP_TOKEN: ${SCOUT_SETUP_TOKEN}
      SCOUT_SECRET_KEY_FILE: /var/lib/scout/wrapping-key
      SCOUT_WEB_DIR: /usr/local/share/scout/web
    ports:
      - "8080:8080"
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

cd "${SCOUT_INSTALL_DIR:-/opt/scout}"
docker compose pull
docker compose up -d --remove-orphans --wait
echo "Scout updated successfully."
EOF
chmod 0755 /usr/bin/update

cleanup_lxc
