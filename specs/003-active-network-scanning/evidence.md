# Evidence Ledger: Active Network Scanning

## Specification delivery

2026-09-11: specification and implementation package prepared from user direction, Scout constitution, 001 discovery/enrollment contracts, and current code inspection. Existing active implementation changes in the working tree were not modified or treated as feature evidence.

Document validation establishes artifact consistency only. It does not prove active scanning, packet boundaries, enrollment, performance, or compatibility.

## Implementation evidence

Implementation and fixture evidence is appended below. No production network,
device, credential, or deployment is claimed by this package. Historical
entries retain the remaining limits that applied when each entry was written;
the latest validation status is at the end of this ledger.

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
- Remaining limits: coordinator scheduling/leases for assigned-agent runs and
  owner-facing candidate actions remain in T023–T031.

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

## T010 — bounded scanner contract tests

- Requirements: FR-004–FR-008, FR-018; SC-002, SC-006, SC-007.
- Date: 2026-09-12 (Europe/London); implementation/test commit: e283149.
- Tests were written before the scanner API existed. The focused command
  `go test ./internal/discovery -run 'TestProbe(ClassifiesTCP|HonorsAttempt|ClassifiesTimeout)' -count=1`
  first failed for the intended missing `AttemptBudget`, `Outcome`,
  `ProbeOptions`, and `ProbeWithOptions` symbols. The completed tests cover
  exact literal/prefix exclusion precedence, bounded target expansion,
  attempt-budget truncation with explicit skipped results, open/closed/
  filtered/unreachable/scanner-error classification, cancellation, timeout,
  and a loopback listener that proves no application bytes are written.
- Exact verification commands and outcomes: `gofmt -w
  internal/discovery/discovery.go internal/discovery/discovery_test.go`; `go
  test ./internal/discovery -count=1`; `go vet ./...`; and `git diff --check`
  passed. The only active listener was a process-local loopback test fixture;
  RFC 5737 targets used an injected dialer. No real device, network, or
  credential was contacted.
- Remaining limits: these are scanner-level tests. Full segmented lab packet
  capture and reference-scale performance evidence remain open in T034/T043.

## T013 — bounded cancellable TCP scanner

- Requirements: FR-004–FR-008, FR-017, FR-018; SC-001, SC-002, SC-006,
  SC-007.
- Date: 2026-09-12 (Europe/London); implementation commit: e283149.
- Added the cancellable `Scanner`/`TCPScanner` boundary and an injectable
  dial seam for deterministic tests. The production scanner canonicalizes
  literal addresses, deduplicates ports, caps concurrency/rate/targets/
  attempts, rejects attempt budgets larger than the planned target/entry-point
  multiplication, applies a per-attempt timeout, and closes the connection
  immediately after the TCP handshake. It emits only safe explicit outcomes:
  `open`, `closed`, `filtered`, `unreachable`, `skipped`, and `scanner_error`,
  with safe reason codes and no remote error text or payload.
- Exact verification commands and outcomes: `go test ./internal/discovery
  -count=1`; `go test ./... -count=1` (including the disposable PostgreSQL
  integration suite); `go vet ./...`; and `git diff --check` passed. The
  integration run completed in 54.959 seconds. No scan was enabled and no
  real device, network, or credential was contacted.
- Remaining limits: server coordinator execution and authenticated result
  paging are covered by T014/T020; assigned-agent scheduling and packaging
  remain open in T031, while segmented packet-boundary evidence remains open
  in T034/T043.

## T011 — coordinator failure-first tests

- Requirements: FR-002, FR-004, FR-016–FR-019; SC-001, SC-007, SC-008.
- Date: 2026-09-12 (Europe/London); implementation/test commits: d687bfa,
  8cea933.
- Tests were written before the coordinator existed. The focused command
  `go test ./internal/discovery -run '^TestCoordinator' -count=1` first
  failed for the intended missing coordinator, server-vantage, and jitter
  symbols. The completed tests cover immediate first scheduling, stable
  interval-plus-jitter scheduling, explicit server opt-in, one active run per
  scope/vantage, expired-lease re-acquisition after restart, deadline and
  scanner-failure partial states, policy-revision fencing, and a controlled
  loopback server scan.
- Exact verification commands and outcomes: `gofmt -w
  internal/discovery/coordinator.go internal/discovery/coordinator_test.go
  internal/store/scanning.go`; `go test ./internal/discovery -run
  '^TestCoordinator' -count=1`; `go test ./internal/discovery -count=1`;
  `go vet ./...`; and `git diff --check` passed. No real device, network,
  or credential was contacted.
- Remaining limits: assigned-agent scheduling/lease materialization, owner
  on-demand routes, and browser coverage remain open.

## T014 — durable server-vantage coordinator

- Requirements: FR-002, FR-004, FR-016–FR-019, FR-022; SC-001, SC-007,
  SC-008.
- Date: 2026-09-12 (Europe/London); implementation commits: d687bfa,
  8cea933.
- Added durable schedule materialization from enabled server policies, stable
  identity-derived jitter, active-run uniqueness, lease and epoch recovery,
  deadline-bounded server execution through the shared TCP scanner, paged
  authenticated result ingestion, final/partial summaries, and a fence
  watcher for cancellation, pause, scope disablement, policy revision, lease
  expiry, and recovery mode. Policy-fenced work is finalized partial and is
  never uploaded as current evidence; expired leases are re-leased under a
  higher epoch.
