import { authenticateAgentRequest } from "@/lib/server/agent-protocol";
import { jsonError } from "@/lib/server/http";
import { authorizeRelayStream, RelayError } from "@/lib/server/relay";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request: Request, { params }: { params: Promise<{ relayId: string }> }) {
  const identity = await authenticateAgentRequest(request, "");
  if (identity instanceof Response) return identity;

  try {
    const channel = authorizeRelayStream(
      (await params).relayId,
      "downstream",
      identity.agentId,
      request.headers.get("x-scout-relay-signature") ?? "",
    );
    return new Response(channel.agentReadable, {
      status: 200,
      headers: {
        "Cache-Control": "no-store",
        "Content-Type": "application/octet-stream",
      },
    });
  } catch (error) {
    return streamErrorResponse(error);
  }
}

function streamErrorResponse(error: unknown): Response {
  if (!(error instanceof RelayError)) return jsonError("The relay stream failed.", 502);
  const status = error.code === "unauthorized" ? 401 : error.code === "expired" ? 410 : 409;
  return jsonError(error.message, status);
}
