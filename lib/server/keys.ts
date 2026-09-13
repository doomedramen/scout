import { createHash, randomBytes } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

function dataDirectory(): string {
  return process.env.SCOUT_DATA_DIR ?? ".scout-data";
}

export function persistentKey(name: string, size = 32): Buffer {
  const directory = dataDirectory();
  fs.mkdirSync(directory, { recursive: true, mode: 0o700 });
  const filename = path.join(directory, name);

  try {
    const key = randomBytes(size);
    const descriptor = fs.openSync(filename, "wx", 0o600);
    try {
      fs.writeFileSync(descriptor, key);
    } finally {
      fs.closeSync(descriptor);
    }
    return key;
  } catch (error) {
    if (!(error instanceof Error) || !((error as NodeJS.ErrnoException).code === "EEXIST")) {
      throw error;
    }
  }

  const key = fs.readFileSync(filename);
  if (key.length !== size) {
    throw new Error(`Persistent key ${name} has an invalid length`);
  }
  return key;
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
