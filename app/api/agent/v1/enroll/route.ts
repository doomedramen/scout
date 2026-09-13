import { enrollInput } from "@/lib/server/agent-protocol";
import { EnrollmentError, enrollAgent } from "@/lib/server/invitations";
import { jsonError } from "@/lib/server/http";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  let input;
  try {
    input = enrollInput.parse(await request.json());
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
      },
      { headers: { "Cache-Control": "no-store" } },
    );
  } catch (error) {
    if (error instanceof EnrollmentError)
      return jsonError(error.message, error.code === "invalid_invitation" ? 401 : 409);
    return jsonError("Agent enrollment failed.", 503);
  }
}
