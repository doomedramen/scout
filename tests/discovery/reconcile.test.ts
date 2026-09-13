import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { ensureNetworkSegment, reconcileScanResults } from "@/lib/server/discovery";
import { getFleetSnapshot } from "@/lib/server/systems";

describe("scan reconciliation", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    closeDatabase();
  });

  it("updates one candidate across repeated scans and removes it after a closed result", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const segment = ensureNetworkSegment(
      {
        cidr: "192.0.2.0/30",
        interfaceName: "test0",
        gateway: "192.0.2.1",
        sourceAddress: "192.0.2.2",
        provenanceKey: "test-network",
      },
      1_000,
    );

    reconcileScanResults(segment.id, [{ address: "192.0.2.2", port: 22, outcome: "open" }], 1_000);
    reconcileScanResults(segment.id, [{ address: "192.0.2.2", port: 22, outcome: "open" }], 2_000);

    const { sqlite } = getDatabase();
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM system").get()).toEqual({ count: 1 });
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM access_evidence").get()).toEqual({
      count: 1,
    });
    expect(getFleetSnapshot(2_000).summary.needsAccess).toBe(1);

    reconcileScanResults(
      segment.id,
      [{ address: "192.0.2.2", port: 22, outcome: "closed" }],
      3_000,
    );

    expect(getFleetSnapshot(3_000).systems).toHaveLength(0);
    expect(sqlite.prepare("SELECT outcome FROM access_evidence").get()).toEqual({
      outcome: "closed",
    });
  });
});
