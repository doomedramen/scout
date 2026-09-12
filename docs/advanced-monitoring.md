# Advanced monitoring release guide

This guide describes the monitoring and alerting behavior implemented in Spec
002, the checks that have actually run, and the gates that are still open.
It is deliberately an evidence guide rather than a blanket compatibility
promise. The authoritative task ledger is the [Spec 002 evidence
ledger](../specs/002-advanced-monitoring/evidence.md); the requirement-by-
requirement view is the [acceptance matrix](../specs/002-advanced-monitoring/acceptance-matrix.md).

## Scope

The release slice contains:

- durable incidents with strict thresholds, hysteresis, acknowledgment, gaps,
  evidence state, and append-only transitions;
- owner-enabled ntfy delivery with encrypted destinations, bounded retries,
  quiet windows, suppression summaries, and restore fencing;
- read-only systemd service inventory and must-run selection;
- CPU, load, swap, filesystem, and per-block-device diagnostics;
- raw, five-minute, and hourly history with count, extrema, coverage, and
  visible gaps;
- bounded SMART, ZFS, hwmon, NVIDIA, AMD, and Intel collector adapters.

The product boundary remains one owner, PostgreSQL, Linux-first agents, one
active agent identity per device, and owner-scoped policy. Collectors discover
and report; they do not start or stop services, repair storage, run SMART
self-tests, scrub ZFS pools, change GPU power, or grant privileges. Credentials
remain owner-scoped and are never sent to ordinary monitoring agents.

## Run the verification suite

From the repository root, the fixture-first checks are:

```sh
npm ci
go test ./... -count=1
go vet ./...
npm run check
npm run build
npm run lint
npm run format:check
npx playwright test
scripts/test-integration.sh
scripts/test-systemd.sh
scripts/test-storage-health.sh
scripts/test-hardware-monitoring.sh
scripts/test-monitoring-load.sh
```

The integration script uses a disposable PostgreSQL 17 container when no
`SCOUT_TEST_DATABASE_URL` is supplied. The other scripts default to fixtures
and do not contact a host bus, storage utility, GPU, or remote device. Their
live paths are opt-in and require both the documented environment switch and
an explicit `*_CONFIRM=YES` value. Run those paths only against infrastructure
the owner has selected as disposable; they do not grant access or mutate a
target.

The browser suite covers the owner setup flow, notifications, diagnostics,
history, hardware presentation, and the integrated incident → service →
device-history journey. The integrated journey uses deterministic API fixtures
so it can protect keyboard and responsive behavior without contacting a real
host.

## Operating behavior

### Incidents

Default incident rules evaluate accepted, deduplicated telemetry. A qualifying
numeric value must remain strictly beyond its trigger threshold for the
configured duration; equality does not trigger. Recovery uses the opposite
clear threshold and duration. Missing, stale, unsupported, reset, duplicate,
or out-of-order evidence does not advance a timer. Acknowledgment records owner
response but does not recover an incident.

Failed services and explicit storage faults use their state evidence. Selected
must-run units can alert when inactive; an intentionally inactive unit outside
that selection does not. A failed or denied optional collector cannot stop
base host monitoring, and partial inventories are never treated as complete
healthy inventories.

Inspect current lag, queue sizes, delivery failures, rollup lag, and storage
pressure in the monitoring status surface or through:

```sh
curl -fsS -b cookies.txt https://scout.example.test/api/v1/monitoring/status
```

The response is operational metadata only. It must not contain passwords,
tokens, private keys, ntfy topics, or encrypted secret envelopes.

### History

The defaults are 30 days of raw observations, 90 days of five-minute
aggregates, and 365 days of hourly aggregates. The server chooses a suitable
available tier and returns no more than 600 points per series. Each point keeps
source time, availability, sample count, extrema, coverage, and partial-bucket
metadata. Empty intervals remain visible; the UI does not interpolate them.

The repository load runner measured a synthetic 100-device shape with 40
numeric series and 50 service states per device over 24 hours: 96,000 samples
and 120,000 observations. Its latest in-memory result was 8.565 ms p95 for the
year-range query and 0 seconds of evaluation lag. This is a regression result,
not a PostgreSQL capacity or production sizing claim. Size PostgreSQL from an
owner-authorized run with the full retained workload and record relation/WAL/
backup bytes before changing defaults.

### Notifications and suppression

