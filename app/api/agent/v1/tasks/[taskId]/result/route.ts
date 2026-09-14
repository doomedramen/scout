import { authenticateAgentRequest } from "@/lib/server/agent-protocol";
import { jsonError } from "@/lib/server/http";
import {
  completeRelayTask,
  completeScanTask,
  relayTaskResultInput,
  scanTaskResultInput,
} from "@/lib/server/tasks";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(request: Request, { params }: { params: Promise<{ taskId: string }> }) {
  const body = await request.text();
  const identity = await authenticateAgentRequest(request, body);
  if (identity instanceof Response) return identity;

  let input;
  try {
    const raw = { ...JSON.parse(body), taskId: (await params).taskId };
    input =
      raw.kind === "relay-connect"
        ? relayTaskResultInput.parse(raw)
        : scanTaskResultInput.parse(raw);
  } catch {
    return jsonError("The scanner task result is invalid.", 400);
  }
  if (input.agentId !== identity.agentId)
    return jsonError("Agent identity does not match the task result.", 401);

  const result = "kind" in input ? completeRelayTask(input) : completeScanTask(input);
  if (!result.accepted) {
    const status =
      result.reason === "unknown" ? 404 : result.reason === "already-complete" ? 409 : 412;
    return jsonError(`Scanner task was not accepted: ${result.reason}.`, status);
  }
  return Response.json({ ok: true }, { headers: { "Cache-Control": "no-store" } });
}
