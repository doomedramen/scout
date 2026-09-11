# Tasks: Scout Monitoring Platform

**Input**: spec.md, plan.md, research.md, data-model.md and contracts/ in this directory.
**Status**: All implementation work below is unchecked. The existing scaffold is a starting asset, not a completed story.
**Format**: T identifiers are stable. US identifiers map to spec stories. Each task names intended files; create missing paths as needed. Tests are required here because the specification explicitly calls for acceptance and negative security cases.

## Phase 1: Setup

Goal: preserve the working scaffold and establish repeatable, isolated verification.

- [X] T001 Record the current baseline and limits in specs/001-scout-platform/evidence.md after inspecting AGENTS.md, README.md, apps/server/main.go, internal/control/http.go, internal/collector/collector.go and apps/web/src/App.tsx; run existing check/build/tests without treating demo screens as real monitoring.
- [X] T002 Add isolated PostgreSQL integration and browser harnesses in tests/integration/, tests/e2e/, scripts/test-integration.sh and apps/web/package.json; separate test configuration from personal .env and provide safe cleanup.
- [X] T003 Translate contracts/control-api.md, agent-protocol.md and releases-and-collectors.md into executable api/openapi.yaml and api/schemas/ with enum, limit, authentication and error fixtures in tests/contracts/; do not invent arbitrary command endpoints.

## Phase 2: Foundation

Goal: trusted data and authorization boundaries before any remote control capability. Complete before story implementation.

- [X] T004 Implement migration runner, transactional repository layer and initial schema from data-model.md in migrations/ and internal/store/, with migration/restart tests in tests/integration/store_test.go.
- [X] T005 Implement redacted structured audit and safe error mapping in internal/audit/ and internal/control/errors.go; verify no raw backend/secret values appear in tests/integration/audit_test.go (FR-034).
- [X] T006 Implement file-provisioned key loading, per-secret encryption, context binding, versioned wrapping-key rotation and fail-closed missing-key behavior in internal/secrets/; add tamper/rotation/backup-key tests in internal/secrets/secrets_test.go (FR-012).
- [X] T007 Add TLS configuration, production startup validation, request size/rate limits and separated owner/agent/worker authorization boundaries in apps/server/ and internal/identity/; test certificate/header impersonation in tests/integration/transport_test.go (FR-006).
- [X] T008 Implement revision-aware job queue, lease renewal/fencing, bounded retries, cancellation and global recovery/pause flags in internal/jobs/ and internal/policy/; prove concurrent claims and stale reports fail in tests/integration/jobs_test.go (FR-010, FR-014, FR-033).

## Phase 3: US1 — Secure workspace and first real agent (P1)

Independent test: protected setup and one native Linux agent reports real measurements, survives restart, and becomes stale/offline when disconnected. No discovery needed.

- [X] T009 [US1] Add setup/session/MFA and invitation/revocation acceptance fixtures in tests/integration/identity_test.go and tests/e2e/setup.spec.ts, covering singleton setup races and unauthorized access (FR-001, FR-002, FR-006).
- [X] T010 [US1] Implement local setup invitation, Argon2id owner password, TOTP, recovery codes, hashed cookie sessions, CSRF, reauthentication and local owner recovery in internal/auth/ and apps/server/ (FR-001).
- [X] T011 [US1] Implement device-bound invitations, CSR validation, certificate issuance/renewal/revocation and lost-response reconciliation in internal/identity/ and internal/store/identity.go; prevent reused tokens and duplicate active identities (FR-002, FR-003).
- [X] T012 [US1] Extend internal/collector/ with Linux CPU/memory/filesystem/uptime/interface traffic readers, counter-reset/unavailability handling and fixture-based collector tests (FR-004).
- [X] T013 [US1] Turn apps/agent/ into a persistent daemon with durable identity, reporting/heartbeat loops, bounded disk spool, jittered retry and explicit snapshot command compatibility; add packaging/linux/agent.service (FR-003, FR-005, FR-033).
- [X] T014 [US1] Implement atomic batch deduplication, partitioned measurement storage, freshness calculation, heartbeat and basic device/history queries in internal/telemetry/ and internal/control/; test replay, clock skew and counter gaps (FR-004, FR-005, FR-006).
- [X] T015 [US1] Wire protected setup/sign-in and first-agent enrollment views in apps/web/src/views/setup.tsx and apps/web/src/lib/api.ts, including token-file instructions and secret-safe errors (FR-001, FR-002, FR-019).
- [X] T016 [US1] Replace fixture-only inventory/current host readings with authenticated queries in apps/web/src/views/systems.tsx and apps/web/src/views/device.tsx while retaining isolated demo mode (FR-004, FR-005, FR-015, FR-019).
- [ ] T017 [US1] Create scripts/test-first-agent.sh and record real systemd-host setup, renewal/revocation, restart persistence and loss-of-contact evidence against SC-001, SC-002 and SC-012.

