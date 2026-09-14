import { createCipheriv, randomBytes, scryptSync } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { createEncryptedBackup, restoreEncryptedBackup } from "@/lib/server/backup";

describe("encrypted backup and restore", () => {
  const directories: string[] = [];

  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    closeDatabase();
    for (const directory of directories.splice(0))
      fs.rmSync(directory, { recursive: true, force: true });
  });

  it("round-trips SQLite and keys, then pauses authority and invalidates invitations", async () => {
    const source = fs.mkdtempSync(path.join(os.tmpdir(), "scout-backup-source-"));
    const destination = fs.mkdtempSync(path.join(os.tmpdir(), "scout-backup-destination-"));
    const archive = path.join(os.tmpdir(), `scout-${Date.now()}.scoutbak`);
    directories.push(source, destination, archive);
    process.env.SCOUT_DATABASE_URL = `file:${path.join(source, "scout.sqlite")}`;
    process.env.SCOUT_DATA_DIR = source;
    const { sqlite } = getDatabase();
    for (const [name, value] of [
      ["auth.secret", 32],
      ["credentials.key", 32],
      ["control-signing.key", 32],
    ] as const)
      fs.writeFileSync(path.join(source, name), Buffer.alloc(value, 7), { mode: 0o600 });
    sqlite
      .prepare("INSERT INTO app_setting (key, value, updated_at) VALUES (?, ?, ?)")
      .run("example", "kept", 1);

    await createEncryptedBackup(archive, "correct horse", source);
    closeDatabase();
    restoreEncryptedBackup(archive, destination, "correct horse");

    const restored = new (await import("better-sqlite3")).default(
      path.join(destination, "scout.sqlite"),
      {
        readonly: true,
      },
    );
    expect(restored.prepare("SELECT value FROM app_setting WHERE key = 'example'").get()).toEqual({
      value: "kept",
    });
    expect(
      restored.prepare("SELECT value FROM app_setting WHERE key = 'authority_paused'").get(),
    ).toEqual({
      value: "true",
    });
    restored.close();
    expect(fs.statSync(path.join(destination, "credentials.key")).mode & 0o777).toBe(0o600);
  });

  it("requires restored agents to reconcile before they receive new work", async () => {
    const source = fs.mkdtempSync(path.join(os.tmpdir(), "scout-backup-reconcile-source-"));
    const destination = fs.mkdtempSync(
      path.join(os.tmpdir(), "scout-backup-reconcile-destination-"),
    );
    const archive = path.join(os.tmpdir(), `scout-${Date.now()}-reconcile.scoutbak`);
    directories.push(source, destination, archive);
    process.env.SCOUT_DATABASE_URL = `file:${path.join(source, "scout.sqlite")}`;
    process.env.SCOUT_DATA_DIR = source;
    const { sqlite } = getDatabase();
    for (const [name, value] of [
      ["auth.secret", 32],
      ["credentials.key", 32],
      ["control-signing.key", 32],
    ] as const)
      fs.writeFileSync(path.join(source, name), Buffer.alloc(value, 7), { mode: 0o600 });
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES ('agent-system', 'agent host', 'online', 1, 1)",
      )
      .run();
    sqlite
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, last_heartbeat_at, created_at, updated_at) VALUES ('agent-1', 'agent-system', ?, 'linux', 'x86_64', '0.1.0', 1, 1, 1)",
      )
      .run("a".repeat(43));
    sqlite
      .prepare(
        "INSERT INTO network_segment (id, site_key, provenance_key, cidr, source, created_at, updated_at) VALUES ('segment-1', 'site-1', 'route-1', '192.0.2.0/30', 'manual', 1, 1)",
      )
      .run();
    sqlite
      .prepare(
        "INSERT INTO scan_task (id, segment_id, scanner_agent_id, kind, generation, policy_version, payload, signature, issued_at, status, deadline_at, lease_expires_at, created_at) VALUES ('task-1', 'segment-1', 'agent-1', 'network-scan', 1, 1, '{}', '', 1, 'leased', 100000, 100000, 1)",
      )
      .run();
    sqlite
      .prepare(
        "INSERT INTO enrollment_job (id, system_id, status, stage, lease_owner, lease_expires_at, created_at, updated_at) VALUES ('job-1', 'agent-system', 'running', 'installation', 'worker', 100000, 1, 1)",
      )
      .run();

    await createEncryptedBackup(archive, "correct horse", source);
    closeDatabase();
    restoreEncryptedBackup(archive, destination, "correct horse");

    const restored = new (await import("better-sqlite3")).default(
      path.join(destination, "scout.sqlite"),
      { readonly: true },
    );
    expect(
      restored.prepare("SELECT reconciliation_required FROM agent WHERE id = 'agent-1'").get(),
    ).toEqual({ reconciliation_required: 1 });
    expect(restored.prepare("SELECT status FROM scan_task WHERE id = 'task-1'").get()).toEqual({
      status: "superseded",
    });
    expect(
      restored.prepare("SELECT status, error_code FROM enrollment_job WHERE id = 'job-1'").get(),
    ).toEqual({
      status: "blocked",
      error_code: "restore-review",
    });
    restored.close();
  });

  it("fails closed for the wrong passphrase and a non-clean restore directory", async () => {
    const source = fs.mkdtempSync(path.join(os.tmpdir(), "scout-backup-source-"));
    const destination = fs.mkdtempSync(path.join(os.tmpdir(), "scout-backup-destination-"));
    const archive = path.join(os.tmpdir(), `scout-${Date.now()}-wrong.scoutbak`);
    directories.push(source, destination, archive);
    process.env.SCOUT_DATABASE_URL = `file:${path.join(source, "scout.sqlite")}`;
    process.env.SCOUT_DATA_DIR = source;
    getDatabase();
    for (const name of ["auth.secret", "credentials.key", "control-signing.key"])
      fs.writeFileSync(path.join(source, name), Buffer.alloc(32, 7), { mode: 0o600 });
    await createEncryptedBackup(archive, "correct horse", source);
    expect(() => restoreEncryptedBackup(archive, destination, "wrong horse")).toThrow();
    fs.writeFileSync(path.join(destination, "do-not-overwrite"), "present");
    expect(() => restoreEncryptedBackup(archive, destination, "correct horse")).toThrow(/clean/i);
  });

  it("rejects a corrupt SQLite snapshot without leaving a partial restore", () => {
    const destination = fs.mkdtempSync(path.join(os.tmpdir(), "scout-backup-corrupt-destination-"));
    const archive = path.join(os.tmpdir(), `scout-${Date.now()}-corrupt.scoutbak`);
    directories.push(destination, archive);
    writeTestArchive(archive, "correct horse", {
      version: 1,
      database: Buffer.from("not a sqlite database").toString("base64"),
      keys: Object.fromEntries(
        ["auth.secret", "credentials.key", "control-signing.key"].map((name) => [
          name,
          Buffer.alloc(32, 7).toString("base64"),
        ]),
      ),
    });

    expect(() => restoreEncryptedBackup(archive, destination, "correct horse")).toThrow(
      /sqlite|integrity|database/i,
    );
    expect(fs.readdirSync(destination)).toEqual([]);
  });
});

function writeTestArchive(archivePath: string, passphrase: string, payload: unknown): void {
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
