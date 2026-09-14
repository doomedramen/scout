import { afterEach, describe, expect, it } from "vitest";
import { generateKeyPairSync, sign } from "node:crypto";

import { closeDatabase, getDatabase } from "@/lib/server/db";
import { enrollmentMessage } from "@/lib/server/agent-protocol";
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
    const input = enrollmentInput(invitation.token);

    const first = enrollAgent(input, 2_000);
    const retry = enrollAgent(input, 3_000);

    expect(retry).toMatchObject({ id: first.id, systemId: "system-1", existing: true });
    expect(sqlite.prepare("SELECT COUNT(*) AS count FROM agent").get()).toEqual({ count: 1 });
    const other = enrollmentInput(invitation.token);
    expect(() => enrollAgent(other, 3_000)).toThrow(/bound|another/i);
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
    expect(() => enrollAgent(enrollmentInput(invitation.token), invitation.expiresAt)).toThrow(
      /expired/i,
    );
  });

  it("rejects enrollment when proof does not match the advertised public key", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    sqlite
      .prepare(
        "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES (?, ?, 'installing', ?, ?)",
      )
      .run("system-1", "192.0.2.10", 1_000, 1_000);
    const invitation = createAgentInvitation("system-1", 1_000);
    const input = enrollmentInput(invitation.token);
    const other = generateKeyPairSync("ed25519");
    const otherPublicKey = other.publicKey
      .export({ format: "der", type: "spki" })
      .subarray(-32)
      .toString("base64url");

    expect(() =>
      enrollAgent(
        {
          ...input,
          publicKey: otherPublicKey,
        },
        2_000,
      ),
    ).toThrow(/proof|private key/i);
  });
});

function enrollmentInput(invitation: string) {
  const keys = generateKeyPairSync("ed25519");
  const publicKey = keys.publicKey
    .export({ format: "der", type: "spki" })
    .subarray(-32)
    .toString("base64url");
  const input = {
    invitation,
    publicKey,
    platform: "linux" as const,
    architecture: "x86_64" as const,
    version: "0.1.0",
  };
  return {
    ...input,
    proof: sign(null, Buffer.from(enrollmentMessage(input), "utf8"), keys.privateKey).toString(
      "base64url",
    ),
  };
}
