import { randomUUID } from "node:crypto";

import type Database from "better-sqlite3";

const MIGRATIONS: ReadonlyArray<{ version: number; sql: string }> = [
  {
    version: 1,
    sql: `
      CREATE TABLE IF NOT EXISTS user (
        id TEXT PRIMARY KEY NOT NULL,
        name TEXT NOT NULL,
        email TEXT NOT NULL UNIQUE,
        email_verified INTEGER NOT NULL DEFAULT 0,
        image TEXT,
        username TEXT UNIQUE,
        display_username TEXT,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE TABLE IF NOT EXISTS session (
        id TEXT PRIMARY KEY NOT NULL,
        expires_at INTEGER NOT NULL,
        token TEXT NOT NULL UNIQUE,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL,
        ip_address TEXT,
        user_agent TEXT,
        user_id TEXT NOT NULL REFERENCES user(id) ON DELETE CASCADE
      );
      CREATE TABLE IF NOT EXISTS account (
        id TEXT PRIMARY KEY NOT NULL,
        account_id TEXT NOT NULL,
        provider_id TEXT NOT NULL,
        user_id TEXT NOT NULL REFERENCES user(id) ON DELETE CASCADE,
        access_token TEXT,
        refresh_token TEXT,
        id_token TEXT,
        access_token_expires_at INTEGER,
        refresh_token_expires_at INTEGER,
        scope TEXT,
        password TEXT,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE TABLE IF NOT EXISTS verification (
        id TEXT PRIMARY KEY NOT NULL,
        identifier TEXT NOT NULL,
        value TEXT NOT NULL,
        expires_at INTEGER NOT NULL,
        created_at INTEGER,
        updated_at INTEGER
      );
      CREATE TABLE IF NOT EXISTS app_setting (
        key TEXT PRIMARY KEY NOT NULL,
        value TEXT NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE TABLE IF NOT EXISTS setup_token (
        id TEXT PRIMARY KEY NOT NULL,
        token_hash TEXT NOT NULL UNIQUE,
        expires_at INTEGER NOT NULL,
        consumed_at INTEGER,
        created_at INTEGER NOT NULL
      );
      CREATE TABLE IF NOT EXISTS network_segment (
        id TEXT PRIMARY KEY NOT NULL,
        site_key TEXT NOT NULL,
        provenance_key TEXT NOT NULL,
        cidr TEXT NOT NULL,
        source TEXT NOT NULL,
        paused INTEGER NOT NULL DEFAULT 0,
        policy_version INTEGER NOT NULL DEFAULT 1,
        last_scan_at INTEGER,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL,
        UNIQUE(site_key, provenance_key)
      );
      CREATE TABLE IF NOT EXISTS system (
        id TEXT PRIMARY KEY NOT NULL,
        segment_id TEXT REFERENCES network_segment(id) ON DELETE SET NULL,
        display_name TEXT NOT NULL,
        hostname TEXT,
        status TEXT NOT NULL DEFAULT 'needs-access',
        excluded INTEGER NOT NULL DEFAULT 0,
        last_seen_at INTEGER,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE TABLE IF NOT EXISTS system_address (
        id TEXT PRIMARY KEY NOT NULL,
        system_id TEXT NOT NULL REFERENCES system(id) ON DELETE CASCADE,
        address TEXT NOT NULL,
        port INTEGER NOT NULL,
        last_seen_at INTEGER NOT NULL,
        UNIQUE(system_id, address, port)
      );
      CREATE TABLE IF NOT EXISTS access_evidence (
        id TEXT PRIMARY KEY NOT NULL,
        system_id TEXT NOT NULL REFERENCES system(id) ON DELETE CASCADE,
        method TEXT NOT NULL,
        address TEXT NOT NULL,
        port INTEGER NOT NULL,
        outcome TEXT NOT NULL,
        fingerprint TEXT,
        mac_address TEXT,
        source TEXT NOT NULL,
        observed_at INTEGER NOT NULL,
        expires_at INTEGER NOT NULL,
        UNIQUE(system_id, method, address, port)
      );
      CREATE TABLE IF NOT EXISTS trusted_host_key (
        id TEXT PRIMARY KEY NOT NULL,
        system_id TEXT NOT NULL REFERENCES system(id) ON DELETE CASCADE,
        method TEXT NOT NULL,
        fingerprint TEXT NOT NULL,
        accepted_at INTEGER NOT NULL,
        revoked_at INTEGER
      );
      CREATE TABLE IF NOT EXISTS credential_grant (
        id TEXT PRIMARY KEY NOT NULL,
        system_id TEXT NOT NULL REFERENCES system(id) ON DELETE CASCADE,
        method TEXT NOT NULL,
        username TEXT NOT NULL,
        secret_ciphertext TEXT NOT NULL,
        nonce TEXT NOT NULL,
        scope TEXT NOT NULL DEFAULT 'exact-host',
        enabled INTEGER NOT NULL DEFAULT 1,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE TABLE IF NOT EXISTS agent (
        id TEXT PRIMARY KEY NOT NULL,
        system_id TEXT NOT NULL REFERENCES system(id) ON DELETE CASCADE,
        public_key TEXT NOT NULL UNIQUE,
        platform TEXT NOT NULL,
        architecture TEXT NOT NULL,
        version TEXT NOT NULL,
        revoked_at INTEGER,
        last_heartbeat_at INTEGER,
        last_telemetry_at INTEGER,
        task_generation INTEGER NOT NULL DEFAULT 0,
        release_sequence INTEGER NOT NULL DEFAULT 0,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE UNIQUE INDEX IF NOT EXISTS agent_active_system_idx ON agent(system_id) WHERE revoked_at IS NULL;
      CREATE TABLE IF NOT EXISTS enrollment_job (
        id TEXT PRIMARY KEY NOT NULL,
        system_id TEXT NOT NULL REFERENCES system(id) ON DELETE CASCADE,
        credential_id TEXT REFERENCES credential_grant(id) ON DELETE SET NULL,
        status TEXT NOT NULL DEFAULT 'queued',
        stage TEXT NOT NULL DEFAULT 'queued',
        error_code TEXT,
        error_message TEXT,
        lease_owner TEXT,
        lease_expires_at INTEGER,
        attempt INTEGER NOT NULL DEFAULT 0,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE TABLE IF NOT EXISTS idempotency_receipt (
        key TEXT PRIMARY KEY NOT NULL,
        operation TEXT NOT NULL,
        resource_id TEXT NOT NULL,
        created_at INTEGER NOT NULL
      );
      CREATE TABLE IF NOT EXISTS scan_task (
        id TEXT PRIMARY KEY NOT NULL,
        segment_id TEXT NOT NULL REFERENCES network_segment(id) ON DELETE CASCADE,
        scanner_agent_id TEXT REFERENCES agent(id) ON DELETE SET NULL,
        generation INTEGER NOT NULL,
        policy_version INTEGER NOT NULL,
        status TEXT NOT NULL DEFAULT 'queued',
        deadline_at INTEGER NOT NULL,
        lease_expires_at INTEGER,
        created_at INTEGER NOT NULL,
        completed_at INTEGER,
        UNIQUE(segment_id, generation)
      );
      CREATE TABLE IF NOT EXISTS telemetry_sample (
        id TEXT PRIMARY KEY NOT NULL,
        agent_id TEXT NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
        batch_id TEXT NOT NULL,
        observed_at INTEGER NOT NULL,
        payload TEXT NOT NULL,
        received_at INTEGER NOT NULL,
        UNIQUE(agent_id, batch_id)
      );
      CREATE TABLE IF NOT EXISTS telemetry_rollup (
        id TEXT PRIMARY KEY NOT NULL,
        agent_id TEXT NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
        bucket_start INTEGER NOT NULL,
        payload TEXT NOT NULL,
        UNIQUE(agent_id, bucket_start)
      );
      CREATE TABLE IF NOT EXISTS audit_event (
        id TEXT PRIMARY KEY NOT NULL,
        action TEXT NOT NULL,
        resource_type TEXT NOT NULL,
        resource_id TEXT,
        metadata TEXT,
        created_at INTEGER NOT NULL
      );
      CREATE TABLE IF NOT EXISTS schema_migration (
        version INTEGER PRIMARY KEY NOT NULL,
        applied_at INTEGER NOT NULL
      );
    `,
  },
  {
    version: 2,
    sql: `
      CREATE TABLE IF NOT EXISTS agent_invitation (
        id TEXT PRIMARY KEY NOT NULL,
        system_id TEXT NOT NULL REFERENCES system(id) ON DELETE CASCADE,
        token_hash TEXT NOT NULL UNIQUE,
        public_key TEXT,
        expires_at INTEGER NOT NULL,
        consumed_at INTEGER,
        created_at INTEGER NOT NULL
      );
      CREATE INDEX IF NOT EXISTS invitation_system_idx ON agent_invitation(system_id);
    `,
  },
  {
    version: 3,
    sql: `
      ALTER TABLE scan_task ADD COLUMN payload TEXT NOT NULL DEFAULT '{}';
      ALTER TABLE scan_task ADD COLUMN signature TEXT NOT NULL DEFAULT '';
      ALTER TABLE scan_task ADD COLUMN issued_at INTEGER NOT NULL DEFAULT 0;
    `,
  },
  {
    version: 4,
    sql: `
      CREATE TABLE IF NOT EXISTS system_alias (
        source_system_id TEXT PRIMARY KEY NOT NULL REFERENCES system(id) ON DELETE CASCADE,
        canonical_system_id TEXT NOT NULL REFERENCES system(id) ON DELETE CASCADE,
        reason TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        CHECK (source_system_id <> canonical_system_id)
      );
      CREATE INDEX IF NOT EXISTS system_alias_canonical_idx ON system_alias(canonical_system_id);
    `,
  },
  {
    version: 5,
    sql: `
      ALTER TABLE idempotency_receipt ADD COLUMN scope_id TEXT NOT NULL DEFAULT '';
      CREATE INDEX IF NOT EXISTS idempotency_scope_idx ON idempotency_receipt(scope_id);
    `,
  },
];

