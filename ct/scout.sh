#!/usr/bin/env bash

# The public entrypoint is intentionally self-contained: the Proxmox VE helper
# engine supplies the LXC prompts and executes install/scout-install.sh inside
# the new container. Keep this URL separate from the engine URL so forks can
# override COMMUNITY_SCRIPTS_URL without changing the one-line command.
_CS_DEFAULT_URL="https://raw.githubusercontent.com/doomedramen/scout/main"
_cs_boot="${COMMUNITY_SCRIPTS_CORE_DIR:-$(dirname "${BASH_SOURCE[0]}")/../../core}/core/build.func"
_cs_core_url="${COMMUNITY_SCRIPTS_CORE_URL:-https://raw.githubusercontent.com/community-scripts/ProxmoxVE/main/misc/build.func}"
case "$_cs_core_url" in
  */build.func) ;;
  *) _cs_core_url="${_cs_core_url%/}/misc/build.func" ;;
esac
source "$_cs_boot" 2>/dev/null || source <(curl -fsSL "$_cs_core_url")

# Copyright (c) 2021-2026 community-scripts ORG
# Author: doomedramen
# License: MIT | https://github.com/community-scripts/ProxmoxVE/raw/main/LICENSE
# Source: https://github.com/doomedramen/scout

APP="Scout"
SCRIPT_SLUG="scout"
var_tags="${var_tags:-monitoring;network;docker}"
var_cpu="${var_cpu:-2}"
var_ram="${var_ram:-2048}"
var_disk="${var_disk:-12}"
var_os="${var_os:-debian}"
var_version="${var_version:-13}"
var_arm64="${var_arm64:-yes}"
var_unprivileged="${var_unprivileged:-1}"

# The current community build engine still uses its own repository URL when it
# fetches the container installer. Intercept only that one request so this
# entrypoint remains a real one-line installer for forks and standalone repos;
# every other engine download continues to use the engine's curl invocation.
SCOUT_SOURCE_URL="${SCOUT_SOURCE_URL:-$_CS_DEFAULT_URL}"
SCOUT_SOURCE_URL="${SCOUT_SOURCE_URL%/}"
SCOUT_INSTALLER_URL="${SCOUT_INSTALLER_URL:-$SCOUT_SOURCE_URL/install/scout-install.sh}"
scout_curl() {
  local argument index=0
  local -a arguments=("$@")
  for argument in "$@"; do
    if [[ "$argument" == "https://raw.githubusercontent.com/community-scripts/ProxmoxVE/main/install/${var_install}.sh" && "${var_install:-}" == scout-install ]]; then
      arguments[index]="$SCOUT_INSTALLER_URL"
      command curl "${arguments[@]}"
      return
    fi
    index=$((index + 1))
  done
  command curl "$@"
}
curl() {
  scout_curl "$@"
}
export SCOUT_SOURCE_URL

header_info "$APP"
variables
color
catch_errors

fetch_helper() {
  local destination="${1:?destination}" source_root helper_url candidate
  source_root="${COMMUNITY_SCRIPTS_URL:-$_CS_DEFAULT_URL}"
  source_root="${source_root%/}"
  helper_url="${SCOUT_HELPER_URL:-$source_root/packaging/host/scout}"
  candidate="$(mktemp /tmp/scout-helper.XXXXXX)"
  if ! curl --fail --location --proto '=https' --tlsv1.2 --silent --show-error --retry 3 \
    --connect-timeout 10 --output "$candidate" "$helper_url"; then
    rm -f -- "$candidate"
    msg_error "Could not download the Scout updater from ${helper_url}"
    return 1
  fi
  if ! sh -n "$candidate"; then
    rm -f -- "$candidate"
    msg_error "The downloaded Scout updater failed shell validation"
    return 1
  fi
  install -m 0755 "$candidate" "$destination"
  rm -f -- "$candidate"
}

function update_script() {
  header_info
  check_container_storage
  check_container_resources

  local helper="/usr/local/bin/scout"
  if [[ ! -x "$helper" ]]; then
    msg_info "Installing the Scout updater"
    fetch_helper "$helper" || exit 1
    msg_ok "Installed the Scout updater"
  fi

  msg_info "Updating Scout"
  if ! SCOUT_DIR="${SCOUT_INSTALL_DIR:-/opt/scout}" "$helper" update; then
    msg_error "Scout update failed; the previous Compose file and data remain in place"
    exit 1
  fi
  msg_ok "Updated Scout"
  exit 0
}

start
build_container
description

msg_ok "Completed successfully!\n"
echo -e "${CREATING}${GN}${APP} setup has been successfully initialized!${CL}"
echo -e "${INFO}${YW}Open Scout at:${CL}"
echo -e "${GATEWAY}${BGN}http://${IP}:8080${CL}"
echo -e "${INFO}${YW}Read the one-time setup token with:${CL}"
echo -e "${TAB}${GN}scout setup-token${CL}"
echo -e "${INFO}${YW}Run updates inside the LXC with:${CL}"
echo -e "${TAB}${GN}scout update${CL}"
