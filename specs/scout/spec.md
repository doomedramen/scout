# Scout v1 Product Specification

## Product promise

Scout is a self-hosted, single-owner network monitor. After the owner creates an
account and signs in, Scout automatically finds reachable systems with a
supported agent installation method, shows those systems alongside monitored
systems, and installs an agent from the system detail page after the owner
provides access. The owner never needs to paste a target-host installation
command for the normal flow.

The first release supports a Linux Scout server and native Linux and macOS
agents. Agents report telemetry only for their own host. An enrolled agent may
perform a bounded network scan only when Scout assigns and signs that task; the
result is access-method evidence, not remote host telemetry.

## User journey

1. The owner runs the documented Docker Compose deployment on a Linux host.
2. A fresh instance shows owner setup. The owner supplies the one-time local
   setup token, username, and password. Passwords require at least eight
   characters, one uppercase letter, one lowercase letter, and one number.
3. After setup, Scout starts discovery and opens the Systems page. Later visits
   default to sign-in rather than setup. The owner sees monitored systems,
   potential systems, discovery state, and only currently actionable Needs
   access items.
4. The owner opens a potential system. Scout displays its address, supporting
   identity evidence, the detected access method, and the observed SSH host
   fingerprint. The page asks for an SSH username and password by default and
   offers private-key authentication when selected.
5. The owner confirms a first-seen fingerprint and submits credentials once.
   Scout encrypts the credential, queues installation, and returns quickly with
   visible progress. SSH authentication, privilege checks, installation, and
   agent enrollment happen asynchronously.
6. Scout serves the matching native agent, installs the correct Linux systemd
   or macOS launchd service, and enrolls exactly one active agent identity for
   the system. The same system page changes to Online when real telemetry
   arrives.
7. Manual installation remains available behind the top navigation `+` as a
   fallback. It is not part of the normal onboarding path.

## Scope and navigation

The signed-in application contains only these primary routes:

- `/systems` — default fleet and discovery view.
- `/systems/[systemId]` — one system's identity, access, installation,
  telemetry, history, and lifecycle actions.
- `/network` — bounded network policy, exclusions, and scan status.
- `/settings` — owner, optional MFA, transport, and operational settings.

Unauthenticated routes are `/setup` and `/sign-in`. Access requests, trust
records, enrollment jobs, scopes, hardware, storage, services, incidents, and
updates are system detail sections or settings sections, not primary pages.

The UI uses standard shadcn components with the default Tailwind tokens and
variants. It contains no demo systems, demo metrics, unlabeled fixtures, or
custom component styling. Loading, empty, success, warning, and error states
must state what happened and the next useful action. Every mutation control
must clear its pending state after success, failure, or a bounded timeout.

## Deployment and authentication

- The server is one standard self-hosted Next.js App Router application running
  in one Linux container. It includes the UI, Route Handlers, control-plane
  scheduler, SSH orchestration, agent artifacts, and durable jobs.
- The server uses a local SQLite database through Drizzle and the stable
  `better-sqlite3` adapter. SQLite runs with WAL, foreign keys, a busy timeout,
  `synchronous=NORMAL`, serialized bounded writes, short transactions, and
  incremental retention work. No PostgreSQL service is required.
- SQLite data and persistent keys live under `/data`. Missing keys fail closed;
  Scout never silently regenerates keys for existing encrypted data.
- Better Auth provides the singleton owner session. Generic public signup is
  disabled. The setup token is hashed, expires after thirty minutes, and is
  consumed only when the owner account is successfully created.
- TOTP is optional in Settings and is never required unless the owner enables
  it.
- The Compose deployment uses Linux host networking so the server can inspect
  its own network interfaces and scan its directly connected subnet. A single
  `SCOUT_PORT` value controls the externally reachable listener. `SCOUT_PUBLIC_URL`
  may override the callback origin used by installed agents.
- HTTP is supported for a trusted LAN with a persistent warning. HTTPS behind a
  reverse proxy is documented and required for unattended manual bootstrap.

## Discovery

Discovery starts after owner setup, never before authorization exists.

Scout derives the private IPv4 CIDR attached to the default route. Prefixes of
`/24` or narrower are honoured; broader networks are capped to their containing
`/24`. Public, loopback, point-to-point, VPN-only, container-only, ambiguous,
or missing route information causes an inline bounded-range request instead of
guessing.

The initial access registry implements SSH on TCP port 22. Discovery performs
bounded TCP reachability checks only: at most 256 addresses, 64 concurrent
connections, and an 800 ms connection timeout. It does not guess passwords,
run arbitrary remote commands, exploit services, or perform vulnerability
scans.

A `NetworkSegment` includes observation provenance, not only CIDR text. Equal
CIDRs from disconnected networks remain separate. The Scout server scans its
own directly connected segment until a healthy agent is available there. For
every other segment, and for the server segment once an agent exists, Scout
elects exactly one healthy enrolled agent by freshest heartbeat and stable agent
ID. If no healthy agent remains on the server segment, server scanning resumes.

