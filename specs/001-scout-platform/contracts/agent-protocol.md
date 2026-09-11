# Agent and Worker Protocol Contract

Production endpoints require verified server TLS. Except bootstrap, agents use mTLS and the server derives identity from the certificate. Ordinary agents and privileged workers have separate roles; a browser session cannot masquerade as either.

## Bootstrap and renewal

- `POST /agent/v1/enroll`: `{invitation, csrPem, agentVersion, platform, architecture}`. Invitation is device/job-bound, five-minute expiry and single use. Reject caller-requested owner/worker privileges or mismatched platform/target. Return `{deviceId, agentId, certificatePem, caBundlePem, expiresAt, protocolVersion:1}`. Never log request bodies. Store agent private key locally before enrollment with 0600 ownership; do not transmit it.
- `POST /agent/v1/renew`: authenticated current identity plus CSR; return a new certificate for the same device. Revoked/expired identities cannot renew. A lost enrollment response follows the revalidated replacement-invitation flow in plan.md, not token reuse or a second device.

## Collection and desired state

- `POST /agent/v1/batches`: `{protocolVersion:1, bootId, batchId, observedAt, collector:{id,schemaVersion}, samples:[], observations:[], droppedCount}`. Identity is never accepted from a payload deviceId. Samples have entityId, metric, unit, finite value or explicit unavailability, labels and observedAt. Observations have kind, contextual subject, expiry and confidence. Server appends receivedAt.
- Maximum decoded body 1 MiB, 1,000 samples and 1,000 observations. Reject the whole batch on invalid core fields, return 413 on size excess, and 429/503 with Retry-After for backpressure. The client splits future batches or quarantines an invalid one rather than retrying forever.
- On commit return `{batchId, acceptedAt, duplicate:false}`. Identical replay returns the original acknowledgement with duplicate=true; same identity/boot/batch ID with different content returns 409. Retries cannot insert duplicate measurements.
- `POST /agent/v1/heartbeat`: `{bootId, installedVersion, uptimeSeconds, collectorStates, updateState}`; return server time and current policy revision. Heartbeat receipt does not refresh metric samples.
- `GET /agent/v1/desired-state?revision=...`: bounded long poll (max 25 seconds) returns `{revision, expiresAt, discoveryPolicy, collectorConfig, updateAssignment}`. Discovery policy contains site/ranges/exclusions, ports, methods, budgets, and expiry. No generic shell/script command and no reusable enrollment credential is permitted.
- `GET /agent/v1/releases/{digest}`: stream only the assigned compatible immutable artifact with authenticated access and bounded/resumable transfer. Every resumed artifact is fully hashed and signature-verified before execution.
- `POST /agent/v1/update-results`: `{assignmentId,generation,state,installedVersion,errorCode}`. Reject stale generation; record actual version separately from desired version. Report rollback truthfully.

## Enrollment worker

Worker registration is an explicit owner/local administration operation. It is not a capability granted by ordinary agent enrollment. Use distinct certificates and endpoint authorization.

- `POST /worker/v1/claim`: worker capability and permitted sites; return at most one currently authorized job with jobId, deviceId, scopeRevision, epoch, deadline, destination, trustRef, credentialGrantId, and release reference.
- `POST /worker/v1/jobs/{id}/renew`: current epoch; renew lease only if job and policy remain valid.
- `POST /worker/v1/grants/{id}/redeem`: live matching lease and worker identity; return a short-lived target-specific credential over authenticated transport. No list/export API for secret values. Audit redemption; scope/credential revocation denies subsequent use.
- `POST /worker/v1/jobs/{id}/progress`: current epoch and enumerated safe progress/error state. Reject old worker reports after reassignment. Worker cannot declare successful enrollment without server-observed agent confirmation.

Default worker lease: 60 seconds, renew at 20 seconds, total job deadline ten minutes. Scope changes fence new work immediately after acknowledgement. Running operations recheck authorization before each privileged step and stop at safe boundaries; target-local locking and crash journal prevent overlapping installation after lease expiry. No worker may use a job as authorization to follow an unvalidated hostname/IP redirect.

## Compatibility

Protocol major version changes require coordinated compatibility tests. Additive optional fields are permitted within size bounds; unknown namespaced collector data cannot override core fields. Unsupported major versions fail clearly and remain visible as needing a compatible update; never silently disable validation.
