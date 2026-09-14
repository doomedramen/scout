import { authorityPaused } from "@/lib/server/settings";
import { getDatabase } from "@/lib/server/db";
import {
  createSignedScanTask,
  getPendingTaskEnvelope,
  type TaskEnvelope,
} from "@/lib/server/tasks";

const HEARTBEAT_TIMEOUT_MS = 45_000;

export type ScannerSelection = {
  agentId: string;
  heartbeatAt: number;
};

/** Select exactly one healthy agent, with stable tie-breaking for failover. */
export function electScannerAgent(segmentId: string, now = Date.now()): ScannerSelection | null {
  const { sqlite } = getDatabase();
  const row = sqlite
    .prepare(
      `
        SELECT a.id AS agentId, a.last_heartbeat_at AS heartbeatAt
        FROM agent a
        INNER JOIN system s ON s.id = a.system_id
        WHERE s.segment_id = ?
          AND a.revoked_at IS NULL
          AND a.last_heartbeat_at IS NOT NULL
          AND a.last_heartbeat_at > ?
        ORDER BY a.last_heartbeat_at DESC, a.id ASC
        LIMIT 1
      `,
    )
    .get(segmentId, now - HEARTBEAT_TIMEOUT_MS) as ScannerSelection | undefined;
  return row ?? null;
}

export function ensureSegmentScanTask(
  segmentId: string,
  now = Date.now(),
): { scanner: ScannerSelection; envelope: TaskEnvelope } | null {
  if (authorityPaused()) return null;
  const { sqlite } = getDatabase();
  const segment = sqlite
    .prepare("SELECT paused FROM network_segment WHERE id = ?")
    .get(segmentId) as { paused: number } | undefined;
  if (!segment || segment.paused === 1) return null;
  const scanner = electScannerAgent(segmentId, now);
  if (!scanner) return null;

  const pending = sqlite
    .prepare(
      "SELECT id FROM scan_task WHERE segment_id = ? AND scanner_agent_id = ? AND status = 'leased' AND lease_expires_at > ? ORDER BY created_at DESC LIMIT 1",
    )
    .get(segmentId, scanner.agentId, now) as { id: string } | undefined;
  if (pending) {
    const envelope = getPendingTaskForSegment(pending.id, now);
    if (envelope) return { scanner, envelope };
  }

  const task = createSignedScanTask(segmentId, scanner.agentId, now);
  return { scanner, envelope: task.envelope };
}

function getPendingTaskForSegment(taskId: string, now: number): TaskEnvelope | null {
  const { sqlite } = getDatabase();
  const row = sqlite
    .prepare(
      "SELECT scanner_agent_id AS agentId FROM scan_task WHERE id = ? AND status = 'leased' AND lease_expires_at > ?",
    )
    .get(taskId, now) as { agentId: string | null } | undefined;
  if (!row?.agentId) return null;

  return getPendingTaskEnvelope(row.agentId, now);
}
