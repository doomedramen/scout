# Design Decisions and Research

**Date**: 2026-09-11. These are implementable design choices, not proof of security or compatibility. Existing repository decisions take precedence over generic stack recommendations.

## Preserve the monorepo and build incrementally

**Decision**: Keep the Go module, existing npm lockfile, Turborepo wrappers, and React/shadcn console. Add backend packages and thin commands by responsibility.
**Rationale**: The scaffold already builds and separates frontend/backend concerns; replacing it creates work without advancing monitoring.
**Alternatives considered**: Re-scaffolding, many microservices, a new frontend framework. None is needed for single-owner scope.
**Evidence**: Repository baseline 7d189a1; `apps/server/main.go`, `internal/control/http.go`, and `internal/collector/collector.go` confirm only health and a local snapshot exist.

## Owner authentication and agent identity

**Decision**: Argon2id password hashes, local TOTP/recovery codes, opaque browser sessions, and distinct mTLS device identities. No SSO or multi-user roles initially.
**Rationale**: Local ownership and revocable device authority fit self-hosting without a cloud identity dependency. Session and certificate lifetimes are planning defaults, not universal best-practice claims.
**Alternatives considered**: JWT-only owner sessions, shared fleet API keys, mandatory OIDC. Shared secrets obstruct device revocation; the others add unnecessary initial scope.
**Source**: [OWASP password storage guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html) recommends Argon2id and gives minimum work parameters; the plan adopts those as a floor, with calibration and abuse limits.

## Durable queue and relational evidence

**Decision**: PostgreSQL-backed jobs, constrained inventory, partitioned metrics, separate batch deduplication, and derived topology.
**Rationale**: Transactions can couple authorization revision, lease ownership and state transitions; provenance is more important than specialized graph traversal at the proposed scale.
**Alternatives considered**: Redis queue, dedicated graph database, timeseries extension. Reconsider only if measurements show the selected storage cannot meet the criteria.
**Source**: [PostgreSQL 17 SELECT documentation](https://www.postgresql.org/docs/17/sql-select.html) describes SKIP LOCKED as useful for queue-like consumers. It does not provide target-side fencing or exactly-once installation; those remain explicit design obligations.

## Signed binary distribution

**Decision**: Detached Ed25519 signature over exact manifest bytes, with SHA-256 and size for each platform binary. Agents verify independently; signing keys never belong on the control server by default.
**Rationale**: A distribution-server compromise must not confer release-authoring authority. Exact signed bytes avoid ambiguous JSON reserialization.
**Alternatives considered**: HTTPS plus checksum, server-signed jobs as executable authority, unsigned container tags. These do not establish independent publisher trust.
**Source**: [Go crypto/ed25519](https://pkg.go.dev/crypto/ed25519) supplies maintained signing/verification primitives. Trust rotation, anti-replay, compatibility and recovery are Scout-specific controls, not provided by the primitive alone.

## Privileged operations

**Decision**: Separate worker capability and target-scoped secret grants; native update guardian with fixed operations and target-local locking.
**Rationale**: A monitoring identity must not become fleet administration authority. Network segmentation may require an owner-provisioned worker rather than giving all agents SSH keys.
**Alternatives considered**: Every agent stores the fleet key; generic remote shell endpoints; a privileged monolithic agent. Rejected because they enlarge the impact of a compromised host.

## Platform and provider test matrix

**Decision**: Plan for systemd Ubuntu Server 24.04 and Debian 12 on AMD64/ARM64. Use Docker and Proxmox as two initial reference service adapters and a fake third provider as an extension-contract test.
**Rationale**: A finite first test matrix makes release acceptance possible without restricting future platforms/providers. macOS and Windows stay later milestones.
**Alternatives considered**: Promise all Linux distributions and hypervisors immediately; hardcode two providers into the core. Both contradict honest support claims or extensibility.
**Validation status**: Actual provider versions and API privileges must be recorded by the adapter lab task before claiming support. Fixture-based tests alone are insufficient.

## Proposed defaults resolved for implementation

The plan selects concrete timeouts, buffering, retention, session lifetimes, canary behavior and resource limits so implementation can start. These are configurable and documented as planning choices; no unresolved user decision blocks the first slice. The implementing agent may refine values based on measured evidence while updating linked acceptance criteria and recording the reason. It must not change automatic enrollment into a manual approval workflow or narrow the provider model without user direction.

## Provider access and host trust

**Docker decision**: Use the maintained Moby Go client with API negotiation, pin its reviewed version when implementing, and target Docker Engine 29 as the first reference major. Prefer an explicit read-endpoint proxy or authorization policy; direct rootful socket access requires an owner-visible privileged-access choice. Never claim a read-only mount restricts daemon commands. Record negotiated version and unavailable fields. [Docker authorization](https://docs.docker.com/engine/extend/plugins_authorization/) and [Engine API versioning](https://docs.docker.com/reference/api/engine/) explain these boundaries; [Moby client](https://github.com/moby/moby/blob/master/client/client.go) is the client source. Alternative: raw HTTP for every endpoint, rejected as unnecessary version-negotiation work.

**Proxmox decision**: Target PVE 9 as the first reference major, with a dedicated monitoring user and privilege-separated API token. Assign PVEAuditor only on the paths required by selected read endpoints; effective permission depends on both user and token grants. Configure the cluster CA and validate hostnames/expiry; no verification bypass. Start with cluster resources, node status/storage, and guest status, recording per-method ACL requirements during the adapter task. [Proxmox token/permission documentation](https://github.com/proxmox/pve-docs/blob/master/pveum.adoc) and [certificate management](https://github.com/proxmox/pve-docs/blob/master/certificate-management.adoc) are primary references. Alternative: admin token with skipped TLS checks, rejected.

**SSH decision**: Use golang.org/x/crypto/ssh with knownhosts.New over owner-managed per-site/scope trust files. Normalize host:port and preserve aliases and IPv6; block unknown, changed and revoked keys. Trust updates are atomic and audited, separate from secret storage. [Go SSH documentation](https://pkg.go.dev/golang.org/x/crypto/ssh) and [known_hosts implementation](https://github.com/golang/crypto/blob/master/ssh/knownhosts/knownhosts.go) define the callback/matching behavior. Do not use InsecureIgnoreHostKey. Host CA support must be covered by explicit tests before offering it in the UI.

Reference major versions above define initial engineering targets, not compatibility claims. Exact installed patches, endpoints, grants, and tested architectures are written into the release matrix by T055.
