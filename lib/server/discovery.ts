import { randomUUID } from "node:crypto";

import { getDatabase } from "@/lib/server/db";
import { inferDefaultRoute, type NetworkBoundary } from "@/lib/discovery/network";
import { scanSegment, type ProbeResult } from "@/lib/discovery/scanner";
import { ownerExists } from "@/lib/server/setup";
import { authorityPaused } from "@/lib/server/settings";
import {
  equivalentSegmentProvenance,
  mergeSystems,
  resolveSystemId,
  segmentMetadata,
} from "@/lib/server/system-identity";

const EVIDENCE_TTL_MS = 30 * 60 * 1000;
const SCAN_INTERVAL_MS = 10 * 60 * 1000;

let discoveryRun: Promise<DiscoveryState> | undefined;

export type DiscoveryState = {
  status: "idle" | "scanning" | "current" | "needs-input" | "error";
  cidr: string | null;
  lastScanAt: string | null;
  reason: string | null;
};

export type SegmentRecord = {
  id: string;
  cidr: string;
  provenanceKey: string;
  lastScanAt: number | null;
};

function setting(key: string): string | null {
  const { sqlite } = getDatabase();
  return (
    (
      sqlite.prepare("SELECT value FROM app_setting WHERE key = ?").get(key) as
        { value: string } | undefined
    )?.value ?? null
  );
}

function setSetting(key: string, value: string, now: number): void {
  const { sqlite } = getDatabase();
  sqlite
    .prepare(
      `
        INSERT INTO app_setting (key, value, updated_at) VALUES (?, ?, ?)
        ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
      `,
    )
    .run(key, value, now);
}

function siteKey(now: number): string {
  const existing = setting("site_key");
  if (existing) return existing;
  const value = randomUUID();
  setSetting("site_key", value, now);
  return value;
}

export function getDiscoveryState(): DiscoveryState {
  const value = setting("discovery_state");
  if (!value) return { status: "idle", cidr: null, lastScanAt: null, reason: null };
  try {
    return JSON.parse(value) as DiscoveryState;
  } catch {
    return {
      status: "error",
      cidr: null,
      lastScanAt: null,
      reason: "Discovery state is unreadable.",
    };
  }
}

function setDiscoveryState(state: DiscoveryState, now: number): void {
  setSetting("discovery_state", JSON.stringify(state), now);
}

export function ensureNetworkSegment(
  boundary: NetworkBoundary,
  now = Date.now(),
  source: "default-route" | "manual" = "default-route",
): SegmentRecord {
  const { sqlite } = getDatabase();
  const key = siteKey(now);
  const existing = sqlite
    .prepare(
      "SELECT id, cidr, provenance_key AS provenanceKey, last_scan_at AS lastScanAt FROM network_segment WHERE site_key = ? AND provenance_key = ?",
    )
    .get(key, boundary.provenanceKey) as SegmentRecord | undefined;

  const equivalent = existing
    ? undefined
    : (sqlite
        .prepare(
          "SELECT id, cidr, provenance_key AS provenanceKey, last_scan_at AS lastScanAt FROM network_segment WHERE site_key = ? AND cidr = ? AND source = ? ORDER BY created_at ASC LIMIT 1",
        )
        .get(key, boundary.cidr, source === "manual" ? "default-route" : "manual") as
        SegmentRecord | undefined);

  if (existing || equivalent) {
    const segment = existing ?? equivalent!;
    sqlite
      .prepare("UPDATE network_segment SET cidr = ?, updated_at = ? WHERE id = ?")
      .run(boundary.cidr, now, segment.id);
    return { ...segment, cidr: boundary.cidr };
  }

  const id = randomUUID();
  sqlite
    .prepare(
      "INSERT INTO network_segment (id, site_key, provenance_key, cidr, source, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
    )
    .run(id, key, boundary.provenanceKey, boundary.cidr, source, now, now);
  return { id, cidr: boundary.cidr, provenanceKey: boundary.provenanceKey, lastScanAt: null };
}

