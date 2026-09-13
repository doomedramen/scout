import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { runTelemetryRetention } from "@/lib/server/telemetry";

describe("telemetry retention", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    closeDatabase();
  });

  it("rolls current raw samples into five-minute buckets and removes raw data after 24 hours", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES ('system-1', 'host', 'online', 1, 1)",
      )
      .run();
    sqlite
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, created_at, updated_at) VALUES ('agent-1', 'system-1', ?, 'linux', 'x86_64', '0.1.0', 1, 1)",
      )
      .run("a".repeat(43));
    const now = 25 * 60 * 1_000;
    const payload = (cpuUsagePercent: number) =>
      JSON.stringify({
        droppedSamples: 1,
        host: {
          cpuUsagePercent,
          memoryUsedBytes: 100,
          memoryTotalBytes: 200,
        },
      });
    sqlite
      .prepare(
        "INSERT INTO telemetry_sample (id, agent_id, batch_id, observed_at, payload, received_at) VALUES (?, 'agent-1', ?, ?, ?, ?)",
      )
      .run("sample-1", "batch-1", 10 * 60 * 1_000, payload(20), 10 * 60 * 1_000);
    sqlite
      .prepare(
        "INSERT INTO telemetry_sample (id, agent_id, batch_id, observed_at, payload, received_at) VALUES (?, 'agent-1', ?, ?, ?, ?)",
      )
      .run("sample-2", "batch-2", 10 * 60 * 1_000 + 1_000, payload(40), 10 * 60 * 1_000 + 1_000);

    expect(runTelemetryRetention(now)).toEqual({
      rolledUp: 1,
      removedSamples: 0,
      removedRollups: 0,
    });
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM telemetry_rollup").get()).toEqual({
      count: 1,
    });
    const rollup = sqlite.prepare("SELECT payload FROM telemetry_rollup").get() as {
      payload: string;
    };
    expect(JSON.parse(rollup.payload)).toEqual(
      expect.objectContaining({ cpuUsagePercentAvg: 30, sampleCount: 2 }),
    );

    expect(runTelemetryRetention(now + 25 * 60 * 60 * 1_000).removedSamples).toBe(2);
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM telemetry_sample").get()).toEqual({
      count: 0,
    });
  });
});
