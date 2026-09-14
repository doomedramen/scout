import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { completeScanTask, createSignedScanTask, getPendingTaskEnvelope } from "@/lib/server/tasks";
import { ensureNetworkSegment } from "@/lib/server/discovery";

describe("signed scanner tasks", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    closeDatabase();
  });

  it("assigns one signed bounded scan task to the freshest healthy agent", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    const segment = ensureNetworkSegment(
      {
        cidr: "192.0.2.0/30",
        interfaceName: "eth0",
        gateway: "192.0.2.1",
        sourceAddress: "192.0.2.2",
        provenanceKey: "task-segment",
      },
      1_000,
    );
    sqlite
      .prepare(
        "INSERT INTO system (id, segment_id, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'online', ?, ?)",
      )
      .run("system-1", segment.id, "host", 1_000, 1_000);
    sqlite
      .prepare(
        "INSERT INTO system_address (id, system_id, address, port, last_seen_at) VALUES (?, ?, ?, ?, ?)",
      )
      .run("address-1", "system-1", "192.0.2.1", 22, 1_000);
    sqlite
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, last_heartbeat_at, created_at, updated_at) VALUES (?, ?, ?, 'linux', 'x86_64', '0.1.0', ?, ?, ?)",
      )
      .run("agent-1", "system-1", "a".repeat(43), 1_000, 1_000, 1_000);

    const task = createSignedScanTask(segment.id, "agent-1", 2_000);
    expect(task.envelope).toMatchObject({
      taskId: task.id,
      agentId: "agent-1",
      kind: "network-scan",
      generation: 1,
      policyVersion: 1,
    });
    expect(task.envelope.signature).toMatch(/^[A-Za-z0-9_-]+$/);
    expect(task.payload).toMatchObject({ cidr: "192.0.2.0/30", port: 22 });
    expect(getPendingTaskEnvelope("agent-1", 2_001)).toEqual(task.envelope);
    expect(sqlite.prepare("SELECT status FROM scan_task WHERE id = ?").get(task.id)).toEqual({
      status: "leased",
    });
  });

  it("rejects a late or superseded result and accepts the current result once", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    const segment = ensureNetworkSegment(
      {
        cidr: "192.0.2.0/30",
        interfaceName: "eth0",
        gateway: "192.0.2.1",
        sourceAddress: "192.0.2.2",
        provenanceKey: "result-segment",
      },
      1_000,
    );
    sqlite
      .prepare(
        "INSERT INTO system (id, segment_id, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'online', ?, ?)",
      )
      .run("system-1", segment.id, "host", 1_000, 1_000);
    sqlite
      .prepare(
        "INSERT INTO system_address (id, system_id, address, port, last_seen_at) VALUES (?, ?, ?, ?, ?)",
      )
      .run("address-1", "system-1", "192.0.2.1", 22, 1_000);
    sqlite
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, last_heartbeat_at, created_at, updated_at) VALUES (?, ?, ?, 'linux', 'x86_64', '0.1.0', ?, ?, ?)",
      )
      .run("agent-1", "system-1", "a".repeat(43), 1_000, 1_000, 1_000);
    const task = createSignedScanTask(segment.id, "agent-1", 2_000);

    expect(
      completeScanTask(
        {
          taskId: task.id,
          agentId: "agent-1",
          generation: task.envelope.generation - 1,
          payloadDigest: task.envelope.payloadDigest,
          results: [],
        },
        2_001,
      ),
    ).toEqual({ accepted: false, reason: "superseded" });
    expect(
      completeScanTask(
        {
          taskId: task.id,
          agentId: "agent-1",
          generation: task.envelope.generation,
          payloadDigest: task.envelope.payloadDigest,
          results: [{ address: "192.0.2.1", port: 22, outcome: "open" }],
        },
        2_002,
      ),
    ).toEqual({ accepted: true });
    expect(
      completeScanTask(
        {
          taskId: task.id,
          agentId: "agent-1",
          generation: task.envelope.generation,
          payloadDigest: task.envelope.payloadDigest,
          results: [],
        },
        2_003,
      ),
    ).toEqual({ accepted: false, reason: "already-complete" });
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM system").get()).toEqual({ count: 1 });
  });

  it("rejects results after a segment is paused or its scanner is no longer healthy", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    const segment = ensureNetworkSegment(
      {
        cidr: "192.0.2.0/30",
        interfaceName: "eth0",
        gateway: "192.0.2.1",
        sourceAddress: "192.0.2.2",
        provenanceKey: "authority-segment",
      },
      1_000,
    );
    sqlite
      .prepare(
        "INSERT INTO system (id, segment_id, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'online', ?, ?)",
      )
      .run("system-1", segment.id, "host", 1_000, 1_000);
    sqlite
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, last_heartbeat_at, created_at, updated_at) VALUES (?, ?, ?, 'linux', 'x86_64', '0.1.0', ?, ?, ?)",
      )
      .run("agent-1", "system-1", "a".repeat(43), 1_000, 1_000, 1_000);

    const pausedTask = createSignedScanTask(segment.id, "agent-1", 2_000);
    sqlite
      .prepare(
        "UPDATE network_segment SET paused = 1, policy_version = policy_version + 1 WHERE id = ?",
      )
      .run(segment.id);
    expect(
      completeScanTask(
        {
          taskId: pausedTask.id,
          agentId: "agent-1",
          generation: pausedTask.envelope.generation,
          payloadDigest: pausedTask.envelope.payloadDigest,
          results: [],
        },
        2_001,
      ),
    ).toEqual({ accepted: false, reason: "superseded" });

    sqlite.prepare("UPDATE network_segment SET paused = 0 WHERE id = ?").run(segment.id);
    const unhealthyTask = createSignedScanTask(segment.id, "agent-1", 3_000);
    sqlite.prepare("UPDATE agent SET revoked_at = 3_001 WHERE id = ?").run("agent-1");
    expect(
      completeScanTask(
        {
          taskId: unhealthyTask.id,
          agentId: "agent-1",
          generation: unhealthyTask.envelope.generation,
          payloadDigest: unhealthyTask.envelope.payloadDigest,
          results: [],
        },
        3_002,
      ),
    ).toEqual({ accepted: false, reason: "superseded" });
  });
});