## Phase 4: US7 — Self-hosting and recovery (P1)

Independent test: deploy and restore the US1 fixture on a clean host; preserve identities/history and pause restored authority. Scheduled before remote enrollment so recovery exists before credential use expands.

- [ ] T018 [US7] Add container images and production Compose/server TLS/persistent volume configuration in packaging/containers/ and compose.yaml; preserve local development commands and avoid default host/socket mounts (FR-031).
- [ ] T019 [US7] Add backup/restore metadata, key requirements and schema/version checks in scripts/backup.sh, scripts/restore.sh and internal/store/recovery.go; restore atomically into a separate verified destination (FR-032).
- [ ] T020 [US7] Implement restored-workspace recovery mode, session invalidation and owner policy/credential/revocation reconciliation in internal/control/recovery.go and apps/web/src/views/recovery.tsx (FR-032).
- [ ] T021 [US7] Implement retention, orphan cleanup and disk-pressure/backpressure with visible dropped-sample status in internal/telemetry/retention.go and internal/control/settings.go; test bounded buffering/retention without erasing exclusions (FR-033).
- [ ] T022 [US7] Create scripts/test-restore.sh and docs/operations.md covering clean container/VM deployment, separate-key restore, stale authorization, rollback and recovery; record SC-001 and SC-011 evidence (FR-031, FR-032, FR-036).

## Phase 5: US5 — Signed agent lifecycle (P1)

Independent test: update an internet-blocked agent through the server and recover from interrupted/bad releases. Existing manual bootstrap is enough; automatic enrollment is not needed.

- [ ] T023 [US5] Add signature/tamper/platform/downgrade and interrupted-slot fixtures in internal/updates/verification_test.go and tests/integration/updates_test.go (FR-020 through FR-025).
- [ ] T024 [US5] Implement manifest verification, immutable artifact storage and bounded offline release import in internal/updates/releases.go and internal/control/releases.go; keep signing keys outside server (FR-021, FR-022).
- [ ] T025 [US5] Implement publisher tooling and trust-transition/revocation verification in scripts/release/ and internal/updates/trust.go; use test keys only in fixtures and document local trust recovery (FR-022, FR-025).
- [ ] T026 [US5] Implement manual/automatic/pinned policies, windows, canaries, concurrency and failure pauses in internal/updates/rollout.go and internal/control/rollouts.go; fence stale assignments (FR-023).
- [ ] T027 [US5] Implement server-only artifact download/resume and desired-state validation in apps/agent/ and internal/updates/download.go; preserve identity/configuration and reject invalid transitions (FR-020, FR-022).
- [ ] T028 [US5] Implement constrained guardian, local peer authorization, crash journal, atomic slots, fsync, readiness, single rollback and guardian self-upgrade recovery in apps/updater/ and packaging/linux/ (FR-024).
- [ ] T029 [US5] Wire release import, rollout progress, pins, policies and truthful installed/desired versions in apps/web/src/views/updates.tsx (FR-025).
- [ ] T030 [US5] Create scripts/test-updates.sh using disposable VMs; exercise public-internet denial, offline import, all interrupted stages, startup rollback, stale policy and key transition; record SC-008, SC-009, SC-012.

## Phase 6: US3 — Scoped access and missing prerequisites (P1)

Independent test: seed candidates directly, resolve missing credentials/trust/privilege/connectivity, and observe eligible jobs resume. Scheduling before US2 makes its automatic flow safe.

- [ ] T031 [US3] Add scope/secret/worker negative contract cases in tests/integration/access_test.go, including wrong target, revoked grant, ordinary-agent redemption, host mismatch and output leakage (FR-011, FR-012, FR-013).
- [ ] T032 [US3] Implement site/scope access bindings, encrypted credential metadata/write-only routes, rotation/revocation and version invalidation in internal/secrets/broker.go and internal/control/access.go (FR-012).
- [ ] T033 [US3] Implement per-site known_hosts/CA trust records, normalized host:port matching and audited key changes in internal/enrollment/trust.go and internal/control/trust.go (FR-013).
- [ ] T034 [US3] Implement unmet-prerequisite classification, request deduplication, access-change retry and safe diagnostics in internal/enrollment/access.go and internal/store/access.go (FR-011).
- [ ] T035 [US3] Wire scoped credential entry, trust resolution and missing-access queue in apps/web/src/views/access.tsx; explain privileged access and never expose saved values (FR-011, FR-012).
- [ ] T036 [US3] Verify resumed work and all secret/trust rejection cases in tests/integration/access_test.go and tests/e2e/access.spec.ts; record SC-005 and SC-012 without using production credentials.

