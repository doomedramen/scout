import { getDatabase } from "@/lib/server/db";
import { authSecret, controlSigningKey, credentialKey } from "@/lib/server/keys";
import { requestDiscovery } from "@/lib/server/discovery";
import { processNextEnrollmentJob } from "@/lib/server/enrollment";
import { authorityPaused } from "@/lib/server/settings";
import { repairDuplicateSystems } from "@/lib/server/system-identity";
import { runTelemetryRetention } from "@/lib/server/telemetry";

const HEARTBEAT_INTERVAL_MS = 15_000;
const LEASE_RECOVERY_INTERVAL_MS = 30_000;

let heartbeatTimer: NodeJS.Timeout | undefined;
let started = false;
let enrollmentRun: Promise<unknown> | undefined;
let lastRetentionAt = 0;

function writeHeartbeat(now = Date.now()): void {
  const { sqlite } = getDatabase();
  sqlite
    .prepare(
      `
        INSERT INTO app_setting (key, value, updated_at) VALUES ('scheduler_heartbeat', ?, ?)
        ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
      `,
    )
    .run(String(now), now);
}

function requestEnrollmentWork(): void {
  if (enrollmentRun || authorityPaused()) return;
  enrollmentRun = processNextEnrollmentJob()
    .catch((error: unknown) => {
      console.error("Scout enrollment worker failed", error);
    })
    .finally(() => {
      enrollmentRun = undefined;
    });
}

export function recoverExpiredLeases(now = Date.now()): void {
  const { sqlite } = getDatabase();
  sqlite
    .prepare(
      "UPDATE enrollment_job SET lease_owner = NULL, lease_expires_at = NULL, status = CASE WHEN status = 'running' THEN 'queued' ELSE status END, attempt = attempt + 1, updated_at = ? WHERE lease_expires_at IS NOT NULL AND lease_expires_at <= ?",
    )
    .run(now, now);
  sqlite
    .prepare(
      "UPDATE scan_task SET scanner_agent_id = NULL, lease_expires_at = NULL, status = CASE WHEN status = 'leased' THEN 'queued' ELSE status END WHERE lease_expires_at IS NOT NULL AND lease_expires_at <= ?",
    )
    .run(now);
}

export function startRuntime(): void {
  if (started) return;

  // Validate all persistent prerequisites before the listener reports ready.
  getDatabase();
  authSecret();
  credentialKey();
  controlSigningKey();
  repairDuplicateSystems(getDatabase().sqlite);
  recoverExpiredLeases();
  writeHeartbeat();
  requestDiscovery();
  requestEnrollmentWork();

  heartbeatTimer = setInterval(
    () => {
      writeHeartbeat();
      recoverExpiredLeases();
      requestDiscovery();
      requestEnrollmentWork();
      if (Date.now() - lastRetentionAt >= 60 * 60 * 1_000) {
        runTelemetryRetention();
        lastRetentionAt = Date.now();
      }
    },
    Math.min(HEARTBEAT_INTERVAL_MS, LEASE_RECOVERY_INTERVAL_MS),
  );
  heartbeatTimer.unref();
  started = true;
}

export function stopRuntime(): void {
  if (heartbeatTimer) clearInterval(heartbeatTimer);
  heartbeatTimer = undefined;
  enrollmentRun = undefined;
  started = false;
}

export function readiness(): { ok: boolean; checks: Record<string, boolean> } {
  try {
    const { sqlite } = getDatabase();
    const migration = sqlite.prepare("SELECT 1 FROM schema_migration WHERE version = 1").get();
    const heartbeat = sqlite
      .prepare("SELECT value FROM app_setting WHERE key = 'scheduler_heartbeat'")
      .get() as { value: string } | undefined;
    const heartbeatFresh = heartbeat
      ? Number(heartbeat.value) > Date.now() - HEARTBEAT_INTERVAL_MS * 4
      : false;
    const checks = {
      database: true,
      migrations: Boolean(migration),
      keys: false,
      scheduler: heartbeatFresh,
    };
    authSecret();
    credentialKey();
    controlSigningKey();
    checks.keys = true;
    return { ok: Object.values(checks).every(Boolean), checks };
  } catch {
    return {
      ok: false,
      checks: { database: false, migrations: false, keys: false, scheduler: false },
    };
  }
}
