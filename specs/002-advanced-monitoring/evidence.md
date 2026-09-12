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
- Remaining limits: T010–T011 still add the incident UI and
  restart/decommission/admission integration before the live US1 acceptance
  journey. The 100,000 active-incident cap is enforced and observable as a
  store error, but no capacity claim is made without the later workload
  evidence task.

## T009 — owner alert and incident control API

- Requirements: FR-003, FR-004, FR-005, FR-007, FR-009; SC-002, SC-011.
- Date: 2026-09-11 (Europe/London); validation was run against the
  implementation that is being committed with this evidence entry.
- Added authenticated owner CRUD for alert rules and site/device overrides,
  nested contract DTOs with catalog metric/state validation, immutable lineage
  identity on PATCH, bounded opaque pagination for rules, overrides, incidents,
  and transition history, site/device incident filtering, and redacted bounded
  incident/evidence responses. Acknowledgment is a revision-checked durable
  mutation that appends exactly one transition and returns the existing state
  on repeated requests, including after a stale retry. Development keeps the
  agreed MFA bypass while retaining session and CSRF checks; production uses
  the existing recent-MFA gate. Alert condition service-pattern and collector
  fields are retained through an additive migration.
- Exact verification commands and outcomes: `go test ./internal/control
  ./internal/store ./internal/alerts -count=1`; `go test ./... -count=1`; `go
  vet ./...`; `scripts/test-integration.sh` against disposable PostgreSQL 17;
  and `git diff --check` passed. Endpoint tests covered successful CRUD,
  stale rule revisions, nested conditions, invalid catalog states,
  site-filtered incidents, transition cursors, and repeated acknowledgment.
  No production database, credential, notification receiver, or real device
  was used.
- Remaining limits: T010 adds the incident/rule UI and T011 adds the full
  restart, target-change, cap, and decommission acceptance evidence.

## T010 — incident workspace and owner rule editor

- Requirements: FR-001, FR-003, FR-004, FR-005, FR-019; SC-010.
- Date: 2026-09-11 (Europe/London); implementation commit: `972732b`.
- Added an Incidents navigation page with an evidence-first active queue,
  incident detail/history view, evidence freshness/unknown/unsupported
  treatment, owner acknowledgment with explicit “acknowledgment is not
  recovery” copy, severity/status/acknowledgment filters, and responsive
  narrow-screen layout. Added the revision-aware rule editor for numeric and
  state conditions, fleet/site/device targeting, bounded durations and
  hysteresis, rule enablement, and retirement. The page explains the seven
  automatic fleet defaults, their concrete thresholds, and that external
  notifications remain off until explicitly configured. The frontend API
  client now covers the rule, override, incident, transition, and
  acknowledgment endpoints.
- Exact verification commands and outcomes: `npm run check`; `npm run build`;
  `npm run lint`; `npm test`; `npm run format:check`; `go test ./...
  -count=1`; `git diff --check`; and `npm run test:e2e:browser` all passed.
  The Playwright browser journey positively covered owner setup, sign-in,
  navigation to Incidents, the empty incident queue, automatic-default
  explanation, opening Rules, and creating a real alert rule through the UI.
  Existing API tests continue to cover invalid conditions, stale revisions,
  pagination, and repeated acknowledgment. No production database,
  credential, receiver, or real device was used.
- Remaining limits: T011 still owns restart, rule disable/retirement target
  changes, admission-cap, decommission closure, and the full live US1
  acceptance journey. No active incident was fabricated solely for this UI
  test, so the browser run does not claim a live incident-detail screenshot
  or acknowledgment acceptance against a monitored device.

## T011 — restart and administrative incident lifecycle

- Requirements: FR-004, FR-005, FR-007, FR-033; SC-001, SC-002, SC-011.
- Date: 2026-09-11 (Europe/London); implementation commit: `7bcd2f2`.
- Administrative policy changes now close active episodes with an explicit
  append-only `administrative_close` transition and bounded reason: rule
  changes, disablement, retirement, target membership changes, and device
  decommissioning are not reported as telemetry recovery. The durable
  evaluator checkpoint, timing state, and queued work are cleared so a stale
  replay cannot reopen the episode. Rule closures are transactionally coupled
  to their SQL policy update; each SQL incident closure locks active rows and
  appends the incident update and transition together.
- `tests/integration/incidents_test.go` positively covers replay after an
  evaluator restart without a duplicate transition, rule target changes,
  rule disablement, rule retirement, device movement out of a site target, and
  device decommission closure. It negatively fills the database to exactly
  `MaxActiveIncidents` (100,000) and verifies a new episode returns
  `store.ErrIncidentCap` with neither an incident nor an evaluation checkpoint
  committed. Closure assertions verify status, reason, transition sequence,
  actor, revision, unknown evidence reset, zeroed timing counters, and cleared
  incident references.
- Exact verification commands and outcomes: `scripts/test-integration.sh`
  against disposable PostgreSQL 17; `go test ./... -race -count=1`; `npm run
  check`; `npm run build`; `npm run lint`; `npm test`; `npm run format:check`;
  `go vet ./...`; `go mod verify`; and `git diff --check` all passed. No
  production database, production credential, notification receiver, or real
  device was used.
- Remaining limits: this is deterministic SQL/evaluator lifecycle evidence;
  no live monitored host or production rollout is claimed. T012 begins the
  notification and suppression phase.

## T012 — ntfy transport safety fixtures

- Requirements: FR-008, FR-009, FR-010; SC-003, SC-011.
- Date: 2026-09-11 (Europe/London); implementation commit: `930b389`.
- Added the ntfy HTTP boundary and a disposable `httptest` receiver. The
  boundary accepts HTTPS by default and only permits explicitly opted-in HTTP
  for private or loopback addresses, validates every resolved address,
  rejects mixed forbidden DNS answers, credentials, query strings, fragments,
  path traversal, and reserved routing topics, and pins validated IPs while
  retaining the original host for TLS verification. It sends the token only as
  a Bearer header, exposes masked topic/token metadata, rejects redirects,
  bounds the UTF-8 JSON body to 4096 bytes, classifies 429/5xx/timeout and
  response-loss as retryable, and classifies other 4xx as permanent.
- Exact verification commands and outcomes: `go test
  ./internal/notifications/ntfy -race -count=1`; `go test ./... -count=1`;
  `go vet ./...`; and `git diff --check` passed. Positive fixtures covered
  accepted bearer-authenticated HTTPS/HTTP delivery, TLS, payload truncation,
  and Retry-After. Negative fixtures covered public/plain HTTP, unsafe DNS,
  unsafe URL components, reserved topics, redirects, timeouts, response loss
  after receiver acceptance, and permanent authentication failure. The
  receiver was local and disposable; no real ntfy endpoint or credential was
  used.
