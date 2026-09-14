#!/bin/sh
set -eu

umask 077

script_directory=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
repository_directory=$(CDPATH="" cd -- "$script_directory/.." && pwd)
work_directory=${SCOUT_QEMU_WORK_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/scout-qemu-acceptance.XXXXXX")}
keep_work_directory=${SCOUT_QEMU_KEEP_WORK_DIR:-0}
server_port=${SCOUT_QEMU_SERVER_PORT:-18084}
target_ssh_port=${SCOUT_QEMU_TARGET_SSH_PORT:-2222}
server_image=${SCOUT_QEMU_IMAGE:-scout:qemu-amd64}
docker_platform=${SCOUT_QEMU_DOCKER_PLATFORM:-linux/amd64}
base_image_url=${SCOUT_QEMU_BASE_IMAGE_URL:-https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2}
test_username=${SCOUT_QEMU_USERNAME:-scouttest}
test_password=${SCOUT_QEMU_PASSWORD:-ScoutTest1}
server_container="scout-qemu-server-$$"
server_volume="scout-qemu-data-$$"
qemu_pid=""

fail() {
  printf '%s\n' "scout QEMU acceptance: $1" >&2
  exit 1
}

cleanup() {
  set +e
  if [ -n "$server_container" ]; then docker rm --force "$server_container" >/dev/null 2>&1; fi
  if [ -n "$server_volume" ]; then docker volume rm "$server_volume" >/dev/null 2>&1; fi
  if [ -n "$qemu_pid" ]; then kill "$qemu_pid" >/dev/null 2>&1; fi
  if [ "$keep_work_directory" != 1 ]; then rm -rf "$work_directory"; fi
}
trap cleanup EXIT INT TERM

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

wait_for_tcp() {
  host=$1
  port=$2
  label=$3
  attempts=$4
  attempt=1
  while [ "$attempt" -le "$attempts" ]; do
    if nc -z -w 1 "$host" "$port" >/dev/null 2>&1; then return 0; fi
    sleep 1
    attempt=$((attempt + 1))
  done
  fail "$label did not become reachable on $host:$port"
}

ssh_target() {
  sshpass -p "$test_password" ssh \
    -p "$target_ssh_port" \
    -o ConnectTimeout=5 \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    -o PreferredAuthentications=password \
    -o PubkeyAuthentication=no \
    "$test_username@127.0.0.1" "$@"
}

wait_for_target_login() {
  attempt=1
  while [ "$attempt" -le 90 ]; do
    if ssh_target 'test -f /var/lib/cloud/instance/boot-finished && systemctl is-active --quiet ssh' >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
    attempt=$((attempt + 1))
  done
  fail "the disposable Debian target did not finish cloud-init"
}

make_seed_iso() {
  seed_directory="$work_directory/seed"
  mkdir -p "$seed_directory"
  password_hash=$(openssl passwd -6 -salt scouttest "$test_password")
  cat >"$seed_directory/user-data" <<EOF
#cloud-config
hostname: scout-linux-target
manage_etc_hosts: true
ssh_pwauth: true
users:
  - name: $test_username
    gecos: Scout disposable test user
    groups: [sudo]
    sudo: ["ALL=(ALL) ALL"]
    shell: /bin/bash
    lock_passwd: false
    passwd: '$password_hash'
packages:
  - openssh-server
  - sudo
  - ca-certificates
  - curl
runcmd:
  - systemctl enable --now ssh
  - sh -c "printf 'Port 22\\nPasswordAuthentication yes\\n' > /etc/ssh/sshd_config.d/scout-test.conf"
  - systemctl restart ssh
EOF
  cat >"$seed_directory/meta-data" <<EOF
instance-id: scout-qemu-acceptance
local-hostname: scout-linux-target
EOF

  if command -v cloud-localds >/dev/null 2>&1; then
    cloud-localds "$work_directory/seed.iso" "$seed_directory/user-data" "$seed_directory/meta-data"
  elif command -v hdiutil >/dev/null 2>&1; then
    hdiutil makehybrid -o "$work_directory/seed.iso" -hfs -joliet -iso \
      -default-volume-name CIDATA "$seed_directory" >/dev/null
  elif command -v genisoimage >/dev/null 2>&1; then
    genisoimage -output "$work_directory/seed.iso" -volid CIDATA -joliet -rock "$seed_directory" >/dev/null
  elif command -v mkisofs >/dev/null 2>&1; then
    mkisofs -output "$work_directory/seed.iso" -volid CIDATA -joliet -rock "$seed_directory" >/dev/null
  else
    fail "cloud-localds, hdiutil, genisoimage, or mkisofs is required to create the NoCloud seed"
  fi
}

require_command curl
require_command docker
require_command nc
require_command openssl
require_command qemu-img
require_command qemu-system-x86_64
require_command ssh
require_command sshpass
mkdir -p "$work_directory"

if nc -z -w 1 127.0.0.1 "$server_port" >/dev/null 2>&1; then
  fail "server port $server_port is already in use"
fi
if nc -z -w 1 127.0.0.1 "$target_ssh_port" >/dev/null 2>&1; then
  fail "target SSH port $target_ssh_port is already in use"
fi

base_image="$work_directory/debian.qcow2"
if [ ! -f "$base_image" ]; then
  curl --fail --silent --show-error --location --retry 2 "$base_image_url" -o "$base_image"
fi
qemu-img create -f qcow2 -F qcow2 -b "$base_image" "$work_directory/target.qcow2" >/dev/null
make_seed_iso

if ! docker image inspect "$server_image" >/dev/null 2>&1; then
  docker build --platform "$docker_platform" -f "$repository_directory/packaging/containers/server.Dockerfile" -t "$server_image" "$repository_directory"
fi

qemu-system-x86_64 \
  -name scout-qemu-acceptance \
  -machine q35,accel=tcg \
  -cpu max \
  -m 1024 \
  -smp 2 \
  -display none \
  -serial "file:$work_directory/qemu-console.log" \
  -monitor none \
  -drive "if=virtio,format=qcow2,file=$work_directory/target.qcow2" \
  -drive "if=virtio,format=raw,readonly=on,file=$work_directory/seed.iso" \
  -netdev "user,id=net0,hostfwd=tcp:0.0.0.0:$target_ssh_port-:22" \
  -device virtio-net-pci,netdev=net0 \
  >/dev/null 2>&1 &
qemu_pid=$!
wait_for_tcp 127.0.0.1 "$target_ssh_port" "QEMU SSH" 90
wait_for_target_login

docker_host_ip=$(docker run --rm --platform "$docker_platform" --add-host=host.docker.internal:host-gateway \
  --entrypoint node "$server_image" -e \
  'require("dns").lookup("host.docker.internal", {family: 4}, (error, address) => { if (error) process.exit(1); console.log(address) })' \
  2>/dev/null) || fail "Docker could not resolve its host gateway"
[ -n "$docker_host_ip" ] || fail "Docker host gateway address is empty"

docker volume create "$server_volume" >/dev/null
docker run --detach --platform "$docker_platform" --name "$server_container" \
  --add-host=host.docker.internal:host-gateway \
  --publish "$server_port:$server_port" \
  --env PORT="$server_port" \
  --env SCOUT_PORT="$server_port" \
  --env SCOUT_DATA_DIR=/data \
  --env SCOUT_PUBLIC_URL="http://10.0.2.2:$server_port" \
  --env SCOUT_DISCOVERY_CIDR="$docker_host_ip/32" \
  --env SCOUT_DISCOVERY_SOURCE_ADDRESS="$docker_host_ip" \
  --env SCOUT_SSH_PORT="$target_ssh_port" \
  --volume "$server_volume:/data" \
  "$server_image" >/dev/null
wait_for_tcp 127.0.0.1 "$server_port" "Scout server" 60

server_url="http://127.0.0.1:$server_port"
cookie_file="$work_directory/cookies"
curl --fail --silent --show-error --retry 10 --retry-all-errors --retry-delay 1 \
  "$server_url/api/v1/setup" >/dev/null
setup_token=$(docker logs "$server_container" 2>&1 | sed -n 's/.*Scout owner setup token.*: \([[:alnum:]]*\)$/\1/p' | tail -1)
[ -n "$setup_token" ] || fail "Scout did not log a disposable setup token"

curl --fail --silent --header 'content-type: application/json' \
  --header "origin: $server_url" \
  --data "{\"setupToken\":\"$setup_token\",\"username\":\"owner\",\"password\":\"ValidPass1\"}" \
  "$server_url/api/v1/setup/owner" >/dev/null
curl --fail --silent --cookie-jar "$cookie_file" --cookie "$cookie_file" \
  --header 'content-type: application/json' \
  --header "origin: $server_url" \
  --data '{"username":"owner","password":"ValidPass1","rememberMe":true,"callbackURL":"/systems"}' \
  "$server_url/api/auth/sign-in/username" >/dev/null

systems=''
system_id=''
attempt=1
while [ "$attempt" -le 30 ]; do
  systems=$(curl --fail --silent --cookie "$cookie_file" "$server_url/api/v1/systems")
  system_id=$(printf '%s' "$systems" | sed -n 's/.*"systems":\[{"id":"\([^"]*\)".*/\1/p')
  if [ -n "$system_id" ]; then break; fi
  sleep 1
  attempt=$((attempt + 1))
done
[ -n "$system_id" ] || fail "Scout did not discover the disposable Debian target"

preflight=$(curl --fail --silent --cookie "$cookie_file" "$server_url/api/v1/systems/$system_id/access-preflight")
fingerprint=$(printf '%s' "$preflight" | sed -n 's/.*"fingerprint":"\([^"]*\)".*/\1/p')
[ -n "$fingerprint" ] || fail "Scout did not read the disposable target fingerprint"
idempotency_key=$(openssl rand -hex 16)
grant=$(curl --fail --silent --cookie "$cookie_file" \
  --header 'content-type: application/json' \
  --header "origin: $server_url" \
  --header "Idempotency-Key: $idempotency_key" \
  --data "{\"method\":\"ssh\",\"username\":\"$test_username\",\"authType\":\"password\",\"secret\":\"$test_password\",\"passphrase\":null,\"privilegePassword\":null,\"fingerprint\":\"$fingerprint\",\"trust\":true,\"scope\":\"exact-host\"}" \
  "$server_url/api/v1/systems/$system_id/access-grants")
job_id=$(printf '%s' "$grant" | sed -n 's/.*"jobId":"\([^"]*\)".*/\1/p')
[ -n "$job_id" ] || fail "Scout did not create an enrollment job"

job=''
attempt=1
while [ "$attempt" -le 90 ]; do
  job=$(curl --fail --silent --cookie "$cookie_file" "$server_url/api/v1/enrollment-jobs/$job_id")
  case "$job" in
    *'"status":"complete"'*) break ;;
    *'"status":"failed"'* | *'"status":"blocked"'*)
      printf '%s\n' "$job" >&2
      fail "the real Linux enrollment job did not complete"
      ;;
  esac
  sleep 1
  attempt=$((attempt + 1))
