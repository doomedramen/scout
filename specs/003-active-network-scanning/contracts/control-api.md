# Control API Additions: Active Network Scanning

All routes use the existing `/api/v1` prefix, owner session and CSRF rules, bounded JSON decoding, error envelope, audit behavior, and cursor conventions. Mutations require recent MFA when they enable scanning, expand targets, assign vantage points, or trigger an on-demand run.

## Scan policy on scopes

`GET /scopes` and `GET /scopes/{scopeId}` include:

```json
{
  "scanPolicy": {
    "serverEnabled": false,
    "agentIds": [],
    "scheduleSeconds": 300,
    "entryPoints": [
      {
        "id": "ssh-default",
        "name": "SSH",
        "transport": "tcp",
        "port": 22,
        "accessMethod": "ssh",
        "enabled": true
      }
    ],
    "limits": {
      "probesPerSecond": 10,
      "concurrency": 16,
      "targetBudget": 256,
      "attemptBudget": 256,
      "timeoutMilliseconds": 2000,
      "runDeadlineSeconds": 600
    }
  }
}
```

Scope create and patch accept the same object. Patch requires `expectedRevision`. Validation uses limits from `data-model.md`; duplicate endpoints, unsupported transports, incompatible access methods, unassigned/revoked agents, and attempt multiplication beyond budget are rejected.

Enabling a previously disabled scan policy or widening ranges, entry points, assigned agents, rate, concurrency, targets, attempts, timeout, or deadline requires recent MFA. Existing scopes migrate with `serverEnabled=false`, `agentIds=[]`, and do not begin scans automatically.

## Scan runs

| Method and path | Input | Result |
| --- | --- | --- |
| `POST /scopes/{scopeId}/scan-runs` | `expectedRevision`, scanner selector (`server` or assigned agent ID), optional idempotency key | 202 with newly queued owner-triggered run, or original run for identical idempotent replay. |
| `GET /scan-runs` | cursor, scopeId, scannerId, state, startedAfter, startedBefore; page size 100 default/500 max | Redacted run summaries and next cursor. |
| `GET /scan-runs/{runId}` | none | Run policy summary, progress, bounds, outcome counts, partial/error code, timing, and page counts. No raw remote response. |
| `POST /scan-runs/{runId}/cancel` | expected state/revision | 202 while cancellation is being acknowledged; terminal state when no more connections may start. |

Run create rejects disabled scopes, global discovery pause, ineligible scanners, active duplicate scope/vantage work, unsupported capabilities, invalid policy, recovery mode, or capacity backpressure.

## Candidates and entry points

| Method and path | Input | Result |
| --- | --- | --- |
| `GET /candidates` | cursor, scopeId, state, accessMethod, freshness, query; page size 100 default/500 max | Candidate summaries including actionable state, coverage, current entry-point count, provenance summary, and next cursor. |
| `GET /candidates/{candidateId}` | none | Candidate identity evidence, current entry-point observations by vantage, freshness, contradictions, access requests, and enrollment link. |
| `GET /candidates/{candidateId}/entry-points` | cursor, currentOnly, scannerId | Bounded observation metadata. No banners or packet content. |
| `POST /candidates/{candidateId}/reevaluate` | expected scope revision | 202 for bounded current-policy access/enrollment re-evaluation. |

Candidate states use exact catalog:

`discovered`, `needs_credentials`, `invalid_credentials`, `needs_privilege`, `needs_host_trust`, `needs_server_connectivity`, `queued`, `enrolling`, `enrolled`, `unsupported`, `unreachable`, `excluded`, `stale`.

An `open` SSH entry point without matching usable access returns an action descriptor linking to credential assignment. Unsupported observed entry points do not request credentials.

## Credential and trust integration

Credential creation/rotation and trust creation/revocation responses include `affectedCandidateCount` and enqueue re-evaluation after protected mutation. No secret is included in candidate, run, audit, or work records.

Credential matching uses method plus target constraints. Scout does not try multiple unrelated credentials against a host. The owner explicitly associates access through existing scope/target metadata.

## Pause and status

Existing discovery pause controls include server coordinator and agent scans. `GET /control/state` adds active scan holders and their acknowledgement state.

`GET /scopes/{scopeId}/scan-status` returns:

- policy revision and enabled state;
- last completed and next scheduled run per vantage;
- current active run and progress;
- assigned vantage capabilities and freshness;
- latest coverage state and partial reason;
- current candidate outcome counts;
- retention or queue lag.

No status field may imply full coverage when a bound, cancellation, scanner loss, or backpressure made a run partial.

## Error codes

Add stable codes:

- `scan_policy_invalid`
- `scan_scope_paused`
- `scan_globally_paused`
- `scan_vantage_not_assigned`
- `scan_vantage_unavailable`
- `scan_capability_unsupported`
- `scan_already_active`
- `scan_assignment_expired`
- `scan_revision_superseded`
- `scan_lease_conflict`
- `scan_result_conflict`
- `scan_bound_exceeded`
- `scan_backpressure`

Messages remain safe and do not echo remote data or secrets.
