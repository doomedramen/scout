import { afterEach, describe, expect, it } from "vitest";
import { generateKeyPairSync, randomBytes, randomUUID, sign } from "node:crypto";

import { GET as downstream } from "@/app/api/agent/v1/relay/[relayId]/downstream/route";
import { POST as openRelay } from "@/app/api/agent/v1/relay/open/route";
import { POST as upstream } from "@/app/api/agent/v1/relay/[relayId]/upstream/route";
import { signedRequestMessage } from "@/lib/server/agent-protocol";
import {
  getRelayServerChannel,
  relayChannelMessage,
  type RelayChannelOpen,
} from "@/lib/server/relay";
import { closeDatabase, getDatabase } from "@/lib/server/db";
import { ensureNetworkSegment } from "@/lib/server/discovery";

const AGENT_ID = "relay-agent";

describe("signed paired SSH relay", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    delete process.env.SCOUT_SSH_PORT;
    closeDatabase();
  });

  it("accepts a target-scoped channel and forwards raw bytes in both directions", async () => {
    const fixture = createRelayFixture();
    const relayId = randomUUID();
    const common = makeOpen(fixture, { relayId });

    const opened = await openRelay(openRequest(fixture, common));
    expect(opened.status).toBe(201);
    expect(await opened.json()).toMatchObject({ relayId, direction: "upstream" });

    const openedDownstreamInput = makeOpen(fixture, {
      relayId: common.relayId,
      targetSystemId: common.targetSystemId,
      targetAddress: common.targetAddress,
      targetPort: common.targetPort,
      nonce: common.nonce,
      expiresAt: common.expiresAt,
      direction: "downstream",
    });
    const openedDownstream = await openRelay(openRequest(fixture, openedDownstreamInput));
    expect(openedDownstream.status).toBe(200);

    const channel = getRelayServerChannel(relayId);
    expect(channel?.target).toEqual({ address: "192.0.2.20", port: 22 });
    expect(channel).not.toBeNull();

    const reader = channel!.readable.getReader();
    const upstreamResponse = await upstream(
      streamRequest(
        fixture,
        `/api/agent/v1/relay/${relayId}/upstream`,
        common,
        new Uint8Array([0, 1, 2, 255]),
      ),
      { params: Promise.resolve({ relayId }) },
    );
    expect(upstreamResponse.status).toBe(202);
    await expect(reader.read()).resolves.toMatchObject({
      done: false,
      value: new Uint8Array([0, 1, 2, 255]),
    });

    const downstreamResponsePromise = downstream(
      streamRequest(
        fixture,
        `/api/agent/v1/relay/${relayId}/downstream`,
        { ...openedDownstreamInput },
        undefined,
        "GET",
      ),
      { params: Promise.resolve({ relayId }) },
    );
    const downstreamResponse = await downstreamResponsePromise;
    channel!.sendToAgent(new Uint8Array([9, 8, 7]));
    channel!.closeToAgent();
    expect(downstreamResponse.status).toBe(200);
    await expect(downstreamResponse.arrayBuffer()).resolves.toEqual(
      new Uint8Array([9, 8, 7]).buffer,
    );
  });

  it("rejects invalid signatures, stale channels, and targets without current SSH evidence", async () => {
    const fixture = createRelayFixture();
    const common = makeOpen(fixture);
    const invalid = await openRelay(openRequest(fixture, { ...common, signature: "a".repeat(86) }));
    expect(invalid.status).toBe(401);

    fixture.sqlite
      .prepare("UPDATE access_evidence SET expires_at = ? WHERE system_id = ?")
      .run(Date.now() - 1, fixture.targetSystemId);
    const noEvidence = await openRelay(openRequest(fixture, makeOpen(fixture)));
    expect(noEvidence.status).toBe(409);

    const expired = makeOpen(fixture, { expiresAt: new Date(Date.now() - 1).toISOString() });
    const expiredResponse = await openRelay(openRequest(fixture, expired));
    expect(expiredResponse.status).toBe(410);
  });

  it("does not allow a second agent or a mismatched stream direction to reuse a channel", async () => {
    const fixture = createRelayFixture();
    const common = makeOpen(fixture);
    expect((await openRelay(openRequest(fixture, common))).status).toBe(201);

    const second = createSecondAgent(fixture);
    const secondInput = makeOpen(second, {
      relayId: common.relayId,
      targetSystemId: common.targetSystemId,
      targetAddress: common.targetAddress,
      targetPort: common.targetPort,
      nonce: common.nonce,
      expiresAt: common.expiresAt,
      direction: "upstream",
    });
    const secondRequest = await openRelay(openRequest(second, secondInput));
    expect(secondRequest.status).toBe(409);

    const wrongDirection = await upstream(
      streamRequest(
        fixture,
        `/api/agent/v1/relay/${common.relayId}/upstream`,
        { ...common, direction: "downstream", signature: "a".repeat(86) },
        new Uint8Array([1]),
      ),
      { params: Promise.resolve({ relayId: common.relayId }) },
    );
    expect(wrongDirection.status).toBe(401);
  });
});

