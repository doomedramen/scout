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
