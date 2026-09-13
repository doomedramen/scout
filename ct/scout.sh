#!/usr/bin/env bash
_CS_DEFAULT_URL="https://raw.githubusercontent.com/doomedramen/scout/main"
_cs_boot="${COMMUNITY_SCRIPTS_CORE_DIR:-$(dirname "${BASH_SOURCE[0]}")/../../core}/core/build.func"
source "$_cs_boot" 2>/dev/null || source <(curl -fsSL "${COMMUNITY_SCRIPTS_CORE_URL:-https://raw.githubusercontent.com/community-scripts/core/main}/core/build.func")
# Copyright (c) 2021-2026 community-scripts ORG
# Author: doomedramen
# License: MIT | https://github.com/community-scripts/ProxmoxVED/raw/main/LICENSE
# Source: https://github.com/doomedramen/scout

APP="Scout"
var_tags="${var_tags:-monitoring;network;docker}"
var_cpu="${var_cpu:-2}"
var_ram="${var_ram:-2048}"
var_disk="${var_disk:-12}"
var_os="${var_os:-debian}"
var_version="${var_version:-13}"
#var_arm64="${var_arm64:-no}" # unset = ask the user; set yes/no only when verified
var_unprivileged="${var_unprivileged:-1}"

header_info "$APP"
variables
color
catch_errors

function update_script() {
  header_info
  check_container_storage
  check_container_resources

  local install_dir="${SCOUT_INSTALL_DIR:-/opt/scout}"
  local compose_file="$install_dir/compose.yaml"
  local compose_url="${SCOUT_COMPOSE_URL:-${_CS_DEFAULT_URL}/compose.proxmox.yaml}"
  local candidate backup

  if [[ ! -f "$compose_file" ]]; then
    msg_error "No ${APP} Installation Found!"
    exit 1
  fi
  if ! command -v curl >/dev/null 2>&1; then
    msg_error "curl is required to refresh the Scout Compose file"
    exit 1
  fi

  msg_info "Refreshing Scout Compose file"
  cd "$install_dir"
  candidate="$(mktemp "$install_dir/compose.yaml.next.XXXXXX")"
  backup="$compose_file.previous"
  curl --fail --location --proto '=https' --tlsv1.2 --silent --show-error --retry 3 \
    --output "$candidate" "$compose_url"
  if ! docker compose -f "$candidate" config >/dev/null; then
    rm -f -- "$candidate"
    msg_error "Downloaded Scout Compose file failed validation"
    exit 1
  fi
  cp -p -- "$compose_file" "$backup"
  mv -- "$candidate" "$compose_file"
  msg_ok "Refreshed Scout Compose file"

  msg_info "Pulling Scout Images"
  if ! $STD docker compose pull; then
    cp -p -- "$backup" "$compose_file"
    msg_error "Image pull failed; restored the previous Compose file"
    exit 1
  fi
  msg_ok "Pulled Scout Images"

  msg_info "Updating Scout"
  if ! $STD docker compose up -d --remove-orphans --wait; then
    cp -p -- "$backup" "$compose_file"
    msg_error "Service restart failed; restored the previous Compose file"
    exit 1
  fi
  msg_ok "Updated Scout"
  exit
}

start
build_container
description

msg_ok "Completed Successfully!\n"
echo -e "${CREATING}${GN}${APP} setup has been successfully initialized!${CL}"
echo -e "${INFO}${YW}Access it using the following URL:${CL}"
echo -e "${GATEWAY}${BGN}http://${IP}:8080${CL}"
echo -e "${INFO}${YW}Read the initial setup token inside the container with:${CL}"
echo -e "${TAB}${GN}grep '^SCOUT_SETUP_TOKEN=' /opt/scout/.env | cut -d= -f2-${CL}"
