import { z } from "zod";

import { authenticateAgentRequest } from "@/lib/server/agent-protocol";
import { jsonError } from "@/lib/server/http";
import { readVerifiedAgentArtifact } from "@/lib/server/releases";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const artifactInput = z.object({
  platform: z.enum(["linux", "macos"]),
  architecture: z.enum(["x86_64", "aarch64"]),
});

export async function GET(request: Request) {
  const identity = await authenticateAgentRequest(request, await request.text());
  if (identity instanceof Response) return identity;
  const parsed = artifactInput.safeParse(
    Object.fromEntries(new URL(request.url).searchParams.entries()),
  );
  if (!parsed.success) return jsonError("The requested agent platform is invalid.", 400);
  try {
    const artifact = readVerifiedAgentArtifact(parsed.data.platform, parsed.data.architecture);
    if (!artifact) return jsonError("The requested agent artifact is unavailable.", 404);
    return new Response(new Uint8Array(artifact), {
      headers: {
        "Cache-Control": "no-store",
        "Content-Type": "application/octet-stream",
        "Content-Length": String(artifact.byteLength),
        "Content-Disposition": `attachment; filename=scout-agent-${parsed.data.platform}-${parsed.data.architecture}`,
      },
    });
  } catch {
    return jsonError("The requested agent artifact failed release verification.", 503);
  }
}
