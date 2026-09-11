# Implementation Plan: Scout Monitoring Platform

**Branch**: `main` (active feature: `001-scout-platform`) | **Date**: 2026-09-11 | **Spec**: [spec.md](spec.md)

**Input**: Implement the agreed platform from the existing scaffold. This plan and its tasks are a handoff, not evidence of completed implementation.

## Summary

Build a modular Go control plane with PostgreSQL, a native Go agent, isolated enrollment execution, and a constrained native updater. Keep the React/shadcn console and replace demo-only paths incrementally with authenticated data. Deliver owner setup and one real agent first, then recovery, signed updates, scoped access/discovery/enrollment, full topology, service adapters, and decommissioning. Automatic installation is enabled by configured scope and access, not by per-device confirmation.

## Technical Context

**Language/Version**: Existing Go module targets 1.26; use that or a compatible supported toolchain. Existing frontend versions are pinned by package-lock.json; do not re-scaffold or update dependencies indiscriminately.

**Primary Dependencies**: Go net/http, crypto/tls, crypto/x509, crypto/ed25519, database/sql with existing pgx driver; add maintained cryptographic/SSH libraries as needed and pin them. Existing React, Vite, TypeScript, Tailwind, shadcn/ui and Recharts; Turborepo/npm workspaces remain the task runner.

**Storage**: PostgreSQL 17; SQL migrations and parameterized queries. Daily metric partitions with retention cleanup; relational inventory and policy. Verified release artifacts on a persistent content-addressed filesystem, metadata in PostgreSQL. No Redis or separate graph database initially.

**Testing**: Go unit/race/integration tests; real PostgreSQL in isolated test Compose; browser tests added to the web workspace; VM-based systemd/SSH/updater acceptance. Container-only tests do not prove host installation or power-loss recovery.

**Target Platform**: Initial native-agent test matrix: Ubuntu Server 24.04 and Debian 12, AMD64 and ARM64, with systemd. These are planned targets, not currently supported claims. Control plane is Linux/container-hosted. macOS development is supported; macOS/Windows production agents are later work.

**Project Type**: Self-hosted web application plus distributed host agents and privileged installation/update helpers.

**Performance Goals**: SC-001 through SC-012. Default 15-second reports, 45-second stale and 90-second offline thresholds; 100-device synthetic reference load and 10-target enrollment lab.

**Constraints**: Single owner; no cloud dependency; no reusable enrollment secrets in ordinary agents; no arbitrary remote shell command API; independent release verification; exact-version test evidence before compatibility claims.

**Scale/Scope**: Reference 4-vCPU/8-GiB server with SSD. 30-day raw metrics and 90-day audit retention are configurable initial defaults. Enforce disk high-water backpressure and report losses; measure actual storage requirements rather than promising this hardware can retain every possible service metric.

## Constitution Check

| Principle                  | Design evidence                                                                                                   | Pre-design / post-design |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------- | ------------------------ |
| Scoped autonomy            | Scope revisions, exclusions, execution revalidation, durable job leases, one active identity per device           | Pass / Pass              |
| Credentials and identity   | Separate owner sessions, agent mTLS, worker capability, encrypted credentials, independently provisioned keys     | Pass / Pass              |
| Evidence-based monitoring  | Typed sample availability, observed/received times, provenance, identity reconciliation, explicit demo separation | Pass / Pass              |
| Recoverable lifecycle      | Verified artifacts, journaled updater, previous slot, local startup checks, restore pause                         | Pass / Pass              |
| Extensible collectors      | Registry and common bounded lifecycle; provider-specific packages                                                 | Pass / Pass              |
| Self-hosting and usability | Persistent Compose/VM path, backup guide, live UI states, keyboard and viewport checks                            | Pass / Pass              |

These are design checks only. Release gates require implementation evidence in tasks and the acceptance matrix. No constitutional exception is proposed.

## Project Structure

### Documentation (this feature)

```text
specs/001-scout-platform/
  spec.md
  plan.md
  research.md
  data-model.md
  contracts/control-api.md
  contracts/agent-protocol.md
  contracts/releases-and-collectors.md
  quickstart.md
  tasks.md
  acceptance-matrix.md
  checklists/requirements.md
  handoff.md
  analysis.md
```

### Source Code (repository root)

