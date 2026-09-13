import fs from "node:fs";
import path from "node:path";

export type InstallerPlatform = "linux" | "macos";
export type InstallerArchitecture = "x86_64" | "aarch64";

const artifactNames: Record<InstallerPlatform, Record<InstallerArchitecture, string>> = {
  linux: {
    x86_64: "scout-agent-linux-x86_64",
    aarch64: "scout-agent-linux-aarch64",
  },
  macos: {
    x86_64: "scout-agent-macos-x86_64",
    aarch64: "scout-agent-macos-aarch64",
  },
};

export function artifactName(
  platform: InstallerPlatform,
  architecture: InstallerArchitecture,
): string {
  return artifactNames[platform][architecture];
}

export function artifactPath(
  platform: InstallerPlatform,
  architecture: InstallerArchitecture,
): string {
  return path.join(process.cwd(), "agent-artifacts", artifactName(platform, architecture));
}

export function readAgentArtifact(
  platform: InstallerPlatform,
  architecture: InstallerArchitecture,
): Buffer | null {
  try {
    return fs.readFileSync(artifactPath(platform, architecture));
  } catch (error) {
    if (error instanceof Error && (error as NodeJS.ErrnoException).code === "ENOENT") return null;
    throw error;
  }
}

export function renderInstallerScript(input: {
  serverUrl: string;
  callbackUrl: string;
  invitation: string;
}): string {
  const serverUrl = shellQuote(input.serverUrl);
  const callbackUrl = shellQuote(input.callbackUrl);
  const invitation = shellQuote(input.invitation);
  const lines = [
    "#!/bin/sh",
    "set -eu",
    "",
    "SCOUT_SERVER_URL=${SCOUT_SERVER_URL:-" + serverUrl + "}",
    "SCOUT_CALLBACK_URL=${SCOUT_CALLBACK_URL:-" + callbackUrl + "}",
    "SCOUT_OTI=${SCOUT_OTI:-" + invitation + "}",
    "SCOUT_PRIVILEGE_FILE=${SCOUT_PRIVILEGE_FILE:-}",
    "",
    "fail() {",
    "  printf '%s\\n' \"scout-agent installer: $1\" >&2",
    "  exit 1",
    "}",
    "",
    "fetch_to() {",
    "  url=$1",
    "  destination=$2",
    "  if command -v curl >/dev/null 2>&1; then",
    "    curl --fail --silent --show-error --location --retry 2 \\",
    '      --header "x-scout-oti: $SCOUT_OTI" "$url" -o "$destination"',
    "  elif command -v wget >/dev/null 2>&1; then",
    '    wget --quiet --output-document="$destination" \\',
    '      --header="x-scout-oti: $SCOUT_OTI" "$url"',
    "  else",
    '    fail "curl or wget is required"',
    "  fi",
    "}",
    "",
    'if [ -z "$SCOUT_SERVER_URL" ] || [ -z "$SCOUT_OTI" ]; then',
    '  fail "server URL and one-time invitation are required"',
    "fi",
    "",
    'preflight="${TMPDIR:-/tmp}/scout-preflight.$$"',
    'fetch_to "$SCOUT_CALLBACK_URL/api/v1/bootstrap/agent/preflight" "$preflight" || \\',
    '  fail "Scout callback is unreachable; make SCOUT_PUBLIC_URL reachable from this host"',
    'rm -f "$preflight"',
    "",
    "os=$(uname -s 2>/dev/null || true)",
    "machine=$(uname -m 2>/dev/null || true)",
    'case "$os:$machine" in',
    "  Linux:x86_64|Linux:amd64) platform=linux; architecture=x86_64 ;;",
    "  Linux:aarch64|Linux:arm64) platform=linux; architecture=aarch64 ;;",
    "  Darwin:x86_64|Darwin:amd64) platform=macos; architecture=x86_64 ;;",
    "  Darwin:arm64|Darwin:aarch64) platform=macos; architecture=aarch64 ;;",
    '  *) fail "unsupported platform or architecture: $os $machine" ;;',
    "esac",
    "",
    'tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/scout-agent.XXXXXX")',
    "cleanup() {",
    '  rm -rf "$tmp_dir"',
    "}",
    "trap cleanup EXIT HUP INT TERM",
    "",
    'artifact="$tmp_dir/scout-agent"',
    'fetch_to "$SCOUT_SERVER_URL/api/v1/bootstrap/agent/artifact?platform=$platform&architecture=$architecture" "$artifact" || \\',
    '  fail "Scout has no verified agent artifact for $platform/$architecture"',
    'chmod 700 "$artifact"',
    "",
    "run_privileged() {",
    '  if [ "$(id -u)" -eq 0 ]; then',
    '    "$@"',
    "  elif command -v sudo >/dev/null 2>&1; then",
    '    if [ -n "$SCOUT_PRIVILEGE_FILE" ] && [ -s "$SCOUT_PRIVILEGE_FILE" ]; then',
    '      sudo -S -p \'\' "$@" < "$SCOUT_PRIVILEGE_FILE"',
    "    else",
    '      sudo "$@"',
    "    fi",
    "  else",
    '    fail "root or sudo access is required"',
    "  fi",
    "}",
    "",
    "run_agent_once() {",
    '  if [ "$(id -u)" -eq 0 ]; then',
    '    SCOUT_SERVER_URL="$SCOUT_SERVER_URL" SCOUT_INVITATION="$SCOUT_OTI" \\',
    '      SCOUT_DATA_DIR="$1" "$2" --once',
    "  else",
    '    run_privileged -u scout-agent env SCOUT_SERVER_URL="$SCOUT_SERVER_URL" \\',
    '      SCOUT_INVITATION="$SCOUT_OTI" SCOUT_DATA_DIR="$1" "$2" --once',
    "  fi",
    "}",
    "",
    'case "$platform" in',
    "  linux)",
    "    run_privileged install -d -m 0755 /usr/local/lib/scout-agent /var/lib/scout-agent",
    '    run_privileged install -m 0755 "$artifact" /usr/local/lib/scout-agent/scout-agent',
    "    if ! getent passwd scout-agent >/dev/null 2>&1; then",
    "      run_privileged useradd --system --home-dir /var/lib/scout-agent --shell /usr/sbin/nologin scout-agent",
    "    fi",
    "    run_privileged chown -R scout-agent:scout-agent /var/lib/scout-agent",
    "    run_privileged install -d -m 0755 /etc/systemd/system",
    '    unit="$tmp_dir/scout-agent.service"',
    '    cat > "$unit" <<EOF',
    "[Unit]",
    "Description=Scout host monitoring agent",
    "After=network-online.target",
    "Wants=network-online.target",
    "",
    "[Service]",
    "Type=simple",
    "User=scout-agent",
    "ExecStart=/usr/local/lib/scout-agent/scout-agent --server $SCOUT_SERVER_URL --data-dir /var/lib/scout-agent",
    "Restart=always",
    "RestartSec=5",
    "NoNewPrivileges=true",
    "ProtectSystem=strict",
    "ProtectHome=true",
    "ReadWritePaths=/var/lib/scout-agent",
    "",
    "[Install]",
    "WantedBy=multi-user.target",
    "EOF",
    '    run_privileged install -m 0644 "$unit" /etc/systemd/system/scout-agent.service',
    "    run_agent_once /var/lib/scout-agent /usr/local/lib/scout-agent/scout-agent",
    "    run_privileged systemctl daemon-reload",
    "    run_privileged systemctl enable --now scout-agent.service",
    "    ;;",
    "  macos)",
    "    run_privileged install -d -m 0755 /usr/local/lib/scout-agent \\",
    '      "/Library/Application Support/ScoutAgent"',
    '    run_privileged install -m 0755 "$artifact" /usr/local/lib/scout-agent/scout-agent',
    '    run_agent_once "/Library/Application Support/ScoutAgent" /usr/local/lib/scout-agent/scout-agent',
    '    plist="$tmp_dir/page.rtin.scout-agent.plist"',
    '    cat > "$plist" <<EOF',
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">',
    '<plist version="1.0"><dict>',
    "  <key>Label</key><string>page.rtin.scout-agent</string>",
    "  <key>ProgramArguments</key><array>",
    "    <string>/usr/local/lib/scout-agent/scout-agent</string><string>--server</string><string>$SCOUT_SERVER_URL</string><string>--data-dir</string><string>/Library/Application Support/ScoutAgent</string>",
    "  </array>",
    "  <key>RunAtLoad</key><true/><key>KeepAlive</key><true/>",
    "</dict></plist>",
    "EOF",
    '    run_privileged install -m 0644 "$plist" /Library/LaunchDaemons/page.rtin.scout-agent.plist',
    "    run_privileged launchctl bootstrap system /Library/LaunchDaemons/page.rtin.scout-agent.plist",
    "    ;;",
    "esac",
    "",
    "printf '%s\\n' \"Scout agent installed and enrolled.\"",
  ];
  return lines.join("\n") + "\n";
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}
