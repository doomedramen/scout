import { createHash, createPrivateKey, createPublicKey, randomUUID, sign } from "node:crypto";

import { z } from "zod";

import type { ProbeResult } from "@/lib/discovery/scanner";
import { configuredSshPort, hostAddresses } from "@/lib/discovery/network";
import { reconcileScanResults } from "@/lib/server/discovery";
import { getDatabase } from "@/lib/server/db";
import { controlSigningKey } from "@/lib/server/keys";

const CONTROL_PRIVATE_KEY_PREFIX = Buffer.from("302e020100300506032b657004220420", "hex");
const TASK_LEASE_MS = 2 * 60 * 1_000;
const TASK_DEADLINE_MS = 60 * 1_000;

export const taskEnvelopeInput = z.object({
  taskId: z.string().uuid(),
  agentId: z.string().min(1).max(128),
  kind: z.literal("network-scan"),
  generation: z.number().int().positive(),
  policyVersion: z.number().int().positive(),
  payloadDigest: z.string().regex(/^[A-Za-z0-9_-]{43}$/),
  issuedAt: z.string().datetime(),
  expiresAt: z.string().datetime(),
  deadlineAt: z.string().datetime(),
  payload: z.object({
    cidr: z.string().min(1).max(32),
    port: z.number().int().min(1).max(65_535),
    addresses: z.array(z.string().min(1).max(128)).max(256),
  }),
  signature: z.string().regex(/^[A-Za-z0-9_-]+$/),
});

export const scanTaskResultInput = z.object({
  taskId: z.string().uuid(),
  agentId: z.string().min(1).max(128),
  generation: z.number().int().positive(),
  payloadDigest: z.string().regex(/^[A-Za-z0-9_-]{43}$/),
  results: z
    .array(
      z.object({
        address: z.string().min(1).max(128),
        port: z.number().int().min(1).max(65_535),
        outcome: z.enum(["open", "closed", "timeout"]),
        macAddress: z.string().max(64).nullable().optional(),
        fingerprint: z.string().max(256).nullable().optional(),
      }),
    )
    .max(256),
});

export type TaskEnvelope = z.infer<typeof taskEnvelopeInput>;
export type ScanTaskResult = z.infer<typeof scanTaskResultInput>;

export function taskMessage(input: Omit<TaskEnvelope, "signature" | "payload">): string {
  return [
    input.taskId,
    input.agentId,
    input.kind,
    String(input.generation),
    String(input.policyVersion),
    input.payloadDigest,
    input.issuedAt,
    input.expiresAt,
    input.deadlineAt,
  ].join("\n");
}

export function controlPublicKey(): string {
  const publicKey = createPublicKey(controlPrivateKey());
  return publicKey.export({ format: "der", type: "spki" }).subarray(-32).toString("base64url");
}

type CreatedScanTask = {
  id: string;
  payload: { cidr: string; port: number; addresses: string[] };
  envelope: TaskEnvelope;
};

export function createSignedScanTask(
  segmentId: string,
  agentId: string,
  now = Date.now(),
): CreatedScanTask {
  const { sqlite } = getDatabase();
  const segment = sqlite
    .prepare(
      "SELECT cidr, paused, policy_version AS policyVersion FROM network_segment WHERE id = ?",
    )
    .get(segmentId) as { cidr: string; paused: number; policyVersion: number } | undefined;
  if (!segment) throw new Error("Network segment not found.");
  if (segment.paused === 1) throw new Error("Network segment discovery is paused.");
  const agent = sqlite
    .prepare(
      "SELECT 1 FROM agent a INNER JOIN system s ON s.id = a.system_id WHERE a.id = ? AND s.segment_id = ? AND a.revoked_at IS NULL AND a.last_heartbeat_at IS NOT NULL AND a.last_heartbeat_at > ?",
    )
    .get(agentId, segmentId, now - 45_000);
  if (!agent) throw new Error("Scanner agent is not healthy.");

  const id = randomUUID();
  const generation =
    ((
      sqlite
        .prepare("SELECT MAX(generation) AS generation FROM scan_task WHERE segment_id = ?")
        .get(segmentId) as { generation: number | null }
    ).generation ?? 0) + 1;
  const payload = {
    cidr: segment.cidr,
    port: configuredSshPort(),
    addresses: hostAddresses(segment.cidr),
  };
  const payloadDigest = createHash("sha256")
    .update(JSON.stringify(payload), "utf8")
    .digest("base64url");
  const issuedAt = new Date(now).toISOString();
  const expiresAt = new Date(now + TASK_LEASE_MS).toISOString();
  const deadlineAt = new Date(now + TASK_DEADLINE_MS).toISOString();
  const unsigned = {
    taskId: id,
    agentId,
    kind: "network-scan" as const,
    generation,
    policyVersion: segment.policyVersion,
    payloadDigest,
    issuedAt,
    expiresAt,
    deadlineAt,
  };
  const signature = sign(
    null,
    Buffer.from(taskMessage(unsigned), "utf8"),
    controlPrivateKey(),
  ).toString("base64url");
  const envelope = { ...unsigned, payload, signature };

  sqlite
    .prepare(
      "UPDATE scan_task SET status = 'superseded', lease_expires_at = NULL WHERE segment_id = ? AND status = 'leased' AND generation < ?",
    )
    .run(segmentId, generation);
  sqlite
    .prepare(
      "INSERT INTO scan_task (id, segment_id, scanner_agent_id, generation, policy_version, payload, signature, issued_at, status, deadline_at, lease_expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'leased', ?, ?, ?)",
    )
    .run(
      id,
      segmentId,
      agentId,
      generation,
      segment.policyVersion,
      JSON.stringify(payload),
      signature,
      now,
      now + TASK_DEADLINE_MS,
      now + TASK_LEASE_MS,
      now,
    );
  return { id, payload, envelope };
}

