import { randomUUID } from "node:crypto";

import { decryptCredential, encryptCredential } from "@/lib/server/credentials";

import type Database from "better-sqlite3";

type Sqlite = Database.Database;

type SegmentRow = {
  id: string;
  siteKey: string;
  cidr: string;
  source: string;
};

type SystemRow = {
  id: string;
  segmentId: string | null;
  displayName: string;
  hostname: string | null;
  status: string;
  excluded: number;
  lastSeenAt: number | null;
  createdAt: number;
};

type AddressRow = {
  id: string;
  address: string;
  port: number;
  lastSeenAt: number;
};

type EvidenceRow = {
  id: string;
  method: string;
  address: string;
  port: number;
  outcome: string;
  fingerprint: string | null;
  macAddress: string | null;
  source: string;
  observedAt: number;
  expiresAt: number;
};

type CredentialRow = {
  id: string;
  method: string;
  username: string;
  secretCiphertext: string;
  nonce: string;
  scope: string;
  scopeSegmentId: string | null;
  automaticEnrollment: number;
  firstSeenKeyPinning: number;
  enabled: number;
  createdAt: number;
  updatedAt: number;
};

type Endpoint = { address: string; port: number };

type VerifiedEndpointCandidate = {
  id: string;
  segmentId: string | null;
  createdAt: number;
  hasAgent: number;
  hasCredential: number;
  hasTrustedKey: number;
  fingerprint: string | null;
  trustedFingerprint: string | null;
};

function system(sqlite: Sqlite, id: string): SystemRow | undefined {
  return sqlite
    .prepare(
      "SELECT id, segment_id AS segmentId, display_name AS displayName, hostname, status, excluded, last_seen_at AS lastSeenAt, created_at AS createdAt FROM system WHERE id = ?",
    )
    .get(id) as SystemRow | undefined;
}

export function resolveSystemId(sqlite: Sqlite, systemId: string): string {
  let current = systemId;
  const visited = new Set<string>();
  for (let depth = 0; depth < 32; depth += 1) {
    if (visited.has(current)) throw new Error("System identity aliases contain a cycle.");
    visited.add(current);
    const alias = sqlite
      .prepare(
        "SELECT canonical_system_id AS canonicalSystemId FROM system_alias WHERE source_system_id = ?",
      )
      .get(current) as { canonicalSystemId: string } | undefined;
    if (!alias) return current;
    current = alias.canonicalSystemId;
  }
  throw new Error("System identity alias chain is too deep.");
}

export function segmentMetadata(sqlite: Sqlite, segmentId: string): SegmentRow | undefined {
  return sqlite
    .prepare("SELECT id, site_key AS siteKey, cidr, source FROM network_segment WHERE id = ?")
    .get(segmentId) as SegmentRow | undefined;
}

export function equivalentSegmentProvenance(
  sqlite: Sqlite,
  leftSegmentId: string | null,
  rightSegmentId: string | null,
): boolean {
  if (!leftSegmentId || !rightSegmentId) return false;
  if (leftSegmentId === rightSegmentId) return true;
  const left = segmentMetadata(sqlite, leftSegmentId);
  const right = segmentMetadata(sqlite, rightSegmentId);
  if (!left || !right || left.siteKey !== right.siteKey || left.cidr !== right.cidr) return false;
  return (
    new Set([left.source, right.source]).size === 2 &&
    [left.source, right.source].includes("manual") &&
    [left.source, right.source].includes("default-route")
  );
}

function verifiedEndpointCandidates(
  sqlite: Sqlite,
  endpoint: Endpoint,
): VerifiedEndpointCandidate[] {
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
    .all(
      endpoint.address,
      endpoint.port,
      endpoint.address,
      endpoint.port,
    ) as VerifiedEndpointCandidate[];
}

function candidateHasConflictingFingerprint(
  candidate: VerifiedEndpointCandidate,
  fingerprint: string,
): boolean {
  return (
    (candidate.fingerprint !== null && candidate.fingerprint !== fingerprint) ||
    (candidate.trustedFingerprint !== null && candidate.trustedFingerprint !== fingerprint)
  );
}

