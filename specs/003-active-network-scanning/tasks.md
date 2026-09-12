# Tasks: Active Network Scanning

**Input**: Design documents from `/specs/003-active-network-scanning/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`, `contracts/`, and completed required 001 identity/scope/access/enrollment foundations.

**Tests**: Required. Write each listed test before its implementation and confirm it fails for the intended reason.

**Organization**: Tasks are dependency-ordered and grouped by user story. Task completion requires evidence, not only a checked box.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: May run concurrently only when separately authorized and when listed dependencies are complete.
- **[Story]**: User story from `spec.md`.

## Phase 1: Setup and prerequisite gate

**Purpose**: Confirm inherited security foundations and lock additive public contracts before storage or runtime changes.

- [X] T001 Run and record the 001 prerequisite gate for identity, scope/exclusion policy, credential/trust broker, enrollment fencing, desired state, audit, pause, backup, and agent telemetry in specs/003-active-network-scanning/evidence.md; leave unmet prerequisites open rather than duplicating them (FR-003, FR-004, FR-013, FR-014, FR-022).
- [X] T002 Add strict scan-policy, entry-point, run, candidate, agent-assignment, and paged-result schemas and routes in api/openapi.yaml and api/schemas/ per contracts/control-api.md and contracts/agent-protocol.md (FR-001–FR-008, FR-016, FR-018, FR-021).
- [X] T003 Add positive and negative JSON contract fixtures plus schema/reference validation in tests/contracts/fixtures/scan-*.json and tests/contracts/discovery_contract_test.go, covering unknown fields, all bounds, excluded targets, unsupported transport, wrong scanner, stale revision, and conflicting replay (FR-001, FR-004–FR-008, FR-15, FR-018).

**Checkpoint**: Contracts are executable; existing 001 security prerequisites are evidenced.

---

## Phase 2: Foundational scan persistence and authority

**Purpose**: Build shared durable state, replay, revision, and policy boundaries required by every story.

**Critical**: No user-story runtime work begins until this phase passes PostgreSQL and in-memory tests.

- [X] T004 Add versioned migrations and Go entities for scan policy, typed entry points, vantage assignments, scan runs and leases, result receipts, entry-point observations/current projections, candidate extensions, and access-request dedupe in internal/store/migrations.go and internal/store/models.go (FR-001, FR-002, FR-003, FR-007, FR-009, FR-010, FR-015, FR-019, FR-022).
- [X] T005 Implement transaction-safe in-memory and PostgreSQL scan policy/run lease/result receipt/observation operations, active-run uniqueness, fencing epochs, pagination, and terminal transitions in internal/store/discovery.go and internal/store/store.go (FR-007–FR-009, FR-015, FR-016, FR-018, FR-019, FR-022).
- [ ] T006 [P] Add typed SSH entry-point catalog, policy normalization, target/attempt multiplication limits, explicit scanner assignment validation, IPv6 finite-budget rules, and pre-probe decisions in internal/discovery/catalog.go and internal/policy/policy.go (FR-001–FR-006, FR-018).
- [ ] T007 Implement authenticated, idempotent paged scan-result ingestion with hash conflict detection, run/scanner/revision/epoch/expiry validation, safe outcome catalogs, atomic counters, and no-action rejection in internal/discovery/reconcile.go and internal/control/agents.go (FR-007–FR-009, FR-015, FR-018–FR-020).
- [ ] T008 Extend agent heartbeat capabilities and desired-state selection so only assigned compatible agents receive one credential-free immutable scan assignment in internal/control/agents.go and internal/store/telemetry.go (FR-003, FR-006, FR-007, FR-014, FR-017, FR-022).
- [ ] T009 Add migration, concurrent lease, duplicate/conflicting page, wrong-agent, superseded-revision, expiry, revocation, and rollback tests for both stores in internal/store/discovery_test.go and tests/integration/scan_storage_test.go (FR-003, FR-004, FR-007–FR-009, FR-015, FR-018, FR-022; SC-003, SC-008).