- Exact verification commands and outcomes: `go test ./internal/discovery
  ./internal/store ./internal/control ./internal/agent -count=1`; `go vet
  ./...`; and `git diff --check` passed. The controlled loopback listener
  produced one persisted non-actionable `open` observation and a completed
  run. No real device, network, or credential was contacted.
- Remaining limits: result-page ingestion remains authoritative evidence first;
  the accepted evidence now has a conservative candidate/access projection, but
  full per-vantage freshness, credential re-evaluation, and enrollment handoff
  remain in T023–T025.

## T015 — server coordinator lifecycle

- Requirements: FR-002, FR-016, FR-017, FR-022.
- Date: 2026-09-12 (Europe/London); implementation commit: 5af3fc6.
- `NewApp` now constructs the coordinator without starting work. The server
  binds its coordinator context to the existing signal context, starts it
  independently from HTTP serving and telemetry, and stops it on SIGINT or
  SIGTERM. Scheduling remains disabled until the owner-enabled scan policy
  explicitly opts into the server vantage.
- Exact verification commands and outcomes: `gofmt -w apps/server/main.go
  internal/control/http.go`; `go test ./internal/control ./apps/server
  -count=1`; `go vet ./...`; and `git diff --check` passed. No real device,
  network, or credential was contacted.
- Remaining limits: process-level lifecycle behavior will be included in the
  disposable Compose and final end-to-end gates; assigned-agent scheduling
  remains T031.

## T012 — agent scan failure-first tests

- Requirements: FR-003–FR-008, FR-014, FR-017, FR-018, FR-022; SC-007, SC-008.
- Date: 2026-09-12 (Europe/London); implementation/test commit: 4434073.
- Tests were written before the agent scan implementation. The initial
  focused command `gofmt -w internal/agent/scan_test.go && go test
  ./internal/agent -run 'Test(ValidateScanAssignment|BuildScanResultPages|StartScan|ScanResultSpool)' -count=1`
  failed for the intended missing `ScanAssignment`, validation, paging,
  runtime, and spool symbols. The completed suite covers expired and unsafe
  assignments, asynchronous single-flight execution, changed-revision
  cancellation, bounded final summaries, retry through the separate result
  spool, a 16 MiB scan-spool ceiling, and telemetry/heartbeat ordering ahead
  of scan execution.
- Exact verification commands and outcomes: `gofmt -w
  internal/agent/runtime.go internal/agent/scan.go
  internal/agent/scan_test.go`; `go test ./internal/agent -count=1`; `go
  test -race ./internal/agent -count=1`; and the focused package, vet, and
  whitespace checks recorded under T016 all passed. No real device, network,
  or credential was contacted.
- Remaining limits: these tests use local seams and an HTTP fixture; native
  service installation, assigned-agent run materialization, and live
  segmented-network evidence remain open.

## T016 — bounded agent scan execution

- Requirements: FR-003–FR-008, FR-014, FR-017, FR-018, FR-022; SC-001,
  SC-002, SC-007, SC-008.
- Date: 2026-09-12 (Europe/London); implementation commit: 4434073.
- Added credential-free desired-state handoff and default bounded TCP scan
  execution. The runtime validates immutable ranges, exclusions, typed TCP
  entry points, limits, expiry, and attempt multiplication before scanning;
  runs one scan asynchronously; cancels it when desired work is withdrawn or
  its run/lease/scope revision changes; and emits deterministic, bounded
  result pages with a final cumulative summary. Scan results use an isolated
  16 MiB/hour spool, while telemetry remains on the existing spool and is
  posted before desired-state scan work. The scan path sends no credentials,
  banners, or application payloads.
- Exact verification commands and outcomes: `go test ./internal/agent
  -count=1`; `go test -race ./internal/agent -count=1`; `go test
  ./internal/discovery ./internal/store ./internal/control ./internal/agent
  -count=1`; `go vet ./...`; and `git diff --check` all passed. No real
  device, network, or credential was contacted.
- Remaining limits: desired-state data is ready for agents but coordinator
  scheduling/lease materialization for assigned agents and service packaging
  remain T031; owner candidate actions and live server/agent network proof
  remain T023–T031 and T043.

## T021 — scan reconciliation failure-first tests

- Requirements: FR-008–FR-013, FR-015, FR-019; SC-003–SC-006.
- Date: 2026-09-12 (Europe/London); implementation/test commit: 7367dbd.
- Tests were written before the candidate projection existed. The focused
  ingestion test first failed because accepted open evidence produced no
  candidate; the direct reconciliation test then failed because the
  reconciliation method was absent. The completed tests cover supported SSH
  versus observation-only entry points, one candidate for multi-vantage
  evidence, one deduplicated candidate/method/endpoint request, retained
  candidates after contradictory evidence, current versus contradicted
  coverage, and actionable open-SSH evidence.
- Exact verification commands and outcomes: `go test ./internal/discovery
  ./internal/store -count=1`; `go test ./... -count=1` (including the
  disposable PostgreSQL integration suite); `go vet ./...`; and `git diff
  --check` passed. No real device, network, or credential was contacted.
- Remaining limits: the projection is intentionally credential-free and does
  not create a device or enrollment job; broader browser coverage remains open
  in T028.

## T017 — configurable agent scan capabilities and service wiring

