# Collector and Metric Contract

Extend 001's registry, descriptors, config validation, bounded scheduler, and signed agent releases. Transport stays protocolVersion=1; add versioned collector descriptors and metrics, not a new device identity. Existing host metric names remain stable. New host fields use host schemaVersion=2; server accepts both 1 and 2. New collectors start at schemaVersion=1. Server-first deployment and capability advertisement prevent sending unsupported schemas to older servers.

## Common rules

Each entity has provider, host namespace, opaque stable ID <=128 bytes, display name, kind, capabilities, observedAt, and availability. Hash long natural IDs into deterministic host-scoped identifiers and retain safe evidence metadata. Distinguish unavailable field from zero, absent service from access denied, and fresh inventory from cached data. Display name changes never change metric identity. Explicit `hardware_fault` observations carry supported boolean fault type and source; absence never supplies false.

Collection is non-overlapping, context-cancelled and bounded. Default schedules below may be increased by owner but not decreased below listed interval in 002. State freshness expires at three intervals; cached values never get a new observation timestamp. Registration/detection alone grants no device or command privileges. Read diagnostics exclude stdout dumps, environment variables, credentials, and journals. Split bounded snapshots into <=1000-sample/1-MiB batches; no more than one snapshot buffered per collector beyond 001's spool. Report truncation and do not retire entities from partial snapshots.

| Provider | Interval / timeout | Identity and collection |
| --- | --- | --- |
| host diagnostics | 15s / 5s | `/proc/stat`, `/proc/loadavg`, `/proc/meminfo`, `/proc/diskstats`; block sysfs identity |
| systemd | 30s / 5s | read-only system D-Bus ListUnitsByPatterns for *.service; host + unit name; loaded units only |
| smart | 300s / 10s per device, 60s total | smartctl JSON, max two concurrent devices, host + WWN/serial/model or explicit ambiguous fallback |
| zfs | 60s / 10s per command, 20s total | fixed zpool/zfs read commands + available kernel counters; pool/dataset GUID |
| sensors | 30s / 5s | hwmon device path/chip + channel; never hwmonN alone |
| gpu | 30s / 10s | vendor UUID where available, otherwise host PCI identity + device evidence; no card index alone |

## Catalog

All quantities use the canonical transport units below; display formatting may scale them.

| Metrics | Units / derivation |
| --- | --- |
| cpu.user_percent, cpu.system_percent, cpu.iowait_percent, cpu.steal_percent | percent from nonnegative procfs deltas; avoid double counting guest time already included in user/nice |
| load.1m, load.5m, load.15m | count, not percent; no division by CPU count in stored metric |
| swap.used, swap.capacity | bytes; zero capacity is valid absence of swap, no division by zero |
| disk.read_rate, disk.write_rate | bytes_per_second; diskstats sectors *512, not physical sector size |
| disk.utilization | percent from I/O ticks / elapsed time, expose anomalous values as unavailable rather than silently clamping |
| disk.read_latency, disk.write_latency | milliseconds from read/write time delta divided by completed operation delta; no operations = unavailable latency |
| smart.temperature, smart.wear_percent, smart.error_count | celsius / percent / count; wear is only exposed where normalized semantics are documented; NVMe percentage-used may exceed 100 |
| zfs.pool.used, zfs.pool.capacity, zfs.dataset.used, zfs.dataset.available | bytes; pool allocation is physical, dataset availability is usable; never sum them as independent free space |
| zfs.pool.read_rate, zfs.pool.write_rate | bytes_per_second from stable cumulative counters; import/reset creates a gap |
| sensor.temperature, sensor.fan_speed | celsius / rpm; negative temperature and zero rpm can be real |
| gpu.utilization, gpu.memory.used, gpu.memory.capacity, gpu.temperature, gpu.power | percent / bytes / bytes / celsius / watts; field-level capability descriptors |

Systemd and ZFS scrub/health use typed observations, not invented numeric percentages. Service activeState maps to active/inactive/failed/transitional; unsupported states become unavailable with raw state name sanitized. Expected-running selectors use glob syntax `*` and `?` only, max 100 patterns of 128 chars, no regex or path expansion. Failed units always use service_failed; must-run inactivity never duplicates the failed incident. Enabled-unit discovery for units never loaded is outside 002; UI labels inventory as observed/loaded services.

## Provider details and permissions

SMART: use installed trusted smartctl path from packaging, fixed `--scan -j` and read-only JSON queries. Decode exit-status bitmask together with JSON; health bits can coexist with valid output. Do not start tests. Default avoids waking standby drives; sleeping/permission-denied values become unavailable or stale without a wake-up retry. Bridge type is an enum of validated smartctl types, never arbitrary options. No serial-only cross-host merging; duplicated IDs within a host require distinct ambiguous entities. Raw-device access is explicit local provisioning; never automatically add sudo or broad capabilities.

ZFS: fixed zpool list/get GUID/status and zfs list/get GUID queries with numeric/tabular output, LC_ALL=C, bounded parsing, and no shell. Scrub progress/state comes from bounded status output with supported-version fixtures. Read-only query permission is verified in the lab; no scrub, import, export, destroy, set or repair operations. Empty successful enumeration is absent, failed enumeration is unavailable. GUID change means replacement; missing GUID uses a marked unstable host/name identity and no automatic historical merge after disappearance.

Sensors: read only hwmon ABI files; convert millidegrees to celsius, preserve zero fan speed and supported negative temperatures. Resolve device symlinks within the configured host sysfs root; bounded enumeration without arbitrary file access. Labels optional; exclusion IDs persisted. Alarm/fault flags are distinct from generic thresholds.

GPU: NVIDIA uses installed nvidia-smi fixed query fields and CSV noheader/nounits with timeout; parse N/A per field. AMD uses amdgpu sysfs busy/VRAM/hwmon fields; Intel uses exposed DRM/hwmon metrics including energy delta for power, with unavailable gaps on reset. Do not invent utilization from clock speed or allocate shared system RAM as VRAM. Hardware exposing only some fields is supported only for those fields. Integrated power may cover a platform/package: capability and chart labels must identify that scope, never silently label it GPU-only. No perf privilege escalation, arbitrary utility args, ROCm requirement, or intel_gpu_top fallback in 002. Broader driver APIs can be a later adapter revision.

## Support evidence

Required representative families: SATA ATA JSON, SAS SCSI JSON, NVMe JSON; OpenZFS on Linux; one temperature/fan hwmon fixture and real exposed channel; one supported NVIDIA, AMD, and Intel GPU device/driver. Record fields supported/unavailable, exact versions, architecture, commands/paths, grants, and timeout behavior. Lack of lab evidence leaves that family unvalidated and blocks a claim of full 002 support. It does not justify changing unsupported fields to zero or claiming every Linux GPU works.

## Default fault predicates

SMART hardware_fault is true only for a supported failing overall-health result or nonzero NVMe critical-warning bitmask; error counters or percentage-used alone never imply that boolean. ZFS hardware_fault is true for DEGRADED, FAULTED, UNAVAIL, or SUSPENDED pool health; ONLINE is healthy, unknown/unparseable states are unavailable. Scrub errors reported explicitly as data errors count as a storage fault; a scrub in progress alone does not. Hardware-provided hwmon fault/alarm flags are mapped only when their documented channel semantics establish a fault. Sensor thresholds, SMART wear thresholds, and GPU usage thresholds remain owner-created numeric rules. Record original bounded state/flag evidence with the normalized predicate.
