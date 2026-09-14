#!/usr/bin/env bash

# Copyright (c) 2021-2026 community-scripts ORG
# Author: doomedramen
# License: MIT | https://github.com/community-scripts/ProxmoxVE/raw/main/LICENSE
# Source: https://github.com/doomedramen/scout

source /dev/stdin <<<"$FUNCTIONS_FILE_PATH"
color
verb_ip6
catch_errors
setting_up_container
network_check
update_os

SCOUT_DIR="${SCOUT_INSTALL_DIR:-/opt/scout}"
SCOUT_SOURCE_URL="${SCOUT_SOURCE_URL:-${COMMUNITY_SCRIPTS_URL:-https://raw.githubusercontent.com/doomedramen/scout/main}}"
SCOUT_SOURCE_URL="${SCOUT_SOURCE_URL%/}"
SCOUT_COMPOSE_URL="${SCOUT_COMPOSE_URL:-$SCOUT_SOURCE_URL/compose.yaml}"
SCOUT_HELPER_URL="${SCOUT_HELPER_URL:-$SCOUT_SOURCE_URL/packaging/host/scout}"
SCOUT_UPDATE_URL="${SCOUT_UPDATE_URL:-$SCOUT_SOURCE_URL/install/scout-update.sh}"

download_checked() {
  local url="${1:?url}" destination="${2:?destination}" mode="${3:?mode}" candidate
  candidate="$(mktemp /tmp/scout-download.XXXXXX)"
  if ! curl --fail --location --proto '=https' --tlsv1.2 --silent --show-error --retry 3 \
    --connect-timeout 10 --output "$candidate" "$url"; then
    rm -f -- "$candidate"
    msg_error "Could not download ${url}"
    exit 1
  fi
  if [[ ! -s "$candidate" ]]; then
    rm -f -- "$candidate"
    msg_error "Downloaded file from ${url} is empty"
    exit 1
  fi
  if [[ "$mode" == 0755 ]] && ! sh -n "$candidate"; then
    rm -f -- "$candidate"
    msg_error "Downloaded script from ${url} failed shell validation"
    exit 1
  fi
  install -m "$mode" "$candidate" "$destination"
  rm -f -- "$candidate"
}

install_docker() {
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    return 0
  fi

  msg_info "Installing Docker Engine and Compose"
  if declare -f setup_docker >/dev/null 2>&1; then
    setup_docker
  else
    $STD apt-get update
    $STD apt-get install -y --no-install-recommends ca-certificates curl docker.io docker-compose-v2
  fi
  if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
    msg_error "Docker Compose is unavailable after installation"
    exit 1
  fi
  msg_ok "Installed Docker Engine and Compose"
}

start_docker() {
  msg_info "Starting Docker"
  if command -v systemctl >/dev/null 2>&1; then
    systemctl enable --now docker
  elif command -v service >/dev/null 2>&1; then
    service docker start
  else
    msg_error "Could not find a service manager to start Docker"
    exit 1
  fi
  docker info >/dev/null 2>&1 || {
    msg_error "Docker daemon is not ready"
    exit 1
  }
  msg_ok "Started Docker"
}

configure_scout() {
  local env_file="$SCOUT_DIR/.env"
  install -d -m 0750 "$SCOUT_DIR"

  if [[ ! -e "$env_file" ]]; then
    msg_info "Creating Scout configuration"
    {
      printf 'SCOUT_PORT=%s\n' "${SCOUT_PORT:-8080}"
      if [[ -n "${SCOUT_PUBLIC_URL:-}" ]]; then
        printf 'SCOUT_PUBLIC_URL=%s\n' "$SCOUT_PUBLIC_URL"
      fi
      if [[ -n "${SCOUT_SOURCE_URL:-}" && "$SCOUT_SOURCE_URL" != "https://raw.githubusercontent.com/doomedramen/scout/main" ]]; then
        printf 'SCOUT_SOURCE_URL=%s\n' "$SCOUT_SOURCE_URL"
      fi
      if [[ -n "${SCOUT_COMPOSE_URL:-}" && "$SCOUT_COMPOSE_URL" != "https://raw.githubusercontent.com/doomedramen/scout/main/compose.yaml" ]]; then
        printf 'SCOUT_COMPOSE_URL=%s\n' "$SCOUT_COMPOSE_URL"
      fi
    } >"$env_file"
    chmod 0600 "$env_file"
    msg_ok "Created Scout configuration"
  else
    chmod 0600 "$env_file"
    msg_ok "Preserved existing Scout configuration"
  fi
}

install_scout_helper() {
  msg_info "Installing Scout host helper"
  download_checked "$SCOUT_HELPER_URL" /usr/local/bin/scout 0755
  msg_ok "Installed Scout host helper"

  msg_info "Installing Scout update command"
  download_checked "$SCOUT_UPDATE_URL" /usr/bin/update 0755
  msg_ok "Installed Scout update command"
}

start_scout() {
  msg_info "Starting Scout"
  cd "$SCOUT_DIR"
  SCOUT_DIR="$SCOUT_DIR" SCOUT_COMPOSE_URL="$SCOUT_COMPOSE_URL" /usr/local/bin/scout up
  msg_ok "Started Scout"
}

install_docker
start_docker
configure_scout
install_scout_helper
start_scout

motd_ssh
customize

msg_ok "Scout has been installed successfully!\n"
echo -e "${INFO}${YW}Open Scout at:${CL}"
echo -e "${GATEWAY}${BGN}http://$(get_ip):${SCOUT_PORT:-8080}${CL}"
echo -e "${INFO}${YW}Read the one-time setup token with:${CL}"
echo -e "${TAB}${GN}scout setup-token${CL}"
echo -e "${INFO}${YW}Run updates inside the LXC with:${CL}"
echo -e "${TAB}${GN}scout update${CL}"

cleanup_lxc
