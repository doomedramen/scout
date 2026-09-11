# Implementation Plan: Active Network Scanning

**Branch**: `main` | **Date**: 2026-09-11 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/003-active-network-scanning/spec.md`

## Summary

Turn Scout's existing dormant TCP probe and candidate-reconciliation primitives into a durable scanning subsystem. The control server schedules bounded scans from itself and explicitly assigned enrolled agents, accepts idempotent result pages, preserves per-vantage evidence, derives actionable access states, and reuses the existing trusted SSH enrollment boundary. Extend the owner API and React views with scan policy, runs, coverage, candidate evidence, and credential-resolution actions. Do not depend on nmap, collect banners, send credentials to scanners, or infer identity from an address.

## Technical Context

**Language/Version**: Go 1.26 module for server and agent; TypeScript 5.7 and React 19 in the existing web workspace.

**Primary Dependencies**: Go standard library networking and concurrency primitives; existing pgx PostgreSQL access, agent mTLS/token identity, job queue, policy engine, secret broker, audit logger, React, Vite, and shadcn-style components. No nmap executable or new scan library.

**Storage**: PostgreSQL 17 for production-shaped scan policies, leases, runs, result receipts, entry-point observations, candidate projections, and access-request links; existing in-memory store for deterministic unit and HTTP tests.

**Testing**: Go unit and package tests, disposable PostgreSQL integration tests, controlled TCP listeners and network namespaces or owner-authorized Linux VMs, Playwright browser tests, existing contract tests, `go test ./...`, `go vet ./...`, and npm checks.

**Target Platform**: Linux control server and Linux AMD64/ARM64 agents. IPv4 CIDRs and literal IPv4/IPv6 targets in the first release; IPv6 prefixes require an explicit finite target list or budget-safe expansion.

**Project Type**: Self-hosted web service plus native distributed agents and browser console.

**Performance Goals**: Detect at least 95% of reachable SSH endpoints in a 256-address reference scope within five minutes; surface accepted actionable evidence within 30 seconds; re-evaluate candidates within one minute of matching access changes; keep inventory and scan views within the existing two-second p95 target at 100 devices.

**Constraints**: Default 10 connection attempts/second, 16 concurrent attempts, two-second timeout, 256 targets, one configured SSH entry point, five-minute schedule with deterministic jitter, ten-minute run deadline, 16,384 attempts per run, 1,000 results and 1 MiB per uploaded page. Hard ceilings: 1,000 attempts/second, 16 concurrent attempts per scanner, 4,096 targets, 64 entry points, 16,384 attempts per run, and 15-minute deadline. Exclusions and current policy are checked before dispatch and immediately before each connection. Scanning never blocks telemetry and never receives credentials.

**Scale/Scope**: Reference workspace of 100 devices, up to 128 scopes, up to 16 active scanning vantage points per scope, and at most one active run per scope/vantage pair. Retain detailed entry-point observations for 30 days and compact run summaries for 90 days; current candidate state persists under existing lifecycle rules.

## Constitution Check

*GATE: Passed before research and after design.*

- **Owner-scoped autonomy**: Pass. Only owner-configured ranges, exclusions, methods, limits, and assigned vantage points authorize probes. New routes or observations never expand scope. Automatic enrollment remains policy-controlled.
- **Credentials and identity**: Pass. Scanners receive no enrollment secrets. Scan evidence does not establish host identity; existing trust verification and isolated enrollment workers remain mandatory.
- **Evidence-based monitoring**: Pass. Results retain source, observation/receipt time, outcome, freshness, policy revision, and contradictory evidence. Closed or missing results never become proof of absence.
- **Recoverable lifecycle**: Pass. Policy revisions fence queued work; durable run leases and idempotent receipts survive restart. Revocation and pause stop new probes and enrollment handoff.
- **Extensible service knowledge**: Pass. Entry-point catalog is typed and bounded without arbitrary executable checks. Initial SSH knowledge does not hard-code future enrollment methods into scheduling.
- **Self-hosting and usability**: Pass. No hosted scanner or cloud service is required. UI includes keyboard-accessible progress, partial states, evidence, and missing-access actions.
- **Workflow**: Pass. Positive and negative scope, identity, replay, credential, pause, and failure tests are explicit. No real network is scanned without owner-authorized lab setup.

Post-design result: no constitutional exception. `Complexity Tracking` is omitted because no gate violation requires justification.

## Project Structure

### Documentation (this feature)

```text
specs/003-active-network-scanning/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── acceptance-matrix.md
├── handoff.md
├── evidence.md
├── checklists/
│   └── requirements.md
├── contracts/
│   ├── agent-protocol.md
│   └── control-api.md
└── tasks.md
```

### Source Code (repository root)

```text
apps/
├── server/main.go
├── agent/main.go
└── web/src/
    ├── lib/api.ts
    └── views/
        ├── scopes.tsx
        ├── network.tsx
        └── access.tsx

