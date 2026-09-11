# Scout Implementation Handoff

## Start here

Implement Scout from this repository using the specification package below. The owner requested a self-hosted monitoring product, not another prototype. Preserve the scaffold and complete the ordered tasks with real evidence. This document contains everything needed to start without the original conversation.

Read in order:

1. [AGENTS.md](../../AGENTS.md) and [constitution](../../.specify/memory/constitution.md).
2. [Product specification](spec.md): eight journeys, FR-001–FR-036, SC-001–SC-012, boundaries and proposed defaults.
3. [Technical plan](plan.md) and [research decisions](research.md).
4. [Data model](data-model.md), [control API](contracts/control-api.md), [agent/worker protocol](contracts/agent-protocol.md), and [release/collector contracts](contracts/releases-and-collectors.md).
5. [Tasks](tasks.md), [validation guide](quickstart.md), [acceptance matrix](acceptance-matrix.md), and [consistency report](analysis.md).

## Current repository state

> Historical note: the inventory below describes the pre-implementation
> handoff. Current implementation status and acceptance evidence live in
> [evidence.md](evidence.md) and [tasks.md](tasks.md).

Application baseline is commit `7d189a1`. Subsequent specification commits add this handoff but do not implement its tasks. Recheck Git status and recent commits before touching files; another agent or the owner may have made newer changes.

What exists:

- Turborepo/npm workspaces for a React/Vite/shadcn console, Go server, and Go agent command.
- Compact system inventory and shadcn area charts with explicit demo fixtures, filtering, a simple illustrative network view, and a planned-updates screen.
- A development `/api/status` handler that checks PostgreSQL connectivity.
- A local host snapshot command reporting identity, CPU count, and interface addresses.
- PostgreSQL-only development Compose, random private local .env generation, and baseline build/check commands.
- Existing tests only for the health handler. Earlier Linux ARM64 container smoke test and AMD64/ARM64 cross-compilation are scaffold checks, not native agent/service validation.

What does not exist: owner authentication, enrolled device identities, real telemetry ingestion/history, discovery, automated SSH installation, credential broker, signed release pipeline/updater, real service adapters, production deployment or full acceptance tests. Do not infer backend behavior from UI fixtures. Do not rebuild the UI or monorepo from scratch.

## Non-negotiable product decisions

- One agent per device. After the seed agent and owner-configured scopes/access, automatic enrollment continues without routine per-device approval.
- Missing credentials, privilege, trust or connectivity return as actionable access requests. Discovery never expands authorized scope.
- Linux first; macOS next; Windows later. Single owner initially.
- Go server/agent, PostgreSQL, React/TypeScript, shadcn/ui and shadcn charts, Turborepo/npm workspaces.
- Agents update through the server with signed verification, manual or automatic rollout, offline import and rollback. They need server connectivity, not public internet.
- Service collectors are extensible across hypervisors, runtimes and other services. Docker and Proxmox are reference adapters only.
- Credentials, mTLS identity, execution fencing, recovery, provenance and truthful stale/unsupported states are core product behavior.

## Execution instructions

Use only **gpt-5.6-luna** unless the owner explicitly changes that instruction. Do not spawn additional agents unless separately authorized by applicable instructions. Commit regularly without co-author trailers. Push when a remote is configured; the repository's `origin` remote is configured. Do not invent or create a remote repository without owner direction.

Select `SPECIFY_FEATURE_DIRECTORY` as described in quickstart.md, validate prerequisites, and begin T001. Work in task order and keep the application runnable. The first deliverable is T001–T017: protected owner setup, one authenticated Linux agent, real stored observations and truthful stale/offline behavior. Continue to the full release unless the owner limits the assignment; this first slice is not completion of the whole product.

All 64 tasks are intentionally unchecked. Check a task only after the implementation and its meaningful verification are complete. Maintain `evidence.md` with commit, command, environment, outcome, FR/SC references and limitations. Use the acceptance matrix to avoid omissions. Supplied contracts are normative outlines; T003 creates machine-readable schemas and tests. Missing source paths in tasks are intended new implementation paths.

Proposed capacity, retention, timing and platform defaults are selected in the plan so work can begin. Refine them with evidence and update the documents together. Do not ask the owner to repeat settled decisions. If a live provider lab is unavailable, implement adapters and fixtures, leave live compatibility acceptance open, and report the exact missing test prerequisite. Never substitute fixture results for real compatibility evidence.

Use isolated test networks and disposable VMs. Repository implementation does not authorize probing the owner's LAN, installing software on real devices, retrieving existing personal credentials, or publishing the service. Do not copy local .env secrets into tests, logs, commits or this package. The server, agents and privileged workers must fail closed when trust or authorization is missing.

## Ready-to-send prompt

```text
Implement Scout in this repository. Start by reading AGENTS.md and
specs/001-scout-platform/handoff.md, then follow its reading order and the
64 dependency-ordered tasks in tasks.md. The existing application is only
a scaffold and demo UI; implement real behavior without re-scaffolding it.

Use only gpt-5.6-luna. Preserve the agreed automatic scoped enrollment,
one-agent-per-device model, signed offline-capable updates, extensible
service collectors, single-owner Linux-first scope, and existing stack.

Begin with T001–T017, verify that real single-agent monitoring works,
then continue through the remaining phases. Keep task state and evidence
accurate, run meaningful positive/negative tests, commit regularly without
co-author trailers, and push only when a remote exists. Do not deploy to
real devices or use production credentials without explicit authorization.
```
