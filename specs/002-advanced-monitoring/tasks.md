# Tasks: Advanced Monitoring and Alerting

All tasks are unimplemented. Requirement IDs refer to this feature; prefix 001 references explicitly. Read [plan.md](plan.md), [contracts/](contracts/) and [acceptance-matrix.md](acceptance-matrix.md). Tests below are required by the approved acceptance plan. Commit only owned changes and record real evidence before checking a task complete.

## Setup and foundations

Independent gate: Verify 001 dependencies, executable contracts and SQL parity before feature behavior.

- [X] T001 Record current commit, dirty-work boundaries, 001 prerequisite evidence and gaps in specs/002-advanced-monitoring/evidence.md; verify 001 registry/recovery/identity rather than inferring from checkboxes (FR-024, FR-032, FR-035).
- [X] T002 Translate contracts into api/openapi.yaml and api/schemas/ fixtures under tests/contracts/; pin the systemd client and record utility/driver test targets in specs/002-advanced-monitoring/research.md (FR-003, FR-008, FR-009, FR-014, FR-022, FR-032, FR-034).
- [X] T003 Implement additive/checkpointed telemetry and 002 schema migrations in internal/store/migrations.go and internal/store/monitoring_migration.go, including legacy import parity and explicit cutover generation (FR-004, FR-010, FR-020, FR-024; SC-008).
- [X] T004 Move receipt/sample/current-series/dirty-work transactions into internal/store/telemetry.go and internal/store/monitoring.go; use full series identity and remove migrated samples from legacy snapshot writes (FR-004, FR-005, FR-018, FR-023, FR-024; SC-002, SC-008).
- [X] T005 Add rollback-on-error, replay, SQL concurrency, legacy migration interruption and disk-budget fixtures in tests/integration/monitoring_storage_test.go; replace the one-million-sample cap with configured measured budgets (FR-018, FR-023, FR-024, FR-033, FR-035; SC-008, SC-011).
## Automatic incidents (P1)

Independent gate: Open, acknowledge and recover one episode under deterministic telemetry and restart.

- [X] T006 [US1] Add clock-controlled threshold/state/revision/gap fixtures in internal/alerts/evaluator_test.go covering strict boundaries, hysteresis, acknowledgment and duplicate/out-of-order evidence (FR-001, FR-002, FR-003, FR-004, FR-005, FR-007; SC-001, SC-002).
- [X] T007 [US1] Implement idempotent default provisioning and lineage/site/device override resolution in internal/alerts/rules.go and internal/store/alert_rules.go (FR-001, FR-002, FR-003; SC-001).
- [X] T008 [US1] Implement persisted dirty work, 15-second sweep, evaluator state and transactional incident transitions in internal/alerts/evaluator.go and internal/store/incidents.go (FR-002, FR-004, FR-005, FR-007, FR-033; SC-001, SC-002).
- [X] T009 [US1] Implement rule/override CRUD, paginated incident/transition reads and idempotent acknowledgment in internal/control/alerts.go with owner authorization and revision tests (FR-003, FR-004, FR-005, FR-007, FR-009; SC-002, SC-011).
- [X] T010 [US1] Add incident list/detail, acknowledgment, rule editor and automatic-default explanation in apps/web/src/views/incidents.tsx and apps/web/src/lib/api.ts; wire navigation through apps/web/src/App.tsx (FR-001, FR-003, FR-004, FR-005, FR-019; SC-010).
- [X] T011 [US1] Test restart, rule disable/retirement, target changes, admission caps and decommission closure in tests/integration/incidents_test.go; record US1 evidence (FR-004, FR-005, FR-007, FR-033; SC-001, SC-002, SC-011).
## ntfy and suppression (P1)

Independent gate: Publish to a controlled receiver, suppress all severities and recover without stale replay.

- [X] T012 [US2] Add ntfy test receiver, token redaction, address-validation, TLS, redirect, timeout and response-loss fixtures in internal/notifications/ntfy/client_test.go (FR-008, FR-009, FR-010; SC-003, SC-011).
- [X] T013 [US2] Implement encrypted destination storage, metadata-only reads, test endpoint and direct publishing client in internal/store/notification_destinations.go, internal/control/notifications.go and internal/notifications/ntfy/client.go (FR-008, FR-009; SC-003, SC-011).
- [X] T014 [US2] Implement transactional delivery intents, epoch leases, retries, expiry, queue caps and acceptance status in internal/alerts/delivery.go and internal/store/notification_deliveries.go (FR-010, FR-013, FR-033; SC-003, SC-011).
- [X] T015 [US2] Implement recurring/one-time window CRUD, union matching, DST semantics, host-offline suppression and release summaries in internal/alerts/suppression.go and internal/control/suppression.go (FR-006, FR-011, FR-012; SC-004).
- [X] T016 [US2] Integrate restore pause, outbox cancellation, fresh re-evaluation and MFA resume in internal/control/recovery.go and internal/alerts/recovery.go; test revocation races in tests/integration/notifications_test.go (FR-009, FR-010, FR-013, FR-024; SC-003, SC-008).
- [X] T017 [US2] Add ntfy destination/test/status and quiet-hour settings in apps/web/src/views/notifications.tsx; cover overlapping windows, overnight and DST release, resolved suppression, and disabled destinations in tests/e2e/notifications.spec.ts (FR-008, FR-010, FR-011, FR-012, FR-019; SC-003, SC-004, SC-010).
## systemd health (P1)

