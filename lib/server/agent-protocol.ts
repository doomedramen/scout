import { createHash, createPublicKey, verify } from "node:crypto";

import { z } from "zod";

import { getDatabase } from "@/lib/server/db";
import { jsonError, rateLimit } from "@/lib/server/http";

export const enrollInput = z.object({
  invitation: z.string().min(32).max(256),
  publicKey: z.string().regex(/^[A-Za-z0-9_-]{43}$/),
  platform: z.enum(["linux", "macos"]),
  architecture: z.enum(["x86_64", "aarch64"]),
  version: z.string().min(1).max(64),
  proof: z.string().regex(/^[A-Za-z0-9_-]{86}$/),
});

export type EnrollmentInput = z.infer<typeof enrollInput>;

export function enrollmentMessage(input: Omit<EnrollmentInput, "proof">): string {
  return [
    input.invitation,
    input.publicKey,
    input.platform,
    input.architecture,
    input.version,
  ].join("\n");
}

export const heartbeatInput = z.object({
  agentId: z.string().min(1).max(128),
  observedAt: z.string().datetime(),
  taskGeneration: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
  releaseSequence: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
});

const interfaceMetric = z.object({
  name: z.string().min(1).max(128),
  macAddress: z.string().max(64).nullable(),
  addresses: z.array(z.string().max(128)).max(32),
  receivedBytes: z.number().int().nonnegative().nullable(),
  transmittedBytes: z.number().int().nonnegative().nullable(),
});

const filesystemMetric = z.object({
  mountPoint: z.string().min(1).max(512),
  totalBytes: z.number().int().nonnegative().nullable(),
  availableBytes: z.number().int().nonnegative().nullable(),
});

export const telemetryInput = z.object({
  agentId: z.string().min(1).max(128),
  batchId: z.string().min(1).max(128),
  observedAt: z.string().datetime(),
  droppedSamples: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
  host: z.object({
    hostname: z.string().max(256).nullable(),
    operatingSystem: z.string().max(256).nullable(),
    kernelVersion: z.string().max(256).nullable(),
    cpuUsagePercent: z.number().min(0).max(100).nullable(),
    memoryUsedBytes: z.number().int().nonnegative().nullable(),
    memoryTotalBytes: z.number().int().nonnegative().nullable(),
    uptimeSeconds: z.number().int().nonnegative().nullable(),
    filesystems: z.array(filesystemMetric).max(128),
    interfaces: z.array(interfaceMetric).max(128),
  }),
});

export type AgentTelemetry = z.infer<typeof telemetryInput>;

const MAX_BODY_BYTES = 1_048_576;
const MAX_CLOCK_SKEW_SECONDS = 120;

export function bodyDigest(body: string): string {
  return createHash("sha256").update(body, "utf8").digest("base64url");
}

export function signedRequestMessage(
  method: string,
  path: string,
  timestamp: string,
  requestId: string,
  body: string,
): string {
  return [method.toUpperCase(), path, timestamp, requestId, bodyDigest(body)].join("\n");
}

function publicKeyFromRaw(raw: Buffer) {
  if (raw.length !== 32) throw new Error("Agent public key has an invalid length");
  return createPublicKey({
    key: Buffer.concat([Buffer.from("302a300506032b6570032100", "hex"), raw]),
    format: "der",
    type: "spki",
  });
}

export async function authenticateAgentRequest(
  request: Request,
  body: string,
): Promise<{ agentId: string } | Response> {
  if (Buffer.byteLength(body, "utf8") > MAX_BODY_BYTES)
    return jsonError("Agent payload is too large.", 413);
  const agentId = request.headers.get("x-scout-agent-id");
  const timestamp = request.headers.get("x-scout-timestamp");
  const requestId = request.headers.get("x-scout-request-id");
  const signature = request.headers.get("x-scout-signature");
  if (!agentId || !timestamp || !requestId || !signature)
    return jsonError("Agent authentication is incomplete.", 401);
  const limited = rateLimit(request, "agent-request", 120, 60_000);
  if (limited) return limited;
  if (!/^[A-Za-z0-9._:-]{1,128}$/.test(agentId) || !/^[A-Za-z0-9._:-]{1,128}$/.test(requestId))
    return jsonError("Agent authentication is invalid.", 401);
  if (!/^[A-Za-z0-9_-]{86}$/.test(signature))
    return jsonError("Agent request signature is invalid.", 401);

  const timestampSeconds = Number(timestamp);
  if (
    !Number.isSafeInteger(timestampSeconds) ||
    Math.abs(Date.now() / 1_000 - timestampSeconds) > MAX_CLOCK_SKEW_SECONDS
  ) {
    return jsonError("Agent request timestamp is outside the allowed window.", 401);
  }

  const { sqlite } = getDatabase();
  const agent = sqlite
    .prepare(
      "SELECT public_key AS publicKey, revoked_at AS revokedAt FROM agent WHERE id = ? LIMIT 1",
    )
    .get(agentId) as { publicKey: string; revokedAt: number | null } | undefined;
  if (!agent || agent.revokedAt !== null)
    return jsonError("Agent identity is not authorized.", 401);

  try {
    const valid = verify(
      null,
      Buffer.from(
        signedRequestMessage(request.method, requestPath(request), timestamp, requestId, body),
        "utf8",
      ),
      publicKeyFromRaw(Buffer.from(agent.publicKey, "base64url")),
      Buffer.from(signature, "base64url"),
    );
    if (!valid) return jsonError("Agent request signature is invalid.", 401);
  } catch {
    return jsonError("Agent request signature is invalid.", 401);
  }

  try {
    sqlite
      .prepare(
        "INSERT INTO idempotency_receipt (key, operation, resource_id, created_at) VALUES (?, 'agent-request', ?, ?)",
      )
      .run(`agent:${agentId}:${requestId}`, agentId, Date.now());
  } catch {
    return jsonError("Agent request was already received.", 409);
  }
  return { agentId };
}

function requestPath(request: Request): string {
  const url = new URL(request.url);
  return `${url.pathname}${url.search}`;
}
