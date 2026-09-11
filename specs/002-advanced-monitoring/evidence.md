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
