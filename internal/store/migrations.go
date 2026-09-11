package store

import (
	"context"
	"database/sql"
	"fmt"
)

type migration struct {
	version int
	sql     string
}

func RunMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS scout_schema_migrations (version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM scout_schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read migration version: %w", err)
	}
	for _, item := range migrations() {
		if item.version <= current {
			continue
		}
		if _, err := tx.ExecContext(ctx, item.sql); err != nil {
			return fmt.Errorf("apply migration %d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO scout_schema_migrations(version) VALUES ($1)`, item.version); err != nil {
			return fmt.Errorf("record migration %d: %w", item.version, err)
		}
	}
	return tx.Commit()
}

func migrations() []migration {
	return []migration{
		{version: 1, sql: initialMigrationSQL},
		{version: 2, sql: monitoringMigrationSQL},
		{version: 3, sql: alertRulesMigrationSQL},
		{version: 4, sql: incidentsMigrationSQL},
		{version: 5, sql: alertConditionFieldsMigrationSQL},
	}
}

const initialMigrationSQL = `CREATE TABLE IF NOT EXISTS owners (
  id text PRIMARY KEY,
  singleton boolean NOT NULL DEFAULT true UNIQUE,
  password_hash text NOT NULL,
  totp_ciphertext bytea,
  totp_nonce bytea,
  totp_key_version integer,
  recovery_code_hashes jsonb NOT NULL DEFAULT '[]'::jsonb,
  created_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS owners_singleton_idx ON owners (singleton) WHERE singleton;
CREATE TABLE IF NOT EXISTS owner_sessions (
  token_hash text PRIMARY KEY, csrf_hash text NOT NULL, owner_id text NOT NULL REFERENCES owners(id), created_at timestamptz NOT NULL,
  last_seen timestamptz NOT NULL, absolute_expiry timestamptz NOT NULL, recent_mfa_at timestamptz, revoked_at timestamptz
);
CREATE TABLE IF NOT EXISTS sites (id text PRIMARY KEY, name text NOT NULL, address_context text NOT NULL DEFAULT '', created_at timestamptz NOT NULL);
CREATE TABLE IF NOT EXISTS scopes (
  id text PRIMARY KEY, site_id text NOT NULL REFERENCES sites(id), ranges jsonb NOT NULL, exclusions jsonb NOT NULL DEFAULT '[]'::jsonb,
  allowed_methods jsonb NOT NULL, ports jsonb NOT NULL, credential_ref text, trust_ref text, limits jsonb NOT NULL DEFAULT '{}'::jsonb,
  enabled boolean NOT NULL DEFAULT false, revision bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS exclusions (id text PRIMARY KEY, scope_id text REFERENCES scopes(id), site_id text REFERENCES sites(id), device_id text, matcher text NOT NULL, reason text NOT NULL, created_at timestamptz NOT NULL);
CREATE TABLE IF NOT EXISTS devices (id text PRIMARY KEY, site_id text, display_name text NOT NULL, platform text NOT NULL DEFAULT 'unknown', architecture text NOT NULL DEFAULT 'unknown', lifecycle text NOT NULL DEFAULT 'candidate', created_at timestamptz NOT NULL, decommissioned_at timestamptz);
CREATE TABLE IF NOT EXISTS device_identifiers (device_id text NOT NULL REFERENCES devices(id), kind text NOT NULL, namespace text NOT NULL DEFAULT '', value text NOT NULL, source text NOT NULL, confidence double precision NOT NULL, first_seen timestamptz NOT NULL, last_seen timestamptz NOT NULL, PRIMARY KEY (device_id, kind, namespace, value));
CREATE TABLE IF NOT EXISTS agent_identities (id text PRIMARY KEY, device_id text NOT NULL REFERENCES devices(id), public_key_hash text NOT NULL, cert_serial text NOT NULL UNIQUE, auth_token_hash text, certificate_pem text NOT NULL, expires_at timestamptz NOT NULL, revoked_at timestamptz, installed_version text NOT NULL DEFAULT '');
CREATE UNIQUE INDEX IF NOT EXISTS active_agent_per_device ON agent_identities(device_id) WHERE revoked_at IS NULL;
CREATE TABLE IF NOT EXISTS bootstrap_invitations (token_hash text PRIMARY KEY, device_id text NOT NULL REFERENCES devices(id), enrollment_job_id text, expires_at timestamptz NOT NULL, consumed_at timestamptz, revoked_at timestamptz);
CREATE TABLE IF NOT EXISTS credential_refs (id text PRIMARY KEY, kind text NOT NULL, endpoint text NOT NULL DEFAULT '', allowed_use jsonb NOT NULL, targets jsonb NOT NULL, ciphertext bytea NOT NULL, nonce bytea NOT NULL, wrapped_data_key bytea NOT NULL, key_version integer NOT NULL, metadata jsonb NOT NULL DEFAULT '{}'::jsonb, revision bigint NOT NULL DEFAULT 1, revoked_at timestamptz);
CREATE TABLE IF NOT EXISTS trust_records (id text PRIMARY KEY, scope_id text, endpoint text NOT NULL, host text NOT NULL, fingerprint text NOT NULL, public_key text, revision bigint NOT NULL DEFAULT 1, owner_established_at timestamptz NOT NULL, revoked_at timestamptz);
CREATE TABLE IF NOT EXISTS access_requests (id text PRIMARY KEY, device_id text NOT NULL REFERENCES devices(id), scope_id text, reason_code text NOT NULL, safe_details jsonb NOT NULL, state text NOT NULL, last_attempt timestamptz NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS unresolved_access_request ON access_requests(device_id, reason_code) WHERE state = 'open';
CREATE TABLE IF NOT EXISTS jobs (id text PRIMARY KEY, kind text NOT NULL, device_id text NOT NULL REFERENCES devices(id), scope_revision bigint NOT NULL, credential_version bigint NOT NULL DEFAULT 0, release_id text, state text NOT NULL, lease_owner text, epoch bigint NOT NULL DEFAULT 0, lease_expiry timestamptz, attempts integer NOT NULL DEFAULT 0, next_attempt timestamptz NOT NULL, deadline timestamptz, result jsonb NOT NULL DEFAULT '{}'::jsonb);
CREATE UNIQUE INDEX IF NOT EXISTS active_enrollment_per_device ON jobs(device_id) WHERE kind = 'enrollment' AND state IN ('queued','claimed','connecting','installing','verifying','pausing');
CREATE TABLE IF NOT EXISTS batch_receipts (agent_id text NOT NULL REFERENCES agent_identities(id), boot_id text NOT NULL, batch_id text NOT NULL, payload_hash text NOT NULL, accepted_at timestamptz NOT NULL, PRIMARY KEY(agent_id, boot_id, batch_id));
CREATE TABLE IF NOT EXISTS metric_samples (id text NOT NULL, device_id text NOT NULL REFERENCES devices(id), agent_id text NOT NULL REFERENCES agent_identities(id), collector_id text NOT NULL, entity_id text NOT NULL, metric text NOT NULL, labels jsonb NOT NULL DEFAULT '{}'::jsonb, value double precision, availability text NOT NULL, unit text NOT NULL, observed_at timestamptz NOT NULL, received_at timestamptz NOT NULL, PRIMARY KEY(id, received_at)) PARTITION BY RANGE(received_at);
CREATE TABLE IF NOT EXISTS metric_samples_default PARTITION OF metric_samples DEFAULT;
CREATE INDEX IF NOT EXISTS metric_samples_device_time_idx ON metric_samples(device_id, observed_at DESC);
CREATE TABLE IF NOT EXISTS observations (id text PRIMARY KEY, reporter_id text NOT NULL, collector_id text NOT NULL, subject_id text NOT NULL, kind text NOT NULL, payload jsonb NOT NULL, observed_at timestamptz NOT NULL, received_at timestamptz NOT NULL, expires_at timestamptz NOT NULL, confidence double precision NOT NULL);
CREATE TABLE IF NOT EXISTS relationships (id text PRIMARY KEY, from_entity text NOT NULL, to_entity text NOT NULL, type text NOT NULL, confidence double precision NOT NULL, projection_revision bigint NOT NULL, evidence_ids jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS collectors (id text PRIMARY KEY, device_id text NOT NULL REFERENCES devices(id), provider text NOT NULL, schema_version integer NOT NULL, config jsonb NOT NULL DEFAULT '{}'::jsonb, credential_ref text, state text NOT NULL, last_success timestamptz, diagnostic text);
CREATE TABLE IF NOT EXISTS service_entities (id text PRIMARY KEY, provider text NOT NULL, cluster_namespace text NOT NULL, external_id text NOT NULL, kind text NOT NULL, owner_host text NOT NULL, associated_device text, UNIQUE(provider, cluster_namespace, external_id));
CREATE TABLE IF NOT EXISTS collection_leases (provider text NOT NULL, cluster_namespace text NOT NULL, owner_agent text NOT NULL, epoch bigint NOT NULL, expiry timestamptz NOT NULL, PRIMARY KEY(provider, cluster_namespace));
CREATE TABLE IF NOT EXISTS releases (id text PRIMARY KEY, manifest_hash text NOT NULL UNIQUE, version text NOT NULL, generation bigint NOT NULL, platform text NOT NULL, architecture text NOT NULL, digest text NOT NULL, bytes bigint NOT NULL, trust_key_id text NOT NULL, immutable_blob_path text NOT NULL, manifest jsonb NOT NULL, revoked_at timestamptz);
CREATE TABLE IF NOT EXISTS rollouts (id text PRIMARY KEY, policy jsonb NOT NULL, window_config jsonb NOT NULL, canaries integer NOT NULL, concurrency integer NOT NULL, failure_threshold integer NOT NULL, paused boolean NOT NULL DEFAULT false, revision bigint NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS assignments (id text PRIMARY KEY, rollout_id text, device_id text NOT NULL REFERENCES devices(id), desired_release text NOT NULL, generation bigint NOT NULL, expires_at timestamptz NOT NULL, state text NOT NULL, UNIQUE(device_id));
CREATE TABLE IF NOT EXISTS audit_events (id text PRIMARY KEY, actor_kind text NOT NULL, actor_id text NOT NULL, action text NOT NULL, target text NOT NULL, event_time timestamptz NOT NULL, redacted_outcome jsonb NOT NULL, request_id text NOT NULL);
CREATE INDEX IF NOT EXISTS audit_events_time_idx ON audit_events(event_time DESC);
CREATE TABLE IF NOT EXISTS workspace_state (singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton), recovery_mode boolean NOT NULL DEFAULT false, enrollment_paused boolean NOT NULL DEFAULT false, updates_paused boolean NOT NULL DEFAULT false, schema_version integer NOT NULL DEFAULT 1, state_json jsonb NOT NULL DEFAULT '{}'::jsonb, updated_at timestamptz NOT NULL DEFAULT now());
INSERT INTO workspace_state(singleton, state_json) VALUES(true, '{}'::jsonb) ON CONFLICT(singleton) DO NOTHING;`