**Checkpoint**: Scan work can be represented, assigned, fenced, ingested, and recovered without sending a network probe.

---

## Phase 3: User Story 1 - Discover reachable management entry points (Priority: P1) MVP

**Goal**: Run bounded scans from server and one explicitly assigned agent and retain truthful entry-point evidence.

**Independent Test**: Configure an isolated scope with open, closed, filtered, unreachable, and excluded targets; verify server and assigned-agent results, provenance, bounds, and zero excluded traffic.

### Tests for User Story 1

- [ ] T010 [P] [US1] Add failing unit tests for target expansion, exact exclusion precedence, attempt-budget truncation, TCP outcome classification, cancellation, timeout, and no application payload in internal/discovery/discovery_test.go (FR-004–FR-008, FR-018; SC-002, SC-006, SC-007).
- [ ] T011 [P] [US1] Add failing coordinator tests for due-run jitter, server-vantage opt-in, one active scope/vantage run, restart re-lease, deadline, and partial states in internal/discovery/coordinator_test.go (FR-002, FR-004, FR-016–FR-019; SC-001, SC-007, SC-008).
- [ ] T012 [P] [US1] Add failing agent tests for assignment validation, expiry, separate execution context, one active scan, result paging/retry, 16 MiB spool, and telemetry priority in internal/agent/scan_test.go and internal/agent/runtime_test.go (FR-003–FR-008, FR-014, FR-017, FR-018, FR-022; SC-007, SC-008).

### Implementation for User Story 1

- [ ] T013 [US1] Refactor internal/discovery/discovery.go behind a cancellable scanner interface and implement bounded target/attempt scheduling plus explicit open/closed/filtered/unreachable/skipped/scanner_error outcomes without banners or payloads (FR-004–FR-008, FR-017, FR-018; SC-001, SC-002, SC-006, SC-007).
- [ ] T014 [US1] Implement durable due-run scheduling, deterministic jitter, server-vantage leases, progress, deadline, cancellation boundaries, final/partial summaries, and restart recovery in internal/discovery/coordinator.go (FR-002, FR-004, FR-016–FR-019, FR-022; SC-001, SC-007, SC-008).
- [ ] T015 [US1] Start and stop the coordinator with server context and graceful shutdown, without scanning until owner-enabled policy exists, in apps/server/main.go and internal/control/http.go (FR-002, FR-016, FR-017, FR-022).
- [ ] T016 [US1] Implement credential-free agent scan execution, desired-state handoff, local cancellation/revision fencing, bounded result pages, separate scan-result spool, and telemetry/update priority in internal/agent/scan.go and internal/agent/runtime.go (FR-003–FR-008, FR-014, FR-017, FR-018, FR-022; SC-001, SC-002, SC-007, SC-008).
- [ ] T017 [US1] Wire agent scan configuration and capability reporting through apps/agent/main.go, packaging/linux/agent.service, and internal/control/agents.go while preserving older-agent compatibility (FR-003, FR-007, FR-014, FR-022).
- [ ] T018 [US1] Implement protected scope scan-policy mutations and on-demand run creation with validation, idempotency, audit, and safe errors in internal/control/discovery.go and internal/control/http.go (FR-001, FR-002, FR-016, FR-018, FR-020).
- [ ] T019 [US1] Add scan schedule, server enablement, assigned-agent selection, typed SSH/alternate-port entry points, limits, review-before-enable, and on-demand action to apps/web/src/views/scopes.tsx and apps/web/src/lib/api.ts (FR-001–FR-003, FR-005, FR-016, FR-018, FR-021).
- [ ] T020 [US1] Add authenticated end-to-end server/agent scan coverage with controlled TCP listeners and excluded-address assertions in tests/integration/active_discovery_test.go and tests/e2e/discovery.spec.ts (FR-001–FR-009, FR-014–FR-018; SC-001–SC-003, SC-006–SC-008).

**Checkpoint**: Server and one assigned agent discover bounded SSH entry points. This is MVP scanning, not complete automated coverage.

---

## Phase 4: User Story 2 - Supply credentials for a discovered device (Priority: P1)

