import { authenticateAgentRequest } from "@/lib/server/agent-protocol";
import { jsonError } from "@/lib/server/http";
import { authorizeRelayStream, pipeRelayUpstream, RelayError } from "@/lib/server/relay";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(request: Request, { params }: { params: Promise<{ relayId: string }> }) {
  const body = "";
  const identity = await authenticateAgentRequest(request, body);
  if (identity instanceof Response) return identity;
  if (!request.body) return jsonError("Relay upstream stream has no body.", 400);

  try {
    const channel = authorizeRelayStream(
      (await params).relayId,
      "upstream",
      identity.agentId,
      request.headers.get("x-scout-relay-signature") ?? "",
    );
    await pipeRelayUpstream(channel, request.body);
    return new Response(null, { status: 202, headers: { "Cache-Control": "no-store" } });
  } catch (error) {
    return streamErrorResponse(error);
  }
}

function streamErrorResponse(error: unknown): Response {
  if (!(error instanceof RelayError)) return jsonError("The relay stream failed.", 502);
  const status = error.code === "unauthorized" ? 401 : error.code === "expired" ? 410 : 409;
  return jsonError(error.message, status);
}