- Requirements: FR-003, FR-007, FR-014, FR-022.
- Date: 2026-09-12 (Europe/London); implementation/test commit: 523a3bd.
- Agent runtime configuration now carries validated scan protocol and
  transport capabilities. The CLI exposes `--scan-protocol-version` and
  `--scan-transport`, defaults to protocol 1/TCP, and reports the configured
  values in authenticated heartbeats. The service template and generated
  installer unit load `/etc/scout/agent.env` while retaining the rendered
  server URL fallback and the protected data directory. Existing control
  behavior remains backward-compatible: heartbeats without capabilities are
  accepted as older agents but receive no scan assignment.
- Exact verification commands and outcomes: `go test ./internal/agent
  ./apps/agent -count=1`; `scripts/test-first-agent.sh`; `bash -n
  scripts/install-agent.sh scripts/test-first-agent.sh`; `go vet ./...`;
  and `git diff --check` passed. Tests cover default/custom capability
  configuration and invalid capability rejection. No real device, network,
  or credential was contacted.
- Remaining limits: native systemd installation and assigned-agent live scan
  acceptance remain open for T020/T031/T043; candidate APIs, credential
  re-evaluation, and enrollment remain T023–T031.

## T018 — protected scan-policy and on-demand run API

- Requirements: FR-001, FR-002, FR-016, FR-018, FR-020.
- Date: 2026-09-12 (Europe/London); implementation/test commit: b07f8cd.
- Added authenticated owner routes for scope scan-policy create/read/update,
  on-demand run creation, run listing/detail/cancellation, and scan status.
  Policy normalization enforces bounded targets, typed entry points, limits,
  explicit server opt-in, assigned-agent capability checks, revision fencing,
  idempotent retries, active-run conflicts, safe redacted responses, audit
  events, and development-mode MFA friction reduction through the existing
  protected-route policy.
- Exact verification commands and outcomes: the focused route tests first
  failed on the missing nested policy contract and scan-run route, then
  `go test ./internal/control ./internal/store -count=1`, `go test ./...`,
  `go vet ./...`, and `git diff --check` passed. Positive coverage verifies
  policy creation, status, queued on-demand execution, idempotent replay,
  safe output, and policy revision supersession. Negative coverage verifies
  active-run conflict and unauthenticated rejection. No real device, network,
  or credential was contacted.
- Remaining limits: server/agent execution proof and candidate-facing routes
  remain open in T020 and T023–T031.

## T019 — owner scan controls

- Requirements: FR-001–FR-003, FR-005, FR-016, FR-018, FR-021.
- Date: 2026-09-12 (Europe/London); implementation/test commit: b07f8cd.
- Added scope controls for schedule, server scan opt-in, enrolled-agent
  selection, typed SSH/TCP entry points, target and attempt limits, a
  review-before-enable flow, and on-demand server scans. The web API client
  exposes scope detail, scan policy, run, cancellation, and status contracts;
  scope creation persists a disabled policy by default and the UI renders
  assigned device addresses to reduce ambiguity.
- Exact verification commands and outcomes: `npm run check` and `npm run
  format:check` passed, along with the backend suite and vet checks recorded
  under T018. No real device, network, or credential was contacted.
- Remaining limits: candidate discovery presentation and credential actions
  remain open in T023–T031.

## T022 — scan candidate access re-evaluation tests

- Requirements: FR-010–FR-015; SC-003–SC-005, SC-008.
- Date: 2026-09-12 (Europe/London); implementation/test commit: 467f79c.
- Tests were written first and initially failed because candidate re-evaluation,
  safe access-check results, and guarded candidate enrollment were absent. The
  completed integration coverage keeps a discovered SSH host as a candidate
  until access is supplied, classifies invalid credentials, insufficient
  privilege, host-key mismatch, and server-connectivity failure without
  creating a device or job, then verifies valid credential plus explicit trust
  produces exactly one device and one enrollment job. Repeating the same
  re-evaluation remains idempotent. HTTP credential and trust mutations drive
  the same bounded re-evaluation and responses/jobs contain no secret.
- Exact verification commands and outcomes: `go test ./tests/integration
  -run 'Test(CredentialAndTrustMutations|ScanCandidateAccess)' -count=1` and
  `go test ./... -count=1` passed; `go vet ./...` and `git diff --check` also
  passed. The first full-suite run caught and removed the old unsafe expectation
  that a sighting should create an address-only device/job. All fixtures use
  process-local state and injected verifier outcomes; no real device, network,
  or credential was contacted.
- Remaining limits: broader browser coverage, live worker installation, and
  segmented lab evidence remain open in T028–T043.

## T020 — controlled server and agent scan acceptance

- Requirements: FR-001–FR-009, FR-014–FR-018; SC-001–SC-003, SC-006–SC-008.
- Date: 2026-09-12 (Europe/London); implementation/test commit: fa13da5.
- Added a controlled integration journey for both vantages. The server test
  authenticates an owner through setup, login, CSRF-protected scope/policy
  creation, and on-demand run creation before executing the coordinator. The
  agent test enrolls a disposable Linux identity, persists heartbeat scan
  capabilities, receives an authenticated desired-state assignment, performs
  a real TCP handshake, uploads results, and projects a needs-credentials SSH
  candidate while ordinary telemetry continues. Both tests use a process-local
  loopback listener and a dial recorder to assert that the excluded RFC 5737
  address receives zero attempts and that no application bytes are sent.
  The Playwright test exercises the same owner-facing scope review, enablement,
  server-scan opt-in, on-demand action, and eventual needs-credentials state
  against the disposable local server port.