**Goal**: Convert current open supported entry-point evidence into one actionable prerequisite flow and one trusted enrollment handoff.

**Independent Test**: Discover SSH without credentials, apply missing/invalid/insufficient/trust/connectivity cases, add valid access, and produce exactly one eligible enrollment job without rescanning.

### Tests for User Story 2

- [ ] T021 [P] [US2] Add failing reconciliation tests for supported versus observation-only entry points, multi-vantage duplicates, stale/contradictory evidence, request dedupe, and candidate state transitions in internal/discovery/reconcile_test.go (FR-008–FR-013, FR-015, FR-019; SC-003–SC-006).
- [ ] T022 [P] [US2] Add failing integration tests for credential and trust mutation re-evaluation, invalid authentication, insufficient privilege, host-key mismatch, server-connectivity failure, and exactly one enrollment job in tests/integration/active_discovery_test.go and tests/integration/access_test.go (FR-010–FR-015; SC-003–SC-005, SC-008).

### Implementation for User Story 2

- [ ] T023 [US2] Implement conservative entry-point-to-candidate projection, per-vantage freshness/contradiction, exact candidate state catalog, and one access-request dedupe key per candidate/method/endpoint in internal/discovery/reconcile.go and internal/store/discovery.go (FR-008–FR-011, FR-015, FR-019; SC-003, SC-004, SC-006).
- [ ] T024 [US2] Trigger bounded candidate re-evaluation after matching credential create/rotate/revoke and trust create/revoke without putting secrets into work records in internal/control/access.go, internal/enrollment/access.go, and internal/store/access.go (FR-010, FR-012–FR-015, FR-020; SC-003–SC-005, SC-008).
- [ ] T025 [US2] Revalidate current scope, exclusion, entry point, credential version, host trust, destination, release, and server reachability before scan-derived enrollment job creation in internal/enrollment/access.go and internal/discovery/reconcile.go (FR-012–FR-015; SC-003, SC-005, SC-008).
- [ ] T026 [US2] Implement owner candidate list/detail/entry-point/reevaluate endpoints with cursor bounds, action descriptors, safe provenance, and no banner/secret fields in internal/control/discovery.go and api/openapi.yaml (FR-010–FR-13, FR-15, FR-19–FR-21).
- [ ] T027 [US2] Add actionable candidate filters, evidence details, entry-point freshness, specific access states, and credential/trust flow links to apps/web/src/views/network.tsx, apps/web/src/views/access.tsx, and apps/web/src/lib/api.ts (FR-010–FR-013, FR-019, FR-021; SC-004, SC-005, SC-009).
- [ ] T028 [US2] Complete keyboard-first browser coverage for needs-credentials through trusted enrollment, duplicate evidence, unsupported service, and safe error text in tests/e2e/discovery.spec.ts and tests/e2e/access.spec.ts (FR-010–FR-015, FR-020, FR-021; SC-003–SC-005, SC-009).

**Checkpoint**: A scan-derived SSH candidate becomes an actionable, secret-safe, trusted enrollment flow.

---

## Phase 5: User Story 3 - Grow coverage from every safe vantage point (Priority: P1)

**Goal**: Cover segmented networks through explicit agent assignments while policy change, pause, scanner loss, and duplicate evidence remain safe.

**Independent Test**: Server discovers one lab network and assigned agent discovers a second; neither probes an adjacent unauthorized range, and revocation or pause fences all effects.

### Tests for User Story 3

- [ ] T029 [P] [US3] Add failing multi-vantage tests for explicit assignment, site mismatch, older-agent capability, route evidence without authority, scanner revocation/decommission, and no all-scopes leakage in internal/control/http_test.go and tests/integration/active_discovery_test.go (FR-002–FR-004, FR-007, FR-009, FR-014, FR-015, FR-022; SC-002, SC-003, SC-008, SC-010).
- [ ] T030 [P] [US3] Add failing pause/cancellation tests covering queued, leased, dialing, uploading, late-result, server restart, and unreachable-agent acknowledgement in internal/discovery/coordinator_test.go and tests/integration/active_discovery_test.go (FR-004, FR-015–FR-18, FR-022; SC-002, SC-007, SC-008).

