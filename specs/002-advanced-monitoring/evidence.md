# Evidence Ledger

## Specification delivery

2026-09-11: documentation package authored from the approved plan, Beszel revision f204dc17e64bba444478c60210d018e43a777193, 001 artifacts, and current code inspection. Current 001 checkout contained unrelated implementation changes; those were excluded from this work.

Document checks are recorded in analysis.md after validation. They establish artifact coverage and consistency only.

## T001 — 001 prerequisite gate

- Date: 2026-09-11 (Europe/London); commit: `93ae5be`
- The working tree was clean before this gate. The active `.specify/feature.json`
  pointer remains `001-scout-platform`; the 002 prerequisite was selected only
  per-command with `SPECIFY_FEATURE_DIRECTORY`.
- `SPECIFY_FEATURE_DIRECTORY="$PWD/specs/002-advanced-monitoring" .specify/scripts/bash/check-prerequisites.sh --json --require-tasks --include-tasks` passed and found the 002 plan, research, data model, contracts, quickstart, and tasks.
- The 001 prerequisite code was verified directly, not inferred from task
  checkboxes: `go test ./internal/identity ./internal/collector/... ./internal/store ./internal/control ./internal/updates ./internal/enrollment -count=1` passed. This covers the existing identity authority and TLS paths, collector registry/adapters, recovery/store, authenticated control handlers, signed release verification, and scoped enrollment boundaries.
- 001 remains an implementation-complete but acceptance-incomplete dependency.
  T017, T022, T030, T044, T049, T055, T059, T061, and T063 remain open for
  owner-authorized Linux/provider/browser/capacity evidence; this 002 work does
  not mark those gates complete or duplicate their foundations.

## Implementation evidence

No 002 feature implementation, runtime tests, migration, benchmark, ntfy
publication, host installation, or hardware validation was performed before
the T001 prerequisite gate. Future entries must include task and requirement
IDs, commit, date, exact command/environment (secrets redacted), expected and
actual outcomes, output/artifact links, and remaining limits.

For future results append: task and requirement IDs, commit, date, exact command/environment (secrets redacted), expected outcome, actual outcome, output/artifact link, and remaining limits. Record prerequisite 001 evidence during T001; do not copy its checkbox state as proof.

## T002 — contracts, bounds, and compatibility targets

- Requirements: FR-003, FR-008, FR-009, FR-014, FR-022, FR-032, FR-034.
- Date: 2026-09-11 (Europe/London); commit: `f6a70c2`.
- Contract outputs: `api/openapi.yaml` now covers the additive rule, incident,
  notification, suppression, monitoring-status, and retention-preview routes;
  `api/schemas/` contains strict bounded input/output schemas; and
  `tests/contracts/fixtures/` contains positive rule, destination, quiet-window,
  and history examples plus a rejected executable-expression example.
- Security/bounds evidence: destination `topic` and `token` are write-only with
  limits of 128 and 4096 bytes; destination responses are redacted; recent MFA
  is declared for protected destination/window/settings actions; history is
  bounded to 16 series and 600 points; numeric/state durations are bounded to
  86400 seconds; arbitrary expressions and service/GPU control routes are
  rejected by the contract test.
- Dependency evidence: `go.mod` pins
  `github.com/coreos/go-systemd/v22 v22.7.0`; `go.sum` matches the module and
  go.mod checksums; `research.md` records smartmontools/OpenZFS/hwmon/NVIDIA/
  AMD/Intel compatibility targets as unvalidated fixture/lab targets.
- Exact verification commands (no credentials or external device access):
  `jq -e empty api/schemas/*.json tests/contracts/fixtures/*.json`;
  `go test ./tests/contracts -count=1`; `go test ./... -count=1`;
  `go vet ./...`; `npm run format:check`; `git diff --check`; and
  `go mod verify`. Compose interpolation was also checked with
  `SCOUT_PORT=18080 docker-compose -f compose.quickstart.yaml config` and
  `SCOUT_PORT=18080 SCOUT_CONTAINER_PORT=18081 docker-compose -f
  compose.quickstart.yaml config`.
- Expected and actual outcome: all commands passed. OpenAPI schema references
  and JSON schema references were checked for existing files. The contract
  test passed both positive fixtures and the negative executable-expression /
  redaction assertions. The Compose checks resolved host port 18080 to the
  configured container target and listener, with `SCOUT_PORT` changing only
  the host side by default and `SCOUT_CONTAINER_PORT` changing the internal
  side when explicitly set.
- Remaining limits: these are executable contract and fixture checks only. No
  ntfy publication, D-Bus/systemd host, smartctl device, ZFS pool, sensor/GPU
  hardware, or live API acceptance is claimed here; those belong to later
  implementation and release-gate tasks.

## T003 — checkpointed telemetry migration

