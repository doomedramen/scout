#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

go test ./internal/collector/sensors ./internal/collector/gpu ./internal/collector -race -count=1
bash -n scripts/test-hardware-monitoring.sh

if [[ "${SCOUT_HARDWARE_LAB:-0}" != "1" ]]; then
	cat <<'EOF'
Fixture sensor and GPU collector checks passed. No hwmon, DRM, NVIDIA, or
host utility path was contacted.
Set SCOUT_HARDWARE_LAB=1 and SCOUT_HARDWARE_LAB_CONFIRM=YES only for an
owner-authorized disposable Linux hardware check. The live path is read-only.
EOF
	exit 0
fi

if [[ "${SCOUT_HARDWARE_LAB_CONFIRM:-}" != "YES" ]]; then
	echo "Set SCOUT_HARDWARE_LAB_CONFIRM=YES only for an owner-authorized disposable Linux hardware check." >&2
	exit 2
fi
if [[ "$(uname -s)" != "Linux" ]]; then
	echo "The hardware lab check requires Linux; no live hardware evidence was collected." >&2
	exit 2
fi
if ! timeout_path=$(command -v timeout); then
	echo "The hardware lab check requires a timeout utility; no live evidence was collected." >&2
	exit 2
fi

temp_dir=$(mktemp -d "${TMPDIR:-/tmp}/scout-hardware-monitoring.XXXXXX")
cleanup() {
	rm -rf "$temp_dir"
}
trap cleanup EXIT

printf 'Hardware lab identity: uid=%s groups=%s kernel=%s arch=%s\n' "$(id -u)" "$(id -Gn)" "$(uname -r)" "$(uname -m)"

hwmon_count=$(find /sys/class/hwmon -mindepth 1 -maxdepth 1 -type l 2>/dev/null | wc -l | tr -d ' ')
printf 'hwmon preflight: channels-root entries=%s; fixed read paths only\n' "$hwmon_count"

amd_cards=0
intel_cards=0
shopt -s nullglob
drm_cards=(/sys/class/drm/card[0-9]*)
for card in "${drm_cards[@]}"; do
	card_name=${card##*/}
	if [[ ! "$card_name" =~ ^card[0-9]+$ || ! -r "$card/device/vendor" ]]; then
		continue
	fi
	vendor=$(tr -d '[:space:]' <"$card/device/vendor" 2>/dev/null || true)
	case "$vendor" in
		0x1002) amd_cards=$((amd_cards + 1)) ;;
		0x8086) intel_cards=$((intel_cards + 1)) ;;
	esac
done
shopt -u nullglob
printf 'DRM preflight: AMD cards=%s; Intel cards=%s; no device mutation performed\n' "$amd_cards" "$intel_cards"

nvidia_path=/usr/bin/nvidia-smi
if [[ -x "$nvidia_path" ]]; then
	set +e
"$timeout_path" --signal=TERM 12s "$nvidia_path" \
		--query-gpu=index,uuid,pci.bus_id,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,driver_version \
		--format=csv,noheader,nounits >"$temp_dir/nvidia.csv" 2>"$temp_dir/nvidia.err"
	nvidia_status=$?
	set -e
	nvidia_rows=$(awk 'NF { count++ } END { print count + 0 }' "$temp_dir/nvidia.csv")
	nvidia_bytes=$(wc -c <"$temp_dir/nvidia.csv" | tr -d ' ')
	printf 'NVIDIA preflight: fixed query exit=%s; rows=%s; bytes=%s; raw output suppressed\n' "$nvidia_status" "$nvidia_rows" "$nvidia_bytes"
else
	echo 'NVIDIA preflight: /usr/bin/nvidia-smi not installed; no NVIDIA evidence collected.'
fi

cat <<'EOF'
This live path performs only bounded reads of /sys and the collector's fixed
nvidia-smi query under a timeout. It does not use sudo, load drivers, wake or
stress hardware, change power settings, write sysfs, or expose raw device
identifiers. Record exact Linux/kernel/driver versions, grants, and supported
fields separately before advertising a hardware family as live-supported.
EOF
