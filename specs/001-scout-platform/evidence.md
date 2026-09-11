# Scout implementation evidence

This file records executed verification only. Planned targets from the specification are not treated as passed until their implementation and independent acceptance run exist.

## Baseline

- Date: 2026-09-11 (Europe/London)
- Commit: `4d28b87` (`plan`), based on scaffold commit `7d189a1`
- Environment: macOS Darwin, arm64, Go 1.27.1, Node v24.14.0, npm 11.19.0
- Repository: clean before baseline checks; `origin` is configured at the existing repository remote
- Commands:
  - `npm ci`: passed; 167 packages installed, audit reported 0 vulnerabilities. npm warned that `fsevents` install scripts were not approved; no application check depends on that optional script.
  - `npm run check`: passed for agent, server, and web workspaces.
  - `npm run build`: passed for agent, server, and web workspaces. Vite reported an existing large-chunk warning; no failure.
  - `npm test`: passed. Existing coverage is the development health handler only; agent and collector had no tests.
  - `npm run snapshot -w @scout/agent`: passed and printed this host's local hostname, OS, architecture, CPU count, and non-loopback interface metadata. It did not enroll, upload, probe peers, or install software.
- Relevant requirements/outcomes: establishes T001 baseline only. It is not evidence for FR-001 through FR-036 or SC-001 through SC-012.
- Limitations: Docker Compose is unavailable on this host (`docker compose` is not a supported Docker command) and `psql` is not installed. Existing scaffold has no authenticated owner, durable telemetry, enrollment, updates, or live service collectors.

## Completed foundation and first-agent slice

- T002/T003: `./scripts/test-integration.sh` passed with a disposable
  `postgres:17-alpine` container and `npm run test:e2e -w @scout/web` passed
  against a locally started server on an alternate loopback port. The
  integration script accepts an explicit `SCOUT_TEST_DATABASE_URL`, never
  reads the personal `.env`, and removes its temporary container on exit.
  Contract tests parse every checked-in JSON schema and assert the bounded
  OpenAPI route/security surface.
- T004–T008: `go test ./...` passed migration/restart, redaction, encryption
  tamper/rotation, transport authorization, concurrent claim/fencing, retry
  bound, and pause tests. PostgreSQL stores the repository snapshot in the
  migrated workspace state and is reopened to prove restart persistence.
- T009–T016: protected singleton setup, Argon2id password handling, MFA and
  recovery flows, CSRF/session boundaries, one-use device invitations, CSR
  issuance, Linux host fixtures, durable agent identity, atomic batch replay
  handling, heartbeat/freshness projection, authenticated inventory queries,
  and isolated demo UI are implemented and covered by unit/integration tests.
- First-agent fixture: `./scripts/test-first-agent.sh` passed
  `TestRuntimeEnrollmentPersistenceAndReporting`. A runtime enrolled once,
  persisted its identity with mode `0600`, reported a real collector batch and
  heartbeat to the control handler, restarted without a second identity, and
  projected lost contact as offline with stale history. On this macOS host the
  platform collector correctly reports unsupported Linux-only metrics; the
  native Linux/systemd acceptance run remains open.
- Still open: T017 requires an owner-authorized disposable Linux systemd host
  for native install/restart/renewal/revocation evidence. No real device,
  production credential, or production endpoint was contacted.

## Recovery, updates, and scoped access

- T018–T022: production-profile Compose wiring and non-root server/agent
  images are checked in with named volumes only; server TLS, agent CA, release
  trust, setup-token, and wrapping-key files are explicit inputs. `go test
  ./internal/store ./internal/telemetry` and `./scripts/test-restore.sh`
  passed backup parsing, restore into a separate in-memory repository, session
  invalidation, expiry revocation, paused authority, retention/backpressure
  projection, and script safety checks. A live PostgreSQL restore remains
  opt-in and was not run against any user database.
- T023–T030: `./scripts/test-updates.sh` passed Ed25519 manifest and artifact
  verification, tamper/unknown-key/revocation rejection, platform and
  generation checks, immutable import/idempotency, range-resume downloads,
  journaled A/B slot recovery, readiness, single rollback, canary/window
  rollout planning, and rollout failure pausing. Native power-loss VM stages
  are still open; the updater does not claim those results.
- T031–T036: `go test ./tests/integration -run 'Test(Credential|Trust|Ordinary)'`
  passed target-bound encrypted credential redemption, wrong operation/target
  and revoked credential rejection, write-only listings, normalized trust
  matching and changed-key rejection, ordinary-agent separation, and
  deduplicated missing-access requests. No production secret was used.

## Discovery, monitoring interface, collectors, and decommissioning

- T035–T043: access, scope, enrollment, and missing-prerequisite views are
  wired to authenticated APIs. `go test ./...` and the access/enrollment
  integration and browser fixtures passed accepted and rejected credential,
  trust, ordinary-agent, pause, lease, exclusion, revision, and target-bound
  worker cases. Routine per-device approval is not introduced.
- T037–T044: `scripts/test-enrollment.sh` passed finite range, exclusion,
  DNS, IPv6-budget, method, rate, concurrency, cancellation, expiry, duplicate,
  and restart fixtures. The script prints the required second-vantage Linux
  lab prerequisites; no real second vantage or device was contacted, so T044
  remains open.
- T045–T048: topology projection, evidence expiry/provenance, identity
  ambiguity handling, bounded history queries, inventory filters, host charts,
  topology list/graph, and evidence inspection are implemented. The focused
  Go suite, `npm run check`, and `npm run build -w @scout/web` passed. Charts
  expose tabular samples and explicit gaps; logical and physical relationships
  remain distinct.
- T050–T054: registry, bounded scheduler, panic/error containment, leases,
  guest association, Docker and Proxmox adapters, fake-provider boundary, and
  collector configuration/health/service views are implemented. Fixture
  adapters load from `tests/fixtures/collectors/`; no live provider endpoint
  or credential was used.
- T056–T060: atomic decommission/revocation, persistent exclusions, truthful
  optional-uninstall status, explicit re-enable, operations guidance, and the
  support/compatibility matrix are implemented. `scripts/test-decommission.sh`
  passed the offline/revocation/re-enable fixture boundary. Native uninstall
  confirmation remains a live-lab requirement.
- T061: `scripts/test-load.sh` passed the synthetic 100-device/24-hour sample
  shape, bounded ingestion/query path, and visible limit behavior in 10ms on
  this run. This is explicitly not a live capacity, disk-pressure, or
  retention-sizing claim; T061 remains open.
- T062: the security matrix in `tests/integration/security_test.go` passed
  accepted and rejected authentication, transport, secret, trust, worker,
  update, audit, and decommission cases. Production enrollment and updates
  remain disabled by default and still require owner-authorized lab evidence.
- T063: measured web build output before splitting was 664.85 kB JavaScript
  (198.88 kB gzip) with a large-chunk warning. Lazy-loading host detail now
  produces a 326.96 kB initial chunk (98.54 kB gzip) and a 339.19 kB detail
  chunk (101.55 kB gzip), with no large-chunk warning. A real browser viewport
  and keyboard journey at both 360px and 1440px remains open.
- Still open: T017 native Linux/systemd acceptance, T022 clean-host
  deployment/restore, T030 power-loss VM update stages, T044 live
  second-vantage placement, T049 browser viewport/accessibility journey, T055
  live Docker/Proxmox version and ACL compatibility, T059 full offline
  decommission lab, and T061 live capacity/disk-pressure measurement.
