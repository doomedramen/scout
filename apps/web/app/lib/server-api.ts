import "server-only";

import { headers } from "next/headers";

const apiOrigin = process.env.SCOUT_API_ORIGIN ?? "http://127.0.0.1:8080";

export type ShellData = {
  auth: "signedOut" | "signedIn";
  status: { mode: string; database: string; enrollmentAvailable: boolean; recoveryMode?: boolean } | null;
  statusError: boolean;
};

export async function getShellData(): Promise<ShellData> {
  const requestHeaders = await headers();
  const cookie = requestHeaders.get("cookie");
  const headersWithCookie = cookie ? { cookie } : undefined;
  const [owner, status] = await Promise.allSettled([
    fetch(`${apiOrigin}/api/v1/owner`, { headers: headersWithCookie, cache: "no-store" }),
    fetch(`${apiOrigin}/api/status`, { headers: headersWithCookie, cache: "no-store" }),
  ]);
  const signedIn = owner.status === "fulfilled" && owner.value.ok;
  if (status.status !== "fulfilled" || !status.value.ok) {
    return { auth: signedIn ? "signedIn" : "signedOut", status: null, statusError: true };
  }
  return { auth: signedIn ? "signedIn" : "signedOut", status: await status.value.json(), statusError: false };
}
