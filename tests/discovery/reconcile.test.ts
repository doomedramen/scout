import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { encryptCredential } from "@/lib/server/credentials";
import { ensureNetworkSegment, reconcileScanResults } from "@/lib/server/discovery";
import { repairDuplicateSystems } from "@/lib/server/system-identity";
import { getFleetSnapshot } from "@/lib/server/systems";

describe("scan reconciliation", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    closeDatabase();
  });

  it("updates one candidate across repeated scans and removes it after a closed result", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const segment = ensureNetworkSegment(
      {
        cidr: "192.0.2.0/30",
        interfaceName: "test0",
        gateway: "192.0.2.1",
        sourceAddress: "192.0.2.2",
        provenanceKey: "test-network",
      },
      1_000,
    );

    reconcileScanResults(segment.id, [{ address: "192.0.2.2", port: 22, outcome: "open" }], 1_000);
    reconcileScanResults(segment.id, [{ address: "192.0.2.2", port: 22, outcome: "open" }], 2_000);

    const { sqlite } = getDatabase();
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM system").get()).toEqual({ count: 1 });
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM access_evidence").get()).toEqual({
      count: 1,
    });
    expect(getFleetSnapshot(2_000).summary.needsAccess).toBe(1);

    reconcileScanResults(
      segment.id,
      [{ address: "192.0.2.2", port: 22, outcome: "closed" }],
      3_000,
    );

    expect(getFleetSnapshot(3_000).systems).toHaveLength(0);
    expect(sqlite.prepare("SELECT outcome FROM access_evidence").get()).toEqual({
      outcome: "closed",
    });
  });

  it("uses one segment when a manual scope matches the current default route", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const manual = ensureNetworkSegment(
      {
        cidr: "192.168.1.0/24",
        interfaceName: "manual",
        gateway: null,
        sourceAddress: "192.168.1.0",
        provenanceKey: "manual|192.168.1.0/24",
      },
      1_000,
      "manual",
    );
    const automatic = ensureNetworkSegment(
      {
        cidr: "192.168.1.0/24",
        interfaceName: "en0",
        gateway: "192.168.1.1",
        sourceAddress: "192.168.1.14",
        provenanceKey: "en0|192.168.1.1|192.168.1.14|192.168.1.0/24",
      },
      2_000,
      "default-route",
    );

    const { sqlite } = getDatabase();
    expect(automatic.id).toBe(manual.id);
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM network_segment").get()).toEqual({
      count: 1,
    });
  });

  it("merges a host that moves addresses when its MAC and SSH identity agree", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const segment = ensureNetworkSegment(
      {
        cidr: "192.0.2.0/29",
        interfaceName: "test0",
        gateway: "192.0.2.1",
        sourceAddress: "192.0.2.2",
        provenanceKey: "stable-host",
      },
      1_000,
    );

    reconcileScanResults(
      segment.id,
      [
        {
          address: "192.0.2.2",
          port: 22,
          outcome: "open",
          macAddress: "02:00:00:00:00:01",
          fingerprint: "SHA256:host",
        },
      ],
      1_000,
    );
    reconcileScanResults(
      segment.id,
      [
        {
          address: "192.0.2.3",
          port: 22,
          outcome: "open",
          macAddress: "02:00:00:00:00:01",
          fingerprint: "SHA256:host",
        },
      ],
      2_000,
    );

    const { sqlite } = getDatabase();
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM system").get()).toEqual({ count: 1 });
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM system_address").get()).toEqual({
      count: 2,
    });
  });

  it("joins duplicate endpoint records when verified SSH identity agrees across segments", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    process.env.SCOUT_DATA_DIR = `/tmp/scout-test-reconcile-${Date.now()}`;
    const { sqlite } = getDatabase();
    const now = 1_000;
    sqlite
      .prepare(
        "INSERT INTO network_segment (id, site_key, provenance_key, cidr, source, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
      )
      .run("segment-a", "site-1", "route-a", "192.0.2.0/29", "default-route", now, now);
    sqlite
      .prepare(
        "INSERT INTO network_segment (id, site_key, provenance_key, cidr, source, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
      )
      .run("segment-b", "site-1", "route-b", "192.0.2.0/29", "default-route", now, now);
    for (const [systemId, segmentId] of [
      ["system-a", "segment-a"],
      ["system-b", "segment-b"],
    ]) {
      sqlite
        .prepare(
          "INSERT INTO system (id, segment_id, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'needs-access', ?, ?)",
        )
        .run(systemId, segmentId, systemId, now, now);
      sqlite
        .prepare(
          "INSERT INTO system_address (id, system_id, address, port, last_seen_at) VALUES (?, ?, ?, ?, ?)",
        )
        .run(`${systemId}-address`, systemId, "192.0.2.2", 22, now);
      sqlite
        .prepare(
          "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, fingerprint, source, observed_at, expires_at) VALUES (?, ?, 'ssh', ?, ?, 'open', ?, 'server-scan', ?, ?)",
        )
        .run(`${systemId}-evidence`, systemId, "192.0.2.2", 22, "SHA256:host", now, now + 10_000);
    }
    const encrypted = encryptCredential(
      {
        authType: "password",
        secret: "CorrectHorse1",
        passphrase: null,
        privilegePassword: null,
      },
      { systemId: "system-a", method: "ssh", username: "root" },
    );
    sqlite
      .prepare(
        "INSERT INTO credential_grant (id, system_id, method, username, secret_ciphertext, nonce, created_at, updated_at) VALUES (?, ?, 'ssh', ?, ?, ?, ?, ?)",
      )
      .run("credential-a", "system-a", "root", encrypted.ciphertext, encrypted.nonce, now, now);

    reconcileScanResults(
      "segment-b",
      [{ address: "192.0.2.2", port: 22, outcome: "open", fingerprint: "SHA256:host" }],
      now + 1_000,
    );

    expect(getFleetSnapshot(now + 1_000).systems).toHaveLength(1);
    expect(sqlite.prepare("SELECT system_id FROM credential_grant").get()).toEqual({
      system_id: "system-a",
    });
    expect(sqlite.prepare("SELECT canonical_system_id FROM system_alias").get()).toEqual({
      canonical_system_id: "system-a",
    });
  });

  it("repairs legacy same-segment endpoint duplicates during startup", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO network_segment (id, site_key, provenance_key, cidr, source, created_at, updated_at) VALUES ('segment-legacy', 'site-1', 'route', '192.0.2.0/29', 'default-route', 1000, 1000)",
      )
      .run();
    for (const systemId of ["system-old", "system-new"]) {
      sqlite
        .prepare(
          "INSERT INTO system (id, segment_id, display_name, status, created_at, updated_at) VALUES (?, 'segment-legacy', ?, 'needs-access', 1000, 1000)",
        )
        .run(systemId, systemId);
      sqlite
        .prepare(
          "INSERT INTO system_address (id, system_id, address, port, last_seen_at) VALUES (?, ?, '192.0.2.2', 22, 1000)",
        )
        .run(`${systemId}-address`, systemId);
      sqlite
        .prepare(
          "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, source, observed_at, expires_at) VALUES (?, ?, 'ssh', '192.0.2.2', 22, 'open', 'server-scan', 1000, 100000)",
        )
        .run(`${systemId}-evidence`, systemId);
    }

    expect(repairDuplicateSystems(sqlite, 2_000)).toBe(1);
    expect(getFleetSnapshot(2_000).systems).toHaveLength(1);
  });

  it("quarantines address reuse when the SSH identity changes", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const segment = ensureNetworkSegment(
      {
        cidr: "192.0.2.0/29",
        interfaceName: "test0",
        gateway: "192.0.2.1",
        sourceAddress: "192.0.2.2",
        provenanceKey: "reused-address",
      },
      1_000,
    );

    reconcileScanResults(
      segment.id,
      [{ address: "192.0.2.2", port: 22, outcome: "open", fingerprint: "SHA256:old" }],
      1_000,
    );
    reconcileScanResults(
      segment.id,
      [{ address: "192.0.2.2", port: 22, outcome: "open", fingerprint: "SHA256:new" }],
      2_000,
    );

    const { sqlite } = getDatabase();
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM system").get()).toEqual({ count: 2 });
    expect(sqlite.prepare("SELECT status FROM system ORDER BY created_at ASC").all()).toEqual([
      { status: "blocked" },
      { status: "needs-access" },
    ]);
    expect(
      sqlite.prepare("SELECT fingerprint FROM access_evidence WHERE outcome = 'open'").get(),
    ).toEqual({ fingerprint: "SHA256:new" });
  });
});
