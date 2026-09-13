import { AppShell } from "@/components/app-shell";
import { requirePageSession } from "@/lib/server/session";
import { getFleetSnapshot } from "@/lib/server/systems";
import { SystemsView } from "@/components/systems/systems-view";

export const dynamic = "force-dynamic";

export default async function SystemsPage({
  searchParams,
}: {
  searchParams: Promise<{ manual?: string }>;
}) {
  const session = await requirePageSession();
  const snapshot = getFleetSnapshot();
  const params = await searchParams;
  return (
    <AppShell username={session.user.username ?? session.user.name}>
      <SystemsView snapshot={snapshot} manual={params.manual === "1"} />
    </AppShell>
  );
}