- Exact verification commands and outcomes: the first integration run exposed
  an unsupported secondary loopback bind; the fixture was changed to the
  injected dial seam. It then exposed an attempt-budget clamp bug in server
  execution and a browser crash caused by empty `agentIds` serializing as
  `null`; both were fixed. `go test ./tests/integration -run
  TestServerAndAgentScansUseControlledListenersAndHonorExclusions -count=1`,
  `go vet ./...`, `npm run check`, `npm run build`, `npm run format:check`, and
  `npm run test:e2e:browser` passed. The browser suite completed with 8 tests
  passed. No real device, network, or credential was contacted.
- Remaining limits: the fixture proves local server/agent behavior, not the
  segmented Linux lab, 256-address performance target, or full credential and
  enrollment journey; those remain T022–T043.

## T023–T027 — actionable scan candidate flow

- Requirements: FR-008–FR-015, FR-019–FR-021; SC-003–SC-005, SC-009.
- Date: 2026-09-12 (Europe/London); implementation/test commit: e3419e8.
- T023 retains one conservative candidate per scoped address, preserves
  scanner provenance, records per-vantage current observations and
  contradictions, and keeps one access-request key per candidate/method/
  endpoint. Candidate detail returns current evidence only, so an older open
  observation cannot make a later closed result look actionable.
- T024–T025 reuse the existing bounded access re-evaluation boundary after
  credential/trust changes. It validates current scope revision, exclusion,
  SSH entry point, target-bound credential version, explicit host trust,
  destination, and server reachability before queuing work; repeated
  re-evaluation returns the same active enrollment job.
- T026 adds owner-authenticated candidate list, detail, current entry-point,
  and re-evaluate routes with bounded filters/cursors, safe action descriptors,
  scanner provenance, and no secret/banner fields. The candidate response now
  includes the current scope revision needed for optimistic mutation fencing.
- T027 adds the Network found-device list and filters, evidence/detail view,
  specific prerequisite states, and a prefilled Access route for the observed
  SSH endpoint. Credential and trust forms remain write-only and automatically
  re-evaluate the selected candidate.
- Tests were written before the missing candidate route/detail behavior and
  initially failed because raw candidate responses omitted the actionable
  summary fields. Exact verification commands and outcomes: `go test
  ./internal/control ./internal/store ./tests/integration -run
  'Test(CandidateRoutesExposeActionableSafeDetail|CurrentEntryPointFilterKeepsNewestVantageEvidenceAndContradiction|CredentialAndTrustMutationsReevaluateScanCandidate|ScanCandidateAccessReevaluationClassifiesFailuresAndQueuesOnce)'
  -count=1`; `go test ./tests/contracts -count=1`; `go test ./... -count=1`;
  `go vet ./...`; `npm run lint`; `npm run check`; `npm run format:check`; and
  `git diff --check` all passed. The controlled Playwright journey
  `npm run test:e2e:browser -- --grep 'owner can configure a bounded scan'`
  passed and covers scope setup, local SSH evidence, keyboard-addressable
  candidate/detail/access controls, credential storage, explicit trust, and
  one queued enrollment job. Only process-local loopback listeners, RFC 5737
  addresses, and redacted fixture credentials were used; no real device,
  production network, or production credential was contacted.
- Remaining limits: T028 still needs the broader duplicate/unsupported/error
  and viewport/accessibility matrix; the worker install on a real Linux host
  and segmented packet-boundary evidence remain intentionally unperformed.

## Served installer origin fix

- Requirements: 001 agent bootstrap usability and deployment correctness.
- Date: 2026-09-12 (Europe/London); implementation/test commit: 9ed1891.
- The compact `SCOUT_OTI=... bash -c "$(curl -fsSL .../install.sh)"`
  invocation is valid when Scout serves the rendered installer. The server
  now honors a validated `Forwarded: proto=` or `X-Forwarded-Proto` value
  before falling back to the direct request transport, so TLS-terminated
  reverse proxies render an HTTPS server URL. A raw/old template now fails
  with an explicit stale-installer message rather than the misleading
  `--server ... is required` error.
- Exact verification commands and outcomes: `go test ./internal/control
  -run TestAgentInstallerEmbedsRequestOrigin -count=1`; `bash -n
  scripts/install-agent.sh`; and `git diff --check` passed. The regression
  test verifies both direct and forwarded HTTPS origins. No installer was
  run against a real device.
- Remaining limit: the already-running public deployment must be rebuilt and
  restarted from the pushed image before its endpoint can serve this fix; no
  production deployment or device installation was performed from this task.

## Server-local discovered enrollment checkpoint

- Requirements: FR-010–FR-015, FR-017, FR-020; SC-003–SC-005, SC-008.
- Date: 2026-09-12 (Europe/London); implementation/test commit: f7aced4.
- Discovered Linux candidates can now be processed by an in-process server
  worker after owner-approved SSH credential and exact host-fingerprint trust
  are present. The worker claims one target-bound job, revalidates scope,
  exclusion, credential revision, destination, and trust immediately before
  use, redeems the secret only for that target, stages the native artifact,
  service unit, and short-lived invitation over SSH, and waits for the new
  agent heartbeat before marking the candidate enrolled. The fixed remote
  command checks root or non-interactive sudo, creates the restricted agent
  account, installs the binary and service, enables it, and confirms the
  service state; connection parameters and secrets are not interpolated into
  that command. The server image contains the AMD64/ARM64 native artifacts;
  the CI workflow publishes only the server image.
