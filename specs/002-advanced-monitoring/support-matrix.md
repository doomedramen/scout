# Required Support Matrix

Status at specification delivery: all runtime rows unvalidated. These are mandatory target families, not universal hardware promises. T002 records pinned dependency versions; T021/T034/T038 record actual tested releases and device models. A supported family may expose only a subset of metrics; publish that subset.

| Family | Required environment / evidence | Status |
| --- | --- | --- |
| Linux baseline | Ubuntu 24.04 and Debian 12; AMD64/ARM64 supported by 001; exact kernel and systemd | Unvalidated |
| systemd | Loaded active/inactive/failed/transitional services, selected must-run, denied bus | Unvalidated |
| Host diagnostics | procfs counters, reset, zero swap, multiple disks, identity replacement | Unvalidated |
| SMART SATA | Physical ATA-family disk, smartctl version, read grants, JSON/exit status, standby | Unvalidated |
| SMART SAS | Physical SCSI-family disk, smartctl version and supported error/health fields | Unvalidated |
| SMART NVMe | Physical NVMe device, smartctl version, critical warning/wear/temperature fields | Unvalidated |
| ZFS | Linux OpenZFS version, disposable pool, GUIDs, scrub/capacity/rate evidence, read grants | Unvalidated |
| Sensors | Real exposed temperature/fan channels, hwmon driver and stable path evidence | Fixture adapter validated; live unvalidated |
| NVIDIA | Device model/UUID, Linux driver and nvidia-smi version, fields/architecture actually supported | Fixture parser validated; live unvalidated |
| AMD | Device model, amdgpu/kernel versions, exposed VRAM/busy/hwmon fields and power scope | Fixture adapter validated; live unvalidated |
| Intel | Device model, i915/xe/kernel versions, exposed DRM/hwmon fields and unavailable fields | Fixture adapter validated; live unvalidated |
| ntfy | Exact receiver version, token/no-token, TLS and allowed private HTTP cases, retries | Unvalidated |

For each completed row record commit/date, operator-authorized environment, commands with secrets redacted, fixtures versus live evidence, permissions, supported/unsupported fields, pass/fail results, and linked acceptance criteria. No forced failure or destructive disk/GPU operation is required; use recorded fixtures for dangerous fault cases.

The fixture-backed hardware results are intentionally separate from live
support. `scripts/test-hardware-monitoring.sh` runs the bounded sensor/GPU
adapter suites by default without contacting host hardware. Its opt-in Linux
path requires explicit confirmation, uses only fixed read paths and a bounded
NVIDIA query, and reports counts/statuses without raw identifiers. No live
sensor or GPU lab was available for this release checkpoint.