function compareIdentityCandidates(
  left: VerifiedEndpointCandidate,
  right: VerifiedEndpointCandidate,
): number {
  for (const field of ["hasAgent", "hasCredential", "hasTrustedKey"] as const) {
    if (left[field] !== right[field]) return right[field] - left[field];
  }
  if (left.createdAt !== right.createdAt) return left.createdAt - right.createdAt;
  return left.id.localeCompare(right.id);
}

function knownFingerprints(candidate: VerifiedEndpointCandidate): string[] {
  return [candidate.fingerprint, candidate.trustedFingerprint].filter(
    (fingerprint): fingerprint is string => fingerprint !== null,
  );
}

function candidatesCanJoin(
  sqlite: Sqlite,
  left: VerifiedEndpointCandidate,
  right: VerifiedEndpointCandidate,
): boolean {
  const leftFingerprints = knownFingerprints(left);
  const rightFingerprints = knownFingerprints(right);
  if (leftFingerprints.length && rightFingerprints.length) {
    if (!leftFingerprints.some((fingerprint) => rightFingerprints.includes(fingerprint)))
      return false;
  }
  if (left.segmentId === right.segmentId) return true;
  if (equivalentSegmentProvenance(sqlite, left.segmentId, right.segmentId)) return true;
  if (!leftFingerprints.length || !rightFingerprints.length) return false;
  const leftSegment = left.segmentId ? segmentMetadata(sqlite, left.segmentId) : undefined;
  const rightSegment = right.segmentId ? segmentMetadata(sqlite, right.segmentId) : undefined;
  return (
    leftSegment?.siteKey === rightSegment?.siteKey &&
    leftFingerprints.some((fingerprint) => rightFingerprints.includes(fingerprint))
  );
}

/**
 * Repair duplicates left by older discovery versions without joining
 * disconnected default-route provenance unless the SSH identity matches.
 */
export function repairDuplicateSystems(sqlite: Sqlite, now = Date.now()): number {
  const endpoints = sqlite
    .prepare(
      "SELECT sa.address, sa.port FROM system_address sa INNER JOIN system s ON s.id = sa.system_id WHERE s.excluded = 0 AND NOT EXISTS (SELECT 1 FROM system_alias alias WHERE alias.source_system_id = s.id) GROUP BY sa.address, sa.port HAVING COUNT(DISTINCT sa.system_id) > 1",
    )
    .all() as Array<{ address: string; port: number }>;
  let merged = 0;
  for (const endpoint of endpoints) {
    const attemptedPairs = new Set<string>();
    while (true) {
      const candidates = verifiedEndpointCandidates(sqlite, endpoint).sort(
        compareIdentityCandidates,
      );
      let pair: [VerifiedEndpointCandidate, VerifiedEndpointCandidate] | undefined;
      for (let leftIndex = 0; leftIndex < candidates.length && !pair; leftIndex += 1) {
        for (let rightIndex = leftIndex + 1; rightIndex < candidates.length; rightIndex += 1) {
          const left = candidates[leftIndex];
          const right = candidates[rightIndex];
          const pairKey = `${left.id}:${right.id}`;
          if (!attemptedPairs.has(pairKey) && candidatesCanJoin(sqlite, left, right)) {
            pair = [left, right];
            break;
          }
        }
      }
      if (!pair) break;

      const [canonical, duplicate] = pair;
      attemptedPairs.add(`${canonical.id}:${duplicate.id}`);
      try {
        sqlite.transaction(() => {
          mergeSystems(sqlite, duplicate.id, canonical.id, "startup-duplicate-repair", now);
        })();
        merged += 1;
      } catch {
        // A conflicting agent or unavailable credential key is a data safety
        // blocker. Leave both records visible for explicit owner review.
      }
    }
  }
  return merged;
}

/**
 * Persist an SSH host-key observation and join only records with compatible
 * provenance or independently matching host identity. The caller can use the
 * returned ID when a preflight caused the requested record to be merged.
 */
