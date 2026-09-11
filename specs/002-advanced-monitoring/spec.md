# Feature Specification: Advanced Monitoring and Alerting

**Feature Branch**: `main` (directory independent of branch)
**Created**: 2026-09-11
**Status**: Specification and implementation package prepared; not implemented or runtime-validated.
**Input**: Implement the approved post-001 specification package for incidents and ntfy, systemd health, diagnostic metrics, year-long rollups, SMART/ZFS, and Linux hardware sensors/GPUs.

## User Scenarios & Testing

### US1 — Notice and investigate incidents (P1)

As the owner, I see actionable incidents automatically and can distinguish current faults, unknown evidence, acknowledgment, and recovery.

**Why this priority**: Directly turns existing monitoring into actionable diagnosis.

**Independent test**: A deterministic enrolled-host stream crosses and clears defaults while replay, gaps, and restart are injected.

**Acceptance scenarios**:

1. Given fresh CPU readings above 90% for five minutes, when evaluation runs, then one incident opens with supporting readings; replay creates none.
2. Given an active incident, when I acknowledge it and telemetry disappears, then acknowledgment is recorded and the incident remains active with unknown evidence.
3. Given values below 85% for two continuous minutes, when evaluated, then the numeric incident resolves once; deleting its rule instead records administrative closure.

### US2 — Receive useful ntfy notifications (P1)

As the owner, I opt into ntfy delivery and quiet periods without losing incident history.

**Why this priority**: Directly turns existing monitoring into actionable diagnosis.

**Independent test**: Use a controlled ntfy receiver and clock to exercise delivery, quiet windows, retry, and restore.

**Acceptance scenarios**:

1. Given automatic in-app incidents and no enabled destination, when a fault opens, then no external request occurs.
2. Given an enabled destination, when a fault opens and recovers, then accepted delivery records link to each transition without claiming phone receipt.
3. Given overlapping quiet windows, when one ends, then delivery remains suppressed until all applicable windows end; one summary includes only still-active incidents.
4. Given a restored backup or revoked destination, when pending work is processed, then no notification is sent before permitted reconciliation.

### US3 — Understand Linux service failures (P1)

As the owner, I inspect services and identify failures even while a host remains online.

**Why this priority**: Directly turns existing monitoring into actionable diagnosis.

**Independent test**: A controlled Linux host exposes active, failed, intentionally inactive, and denied-access service fixtures.

**Acceptance scenarios**:

1. Given a newly observed failed unit, when collected, then it appears in inventory and opens an incident.
2. Given an inactive unit outside must-run selection, when collected, then no inactivity incident opens; selecting it enables the must-run rule.
3. Given denied service access, when collection runs, then availability and permission diagnostics appear without disrupting host telemetry.

### US4 — Diagnose resource bottlenecks (P1)

As the owner, I inspect CPU states, load, swap, and disk performance with honest charts.

**Why this priority**: Directly turns existing monitoring into actionable diagnosis.

**Independent test**: Replay known procfs counters and inspect the resulting multi-entity charts.

**Acceptance scenarios**:

1. Given two disks with different throughput, when selected, then their units and histories remain separate.
2. Given a counter reset or no completed disk operations, when rates or latency are derived, then reset rates and undefined latency appear unavailable rather than negative or fabricated.
3. Given a keyboard-only session, when inspecting charts, then timestamps, values, units, and gaps are accessible.

### US5 — Review a year of trends (P2)

As the owner, I compare long-term trends without losing peaks or mistaking gaps for healthy measurements.

**Why this priority**: Extends diagnosis to long-term or hardware-specific evidence after the common path is established.

**Independent test**: Generate a year-equivalent dataset, aggregate it, interrupt jobs, and query each retention tier.

**Acceptance scenarios**:

1. Given a short spike and missing intervals, when viewing hourly history, then extrema and coverage remain visible.
2. Given a year-long range, when requested, then the complete range is represented within 600 points per series with explicit resolution.
3. Given aggregation interruption or disk pressure, when jobs resume, then buckets are not doubled and eligible raw data is protected with visible backpressure.

### US6 — Detect storage degradation (P2)

As the owner, I see failing disks and degraded ZFS storage before interpreting capacity as health.