## Phase 7: US2 — Automatic discovery and enrollment (P1)

Independent test: isolated multi-vantage network with eligible and excluded targets, duplicate sightings and revoked policy. Requires US1, US3 and signed artifacts from US5.

- [ ] T037 [US2] Add finite lab scope fixtures and exclusion/DNS/IPv6/rate/concurrency tests in internal/policy/policy_test.go and tests/integration/discovery_test.go (FR-007, FR-008).
- [ ] T038 [US2] Implement local interface/route/neighbor observations and bounded authorized probes in internal/discovery/; enforce finite IPv6 budgets, method limits, cancellation and expiry (FR-008).
- [ ] T039 [US2] Implement versioned scope APIs, current-policy distribution and candidate reconciliation in internal/control/scopes.go and internal/enrollment/candidates.go (FR-007, FR-009).
- [ ] T040 [US2] Implement explicitly registered worker identity, job claims and target-bound grants in apps/enroller/ and internal/enrollment/worker.go; reject ordinary agent capabilities (FR-009, FR-010, FR-012).
- [ ] T041 [US2] Implement verified fixed SSH installation, target-local lock/journal, bounded elevation and post-install agent confirmation in internal/enrollment/install.go and packaging/linux/install/ (FR-003, FR-009, FR-010, FR-013).
- [ ] T042 [US2] Implement global/per-scope pause, execution revalidation, credential/scope revision fencing and safe-boundary cancellation in internal/policy/ and internal/enrollment/worker.go (FR-014).
- [ ] T043 [US2] Wire scope creation, activation, exclusions, missing access and job progress in apps/web/src/views/scopes.tsx and apps/web/src/views/enrollment.tsx; no routine per-device approval (FR-007, FR-009, FR-011, FR-014).
- [ ] T044 [US2] Create scripts/test-enrollment.sh with second-vantage discovery and trusted worker placement; record zero excluded-target attempts, duplicate/restart handling and SC-003, SC-004, SC-005, SC-012.

## Phase 8: US4 — History, topology and interface quality (P1)

Independent test: deterministic real-storage fixture including gaps, conflicting identities and overlapping sites; keyboard and pointer journeys. Requires US1; discovery evidence may be seeded independently.

- [ ] T045 [US4] Implement provenance/expiry projection and conservative multi-source identity reconciliation in internal/topology/ with ambiguity, clone, NAT/site and manual-correction tests (FR-016, FR-017).
- [ ] T046 [US4] Implement bounded historical queries/aggregation and all filter fields in internal/control/devices.go and internal/telemetry/query.go; preserve gaps, units and min/max through downsampling (FR-015).
- [ ] T047 [US4] Complete shadcn host charts, time selection, service state, inventory filters and API error/retry behavior in apps/web/src/views/device.tsx and apps/web/src/views/systems.tsx (FR-015, FR-019).
- [ ] T048 [US4] Implement topology graph plus equivalent keyboard-accessible list/evidence inspector in apps/web/src/views/network.tsx; keep logical/physical confidence distinct (FR-016, FR-017, FR-018).
- [ ] T049 [US4] Verify dark UI at 360/1440 pixels, chart keyboard tooltips, meaningful status text, reduced motion, no-data/error states and demo isolation in tests/e2e/monitoring.spec.ts; record SC-006 and SC-007 (FR-018, FR-019).

## Phase 9: US6 — Extensible service collectors (P2)

Independent test: two reference provider categories plus a fake third provider; a failed adapter never blocks the base collector. P2 remains part of target release.

