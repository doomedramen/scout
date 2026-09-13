import { AppShell } from "@/components/app-shell";
import { SettingsForm } from "@/components/settings/settings-form";
import { requirePageSession } from "@/lib/server/session";
import { getSettings } from "@/lib/server/settings";

export const dynamic = "force-dynamic";

export default async function SettingsPage() {
  const session = await requirePageSession();
  return (
    <AppShell
      username={session.user.username ?? session.user.name}
      title="Settings"
      description="Owner and operational settings for this Scout instance."
    >
      <SettingsForm initialPaused={getSettings().authorityPaused} />
    </AppShell>
  );
}