Agents heartbeat every fifteen seconds and become unavailable after forty-five
seconds. The first eligible vantage scans immediately; later scans run every
ten minutes with up to sixty seconds of jitter. Scan assignments have a signed
policy version, generation, deadline, and two-minute lease. Agents apply policy
changes on the next authenticated heartbeat and stop by the sixty-second hard
deadline while disconnected. Late or superseded results are rejected.

Open access evidence is refreshed on each scan. Closed results remove current
eligibility immediately, and evidence older than thirty minutes expires. Only
unique, non-excluded, non-enrolled systems with current open supported access
evidence appear as potential systems or count as Needs access. Closed,
filtered, unreachable, and unsupported outcomes belong in diagnostics only.

IP addresses, MAC addresses, hostnames, and SSH fingerprints are supporting
evidence. They must not independently merge devices. Repeated observations of
the same endpoint and unchanged evidence update one candidate across restarts.
Address reuse or conflicting host identity quarantines the old association.
The durable enrolled agent identity is authoritative, with one active agent
allowed per system.

## Access and enrollment

SSH is an extensible access-method adapter. The adapter owns discovery,
credential schema, fingerprint trust, preflight, installation, and uninstall.
Future methods must use the same System and enrollment model.

Credentials are exact-host scoped by default. The owner can explicitly grant a
credential to a bounded subnet. A separate acknowledgement permits unattended
enrollment and first-seen fingerprint pinning inside that subnet. Changed
fingerprints always block.

Credential values are write-only, encrypted with AES-256-GCM, excluded from
responses, logs, audit events, scan tasks, and ordinary agents, and retained
only as long as the owner grant requires. The browser clears secret fields after
acceptance.

The access submission includes a browser idempotency key. Scout atomically
stores the encrypted grant and creates or reuses one active enrollment job,
returning `202 Accepted` and a job URL. A lost response can be recovered by
retrying with the same key without re-entering the secret. Jobs persist stages:

`queued → connecting → verifying identity → authenticating → checking privilege
→ installing → awaiting agent → complete`

Failures identify a targeted blocker such as invalid credentials, missing
privilege, changed identity, unsupported architecture, unavailable callback,
or bounded retry exhaustion. Workers use leases and re-check owner authority
before each privileged action. The target installer uses an atomic lock and is
idempotent.

Direct targets use the server's SSH client. If only an enrolled scanner can
reach a target, Scout uses a signed, expiring relay restricted to that target
address and SSH port. Two authenticated HTTP streams carry raw SSH ciphertext;
the relay never receives reusable credentials or plaintext. The channel-open
signature binds agent, relay, target, direction, nonce, and expiry. The relay
supports credential-free SSH fingerprint preflight before the access form is
enabled.

The server resolves the agent callback from explicit `SCOUT_PUBLIC_URL`, or its
default-route private address and `SCOUT_PORT`. Before installation, the
transferred agent performs an outbound callback preflight. If it cannot reach
Scout, installation stops with a precise explanation and the encrypted grant
remains recoverable.

The installer itself checks operating system, architecture, root/admin access,
sudo, and service manager. Root skips sudo; otherwise the SSH password is reused
for sudo by default, with an optional separate privilege password.

Linux uses systemd. macOS uses launchd. Both retain a durable agent identity,
versioned binaries, configuration, and a stable update launcher.

Automatic installation transfers the installer, invitation, callback URL, trust
pins, and signed artifact over the verified SSH connection. Manual HTTPS
fallback uses a one-line OTI command with the server URL embedded. Manual HTTP
fallback additionally requires an out-of-band bootstrap fingerprint from
`scout trust-pin`.

Invitations expire after ten minutes and are bound to one system. Artifact
download does not consume the invitation. Enrollment binds it to the agent's
Ed25519 public key. If the enrollment response is lost after commit, proof of
that same key returns the existing identity; a different key is rejected.

## Agent protocol and monitoring

Every agent has a durable, independently revocable Ed25519 identity.

Agent requests sign the HTTP method, path, timestamp, request ID, and body
hash. The server enforces two-minute clock skew, persistent replay deduplication,
payload limits, rate limits, and revocation.

Server task envelopes are signed too. They bind the intended agent, task ID,
task kind, policy generation or release sequence, payload digest, issue time,
validity window, and deadline. Agents reject unsigned, replayed, stale,
expired, wrong-agent, or out-of-scope tasks.

The agent reports CPU, memory, filesystems, uptime, and network interfaces for
its own host immediately and every thirty seconds. Linux and macOS use native
platform adapters behind a common Rust collector interface. Unsupported,
missing, stale, and zero values remain distinct.

Agents buffer at most twenty-four hours or 32 MiB of telemetry while Scout is
unavailable, whichever comes first, and report dropped data. Scout retains raw
samples for twenty-four hours and five-minute rollups for ninety days.

