# Implementation Handoff: Active Network Scanning

## Objective

Implement [spec.md](spec.md) using [plan.md](plan.md), [research.md](research.md), [data-model.md](data-model.md), and [contracts](contracts/). Execute [tasks.md](tasks.md) in order and record acceptance results in [evidence.md](evidence.md).

## Critical boundary

Scout already contains bounded TCP probe and candidate/access primitives. They are not scheduled by server or agent. This package wires durable execution without broadening authorization.

Never:

- scan outside current owner-approved scopes or exclusions;
- return every enabled scope to every agent;
- send credential or trust material to scanners;
- use open ports or addresses as trusted identity;
- invoke nmap, capture banners, guess passwords, run exploits, scan UDP, or execute arbitrary scripts;
- let late, revoked, expired, or superseded results trigger enrollment;
- let scan work block telemetry, heartbeat, certificate renewal, pause acknowledgement, or updates.

## Implementation order

1. Lock contracts, bounds, migration, run state, receipts, and policy assignment.
2. Build shared bounded scanner and server coordinator with durable fencing.
3. Add agent assignment, isolated execution, separate spool, and result paging.
4. Reconcile evidence into candidates and access/enrollment states.
5. Add owner scan/run/candidate APIs and UI.
6. Validate negative security cases, segmented lab, accessibility, retention, and reference workload.

## Existing code to preserve

- `internal/discovery/discovery.go`: target expansion and TCP probe safety.
- `internal/discovery/service.go`: conservative candidate and access reconciliation.
- `internal/policy/policy.go`: scope/method/port/exclusion checks.
- `internal/enrollment/access.go`: trusted access and enrollment-job boundary.
- `internal/control/agents.go`: authenticated desired state and result identity pattern.
- `internal/agent/runtime.go`: telemetry/update runtime; scanning must remain independent.
- `internal/store/discovery.go`: candidate/access persistence semantics.
- `apps/web/src/views/scopes.tsx`, `network.tsx`, and `access.tsx`: owner journeys to extend.

## Completion standard

Task checkbox is not evidence. Each completed phase must include exact commands, environment, expected and actual result, commit, and remaining limits. Live compatibility requires owner-authorized lab evidence; fixtures alone cannot prove network behavior.
