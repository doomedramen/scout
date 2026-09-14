import { z } from "zod";

import { decommissionSystem, LifecycleError, retryEnrollment } from "@/lib/server/lifecycle";
import { jsonError, sameOrigin } from "@/lib/server/http";
import { requireApiSession } from "@/lib/server/session";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const actionInput = z.enum(["retry", "decommission"]);

export async function POST(
  request: Request,
  { params }: { params: Promise<{ systemId: string; action: string }> },
) {
  const session = await requireApiSession(request);
  if (session instanceof Response) return session;
  if (!sameOrigin(request)) return jsonError("Request origin is not allowed.", 403);
  const { systemId, action: rawAction } = await params;
  const action = actionInput.safeParse(rawAction);
  if (!action.success) return jsonError("The requested system action is invalid.", 404);
  try {
    const result =
      action.data === "retry" ? retryEnrollment(systemId) : await decommissionSystem(systemId);
    return Response.json(result, { headers: { "Cache-Control": "no-store" } });
  } catch (error) {
    if (error instanceof LifecycleError)
      return jsonError(error.message, error.code === "not-found" ? 404 : 409);
    return jsonError("The system action could not be completed.", 503);
  }
}
