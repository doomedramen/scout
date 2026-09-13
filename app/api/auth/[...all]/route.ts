import { toNextJsHandler } from "better-auth/next-js";
import { NextResponse } from "next/server";

import { auth } from "@/lib/server/auth";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const authHandler = toNextJsHandler(auth);

async function guardSignup(request: Request): Promise<Response | undefined> {
  if (request.method !== "POST" || new URL(request.url).pathname.endsWith("/sign-up/email")) {
    if (request.method === "POST" && new URL(request.url).pathname.endsWith("/sign-up/email")) {
      return NextResponse.json({ message: "Owner setup is required." }, { status: 403 });
    }
    return undefined;
  }
  return undefined;
}

export async function GET(request: Request) {
  return authHandler.GET(request);
}

export async function POST(request: Request) {
  const blocked = await guardSignup(request);
  return blocked ?? authHandler.POST(request);
}

export async function PATCH(request: Request) {
  return authHandler.PATCH(request);
}

export async function PUT(request: Request) {
  return authHandler.PUT(request);
}

export async function DELETE(request: Request) {
  return authHandler.DELETE(request);
}