function reconcileOne(
  sqlite: ReturnType<typeof getDatabase>["sqlite"],
  segmentId: string,
  result: ProbeResult,
  now: number,
  source: "server-scan" | "agent-scan",
): void {
  const candidates = endpointCandidates(sqlite, result.address, result.port);
  const compatible = candidates.filter((candidate) =>
    isCompatibleCandidate(sqlite, candidate, segmentId, result),
  );

  if (result.outcome === "open" && result.fingerprint) {
    for (const candidate of candidates) {
      if (hasConflictingFingerprint(candidate, result.fingerprint)) {
        quarantineEndpoint(sqlite, candidate.id, result.address, result.port, now);
      }
    }
  }

  let selected = compatible;
  if (result.outcome === "open" && selected.length === 0 && result.macAddress) {
    selected = sameMacCandidates(sqlite, segmentId, result.macAddress, now).filter((candidate) =>
      isCompatibleCandidate(sqlite, candidate, segmentId, result),
    );
  }

  if (result.outcome !== "open") {
    selected = candidates.filter((candidate) => candidate.segmentId === segmentId);
  }
  if (selected.length === 0 && result.outcome !== "open") return;

  const canonical = [...selected].sort(compareCandidates)[0];
  const systemId = canonical?.id ?? randomUUID();
  if (canonical) {
    for (const candidate of selected) {
      if (candidate.id !== canonical.id) {
        mergeSystems(
          sqlite,
          candidate.id,
          canonical.id,
          result.fingerprint ? "verified-ssh-identity" : "equivalent-network-provenance",
          now,
        );
      }
    }
  } else {
    sqlite
      .prepare(
        "INSERT INTO system (id, segment_id, display_name, status, created_at, updated_at, last_seen_at) VALUES (?, ?, ?, 'needs-access', ?, ?, ?)",
      )
      .run(systemId, segmentId, result.address, now, now, now);
  }

  sqlite
    .prepare(
      "INSERT INTO system_address (id, system_id, address, port, last_seen_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(system_id, address, port) DO UPDATE SET last_seen_at = excluded.last_seen_at",
    )
    .run(randomUUID(), systemId, result.address, result.port, now);

  sqlite
    .prepare(
      `
        INSERT INTO access_evidence
          (id, system_id, method, address, port, outcome, fingerprint, mac_address, source, observed_at, expires_at)
        VALUES (?, ?, 'ssh', ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(system_id, method, address, port) DO UPDATE SET
          outcome = excluded.outcome,
          fingerprint = COALESCE(excluded.fingerprint, access_evidence.fingerprint),
          mac_address = COALESCE(excluded.mac_address, access_evidence.mac_address),
          source = excluded.source,
          observed_at = excluded.observed_at,
          expires_at = excluded.expires_at
      `,
    )
    .run(
      randomUUID(),
      systemId,
      result.address,
      result.port,
      result.outcome,
      result.fingerprint ?? null,
      result.macAddress ?? null,
      source,
      now,
      result.outcome === "open" ? now + EVIDENCE_TTL_MS : now,
    );

  if (result.outcome === "open") {
    sqlite
      .prepare(
        "UPDATE system SET status = CASE WHEN EXISTS (SELECT 1 FROM agent WHERE system_id = system.id AND revoked_at IS NULL) THEN status ELSE 'needs-access' END, last_seen_at = ?, updated_at = ? WHERE id = ?",
      )
      .run(now, now, systemId);
  }
}

type EndpointCandidate = {
  id: string;
  segmentId: string | null;
  createdAt: number;
  hasAgent: number;
  hasCredential: number;
  hasTrustedKey: number;
  fingerprint: string | null;
  trustedFingerprint: string | null;
};

function endpointCandidates(
  sqlite: ReturnType<typeof getDatabase>["sqlite"],
  address: string,
  port: number,
): EndpointCandidate[] {
  return sqlite
    .prepare(
      `
        SELECT
          s.id,
          s.segment_id AS segmentId,
          s.created_at AS createdAt,
          EXISTS (SELECT 1 FROM agent a WHERE a.system_id = s.id AND a.revoked_at IS NULL) AS hasAgent,
          EXISTS (SELECT 1 FROM credential_grant cg WHERE cg.system_id = s.id AND cg.enabled = 1) AS hasCredential,
          EXISTS (SELECT 1 FROM trusted_host_key tk WHERE tk.system_id = s.id AND tk.revoked_at IS NULL) AS hasTrustedKey,
          (
            SELECT ae.fingerprint
            FROM access_evidence ae
            WHERE ae.system_id = s.id AND ae.method = 'ssh' AND ae.address = ? AND ae.port = ?
            ORDER BY ae.observed_at DESC
            LIMIT 1
          ) AS fingerprint,
          (
            SELECT tk.fingerprint
            FROM trusted_host_key tk
            WHERE tk.system_id = s.id AND tk.method = 'ssh' AND tk.revoked_at IS NULL
            ORDER BY tk.accepted_at DESC
            LIMIT 1
          ) AS trustedFingerprint
        FROM system s
        INNER JOIN system_address sa ON sa.system_id = s.id
        WHERE s.excluded = 0
          AND sa.address = ?
          AND sa.port = ?
          AND NOT EXISTS (
            SELECT 1 FROM system_alias alias WHERE alias.source_system_id = s.id
          )
      `,
    )
    .all(address, port, address, port) as EndpointCandidate[];
}

