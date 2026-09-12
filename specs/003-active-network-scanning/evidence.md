# Evidence Ledger: Active Network Scanning

## Specification delivery

2026-09-11: specification and implementation package prepared from user direction, Scout constitution, 001 discovery/enrollment contracts, and current code inspection. Existing active implementation changes in the working tree were not modified or treated as feature evidence.

Document validation establishes artifact consistency only. It does not prove active scanning, packet boundaries, enrollment, performance, or compatibility.

## Implementation evidence

No implementation or live network scan is claimed by this package.

For every completed task or release gate append:

- task, requirement, story, and success-criterion IDs;
- date and commit;
- exact command and environment, with secrets redacted;
- authorized ranges, exclusions, scanner identity, topology, and capture point for live tests;
- expected outcome and actual outcome;
- logs, packet capture, screenshots, timing, and storage artifacts where applicable;
- remaining limits and unverified platforms.

Never attach credential values, private keys, host-key private material, banners, packet payloads, or production addressing.

## T001 — 001 prerequisite gate

- Requirements: FR-003, FR-004, FR-013, FR-014, FR-022.
- Date: 2026-09-12 (Europe/London); implementation commit: 8cd137a.
- Baseline: repository was clean at commit `1fc68ec` with the configured
  `origin` remote `https://github.com/doomedramen/scout.git`. The 001 handoff,
  evidence ledger, and task ledger were inspected before this gate.
- Exact verification commands and outcomes: `go test ./internal/... -count=1`
  passed; `scripts/test-first-agent.sh` passed its fixture coverage;
  `scripts/test-enrollment.sh` passed finite target, exclusion, method,
  bounds, duplicate, and restart fixtures; and `scripts/test-restore.sh`
  passed its fixture restore/recovery boundary. No live device, production
  database, or credential was contacted.
- Inherited boundaries confirmed for this feature: owner authentication and
  CSRF/MFA mutation fencing; host/site/scope and exclusion policy; encrypted,
  write-only credential storage and normalized host trust; target-bound
  enrollment jobs with current revision/epoch/destination/release checks;
  authenticated desired state; audit events; global pause/recovery fencing;
  PostgreSQL backup/restore checks; and authenticated telemetry heartbeat and
  batch ingestion. Active scanning must call these boundaries rather than
  create parallel authority paths.
- Unmet prerequisites intentionally remain open in 001 and are not duplicated
  here: native Linux/systemd first-agent acceptance (T017), live clean-host
  restore (T022), power-loss updater lab (T030), live second-vantage placement
  (T044), full browser viewport/accessibility acceptance (T049), live
  collector/provider compatibility (T055), full offline decommission lab
  (T059), and live capacity/disk-pressure measurement (T061). In particular,
  no 001 evidence authorizes scanning the owner's LAN or installing an agent
  on a real device.

## T002 — active scanning contracts

- Requirements: FR-001–FR-008, FR-016, FR-018, FR-021.
- Date: 2026-09-12 (Europe/London); implementation commit: 4f2a489.
- Added strict JSON Schema contracts for typed scan policies and entry points,
  bounded runs and result pages, credential-free agent assignments, redacted
  observations, candidate/access detail, scan status, and safe cancellation or
  re-evaluation requests. Existing scope schemas now accept the additive scan
  policy shape. The OpenAPI document includes owner scan configuration, run,
  candidate, status, and authenticated agent result routes with bounded
  headers, body references, and recent-MFA annotations where network authority
  changes.
- Exact verification commands and outcomes: `npx prettier --write` over the
  changed OpenAPI and schema files completed; `go test ./tests/contracts
  -count=1` passed, including JSON validity, security-boundary, and existing
  bounded-route checks; and `git diff --check` passed. No route handler has
  been claimed yet, and no scan or device was contacted.
- Remaining limits: T003 must add executable positive/negative scan fixtures
  and reference validation; runtime routes, persistence, agent execution,
  policy enforcement, and UI remain unimplemented.

## T003 — active scanning contract fixtures

- Requirements: FR-001, FR-004–FR-008, FR-015, FR-018.
- Date: 2026-09-12 (Europe/London); implementation commit: 4f2a489.
- Added positive fixtures for policy, assignment, result page, candidate, and
  run creation plus negative fixtures for unknown fields, bound overflow,
  unsupported UDP transport, remote identity injection, excluded targets, and
  stale revisions. Paired replay fixtures share a run/page identity but have
  different content hashes.
