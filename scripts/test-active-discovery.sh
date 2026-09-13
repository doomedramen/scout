#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

go test ./internal/agent ./internal/control ./internal/discovery ./internal/policy ./internal/store ./tests/contracts -count=1
go test ./tests/integration -run TestServerAndAgentScansUseControlledListenersAndHonorExclusions -count=1
bash -n scripts/install-agent.sh

if [[ "${SCOUT_ACTIVE_DISCOVERY_LAB:-0}" != "1" ]]; then
	cat <<'EOF'
Fixture active-discovery checks passed. No Docker network or host packet capture was used.
Set SCOUT_ACTIVE_DISCOVERY_LAB=1 and SCOUT_ACTIVE_DISCOVERY_LAB_CONFIRM=YES only for an
owner-authorized disposable Linux Docker lab. The live lab uses isolated internal
Docker bridges and never scans the host LAN, public network, or production endpoint.
EOF
	exit 0
fi

if [[ "${SCOUT_ACTIVE_DISCOVERY_LAB_CONFIRM:-}" != "YES" ]]; then
	echo "Set SCOUT_ACTIVE_DISCOVERY_LAB_CONFIRM=YES only for an owner-authorized disposable Linux lab." >&2
	exit 2
fi
if [[ "$(uname -s)" != "Linux" ]]; then
	echo "The active-discovery lab requires Linux Docker bridges for packet capture." >&2
	exit 2
fi

require_command() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "$1 is required for the active-discovery lab" >&2
		exit 2
	}
}

for command_name in docker curl go ip jq tcpdump sudo; do
	require_command "$command_name"
done
docker info >/dev/null 2>&1 || {
	echo "Docker daemon is unavailable" >&2
	exit 2
}
sudo -n true >/dev/null 2>&1 || {
	echo "Passwordless sudo is required for bridge-scoped tcpdump capture" >&2
	exit 2
}

lab_dir="$(mktemp -d "${TMPDIR:-/tmp}/scout-active-discovery.XXXXXX")"
keep_lab="${SCOUT_ACTIVE_DISCOVERY_LAB_KEEP:-0}"
lab_id="scout-active-${PPID}-${RANDOM}"
container_names=()
network_names=()
capture_pids=()

