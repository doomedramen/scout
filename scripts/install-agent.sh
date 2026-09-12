#!/usr/bin/env bash
set -euo pipefail

agent_server_url=""
agent_invitation_file=""
agent_artifact=""
agent_default_server_url='__SCOUT_SERVER_URL__'
agent_server_url="${SCOUT_SERVER_URL:-$agent_default_server_url}"

usage() {
    cat <<'EOF'
Install and start a Scout Linux agent.

Usage:
  scripts/install-agent.sh --server URL --invitation-file PATH
  scripts/install-agent.sh --server URL --invitation-file PATH --artifact PATH

The server-served installer also accepts SCOUT_OTI and embeds the server URL
when downloaded from /api/v1/bootstrap/agent/install.sh. That enables the
short form:
  SCOUT_OTI=... bash -c "\$(curl -fsSL URL/api/v1/bootstrap/agent/install.sh)"

The default path downloads the matching Linux agent from the Scout server and
verifies its SHA-256 transfer checksum. Use --artifact with a locally built or
separately verified binary when the server was built from source without the
bootstrap artifacts.

Options:
  --server URL              Scout URL reachable from this Linux host
  --invitation-file PATH    one-time invitation file created by the owner UI
  --artifact PATH           local scout-agent binary; skips the download
  --help                    show this help

Environment:
  SCOUT_OTI                 one-time invitation; written to a protected
                            temporary file and unset before installation
  SCOUT_SERVER_URL          server URL when the installer was not served by
                            Scout and --server was not supplied
EOF
}

fail() {
    echo "scout-agent installer: $*" >&2
    exit 2
}

while [[ $# -gt 0 ]]; do
    case "$1" in
    --server)
        [[ $# -ge 2 ]] || fail "--server requires a URL"
        agent_server_url=$2
        shift 2
        ;;
    --invitation-file)
        [[ $# -ge 2 ]] || fail "--invitation-file requires a path"
        agent_invitation_file=$2
        shift 2
        ;;
    --artifact)
        [[ $# -ge 2 ]] || fail "--artifact requires a path"
        agent_artifact=$2
        shift 2
        ;;
    --help|-h)
        usage
        exit 0
        ;;
    *)
        fail "unknown option: $1"
        ;;
    esac
done

[[ "$(uname -s)" == "Linux" ]] || fail "native installation requires Linux"
[[ "$agent_server_url" != "$agent_default_server_url" ]] || fail "server URL was not embedded; use a current Scout installer or provide --server/SCOUT_SERVER_URL"
server_url_pattern='^https?://[A-Za-z0-9:/._~+\[\]-]+$'
[[ "$agent_server_url" =~ $server_url_pattern ]] || fail "--server must be an http(s) URL without shell metacharacters"

installer_tmp_dir=$(mktemp -d)
cleanup() {
    rm -rf -- "$installer_tmp_dir"
}
trap cleanup EXIT

if [[ -n "${SCOUT_OTI:-}" ]]; then
    [[ -z "$agent_invitation_file" ]] || fail "use SCOUT_OTI or --invitation-file, not both"
    agent_invitation_file="$installer_tmp_dir/invitation"
    (umask 077; printf '%s\n' "$SCOUT_OTI" > "$agent_invitation_file")
    unset SCOUT_OTI
fi

[[ -n "$agent_invitation_file" && -s "$agent_invitation_file" ]] || fail "invitation file is missing or empty"

command -v install >/dev/null 2>&1 || fail "install is required"
command -v systemctl >/dev/null 2>&1 || fail "systemd is required"
command -v useradd >/dev/null 2>&1 || fail "useradd is required"

run_privileged() {
    if [[ "$(id -u)" -eq 0 ]]; then
        "$@"
        return
    fi
    sudo "$@"
}

if [[ "$(id -u)" -ne 0 ]]; then
    command -v sudo >/dev/null 2>&1 || fail "sudo is required for native installation"
    sudo -v || fail "sudo authorization failed"
fi

agent_arch=""
case "$(uname -m)" in
    x86_64|amd64) agent_arch="amd64" ;;
    aarch64|arm64) agent_arch="arm64" ;;
    *) fail "unsupported Linux architecture: $(uname -m)" ;;
