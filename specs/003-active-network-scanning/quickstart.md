# Quickstart Validation: Active Network Scanning

This guide defines acceptance runs. It does not authorize scanning production, public, or third-party networks.

## Prerequisites

- Complete required 001 identity, scope, credential, trust, enrollment, audit, and pause foundations.
- Use PostgreSQL 17 and repository-pinned Go and Node versions.
- Provision an owner-authorized isolated Linux lab with:
  - one Scout control server;
  - one enrolled scan-capable agent;
  - one network reachable by server;
  - one different network reachable only by agent;
  - at least one open SSH endpoint, one closed endpoint, one filtered or timed-out endpoint, and one excluded address;
  - an adjacent unauthorized range whose traffic can be captured to prove zero probes;
  - disposable SSH credentials and host keys, never production credentials.
- Record exact topology, firewall rules, operating-system versions, addresses, and packet-capture point in `evidence.md`.

## 1. Static and fixture checks

```sh
go test ./internal/discovery ./internal/policy ./internal/enrollment ./internal/store ./internal/control ./internal/agent -count=1
go test ./tests/contracts ./tests/integration -count=1
npm run check
npm run build
```

Expected:

- Policy rejects invalid ranges, exclusions, ports, bounds, unsupported transports, unassigned scanners, stale revisions, and unauthorized result pages.
- Replay is idempotent and conflicting page content is rejected.
- Closed, filtered, unreachable, skipped, scanner-error, stale, and partial states remain distinct.
- Scan failure does not interrupt telemetry fixtures.

## 2. Start isolated Scout

Use repository development setup or disposable Compose profile. Keep the control endpoint and lab networks private.

```sh
npm ci
npm run setup
npm run db:up
npm run dev
```

Create the owner, bootstrap one lab agent, and confirm current host telemetry before enabling scans.

## 3. Configure server-vantage scope

Create a disabled site scope for exactly the server-reachable lab range. Add the excluded target. Configure only SSH/TCP/22, defaults of 10 attempts/second, 16 concurrency, 256 targets, two-second timeout, and server scanning enabled. Review policy, complete protected enablement, then start one on-demand scan.

Expected:

- UI shows queued, running/uploading, then completed or explicitly partial run.
- Open SSH endpoint appears within five minutes with server provenance.
- Closed and filtered endpoints retain exact outcomes.
- Excluded and unauthorized adjacent targets receive zero packets.
- SSH candidate without access shows `needs_credentials` within 30 seconds.

## 4. Resolve access and enroll

Assign a disposable matching SSH credential and separately establish expected host trust. Exercise invalid authentication and insufficient privilege before valid access.

Expected:

- One access request changes through specific failure states without revealing secrets.
- Matching changes trigger re-evaluation within one minute without another full scan.
- Exactly one enrollment job is queued after all prerequisites pass.
- Host-key mismatch blocks installation.
- Successful enrollment requires authenticated agent reporting, not an open port or copied file.

## 5. Configure agent-vantage scope

Create a second disabled scope for the network reachable only by the enrolled agent. Assign that exact agent; leave server scanning disabled. Enable and run the scan.

Expected:

- Only assigned agent receives scan work.
- Another enrolled agent receives no ranges or work for this scope.
- Agent-discovered candidates use same access and enrollment states as server discoveries.
- Agent receives no credential material.

## 6. Exercise fencing and failure

During separate runs:

1. Add an exclusion while scanning.
2. Disable the scope.
3. Pause discovery globally.
4. Revoke or decommission scanning agent.
5. Disconnect agent before upload and restart server coordinator.
6. Submit identical and conflicting result-page replays.
7. Exceed target, attempt, duration, page, and storage limits.

Expected:

- No new connection begins after latest control reaches scanner at target boundary.
- Stale or late results create no candidate, access, or enrollment effects.
- Identical replay is acknowledged once; conflicting replay is rejected.
- Interrupted runs recover or finish as partial/expired without duplicate active work.
- Bounds and backpressure are visible; no run falsely claims complete coverage.
- Host telemetry and heartbeats continue on schedule.

## 7. UI and accessibility

Run browser tests and manually inspect 360, 768, and 1440 CSS-pixel layouts using keyboard only.

Expected:

- Owner can configure a scan, inspect progress and provenance, filter actionable candidates, open evidence, and reach credential/trust resolution.
- Focus order is logical; outcomes include text; no meaning depends only on color.
- Partial, stale, error, and empty states are distinct.

## 8. Reference-scale validation

Run the controlled 256-address scan and the repository 100-device workspace workload. Capture duration, actual attempt rate/concurrency, database growth, queue lag, telemetry schedule delay, API p95, and dropped result pages.

Expected:

- Success criteria SC-001 through SC-010 pass under documented conditions.
- Any unmet criterion remains open and is not advertised as supported.

## Safety cleanup

Disable both scopes, revoke disposable credentials and trust, decommission lab agents if no longer needed, stop lab services, and retain redacted evidence. Verify packet capture contains no probes outside authorized targets.