export function recordVerifiedSshIdentity(
  sqlite: Sqlite,
  systemId: string,
  endpoint: Endpoint,
  fingerprint: string,
  now = Date.now(),
): string {
  const requestedId = resolveSystemId(sqlite, systemId);
  const evidence = sqlite
    .prepare(
      "SELECT id FROM access_evidence WHERE system_id = ? AND method = 'ssh' AND address = ? AND port = ?",
    )
    .get(requestedId, endpoint.address, endpoint.port) as { id: string } | undefined;
  if (evidence) {
    sqlite
      .prepare(
        "UPDATE access_evidence SET outcome = 'open', fingerprint = ?, source = 'ssh-preflight', observed_at = ?, expires_at = ? WHERE id = ?",
      )
      .run(fingerprint, now, now + 30 * 60 * 1000, evidence.id);
  } else {
    sqlite
      .prepare(
        "INSERT INTO system_address (id, system_id, address, port, last_seen_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(system_id, address, port) DO UPDATE SET last_seen_at = excluded.last_seen_at",
      )
      .run(randomUUID(), requestedId, endpoint.address, endpoint.port, now);
    sqlite
      .prepare(
        "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, fingerprint, source, observed_at, expires_at) VALUES (?, ?, 'ssh', ?, ?, 'open', ?, 'ssh-preflight', ?, ?)",
      )
      .run(
        randomUUID(),
        requestedId,
        endpoint.address,
        endpoint.port,
        fingerprint,
        now,
        now + 30 * 60 * 1000,
      );
  }

  const candidates = verifiedEndpointCandidates(sqlite, endpoint);
  const requested = candidates.find((candidate) => candidate.id === requestedId);
  const compatible = candidates.filter((candidate) => {
    if (candidate.id === requestedId) return true;
    if (candidateHasConflictingFingerprint(candidate, fingerprint)) return false;
    if (candidate.segmentId === requested?.segmentId) return true;
    if (equivalentSegmentProvenance(sqlite, candidate.segmentId, requested?.segmentId ?? null))
      return true;
    const candidateSegment = candidate.segmentId
      ? segmentMetadata(sqlite, candidate.segmentId)
      : undefined;
    const requestedSegment = requested?.segmentId
      ? segmentMetadata(sqlite, requested.segmentId)
      : undefined;
    return (
      candidateSegment?.siteKey === requestedSegment?.siteKey &&
      (candidate.fingerprint === fingerprint || candidate.trustedFingerprint === fingerprint)
    );
  });
  if (compatible.length === 0) return requestedId;

  const canonical = [...compatible].sort(compareIdentityCandidates)[0];
  for (const candidate of compatible) {
    if (candidate.id !== canonical.id) {
      mergeSystems(sqlite, candidate.id, canonical.id, "verified-ssh-identity", now);
    }
  }
  return resolveSystemId(sqlite, canonical.id);
}

function assertNoConflictingTrustedKeys(
  sqlite: Sqlite,
  sourceId: string,
  canonicalId: string,
): void {
  const sourceKeys = sqlite
    .prepare(
      "SELECT method, fingerprint FROM trusted_host_key WHERE system_id = ? AND revoked_at IS NULL",
    )
    .all(sourceId) as Array<{ method: string; fingerprint: string }>;
  const canonicalKeys = sqlite
    .prepare(
      "SELECT method, fingerprint FROM trusted_host_key WHERE system_id = ? AND revoked_at IS NULL",
    )
    .all(canonicalId) as Array<{ method: string; fingerprint: string }>;
  for (const sourceKey of sourceKeys) {
    if (
      canonicalKeys.some(
        (canonicalKey) =>
          canonicalKey.method === sourceKey.method &&
          canonicalKey.fingerprint !== sourceKey.fingerprint,
      )
    ) {
      throw new Error("Cannot merge systems with conflicting trusted host identities.");
    }
  }
}

