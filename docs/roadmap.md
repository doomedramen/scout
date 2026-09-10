# Roadmap

## 1. Observable vertical slice

Build the control server, a manually enrolled Linux agent, persistent metrics, and the browser interface. Package the server for Docker Compose and document a Linux VM deployment.

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

- An owner can approve a discovered Linux device and install its agent over SSH.
- A retry cannot create duplicate agents or leave uncontrolled installation jobs.
- Secret values never appear in browser responses, logs, telemetry, or ordinary agent storage.
- Expired approvals, revoked policies, changed SSH keys, and out-of-scope targets block installation.
- Owners can revoke identities, rotate secrets, stop enrollment, and inspect audit history.
- The security failure cases in the threat model have implementation-level verification.

## 5. Broader infrastructure coverage

Evaluate Proxmox API integration, SNMP devices, Windows agents, service checks, alerts, and notification integrations against real deployments. An agent is not appropriate for every device; agentless observations are first-class inventory sources.

## Decisions before implementation

- Selected: a functioning Linux-first monitoring MVP, including agent lifecycle support.
- Choose implementation languages, storage, and UI tooling based on deployment footprint and maintenance requirements.
- Set the initial operating-system support and expected fleet size.
- Decide whether the first release is single-owner or requires multi-user roles immediately.