- Remaining limits: T013 still adds encrypted destination persistence,
  metadata-only control handlers, and delivery integration; this task does
  not claim live ntfy acceptance or subscriber receipt.

## T013 — encrypted ntfy destinations and direct test delivery

- Requirements: FR-008, FR-009; SC-003, SC-011.
- Date: 2026-09-11 (Europe/London); implementation commit: `9f1970b`.
- Added additive SQL/memory destination storage with a ten-active-destination
  bound, revision-checked updates, retirement, and test timestamps. Topic and
  token are stored together only as an envelope encrypted by the existing
  Scout wrapping key; normal list/get/control responses strip all encrypted
  fields and return only masked topic, token presence, and safe configuration
  metadata. The owner API validates the complete saved destination before
  create/update, preserves omitted PATCH secrets, supports explicit token
  removal, records only redacted audit outcomes, rejects retired destinations,
  and exposes a direct saved-revision test publish. Explicit private/loopback
  plain HTTP opt-in is persisted and returned as non-secret metadata.
- Exact verification commands and outcomes: `go test ./internal/control
  ./internal/store ./internal/notifications/ntfy -race -count=1`; `go test
  ./... -race -count=1`; `scripts/test-integration.sh` against disposable
  PostgreSQL 17; `npm run lint`; `npm run build`; `npm test`; `npm run
  format:check`; `npm run test:e2e:browser`; `go vet ./...`; and `git diff
  --check` all passed. Control tests used a local disposable HTTP receiver,
  asserted Bearer authentication and bounded payload routing, verified that
  metadata reads contain no envelope material or plaintext secrets, exercised
  token retention/removal, stale revision rejection, unsafe endpoint rejection,
  retirement, and the active destination cap. The SQL integration fixture
  verified migration, encrypted envelope persistence/decryption, restart
  reads, test timestamps, compare-and-swap, and retirement filtering.
- No real ntfy endpoint, notification credential, production database, real
  device, or production deployment was used. T015–T017 add quiet-hour,
  recovery, and notification UI behavior.

## T014 — durable notification delivery queue

- Requirements: FR-010, FR-013, FR-033; SC-003, SC-011.
- Date: 2026-09-11 (Europe/London); implementation commit: `1a9f178`.
- Added safe trigger/recovery delivery-intent projection, transactional
  incident/checkpoint/transition/intent commits, a 10,000-pending queue cap
  with an observable overflow counter, destination revision fencing, one
  in-flight lease per destination, epoch/owner CAS completion, bounded retry
  backoff with jitter, expiry, and redacted delivery-history reads. Delivery
  workers decrypt the saved destination only after a current enabled/revision
  check, publish through the existing ntfy safety boundary, and persist only
  accepted remote IDs or fixed safe error categories. Payloads contain no
  topic, token, lease, or raw upstream error.
- Exact verification commands and outcomes: `go test ./... -race -count=1`;
  `scripts/test-integration.sh` against disposable PostgreSQL 17; `go vet
  ./...`; and `git diff --check` passed. Positive tests cover trigger/recovery
  projection, encrypted local receiver acceptance, lease exclusivity, retry,
  acceptance, SQL restart-compatible persistence, and redacted owner history.
  Negative tests cover disabled/changed destinations, expiry, stale lease
  completion, queue overflow, malformed payload rollback in memory and SQL,
  and permanent upstream rejection. No real ntfy endpoint, production
  credential, real device, or production deployment was used.
- Remaining limits: T015 adds quiet-hour suppression, T016 adds restore and
  recovery no-replay policy, and T017 adds the notification settings UI and
  browser coverage. The worker is library-level at this task boundary; live
  notification acceptance remains intentionally limited to disposable local
  receivers.

## T015 — quiet windows and suppression release

- Requirements: FR-006, FR-011, FR-012; SC-004.
- Date: 2026-09-11 (Europe/London); implementation commit: `700dabb`.
- Added memory and PostgreSQL CRUD for bounded recurring and one-time
  suppression windows with revision checks, fleet/site/device targeting,
  IANA timezone validation, end-exclusive local-time matching, overnight
  semantics, unioned reasons, and restart-persistent suppression episodes.
  Repeated fall-back wall times match both UTC occurrences while skipped
  spring-forward wall times match none. Dependent service/state incidents are
  suppressed while their host is offline, while the host-offline incident and
  numeric incidents remain eligible. Suppressed transitions are persisted
  transactionally with incident state, and release closes each episode once
  with one bounded summary for still-active incidents; resolved incidents and
  disabled destinations produce no summary.
- Exact verification commands and outcomes: `go test ./... -race -count=1`;
  `scripts/test-integration.sh` against disposable PostgreSQL 17;
  `go vet ./...`; `npm run lint`; `npm run format:check`; `npm test`;
  `npm run check`; `npm run build`; `npm run test:e2e:browser`; and
  `git diff --check` all passed. Positive tests cover CRUD, pagination,
  target matching, unioned windows, DST fold/gap behavior, active summary
  release, and SQL restart persistence. Negative tests cover invalid window
  shapes, stale revisions, missing targets, end boundaries, resolved
  suppression, and idempotent episode closure. No real receiver, production
  credential, production database, or real device was used.
- Remaining limits: T016 adds restore/revocation races and recovery
  no-replay policy; T017 adds the notification settings UI and dedicated
  browser coverage.

## T016 — recovery pause, fresh evaluation, and notification resume

- Requirements: FR-009, FR-010, FR-013, FR-024; SC-003, SC-008.
- Date: 2026-09-11 (Europe/London); implementation commit: `614d832`.
- Restore and recovery now set an explicit notification pause fence, revoke
  sessions where required, cancel queued/retry outbox rows, invalidate expired
  delivery leases, reset evaluator timing/evidence while preserving active
  incident identity, and release alert-work leases for fresh evaluation.
  PostgreSQL uses workspace row locking and the same transaction for the
  recovery state, evaluation/work reset, and outbox cancellation. The
  notification worker checks the fence immediately before network I/O, so an
  already in-flight request may finish but no new request starts after the
  pause is acknowledged. The MFA-protected resume route (development mode
  keeps the existing no-MFA test path) requires the current workspace
  revision, rebuilds bounded summaries from active incidents only, cancels
  anything queued during reconciliation, and atomically queues only those
  fresh summaries.
