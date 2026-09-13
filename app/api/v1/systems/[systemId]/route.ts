import { notFound } from "next/navigation";

import { requireApiSession } from "@/lib/server/session";
import { getSystemDetails } from "@/lib/server/systems";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request: Request, { params }: { params: Promise<{ systemId: string }> }) {
  const session = await requireApiSession(request);
  if (session instanceof Response) return session;
  const { systemId } = await params;
  const system = getSystemDetails(systemId);
  if (!system) return notFound();
  return Response.json(system, { headers: { "Cache-Control": "no-store" } });
}
