import { headers } from "next/headers";
import { redirect } from "next/navigation";

import { auth } from "@/lib/server/auth";
import { jsonError } from "@/lib/server/http";

export async function currentSession(requestHeaders?: Headers) {
  return auth.api.getSession({ headers: requestHeaders ?? (await headers()) });
}

export async function requirePageSession() {
  const session = await currentSession();
  if (!session) redirect("/sign-in");
  return session;
}

export async function requireApiSession(request: Request) {
  const session = await currentSession(request.headers);
  return session ?? jsonError("Sign in is required.", 401);
}