- Exact verification commands and outcomes: `go test ./... -count=1`,
  `go test ./... -race -count=1`, `scripts/test-integration.sh` against
  disposable PostgreSQL 17, `go vet ./...`, `npm run lint`, and
  `npm run format:check` all passed. Positive tests cover the development
  resume endpoint, active-only bounded summaries, recovery evaluation reset,
  fresh alert-work scheduling, in-flight completion, and fresh-summary
  delivery. Negative tests cover stale resume revisions, queued work during
  recovery, paused worker claims, expired leases, disabled destinations, and
  resolved incidents omitted from summaries. No production database,
  notification endpoint, credential, real device, or deployment was used.
- Remaining limits: T017 adds the notification settings UI and dedicated
  Playwright coverage for overlapping windows, overnight/DST release,
  resolved suppression, and disabled destinations. Full backup parity across
  every 002 table remains T039.

## T017 — notification settings UI and browser coverage

- Requirements: FR-008, FR-010, FR-011, FR-012, FR-019; SC-003, SC-004,
  SC-010.
- Date: 2026-09-11 (Europe/London); implementation commit: `c86ea60`.
- Added an authenticated Notifications workspace with redacted ntfy
  destination metadata, write-only token entry, explicit enable/pause state,
  private-HTTP opt-in, saved-revision test/pause/remove actions, delivery
  status history, and recovery notification-fence/resume state. Added
  recurring fleet/site/device quiet windows, one-time maintenance windows,
  weekday selection, IANA timezone and overnight guidance, overlapping-window
  union guidance, and keyboard-native responsive controls. The existing
  Systems and device views also now tolerate discovered candidates whose
  address evidence is not available yet.
- Exact verification commands and outcomes: `go test
  ./internal/control -count=1`; `go test ./... -race -count=1`; `scripts/test-integration.sh`
  against disposable PostgreSQL 17; `go vet ./...`; `npm run check`; `npm run
  lint`; `npm run format:check`; `npm run build`; `npm test`; `npm run
  test:e2e:browser`; and `git diff --check` all passed. Browser coverage lives
  at `tests/e2e/playwright/notifications.spec.ts` under the configured
  Playwright test directory and uses one worker against a fresh local server.
  It publishes through the real authenticated ntfy client to a disposable
  localhost receiver, verifies Bearer delivery and secret redaction, proves a
  paused destination makes no request and the direct test boundary returns
  409, configures overlapping/overnight/DST/one-time windows, and verifies the
  suppressed delivery filter has no stale records. T015’s deterministic
  fixtures separately verify resolved incidents never produce a suppression
  release summary.
- No real ntfy endpoint, notification credential, production database, real
  device, or production deployment was used. The browser test proves ntfy
  server acceptance only; it does not claim subscriber receipt.

## T018 — systemd fixtures and collector contract cases

- Requirements: FR-014, FR-015, FR-016, FR-032; SC-005.
- Date: 2026-09-11 (Europe/London); implementation commit: `9953e11`.
- Added the controlled `systemd-units.json` fixture and tests for loaded
  active, inactive, failed, and activating/transitional services; excludes
  unloaded units and non-service units. Tests also cover exact and wildcard
  must-run selectors, bounded glob-only syntax (rejecting regex, character
  classes, path expansion, empty/oversize patterns, and more than 100
  patterns), the 1,000-entity partial inventory bound, and denied/unavailable
  D-Bus access.
- Exact verification commands and outcomes: `go test
  ./internal/collector/systemd -count=1`; `go test ./... -count=1`; `go test
  ./... -race -count=1`; `go vet ./...`; `npm run check`; `npm run
  format:check`; and `git diff --check` passed. All service data came from
  the repository fixture or an in-process fake manager; no host system bus
  was contacted.
- Remaining limits: T021 still needs the service inventory UI and controlled
  Linux evidence. No Linux host compatibility or provider support claim is
  made from fixtures.

## T019 — bounded read-only systemd adapter

- Requirements: FR-014, FR-015, FR-016, FR-032; SC-005.
- Date: 2026-09-11 (Europe/London); implementation commit: `9953e11`.
- Added a pinned `go-systemd/v22` adapter that opens only the system bus and
  calls `ListUnitsByPatternsContext` for `*.service`. It never exposes unit
  mutation methods, limits inventory to 1,000 loaded services, marks
  truncation as partial with a bounded diagnostic, maps active/inactive/
  failed/activating/deactivating/reloading and unknown states to typed
  statuses, preserves source/sub-state labels, and expires observations after
  three 30-second intervals. Must-run configuration is validated and matched
  with only `*` and `?`; the descriptor declares `systemd:read`, 30-second
  collection, 5-second deadline, and the existing shared registry/catalog
  integration.
- The adapter is fixture-injectable for deterministic tests, propagates
  cancellation, classifies access denial separately from unavailable bus
  errors, and closes its caller-owned D-Bus connection. No real host bus,
  production credential, production database, device, or deployment was
  used.
- Remaining limits: Live systemd distribution/architecture support remains
  unvalidated until the controlled Linux script in T021; this adapter has not
  been exercised against a production host.

## T020 — service-state incident evaluation and suppression

- Requirements: FR-001, FR-006, FR-014, FR-015; SC-001, SC-005.
- Date: 2026-09-11 (Europe/London); implementation commit: `7ef77c9`.
- Added the service observation bridge in `internal/alerts/services.go`.
  Stored systemd entities now become typed state observations with copied
  labels, three-interval freshness, unsupported/expired evidence, canonical
  API aliases, bounded `*`/`?` service-pattern matching, and collector
  matching. Default provisioning now includes a critical `systemd_failed`
  rule (first fresh failure, two healthy samples separated by the 30-second
  collection interval) and a warning `service_required_inactive` rule (60
  seconds inactive, 60 seconds active to clear). The latter is evaluated only
  for the collector's selected `mustRun` services, while failed services take
  the failure lineage without a duplicate inactivity incident. Service entity
  writes mark coalesced alert work; evaluation passes the entity device ID
  through the existing transactional evaluator, so dependent service
  notifications inherit host-offline suppression without suppressing the
  host-offline incident itself.
- Exact verification commands and outcomes: `go test ./... -race -count=1`,
  `go vet ./...`, `npm run check`, `npm run lint`, `npm run format:check`,
  `npm run build`, and `git diff --check` all passed. Positive tests cover
  fresh-to-expired service evidence, systemd failure trigger/recovery,
  selected must-run delay, owner alias/pattern resolution, and device-aware
  suppressed delivery intents. Negative tests cover unsupported states,
  regex-like/non-matching patterns, unselected inactivity, failed-plus-
  must-run duplication, and stale inventory. All service tests use in-process
  entities, a manual clock, the memory store, and a fake destination; no host
  D-Bus, real device, notification endpoint, production credential, or
  deployment was used.
