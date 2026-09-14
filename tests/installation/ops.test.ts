import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";

import Database from "better-sqlite3";

import { describe, expect, it } from "vitest";

import { migrateDatabase } from "@/db/migrations";

const hostHelper = fs.readFileSync(path.resolve("packaging/host/scout"), "utf8");
const ops = fs.readFileSync(path.resolve("packaging/containers/scout-ops.mjs"), "utf8");

describe("packaged operational safeguards", () => {
  it("snapshots authority, SQLite, and keys before update and restores them on failure", () => {
    expect(hostHelper).toContain("snapshot");
    expect(hostHelper).toContain("run_data_op pause");
    expect(hostHelper).toContain("restore-snapshot");
    expect(hostHelper).toContain("docker image inspect");
    expect(hostHelper).toContain("RepoDigests");
    expect(hostHelper).toContain("rollback");
    expect(hostHelper).toContain("set-authority");
  });

  it("provides an image-side snapshot and authority operator", () => {
    expect(ops).toContain('"snapshot"');
    expect(ops).toContain('"restore-snapshot"');
    expect(ops).toContain('"authority"');
    expect(ops).toContain('"set-authority"');
    expect(ops).toContain("database.backup");
    expect(ops).toContain("authority_paused");
  });

  it("round-trips an update snapshot and preserves authority state", () => {
    const data = fs.mkdtempSync(path.join(os.tmpdir(), "scout-ops-data-"));
    const snapshot = fs.mkdtempSync(path.join(os.tmpdir(), "scout-ops-snapshot-"));
    try {
      const database = new Database(path.join(data, "scout.sqlite"));
      migrateDatabase(database);
      database
        .prepare("INSERT INTO app_setting (key, value, updated_at) VALUES ('example', 'before', 1)")
        .run();
      database.close();
      for (const name of ["auth.secret", "credentials.key", "control-signing.key"])
        fs.writeFileSync(path.join(data, name), Buffer.alloc(32, 7), { mode: 0o600 });

      runOps(data, "snapshot", snapshot);
      const changed = new Database(path.join(data, "scout.sqlite"));
      changed.prepare("UPDATE app_setting SET value = 'after' WHERE key = 'example'").run();
      changed.close();
      runOps(data, "restore-snapshot", snapshot);

      const restored = new Database(path.join(data, "scout.sqlite"), { readonly: true });
      expect(restored.prepare("SELECT value FROM app_setting WHERE key = 'example'").get()).toEqual(
        {
          value: "before",
        },
      );
      restored.close();
      expect(runOps(data, "authority").trim()).toBe("false");
      runOps(data, "pause");
      expect(runOps(data, "authority").trim()).toBe("true");
      runOps(data, "set-authority", "false");
      expect(runOps(data, "authority").trim()).toBe("false");
    } finally {
      fs.rmSync(data, { recursive: true, force: true });
      fs.rmSync(snapshot, { recursive: true, force: true });
    }
  });
});

function runOps(data: string, operation: string, target?: string): string {
  return execFileSync(
    process.execPath,
    [path.resolve("packaging/containers/scout-ops.mjs"), operation, ...(target ? [target] : [])],
    { env: { ...process.env, SCOUT_DATA_DIR: data }, encoding: "utf8" },
  );
}
