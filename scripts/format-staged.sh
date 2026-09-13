#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

prettier_files=()
go_files=()
while IFS= read -r -d '' path; do
  case "$path" in
    *.css|*.js|*.json|*.mjs|*.ts|*.tsx|*.yml|*.yaml)
      prettier_files+=("$path")
      ;;
    *.go)
      go_files+=("$path")
      ;;
  esac
done < <(git diff --cached --name-only --diff-filter=ACMR -z)

if ((${#prettier_files[@]} > 0)); then
  npx prettier --write -- "${prettier_files[@]}"
  git add -- "${prettier_files[@]}"
fi

if ((${#go_files[@]} > 0)); then
  gofmt -w -- "${go_files[@]}"
  git add -- "${go_files[@]}"
fi
