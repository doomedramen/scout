# Data Model

Normative additions to 001. IDs remain UUIDs; times UTC; revision changes use compare-and-swap. JSON configuration is schema-validated and bounded. All tables below are SQL-backed in production, not extensions to the serialized sample array.

| Entity/table | Fields and keys | Invariants |
| --- | --- | --- |
| metric_series | id; device_id, collector_id, entity_id, metric, unit; canonical permitted labels; interval_seconds; first/last_seen | Unique full identity including canonical labels; mutable display labels stay in entity metadata, not series keys. Replaced hardware gets a new entity. |
| metric_samples | existing ID/device/agent/time fields plus series_id; value; availability | Preserve 001 receipt-time partitioning; unique batch sample ordinal prevents replay; index series_id + observed_at. Availability governs nullable value. |
| telemetry_receipts | agent_id, boot_id, batch_id, hash, accepted_at | Unique triple, hash conflict rejected. Retain at least through maximum accepted replay window; expired old batches rejected, not re-ingested. |
| current_series | series_id PK; sample_id; observed_at; received_at; value; availability | Strictly newer eligible observations update current value; entity-level availability remains separate. |
| alert_rules | id=lineage_id; template_key nullable unique; name; kind; metric/state selector; comparison; trigger/clear values and seconds; minimum_consecutive_samples; severity; target_kind/id; enabled; revision; retired_at | No executable expressions. Numeric > or < with opposite clear comparator and non-overlapping thresholds; state predicates from catalog. |
| alert_overrides | id; lineage_id; target_kind/id; replacement condition/timing/severity/enabled; revision | Unique lineage+target; target narrower than base rule; device > site > fleet. |
| alert_evaluations | lineage_id, entity_id PK; effective_revision_hash; evidence_time; pending_since; recovery_since; last_valid_time; consecutive state counters; incident_id nullable; evidence_state | Lock row for transitions; evidence_state=fresh/unknown/unsupported; unavailable samples reset timers, not active episode. |
| incidents | id; lineage_id; entity_id; device_id; effective rule snapshot; status; severity; opened_at; observed_at; acknowledged_at/actor; closed_at; close_reason; evidence snapshot; revision | Partial unique lineage+entity where status=active. status active/resolved/closed; acknowledgment independent. Evidence includes value/unit/threshold/source/time, no arbitrary payloads. |
| incident_transitions | id; incident_id; sequence; kind; occurred_at; actor; bounded evidence; effective_revision | Unique incident+sequence; append-only; initial trigger, acknowledgment, unknown/fresh changes, recovered, administrative close. |
| notification_destinations | id; name; base_url; encrypted_topic_ref; token_ref nullable; enabled; revision; last_test_at | Topic treated as protected routing information; reads return masked topic. Exact configured endpoint only. |
| notification_deliveries | id; destination_id/revision; transition_id nullable; summary_key nullable; status; attempts; next_attempt_at; expires_at; lease_epoch/until; accepted_at; remote_id nullable; sanitized_error | Unique destination+transition or destination+summary_key. queued/sending/retry/accepted/failed/cancelled/suppressed/expired. Payload derived from bounded safe snapshots. |
| suppression_windows | id; name; target_kind/id; mode; timezone; weekdays; start/end local times OR starts_at/ends_at; enabled; revision | Recurring and absolute schemas exclusive; end-exclusive; overlapping union semantics. |
| suppression_episodes | destination_id, entity_id, epoch PK; started_at; ended_at; reason bits; summary_batch_id | Track transitions into/out of aggregate suppression, not individual overlapping windows; prevent repeated end summaries across restart. |
| metric_aggregates | series_id, resolution_seconds, bucket_start PK; count; sum; min; max; expected_count; covered_seconds; bucket_seconds; partial; generation | UTC-aligned 300s/3600s buckets; no average-of-averages; empty bucket has count=0, null extrema/mean. |
| rollup_work | series_id, resolution, bucket_start PK; dirty_generation; lease; attempts; last_error | Recompute buckets idempotently; hourly rows aggregate five-minute rows. Delete only when coverage generation committed. |
| monitoring_settings | singleton; defaults_version; notifications_paused; retention tiers; disk budget; migration_generation; counters | External delivery starts disabled and restore overrides it to paused. |

## Transaction and lifecycle rules

- Ingestion consumes a receipt and stores samples/current projection/dirty evaluation and rollup work atomically. Counter reset remains explicit unavailability. The memory store must emulate transaction rollback on error but cannot replace SQL integration tests.
- Evaluation locks its state, selects current effective policy, writes incident plus transition and delivery intents in one transaction. Initial insert races resolve via uniqueness and retry, never two incidents.
- Delivery claims bounded work with row locking and an epoch, commits before network I/O, and stores result only under the same lease. An uncertain network result can cause duplicate external messages on retry; stable incident IDs make them recognizable. Do not claim exactly-once ntfy delivery.
- Incident acknowledgment does not stop recovery evaluation or alter severity. Closed/resolved episodes are immutable except retention and linked metadata; recurrence creates a new ID.
- Decommission/retirement closes affected episodes with a non-recovery reason and cancels related pending sends. An entity missing from a complete collector inventory twice is retired; partial/failed enumeration cannot retire entities. Record a disappearance transition first; do not claim healthy.
- Destinations/windows/rules audit writes using 001 redaction. Rule retirement retains incident snapshots. Revoking a secret prevents future sends even when delivery is already queued.
- Backup includes all tables, settings, encrypted destination material, and migration/rollup checkpoints. Key recovery remains separate. Restore resets worker leases and applies the plan's no-replay policy.

## Retention and bounded growth

Time-based deletion applies only to closed episodes and terminal delivery records. Active incidents at their cap reject new admission with an observable counter; do not delete old active faults. Default transitions per incident capped at 1000; coalesce repeated unknown/fresh changes after that cap into a counted diagnostic while preserving trigger, acknowledgment, and close transitions. No unbounded error text (maximum 512 UTF-8 bytes); evidence <=4 KiB. Control-plane entities/exclusions inherit 001 retention and cannot disappear with historical samples.
