import { afterEach, describe, expect, it } from "vitest";
import { generateKeyPairSync, sign as signMessage } from "node:crypto";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { heartbeatInput, signedRequestMessage, telemetryInput } from "@/lib/server/agent-protocol";
import { POST as heartbeat } from "@/app/api/agent/v1/heartbeat/route";
import { POST as telemetry } from "@/app/api/agent/v1/telemetry/route";

const AGENT_ID = "agent-1";
const SYSTEM_ID = "system-1";

describe("signed agent protocol", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    closeDatabase();
  });

  it("accepts a signed heartbeat and advances the agent watermarks", async () => {
    const fixture = createAgent();
    const payload = {
      agentId: AGENT_ID,
      observedAt: new Date().toISOString(),
      taskGeneration: 4,
      releaseSequence: 9,
    };
    const request = signedRequest(fixture, "/api/agent/v1/heartbeat", payload, "request-1");

    const response = await heartbeat(request);

    expect(response.status).toBe(200);
    expect(heartbeatInput.parse(payload)).toEqual(payload);
    expect(
      fixture.sqlite
        .prepare(
          "SELECT last_heartbeat_at, task_generation, release_sequence FROM agent WHERE id = ?",
        )
        .get(AGENT_ID),
    ).toMatchObject({
      task_generation: 4,
      release_sequence: 9,
    });
  });

  it("rejects a replayed request id", async () => {
    const fixture = createAgent();
    const payload = {
      agentId: AGENT_ID,
      observedAt: new Date().toISOString(),
      taskGeneration: 1,
      releaseSequence: 1,
    };
    const request = signedRequest(fixture, "/api/agent/v1/heartbeat", payload, "replayed");

    expect((await heartbeat(request)).status).toBe(200);
    const replay = signedRequest(fixture, "/api/agent/v1/heartbeat", payload, "replayed");
    expect((await heartbeat(replay)).status).toBe(409);
  });

  it("rejects a changed body, stale timestamp, and revoked identity", async () => {
    const fixture = createAgent();
    const payload = {
      agentId: AGENT_ID,
      observedAt: new Date().toISOString(),
      taskGeneration: 1,
      releaseSequence: 1,
    };
    const valid = signedRequest(fixture, "/api/agent/v1/heartbeat", payload, "valid");
    const badSignature = new Request(valid.url, {
      method: "POST",
      headers: { ...Object.fromEntries(valid.headers), "x-scout-request-id": "bad-body" },
      body: JSON.stringify({ ...payload, taskGeneration: 99 }),
    });
    expect((await heartbeat(badSignature)).status).toBe(401);

    const stale = signedRequest(fixture, "/api/agent/v1/heartbeat", payload, "stale", {
      timestamp: Math.floor(Date.now() / 1_000) - 121,
    });
    expect((await heartbeat(stale)).status).toBe(401);

    fixture.sqlite
      .prepare("UPDATE agent SET revoked_at = ? WHERE id = ?")
      .run(Date.now(), AGENT_ID);
    const revoked = signedRequest(fixture, "/api/agent/v1/heartbeat", payload, "revoked");
    expect((await heartbeat(revoked)).status).toBe(401);
  });

  it("stores a local-host telemetry sample and deduplicates its batch", async () => {
    const fixture = createAgent();
    const payload = {
      agentId: AGENT_ID,
      batchId: "batch-1",
      observedAt: new Date().toISOString(),
      droppedSamples: 0,
      host: {
        hostname: "host-1",
        operatingSystem: "Linux 6.1",
        kernelVersion: "6.1.0",
        cpuUsagePercent: 12.5,
        memoryUsedBytes: 10,
        memoryTotalBytes: 20,
        uptimeSeconds: 30,
        filesystems: [{ mountPoint: "/", totalBytes: 100, availableBytes: 40 }],
        interfaces: [
          {
            name: "eth0",
            macAddress: "02:00:00:00:00:01",
            addresses: ["192.0.2.10"],
            receivedBytes: 1,
            transmittedBytes: 2,
          },
        ],
      },
    };

    expect(telemetryInput.parse(payload)).toEqual(payload);
    expect(
      (await telemetry(signedRequest(fixture, "/api/agent/v1/telemetry", payload, "telemetry-1")))
        .status,
    ).toBe(200);
    expect(
      (await telemetry(signedRequest(fixture, "/api/agent/v1/telemetry", payload, "telemetry-2")))
        .status,
    ).toBe(200);
    expect(fixture.sqlite.prepare("SELECT COUNT(*) AS count FROM telemetry_sample").get()).toEqual({
      count: 1,
    });
    expect(
      fixture.sqlite.prepare("SELECT hostname, status FROM system WHERE id = ?").get(SYSTEM_ID),
    ).toEqual({
      hostname: "host-1",
      status: "online",
    });
  });
});

function createAgent() {
  process.env.SCOUT_DATABASE_URL = "file::memory:";
  const { sqlite } = getDatabase();
  const keys = generateKeyPairSync("ed25519");
  const publicKey = keys.publicKey
    .export({ format: "der", type: "spki" })
    .subarray(-32)
    .toString("base64url");
  const now = Date.now();
  sqlite
    .prepare(
      "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES (?, ?, 'installing', ?, ?)",
    )
    .run(SYSTEM_ID, "192.0.2.10", now, now);
  sqlite
    .prepare(
      "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, last_heartbeat_at, created_at, updated_at) VALUES (?, ?, ?, 'linux', 'x86_64', '0.1.0', ?, ?, ?)",
    )
    .run(AGENT_ID, SYSTEM_ID, publicKey, now, now, now);
  return { sqlite, privateKey: keys.privateKey };
}

function signedRequest(
  fixture: ReturnType<typeof createAgent>,
  path: string,
  payload: unknown,
  requestId: string,
  options: { timestamp?: number } = {},
) {
  const body = JSON.stringify(payload);
  const timestamp = String(options.timestamp ?? Math.floor(Date.now() / 1_000));
  const signature = signMessage(
    null,
    Buffer.from(signedRequestMessage("POST", path, timestamp, requestId, body)),
    fixture.privateKey,
  ).toString("base64url");
  return new Request(`http://localhost${path}`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-scout-agent-id": AGENT_ID,
      "x-scout-timestamp": timestamp,
      "x-scout-request-id": requestId,
      "x-scout-signature": signature,
    },
    body,
  });
}