function mergeAddresses(sqlite: Sqlite, sourceId: string, canonicalId: string): void {
  const addresses = sqlite
    .prepare(
      "SELECT id, address, port, last_seen_at AS lastSeenAt FROM system_address WHERE system_id = ?",
    )
    .all(sourceId) as AddressRow[];
  for (const address of addresses) {
    const existing = sqlite
      .prepare(
        "SELECT id, last_seen_at AS lastSeenAt FROM system_address WHERE system_id = ? AND address = ? AND port = ?",
      )
      .get(canonicalId, address.address, address.port) as
      { id: string; lastSeenAt: number } | undefined;
    if (existing) {
      sqlite
        .prepare("UPDATE system_address SET last_seen_at = ? WHERE id = ?")
        .run(Math.max(existing.lastSeenAt, address.lastSeenAt), existing.id);
      sqlite.prepare("DELETE FROM system_address WHERE id = ?").run(address.id);
    } else {
      sqlite
        .prepare("UPDATE system_address SET system_id = ? WHERE id = ?")
        .run(canonicalId, address.id);
    }
  }
}

function mergeEvidence(sqlite: Sqlite, sourceId: string, canonicalId: string): void {
  const evidence = sqlite
    .prepare(
      "SELECT id, method, address, port, outcome, fingerprint, mac_address AS macAddress, source, observed_at AS observedAt, expires_at AS expiresAt FROM access_evidence WHERE system_id = ?",
    )
    .all(sourceId) as EvidenceRow[];
  for (const item of evidence) {
    const existing = sqlite
      .prepare(
        "SELECT id, outcome, fingerprint, mac_address AS macAddress, source, observed_at AS observedAt, expires_at AS expiresAt FROM access_evidence WHERE system_id = ? AND method = ? AND address = ? AND port = ?",
      )
      .get(canonicalId, item.method, item.address, item.port) as
      (Omit<EvidenceRow, "method" | "address" | "port" | "id"> & { id: string }) | undefined;
    if (!existing) {
      sqlite
        .prepare("UPDATE access_evidence SET system_id = ? WHERE id = ?")
        .run(canonicalId, item.id);
      continue;
    }

    if (item.fingerprint && existing.fingerprint && item.fingerprint !== existing.fingerprint) {
      throw new Error("Cannot merge systems with conflicting SSH identities.");
    }

    const latest = item.observedAt >= existing.observedAt ? item : existing;
    sqlite
      .prepare(
        "UPDATE access_evidence SET outcome = ?, fingerprint = ?, mac_address = ?, source = ?, observed_at = ?, expires_at = ? WHERE id = ?",
      )
      .run(
        latest.outcome,
        item.fingerprint ?? existing.fingerprint,
        item.macAddress ?? existing.macAddress,
        latest.source,
        latest.observedAt,
        latest.expiresAt,
        existing.id,
      );
    sqlite.prepare("DELETE FROM access_evidence WHERE id = ?").run(item.id);
  }
}

function mergeTrustedKeys(sqlite: Sqlite, sourceId: string, canonicalId: string): void {
  const keys = sqlite
    .prepare(
      "SELECT id, method, fingerprint, accepted_at AS acceptedAt, revoked_at AS revokedAt FROM trusted_host_key WHERE system_id = ?",
    )
    .all(sourceId) as Array<{
    id: string;
    method: string;
    fingerprint: string;
    acceptedAt: number;
    revokedAt: number | null;
  }>;
  for (const key of keys) {
    const duplicate = sqlite
      .prepare(
        "SELECT id FROM trusted_host_key WHERE system_id = ? AND method = ? AND fingerprint = ? AND revoked_at IS ?",
      )
      .get(canonicalId, key.method, key.fingerprint, key.revokedAt) as { id: string } | undefined;
    if (duplicate) {
      sqlite.prepare("DELETE FROM trusted_host_key WHERE id = ?").run(key.id);
    } else {
      sqlite
        .prepare("UPDATE trusted_host_key SET system_id = ? WHERE id = ?")
        .run(canonicalId, key.id);
    }
  }
}

