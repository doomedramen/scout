# Beszel feature review for Scout

Reviewed 2026-09-11. Recommendations for development after spec 001; not approved requirements or changes to its implementation tasks.

## Evidence and limits

Cloned `https://github.com/henrygd/beszel.git` to `/tmp/scout-beszel-review`, pinned at `f204dc17e64bba444478c60210d018e43a777193`. Links below point to that revision. This was a targeted static source review, not a runtime comparison, performance benchmark, security audit, or full feature inventory. Scout was inspected during active development, including uncommitted files; implementation observations are a snapshot, not release claims.

Compared against `specs/001-scout-platform/spec.md`, `docs/roadmap.md`, `docs/service-collectors.md`, and current host collection, retention, and device-chart code.

## Recommended additions, in priority order

### 1. Alerting and incident history

**Source evidence:** Beszel evaluates resource thresholds over configurable time windows. Its alerts_history.go records trigger and resolution transitions; alerts.go supports quiet hours, email, and Shoutrrr delivery. [Source](https://github.com/henrygd/beszel/blob/f204dc17e64bba444478c60210d018e43a777193/internal/alerts/alerts_system.go).

**Scout proposal:** Add offline, sustained CPU/memory, filesystem capacity, and collector-failure rules first. Show active and resolved incidents, with host links and notification tests. Add per-scope maintenance suppression, deduplication, recovery notifications, and a bounded delivery queue. Missing telemetry must remain unknown, never resolve an incident as healthy.

**Priority and scope:** High value; medium–large effort. External notifications and advanced rules are explicitly later scope in 001. Start the next specification here.

### 2. Linux service health

**Source evidence:** Beszel reads systemd over D-Bus, supports service selection patterns, and reports service states. internal/alerts/alerts_systemd.go handles failed-service alerts. [Source](https://github.com/henrygd/beszel/blob/f204dc17e64bba444478c60210d018e43a777193/agent/systemd.go).

**Scout proposal:** Add a systemd collector for selected critical units. Show active, failed, inactive, and unavailable separately. Link each service to its host and alert on unexpected failure. Keep collection read-only; no restart controls.

**Priority and scope:** High value; medium effort. Fits the planned collector contract but systemd is not an explicit reference adapter in 001.

### 3. Diagnostic host metrics

**Source evidence:** Beszel represents CPU user/system/iowait/steal breakdown, load averages, and disk throughput, utilization, and latency. [Source](https://github.com/henrygd/beszel/blob/f204dc17e64bba444478c60210d018e43a777193/internal/entities/system/system.go).

**Scout proposal:** Extend Scout beyond aggregate CPU and capacity: CPU iowait/steal, load, swap, and per-device disk I/O. Add matching unit-aware charts. These explain slow hosts that still have free RAM and disk space. Handle counter reset and unsupported fields explicitly.

**Priority and scope:** High value; medium effort. Current internal/collector/host.go emits aggregate CPU, memory, filesystem, uptime, and interface traffic. These diagnostic fields extend 001’s baseline.

### 4. Long-term history with rollups

**Source evidence:** Beszel aggregates shorter records into progressively longer intervals and has separate retention deletion code. [Source](https://github.com/henrygd/beszel/blob/f204dc17e64bba444478c60210d018e43a777193/internal/records/records.go).

**Scout proposal:** Keep recent raw samples and retain coarser history for longer ranges. Store sample counts, coverage, minima/maxima, and averages so peaks and missing periods remain visible. Bound query point counts and expose resolution in the chart. Choose intervals after measuring Scout’s PostgreSQL workload.

**Priority and scope:** High value; medium–large effort. 001 already requires bounded retention; current internal/telemetry/retention.go prunes samples. Durable multi-resolution history is additional scope.

### 5. Disk and storage-pool health

**Source evidence:** Beszel discovers and collects SMART devices using smartctl. agent/zfs/zfs.go exposes pool capacity, health, I/O counters, and datasets with bounded command execution. [Source](https://github.com/henrygd/beszel/blob/f204dc17e64bba444478c60210d018e43a777193/agent/smart.go).

**Scout proposal:** Add separate SMART and ZFS collectors: failing disks, wear, degraded pools, scrub state, and dataset usage. Surface denied access and unsupported devices. Keep stable device identity and distinguish physical allocation from usable capacity.

**Priority and scope:** High value for NAS/Proxmox deployments; medium–large effort. Additional provider scope, dependent on the collector framework and real hardware testing.

### 6. Hardware sensors and GPUs

**Source evidence:** Beszel supports sensor selection and collection timeouts. agent/gpu.go and vendor-specific implementations cover GPU collection. [Source](https://github.com/henrygd/beszel/blob/f204dc17e64bba444478c60210d018e43a777193/agent/sensors.go).

**Scout proposal:** Add temperature and fan monitoring first where hardware exposes it. Add GPU utilization, memory, and power as an optional adapter for GPU hosts. Preserve per-sensor availability; do not force one universal temperature threshold.

**Priority and scope:** Medium value, deployment-dependent; medium–large effort across hardware. Prioritize after service and disk health unless GPU hosts dominate the fleet.

### 7. More useful chart interactions

**Source evidence:** Beszel supports multiple series, legends, configurable domains/formatters, and limits redraws when charts are offscreen. [Source](https://github.com/henrygd/beszel/blob/f204dc17e64bba444478c60210d018e43a777193/internal/site/src/components/charts/line-chart.tsx).

**Scout proposal:** Use metric-aware units and domains, per-interface/per-disk series selection, and bounded rendering. Preserve keyboard access and explicit gaps. Scout’s current device view has CPU/memory/disk percentage charts; enrich these as new metrics arrive.

**Priority and scope:** Medium value; small–medium effort. General chart usability already belongs to 001; these are concrete refinements rather than a new chart feature from scratch.

### 8. Container image update visibility

**Source evidence:** Beszel normalizes image references, skips digest-pinned references, and checks updates in a background batch separate from metrics collection. [Source](https://github.com/henrygd/beszel/blob/f204dc17e64bba444478c60210d018e43a777193/agent/docker_image_updates.go).

**Scout proposal:** Consider an opt-in “image update available” indicator later. Show check time and registry errors; never imply a newer image is secure or compatible. Preserve isolated-network operation and avoid automatic container updates.

**Priority and scope:** Lower priority; medium effort. Requires registry access and rate/credential handling beyond 001’s read-only runtime metrics.

## Existing scope and architectural boundaries

Docker resource/history collection, Proxmox, backups and restore, selectable history ranges, compact inventory, and stale/offline states already belong to spec 001. Use Beszel to refine those implementations rather than count them as new roadmap additions. Multi-user and OIDC can wait for an actual shared-administration need; Scout currently targets one owner.

Keep Scout’s Go/PostgreSQL architecture, device-bound identities, scoped discovery, credential boundaries, and independently verified server-delivered updates. Beszel’s PocketBase model is not a reason to replace them. Adapt collector ideas through Scout’s versioned contract, with deadlines, permission states, and bounded entity counts. Any later source reuse needs a separate dependency and license review; no third-party code was copied into Scout by this review.

## Suggested next phase

After 001 acceptance, specify actionable monitoring: alert rules and incident history, quiet hours and notification delivery, a systemd collector, and diagnostic host metrics. Give rollups a separate storage milestone; then add SMART/ZFS and hardware-specific collectors according to the deployment mix.

Acceptance should include sustained failure and recovery, interrupted telemetry, notification outages, maintenance windows, server restart, and unavailable collector permissions. Rollup acceptance should prove bounded query cost and preservation of gaps and peaks. These are proposed checks for future work, not additions to the current task list.
