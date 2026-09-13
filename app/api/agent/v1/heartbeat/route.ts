import { heartbeatInput, authenticateAgentRequest } from "@/lib/server/agent-protocol";
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
    input = heartbeatInput.parse(JSON.parse(body));
  } catch {
    return jsonError("Agent heartbeat payload is invalid.", 400);
  }
  if (input.agentId !== identity.agentId)
    return jsonError("Agent identity does not match the request.", 401);

  const now = Date.now();
  const { sqlite } = getDatabase();
  const changed = sqlite
    .prepare(
      "UPDATE agent SET last_heartbeat_at = ?, task_generation = MAX(task_generation, ?), release_sequence = MAX(release_sequence, ?), updated_at = ? WHERE id = ? AND revoked_at IS NULL",
    )
    .run(
      Date.parse(input.observedAt),
      input.taskGeneration,
      input.releaseSequence,
      now,
      identity.agentId,
    );
  if (changed.changes !== 1) return jsonError("Agent identity is not authorized.", 401);
  return Response.json(
    { ok: true, serverTime: new Date(now).toISOString() },
    { headers: { "Cache-Control": "no-store" } },
  );
}
