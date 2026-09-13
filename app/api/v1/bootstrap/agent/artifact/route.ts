import { z } from "zod";

import { authorizeAgentInvitation } from "@/lib/server/invitations";
import { jsonError } from "@/lib/server/http";
import {
  readAgentArtifact,
  type InstallerArchitecture,
  type InstallerPlatform,
} from "@/lib/server/installer";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const artifactInput = z.object({
  platform: z.enum(["linux", "macos"]),
  architecture: z.enum(["x86_64", "aarch64"]),
});

export async function GET(request: Request) {
  const invitation =
    request.headers.get("x-scout-oti") ?? new URL(request.url).searchParams.get("oti");
  if (!invitation || !authorizeAgentInvitation(invitation))
    return jsonError("The agent invitation is invalid or expired.", 401);
  const query = Object.fromEntries(new URL(request.url).searchParams.entries());
  const parsed = artifactInput.safeParse(query);
  if (!parsed.success) return jsonError("The requested agent platform is invalid.", 400);
  const artifact = readAgentArtifact(
    parsed.data.platform as InstallerPlatform,
    parsed.data.architecture as InstallerArchitecture,
  );
  if (!artifact) return jsonError("The requested agent artifact is unavailable.", 404);
  return new Response(new Uint8Array(artifact), {
    headers: {
      "Cache-Control": "no-store",
      "Content-Type": "application/octet-stream",
      "Content-Length": String(artifact.byteLength),
      "Content-Disposition": `attachment; filename=${parsed.data.platform}-${parsed.data.architecture}-scout-agent`,
    },
  });
}
