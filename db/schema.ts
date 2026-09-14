import { relations } from "drizzle-orm";
import { integer, sqliteTable, text, uniqueIndex, index } from "drizzle-orm/sqlite-core";

export const user = sqliteTable("user", {
  id: text("id").primaryKey(),
  name: text("name").notNull(),
  email: text("email").notNull().unique(),
  emailVerified: integer("email_verified", { mode: "boolean" }).notNull().default(false),
  image: text("image"),
  username: text("username").unique(),
  displayUsername: text("display_username"),
  twoFactorEnabled: integer("two_factor_enabled", { mode: "boolean" }).notNull().default(false),
  createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
  updatedAt: integer("updated_at", { mode: "timestamp_ms" }).notNull(),
});

export const session = sqliteTable(
  "session",
  {
    id: text("id").primaryKey(),
    expiresAt: integer("expires_at", { mode: "timestamp_ms" }).notNull(),
    token: text("token").notNull().unique(),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
    updatedAt: integer("updated_at", { mode: "timestamp_ms" }).notNull(),
    ipAddress: text("ip_address"),
    userAgent: text("user_agent"),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
  },
  (table) => [index("session_user_idx").on(table.userId)],
);

export const account = sqliteTable(
  "account",
  {
    id: text("id").primaryKey(),
    accountId: text("account_id").notNull(),
    providerId: text("provider_id").notNull(),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
    accessToken: text("access_token"),
    refreshToken: text("refresh_token"),
    idToken: text("id_token"),
    accessTokenExpiresAt: integer("access_token_expires_at", { mode: "timestamp_ms" }),
    refreshTokenExpiresAt: integer("refresh_token_expires_at", { mode: "timestamp_ms" }),
    scope: text("scope"),
    password: text("password"),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
    updatedAt: integer("updated_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [index("account_user_idx").on(table.userId)],
);

export const verification = sqliteTable("verification", {
  id: text("id").primaryKey(),
  identifier: text("identifier").notNull(),
  value: text("value").notNull(),
  expiresAt: integer("expires_at", { mode: "timestamp_ms" }).notNull(),
  createdAt: integer("created_at", { mode: "timestamp_ms" }),
  updatedAt: integer("updated_at", { mode: "timestamp_ms" }),
});

export const twoFactor = sqliteTable(
  "two_factor",
  {
    id: text("id").primaryKey(),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
    secret: text("secret").notNull(),
    backupCodes: text("backup_codes").notNull(),
    verified: integer("verified", { mode: "boolean" }).notNull().default(true),
    failedVerificationCount: integer("failed_verification_count").notNull().default(0),
    lockedUntil: integer("locked_until", { mode: "timestamp_ms" }),
  },
  (table) => [index("two_factor_user_idx").on(table.userId)],
);

export const appSettings = sqliteTable("app_setting", {
  key: text("key").primaryKey(),
  value: text("value").notNull(),
  updatedAt: integer("updated_at", { mode: "timestamp_ms" }).notNull(),
});

export const setupTokens = sqliteTable("setup_token", {
  id: text("id").primaryKey(),
  tokenHash: text("token_hash").notNull().unique(),
  expiresAt: integer("expires_at", { mode: "timestamp_ms" }).notNull(),
  consumedAt: integer("consumed_at", { mode: "timestamp_ms" }),
  createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
});

export const networkSegments = sqliteTable(
  "network_segment",
  {
    id: text("id").primaryKey(),
    siteKey: text("site_key").notNull(),
    provenanceKey: text("provenance_key").notNull(),
    cidr: text("cidr").notNull(),
    source: text("source").notNull(),
    paused: integer("paused", { mode: "boolean" }).notNull().default(false),
    policyVersion: integer("policy_version").notNull().default(1),
    lastScanAt: integer("last_scan_at", { mode: "timestamp_ms" }),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
    updatedAt: integer("updated_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [uniqueIndex("segment_provenance_idx").on(table.siteKey, table.provenanceKey)],
);

export const systems = sqliteTable(
  "system",
  {
    id: text("id").primaryKey(),
    segmentId: text("segment_id").references(() => networkSegments.id, { onDelete: "set null" }),
    displayName: text("display_name").notNull(),
    hostname: text("hostname"),
    status: text("status").notNull().default("needs-access"),
    excluded: integer("excluded", { mode: "boolean" }).notNull().default(false),
    lastSeenAt: integer("last_seen_at", { mode: "timestamp_ms" }),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
    updatedAt: integer("updated_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [
    index("system_status_idx").on(table.status),
    index("system_segment_idx").on(table.segmentId),
  ],
);

export const systemAliases = sqliteTable(
  "system_alias",
  {
    sourceSystemId: text("source_system_id")
      .primaryKey()
      .references(() => systems.id, { onDelete: "cascade" }),
    canonicalSystemId: text("canonical_system_id")
      .notNull()
      .references(() => systems.id, { onDelete: "cascade" }),
    reason: text("reason").notNull(),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [index("system_alias_canonical_idx").on(table.canonicalSystemId)],
);

export const agentInvitations = sqliteTable(
  "agent_invitation",
  {
    id: text("id").primaryKey(),
    systemId: text("system_id")
      .notNull()
      .references(() => systems.id, { onDelete: "cascade" }),
    tokenHash: text("token_hash").notNull().unique(),
    publicKey: text("public_key"),
    expiresAt: integer("expires_at", { mode: "timestamp_ms" }).notNull(),
    consumedAt: integer("consumed_at", { mode: "timestamp_ms" }),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [index("invitation_system_idx").on(table.systemId)],
);

export const systemAddresses = sqliteTable(
  "system_address",
  {
    id: text("id").primaryKey(),
    systemId: text("system_id")
      .notNull()
      .references(() => systems.id, { onDelete: "cascade" }),
    address: text("address").notNull(),
    port: integer("port").notNull(),
    lastSeenAt: integer("last_seen_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [uniqueIndex("system_address_idx").on(table.systemId, table.address, table.port)],
);

export const accessEvidence = sqliteTable(
  "access_evidence",
  {
    id: text("id").primaryKey(),
    systemId: text("system_id")
      .notNull()
      .references(() => systems.id, { onDelete: "cascade" }),
    method: text("method").notNull(),
    address: text("address").notNull(),
    port: integer("port").notNull(),
    outcome: text("outcome").notNull(),
    fingerprint: text("fingerprint"),
    macAddress: text("mac_address"),
    source: text("source").notNull(),
    observedAt: integer("observed_at", { mode: "timestamp_ms" }).notNull(),
    expiresAt: integer("expires_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [
    uniqueIndex("access_evidence_endpoint_idx").on(
      table.systemId,
      table.method,
      table.address,
      table.port,
    ),
  ],
);

export const trustedHostKeys = sqliteTable(
  "trusted_host_key",
  {
    id: text("id").primaryKey(),
    systemId: text("system_id")
      .notNull()
      .references(() => systems.id, { onDelete: "cascade" }),
    method: text("method").notNull(),
    fingerprint: text("fingerprint").notNull(),
    acceptedAt: integer("accepted_at", { mode: "timestamp_ms" }).notNull(),
    revokedAt: integer("revoked_at", { mode: "timestamp_ms" }),
  },
  (table) => [index("trusted_key_system_idx").on(table.systemId)],
);

export const credentials = sqliteTable(
  "credential_grant",
  {
    id: text("id").primaryKey(),
    systemId: text("system_id")
      .notNull()
      .references(() => systems.id, { onDelete: "cascade" }),
    method: text("method").notNull(),
    username: text("username").notNull(),
    secretCiphertext: text("secret_ciphertext").notNull(),
    nonce: text("nonce").notNull(),
    scope: text("scope").notNull().default("exact-host"),
    scopeSegmentId: text("scope_segment_id").references(() => networkSegments.id, {
      onDelete: "set null",
    }),
    automaticEnrollment: integer("automatic_enrollment", { mode: "boolean" })
      .notNull()
      .default(false),
    firstSeenKeyPinning: integer("first_seen_key_pinning", { mode: "boolean" })
      .notNull()
      .default(false),
    enabled: integer("enabled", { mode: "boolean" }).notNull().default(true),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
    updatedAt: integer("updated_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [index("credential_system_idx").on(table.systemId)],
);

export const agents = sqliteTable(
  "agent",
  {
    id: text("id").primaryKey(),
    systemId: text("system_id")
      .notNull()
      .references(() => systems.id, { onDelete: "cascade" }),
    publicKey: text("public_key").notNull(),
    platform: text("platform").notNull(),
    architecture: text("architecture").notNull(),
    version: text("version").notNull(),
    revokedAt: integer("revoked_at", { mode: "timestamp_ms" }),
    lastHeartbeatAt: integer("last_heartbeat_at", { mode: "timestamp_ms" }),
    lastTelemetryAt: integer("last_telemetry_at", { mode: "timestamp_ms" }),
    taskGeneration: integer("task_generation").notNull().default(0),
    releaseSequence: integer("release_sequence").notNull().default(0),
    reconciliationRequired: integer("reconciliation_required", { mode: "boolean" })
      .notNull()
      .default(false),
    reconciledAt: integer("reconciled_at", { mode: "timestamp_ms" }),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
    updatedAt: integer("updated_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [
    uniqueIndex("agent_active_system_idx").on(table.systemId, table.revokedAt),
    uniqueIndex("agent_public_key_idx").on(table.publicKey),
  ],
);

export const enrollmentJobs = sqliteTable(
  "enrollment_job",
  {
    id: text("id").primaryKey(),
    systemId: text("system_id")
      .notNull()
      .references(() => systems.id, { onDelete: "cascade" }),
    credentialId: text("credential_id").references(() => credentials.id, { onDelete: "set null" }),
    status: text("status").notNull().default("queued"),
    stage: text("stage").notNull().default("queued"),
    errorCode: text("error_code"),
    errorMessage: text("error_message"),
    leaseOwner: text("lease_owner"),
    leaseExpiresAt: integer("lease_expires_at", { mode: "timestamp_ms" }),
    attempt: integer("attempt").notNull().default(0),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
    updatedAt: integer("updated_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [index("enrollment_system_idx").on(table.systemId, table.status)],
);

export const idempotencyReceipts = sqliteTable(
  "idempotency_receipt",
  {
    key: text("key").primaryKey(),
    operation: text("operation").notNull(),
    scopeId: text("scope_id").notNull().default(""),
    resourceId: text("resource_id").notNull(),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [index("idempotency_operation_idx").on(table.operation, table.resourceId)],
);

export const scanTasks = sqliteTable(
  "scan_task",
  {
    id: text("id").primaryKey(),
    segmentId: text("segment_id")
      .notNull()
      .references(() => networkSegments.id, { onDelete: "cascade" }),
    scannerAgentId: text("scanner_agent_id").references(() => agents.id, { onDelete: "set null" }),
    kind: text("kind").notNull().default("network-scan"),
    generation: integer("generation").notNull(),
    policyVersion: integer("policy_version").notNull(),
    payload: text("payload").notNull().default("{}"),
    signature: text("signature").notNull().default(""),
    issuedAt: integer("issued_at", { mode: "timestamp_ms" }).notNull(),
    status: text("status").notNull().default("queued"),
    deadlineAt: integer("deadline_at", { mode: "timestamp_ms" }).notNull(),
    leaseExpiresAt: integer("lease_expires_at", { mode: "timestamp_ms" }),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
    completedAt: integer("completed_at", { mode: "timestamp_ms" }),
  },
  (table) => [uniqueIndex("scan_generation_idx").on(table.segmentId, table.generation)],
);

export const relayChannels = sqliteTable(
  "relay_channel",
  {
    id: text("id").primaryKey(),
    agentId: text("agent_id")
      .notNull()
      .references(() => agents.id, { onDelete: "cascade" }),
    targetSystemId: text("target_system_id")
      .notNull()
      .references(() => systems.id, { onDelete: "cascade" }),
    targetAddress: text("target_address").notNull(),
    targetPort: integer("target_port").notNull(),
    nonce: text("nonce").notNull(),
    expiresAt: integer("expires_at", { mode: "timestamp_ms" }).notNull(),
    upstreamSignature: text("upstream_signature"),
    downstreamSignature: text("downstream_signature"),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [
    index("relay_agent_idx").on(table.agentId),
    index("relay_expiry_idx").on(table.expiresAt),
  ],
);

export const telemetrySamples = sqliteTable(
  "telemetry_sample",
  {
    id: text("id").primaryKey(),
    agentId: text("agent_id")
      .notNull()
      .references(() => agents.id, { onDelete: "cascade" }),
    batchId: text("batch_id").notNull(),
    observedAt: integer("observed_at", { mode: "timestamp_ms" }).notNull(),
    payload: text("payload").notNull(),
    receivedAt: integer("received_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [uniqueIndex("telemetry_batch_idx").on(table.agentId, table.batchId)],
);

export const telemetryRollups = sqliteTable(
  "telemetry_rollup",
  {
    id: text("id").primaryKey(),
    agentId: text("agent_id")
      .notNull()
      .references(() => agents.id, { onDelete: "cascade" }),
    bucketStart: integer("bucket_start", { mode: "timestamp_ms" }).notNull(),
    payload: text("payload").notNull(),
  },
  (table) => [uniqueIndex("telemetry_bucket_idx").on(table.agentId, table.bucketStart)],
);

export const auditEvents = sqliteTable(
  "audit_event",
  {
    id: text("id").primaryKey(),
    action: text("action").notNull(),
    resourceType: text("resource_type").notNull(),
    resourceId: text("resource_id"),
    metadata: text("metadata"),
    createdAt: integer("created_at", { mode: "timestamp_ms" }).notNull(),
  },
  (table) => [index("audit_created_idx").on(table.createdAt)],
);

export const userRelations = relations(user, ({ many }) => ({
  sessions: many(session),
  accounts: many(account),
}));

export const systemRelations = relations(systems, ({ many, one }) => ({
  segment: one(networkSegments, { fields: [systems.segmentId], references: [networkSegments.id] }),
  addresses: many(systemAddresses),
  accessEvidence: many(accessEvidence),
  credentials: many(credentials),
  agents: many(agents),
  enrollmentJobs: many(enrollmentJobs),
  invitations: many(agentInvitations),
}));

export const agentRelations = relations(agents, ({ one, many }) => ({
  system: one(systems, { fields: [agents.systemId], references: [systems.id] }),
  samples: many(telemetrySamples),
  rollups: many(telemetryRollups),
}));

export const schema = {
  user,
  session,
  account,
  verification,
  twoFactor,
  appSettings,
  setupTokens,
  agentInvitations,
  networkSegments,
  systems,
  systemAliases,
  systemAddresses,
  accessEvidence,
  trustedHostKeys,
  credentials,
  agents,
  enrollmentJobs,
  idempotencyReceipts,
  scanTasks,
  relayChannels,
  telemetrySamples,
  telemetryRollups,
  auditEvents,
};
