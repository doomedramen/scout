import { authenticateAgentRequest } from "@/lib/server/agent-protocol";
import { jsonError } from "@/lib/server/http";
import { openRelayChannel, RelayError } from "@/lib/server/relay";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const body = await request.text();
  const identity = await authenticateAgentRequest(request, body);
  if (identity instanceof Response) return identity;

  let input: unknown;
  try {
    input = JSON.parse(body);
  } catch {
    return jsonError("Relay channel parameters are invalid.", 400);
  }
  if (
    !input ||
    typeof input !== "object" ||
    !("agentId" in input) ||
    input.agentId !== identity.agentId
  ) {
    return jsonError("Relay agent identity does not match the request.", 401);
  }

  try {
    const receipt = openRelayChannel(input);
    return Response.json(receipt, {
      status: receipt.created ? 201 : 200,
      headers: { "Cache-Control": "no-store" },
    });
  } catch (error) {
    return relayErrorResponse(error);
  }
}

function relayErrorResponse(error: unknown): Response {
  if (!(error instanceof RelayError))
    return jsonError("Scout could not open the relay channel.", 503);
  const status =
    error.code === "invalid"
      ? 400
      : error.code === "unauthorized"
        ? 401
        : error.code === "expired"
          ? 410
          : 409;
  return jsonError(error.message, status);
}