```text
apps/web/src/             Existing console; split App.tsx into views as wired
apps/server/              HTTP listeners, configuration, startup/shutdown
apps/agent/               Persistent native agent command, snapshot compatibility
apps/enroller/            New bounded SSH job worker command
apps/updater/             New privileged native update guardian command
internal/auth/            Owner setup, password/MFA, sessions, recovery
internal/identity/        Bootstrap invitations, certificates, revocation
internal/store/           Queries and transaction boundaries
internal/audit/           Structured redacted security events
internal/secrets/         Credential broker and encryption/key rotation
internal/policy/          Scope matching and current authorization
internal/jobs/            Durable leases, retry and cancellation
internal/collector/       Existing base collector; registry and host adapters
internal/collector/docker/   Docker-specific adapter
internal/collector/proxmox/  Proxmox-specific adapter
internal/telemetry/       Validation, ingestion, buffering and retention
internal/discovery/      Local observations and bounded active probing
internal/topology/       Evidence reconciliation and graph projection
internal/enrollment/     Verified SSH installation steps
internal/updates/        Manifest verification, rollout and slot state
internal/control/        Extend existing health handler with owner APIs
migrations/              Numbered SQL migrations
api/                     Executable OpenAPI / JSON Schema contracts
packaging/               Container builds, Linux service/install definitions
scripts/                 Existing developer helpers and new acceptance runners
tests/                   Contract, integration, browser and VM-lab evidence
```

**Structure Decision**: Preserve one Go module and npm workspace wrappers. Add enroller/updater workspaces when needed, not empty scaffolding now. Keep a modular monolith for control logic; separate processes exist only where privileges differ.

## Design decisions and implementation sequence

### Owner and agent identity

Use local owner password authentication with Argon2id (19 MiB, two iterations, one lane minimum; calibrate upward), TOTP, and hashed one-use recovery codes. Initial setup uses a five-minute token provisioned to a private local file, never a public default password. Enforce a singleton owner in the database. Browser sessions use random opaque tokens stored as hashes, Secure/HttpOnly/SameSite cookies, CSRF tokens and Origin checks; default eight-hour absolute and 30-minute idle expiry. Sensitive actions require password/second-factor reauthentication within five minutes. Rate-limit authentication with generic errors. Local-console recovery rotates credentials and invalidates sessions; it must not expose a public unauthenticated reset.

Serve production owner traffic over configured TLS. Run an agent listener with mutual TLS and certificate-derived device identity; do not trust caller-supplied identity headers. Bootstrap happens over server-verified TLS with a device-bound invitation and CSR. The server chooses certificate identity/extensions, ignoring requested privilege claims. Thirty-day agent certificates renew before their final ten days; revoked identities are checked on every request. Expired credentials require re-bootstrap, never disabled verification.

Consume bootstrap invitation and record issued identity transactionally. If the response is lost, do not replay a consumed invitation: recover using the persisted device/job association, revoke any orphan issuance, and issue a replacement invitation after revalidating the original scope or owner bootstrap authorization. Never create a second active device to recover a network failure.

### Storage and limits

Use explicit migrations, transaction-scoped queries, unique constraints and a lease queue. Poll jobs with row locking and SKIP LOCKED; leases need fencing epochs so a recovered worker cannot report an old attempt as current. A lease timeout alone must not permit a second installer to modify a target already being modified: also use a target-local installation lock and reconcile its journal before retry.

Store metric samples with device, collector, entity, metric name, timestamp, availability and unit. Partition by receipt day; enforce deduplication via a separate batch-receipt key and atomic ingestion. Cap batches at 1 MiB and 1,000 samples, discovery observations at 1,000 per batch, and label key/value lengths at 64/256 characters. Default each collector to 2,000 entities and an explicit allowlist of metric labels. Page list APIs (100 default, 500 maximum). Aggregate chart responses to at most 600 points per series using min/max/mean and preserve availability gaps. Unknown namespaced collector fields can be retained within limits but must not affect core identity or authorization.

Agent disk buffering is at most one hour or 64 MiB; evict oldest batches at the limit and report a dropped-count event. Flush with jittered bounded backoff. Timestamp skew beyond five minutes is flagged and does not advance measurement freshness. Receipt time tracks contact, not the apparent age of a future sample. At 85% data-volume use warn the owner; at 95% stop accepting new metric batches with retryable backpressure while reserving capacity for identity, audit and recovery. Do not silently shorten promised retention; require owner policy changes or storage expansion.

### Secrets and installation authority

Store reusable enrollment credentials in a broker-controlled encrypted table using per-secret data keys, AES-256-GCM with fresh nonces, and record ID/type as authenticated context. Wrap data keys under an independently provisioned master key file (0400) outside the database and release store. Rotation rewraps keys; retained key versions support recovery until backups expire. Encryption does not defend against a compromised process permitted to decrypt.