const monitoringMigrationSQL = `
CREATE TABLE IF NOT EXISTS monitoring_storage_state (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  storage_generation bigint NOT NULL DEFAULT 0 CHECK (storage_generation >= 0),
  migration_generation bigint NOT NULL DEFAULT 0 CHECK (migration_generation >= 0),
  phase text NOT NULL DEFAULT 'legacy' CHECK (phase IN ('legacy', 'importing', 'authoritative')),
  legacy_state_hash text NOT NULL DEFAULT '',
  legacy_sample_count bigint NOT NULL DEFAULT 0 CHECK (legacy_sample_count >= 0),
  normalized_sample_count bigint NOT NULL DEFAULT 0 CHECK (normalized_sample_count >= 0),
  legacy_receipt_count bigint NOT NULL DEFAULT 0 CHECK (legacy_receipt_count >= 0),
  normalized_receipt_count bigint NOT NULL DEFAULT 0 CHECK (normalized_receipt_count >= 0),
  legacy_observation_count bigint NOT NULL DEFAULT 0 CHECK (legacy_observation_count >= 0),
  normalized_observation_count bigint NOT NULL DEFAULT 0 CHECK (normalized_observation_count >= 0),
  parity_checked_at timestamptz,
  cutover_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (migration_generation)
);
INSERT INTO monitoring_storage_state(singleton) VALUES (true) ON CONFLICT (singleton) DO NOTHING;

CREATE TABLE IF NOT EXISTS monitoring_migration_checkpoints (
  migration_generation bigint NOT NULL REFERENCES monitoring_storage_state(migration_generation),
  stream text NOT NULL CHECK (stream IN ('devices', 'agents', 'receipts', 'samples', 'observations')),
  next_index bigint NOT NULL DEFAULT 0 CHECK (next_index >= 0),
  completed boolean NOT NULL DEFAULT false,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (migration_generation, stream)
);

CREATE TABLE IF NOT EXISTS metric_series (
  id text PRIMARY KEY,
  device_id text NOT NULL REFERENCES devices(id),
  collector_id text NOT NULL,
  entity_id text NOT NULL,
  metric text NOT NULL,
  unit text NOT NULL,
  labels jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(labels) = 'object'),
  interval_seconds integer NOT NULL DEFAULT 0 CHECK (interval_seconds >= 0),
  first_seen timestamptz NOT NULL,
  last_seen timestamptz NOT NULL,
  storage_generation bigint NOT NULL DEFAULT 0 CHECK (storage_generation >= 0),
  UNIQUE (device_id, collector_id, entity_id, metric, unit, labels)
);
CREATE INDEX IF NOT EXISTS metric_series_device_metric_idx ON metric_series(device_id, metric, entity_id);

ALTER TABLE metric_samples ADD COLUMN IF NOT EXISTS series_id text;
ALTER TABLE metric_samples ADD COLUMN IF NOT EXISTS boot_id text;
ALTER TABLE metric_samples ADD COLUMN IF NOT EXISTS batch_id text;
ALTER TABLE metric_samples ADD COLUMN IF NOT EXISTS batch_ordinal integer;
ALTER TABLE metric_samples ADD COLUMN IF NOT EXISTS storage_generation bigint NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS metric_samples_series_time_idx ON metric_samples(series_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS metric_samples_batch_ordinal_idx
  ON metric_samples(agent_id, boot_id, batch_id, batch_ordinal, received_at)
  WHERE boot_id IS NOT NULL AND batch_id IS NOT NULL AND batch_ordinal IS NOT NULL;

CREATE TABLE IF NOT EXISTS telemetry_receipts (
  agent_id text NOT NULL REFERENCES agent_identities(id),
  boot_id text NOT NULL,
  batch_id text NOT NULL,
  payload_hash text NOT NULL,
  accepted_at timestamptz NOT NULL,
  storage_generation bigint NOT NULL DEFAULT 0 CHECK (storage_generation >= 0),
  PRIMARY KEY (agent_id, boot_id, batch_id)
);
CREATE TABLE IF NOT EXISTS telemetry_sample_ordinals (
  agent_id text NOT NULL REFERENCES agent_identities(id),
  boot_id text NOT NULL,
  batch_id text NOT NULL,
  batch_ordinal integer NOT NULL CHECK (batch_ordinal >= 0),
  sample_id text NOT NULL,
  received_at timestamptz NOT NULL,
  PRIMARY KEY (agent_id, boot_id, batch_id, batch_ordinal)
);

ALTER TABLE observations ADD COLUMN IF NOT EXISTS storage_generation bigint NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS observations_subject_time_idx ON observations(subject_id, observed_at DESC);

CREATE TABLE IF NOT EXISTS current_series (
  series_id text PRIMARY KEY,
  sample_id text NOT NULL,
  observed_at timestamptz NOT NULL,
  received_at timestamptz NOT NULL,
  value double precision,
  availability text NOT NULL,
  storage_generation bigint NOT NULL DEFAULT 0 CHECK (storage_generation >= 0)
);

CREATE TABLE IF NOT EXISTS metric_aggregates (
  series_id text NOT NULL,
  resolution_seconds integer NOT NULL CHECK (resolution_seconds IN (300, 3600)),
  bucket_start timestamptz NOT NULL,
  count bigint NOT NULL CHECK (count >= 0),
  sum double precision,
  min double precision,
  max double precision,
  expected_count bigint NOT NULL DEFAULT 0 CHECK (expected_count >= 0),
  covered_seconds integer NOT NULL DEFAULT 0 CHECK (covered_seconds >= 0),
  bucket_seconds integer NOT NULL CHECK (bucket_seconds IN (300, 3600)),
  partial boolean NOT NULL DEFAULT false,
  generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
  PRIMARY KEY (series_id, resolution_seconds, bucket_start)
);

CREATE TABLE IF NOT EXISTS rollup_work (
  series_id text NOT NULL,
  resolution_seconds integer NOT NULL CHECK (resolution_seconds IN (300, 3600)),
  bucket_start timestamptz NOT NULL,
  dirty_generation bigint NOT NULL CHECK (dirty_generation >= 0),
  lease_epoch bigint NOT NULL DEFAULT 0 CHECK (lease_epoch >= 0),
  lease_until timestamptz,
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error text,
  PRIMARY KEY (series_id, resolution_seconds, bucket_start)
);
CREATE INDEX IF NOT EXISTS rollup_work_ready_idx ON rollup_work(resolution_seconds, bucket_start, lease_until);
`

