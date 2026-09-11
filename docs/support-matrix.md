# Support and verification matrix

This is an evidence ledger, not a promise of compatibility with every
distribution or provider version. A row is marked verified only when the
repository has run the named fixture or an owner-authorized live lab.

| Component | Version / platform | Status | Evidence and limits |
| --- | --- | --- | --- |
| Control and shared Go code | Go 1.27.1, Darwin arm64 | Fixture verified | go test ./... and go vet paths pass on 2026-09-11. Linux deployment is still a separate acceptance run. |
| Web application | Node.js v24.14.0, npm 11.19.0 | Build verified | npm ci, npm run check, npm run build, and browser fixture tests pass. |
| Database image | postgres:17-alpine | Integration fixture verified | Disposable PostgreSQL integration script passed when a compatible local container was available. |
| Docker engine | Docker 29.8.0 on Darwin arm64 | Runtime present | The host has Docker, but no Compose plugin. No live provider collector was run. |
| Docker Compose | Not installed on verification host | Not verified | The production profile is checked in; install Compose before clean-host deployment. |
| Linux agent | linux/amd64 and linux/arm64 build targets | Cross-build path verified | Builds are configured; native systemd install, restart, renewal, and revocation remain open. |
| Linux systemd | No owner-authorized disposable host used | Open | scripts/test-first-agent.sh fixture covers persistence and reporting only. |
| Docker collector | Fixture adapter and denied/malformed responses | Fixture verified | Entity limits, bounded reads, and redaction are covered. Live Engine version/ACL compatibility is open. |
| Proxmox VE collector | HTTPS/TLS fixture boundary | Fixture verified | Typed resource translation and TLS/fingerprint configuration are covered. Live PVE version/ACL compatibility is open. |
| Third-party collector | Fake provider fixture | Fixture verified | Registry, deadline, panic/error containment, and bounded scheduling are covered. |
| Release updater | Ed25519, Linux artifact fixtures | Fixture verified | Signature, digest, platform, generation, resume, journal, readiness, and one rollback are covered. |
| Updater power loss | Disposable Linux VM required | Open | No process termination or power-cut lab was run. |
| Automatic enrollment | In-memory, scope-bound fixtures | Boundary verified | Duplicate sightings, exclusions, grants, leases, and revision fencing are covered. Live second-vantage placement is open. |
| 100-device / 24-hour capacity | Synthetic 100-device, 24-hour sample shape | Synthetic only | The test ingests 2,400 fixture samples and exercises bounded querying; it is not a live capacity or retention-sizing claim. |

## Required permissions

- The control server needs its PostgreSQL connection, server TLS key,
  agent-CA key, setup token, wrapping key, and release trust file. Private
  inputs are provisioned outside the image and mounted with restrictive
  permissions.
- The ordinary agent needs its own data directory and outbound access to the
  configured Scout server. It does not receive enrollment credentials or
  worker capabilities.
- An enroller worker needs a site-scoped worker token, a verified agent
  artifact, a known_hosts file, and an owner-approved SSH account with only
  the bounded installation permissions documented by the target environment.
- A Docker collector may need access to a local Docker endpoint. Socket access
  is administration authority even when the mount is read-only; a restricted
  proxy is preferred.
- A Proxmox collector needs an HTTPS endpoint, validated CA or fingerprint,
  and a dedicated read-scoped API token. It must not be granted guest
  installation privileges merely to collect status.

## Compatibility and recovery

Release manifests bind platform, architecture, artifact digest, size, and
protocol range. Agents reject unknown keys, altered artifacts, incompatible
versions, and unauthorized downgrade generations. Trust-key changes require
an explicit operator transition or local recovery operation.

Backups contain encrypted secret envelopes but not the wrapping key. Restore
requires the matching protected key and a separate destination. Restored
authority enters recovery mode and pauses enrollment and updates until the
owner reviews revocations, credentials, trust, and policy.
