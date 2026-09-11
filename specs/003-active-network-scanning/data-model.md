# Data Model: Active Network Scanning

## Principles

- Scope policy authorizes network actions; observations never create authority.
- A scanning vantage point is an authenticated executor, not a credential holder.
- Address and open-port evidence never establish stable device identity or host trust.
- Historical observations are append-only within retention; projections are replaceable derivations.
- Every queued or accepted effect is fenced by scope revision, scanner identity, expiry, and lease epoch.
- Missing, stale, filtered, closed, unreachable, skipped, and failed evidence remain distinct.

## Entities

### Scan policy

Extends one existing scope without changing its identity.

| Field | Meaning and validation |
| --- | --- |
| `scope_id` | Existing scope; one scan policy per scope. |
| `revision` | Monotonically increasing policy revision; every material mutation increments it. |
| `enabled` | Existing scope authorization. Disabled policy cannot materialize or continue scans. |
| `server_enabled` | Explicit permission for control-server vantage; false by default on existing scopes. |
| `agent_ids` | Explicit assigned active agent identities or device-bound agent references; maximum 16. |
| `schedule_seconds` | Periodic cadence; 60–86,400 seconds, default 300. |
| `entry_points` | One to 64 typed entries while active; SSH/TCP/22 default. |
| `ranges` | Existing address or prefix allowlist; maximum 128. |
| `exclusions` | Existing address or prefix denylist; exclusions always win. |
| `probes_per_second` | 1–1,000; default 10. |
| `concurrency` | 1–16; default 16. |
| `target_budget` | 1–4,096; default 256. |
| `attempt_budget` | 1–16,384 and no greater than target count multiplied by entry-point count. |
| `timeout_milliseconds` | 100–10,000; default 2,000. |
| `run_deadline_seconds` | 30–900; default 600. |
| `updated_at` | Server time of latest revision. |

### Entry-point definition

| Field | Meaning and validation |
| --- | --- |
| `id` | Stable identifier unique inside policy revision. |
| `name` | Owner-facing label, 1–64 characters. |
| `transport` | Catalog value; `tcp` only initially. |
| `port` | 1–65,535. |
| `access_method` | Optional supported enrollment method; `ssh` initially. Null means observed only. |
| `enabled` | Whether new runs include it. |

Duplicate transport/port pairs are rejected. A custom label does not change protocol behavior.

### Scanning vantage point

| Field | Meaning and validation |
| --- | --- |
| `kind` | `server` or `agent`. |
| `id` | Stable server instance identifier or existing agent identity ID. |
| `device_id` | Present for agent vantage points. |
| `site_ids` | Existing site context allowed for this executor. |
| `capabilities` | Supported scan protocol versions and transports. |
| `last_seen` | Agent heartbeat or server coordinator time. |
| `state` | `available`, `stale`, `revoked`, `decommissioned`, or `unsupported`. |

An agent assignment is valid only while its device association and identity remain active.

### Scan run

| Field | Meaning and validation |
| --- | --- |
| `id` | Server-generated unique run identifier. |
| `scope_id` | Authorizing scope. |
| `scope_revision` | Exact revision snapshot used for execution. |
| `scanner_kind`, `scanner_id` | Assigned vantage point. |
| `trigger` | `schedule` or `owner`. |
| `state` | State machine below. |
| `scheduled_at`, `started_at`, `finished_at` | Server times; start/finish nullable until reached. |
| `assignment_expires_at` | Latest time agent may begin or continue new attempts. |
| `lease_owner`, `lease_epoch`, `lease_expires_at` | Durable execution fencing. |
| `policy_snapshot` | Immutable bounded ranges, exclusions, entry points, and limits. Contains no credentials. |
| `targets_planned`, `attempts_planned` | Counts after bounded expansion. |
| `attempts_completed` | Accepted unique outcomes. |
| `outcome_counts` | Counts by explicit outcome code. |
| `partial_reason` | Catalog reason when coverage is incomplete. |
| `error_code` | Safe catalog error; no remote payload. |

Unique active constraint: at most one `queued`, `leased`, `running`, or `uploading` run per scope/vantage pair.

State transitions:

```text
queued -> leased -> running -> uploading -> completed
   |         |         |          |          
   +---------+---------+----------+-> partial
   +---------+---------+----------+-> failed
   +---------+---------+----------+-> cancelled
   +---------+---------+----------+-> rejected
   +---------+---------+----------+-> expired
```

Terminal states never return to active states. Retry creates or re-leases work only under current policy and a higher fencing epoch.

### Scan-result receipt

| Field | Meaning and validation |
| --- | --- |
| `run_id` | Parent run. |
| `page_ordinal` | Zero-based integer; unique within run. |
| `content_hash` | Hash of canonical uploaded body. |
| `accepted_at` | Server receipt time. |
| `result_count` | 0–1,000. |
| `is_final` | Declares last page and includes completion summary. |

`(run_id, page_ordinal)` is unique. Identical replay returns original acceptance; another hash conflicts. Pages from wrong scanners, stale epochs, or expired assignments are rejected before observations become actionable.

