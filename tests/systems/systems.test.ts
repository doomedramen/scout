import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { getFleetSnapshot } from "@/lib/server/systems";

describe("fleet snapshot", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    closeDatabase();
  });

  it("counts only unique systems with current open access evidence", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();

    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, excluded, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?)",
      )
      .run("system-open", "192.0.2.10", "needs-access", 1_000, 1_000);
    sqlite
      .prepare(
        "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, source, observed_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
      )
      .run("evidence-open", "system-open", "ssh", "192.0.2.10", 22, "open", "test", 1_000, 2_000);

    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, excluded, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?)",
      )
      .run("system-expired", "192.0.2.11", "needs-access", 1_000, 1_000);
    sqlite
      .prepare(
        "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, source, observed_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
      )
      .run(
        "evidence-expired",
        "system-expired",
        "ssh",
        "192.0.2.11",
        22,
        "open",
        "test",
        1_000,
        1_499,
      );

    const snapshot = getFleetSnapshot(1_500);

    expect(snapshot.summary).toMatchObject({ monitored: 0, needsAccess: 1 });
    expect(snapshot.systems).toHaveLength(1);
    expect(snapshot.systems[0]).toMatchObject({ id: "system-open", status: "needs-access" });
  });
});
