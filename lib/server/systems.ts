import type { Database as SqliteDatabase } from "better-sqlite3";

import { getDatabase } from "@/lib/server/db";
import { resolveSystemId } from "@/lib/server/system-identity";

export const SYSTEM_STATES = [
  "needs-access",
  "needs-trust",
  "installing",
  "online",
  "stale",
  "offline",
  "blocked",
  "revoked",
  "excluded",
] as const;

export type SystemState = (typeof SYSTEM_STATES)[number];

export type SystemSummary = {
  id: string;
  displayName: string;
  hostname: string | null;
  status: SystemState;
  lastSeenAt: string | null;
  addresses: string[];
  macAddresses: string[];
  accessMethods: string[];
  agent: {
    id: string;
    platform: string;
    architecture: string;
    version: string;
    lastHeartbeatAt: string | null;
    lastTelemetryAt: string | null;
  } | null;
};

export type FleetSnapshot = {
  generatedAt: string;
  summary: {
    monitored: number;
    online: number;
    stale: number;
    offline: number;
    needsAccess: number;
  };
  systems: SystemSummary[];
};

type SystemRow = {
  id: string;
  displayName: string;
  hostname: string | null;
  storedStatus: string;
  lastSeenAt: number | null;
  addressList: string | null;
  macAddressList: string | null;
  accessMethodList: string | null;
  agentId: string | null;
  platform: string | null;
  architecture: string | null;
  version: string | null;
  lastHeartbeatAt: number | null;
  lastTelemetryAt: number | null;
};

type DetailRow = SystemRow & {
  segmentId: string | null;
  excluded: number;
};

const HEARTBEAT_TIMEOUT_MS = 45_000;
const STALE_TELEMETRY_MS = 90_000;

function asDate(value: number | null): string | null {
  return value === null ? null : new Date(value).toISOString();
}

function asState(value: string): SystemState {
  return SYSTEM_STATES.includes(value as SystemState) ? (value as SystemState) : "blocked";
}

function effectiveState(row: SystemRow, now: number): SystemState {
  if (row.agentId) {
    if (!row.lastHeartbeatAt || row.lastHeartbeatAt <= now - HEARTBEAT_TIMEOUT_MS) return "offline";
    if (!row.lastTelemetryAt || row.lastTelemetryAt <= now - STALE_TELEMETRY_MS) return "stale";
    return "online";
  }

  return asState(row.storedStatus);
}

function splitList(value: string | null): string[] {
  return value ? value.split(",").filter(Boolean) : [];
}

function toSummary(row: SystemRow, now: number): SystemSummary {
  const agent = row.agentId
    ? {
        id: row.agentId,
        platform: row.platform ?? "unknown",
        architecture: row.architecture ?? "unknown",
        version: row.version ?? "unknown",
        lastHeartbeatAt: asDate(row.lastHeartbeatAt),
        lastTelemetryAt: asDate(row.lastTelemetryAt),
      }
    : null;

  return {
    id: row.id,
    displayName: row.displayName,
    hostname: row.hostname,
    status: effectiveState(row, now),
    lastSeenAt: asDate(row.lastSeenAt),
    addresses: splitList(row.addressList),
    macAddresses: splitList(row.macAddressList),
    accessMethods: splitList(row.accessMethodList),
    agent,
  };
}

