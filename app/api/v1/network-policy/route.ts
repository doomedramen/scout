import { z } from "zod";

import { hostAddresses } from "@/lib/discovery/network";
import { jsonError, readRequestBody, sameOrigin } from "@/lib/server/http";
import { requireApiSession } from "@/lib/server/session";
import {
  discoverNow,
  ensureNetworkSegment,
  getDiscoveryState,
  setNetworkSegmentPaused,
} from "@/lib/server/discovery";
import { getDatabase } from "@/lib/server/db";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const policyInput = z.union([
  z.object({
    cidr: z.string().regex(/^\d+\.\d+\.\d+\.\d+\/(\d|[12]\d|3[0-2])$/),
  }),
  z.object({ segmentId: z.string().min(1).max(128), paused: z.boolean() }),
]);

function segments() {
  const { sqlite } = getDatabase();
  return sqlite
    .prepare(
      "SELECT id, cidr, source, paused, policy_version AS policyVersion, last_scan_at AS lastScanAt FROM network_segment ORDER BY created_at",
    )
    .all() as Array<{
    id: string;
    cidr: string;
    source: string;
    paused: number;
    policyVersion: number;
    lastScanAt: number | null;
  }>;
}

function response() {
  return {
    discovery: getDiscoveryState(),
    segments: segments().map((segment) => ({
      ...segment,
      paused: segment.paused === 1,
      lastScanAt: segment.lastScanAt ? new Date(segment.lastScanAt).toISOString() : null,
    })),
  };
}

export async function GET(request: Request) {
  const session = await requireApiSession(request);
  if (session instanceof Response) return session;
  return Response.json(response(), { headers: { "Cache-Control": "no-store" } });
}

export async function PUT(request: Request) {
  const session = await requireApiSession(request);
  if (session instanceof Response) return session;
  if (!sameOrigin(request)) return jsonError("Request origin is not allowed.", 403);

  let input: z.infer<typeof policyInput>;
  const body = await readRequestBody(request, 16 * 1024);
  if (body instanceof Response) return body;
  try {
    input = policyInput.parse(JSON.parse(body));
    if ("cidr" in input) hostAddresses(input.cidr);
  } catch {
    return jsonError("Enter a valid IPv4 network between /24 and /32.", 400);
  }

  if ("segmentId" in input) {
    if (!setNetworkSegmentPaused(input.segmentId, input.paused))
      return jsonError("Network segment not found.", 404);
    if (!input.paused) void discoverNow({ force: true });
    return Response.json(response(), { status: 202, headers: { "Cache-Control": "no-store" } });
  }

  const prefix = Number(input.cidr.split("/")[1]);
  if (prefix < 24) return jsonError("Scout accepts /24 or narrower bounded networks.", 400);

  const boundary = {
    cidr: input.cidr,
    interfaceName: "manual",
    gateway: null,
    sourceAddress: input.cidr.split("/")[0],
    provenanceKey: `manual|${input.cidr}`,
  };
  ensureNetworkSegment(boundary, Date.now(), "manual");
  void discoverNow({ boundary, force: true, source: "manual" });
  return Response.json(response(), { status: 202, headers: { "Cache-Control": "no-store" } });
}