The enroller is a separate process/role permitted to retrieve only the credential for a currently leased, authorized target job. Ordinary agents cannot claim worker jobs. Initial workers execute at the server or another owner-provisioned location; deployment of remote privileged workers is not automatic. Any service API tokens are a separate credential class, bound to the collector and endpoint, with explicit local provisioning or narrow authenticated delivery.

Authorize resolved target addresses at connection time using net/netip, scope/site context and exclusions; reject redirects and alternate out-of-scope addresses. Validate SSH against configured known_hosts or host CA trust. Use fixed installer operations, sanitized structured inputs, signed artifacts, target-local locking, narrowly defined elevation, and a persistent attempt journal. The worker marks enrollment complete only after the installed agent authenticates and reports. If server connectivity is absent, record that prerequisite rather than continuously reinstalling.

### Updates and trust

Sign exact manifest bytes with Ed25519 under an offline publisher key. SHA-256 binds the immutable binary to its signed metadata. The server is a distributor, not a signer. Bootstrap packages carry the initial trusted publisher keys; key changes require a transition signed by currently trusted keys or an explicit local recovery operation. Record monotonic trust/release generations and offline rollback state; stale signed data cannot authorize arbitrary downgrade.

Native installation uses version directories and an atomic current link. A small root-owned guardian accepts only validated desired-version jobs, writes a crash-safe journal, stages an inactive slot, re-verifies local file ownership/digest, switches, restarts, and checks local readiness for up to 60 seconds. Roll back once to the previous verified slot and mark failed; do not loop. The agent's writable data/config is separate. Agent configuration changes must remain backwards compatible across the rollback window. Updating the guardian itself requires a dual-slot launcher/recovery path and the same signed verification; do not give the ordinary agent arbitrary root command execution.

Automatic rollout default: a canary group of one device, five-minute healthy observation window, then two simultaneous updates; pause on any failed startup/rollback until owner resolution. Owners can tune these policies. A globally acknowledged pause fences all unstarted jobs. A pause request is initially pending while execution holders acknowledge a safe boundary; an unreachable holder remains pending rather than claiming it has stopped. Native artifacts are raw binaries (no archive extraction in the agent); offline server import may use a tightly validated bundle format. Import paths never become filesystem destinations without validation.

### Discovery, topology, and service adapters

Start with local interface/route/neighbor observations and scoped TCP connection probes. Default ten probes/second per scope, at most 16 concurrent, two-second connection timeout; probe only configured ports, initially SSH port 22. Default periodic discovery is five minutes with jitter. IPv6 accepts explicit targets/prefixes with a configured finite target budget; never enumerate an entire /64. Enrolled agents still contribute new local observations on their normal reporting schedule.

Graph projections come from expiring evidence; initial neighbor evidence expires after 15 minutes unless refreshed. Keep stable inventory and audit corrections independent of evidence expiry. Guest IDs are scoped to provider/cluster, IP addresses to site. Never merge only because an address matches. Ambiguous merges are owner-resolvable; revoked tombstones cannot be erased by reconciliation.

Collector registry uses a common descriptor, detect, collect, and close lifecycle with context deadlines, entity limits, and health output. Default ten-second deadline on a 30-second service interval, one invocation at a time per collector. Panic/error recovery marks only that collector degraded. Built-in collectors are trusted code; process isolation for third-party code is deferred, so do not enable executable plugin uploads. Reference adapters are Docker and Proxmox; a fake third provider verifies extensibility. Provider versions are recorded from actual lab tests before release, not guessed from current marketing versions.

### User interface and deployment

Retain shadcn charts and compact inventory styling. Split views and API access from App.tsx, isolate fixtures behind demo mode, add real selection/history ranges, pending/error states, and keyboard graph/list equivalents. Service metrics and topology reuse typed entities rather than adding vendor-specific navigation to the agent core.

Compose eventually includes the server, PostgreSQL, and optional enroller, with persistent data/release volumes and separately mounted key material. Serve the built UI from the control plane or an explicitly configured same-origin reverse proxy. Expose only required TLS ports; do not mount Docker socket or host root by default. Native agent packaging includes systemd units and documented permissions. Restores enter a persisted recovery mode that pauses installation/updates until the owner reconciles stale authority.

## Complexity Tracking

No constitutional exceptions. Separate enroller and guardian are justified by distinct privilege boundaries. A new broker service, graph database, cluster orchestrator, public plugin runtime, and mandatory cloud services are explicitly not required for this release.
