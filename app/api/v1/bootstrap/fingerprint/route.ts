import { bootstrapFingerprint } from "@/lib/server/keys";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET() {
  return Response.json(
    {
      algorithm: "sha256",
      fingerprint: bootstrapFingerprint(),
    },
    { headers: { "Cache-Control": "no-store" } },
  );
}
