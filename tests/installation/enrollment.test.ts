import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { createAccessGrant } from "@/lib/server/access";
import { claimNextEnrollmentJob, processNextEnrollmentJob } from "@/lib/server/enrollment";

describe("enrollment worker", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    delete process.env.SCOUT_PUBLIC_URL;
    closeDatabase();
  });

  it("claims, stages, and completes one automatic installation", async () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    process.env.SCOUT_DATA_DIR = `/tmp/scout-test-enrollment-${Date.now()}`;
    process.env.SCOUT_PUBLIC_URL = "http://127.0.0.1:18081";
    const { sqlite } = getDatabase();
    insertSystemAndEvidence(sqlite, 1_000);
    const receipt = createAccessGrant(
      {
        systemId: "system-1",
        method: "ssh",
        username: "root",
        authType: "password",
        secret: "CorrectHorse1",
        passphrase: null,
        fingerprint: "SHA256:test",
        trust: true,
        idempotencyKey: "job-idempotency-1",
      },
      1_000,
    );
    const seenStages: string[] = [];
    const result = await processNextEnrollmentJob(async (context, setStage) => {
      expect(context.endpoint).toEqual({ address: "192.0.2.10", port: 22 });
      expect(context.credential.username).toBe("root");
      expect(context.credential.secret).toBe("CorrectHorse1");
      expect(context.serverUrl).toBe("http://127.0.0.1:18081");
      expect(context.invitation).toHaveLength(64);
      for (const stage of ["authentication", "privilege-check", "installation"] as const) {
        seenStages.push(stage);
        setStage(stage);
      }
      sqlite
        .prepare(
          "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, last_heartbeat_at, created_at, updated_at) VALUES (?, ?, ?, 'linux', 'x86_64', '0.1.0', ?, ?, ?)",
        )
        .run("agent-1", context.systemId, "a".repeat(43), 2_000, 2_000, 2_000);
    }, 2_000);

    expect(result).toEqual({
      jobId: receipt.jobId,
      status: "complete",
      stage: "complete",
      errorMessage: null,
    });
    expect(seenStages).toEqual(["authentication", "privilege-check", "installation"]);
    expect(
      sqlite
        .prepare("SELECT status, stage, lease_owner FROM enrollment_job WHERE id = ?")
        .get(receipt.jobId),
    ).toEqual({
      status: "complete",
      stage: "complete",
      lease_owner: null,
    });
    expect(
      sqlite
        .prepare("SELECT COUNT(*) AS count FROM agent_invitation WHERE system_id = ?")
        .get("system-1"),
    ).toEqual({
      count: 1,
    });
  });

  it("records a blocker when SSH evidence disappears before the worker starts", async () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    process.env.SCOUT_DATA_DIR = `/tmp/scout-test-enrollment-missing-${Date.now()}`;
    const { sqlite } = getDatabase();
    insertSystemAndEvidence(sqlite, 1_000);
    const receipt = createAccessGrant(
      {
        systemId: "system-1",
        method: "ssh",
        username: "root",
        authType: "password",
        secret: "CorrectHorse1",
        passphrase: null,
        fingerprint: "SHA256:test",
        trust: true,
        idempotencyKey: "job-idempotency-2",
      },
      1_000,
    );
    sqlite.prepare("UPDATE access_evidence SET expires_at = ?").run(1_500);

    const result = await processNextEnrollmentJob(async () => undefined, 2_000);

    expect(result).toMatchObject({
      jobId: receipt.jobId,
      status: "failed",
      stage: "connecting",
      errorMessage: "The system no longer has current SSH evidence.",
    });
    expect(sqlite.prepare("SELECT status FROM system WHERE id = ?").get("system-1")).toEqual({
      status: "blocked",
    });
  });

  it("does not let an unexpired lease be claimed twice and reclaims it after expiry", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    insertSystemAndEvidence(sqlite, 1_000);
    createAccessGrant(
      {
        systemId: "system-1",
        method: "ssh",
        username: "root",
        authType: "password",
        secret: "CorrectHorse1",
        passphrase: null,
        fingerprint: "SHA256:test",
        trust: true,
        idempotencyKey: "job-idempotency-3",
      },
      1_000,
    );

    expect(claimNextEnrollmentJob(2_000)).not.toBeNull();
    expect(claimNextEnrollmentJob(2_001)).toBeNull();
    expect(claimNextEnrollmentJob(302_001)).not.toBeNull();
  });
});

function insertSystemAndEvidence(sqlite: ReturnType<typeof getDatabase>["sqlite"], now: number) {
  sqlite
    .prepare(
      "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES (?, ?, 'needs-access', ?, ?)",
    )
    .run("system-1", "192.0.2.10", now, now);
  sqlite
    .prepare(
      "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, fingerprint, source, observed_at, expires_at) VALUES (?, ?, 'ssh', ?, ?, 'open', NULL, 'test', ?, ?)",
    )
    .run("evidence-1", "system-1", "192.0.2.10", 22, now, now + 60_000);
}
