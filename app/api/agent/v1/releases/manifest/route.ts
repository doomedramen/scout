import { authenticateAgentRequest } from "@/lib/server/agent-protocol";
import { jsonError } from "@/lib/server/http";
import { readVerifiedReleaseManifest } from "@/lib/server/releases";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const identity = await authenticateAgentRequest(request, await request.text());
  if (identity instanceof Response) return identity;
  try {
    return Response.json(readVerifiedReleaseManifest(), {
      headers: { "Cache-Control": "no-store" },
    });
  } catch {
    return jsonError("No verified agent release manifest is available.", 503);
  }
}
