# Research and Decisions

**Date**: 2026-09-11. Primary source review and local static inspection; no hardware, load, or runtime acceptance is claimed.

## Confirmed product choices

The owner selected six areas: incidents/ntfy, systemd, diagnostics, rollups, SMART/ZFS, and sensors/GPUs; a complete implementation package; ntfy only; broad Linux hardware families; one-year history; automatically enabled in-app defaults; silence all severities during quiet hours; discover services and alert on failed units, with explicit must-run selection. OIDC, multi-user, additional operating systems, Podman, image checks, and remediation remain excluded. The active 001 pointer is preserved by restoring its saved contents after mutating setup/check helpers. Explicit selection alone is insufficient: these helpers persist the override. Read-only path checks use --paths-only.

## Beszel as a reference

Reviewed revision [f204dc17e64bba444478c60210d018e43a777193](https://github.com/henrygd/beszel/tree/f204dc17e64bba444478c60210d018e43a777193), cloned at `/tmp/scout-beszel-review`. The earlier [feature review](../../docs/research/beszel-feature-review.md) identifies scope overlap. Source paths at that revision: `internal/alerts/`, `internal/records/records.go`, `agent/systemd.go`, `agent/smart.go`, `agent/zfs/`, `agent/sensors.go`, vendor GPU collectors, and chart components.

Decision: adapt behaviors through Scout's existing contracts. Rationale: Scout's scope/identity/release boundaries differ. Alternative rejected: adopting PocketBase or transplanting the entire agent. No Beszel source is copied by this documentation change. Any later source reuse must retain applicable notices and undergo dependency review.

## Storage and transactional work

Decision: normalize telemetry and keep incident/outbox/rollup state in PostgreSQL. Local evidence: `internal/store/store.go` rewrites `workspace_state.state_json`; current-series state indexes by metric name. Those paths cannot safely establish bounded multi-entity history at the target volume. Alternative rejected: add year-long aggregates to the same serialized workspace snapshot.

Use transactional row locks and unique keys for evaluation/outbox claims. PostgreSQL documents row and advisory lock behavior in [explicit locking](https://www.postgresql.org/docs/17/explicit-locking.html). Locks protect local consistency; they cannot provide exactly-once effects at ntfy. Specific schema and lease handling are Scout design decisions, not claims supplied by PostgreSQL.

## Notifications

Decision: direct ntfy JSON publication with optional Bearer token, title, priority, and Scout link. The [ntfy publishing documentation](https://docs.ntfy.sh/publish/) describes these fields and authentication. Rationale: the owner requested one provider, so a multi-provider framework adds unnecessary scope. Alternatives: Shoutrrr, SMTP, generic webhooks; deferred. Queueing, suppression, retries and acceptance-versus-receipt semantics remain Scout responsibilities.

Decision: retain in-app incident history during every quiet window and send a current summary after suppression ends. Reject replaying every queued transition: it creates stale noise. No remote content fetching or actionable HTTP buttons in messages.

## Linux services and disk diagnostics

Decision: read-only systemd D-Bus loaded-service inventory using a maintained pinned Go client. [systemd D-Bus API](https://www.freedesktop.org/software/systemd/man/org.freedesktop.systemd1.html) defines unit listing and states. Alternative rejected: shelling out to parse localized systemctl output or collecting journal content. State collection is required; per-service CPU/memory metrics are not added to 002.

Decision: derive disk rates and latency from procfs counters. [Linux I/O statistics](https://docs.kernel.org/admin-guide/iostats.html) defines fields; counter resets and no operations require distinct handling. Do not infer latency from utilization, or interpret sectors as physical-sector size. CPU guest accounting must avoid double counting.

## Storage collectors

Decision: use installed smartctl JSON with exit-bitmask interpretation, non-waking standby policy, and family-specific fields. [smartctl source/options](https://www.smartmontools.org/static/doxygen/smartctl_8cpp_source.html) is the primary interface reference. Alternative rejected: parse human output or assume all nonzero exits mean no usable data. Access to raw devices is a separately provisioned privilege; field absence is not good health.

Decision: fixed read-only OpenZFS commands plus exposed kernel counters. [OpenZFS documentation](https://openzfs.github.io/openzfs-docs/) and [pool statistics source](https://github.com/openzfs/zfs/blob/master/module/zfs/spa_stats.c) explain supported data sources. Version-specific scrub parsing needs fixtures and live verification; no physical/usable capacity conflation. Alternative rejected: shell commands supplied by owner configuration or automatic repair.

## Sensors and GPUs

Decision: read Linux hwmon with per-field capabilities. [hwmon ABI](https://docs.kernel.org/hwmon/sysfs-interface.html) specifies units and optional files. Zero RPM and negative temperature remain valid. Alternative rejected: naming channels from global hwmon indexes or applying a universal thermal threshold.

Decision: NVIDIA uses fixed [nvidia-smi](https://docs.nvidia.com/deploy/nvidia-smi/index.html) queries; AMD/Intel use exposed DRM/hwmon fields. [AMDGPU thermal/sysfs documentation](https://docs.kernel.org/gpu/amdgpu/thermal.html) explains available vendor data. [NVML device queries](https://docs.nvidia.com/deploy/nvml-api/api/group__nvmlDeviceQueries.html) illustrate that fields can be unsupported or permission-limited. Alternative deferred: mandatory NVML/cgo, ROCm, or privileged perf-based Intel polling. This narrower collection interface still targets all three vendor families with honest per-field support.

## Technical defaults and remaining evidence

Timing, retry, retention and cardinality choices in plan.md are explicit engineering defaults, not owner-supplied measurements. Dependencies are pinned during T002 against the repository toolchain and recorded with compatibility evidence. Research uncertainty about actual hardware is resolved by a required support matrix and release gates, not an unspecified implementation choice. Missing lab access must remain visible in evidence.md.
