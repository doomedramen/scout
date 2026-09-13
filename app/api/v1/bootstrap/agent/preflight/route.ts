import { authorizeAgentInvitation } from "@/lib/server/invitations";
import { jsonError } from "@/lib/server/http";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const invitation =
    request.headers.get("x-scout-oti") ?? new URL(request.url).searchParams.get("oti");
  if (!invitation || !authorizeAgentInvitation(invitation))
    return jsonError("The agent invitation is invalid or expired.", 401);
  return Response.json(
    { ok: true, serverTime: new Date().toISOString() },
    { headers: { "Cache-Control": "no-store" } },
  );
}
