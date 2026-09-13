#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

go test ./tests/integration -run TestRuntimeEnrollmentPersistenceAndReporting -count=1
bash -n scripts/install-agent.sh
scripts/install-agent.sh --help >/dev/null
if grep -q 'github.com/doomedramen/scout/releases' scripts/install-agent.sh; then
	echo "agent installer still depends on public release assets" >&2
	exit 1
fi
if ! grep -q '/api/v1/bootstrap/agent/' scripts/install-agent.sh; then
	echo "agent installer does not use the Scout bootstrap endpoint" >&2
	exit 1
fi
if ! grep -q 'SCOUT_OTI' scripts/install-agent.sh; then
	echo "agent installer does not support the one-command invitation flow" >&2
	exit 1
fi
if ! grep -q '__SCOUT_SERVER_URL__' scripts/install-agent.sh; then
	echo "agent installer does not expose the server URL placeholder" >&2
	exit 1
fi
if grep -q 'run this installer with sudo or as root' scripts/install-agent.sh; then
	echo "agent installer still requires the caller to prepend sudo" >&2
	exit 1
fi
if grep -q 'scout.example.invalid\|/usr/local/bin/scout-agent' packaging/linux/agent.service; then
	echo "agent service still contains a placeholder installation path" >&2
	exit 1
fi
if ! grep -q 'EnvironmentFile=-/etc/scout/agent.env' packaging/linux/agent.service; then
	echo "agent service does not load the installer environment" >&2
	exit 1
fi

