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

```sh
npm ci
npm run setup
npm run db:up
npm run dev
```

Open the Vite URL printed in the terminal (normally `http://127.0.0.1:5173`). The API binds to `127.0.0.1:8080`. Setup creates a private, ignored `.env` with a random local database password; existing files are never overwritten. The development runner loads it automatically. Both the Compose plugin and standalone `docker-compose` are supported.

The UI also runs without a database and reports its actual connection state. Use **Explore demo** to inspect illustrative systems, filtering, host charts, and network membership. Leaving demo clears those fixtures. No network devices are contacted by the UI.

```sh
npm run check
npm run build
npm test
npm run snapshot -w @scout/agent
```

The snapshot command prints this machine's hostname, OS, architecture, CPU count, and active non-loopback interface addresses. It does not enroll, upload metrics, discover remote devices, or install software. It is a collector scaffold, not a running monitoring daemon.

Build a Linux agent from any supported development host:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o apps/agent/dist/scout-agent-linux-amd64 ./apps/agent
```

Use `GOARCH=arm64` for Linux ARM64. Builds should be tested on their target OS before release. Stop the local database with `npm run db:down`; its named volume is preserved. The Compose file currently runs PostgreSQL only; full server packaging is pending.

This is a local development scaffold. Owner authentication, stored inventory, ingestion, remote enrollment, service adapters, and signed updates are not implemented. Do not expose the development API publicly or provide SSH credentials to it.

## First usable milestone

One control server and one manually enrolled Linux agent, with live host metrics, health history, an inventory, and a network map that distinguishes observed relationships from inferred ones. This milestone does not collect SSH credentials or install agents remotely.

The Linux-first MVP is the selected direction. It includes server-delivered agent updates before expanding into scoped discovery, access requests, and audited SSH enrollment. The sequence is in [the roadmap](docs/roadmap.md).

## Design documents

- [Architecture](docs/architecture.md)
- [Confirmed decisions](docs/decisions.md)
- [Security model](docs/security.md)
- [UI direction](docs/interface.md)
- [Agent updates](docs/agent-updates.md)
- [Service-aware collectors](docs/service-collectors.md)
- [Roadmap and acceptance criteria](docs/roadmap.md)

## Status

Runnable monorepo foundation with a reference-guided UI preview, shadcn charts, a development health API that checks PostgreSQL, and a local host collector. The product roadmap describes the remaining work; this scaffold is not the completed Linux MVP.
