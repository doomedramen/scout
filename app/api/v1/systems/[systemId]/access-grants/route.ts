import { z } from "zod";

import {
  AccessError,
  createAccessGrant,
  getAccessGrantReceipt,
  preflightSsh,
} from "@/lib/server/access";
import { jsonError, rateLimit, readRequestBody, sameOrigin } from "@/lib/server/http";
import { requireApiSession } from "@/lib/server/session";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const grantInput = z.object({
  method: z.literal("ssh"),
  username: z.string().min(1).max(128),
  authType: z.enum(["password", "private-key"]),
  secret: z.string().min(1).max(128_000),
  passphrase: z.string().max(1_000).nullable().optional(),
  privilegePassword: z.string().min(1).max(1_000).nullable().optional(),
  fingerprint: z.string().min(8).max(256),
  trust: z.boolean(),
  scope: z.literal("exact-host").optional(),
});

function accessError(error: unknown): Response {
  if (error instanceof AccessError) {
    const status = error.code === "not_found" ? 404 : error.code === "invalid" ? 400 : 409;
    return jsonError(error.message, status);
  }
  return jsonError("Scout could not queue this installation.", 503);
}

export async function POST(
  request: Request,
  { params }: { params: Promise<{ systemId: string }> },
) {
  const session = await requireApiSession(request);
  if (session instanceof Response) return session;
  if (!sameOrigin(request)) return jsonError("Request origin is not allowed.", 403);
  const limited = rateLimit(request, `access-grant:${session.user.id}`, 30, 60_000);
  if (limited) return limited;

  const idempotencyKey = request.headers.get("idempotency-key");
  if (!idempotencyKey || idempotencyKey.length > 128)
    return jsonError("An idempotency key is required.", 400);

  const { systemId } = await params;
  const existing = getAccessGrantReceipt(idempotencyKey, session.user.id, systemId);
  if (existing) {
    return Response.json(
      { ...existing, target: null, jobUrl: `/api/v1/enrollment-jobs/${existing.jobId}` },
      { status: 202, headers: { "Cache-Control": "no-store" } },
    );
  }

  let input: z.infer<typeof grantInput>;
  const body = await readRequestBody(request, 256 * 1024);
  if (body instanceof Response) return body;
  try {
    input = grantInput.parse(JSON.parse(body));
  } catch {
    return jsonError("Enter the SSH username, credential, and confirmed fingerprint.", 400);
  }

  try {
    const current = await preflightSsh(systemId);
    if (current.fingerprint !== input.fingerprint)
      return jsonError("The SSH host fingerprint changed. Review it before continuing.", 409);
    const job = createAccessGrant(
      {
        systemId,
        ownerId: session.user.id,
        ...input,
        passphrase: input.passphrase ?? null,
        privilegePassword: input.privilegePassword ?? null,
        idempotencyKey,
      },
      Date.now(),
    );
    return Response.json(
      {
        ...job,
        target: `${current.endpoint.address}:${current.endpoint.port}`,
        jobUrl: `/api/v1/enrollment-jobs/${job.jobId}`,
      },
      { status: 202, headers: { "Cache-Control": "no-store" } },
    );
  } catch (error) {
    return accessError(error);
  }
}
