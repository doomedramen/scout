import { once } from "node:events";
import { generateKeyPairSync, randomUUID, sign } from "node:crypto";

import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { ensureNetworkSegment } from "@/lib/server/discovery";
import { openRelayChannel, relayChannelMessage } from "@/lib/server/relay";
import { createRelayDuplex, prepareRelaySocket } from "@/lib/server/relay-client";

describe("server-side relay socket", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    closeDatabase();
  });

  it("maps Node socket writes and reads to the paired relay streams", async () => {
    let inboundController: ReadableStreamDefaultController<Uint8Array> | undefined;
    const sentToAgent: Uint8Array[] = [];
    let closedToAgent = false;
    const channel = {
      relayId: "relay-1",
      target: { address: "192.0.2.20", port: 22 },
      readable: new ReadableStream<Uint8Array>({
        start: (controller) => {
          inboundController = controller;
        },
      }),
      agentReadable: new ReadableStream<Uint8Array>(),
      sendToAgent: (chunk: Uint8Array) => sentToAgent.push(chunk),
      closeToAgent: () => {
        closedToAgent = true;
      },
    };
    const socket = createRelayDuplex(channel);

    socket.write(Buffer.from("to-agent"));
    expect(sentToAgent).toEqual([new Uint8Array(Buffer.from("to-agent"))]);

    const received = once(socket, "data");
    inboundController!.enqueue(new TextEncoder().encode("from-agent"));
    await expect(received).resolves.toEqual([Buffer.from("from-agent")]);

    socket.end();
    await once(socket, "finish");
    expect(closedToAgent).toBe(true);
  });

  it("leases a healthy same-segment scanner and waits for both signed channels", async () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    const now = Date.now();
    const segment = ensureNetworkSegment(
      {
        cidr: "192.0.2.0/24",
        interfaceName: "test0",
        gateway: "192.0.2.1",
        sourceAddress: "192.0.2.10",
        provenanceKey: "relay-client-segment",
      },
      now,
    );
    const scannerSystemId = randomUUID();
    const targetSystemId = randomUUID();
    sqlite
      .prepare(
        "INSERT INTO system (id, segment_id, display_name, status, created_at, updated_at) VALUES (?, ?, 'scanner', 'online', ?, ?), (?, ?, 'target', 'needs-access', ?, ?)",
      )
      .run(scannerSystemId, segment.id, now, now, targetSystemId, segment.id, now, now);
    const keys = generateKeyPairSync("ed25519");
    const publicKey = keys.publicKey
      .export({ format: "der", type: "spki" })
      .subarray(-32)
      .toString("base64url");
    sqlite
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, last_heartbeat_at, created_at, updated_at) VALUES ('relay-agent', ?, ?, 'linux', 'x86_64', '0.1.0', ?, ?, ?)",
      )
      .run(scannerSystemId, publicKey, now, now, now);
    sqlite
      .prepare(
        "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, source, observed_at, expires_at) VALUES (?, ?, 'ssh', '192.0.2.20', 22, 'open', 'agent-scan', ?, ?)",
      )
      .run(randomUUID(), targetSystemId, now, now + 60_000);

    const openChannels = (async () => {
      let row:
        | {
            relayId: string;
            targetSystemId: string;
            targetAddress: string;
            targetPort: number;
            nonce: string;
            expiresAt: string;
          }
        | undefined;
      while (!row) {
        row = sqlite
          .prepare(
            "SELECT json_extract(payload, '$.relayId') AS relayId, json_extract(payload, '$.targetSystemId') AS targetSystemId, json_extract(payload, '$.targetAddress') AS targetAddress, json_extract(payload, '$.port') AS targetPort, json_extract(payload, '$.nonce') AS nonce, json_extract(payload, '$.expiresAt') AS expiresAt FROM scan_task WHERE kind = 'relay-connect' ORDER BY created_at DESC LIMIT 1",
          )
          .get() as typeof row;
        if (!row) await new Promise((resolve) => setTimeout(resolve, 5));
      }
      for (const direction of ["upstream", "downstream"] as const) {
        const unsigned = {
          relayId: row.relayId,
          agentId: "relay-agent",
          targetSystemId: row.targetSystemId,
          targetAddress: row.targetAddress,
          targetPort: row.targetPort,
          direction,
          nonce: row.nonce,
          expiresAt: row.expiresAt,
        };
        openRelayChannel({
          ...unsigned,
          signature: sign(
            null,
            Buffer.from(relayChannelMessage(unsigned)),
            keys.privateKey,
          ).toString("base64url"),
        });
      }
    })();

    const socket = await prepareRelaySocket(
      targetSystemId,
      { address: "192.0.2.20", port: 22 },
      { timeoutMs: 1_000, pollMs: 5 },
    );
    await openChannels;
    expect(socket).toBeTruthy();
    socket.destroy();
  });
});
