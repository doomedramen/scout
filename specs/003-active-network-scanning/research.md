# Research: Active Network Scanning

## Existing implementation boundary

**Decision**: Extend the existing `ExpandTargets`, `Probe`, `ReconcileSightings`, scope policy, access evaluation, desired-state endpoint, job queue, and candidate store rather than replacing discovery.

**Rationale**: Scout already validates literal addresses and CIDRs, applies exclusions, defaults to SSH port 22, limits target count, rate, concurrency, and timeout, and can derive access requests and enrollment jobs. Production gap is orchestration, durable runs, assignment, ingestion, evidence, and UI—not basic socket dialing.

**Alternatives considered**: Replace discovery wholesale; trigger scans directly from HTTP handlers; treat passive observations as sufficient. Replacement discards tested boundaries, direct HTTP execution does not survive restart, and passive observations miss quiet devices.

## Scanner engine

**Decision**: Use bounded TCP connect scanning in-process for initial release. Do not invoke or embed nmap.

**Rationale**: TCP connect is unprivileged, portable across supported Linux deployments, easy to cancel and bound, and enough to identify reachable SSH entry points. It avoids external binary installation, nmap output compatibility, privileged raw sockets, script-engine risk, and a second update surface.

**Alternatives considered**: Shell out to nmap; raw SYN scanning; ICMP-first discovery; UDP scanning. These add privileges, dependencies, ambiguous host-up assumptions, or intrusive behavior without improving initial SSH enrollment enough.

## Entry-point recognition

**Decision**: Represent configured probes as typed catalog entries containing display name, transport, port, and optional supported enrollment method. Ship SSH/TCP/22 as only default and supported enrollment method.

**Rationale**: A plain port list cannot distinguish a useful SSH entry point from an observed unsupported service. Typed entries let UI report evidence without creating irrelevant credential requests. Alternate SSH ports remain possible without banner inspection.

**Alternatives considered**: Infer service solely from well-known ports; capture banners; run version detection. Port inference becomes misleading when ports are remapped, while banner collection and version detection add sensitive data and hostile-parser risk.

## Vantage authorization

**Decision**: Require explicit server enablement and explicit enrolled-agent assignment per scope. Do not expose every enabled scope to every agent.

**Rationale**: Current desired-state handling returns all enabled scopes to every authenticated agent. Active scanning turns loose visibility into material network action. Explicit assignment preserves owner-scoped autonomy and supports segmented networks.

**Alternatives considered**: All agents scan all scopes; infer assignments from routes; let agents propose new scopes. These create duplicate load or silently expand authorization. Route evidence may inform an owner choice but cannot grant it.

## Durable scheduling

**Decision**: Store scheduled runs and leases with scope revision, scanner identity, expiry, attempt count, and fencing epoch. Apply deterministic jitter and allow one active run per scope/vantage pair.

**Rationale**: Durable runs survive server restart, provide truthful UI progress, and prevent duplicate work under multiple server processes. Fencing makes late completion from an expired lease non-authoritative.

**Alternatives considered**: In-memory tickers only; reuse enrollment jobs without a distinct run entity; allow overlapping cycles. In-memory schedules lose state, enrollment jobs have different authority and result semantics, and overlap multiplies traffic.

## Agent delivery and result upload

**Decision**: Extend authenticated desired state with a bounded scan assignment and add idempotent paged result submission. Keep scan results separate from telemetry batches and use a dedicated bounded agent spool.

**Rationale**: Desired state already carries expiring policy and update work. Separate result contracts make scan-specific limits, replay handling, completion, partial status, and policy revision explicit. Separate spooling prevents scanning from consuming telemetry's buffer.

**Alternatives considered**: Encode results as generic telemetry observations; use a persistent command channel; send one unpaged response. Generic observations lack run truth, command channels broaden remote execution, and single responses exceed request bounds.

## Policy checks

**Decision**: Validate targets at policy creation, run materialization, immediately before each probe, on page ingestion, and before reconciliation effects or enrollment handoff.

**Rationale**: Policy can change while work is queued, executing, disconnected, or uploading. Multiple checks close time-of-check/time-of-use gaps while immutable run snapshots keep execution deterministic.

**Alternatives considered**: Validate only at dispatch or result ingestion. Dispatch-only permits continued probing after revocation; ingestion-only allows unauthorized traffic even if results are rejected.

## Evidence and identity

**Decision**: Preserve observations per scope, endpoint, and vantage point with independent freshness. Never merge devices or trust hosts using address or open-port evidence alone.

**Rationale**: DHCP, NAT, cloned systems, multiple interfaces, and conflicting vantage points make address-only identity unsafe. Entry-point evidence answers reachability, not identity.

**Alternatives considered**: One mutable row per address; automatically accept SSH host keys discovered during scanning; delete candidates when a port closes. These erase provenance or violate trust boundaries.

## Credential-needed state

**Decision**: Create one deduplicated access request per candidate, supported access method, and endpoint. Re-evaluate affected candidates on credential and trust mutations.

**Rationale**: Owners need one actionable item, not a new request every scan. Event-driven re-evaluation makes supplied access useful immediately while enrollment still applies all current checks.

**Alternatives considered**: Bind one credential to every scope; retry every credential; wait for next scan. A single binding is too restrictive, guessing risks lockouts and leaks, and scan-cadence delay makes UI feel broken.

## Bounds and retention

**Decision**: Preserve defaults of 10 probes/second, 16 concurrent connections, two-second timeout, 256 targets, and five-minute cadence. Add a hard 16,384-attempt run budget, ten-minute default run deadline, 1,000-result/1 MiB upload pages, 30-day evidence retention, and 90-day run-summary retention.

**Rationale**: Defaults already appear in Scout's 001 plan and code. Attempt budget closes target-count multiplied by port-count gap. Page limits reuse established ingestion constraints.

**Alternatives considered**: Unlimited port multiplication; unbounded IPv6 prefix enumeration; permanent raw evidence. All permit traffic or storage amplification.

## Failure and pause semantics

**Decision**: Mark runs `queued`, `leased`, `running`, `uploading`, then `completed`, `partial`, `failed`, `cancelled`, `rejected`, or `expired`. Global discovery pause, scope disablement, revision change, scanner revocation, or deadline stops new attempts at next target boundary.

**Rationale**: States distinguish no devices from incomplete work and allow safe cancellation without pretending already-sent connections can be undone.

**Alternatives considered**: Binary success/failure; hard-kill agent process; let old work finish after pause. Binary state hides coverage, termination harms monitoring, and stale work violates owner intent.