const alertRulesMigrationSQL = `
CREATE TABLE IF NOT EXISTS alert_rules (
  id text PRIMARY KEY,
  template_key text UNIQUE,
  name text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('numeric', 'state')),
  metric text NOT NULL DEFAULT '',
  entity_id text NOT NULL DEFAULT '',
  operator text NOT NULL DEFAULT '',
  trigger_value double precision,
  clear_value double precision,
  trigger_state text NOT NULL DEFAULT '',
  clear_state text NOT NULL DEFAULT '',
  trigger_seconds integer NOT NULL DEFAULT 0 CHECK (trigger_seconds >= 0 AND trigger_seconds <= 86400),
  clear_seconds integer NOT NULL DEFAULT 0 CHECK (clear_seconds >= 0 AND clear_seconds <= 86400),
  minimum_consecutive_samples integer NOT NULL DEFAULT 1 CHECK (minimum_consecutive_samples >= 1 AND minimum_consecutive_samples <= 10),
  severity text NOT NULL CHECK (severity IN ('warning', 'critical')),
  target_kind text NOT NULL CHECK (target_kind IN ('fleet', 'site', 'device')),
  target_id text NOT NULL DEFAULT '',
  enabled boolean NOT NULL DEFAULT true,
  revision bigint NOT NULL DEFAULT 1 CHECK (revision >= 1),
  retired_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS alert_rules_target_idx ON alert_rules(target_kind, target_id, enabled, retired_at);

CREATE TABLE IF NOT EXISTS alert_overrides (
  id text PRIMARY KEY,
  lineage_id text NOT NULL REFERENCES alert_rules(id),
  target_kind text NOT NULL CHECK (target_kind IN ('site', 'device')),
  target_id text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('numeric', 'state')),
  metric text NOT NULL DEFAULT '',
  entity_id text NOT NULL DEFAULT '',
  operator text NOT NULL DEFAULT '',
  trigger_value double precision,
  clear_value double precision,
  trigger_state text NOT NULL DEFAULT '',
  clear_state text NOT NULL DEFAULT '',
  trigger_seconds integer NOT NULL DEFAULT 0 CHECK (trigger_seconds >= 0 AND trigger_seconds <= 86400),
  clear_seconds integer NOT NULL DEFAULT 0 CHECK (clear_seconds >= 0 AND clear_seconds <= 86400),
  minimum_consecutive_samples integer NOT NULL DEFAULT 1 CHECK (minimum_consecutive_samples >= 1 AND minimum_consecutive_samples <= 10),
  severity text NOT NULL CHECK (severity IN ('warning', 'critical')),
  enabled boolean NOT NULL DEFAULT true,
  revision bigint NOT NULL DEFAULT 1 CHECK (revision >= 1),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (lineage_id, target_kind, target_id)
);
CREATE INDEX IF NOT EXISTS alert_overrides_target_idx ON alert_overrides(target_kind, target_id);
`

