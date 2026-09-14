import { createCipheriv, createDecipheriv, randomBytes, randomUUID, scryptSync } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import Database from "better-sqlite3";

import { LATEST_SCHEMA_VERSION, migrateDatabase } from "@/db/migrations";
import { getDatabase } from "@/lib/server/db";

const KEY_FILES = ["auth.secret", "credentials.key", "control-signing.key"] as const;
const ARCHIVE_VERSION = 1;

type BackupPayload = {
  version: number;
  database: string;
  keys: Record<string, string>;
};

type BackupEnvelope = {
  version: number;
  salt: string;
  nonce: string;
  tag: string;
  ciphertext: string;
};

export async function createEncryptedBackup(
  outputPath: string,
  passphrase: string,
  dataDirectory = process.env.SCOUT_DATA_DIR ?? ".scout-data",
): Promise<void> {
  requirePassphrase(passphrase);
  const temporaryDirectory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-backup-"));
  const snapshotPath = path.join(temporaryDirectory, "scout.sqlite");
  try {
    const { sqlite } = getDatabase();
    await sqlite.backup(snapshotPath);
    const payload: BackupPayload = {
      version: ARCHIVE_VERSION,
      database: fs.readFileSync(snapshotPath).toString("base64"),
      keys: Object.fromEntries(
        KEY_FILES.map((name) => [
          name,
          fs.readFileSync(path.join(dataDirectory, name)).toString("base64"),
        ]),
      ),
    };
    writeAtomically(
      outputPath,
      JSON.stringify(encrypt(Buffer.from(JSON.stringify(payload)), passphrase)),
    );
  } finally {
    fs.rmSync(temporaryDirectory, { recursive: true, force: true });
  }
}

export function restoreEncryptedBackup(
  archivePath: string,
  destinationDirectory: string,
  passphrase: string,
): void {
  requirePassphrase(passphrase);
  if (fs.existsSync(destinationDirectory) && fs.readdirSync(destinationDirectory).length > 0) {
    throw new Error("Restore destination must be a clean directory.");
  }
  const envelope = JSON.parse(fs.readFileSync(archivePath, "utf8")) as BackupEnvelope;
  const payload = JSON.parse(decrypt(envelope, passphrase).toString("utf8")) as BackupPayload;
  if (payload.version !== ARCHIVE_VERSION || typeof payload.database !== "string")
    throw new Error("Backup archive format is invalid.");

  const keys = Object.fromEntries(
    KEY_FILES.map((name) => {
      const encoded = payload.keys?.[name];
      if (!encoded) throw new Error(`Backup key is missing: ${name}`);
      const value = Buffer.from(encoded, "base64");
      if (value.length !== 32) throw new Error(`Backup key has an invalid length: ${name}`);
      return [name, value];
    }),
  );
  const database = Buffer.from(payload.database, "base64");
  if (database.length === 0) throw new Error("Backup database snapshot is empty.");

  const parent = path.dirname(destinationDirectory);
  fs.mkdirSync(parent, { recursive: true, mode: 0o700 });
  const stagingDirectory = fs.mkdtempSync(path.join(parent, ".scout-restore-"));
  try {
    writeAtomically(path.join(stagingDirectory, "scout.sqlite"), database);
    for (const [name, value] of Object.entries(keys))
      writeAtomically(path.join(stagingDirectory, name), value);
    validateAndPrepareRestoredDatabase(path.join(stagingDirectory, "scout.sqlite"));

    if (fs.existsSync(destinationDirectory) && fs.readdirSync(destinationDirectory).length > 0)
      throw new Error("Restore destination must be a clean directory.");
    fs.mkdirSync(destinationDirectory, { recursive: true, mode: 0o700 });
    for (const name of ["scout.sqlite", ...KEY_FILES])
      fs.renameSync(path.join(stagingDirectory, name), path.join(destinationDirectory, name));
  } finally {
    fs.rmSync(stagingDirectory, { recursive: true, force: true });
  }
}

