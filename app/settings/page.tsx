import { AppShell } from "@/components/app-shell";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { requirePageSession } from "@/lib/server/session";

export const dynamic = "force-dynamic";

export default async function SettingsPage() {
  const session = await requirePageSession();
  return (
    <AppShell
      username={session.user.username ?? session.user.name}
      title="Settings"
      description="Owner and operational settings for this Scout instance."
    >
      <Card>
        <CardHeader>
          <CardTitle>Owner settings</CardTitle>
          <CardDescription>
            Optional MFA and transport controls will be available here.
          </CardDescription>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          Signed in as {session.user.username ?? session.user.name}.
        </CardContent>
      </Card>
    </AppShell>
  );
}