- Remaining limits: T021 still needs the service view, persisted collector
  configuration UI flow, and controlled Linux live evidence. The background
  runtime has not yet been wired to emit systemd entities over the agent
  transport; that remains part of the subsequent agent/collector integration.

## T021 — service inventory controls and controlled systemd evidence

- Requirements: FR-014, FR-015, FR-016, FR-019, FR-034; SC-005, SC-009,
  SC-010.
- Date: 2026-09-11 (Europe/London); implementation commit: `829730a`.
- Extended the Services workspace with provider/device filtering, typed state
  labels, source and expiry timestamps, freshness status, failed counts,
  host association, must-run selection for observed systemd units, bounded
  collector diagnostics, and links from active service incidents into the
  incident detail view. The page uses native select/input/checkbox/button
  controls and states in text as well as color. It explicitly tells owners
  that inventory is evidence only and Scout exposes no service-control action.
  Added `scripts/test-systemd.sh`: its default path runs the fixture-backed
  collector and alert tests without touching the host system bus; an explicit
  owner-confirmed Linux mode performs only a read-only `systemctl` preflight.
- Exact verification commands and outcomes: `scripts/test-systemd.sh` passed
  the systemd and service-alert fixture suites and printed that no host bus was
  contacted; `npm run check`; `npm run lint`; `npm run format:check`;
  `npm run build`; `go test ./... -race -count=1`; `go vet ./...`; `npm run
  test:e2e:browser`; and `git diff --check` passed. The browser suite passed
  both configured Playwright journeys against a fresh local server. Negative
  coverage includes denied/unavailable systemd access, partial inventory,
  expired/unsupported service evidence, unselected inactivity, duplicate
  failed-plus-must-run suppression, and host-offline notification suppression.
- No live Linux systemd bus, production database, real device, production
  credential, service mutation, or deployment was used. The optional live
  script mode was not run because this workspace is macOS and no owner-
  authorized disposable Linux host was provided. The agent runtime still needs
  the subsequent transport wiring that sends service entities from a live
  enrolled agent; this task intentionally does not claim live inventory
  support or SC-009 compatibility evidence.

## T022 — diagnostic counter fixtures

- Requirements: FR-017, FR-018; SC-006.
- Date: 2026-09-11 (Europe/London); implementation commit: `2de8194`.
- Added deterministic temporary-root fixtures in
  `internal/collector/diagnostics_test.go` for `/proc/stat`, `/proc/loadavg`,
  `/proc/meminfo`, `/proc/diskstats`, and block identity metadata. The cases
  assert guest time is removed from CPU user time, load averages remain counts,
  zero-capacity swap remains a current zero, disk sectors use 512-byte units,
  no-operation latency is unavailable, counter resets create an unavailable
  gap, and changed device identity does not reuse the previous entity's
  counters.
- Exact verification commands and outcomes: `go test
  ./internal/collector -run TestDiagnostics -count=1`; `go test ./... -count=1`;
  `go test ./... -race -count=1`; `go vet ./...`; `npm run check`; `npm run
  lint`; `npm run format:check`; `npm run build`; and `git diff --check` all
  passed. Positive and negative counter cases passed without host filesystem,
  device, or systemd access.
- No production database, real device, production credential, or deployment
  was used. The fixtures establish semantics but are not live hardware
  compatibility evidence.

## T023 — host diagnostics schema and stable block identity

- Requirements: FR-017, FR-018, FR-024, FR-032; SC-006.
- Date: 2026-09-11 (Europe/London); implementation commit: `2de8194`.
- Implemented host schema version 2 diagnostic collection while retaining the
  legacy schema-1 transport fallback. Linux collection now reads CPU counters,
  load averages, swap, diskstats, and bounded block sysfs identity metadata.
  CPU metrics include guest-adjusted user, system, iowait, steal, and the
  existing utilization metric. Disk metrics expose rates, utilization, and
  operation-derived latency with reset, first-sample, zero-operation, and
  identity-change gaps represented as unavailable rather than fabricated zeroes.
  Stable WWID/WWN/serial/UUID values are hashed into opaque entity IDs;
  missing identity is explicitly labeled unstable. `FromCollector` carries
  schema 2 for diagnostic snapshots and continues to emit schema 1 for legacy
  snapshots, both accepted by the existing batch validator.
- Exact verification commands and outcomes: `go test
  ./internal/collector -run TestDiagnostics -count=1`; `go test ./... -count=1`;
  `go test ./... -race -count=1`; `go vet ./...`; `npm run check`; `npm run
  lint`; `npm run format:check`; `npm run build`; and `git diff --check` all
  passed. The telemetry protocol test confirms schema-1 compatibility and
  schema-2 acceptance. No diagnostic reader shells out or requests elevated
  privileges.
- No live Linux host, production database, real disk, production credential,
  or deployment was used. T024 still needs per-entity diagnostic chart/gap
  presentation and browser coverage; T030–T038/T042 remain the live
  storage/hardware/support evidence gates.

## T024 — entity-aware diagnostic charts and browser coverage

- Requirements: FR-017, FR-018, FR-019; SC-006, SC-010.
- Date: 2026-09-11 (Europe/London); implementation commit: `679fef2`.
- Added typed metric points with availability and optional extrema, entity-aware
  memory history/current projections matching PostgreSQL, and a diagnostic
  signal board in the device view. Owners can select a metric and entity with
  native controls; percent, byte, rate, latency, seconds, and count units use
  explicit formatting and honest chart domains. Downsampled ranges retain
  visible min/max bounds, unavailable points stay as chart gaps, and partial
  ranges/gap counts are announced in text. The accessible sample table exposes
  timestamps, values, extrema, and availability for keyboard inspection.
- Added `tests/e2e/playwright/z-diagnostics.spec.ts`, which uses a bounded
  fixture route against the real authenticated UI to verify separate disk
  series, unit-aware values, extrema, focusable metric/entity controls, and
  unavailable-gap rendering. The `z-` filename preserves the existing
  first-run test's one-owner ordering because this Playwright configuration
  intentionally shares one isolated in-memory server across its browser files.
- Exact verification commands and outcomes: `go test ./internal/store
  -count=1`; `go test ./... -race -count=1`; `go vet ./...`; `npm run check`;
  `npm run lint`; `npm run format:check`; `npm run build`; `npm run
  test:e2e:browser`; and `git diff --check` all passed. The final browser run
  passed all three configured journeys against a fresh local server. An
  earlier run caught the shared-server ordering issue; it was corrected before
  this evidence entry rather than weakening the first-run assertion.
