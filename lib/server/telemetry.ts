import { randomUUID } from "node:crypto";

import { getDatabase } from "@/lib/server/db";

const RAW_RETENTION_MS = 24 * 60 * 60 * 1_000;
const ROLLUP_RETENTION_MS = 90 * 24 * 60 * 60 * 1_000;
const ROLLUP_BUCKET_MS = 5 * 60 * 1_000;

export type RetentionResult = { rolledUp: number; removedSamples: number; removedRollups: number };

export function runTelemetryRetention(now = Date.now()): RetentionResult {
  const { sqlite } = getDatabase();
  const rawCutoff = now - RAW_RETENTION_MS;
  const rollupCutoff = now - ROLLUP_RETENTION_MS;
  const run = sqlite.transaction(() => {
    const groups = sqlite
      .prepare(
        "SELECT agent_id AS agentId, CAST(observed_at / ? AS INTEGER) * ? AS bucketStart FROM telemetry_sample WHERE observed_at > ? AND observed_at <= ? GROUP BY agent_id, CAST(observed_at / ? AS INTEGER) * ?",
      )
      .all(
        ROLLUP_BUCKET_MS,
        ROLLUP_BUCKET_MS,
        rawCutoff,
        now,
        ROLLUP_BUCKET_MS,
        ROLLUP_BUCKET_MS,
      ) as Array<{
      agentId: string;
      bucketStart: number;
    }>;
    let rolledUp = 0;
    for (const group of groups) {
      const samples = sqlite
        .prepare(
          "SELECT payload FROM telemetry_sample WHERE agent_id = ? AND observed_at >= ? AND observed_at < ? ORDER BY observed_at",
        )
        .all(group.agentId, group.bucketStart, group.bucketStart + ROLLUP_BUCKET_MS) as Array<{
        payload: string;
      }>;
      const parsed = samples.flatMap((sample) => {
        try {
          return [JSON.parse(sample.payload) as TelemetryPayloadShape];
        } catch {
          return [];
        }
      });
      if (!parsed.length) continue;
      const payload = JSON.stringify({
        sampleCount: parsed.length,
        cpuUsagePercentAvg: average(parsed.map((sample) => sample.host.cpuUsagePercent)),
        memoryUsedBytesAvg: average(parsed.map((sample) => sample.host.memoryUsedBytes)),
        memoryTotalBytesAvg: average(parsed.map((sample) => sample.host.memoryTotalBytes)),
        droppedSamples: parsed.reduce((total, sample) => total + sample.droppedSamples, 0),
      });
      sqlite
        .prepare(
          "INSERT INTO telemetry_rollup (id, agent_id, bucket_start, payload) VALUES (?, ?, ?, ?) ON CONFLICT(agent_id, bucket_start) DO UPDATE SET payload = excluded.payload",
        )
        .run(randomUUID(), group.agentId, group.bucketStart, payload);
      rolledUp += 1;
    }
    const removedSamples = sqlite
      .prepare("DELETE FROM telemetry_sample WHERE observed_at <= ?")
      .run(rawCutoff).changes;
    const removedRollups = sqlite
      .prepare("DELETE FROM telemetry_rollup WHERE bucket_start <= ?")
      .run(rollupCutoff).changes;
    return { rolledUp, removedSamples, removedRollups };
  });
  return run();
}

type TelemetryValue = number | null | undefined;

type TelemetryPayloadShape = {
  droppedSamples: number;
  host: {
    cpuUsagePercent?: TelemetryValue;
    memoryUsedBytes?: TelemetryValue;
    memoryTotalBytes?: TelemetryValue;
  };
};

function average(values: TelemetryValue[]): number | null {
  const available = values.filter((value): value is number => typeof value === "number");
  if (!available.length) return null;
  return available.reduce((total, value) => total + value, 0) / available.length;
}
