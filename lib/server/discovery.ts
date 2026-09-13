import { randomUUID } from "node:crypto";

import { getDatabase } from "@/lib/server/db";
import { inferDefaultRoute, type NetworkBoundary } from "@/lib/discovery/network";
import { scanSegment, type ProbeResult } from "@/lib/discovery/scanner";
import { ownerExists } from "@/lib/server/setup";

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

  if (existing) {
    sqlite
      .prepare("UPDATE network_segment SET cidr = ?, updated_at = ? WHERE id = ?")
      .run(boundary.cidr, now, existing.id);
    return { ...existing, cidr: boundary.cidr };
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
): void {
  const existing = sqlite
    .prepare(
      `
        SELECT s.id
        FROM system s
        INNER JOIN system_address sa ON sa.system_id = s.id
        WHERE s.segment_id = ? AND sa.address = ? AND sa.port = ?
        LIMIT 1
      `,
    )
    .get(segmentId, result.address, result.port) as { id: string } | undefined;

  if (!existing && result.outcome !== "open") return;

  const systemId = existing?.id ?? randomUUID();
  if (!existing) {
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
          (id, system_id, method, address, port, outcome, source, observed_at, expires_at)
        VALUES (?, ?, 'ssh', ?, ?, ?, 'server-scan', ?, ?)
        ON CONFLICT(system_id, method, address, port) DO UPDATE SET
          outcome = excluded.outcome,
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

export function reconcileScanResults(
  segmentId: string,
  results: ProbeResult[],
  now = Date.now(),
): void {
  const { sqlite } = getDatabase();
  const reconcile = sqlite.transaction(() => {
    for (const result of results) reconcileOne(sqlite, segmentId, result, now);
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
  if (process.env.SCOUT_DISABLE_DISCOVERY === "true" || discoveryRun || !ownerExists()) return;
  discoveryRun = discoverNow().finally(() => {
    discoveryRun = undefined;
  });
}
