import { z } from "zod";

import { jsonError, sameOrigin } from "@/lib/server/http";
import { requireApiSession } from "@/lib/server/session";
import { getSettings, setAuthorityPaused } from "@/lib/server/settings";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const settingsInput = z.object({ authorityPaused: z.boolean() });

export async function GET(request: Request) {
  const session = await requireApiSession(request);
  if (session instanceof Response) return session;
  return Response.json(getSettings(), { headers: { "Cache-Control": "no-store" } });
}

export async function PUT(request: Request) {
  const session = await requireApiSession(request);
  if (session instanceof Response) return session;
  if (!sameOrigin(request)) return jsonError("Request origin is not allowed.", 403);
  try {
    const input = settingsInput.parse(await request.json());
    return Response.json(setAuthorityPaused(input.authorityPaused), {
      headers: { "Cache-Control": "no-store" },
    });
  } catch {
    return jsonError("The settings payload is invalid.", 400);
  }
}
