import { enrollInput } from "@/lib/server/agent-protocol";
import { EnrollmentError, enrollAgent } from "@/lib/server/invitations";
import { jsonError, rateLimit, readRequestBody } from "@/lib/server/http";
import { controlPublicKey } from "@/lib/server/tasks";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const limited = rateLimit(request, "agent-enroll", 20, 60_000);
  if (limited) return limited;
  const body = await readRequestBody(request, 32 * 1024);
  if (body instanceof Response) return body;
  let input;
  try {
    input = enrollInput.parse(JSON.parse(body));
  } catch {
    return jsonError("Agent enrollment payload is invalid.", 400);
  }

  try {
    const agent = enrollAgent(input);
    return Response.json(
      {
        agentId: agent.id,
        systemId: agent.systemId,
        serverTime: new Date().toISOString(),
        heartbeatIntervalSeconds: 15,
        telemetryIntervalSeconds: 30,
        controlPublicKey: controlPublicKey(),
      },
      { headers: { "Cache-Control": "no-store" } },
    );
  } catch (error) {
    if (error instanceof EnrollmentError)
      return jsonError(error.message, error.code === "invalid_invitation" ? 401 : 409);
    return jsonError("Agent enrollment failed.", 503);
  }
}
