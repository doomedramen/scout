# Implementation Plan: Advanced Monitoring and Alerting

**Branch**: `main` | **Date**: 2026-09-11 | **Spec**: [spec.md](spec.md)

## Summary

Extend 001 with a durable incident evaluator, ntfy outbox, read-only Linux collectors, and multi-resolution telemetry. Deliver the seven user journeys in tasks.md order. This package defines future implementation; no feature code is delivered here.

## Technical Context

- Language/runtime: repository Go 1.26.0 and existing Node/npm toolchain; preserve locked React/TypeScript/shadcn/Recharts dependencies.
- Storage: existing PostgreSQL 17 and pgx; normalized telemetry and 002 tables. No Redis, TimescaleDB, PocketBase, or second database.
- Libraries: standard Go HTTP/time/JSON and existing secrets/auth services; use a pinned maintained Go systemd D-Bus client for systemd only. Collect SMART/ZFS through fixed allowlisted utilities. AMD/Intel/sensors use read-only sysfs; NVIDIA uses fixed nvidia-smi queries, avoiding a required cgo/NVML build dependency.
- Platforms: systemd Ubuntu 24.04 and Debian 12, AMD64/ARM64, subject to 001 acceptance. GPU families are capability-dependent; no promise of every architecture/vendor combination.
- Tests: Go unit and PostgreSQL integration tests, existing web checks/build, Playwright journeys, disposable Linux/ntfy/ZFS labs, and representative physical hardware.

## Current baseline and dependency gate

On 2026-09-11 the checkout was actively changing. `internal/store/store.go` loads and rewrites `workspace_state.state_json`; `internal/store/telemetry.go` appends to in-memory sample arrays. Declared relational tables are not proof that ingestion uses them. `Device.CurrentMetrics` is keyed by metric name, which cannot distinguish multiple disks. Resolve both before 002 evaluation/history workloads.

T001 re-inspects actual 001 completion and records evidence. Required 001 capabilities: protected owner/MFA and agent identity (T010–T014), authenticated history/UI (T015–T017, T046–T049), recovery and retention (T018–T022), signed agent delivery (US5), registry/scheduler/provider entity UI (T050–T055). Task numbering is local to 001. Unfinished capabilities remain 001 obligations; do not duplicate its registry or mark it complete from this package. 002 release acceptance waits for 001 acceptance; fixtures and documentation can progress independently.

002 T003–T005 own telemetry normalization if still needed after the gate. Retain non-telemetry legacy state for compatibility while moving samples, receipts, current-series projection, evaluator work, and all new 002 records to SQL. A single server process remains the supported deployment; SQL leases protect worker restart and accidental duplicate execution, not a new HA guarantee.

## Constitution Check

Pre-design and post-design: pass. Scope/enrollment authority is unchanged; all credentials remain owner-scoped and outside agents; freshness and provenance persist; collectors ship through existing signed releases; permission discovery does not grant access; no remote shell or remediation is added. Core incidents need no network service. Recovery pauses external delivery. No constitution amendment or model exception is required.

## Project Structure

- `internal/alerts/`: rule resolution, durable evaluation, incident transitions, suppression and notification outbox; `internal/notifications/ntfy/`: destination validation and HTTP publishing.
- `internal/store/`: additive migrations and SQL repositories; `internal/telemetry/`: current-series projection, rollups, tier selection, retention coordination.
- `internal/collector/`: extend host diagnostics and add systemd, smart, zfs, sensors, gpu adapters through the 001 registry.
- `internal/control/` and `api/`: authenticated contracts and handlers; `apps/web/src/views/`: incidents/settings/service/hardware/history views using shared components.
- `tests/integration/`, `tests/e2e/`, `tests/fixtures/`, `scripts/`: deterministic acceptance and live-lab runners. New paths in tasks are intended implementation outputs, not existing files.

## Evaluation and timing

Use accepted, deduplicated telemetry plus a 15-second offline/reconciliation sweep. Persist a bounded dirty-entity work queue; workers lock effective rule/entity state. Numeric continuity uses strictly increasing observation time and a maximum gap of three configured collection intervals. Only samples no older than that freshness budget and no more than five seconds future-skewed advance evaluation. Older/skewed observations can remain historical with unavailable evaluation evidence. Offline status uses server receipt time and 001 heartbeat semantics.

A gap resets pending trigger/recovery timers. Existing incidents remain open with unknown evidence. Equal-to-threshold values do not satisfy strict comparisons. Values between clear and trigger thresholds retain an active episode but reset recovery; they reset a pending trigger. No arithmetic average substitutes for continuous threshold satisfaction. Replayed/backfilled measurements never create retrospective notifications.

Rule lineage selection: device override, then site override, then fleet definition. One site per device is inherited from 001. Overrides replace the entire condition/timing/severity/enabled tuple. At most one override per lineage and target; different custom lineages may intentionally create separate incidents. Target matching uses device IDs/site IDs, never network address scopes that imply enrollment authorization.