- No live Linux host, production database, real device, production credential,
  or deployment was used. The fixture proves presentation and contract
  behavior; live diagnostic collection remains limited to the fixture-backed
  Linux collector evidence in T022/T023.

## T025 — deterministic rollup contract fixtures

- Requirements: FR-020, FR-021, FR-022, FR-023; SC-007, SC-008.
- Date: 2026-09-11 (Europe/London); implementation commit: `51733f9`.
- Added `internal/telemetry/rollups_test.go` as a deterministic contract
  fixture layer for UTC-aligned half-open five-minute/hourly buckets, cadence
  changes, strict retention boundaries, valid-sample statistics, unavailable
  samples, clipped partial query boundaries, interval-union coverage, late
  sample recomputation without duplicate bucket keys, weighted coarser means,
  and a 8,760-hour year grouped to 584 points while retaining a known spike
  and the complete span. Invalid history limits below 2 or above 600 are
  rejected in the fixture contract.
- Exact verification commands and outcomes: `go test ./internal/telemetry
  -count=1`; `go test ./internal/telemetry -run TestRollupFixture -count=1`;
  `go vet ./internal/telemetry`; and `git diff --check` all passed. These
  fixtures are intentionally independent of PostgreSQL and do not claim that
  the production rollup worker or history tier selector is implemented yet.
- No production database, real device, production credential, or deployment
  was used. T026 must implement the leased generation-aware worker against
  these semantics before this story's historical gate can pass.

## T026 — leased generation-aware historical rollups

- Requirements: FR-020, FR-021, FR-023; SC-007, SC-008.
- Date: 2026-09-11 (Europe/London); implementation commit: `14961d3`.
- Added authoritative PostgreSQL five-minute rollups from retained raw
  samples and hourly rollups from their twelve five-minute children. Accepted
  current samples contribute count/sum/min/max; unavailable samples retain
  expected cadence without fabricating a value. Coverage is the union of
  cadence intervals clipped to the bucket and the next observation, and agent
  cadence is persisted with each sample/series. Every accepted sample marks
  both tiers dirty in the ingestion transaction. The worker claims up to 1,000
  rows with owner/epoch/lease fencing, overwrites the stable aggregate key,
  dirties the hourly parent in the same completion transaction, and records
  bounded failure diagnostics. Late samples therefore invalidate newer
  generations and stale workers cannot commit.
- `BackfillRollupWork` bounds the distinct retained raw five-minute source
  buckets, enqueues both resolutions with conflict-safe generation increments,
  and rejects reversed ranges or limits above 1,000. A PostgreSQL migration
  adds the interval and lease-owner compatibility columns for existing
  installations. `tests/integration/z_rollups_test.go` exercises lease
  takeover, pending hourly dependencies, stale completion rejection, late
  sample recomputation, weighted/hourly sufficient statistics, coverage,
  bounded backfill, stable aggregate primary keys, and invalid bounds.
- Exact verification commands and outcomes: `scripts/test-integration.sh`
  passed against disposable PostgreSQL 17; `go test ./... -race`; `go vet
  ./...`; `npm run format:check`; `npm run lint`; `npm run check`; `npm run
  build`; `npm test`; and `git diff --check` all passed. No production
  database, real device, production credential, or deployment was used.
- Remaining limits: T027 still owns automatic history tier selection,
  full-range empty buckets, and API resolution/coverage metadata; T028 owns
  retention policy and rollup pressure telemetry. The bounded integration
  fixture is not a 100-host/year capacity claim.

## T027 — tiered metric history queries and quality metadata

- Requirements: FR-019, FR-021, FR-022; SC-007.
- Date: 2026-09-11 (Europe/London); implementation commit: `d2415b2`.
- Extended memory and authoritative PostgreSQL history queries with bounded
  defaults (maximum 600 points and 16 explicit series), automatic raw/
  five-minute/hourly tier selection from the oldest requested time, UTC-aligned
  integer source-resolution grouping, and complete requested ranges including
  empty buckets. Responses now expose stable series/entity identity,
  resolution, count, coverage, extrema, and partial-boundary metadata. Raw
  history uses cadence intervals for coverage; aggregate history combines
  sufficient statistics without double-counting tiers. Explicit unknown series
  return an empty identity-preserving unavailable series, and explicit filter
  mismatches cannot fall back to host totals. The HTTP response reports the
  effective defaulted `from`/`to` bounds and accepts repeated `seriesId`
  parameters.
- Added red/green fixtures for partial first/last buckets, weighted means,
  extrema, interior gaps, grouped resolution, invalid point/series bounds,
  unknown and mismatched explicit series, SQL raw history, SQL hourly
  aggregate history, and authenticated HTTP metadata/negative cases. The
  existing SQL identity fixture now queries explicit bounds so its assertions
  remain about entity identity rather than default-range placement.
- Exact verification commands and outcomes: `scripts/test-integration.sh`
  passed against disposable PostgreSQL 17; `go test ./... -count=1` passed;
  `go vet ./...` passed; `npm run format:check` passed; `npm run lint`
  passed; `npm run check` passed; `npm run build` passed; and
  `npx playwright test tests/e2e/playwright/z-diagnostics.spec.ts` passed.
  `git diff --check` passed before commit. No production database, real
  device, production credential, or deployment was used.
- Remaining limits: T028 still owns retention policy coordination, dirty
  generation/pressure status, and operator policy changes. T029 still owns
  history resolution/partial metadata presentation in the device/recovery UI
  and its mixed-version/interrupted-aggregation integration evidence. The
  disposable fixture does not claim 100-device/year capacity or live hardware
  support.

## T028 — durable monitoring policy and retention coordination

- Requirements: FR-020, FR-023, FR-024, FR-033; SC-008, SC-011.
- Date: 2026-09-12 (Europe/London); implementation commit: `3f776bd`.
- Added the versioned `monitoring_settings` store with revision-fenced policy
  updates, bounded disk budgets, tiered raw/five-minute/hourly retention, and
  five-minute idempotent retention previews. Destructive retention reductions
  require an unexpired preview tied to the expected revision. Monitoring
  status reports evaluation/rollup lag, bounded queues, dropped/truncated/
  backpressure/admission-failure counters, delivery failures, storage
  pressure thresholds, and last successful job times without exposing secrets.
- Retention now uses observation-time cutoffs and keeps raw samples while
  required five-minute/hourly rollups are missing, partial, dirty, or queued;
  completed aggregate generations are safe to delete according to their tier.
  Ingestion, collector diagnostics, active-incident admission, rollups, and
  recovery paths contribute bounded operational counters and job timestamps.
  Authenticated monitoring settings, preview, and status routes are covered by
  a development-mode HTTP test that confirms the preview fence and negative
  path.
