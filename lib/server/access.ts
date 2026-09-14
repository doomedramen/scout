import { randomUUID } from "node:crypto";

import { getDatabase } from "@/lib/server/db";
import { encryptCredential } from "@/lib/server/credentials";
import { readSshFingerprint, type SshEndpoint } from "@/lib/server/ssh";
import { recordVerifiedSshIdentity, resolveSystemId } from "@/lib/server/system-identity";

export type AccessGrantInput = {
  systemId: string;
  method: "ssh";
  username: string;
  authType: "password" | "private-key";
  secret: string;
  passphrase: string | null;
  privilegePassword?: string | null;
  fingerprint: string;
  trust: boolean;
  idempotencyKey: string;
  scope?: "exact-host";
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
  };
}

export function createAccessGrant(input: AccessGrantInput, now = Date.now()): EnrollmentJobReceipt {
  const { sqlite } = getDatabase();
  const create = sqlite.transaction(() => {
    const systemId = resolveSystemId(sqlite, input.systemId);
    const prior = sqlite
      .prepare("SELECT operation, resource_id AS resourceId FROM idempotency_receipt WHERE key = ?")
      .get(input.idempotencyKey) as { operation: string; resourceId: string } | undefined;
    if (prior) {
      if (prior.operation !== "access-grant")
        throw new AccessError("This idempotency key was already used.", "conflict");
      const job = sqlite
        .prepare("SELECT status, stage FROM enrollment_job WHERE id = ?")
        .get(prior.resourceId) as { status: string; stage: string } | undefined;
      if (!job) throw new AccessError("The previous enrollment job is unavailable.", "conflict");
      return { jobId: prior.resourceId, status: job.status, stage: job.stage };
    }

    const system = sqlite
      .prepare("SELECT id FROM system WHERE id = ? AND excluded = 0")
      .get(systemId);
    if (!system) throw new AccessError("System not found.", "not_found");
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
    if (!trusted && !input.trust)
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
        "INSERT INTO credential_grant (id, system_id, method, username, secret_ciphertext, nonce, scope, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
      )
      .run(
        credentialId,
        systemId,
        input.method,
        input.username,
        encrypted.ciphertext,
        encrypted.nonce,
        input.scope ?? "exact-host",
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
        "INSERT INTO idempotency_receipt (key, operation, resource_id, created_at) VALUES (?, 'access-grant', ?, ?)",
      )
      .run(input.idempotencyKey, job.id, now);
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
