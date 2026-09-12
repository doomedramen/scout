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
  not create a device or enrollment job; full per-vantage current projections,
  credential/trust re-evaluation, and owner-facing candidate APIs remain open
  in T023–T028.

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
- Remaining limits: complete candidate projection/freshness, release and
  reachability fencing, owner candidate endpoints/UI, and browser journey
  remain T023–T028.

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
