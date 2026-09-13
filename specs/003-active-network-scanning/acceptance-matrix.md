# Acceptance Matrix: Active Network Scanning

| Requirements | Stories | Success criteria | Required evidence |
| --- | --- | --- | --- |
| FR-001–FR-006 | US1, US3 | SC-001, SC-002, SC-007, SC-010 | Policy contract tests plus packet capture proving exact targets, exclusions, transport, bounds, and no intrusive checks from server and agent vantage points. |
| FR-007–FR-009, FR-019 | US1, US4 | SC-003, SC-006, SC-010 | Multi-vantage fixtures showing separate provenance, explicit outcomes, stale/contradicted evidence, and no address-only device merge. |
| FR-010–FR-015 | US2, US3 | SC-003–SC-005, SC-008 | Missing/invalid/privilege/trust/connectivity fixtures, credential mutation re-evaluation, duplicate evidence, and exactly one authorized enrollment job. |
| FR-016–FR-018 | US3, US4 | SC-001, SC-002, SC-007, SC-008 | On-demand and scheduled runs; cancellation, global/scope pause, revision fencing, revocation, restart, spool, queue, and bound tests while telemetry continues. |
| FR-020–FR-022 | US4 | SC-006–SC-009 | Audit redaction, retention/restart compatibility, scan status, browser accessibility, and older-agent compatibility evidence. |

## Release gates

- All contract schemas reject unknown fields, excessive counts, invalid ranges, unsupported transport, stale revisions, wrong scanners, and conflicting replay.
- Server and agent lab runs each find expected open SSH endpoints without probing excluded or adjacent unauthorized targets.
- Scan evidence never establishes trust and scanners never receive credential values.
- Credential and trust changes re-evaluate current candidates without full rescans or credential guessing.
- Pause, revocation, policy changes, restart, and backpressure stop or fence effects truthfully.
- Detailed observations and summaries respect retention while exclusions and unresolved work persist.
- Browser journey passes keyboard and viewport checks.
- Reference workload records actual timing, rate, concurrency, storage, queue, and telemetry-isolation results.

## Validation status — 2026-09-13

| Area | Status | Evidence |
| --- | --- | --- |
| Contracts, Go tests, vet, web checks/build/format, diff cleanliness | Passed | Final repository gate recorded in `evidence.md`; 16 Playwright tests passed. |
| Fixture scanning, enrollment, credentials/trust, recovery, retention, and backpressure | Passed | All repository fixture scripts passed, including disposable PostgreSQL 17 integration. |
| Synthetic 100-device/24-hour workload | Passed | 96,000 samples and 120,000 observations; 41.687s ingest, 11.66ms query p95; not a live capacity claim. |
| Live segmented packet capture | Passed | Guarded three-target Linux Docker lab passed in Colima; packet capture showed authorized server/agent SYNs and no excluded/adjacent SYNs. |
| Live 256-address workload | Passed | Owner-authorized QEMU Linux lab ran a bounded `/24` with one excluded address: 255 targets planned, open/closed endpoints observed, and actionable access candidates matched open SSH results. This is a bounded workload result, not a live 100-device capacity claim. |
| Native systemd, older-agent, provider, and live hardware/storage compatibility | Open | Fixture coverage passed; live evidence remains intentionally opt-in and unclaimed. |