- Exact verification commands and outcomes: `go test ./internal/enrollment
  ./internal/identity ./internal/control ./internal/store ./apps/server
  ./apps/enroller -count=1`; `go test ./... -count=1`; `go vet ./...`; `npm
  run lint`; `npm run check`; `npm run format:check`; `npm run build`; `go
  test ./tests/contracts -count=1`; and `git diff --check` passed. The local
  worker tests use an in-memory store, an injected SSH transport, RFC 5737
  target data, and redacted fixture material; the positive case verifies
  invitation/service uploads and confirmation, while the negative case
  projects host-key mismatch without leaking the credential. No real device,
  network, production credential, or deployment was used.
- Remaining limits: this proves the server-local orchestration boundary and
  fixed installer behavior, not a real Linux/systemd installation, segmented
  packet capture, 256-address performance, or the full keyboard/viewport
  browser matrix. The live deployment must rebuild from the pushed commits
  before its served installer contains the latest fixes.

## System-detail access flow and isolated live-agent verification

- Requirements: FR-010–FR-015, FR-019–FR-021; SC-003–SC-005, SC-009.
- Date: 2026-09-12 (Europe/London); implementation/test commits: 2db33fa,
  3107941, f952a85.
- The Systems page now keeps a discovered, unenrolled host visible as
  `Needs access`. Opening that system renders the credential form in place,
  headed `SSH found on this system`, with the discovered SSH target
  prefilled. Supplying an owner-approved credential and host trust continues
  through the existing bounded re-evaluation and server-local enrollment
  worker. The detail view polls device metadata and candidates so the same
  page transitions to live agent state after the first heartbeat.
- The local isolated verification used Scout Compose project
  `scout-e2e-local` on `127.0.0.1:18081` plus a privileged Debian systemd
  container on the private Compose network. The target's agent service was
  active; PostgreSQL projected the same stable device record as `online` with
  agent version `0.1.0` and a recent heartbeat; the telemetry spool was empty;
  and the server remained running with zero restarts. The browser showed the
  target on the Systems page as `Online`, then showed live CPU/memory values,
  agent identity, version, availability, and last heartbeat on its detail
  page without a manual reload.
- The run exposed and fixed two local-only correctness issues: direct legacy
  Docker builds on the arm64 host now build a native server binary, and all
  persisted in-memory composite keys avoid NUL bytes rejected by PostgreSQL
  JSONB. Regression coverage was added for the key builders.
- Exact verification commands and outcomes: `go test ./... -count=1`, `go vet
  ./...`, `npm run check`, `npm run lint`, `npm run build`, `npm run
  format:check`, `go test ./tests/contracts -count=1`, `git diff --check`, and
  `npx playwright test tests/e2e/playwright/discovery.spec.ts --grep
  "owner can configure a bounded scan"` all passed. The final browser
  assertion covered the system-detail heading, automatic-install copy, and
  exact discovered target. Only the disposable local container and fixture
  credentials were used; no real device, production network, or production
  credential was contacted.
- Remaining limits: the isolated run does not replace T029–T044's broader
  multi-vantage, segmented-packet, workload, recovery, and viewport matrices;
  the public deployment must rebuild from the pushed commits before it serves
  these UI and installer changes.

## Password-authenticated enrollment and multi-key SSH follow-up

- Requirements: FR-010–FR-015, FR-017, FR-019–FR-021; SC-003–SC-005,
  SC-008, SC-009.
- Date: 2026-09-12 (Europe/London); implementation/test commit: f3607d2.
- Authorized topology: disposable Docker network `scout-v1-lab`, a local
  Scout server published only on `127.0.0.1:18083`, and disposable Debian
  Bookworm systemd targets at isolated container addresses `172.20.0.4` and
  `172.20.0.5`. Both targets exposed SSH on port 22 with a disposable
  username/password account and non-interactive sudo. No production device,
  network, credential, or deployment was contacted.
- Positive journey: the current UI created the second site/scope, enabled the
  reviewed server scan, and queued an on-demand run. The scan retained one
  open SSH entry point and one `needs_credentials` candidate. Opening the
  system from Systems rendered `SSH found on this system`, defaulted to
  `Username + password`, showed the required SSH username/password fields,
  and prefilled the exact `172.20.0.5:22` target. Submitting the credential
  and an owner-trusted Ed25519 host fingerprint caused a re-evaluated job to
  install the native agent over SSH. The candidate became `enrolled`; the
  Systems row became `Online`; and the detail view showed agent identity,
  version `0.1.0`, current CPU/memory values, and a recent heartbeat.
- Negative and compatibility evidence: a pre-fix local run with a trusted
  Ed25519 fingerprint recorded `host_key_mismatch` because the SSH client
  selected the target's ECDSA key first, leaving the candidate at
  `needs_host_trust`. A red regression test reproduced this with a server
  publishing both keys. The fixed worker now retries each supported secure
  host-key algorithm until the explicit fingerprint matches; the green
  regression test proves the Ed25519-trusted path succeeds without accepting
  a different key. Wrong-password coverage remains green, and no secret is
  returned by credential listing or included in job/result output.