System states are `needs-access`, `needs-trust`, `installing`, `online`,
`stale`, `offline`, `blocked`, `revoked`, and `excluded`. A heartbeat is online
for up to forty-five seconds; telemetry that stops arriving while heartbeat
continues becomes stale rather than fabricated or silently zero.

The initial release has a dormant `ServiceCollector` extension point but no
Docker, Proxmox, or provider-specific collector UI.

## Updates, backup, restore, and decommissioning

CI builds native agent artifacts for Linux amd64/arm64 and macOS Intel/Apple
Silicon. A protected publisher Ed25519 key, never present in Scout, signs a
manifest containing platform, architecture, compatibility, version, digest,
and monotonically increasing release sequence.

Agents receive artifacts through Scout, verify publisher signatures, digest,
compatibility, and anti-rollback sequence, then atomically activate a versioned
binary. A stable launcher rolls back only to the previous locally verified
version if health fails.

Restore invalidates invitations and executable leases/jobs. Before authority
resumes, agents reconcile local task IDs/generations and release watermarks;
the server advances its counters and creates fresh work only after
reconciliation. Offline agents receive no new work until they reconcile.

Decommissioning revokes the agent identity first, then attempts uninstall using
authorized SSH. If SSH is unavailable, Scout reports `revoked; uninstall
pending/manual` and never claims removal.

`scout update` enters maintenance, creates a consistent encrypted pre-update
snapshot of SQLite and keys, downloads and validates the matching Compose
definition and immutable image digest, runs migrations and health checks, then
resumes authority. A failed update restores the previous definition, image, and
database snapshot when schema compatibility requires it.

`scout backup` creates a passphrase-encrypted authenticated archive containing a
consistent database snapshot and required keys. `scout restore` validates it in
a clean data directory and starts discovery, enrollment, and updates paused
until owner review.

## Public HTTP contracts

Owner Route Handlers:

- `GET /api/v1/setup`
- `POST /api/v1/setup/owner`
- `GET /api/v1/systems`
- `GET /api/v1/systems/{systemId}`
- `GET /api/v1/systems/{systemId}/metrics`
- `POST /api/v1/systems/{systemId}/access-grants`
- `GET /api/v1/enrollment-jobs/{jobId}`
- `POST /api/v1/systems/{systemId}/actions/{retry|decommission}`
- `GET|PUT /api/v1/network-policy`
- `GET|PUT /api/v1/settings`
- `GET /api/health/{live|ready}`

Owner mutations require a valid owner session, same-origin/CSRF protection,
validation, and idempotency where retryable. Owner setup uses the setup token
instead of an owner session.

Agent v1 endpoints:

- `POST /api/agent/v1/enroll`
- Signed `POST /api/agent/v1/heartbeat`
- Signed `POST /api/agent/v1/telemetry`
- Signed `POST /api/agent/v1/tasks/{taskId}/result`
- Signed paired relay stream endpoints
- Signed release manifest and artifact downloads
- Invitation-authorized bootstrap installer and artifact downloads

TypeScript Zod schemas and Rust serde types share checked-in valid and invalid
JSON fixtures. The Rust seams are `HostCollector`, `NetworkScanner`,
`ServiceManager`, `Installer`, `UpdatePlatform`, and `ServiceCollector`.

## Acceptance criteria

- Clean installation reaches owner setup in under ten minutes; later visits
  default to sign-in.
- A reachable SSH host appears once within sixty seconds after setup. Three
  Scout restarts do not duplicate it. Closed hosts never count as Needs access.
- A first-time owner completes system selection, fingerprint confirmation,
  credential entry, and one submit action without a target-host command.
- Valid disposable Linux and macOS targets install and enroll automatically.
- Invalid credentials, insufficient privilege, changed fingerprint, callback
  failure, worker failure, and request timeout restore an actionable UI state
  without exposing secrets.
- First telemetry makes the same system Online within five seconds. Current,
  stale, offline, recovery, retention, and drop states are truthful.
- Scanner assignment is one-per-segment and failover rejects stale results.
- Tampered, unsigned, incompatible, and stale agent releases change nothing;
  valid offline updates and health-triggered rollback work.
- Alternate port, Compose-definition update, backup/restore, revocation,
  responsive viewports, keyboard navigation, and packaged production-image
  journeys pass.
- Formatter, linter, type checks, Rust checks, unit/integration tests,
  Playwright, QEMU, macOS, packaging, and Lefthook checks are green in CI.
- The README contains verified copy/paste deployment, alternate-port, update,
  backup, and recovery commands.

## Development constraints

- Use strict red-green-refactor TDD at the agreed browser, HTTP protocol,
  platform-adapter, and SQLite migration seams.
- Use ESLint flat config, Prettier, `cargo fmt`, Clippy, shfmt, ShellCheck,
  actionlint, hadolint, and Lefthook.
- Use disposable QEMU/Linux and GitHub-hosted macOS test environments only.
- Never deploy to real devices or use production credentials or production
  signing keys during implementation.
- Commit each green vertical slice and push when a remote exists. Never add
  co-author trailers.