- `tests/contracts/discovery_contract_test.go` now resolves local JSON Schema
  references and validates the exercised strict shapes, pins every configured
  rate/concurrency/target/attempt/timeout/deadline/page bound, applies an
  exclusion decision, and checks wrong-scanner, stale-revision, and conflicting
  replay semantics. The fixture validator deliberately treats remote identity
  as transport-authenticated and rejects it from the body.
- Exact verification commands and outcomes: `gofmt -w
  tests/contracts/discovery_contract_test.go`; `npx prettier --write
  tests/contracts/fixtures/scan-*.json`; `go test ./tests/contracts -count=1`;
  and `git diff --check` passed. No scanner, route handler, real network, or
  credential was used.
- Remaining limits: schema/reference tests are contract-level checks; T004+
  must enforce the same bounds and fencing in durable stores and handlers.

## T004 — scan persistence entities and migration

- Requirements: FR-001, FR-002, FR-003, FR-007, FR-009, FR-010, FR-015,
  FR-019, FR-022.
- Date: 2026-09-12 (Europe/London); implementation commit: 9dc4f79.
- Added versioned migration 11 with disabled-by-default scan policies,
  explicit server/agent vantage assignments, bounded run state and active
  uniqueness, lease fencing, paged result receipts, append-only TCP entry-point
  observations, per-vantage current projections, candidate extensions, and
  candidate/method/endpoint access-request dedupe columns and keys. Added
  corresponding Go entities and backward-compatible in-memory state maps.
  Existing scope/candidate/access models retain their identity while gaining
  additive scan fields.
- Exact verification commands and outcomes: `gofmt -w
  internal/store/models.go internal/store/store.go internal/store/migrations.go`;
  `go test ./internal/store -count=1`; `scripts/test-integration.sh` against a
  disposable PostgreSQL 17 container; and `git diff --check` passed. Running
  migrations twice remains covered by the integration harness. No scan was
  enabled and no real device or credential was used.
- Remaining limits: normalized scan-table parity, concurrent PostgreSQL lease
  contention, and scanner/result policy validation remain for T005–T009.

## T005 — scan store authority and fencing

- Requirements: FR-007–FR-009, FR-015, FR-016, FR-018, FR-019, FR-022.
- Date: 2026-09-12 (Europe/London); implementation commit: c04bb7e.
- Added store operations for disabled-by-default policy materialization and
  expected-revision updates, one active run per scope/vantage pair, owner
  idempotency, lease epochs and expiry re-lease, owner/epoch transition
  checks, cancellation and terminal fencing, bounded result receipt replay,
  conflicting-page rejection, atomic outcome counters, current per-vantage
  projections, and cursor-bounded run/observation pages. SQL-backed stores
  use the existing serialized transactional workspace state while migration
  11 provides the normalized PostgreSQL authority tables for the subsequent
  SQL parity work.
- Tests were written first and initially failed with missing store methods;
  the intended failures were then resolved. Exact verification commands and
  outcomes: `go test ./internal/store -run 'TestScan' -count=1`; `gofmt -w
  internal/store/scanning.go internal/store/scanning_test.go`; and `git diff
  --check` passed. The full disposable PostgreSQL migration suite had already
  passed in T004; this task's store tests are deterministic in-memory tests.
- Remaining limits: normalized-table reads/writes and concurrent PostgreSQL
  lease contention remain for T009; agent assignment and capability handoff
  remain for T008–T009.
  No scan or device was contacted.

## T006 — bounded scan policy authority

- Requirements: FR-001–FR-006, FR-018.
- Date: 2026-09-12 (Europe/London); implementation commit: 4c3558c.
- Added typed TCP entry-point catalog validation with SSH as the only default
  access-capable entry point and observation-only alternate ports. Added scan
  policy normalization with safe schedule, rate, concurrency, target, attempt,
  timeout, deadline, and result-page defaults; attempt multiplication bounds;
  finite IPv6 prefix enforcement; explicit assigned-agent and server-vantage
  checks; exclusion, scope, pause, policy-revision, entry-point, and server
  opt-in pre-probe decisions. Removed a vet-detected self-assignment in the
  adjacent scan run clone helper.
- Tests were written first and initially failed because the catalog, policy
  normalization, assignment, and pre-probe APIs were absent. Exact verification
  commands and outcomes: `go test ./internal/discovery ./internal/policy -run
  'Test(DefaultScanCatalog|ScanCatalog|NormalizeScanPolicy|ScanAssignment)'
  -count=1` passed; `go test ./internal/... -count=1` passed; `go vet
  ./internal/...` passed; and `git diff --check` passed. No scanner, real
  network, device, or credential was contacted.
