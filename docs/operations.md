# Scout operations

This guide covers the local and production-shaped paths that are implemented
in this repository. Run the live paths only against an owner-authorized
disposable environment. The default fixture scripts do not contact devices,
provider endpoints, or production databases.

For the Spec 2 monitoring behavior, verification evidence, and explicit live
acceptance gates, see the [advanced monitoring release guide](advanced-monitoring.md).

## Local development

Requirements are Node.js 22.14 or newer (24.14.0 was used for the current
verification), npm 11, Go 1.26 or newer, and Docker with either the Compose
plugin or the standalone docker-compose command.

```sh
npm ci
npm run setup
npm run db:up
npm run dev
```

The setup command creates a private, ignored .env only when it does not exist.
The development server uses 127.0.0.1:8080 and the Vite UI uses the printed
local URL. The database volume is named and is preserved by npm run db:down.
Development startup uses in-memory state when no database is available; do
not mistake that mode for a durable deployment.

The safe verification entry points are:

```sh
npm run lint
npm test
scripts/test-first-agent.sh
scripts/test-restore.sh
scripts/test-updates.sh
scripts/test-enrollment.sh
scripts/test-collectors.sh
scripts/test-decommission.sh
scripts/test-load.sh
```

The live-lab environment variables in the enrollment, collector,
decommission, and load scripts print prerequisites after fixture tests. They
do not provision or contact a target. The restore script performs a live
restore only when SCOUT_RESTORE_LAB=1 is set and all required destination and
confirmation variables are present.

For a local UI/API evaluation using the published image, copy
`compose.quickstart.yaml` to a new directory and run:

```sh
curl -fsSL https://raw.githubusercontent.com/doomedramen/scout/main/compose.quickstart.yaml -o compose.yaml
docker compose up -d --pull always
```

This quickstart is bound to localhost, uses development mode without TLS or
agent mTLS, and uses the documented local-only setup token. It is not a
production deployment. Compose automatically reads `.env` beside the Compose
file. Set `SCOUT_PORT=18080` to change the host port. Set
`SCOUT_BIND_ADDRESS=0.0.0.0` only when network access is intentional and a
firewall protects the host; this exposes the no-TLS quickstart on every host
interface. Set `SCOUT_IMAGE` to a pinned GHCR or Docker Hub tag when
reproducing a specific release.

## Production-shaped Compose

The production profile is intentionally explicit. Provision these inputs
outside the repository and mount them read-only:

- a setup token file;
- a 32-byte wrapping-key file with restrictive ownership and permissions;
- a server certificate and private key;
- an agent CA certificate and CA private key;
- a release trust file containing operator-approved publisher public keys.

The production profile requires SCOUT_DB_PASSWORD, SCOUT_ALLOWED_ORIGIN, and
SCOUT_PUBLIC_ORIGIN. The latter is the HTTPS URL target Linux hosts use to
connect back after installation. It enables SCOUT_PRODUCTION, TLS, agent mTLS,
PostgreSQL migrations, named server data storage, and an immutable release
artifact directory. The images run as non-root. The Compose file does not mount
the host root or Docker socket.

After provisioning the secret, TLS, and database inputs, an owner can start
the profile with:

```sh
docker compose --profile production up --pull always -d
```

Use the equivalent docker-compose command if the standalone binary is
installed. The current verification host has Docker 29.8.0 but no Compose
plugin, so a live Compose deployment is not claimed. The production service
pulls `${SCOUT_IMAGE:-ghcr.io/doomedramen/scout:latest}`; pin
`SCOUT_IMAGE` to a release tag or digest for a reproducible deployment.

Complete first-owner setup through the HTTPS UI or API using the provisioned
one-time token. Enable MFA before creating reusable credentials or enabling
remote enrollment. Keep the server behind an owner-controlled network
boundary and configure the TLS certificate for the actual hostname.

## First Linux agent

After owner setup, create a site, then open **Systems → Agent setup**. The
panel creates a five-minute, device-bound invitation. Discovered Linux targets
are normally enrolled by the server-local worker after the owner supplies SSH
access and host trust; the native installer is retained as a recovery path.
The browser does not install a privileged service itself.

The native installer at `scripts/install-agent.sh` downloads the matching
AMD64/ARM64 bootstrap binary from the configured Scout server and verifies its
SHA-256 transfer checksum. It creates the
`scout-agent` system user, writes the server URL to
`/etc/scout/agent.env`, installs `/usr/local/libexec/scout-agent`, copies the
invitation with mode 0400 into `/var/lib/scout/agent`, and enables
`scout-agent.service`. Official server images include both architectures;
source deployments can set `SCOUT_AGENT_BOOTSTRAP_DIR` to a directory with
`scout-agent-linux-amd64` and `scout-agent-linux-arm64`. Use `--artifact PATH`
for a locally built binary when the server has no bootstrap artifact. Do not
put invitations in command arguments, shell history, logs, or repository
files.

The manual native installer remains a recovery path for the first agent or a
host the server cannot reach. Discovered Linux targets use the server-local
enrollment worker after the owner supplies SSH access; no agent image or
separate worker container is required for the normal flow.

