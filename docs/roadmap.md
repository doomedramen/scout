# Roadmap

## 1. Observable vertical slice

Build the control server, an owner-bootstrapped Linux agent, persistent metrics, and the browser interface. Package the server for Docker Compose and document a Linux VM deployment. This first slice establishes authenticated transport before automatic fleet enrollment.

Acceptance:

- A fresh deployment has a protected owner setup flow and no default password.
- One real agent enrolls using an expiring, single-use token and authenticated transport.
- CPU, memory, filesystem, uptime, and interface metrics appear with timestamps.
- Disconnecting the agent produces a visible stale/offline state.
- Restarting the server preserves identity and collected history.
- Inventory and network views reflect actual observations and support empty and error states.
- A documented backup can be restored successfully.

## 2. Agent lifecycle within the Linux MVP

Implement server-hosted signed releases, manual version assignment, offline bundle import, a constrained Linux updater, and startup rollback. Add opt-in automatic policies, pins, maintenance windows, and staged rollout once the manual path passes failure testing.

Acceptance follows [the agent update specification](agent-updates.md), including updates with the agent's internet access blocked and recovery from interrupted installation. This is part of the Linux MVP, before unattended enrollment expands the fleet.

## 3. Scoped discovery and topology

Add owner-defined network scopes, bounded discovery, candidate devices, provenance, expiry, and access requests. Establish identity reconciliation before remote installation.

Acceptance:

- Discovery never probes an excluded or out-of-scope address.
- Multiple agents observing the same device do not blindly duplicate or merge it.
- Each graph relationship exposes evidence and age.
- Unknown, unmonitored, stale, and healthy devices remain distinct.
- Discovery can be paused globally and disabled for individual scopes.

## 4. Secure remote enrollment

Add isolated enrollment execution, encrypted credential storage or external references, verified SSH identities, scoped approval policies, and revocable agent certificates. Complete the security review before enabling this feature for production use.

Acceptance:

- Scout automatically installs one agent per discovered eligible Linux device over SSH using owner-configured scopes and access. New agents continue contributing discovery observations without routine per-device approvals.
- A retry cannot create duplicate agents or leave uncontrolled installation jobs.
- Secret values never appear in browser responses, logs, telemetry, or ordinary agent storage.
- Expired approvals, revoked policies, changed SSH keys, and out-of-scope targets block installation.
- Owners can revoke identities, rotate secrets, stop enrollment, and inspect audit history.
- The security failure cases in the threat model have implementation-level verification.

## 5. Extensible service collectors

Implement a provider-independent collector registry and scheduling contract. Initial adapters can cover Docker and Proxmox VE; additional hypervisors, runtimes, and services must fit without changes to the agent core. Follow [the collector specification](service-collectors.md) for permissions, schema evolution, bounded execution, and topology reconciliation.

## 6. Advanced monitoring and alerting (spec 002)

After the 001 Linux foundations, deliver [spec 002](../specs/002-advanced-monitoring/spec.md) through its [implementation package](../specs/002-advanced-monitoring/handoff.md):

- Automatically enabled in-app incidents, sustained thresholds, acknowledgment and evidence-based recovery.
- Owner-enabled ntfy delivery, bounded retries, quiet hours and current-incident summaries.
- Read-only systemd health and CPU/load/swap/disk diagnostics with per-entity charts.
- Thirty days raw, ninety days five-minute, and one year hourly history with peaks and gaps preserved.
- SMART and ZFS health plus Linux temperature, fan, and NVIDIA/AMD/Intel GPU capabilities validated against real hardware.

Acceptance includes normalized telemetry migration, restart-safe aggregation, notification suppression after restore, the expanded 100-host workload, and published hardware evidence. All six areas belong to the target release; staged development milestones remain explicitly incomplete. This package is prepared, not implemented. It does not add OIDC, multi-user access, image-update checks, Podman support, new operating systems, or remediation.

## 7. Broader platform coverage

Expand agent support to macOS, then Windows. Evaluate SNMP devices, additional service adapters, and notification providers beyond ntfy against real deployments. An agent is not appropriate for every device; agentless observations are first-class inventory sources.

## Decisions before implementation

- Selected: a functioning Linux-first monitoring MVP, including agent lifecycle support.
- Selected: Go, PostgreSQL, React/TypeScript, shadcn/ui, and Turborepo.
- Selected: Linux first, macOS next, Windows after that; a single owner initially.
- Selected: automatic enrollment within configured scopes. Fleet capacity remains unmeasured.
