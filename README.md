# Scout

Scout is a self-hosted, single-owner network monitor. It discovers reachable
Linux and macOS systems, installs one native agent per system over verified SSH,
and reports telemetry from each host.

## Quick start

On a Linux host with Docker Compose:

```bash
mkdir -p /opt/scout
cd /opt/scout
curl -fsSL https://raw.githubusercontent.com/doomedramen/scout/main/compose.yaml -o compose.yaml
printf 'SCOUT_PORT=8080\nSCOUT_PUBLIC_URL=http://127.0.0.1:8080\n' > .env
docker compose up -d
docker compose logs -f scout
```

Open `http://HOST:8080`. The first visit asks for the one-time setup token,
which is printed in the Scout container logs, followed by the owner username
and password. Later visits go directly to sign-in.

The Compose file uses host networking so Scout can inspect and scan its directly
connected private network. It stores SQLite, keys, and agent artifacts in the
`scout-data` volume. Scout does not need a PostgreSQL service or a separate
agent container.

## Use another port

Only one value needs to change:

```bash
cd /opt/scout
printf 'SCOUT_PORT=18081\nSCOUT_PUBLIC_URL=http://192.0.2.10:18081\n' > .env
docker compose up -d
```

`SCOUT_PORT` controls the Next.js listener and the host-network endpoint.
`SCOUT_PUBLIC_URL` is optional, but should be set to the HTTPS URL or reachable
LAN URL that installed agents will use to call Scout.

## Operations

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=200 scout
```

Keep the data volume and the persistent keys together when backing up or moving
an instance. Do not reuse a production data directory for tests. HTTPS through
a reverse proxy is recommended before exposing Scout beyond a trusted LAN.

## Development

Requirements: Node 24, npm, Rust 1.96, and Docker for packaged checks.

```bash
npm install
npm run dev
npm run format:check
npm run lint
npm run typecheck
npm test -- --run
cargo test --workspace
```

The Rust workspace contains the native `scout-agent`. The server image embeds
the signed release artifacts; there is intentionally no separate agent image.