function mergeCredentials(sqlite: Sqlite, sourceId: string, canonicalId: string): void {
  const credentials = sqlite
    .prepare(
      "SELECT id, method, username, secret_ciphertext AS secretCiphertext, nonce, scope, scope_segment_id AS scopeSegmentId, automatic_enrollment AS automaticEnrollment, first_seen_key_pinning AS firstSeenKeyPinning, enabled, created_at AS createdAt, updated_at AS updatedAt FROM credential_grant WHERE system_id = ?",
    )
    .all(sourceId) as CredentialRow[];
  for (const credential of credentials) {
    const existing = sqlite
      .prepare(
        "SELECT id, method, username, secret_ciphertext AS secretCiphertext, nonce, scope, scope_segment_id AS scopeSegmentId, automatic_enrollment AS automaticEnrollment, first_seen_key_pinning AS firstSeenKeyPinning, enabled, created_at AS createdAt, updated_at AS updatedAt FROM credential_grant WHERE system_id = ? AND method = ? AND username = ? ORDER BY updated_at DESC LIMIT 1",
      )
      .get(canonicalId, credential.method, credential.username) as CredentialRow | undefined;

    if (existing && existing.updatedAt >= credential.updatedAt) {
      sqlite
        .prepare("UPDATE enrollment_job SET credential_id = ? WHERE credential_id = ?")
        .run(existing.id, credential.id);
      sqlite.prepare("DELETE FROM credential_grant WHERE id = ?").run(credential.id);
      continue;
    }

    const secret = decryptCredential(
      { ciphertext: credential.secretCiphertext, nonce: credential.nonce },
      { systemId: sourceId, method: credential.method, username: credential.username },
    );
    const encrypted = encryptCredential(secret, {
      systemId: canonicalId,
      method: credential.method,
      username: credential.username,
    });

    if (existing) {
      sqlite
        .prepare("UPDATE enrollment_job SET credential_id = ? WHERE credential_id = ?")
        .run(existing.id, credential.id);
      sqlite
        .prepare(
          "UPDATE credential_grant SET secret_ciphertext = ?, nonce = ?, scope = ?, scope_segment_id = ?, automatic_enrollment = ?, first_seen_key_pinning = ?, enabled = ?, created_at = ?, updated_at = ? WHERE id = ?",
        )
        .run(
          encrypted.ciphertext,
          encrypted.nonce,
          credential.scope,
          credential.scopeSegmentId,
          credential.automaticEnrollment,
          credential.firstSeenKeyPinning,
          credential.enabled,
          credential.createdAt,
          credential.updatedAt,
          existing.id,
        );
      sqlite.prepare("DELETE FROM credential_grant WHERE id = ?").run(credential.id);
    } else {
      sqlite
        .prepare(
          "UPDATE credential_grant SET system_id = ?, secret_ciphertext = ?, nonce = ?, scope_segment_id = ?, automatic_enrollment = ?, first_seen_key_pinning = ? WHERE id = ?",
        )
        .run(
          canonicalId,
          encrypted.ciphertext,
          encrypted.nonce,
          credential.scopeSegmentId,
          credential.automaticEnrollment,
          credential.firstSeenKeyPinning,
          credential.id,
        );
    }
  }
}

function mergeAgents(sqlite: Sqlite, sourceId: string, canonicalId: string): void {
  const sourceActive = sqlite
    .prepare("SELECT id FROM agent WHERE system_id = ? AND revoked_at IS NULL")
    .get(sourceId) as { id: string } | undefined;
  const canonicalActive = sqlite
    .prepare("SELECT id FROM agent WHERE system_id = ? AND revoked_at IS NULL")
    .get(canonicalId) as { id: string } | undefined;
  if (sourceActive && canonicalActive) {
    throw new Error("Cannot merge systems that both have an active agent.");
  }

  const agents = sqlite
    .prepare("SELECT id, revoked_at AS revokedAt FROM agent WHERE system_id = ?")
    .all(sourceId) as Array<{ id: string; revokedAt: number | null }>;
  for (const agent of agents) {
    const duplicate = sqlite
      .prepare("SELECT id FROM agent WHERE system_id = ? AND revoked_at IS ?")
      .get(canonicalId, agent.revokedAt) as { id: string } | undefined;
    if (duplicate) throw new Error("Cannot merge systems with duplicate agent identities.");
    sqlite.prepare("UPDATE agent SET system_id = ? WHERE id = ?").run(canonicalId, agent.id);
  }
}