done
case "$job" in *'"status":"complete"'*) ;; *) fail "the Linux enrollment job timed out" ;; esac

system=''
attempt=1
while [ "$attempt" -le 30 ]; do
  system=$(curl --fail --silent --cookie "$cookie_file" "$server_url/api/v1/systems/$system_id")
  case "$system" in *'"status":"online"'*'"lastTelemetryAt":'*) break ;; esac
  sleep 1
  attempt=$((attempt + 1))
done
case "$system" in *'"status":"online"'*) ;; *) fail "the enrolled target did not report telemetry" ;; esac
ssh_target 'systemctl is-enabled scout-agent.service && systemctl is-active scout-agent.service' >/dev/null
ssh_target "printf '%s\\n' '$test_password' | sudo -S -p '' systemctl restart scout-agent.service" >/dev/null
sleep 5
ssh_target 'systemctl is-active scout-agent.service' >/dev/null
ssh_target "printf '%s\\n' '$test_password' | sudo -S -p '' systemctl reboot" >/dev/null 2>&1 || true
wait_for_tcp 127.0.0.1 "$target_ssh_port" "rebooted target SSH" 90
wait_for_target_login
ssh_target 'systemctl is-active scout-agent.service' >/dev/null
sleep 5
system=$(curl --fail --silent --cookie "$cookie_file" "$server_url/api/v1/systems/$system_id")
case "$system" in *'"status":"online"'*) ;; *) fail "the target did not recover telemetry after reboot" ;; esac

printf '%s\n' "Scout Linux QEMU acceptance passed: discovered $system_id, installed and enrolled the agent, received telemetry, restarted the service, and recovered after reboot."
