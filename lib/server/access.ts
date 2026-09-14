import { randomUUID } from "node:crypto";

import { getDatabase } from "@/lib/server/db";
import { decryptCredential, encryptCredential } from "@/lib/server/credentials";
import { readSshFingerprint, type SshEndpoint } from "@/lib/server/ssh";
import { recordVerifiedSshIdentity, resolveSystemId } from "@/lib/server/system-identity";

export type AccessGrantInput = {
  systemId: string;
  ownerId?: string;
  method: "ssh";
  username: string;
  authType: "password" | "private-key";
  secret: string;
  passphrase: string | null;
  privilegePassword?: string | null;
  fingerprint: string;
  trust: boolean;
  idempotencyKey: string;
  scope?: "exact-host" | "bounded-subnet";
  automaticEnrollment?: boolean;
  firstSeenKeyPinning?: boolean;
};

export class AccessError extends Error {
  constructor(
    message: string,
    public readonly code:
      "not_found" | "trust_required" | "fingerprint_changed" | "conflict" | "invalid",
  ) {
    super(message);
  }
}

export type EnrollmentJobReceipt = {
  jobId: string;
  status: string;
  stage: string;
  attempt: number;
  createdAt: string;
  updatedAt: string;
  leaseExpiresAt: string | null;
};

export type AutomaticEnrollmentResult = {
  queued: boolean;
  reason: "queued" | "already-queued" | "awaiting-trust" | "needs-review" | "none";
  jobId: string | null;
};

export type ReusableCredentialMetadata = {
  id: string;
  sourceSystemId: string;
  method: string;
  username: string;
  scope: "bounded-subnet";
  scopeSegmentId: string;
  automaticEnrollment: boolean;
  firstSeenKeyPinning: boolean;
};

export function getReusableCredentialForSystem(
  requestedSystemId: string,
): ReusableCredentialMetadata | null {
  const { sqlite } = getDatabase();
  const systemId = resolveSystemId(sqlite, requestedSystemId);
  const row = sqlite
    .prepare(
      `
        SELECT
          cg.id,
          cg.system_id AS sourceSystemId,
          cg.method,
          cg.username,
          cg.scope,
          cg.scope_segment_id AS scopeSegmentId,
          cg.automatic_enrollment AS automaticEnrollment,
          cg.first_seen_key_pinning AS firstSeenKeyPinning
        FROM credential_grant cg
        INNER JOIN system target ON target.id = ?
        INNER JOIN system source ON source.id = cg.system_id
        WHERE target.segment_id = cg.scope_segment_id
          AND cg.scope = 'bounded-subnet'
          AND cg.enabled = 1
          AND source.excluded = 0
        ORDER BY cg.updated_at DESC
        LIMIT 1
      `,
    )
    .get(systemId) as
    | {
        id: string;
        sourceSystemId: string;
        method: string;
        username: string;
        scope: string;
        scopeSegmentId: string | null;
        automaticEnrollment: number;
        firstSeenKeyPinning: number;
      }
    | undefined;
  if (!row || row.scopeSegmentId === null || row.scope !== "bounded-subnet") return null;
  return {
    id: row.id,
    sourceSystemId: row.sourceSystemId,
    method: row.method,
    username: row.username,
    scope: "bounded-subnet",
    scopeSegmentId: row.scopeSegmentId,
    automaticEnrollment: row.automaticEnrollment === 1,
    firstSeenKeyPinning: row.firstSeenKeyPinning === 1,
  };
}

export function getAccessGrantReceipt(
  idempotencyKey: string,
  ownerId: string,
  requestedSystemId: string,
): EnrollmentJobReceipt | null {
  const { sqlite } = getDatabase();
  const systemId = resolveSystemId(sqlite, requestedSystemId);
  const row = sqlite
    .prepare(
      "SELECT operation, scope_id AS scopeId, resource_id AS resourceId FROM idempotency_receipt WHERE key = ?",
    )
    .get(idempotencyKey) as { operation: string; scopeId: string; resourceId: string } | undefined;
  if (!row || row.operation !== "access-grant") return null;
  if (row.scopeId !== `${ownerId}:${systemId}`) return null;
  return readEnrollmentReceipt(sqlite, row.resourceId);
}

export function sshEndpointForSystem(systemId: string, now = Date.now()): SshEndpoint {
  const { sqlite } = getDatabase();
  const canonicalId = resolveSystemId(sqlite, systemId);
  const row = sqlite
    .prepare(
      "SELECT address, port FROM access_evidence WHERE system_id = ? AND method = 'ssh' AND outcome = 'open' AND expires_at > ? ORDER BY observed_at DESC LIMIT 1",
    )
    .get(canonicalId, now) as SshEndpoint | undefined;
  if (!row) throw new AccessError("This system has no current open SSH evidence.", "not_found");
  return row;
}

