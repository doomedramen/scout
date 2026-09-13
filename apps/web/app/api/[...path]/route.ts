import { type NextRequest } from "next/server";
import { apiOrigins } from "../../lib/api-origin";

export const dynamic = "force-dynamic";

async function proxy(request: NextRequest, path: string[]) {
  const headers = new Headers(request.headers);
  for (const header of [
    "connection",
    "content-encoding",
    "content-length",
    "expect",
    "host",
    "keep-alive",
    "te",
    "trailer",
    "transfer-encoding",
    "upgrade",
  ]) {
    headers.delete(header);
  }
  const body = request.method === "GET" || request.method === "HEAD" ? undefined : await request.arrayBuffer();
  let lastError: unknown;
  for (const apiOrigin of apiOrigins()) {
    try {
      const target = new URL(`/api/${path.map(encodeURIComponent).join("/")}`, apiOrigin);
      target.search = request.nextUrl.search;
      const response = await fetch(target, {
        method: request.method,
        headers,
        body,
        cache: "no-store",
        redirect: "manual",
      });
      const responseHeaders = new Headers(response.headers);
      responseHeaders.delete("content-encoding");
      responseHeaders.delete("content-length");
      responseHeaders.delete("set-cookie");
      for (const cookie of response.headers.getSetCookie()) responseHeaders.append("set-cookie", cookie);
      return new Response(response.body, { status: response.status, headers: responseHeaders });
    } catch (error) {
      lastError = error;
    }
  }

  console.error("Scout API proxy unavailable", {
    path: `/api/${path.join("/")}`,
    origins: apiOrigins(),
    error: lastError instanceof Error ? lastError.message : "unknown error",
  });
  return Response.json({ error: { code: "api_unavailable", message: "Scout API is unavailable" } }, { status: 502 });
}

export async function GET(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  return proxy(request, (await context.params).path);
}

export async function POST(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  return proxy(request, (await context.params).path);
}

export async function PUT(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  return proxy(request, (await context.params).path);
}

export async function PATCH(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  return proxy(request, (await context.params).path);
}

export async function DELETE(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  return proxy(request, (await context.params).path);
}

export async function HEAD(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  return proxy(request, (await context.params).path);
}

export async function OPTIONS(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  return proxy(request, (await context.params).path);
}
