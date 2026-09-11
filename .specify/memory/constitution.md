<!--
Sync Impact Report
Version: unconfigured template -> 1.0.0 (initial project governance)
Principles added: Owner-scoped autonomy; Credentials and identity;
Evidence-based monitoring; Recoverable agent lifecycle; Extensible collectors;
Self-hosting and usable interfaces.
Sections added: Delivery constraints; Development workflow; Governance.
Sections removed: none. Deferred template values: none.
-->

# Scout Constitution

## Core Principles

### I. Owner-scoped autonomy

Scout MUST automatically enroll eligible devices inside owner-configured network scopes using assigned access. Routine per-device approval MUST NOT be required. Discovery MUST NOT expand the authorized scope or grant installation authority. Newly enrolled agents MUST contribute observations so coverage can continue growing. Installation MUST be idempotent, preserving one active agent identity per device. The owner MUST be able to exclude devices and stop discovery or enrollment.

### II. Credentials and identity are security boundaries

Every device agent MUST have an independently revocable identity. Credentials MUST be scoped, encrypted in storage, excluded from logs and telemetry, and available only to the component that needs them. Ordinary monitoring agents MUST NOT receive reusable fleet enrollment credentials. Privileged enrollment and update roles MUST have explicit, narrow capabilities. Authenticated transport, verified remote identities, and owner authentication MUST precede production telemetry or remote installation. A discovered address MUST NOT be treated as a trusted identity.

### III. Monitoring claims require evidence

Metrics and relationships MUST retain source, time, and availability information. Missing, stale, unsupported, and zero measurements MUST remain distinct. Topology MUST distinguish observed relationships from inference and physical cabling. Demo data MUST be explicitly labeled and isolated from operational data. Compromised or faulty collectors MUST NOT be able to impersonate other devices or exhaust unbounded server resources.

### IV. Agent lifecycle must be recoverable

Agents MUST support server-delivered, publisher-verified updates without public internet access. Manual and automatic rollouts MUST share verification, policy, compatibility, and rollback rules. Failed installation or update MUST leave a recoverable state. Credentials and device identity MUST survive valid upgrades without re-enrollment. The control server MUST NOT possess release-signing authority by default. Decommissioning MUST revoke identity and prevent accidental immediate reinstallation.

### V. Service knowledge is extensible

The agent MUST support independent collectors for hypervisors, container runtimes, and other services. Docker and Proxmox are example adapters, never the boundary of the model. Provider-specific behavior MUST stay outside the agent's common scheduling, identity, transport, and update mechanisms. Collectors MUST declare permissions, limits, health, and versioned output. A collector failure MUST NOT stop base host monitoring. Detection MUST NOT silently grant new privileges.

### VI. Self-hosting and usability are product requirements

Core monitoring MUST work without a hosted account or cloud control plane. Deployment, persistence, backup, restore, and upgrades MUST be documented and exercised. The interface MUST support fast inventory inspection, readable host charts, a separate topology view, keyboard use, and explicit empty, error, stale, and missing-access states. Accessibility MUST NOT depend on interpreting color alone.

## Delivery Constraints

- Initial product: one owner, Linux agents first; macOS follows, then Windows. Multi-owner and customer isolation are later scope.
- Agreed stack: Go server and agent, PostgreSQL, React/TypeScript, shadcn/ui and charts, Turborepo with npm workspaces.
- Docker Compose is the initial server deployment target; a Linux VM on Proxmox VE is a supported hosting direction, not a mandatory platform dependency.
- Existing code is a development scaffold and UI demo. It MUST NOT be described as a working monitoring MVP or evidence of unimplemented behavior.
- Fleet capacity and performance claims MUST be based on published test conditions. Proposed acceptance targets are not measured support limits.

## Development Workflow

Specifications MUST define user outcomes and testable acceptance criteria before implementation. Technical plans MUST identify trust boundaries, data ownership, failure recovery, and compatibility before remote enrollment or updates are built. Tasks MUST trace to requirements; completed tasks require relevant evidence. Changes affecting identity, credential access, scope enforcement, installation, or updates require positive and negative integration tests. Reversible cosmetic work does not require redundant tests.

Follow the repository's AGENTS.md: use only gpt-5.6-luna unless the user says otherwise, commit regularly, push when a remote exists, and never add co-author trailers. Do not create parallel agents unless separately authorized. Preserve user changes. A spec-writing request does not authorize deploying Scout onto real machines.

## Governance

This constitution governs project artifacts subject to current user instructions. The product specification is the source of truth for behavior; plans describe implementation; legacy design notes supply context. Record amendments with their rationale and update affected specifications and checklists together. Use a major version for incompatible principle changes, a minor version for new principles or material additions, and a patch for clarifications. Check every implementation plan against these principles and document any exception explicitly rather than silently weakening a control.

**Version**: 1.0.0 | **Ratified**: 2026-09-11 | **Last Amended**: 2026-09-11
