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
	return []migration{{version: 1, sql: initialMigrationSQL}}
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