api/
├── openapi.yaml
└── schemas/

internal/
├── agent/
│   ├── runtime.go
│   └── scan.go
├── control/
│   ├── agents.go
│   └── discovery.go
├── discovery/
│   ├── discovery.go
│   ├── coordinator.go
│   ├── reconcile.go
│   └── catalog.go
├── policy/policy.go
└── store/
    ├── migrations.go
    ├── models.go
    └── discovery.go

tests/
├── contracts/discovery_contract_test.go
├── e2e/discovery.spec.ts
└── integration/
    ├── active_discovery_test.go
    └── scan_storage_test.go

scripts/
└── test-active-discovery.sh
```

**Structure Decision**: Extend existing Go server/agent, shared store, policy, discovery, enrollment, and React console. Keep raw probing in `internal/discovery`, agent orchestration in `internal/agent`, durable scheduling in `internal/discovery/coordinator.go`, HTTP authorization in `internal/control`, and credential use inside existing enrollment components.

## Design

### Scheduling and vantage assignment

Scope policy gains an explicit schedule, entry-point catalog, server-vantage flag, assigned agent IDs, run bounds, and revision. Enabling a scope does not silently assign every agent. The server coordinator materializes one due run per scope/vantage pair with deterministic jitter derived from stable scope and scanner identity. A durable lease with epoch fencing prevents duplicate execution after restart or when multiple control-server processes share PostgreSQL.

Server runs execute locally through the same scanner interface used by agents. Agent runs are returned in desired state only to the assigned, current, non-revoked identity. Assignment includes a complete immutable policy snapshot, run ID, revision, expiry, ranges, exclusions, entry points, and bounds. Agent policy expiry ends scanning even if the server becomes unreachable.

### Probe safety and result truth

The first release uses TCP connect outcomes only. It does not send application payloads or store banners. Each target/entry-point attempt is re-authorized against the immutable run snapshot immediately before dialing; a concurrent local cancellation or newer desired-state revision stops remaining attempts. Server reconciliation rejects a run whose current scope revision, scanner identity, or lease no longer permits effects. Rejected stale evidence may be retained only as non-actionable audit metadata, never as an enrollment trigger.

Each uploaded page has a run ID, page ordinal, content hash, observation window, completion marker, and bounded results. Identical replay returns the original receipt; conflicting replay is rejected. Results use explicit outcome codes: `open`, `closed`, `filtered`, `unreachable`, `skipped`, or `scanner_error`. Only `open` for a recognized supported enrollment entry point can advance an access or enrollment state.

### Reconciliation and enrollment handoff

Entry-point observations are append-only evidence with expiry. A current projection groups by site, scope, address, transport, port, and source. Candidate reconciliation preserves separate vantage evidence and uses existing site-scoped candidate identity; it does not merge devices by address alone. Contradictory or missing later evidence affects freshness, not historical identity.

For open SSH, reconciliation creates or updates one access request keyed by candidate, method, and endpoint when credentials, trust, privilege, or server reachability are missing. Credential or trust changes enqueue bounded candidate re-evaluation rather than requiring another scan. Enrollment job creation remains in `internal/enrollment`, rechecks current scope revision, credential version, trusted host identity, destination, release, and target reachability, and uses the privileged worker. Scanners never authenticate to discovered services.

### Backpressure and isolation

Agent scanning runs on a separate schedule and context from host collection. It uses a separate bounded result spool so a disconnected server cannot evict telemetry. One active scan per agent is the default, and scan work yields to telemetry deadlines. The server reserves database and queue capacity for identity, audit, telemetry, and pause controls; scan saturation returns retryable backpressure and marks the run partial.

Detailed observations expire after 30 days. Run summaries and audit outcomes remain for 90 days. Cleanup never removes active exclusions, candidates, access requests, or device identity merely because scan evidence expired.