cleanup() {
	local exit_code=$?
	set +e
	for pid in "${capture_pids[@]}"; do
		kill "$pid" >/dev/null 2>&1 || true
		wait "$pid" >/dev/null 2>&1 || true
	done
	if ((${#container_names[@]} > 0)); then
		docker rm -f "${container_names[@]}" >/dev/null 2>&1 || true
	fi
	if ((${#network_names[@]} > 0)); then
		docker network rm "${network_names[@]}" >/dev/null 2>&1 || true
	fi
	if [[ "$keep_lab" == "1" ]]; then
		echo "Active-discovery lab artifacts retained at $lab_dir" >&2
	else
		rm -rf -- "$lab_dir"
	fi
	exit "$exit_code"
}
trap cleanup EXIT

fail_lab() {
	echo "active-discovery lab: $*" >&2
	if [[ -n "${server_name:-}" ]]; then
		docker logs "$server_name" >&2 2>/dev/null || true
	fi
	exit 1
}

server_subnet_octet=$((RANDOM % 200 + 20))
server_subnet="172.30.${server_subnet_octet}.0/24"
agent_subnet="172.30.$((server_subnet_octet + 1)).0/24"
adjacent_subnet="172.30.$((server_subnet_octet + 2)).0/24"
server_net="${lab_id}-server"
agent_net="${lab_id}-agent"
adjacent_net="${lab_id}-adjacent"
network_names+=("$server_net" "$agent_net" "$adjacent_net")

server_ip="172.30.${server_subnet_octet}.10"
postgres_ip="172.30.${server_subnet_octet}.11"
server_target_ip="172.30.${server_subnet_octet}.20"
excluded_target_ip="172.30.${server_subnet_octet}.21"
closed_target_ip="172.30.${server_subnet_octet}.22"
agent_ip="172.30.$((server_subnet_octet + 1)).10"
agent_target_ip="172.30.$((server_subnet_octet + 1)).20"
adjacent_target_ip="172.30.$((server_subnet_octet + 2)).20"
host_port="${SCOUT_ACTIVE_DISCOVERY_LAB_PORT:-18087}"
db_password="ScoutDbAa1"
setup_token="${SCOUT_ACTIVE_DISCOVERY_LAB_SETUP_TOKEN:-scout-active-${PPID}-${RANDOM}}"
owner_password="${SCOUT_ACTIVE_DISCOVERY_LAB_PASSWORD:-ScoutAa1}"

if ! [[ "$host_port" =~ ^[0-9]+$ ]] || ((host_port < 1024 || host_port > 65535)); then
	fail_lab "SCOUT_ACTIVE_DISCOVERY_LAB_PORT must be between 1024 and 65535"
fi
if curl --silent --show-error --max-time 1 "http://127.0.0.1:${host_port}/api/status" >/dev/null 2>&1; then
	fail_lab "localhost port ${host_port} is already in use"
fi

CGO_ENABLED=0 go build -o "$lab_dir/scout-server" ./apps/server
CGO_ENABLED=0 go build -o "$lab_dir/scout-agent" ./apps/agent
chmod 0755 "$lab_dir/scout-server" "$lab_dir/scout-agent"

docker network create --internal --subnet "$server_subnet" "$server_net" >/dev/null
docker network create --internal --subnet "$agent_subnet" "$agent_net" >/dev/null
docker network create --internal --subnet "$adjacent_subnet" "$adjacent_net" >/dev/null

server_network_id="$(docker network inspect -f '{{.Id}}' "$server_net")"
agent_network_id="$(docker network inspect -f '{{.Id}}' "$agent_net")"
adjacent_network_id="$(docker network inspect -f '{{.Id}}' "$adjacent_net")"
server_bridge="br-${server_network_id:0:12}"
agent_bridge="br-${agent_network_id:0:12}"
adjacent_bridge="br-${adjacent_network_id:0:12}"
for bridge in "$server_bridge" "$agent_bridge" "$adjacent_bridge"; do
	ip link show "$bridge" >/dev/null 2>&1 || fail_lab "Docker bridge $bridge is unavailable for scoped capture"
done

start_capture() {
	local bridge=$1
	local output=$2
	sudo -n tcpdump -i "$bridge" -nn -U -w "$output" 'tcp' >"$output.log" 2>&1 &
	capture_pids+=("$!")
}

start_capture "$server_bridge" "$lab_dir/server.pcap"
start_capture "$agent_bridge" "$lab_dir/agent.pcap"
start_capture "$adjacent_bridge" "$lab_dir/adjacent.pcap"
sleep 1
for pid in "${capture_pids[@]}"; do
	kill -0 "$pid" >/dev/null 2>&1 || fail_lab "tcpdump exited before scanning"
done

postgres_name="${lab_id}-postgres"
server_name="${lab_id}-server"
agent_name="${lab_id}-agent"
server_target_name="${lab_id}-server-target"
excluded_target_name="${lab_id}-excluded-target"
closed_target_name="${lab_id}-closed-target"
agent_target_name="${lab_id}-agent-target"
adjacent_target_name="${lab_id}-adjacent-target"

docker run --rm -d --name "$postgres_name" --network "$server_net" --ip "$postgres_ip" \
	-e POSTGRES_USER=scout -e POSTGRES_DB=scout -e POSTGRES_PASSWORD="$db_password" postgres:17-alpine >/dev/null
container_names+=("$postgres_name")
for attempt in $(seq 1 60); do
	if docker exec "$postgres_name" pg_isready -U scout -d scout >/dev/null 2>&1; then
		break
	fi
	if [[ "$attempt" == "60" ]]; then
		fail_lab "PostgreSQL did not become ready"
	fi
	sleep 1
done

docker run --rm -d --name "$server_name" --network "$server_net" --ip "$server_ip" -p "127.0.0.1:${host_port}:8080" \
	-v "$lab_dir/scout-server:/usr/local/bin/scout-server:ro" -v "$lab_dir/server-data:/var/lib/scout" \
	-e SCOUT_PRODUCTION=false -e SCOUT_LISTEN=0.0.0.0:8080 \
	-e SCOUT_DATABASE_URL="postgres://scout:${db_password}@${postgres_name}:5432/scout?sslmode=disable" \
	-e SCOUT_SETUP_TOKEN="$setup_token" -e SCOUT_SECRET_KEY_FILE=/var/lib/scout/wrapping-key \
	-e SCOUT_AUTO_ENROLLMENT=true debian:bookworm-slim /usr/local/bin/scout-server >/dev/null
container_names+=("$server_name")

lab_base_url="http://127.0.0.1:${host_port}"
for attempt in $(seq 1 60); do
	if curl --silent --show-error --fail --max-time 2 "$lab_base_url/api/status" >/dev/null 2>&1; then
		break
	fi
	if [[ "$attempt" == "60" ]]; then
		fail_lab "Scout server did not become ready"
	fi
	sleep 1
done

cookie_jar="$lab_dir/cookies.txt"
setup_payload="$(jq -n --arg setupToken "$setup_token" --arg password "$owner_password" '{setupToken:$setupToken,password:$password}')"
curl --silent --show-error --fail --max-time 10 -c "$cookie_jar" -H 'Content-Type: application/json' \
	-d "$setup_payload" "$lab_base_url/api/v1/setup" >/dev/null
login_payload="$(jq -n --arg password "$owner_password" '{password:$password}')"
login_response="$(curl --silent --show-error --fail --max-time 10 -c "$cookie_jar" -b "$cookie_jar" -H 'Content-Type: application/json' \
	-d "$login_payload" "$lab_base_url/api/v1/sessions")"
csrf_token="$(jq -er '.csrfToken // empty' <<<"$login_response")" || fail_lab "Scout login omitted CSRF token"

owner_get() {
	curl --silent --show-error --fail --max-time 10 -b "$cookie_jar" "$lab_base_url$1"
}

owner_post() {
	local path=$1
	local body=$2
	local idempotency_key=${3:-}
	local headers=(-H 'Content-Type: application/json' -H "X-CSRF-Token: $csrf_token")
	if [[ -n "$idempotency_key" ]]; then
		headers+=(-H "Idempotency-Key: $idempotency_key")
	fi
	curl --silent --show-error --fail --max-time 10 -b "$cookie_jar" "${headers[@]}" -d "$body" "$lab_base_url$path"
}

site_payload="$(jq -n --arg name "${lab_id}-site" '{name:$name}')"
site_response="$(owner_post /api/v1/sites "$site_payload")" || fail_lab "site creation failed"
site_id="$(jq -er '.id // empty' <<<"$site_response")" || fail_lab "site creation omitted id"

server_scan_policy="$(jq -n '{serverEnabled:true,agentIds:[],scheduleSeconds:300,entryPoints:[{id:"ssh-default",name:"SSH",transport:"tcp",port:22,accessMethod:"ssh",enabled:true}],limits:{probesPerSecond:20,concurrency:1,targetBudget:3,attemptBudget:3,timeoutMilliseconds:500,runDeadlineSeconds:60,resultPageSize:10}}')"
server_scope_payload="$(jq -n --arg siteId "$site_id" --arg open "$server_target_ip" --arg excluded "$excluded_target_ip" --arg closed "$closed_target_ip" --argjson scanPolicy "$server_scan_policy" \
	'{siteId:$siteId,ranges:[$open,$excluded,$closed],exclusions:[$excluded],methods:["tcp"],ports:[22],enabled:true,scanPolicy:$scanPolicy}')"
server_scope_response="$(owner_post /api/v1/scopes "$server_scope_payload")" || fail_lab "server scope creation failed"
server_scope_id="$(jq -er '.id // empty' <<<"$server_scope_response")" || fail_lab "server scope omitted id"
server_policy_revision="$(jq -er '.scanPolicy.revision // empty' <<<"$server_scope_response")" || fail_lab "server scope omitted policy revision"

docker run --rm -d --name "$server_target_name" --network "$server_net" --ip "$server_target_ip" alpine:3.22 \
	sh -c 'while :; do nc -l -p 22 >/dev/null 2>&1 || sleep 0.1; done' >/dev/null
container_names+=("$server_target_name")
docker run --rm -d --name "$excluded_target_name" --network "$server_net" --ip "$excluded_target_ip" alpine:3.22 \
	sh -c 'while :; do nc -l -p 22 >/dev/null 2>&1 || sleep 0.1; done' >/dev/null
container_names+=("$excluded_target_name")
docker run --rm -d --name "$closed_target_name" --network "$server_net" --ip "$closed_target_ip" alpine:3.22 sleep infinity >/dev/null
container_names+=("$closed_target_name")
docker run --rm -d --name "$agent_target_name" --network "$agent_net" --ip "$agent_target_ip" alpine:3.22 \
	sh -c 'while :; do nc -l -p 22 >/dev/null 2>&1 || sleep 0.1; done' >/dev/null
container_names+=("$agent_target_name")
docker run --rm -d --name "$adjacent_target_name" --network "$adjacent_net" --ip "$adjacent_target_ip" alpine:3.22 \
	sh -c 'while :; do nc -l -p 22 >/dev/null 2>&1 || sleep 0.1; done' >/dev/null
container_names+=("$adjacent_target_name")

run_payload="$(jq -n --argjson expectedRevision "$server_policy_revision" '{expectedRevision:$expectedRevision,scanner:{kind:"server",id:"control-server"}}')"
server_run_response="$(owner_post "/api/v1/scopes/${server_scope_id}/scan-runs" "$run_payload" "${lab_id}-server-run")" || fail_lab "server scan creation failed"
server_run_id="$(jq -er '.id // empty' <<<"$server_run_response")" || fail_lab "server scan omitted id"

wait_for_scan_run() {
	local run_id=$1
	local state
	local response
	for attempt in $(seq 1 90); do
		response="$(owner_get "/api/v1/scan-runs/${run_id}")"
		state="$(jq -r '.state // empty' <<<"$response")"
		case "$state" in
		completed|partial|failed|cancelled|rejected|expired)
			echo "$state"
			return 0
			;;
		esac
		sleep 1
	done
	fail_lab "scan run ${run_id} did not reach terminal state"
}

wait_for_candidate() {
	local scope_id=$1
	local address=$2
	local state
	local response
	for attempt in $(seq 1 90); do
		response="$(owner_get "/api/v1/candidates?scopeId=${scope_id}")"
		state="$(jq -r --arg address "$address" '.items[]? | select(.address == $address) | .state' <<<"$response" | head -n 1)"
		if [[ "$state" == "needs_credentials" || "$state" == "discovered" || "$state" == "enrolled" || "$state" == "queued" ]]; then
			echo "$state"
			return 0
		fi
		sleep 1
	done
	fail_lab "candidate ${address} was not projected for scope ${scope_id}"
}

server_run_state="$(wait_for_scan_run "$server_run_id")"
server_candidate_state="$(wait_for_candidate "$server_scope_id" "$server_target_ip")"
server_run_detail="$(owner_get "/api/v1/scan-runs/${server_run_id}")"
server_open_count="$(jq -r '.outcomeCounts.open // 0' <<<"$server_run_detail")"
server_closed_count="$(jq -r '.outcomeCounts.closed // 0' <<<"$server_run_detail")"
if [[ "$server_open_count" != "1" || "$server_closed_count" != "1" ]]; then
	fail_lab "server-only run outcomes were not open=1, closed=1: $server_run_detail"
fi

invitation_payload="$(jq -n --arg displayName "${lab_id}-agent" --arg siteId "$site_id" '{displayName:$displayName,siteId:$siteId}')"
invitation_response="$(owner_post /api/v1/bootstrap-invitations "$invitation_payload")" || fail_lab "agent invitation creation failed"
agent_device_id="$(jq -er '.deviceId // empty' <<<"$invitation_response")" || fail_lab "agent invitation omitted device id"
invitation="$(jq -er '.invitation // empty' <<<"$invitation_response")" || fail_lab "agent invitation omitted token"
printf '%s\n' "$invitation" >"$lab_dir/invitation"
chmod 0600 "$lab_dir/invitation"

docker run --rm -d --name "$agent_name" --network "$server_net" --ip "$agent_ip" \
	-v "$lab_dir/scout-agent:/usr/local/bin/scout-agent:ro" debian:bookworm-slim sleep infinity >/dev/null
container_names+=("$agent_name")
docker network connect --ip "$agent_ip" "$agent_net" "$agent_name"
docker exec "$agent_name" mkdir -p /run/scout /var/lib/scout-agent
docker cp "$lab_dir/invitation" "$agent_name:/run/scout/invitation"
docker exec "$agent_name" chmod 0600 /run/scout/invitation
docker exec -d "$agent_name" /usr/local/bin/scout-agent --daemon --server "http://${server_name}:8080" \
	--invitation-file /run/scout/invitation --data-dir /var/lib/scout-agent --interval 1s >/dev/null

agent_id=""
for attempt in $(seq 1 60); do
	devices_response="$(owner_get /api/v1/devices)"
	agent_id="$(jq -r --arg deviceId "$agent_device_id" '.items[]? | select(.id == $deviceId) | .agentId // empty' <<<"$devices_response" | head -n 1)"
	if [[ -n "$agent_id" ]]; then
		break
	fi
	sleep 1
done
[[ -n "$agent_id" ]] || fail_lab "agent did not enroll"

agent_scan_policy="$(jq -n --arg agentId "$agent_id" '{serverEnabled:false,agentIds:[$agentId],scheduleSeconds:300,entryPoints:[{id:"ssh-default",name:"SSH",transport:"tcp",port:22,accessMethod:"ssh",enabled:true}],limits:{probesPerSecond:20,concurrency:1,targetBudget:1,attemptBudget:1,timeoutMilliseconds:500,runDeadlineSeconds:60,resultPageSize:10}}')"
agent_scope_payload="$(jq -n --arg siteId "$site_id" --arg target "$agent_target_ip" --argjson scanPolicy "$agent_scan_policy" \
	'{siteId:$siteId,ranges:[$target],exclusions:[],methods:["tcp"],ports:[22],enabled:true,scanPolicy:$scanPolicy}')"
agent_scope_response=""
for attempt in $(seq 1 60); do
	if agent_scope_response="$(owner_post /api/v1/scopes "$agent_scope_payload")"; then
		break
	fi
	sleep 1
done
[[ -n "$agent_scope_response" ]] || fail_lab "agent scope creation failed after capability wait"
agent_scope_id="$(jq -er '.id // empty' <<<"$agent_scope_response")" || fail_lab "agent scope omitted id"
agent_policy_revision="$(jq -er '.scanPolicy.revision // empty' <<<"$agent_scope_response")" || fail_lab "agent scope omitted policy revision"

agent_run_payload="$(jq -n --argjson expectedRevision "$agent_policy_revision" --arg agentId "$agent_id" --arg deviceId "$agent_device_id" \
	'{expectedRevision:$expectedRevision,scanner:{kind:"agent",id:$agentId,deviceId:$deviceId}}')"
agent_run_response="$(owner_post "/api/v1/scopes/${agent_scope_id}/scan-runs" "$agent_run_payload" "${lab_id}-agent-run")" || fail_lab "agent scan creation failed"
agent_run_id="$(jq -er '.id // empty' <<<"$agent_run_response")" || fail_lab "agent scan omitted id"
agent_run_state="$(wait_for_scan_run "$agent_run_id")"
agent_candidate_state="$(wait_for_candidate "$agent_scope_id" "$agent_target_ip")"
agent_run_detail="$(owner_get "/api/v1/scan-runs/${agent_run_id}")"
agent_open_count="$(jq -r '.outcomeCounts.open // 0' <<<"$agent_run_detail")"
if [[ "$agent_open_count" != "1" ]]; then
	fail_lab "agent-only run did not record one open endpoint: $agent_run_detail"
fi

echo "Server run and agent run completed; stopping bridge packet capture." >&2
for pid in "${capture_pids[@]}"; do
	kill "$pid" >/dev/null 2>&1 || true
	wait "$pid" >/dev/null 2>&1 || true
done
capture_pids=()

count_syn() {
	local capture=$1
	local address=$2
	sudo -n tcpdump -nn -r "$capture" "host ${address} and tcp dst port 22 and tcp[tcpflags] & tcp-syn != 0 and tcp[tcpflags] & tcp-ack == 0" 2>/dev/null | wc -l | tr -d ' '
}

server_target_syn="$(count_syn "$lab_dir/server.pcap" "$server_target_ip")"
excluded_target_syn="$(count_syn "$lab_dir/server.pcap" "$excluded_target_ip")"
agent_target_syn="$(count_syn "$lab_dir/agent.pcap" "$agent_target_ip")"
adjacent_target_syn="$(count_syn "$lab_dir/adjacent.pcap" "$adjacent_target_ip")"
if ((server_target_syn < 1 || agent_target_syn < 1)); then
	fail_lab "expected server and agent probes were not captured: server=${server_target_syn}, agent=${agent_target_syn}"
fi
if ((excluded_target_syn != 0 || adjacent_target_syn != 0)); then
	fail_lab "unauthorized probe captured: excluded=${excluded_target_syn}, adjacent=${adjacent_target_syn}"
fi

cat <<EOF
Active-discovery segmented lab passed.
Server-only scope: ${server_scope_id}; run ${server_run_id}; state ${server_run_state}; candidate ${server_candidate_state}.
Agent-only scope: ${agent_scope_id}; run ${agent_run_id}; state ${agent_run_state}; candidate ${agent_candidate_state}; agent ${agent_id}.
Packet SYN counts: server target ${server_target_syn}, excluded target ${excluded_target_syn}, agent target ${agent_target_syn}, adjacent target ${adjacent_target_syn}.
Topology: server ${server_subnet}, agent ${agent_subnet}, adjacent unauthorized ${adjacent_subnet}.
EOF
