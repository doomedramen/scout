#!/bin/sh
set -eu

script_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_directory=$(CDPATH= cd -- "$script_directory/.." && pwd)
artifact_directory=${SCOUT_AGENT_ARTIFACT_DIR:-agent-artifacts}
artifact_binary=${SCOUT_AGENT_BINARY:-}
agent_platform=${SCOUT_AGENT_PLATFORM:-}
agent_architecture=${SCOUT_AGENT_ARCHITECTURE:-}
agent_target=${SCOUT_AGENT_TARGET:-}

if [ "${SCOUT_USE_PREBUILT_ARTIFACTS:-0}" = 1 ]; then
  for prebuilt in \
    scout-agent-linux-x86_64 \
    scout-agent-linux-aarch64 \
    scout-agent-macos-x86_64 \
    scout-agent-macos-aarch64 \
    release-manifest.json \
    publisher-public.key; do
    [ -s "$artifact_directory/$prebuilt" ] || {
      printf '%s\n' "scout agent build: prebuilt artifact is missing: $artifact_directory/$prebuilt" >&2
      exit 1
    }
  done
  printf '%s\n' "Using the complete prebuilt Scout agent release set"
  exit 0
fi

if [ -z "$agent_platform" ]; then
  case "$(uname -s 2>/dev/null || true)" in
    Linux) agent_platform=linux ;;
    Darwin) agent_platform=macos ;;
    *)
      printf '%s\n' "scout agent build: set SCOUT_AGENT_PLATFORM on this host" >&2
      exit 1
      ;;
  esac
fi

if [ -z "$agent_architecture" ]; then
  case "$(uname -m 2>/dev/null || true)" in
    x86_64|amd64) agent_architecture=x86_64 ;;
    aarch64|arm64) agent_architecture=aarch64 ;;
    *)
      printf '%s\n' "scout agent build: set SCOUT_AGENT_ARCHITECTURE on this host" >&2
      exit 1
      ;;
  esac
fi

case "$agent_platform:$agent_architecture" in
  linux:x86_64) agent_target=${agent_target:-x86_64-unknown-linux-musl} ;;
  linux:aarch64) agent_target=${agent_target:-aarch64-unknown-linux-musl} ;;
  macos:x86_64) agent_target=${agent_target:-x86_64-apple-darwin} ;;
  macos:aarch64) agent_target=${agent_target:-aarch64-apple-darwin} ;;
  *)
    printf '%s\n' "scout agent build: unsupported platform or architecture: $agent_platform/$agent_architecture" >&2
    exit 1
    ;;
esac

if [ -z "$artifact_binary" ]; then
  cargo build --release --locked --manifest-path "$repository_directory/agent/Cargo.toml" --target "$agent_target"
  artifact_binary="$repository_directory/target/$agent_target/release/scout-agent"
fi

[ -f "$artifact_binary" ] || {
  printf '%s\n' "scout agent build: binary not found at $artifact_binary" >&2
  exit 1
}

mkdir -p "$artifact_directory"
artifact_name="scout-agent-$agent_platform-$agent_architecture"
cp "$artifact_binary" "$artifact_directory/$artifact_name"
chmod 0755 "$artifact_directory/$artifact_name"
SCOUT_AGENT_ARTIFACT_DIR="$artifact_directory" \
SCOUT_AGENT_PLATFORM="$agent_platform" \
SCOUT_AGENT_ARCHITECTURE="$agent_architecture" \
  node "$repository_directory/scripts/create-release-manifest.mjs"
printf '%s\n' "Prepared $artifact_directory/$artifact_name"
