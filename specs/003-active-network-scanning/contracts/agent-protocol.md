# Agent Protocol Additions: Active Network Scanning

These additions extend the authenticated agent protocol from spec 001. Existing mTLS/token identity, body limits, error envelope, replay rules, and update/telemetry behavior remain unchanged.

## Desired state

`GET /api/v1/agent/v1/desired-state?revision=<last-seen>` may add `scanAssignment`. It is null when the agent has no eligible work.

```json
{
  "revision": 42,
  "expiresAt": "2026-09-11T15:00:00Z",
  "scanAssignment": {
    "runId": "uuid",
    "leaseEpoch": 3,
    "scopeId": "uuid",
    "scopeRevision": 9,
    "assignmentExpiresAt": "2026-09-11T14:30:00Z",
    "ranges": ["192.0.2.0/24"],
    "exclusions": ["192.0.2.9"],
    "entryPoints": [
      {
        "id": "ssh-default",
        "name": "SSH",
        "transport": "tcp",
        "port": 22,
        "accessMethod": "ssh"
      }
    ],
    "limits": {
      "probesPerSecond": 10,
      "concurrency": 16,
      "targetBudget": 256,
      "attemptBudget": 256,
      "timeoutMilliseconds": 2000,
      "runDeadlineSeconds": 600,
      "resultPageSize": 1000
    }
  }
}
```

Rules:

- Assignment contains no credential, trust secret, command, script, banner rule, or arbitrary payload.
- Server returns work only to explicitly assigned active agent identity with compatible scan capability.
- Agent MUST reject expired assignments, unknown transports, exceeded limits, invalid ranges, or a policy whose bounds exceed its supported protocol.
- Agent MUST apply exclusions and attempt budget after target expansion and immediately before every connection.
- One active scan at a time per agent. Newer policy or cancellation stops new connections at next target boundary.
- Desired-state expiry is fail-closed: disconnected agents stop starting scan work.

## Result pages

`POST /api/v1/agent/v1/scan-results`

Maximum decoded body: 1 MiB. Maximum 1,000 results per request.

```json
{
  "protocolVersion": 1,
  "runId": "uuid",
  "leaseEpoch": 3,
  "scopeRevision": 9,
  "pageOrdinal": 0,
  "observedFrom": "2026-09-11T14:20:00Z",
  "observedTo": "2026-09-11T14:20:30Z",
  "final": true,
  "results": [
    {
      "address": "192.0.2.10",
      "entryPointId": "ssh-default",
      "transport": "tcp",
      "port": 22,
      "outcome": "open",
      "latencyMilliseconds": 4,
      "observedAt": "2026-09-11T14:20:01Z"
    }
  ],
  "summary": {
    "targetsPlanned": 256,
    "attemptsPlanned": 256,
    "attemptsCompleted": 256,
    "partialReason": null
  }
}
```

Validation:

- Identity in payload is ignored; authenticated caller must equal run scanner.
- `protocolVersion` is exactly 1.
- Run, epoch, scope revision, entry-point ID, transport, port, address inclusion, exclusion, and assignment expiry must match server record.
- Outcomes are exactly `open`, `closed`, `filtered`, `unreachable`, `skipped`, or `scanner_error`.
- `reasonCode` is optional and restricted to a safe catalog; arbitrary remote errors are not accepted.
- Latency is optional, finite, non-negative, and no larger than configured timeout plus clock-safe tolerance.
- Observation times must fall inside assignment lifetime with at most five seconds future skew.
- `summary` is required only on final page. Counts cannot exceed run bounds or accepted unique results.
- Page ordinal may arrive once. Identical body replay returns original receipt with `duplicate=true`; conflicting content returns 409.
- A stale, revoked, decommissioned, wrong-scope, expired, cancelled, or superseded result returns 409 or 403 and creates no actionable evidence.
- Backpressure returns 429 or 503 with `Retry-After`. Agent uses bounded jittered retry and its separate scan-result spool.

Success response:

```json
{
  "runId": "uuid",
  "pageOrdinal": 0,
  "acceptedAt": "2026-09-11T14:20:31Z",
  "duplicate": false,
  "runState": "completed"
}
```

## Scan capability

Agent heartbeat adds a bounded capability declaration:

```json
{
  "capabilities": {
    "scanProtocolVersions": [1],
    "scanTransports": ["tcp"]
  }
}
```

Older agents may omit this object and are treated as scan-unsupported. Capability declaration grants no scope.

## Pause acknowledgement

`POST /api/v1/agent/v1/pause-ack` uses the authenticated agent identity and an
empty JSON object. The server keeps the agent in `ExecutionHolders` while a
leased scan is still active, even when cancellation has been requested. The
agent acknowledges only after its scan executor has stopped; a pending
acknowledgement returns the current pause state with HTTP 202.

## Isolation requirements

- Scan execution and result retry MUST NOT hold the host telemetry collection lock.
- Scan results use a separate spool capped at 16 MiB and one hour; oldest pages may be dropped only with a reported partial outcome.
- Telemetry, heartbeat, certificate renewal, pause acknowledgement, and updates retain priority over scan work.
- No scan payload or diagnostic may contain credentials, host keys, banners, packet bodies, or arbitrary response text.
