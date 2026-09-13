const defaultApiOrigin = "http://127.0.0.1:8080";

function validOrigin(value: string | undefined): string | null {
  if (!value) return null;
  try {
    const url = new URL(value);
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    return url.origin;
  } catch {
    return null;
  }
}

function originForListener(value: string | undefined): string | null {
  const port = value?.trim().match(/:(\d+)$/)?.[1];
  if (!port) return null;
  return `${process.env.SCOUT_PRODUCTION === "true" ? "https" : "http"}://127.0.0.1:${port}`;
}

export function apiOrigins(): string[] {
  const configured = validOrigin(process.env.SCOUT_API_ORIGIN);
  const listenerOrigin = originForListener(process.env.SCOUT_LISTEN);
  const fallback = process.env.SCOUT_LISTEN ? listenerOrigin : defaultApiOrigin;
  return [...new Set([configured, fallback].filter((value): value is string => value !== null))];
}
