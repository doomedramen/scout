# Scout

Scout is a self-hosted server, network, and device monitor. Its planned distributed agents collect metrics, contribute observations to a shared network map, and automatically expand monitoring within owner-configured scopes using supplied access. One agent runs on each device. Machines needing access return to the owner.

## Product direction

- See infrastructure health and network relationships in one interface.
- Grow monitoring coverage through policy-controlled discovery and enrollment.
- Report inaccessible machines with an actionable access request.
- Run on infrastructure you own, starting with Docker Compose and a Linux VM suitable for Proxmox VE.
- Treat credentials, agent identity, and installation permissions as core product concerns.
- Keep agents current through automatic or owner-triggered, signed updates delivered by the control server, including on networks without internet access.
- Extend host monitoring through service collectors for hypervisors, container runtimes, and other tools. Docker and Proxmox are initial examples, not an exhaustive list.

The owner configures allowed networks and credentials once. Enrollment proceeds automatically within those boundaries without routine per-device approvals. Newly enrolled agents contribute further discovery observations.

## Specification

The [Scout platform specification](specs/001-scout-platform/spec.md) is the source of truth for target behavior. Start implementation from the [agent handoff](specs/001-scout-platform/handoff.md), which links the constitution, technical plan, contracts, 64 ordered tasks, and acceptance coverage. See the [Spec Kit workflow](specs/README.md) for tooling. Performance and retention defaults are proposed targets, not claims about this scaffold.

## Development setup

Requires Node.js 22.14+ or 24+, npm 11, Go 1.26+, and Docker with Compose for the optional local database. The repository uses npm workspaces and Turborepo:

```text
apps/web       React + TypeScript + Vite + shadcn/ui and charts
apps/server    Go control API
apps/agent     Go local host collector
internal       Shared Go packages
scripts        Local development setup
docs           Product, architecture, and security decisions
```

## Docker quick start (copy/paste)

For a local, localhost-only evaluation, use the published multi-architecture
server image. It includes the Scout API and web UI, starts PostgreSQL, and
persists its database and development encryption key in named volumes. This
quickstart intentionally runs without TLS or agent mTLS and must not be
exposed to the internet.

The shortest setup is:

```sh
mkdir scout && cd scout
curl -fsSL https://raw.githubusercontent.com/doomedramen/scout/main/compose.quickstart.yaml -o compose.yaml
docker compose up -d --pull always
```

Compose automatically reads a `.env` file beside `compose.yaml`. To use port
8041 and make Scout reachable from other machines on your network, create:

```dotenv
SCOUT_PORT=8041
SCOUT_BIND_ADDRESS=0.0.0.0
```

Then run `docker compose up -d --pull always` and open `http://<server-address>:8041`.
Binding to `0.0.0.0` exposes this no-TLS quickstart on every host interface;
use it only on a trusted network protected by a firewall. Omit
`SCOUT_BIND_ADDRESS` to keep the safer localhost-only default.

If the server will install agents on other hosts, also set
`SCOUT_PUBLIC_ORIGIN` to the URL those hosts can reach, for example
`SCOUT_PUBLIC_ORIGIN=http://192.168.1.242:8041`. The internal listener remains
`0.0.0.0:8080`; changing `SCOUT_PORT` changes only the host-side port mapping.

The equivalent Compose file, for direct copy/paste, is:

```yaml
name: scout

services:
  postgres:
    image: postgres:17-alpine
    restart: unless-stopped
    stop_grace_period: 1m
    environment:
      POSTGRES_USER: scout
      POSTGRES_DB: scout
      POSTGRES_PASSWORD: ${SCOUT_DB_PASSWORD:-scout-local-only}
    volumes:
      - scout-postgres:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U scout -d scout"]
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 10s

  server:
    image: ${SCOUT_IMAGE:-ghcr.io/doomedramen/scout:latest}
    pull_policy: always
    restart: unless-stopped
    stop_grace_period: 10s
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      SCOUT_PRODUCTION: "false"
      SCOUT_LISTEN: "0.0.0.0:8080"
      SCOUT_DATABASE_URL: postgres://scout:${SCOUT_DB_PASSWORD:-scout-local-only}@postgres:5432/scout?sslmode=disable
      SCOUT_SETUP_TOKEN: ${SCOUT_SETUP_TOKEN:-local-only-change-me}
      SCOUT_SECRET_KEY_FILE: /var/lib/scout/wrapping-key
      SCOUT_WEB_DIR: /usr/local/share/scout/web
      SCOUT_PUBLIC_ORIGIN: ${SCOUT_PUBLIC_ORIGIN:-http://127.0.0.1:${SCOUT_PORT:-8080}}
      SCOUT_AUTO_ENROLLMENT: ${SCOUT_AUTO_ENROLLMENT:-true}
    ports:
      - "${SCOUT_BIND_ADDRESS:-127.0.0.1}:${SCOUT_PORT:-8080}:8080"
    volumes:
      - scout-server-data:/var/lib/scout
    read_only: true
    tmpfs:
      - /tmp:size=16m,mode=1777
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL

volumes:
  scout-postgres:
  scout-server-data:
```

