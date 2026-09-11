# Control API Contract

This is the normative v1 interface outline for implementation. T003 converts it into executable OpenAPI and JSON Schema before handlers are added. These routes do not exist in the scaffold, except its separate development `/api/status` endpoint.

## Shared rules

Base path `/api/v1`. JSON uses camelCase, UUID IDs, RFC3339 UTC timestamps, explicit units and enum states. Lists return `{items, nextCursor}`; cursor is opaque, default limit 100/max 500. Errors return `{error:{code,message,requestId,retryable}}`; never include raw backend errors or secret values. Use 400 validation, 401 missing/invalid identity, 403 capability denied, 404 unknown resource, 409 state/revision conflict, 413 size limit, 429 rate limit, and 503 temporary unavailable. Retry responses include Retry-After when applicable.

Owner routes require secure cookie session, CSRF and Origin checks for mutations. Credential, trust, recovery, scope-enablement, enrollment, release and decommission mutations require recent second-factor verification. POST job-creating requests require an Idempotency-Key; repeated key with the same body returns the original outcome, a different body returns 409. Policy PATCH requests carry expectedRevision and fail on conflict. Version assignments are declarations of desired state, not arbitrary command strings.

## Authentication and setup

| Method / path              | Input                                              | Result / authorization                                                                              |
| -------------------------- | -------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| POST /setup                | setupToken, password                               | Create singleton owner; 201; token is locally provisioned and expires. No owner/session exists yet. |
| POST /sessions             | password, totpCode or recoveryCode when configured | Set session cookie; 200 metadata; generic 401 and rate limits. No credentials in URL.               |
| DELETE /sessions/current   | none                                               | 204; revoke session and clear cookie.                                                               |
| POST /sessions/reauth      | password, totpCode                                 | Update recent verification; 204.                                                                    |
| POST /owner/mfa/setup      | password                                           | Return one-time enrollment material, no persisted secret read route.                                |
| POST /owner/mfa/confirm    | totpCode                                           | Activate and return recovery codes once; invalidate setup challenge.                                |
| POST /owner/recovery-codes | password, totpCode                                 | Rotate recovery codes, returned once; old codes invalidated.                                        |
| GET /owner                 | none                                               | Safe owner/session/MFA metadata only.                                                               |

Lost-factor/password recovery requires a local console command with local administration, session revocation, and audit. There is no public unauthenticated reset endpoint.

## Inventory and observations

| Method / path                | Input                                                                 | Result                                                                                                                               |
| ---------------------------- | --------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| GET /devices                 | cursor, limit, query, health, monitoringState, siteId                 | Device summary: id, displayName, addresses, availability, metricFreshness, lastSeen, agentVersion, collectorStates.                  |
| GET /devices/{id}            | none                                                                  | Safe identity, metadata, current metrics, source evidence.                                                                           |
| GET /devices/{id}/metrics    | from, to, metric, entityId, maxPoints<=600                            | `{series:[{metric,unit,points:[{observedAt,value,min,max,availability}]}]}`; null/gap retained. Reject reversed or excessive ranges. |
| GET /topology                | siteId, cursor, limit                                                 | Bounded nodes and relationships with evidence IDs, age and confidence.                                                               |
| GET /observations/{id}       | none                                                                  | Redacted source and observation metadata.                                                                                            |
| POST /devices/{id}/reconcile | otherDeviceId, action=associate or separate, reason, expectedRevision | Audited correction, 409 on revoked/conflicting current state.                                                                        |

## Scope, access, and enrollment

