# Control API Additions

Normative target contract; routes are implementation tasks, not claims about current handlers. Base `/api/v1`; inherit 001 UUID, camelCase, RFC3339 UTC, `{items,nextCursor}`, error envelope, secure session, CSRF/Origin, and pagination (100 default/500 maximum). Mutations require authentication; destination/secret writes, tests, and notification resume also require recent MFA. PATCH and DELETE use expectedRevision; stale revision returns 409. POST action/create operations accept Idempotency-Key with the 001 replay/conflict rules. No unauthenticated incident or metrics feeds.

## Routes

| Method / path | Input | Output |
| --- | --- | --- |
| GET/POST /alert-rules | list filters: enabled,targetKind,targetId; create: RuleInput | rule metadata / 201 rule |
| GET/PATCH/DELETE /alert-rules/{id} | expectedRevision for mutation; changed RuleInput fields | revised rule / 204 retirement |
| GET/POST /alert-rules/{id}/overrides | targetKind=site/device, targetId, complete replacement tuple | override list / 201 |
| PATCH/DELETE /alert-rules/{id}/overrides/{overrideId} | expectedRevision, replacement tuple | revised override / 204 |
| GET /incidents | status=active/resolved/closed, acknowledged, severity, deviceId, siteId, cursor | incident summaries including evidenceState and notificationSuppression |
| GET /incidents/{id} | none | episode snapshot and links |
| GET /incidents/{id}/transitions | cursor | chronological transitions |
| POST /incidents/{id}/acknowledgment | expectedRevision | updated incident; repeated acknowledgment idempotent |
| GET/POST /notification-destinations | DestinationInput on create | redacted metadata / 201; never topic/token plaintext |
| PATCH/DELETE /notification-destinations/{id} | expectedRevision; changes or retirement | redacted revised metadata / 204 |
| POST /notification-destinations/{id}/test | expectedRevision | 202 deliveryId; test only configured saved revision |
| GET /notification-deliveries | destinationId, incidentId, status, cursor | status/attempt/acceptedAt/safe error; no raw request or credential |
| GET/POST /suppression-windows | WindowInput | list / 201 |
| PATCH/DELETE /suppression-windows/{id} | expectedRevision; WindowInput changes | revised window / 204 |
| GET /monitoring/settings | none | tier policy, notificationsPaused, configured budgets |
| POST /monitoring/retention-preview | proposed tier days, expectedRevision | previewId, affected ranges/count estimate, expiresAt (five minutes) |
| PATCH /monitoring/settings | expectedRevision; retention and budget values; previewId for destructive retention reduction | revised policy; retention values may only shorten release maxima |
| POST /monitoring/notifications/resume | expectedRevision | requires recovery reconciliation + MFA; 200 resumed, schedules current summary |
| GET /monitoring/status | none | evaluation/rollup lag, queues, drop/truncation counts, pressure and last successful jobs |

No manual incident resolve endpoint: actual recovery is evidence-driven. No service start/stop, disk self-test, or GPU control routes. Reuse 001 collector config/entity endpoints for must-run patterns, sensor exclusions, and new hardware inventory; do not introduce a second collector API.

## Input types

**RuleInput**: name (1–120 chars), targetKind=fleet/site/device (targetId absent for fleet), kind=numeric/state, severity=warning/critical, enabled, condition. Numeric condition: metric identifier from registry, optional entityId, operator=gt/lt, triggerValue, clearValue, triggerSeconds, clearSeconds. gt requires clearValue<triggerValue; lt requires clearValue>triggerValue. Finite values, matching metric unit/range, durations 0–86400 seconds. Catalog state conditions: host_offline, service_failed, service_required_inactive, collector_degraded, hardware_fault. Reject arbitrary expressions, unknown metrics/state names, incompatible targets, or fields for the wrong kind. State conditions include triggerSeconds/clearSeconds (0–86400) and use fresh state observations; the default two-sample behavior is encoded as a minimumConsecutiveSamples=2 for state rules that need it (allowed 1–10). host_offline uses receipt-time deadline and one-heartbeat recovery. Do not allow changing lineage identity through PATCH.

**DestinationInput**: name, baseUrl, topic, optional token, enabled=false by default. Topic/token are write-only; omitted PATCH secrets retain current values, explicit null token removes authentication, topic cannot be null. Response: id/name/baseUrl/maskedTopic/hasToken/enabled/revision/lastTestAt. Server normalizes topic as a single path segment and disallows reserved ntfy routing names; max 128 ASCII letters/digits/underscore/hyphen. Token max 4096 bytes; name max 120. Rules default to all enabled destinations; per-provider routing/escalation is outside 002.

