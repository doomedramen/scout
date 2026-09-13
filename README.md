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
printf 'SCOUT_PORT=8080\n' > .env
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
`SCOUT_PUBLIC_URL` is optional: when it is unset, Scout advertises the private
address on its default route. Set it to the HTTPS URL or reachable LAN URL that
installed agents will use to call Scout when using a reverse proxy or a fixed
hostname. Do not use `127.0.0.1` unless every target host is the Scout host
itself; remote hosts cannot reach the Scout host through their own loopback.

## Install the host helper

The optional `scout` helper keeps the Compose definition current as well as the
image. Install it on the Linux host that runs Docker:

```bash
curl -fsSL https://raw.githubusercontent.com/doomedramen/scout/main/packaging/host/scout \
  -o /usr/local/bin/scout
chmod 0755 /usr/local/bin/scout
```

It uses `/opt/scout` by default. Set `SCOUT_DIR` when the instance lives
elsewhere.

## Operations

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=200 scout
```

With the host helper:

```bash
scout status
scout logs --tail=200 scout
scout update
scout backup /safe/location/scout-backup.scoutbak
scout restore /safe/location/scout-backup.scoutbak
```

`scout update` downloads and validates the current Compose definition before
pulling the image. Backups are passphrase-encrypted and include SQLite plus the
persistent authentication, credential, and control-signing keys. Restore keeps
the previous data files and pauses discovery and installation until the owner
reviews the restored instance in Settings.

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
npm run build
```

The Rust workspace contains the native `scout-agent`. The server image embeds
the signed release artifacts; there is intentionally no separate agent image.
The production build prepares the local Linux amd64 artifact automatically;
`npm run agent:build` remains available when only the local artifact is needed.
