#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
container_name="scout-test-postgres-${PPID}-${RANDOM}"
host_port="${SCOUT_TEST_POSTGRES_PORT:-55432}"
password="scout-test-password-${PPID}-${RANDOM}"
cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if [[ -n "${SCOUT_TEST_DATABASE_URL:-}" ]]; then
  (cd "$repo_dir" && SCOUT_TEST_DATABASE_URL="$SCOUT_TEST_DATABASE_URL" go test ./tests/integration -count=1)
  exit $?
fi

command -v docker >/dev/null
docker run --name "$container_name" --rm -d \
  -e POSTGRES_USER=scout \
  -e POSTGRES_DB=scout \
  -e POSTGRES_PASSWORD="$password" \
  -p "127.0.0.1:${host_port}:5432" \
  postgres:17-alpine >/dev/null

for attempt in $(seq 1 40); do
  if docker exec "$container_name" pg_isready -U scout -d scout >/dev/null 2>&1; then
    break
  fi
  if [[ "$attempt" == 40 ]]; then
    echo "PostgreSQL test container did not become ready" >&2
    exit 1
  fi
  sleep 0.25
done

(cd "$repo_dir" && \
  SCOUT_TEST_DATABASE_URL="postgres://scout:${password}@127.0.0.1:${host_port}/scout?sslmode=disable" \
  go test ./tests/integration -count=1)
