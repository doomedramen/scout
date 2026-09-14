import { execFileSync } from "node:child_process";
import { createCipheriv, randomBytes, scryptSync } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";

import Database from "better-sqlite3";

import { afterEach, describe, expect, it } from "vitest";

import { migrateDatabase } from "@/db/migrations";

const hostHelper = fs.readFileSync(path.resolve("packaging/host/scout"), "utf8");
const ops = fs.readFileSync(path.resolve("packaging/containers/scout-ops.mjs"), "utf8");
const directories: string[] = [];

afterEach(() => {
  for (const directory of directories.splice(0))
    fs.rmSync(directory, { recursive: true, force: true });
});

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
    expect(ops).toContain("reconciliation_required");
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

  it("rejects a corrupt update snapshot before overwriting the live database", () => {
    const data = fs.mkdtempSync(path.join(os.tmpdir(), "scout-ops-corrupt-data-"));
    const snapshot = fs.mkdtempSync(path.join(os.tmpdir(), "scout-ops-corrupt-snapshot-"));
    try {
      const database = new Database(path.join(data, "scout.sqlite"));
      migrateDatabase(database);
      database
        .prepare("INSERT INTO app_setting (key, value, updated_at) VALUES ('example', 'before', 1)")
        .run();
      database.close();
      fs.writeFileSync(path.join(snapshot, "snapshot.json"), JSON.stringify({ version: 1 }));
      fs.writeFileSync(path.join(snapshot, "scout.sqlite"), "not a sqlite database");
      for (const name of ["auth.secret", "credentials.key", "control-signing.key"])
        fs.writeFileSync(path.join(snapshot, name), Buffer.alloc(32, 7), { mode: 0o600 });

      let failure: unknown;
      try {
        runOps(data, "restore-snapshot", snapshot);
      } catch (error) {
        failure = error;
      }
      expect(failure).toBeDefined();
      const stderr = String((failure as { stderr?: string | Buffer }).stderr ?? "");
      expect(stderr).toContain("scout-ops:");
      expect(stderr).toMatch(/sqlite|integrity|database/i);
      expect(stderr).not.toContain("at validateSQLite");
      const unchanged = new Database(path.join(data, "scout.sqlite"), { readonly: true });
      expect(
        unchanged.prepare("SELECT value FROM app_setting WHERE key = 'example'").get(),
      ).toEqual({ value: "before" });
      unchanged.close();
    } finally {
      fs.rmSync(data, { recursive: true, force: true });
      fs.rmSync(snapshot, { recursive: true, force: true });
    }
  });

  it("prepares a pre-reconciliation backup before replacing live data", () => {
    const source = fs.mkdtempSync(path.join(os.tmpdir(), "scout-ops-legacy-source-"));
    const destination = fs.mkdtempSync(path.join(os.tmpdir(), "scout-ops-legacy-destination-"));
    const archive = path.join(os.tmpdir(), `scout-${Date.now()}-legacy.scoutbak`);
    directories.push(source, destination, archive);
    const database = new Database(path.join(source, "scout.sqlite"));
    migrateDatabase(database);
    database
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES ('legacy-system', 'legacy host', 'online', 1, 1)",
      )
      .run();
    database
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, created_at, updated_at) VALUES ('legacy-agent', 'legacy-system', ?, 'linux', 'x86_64', '0.1.0', 1, 1)",
      )
      .run("l".repeat(43));
    database.exec(
      "DROP INDEX agent_reconciliation_idx; ALTER TABLE agent DROP COLUMN reconciled_at; ALTER TABLE agent DROP COLUMN reconciliation_required;",
    );
    database.prepare("DELETE FROM schema_migration WHERE version = 10").run();
    database.close();
    writeEncryptedArchive(archive, "correct horse", {
      version: 1,
      database: fs.readFileSync(path.join(source, "scout.sqlite")).toString("base64"),
      keys: Object.fromEntries(
        ["auth.secret", "credentials.key", "control-signing.key"].map((name) => [
          name,
          Buffer.alloc(32, 7).toString("base64"),
        ]),
      ),
    });

    expect(runOps(destination, "restore", archive, "correct horse\n")).toContain(
      "Scout backup restored",
    );
    const restored = new Database(path.join(destination, "scout.sqlite"), { readonly: true });
    expect(
      restored.prepare("SELECT reconciliation_required FROM agent WHERE id = 'legacy-agent'").get(),
    ).toEqual({ reconciliation_required: 1 });
    expect(
      restored.prepare("SELECT version FROM schema_migration WHERE version = 10").get(),
    ).toEqual({ version: 10 });
    restored.close();
  });
});

function runOps(data: string, operation: string, target?: string, input?: string): string {
  return execFileSync(
    process.execPath,
    [path.resolve("packaging/containers/scout-ops.mjs"), operation, ...(target ? [target] : [])],
    { env: { ...process.env, SCOUT_DATA_DIR: data }, encoding: "utf8", input },
  );
}

function writeEncryptedArchive(archivePath: string, passphrase: string, payload: unknown): void {
  const salt = randomBytes(16);
  const nonce = randomBytes(12);
  const cipher = createCipheriv("aes-256-gcm", scryptSync(passphrase, salt, 32), nonce);
  const ciphertext = Buffer.concat([
    cipher.update(Buffer.from(JSON.stringify(payload), "utf8")),
    cipher.final(),
  ]);
  fs.writeFileSync(
    archivePath,
    JSON.stringify({
      version: 1,
      salt: salt.toString("base64url"),
      nonce: nonce.toString("base64url"),
      tag: cipher.getAuthTag().toString("base64url"),
      ciphertext: ciphertext.toString("base64url"),
    }),
  );
}
