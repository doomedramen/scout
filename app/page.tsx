import { headers } from "next/headers";
import { redirect } from "next/navigation";

import { auth } from "@/lib/server/auth";
import { setupStatus } from "@/lib/server/setup";

export const dynamic = "force-dynamic";

export default async function HomePage() {
  if (setupStatus().required) redirect("/setup");

  const session = await auth.api.getSession({ headers: await headers() });
  redirect(session ? "/systems" : "/sign-in");
}