### Implementation for User Story 3

- [ ] T031 [US3] Enforce explicit scope/vantage assignments and scan capabilities in desired state, removing all-enabled-scope exposure and fencing stale/revoked/decommissioned scanners in internal/control/agents.go, internal/policy/policy.go, and internal/store/discovery.go (FR-002–FR-004, FR-007, FR-014, FR-022; SC-002, SC-008, SC-010).
- [ ] T032 [US3] Integrate global discovery pause, scope disablement/revision changes, active-holder acknowledgement, cancellation requests, and late-result rejection across internal/control/operations.go, internal/discovery/coordinator.go, and internal/agent/scan.go (FR-004, FR-015–FR-018, FR-022; SC-002, SC-007, SC-008).
- [ ] T033 [US3] Preserve separate authoritative evidence from server and agents while reconciling one conservative candidate and preventing address-only identity merge in internal/discovery/reconcile.go and internal/topology/reconcile.go (FR-007–FR-009, FR-015, FR-019; SC-003, SC-006, SC-010).
- [ ] T034 [US3] Create an owner-authorized segmented Linux lab harness with server-only, agent-only, excluded, and adjacent unauthorized networks plus packet capture in scripts/test-active-discovery.sh (FR-002–FR-009, FR-014, FR-017, FR-018; SC-001, SC-002, SC-007, SC-008, SC-010).
- [ ] T035 [US3] Add assigned-vantage freshness, capability, and pause/cancellation status to apps/web/src/views/scopes.tsx, apps/web/src/views/network.tsx, and tests/e2e/discovery.spec.ts (FR-002, FR-003, FR-016, FR-017, FR-021; SC-009, SC-010).

**Checkpoint**: Segmented multi-vantage coverage works without implicit scope grants or unsafe late effects.

---

## Phase 6: User Story 4 - Understand scan progress and limits (Priority: P2)

**Goal**: Make run coverage, limits, failures, freshness, retention, and audit evidence honest and operable.

**Independent Test**: Force each bound, timeout, cancellation, scanner loss, overlap, and stale-evidence case, then inspect status and audit using keyboard navigation.

### Tests for User Story 4

- [ ] T036 [P] [US4] Add failing API/store tests for run/candidate pagination, outcome counts, partial reasons, status projection, retention blockers, cleanup restart, audit redaction, and 100-device query bounds in internal/store/discovery_test.go, internal/control/http_test.go, and tests/integration/scan_storage_test.go (FR-007, FR-016, FR-018–FR-022; SC-006–SC-009).
- [ ] T037 [P] [US4] Add failing browser tests for run progress, last/next schedule, coverage, partial/stale/error/empty states, candidate filtering, keyboard navigation, and 360/768/1440 CSS-pixel layouts in tests/e2e/discovery.spec.ts (FR-016, FR-019, FR-021; SC-006, SC-009).

### Implementation for User Story 4

- [ ] T038 [US4] Implement scan-run list/detail/cancel, scope scan-status, coverage projection, bounded candidate filters, cursor pagination, and exact safe errors in internal/control/discovery.go and apps/web/src/lib/api.ts (FR-016, FR-018–FR-021; SC-006, SC-007).
- [ ] T039 [US4] Implement 30-day result/evidence and 90-day run-summary cleanup with unresolved-request protection, restart-safe batches, lag/backpressure status, and no candidate/exclusion deletion in internal/store/discovery.go and internal/discovery/coordinator.go (FR-018, FR-019, FR-022; SC-006, SC-007).
- [ ] T040 [US4] Build accessible scan-run progress, outcome summaries, partial reasons, last/next schedule, vantage provenance, evidence freshness, and action filters in apps/web/src/views/network.tsx and apps/web/src/views/scopes.tsx (FR-016, FR-018–FR-021; SC-006, SC-009).

**Checkpoint**: Owner can distinguish complete coverage from partial, stale, failed, or bounded work and act without reading logs.

