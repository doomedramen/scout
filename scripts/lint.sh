#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

go_files="$(rg --files -g '*.go' . | sort)"
if [[ -n "$go_files" ]]; then
  unformatted="$(gofmt -l $go_files)"
  if [[ -n "$unformatted" ]]; then
    echo "gofmt required for:" >&2
    printf '%s\n' "$unformatted" >&2
    exit 1
  fi
fi

go vet ./...
npm run format:check
npm run check
