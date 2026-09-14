import { afterEach, describe, expect, it, vi } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { createAccessGrant, preflightSsh } from "@/lib/server/access";
import * as ssh from "@/lib/server/ssh";

describe("SSH preflight identity reconciliation", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    closeDatabase();
  });

  it("joins a newly scanned record to the credential-bearing record after preflight", async () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    process.env.SCOUT_DATA_DIR = `/tmp/scout-test-preflight-${Date.now()}`;
    const { sqlite } = getDatabase();
    for (const [segmentId, provenance] of [
      ["segment-old", "route-old"],
      ["segment-new", "route-new"],
    ]) {
      sqlite
        .prepare(
          "INSERT INTO network_segment (id, site_key, provenance_key, cidr, source, created_at, updated_at) VALUES (?, 'site-1', ?, '192.0.2.0/29', 'default-route', 1000, 1000)",
        )
        .run(segmentId, provenance);
    }
    for (const [systemId, segmentId, fingerprint] of [
      ["system-old", "segment-old", "SHA256:host"],
      ["system-new", "segment-new", null],
    ] as const) {
      sqlite
        .prepare(
          "INSERT INTO system (id, segment_id, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'needs-access', 1000, 1000)",
        )
        .run(systemId, segmentId, systemId);
      sqlite
        .prepare(
          "INSERT INTO system_address (id, system_id, address, port, last_seen_at) VALUES (?, ?, '192.0.2.2', 22, 1000)",
        )
        .run(`${systemId}-address`, systemId);
      sqlite
        .prepare(
          "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, fingerprint, source, observed_at, expires_at) VALUES (?, ?, 'ssh', '192.0.2.2', 22, 'open', ?, 'server-scan', 1000, 100000)",
        )
        .run(`${systemId}-evidence`, systemId, fingerprint);
    }
    createAccessGrant(
      {
        systemId: "system-old",
        method: "ssh",
        username: "root",
        authType: "password",
        secret: "CorrectHorse1",
        passphrase: null,
        fingerprint: "SHA256:host",
        trust: true,
        idempotencyKey: "preflight-identity-job",
      },
      1_000,
    );
    vi.spyOn(ssh, "readSshFingerprint").mockResolvedValue("SHA256:host");

    await expect(preflightSsh("system-new", 2_000)).resolves.toMatchObject({
      systemId: "system-old",
      fingerprint: "SHA256:host",
      trusted: true,
    });
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM system_alias").get()).toEqual({
      count: 1,
    });
    expect(sqlite.prepare("SELECT system_id FROM credential_grant").get()).toEqual({
      system_id: "system-old",
    });
  });
});