function mergeJobs(sqlite: Sqlite, sourceId: string, canonicalId: string, now: number): void {
  sqlite
    .prepare("UPDATE agent_invitation SET system_id = ? WHERE system_id = ?")
    .run(canonicalId, sourceId);
  sqlite
    .prepare("UPDATE enrollment_job SET system_id = ? WHERE system_id = ?")
    .run(canonicalId, sourceId);

  const activeJobs = sqlite
    .prepare(
      "SELECT id FROM enrollment_job WHERE system_id = ? AND status IN ('queued', 'running') ORDER BY updated_at DESC, created_at DESC",
    )
    .all(canonicalId) as Array<{ id: string }>;
  for (const job of activeJobs.slice(1)) {
    sqlite
      .prepare(
        "UPDATE enrollment_job SET status = 'blocked', error_code = 'merged-system', error_message = 'This installation was superseded while duplicate system records were merged.', lease_owner = NULL, lease_expires_at = NULL, updated_at = ? WHERE id = ?",
      )
      .run(now, job.id);
  }
}

/**
 * Merge source into canonical. The caller must invoke this inside its own
 * SQLite transaction when the merge is part of a larger operation.
 */
export function mergeSystems(
  sqlite: Sqlite,
  sourceSystemId: string,
  canonicalSystemId: string,
  reason: string,
  now = Date.now(),
): void {
  const sourceId = resolveSystemId(sqlite, sourceSystemId);
  const canonicalId = resolveSystemId(sqlite, canonicalSystemId);
  if (sourceId === canonicalId) return;

  const source = system(sqlite, sourceId);
  const canonical = system(sqlite, canonicalId);
  if (!source || !canonical) throw new Error("Cannot merge a missing system identity.");
  assertNoConflictingTrustedKeys(sqlite, sourceId, canonicalId);

  mergeAddresses(sqlite, sourceId, canonicalId);
  mergeEvidence(sqlite, sourceId, canonicalId);
  mergeTrustedKeys(sqlite, sourceId, canonicalId);
  mergeCredentials(sqlite, sourceId, canonicalId);
  mergeAgents(sqlite, sourceId, canonicalId);
  mergeJobs(sqlite, sourceId, canonicalId, now);

  sqlite
    .prepare(
      "UPDATE system SET hostname = COALESCE(hostname, ?), last_seen_at = ?, updated_at = ?, excluded = CASE WHEN excluded = 1 OR ? = 1 THEN 1 ELSE 0 END WHERE id = ?",
    )
    .run(
      source.hostname,
      Math.max(canonical.lastSeenAt ?? 0, source.lastSeenAt ?? 0) || null,
      now,
      source.excluded,
      canonicalId,
    );
  sqlite.prepare("UPDATE system SET excluded = 1, updated_at = ? WHERE id = ?").run(now, sourceId);

  sqlite
    .prepare(
      "UPDATE system_alias SET canonical_system_id = ? WHERE canonical_system_id = ? AND source_system_id <> ?",
    )
    .run(canonicalId, sourceId, canonicalId);
  sqlite
    .prepare(
      "INSERT INTO system_alias (source_system_id, canonical_system_id, reason, created_at) VALUES (?, ?, ?, ?)",
    )
    .run(sourceId, canonicalId, reason, now);
  sqlite
    .prepare(
      "INSERT INTO audit_event (id, action, resource_type, resource_id, metadata, created_at) VALUES (?, 'merge', 'system', ?, ?, ?)",
    )
    .run(randomUUID(), canonicalId, JSON.stringify({ sourceSystemId: sourceId, reason }), now);
}