Open `http://localhost:8080` (or `http://localhost:$SCOUT_PORT` when you set a
custom port), use setup token `local-only-change-me`, and choose an owner
password. Both services restart after failures and host reboots. The server
container uses a read-only root filesystem, a small temporary filesystem, no
Linux capabilities, and no-new-privileges. Stop it with `docker compose down`;
named volumes are retained. The container listens on port 8080 internally, so
a host port conflict only requires setting one variable:

```sh
SCOUT_PORT=18080 docker compose up -d --pull always
```

The default image is published at
[GitHub Container Registry](https://github.com/doomedramen/scout/pkgs/container/scout).
If the package is private, run `docker login ghcr.io` first. Use the
production-shaped [Compose file](compose.yaml) and [operations guide](docs/operations.md)
for TLS, agent mTLS, separately provisioned keys, and owner-authorized
deployment.

The image workflow publishes only `scout` (server plus UI and the native
Linux agent artifacts it serves) for AMD64 and ARM64 on pushes to `main` and
version tags. To mirror it to Docker Hub as well, configure repository secrets
`DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN`; the workflow then publishes
`docker.io/<username>/scout`.

## Proxmox VE helper script

Run this command on a Proxmox VE host:

```sh
var_os='debian' bash -c "$(curl -fsSL https://raw.githubusercontent.com/doomedramen/scout/main/ct/scout.sh)"
```

The public entrypoint is the Community Scripts-compatible LXC definition in
[`ct/scout.sh`](ct/scout.sh). Internal container installation logic and metadata
live in [`install/scout-install.sh`](install/scout-install.sh) and
[`json/scout.json`](json/scout.json).

The helper creates an unprivileged Debian 13 LXC, installs Docker, and starts
the Scout quickstart stack with generated database and setup credentials. Run
`update` inside the resulting LXC to pull the latest Scout and PostgreSQL
images and recreate the services while retaining both named data volumes.

This integration has syntax and metadata contract coverage, but still needs a
live Proxmox VE installation test before it can be described as verified.

```sh
npm ci
npm run setup
npm run db:up
npm run dev
```

Open the Vite URL printed in the terminal (normally `http://127.0.0.1:5173`). The API binds to `127.0.0.1:8080`. Setup creates a private, ignored `.env` with a random local database password; existing files are never overwritten. The development runner loads it automatically. Both the Compose plugin and standalone `docker-compose` are supported.

Run the browser regression journey against an isolated in-memory server with:

```bash
npx playwright install chromium   # first run only
npm run test:e2e:browser
```

The test creates a disposable owner, site, scope, and agent invitation. Set
`SCOUT_E2E_URL` and `SCOUT_E2E_SETUP_TOKEN` to point it at a separately managed
development server; never point it at production.

The UI reports its actual API and database connection state. An empty workspace
shows real loading, empty, error, and access states; it does not fabricate
devices or topology. The `+` action in the top navigation opens the manual
agent recovery flow.

```sh
npm run check
npm run build
npm run lint
npm test
npm run snapshot -w @scout/agent
```

`npm run lint` checks Go formatting and vet, Prettier formatting, and the
TypeScript workspace checks. Run `npm run format` to rewrite the supported
source and configuration files, or `npm run format:check` for a read-only
CI-style check.

The snapshot command prints this machine's hostname, OS, architecture, CPU count, and active non-loopback interface addresses. It does not enroll, upload metrics, discover remote devices, or install software. It is a collector scaffold, not a running monitoring daemon.

Build a Linux agent from any supported development host:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o apps/agent/dist/scout-agent-linux-amd64 ./apps/agent
```

Use `GOARCH=arm64` for Linux ARM64. Builds should be tested on their target OS before release. Stop the local database with `npm run db:down`; its named volume is preserved. The published container packages the control server, web UI, and both Linux bootstrap agents; source development still uses the Vite server.

## Install the first Linux agent

Use **+ (Add agent manually)** in the top navigation after creating a site.
Scout creates a device-bound invitation that expires after five minutes, then
shows a copyable installation recipe. The browser does not install a
privileged service by itself.

The native recipe is a single-command recovery path for the first agent or a
host the server cannot reach. Run it on the Linux host Scout should monitor,
replacing the invitation placeholder with the one-time value shown in the
setup panel:

```sh
SCOUT_OTI='paste-the-one-time-invitation-here' bash -c "$(curl -fsSL 'https://scout.example.test/api/v1/bootstrap/agent/install.sh')"
```

The downloaded installer checks that it is running on Linux, checks `sudo` and
the required host tools, downloads the matching AMD64 or ARM64 binary, verifies
its SHA-256 transfer checksum, creates the unprivileged `scout-agent` system
user, installs a hardened systemd unit, and starts it. It invokes `sudo`
internally only for the privileged operations; do not prepend `sudo` to the
copyable command. The server embeds its request origin into the downloaded
installer, so the command does not need a second server URL argument.

Because this compact form includes the one-time invitation in the command text,
avoid saving it in shell history on shared hosts. The lower-level
`scripts/install-agent.sh --server ... --invitation-file ...` form remains
available for environments that need to handle the invitation through a file.

For direct script usage, the `--server` URL must be reachable from the Linux
host over HTTPS in production. Official server images contain both bootstrap
architectures under `/usr/local/share/scout/agent`; source deployments can set
`SCOUT_AGENT_BOOTSTRAP_DIR` to a directory containing
`scout-agent-linux-amd64` and `scout-agent-linux-arm64`, or pass a locally built
binary with `--artifact`. The one-time invitation is copied into the agent
data directory, consumed during first enrollment, and then removed. The
bootstrap checksum detects transfer corruption; signed release metadata still
governs subsequent agent updates.

The setup panel keeps the manual native installer as a recovery path. Normal
discovered-device enrollment is performed by the server-local worker using
owner-supplied SSH access; no separate agent image or worker container is
needed.

This repository now contains the runnable implementation slices described by
the handoff. Fixture and local integration coverage exists for owner
authentication, durable telemetry, signed updates, scoped discovery/enrollment
boundaries, service adapters, and decommissioning. Native systemd, live
second-vantage enrollment, live Docker/Proxmox compatibility, 24-hour capacity,
and power-loss VM acceptance remain explicitly unverified. Do not expose the
development API publicly, use production credentials, or point an enroller at
real devices without an owner-authorized lab.

## First usable milestone

One control server and one Linux agent, with live host metrics, health history,
an inventory, and a network map that distinguishes observed relationships from
inferred ones. After the owner defines a bounded scope and supplies target-
bound SSH access plus host trust, the server installs agents on eligible Linux
devices automatically.

The Linux-first MVP is the selected direction. It includes server-delivered agent updates before expanding into scoped discovery, access requests, and audited SSH enrollment. The sequence is in [the roadmap](docs/roadmap.md).

## Design documents

- [Architecture](docs/architecture.md)
- [Confirmed decisions](docs/decisions.md)
- [Security model](docs/security.md)
- [UI direction](docs/interface.md)
- [Agent updates](docs/agent-updates.md)
- [Service-aware collectors](docs/service-collectors.md)
- [Operations runbook](docs/operations.md)
- [Advanced monitoring release guide](docs/advanced-monitoring.md)
- [Support and verification matrix](docs/support-matrix.md)
- [Roadmap and acceptance criteria](docs/roadmap.md)

## Status

Runnable Linux-first monitoring platform implementation with a single-owner
control API, authenticated agent telemetry, bounded automatic enrollment
boundaries, signed offline-capable update handling, extensible collector
adapters, recovery controls, and a shadcn-based UI. The remaining acceptance
gaps are tracked in [implementation evidence](specs/001-scout-platform/evidence.md)
and are not silently treated as production compatibility claims.