Independent gate: Inspect failed and selected must-run units on a controlled host.

- [X] T018 [US3] Create systemd fixtures for loaded active/inactive/failed/transitional units, must-run selection, partial discovery and denial in internal/collector/systemd/collector_test.go (FR-014, FR-015, FR-016, FR-032; SC-005).
- [X] T019 [US3] Implement bounded read-only D-Bus systemd adapter and descriptor in internal/collector/systemd/collector.go through the 001 registry; expose must-run config and fresh/partial inventory (FR-014, FR-015, FR-016, FR-032; SC-005).
- [X] T020 [US3] Connect service-state observations to default rules and host-offline suppression in internal/alerts/services.go; add state-transition and must-run dedup tests (FR-001, FR-006, FR-014, FR-015; SC-001, SC-005).
- [X] T021 [US3] Extend apps/web/src/views/services.tsx with state/freshness, must-run selectors and incident links; add scripts/test-systemd.sh for controlled Linux evidence (FR-014, FR-015, FR-016, FR-019, FR-034; SC-005, SC-009, SC-010).
## Diagnostic metrics (P1)

Independent gate: Known counters produce accurate per-device values and accessible charts.

- [X] T022 [US4] Add CPU guest accounting, disk reset/no-operations, swap-zero and device replacement fixtures in internal/collector/diagnostics_test.go (FR-017, FR-018; SC-006).
- [X] T023 [US4] Implement host schema v2 diagnostic readers and stable block identity in internal/collector/diagnostics.go and internal/collector/host.go, preserving schema v1 ingestion (FR-017, FR-018, FR-024, FR-032; SC-006).
- [X] T024 [US4] Extend apps/web/src/views/device.tsx and shared chart components with metric units/domains, per-entity selection and explicit partial/gap rendering; add tests/e2e/diagnostics.spec.ts (FR-017, FR-018, FR-019; SC-006, SC-010).
## Historical rollups (P2)

Independent gate: Year-range queries retain extrema/coverage after restart within the point limit.

- [X] T025 [US5] Add deterministic bucket/cadence/coverage/late-data/retention fixtures in internal/telemetry/rollups_test.go, including weighted means, partial boundaries and >600-hour ranges (FR-020, FR-021, FR-022, FR-023; SC-007, SC-008).
- [X] T026 [US5] Implement leased generation-aware five-minute/hourly aggregation and bounded retained-data backfill in internal/telemetry/rollups.go and internal/store/rollups.go (FR-020, FR-021, FR-023; SC-007, SC-008).
- [X] T027 [US5] Extend internal/store/telemetry.go and internal/control/inventory.go history queries with automatic tier grouping, count/coverage/resolution and full-range empty buckets (FR-019, FR-021, FR-022; SC-007).
- [X] T028 [US5] Coordinate retention, dirty generations, storage thresholds and operator policy changes in internal/telemetry/retention.go and internal/control/settings.go; expose rollup lag and pressure (FR-020, FR-023, FR-024, FR-033; SC-008, SC-011).
- [X] T029 [US5] Wire history resolution/partial metadata and retention preview into apps/web/src/views/device.tsx and apps/web/src/views/recovery.tsx; verify interrupted aggregation and mixed-version agents in tests/integration/rollups_test.go (FR-019, FR-020, FR-021, FR-022, FR-023, FR-024; SC-007, SC-008, SC-010).
## Storage health (P2)

Independent gate: Each required family handles faults, missing permission and identity changes.

