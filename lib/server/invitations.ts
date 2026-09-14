import { createHash, createPublicKey, randomBytes, randomUUID, verify } from "node:crypto";

import { getDatabase } from "@/lib/server/db";
import { enrollmentMessage, type EnrollmentInput } from "@/lib/server/agent-protocol";

const INVITATION_TTL_MS = 10 * 60 * 1000;

function hash(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}

export type AgentInvitation = { id: string; systemId: string; token: string; expiresAt: number };

export type InvitationAuthorization = {
  id: string;
  systemId: string;
  expiresAt: number;
  consumedAt: number | null;
};

export function createAgentInvitation(systemId: string, now = Date.now()): AgentInvitation {
  const token = randomBytes(32).toString("hex");
  const id = randomUUID();
  const expiresAt = now + INVITATION_TTL_MS;
  const { sqlite } = getDatabase();
  sqlite
    .prepare(
      "INSERT INTO agent_invitation (id, system_id, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?)",
    )
    .run(id, systemId, hash(token), expiresAt, now);
  return { id, systemId, token, expiresAt };
}

export function authorizeAgentInvitation(
  token: string,
  now = Date.now(),
): InvitationAuthorization | null {
  const { sqlite } = getDatabase();
  const invitation = sqlite
    .prepare(
      "SELECT id, system_id AS systemId, expires_at AS expiresAt, consumed_at AS consumedAt FROM agent_invitation WHERE token_hash = ? AND expires_at > ? LIMIT 1",
    )
    .get(hash(token), now) as InvitationAuthorization | undefined;
  return invitation ?? null;
}

export type EnrollAgentInput = {
  invitation: string;
  publicKey: string;
  platform: "linux" | "macos";
  architecture: "x86_64" | "aarch64";
  version: string;
  proof: string;
};

export class EnrollmentError extends Error {
  constructor(
    message: string,
    public readonly code:
      "invalid_invitation" | "key_conflict" | "agent_conflict" | "invalid_input",
  ) {
    super(message);
  }
}

export type EnrolledAgent = {
  id: string;
  systemId: string;
  platform: string;
  architecture: string;
  version: string;
  existing: boolean;
};

export function enrollAgent(input: EnrollAgentInput, now = Date.now()): EnrolledAgent {
  if (!validEnrollmentProof(input))
    throw new EnrollmentError(
      "The agent did not prove possession of its private key.",
      "invalid_input",
    );
  const { sqlite } = getDatabase();
  const enroll = sqlite.transaction(() => {
    const invitation = sqlite
      .prepare(
        "SELECT id, system_id AS systemId, public_key AS publicKey, expires_at AS expiresAt, consumed_at AS consumedAt FROM agent_invitation WHERE token_hash = ? LIMIT 1",
      )
      .get(hash(input.invitation)) as
      | {
          id: string;
          systemId: string;
          publicKey: string | null;
          expiresAt: number;
          consumedAt: number | null;
        }
      | undefined;
    if (!invitation || invitation.expiresAt <= now)
      throw new EnrollmentError(
        "The agent invitation is invalid or expired.",
        "invalid_invitation",
      );
    if (invitation.publicKey && invitation.publicKey !== input.publicKey) {
      throw new EnrollmentError(
        "The invitation is already bound to another agent key.",
        "key_conflict",
      );
    }

    const existingKey = sqlite
      .prepare(
        "SELECT id, system_id AS systemId, platform, architecture, version FROM agent WHERE public_key = ? LIMIT 1",
      )
      .get(input.publicKey) as
      | { id: string; systemId: string; platform: string; architecture: string; version: string }
      | undefined;
    if (existingKey && existingKey.systemId !== invitation.systemId) {
      throw new EnrollmentError(
        "This agent key is already enrolled on another system.",
        "key_conflict",
      );
    }
    if (existingKey) {
      sqlite
        .prepare("UPDATE agent_invitation SET public_key = ?, consumed_at = ? WHERE id = ?")
        .run(input.publicKey, now, invitation.id);
      return { ...existingKey, existing: true };
    }

    const active = sqlite
      .prepare("SELECT id FROM agent WHERE system_id = ? AND revoked_at IS NULL LIMIT 1")
      .get(invitation.systemId) as { id: string } | undefined;
    if (active)
      throw new EnrollmentError("This system already has an active agent.", "agent_conflict");

    const id = randomUUID();
    sqlite
      .prepare(
        "INSERT INTO agent (id, system_id, public_key, platform, architecture, version, last_heartbeat_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
      )
      .run(
        id,
        invitation.systemId,
        input.publicKey,
        input.platform,
        input.architecture,
        input.version,
        now,
        now,
        now,
      );
    sqlite
      .prepare("UPDATE agent_invitation SET public_key = ?, consumed_at = ? WHERE id = ?")
      .run(input.publicKey, now, invitation.id);
    sqlite
      .prepare("UPDATE system SET status = 'installing', updated_at = ? WHERE id = ?")
      .run(now, invitation.systemId);
    return {
      id,
      systemId: invitation.systemId,
      platform: input.platform,
      architecture: input.architecture,
      version: input.version,
      existing: false,
    };
  });
  return enroll();
}

function validEnrollmentProof(input: EnrollAgentInput): boolean {
  try {
    const rawPublicKey = Buffer.from(input.publicKey, "base64url");
    if (rawPublicKey.length !== 32) return false;
    const publicKey = createPublicKey({
      key: Buffer.concat([Buffer.from("302a300506032b6570032100", "hex"), rawPublicKey]),
      format: "der",
      type: "spki",
    });
    return verify(
      null,
      Buffer.from(enrollmentMessage(input as EnrollmentInput), "utf8"),
      publicKey,
      Buffer.from(input.proof, "base64url"),
    );
  } catch {
    return false;
  }
}