export function getPendingTaskEnvelope(agentId: string, now = Date.now()): TaskEnvelope | null {
  const { sqlite } = getDatabase();
  const row = sqlite
    .prepare(
      "SELECT st.id AS taskId, st.scanner_agent_id AS agentId, st.generation, st.policy_version AS policyVersion, st.payload, st.signature, st.issued_at AS issuedAt, st.deadline_at AS deadlineAt, st.lease_expires_at AS expiresAt FROM scan_task st INNER JOIN network_segment ns ON ns.id = st.segment_id INNER JOIN agent a ON a.id = st.scanner_agent_id WHERE st.scanner_agent_id = ? AND st.status = 'leased' AND st.lease_expires_at > ? AND ns.paused = 0 AND a.revoked_at IS NULL ORDER BY st.created_at LIMIT 1",
    )
    .get(agentId, now) as
    | {
        taskId: string;
        agentId: string;
        generation: number;
        policyVersion: number;
        payload: string;
        signature: string;
        issuedAt: number;
        deadlineAt: number;
        expiresAt: number;
      }
    | undefined;
  if (!row) return null;
  const payloadDigest = createHash("sha256").update(row.payload, "utf8").digest("base64url");
  return taskEnvelopeInput.parse({
    taskId: row.taskId,
    agentId: row.agentId,
    kind: "network-scan",
    generation: row.generation,
    policyVersion: row.policyVersion,
    payloadDigest,
    issuedAt: new Date(row.issuedAt).toISOString(),
    expiresAt: new Date(row.expiresAt).toISOString(),
    deadlineAt: new Date(row.deadlineAt).toISOString(),
    payload: JSON.parse(row.payload),
    signature: row.signature,
  });
}

export function completeScanTask(
  input: ScanTaskResult,
  now = Date.now(),
):
  | { accepted: true }
  | {
      accepted: false;
      reason: "unknown" | "already-complete" | "expired" | "superseded";
    } {
  const { sqlite } = getDatabase();
  const row = sqlite
    .prepare(
      "SELECT st.segment_id AS segmentId, st.scanner_agent_id AS scannerAgentId, st.generation, st.payload, st.status, st.deadline_at AS deadlineAt, st.lease_expires_at AS leaseExpiresAt, st.policy_version AS policyVersion, ns.policy_version AS currentPolicyVersion, ns.paused, a.revoked_at AS revokedAt, a.last_heartbeat_at AS lastHeartbeatAt FROM scan_task st INNER JOIN network_segment ns ON ns.id = st.segment_id LEFT JOIN agent a ON a.id = st.scanner_agent_id WHERE st.id = ?",
    )
    .get(input.taskId) as
    | {
        segmentId: string;
        scannerAgentId: string | null;
        generation: number;
        payload: string;
        status: string;
        deadlineAt: number;
        leaseExpiresAt: number | null;
        policyVersion: number;
        currentPolicyVersion: number;
        paused: number;
        revokedAt: number | null;
        lastHeartbeatAt: number | null;
      }
    | undefined;
  if (!row) return { accepted: false, reason: "unknown" };
  if (row.status === "completed") return { accepted: false, reason: "already-complete" };
  if (row.scannerAgentId !== input.agentId || row.generation !== input.generation)
    return { accepted: false, reason: "superseded" };
  if (
    row.paused === 1 ||
    row.policyVersion !== row.currentPolicyVersion ||
    row.revokedAt !== null ||
    row.lastHeartbeatAt === null ||
    row.lastHeartbeatAt <= now - 45_000
  )
    return { accepted: false, reason: "superseded" };
  if (row.deadlineAt <= now || (row.leaseExpiresAt !== null && row.leaseExpiresAt <= now))
    return { accepted: false, reason: "expired" };
  const digest = createHash("sha256").update(row.payload, "utf8").digest("base64url");
  if (digest !== input.payloadDigest) return { accepted: false, reason: "superseded" };

  const changed = sqlite
    .prepare(
      "UPDATE scan_task SET status = 'completed', completed_at = ? WHERE id = ? AND status = 'leased'",
    )
    .run(now, input.taskId);
  if (changed.changes !== 1) return { accepted: false, reason: "already-complete" };
  reconcileScanResults(row.segmentId, input.results as ProbeResult[], now, "agent-scan");
  return { accepted: true };
}

function controlPrivateKey() {
  return createPrivateKey({
    key: Buffer.concat([CONTROL_PRIVATE_KEY_PREFIX, controlSigningKey()]),
    format: "der",
    type: "pkcs8",
  });
}
