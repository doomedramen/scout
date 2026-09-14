import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import Database from "better-sqlite3";

const dataDirectory = process.env.SCOUT_DATA_DIR || "/data";
const keyFiles = ["auth.secret", "credentials.key", "control-signing.key"];

const [operation, target] = process.argv.slice(2);

if (
  !operation ||
  ![
    "backup",
    "restore",
    "snapshot",
    "restore-snapshot",
    "authority",
    "set-authority",
    "pause",
    "resume",
  ].includes(operation)
) {
  console.error(
    "Usage: scout-ops.mjs {backup|restore|snapshot|restore-snapshot} TARGET | {authority|pause|resume|set-authority VALUE}",
  );
  process.exit(2);
}

if (["backup", "restore"].includes(operation)) {
  const passphrase = fs.readFileSync(0, "utf8").split(/\r?\n/, 1)[0];
  if (!passphrase) {
    console.error("A non-empty backup passphrase is required.");
    process.exit(2);
  }
  if (operation === "backup") await backup(target, passphrase);
  else await restore(target, passphrase);
} else if (operation === "snapshot") await snapshot(target);
else if (operation === "restore-snapshot") await restoreSnapshot(target);
else if (operation === "authority") process.stdout.write(`${readAuthority()}\n`);
else if (operation === "pause") setAuthority(true);
else if (operation === "resume") setAuthority(false);
else if (operation === "set-authority") {
  if (target !== "true" && target !== "false") {
    console.error("Authority value must be true or false.");
    process.exit(2);
  }
  setAuthority(target === "true");
}

function databasePath() {
  return path.join(dataDirectory, "scout.sqlite");
}

async function backup(output, secret) {
  const source = databasePath();
  if (!fs.existsSync(source)) throw new Error("Scout database does not exist.");
  const temporaryDirectory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-backup-"));
  const snapshot = path.join(temporaryDirectory, "scout.sqlite");
  const database = new Database(source, { readonly: true, fileMustExist: true });
  try {
    await database.backup(snapshot);
  } finally {
    database.close();
  }
  const payload = {
    version: 1,
    database: fs.readFileSync(snapshot).toString("base64"),
    keys: Object.fromEntries(
      keyFiles.map((name) => [
        name,
        fs.readFileSync(path.join(dataDirectory, name)).toString("base64"),
      ]),
    ),
  };
  const encrypted = encrypt(Buffer.from(JSON.stringify(payload)), secret);
  writeAtomically(output, JSON.stringify(encrypted));
  fs.rmSync(temporaryDirectory, { recursive: true, force: true });
  process.stdout.write(`Scout backup written to ${output}\n`);
}

async function snapshot(output) {
  if (!output) throw new Error("Update snapshot directory is required.");
  fs.mkdirSync(output, { recursive: true, mode: 0o700 });
  const source = databasePath();
  if (!fs.existsSync(source)) throw new Error("Scout database does not exist.");
  const snapshotPath = path.join(output, "scout.sqlite");
  const database = new Database(source, { readonly: true, fileMustExist: true });
  try {
    await database.backup(snapshotPath);
  } finally {
    database.close();
  }
  for (const name of keyFiles) {
    const sourceKey = path.join(dataDirectory, name);
    if (!fs.existsSync(sourceKey)) throw new Error(`Scout key does not exist: ${name}`);
    fs.copyFileSync(sourceKey, path.join(output, name));
    fs.chmodSync(path.join(output, name), 0o600);
  }
  writeAtomically(path.join(output, "snapshot.json"), JSON.stringify({ version: 1 }));
  process.stdout.write(`Scout update snapshot written to ${output}\n`);
}

async function restoreSnapshot(input) {
  if (!input || !fs.existsSync(input)) throw new Error("Update snapshot directory does not exist.");
  const marker = JSON.parse(fs.readFileSync(path.join(input, "snapshot.json"), "utf8"));
  if (marker.version !== 1) throw new Error("Update snapshot format is unsupported.");
  const database = fs.readFileSync(path.join(input, "scout.sqlite"));
  validateSQLite(path.join(input, "scout.sqlite"));
  const keys = Object.fromEntries(
    keyFiles.map((name) => {
      const value = fs.readFileSync(path.join(input, name));
      if (value.length !== 32) throw new Error(`Update snapshot key is invalid: ${name}`);
      return [name, value];
    }),
  );
  fs.mkdirSync(dataDirectory, { recursive: true, mode: 0o700 });
  writeAtomically(databasePath(), database);
  for (const [name, value] of Object.entries(keys))
    writeAtomically(path.join(dataDirectory, name), value);
  process.stdout.write("Scout update snapshot restored\n");
}

