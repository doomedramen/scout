#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

if command -v rg >/dev/null 2>&1; then
  go_files="$(rg --files -g '*.go' . | sort)"
else
  go_files="$(find . -type f -name '*.go' -not -path './.git/*' -print | sort)"
fi
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