- Exact verification commands and outcomes: `go test
  ./internal/store ./internal/telemetry ./internal/control`; `scripts/test-integration.sh`
  against disposable PostgreSQL 17; `go test ./... -count=1`; `go vet ./...`;
  `npm run format:check`; `npm run lint`; `npm run check`; `npm run build`; and
  `git diff --check` all passed. The SQL integration fixture proves that an
  unaggregated raw row is deferred and is removed only after both aggregate
  tiers are committed.
- No production database, real device, production credential, or deployment
  was used. The device/recovery UI wiring and interrupted or mixed-version
  aggregation presentation evidence were completed in T029; live hardware,
  100-device/year capacity, and production deployment remain unclaimed gates.

## T029 — history quality and retention review UI

- Requirements: FR-019, FR-020, FR-021, FR-022, FR-023, FR-024; SC-007,
  SC-008, SC-010.
- Date: 2026-09-12 (Europe/London); implementation commit: `f2c0d26`.
- Device history now presents the server-selected resolution, explicit partial
  buckets, per-bucket sample counts, coverage, availability, and empty gaps in
  the chart and accessible sample table. The UI uses the API's partial metadata
  rather than inferring an incomplete range from the age of the first point.
  Recovery review now exposes queue/lag/storage pressure, bounded counters,
  retention days, an exact revision-fenced retention preview, affected ranges,
  expiry, estimated deletions, and an explicit apply action.
- Added `tests/e2e/playwright/z-history-retention.spec.ts` for authenticated
  history metadata and recovery retention-preview/apply journeys. Extended
  `tests/integration/z_rollups_test.go` with two agent versions that preserve
  base CPU telemetry while reporting an unsupported collector explicitly; the
  same file retains the interrupted lease/generation and retry coverage from
  T026. Updated the diagnostics fixture to carry the explicit coverage
  contract.
- Exact verification commands and outcomes: `go test ./... -count=1`; `go vet
  ./...`; `scripts/test-integration.sh` against disposable PostgreSQL 17;
  `npm run format:check`; `npm run lint`; `npm run check`; `npm run build`;
  `npm run test:e2e:browser` (5/5 tests passed); and `git diff --check` all
  passed. No production database, real device, production credential, or
  deployment was used.
- T032 is now the next unchecked task. Live hardware support, 100-device/year
  capacity, and production deployment remain unclaimed evidence gates.

## T030/T031 — SMART storage fixtures and bounded collector

- Requirements: FR-025, FR-027, FR-028, FR-032; SC-009, SC-011.
- Date: 2026-09-12 (Europe/London); implementation commit: `ad33cba`.
- Added deterministic, secret-free smartctl JSON fixtures for SATA/ATA health
  failure, SAS, NVMe, standby, and permission denial, including process exit
  bitmask cases and absent attributes. Tests cover host-scoped identity that
  survives a path change, separates replacement media, and marks repeated
  serials as ambiguous instead of merging them.
- Implemented the SMART adapter through the shared collector contract. It uses
  a fixed `smartctl --scan -j` discovery call and fixed per-device
  `-j -n standby,0 -d <allowlisted-type> -- <path>` reads, with no shell or
  arbitrary utility arguments. Output is capped at 1 MiB, each device has a
  10-second deadline, the full collection has a 60-second bound, and the
  inventory is capped at 64 devices. Temperature, wear, and error fields are
  emitted with explicit unavailable values when absent; health exit bits and
  SMART/NVMe fault state remain visible without discarding valid readings.
  Standby, denied access, malformed output, missing smartctl, truncation, and
  per-device failures remain bounded diagnostics, while other disks continue.
  The descriptor declares SMART/block-device read permissions and is listed in
  the shared registry.
- Exact verification commands and outcomes: `go test
  ./internal/collector/... -count=1`; `go vet ./internal/collector/...`;
  `go test ./... -count=1`; `go vet ./...`; `npm run lint`; `npm run
  format:check`; `npm run check`; `npm run build`; `scripts/test-integration.sh`
  against disposable PostgreSQL 17; `npm run test:e2e:browser` (5/5 passed);
  and `git diff --check` all passed.
- The development host does not have smartmontools or representative SATA,
  SAS, or NVMe hardware available, so no live family support claim is made.
  T033 is next; ZFS live evidence remains a later T034/release gate.

## T032 — bounded ZFS storage collector

- Requirements: FR-026, FR-027, FR-028, FR-032; SC-009, SC-011.
- Date: 2026-09-12 (Europe/London); implementation commit: `ba39a0a`.
- Added secret-free OpenZFS pool, dataset, scrub, partial, permission, and
  cumulative-counter fixtures. Pool and dataset entities use host-scoped
  GUID identities when present; name-only identities are marked unstable and
  duplicate GUID/name evidence is kept ambiguous. A changed GUID produces a
  new entity identity rather than merging replacement history.
- Implemented fixed, read-only `zpool list -H -p -o
  name,guid,size,allocated,health`, `zfs list -H -p -o
  name,guid,used,available`, and `zpool status -p` queries with `LC_ALL=C`, no
  shell, 1 MiB output bounds, 10-second per-command deadlines, and a
  20-second total collector deadline. Linux ZFS kstat counters are read only
  from the fixed `/proc/spl/kstat/zfs` path (injectable in tests); decreases or
  missing samples produce unavailable rate gaps rather than fabricated zeroes.
  Pool allocation/capacity is labeled physical, dataset used/available is
  labeled usable, and scrub/health/data-error state remains typed labels with
  explicit hardware-fault predicates.
- Positive and negative fixtures cover ONLINE/DEGRADED pools, finished and
  in-progress scrubs, explicit data errors, counter rates, counter reset,
  malformed rows, denied access, missing counters, empty inventories,
  truncated output, replacement identity, and ambiguous identity. The ZFS
  descriptor is registered with bounded permissions and an entity cap of 32
  pools plus 1000 datasets.
- Exact verification commands and outcomes: `go test
  ./internal/collector/zfs ./internal/collector -count=1`; `go vet
  ./internal/collector/...`; `go test ./... -count=1`; `go vet ./...`; and
  `git diff --check` all passed. No production database, real device, ZFS
  pool, production credential, or deployment was used; the development host
  has no live OpenZFS lab, so T034 remains the live-evidence gate.
- T034 is now the next unchecked task. Live SMART/ZFS family support, expanded
  load capacity, and production deployment remain unclaimed evidence gates.

## T033 — typed storage-fault evaluation and storage health view

