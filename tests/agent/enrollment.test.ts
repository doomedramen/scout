import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { createAgentInvitation, enrollAgent } from "@/lib/server/invitations";

describe("agent enrollment", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    closeDatabase();
  });

  it("binds one invitation to one durable key and returns the existing agent on retry", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES (?, ?, 'installing', ?, ?)",
      )
      .run("system-1", "192.0.2.10", 1_000, 1_000);
    const invitation = createAgentInvitation("system-1", 1_000);
    const input = {
      invitation: invitation.token,
      publicKey: "a".repeat(43),
      platform: "linux" as const,
      architecture: "x86_64" as const,
      version: "0.1.0",
    };

    const first = enrollAgent(input, 2_000);
    const retry = enrollAgent(input, 3_000);

    expect(retry).toMatchObject({ id: first.id, systemId: "system-1", existing: true });
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM agent").get()).toEqual({ count: 1 });
    expect(() => enrollAgent({ ...input, publicKey: "b".repeat(43) }, 3_000)).toThrow(
      /bound|another/i,
    );
  });

  it("rejects an expired invitation", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES (?, ?, 'installing', ?, ?)",
      )
      .run("system-1", "192.0.2.10", 1_000, 1_000);
    const invitation = createAgentInvitation("system-1", 1_000);
    expect(() =>
      enrollAgent(
        {
          invitation: invitation.token,
          publicKey: "a".repeat(43),
          platform: "linux",
          architecture: "x86_64",
          version: "0.1.0",
        },
        invitation.expiresAt,
      ),
    ).toThrow(/expired/i);
  });
});