- [ ] T050 [US6] Implement descriptor/config schema, registry, bounded scheduling, cancellation, health and panic/error containment in internal/collector/registry.go and internal/collector/scheduler.go (FR-026, FR-028).
- [ ] T051 [US6] Implement Docker reference adapter in internal/collector/docker/ using a pinned maintained client with version negotiation, explicit read permissions and secret-safe entity/metric translation (FR-027, FR-029).
- [ ] T052 [US6] Implement Proxmox reference adapter in internal/collector/proxmox/ with cluster CA verification, scoped privilege-separated token, documented endpoint ACLs and typed entities/metrics (FR-027, FR-029).
- [ ] T053 [US6] Implement cluster collector ownership/failover and guest/device association in internal/collector/leases.go and internal/topology/services.go with duplicate/epoch tests (FR-030).
- [ ] T054 [US6] Add collector configuration/access/health and service entity views in apps/web/src/views/services.tsx and internal/control/collectors.go; use common provider metadata rather than hardcoded agent core branches (FR-026 through FR-030).
- [ ] T055 [US6] Create scripts/test-collectors.sh and tests/fixtures/collectors/ with fake third adapter, permission denial, malformed/slow data, limits and redaction; record exact Docker/PVE live versions and grants in docs/support-matrix.md and SC-010 evidence (FR-036).

## Phase 10: US8 — Decommissioning (P2)

Independent test: revoke and exclude, rediscover without reinstalling, verify optional uninstall separately, explicitly re-enable.

- [ ] T056 [US8] Implement atomic identity revocation, persistent exclusions and job cancellation in internal/enrollment/decommission.go and internal/control/devices.go (FR-035).
- [ ] T057 [US8] Implement optional verified uninstall and truthful offline/removal outcomes in internal/enrollment/uninstall.go and packaging/linux/uninstall/; no claim of removal without confirmation (FR-035).
- [ ] T058 [US8] Add decommission/re-enable workflows and history retention explanation in apps/web/src/views/device.tsx; re-enable revalidates scope and access (FR-035).
- [ ] T059 [US8] Create scripts/test-decommission.sh covering offline agents, revoked requests, repeated discovery, metric retention and explicit re-enable; record SC-012.

## Phase 11: Release verification and handover

- [ ] T060 Finish docs/support-matrix.md and docs/operations.md with exact tested distro/provider versions, permissions, key rotation, compatibility, backup/restore and release trust recovery (FR-036).
- [ ] T061 Create scripts/test-load.sh and record 100-device/24-hour ingestion/read results, queue limits, disk pressure and retention sizing in specs/001-scout-platform/evidence.md; tune documented defaults only with evidence (SC-002, SC-006, FR-033).
- [ ] T062 Review and run the complete negative/security matrix in tests/integration/security_test.go and specs/001-scout-platform/acceptance-matrix.md; fix failures before enabling production enrollment or updates.
- [ ] T063 Complete remaining browser accessibility/empty/error checks and split heavy chart code when measured bundle/loading behavior justifies it in apps/web/src/; record final UI evidence (FR-018, FR-019).
- [ ] T064 Reconcile every FR and SC against evidence, update README.md and specs/001-scout-platform/evidence.md with accurate capability status, run final appropriate checks, commit without co-author trailers and push only to a configured remote. Do not mark unfinished lab-dependent acceptance complete.

## Dependencies and execution strategy

Execute in the listed order by default: setup, foundation, US1, US7, US5, US3, US2, US4, US6, US8, release verification. This deliberately puts credential access and signed installation artifacts before automatic enrollment. US4 can be developed using seeded authenticated observations once US1 exists; it does not require live discovery. US6 depends on US1 and topology association interfaces. US8 depends on revocation and enrollment cancellation. Phases share files; do not blindly execute tasks concurrently.

First deliverable: T001–T017, a secure single-agent monitoring slice. It is not the full release. Subsequent commits keep the product runnable and update task/evidence state. Work can continue autonomously through routine implementation choices; ask only for genuinely missing external access or a material scope decision. A missing live provider lab does not prevent fixture/adapter implementation, but it prevents claiming live compatibility.

Potential parallel work, only if separately authorized: US1 collector fixtures and setup UI after identity contract; US7 operations docs and restore fixtures after recovery design; US5 release verification and rollout UI after release schema; US3 trust fixtures and access UI after contracts; US2 discovery probes and scope UI after policy contract; US4 history charts and graph view after query contracts; US6 Docker/Proxmox adapters after registry contract; US8 uninstall fixtures and device UI after decommission contract. These are opportunities, not instructions to spawn agents.

## Completion rules

Check a task only after its implementation and relevant acceptance evidence exist. Record blockers and partial results without changing the checkbox. Never count a stub, mock result, demo chart or scaffold smoke test as real enrollment, service compatibility or update recovery. Preserve source-of-truth spec IDs when splitting tasks further.