function fleetRows(sqlite: SqliteDatabase, now: number): SystemRow[] {
  return sqlite
    .prepare(
      `
        SELECT
          s.id,
          s.display_name AS displayName,
          s.hostname,
          s.status AS storedStatus,
          s.last_seen_at AS lastSeenAt,
          GROUP_CONCAT(DISTINCT sa.address || ':' || sa.port) AS addressList,
          GROUP_CONCAT(DISTINCT ae.mac_address) AS macAddressList,
          GROUP_CONCAT(DISTINCT CASE WHEN ae.outcome = 'open' AND ae.expires_at > ? THEN ae.method END) AS accessMethodList,
          a.id AS agentId,
          a.platform,
          a.architecture,
          a.version,
          a.last_heartbeat_at AS lastHeartbeatAt,
          a.last_telemetry_at AS lastTelemetryAt
        FROM system s
        LEFT JOIN system_address sa ON sa.system_id = s.id
        LEFT JOIN access_evidence ae ON ae.system_id = s.id
        LEFT JOIN agent a ON a.system_id = s.id AND a.revoked_at IS NULL
        WHERE s.excluded = 0
          AND NOT EXISTS (
            SELECT 1 FROM system_alias alias WHERE alias.source_system_id = s.id
          )
          AND (
            a.id IS NOT NULL
            OR EXISTS (
              SELECT 1 FROM access_evidence current_evidence
              WHERE current_evidence.system_id = s.id
                AND current_evidence.outcome = 'open'
                AND current_evidence.expires_at > ?
            )
            OR s.status IN ('installing', 'needs-trust', 'blocked', 'revoked')
          )
        GROUP BY s.id
        ORDER BY CASE WHEN a.id IS NULL THEN 1 ELSE 0 END, lower(s.display_name), s.id
      `,
    )
    .all(now, now) as SystemRow[];
}

export function getFleetSnapshot(now = Date.now()): FleetSnapshot {
  const { sqlite } = getDatabase();
  const systems = fleetRows(sqlite, now).map((row) => toSummary(row, now));
  const monitored = systems.filter((system) => system.agent !== null);

  return {
    generatedAt: new Date(now).toISOString(),
    summary: {
      monitored: monitored.length,
      online: systems.filter((system) => system.status === "online").length,
      stale: systems.filter((system) => system.status === "stale").length,
      offline: systems.filter((system) => system.status === "offline").length,
      needsAccess: systems.filter((system) => system.status === "needs-access").length,
    },
    systems,
  };
}

export type SystemDetails = SystemSummary & {
  segmentId: string | null;
  excluded: boolean;
  evidence: Array<{
    method: string;
    address: string;
    port: number;
    outcome: string;
    fingerprint: string | null;
    source: string;
    observedAt: string;
    expiresAt: string;
    current: boolean;
  }>;
  trustedKeys: Array<{ method: string; fingerprint: string; acceptedAt: string }>;
  credentialMethods: string[];
  enrollment: {
    id: string;
    status: string;
    stage: string;
    errorCode: string | null;
    errorMessage: string | null;
    attempt: number;
    createdAt: string;
    updatedAt: string;
    leaseExpiresAt: string | null;
    target: string | null;
  } | null;
};