**Why this priority**: Extends diagnosis to long-term or hardware-specific evidence after the common path is established.

**Independent test**: Use SMART family fixtures and a disposable ZFS lab plus representative physical device evidence.

**Acceptance scenarios**:

1. Given explicit SMART failure or degraded pool state, when collected, then an incident identifies the exact entity and evidence.
2. Given a disk renamed without changing stable identity, when rediscovered, then its history remains linked; replacement with another identity remains separate.
3. Given missing smartctl or denied ZFS access, when collected, then the collector explains unavailability and performs no repair action.

### US7 — Inspect hardware sensors and GPUs (P2)

As the owner, I inspect thermal, fan, and GPU conditions supported by my Linux hardware.

**Why this priority**: Extends diagnosis to long-term or hardware-specific evidence after the common path is established.

**Independent test**: Exercise each supported family with fixtures and record representative device/driver results.

**Acceptance scenarios**:

1. Given an exposed sensor or supported GPU field, when collected, then its value, unit, identity, and time appear; other fields remain unsupported.
2. Given a temperature without a hardware fault indication, when first discovered, then no universal temperature alert is enabled.
3. Given a slow or failing hardware collector, when its deadline expires, then base host collection continues.

### Edge cases

- Gaps during pending/recovering rules, equal threshold values, reordered batches, clock skew, host restart, and resumed buffered telemetry.
- Conflicting target overrides, rule retirement, acknowledged incidents, removed entities, and host decommissioning.
- Overlapping windows, midnight crossings, daylight-saving gaps/folds, changed destination configuration, accepted requests whose response is lost, and quiet hours during retries.
- SMART exit status containing health bits despite valid JSON, absent attributes, sleeping disks, repeated serials, virtual disks, device-path reuse, and incomplete enumeration.
- Inactive one-shot units, transitional services, inaccessible system bus, partial GPU fields, unavailable drivers, sensor renumbering, and collector timeout.
- Incomplete rollup windows, mixed collection intervals, late data, deleted raw observations, year-range point bounds, migration interruption, and storage exhaustion.

## Requirements

### Functional Requirements

#### Alerts and incidents

- **FR-001**: Scout MUST automatically provision enabled in-app defaults for enrolled-host offline status, sustained CPU/memory/filesystem pressure, failed systemd units, degraded enabled collectors, and explicit storage faults; it MUST NOT send external notifications before the owner enables a destination.
- **FR-002**: Numeric defaults MUST trigger above 90% continuously for five minutes and recover below 85% continuously for two minutes for CPU utilization, memory utilization, and each filesystem. Offline detection MUST inherit the workspace threshold from 001, initially 90 seconds.
- **FR-003**: The owner MUST create, revise, disable, and retire numeric or state rules with warning/critical severity, durations, and fleet/site/device targeting, and override inherited rules per device without generating duplicate incidents for the same rule lineage.
- **FR-004**: Scout MUST retain at most one active incident per rule lineage and entity, including triggering evidence, observation time, evaluation time, acknowledgment, recovery or administrative closure reason, and immutable transition history.
- **FR-005**: Acknowledgment MUST NOT establish recovery. Missing, stale, unsupported, duplicate, or out-of-order measurements MUST NOT advance a sustained trigger/recovery interval or falsely clear an incident.
- **FR-006**: Scout MUST continue to record service/collector incidents while their host is offline but suppress their external notifications; after reconnection it MUST re-evaluate current evidence before notifying.
- **FR-007**: Rule changes, entity retirement, decommissioning, server restart, and changes of target membership MUST have deterministic, auditable outcomes without fabricating recovery or replaying historical notifications.

#### ntfy and suppression

