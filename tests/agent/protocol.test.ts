import { afterEach, describe, expect, it } from "vitest";
import { generateKeyPairSync, sign as signMessage } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { heartbeatInput, signedRequestMessage, telemetryInput } from "@/lib/server/agent-protocol";
import { POST as heartbeat } from "@/app/api/agent/v1/heartbeat/route";
import { POST as telemetry } from "@/app/api/agent/v1/telemetry/route";
import { GET as releaseArtifact } from "@/app/api/agent/v1/releases/artifact/route";
import { GET as releaseManifest } from "@/app/api/agent/v1/releases/manifest/route";
import { releaseManifestMessage, sha256Hex } from "@/lib/server/releases";

const AGENT_ID = "agent-1";
const SYSTEM_ID = "system-1";

describe("signed agent protocol", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    delete process.env.SCOUT_AGENT_ARTIFACT_DIR;
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
    expect((await response.clone().json()).task).toBeNull();
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

  it("marks a restored agent reconciled only after its authenticated heartbeat", async () => {
    const fixture = createAgent();
    fixture.sqlite
      .prepare("UPDATE agent SET reconciliation_required = 1 WHERE id = ?")
      .run(AGENT_ID);

    const payload = {
      agentId: AGENT_ID,
      observedAt: new Date().toISOString(),
      taskGeneration: 12,
      releaseSequence: 7,
    };
    const response = await heartbeat(
      signedRequest(fixture, "/api/agent/v1/heartbeat", payload, "restore-reconcile"),
    );

    expect(response.status).toBe(200);
    expect(
      fixture.sqlite
        .prepare(
          "SELECT reconciliation_required, task_generation, release_sequence FROM agent WHERE id = ?",
        )
        .get(AGENT_ID),
    ).toEqual({ reconciliation_required: 0, task_generation: 12, release_sequence: 7 });
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

  it("records receipt liveness for delayed telemetry without rewriting its observation time", async () => {
    const fixture = createAgent();
    const receivedBefore = Date.now();
    const observedAt = new Date(receivedBefore - 60 * 60 * 1_000).toISOString();
    const payload = {
      agentId: AGENT_ID,
      batchId: "delayed-batch",
      observedAt,
      droppedSamples: 0,
      host: {
        hostname: "delayed-host",
        operatingSystem: "Linux",
        kernelVersion: "6.1",
        cpuUsagePercent: null,
        memoryUsedBytes: null,
        memoryTotalBytes: null,
        uptimeSeconds: null,
        filesystems: [],
        interfaces: [],
      },
    };

    const response = await telemetry(
      signedRequest(fixture, "/api/agent/v1/telemetry", payload, "delayed-telemetry"),
    );

    expect(response.status).toBe(200);
    const sample = fixture.sqlite
      .prepare("SELECT observed_at AS observedAt, received_at AS receivedAt FROM telemetry_sample")
      .get() as { observedAt: number; receivedAt: number };
    expect(sample.observedAt).toBe(Date.parse(observedAt));
    expect(sample.receivedAt).toBeGreaterThanOrEqual(receivedBefore);
    expect(
      (
        fixture.sqlite
          .prepare("SELECT last_telemetry_at AS lastTelemetryAt FROM agent WHERE id = ?")
          .get(AGENT_ID) as { lastTelemetryAt: number }
      ).lastTelemetryAt,
    ).toBeGreaterThanOrEqual(receivedBefore);
  });

  it("rejects telemetry timestamps outside the buffered observation window", async () => {
    const fixture = createAgent();
    const base = {
      agentId: AGENT_ID,
      droppedSamples: 0,
      host: {
        hostname: "timestamp-host",
        operatingSystem: "Linux",
        kernelVersion: "6.1",
        cpuUsagePercent: null,
        memoryUsedBytes: null,
        memoryTotalBytes: null,
        uptimeSeconds: null,
        filesystems: [],
        interfaces: [],
      },
    };
    const future = {
      ...base,
      batchId: "future-telemetry",
      observedAt: new Date(Date.now() + 5 * 60 * 1_000).toISOString(),
    };
    expect(
      (
        await telemetry(
          signedRequest(fixture, "/api/agent/v1/telemetry", future, "future-telemetry-request"),
        )
      ).status,
    ).toBe(400);

    const tooOld = {
      ...base,
      batchId: "too-old-telemetry",
      observedAt: new Date(Date.now() - 24 * 60 * 60 * 1_000 - 1_000).toISOString(),
    };
    expect(
      (
        await telemetry(
          signedRequest(fixture, "/api/agent/v1/telemetry", tooOld, "too-old-telemetry-request"),
        )
      ).status,
    ).toBe(400);
    expect(fixture.sqlite.prepare("SELECT COUNT(*) AS count FROM telemetry_sample").get()).toEqual({
      count: 0,
    });
  });

  it("authenticates signed release reads including their query string", async () => {
    const fixture = createAgent();
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-agent-release-route-"));
    const signingKeys = generateKeyPairSync("ed25519");
    const publisherPublicKey = signingKeys.publicKey
      .export({ format: "der", type: "spki" })
      .subarray(-32)
      .toString("base64url");
    const artifact = Buffer.from("signed-agent-artifact");
    const payload = {
      schemaVersion: 1 as const,
      releaseSequence: 2,
      artifacts: [
        {
          platform: "linux" as const,
          architecture: "x86_64" as const,
          version: "0.2.0",
          sequence: 2,
          sha256: sha256Hex(artifact),
          size: artifact.byteLength,
          minimumProtocol: 1,
        },
      ],
    };
    const manifest = {
      ...payload,
      signature: signMessage(
        null,
        Buffer.from(releaseManifestMessage(payload)),
        signingKeys.privateKey,
      ).toString("base64url"),
    };
    fs.writeFileSync(path.join(directory, "release-manifest.json"), JSON.stringify(manifest));
    fs.writeFileSync(path.join(directory, "publisher-public.key"), publisherPublicKey);
    fs.writeFileSync(path.join(directory, "scout-agent-linux-x86_64"), artifact);
    process.env.SCOUT_AGENT_ARTIFACT_DIR = directory;

    const manifestResponse = await releaseManifest(
      signedGetRequest(fixture, "/api/agent/v1/releases/manifest", "release-manifest"),
    );
    expect(manifestResponse.status).toBe(200);
    expect(await manifestResponse.json()).toEqual(manifest);

    const artifactResponse = await releaseArtifact(
      signedGetRequest(
        fixture,
        "/api/agent/v1/releases/artifact?platform=linux&architecture=x86_64",
        "release-artifact",
      ),
    );
    expect(artifactResponse.status).toBe(200);
    expect(Buffer.from(await artifactResponse.arrayBuffer())).toEqual(artifact);
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

function signedGetRequest(
  fixture: ReturnType<typeof createAgent>,
  requestPath: string,
  requestId: string,
) {
  const timestamp = String(Math.floor(Date.now() / 1_000));
  const signature = signMessage(
    null,
    Buffer.from(signedRequestMessage("GET", requestPath, timestamp, requestId, "")),
    fixture.privateKey,
  ).toString("base64url");
  return new Request(`http://localhost${requestPath}`, {
    method: "GET",
    headers: {
      "x-scout-agent-id": AGENT_ID,
      "x-scout-timestamp": timestamp,
      "x-scout-request-id": requestId,
      "x-scout-signature": signature,
    },
  });
}
