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

export function jsonError(message: string, status: number): Response {
  return Response.json(
    { error: { message } },
    { status, headers: { "Cache-Control": "no-store" } },
  );
}