- Remaining limits: runtime scanner execution, due-run coordination, and
  controller/UI wiring remain for T008+; no network scan is claimed by this
  task.

## T007 — authenticated scan-result ingestion

- Requirements: FR-007–FR-009, FR-015, FR-018–FR-020.
- Date: 2026-09-12 (Europe/London); implementation commit: 3ae0b9c.
- Added strict scan-result page types and authenticated agent ingestion on both
  agent route prefixes. The service validates authenticated scanner identity,
  explicit assignment, current policy revision, lease owner/epoch and expiry,
  assignment lifetime, bounded result fields, safe outcomes/reason codes,
  entry-point identity, scope/exclusion membership, latency, page summaries,
  and final completion or partial transitions. Request-body SHA-256 hashes
  provide identical replay acknowledgement and conflicting replay rejection.
  Stored observations are credential-free and non-actionable; ingestion alone
  creates no candidate, access request, or enrollment job. Cross-page duplicate
  observations are rejected atomically.
- Tests were written first and initially failed because result-page types,
  ingestion, and the agent route were absent. Exact verification commands and
  outcomes: `go test ./internal/discovery ./internal/control ./internal/store
  -count=1` passed; `go test ./... -count=1` passed; `go vet ./...` passed; and
  `git diff --check` passed. Route coverage confirms authenticated agent
  identity and final run completion; negative coverage confirms wrong scanner,
  epoch, excluded/out-of-scope target, unsafe reason, expired assignment,
  replay conflict, and no-action rejection. No real device, network, or
  credential was contacted.
- Remaining limits: result ingestion does not yet materialize desired-state
  assignments, execute probes, reconcile candidates, or start enrollment;
  those remain in T008+.

## T008 — capability-gated agent desired state

- Requirements: FR-003, FR-006, FR-007, FR-014, FR-017, FR-022.
- Date: 2026-09-12 (Europe/London); implementation commit: 2d7af77.
- Added bounded scan capability declarations to agent heartbeats and persisted
  them with authenticated agent state. The agent runtime reports protocol 1 and
  TCP support. Desired state now returns at most one active leased/running scan
  assignment matching the authenticated, explicitly assigned agent and current
  policy revision. It returns no scan ranges or scopes to older, unassigned,
  revoked, expired, cancelled, stale, or incompatible agents. Assignment data
  contains only immutable ranges, exclusions, typed entry points, limits, run
  identity, lease epoch, and expiry; it contains no credentials, trust material,
  commands, or remote payloads.
- Exact verification commands and outcomes: `npx prettier --write
  api/schemas/heartbeat.json`; `go test ./... -count=1` passed, including the
  disposable PostgreSQL integration suite; `go vet ./...` passed; and `git diff
  --check` passed. Control tests verify compatible assignment delivery,
  capability persistence, no all-scope exposure, and no assignment after an
  older-agent heartbeat omits capabilities. No real device, network, or
  credential was contacted.
- Remaining limits: coordinator scheduling/leases, agent scan execution,
  candidate reconciliation, and owner UI/API wiring remain for T009+.

## T009 — PostgreSQL and in-memory scan fencing tests

- Requirements: FR-003, FR-004, FR-007–FR-009, FR-015, FR-018, FR-022;
  SC-003, SC-008.
- Date: 2026-09-12 (Europe/London); implementation/test commits: ab7c6c6,
  90f75a0.
- Added in-memory and disposable-PostgreSQL coverage for idempotent and
  conflicting pages, cross-page duplicate observations, wrong scanner,
  superseded policy, expired assignment, revoked scanner, transaction
  rollback, durable restart, and concurrent lease ownership. Migration checks
  run twice and verify all nine active-scanning tables. The initial SQL
  concurrency test exposed two successful leases across separate Store
  instances; scan mutations now lock `workspace_state` with PostgreSQL
  `FOR UPDATE` and persist atomically before either lease can proceed.
- Exact verification commands and outcomes: `go test ./internal/store -run
  'TestScanStore' -count=1` passed; `scripts/test-integration.sh` passed all
  integration tests against disposable PostgreSQL 17; and `git diff --check`
  passed. No real device, network, or credential was contacted.
- Remaining limits: scan-table-specific SQL query parity and runtime
  coordinator/executor behavior remain for T010+.