- Requirements: FR-001, FR-019, FR-025, FR-026, FR-027, FR-028; SC-009,
  SC-010.
- Date: 2026-09-12 (Europe/London); implementation commit: `ac4c06c`.
- Added typed storage observations for SMART disks and ZFS pools. Explicit
  failing SMART health, NVMe critical-warning/family predicates already
  translated by the collector, ZFS degraded pool states, and explicit ZFS
  scrub/data errors can trigger the critical storage-fault lineage. Wear and
  error counters alone remain healthy evidence; a scrub in progress alone is
  healthy; missing/denied/expired evidence is unsupported or unknown and
  cannot recover an active incident. Storage recovery requires two fresh
  samples separated by the provider collection interval. ZFS datasets remain
  inventory-only for this fault rule, so one device/pool/entity key cannot
  create a duplicate dataset fault.
- Added the authenticated Storage workspace using the existing service-entity
  API. It polls for updates, filters by device, links active incidents, shows
  stable identity/freshness metadata, and renders SMART metrics plus ZFS pool
  physical allocation and dataset usable space in separate sections. The UI
  explicitly explains that physical and usable capacity are not summed or
  treated as one health value, and all health meaning is represented by text
  and icons as well as color.
- Exact verification commands and outcomes: `go test
  ./internal/alerts -race -count=1`; `go test ./... -count=1`; `go vet
  ./...`; `npm run check`; `npm run build`; `npm run lint`; `npm run
  format:check`; and `git diff --check` all passed. No production database,
  credential, real storage device, ZFS pool, or deployment was used.
- Remaining limits: T034 still owns the fixture-backed/live SMART and
  disposable-ZFS evidence script and support record. The agent runtime has
  not yet been wired to transmit SMART/ZFS service entities from a live
  enrolled host; no live hardware support claim is made.

## T034 — storage health evidence runner

- Requirements: FR-025, FR-026, FR-027, FR-028, FR-034; SC-009.
- Date: 2026-09-12 (Europe/London); implementation commit: `4e92a33`.
- Added `scripts/test-storage-health.sh`. Its default path runs the SMART,
  ZFS, and typed storage-alert fixture suites with race detection and does
  not contact host utilities or devices. The opt-in lab path requires both
  `SCOUT_STORAGE_LAB=1` and `SCOUT_STORAGE_LAB_CONFIRM=YES`, requires Linux,
  reports only utility versions, command exit status, bounded row counts, and
  the invoking uid/groups, and suppresses raw SMART serial/WWN/path and ZFS
  status output. It executes only the fixed read queries used by the
  collectors and never uses sudo, wakes disks, starts tests, creates or
  destroys pools, scrubs, repairs, imports, exports, or mutates datasets.
- Exact verification commands and outcomes: `scripts/test-storage-health.sh`
  passed the SMART/ZFS/storage-alert fixtures; `bash -n
  scripts/test-storage-health.sh` passed; `SCOUT_STORAGE_LAB=1
  scripts/test-storage-health.sh` returned the expected status 2 for missing
  confirmation; and `SCOUT_STORAGE_LAB=1 SCOUT_STORAGE_LAB_CONFIRM=YES
  scripts/test-storage-health.sh` returned the expected status 2 on this
  macOS development host without contacting storage utilities. Existing
  fixture suites prove SATA/SAS/NVMe exit-bitmask handling, path-change and
  replacement separation, ambiguous identities, ZFS GUID replacement, and
  permission/standby/reset behavior. No production database, credential,
  real storage device, ZFS pool, or deployment was used.
- Remaining limits: no owner-authorized disposable Linux host or physical
  SMART/ZFS lab is available in this workspace, so exact live utility/kernel
  versions, grants, and representative family compatibility remain
  unvalidated. The script is ready for that explicitly authorized run; no
  live support claim is made.

## T035 — bounded hwmon sensor collector

- Requirements: FR-029, FR-031, FR-032; SC-009, SC-011.
- Date: 2026-09-12 (Europe/London); implementation commit: `69c5d9b`.
- Added temporary fixture trees covering hwmon temperature and fan channels,
  negative millidegrees, zero RPM, optional labels, absent alarms, explicit
  alarm faults, malformed values, negative fan values, sensor renumbering,
  exact exclusions, unsafe symlink targets, empty inventories, and the 256
  entity bound. Stable IDs include the host, resolved device path, chip when
  available, channel kind, and channel number; hwmon entry names are never
  used alone. Duplicate aliases of the same resolved channel are suppressed.
- Implemented a read-only hwmon adapter through the collector contract. It
  reads only fixed `name`, `tempN_input/label/alarm`, and
  `fanN_input/label/alarm` attributes, resolves every device and attribute
  within the configured sysfs root, caps each read at 4 KiB, caps directory
  enumeration and sensor entities, and enforces a five-second collector
  deadline. Celsius conversion preserves supported negative values; fan
  conversion preserves zero and rejects negative readings as unavailable.
  Alarm `1` is the only sensor hardware-fault predicate; absent or invalid
  alarms remain unavailable, and temperature max/crit files do not create a
  default incident. Per-field capability labels and metric availability make
  malformed or inaccessible values explicit without blocking other channels.
- Exact verification commands and outcomes: `gofmt -w
  internal/collector/sensors/collector.go
  internal/collector/sensors/collector_test.go`; `go test
  ./internal/collector/sensors ./internal/collector -race -count=1`; `go vet
  ./internal/collector/sensors ./internal/collector`; and `git diff --check`
  all passed. No production database, credential, real device, or sysfs host
  mount was used.
- Remaining limits: the development host is macOS and has no owner-authorized
  representative Linux hwmon lab, so live driver/kernel support and exact
  permissions remain unvalidated. T037 is next; it must expose persisted
  exclusions and the hardware view, while T038 remains the live hardware
  evidence gate.

## T036 — bounded GPU collectors

- Requirements: FR-030, FR-031, FR-032; SC-009, SC-011.
- Date: 2026-09-12 (Europe/London); implementation commit: `99d8b2e`.
- Added secret-free fixtures for NVIDIA CSV rows with UUID/PCI identities,
  `N/A` fields, malformed fields, duplicate evidence, AMD DRM/sysfs VRAM and
  hwmon temperature/power, and Intel DRM/sysfs local-memory plus cumulative
  energy samples. Intel tests cover the first-sample gap, watts derived from
  energy deltas, and a counter reset gap; the cross-source fixture confirms a
  PCI-identical sysfs GPU is not reported twice.
