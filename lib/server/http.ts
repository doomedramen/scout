export function sameOrigin(request: Request): boolean {
  const origin = request.headers.get("origin");
  if (!origin) return true;

  try {
    const requestOrigin = new URL(request.url).origin;
    const configuredOrigin = process.env.SCOUT_PUBLIC_URL
      ? new URL(process.env.SCOUT_PUBLIC_URL).origin
      : requestOrigin;
    return origin === requestOrigin || origin === configuredOrigin;
  } catch {
    return false;
  }
}

export function jsonError(message: string, status: number): Response {
  return Response.json(
    { error: { message } },
    { status, headers: { "Cache-Control": "no-store" } },
  );
}