export function migrateDatabase(sqlite: Database.Database): void {
  sqlite.pragma("foreign_keys = ON");
  sqlite.pragma("journal_mode = WAL");
  sqlite.pragma("synchronous = NORMAL");
  sqlite.pragma("busy_timeout = 5000");
  // Next may initialize more than one server worker during a production
  // build. IMMEDIATE takes the SQLite write lock before reading the applied
  // versions so only one initializer can run the migration decision.
  sqlite.exec("BEGIN IMMEDIATE");
  try {
    sqlite.exec(
      "CREATE TABLE IF NOT EXISTS schema_migration (version INTEGER PRIMARY KEY NOT NULL, applied_at INTEGER NOT NULL)",
    );

    const applied = new Set(
      (
        sqlite.prepare("SELECT version FROM schema_migration ORDER BY version").all() as Array<{
          version: number;
        }>
      ).map((row) => row.version),
    );

    for (const migration of MIGRATIONS) {
      if (applied.has(migration.version)) {
        continue;
      }

      sqlite.exec(migration.sql);
      sqlite
        .prepare("INSERT INTO schema_migration (version, applied_at) VALUES (?, ?)")
        .run(migration.version, Date.now());
    }
    sqlite.exec("COMMIT");
  } catch (error) {
    sqlite.exec("ROLLBACK");
    throw error;
  }
}

export function makeId(): string {
  return randomUUID();
}