## Access and automatic enrollment

Create a site and disabled scope first. Review ranges, exclusions, methods,
ports, rate, concurrency, target budget, credential reference, and trust
reference before enabling it. Enabling a scope is the authorization for
automatic, target-bound enrollment; routine per-device approval is not part
of the model. Exclusions win over sightings and are persistent.

The server starts its site-scoped enrollment worker automatically. Set
`SCOUT_PUBLIC_ORIGIN` to the HTTPS URL that the target Linux host can reach;
the server image contains the amd64/arm64 bootstrap artifacts and signed
release verification remains the update path. The worker uses the owner’s
target-bound SSH credential, checks the stored host fingerprint, uploads a
short-lived one-time invitation, and runs only the fixed agent installation
operation. The SSH account must have root access or non-interactive sudo for
that bounded operation.

The Access form defaults to SSH username/password authentication. Enter the
account used on the target host and its password, or select **Private key** for
key-based SSH hosts. The selected method and username are safe metadata; the
password or private key remains write-only and encrypted.

SSH hosts may publish more than one host key. When an explicit fingerprint is
trusted, the worker tries each supported secure host-key algorithm until that
fingerprint matches; it never accepts a different key or silently trusts a
changed identity.

An explicitly registered `apps/enroller` relay remains available for a later
segmented-network deployment, but it is not needed by the default Compose
stack and is not started automatically.

Pause discovery, enrollment, or updates before changing access or scope
policy. A pause may remain pending while a worker acknowledges its safe
boundary. Changing scope, trust, or credential revisions fences queued work.
The ordinary agent cannot claim worker jobs or redeem enrollment credentials.

## Signed release import and rollback

Build or obtain a Linux artifact, then use the publisher tool described in
scripts/release/README.md. Keep the Ed25519 private key offline. Add only the
approved public key to SCOUT_RELEASE_TRUST_FILE; importing a bundle never
trusts a key included by the bundle.

The Updates view or POST /api/v1/releases/import accepts the signed JSON
bundle. The server verifies the manifest, publisher signature, artifact
digest, size, platform, architecture, and generation before storing an
immutable artifact. Agents independently verify the release through their
trusted keys. Manual, automatic, and pinned rollout records are fenced by
generation and expiry; a paused rollout must be re-evaluated when an agent
reconnects.

The native updater uses two local slots, a crash journal, readiness, and one
bounded rollback. A failed or interrupted VM test is not represented as
accepted until scripts/test-updates.sh is run in the disposable VM lab.

## Backup, restore, and recovery

Backups contain PostgreSQL data and metadata, including encrypted secret
envelopes, but never the wrapping key. Keep the key in a separate protected
recovery location.

```sh
SCOUT_DATABASE_URL=... \
SCOUT_SECRET_KEY_FILE=/protected/scout/wrapping-key \
SCOUT_BACKUP_FILE=/protected/backups/scout.dump \
scripts/backup.sh
```

Restore only to a separate verified database destination. The script checks
the backup metadata fingerprint, refuses the active SCOUT_DATABASE_URL, and
sets restored authority to recovery mode with enrollment and updates paused.

```sh
SCOUT_DATABASE_URL=... \
SCOUT_BACKUP_FILE=/protected/backups/scout.dump \
SCOUT_RESTORE_DATABASE_URL=... \
SCOUT_SECRET_KEY_FILE=/protected/scout/wrapping-key \
SCOUT_RESTORE_CONFIRM=YES \
scripts/restore.sh
```

After restore, inspect owner sessions, agent expiry and revocation, scopes,
credentials, trust records, release trust, and pending jobs. Use the
authenticated recovery reconciliation action only after the owner has
reviewed stale authority. Reconciliation invalidates active work and leaves
enrollment and updates paused until they are intentionally resumed.

Configure retention and the optional row cap through the authenticated
telemetry settings endpoint. The implementation admits telemetry against the
configured disk budget using measured PostgreSQL relation size, exposes usage,
dropped samples, and backpressure in the status and recovery views, and does
not impose an implicit one-million-sample ceiling. Size the budget from a
measured workload; the synthetic load test is not a capacity guarantee.

## Decommissioning

Decommissioning first revokes the device identity, persists its exclusion,
and cancels active enrollment jobs. It retains history. Optional uninstall is
a separate owner-authorized worker operation. Offline or unconfirmed removal
is shown as unconfirmed; the UI never claims that a binary was removed
without the explicit target marker.

Re-enable is explicit and requires an enabled site scope and valid access.
The device returns to candidate state and must pass scope, trust, and
credential checks again. Rediscovery cannot erase the exclusion on its own.

## Not yet accepted

The repository intentionally does not claim native systemd installation,
live second-vantage enrollment, live Docker or Proxmox compatibility,
power-loss VM recovery, a clean-host restore, or 100-device/24-hour capacity.
Those acceptance runs require owner-authorized disposable infrastructure and
are tracked in specs/001-scout-platform/evidence.md.
