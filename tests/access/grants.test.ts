import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { createAccessGrant } from "@/lib/server/access";
import { decryptCredential } from "@/lib/server/credentials";

describe("access grants", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    closeDatabase();
  });

  it("accepts a trusted SSH credential once and reuses the job for an idempotent retry", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    process.env.SCOUT_DATA_DIR = "/tmp/scout-test-access";
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES (?, ?, 'needs-trust', ?, ?)",
      )
      .run("system-1", "192.0.2.10", 1_000, 1_000);

    const input = {
      systemId: "system-1",
      method: "ssh" as const,
      username: "root",
      authType: "password" as const,
      secret: "correct horse",
      passphrase: null,
      fingerprint: "SHA256:test",
      trust: true,
      idempotencyKey: "6ecf8a3e-bf59-4ca7-9e59-4e8b4f4b7c3a",
    };

    const first = createAccessGrant(input, 1_000);
    const retry = createAccessGrant({ ...input, secret: "different" }, 2_000);

    expect(retry).toEqual(first);
    expect(first).toMatchObject({
      attempt: 0,
      createdAt: new Date(1_000).toISOString(),
      updatedAt: new Date(1_000).toISOString(),
      leaseExpiresAt: null,
    });
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM credential_grant").get()).toEqual({
      count: 1,
    });
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM enrollment_job").get()).toEqual({
      count: 1,
    });
    expect(
      sqlite.prepare("SELECT secret_ciphertext FROM credential_grant").get(),
    ).not.toMatchObject({ secret_ciphertext: "correct horse" });
  });

  it("rejects a changed host fingerprint", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    process.env.SCOUT_DATA_DIR = "/tmp/scout-test-access-changed";
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES (?, ?, 'needs-trust', ?, ?)",
      )
      .run("system-1", "192.0.2.10", 1_000, 1_000);
    sqlite
      .prepare(
        "INSERT INTO trusted_host_key (id, system_id, method, fingerprint, accepted_at) VALUES (?, ?, ?, ?, ?)",
      )
      .run("key-1", "system-1", "ssh", "SHA256:old", 1_000);

    expect(() =>
      createAccessGrant(
        {
          systemId: "system-1",
          method: "ssh",
          username: "root",
          authType: "password",
          secret: "correct horse",
          passphrase: null,
          fingerprint: "SHA256:new",
          trust: true,
          idempotencyKey: "f6a3d42e-a2e5-45e3-b591-2f8b4f54a6fd",
        },
        2_000,
      ),
    ).toThrow(/fingerprint/i);
  });

  it("encrypts a separate privilege password with the SSH credential", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    process.env.SCOUT_DATA_DIR = "/tmp/scout-test-access-privilege";
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES (?, ?, 'needs-trust', ?, ?)",
      )
      .run("system-1", "192.0.2.10", 1_000, 1_000);

    const result = createAccessGrant(
      {
        systemId: "system-1",
        method: "ssh",
        username: "scout",
        authType: "private-key",
        secret: "-----BEGIN OPENSSH PRIVATE KEY-----\\nkey\\n-----END OPENSSH PRIVATE KEY-----",
        passphrase: "key-passphrase",
        privilegePassword: "sudo-password",
        fingerprint: "SHA256:test",
        trust: true,
        idempotencyKey: "privilege-password-idempotency",
      },
      1_000,
    );

    const row = sqlite
      .prepare(
        "SELECT system_id AS systemId, method, username, secret_ciphertext AS ciphertext, nonce FROM credential_grant WHERE id = (SELECT credential_id FROM enrollment_job WHERE id = ?)",
      )
      .get(result.jobId) as {
      systemId: string;
      method: string;
      username: string;
      ciphertext: string;
      nonce: string;
    };
    expect(decryptCredential(row, row)).toEqual({
      authType: "private-key",
      secret: expect.stringContaining("BEGIN OPENSSH PRIVATE KEY"),
      passphrase: "key-passphrase",
      privilegePassword: "sudo-password",
    });
  });
});