function validateAndPrepareRestoredDatabase(filename: string): void {
  const sqlite = new Database(filename, { fileMustExist: true });
  try {
    const beforeMigration = sqlite.pragma("integrity_check", { simple: true });
    if (beforeMigration !== "ok")
      throw new Error(`Backup SQLite integrity check failed: ${String(beforeMigration)}`);

    migrateDatabase(sqlite);
    const migration = sqlite
      .prepare("SELECT 1 FROM schema_migration WHERE version = ?")
      .get(LATEST_SCHEMA_VERSION);
    if (!migration) throw new Error("Backup database migrations are incomplete.");

    const afterMigration = sqlite.pragma("integrity_check", { simple: true });
    if (afterMigration !== "ok")
      throw new Error(`Restored SQLite integrity check failed: ${String(afterMigration)}`);
    const now = Date.now();
    sqlite.exec("DELETE FROM agent_invitation;");
    sqlite
      .prepare(
        "UPDATE enrollment_job SET status = 'blocked', stage = 'queued', error_code = 'restore-review', error_message = 'Restore requires owner review before installation resumes.', lease_owner = NULL, lease_expires_at = NULL, updated_at = ?",
      )
      .run(now);
    sqlite
      .prepare(
        "UPDATE scan_task SET status = 'superseded', lease_expires_at = NULL, completed_at = NULL WHERE status IN ('queued', 'leased')",
      )
      .run();
    sqlite
      .prepare(
        "UPDATE agent SET reconciliation_required = 1, reconciled_at = NULL, updated_at = ? WHERE revoked_at IS NULL",
      )
      .run(now);
    sqlite
      .prepare(
        "INSERT INTO app_setting (key, value, updated_at) VALUES ('authority_paused', 'true', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at",
      )
      .run(now);
  } finally {
    sqlite.close();
  }
}

function encrypt(plaintext: Buffer, passphrase: string): BackupEnvelope {
  const salt = randomBytes(16);
  const nonce = randomBytes(12);
  const cipher = createCipheriv("aes-256-gcm", scryptSync(passphrase, salt, 32), nonce);
  const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);
  return {
    version: ARCHIVE_VERSION,
    salt: salt.toString("base64url"),
    nonce: nonce.toString("base64url"),
    tag: cipher.getAuthTag().toString("base64url"),
    ciphertext: ciphertext.toString("base64url"),
  };
}

function decrypt(envelope: BackupEnvelope, passphrase: string): Buffer {
  if (envelope.version !== ARCHIVE_VERSION)
    throw new Error("Backup archive version is unsupported.");
  const decipher = createDecipheriv(
    "aes-256-gcm",
    scryptSync(passphrase, Buffer.from(envelope.salt, "base64url"), 32),
    Buffer.from(envelope.nonce, "base64url"),
  );
  decipher.setAuthTag(Buffer.from(envelope.tag, "base64url"));
  return Buffer.concat([
    decipher.update(Buffer.from(envelope.ciphertext, "base64url")),
    decipher.final(),
  ]);
}

function writeAtomically(destination: string, value: string | Buffer): void {
  const parent = path.dirname(destination);
  fs.mkdirSync(parent, { recursive: true, mode: 0o700 });
  const temporary = path.join(
    parent,
    `.${path.basename(destination)}.tmp-${process.pid}-${randomUUID()}`,
  );
  let descriptor: number | undefined;
  try {
    descriptor = fs.openSync(
      temporary,
      fs.constants.O_WRONLY | fs.constants.O_CREAT | fs.constants.O_EXCL,
      0o600,
    );
    fs.writeFileSync(descriptor, value);
    fs.fsyncSync(descriptor);
    fs.closeSync(descriptor);
    descriptor = undefined;
    fs.renameSync(temporary, destination);
    fs.chmodSync(destination, 0o600);
    syncDirectory(parent);
  } finally {
    if (descriptor !== undefined) fs.closeSync(descriptor);
    fs.rmSync(temporary, { force: true });
  }
}

function syncDirectory(directory: string): void {
  const descriptor = fs.openSync(directory, fs.constants.O_RDONLY);
  try {
    fs.fsyncSync(descriptor);
  } finally {
    fs.closeSync(descriptor);
  }
}

function requirePassphrase(passphrase: string): void {
  if (passphrase.length < 8)
    throw new Error("Backup passphrase must be at least eight characters.");
}