if [[ "${SCOUT_SYSTEMD_LAB:-0}" == "1" ]]; then
	if [[ "$(uname -s)" != "Linux" ]] || ! command -v systemctl >/dev/null 2>&1; then
		echo "SCOUT_SYSTEMD_LAB=1 requires a Linux host with systemd" >&2
		exit 2
	fi
	if [[ "${SCOUT_SYSTEMD_LAB_CONFIRM:-}" != "YES" ]]; then
		echo "Set SCOUT_SYSTEMD_LAB_CONFIRM=YES only for an owner-authorized disposable Linux host." >&2
		exit 2
	fi

	for command_name in curl go install jq sha256sum stat useradd; do
		command -v "$command_name" >/dev/null 2>&1 || {
			echo "$command_name is required for the systemd acceptance lab" >&2
			exit 2
		}
	done

	if [[ "$(id -u)" -eq 0 ]]; then
		run_privileged() { "$@"; }
	else
		command -v sudo >/dev/null 2>&1 || {
			echo "sudo is required for the systemd acceptance lab when not running as root" >&2
			exit 2
		}
		sudo -v || {
			echo "sudo authorization failed" >&2
			exit 2
		}
		run_privileged() { sudo "$@"; }
	fi

	if [[ -e /etc/systemd/system/scout-agent.service || -e /usr/lib/systemd/system/scout-agent.service || -e /lib/systemd/system/scout-agent.service ]]; then
		echo "refusing the systemd acceptance lab because scout-agent.service already exists" >&2
		exit 2
	fi
	if id scout-agent >/dev/null 2>&1 || [[ -e /var/lib/scout/agent ]]; then
		echo "refusing the systemd acceptance lab because the Scout agent state already exists" >&2
		exit 2
	fi

	lab_dir="$(mktemp -d "${TMPDIR:-/tmp}/scout-first-agent.XXXXXX")"
	server_pid=""
	installation_started=0
	keep_lab="${SCOUT_SYSTEMD_LAB_KEEP:-0}"
	host_port="${SCOUT_SYSTEMD_LAB_PORT:-18089}"
	if ! [[ "$host_port" =~ ^[0-9]+$ ]] || ((host_port < 1024 || host_port > 65535)); then
		echo "SCOUT_SYSTEMD_LAB_PORT must be between 1024 and 65535" >&2
		exit 2
	fi
	server_url="http://127.0.0.1:${host_port}"
	if curl --silent --show-error --max-time 1 "$server_url/api/status" >/dev/null 2>&1; then
		echo "localhost port ${host_port} is already in use" >&2
		exit 2
	fi

	cleanup_systemd_lab() {
		local exit_code=$?
		set +e
		if [[ "$installation_started" == "1" ]]; then
			run_privileged systemctl disable --now scout-agent.service >/dev/null 2>&1 || true
			run_privileged rm -f /etc/systemd/system/scout-agent.service /etc/scout/agent.env /usr/local/libexec/scout-agent
			run_privileged systemctl daemon-reload >/dev/null 2>&1 || true
			run_privileged rm -rf -- /var/lib/scout/agent
			run_privileged userdel scout-agent >/dev/null 2>&1 || true
		fi
		if [[ -n "$server_pid" ]]; then
			kill "$server_pid" >/dev/null 2>&1 || true
			wait "$server_pid" >/dev/null 2>&1 || true
		fi
		if [[ "$keep_lab" == "1" ]]; then
			echo "systemd acceptance lab artifacts retained at $lab_dir" >&2
		else
			rm -rf -- "$lab_dir"
		fi
		exit "$exit_code"
	}
	trap cleanup_systemd_lab EXIT

	fail_lab() {
		echo "first-agent systemd lab: $*" >&2
		if [[ -f "$lab_dir/server.log" ]]; then
			tail -n 80 "$lab_dir/server.log" >&2 || true
		fi
		exit 1
	}

	target_arch="$(go env GOARCH)"
	case "$target_arch" in
	amd64|arm64) ;;
	*) fail_lab "unsupported Linux architecture: $target_arch" ;;
	esac

	install -d -m 0700 "$lab_dir/bootstrap"
	CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go build -trimpath -o "$lab_dir/scout-server" ./apps/server || fail_lab "Scout server build failed"
	CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go build -trimpath -o "$lab_dir/bootstrap/scout-agent-linux-$target_arch" ./apps/agent || fail_lab "Scout agent build failed"
	install -m 0700 scripts/install-agent.sh "$lab_dir/bootstrap/install-agent.sh"
	chmod 0755 "$lab_dir/scout-server" "$lab_dir/bootstrap/scout-agent-linux-$target_arch"

	setup_token="scout-systemd-${PPID}-${RANDOM}"
	owner_password="ScoutAa1"
	SCOUT_PRODUCTION=false \
	SCOUT_LISTEN="127.0.0.1:${host_port}" \
	SCOUT_SETUP_TOKEN="$setup_token" \
	SCOUT_SECRET_KEY_FILE="$lab_dir/wrapping-key" \
	SCOUT_AGENT_BOOTSTRAP_DIR="$lab_dir/bootstrap" \
	SCOUT_AGENT_INSTALLER_FILE="$lab_dir/bootstrap/install-agent.sh" \
	SCOUT_AUTO_ENROLLMENT=false \
	"$lab_dir/scout-server" >"$lab_dir/server.log" 2>&1 &
	server_pid=$!

	for attempt in $(seq 1 60); do
		if curl --silent --show-error --fail --max-time 2 "$server_url/api/status" >/dev/null 2>&1; then
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
		-d "$setup_payload" "$server_url/api/v1/setup" >/dev/null || fail_lab "owner setup failed"
	login_payload="$(jq -n --arg password "$owner_password" '{password:$password}')"
	login_response="$(curl --silent --show-error --fail --max-time 10 -c "$cookie_jar" -b "$cookie_jar" -H 'Content-Type: application/json' \
		-d "$login_payload" "$server_url/api/v1/sessions")" || fail_lab "owner sign-in failed"
	csrf_token="$(jq -er '.csrfToken // empty' <<<"$login_response")" || fail_lab "owner sign-in omitted CSRF token"

	owner_get() {
		curl --silent --show-error --fail --max-time 10 -b "$cookie_jar" "$server_url$1"
	}
	owner_post() {
		local path=$1
		local body=$2
		curl --silent --show-error --fail --max-time 10 -b "$cookie_jar" -H 'Content-Type: application/json' -H "X-CSRF-Token: $csrf_token" \
			-d "$body" "$server_url$path"
	}

	site_payload="$(jq -n '{name:"systemd-acceptance-site",addressContext:"disposable-localhost"}')"
	site_response="$(owner_post /api/v1/sites "$site_payload")" || fail_lab "site creation failed"
	site_id="$(jq -er '.id // empty' <<<"$site_response")" || fail_lab "site creation omitted id"
	invit_payload="$(jq -n --arg siteId "$site_id" '{displayName:"systemd-acceptance-agent",siteId:$siteId}')"
	invit_response="$(owner_post /api/v1/bootstrap-invitations "$invit_payload")" || fail_lab "invitation creation failed"
	device_id="$(jq -er '.deviceId // empty' <<<"$invit_response")" || fail_lab "invitation omitted device id"
	invit="$(jq -er '.invitation // empty' <<<"$invit_response")" || fail_lab "invitation omitted token"

	installer_body="$(curl --silent --show-error --fail --max-time 10 "$server_url/api/v1/bootstrap/agent/install.sh")" || fail_lab "server-served installer was unavailable"
	grep -Fq "$server_url" <<<"$installer_body" || fail_lab "server-served installer did not embed its request origin"
	installation_started=1
	SCOUT_OTI="$invit" bash -c "$installer_body" >"$lab_dir/installer.log" 2>&1 || fail_lab "server-served installer failed"

	identity_path=/var/lib/scout/agent/identity.json
	wait_for_device() {
		local expected_availability=$1
		local response
		local availability
		local discovered_agent_id
		for attempt in $(seq 1 90); do
			response="$(owner_get /api/v1/devices)"
			discovered_agent_id="$(jq -r --arg id "$device_id" '.items[]? | select(.id == $id) | .agentId // empty' <<<"$response" | head -n 1)"
			availability="$(jq -r --arg id "$device_id" '.items[]? | select(.id == $id) | .availability // empty' <<<"$response" | head -n 1)"
			if [[ -n "$discovered_agent_id" && "$availability" == "$expected_availability" ]]; then
				agent_id="$discovered_agent_id"
				return 0
			fi
			sleep 1
		done
		fail_lab "device did not reach availability $expected_availability"
	}
	wait_for_device online
	run_privileged systemctl is-active --quiet scout-agent.service || fail_lab "scout-agent.service is not active"

	identity_json="$(run_privileged cat "$identity_path")" || fail_lab "agent identity file was not created"
	identity_agent_id="$(jq -er '.agentId // empty' <<<"$identity_json")" || fail_lab "identity file omitted agent id"
	[[ "$identity_agent_id" == "$agent_id" ]] || fail_lab "server and local agent identities differ"
	identity_mode="$(run_privileged stat -c '%a' "$identity_path")" || fail_lab "agent identity permissions could not be read"
	[[ "$identity_mode" == "600" ]] || fail_lab "agent identity permissions were $identity_mode, expected 600"
	certificate_digest="$(run_privileged jq -r '.certificatePem' "$identity_path" | sha256sum | awk '{print $1}')"
	agent_token="$(jq -er '.agentToken // empty' <<<"$identity_json")" || fail_lab "identity file omitted agent token"

	run_privileged systemctl restart scout-agent.service || fail_lab "agent service restart failed"
	for attempt in $(seq 1 60); do
		current_identity_id="$(run_privileged jq -r '.agentId // empty' "$identity_path" 2>/dev/null || true)"
		if [[ "$current_identity_id" == "$identity_agent_id" ]]; then
			break
		fi
		if [[ "$attempt" == "60" ]]; then
			fail_lab "agent identity did not persist across service restart"
		fi
		sleep 1
	done
	wait_for_device online

	run_privileged sh -c 'printf "SCOUT_RENEWAL_LEAD_TIME=720h\\n" >> /etc/scout/agent.env' || fail_lab "renewal test configuration failed"
	run_privileged systemctl restart scout-agent.service || fail_lab "agent restart for renewal failed"
	renewed_certificate_digest="$certificate_digest"
	for attempt in $(seq 1 60); do
		renewed_certificate_digest="$(run_privileged jq -r '.certificatePem' "$identity_path" 2>/dev/null | sha256sum | awk '{print $1}' || true)"
		if [[ -n "$renewed_certificate_digest" && "$renewed_certificate_digest" != "$certificate_digest" ]]; then
			break
		fi
		if [[ "$attempt" == "60" ]]; then
			fail_lab "agent certificate did not renew under the configured lead time"
		fi
		sleep 1
	done
	renewed_expiry="$(run_privileged jq -r '.expiresAt // empty' "$identity_path")" || fail_lab "renewal omitted certificate expiry"
	[[ -n "$renewed_expiry" ]] || fail_lab "renewal certificate expiry was empty"

	run_privileged systemctl stop scout-agent.service || fail_lab "agent service stop failed"
	wait_for_device offline
	run_privileged systemctl start scout-agent.service || fail_lab "agent service recovery start failed"
	wait_for_device online

	revocation_payload='{"reason":"systemd acceptance revocation","uninstall":false}'
	owner_post "/api/v1/devices/${device_id}/decommission" "$revocation_payload" >/dev/null || fail_lab "device revocation failed"
	revoked_response="$lab_dir/revoked-response.json"
	revoked_status="$(curl --silent --show-error --max-time 10 -o "$revoked_response" -w '%{http_code}' -X POST \
		-H 'Content-Type: application/json' -H "Authorization: Bearer $agent_token" -d '{}' "$server_url/api/v1/agent/v1/heartbeat")"
	[[ "$revoked_status" == "401" ]] || fail_lab "revoked agent heartbeat returned HTTP $revoked_status"

	cat <<EOF
Native systemd first-agent lab passed.
Host: $(. /etc/os-release && printf '%s' "${PRETTY_NAME:-unknown}") / $(uname -m) / $(systemctl --version | sed -n '1p')
Device: ${device_id}; agent: ${agent_id}; service: scout-agent.service
Installer: server-served SCOUT_OTI one-command flow; identity mode ${identity_mode}
Restart: same agent identity persisted across systemd restart
Renewal: certificate digest changed; new expiry ${renewed_expiry}
Loss of contact: service stop projected availability offline, then recovered online
Revocation: bearer heartbeat rejected with HTTP ${revoked_status}
EOF
fi
