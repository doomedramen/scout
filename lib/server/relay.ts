import { isIP } from "node:net";

import { z } from "zod";

import { verifyAgentMessage } from "@/lib/server/agent-protocol";
import { configuredSshPort } from "@/lib/discovery/network";
import { getDatabase } from "@/lib/server/db";

const RELAY_TTL_MS = 2 * 60 * 1_000;
const MAX_QUEUED_BYTES = 1 * 1024 * 1024;

export const relayChannelOpenInput = z.object({
  relayId: z.string().uuid(),
  agentId: z.string().min(1).max(128),
  targetSystemId: z.string().uuid(),
  targetAddress: z.string().min(1).max(128),
  targetPort: z.number().int().min(1).max(65_535),
  direction: z.enum(["upstream", "downstream"]),
  nonce: z.string().regex(/^[A-Za-z0-9_-]{43}$/),
  expiresAt: z.string().datetime(),
  signature: z.string().regex(/^[A-Za-z0-9_-]{86}$/),
});

export type RelayChannelOpen = z.infer<typeof relayChannelOpenInput>;
export type RelayDirection = RelayChannelOpen["direction"];

export class RelayError extends Error {
  constructor(
    message: string,
    public readonly code: "invalid" | "unauthorized" | "conflict" | "expired" | "unavailable",
  ) {
    super(message);
  }
}

export type RelayChannelReceipt = {
  relayId: string;
  agentId: string;
  targetSystemId: string;
  target: { address: string; port: number };
  direction: RelayDirection;
  expiresAt: string;
  created: boolean;
};

export type RelayServerChannel = {
  relayId: string;
  target: { address: string; port: number };
  readable: ReadableStream<Uint8Array>;
  agentReadable: ReadableStream<Uint8Array>;
  sendToAgent(chunk: Uint8Array): void;
  closeToAgent(): void;
};

type RelayRecord = {
  id: string;
  agentId: string;
  targetSystemId: string;
  targetAddress: string;
  targetPort: number;
  nonce: string;
  expiresAt: number;
  upstreamSignature: string | null;
  downstreamSignature: string | null;
};

class BytePipe {
  readonly readable: ReadableStream<Uint8Array>;
  private controller: ReadableStreamDefaultController<Uint8Array> | undefined;
  private readonly queue: Uint8Array[] = [];
  private queuedBytes = 0;
  private ended = false;
  private failure: Error | undefined;

  constructor() {
    this.readable = new ReadableStream<Uint8Array>({
      start: (controller) => {
        this.controller = controller;
        this.flush();
      },
      pull: () => this.flush(),
      cancel: () => this.close(),
    });
  }

  push(chunk: Uint8Array): void {
    if (this.ended || this.failure) return;
    const copy = new Uint8Array(chunk);
    if (
      this.controller &&
      this.controller.desiredSize !== null &&
      this.controller.desiredSize > 0
    ) {
      this.controller.enqueue(copy);
      return;
    }
    if (this.queuedBytes + copy.byteLength > MAX_QUEUED_BYTES) {
      this.error(new Error("Relay stream backpressure limit was exceeded."));
      return;
    }
    this.queue.push(copy);
    this.queuedBytes += copy.byteLength;
  }

  close(): void {
    if (this.ended || this.failure) return;
    this.ended = true;
    this.flush();
  }

  error(error: Error): void {
    if (this.ended || this.failure) return;
    this.failure = error;
    this.queue.length = 0;
    this.queuedBytes = 0;
    this.controller?.error(error);
  }

  private flush(): void {
    if (!this.controller || this.failure) return;
    while (
      this.queue.length > 0 &&
      this.controller.desiredSize !== null &&
      this.controller.desiredSize > 0
    ) {
      const chunk = this.queue.shift()!;
      this.queuedBytes -= chunk.byteLength;
      this.controller.enqueue(chunk);
    }
    if (this.ended && this.queue.length === 0) this.controller.close();
  }
}

type LiveRelay = {
  inbound: BytePipe;
  outbound: BytePipe;
  upstreamAttached: boolean;
  downstreamAttached: boolean;
  expiresTimer: NodeJS.Timeout;
};

const liveRelays = new Map<string, LiveRelay>();

export function relayChannelMessage(input: Omit<RelayChannelOpen, "signature">): string {
  return [
    input.relayId,
    input.agentId,
    input.targetSystemId,
    input.targetAddress,
    String(input.targetPort),
    input.direction,
    input.nonce,
    input.expiresAt,
  ].join("\n");
}

