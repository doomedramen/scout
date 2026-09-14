#!/bin/sh
set -eu

work_directory=${SCOUT_PACKAGED_E2E_WORK_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/scout-packaged-e2e.XXXXXX")}
keep_work_directory=${SCOUT_PACKAGED_E2E_KEEP_WORK_DIR:-0}
server_port=${SCOUT_PACKAGED_E2E_PORT:-18083}
image=${SCOUT_PACKAGED_E2E_IMAGE:-scout:packaged-e2e-$$}
container="scout-packaged-e2e-$$"
volume="scout-packaged-e2e-$$"
image_built=0

fail() {
  printf '%s\n' "scout packaged Playwright: $1" >&2
  exit 1
}

cleanup() {
  set +e
  docker rm --force "$container" >/dev/null 2>&1
  docker volume rm "$volume" >/dev/null 2>&1
  if [ "$image_built" = 1 ] && [ "${SCOUT_PACKAGED_E2E_KEEP_IMAGE:-0}" != 1 ]; then
    docker image rm "$image" >/dev/null 2>&1
  fi
  if [ "$keep_work_directory" != 1 ]; then
    rm -rf "$work_directory"
  fi
}
trap cleanup EXIT INT TERM

command -v docker >/dev/null 2>&1 || fail "Docker is required"
command -v curl >/dev/null 2>&1 || fail "curl is required"
mkdir -p "$work_directory"

if curl --silent --show-error --fail "http://127.0.0.1:$server_port/api/health/live" >/dev/null 2>&1; then
  fail "port $server_port is already in use"
fi

if [ "${SCOUT_PACKAGED_E2E_SKIP_BUILD:-0}" = 1 ]; then
  docker image inspect "$image" >/dev/null 2>&1 || fail "the supplied packaged image is unavailable"
else
  docker build \
    --file packaging/containers/server.Dockerfile \
    --tag "$image" \
    . >/dev/null
  image_built=1
fi
docker volume create "$volume" >/dev/null
docker run --detach \
  --name "$container" \
  --publish "127.0.0.1:$server_port:8080" \
  --env NODE_ENV=production \
  --env PORT=8080 \
  --env SCOUT_PORT=8080 \
  --env SCOUT_DATA_DIR=/data \
  --env SCOUT_PUBLIC_URL="http://127.0.0.1:$server_port" \
  --env SCOUT_DISCOVERY_CIDR=127.0.0.0/30 \
  --env SCOUT_DISCOVERY_SOURCE_ADDRESS=127.0.0.1 \
  --env SCOUT_DISCOVERY_INTERFACE=packaged-e2e \
  --env SCOUT_SSH_PORT=18022 \
  --volume "$volume:/data" \
  "$image" >/dev/null

docker exec --detach --user 0 "$container" node --input-type=commonjs -e \
  'const net = require("node:net"); net.createServer((socket) => socket.end()).listen(18022, "127.0.0.1");' \
  >/dev/null || fail "the disposable discovery target could not start"

target_attempt=1
while [ "$target_attempt" -le 30 ]; do
  if docker exec "$container" node --input-type=commonjs -e \
    'const net = require("node:net"); const socket = net.createConnection({ host: "127.0.0.1", port: 18022 }); socket.once("connect", () => { socket.destroy(); process.exit(0); }); socket.once("error", () => process.exit(1)); setTimeout(() => process.exit(1), 1000);' \
    >/dev/null 2>&1; then
    break
  fi
  sleep 1
  target_attempt=$((target_attempt + 1))
done
[ "$target_attempt" -le 30 ] || fail "the disposable discovery target did not become reachable"

attempt=1
while [ "$attempt" -le 90 ]; do
  if curl --silent --show-error --fail "http://127.0.0.1:$server_port/api/health/live" >/dev/null 2>&1; then
    break
  fi
  sleep 1
  attempt=$((attempt + 1))
done
[ "$attempt" -le 90 ] || fail "the packaged image did not become ready"

curl --silent --show-error --fail "http://127.0.0.1:$server_port/api/v1/setup" >/dev/null
setup_token=$(docker logs "$container" 2>&1 | sed -n 's/.*Scout owner setup token.*: \([[:alnum:]]*\)$/\1/p' | tail -1)
[ -n "$setup_token" ] || fail "the packaged image did not log a disposable setup token"
printf '%s' "$setup_token" >"$work_directory/setup-token"
chmod 0600 "$work_directory/setup-token"

PLAYWRIGHT_BASE_URL="http://127.0.0.1:$server_port" \
  SCOUT_E2E_PACKAGED=1 \
  SCOUT_E2E_SETUP_TOKEN_FILE="$work_directory/setup-token" \
  npm run test:e2e
