import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { getSystemMetrics } from "@/lib/server/systems";

describe("system metrics", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    closeDatabase();
  });

  it("returns current samples, retained rollups, timestamps, and dropped counts", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES ('system-1', 'host', 'online', ?, ?)",
      )
      .run(1, 1);
    sqlite
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, last_heartbeat_at, last_telemetry_at, created_at, updated_at) VALUES ('agent-1', 'system-1', ?, 'linux', 'x86_64', '0.1.0', ?, ?, ?, ?)",
      )
      .run("a".repeat(43), 4_000, 4_000, 4_000, 4_000);
    sqlite
      .prepare(
        "INSERT INTO telemetry_sample (id, agent_id, batch_id, observed_at, payload, received_at) VALUES ('sample-1', 'agent-1', 'batch-1', ?, ?, ?)",
      )
      .run(5_000, JSON.stringify({ droppedSamples: 2, host: { cpuUsagePercent: 12.5 } }), 5_100);
    sqlite
      .prepare(
        "INSERT INTO telemetry_rollup (id, agent_id, bucket_start, payload) VALUES ('rollup-1', 'agent-1', ?, ?)",
      )
      .run(3_000, JSON.stringify({ sampleCount: 4, cpuUsagePercentAvg: 10 }));

    expect(getSystemMetrics("system-1", 6_000)).toEqual({
      systemId: "system-1",
      current: {
        observedAt: "1970-01-01T00:00:05.000Z",
        receivedAt: "1970-01-01T00:00:05.100Z",
        droppedSamples: 2,
      },
      droppedSamples: 2,
      samples: [
        {
          observedAt: "1970-01-01T00:00:05.000Z",
          receivedAt: "1970-01-01T00:00:05.100Z",
          payload: { droppedSamples: 2, host: { cpuUsagePercent: 12.5 } },
        },
      ],
      rollups: [
        {
          bucketStart: "1970-01-01T00:00:03.000Z",
          payload: { sampleCount: 4, cpuUsagePercentAvg: 10 },
        },
      ],
    });
  });
});