function sameMacCandidates(
  sqlite: ReturnType<typeof getDatabase>["sqlite"],
  segmentId: string,
  macAddress: string,
  now: number,
): EndpointCandidate[] {
  return sqlite
    .prepare(
      `
        SELECT DISTINCT
          s.id,
          s.segment_id AS segmentId,
          s.created_at AS createdAt,
          EXISTS (SELECT 1 FROM agent a WHERE a.system_id = s.id AND a.revoked_at IS NULL) AS hasAgent,
          EXISTS (SELECT 1 FROM credential_grant cg WHERE cg.system_id = s.id AND cg.enabled = 1) AS hasCredential,
          EXISTS (SELECT 1 FROM trusted_host_key tk WHERE tk.system_id = s.id AND tk.revoked_at IS NULL) AS hasTrustedKey,
          (
            SELECT ae2.fingerprint
            FROM access_evidence ae2
            WHERE ae2.system_id = s.id AND ae2.method = 'ssh'
            ORDER BY ae2.observed_at DESC
            LIMIT 1
          ) AS fingerprint,
          (
            SELECT tk.fingerprint
            FROM trusted_host_key tk
            WHERE tk.system_id = s.id AND tk.method = 'ssh' AND tk.revoked_at IS NULL
            ORDER BY tk.accepted_at DESC
            LIMIT 1
          ) AS trustedFingerprint
        FROM system s
        INNER JOIN access_evidence ae ON ae.system_id = s.id
        WHERE s.segment_id = ?
          AND s.excluded = 0
          AND ae.method = 'ssh'
          AND ae.mac_address = ?
          AND ae.outcome = 'open'
          AND ae.expires_at > ?
          AND NOT EXISTS (
            SELECT 1 FROM system_alias alias WHERE alias.source_system_id = s.id
          )
      `,
    )
    .all(segmentId, macAddress, now) as EndpointCandidate[];
}

function hasConflictingFingerprint(candidate: EndpointCandidate, fingerprint: string): boolean {
  return (
    (candidate.fingerprint !== null && candidate.fingerprint !== fingerprint) ||
    (candidate.trustedFingerprint !== null && candidate.trustedFingerprint !== fingerprint)
  );
}

function isCompatibleCandidate(
  sqlite: ReturnType<typeof getDatabase>["sqlite"],
  candidate: EndpointCandidate,
  segmentId: string,
  result: ProbeResult,
): boolean {
  if (result.outcome !== "open") return candidate.segmentId === segmentId;
  if (result.fingerprint && hasConflictingFingerprint(candidate, result.fingerprint)) return false;
  if (candidate.segmentId === segmentId) return true;
  if (equivalentSegmentProvenance(sqlite, candidate.segmentId, segmentId)) return true;
  if (!result.fingerprint) return false;

  const currentSegment = segmentMetadata(sqlite, segmentId);
  const candidateSegment = candidate.segmentId
    ? segmentMetadata(sqlite, candidate.segmentId)
    : undefined;
  if (!currentSegment || !candidateSegment || currentSegment.siteKey !== candidateSegment.siteKey)
    return false;
  return (
    candidate.fingerprint === result.fingerprint ||
    candidate.trustedFingerprint === result.fingerprint
  );
}

function compareCandidates(left: EndpointCandidate, right: EndpointCandidate): number {
  for (const field of ["hasAgent", "hasCredential", "hasTrustedKey"] as const) {
    if (left[field] !== right[field]) return right[field] - left[field];
  }
  if (left.createdAt !== right.createdAt) return left.createdAt - right.createdAt;
  return left.id.localeCompare(right.id);
}

