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
