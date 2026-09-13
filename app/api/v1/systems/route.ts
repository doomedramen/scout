import { jsonError } from "@/lib/server/http";
import { requireApiSession } from "@/lib/server/session";
import { getFleetSnapshot } from "@/lib/server/systems";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const session = await requireApiSession(request);
  if (session instanceof Response) return session;

  try {
    return Response.json(getFleetSnapshot(), { headers: { "Cache-Control": "no-store" } });
  } catch {
    return jsonError("Systems are temporarily unavailable.", 503);
  }
}