- Requirements: FR-004, FR-010, FR-020, FR-024; SC-008.
- Date: 2026-09-11 (Europe/London); commit: `fe8ca9a`.
- Migration v2 adds durable monitoring storage state, generation-scoped
  checkpoints, metric-series identity, current-series state, receipts, global
  sample ordinals, rollup work, and aggregate tables without removing the
  legacy workspace snapshot. The importer reads a stable SHA-256 source
  snapshot, sorts each stream deterministically, commits bounded row batches
  together with their checkpoint, resumes after an interrupted batch, and
  requires source-hash/checkpoint/count parity before explicit authoritative
  cutover. An advisory transaction lock serializes begin, import, verification,
  and cutover. Legacy receipt keys are imported in both the new JSON-safe form
  and the original NUL-delimited form; new memory receipts no longer write NUL
  bytes into JSONB-backed workspace state.
- Exact verification commands and outcomes:
  `go test ./internal/store ./tests/integration -count=1` passed;
  `go vet ./internal/store ./tests/integration` passed;
  `scripts/test-integration.sh` passed against a disposable local PostgreSQL
  17 container; `go test ./... -count=1`; `go vet ./...`;
  `npm run format:check`; `git diff --check`; and `go mod verify` all passed.
  The disposable run exercised migration, one-row batches, parity, normalized
  row counts, authoritative cutover, and rejection of a post-cutover restart.
  The regression test also covers JSON serialization and legacy receipt-key
  parsing.
- Negative outcomes caught during verification and fixed before completion:
  PostgreSQL rejected a unique index on the partitioned samples table without
  its partition key, so global replay identity is enforced by the separate
  unpartitioned ordinal table; PostgreSQL JSONB rejected the pre-existing NUL
  receipt-key encoding, so the memory key format was made JSON-safe while
  retaining backward import compatibility.
- Remaining limits: T004 still moves live ingestion/history/current-series and
  dirty-work transactions off the legacy snapshot; T005 still adds crash,
  concurrency, interruption, and disk-budget fixtures. No production rollout,
  real device, or production credential was used.

## T004 — authoritative SQL telemetry transactions

- Requirements: FR-004, FR-005, FR-018, FR-023, FR-024; SC-002, SC-008.
- Date: 2026-09-11 (Europe/London); commit: `4b9b7c5`.
- After explicit migration cutover, ingestion uses one PostgreSQL transaction
  for the receipt, sample ordinals, normalized samples, full series identity,
  current-series projection, observations, five-minute/hourly rollup work, and
  workspace counters. The receipt triple is idempotent and hash conflicts are
  rejected. Current values use observed time then receipt time, while history
  remains ordered and entity-specific. SQL history/status/observation/retention
  paths are authoritative; device reads hydrate compatibility current metrics
  from `current_series`. Legacy snapshot mutations strip samples, receipts,
  and observations after cutover, preserving the non-telemetry state.
- Exact verification commands and outcomes: `scripts/test-integration.sh`
  passed against disposable PostgreSQL 17; `go test ./... -count=1` passed;
  `go vet ./...` passed; `npm run format:check` passed; `git diff --check`
  passed; and `go mod verify` passed. The SQL integration fixture covers
  duplicate and conflicting replay, invalid-batch rollback, separate
  full-identity entities, out-of-order history, current projection ordering,
  dirty bucket creation, SQL observations, dropped counters, and empty legacy
  telemetry backup fields.
- Remaining limits: T005 still owns the dedicated concurrent-writer,
  interrupted-migration, disk-budget, and rollback/replay load fixtures. SQL
  rollup computation, tier selection, disk-budget policy, and incident
  evaluation remain later tasks. No production rollout, real device, or
  production credential was used.

## T005 — measured telemetry budgets and failure fixtures

- Requirements: FR-018, FR-023, FR-024, FR-033, FR-035; SC-008, SC-011.
- Date: 2026-09-11 (Europe/London); commit: `c6beebd`.
- The implicit one-million-sample default was removed. Workspace telemetry now
  has a configured 500 GiB default budget, an optional explicit row cap where
  zero means unlimited, and status fields for measured bytes and configured
  budget. PostgreSQL accounting sums `pg_total_relation_size` for the
  normalized telemetry tables, indexes, TOAST data, and all direct
  `metric_samples` partitions. New batches are admitted against a conservative
  measured-size estimate; 90% pressure rejects new telemetry with existing
  retryable backpressure while read/control paths remain available. Cleanup
  clears pressure only below both row and byte thresholds. The memory fixture
  follows the same bounded-budget semantics.
- `tests/integration/monitoring_storage_test.go` uses a disposable PostgreSQL
  17 database and covers a cancellation inside a paused migration insert with
  checkpoint/row rollback, resumed import and cutover, invalid-batch rollback,
  duplicate and conflicting replay, two concurrent SQL writers, positive
  measured-budget status, and negative disk-budget admission with unchanged
  rows and measured bytes. Existing SQL telemetry fixtures continue to cover
  replay and rollback behavior across the full normalized path.