- Exact verification commands and outcomes: `go test ./... -count=1` passed;
  `go vet ./...` passed; `npm run check` passed; `npm run lint` passed;
  `npm run format:check` passed; `npm run build` passed; `go test
  ./tests/contracts -count=1` passed; `npx playwright test` passed with 10
  tests; and `git diff --check` passed. The local image was rebuilt with
  `docker build -f packaging/containers/server.Dockerfile -t scout:local-v1
  .` and the server was restarted from the resulting image before the
  successful Ed25519-trust retry.
- Remaining limits: this is a two-target local Docker acceptance run, not the
  packet-captured segmented multi-vantage lab, 256-address/100-device load,
  recovery, older-agent, or full viewport matrix. T029–T044 therefore remain
  open in `tasks.md`.

## Multi-vantage authority and pause fencing slice

- Requirements: FR-002–FR-004, FR-007, FR-009, FR-014–FR-018, FR-022;
  implementation/test commit: 87b258d.
- Explicit agent assignments now require an active identity, current TCP scan
  capability, compatible device lifecycle, and site match at policy update,
  run creation, desired-state delivery, and result ingestion boundaries. A
  moved, revoked, or decommissioned agent cannot receive or complete stale
  scan work; queued and leased work is canceled immediately, while running or
  uploading work is cancellation-fenced and rejects new result pages.
- Discovery pause now cancels queued/leased scan runs, exposes active scan
  holders in `ExecutionHolders`, blocks new scheduling and desired-state
  delivery, and supports authenticated agent `POST /agent/v1/pause-ack` only
  after the agent scan executor has stopped. Pause acknowledgement cannot clear
  a holder while its active scan lease remains.
- Tests were written before the new behavior and initially failed for leased
  cancellation, early pause acknowledgement, absent agent pause route, and
  missing runtime acknowledgement. Exact passing checks after implementation:
  `go test ./internal/agent ./internal/control ./internal/discovery
  ./internal/policy ./internal/store ./tests/integration -count=1`; and
  `git diff --check`. The integration suite passed in 42.6 seconds. No live
  device, production network, or production credential was contacted.
- Remaining limits: T029/T030 still need the complete multi-vantage and
  restart/unreachable matrix, and T031/T032 remain open pending the broader
  authority/recovery audit.

## Multi-vantage authority and pause fencing completion

- Requirements: FR-002–FR-004, FR-007, FR-009, FR-014–FR-018, FR-022;
  implementation/test commits: 87b258d, c4f1126.
- T029 coverage passes for explicit agent assignment, site mismatch after
  device movement, missing or older scan capability, authenticated result
  submission without run authority, revoked and decommissioned scanners, and
  desired-state responses with no all-scope leakage.
- T030 coverage passes for queued, leased, active/dialing, and uploading
  cancellation fences, late-result rejection, pause scheduling suppression,
  server restart re-lease, unreachable-scanner recovery, and authenticated
  pause acknowledgement. Scope disablement now has a regression test proving
  active work becomes terminally cancelled and scan policy is disabled.
- Exact verification: `go test ./internal/control ./internal/discovery
  ./internal/policy ./internal/store ./tests/integration -count=1` passed;
  integration package completed in 40.0 seconds. `git diff --check` and
  formatting checks remain required at next commit gate. No live device,
  production network, or production credential was contacted.
- T029–T032 are complete. T033–T044 remain open.

## Separate multi-vantage evidence and conservative identity

- Requirements: FR-007–FR-009, FR-015, FR-019; implementation/test commits:
  87b258d, 6daab4c.
- Reconciliation test now combines same-address open SSH evidence from the
  server and assigned agent into one candidate while retaining two provenance
  records, two evidence IDs, one deduplicated access request, and no inferred
  device identity.
- Store tests retain entry-point observations/current projections by scanner
  identity. Topology tests cap address-only relationship confidence and skip
  decommissioned inventory; no address-only path creates or merges devices.
- Exact verification: `go test ./internal/discovery -run
  'TestReconcileScanObservationsMergesVantagesAndDeduplicatesAccess|TestReconcileScanObservationKeepsCandidateWhenLaterProbeContradictsIt'
  -count=1` passed; `go test ./internal/topology -count=1` passed; `git diff
  --check` passed. No live device, production network, or production credential
  was contacted.
- T033 is complete. T034–T044 remain open.

## Agent handoff and segmented lab harness

- Requirements: FR-002–FR-009, FR-014, FR-017, FR-018; SC-001, SC-002,
  SC-007, SC-008, SC-010; implementation/test commit: 40ecba3.
- A queued assigned-agent run is now claimed and started by the authenticated
  agent desired-state request. The coordinator also materializes and leases
  scheduled agent runs per explicit scope assignment; server runs remain owned
  by the server coordinator. Red-first coverage proves the previous queued-run
  failure and the corrected desired-state, on-demand, and scheduled paths.
- `scripts/test-active-discovery.sh` now runs fixture checks by default and
  fails closed for live mode unless Linux, Docker, bridge-scoped tcpdump,
  passwordless capture permission, and explicit owner confirmation exist. Live
  mode builds local server/agent binaries, uses three Docker `--internal`
  bridges, creates server-only, agent-only, closed, excluded, and adjacent
  targets, runs authenticated scans, and asserts packet-level SYN counts.
- Exact verification: `bash -n scripts/test-active-discovery.sh` passed;
  `bash scripts/test-active-discovery.sh` passed in fixture mode; targeted
  desired-state and agent integration tests passed; `go test ./internal/control
  ./internal/discovery ./internal/agent ./tests/integration -count=1` passed;
  `go vet ./internal/control ./internal/discovery ./internal/agent` passed;
  `git diff --check` passed. Live Linux lab execution remains T043. No live
  device, production network, or production credential was contacted.
