import { authenticateAgentRequest, telemetryInput } from "@/lib/server/agent-protocol";
import { getDatabase } from "@/lib/server/db";
import { jsonError } from "@/lib/server/http";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const body = await request.text();
  const identity = await authenticateAgentRequest(request, body);
  if (identity instanceof Response) return identity;

  let input;
  try {
    input = telemetryInput.parse(JSON.parse(body));
  } catch {
    return jsonError("Agent telemetry payload is invalid.", 400);
  }
  if (input.agentId !== identity.agentId)
    return jsonError("Agent identity does not match the request.", 401);

  const observedAt = Date.parse(input.observedAt);
  if (!Number.isFinite(observedAt)) return jsonError("Agent telemetry timestamp is invalid.", 400);
  const now = Date.now();
  const { sqlite } = getDatabase();
  const store = sqlite.transaction(() => {
    const inserted = sqlite
      .prepare(
        "INSERT OR IGNORE INTO telemetry_sample (id, agent_id, batch_id, observed_at, payload, received_at) VALUES (?, ?, ?, ?, ?, ?)",
      )
      .run(
        crypto.randomUUID(),
        identity.agentId,
        input.batchId,
        observedAt,
        JSON.stringify(input),
        now,
      );
    if (inserted.changes === 1) {
      sqlite
        .prepare(
          "UPDATE agent SET last_telemetry_at = MAX(COALESCE(last_telemetry_at, 0), ?), updated_at = ? WHERE id = ? AND revoked_at IS NULL",
        )
        .run(observedAt, now, identity.agentId);
      sqlite
        .prepare(
          "UPDATE system SET hostname = COALESCE(?, hostname), status = 'online', updated_at = ? WHERE id = (SELECT system_id FROM agent WHERE id = ?)",
        )
        .run(input.host.hostname, now, identity.agentId);
    }
    return inserted.changes;
  });
  store();
  return Response.json(
    { ok: true, serverTime: new Date(now).toISOString() },
    { headers: { "Cache-Control": "no-store" } },
  );
}
