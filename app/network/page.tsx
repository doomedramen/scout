import { AppShell } from "@/components/app-shell";
import { NetworkPolicyForm } from "@/components/network/network-policy-form";
import { getDatabase } from "@/lib/server/db";
import { getDiscoveryState } from "@/lib/server/discovery";
import { requirePageSession } from "@/lib/server/session";

export const dynamic = "force-dynamic";

export default async function NetworkPage() {
  const session = await requirePageSession();
  const { sqlite } = getDatabase();
  const segments = sqlite
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
  return (
    <AppShell
      username={session.user.username ?? session.user.name}
      title="Network"
      description="Control the bounded networks Scout is allowed to discover."
    >
      <NetworkPolicyForm
        initial={{
          discovery: getDiscoveryState(),
          segments: segments.map((segment) => ({
            ...segment,
            paused: segment.paused === 1,
            lastScanAt: segment.lastScanAt ? new Date(segment.lastScanAt).toISOString() : null,
          })),
        }}
      />
    </AppShell>
  );
}