### Entry-point observation

| Field | Meaning and validation |
| --- | --- |
| `id` | Server-generated evidence identifier. |
| `run_id`, `page_ordinal` | Provenance and deduplication source. |
| `scope_id`, `scope_revision` | Authorizing context. |
| `scanner_kind`, `scanner_id` | Vantage provenance. |
| `address` | Canonical literal IPv4 or IPv6 address. |
| `transport`, `port`, `entry_point_id` | Attempted entry point. |
| `outcome` | `open`, `closed`, `filtered`, `unreachable`, `skipped`, or `scanner_error`. |
| `reason_code` | Optional safe catalog detail. |
| `latency_milliseconds` | Optional non-negative value capped at configured timeout. |
| `observed_at`, `received_at`, `expires_at` | Evidence time, receipt time, and freshness boundary. |
| `actionable` | True only after current-policy and executor checks; false for retained rejected evidence. |

No banner, host key, credential, packet capture, or arbitrary response body is stored.

### Entry-point current projection

Derived by `(site_id, scope_id, address, transport, port, scanner_kind, scanner_id)`.

Stores newest authoritative observation, freshness, contradiction status, and history link. Projection update uses observation time then receipt time; older or duplicate evidence cannot overwrite newer state.

### Candidate device

Extends existing candidate projection.

| Field | Meaning and validation |
| --- | --- |
| `id`, `site_id`, `scope_id` | Existing candidate identity and context. |
| `address` | Contextual observed address, not global identity. |
| `state` | Catalog in specification FR-011. |
| `entry_point_ids` | Current evidence references, bounded to 64. |
| `preferred_access_method` | Supported method selected from current open evidence. |
| `last_scanned_at` | Latest authoritative scan observation. |
| `coverage_state` | `current`, `partial`, `stale`, `contradicted`, or `unknown`. |

Existing exclusion and decommission tombstones remain stronger than scan evidence.

### Access request

Extends existing access request semantics.

| Field | Meaning and validation |
| --- | --- |
| `dedupe_key` | Candidate ID, access method, and canonical endpoint. |
| `reason_code` | `missing_credentials`, `invalid_credentials`, `insufficient_privilege`, `host_trust_required`, `server_connectivity_required`, or `unsupported_platform`. |
| `entry_point_observation_id` | Evidence supporting request. |
| `state` | `open`, `reevaluating`, `resolved`, `superseded`, or `closed`. |

One open/reevaluating request may exist per dedupe key. New evidence refreshes it; it does not create another request.

## Relationships

```text
scope 1 --- 1 scan_policy
scan_policy 1 --- many entry_point_definitions
scope many --- many scanning_vantage_points (explicit assignments)
scope 1 --- many scan_runs
scanning_vantage_point 1 --- many scan_runs
scan_run 1 --- many scan_result_receipts
scan_result_receipt 1 --- many entry_point_observations
entry_point_observations many --- 1 candidate projection (conservative reconciliation)
candidate 1 --- many access requests over time
candidate 1 --- zero/one active enrollment job
```

## Transaction boundaries

1. **Run materialization**: Lock due scope/vantage key; read current policy and workspace pause; insert run with immutable policy snapshot and active uniqueness in one transaction.
2. **Result page ingestion**: Authenticate scanner; lock run; validate revision, epoch, expiry, bounds, and hash; insert receipt and observations; update counters and current projections atomically.
3. **Finalization**: Validate result counts and terminal summary; set completed or partial terminal state in same transaction as final receipt.
4. **Reconciliation**: Lock candidate key; apply current actionable evidence; upsert one access request or invoke enrollment evaluation. Recheck current scope and exclusion before creating effects.
5. **Credential/trust mutation**: Commit protected mutation, then enqueue affected candidate reconciliation transactionally; no secret enters work payload.
6. **Pause/revision**: Increment/fence policy and mark queued/active runs cancellation-requested. Executors stop at target boundary; stale completion cannot create effects.

## Retention and deletion

- Detailed entry-point observations and result receipts: 30 days after receipt, except evidence referenced by an unresolved access request or active enrollment investigation.
- Scan-run summaries: 90 days.
- Current entry-point projection: replaced by newer evidence; stale rows may remain while candidate exists.
- Candidates, exclusions, access requests, devices, trust, and audit records follow their existing policies and are never deleted merely because raw scan evidence expires.
- Cleanup is bounded, restart-safe, and reports lag or blocked retention.

## Compatibility

- Existing scopes receive a disabled scan policy until owner review; no deployment begins scanning due to migration alone.
- Existing `ports` and `allowed_methods` map to typed TCP entry points where valid, with SSH/TCP/22 recognized; migration rejects no existing scope but may mark unsupported entries observation-only.
- Older agents receive no scan assignment and continue telemetry and updates. Their scan capability is reported unsupported.
- Existing candidate and access-request IDs remain stable. New dedupe fields are backfilled from safe target/method details where possible.
