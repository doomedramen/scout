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
- Date: 2026-09-12 (Europe/London); implementation commit: pending.
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
- Date: 2026-09-12 (Europe/London); implementation commit: pending.
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
