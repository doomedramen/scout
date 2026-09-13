import { readiness } from "@/lib/server/runtime";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export function GET() {
  const status = readiness();
  return Response.json(status, {
    status: status.ok ? 200 : 503,
    headers: { "Cache-Control": "no-store" },
  });
}