Any effective-condition revision administratively closes its existing episode with `rule_changed` and resets timing; disable/retire/decommission use explicit closure reasons, not recovered notifications. Re-acknowledgment is idempotent. Rule ownership changes from site reassignment apply the same revision reset. Default creation is idempotent; later software updates never overwrite owner-edited values or re-enable disabled rules.

Default CPU/memory/filesystem rules are warning, >90 for 300 seconds, <85 for 120 seconds. Offline is critical at the inherited 90-second threshold and recovers on a fresh accepted heartbeat. Failed units and explicit SMART/ZFS fault states are critical on first fresh fault, recover after two healthy samples separated by at least their collection interval. Degraded enabled collectors are warning after two consecutive failed scheduled attempts, clear after two successes. Selected must-run inactive units are warning after 60 seconds, clear after 60 seconds active; transitional states do not count as healthy or failed. Metadata-only discovered/disabled/needs-access collectors do not generate generic degradation defaults. Known explicit sensor hardware fault flags may use the same state-fault rule; generic thresholds remain owner-configured.

## Bounds and defaults

| Concern | Default / hard maximum |
| --- | --- |
| Rules and overrides | 1000 lineages, 10,000 overrides per workspace |
| Evaluation work | one dirty item per entity; 100,000 items; reconcile sweep recovers missed scheduling |
| Destinations / windows | 10 destinations; 1000 windows |
| Notification queue | 10,000 pending rows; excess counted, no unbounded payload storage |
| Delivery | 4 workers; one in flight per destination; 60 requests/minute/destination; 10-second timeout |
| Retry | initial plus retries after 30s, 2m, 10m, 30m, 2h; ±20% jitter; expire at 24h |
| History | max 600 points/series, 16 series/request, range <=365 days |
| Incident retention | resolved/admin-closed episodes and deliveries: 365 / 30 days; active episodes not age-deleted |
| Active incidents | 100,000; admission failure counted and surfaced; existing episodes still evaluated |
| Collector output | inherit 1 MiB/1000 samples/1000 observations per batch; split larger bounded snapshots |
| Per-host entities | 1000 systemd services, 64 SMART disks, 32 pools, 1000 datasets, 256 sensors, 32 GPUs |
| Retention | 30-day raw, 90-day five-minute, 365-day hourly, configurable only to shorter values in 002 |

Cap violations expose truncation/lag, never clean health. Reference load is 100 hosts with 40 numeric series each at 15s and up to 50 service states each at 30s. This yields 691.2 million raw points for 30 days, 103.68 million five-minute rows for 90 days, and 35.04 million hourly rows/year. At a provisional 160 bytes/row these total roughly 133 GB before index/WAL/backup overhead; reserve 500 GB SSD for the load lab and measure actual bytes. This estimate is not a deployment promise. Use the inherited 4-vCPU/8-GiB control host initially and publish measurements; do not hide failing targets by reducing the dataset. The existing one-million-sample cap must be replaced by configured disk/row budgets appropriate to the reference dataset during T005, not silently exhausted in hours.

## Migration, compatibility and rollout

1. Backup using 001 recovery tooling. Stop ingestion during a maintenance migration (agents use bounded spool); retain a pre-migration backup. Advisory-lock migration and record checkpoints.
2. Materialize required legacy device/agent/collector foreign-key references first without changing authority; copy legacy retained samples and receipts to normalized tables idempotently, preserve IDs, verify counts and representative hashes, build full series keys, then atomically mark the telemetry storage generation authoritative. Resume only when parity passes. Stop and retry from checkpoint on failure.
3. Strip migrated samples/receipts from subsequent legacy state writes; all ingestion/history/current queries use SQL. Leave legacy backup intact for rollback. Use one transaction for receipt, samples, current projection, and dirty work; publish no incident work on failed ingestion. Do not dual-write indefinitely.
4. Add rules/defaults and new tables, with notification enablement false. Upgrade server first. Older agents keep base features; new capabilities appear only after supported signed agent upgrade. Unknown collector schemas are quarantined from evaluation with visible diagnostics.
5. Backfill only retained observations in bounded chunks. Rollup coverage starts at actual data, not deployment anniversary. Expand to supported lab devices before release.
6. Restore validates storage generation and recovers all new records/credentials with separate keys. Pause delivery, cancel old queued transmissions, reset pending timing, and require fresh evaluation after owner reconciliation. Send a current-active summary only after resume, never an old outbox replay. App downgrade after SQL cutover requires restoring the matching old backup; no unsupported in-place down migration.

## Validation and release gates

Follow [acceptance-matrix.md](acceptance-matrix.md) and [quickstart.md](quickstart.md). SQL transaction/crash tests are mandatory; in-memory mocks cannot prove persistence. Security tests cover unauthorized mutations, secret reads, destination redirects/address validation, revoked identities, and restore suppression. Publish measured load/storage results and exact hardware versions in evidence.md before marking corresponding tasks complete. A missing hardware lab blocks its support claim, not fixture development. See [handoff.md](handoff.md) for execution boundaries.