const incidentsMigrationSQL = `
CREATE TABLE IF NOT EXISTS alert_evaluations (
  lineage_id text NOT NULL,
  entity_id text NOT NULL,
  effective_revision bigint NOT NULL DEFAULT 0 CHECK (effective_revision >= 0),
  evidence_state text NOT NULL CHECK (evidence_state IN ('fresh', 'unknown', 'unsupported')),
  last_observed_at timestamptz,
  last_received_at timestamptz,
  pending_since timestamptz,
  recovery_since timestamptz,
  last_valid_at timestamptz,
  trigger_consecutive integer NOT NULL DEFAULT 0 CHECK (trigger_consecutive >= 0),
  recovery_consecutive integer NOT NULL DEFAULT 0 CHECK (recovery_consecutive >= 0),
  incident_id text,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (lineage_id, entity_id)
);

CREATE TABLE IF NOT EXISTS incidents (
  id text PRIMARY KEY,
  lineage_id text NOT NULL,
  entity_id text NOT NULL,
  device_id text NOT NULL DEFAULT '',
  rule_revision bigint NOT NULL DEFAULT 0 CHECK (rule_revision >= 0),
  rule_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
  severity text NOT NULL CHECK (severity IN ('warning', 'critical')),
  status text NOT NULL CHECK (status IN ('active', 'resolved', 'closed')),
  evidence_state text NOT NULL CHECK (evidence_state IN ('fresh', 'unknown', 'unsupported')),
  value double precision,
  unit text NOT NULL DEFAULT '',
  source text NOT NULL DEFAULT '',
  opened_at timestamptz NOT NULL,
  observed_at timestamptz NOT NULL,
  evaluated_at timestamptz NOT NULL,
  acknowledged_at timestamptz,
  acknowledged_by text NOT NULL DEFAULT '',
  closed_at timestamptz,
  close_reason text NOT NULL DEFAULT '',
  revision bigint NOT NULL DEFAULT 1 CHECK (revision >= 1)
);
CREATE UNIQUE INDEX IF NOT EXISTS active_incident_lineage_entity_idx ON incidents(lineage_id, entity_id) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS incidents_status_time_idx ON incidents(status, evaluated_at DESC, id);

CREATE TABLE IF NOT EXISTS incident_transitions (
  id text PRIMARY KEY,
  incident_id text NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
  sequence bigint NOT NULL CHECK (sequence >= 1),
  kind text NOT NULL,
  actor text NOT NULL DEFAULT '',
  evidence_state text NOT NULL CHECK (evidence_state IN ('fresh', 'unknown', 'unsupported')),
  reason text NOT NULL DEFAULT '',
  value double precision,
  observed_at timestamptz,
  occurred_at timestamptz NOT NULL,
  rule_revision bigint NOT NULL DEFAULT 0 CHECK (rule_revision >= 0),
  UNIQUE (incident_id, sequence)
);
CREATE INDEX IF NOT EXISTS incident_transitions_incident_idx ON incident_transitions(incident_id, sequence);

CREATE TABLE IF NOT EXISTS alert_work (
  lineage_id text NOT NULL,
  entity_id text NOT NULL,
  dirty_generation bigint NOT NULL DEFAULT 1 CHECK (dirty_generation >= 1),
  lease_epoch bigint NOT NULL DEFAULT 0 CHECK (lease_epoch >= 0),
  lease_owner text NOT NULL DEFAULT '',
  lease_until timestamptz,
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (lineage_id, entity_id)
);
CREATE INDEX IF NOT EXISTS alert_work_ready_idx ON alert_work(lease_until, updated_at, lineage_id, entity_id);
`

const alertConditionFieldsMigrationSQL = `
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS service_pattern text NOT NULL DEFAULT '';
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS collector_id text NOT NULL DEFAULT '';
ALTER TABLE alert_overrides ADD COLUMN IF NOT EXISTS service_pattern text NOT NULL DEFAULT '';
ALTER TABLE alert_overrides ADD COLUMN IF NOT EXISTS collector_id text NOT NULL DEFAULT '';
`
