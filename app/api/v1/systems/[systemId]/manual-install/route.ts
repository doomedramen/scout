import { bootstrapFingerprint } from "@/lib/server/keys";
import { jsonError, rateLimit, sameOrigin } from "@/lib/server/http";
import { createAgentInvitation } from "@/lib/server/invitations";
import { renderManualInstallerCommand, scoutOrigin } from "@/lib/server/manual-install";
import { requireApiSession } from "@/lib/server/session";
import { getSystemDetails } from "@/lib/server/systems";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(
  request: Request,
  { params }: { params: Promise<{ systemId: string }> },
) {
  const session = await requireApiSession(request);
  if (session instanceof Response) return session;
  if (!sameOrigin(request)) return jsonError("Request origin is not allowed.", 403);
  const limited = rateLimit(request, `manual-install:${session.user.id}`, 20, 60_000);
  if (limited) return limited;

  const { systemId } = await params;
  const system = getSystemDetails(systemId);
  if (!system) return jsonError("System not found.", 404);
  if (system.excluded) return jsonError("Manual installation is disabled for this system.", 409);
  if (system.agent) return jsonError("This system already has an active Scout agent.", 409);
  if (!system.evidence.some((evidence) => evidence.method === "ssh" && evidence.current))
    return jsonError("Manual installation requires current open SSH evidence.", 409);

  let serverUrl: string;
  try {
    serverUrl = scoutOrigin(request);
  } catch {
    return jsonError("Scout does not have a valid public URL for manual installation.", 503);
  }
  const invitation = createAgentInvitation(system.id);
  const trustPin = new URL(serverUrl).protocol === "http:" ? bootstrapFingerprint() : null;
  return Response.json(
    {
      systemId: system.id,
      target: system.addresses[0] ?? null,
      serverUrl,
      expiresAt: new Date(invitation.expiresAt).toISOString(),
      trustPin,
      command: renderManualInstallerCommand({
        serverUrl,
        invitation: invitation.token,
        trustPin,
      }),
    },
    { status: 201, headers: { "Cache-Control": "no-store" } },
  );
}
