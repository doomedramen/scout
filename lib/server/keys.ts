import { createHash, randomBytes } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import Database from "better-sqlite3";

const KEY_FILES = ["auth.secret", "credentials.key", "control-signing.key"] as const;
const INITIALIZED_MARKER = "keys.initialized";

function dataDirectory(): string {
  return process.env.SCOUT_DATA_DIR ?? ".scout-data";
}

export function persistentKey(name: string, size = 32): Buffer {
  const directory = dataDirectory();
  fs.mkdirSync(/*turbopackIgnore: true*/ directory, { recursive: true, mode: 0o700 });
  const filename = path.join(/*turbopackIgnore: true*/ directory, name);

  if (!fs.existsSync(/*turbopackIgnore: true*/ filename)) {
    if (
      fs.existsSync(/*turbopackIgnore: true*/ path.join(directory, INITIALIZED_MARKER)) ||
      hasApplicationData(directory)
    ) {
      throw new Error(`Persistent key ${name} is missing; restore Scout data and keys together`);
    }
  }

  try {
    const key = randomBytes(size);
    const descriptor = fs.openSync(/*turbopackIgnore: true*/ filename, "wx", 0o600);
    try {
      fs.writeFileSync(/*turbopackIgnore: true*/ descriptor, key);
    } finally {
      fs.closeSync(descriptor);
    }
    markInitialized(directory);
    return key;
  } catch (error) {
    if (!(error instanceof Error) || !((error as NodeJS.ErrnoException).code === "EEXIST")) {
      throw error;
    }
  }

  const key = fs.readFileSync(/*turbopackIgnore: true*/ filename);
  if (key.length !== size) {
    throw new Error(`Persistent key ${name} has an invalid length`);
  }
  const mode = fs.statSync(/*turbopackIgnore: true*/ filename).mode & 0o777;
  if (mode & 0o077) {
    throw new Error(`Persistent key ${name} has unsafe permissions`);
  }
  markInitialized(directory);
  return key;
}

function markInitialized(directory: string): void {
  if (
    !KEY_FILES.every((name) => fs.existsSync(/*turbopackIgnore: true*/ path.join(directory, name)))
  )
    return;
  const marker = path.join(/*turbopackIgnore: true*/ directory, INITIALIZED_MARKER);
  try {
    const descriptor = fs.openSync(/*turbopackIgnore: true*/ marker, "wx", 0o600);
    fs.closeSync(descriptor);
  } catch (error) {
    if (!(error instanceof Error) || (error as NodeJS.ErrnoException).code !== "EEXIST")
      throw error;
  }
}

function hasApplicationData(directory: string): boolean {
  const configured = process.env.SCOUT_DATABASE_URL;
  const filename = configured?.startsWith("file:")
    ? configured.slice("file:".length) || ":memory:"
    : path.join(/*turbopackIgnore: true*/ directory, "scout.sqlite");
  if (filename === ":memory:" || !fs.existsSync(/*turbopackIgnore: true*/ filename)) return false;

  let sqlite: Database.Database | undefined;
  try {
    sqlite = new Database(filename, { readonly: true, fileMustExist: true });
    for (const table of [
      "user",
      "setup_token",
      "network_segment",
      "system",
      "credential_grant",
      "agent",
      "app_setting",
    ]) {
      const exists = sqlite
        .prepare("SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?")
        .get(table);
      if (exists && (sqlite.prepare(`SELECT 1 FROM ${table} LIMIT 1`).get() ?? null)) return true;
    }
    return false;
  } catch {
    return true;
  } finally {
    sqlite?.close();
  }
}

export function authSecret(): string {
  return persistentKey("auth.secret").toString("base64url");
}

export function credentialKey(): Buffer {
  return persistentKey("credentials.key");
}

export function controlSigningKey(): Buffer {
  return persistentKey("control-signing.key");
}

/**
 * A stable, out-of-band identity for the Scout HTTP bootstrap endpoint.
 *
 * This is deliberately derived from the persistent control key rather than a
 * per-process value. It is not a replacement for HTTPS; it gives a trusted LAN
 * operator a value they can compare before allowing an HTTP installer to run.
 */
export function bootstrapFingerprint(): string {
  return `SHA256:${createHash("sha256").update(controlSigningKey()).digest("base64url")}`;
}
