#!/bin/sh
set -eu

script_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_directory=$(CDPATH= cd -- "$script_directory/.." && pwd)
artifact_directory=${SCOUT_AGENT_ARTIFACT_DIR:-agent-artifacts}
artifact_binary=${SCOUT_AGENT_BINARY:-}

if [ -z "$artifact_binary" ]; then
  cargo build --release --locked --manifest-path "$repository_directory/agent/Cargo.toml"
  artifact_binary="$repository_directory/target/release/scout-agent"
fi

[ -f "$artifact_binary" ] || {
  printf '%s\n' "scout agent build: binary not found at $artifact_binary" >&2
  exit 1
}

mkdir -p "$artifact_directory"
cp "$artifact_binary" "$artifact_directory/scout-agent-linux-x86_64"
chmod 0755 "$artifact_directory/scout-agent-linux-x86_64"
printf '%s\n' "Prepared $artifact_directory/scout-agent-linux-x86_64"