export async function preflightSsh(systemId: string, now = Date.now()) {
  const { sqlite } = getDatabase();
  const requestedId = resolveSystemId(sqlite, systemId);
  const endpoint = sshEndpointForSystem(requestedId, now);
  const fingerprint = await readSshFingerprint(endpoint);
  const canonicalId = sqlite.transaction(() =>
    recordVerifiedSshIdentity(sqlite, requestedId, endpoint, fingerprint, now),
  )();
  const automaticEnrollment = queueAutomaticEnrollment(canonicalId, fingerprint, now);
  const trusted = sqlite
    .prepare(
      "SELECT fingerprint FROM trusted_host_key WHERE system_id = ? AND method = 'ssh' AND revoked_at IS NULL ORDER BY accepted_at DESC LIMIT 1",
    )
    .get(canonicalId) as { fingerprint: string } | undefined;
  return {
    systemId: canonicalId,
    endpoint,
    fingerprint,
    trustedFingerprint: trusted?.fingerprint ?? null,
    trusted: trusted?.fingerprint === fingerprint,
    automaticEnrollment,
  };
}

function queueAutomaticEnrollment(
  requestedSystemId: string,
  fingerprint: string,
  now: number,
): AutomaticEnrollmentResult {
  const { sqlite } = getDatabase();
  return sqlite.transaction((): AutomaticEnrollmentResult => {
    const systemId = resolveSystemId(sqlite, requestedSystemId);
    const target = sqlite
      .prepare("SELECT id, segment_id AS segmentId FROM system WHERE id = ? AND excluded = 0")
      .get(systemId) as { id: string; segmentId: string | null } | undefined;
    if (!target?.segmentId) return { queued: false, reason: "none", jobId: null };

    if (
      sqlite
        .prepare("SELECT 1 FROM agent WHERE system_id = ? AND revoked_at IS NULL LIMIT 1")
        .get(systemId)
    ) {
      return { queued: false, reason: "none", jobId: null };
    }

    const latestJob = sqlite
      .prepare(
        "SELECT id, status FROM enrollment_job WHERE system_id = ? ORDER BY created_at DESC LIMIT 1",
      )
      .get(systemId) as { id: string; status: string } | undefined;
    if (latestJob && ["queued", "running"].includes(latestJob.status)) {
      return { queued: true, reason: "already-queued", jobId: latestJob.id };
    }
    if (latestJob && ["failed", "blocked"].includes(latestJob.status)) {
      return { queued: false, reason: "needs-review", jobId: latestJob.id };
    }

    const reusable = sqlite
      .prepare(
        `
          SELECT
            cg.id,
            cg.system_id AS sourceSystemId,
            cg.method,
            cg.username,
            cg.secret_ciphertext AS secretCiphertext,
            cg.nonce,
            cg.scope,
            cg.scope_segment_id AS scopeSegmentId,
            cg.automatic_enrollment AS automaticEnrollment,
            cg.first_seen_key_pinning AS firstSeenKeyPinning
          FROM credential_grant cg
          INNER JOIN system source ON source.id = cg.system_id
          WHERE cg.scope = 'bounded-subnet'
            AND cg.scope_segment_id = ?
            AND cg.automatic_enrollment = 1
            AND cg.enabled = 1
            AND source.excluded = 0
          ORDER BY cg.updated_at DESC
          LIMIT 1
        `,
      )
      .get(target.segmentId) as
      | {
          id: string;
          sourceSystemId: string;
          method: string;
          username: string;
          secretCiphertext: string;
          nonce: string;
          scope: string;
          scopeSegmentId: string | null;
          automaticEnrollment: number;
          firstSeenKeyPinning: number;
        }
      | undefined;
    if (!reusable || reusable.scopeSegmentId === null) {
      return { queued: false, reason: "none", jobId: null };
    }

    const trusted = sqlite
      .prepare(
        "SELECT fingerprint FROM trusted_host_key WHERE system_id = ? AND method = 'ssh' AND revoked_at IS NULL ORDER BY accepted_at DESC LIMIT 1",
      )
      .get(systemId) as { fingerprint: string } | undefined;
    if (trusted && trusted.fingerprint !== fingerprint) {
      throw new AccessError(
        "The SSH host fingerprint changed. Review the new identity before continuing.",
        "fingerprint_changed",
      );
    }
    if (!trusted && reusable.firstSeenKeyPinning !== 1) {
      return { queued: false, reason: "awaiting-trust", jobId: null };
    }
    if (!trusted) {
      sqlite
        .prepare(
          "INSERT INTO trusted_host_key (id, system_id, method, fingerprint, accepted_at) VALUES (?, ?, 'ssh', ?, ?)",
        )
        .run(randomUUID(), systemId, fingerprint, now);
    }

    const existingCredential = sqlite
      .prepare(
        "SELECT id FROM credential_grant WHERE system_id = ? AND method = ? AND username = ? AND enabled = 1 ORDER BY updated_at DESC LIMIT 1",
      )
      .get(systemId, reusable.method, reusable.username) as { id: string } | undefined;
    const credentialId = existingCredential?.id ?? randomUUID();
    if (!existingCredential) {
      const secret = decryptCredential(
        { ciphertext: reusable.secretCiphertext, nonce: reusable.nonce },
        { systemId: reusable.sourceSystemId, method: reusable.method, username: reusable.username },
      );
      const encrypted = encryptCredential(secret, {
        systemId,
        method: reusable.method,
        username: reusable.username,
      });
      sqlite
        .prepare(
          "INSERT INTO credential_grant (id, system_id, method, username, secret_ciphertext, nonce, scope, scope_segment_id, automatic_enrollment, first_seen_key_pinning, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, 'bounded-subnet', ?, 1, ?, ?, ?)",
        )
        .run(
          credentialId,
          systemId,
          reusable.method,
          reusable.username,
          encrypted.ciphertext,
          encrypted.nonce,
          reusable.scopeSegmentId,
          reusable.firstSeenKeyPinning,
          now,
          now,
        );
    }

    const jobId = randomUUID();
    sqlite
      .prepare(
        "INSERT INTO enrollment_job (id, system_id, credential_id, status, stage, created_at, updated_at) VALUES (?, ?, ?, 'queued', 'queued', ?, ?)",
      )
      .run(jobId, systemId, credentialId, now, now);
    sqlite
      .prepare("UPDATE system SET status = 'installing', updated_at = ? WHERE id = ?")
      .run(now, systemId);
    return { queued: true, reason: "queued", jobId };
  })();
}