export function openRelayChannel(input: unknown, now = Date.now()): RelayChannelReceipt {
  const parsed = parseOpen(input);
  const expiry = Date.parse(parsed.expiresAt);
  if (!Number.isFinite(expiry) || expiry <= now || expiry > now + RELAY_TTL_MS) {
    throw new RelayError(
      "The relay channel expiry is invalid or too far in the future.",
      "expired",
    );
  }
  if (isIP(parsed.targetAddress) !== 4 || parsed.targetPort !== configuredSshPort()) {
    throw new RelayError("Relay targets must be an observed IPv4 SSH endpoint.", "invalid");
  }
  if (!verifyAgentMessage(parsed.agentId, relayChannelMessage(parsed), parsed.signature)) {
    throw new RelayError("The relay channel signature is invalid.", "unauthorized");
  }

  const { sqlite } = getDatabase();
  const target = sqlite
    .prepare(
      `
        SELECT
          source.segment_id AS agentSegmentId,
          source.excluded AS agentExcluded,
          target.segment_id AS targetSegmentId,
          target.excluded AS targetExcluded,
          EXISTS (
            SELECT 1 FROM access_evidence evidence
            WHERE evidence.system_id = target.id
              AND evidence.method = 'ssh'
              AND evidence.address = ?
              AND evidence.port = ?
              AND evidence.outcome = 'open'
              AND evidence.expires_at > ?
          ) AS currentEvidence
        FROM agent
        INNER JOIN system source ON source.id = agent.system_id
        INNER JOIN system target ON target.id = ?
        WHERE agent.id = ? AND agent.revoked_at IS NULL
      `,
    )
    .get(parsed.targetAddress, parsed.targetPort, now, parsed.targetSystemId, parsed.agentId) as
    | {
        agentSegmentId: string | null;
        agentExcluded: number;
        targetSegmentId: string | null;
        targetExcluded: number;
        currentEvidence: number;
      }
    | undefined;
  if (!target || target.agentExcluded === 1 || target.targetExcluded === 1) {
    throw new RelayError("The relay agent or target is not authorized.", "unauthorized");
  }
  if (
    !target.agentSegmentId ||
    target.agentSegmentId !== target.targetSegmentId ||
    target.currentEvidence !== 1
  ) {
    throw new RelayError(
      "The target has no current SSH evidence from this agent's segment.",
      "conflict",
    );
  }

  const receipt = sqlite.transaction(() => {
    const existing = sqlite
      .prepare(
        "SELECT id, agent_id AS agentId, target_system_id AS targetSystemId, target_address AS targetAddress, target_port AS targetPort, nonce, expires_at AS expiresAt, upstream_signature AS upstreamSignature, downstream_signature AS downstreamSignature FROM relay_channel WHERE id = ?",
      )
      .get(parsed.relayId) as RelayRecord | undefined;
    if (existing) {
      if (
        existing.agentId !== parsed.agentId ||
        existing.targetSystemId !== parsed.targetSystemId ||
        existing.targetAddress !== parsed.targetAddress ||
        existing.targetPort !== parsed.targetPort ||
        existing.nonce !== parsed.nonce ||
        existing.expiresAt !== expiry
      ) {
        throw new RelayError("The relay ID is already bound to another target.", "conflict");
      }
      const column =
        parsed.direction === "upstream" ? "upstream_signature" : "downstream_signature";
      const existingSignature =
        parsed.direction === "upstream" ? existing.upstreamSignature : existing.downstreamSignature;
      if (existingSignature && existingSignature !== parsed.signature) {
        throw new RelayError(
          "The relay direction is already bound to another signature.",
          "conflict",
        );
      }
      if (!existingSignature) {
        sqlite
          .prepare(`UPDATE relay_channel SET ${column} = ? WHERE id = ?`)
          .run(parsed.signature, parsed.relayId);
      }
      return { ...existing, created: false };
    }

    sqlite
      .prepare(
        "INSERT INTO relay_channel (id, agent_id, target_system_id, target_address, target_port, nonce, expires_at, upstream_signature, downstream_signature, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
      )
      .run(
        parsed.relayId,
        parsed.agentId,
        parsed.targetSystemId,
        parsed.targetAddress,
        parsed.targetPort,
        parsed.nonce,
        expiry,
        parsed.direction === "upstream" ? parsed.signature : null,
        parsed.direction === "downstream" ? parsed.signature : null,
        now,
      );
    return {
      id: parsed.relayId,
      agentId: parsed.agentId,
      targetSystemId: parsed.targetSystemId,
      targetAddress: parsed.targetAddress,
      targetPort: parsed.targetPort,
      nonce: parsed.nonce,
      expiresAt: expiry,
      upstreamSignature: parsed.direction === "upstream" ? parsed.signature : null,
      downstreamSignature: parsed.direction === "downstream" ? parsed.signature : null,
      created: true,
    };
  })();

  ensureLiveRelay(parsed.relayId, expiry);
  return {
    relayId: receipt.id,
    agentId: receipt.agentId,
    targetSystemId: receipt.targetSystemId,
    target: { address: receipt.targetAddress, port: receipt.targetPort },
    direction: parsed.direction,
    expiresAt: new Date(receipt.expiresAt).toISOString(),
    created: receipt.created,
  };
}

