import "server-only";

import { headers } from "next/headers";
import { apiOrigins } from "./api-origin";

export type ShellData = {
  auth: "signedOut" | "signedIn";
  status: { mode: string; database: string; enrollmentAvailable: boolean; recoveryMode?: boolean } | null;
  statusError: boolean;
};

async function fetchApi(path: string, init: RequestInit): Promise<Response> {
  let lastError: unknown;
  for (const origin of apiOrigins()) {
    try {
      return await fetch(new URL(path, origin), init);
    } catch (error) {
      lastError = error;
    }
  }
  throw lastError instanceof Error ? lastError : new Error("Scout API unavailable");
}

export async function getShellData(): Promise<ShellData> {
  const requestHeaders = await headers();
  const cookie = requestHeaders.get("cookie");
  const headersWithCookie = cookie ? { cookie } : undefined;
  const [owner, status] = await Promise.allSettled([
    fetchApi("/api/v1/owner", { headers: headersWithCookie, cache: "no-store" }),
    fetchApi("/api/status", { headers: headersWithCookie, cache: "no-store" }),
  ]);
  const signedIn = owner.status === "fulfilled" && owner.value.ok;
  if (status.status !== "fulfilled" || !status.value.ok) {
    return { auth: signedIn ? "signedIn" : "signedOut", status: null, statusError: true };
  }
  return { auth: signedIn ? "signedIn" : "signedOut", status: await status.value.json(), statusError: false };
}