- [X] T030 [US6] Add SATA/SAS/NVMe JSON/exit-bitmask/standby/permission fixtures and stable identity cases in internal/collector/smart/collector_test.go (FR-025, FR-027, FR-028, FR-032; SC-009, SC-011).
- [X] T031 [US6] Implement bounded non-waking SMART adapter in internal/collector/smart/collector.go and capability/fault translation without arbitrary arguments (FR-025, FR-027, FR-028, FR-032; SC-009).
- [X] T032 [US6] Implement fixed-query ZFS pool/dataset GUID, capacity, rate and scrub parser with reset/partial/permission fixtures in internal/collector/zfs/collector.go and internal/collector/zfs/collector_test.go (FR-026, FR-027, FR-028, FR-032; SC-009, SC-011).
- [X] T033 [US6] Add typed storage-fault evaluation and per-entity storage views in internal/alerts/storage.go and apps/web/src/views/storage.tsx; prevent physical/usable double-counting (FR-001, FR-025, FR-026, FR-027, FR-028, FR-019; SC-009, SC-010).
- [X] T034 [US6] Create scripts/test-storage-health.sh and record SMART family and disposable-ZFS live evidence in specs/002-advanced-monitoring/evidence.md including grants and replacement behavior (FR-025, FR-026, FR-027, FR-028, FR-034; SC-009).
## Sensors and GPUs (P2)

Independent gate: Expose honest per-field capability and survive collector failure.

- [X] T035 [US7] Add hwmon zero/negative/fault/rename fixtures and implement sensors adapter in internal/collector/sensors/collector.go and internal/collector/sensors/collector_test.go (FR-029, FR-031, FR-032; SC-009, SC-011).
- [X] T036 [US7] Implement fixed NVIDIA queries and AMD/Intel sysfs GPU adapters with unavailable-field/reset/shared-power fixtures in internal/collector/gpu/collector.go and internal/collector/gpu/collector_test.go (FR-030, FR-031, FR-032; SC-009, SC-011).
- [X] T037 [US7] Add sensor exclusions, per-field capability labels and GPU/hardware charts in apps/web/src/views/hardware.tsx; allow explicit thresholds without universal thermal defaults (FR-019, FR-029, FR-030, FR-031; SC-009, SC-010).
- [X] T038 [US7] Create scripts/test-hardware-monitoring.sh and complete representative sensor/NVIDIA/AMD/Intel results in specs/002-advanced-monitoring/support-matrix.md with timeout/noninterference evidence (FR-029, FR-030, FR-031, FR-032, FR-034; SC-009, SC-011).
## Integration and release evidence

Independent gate: All seven journeys pass together against PostgreSQL and published lab conditions.

- [X] T039 Extend backup/restore scripts and tests/integration/monitoring_recovery_test.go to cover all 002 tables, keys, migration generations, aggregate checkpoints and notification no-replay policy (FR-013, FR-024; SC-008).
- [X] T040 Create scripts/test-monitoring-load.sh with the documented 100-host expanded series/state workload, year-tier queries, queue saturation and disk pressure; record sizes/latency/lag in specs/002-advanced-monitoring/evidence.md (FR-020, FR-022, FR-023, FR-033, FR-035; SC-001, SC-003, SC-007, SC-011).
- [X] T041 Add integrated keyboard/responsive incident-service-history journeys in tests/e2e/advanced-monitoring.spec.ts and final auth/MFA/secret failure cases in tests/integration/monitoring_security_test.go (FR-009, FR-016, FR-019, FR-028, FR-032, FR-035; SC-010, SC-011).
- [X] T042 Publish tested support and operations guidance in docs/advanced-monitoring.md, reconcile specs/002-advanced-monitoring/acceptance-matrix.md and evidence.md, and record release gates without claiming untested hardware (FR-024, FR-033, FR-034, FR-035; SC-008, SC-009, SC-011).

## Dependencies and delivery strategy

T001 blocks production implementation where 001 prerequisites are missing. T002 precedes T003–T005; all user stories require T005. Execute remaining tasks in listed order. US2 builds on US1; US3 default incidents use US1 and suppression from US2; US4 prepares entity-aware charts for US5; US6/US7 reuse the completed registry, incident and chart paths. US5 storage integration precedes expanded hardware load. Final tasks require all stories. US1 alone is the first useful development milestone, not the complete 002 release.

Potential parallel work, only if separately authorized: US1 evaluator fixtures and incident UI; US2 receiver tests and notification UI; US3 service fixtures and service view; US4 counter fixtures and chart view; US5 rollup fixtures and retention UI; US6 SMART and ZFS adapters; US7 sensor and GPU adapters. Each pair starts only after its shared contracts and foundations are complete. No [P] markers authorize agents or concurrent edits in this package; default execution is sequential.

Missing hardware access permits fixture/adapter progress but leaves the corresponding live-evidence task open. Never convert a missing lab into a passed support claim. Re-inspect 001 changes before implementation; adjust paths to current modules without changing required behavior or duplicating foundations.