---

## Phase 7: Recovery, operations, and release evidence

**Purpose**: Prove upgrade/recovery safety, network boundaries, performance, accessibility, and truthful support claims across all stories.

- [ ] T041 Extend backup/restore and recovery-mode tests to preserve scan policy, assignments, candidate/evidence links, access requests, terminal summaries, and cancellation of stale active authority in internal/store/recovery.go, scripts/backup.sh, scripts/restore.sh, and tests/integration/scan_storage_test.go (FR-015, FR-016, FR-019, FR-022; SC-003, SC-006, SC-008).
- [ ] T042 [P] Document configuration, permissions, traffic model, retention, pause, failure recovery, and explicit non-goals in docs/operations.md, docs/security.md, docs/support-matrix.md, and README.md (FR-001–FR-006, FR-014, FR-016–FR-022).
- [ ] T043 Run the isolated segmented lab, packet-boundary capture, credential/trust journey, restart/replay/backpressure cases, older-agent compatibility, keyboard/viewport checks, and 256-address/100-device workload from specs/003-active-network-scanning/quickstart.md; record exact results and remaining limits in specs/003-active-network-scanning/evidence.md and acceptance-matrix.md (FR-001–FR-022; SC-001–SC-010).
- [ ] T044 Run `go test ./... -count=1`, `go vet ./...`, `npm run check`, `npm run build`, `npm run format:check`, `git diff --check`, contract validation, and repository integration scripts; record outputs and commit in specs/003-active-network-scanning/evidence.md (FR-001–FR-022; SC-001–SC-010).

## Dependencies and execution order

### Phase dependencies

- Phase 1 starts immediately.
- Phase 2 depends on Phase 1 and blocks every user story.
- US1 depends on Phase 2 and delivers minimum useful scanning.
- US2 depends on US1 evidence ingestion but remains independently testable with seeded open-SSH evidence.
- US3 depends on US1 assignment/execution and US2 reconciliation; it proves segmented autonomous growth.
- US4 depends on durable runs/evidence from US1 and candidate states from US2; it may begin with seeded fixtures after Phase 2.
- Phase 7 depends on all selected stories.

### Within each story

- Write listed tests first and confirm intended failure.
- Complete store/policy behavior before handlers.
- Complete handlers before UI integration.
- Revalidate current scope and identity at every effect boundary.
- Record evidence and commit each logical slice without co-author trailers.

### Parallel opportunities

Parallel work requires separate authorization. If authorized:

- T006 can proceed beside T004–T005 after T002 contracts stabilize.
- US1 unit tests T010–T012 touch separate packages.
- US2 tests T021–T022 touch separate layers.
- US3 tests T029–T030 touch separate layers.
- US4 tests T036–T037 separate backend and browser coverage.
- T042 documentation can proceed beside final validation after behavior stabilizes.

## Parallel example: User Story 1

```text
Task T010: Probe safety and outcome tests in internal/discovery/discovery_test.go
Task T011: Coordinator lease and scheduling tests in internal/discovery/coordinator_test.go
Task T012: Agent isolation and spool tests in internal/agent/scan_test.go
```

## Implementation strategy

### MVP first

1. Complete contracts and foundational persistence.
2. Complete US1 server plus assigned-agent bounded scanning.
3. Stop and validate scope/exclusion packet boundaries and telemetry isolation.
4. Do not describe MVP as automatic enrollment until US2 and US3 pass.

### Incremental delivery

1. US1: truthful active entry-point evidence.
2. US2: actionable credentials/trust and one enrollment handoff.
3. US3: segmented multi-vantage autonomous coverage.
4. US4: operational visibility, freshness, limits, and retention.
5. Final gates: recovery, lab capture, accessibility, and reference workload.

## Notes

- `[P]` means file/dependency independence, not standing authorization for parallel agents.
- Existing working-tree changes belong to current work and must not be reverted.
- No live scan is authorized by this task list; use only owner-approved lab ranges during T043.
- All task paths are repository-relative and immediately actionable.