- T034 is complete. T035–T044 remain open.

## Assigned-vantage status in owner views

- Requirements: FR-002, FR-003, FR-016, FR-017, FR-021; SC-009, SC-010;
  implementation/test commit: e25cd95.
- The Scopes view now polls the protected scan-status projection and shows
  current/partial/stale/unknown coverage, last and next schedule, every
  configured server or assigned-agent vantage, capability declarations,
  heartbeat freshness, and safe pause/cancellation messaging. A scope with
  no enabled vantage is visibly paused even when the broader scope remains
  enabled. Scan-run responses now expose the redacted
  `cancellationRequested` flag so an active holder can be shown as awaiting
  acknowledgement without exposing lease authority.
- The Network view polls status once per distinct candidate scope and adds
  coverage, assigned-vantage state, active-run/cancellation, and completion
  freshness beside each found device. Candidate evidence remains visible if a
  status request fails; the row reports loading rather than hiding the device.
- Failure-first browser coverage was added before implementation. The red
  run failed because `Scan coverage` was absent; the green run passed after
  the status projection was wired. The browser also toggles server scanning
  off and verifies the explicit paused/fenced message, then resumes scanning.
- Exact verification: `npm run check` passed; `npm run build` passed;
  `npx playwright test tests/e2e/playwright/discovery.spec.ts
  tests/e2e/playwright/access.spec.ts` passed with 4 tests; and
  `git diff --check` passed. No live device, production network, production
  credential, or deployment was contacted.
- T035 is complete. T036–T044 remain open.

## Scan status, bounded pagination, and retention visibility

- Requirements: FR-007, FR-016, FR-018–FR-022; SC-006–SC-009; implementation/test commit: 1d5b413.
- Run status now projects active, latest terminal, and next scheduled work independently for each assigned server or agent vantage. The owner-facing status contract and TypeScript model expose per-vantage completion, schedule, and active-run progress without collapsing an agent-only or multi-vantage scope into a misleading server aggregate. Candidate API limits now reject non-positive, malformed, or over-500 page sizes with the existing safe invalid-request envelope instead of silently expanding or clamping unsafe scan queries.
- The Network view reports scan coverage, assigned-vantage state, active progress, and completion freshness next to each found device. The Scopes view reports coverage, outcome summary, partial/error/stale notes, retention lag, queue pressure, capabilities, heartbeat freshness, and per-vantage last/next schedule. Deterministic browser coverage exercises active progress, partial evidence, stale evidence, empty results, unavailable results, candidate filtering/keyboard navigation, and the 360/768/1440 layout checks.
- Existing bounded cleanup implementation is retained: 30-day observations/receipts and 90-day terminal run summaries are deleted in restart-safe batches; open or re-evaluating access requests protect their evidence and owning runs; candidates and exclusions are never deleted; status exposes cleanup lag/blocking and queue backpressure. Existing memory and PostgreSQL tests cover cleanup continuation, unresolved-request protection, duplicate/conflicting pages, counter bounds, and restart persistence. Audit and API error tests confirm secrets/backend errors are redacted.
- Exact verification commands and outcomes: `go test ./... -count=1` passed; `go vet ./...` passed; `npm run lint` passed; `npm run check` passed; `npm run format:check` passed; `git diff --check` passed; `npm run test:e2e:browser` rebuilt the application and passed all 16 Playwright tests, including the new scan-status suite. The new browser suite also passed independently with 2 tests. PostgreSQL-backed integration is run separately by `scripts/test-integration.sh` because the full Go gate does not assume a database URL. No real device, production network, production credential, or deployment was contacted.
- T036–T040 are complete. T041–T044 remain open for recovery preservation, final operational documentation, live segmented packet-boundary/workload evidence, and the final release gate.

## Recovery preservation and active-scanning operations documentation

- Requirements: FR-001–FR-006, FR-014–FR-016, FR-018–FR-022; SC-003, SC-006–SC-008; implementation/test commit: 00d7fb4.
- Logical backup/restore now has regression coverage for scan policy, explicit agent assignment, terminal run summaries, active run fencing, leases, result receipts, entry-point observations/current projection, candidate extensions, candidate identity, deduplicated access-request links, and recovery pause state. PostgreSQL-backed coverage creates a real non-final scan page before backup, restores it through the store boundary, verifies the candidate/access/evidence link, proves the active run is cancelled and no longer leaseable, and rejects stale result replay.
- `scripts/backup.sh` and `scripts/restore.sh` continue to use the full PostgreSQL custom dump while now requiring and verifying all nine active-scanning tables in sidecar metadata in addition to the Spec 2 monitoring tables. The restore path still requires the matching wrapping key, a separate destination, explicit confirmation, and leaves authority in recovery mode; a live clean-database restore remains intentionally opt-in.
- README and operator/security/support documentation now describe the exact scope/vantage configuration, bounded TCP-only traffic model, permissions, retention windows, pause and late-result fences, recovery behavior, automatic server-local enrollment boundary, Docker quickstart limits, and explicit unverified live/non-goal areas.
- Exact verification commands and outcomes: `go test ./internal/store ./tests/integration -run 'TestBackupRestorePreservesScanStateAndFencesActiveAuthority|TestSQLScanBackupPreservesEvidenceAndFencesActiveAuthority|TestSQLScanHistoryCleanupIsRestartSafeAndProtectsOpenRequests|TestBackupRestoreAndRecoveryPauseAuthority' -count=1` passed; `bash -n scripts/backup.sh scripts/restore.sh scripts/test-restore.sh` passed; `scripts/test-restore.sh` passed in fixture mode. No live restore, production database, device, network, or credential was used.
- T041–T042 are complete. T043–T044 remain open for owner-authorized segmented packet capture, live/reference workload and compatibility evidence, and the final repository release gate.

