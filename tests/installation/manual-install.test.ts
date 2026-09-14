import { afterEach, describe, expect, it, vi } from "vitest";

import { POST as createManualInstall } from "@/app/api/v1/systems/[systemId]/manual-install/route";
import { closeDatabase, getDatabase } from "@/lib/server/db";
import { renderManualInstallerCommand, scoutOrigin } from "@/lib/server/manual-install";

vi.mock("@/lib/server/session", () => ({
  requireApiSession: vi.fn(async () => ({ user: { id: "owner-1" } })),
}));

describe("manual agent installation", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_PUBLIC_URL;
    closeDatabase();
  });

  it("renders the requested one-line HTTPS installer command", () => {
    expect(
      renderManualInstallerCommand({
        serverUrl: "https://scout.example.test:8443",
        invitation: "a".repeat(64),
        trustPin: null,
      }),
    ).toBe(
      "SCOUT_OTI='" +
        "a".repeat(64) +
        "' bash -c \"$(curl -fsSL 'https://scout.example.test:8443/api/v1/bootstrap/agent/install.sh')\"",
    );
  });

  it("includes the out-of-band pin for HTTP bootstrap", () => {
    const command = renderManualInstallerCommand({
      serverUrl: "http://192.0.2.10:8080",
      invitation: "invitation",
      trustPin: "SHA256:bootstrap-pin",
    });

    expect(command).toContain("SCOUT_OTI='invitation'");
    expect(command).toContain("SCOUT_TRUST_PIN='SHA256:bootstrap-pin'");
    expect(command).toContain("/api/v1/bootstrap/agent/install.sh");
  });

  it("normalizes the configured public URL to an origin", () => {
    process.env.SCOUT_PUBLIC_URL = "https://scout.example.test:8443/systems";
    expect(scoutOrigin(new Request("http://internal.test/api/manual"))).toBe(
      "https://scout.example.test:8443",
    );
    delete process.env.SCOUT_PUBLIC_URL;
    expect(scoutOrigin(new Request("http://internal.test/api/manual"))).toBe(
      "http://internal.test",
    );
  });

  it("creates a system-bound invitation only for a current SSH candidate", async () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    const now = Date.now();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES ('system-1', 'target', 'needs-access', 1, 1)",
      )
      .run();
    sqlite
      .prepare(
        "INSERT INTO system_address (id, system_id, address, port, last_seen_at) VALUES ('address-1', 'system-1', '192.0.2.10', 22, 1)",
      )
      .run();
    sqlite
      .prepare(
        "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, source, observed_at, expires_at) VALUES ('evidence-1', 'system-1', 'ssh', '192.0.2.10', 22, 'open', 'test', ?, ?)",
      )
      .run(now, now + 1_800_000);

    const response = await createManualInstall(new Request("https://scout.example.test/systems"), {
      params: Promise.resolve({ systemId: "system-1" }),
    });
    const payload = (await response.json()) as {
      systemId: string;
      target: string;
      command: string;
      expiresAt: string;
    };

    expect(response.status).toBe(201);
    expect(payload).toMatchObject({ systemId: "system-1", target: "192.0.2.10:22" });
    expect(payload.expiresAt).toMatch(/Z$/);
    expect(payload.command).toContain("SCOUT_OTI=");
    expect(payload.command).toContain(
      "https://scout.example.test/api/v1/bootstrap/agent/install.sh",
    );
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM agent_invitation").get()).toEqual({
      count: 1,
    });
  });

  it("refuses manual installation when SSH evidence is no longer current", async () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES ('system-1', 'target', 'needs-access', 1, 1)",
      )
      .run();
    sqlite
      .prepare(
        "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, source, observed_at, expires_at) VALUES ('evidence-1', 'system-1', 'ssh', '192.0.2.10', 22, 'open', 'test', ?, ?)",
      )
      .run(Date.now() - 1_800_000, Date.now() - 1);

    const response = await createManualInstall(new Request("https://scout.example.test/systems"), {
      params: Promise.resolve({ systemId: "system-1" }),
    });

    expect(response.status).toBe(409);
    expect(await response.json()).toEqual({
      error: { message: "Manual installation requires current open SSH evidence." },
    });
  });
});
