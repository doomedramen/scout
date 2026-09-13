import { createHash, randomBytes, randomUUID, timingSafeEqual } from "node:crypto";

import { isNull } from "drizzle-orm";

import { setupTokens, user } from "@/db/schema";
import { getDatabase } from "@/lib/server/db";

const OWNER_EMAIL = "owner@scout.internal";
const TOKEN_TTL_MS = 30 * 60 * 1000;

export type SetupStatus = {
  required: boolean;
  tokenExpiresAt: number | null;
};

function hashToken(token: string): string {
  return createHash("sha256").update(token).digest("hex");
}

export function setupOwnerEmail(): string {
  return OWNER_EMAIL;
}

export function setupStatus(now = Date.now()): SetupStatus {
  const { sqlite } = getDatabase();
  const owner = sqlite.prepare("SELECT 1 FROM user LIMIT 1").get();
  const token = sqlite
    .prepare(
      "SELECT expires_at AS expiresAt FROM setup_token WHERE consumed_at IS NULL AND expires_at > ? ORDER BY created_at DESC LIMIT 1",
    )
    .get(now) as { expiresAt: number } | undefined;
  return { required: !owner, tokenExpiresAt: token?.expiresAt ?? null };
}

export function provisionSetupToken(now = Date.now()): { token: string; expiresAt: number } {
  const { db } = getDatabase();
  const token = randomBytes(24).toString("hex");
  const expiresAt = now + TOKEN_TTL_MS;
  db.insert(setupTokens)
    .values({
      id: randomUUID(),
      tokenHash: hashToken(token),
      expiresAt: new Date(expiresAt),
      createdAt: new Date(now),
    })
    .run();
  return { token, expiresAt };
}

export function ensureSetupToken(now = Date.now()): SetupStatus {
  const status = setupStatus(now);
  if (status.required && !status.tokenExpiresAt) {
    const created = provisionSetupToken(now);
    console.warn(
      `Scout owner setup token (expires ${new Date(created.expiresAt).toISOString()}): ${created.token}`,
    );
    return { required: true, tokenExpiresAt: created.expiresAt };
  }
  return status;
}

export function consumeSetupToken(token: string, now = Date.now()): boolean {
  const candidate = Buffer.from(hashToken(token), "utf8");
  const { sqlite } = getDatabase();
  const row = sqlite
    .prepare(
      "SELECT id, token_hash AS tokenHash FROM setup_token WHERE consumed_at IS NULL AND expires_at > ? ORDER BY created_at DESC LIMIT 1",
    )
    .get(now) as { id: string; tokenHash: string } | undefined;
  if (!row) return false;

  const actual = Buffer.from(row.tokenHash, "utf8");
  if (candidate.length !== actual.length || !timingSafeEqual(candidate, actual)) return false;

  const changed = sqlite
    .prepare("UPDATE setup_token SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL")
    .run(now, row.id);
  return changed.changes === 1;
}

export function isSetupTokenValid(token: string, now = Date.now()): boolean {
  const candidate = Buffer.from(hashToken(token), "utf8");
  const { sqlite } = getDatabase();
  const row = sqlite
    .prepare(
      "SELECT token_hash AS tokenHash FROM setup_token WHERE consumed_at IS NULL AND expires_at > ? ORDER BY created_at DESC LIMIT 1",
    )
    .get(now) as { tokenHash: string } | undefined;
  if (!row) return false;

  const actual = Buffer.from(row.tokenHash, "utf8");
  return candidate.length === actual.length && timingSafeEqual(candidate, actual);
}

export function ownerExists(): boolean {
  const { db } = getDatabase();
  return Boolean(db.select({ id: user.id }).from(user).limit(1).get());
}

export function expireUnusedTokens(): void {
  const { db } = getDatabase();
  db.delete(setupTokens).where(isNull(setupTokens.consumedAt)).run();
}