- **FR-008**: Scout MUST provide ntfy destinations with owner-configured server, topic, optional access token, explicit enablement, test delivery, and delivery-status inspection; ntfy MUST be the only external notification provider in this release.
- **FR-009**: Destination secrets MUST be encrypted, write-only after entry, excluded from diagnostics and agents, and protected by recent owner second-factor verification for configuration and test delivery. Network requests MUST validate destination and TLS identity.
- **FR-010**: Scout MUST durably queue trigger and recovery notifications with bounded retries, deduplicate internal events, expose permanent failure and queue overflow, and distinguish ntfy acceptance from confirmed subscriber receipt.
- **FR-011**: The owner MUST define recurring timezone-aware quiet hours and one-time maintenance windows at fleet/site/device level. All severities MUST be externally silenced while any matching window applies; incident recording MUST continue.
- **FR-012**: When suppression ends, Scout MUST send one bounded summary of still-active incidents for the affected destination and entities, without replaying resolved incidents or every suppressed transition.
- **FR-013**: Restore MUST pause external notification delivery until owner reconciliation. Destination revocation and disablement MUST prevent new sends and cancel queued work at the next execution boundary.

#### Linux services

- **FR-014**: Scout MUST discover bounded systemd service inventory and expose active, inactive, failed, transitional, and unavailable states with host association, source time, and freshness.
- **FR-015**: Scout MUST alert on failed units automatically and allow owner-selected exact names or patterns for services expected to remain active; intentional inactivity outside that selection MUST NOT alert.
- **FR-016**: Service collection MUST remain read-only, report denied access or partial enumeration, and never collect unit environment secrets, journal contents, or expose service-control operations.

#### Diagnostic host metrics

- **FR-017**: Scout MUST add available CPU user/system/iowait/steal percentages, 1/5/15-minute load, swap used/capacity, and per-block-device read/write throughput, utilization, and read/write latency.
- **FR-018**: Rates MUST preserve counter reset, first observation, missing counters, and no-operation distinctions. Device replacement MUST NOT silently join unrelated histories.
- **FR-019**: Charts MUST support metric-appropriate units and domains, per-entity selection, visible gaps, effective resolution, source times, and accessible keyboard inspection across 360–1440 CSS-pixel layouts.

#### Historical rollups

- **FR-020**: Scout MUST retain raw observations for 30 days, five-minute aggregates for 90 days, and hourly aggregates for 365 days by default, with explicit storage bounds and retention status.
- **FR-021**: Aggregates MUST retain count, minimum, maximum, average, expected/observed coverage, and missing intervals. Coarser averages MUST weight source counts rather than average averages blindly.
- **FR-022**: History queries MUST choose a suitable available resolution, return no more than 600 points per series, identify partial coverage, and never silently truncate the requested time range.
- **FR-023**: Aggregation and backfill MUST be idempotent, restart-safe, and based only on retained data. Eligible aggregation MUST complete before raw deletion; bounded storage pressure MUST produce visible backpressure rather than silent loss.
- **FR-024**: Scout MUST preserve telemetry identity, retained history, and incident state across migration and backup/restore; older agents MUST continue base monitoring with unsupported new features explicitly identified.

#### Storage health

- **FR-025**: Scout MUST support tested SATA/SAS/NVMe SMART device families and expose available health, wear, temperature, and error counters; absent attributes MUST remain unavailable rather than passed.
- **FR-026**: Scout MUST collect ZFS pool health, capacity, I/O, scrub state, and dataset usage, distinguishing disk, pool, dataset, filesystem, and physical versus usable capacity.
- **FR-027**: Storage entities MUST have host-scoped stable identity using available device/pool identifiers; path changes, replacement, ambiguous identifiers, and disappearance MUST retain evidence without false merging.
- **FR-028**: Storage collectors MUST expose needed privileges and missing utilities, bound execution, and perform no repair, self-test initiation, scrub initiation, or automatic privilege grant.

#### Hardware sensors and GPUs

- **FR-029**: Scout MUST collect exposed Linux temperature/fan sensors with stable identities, labels, exclusions, units, and per-field availability.
- **FR-030**: Scout MUST support tested NVIDIA, AMD, and Intel GPU families for available utilization, memory, temperature, and power, with explicit hardware/driver capability reporting.
- **FR-031**: Generic thermal, fan, GPU utilization, and wear thresholds MUST require owner configuration unless hardware reports an explicit fault; unsupported measurements MUST NOT activate defaults.
- **FR-032**: Every new collector MUST use the existing versioned extension contract, declared permissions, bounded scheduling and cardinality, and independent failure handling without interrupting base monitoring.

#### Operations and acceptance

