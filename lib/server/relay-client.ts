import { Duplex } from "node:stream";

import { getDatabase } from "@/lib/server/db";
import { getRelayServerChannel, type RelayServerChannel } from "@/lib/server/relay";
import { createSignedRelayTask } from "@/lib/server/tasks";
import type { SshEndpoint } from "@/lib/server/ssh";

const RELAY_WAIT_MS = 45_000;
const RELAY_POLL_MS = 100;

export type RelaySocketOptions = {
  now?: number;
  timeoutMs?: number;
  pollMs?: number;
};

export async function prepareRelaySocket(
  targetSystemId: string,
  endpoint: SshEndpoint,
  options: RelaySocketOptions = {},
): Promise<Duplex> {
  const now = options.now ?? Date.now();
  const target = findRelayScanner(targetSystemId, now);
  if (!target) {
    throw new Error("No healthy same-segment Scout agent is available for SSH relay.");
  }
  const task = createSignedRelayTask(
    target.segmentId,
    target.agentId,
    targetSystemId,
    endpoint.address,
    endpoint.port,
    now,
  );
  const channel = await waitForRelayChannel(
    task.payload.relayId,
    options.timeoutMs ?? RELAY_WAIT_MS,
    options.pollMs ?? RELAY_POLL_MS,
  );
  return createRelayDuplex(channel);
}

export function createRelayDuplex(channel: RelayServerChannel): Duplex {
  const reader = channel.readable.getReader();
  let pumping = false;
  let ended = false;

  async function pump() {
    if (pumping || ended) return;
    pumping = true;
    try {
      while (!ended) {
        const next = await reader.read();
        if (next.done) {
          ended = true;
          socket.push(null);
          break;
        }
        if (!socket.push(Buffer.from(next.value))) break;
      }
    } catch (error) {
      ended = true;
      socket.destroy(error instanceof Error ? error : new Error("Relay socket read failed."));
    } finally {
      pumping = false;
    }
  }

  const socket = new Duplex({
    read: () => {
      void pump();
    },
    write: (chunk, _encoding, callback) => {
      try {
        channel.sendToAgent(typeof chunk === "string" ? Buffer.from(chunk) : new Uint8Array(chunk));
        callback();
      } catch (error) {
        callback(error instanceof Error ? error : new Error("Relay socket write failed."));
      }
    },
    final: (callback) => {
      try {
        channel.closeToAgent();
        callback();
      } catch (error) {
        callback(error instanceof Error ? error : new Error("Relay socket close failed."));
      }
    },
    destroy: (error, callback) => {
      ended = true;
      channel.closeToAgent();
      void reader
        .cancel(error ?? undefined)
        .catch(() => undefined)
        .finally(() => callback(error));
    },
  });
  return socket;
}

function findRelayScanner(
  targetSystemId: string,
  now: number,
): { segmentId: string; agentId: string } | undefined {
  const { sqlite } = getDatabase();
  return sqlite
    .prepare(
      `
        SELECT target.segment_id AS segmentId, agent.id AS agentId
        FROM system target
        INNER JOIN system scanner ON scanner.segment_id = target.segment_id
        INNER JOIN agent ON agent.system_id = scanner.id
        WHERE target.id = ?
          AND target.segment_id IS NOT NULL
          AND scanner.id <> target.id
          AND scanner.excluded = 0
          AND agent.revoked_at IS NULL
          AND agent.last_heartbeat_at IS NOT NULL
          AND agent.last_heartbeat_at > ?
        ORDER BY agent.last_heartbeat_at DESC, agent.id ASC
        LIMIT 1
      `,
    )
    .get(targetSystemId, now - 45_000) as { segmentId: string; agentId: string } | undefined;
}

async function waitForRelayChannel(
  relayId: string,
  timeoutMs: number,
  pollMs: number,
): Promise<RelayServerChannel> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const channel = getRelayServerChannel(relayId);
    if (channel) return channel;
    await new Promise((resolve) => setTimeout(resolve, Math.min(pollMs, deadline - Date.now())));
  }
  throw new Error("The scanner agent did not open the SSH relay before it expired.");
}
