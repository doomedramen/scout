import { randomUUID } from "node:crypto";

import { getDatabase } from "@/lib/server/db";
import { resolveSystemId } from "@/lib/server/system-identity";

export class LifecycleError extends Error {
  constructor(
    message: string,
    public readonly code: "not-found" | "invalid-state" | "no-credential",
  ) {
    super(message);
  }
}

export type RetryReceipt = { jobId: string; status: "queued"; stage: "queued" };

export function retryEnrollment(systemId: string, now = Date.now()): RetryReceipt {
  const { sqlite } = getDatabase();
  const retry = sqlite.transaction(() => {
    const canonicalId = resolveSystemId(sqlite, systemId);
    const system = sqlite
      .prepare("SELECT id, excluded FROM system WHERE id = ? LIMIT 1")
      .get(canonicalId) as { id: string; excluded: number } | undefined;
    if (!system || system.excluded) throw new LifecycleError("System not found.", "not-found");
    const credential = sqlite
      .prepare(
        "SELECT id FROM credential_grant WHERE system_id = ? AND method = 'ssh' AND enabled = 1 ORDER BY updated_at DESC LIMIT 1",
      )
      .get(canonicalId) as { id: string } | undefined;
    if (!credential)
      throw new LifecycleError("No stored SSH credential is available for retry.", "no-credential");
    const current = sqlite
      .prepare(
        "SELECT id, status FROM enrollment_job WHERE system_id = ? ORDER BY created_at DESC LIMIT 1",
      )
      .get(canonicalId) as { id: string; status: string } | undefined;
    if (current && ["queued", "running"].includes(current.status))
      throw new LifecycleError("An installation is already in progress.", "invalid-state");
    const jobId = current?.id ?? randomUUID();
    if (current) {
      sqlite
        .prepare(
          "UPDATE enrollment_job SET credential_id = ?, status = 'queued', stage = 'queued', error_code = NULL, error_message = NULL, lease_owner = NULL, lease_expires_at = NULL, updated_at = ? WHERE id = ?",
        )
        .run(credential.id, now, jobId);
    } else {
      sqlite
        .prepare(
          "INSERT INTO enrollment_job (id, system_id, credential_id, status, stage, created_at, updated_at) VALUES (?, ?, ?, 'queued', 'queued', ?, ?)",
        )
        .run(jobId, canonicalId, credential.id, now, now);
    }
    sqlite
      .prepare("UPDATE system SET status = 'installing', updated_at = ? WHERE id = ?")
      .run(now, canonicalId);
    return { jobId, status: "queued" as const, stage: "queued" as const };
  });
  return retry();
}

export function decommissionSystem(
  systemId: string,
  now = Date.now(),
): { systemId: string; status: "revoked"; uninstall: "pending/manual" } {
  const { sqlite } = getDatabase();
  const decommission = sqlite.transaction(() => {
    const canonicalId = resolveSystemId(sqlite, systemId);
    const system = sqlite.prepare("SELECT id FROM system WHERE id = ? LIMIT 1").get(canonicalId);
    if (!system) throw new LifecycleError("System not found.", "not-found");
    sqlite
      .prepare(
        "UPDATE agent SET revoked_at = ?, updated_at = ? WHERE system_id = ? AND revoked_at IS NULL",
      )
      .run(now, now, canonicalId);
    sqlite
      .prepare("UPDATE system SET status = 'revoked', updated_at = ? WHERE id = ?")
      .run(now, canonicalId);
    sqlite
      .prepare(
        "INSERT INTO audit_event (id, action, resource_type, resource_id, metadata, created_at) VALUES (?, 'decommission', 'system', ?, ?, ?)",
      )
      .run(randomUUID(), canonicalId, JSON.stringify({ uninstall: "pending/manual" }), now);
    return {
      systemId: canonicalId,
      status: "revoked" as const,
      uninstall: "pending/manual" as const,
    };
  });
  return decommission();
}
