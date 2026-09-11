# Security model

The implementation includes the owner, agent, worker, secret, transport,
update, and decommissioning boundaries described below. Fixture and local
integration tests exercise the negative cases; native Linux, live provider,
and production deployment acceptance remains open where noted in
[implementation evidence](../specs/001-scout-platform/evidence.md).

## Assets and trust boundaries

Protect owner credentials, enrollment authority, agent identities, infrastructure metadata, and monitoring integrity. Treat networks, discovered devices, telemetry payloads, and individual agents as untrusted. A compromised agent must not gain installation authority or read another device's secrets.

The control plane and enrollment worker are high-value systems. Encryption at rest does not protect secrets from a compromised process authorized to decrypt them. Isolation, minimal permissions, short-lived access, revocation, and audit are necessary alongside encryption.

## Required controls

### Discovery and remote installation

- Require explicit network scopes and exclusions; use conservative probe rates and concurrency limits.
- Validate targets again at execution time, including resolved addresses, against approved scope. Prevent redirects or DNS changes from escaping scope.
- Bind jobs to a target, approved artifact version, credential reference, policy version, and expiry.
- Creating an enrollment scope explicitly authorizes automatic installation within it using its assigned access. Do not require routine per-device approvals. Provide a global stop control and per-device exclusion.
- Pin or explicitly verify SSH host keys. Do not silently trust changed keys.
- Use verified, versioned artifacts and fixed installation operations. Do not interpolate discovered values into shell commands.
- Use least-privilege SSH accounts and constrained elevation. Show required permissions before enrollment.

### Credentials

- Prefer short-lived SSH certificates or scoped keys where available; support owner-supplied credentials only through the protected enrollment flow.
- Never return stored secret values to the UI, send them to ordinary agents, or put them in logs, process arguments, telemetry, or audit payloads.
- Encrypt each secret using authenticated encryption with a maintained cryptographic library. Keep wrapping keys outside the application database and out of source control.
- Support rotation, deletion, access auditing, and external secret references. Document how backups and key recovery interact.
- Materialize a secret only for an authorized enrollment operation and minimize its lifetime. Avoid promises of perfect memory erasure in managed runtimes.

### Agent identity and transport

- Use TLS with certificate verification and per-agent authentication; target mutual TLS for agent connections.
- Enrollment tokens expire quickly, are single-use, and are bound to an approved enrollment. Store verifiers rather than plaintext tokens.
- Authorize ingestion and commands by agent identity and capability. An agent cannot submit telemetry as another agent.
- Support certificate renewal, revocation, decommissioning, and recovery from expired identities without weakening verification.
- Limit payload sizes, ingestion rates, queues, and metric labels. Validate all agent-supplied fields.

### Owner access

- Never ship default credentials. Make initial owner setup exclusive and time-bounded.
- Support a single authenticated owner initially. Defer administrator, operator, and viewer roles to a later release; every sensitive action remains owner-only.
- Protect browser sessions with secure cookies, CSRF defenses where relevant, expiration, and reauthentication for sensitive changes.
- Provide MFA before enabling stored credentials and remote enrollment in a production release.
- Audit sign-in events, policy edits, secret access, enrollment attempts, exclusions, and identity revocations. Redact sensitive payloads.

### Updates

- Verify signed release metadata and artifacts on the agent independently of the server, using trusted publisher keys.
- Keep release-signing private keys outside the control plane. Define key rotation, revocation, and isolated-site recovery.
- Restrict the privileged updater to verified version transitions; it must not become a general remote command runner.
- Enforce compatibility and downgrade policy, stage replacements atomically, and preserve a verified rollback target.
- Audit release import, version assignment, policy changes, update failures, and rollback. See [the update specification](agent-updates.md).

## Abuse and failure cases to test

| Case                                              | Required outcome                                                |
| ------------------------------------------------- | --------------------------------------------------------------- |
| Compromised agent requests installation elsewhere | Rejected without an authorized enrollment job                   |
| Replayed bootstrap token                          | Rejected after its first successful use or expiry               |
| Device changes SSH host key                       | Enrollment stops pending owner verification                     |
| DNS resolves outside the approved network         | Connection blocked                                              |
| Agent reports another agent's identity            | Rejected and audited                                            |
| Discovery produces duplicate candidates           | No duplicate concurrent enrollment                              |
| Target or policy is revoked after queueing        | Worker rejects job before execution                             |
| Malicious hostname or metric label                | Safely displayed; no command execution or unbounded cardinality |
| Database backup is stolen                         | Secret values remain encrypted; key material is absent          |
| Agent goes offline or submits stale data          | UI shows staleness; no false healthy status                     |

Before enabling remote enrollment or updates against production devices, run
the remaining disposable Linux, provider, VM, and capacity labs. The current
implementation deliberately leaves those acceptance claims open and keeps
live lab scripts opt-in.