export function getRelayServerChannel(
  relayId: string,
  now = Date.now(),
): RelayServerChannel | null {
  const record = relayRecord(relayId);
  if (
    !record ||
    record.expiresAt <= now ||
    !record.upstreamSignature ||
    !record.downstreamSignature
  ) {
    return null;
  }
  const live = liveRelays.get(relayId);
  if (!live) return null;
  return serverChannel(relayId, record, live);
}

export function authorizeRelayStream(
  relayId: string,
  direction: RelayDirection,
  agentId: string,
  signature: string,
  now = Date.now(),
): RelayServerChannel {
  const record = relayRecord(relayId);
  if (!record) throw new RelayError("Relay channel was not found.", "conflict");
  if (record.expiresAt <= now) throw new RelayError("Relay channel has expired.", "expired");
  if (record.agentId !== agentId)
    throw new RelayError("Relay agent is not authorized.", "unauthorized");
  const expected = direction === "upstream" ? record.upstreamSignature : record.downstreamSignature;
  if (!expected || expected !== signature) {
    throw new RelayError(
      "Relay channel signature does not match the opened direction.",
      "unauthorized",
    );
  }
  const live = liveRelays.get(relayId);
  if (!live)
    throw new RelayError("Relay channel data plane is no longer available.", "unavailable");
  if (direction === "upstream") {
    if (live.upstreamAttached)
      throw new RelayError("The upstream relay stream is already attached.", "conflict");
    live.upstreamAttached = true;
  } else {
    if (live.downstreamAttached)
      throw new RelayError("The downstream relay stream is already attached.", "conflict");
    live.downstreamAttached = true;
  }
  return serverChannel(relayId, record, live);
}

export async function pipeRelayUpstream(
  channel: RelayServerChannel,
  body: ReadableStream<Uint8Array>,
): Promise<void> {
  const reader = body.getReader();
  try {
    while (true) {
      const next = await reader.read();
      if (next.done) break;
      channelFromServerChannel(channel).inbound.push(next.value);
    }
  } catch (error) {
    channelFromServerChannel(channel).inbound.error(
      error instanceof Error ? error : new Error("Relay upstream stream failed."),
    );
    throw error;
  } finally {
    channelFromServerChannel(channel).inbound.close();
    reader.releaseLock();
  }
}

function parseOpen(input: unknown): RelayChannelOpen {
  try {
    return relayChannelOpenInput.parse(input);
  } catch {
    throw new RelayError("Relay channel parameters are invalid.", "invalid");
  }
}

function relayRecord(relayId: string): RelayRecord | undefined {
  const { sqlite } = getDatabase();
  return sqlite
    .prepare(
      "SELECT id, agent_id AS agentId, target_system_id AS targetSystemId, target_address AS targetAddress, target_port AS targetPort, nonce, expires_at AS expiresAt, upstream_signature AS upstreamSignature, downstream_signature AS downstreamSignature FROM relay_channel WHERE id = ?",
    )
    .get(relayId) as RelayRecord | undefined;
}

function ensureLiveRelay(relayId: string, expiresAt: number): LiveRelay {
  const existing = liveRelays.get(relayId);
  if (existing) return existing;
  const live: LiveRelay = {
    inbound: new BytePipe(),
    outbound: new BytePipe(),
    upstreamAttached: false,
    downstreamAttached: false,
    expiresTimer: setTimeout(
      () => {
        live.inbound.close();
        live.outbound.close();
        liveRelays.delete(relayId);
      },
      Math.max(0, expiresAt - Date.now()),
    ),
  };
  live.expiresTimer.unref();
  liveRelays.set(relayId, live);
  return live;
}

function serverChannel(relayId: string, record: RelayRecord, live: LiveRelay): RelayServerChannel {
  return {
    relayId,
    target: { address: record.targetAddress, port: record.targetPort },
    readable: live.inbound.readable,
    agentReadable: live.outbound.readable,
    sendToAgent: (chunk) => live.outbound.push(chunk),
    closeToAgent: () => live.outbound.close(),
  };
}

function channelFromServerChannel(channel: RelayServerChannel): LiveRelay {
  const live = liveRelays.get(channel.relayId);
  if (!live)
    throw new RelayError("Relay channel data plane is no longer available.", "unavailable");
  return live;
}