function createRelayFixture() {
  process.env.SCOUT_DATABASE_URL = "file::memory:";
  const { sqlite } = getDatabase();
  const keys = generateKeyPairSync("ed25519");
  const publicKey = keys.publicKey
    .export({ format: "der", type: "spki" })
    .subarray(-32)
    .toString("base64url");
  const now = Date.now();
  const segment = ensureNetworkSegment(
    {
      cidr: "192.0.2.0/24",
      interfaceName: "test0",
      gateway: "192.0.2.1",
      sourceAddress: "192.0.2.10",
      provenanceKey: "relay-segment",
    },
    now,
  );
  const targetSystemId = randomUUID();
  sqlite
    .prepare(
      "INSERT INTO system (id, segment_id, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'needs-access', ?, ?)",
    )
    .run("relay-system", segment.id, "relay-system", now, now);
  sqlite
    .prepare(
      "INSERT INTO system (id, segment_id, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'needs-access', ?, ?)",
    )
    .run(targetSystemId, segment.id, "target-system", now, now);
  sqlite
    .prepare(
      "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, last_heartbeat_at, created_at, updated_at) VALUES (?, ?, ?, 'linux', 'x86_64', '0.1.0', ?, ?, ?)",
    )
    .run(AGENT_ID, "relay-system", publicKey, now, now, now);
  sqlite
    .prepare(
      "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, fingerprint, source, observed_at, expires_at) VALUES (?, ?, 'ssh', ?, 22, 'open', ?, 'agent-scan', ?, ?)",
    )
    .run(randomUUID(), targetSystemId, "192.0.2.20", "SHA256:target", now, now + 60_000);
  return { sqlite, privateKey: keys.privateKey, targetSystemId, agentId: AGENT_ID };
}

function createSecondAgent(fixture: ReturnType<typeof createRelayFixture>) {
  const keys = generateKeyPairSync("ed25519");
  const publicKey = keys.publicKey
    .export({ format: "der", type: "spki" })
    .subarray(-32)
    .toString("base64url");
  const now = Date.now();
  const agentId = "second-relay-agent";
  fixture.sqlite
    .prepare(
      "INSERT INTO system (id, segment_id, display_name, status, created_at, updated_at) VALUES (?, (SELECT segment_id FROM system WHERE id = 'relay-system'), ?, 'needs-access', ?, ?)",
    )
    .run("second-relay-system", "second-relay-system", now, now);
  fixture.sqlite
    .prepare(
      "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, last_heartbeat_at, created_at, updated_at) VALUES (?, 'second-relay-system', ?, 'linux', 'x86_64', '0.1.0', ?, ?, ?)",
    )
    .run(agentId, publicKey, now, now, now);
  return { ...fixture, agentId, privateKey: keys.privateKey };
}

function makeOpen(
  fixture: ReturnType<typeof createRelayFixture>,
  overrides: Partial<RelayChannelOpen> = {},
): RelayChannelOpen {
  const unsigned = {
    relayId: randomUUID(),
    agentId: fixture.agentId,
    targetSystemId: fixture.targetSystemId,
    targetAddress: "192.0.2.20",
    targetPort: 22,
    direction: "upstream" as const,
    nonce: randomBytes(32).toString("base64url"),
    expiresAt: new Date(Date.now() + 60_000).toISOString(),
    ...overrides,
  };
  return {
    ...unsigned,
    signature: sign(null, Buffer.from(relayChannelMessage(unsigned)), fixture.privateKey).toString(
      "base64url",
    ),
  };
}

function openRequest(
  fixture: ReturnType<typeof createRelayFixture>,
  input: RelayChannelOpen,
): Request {
  return signedRequest(
    fixture,
    "/api/agent/v1/relay/open",
    input,
    `open-${input.relayId}-${input.direction}`,
  );
}

function streamRequest(
  fixture: ReturnType<typeof createRelayFixture>,
  path: string,
  input: Omit<RelayChannelOpen, "signature"> & { signature?: string },
  body?: Uint8Array,
  method: "POST" | "GET" = "POST",
): Request {
  const timestamp = String(Math.floor(Date.now() / 1_000));
  const requestId = `stream-${input.relayId}-${input.direction}`;
  const outerSignature = sign(
    null,
    Buffer.from(signedRequestMessage(method, path, timestamp, requestId, "")),
    fixture.privateKey,
  ).toString("base64url");
  const channelSignature =
    input.signature ??
    sign(null, Buffer.from(relayChannelMessage(input)), fixture.privateKey).toString("base64url");
  return new Request(`http://localhost${path}`, {
    method,
    headers: {
      "x-scout-agent-id": fixture.agentId,
      "x-scout-timestamp": timestamp,
      "x-scout-request-id": requestId,
      "x-scout-signature": outerSignature,
      "x-scout-relay-signature": channelSignature,
    },
    ...(body ? { body: Buffer.from(body) } : {}),
  });
}

function signedRequest(
  fixture: ReturnType<typeof createRelayFixture>,
  path: string,
  payload: unknown,
  requestId: string,
): Request {
  const body = JSON.stringify(payload);
  const timestamp = String(Math.floor(Date.now() / 1_000));
  const signature = sign(
    null,
    Buffer.from(signedRequestMessage("POST", path, timestamp, requestId, body)),
    fixture.privateKey,
  ).toString("base64url");
  return new Request(`http://localhost${path}`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-scout-agent-id": fixture.agentId,
      "x-scout-timestamp": timestamp,
      "x-scout-request-id": requestId,
      "x-scout-signature": signature,
    },
    body,
  });
}
