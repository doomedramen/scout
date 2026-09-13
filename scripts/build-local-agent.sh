#!/bin/sh
set -eu

cargo build --release --locked -p scout-agent
mkdir -p agent-artifacts
cp target/release/scout-agent agent-artifacts/scout-agent-linux-x86_64
chmod 0755 agent-artifacts/scout-agent-linux-x86_64
printf '%s\n' "Prepared agent-artifacts/scout-agent-linux-x86_64"
