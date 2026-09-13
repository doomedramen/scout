import { AccessError, preflightSsh } from "@/lib/server/access";
import { jsonError } from "@/lib/server/http";
import { requireApiSession } from "@/lib/server/session";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

function errorResponse(error: unknown): Response {
  if (error instanceof AccessError) {
    const status = error.code === "not_found" ? 404 : 409;
    return jsonError(error.message, status);
  }
  return jsonError("Scout could not read the SSH host fingerprint yet.", 502);
}

export async function GET(request: Request, { params }: { params: Promise<{ systemId: string }> }) {
  const session = await requireApiSession(request);
  if (session instanceof Response) return session;
  const { systemId } = await params;
  try {
    const result = await preflightSsh(systemId);
    return Response.json(
      {
        method: "ssh",
        target: `${result.endpoint.address}:${result.endpoint.port}`,
        fingerprint: result.fingerprint,
        trustedFingerprint: result.trustedFingerprint,
        trusted: result.trusted,
      },
      { headers: { "Cache-Control": "no-store" } },
    );
  } catch (error) {
    return errorResponse(error);
  }
}