ntfy is the only external notification provider in this slice. Destinations
are encrypted at rest and write-only after entry: list and status responses
return metadata and masked routing information, never the topic or token.
Transport validates the configured endpoint and TLS behavior, uses bounded
timeouts and retries, and reports receiver acceptance separately from
subscriber receipt.

Quiet windows silence every severity while a matching fleet, site, or device
window applies. Incidents continue to be recorded. When suppression ends, one
bounded summary covers incidents that are still active; resolved incidents and
every suppressed transition are not replayed.

### Service and hardware collectors

The service view exposes provider, entity, state, source time, freshness, and
collector diagnostics. Systemd collection is read-only and does not read unit
environment secrets or journal contents. Storage views distinguish physical
devices, pools, datasets, and usable capacity. Sensor and GPU views expose
field-level availability and stable entity identity; zero RPM and negative
temperatures remain valid values.

The adapters and UI have fixture coverage. Live Linux systemd, SMART/ZFS,
hwmon, and GPU family support remains an owner-authorized acceptance gate, as
shown in the [support matrix](../specs/002-advanced-monitoring/support-matrix.md).
In particular, fixture parsing must not be read as evidence that a specific
kernel, driver, utility, device model, or permission set is supported.

## Backup, restore, and recovery

Use the existing [operations runbook](operations.md) for provisioning and
backup commands. Spec 002 backups include normalized telemetry, alert state,
rollup checkpoints, suppression state, notification destinations, and
encrypted secret envelopes. The wrapping key is kept separately and is never
copied into the database dump.

Restore only into a separately verified destination. The restore script checks
the migration generation, monitoring table coverage, checkpoint count, and
settings revision before applying a recovery fence. The fence cancels queued
notification work, resets evaluator timing, pauses enrollment and updates,
and requires owner reconciliation before delivery resumes. It prevents stale
outbox messages from being replayed as if they were current.

The repository has disposable PostgreSQL integration evidence for the durable
state and no-replay policy. A protected clean-destination `pg_dump`/`pg_restore`
run and owner reconciliation remain open release gates.

## Release gates and claims

| Gate | Current evidence | Claim allowed now | Required before acceptance |
| --- | --- | --- | --- |
| Incidents, rules, suppression, and delivery | Go, SQL, and browser fixtures | Behavior is covered by automated fixtures | Owner-authorized live receiver and monitored-host run |
| Keyboard and responsive monitoring UI | Seven Playwright journeys, including 360px and 1440px checks | Accessible fixture journey is verified | Repeat against the release artifact and a real authenticated workspace |
| Migration, history, and recovery | Disposable PostgreSQL integration plus failure fixtures | Durable transaction and no-replay behavior is evidenced | Protected clean-destination restore and reconciliation |
| Expanded workload | 100-device synthetic in-memory workload | Regression shape and measured query result | PostgreSQL bytes, WAL/backup size, p95, lag, and saturation from an authorized lab |
| Systemd | Read-only adapter fixtures | Parser and failure boundaries are verified | Ubuntu/Debian Linux systemd lab with exact versions and permissions |
| SMART, ZFS, sensors, and GPUs | Secret-free adapter fixtures | Unavailable fields and bounds are verified | Representative owner-authorized hardware and utility/driver evidence |
| Agent delivery of optional collectors | Registry and adapter fixtures | Contract shape is verified | Enrolled Linux agent run proving scheduling, transport, and independent failure |

Do not advertise a hardware family as supported solely because its fixture
passes. Do not deploy the live lab paths to real devices, use production
credentials, or publish a capacity limit without the owner-authorized evidence
listed above.

## Troubleshooting boundaries

- A stale or unavailable field is evidence about collection freshness, not a
  zero value. Check collector state and permissions before changing rules.
- A full evaluation, rollup, or notification queue is surfaced as
  backpressure. Do not remove the bound or silently drop the oldest work.
- A changed SSH host key is a trust failure, not an invitation to accept the
  new key automatically.
- After restore, keep delivery, enrollment, and updates paused until the owner
  has reviewed identity, scope, credential, trust, and job state.
- If a collector is absent or denied, base host monitoring should continue and
  the UI should say what is unavailable rather than showing fabricated health.

For first-agent installation, Compose port configuration, signed releases,
and the development MFA bypass, see [operations.md](operations.md) and the
copyable setup in the [README](../README.md).
