import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { createAccessGrant } from "@/lib/server/access";
import { decommissionSystem, retryEnrollment } from "@/lib/server/lifecycle";

describe("system lifecycle actions", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    closeDatabase();
  });

  it("revokes the API identity before reporting uninstall as pending/manual", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES ('system-1', 'host', 'online', 1, 1)",
      )
      .run();
    sqlite
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, created_at, updated_at) VALUES ('agent-1', 'system-1', ?, 'linux', 'x86_64', '0.1.0', 1, 1)",
      )
      .run("a".repeat(43));

    expect(decommissionSystem("system-1", 2_000)).toEqual({
      systemId: "system-1",
      status: "revoked",
      uninstall: "pending/manual",
    });
    expect(sqlite.prepare("SELECT revoked_at FROM agent WHERE id = 'agent-1'").get()).toEqual({
      revoked_at: 2_000,
    });
  });

  it("requeues a failed job using the existing encrypted credential", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    process.env.SCOUT_DATA_DIR = `/tmp/scout-test-retry-${Date.now()}`;
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES ('system-1', 'host', 'blocked', 1, 1)",
      )
      .run();
    const grant = createAccessGrant(
      {
        systemId: "system-1",
        method: "ssh",
        username: "root",
        authType: "password",
        secret: "CorrectHorse1",
        passphrase: null,
        fingerprint: "SHA256:test",
        trust: true,
        idempotencyKey: "retry-grant",
      },
      1_000,
    );
    sqlite
      .prepare(
        "UPDATE enrollment_job SET status = 'failed', stage = 'authentication', error_code = 'authentication', error_message = 'bad password' WHERE id = ?",
      )
      .run(grant.jobId);

    expect(retryEnrollment("system-1", 2_000)).toEqual({
      jobId: grant.jobId,
      status: "queued",
      stage: "queued",
    });
    expect(
      sqlite
        .prepare("SELECT status, error_message FROM enrollment_job WHERE id = ?")
        .get(grant.jobId),
    ).toEqual({
      status: "queued",
      error_message: null,
    });
  });
});