## Final fixture gates and live-lab boundary

- Requirements: FR-001–FR-022; SC-001–SC-010; validation date: 2026-09-13
  (Europe/London); implementation/test commit: 5d490f1.
- Final repository checks passed: `go test ./... -count=1` (including the
  recovery replay regression; integration package 43.317 seconds), `go vet
  ./...`, `npm run check`, `npm run build`, `npm run format:check`, `git diff
  --check`, and `go test ./tests/contracts -count=1`.
- Repository fixture scripts passed: `scripts/test-active-discovery.sh`,
  `scripts/test-load.sh`, `scripts/test-integration.sh` using a disposable
  PostgreSQL 17 container (integration package 53.033 seconds),
  `scripts/test-collectors.sh`, `scripts/test-decommission.sh`,
  `scripts/test-enrollment.sh`, `scripts/test-first-agent.sh`,
  `scripts/test-hardware-monitoring.sh`, `scripts/test-monitoring-load.sh`,
  `scripts/test-proxmox-helper.sh`, `scripts/test-restore.sh`,
  `scripts/test-storage-health.sh`, `scripts/test-systemd.sh`, and
  `scripts/test-updates.sh`. Live lab modes stayed disabled and therefore
  contacted no real device, network, provider, storage utility, system bus, or
  production credential.
- Synthetic reference workload passed: 100 devices, 40 numeric series, 50
  service states, 24 hours, 96,000 samples, and 120,000 observations. The
  latest run reported 41.687 seconds ingest time, 11.66 ms query p95, zero
  evaluation lag, and zero rollup queue depth. This is explicitly a synthetic
  regression result, not a live capacity claim.
- Browser acceptance passed: `npm run test:e2e:browser` ran 16 Playwright
  tests, including keyboard navigation and 360/768/1440 CSS-pixel layouts,
  scan progress/partial/stale/empty/unavailable states, bounded filters, and
  SSH username/password enrollment UI. No browser test used a real device or
  credential.
- The live segmented packet-capture lab has since passed in the owner-authorized
  Colima Linux VM; see the dated section below. The live older-agent, native
  systemd, hardware, storage, and provider checks remain unclaimed. The
  controlled 256-address scan is also still open; the lab run below uses a
  three-target topology.
- T043 remains open for the live 256-address workload and unvalidated live
  compatibility checks. T044 is complete for the repository gates and fixture
  integration scripts listed above.

## Live segmented Docker lab in the Linux VM

- Requirements: FR-001–FR-022; SC-001, SC-002, SC-003, SC-006, SC-007,
  SC-008, and SC-010; validation date: 2026-09-13 (Europe/London);
  implementation/test commit: `04d1945`.
- The lab ran inside the existing Colima Linux VM rather than against the
  macOS host network: Ubuntu 24.04.4 arm64, Docker 29.5.2, two isolated
  internal Docker bridges, and bridge-scoped `tcpdump`. QEMU was available on
  the host, but a second guest was unnecessary for this Linux-only boundary
  check.
- Command used, with no production endpoint or credential:
  `colima ssh -- sh -lc 'cd /Users/martin/Developer/scout && SCOUT_ACTIVE_DISCOVERY_LAB=1 SCOUT_ACTIVE_DISCOVERY_LAB_CONFIRM=YES SCOUT_ACTIVE_DISCOVERY_LAB_KEEP=1 bash scripts/test-active-discovery.sh'`.
- The segmented run passed. The server-only scope completed with candidate
  outcome `needs_credentials`; the agent-only scope completed with candidate
  outcome `needs_credentials` and one enrolled disposable agent. Packet
  counts were one SYN to the authorized server target, zero to the excluded
  server target, one SYN to the authorized agent target, and zero to the
  adjacent unauthorized subnet. The server bridge was `172.30.154.0/24`, the
  agent bridge was `172.30.155.0/24`, and the adjacent unauthorized bridge was
  `172.30.156.0/24`.
- Retained packet-capture hashes were: server
  `0160324e2ac399a7fddf3842ba88c6e5db9af5f007f91066bc317b07fcf6f9d2`, agent
  `b2ad15d6d7946f012ababee12fe124e31817dbe00c9de96506ecf17a1583aa25`, and
  adjacent `704e5e5b3234433c01fd1b20a306e77e985038120492dc53965c3edd38a4ea`.
- The lab script was hardened in `04d1945` for Linux VM execution: it uses
  the server container's private address, attaches the agent to both
  intentionally separated networks, and interrupts `tcpdump` cleanly so
  short captures flush before assertions. No real LAN address, device,
  production credential, or external deployment was used.
- Cleanup was issued against the four exact disposable artifact directories;
  the Colima guest then became unavailable with a VM I/O error before a final
  filesystem verification could complete, so the VM was force-stopped and was
  left stopped. This is an environment cleanup limitation, not a Scout test
  result. The retained artifacts were never copied into the repository.