- **FR-033**: Scout MUST expose incident-evaluation lag, notification backlog/failures, aggregation lag, collector truncation, and storage backpressure without secret values.
- **FR-034**: Scout MUST publish exact tested operating-system, utility, hardware, and driver versions and permissions for advertised support. Fixtures alone MUST NOT count as live compatibility evidence.
- **FR-035**: The release MUST validate the 100-device reference workspace with expanded entity counts, storage sizing, accessible history/incident journeys, and recovery/failure scenarios before claiming acceptance.

### Key Entities

- **Rule lineage and override**: Owner policy, target selection, threshold/state condition, timing, severity, revision, and per-device replacement.
- **Evaluation state**: Effective rule/entity, latest eligible evidence, pending/recovery intervals, and current evidence availability.
- **Incident and transition**: Durable episode, evidence snapshots, acknowledgment, recovery or administrative closure, and notification links.
- **Destination and delivery**: ntfy configuration with protected credential reference, enabled state, revision, attempt and acceptance status.
- **Suppression window**: Fleet/site/device matching and recurring local-time or one-time absolute interval.
- **Service/hardware entity**: Host-scoped stable provider identity, capabilities, source, freshness, and retirement state.
- **Metric aggregate**: Series identity, time bucket, statistics, coverage, source generation, and completeness.

## Success Criteria

These are acceptance targets, not measured support claims. See [plan.md](plan.md) for reference conditions.

- **SC-001**: Within one 15-second evaluation cycle after a qualifying threshold interval or inherited offline deadline, the reference workspace shows exactly one incident per effective rule/entity.
- **SC-002**: All missing/reordered/duplicate telemetry fixtures preserve unknown evidence and produce zero false recoveries or duplicate incident transitions.
- **SC-003**: With a healthy configured ntfy receiver, 95% of eligible notifications are accepted within 30 seconds of the transition; disabled, suppressed, and restored-paused scenarios produce zero forbidden requests.
- **SC-004**: All quiet-window and daylight-saving fixtures suppress all severities and produce one eligible summary per destination/release batch, with no resolved-incident replay.
- **SC-005**: Controlled systemd fixtures distinguish failed, intentionally inactive, selected must-run inactive, and denied-access cases; a failed adapter never stops scheduled host telemetry.
- **SC-006**: Diagnostic fixtures reproduce expected units and rates within 1% numeric tolerance, retain reset gaps, and keep different device identities separate.
- **SC-007**: Year-range queries return at most 600 points per series, preserve known extrema and coverage, and complete within two seconds at p95 under the documented 100-device workload.
- **SC-008**: Interrupted migration, aggregation, and restore fixtures preserve retained identity/history, produce no duplicate aggregates, and keep external delivery paused until reconciliation.
- **SC-009**: Every advertised SMART/GPU family and ZFS/sensor collector has fixture coverage plus representative live evidence with exact versions and permissions; unavailable families are not advertised as validated.
- **SC-010**: Keyboard-only users complete incident inspection/acknowledgment, quiet-hour configuration, and metric-series selection at supported viewport sizes; no meaning depends only on color.
- **SC-011**: Queue saturation, disk pressure, slow collectors, redaction, and destination validation fixtures report bounded failure without secret leakage or unbounded resource growth.

## Assumptions

- One owner; Linux AMD64/ARM64 baseline inherited from 001. Hardware support is a tested subset, not all devices on those architectures.
- All six feature areas are required for the target 002 release, delivered as independently testable increments. P2 does not mean optional release scope.
- ntfy is owner-provided; Scout neither deploys it nor requires a hosted account. Core incidents work offline. No messages are sent merely by writing this package.
- No Podman additions, image-update checks, OIDC, multi-user access, macOS/Windows, service restarts, storage repair, or other remediation.
- 001 remains responsible for base identity, transport, discovery, enrollment, lifecycle, collector infrastructure, and recovery. 002 extends these contracts and does not declare unfinished 001 work complete.
- Raw/rollup defaults are ages from observation time. Existing explicit shorter owner retention must be respected until changed by the owner; defaults apply to new workspaces and uncustomized policies.
- Technical decisions and exact resource limits live in [plan.md](plan.md) and [contracts/](contracts/); the approved user choices are recorded in [research.md](research.md).