export function getSystemDetails(systemId: string, now = Date.now()): SystemDetails | null {
  const { sqlite } = getDatabase();
  const canonicalId = resolveSystemId(sqlite, systemId);
  const row = sqlite
    .prepare(
      `
        SELECT
          s.id,
          s.display_name AS displayName,
          s.hostname,
          s.status AS storedStatus,
          s.segment_id AS segmentId,
          s.excluded,
          s.last_seen_at AS lastSeenAt,
          GROUP_CONCAT(DISTINCT sa.address || ':' || sa.port) AS addressList,
          GROUP_CONCAT(DISTINCT ae.mac_address) AS macAddressList,
          GROUP_CONCAT(DISTINCT CASE WHEN ae.outcome = 'open' AND ae.expires_at > ? THEN ae.method END) AS accessMethodList,
          a.id AS agentId,
          a.platform,
          a.architecture,
          a.version,
          a.last_heartbeat_at AS lastHeartbeatAt,
          a.last_telemetry_at AS lastTelemetryAt
        FROM system s
        LEFT JOIN system_address sa ON sa.system_id = s.id
        LEFT JOIN access_evidence ae ON ae.system_id = s.id
        LEFT JOIN agent a ON a.system_id = s.id AND a.revoked_at IS NULL
        WHERE s.id = ?
        GROUP BY s.id
      `,
    )
    .get(now, canonicalId) as DetailRow | undefined;
  if (!row) return null;

  const evidence = sqlite
    .prepare(
      `
        SELECT method, address, port, outcome, fingerprint, source,
          observed_at AS observedAt, expires_at AS expiresAt
        FROM access_evidence
        WHERE system_id = ?
        ORDER BY observed_at DESC
      `,
    )
    .all(canonicalId) as Array<{
    method: string;
    address: string;
    port: number;
    outcome: string;
    fingerprint: string | null;
    source: string;
    observedAt: number;
    expiresAt: number;
  }>;
  const trustedKeys = sqlite
    .prepare(
      "SELECT method, fingerprint, accepted_at AS acceptedAt FROM trusted_host_key WHERE system_id = ? AND revoked_at IS NULL ORDER BY accepted_at DESC",
    )
    .all(canonicalId) as Array<{ method: string; fingerprint: string; acceptedAt: number }>;
  const credentialMethods = sqlite
    .prepare(
      "SELECT DISTINCT method FROM credential_grant WHERE system_id = ? AND enabled = 1 ORDER BY method",
    )
    .all(canonicalId) as Array<{ method: string }>;
  const enrollment = sqlite
    .prepare(
      `
        SELECT id, status, stage, error_code AS errorCode, error_message AS errorMessage,
          attempt, created_at AS createdAt, updated_at AS updatedAt, lease_expires_at AS leaseExpiresAt
        FROM enrollment_job
        WHERE system_id = ?
        ORDER BY created_at DESC
        LIMIT 1
      `,
    )
    .get(canonicalId) as
    | {
        id: string;
        status: string;
        stage: string;
        errorCode: string | null;
        errorMessage: string | null;
        attempt: number;
        createdAt: number;
        updatedAt: number;
        leaseExpiresAt: number | null;
      }
    | undefined;
  const enrollmentTarget = enrollment
    ? (sqlite
        .prepare(
          "SELECT address, port FROM access_evidence WHERE system_id = ? AND method = 'ssh' AND outcome = 'open' ORDER BY observed_at DESC LIMIT 1",
        )
        .get(canonicalId) as { address: string; port: number } | undefined)
    : undefined;
  const summary = toSummary(row, now);

  return {
    ...summary,
    segmentId: row.segmentId,
    excluded: row.excluded === 1,
    evidence: evidence.map((item) => ({
      ...item,
      observedAt: new Date(item.observedAt).toISOString(),
      expiresAt: new Date(item.expiresAt).toISOString(),
      current: item.outcome === "open" && item.expiresAt > now,
    })),
    trustedKeys: trustedKeys.map((item) => ({
      ...item,
      acceptedAt: new Date(item.acceptedAt).toISOString(),
    })),
    credentialMethods: credentialMethods.map((item) => item.method),
    enrollment: enrollment
      ? {
          ...enrollment,
          createdAt: new Date(enrollment.createdAt).toISOString(),
          updatedAt: new Date(enrollment.updatedAt).toISOString(),
          leaseExpiresAt: enrollment.leaseExpiresAt
            ? new Date(enrollment.leaseExpiresAt).toISOString()
            : null,
          target: enrollmentTarget
            ? `${enrollmentTarget.address}:${enrollmentTarget.port}`
            : (summary.addresses[0] ?? null),
        }
      : null,
  };
}

export function getSystemMetrics(systemId: string, now = Date.now()) {
  const { sqlite } = getDatabase();
  const canonicalId = resolveSystemId(sqlite, systemId);
  const samples = sqlite
    .prepare(
      `
        SELECT ts.observed_at AS observedAt, ts.payload
        FROM telemetry_sample ts
        INNER JOIN agent a ON a.id = ts.agent_id
        WHERE a.system_id = ? AND ts.observed_at > ?
        ORDER BY ts.observed_at ASC
      `,
    )
    .all(canonicalId, now - 24 * 60 * 60 * 1000) as Array<{ observedAt: number; payload: string }>;
  return {
    systemId: canonicalId,
    samples: samples.map((sample) => ({
      observedAt: new Date(sample.observedAt).toISOString(),
      payload: JSON.parse(sample.payload),
    })),
  };
}
