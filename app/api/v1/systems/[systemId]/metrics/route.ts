import { requireApiSession } from "@/lib/server/session";
import { getSystemMetrics } from "@/lib/server/systems";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request: Request, { params }: { params: Promise<{ systemId: string }> }) {
  const session = await requireApiSession(request);
  if (session instanceof Response) return session;
  const { systemId } = await params;
  return Response.json(getSystemMetrics(systemId), { headers: { "Cache-Control": "no-store" } });
}
