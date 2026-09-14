import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import { createDatabase } from "@/lib/server/db";
import { authSecret, controlSigningKey, credentialKey } from "@/lib/server/keys";

const directories: string[] = [];

afterEach(() => {
  delete process.env.SCOUT_DATA_DIR;
  for (const directory of directories.splice(0))
    fs.rmSync(directory, { recursive: true, force: true });
});

function temporaryDirectory(): string {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-keys-"));
  directories.push(directory);
  process.env.SCOUT_DATA_DIR = directory;
  return directory;
}

describe("persistent key lifecycle", () => {
  it("creates all keys on an empty first boot", () => {
    const directory = temporaryDirectory();

    expect(authSecret()).toBeTruthy();
    expect(credentialKey()).toHaveLength(32);
    expect(controlSigningKey()).toHaveLength(32);
    expect(fs.readdirSync(directory).sort()).toEqual([
      "auth.secret",
      "control-signing.key",
      "credentials.key",
      "keys.initialized",
    ]);
  });

  it("fails closed when an initialized installation loses one key", () => {
    const directory = temporaryDirectory();
    authSecret();
    credentialKey();
    controlSigningKey();
    fs.unlinkSync(path.join(directory, "credentials.key"));

    expect(() => credentialKey()).toThrow(/missing/i);
  });

  it("fails closed when existing application data has no keys", () => {
    const directory = temporaryDirectory();
    const database = createDatabase(path.join(directory, "scout.sqlite"));
    database.sqlite
      .prepare(
        "INSERT INTO user (id, name, email, username, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
      )
      .run("owner", "Owner", "owner@scout.internal", "owner", 1_000, 1_000);
    database.sqlite.close();

    expect(() => authSecret()).toThrow(/missing/i);
  });
});