esac

if [[ -z "$agent_artifact" ]]; then
    command -v curl >/dev/null 2>&1 || fail "curl is required to download the agent from Scout"
    agent_name="scout-agent-linux-$agent_arch"
    agent_artifact="$installer_tmp_dir/$agent_name"
    server_base_url="${agent_server_url%/}"
    curl --fail --location --proto '=https,http' --tlsv1.3 --silent --show-error --dump-header "$installer_tmp_dir/headers" --output "$agent_artifact" "$server_base_url/api/v1/bootstrap/agent/$agent_arch"
    expected_hash=$(awk 'tolower($1) == "x-scout-agent-sha256:" { print $2 }' "$installer_tmp_dir/headers" | tr -d '\r' | tail -n 1)
    [[ "$expected_hash" =~ ^[A-Fa-f0-9]{64}$ ]] || fail "Scout server response did not include a valid agent checksum"
    if command -v sha256sum >/dev/null 2>&1; then
        printf '%s  %s\n' "$expected_hash" "$agent_artifact" | sha256sum --check --status - || fail "agent checksum verification failed"
    else
        command -v shasum >/dev/null 2>&1 || fail "sha256sum or shasum is required"
        actual_hash=$(shasum -a 256 "$agent_artifact" | awk '{print $1}')
        [[ "$actual_hash" == "$expected_hash" ]] || fail "agent checksum verification failed"
    fi
fi

[[ -f "$agent_artifact" && -s "$agent_artifact" ]] || fail "agent artifact is missing or empty"

if ! id scout-agent >/dev/null 2>&1; then
    run_privileged useradd --system --user-group --home-dir /var/lib/scout/agent --shell /usr/sbin/nologin scout-agent
fi

agent_uid=$(id -u scout-agent)
agent_gid=$(id -g scout-agent)
run_privileged install -d -o "$agent_uid" -g "$agent_gid" -m 0700 /var/lib/scout/agent
run_privileged install -d -o root -g root -m 0755 /usr/local/libexec
run_privileged install -o root -g root -m 0755 "$agent_artifact" /usr/local/libexec/scout-agent
run_privileged install -o "$agent_uid" -g "$agent_gid" -m 0400 "$agent_invitation_file" /var/lib/scout/agent/invitation

run_privileged install -d -o root -g root -m 0755 /etc/scout
printf 'SCOUT_SERVER_URL=%s\n' "$agent_server_url" > "$installer_tmp_dir/agent.env"
run_privileged install -o root -g root -m 0644 "$installer_tmp_dir/agent.env" /etc/scout/agent.env

printf '%s\n' \
    '[Unit]' \
    'Description=Scout Linux monitoring agent' \
    'Wants=network-online.target' \
    'After=network-online.target' \
    '' \
    '[Service]' \
    'Type=simple' \
    'User=scout-agent' \
    'Group=scout-agent' \
    'EnvironmentFile=-/etc/scout/agent.env' \
    'ExecStart=/usr/local/libexec/scout-agent --daemon --invitation-file /var/lib/scout/agent/invitation --data-dir /var/lib/scout/agent' \
    'Restart=on-failure' \
    'RestartSec=5s' \
    'UMask=0077' \
    'NoNewPrivileges=true' \
    'PrivateTmp=true' \
    'ProtectSystem=strict' \
    'ProtectHome=true' \
    'ReadWritePaths=/var/lib/scout/agent' \
    'CapabilityBoundingSet=' \
    'LockPersonality=true' \
    'MemoryDenyWriteExecute=true' \
    'RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6' \
    '' \
    '[Install]' \
    'WantedBy=multi-user.target' \
    > "$installer_tmp_dir/scout-agent.service"
run_privileged install -o root -g root -m 0644 "$installer_tmp_dir/scout-agent.service" /etc/systemd/system/scout-agent.service

run_privileged systemctl daemon-reload
run_privileged systemctl enable --now scout-agent.service
run_privileged systemctl is-active --quiet scout-agent.service || fail "scout-agent.service did not become active"

echo "Scout agent installed and started as scout-agent.service"
echo "The one-time invitation remains at /var/lib/scout/agent/invitation until enrollment succeeds."
