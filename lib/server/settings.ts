import { getDatabase } from "@/lib/server/db";

export type ScoutSettings = {
  authorityPaused: boolean;
  releaseChannel: string;
  twoFactorEnabled: boolean;
};

function readSetting(key: string): string | null {
  const { sqlite } = getDatabase();
  return (
    (
      sqlite.prepare("SELECT value FROM app_setting WHERE key = ?").get(key) as
        { value: string } | undefined
    )?.value ?? null
  );
}

function writeSetting(key: string, value: string, now = Date.now()): void {
  const { sqlite } = getDatabase();
  sqlite
    .prepare(
      "INSERT INTO app_setting (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at",
    )
    .run(key, value, now);
}

export function authorityPaused(): boolean {
  return readSetting("authority_paused") === "true";
}

export function getSettings(userId?: string): ScoutSettings {
  const { sqlite } = getDatabase();
  const user = sqlite
    .prepare(
      "SELECT two_factor_enabled AS twoFactorEnabled FROM user WHERE (? IS NULL OR id = ?) ORDER BY created_at LIMIT 1",
    )
    .get(userId ?? null, userId ?? null) as { twoFactorEnabled: number } | undefined;
  return {
    authorityPaused: authorityPaused(),
    releaseChannel: readSetting("release_channel") ?? process.env.SCOUT_RELEASE_CHANNEL ?? "stable",
    twoFactorEnabled: user?.twoFactorEnabled === 1,
  };
}

export function setAuthorityPaused(paused: boolean, now = Date.now()): ScoutSettings {
  writeSetting("authority_paused", String(paused), now);
  return getSettings();
}
