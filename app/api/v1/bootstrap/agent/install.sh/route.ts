import { renderInstallerScript } from "@/lib/server/installer";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const serverUrl = publicOrigin(request);
  const script = renderInstallerScript({
    serverUrl,
    callbackUrl: serverUrl,
    invitation: "",
  });
  return new Response(script, {
    headers: {
      "cache-control": "no-store",
      "content-type": "text/plain; charset=utf-8",
      "content-disposition": "inline; filename=scout-install-agent.sh",
    },
  });
}

function publicOrigin(request: Request): string {
  const configured = process.env.SCOUT_PUBLIC_URL?.trim();
  if (configured) {
    try {
      return new URL(configured).origin;
    } catch {
      return new URL(request.url).origin;
    }
  }
  return new URL(request.url).origin;
}
