# Validation Quickstart

This guide describes future validation after the implementation tasks land. Scripts named below are planned outputs in tasks.md and do not exist merely because this document names them. Do not run scenarios against production or install agents on unrelated hosts.

## Prerequisites

Complete 001 prerequisites and a protected workspace with one enrolled Linux test host. Have PostgreSQL 17, repository Go/Node versions, a disposable ntfy receiver, separate-key backup capability, and owner MFA. For hardware acceptance, provision representative devices and explicit read access as recorded in support-matrix.md. All lab infrastructure remains owner-authorized.

Select 002 without changing the active 001 pointer:

```sh
SPECIFY_FEATURE_DIRECTORY="$PWD/specs/002-advanced-monitoring" .specify/scripts/bash/check-prerequisites.sh --paths-only --json
```

For full prerequisite validation use --json --require-tasks --include-tasks, saving and restoring .specify/feature.json around the call because that mode persists feature selection. Verify all package files exist before implementation.

After implementing the relevant tasks, run existing checks:

```sh
go test ./...
npm run check
npm run build
```

Use the repository's configured PostgreSQL test connection for integration tests. T002/T005 must document the exact non-secret variable names and invocation in evidence.md after inspecting the current 001 test harness; do not invent credentials or print connection secrets here.

## End-to-end scenarios

1. **Migration and incidents (T003–T011)**: back up a populated 001 lab, perform the checkpointed migration, compare retained sample counts/identity, and replay controlled values. Above 90% for five minutes opens once; acknowledgment leaves it active; a gap prevents recovery; below 85% for two minutes resolves once. Repeat through restart and duplicate batches.
2. **ntfy (T012–T017)**: configure and test a saved destination in the UI with MFA. Verify no sends while disabled. Enable it, induce a fault/recovery, and observe accepted status at the receiver. Inject 429, 500, timeout, and permanent authentication failure. Verify bounded retry and distinguish receiver acceptance from device receipt.
3. **Suppression**: configure overlapping fleet/device windows, cross-midnight recurrence, both DST transitions, and an offline parent host. Confirm no external request while any matching reason remains. End suppression: one summary per destination/release batch contains only currently active incidents with fresh post-offline evidence.
4. **systemd**: run `scripts/test-systemd.sh` in a disposable systemd host after T021. Create controlled active, failed, inactive and must-run test units through the test harness, not Scout control actions. Deny collection access; confirm host telemetry continues.
5. **Diagnostics/history**: run deterministic tests and inspect per-device charts with keyboard navigation. Generate retained samples with a peak, gaps, counter resets and cadence changes; aggregate and query raw/five-minute/hourly/year views. Verify <=600 points per series, extrema and partial coverage.
6. **Storage**: run `scripts/test-storage-health.sh` after T034 with fixtures first, then explicitly provisioned hardware/disposable ZFS. No destructive fault induction on valuable disks. Verify absence, standby, denial, fault bitmasks and replacement identity against captured evidence.
7. **Sensors/GPUs**: run `scripts/test-hardware-monitoring.sh` after T038. Record one representative supported device per vendor and actual field availability. Exclude a sensor and induce a synthetic timeout. Verify zero RPM/negative temperatures remain valid and no universal thermal alert appears.
8. **Recovery and load**: run recovery tests and `scripts/test-monitoring-load.sh` after T039–T040. Restore with pending ntfy work: delivery remains paused and stale messages never replay. Record the 100-host dataset, actual storage bytes, p95 history latency, evaluation/rollup lag, queue saturation and backpressure.

## Completion evidence

Record command, commit, date, versions, test fixture/hardware, expected/actual result and sanitized output location in evidence.md. Link each result to acceptance-matrix.md rows. An unavailable physical lab keeps its support row unvalidated. All required live gates must pass before claiming the full 002 release.