function quarantineEndpoint(
  sqlite: ReturnType<typeof getDatabase>["sqlite"],
  systemId: string,
  address: string,
  port: number,
  now: number,
): void {
  const canonicalId = resolveSystemId(sqlite, systemId);
  sqlite
    .prepare(
      "UPDATE access_evidence SET outcome = 'quarantined', expires_at = ?, observed_at = ? WHERE system_id = ? AND method = 'ssh' AND address = ? AND port = ?",
    )
    .run(now, now, canonicalId, address, port);
  sqlite
    .prepare("UPDATE system SET status = 'blocked', updated_at = ? WHERE id = ?")
    .run(now, canonicalId);
}

export function reconcileScanResults(
  segmentId: string,
  results: ProbeResult[],
  now = Date.now(),
  source: "server-scan" | "agent-scan" = "server-scan",
): void {
  const { sqlite } = getDatabase();
  const reconcile = sqlite.transaction(() => {
    for (const result of results) reconcileOne(sqlite, segmentId, result, now, source);
    sqlite
      .prepare("UPDATE network_segment SET last_scan_at = ?, updated_at = ? WHERE id = ?")
      .run(now, now, segmentId);
  });
  reconcile();
}

export async function discoverNow(
  options: {
    boundary?: NetworkBoundary;
    force?: boolean;
    source?: "default-route" | "manual";
  } = {},
): Promise<DiscoveryState> {
  const current = getDiscoveryState();
  if (!options.force && current.status === "scanning") return current;

  const inference = options.boundary
    ? { kind: "ready" as const, boundary: options.boundary }
    : inferDefaultRoute();
  if (inference.kind === "needs-input") {
    const next = {
      status: "needs-input" as const,
      cidr: null,
      lastScanAt: current.lastScanAt,
      reason: inference.reason,
    };
    setDiscoveryState(next, Date.now());
    return next;
  }

  const startedAt = Date.now();
  const segment = ensureNetworkSegment(
    inference.boundary,
    startedAt,
    options.source ?? "default-route",
  );
  if (!options.force && segment.lastScanAt && segment.lastScanAt > startedAt - SCAN_INTERVAL_MS) {
    const next = {
      status: "current" as const,
      cidr: segment.cidr,
      lastScanAt: new Date(segment.lastScanAt).toISOString(),
      reason: null,
    };
    setDiscoveryState(next, startedAt);
    return next;
  }

  // Once a healthy enrolled agent is available on this segment, it becomes
  // the single scanning vantage point. The dynamic import avoids coupling the
  // discovery module to the task module during startup initialization.
  const { ensureSegmentScanTask } = await import("@/lib/server/scanning");
  const delegated = ensureSegmentScanTask(segment.id, startedAt);
  if (delegated) {
    const next = {
      status: "scanning" as const,
      cidr: segment.cidr,
      lastScanAt: segment.lastScanAt ? new Date(segment.lastScanAt).toISOString() : null,
      reason: `Scanner agent ${delegated.scanner.agentId} is working on this segment.`,
    };
    setDiscoveryState(next, startedAt);
    return next;
  }

  setDiscoveryState(
    { status: "scanning", cidr: segment.cidr, lastScanAt: current.lastScanAt, reason: null },
    startedAt,
  );
  try {
    const results = await scanSegment(segment.cidr, 22, { timeoutMs: 800, concurrency: 64 });
    const completedAt = Date.now();
    reconcileScanResults(segment.id, results, completedAt);
    const next = {
      status: "current" as const,
      cidr: segment.cidr,
      lastScanAt: new Date(completedAt).toISOString(),
      reason: null,
    };
    setDiscoveryState(next, completedAt);
    return next;
  } catch (error) {
    const next = {
      status: "error" as const,
      cidr: segment.cidr,
      lastScanAt: current.lastScanAt,
      reason: error instanceof Error ? error.message : "Network scan failed.",
    };
    setDiscoveryState(next, Date.now());
    return next;
  }
}

export function requestDiscovery(): void {
  if (
    process.env.SCOUT_DISABLE_DISCOVERY === "true" ||
    authorityPaused() ||
    discoveryRun ||
    !ownerExists()
  )
    return;
  discoveryRun = discoverNow().finally(() => {
    discoveryRun = undefined;
  });
}