| Method / path                   | Input                                                                                | Result                                                                                                                                                                                                                   |
| ------------------------------- | ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| GET/POST /sites                 | name on create                                                                       | Site records; 201 on create.                                                                                                                                                                                             |
| GET/POST /scopes                | siteId, ranges, exclusions, ports, methods, limits, credentialRef, trustRef, enabled | Versioned scope; enabling is the owner's authorization for automatic enrollment.                                                                                                                                         |
| PATCH /scopes/{id}              | expectedRevision and changed fields                                                  | Updated revision; queued work invalidated as necessary.                                                                                                                                                                  |
| POST /control/pause             | discovery, enrollment, updates booleans                                              | 202 while pausing; 200 only when effective. Fence new leases immediately and await execution holders' safe-boundary acknowledgement. An unreachable holder remains visibly pausing, not falsely acknowledged as stopped. |
| GET /control/state              | none                                                                                 | Requested/effective pause flags and unresolved execution holders; no secrets.                                                                                                                                            |
| GET/POST /credentials           | metadata on reads; kind, secret, allowedUse/targets on create                        | Metadata only after creation. Secret is write-only and never echoed.                                                                                                                                                     |
| POST /credentials/{id}/rotate   | new secret, expectedRevision                                                         | New version; old unstarted grants invalidated.                                                                                                                                                                           |
| DELETE /credentials/{id}        | none                                                                                 | Revoke new use; 204.                                                                                                                                                                                                     |
| GET/POST /trust                 | scoped host key or host CA and safe metadata                                         | Trusted identity records; no automatic acceptance of discovered keys.                                                                                                                                                    |
| GET /access-requests            | cursor, reason, state                                                                | Specific sanitized unmet prerequisites.                                                                                                                                                                                  |
| GET /jobs                       | cursor, kind, state, deviceId                                                        | Progress and outcome, never credentials or command contents.                                                                                                                                                             |
| POST /bootstrap-invitations     | displayName, siteId                                                                  | Reserve the first/manual device record and return a five-minute invitation once; same Idempotency-Key never creates a second device. This is the first-agent flow before any discovered device exists.                   |
| GET /bootstrap/agent/{architecture} | architecture (`amd64` or `arm64`)                                                   | Public server-hosted Linux bootstrap binary. Response includes `X-Scout-Agent-SHA256`; no invitation or credential is accepted.                                                                                 |
| GET /bootstrap/agent/install.sh | none                                                                                  | Public server-hosted installer script that downloads the selected bootstrap binary from the same server.                                                                                                      |
| POST /devices/{id}/bootstrap    | expectedRevision                                                                     | Device-bound invitation returned once, expiry, installation instructions with token-file/stdin use. No reusable secret in command arguments.                                                                             |
| POST /devices/{id}/decommission | uninstall boolean, reason                                                            | Revoke, exclude, and optionally queue verified uninstall; 202.                                                                                                                                                           |
| POST /devices/{id}/reenable     | expectedRevision                                                                     | Explicitly clear exclusion and revalidate scope/access for new enrollment.                                                                                                                                               |

## Releases, collectors, audit, and recovery

| Method / path                                    | Input                                                                         | Result                                                                                    |
| ------------------------------------------------ | ----------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| POST /releases/import                            | multipart manifest, signature, raw binary; bounded stream                     | 201 verified release metadata or rejection; imported trust keys are not auto-trusted.     |
| GET /releases                                    | cursor, platform                                                              | Immutable approved release metadata.                                                      |
| GET/POST /rollouts                               | on create: releaseId, targets, policy, window+timezone, canaries, concurrency | Desired assignments; 202; pins and compatibility checked.                                 |
| PATCH /rollouts/{id}                             | expectedRevision, paused/policy changes                                       | Audited revision.                                                                         |
| PATCH /devices/{id}/update-policy                | expectedRevision, manual/automatic/pinned, version/window                     | Device override with explicit pin semantics.                                              |
| GET/PATCH /devices/{id}/collectors/{collectorId} | on change: enabled, config, credentialRef, expectedRevision                   | Descriptor, safe config, required permissions and health; no arbitrary executable upload. |
| GET /audit                                       | cursor, action, target, time range                                            | Redacted events for owner.                                                                |
| GET/PATCH /settings/retention                    | rawDays, auditDays, expectedRevision                                          | Effective validated policy; reductions clearly disclosed before application.              |
| POST /recovery/reconcile                         | reconciled policy/credential/revocation revisions                             | Leave recovery mode after local restore and owner verification; record decision.          |

Backup/restore commands are local administrative operations documented in quickstart.md, not public file-upload/restore endpoints.