- Implemented a fixed `nvidia-smi` query with CSV/no-header/no-units parsing,
  1 MiB output and 10-second collection bounds, without a shell or arbitrary
  arguments. NVIDIA memory values are converted from MiB to bytes; `N/A`
  remains unavailable. AMD and Intel use bounded, allowlisted DRM, PCI
  `uevent`, driver, VRAM/local-memory, hwmon, and Intel energy paths only.
  GPU identity uses vendor UUID or PCI slot where available; fallback evidence
  is explicitly unstable and never uses a card index alone. Power labels
  identify `gpu-board` versus Intel `package` scope, and missing fields emit
  per-metric availability instead of zeroes.
- Exact verification commands and outcomes: `gofmt -w
  internal/collector/gpu/collector.go
  internal/collector/gpu/collector_test.go internal/collector/registry.go`;
  `go test ./internal/collector/gpu ./internal/collector -race -count=1`;
  `go vet ./internal/collector/gpu ./internal/collector`; and `git diff
  --check` all passed. No production database, credential, real GPU, driver,
  or host sysfs mount was used.
- Remaining limits: this macOS workspace has no owner-authorized NVIDIA,
  AMD, or Intel Linux lab, so live driver/kernel versions, permissions, and
  family compatibility remain unvalidated. T037 is next; T038 remains the
  live hardware evidence gate and no GPU family is advertised as validated.

## T037 — hardware telemetry view and exact exclusions

- Requirements: FR-019, FR-029, FR-030, FR-031; SC-009, SC-010.
- Date: 2026-09-12 (Europe/London); implementation commit: `7456380`.
- Added the authenticated Hardware workspace to the main navigation. It
  filters sensor and GPU entities by enrolled device, shows online/fault/
  unavailable state, stable identity and freshness metadata, and renders each
  field with its unit and explicit availability. GPU history uses the existing
  bounded metrics API and keeps unavailable/partial samples as visible gaps;
  tabular samples remain available for keyboard and screen-reader users.
  The view exposes exact stable sensor IDs as checkbox exclusions and persists
  only the bounded `excludedIds` collector setting with revision fencing. It
  does not create universal thermal, fan, utilization, or wear thresholds;
  explicit owner alert rules remain the path for thresholds.
- Exact verification commands and outcomes: `npx prettier --write
  apps/web/src/views/hardware.tsx apps/web/src/App.tsx apps/web/src/styles.css
  tests/e2e/playwright/hardware.spec.ts`; `npm run format:check`; `npm run
  check`; `npm run build`; and `npx playwright test
  tests/e2e/playwright/hardware.spec.ts` all passed. The focused browser test
  used a secret-free fixture and proved current temperature rendering,
  unavailable GPU fields, visible history gaps, stable-ID-only exclusion
  persistence, and the success response after refresh. No production
  database, credential, real device, or deployment was used.
- Remaining limits: the development host is macOS and has no owner-authorized
  representative Linux hwmon/GPU lab, so live driver/kernel and permission
  support remains unvalidated under T038. The agent runtime still needs to
  schedule and transmit the sensors/GPU collector results from enrolled hosts;
  this UI evidence does not claim live hardware telemetry delivery.

## T038 — hardware monitoring evidence runner and support matrix

- Requirements: FR-029, FR-030, FR-031, FR-032, FR-034; SC-009, SC-011.
- Date: 2026-09-12 (Europe/London); implementation commit: `b4a1334`.
- Added `scripts/test-hardware-monitoring.sh` with a fixture-first default and
  an explicitly confirmed Linux-only lab path. The lab path requires a
  timeout utility, reads only fixed hwmon/DRM paths, runs the collector's exact
  bounded NVIDIA query under a 12-second timeout, suppresses raw identifiers,
  and performs no sudo, driver load, sysfs write, power change, stress, or
  hardware mutation. The support matrix now distinguishes fixture-validated
  sensor/NVIDIA/AMD/Intel parsing from unvalidated live family support.
- Exact verification commands and outcomes: `scripts/test-hardware-monitoring.sh`
  passed all sensor/GPU/registry fixture tests and the script syntax check;
  `bash -n scripts/test-hardware-monitoring.sh` passed; and
  `SCOUT_HARDWARE_LAB=1 scripts/test-hardware-monitoring.sh` returned the
  expected status 2 on this macOS development host without contacting host
  hardware. No production database, credential, real device, or deployment
  was used.
- Remaining limits: no owner-authorized Linux sensor/GPU lab was available, so
  exact kernel, driver, utility, permissions, and device-model results remain
  live-unvalidated. Fixture results do not advertise NVIDIA, AMD, Intel, or
  hwmon production support. The agent runtime still needs to schedule and
  transmit these collector results from enrolled hosts.

## T039 — monitoring backup/restore coverage and no-replay recovery

- Requirements: FR-013, FR-024; SC-008.
- Date: 2026-09-12 (Europe/London); implementation commit: `409597c`.
- Backup metadata now verifies that all 21 002 monitoring tables exist before
  creating a dump and records the storage generation/phase, current migration
  checkpoint count, and monitoring-settings revision. Restore validates those
  values after `pg_restore` before applying the recovery fence. The restore
  fence pauses workspace and monitoring delivery state, cancels queued/retry/
  sending notification work, resets alert evaluation timing and worker leases,
  closes stale suppression episodes, and queues only fresh post-reconciliation
  evaluation work.
- Added `tests/integration/monitoring_recovery_test.go`. Against disposable
  PostgreSQL it verifies representative rows and encrypted key material across
  every 002 table, preserves telemetry receipt/sample-ordinal/current-series
  identity, migration checkpoints, aggregate and rollup work, rules/incidents,
  notification destinations, suppression policy, and retention previews. It
  then starts SQL recovery, proves stale notification work is cancelled and
  cannot be claimed, and proves alert timing is reset while durable history and
  suppression context remain present.
- Exact verification commands and outcomes: `scripts/test-integration.sh`
  passed against a disposable PostgreSQL 17 container; `go test ./... -count=1`;
  `go vet ./...`; `scripts/test-restore.sh`; `bash -n scripts/backup.sh
  scripts/restore.sh scripts/test-restore.sh`; `npm run check`; `npm run build`;
  `npm run lint`; `npm run format:check`; `go mod verify`; and `git diff
  --check` all passed. The missing-DSN restore path remains fixture-only and no
  production database, credential, or real device was used.
- Remaining limits: the actual `pg_dump`/`pg_restore` command pair was not run
  against an owner-provided protected restore destination in this workspace;
  the scripts are explicitly opt-in for that operation. A future authorized
  restore run must record PostgreSQL versions, separate key custody, clean
  destination, restored row parity, and owner reconciliation evidence.