**WindowInput**: name, targetKind=fleet/site/device, targetId, enabled, mode=recurring/oneTime. Recurring requires IANA timezone, weekdays (ISO 1–7), startLocal/endLocal HH:mm with unequal values; overnight window belongs to starting weekday. oneTime requires startsAt<endsAt UTC, at most 365 days. No arbitrary cron expressions. Evaluate recurrence by converting each UTC instant to local wall time: repeated fall-back hour matches both occurrences; nonexistent spring-forward times match no instants. End is exclusive. All matching windows form a union, including host-offline derivative suppression; a still-matching reason prevents release.

## Notification delivery contract

POST a bounded JSON message to the configured ntfy server with topic, title, message, priority and optional click link to the configured Scout base URL plus incident ID. Token uses Authorization Bearer; never query parameters. Critical priority=4, warning=3, recovery/summary=3. No attachments, remote action buttons, email forwarding, or supplied message templates. Total encoded body <=4096 bytes; truncate UTF-8 safely and link to details. Summary lists up to 20 incident labels then total remaining count.

HTTPS with normal CA/hostname verification is required by default. Owner may explicitly allow plain HTTP only for an exact private/loopback destination with MFA; record this choice in redacted audit. Public HTTP, embedded URL credentials, fragments, query strings, and redirects are rejected. Private self-hosted addresses are valid. Validate every resolved address at connection time; reject unspecified, multicast, link-local (including metadata services) and mixed allowed/forbidden answers. Pin the validated connection address while retaining original hostname for TLS; custom trust uses an owner-installed CA, never skip verification. Endpoint path prefix is allowed only as normalized path without traversal. Do not permit redirect-based secret forwarding.

2xx means accepted, not received on a phone. 408/429/5xx, timeout and transport errors retry within plan limits; other 4xx fail permanently; 3xx fail without follow. Honor Retry-After up to expiry. Retry attempts use current enabled destination revision; changed revision cancels pending old work rather than forwarding old queued payload to a new topic. A response lost after acceptance may duplicate the remote message; show stable incident/transition identity.

Check suppression and recovery pause immediately before each send. Pause/revoke prevents new requests after acknowledgment; an already in-flight request may complete and is reported, not claimed recalled. Quiet-period delivery intents become suppressed; release uses one transactional summary batch per destination for entities whose suppression union ended in that evaluation cycle. Resolve-before-release yields no summary. A future recurrence remains eligible for normal notification.

## History extension

Extend existing `GET /devices/{id}/metrics` without removing 001 fields. Accept from/to, metric, entityId, maxPoints<=600 and an optional repeated seriesId (max 16). Unknown series returns an empty, explicit unavailable series, not substituted host totals.

Response adds seriesId/entityId, resolutionSeconds, and per-point count, coverage (0..1), partial; existing value/min/max/availability remain. Example: `{series:[{seriesId,entityId,metric,unit,resolutionSeconds,points:[{observedAt,value,min,max,availability,count,coverage,partial}]}]}`. Numeric fields are null when no data; partial buckets are availability=unavailable with partial=true, while min/max/value remain inspectable as partial statistics. UI never joins a healthy line across these buckets.

Choose source tier by oldest requested time (raw <=30d; five-minute <=90d; hourly <=365d, subject to actual retained data), then group into UTC-aligned integer multiples of source resolution to meet maxPoints, including partial first/last buckets. Count total intersecting buckets when choosing multiplier. Entire requested span is returned including empty buckets. Tiers may be present in overlapping ages but are never summed twice. Queries outside retention show unavailable periods, never fabricated backfill. Long-range grouping reports partial coverage rather than promising sub-bucket gap locations.

## Delivery relevance and retention confirmation

Before sending an initial trigger, verify the incident is still active. If it resolved before any accepted trigger, cancel the stale trigger and omit recovery delivery. A recovery is eligible only where that destination accepted the trigger or a summary containing the incident. Acknowledged active incidents still appear in suppression-release summaries. Administrative closure never emits a healthy/recovered message. When an uncertain prior network attempt prevents knowing remote receipt, use recorded acceptance as the decision boundary and retain the uncertainty diagnostic.

A retention preview binds current settings revision and the exact proposed tier values. PATCH must match an unexpired preview whenever existing retention is reduced; otherwise return 409. Preview counts are estimates, but affected age ranges and the possibility of irreversible deletion are explicit. Audit the confirmed change. This preview is a future product flow, not permission to delete data during spec authoring.