export function createAccessGrant(input: AccessGrantInput, now = Date.now()): EnrollmentJobReceipt {
  const { sqlite } = getDatabase();
  const create = sqlite.transaction(() => {
    const systemId = resolveSystemId(sqlite, input.systemId);
    const scope = input.scope ?? "exact-host";
    const automaticEnrollment = input.automaticEnrollment === true;
    const firstSeenKeyPinning = input.firstSeenKeyPinning === true;
    if (scope === "exact-host" && (automaticEnrollment || firstSeenKeyPinning)) {
      throw new AccessError(
        "Automatic enrollment and first-seen key pinning require bounded-subnet scope.",
        "invalid",
      );
    }
    if (firstSeenKeyPinning && !automaticEnrollment) {
      throw new AccessError(
        "Acknowledge automatic enrollment before enabling first-seen key pinning.",
        "invalid",
      );
    }
    const prior = sqlite
      .prepare(
        "SELECT operation, scope_id AS scopeId, resource_id AS resourceId FROM idempotency_receipt WHERE key = ?",
      )
      .get(input.idempotencyKey) as
      { operation: string; scopeId: string; resourceId: string } | undefined;
    if (prior) {
      const requestedScope = `${input.ownerId ?? "owner"}:${resolveSystemId(sqlite, input.systemId)}`;
      if (prior.operation !== "access-grant" || prior.scopeId !== requestedScope)
        throw new AccessError("This idempotency key was already used.", "conflict");
      const job = sqlite
        .prepare("SELECT status, stage FROM enrollment_job WHERE id = ?")
        .get(prior.resourceId) as { status: string; stage: string } | undefined;
      if (!job) throw new AccessError("The previous enrollment job is unavailable.", "conflict");
      return { jobId: prior.resourceId, status: job.status, stage: job.stage };
    }

    const system = sqlite
      .prepare("SELECT id, segment_id AS segmentId FROM system WHERE id = ? AND excluded = 0")
      .get(systemId);
    if (!system) throw new AccessError("System not found.", "not_found");
    const segmentId = (system as { id: string; segmentId: string | null }).segmentId;
    if (scope === "bounded-subnet" && !segmentId) {
      throw new AccessError(
        "This system is not attached to a bounded network segment yet.",
        "invalid",
      );
    }
    const trusted = sqlite
      .prepare(
        "SELECT fingerprint FROM trusted_host_key WHERE system_id = ? AND method = 'ssh' AND revoked_at IS NULL ORDER BY accepted_at DESC LIMIT 1",
      )
      .get(systemId) as { fingerprint: string } | undefined;
    if (trusted && trusted.fingerprint !== input.fingerprint) {
      throw new AccessError(
        "The SSH host fingerprint changed. Review the new identity before continuing.",
        "fingerprint_changed",
      );
    }
    if (!trusted && !input.trust && !firstSeenKeyPinning)
      throw new AccessError(
        "Confirm the first-seen SSH host fingerprint before continuing.",
        "trust_required",
      );

    if (!trusted) {
      sqlite
        .prepare(
          "INSERT INTO trusted_host_key (id, system_id, method, fingerprint, accepted_at) VALUES (?, ?, 'ssh', ?, ?)",
        )
        .run(randomUUID(), systemId, input.fingerprint, now);
    }

    const encrypted = encryptCredential(
      {
        authType: input.authType,
        secret: input.secret,
        passphrase: input.passphrase,
        privilegePassword:
          input.privilegePassword ?? (input.authType === "password" ? input.secret : null),
      },
      { systemId, method: input.method, username: input.username },
    );
    const credentialId = randomUUID();
    sqlite
      .prepare(
        "INSERT INTO credential_grant (id, system_id, method, username, secret_ciphertext, nonce, scope, scope_segment_id, automatic_enrollment, first_seen_key_pinning, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
      )
      .run(
        credentialId,
        systemId,
        input.method,
        input.username,
        encrypted.ciphertext,
        encrypted.nonce,
        scope,
        scope === "bounded-subnet" ? segmentId : null,
        automaticEnrollment ? 1 : 0,
        firstSeenKeyPinning ? 1 : 0,
        now,
        now,
      );

    const active = sqlite
      .prepare(
        "SELECT id, status, stage FROM enrollment_job WHERE system_id = ? AND status IN ('queued', 'running') ORDER BY created_at DESC LIMIT 1",
      )
      .get(systemId) as { id: string; status: string; stage: string } | undefined;
    const job = active ?? { id: randomUUID(), status: "queued", stage: "queued" };
    if (active) {
      sqlite
        .prepare("UPDATE enrollment_job SET credential_id = ?, updated_at = ? WHERE id = ?")
        .run(credentialId, now, active.id);
    } else {
      sqlite
        .prepare(
          "INSERT INTO enrollment_job (id, system_id, credential_id, status, stage, created_at, updated_at) VALUES (?, ?, ?, 'queued', 'queued', ?, ?)",
        )
        .run(job.id, systemId, credentialId, now, now);
    }
    sqlite
      .prepare("UPDATE system SET status = 'installing', updated_at = ? WHERE id = ?")
      .run(now, systemId);
    sqlite
      .prepare(
        "INSERT INTO idempotency_receipt (key, operation, scope_id, resource_id, created_at) VALUES (?, 'access-grant', ?, ?, ?)",
      )
      .run(input.idempotencyKey, `${input.ownerId ?? "owner"}:${systemId}`, job.id, now);
    return { jobId: job.id, status: job.status, stage: job.stage };
  });
  const receipt = create();
  const details = sqlite
    .prepare(
      "SELECT attempt, created_at AS createdAt, updated_at AS updatedAt, lease_expires_at AS leaseExpiresAt FROM enrollment_job WHERE id = ?",
    )
    .get(receipt.jobId) as
    | {
        attempt: number;
        createdAt: number;
        updatedAt: number;
        leaseExpiresAt: number | null;
      }
    | undefined;
  if (!details) throw new AccessError("The enrollment job is unavailable.", "conflict");
  return {
    ...receipt,
    attempt: details.attempt,
    createdAt: new Date(details.createdAt).toISOString(),
    updatedAt: new Date(details.updatedAt).toISOString(),
    leaseExpiresAt: details.leaseExpiresAt ? new Date(details.leaseExpiresAt).toISOString() : null,
  };
}

function readEnrollmentReceipt(
  sqlite: ReturnType<typeof getDatabase>["sqlite"],
  jobId: string,
): EnrollmentJobReceipt | null {
  const details = sqlite
    .prepare(
      "SELECT status, stage, attempt, created_at AS createdAt, updated_at AS updatedAt, lease_expires_at AS leaseExpiresAt FROM enrollment_job WHERE id = ?",
    )
    .get(jobId) as
    | {
        status: string;
        stage: string;
        attempt: number;
        createdAt: number;
        updatedAt: number;
        leaseExpiresAt: number | null;
      }
    | undefined;
  if (!details) return null;
  return {
    jobId,
    status: details.status,
    stage: details.stage,
    attempt: details.attempt,
    createdAt: new Date(details.createdAt).toISOString(),
    updatedAt: new Date(details.updatedAt).toISOString(),
    leaseExpiresAt: details.leaseExpiresAt ? new Date(details.leaseExpiresAt).toISOString() : null,
  };
}