function readAuthority() {
  if (!fs.existsSync(databasePath())) return false;
  const database = new Database(databasePath(), { readonly: true, fileMustExist: true });
  try {
    const row = database
      .prepare("SELECT value FROM app_setting WHERE key = 'authority_paused'")
      .get();
    return row?.value === "true";
  } finally {
    database.close();
  }
}

function setAuthority(paused) {
  const database = new Database(databasePath(), { fileMustExist: true });
  try {
    database
      .prepare(
        "INSERT INTO app_setting (key, value, updated_at) VALUES ('authority_paused', ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at",
      )
      .run(String(paused), Date.now());
  } finally {
    database.close();
  }
  process.stdout.write(`Scout authority ${paused ? "paused" : "resumed"}\n`);
}

async function restore(input, secret) {
  const envelope = JSON.parse(fs.readFileSync(input, "utf8"));
  const payload = JSON.parse(decrypt(envelope, secret).toString("utf8"));
  if (payload.version !== 1 || typeof payload.database !== "string" || !payload.keys) {
    throw new Error("The backup archive format is invalid.");
  }
  const database = Buffer.from(payload.database, "base64");
  if (database.length === 0) throw new Error("Backup database snapshot is empty.");
  const keys = Object.fromEntries(
    keyFiles.map((name) => {
      const value = payload.keys[name];
      if (typeof value !== "string") throw new Error(`Backup key is missing: ${name}`);
      const decoded = Buffer.from(value, "base64");
      const expected = name === "auth.secret" ? 32 : 32;
      if (decoded.length !== expected) throw new Error(`Backup key has an invalid length: ${name}`);
      return [name, decoded];
    }),
  );
  const temporaryDirectory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-restore-"));
  const temporaryDatabase = path.join(temporaryDirectory, "scout.sqlite");
  fs.writeFileSync(temporaryDatabase, database, { mode: 0o600 });
  try {
    validateSQLite(temporaryDatabase);
  } finally {
    fs.rmSync(temporaryDirectory, { recursive: true, force: true });
  }
  fs.mkdirSync(dataDirectory, { recursive: true, mode: 0o700 });
  const previous = path.join(dataDirectory, `.restore-previous-${Date.now()}`);
  fs.mkdirSync(previous, { mode: 0o700 });
  for (const name of ["scout.sqlite", ...keyFiles]) {
    const current = path.join(dataDirectory, name);
    if (fs.existsSync(current)) fs.renameSync(current, path.join(previous, name));
  }
  writeAtomically(path.join(dataDirectory, "scout.sqlite"), database);
  for (const [name, value] of Object.entries(keys))
    writeAtomically(path.join(dataDirectory, name), value);
  const restored = new Database(path.join(dataDirectory, "scout.sqlite"));
  restored
    .prepare(
      "INSERT INTO app_setting (key, value, updated_at) VALUES ('authority_paused', 'true', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at",
    )
    .run(Date.now());
  restored.close();
  process.stdout.write(`Scout backup restored. Previous files are retained in ${previous}\n`);
}

function validateSQLite(filename) {
  const database = new Database(filename, { readonly: true, fileMustExist: true });
  try {
    const integrity = database.pragma("integrity_check", { simple: true });
    if (integrity !== "ok") throw new Error(`SQLite integrity check failed: ${String(integrity)}`);
    const schema = database
      .prepare("SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migration'")
      .get();
    if (!schema) throw new Error("SQLite snapshot has no Scout schema.");
  } finally {
    database.close();
  }
}

function encrypt(plaintext, secret) {
  const salt = crypto.randomBytes(16);
  const nonce = crypto.randomBytes(12);
  const key = crypto.scryptSync(secret, salt, 32);
  const cipher = crypto.createCipheriv("aes-256-gcm", key, nonce);
  const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);
  return {
    version: 1,
    salt: salt.toString("base64url"),
    nonce: nonce.toString("base64url"),
    tag: cipher.getAuthTag().toString("base64url"),
    ciphertext: ciphertext.toString("base64url"),
  };
}

function decrypt(envelope, secret) {
  if (envelope.version !== 1) throw new Error("The backup archive version is unsupported.");
  const key = crypto.scryptSync(secret, Buffer.from(envelope.salt, "base64url"), 32);
  const decipher = crypto.createDecipheriv(
    "aes-256-gcm",
    key,
    Buffer.from(envelope.nonce, "base64url"),
  );
  decipher.setAuthTag(Buffer.from(envelope.tag, "base64url"));
  return Buffer.concat([
    decipher.update(Buffer.from(envelope.ciphertext, "base64url")),
    decipher.final(),
  ]);
}

function writeAtomically(destination, value) {
  const temporary = `${destination}.tmp-${process.pid}`;
  fs.writeFileSync(temporary, value, { mode: 0o600 });
  fs.renameSync(temporary, destination);
  fs.chmodSync(destination, 0o600);
}
