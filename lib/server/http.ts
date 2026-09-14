const rateLimitBuckets = new Map<string, { startedAt: number; count: number }>();

export function sameOrigin(request: Request): boolean {
  const origin = request.headers.get("origin");
  if (!origin) return true;

  try {
    const requestUrl = new URL(request.url);
    const requestOrigins = new Set([requestUrl.origin, hostOrigin(request, requestUrl)]);
    const configuredOrigin = process.env.SCOUT_PUBLIC_URL
      ? new URL(process.env.SCOUT_PUBLIC_URL).origin
      : null;
    return requestOrigins.has(origin) || origin === configuredOrigin;
  } catch {
    return false;
  }
}

function hostOrigin(request: Request, requestUrl: URL): string {
  const host = request.headers.get("host");
  if (!host) return requestUrl.origin;
  return new URL(`${requestUrl.protocol}//${host}`).origin;
}

export function jsonError(
  message: string,
  status: number,
  headers: Record<string, string> = {},
): Response {
  return Response.json(
    { error: { message } },
    { status, headers: { "Cache-Control": "no-store", ...headers } },
  );
}

export const MAX_JSON_BODY_BYTES = 1_048_576;

export async function readRequestBody(
  request: Request,
  maxBytes = MAX_JSON_BODY_BYTES,
): Promise<string | Response> {
  const contentLength = request.headers.get("content-length");
  if (contentLength) {
    const declaredLength = Number(contentLength);
    if (!Number.isSafeInteger(declaredLength) || declaredLength < 0 || declaredLength > maxBytes)
      return jsonError("Request payload is too large.", 413);
  }
  const body = await request.text();
  if (Buffer.byteLength(body, "utf8") > maxBytes)
    return jsonError("Request payload is too large.", 413);
  return body;
}

export function rateLimit(
  request: Request,
  bucket: string,
  limit: number,
  windowMs: number,
  now = Date.now(),
): Response | null {
  const identity =
    request.headers.get("x-scout-agent-id") ??
    request.headers.get("x-forwarded-for")?.split(",", 1)[0]?.trim() ??
    "anonymous";
  const key = `${bucket}:${identity.slice(0, 128)}`;
  const current = rateLimitBuckets.get(key);
  if (!current || now - current.startedAt >= windowMs) {
    rateLimitBuckets.set(key, { startedAt: now, count: 1 });
    return null;
  }
  current.count += 1;
  if (current.count <= limit) return null;
  const retryAfter = Math.max(1, Math.ceil((current.startedAt + windowMs - now) / 1_000));
  return jsonError("Too many requests. Try again later.", 429, {
    "Retry-After": String(retryAfter),
  });
}

export function resetRateLimits(): void {
  rateLimitBuckets.clear();
}
