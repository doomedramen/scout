#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

go test ./internal/collector/smart ./internal/collector/zfs ./internal/alerts -race -count=1
bash -n scripts/test-storage-health.sh

if [[ "${SCOUT_STORAGE_LAB:-0}" != "1" ]]; then
	cat <<'EOF'
Fixture SMART, ZFS, and storage-alert checks passed. No storage utility or
host device was contacted.
Set SCOUT_STORAGE_LAB=1 and SCOUT_STORAGE_LAB_CONFIRM=YES only for an
owner-authorized disposable Linux storage lab. The live path is read-only.
EOF
	exit 0
fi

if [[ "${SCOUT_STORAGE_LAB_CONFIRM:-}" != "YES" ]]; then
	echo "Set SCOUT_STORAGE_LAB_CONFIRM=YES only for an owner-authorized disposable Linux storage lab." >&2
	exit 2
fi
if [[ "$(uname -s)" != "Linux" ]]; then
	echo "The storage lab preflight requires Linux; no live storage evidence was collected." >&2
	exit 2
fi

temp_dir=$(mktemp -d "${TMPDIR:-/tmp}/scout-storage-health.XXXXXX")
cleanup() {
	rm -rf "$temp_dir"
}
trap cleanup EXIT

printf 'Storage lab identity: uid=%s groups=%s\n' "$(id -u)" "$(id -Gn)"

if ! smartctl_path=$(command -v smartctl); then
	echo "smartctl is not installed; SMART live evidence is unavailable." >&2
else
	smartctl_version=$($smartctl_path --version 2>/dev/null | sed -n '1p' || true)
	set +e
	"$smartctl_path" --scan -j >"$temp_dir/smart-scan.json" 2>"$temp_dir/smart-scan.err"
	smart_status=$?
	set -e
	smart_bytes=$(wc -c <"$temp_dir/smart-scan.json" | tr -d ' ')
	printf 'SMART preflight: %s; fixed scan exit=%s; JSON bytes=%s; raw device output suppressed\n' "${smartctl_version:-unknown}" "$smart_status" "$smart_bytes"
	if [[ "$smart_status" -ne 0 ]]; then
		echo "SMART scan did not complete successfully; inspect permissions in the disposable lab without copying raw output." >&2
	fi
fi

if ! zpool_path=$(command -v zpool); then
	echo "zpool is not installed; ZFS live evidence is unavailable." >&2
else
	zpool_version=$("$zpool_path" --version 2>/dev/null | sed -n '1p' || true)
	set +e
	"$zpool_path" list -H -p -o name,guid,size,allocated,health >"$temp_dir/zpool-list.txt" 2>"$temp_dir/zpool-list.err"
	zpool_list_status=$?
	"$zpool_path" status -p >"$temp_dir/zpool-status.txt" 2>"$temp_dir/zpool-status.err"
	zpool_status=$?
	set -e
	zpool_count=$(awk 'NF { count++ } END { print count + 0 }' "$temp_dir/zpool-list.txt")
	printf 'ZFS pool preflight: %s; list exit=%s; status exit=%s; pools=%s; raw status suppressed\n' "${zpool_version:-unknown}" "$zpool_list_status" "$zpool_status" "$zpool_count"
fi

if ! zfs_path=$(command -v zfs); then
	echo "zfs is not installed; ZFS dataset live evidence is unavailable." >&2
else
	set +e
	"$zfs_path" list -H -p -o name,guid,used,available >"$temp_dir/zfs-list.txt" 2>"$temp_dir/zfs-list.err"
	zfs_status=$?
	set -e
	zfs_count=$(awk 'NF { count++ } END { print count + 0 }' "$temp_dir/zfs-list.txt")
	printf 'ZFS dataset preflight: list exit=%s; datasets=%s; raw dataset output suppressed\n' "$zfs_status" "$zfs_count"
fi

cat <<'EOF'
This live path performs only the fixed read queries above. It does not use
sudo, grant capabilities, wake disks, start SMART tests, create or destroy a
pool, scrub, repair, import, export, or mutate a dataset. Record exact Linux,
smartmontools, OpenZFS, architecture, permission grants, and fixture-backed
replacement/ambiguous-identity results separately before advertising support.
EOF
