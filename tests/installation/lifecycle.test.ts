import { afterEach, describe, expect, it, vi } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { createAccessGrant } from "@/lib/server/access";
import { decommissionSystem, retryEnrollment } from "@/lib/server/lifecycle";
import * as ssh from "@/lib/server/ssh";

describe("system lifecycle actions", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    closeDatabase();
  });

  it("revokes the API identity before reporting uninstall as pending/manual", async () => {
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

    await expect(decommissionSystem("system-1", 2_000)).resolves.toEqual({
      systemId: "system-1",
      status: "revoked",
      uninstall: "pending/manual",
    });
    expect(sqlite.prepare("SELECT revoked_at FROM agent WHERE id = 'agent-1'").get()).toEqual({
      revoked_at: 2_000,
    });
  });

  it("revokes first, then uninstalls through the authorized SSH executor", async () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    process.env.SCOUT_DATA_DIR = `/tmp/scout-test-decommission-${Date.now()}`;
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES ('system-1', 'host', 'online', 1, 1)",
      )
      .run();
    sqlite
      .prepare(
        "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, fingerprint, source, observed_at, expires_at) VALUES ('evidence-1', 'system-1', 'ssh', '192.0.2.10', 22, 'open', 'SHA256:host', 'server-scan', 1, 100000)",
      )
      .run();
    createAccessGrant(
      {
        systemId: "system-1",
        method: "ssh",
        username: "root",
        authType: "password",
        secret: "CorrectHorse1",
        passphrase: null,
        fingerprint: "SHA256:host",
        trust: true,
        idempotencyKey: "decommission-grant",
      },
      1_000,
    );
    sqlite
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, created_at, updated_at) VALUES ('agent-1', 'system-1', ?, 'linux', 'x86_64', '0.1.0', 1, 1)",
      )
      .run("a".repeat(43));

    let revokedBeforeUninstall = false;
    const result = await decommissionSystem("system-1", 2_000, async (context) => {
      revokedBeforeUninstall =
        sqlite.prepare("SELECT revoked_at FROM agent WHERE id = 'agent-1'").get() !== undefined;
      expect(context.endpoint).toEqual({ address: "192.0.2.10", port: 22 });
      expect(context.credential.username).toBe("root");
    });

    expect(revokedBeforeUninstall).toBe(true);
    expect(result).toEqual({
      systemId: "system-1",
      status: "revoked",
      uninstall: "complete",
    });
    expect(sqlite.prepare("SELECT enabled FROM credential_grant").get()).toEqual({ enabled: 0 });
    expect(sqlite.prepare("SELECT status FROM enrollment_job").get()).toEqual({
      status: "blocked",
    });
  });

  it("keeps the truthful pending/manual state when SSH uninstall fails", async () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    process.env.SCOUT_DATA_DIR = `/tmp/scout-test-decommission-failed-${Date.now()}`;
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES ('system-1', 'host', 'online', 1, 1)",
      )
      .run();
    sqlite
      .prepare(
        "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, fingerprint, source, observed_at, expires_at) VALUES ('evidence-1', 'system-1', 'ssh', '192.0.2.10', 22, 'open', 'SHA256:host', 'server-scan', 1, 100000)",
      )
      .run();
    createAccessGrant(
      {
        systemId: "system-1",
        method: "ssh",
        username: "root",
        authType: "password",
        secret: "CorrectHorse1",
        passphrase: null,
        fingerprint: "SHA256:host",
        trust: true,
        idempotencyKey: "decommission-failed-grant",
      },
      1_000,
    );
    sqlite
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, created_at, updated_at) VALUES ('agent-1', 'system-1', ?, 'linux', 'x86_64', '0.1.0', 1, 1)",
      )
      .run("b".repeat(43));

    await expect(
      decommissionSystem("system-1", 2_000, async () => {
        throw new Error("SSH unavailable");
      }),
    ).resolves.toEqual({
      systemId: "system-1",
      status: "revoked",
      uninstall: "pending/manual",
    });
  });

  it("uses the verified SSH connection to remove Linux and macOS service state", async () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    process.env.SCOUT_DATA_DIR = `/tmp/scout-test-decommission-command-${Date.now()}`;
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES ('system-1', 'host', 'online', 1, 1)",
      )
      .run();
    sqlite
      .prepare(
        "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, fingerprint, source, observed_at, expires_at) VALUES ('evidence-1', 'system-1', 'ssh', '192.0.2.10', 22, 'open', 'SHA256:host', 'server-scan', 1, 100000)",
      )
      .run();
    createAccessGrant(
      {
        systemId: "system-1",
        method: "ssh",
        username: "root",
        authType: "password",
        secret: "CorrectHorse1",
        passphrase: null,
        fingerprint: "SHA256:host",
        trust: true,
        idempotencyKey: "decommission-command-grant",
      },
      1_000,
    );
    sqlite
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, created_at, updated_at) VALUES ('agent-1', 'system-1', ?, 'linux', 'x86_64', '0.1.0', 1, 1)",
      )
      .run("c".repeat(43));

    const commands: string[] = [];
    const close = vi.fn();
    vi.spyOn(ssh, "connectSsh").mockResolvedValue({
      exec: async (command) => {
        commands.push(command);
        return { code: 0, stdout: command === "id -u" ? "0\n" : "", stderr: "" };
      },
      close,
    });

    await expect(decommissionSystem("system-1", 2_000)).resolves.toMatchObject({
      uninstall: "complete",
    });
    expect(commands[0]).toBe("id -u");
    expect(commands[1]).toContain("systemctl disable --now scout-agent.service");
    expect(commands[1]).toContain("launchctl bootout system");
    expect(close).toHaveBeenCalledOnce();
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