- Exact verification commands and outcomes: `scripts/test-integration.sh`
  passed against disposable PostgreSQL 17; `go test ./... -count=1`; `go vet
  ./...`; `npm run format:check`; `npm run check`; `npm run build`; `go mod
  verify`; and `git diff --cached --check` all passed. No production database,
  credentials, or real device was used.
- Remaining limits: T006 onward still implement incident evaluation,
  notifications, collectors, rollups, and UI behavior. The 100-host capacity
  and live storage-sizing target remains T040; no production deployment or
  full acceptance claim is made here.

## T006 — deterministic incident evaluator fixtures

- Requirements: FR-001, FR-002, FR-003, FR-004, FR-005, FR-007; SC-001, SC-002.
- Date: 2026-09-11 (Europe/London); commit: `9da782c`.
- Added a clock-injected evaluator core and fixtures for strict numeric trigger
  and clear comparisons, five-minute/two-minute hysteresis, state rules with
  minimum consecutive fresh samples, idempotent acknowledgment that does not
  establish recovery, evidence gaps that reset timing without fabricating
  recovery, duplicate and out-of-order observation rejection, and rule
  revision closure with a `rule_changed` reason. The evaluator emits bounded
  transition records and preserves active incidents while evidence is unknown.
- Exact verification commands and outcomes: `go test ./internal/alerts
  -race -count=1`; `go test ./... -count=1`; `go vet ./...`; `npm run
  format:check`; and `git diff --check` all passed. No external receiver,
  production credential, or real device was used.
- Remaining limits: this is the deterministic evaluator foundation. T009–T011
  still add control/API/UI surfaces, restart integration, and the live US1
  acceptance journey.

## T007 — default rules and scoped override persistence

- Requirements: FR-001, FR-002, FR-003; SC-001.
- Date: 2026-09-11 (Europe/London); commit: `bdd4927`.
- Added seven idempotent fleet defaults: host offline, CPU, memory,
  filesystem, failed systemd unit, degraded collector, and explicit storage
  fault. Numeric defaults use strict 90/85 hysteresis and 300/120-second
  timing; no universal thermal/GPU rule is provisioned. Existing template
  rows, including owner-disabled or retired rows, are never overwritten.
  Rule and override writes use revisions; overrides replace the full condition
  tuple, allow only site/device narrowing, reject cross-site device targets,
  and resolve device before site before fleet. Rules and overrides have
  additive SQL tables plus memory fixtures.
- Exact verification commands and outcomes: `go test ./internal/alerts
  ./internal/store -count=1`; `scripts/test-integration.sh` passed against
  disposable PostgreSQL 17; `go test ./... -count=1`; `go vet ./...`; and
  `git diff --check` passed. The SQL fixture verified idempotent provisioning,
  compare-and-swap rejection, override persistence, and retirement filtering.
  No production database, credential, or real device was used.
- Remaining limits: T009–T011 still add control handlers, UI,
  restart/decommission integration, and the live US1 acceptance journey.

## T008 — durable alert work and incident transitions

- Requirements: FR-002, FR-004, FR-005, FR-007, FR-033; SC-001, SC-002.
- Date: 2026-09-11 (Europe/London); commit: `1be09f7`.
- Added migration v4 tables for alert evaluation checkpoints, incidents,
  append-only transitions, and leased dirty work. Telemetry ingestion marks
  wildcard-lineage work in the same memory/SQL transaction as samples,
  current state, observations, and rollup work. Work claims use bounded
  leases and epochs; completion removes only an unchanged generation, so
  telemetry arriving during evaluation remains queued. The durable evaluator
  restores checkpoint and active-incident state after restart, runs an
  immediate sweep followed by a 15-second cadence, emits globally unique
  incident IDs, preserves unknown evidence without false recovery, and writes
  the checkpoint, incident snapshot, and transition append atomically.
- Exact verification commands and outcomes: `go test ./internal/alerts
  ./internal/store ./internal/control ./internal/auth -count=1` passed;
  `go test ./... -count=1` passed; `go vet ./...` passed; `npm run lint`
  passed; `npm test` passed; `npm run check` passed; `npm run build` passed;
  `scripts/test-integration.sh` passed against disposable PostgreSQL 17,
  including SQL incident persistence, lease completion, and rollback on an
  invalid transition; `npx playwright install chromium` completed; and
  `npm run test:e2e:browser` passed the isolated browser journey through
  owner setup, sign-in, development security state, site/scope creation,
  scope enablement, and first-agent invitation.
- Development friction evidence: development mode retains owner
  authentication and CSRF checks but bypasses only the recent-MFA mutation
  ceremony; production still enforces recent MFA. The interactive browser
  repro that previously returned `Action not permitted` now completed the
  same scope and invitation actions on an isolated local server without
  that error. No real device or production credential was used.
- Remaining limits: T009–T011 still add the owner control API, incident UI,
  restart/decommission/admission integration, and the live US1 acceptance
  journey. The 100,000 active-incident cap is enforced and observable as a
  store error, but no capacity claim is made without the later workload
  evidence task.
