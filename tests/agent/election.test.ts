import { afterEach, describe, expect, it } from "vitest";

import { ensureNetworkSegment } from "@/lib/server/discovery";
import { closeDatabase, getDatabase } from "@/lib/server/db";
import { electScannerAgent, ensureSegmentScanTask } from "@/lib/server/scanning";

describe("segment scanner election", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    closeDatabase();
  });

  it("selects the freshest healthy agent and keeps one pending task", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    const segment = ensureNetworkSegment(
      {
        cidr: "192.0.2.0/30",
        interfaceName: "eth0",
        gateway: "192.0.2.1",
        sourceAddress: "192.0.2.2",
        provenanceKey: "election",
      },
      1_000,
    );
    insertAgent(sqlite, "agent-b", "system-b", segment.id, 2_000);
    insertAgent(sqlite, "agent-a", "system-a", segment.id, 2_000);

    expect(electScannerAgent(segment.id, 2_001)).toEqual({
      agentId: "agent-a",
      heartbeatAt: 2_000,
    });

    const first = ensureSegmentScanTask(segment.id, 2_001);
    const second = ensureSegmentScanTask(segment.id, 2_002);
    expect(first?.scanner.agentId).toBe("agent-a");
    expect(second).toEqual(first);
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM scan_task").get()).toEqual({ count: 1 });
  });

  it("fails over after the selected heartbeat becomes unavailable", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    const segment = ensureNetworkSegment(
      {
        cidr: "192.0.2.0/30",
        interfaceName: "eth0",
        gateway: "192.0.2.1",
        sourceAddress: "192.0.2.2",
        provenanceKey: "failover",
      },
      1_000,
    );
    insertAgent(sqlite, "agent-a", "system-a", segment.id, 20_000);
    insertAgent(sqlite, "agent-b", "system-b", segment.id, 10_000);
    expect(electScannerAgent(segment.id, 20_001)?.agentId).toBe("agent-a");
    sqlite.prepare("UPDATE agent SET last_heartbeat_at = ? WHERE id = ?").run(45_000, "agent-b");
    expect(electScannerAgent(segment.id, 66_001)?.agentId).toBe("agent-b");
    expect(electScannerAgent(segment.id, 90_001)).toBeNull();
  });

  it("does not issue scan work for a paused segment", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    const segment = ensureNetworkSegment(
      {
        cidr: "192.0.2.0/30",
        interfaceName: "eth0",
        gateway: "192.0.2.1",
        sourceAddress: "192.0.2.2",
        provenanceKey: "paused-segment",
      },
      1_000,
    );
    insertAgent(sqlite, "agent-a", "system-a", segment.id, 2_000);
    sqlite.prepare("UPDATE network_segment SET paused = 1 WHERE id = ?").run(segment.id);

    expect(ensureSegmentScanTask(segment.id, 2_001)).toBeNull();
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM scan_task").get()).toEqual({ count: 0 });
  });
});

function insertAgent(
  sqlite: ReturnType<typeof getDatabase>["sqlite"],
  agentId: string,
  systemId: string,
  segmentId: string,
  heartbeatAt: number,
) {
  sqlite
    .prepare(
      "INSERT INTO system (id, segment_id, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'online', ?, ?)",
    )
    .run(systemId, segmentId, systemId, heartbeatAt, heartbeatAt);
  sqlite
    .prepare(
      "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, last_heartbeat_at, created_at, updated_at) VALUES (?, ?, ?, 'linux', 'x86_64', '0.1.0', ?, ?, ?)",
    )
    .run(
      agentId,
      systemId,
      `${agentId}${"a".repeat(43 - agentId.length)}`,
      heartbeatAt,
      heartbeatAt,
      heartbeatAt,
    );
}
