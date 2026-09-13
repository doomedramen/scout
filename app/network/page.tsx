import { AppShell } from "@/components/app-shell";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { requirePageSession } from "@/lib/server/session";

export const dynamic = "force-dynamic";

export default async function NetworkPage() {
  const session = await requirePageSession();
  return (
    <AppShell
      username={session.user.username ?? session.user.name}
      title="Network"
      description="Control the bounded networks Scout is allowed to discover."
    >
      <Card>
        <CardHeader>
          <CardTitle>Discovery policy</CardTitle>
          <CardDescription>
            Scout will derive a private default-route range after owner setup and ask when the route
            is ambiguous.
          </CardDescription>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          No network segment has been authorized yet.
        </CardContent>
      </Card>
    </AppShell>
  );
}
