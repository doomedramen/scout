import { notFound } from "next/navigation";

import { AppShell } from "@/components/app-shell";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { requirePageSession } from "@/lib/server/session";
import { getSystemDetails } from "@/lib/server/systems";

export const dynamic = "force-dynamic";

export default async function SystemPage({ params }: { params: Promise<{ systemId: string }> }) {
  const session = await requirePageSession();
  const { systemId } = await params;
  const system = getSystemDetails(systemId);
  if (!system) notFound();

  return (
    <AppShell
      username={session.user.username ?? session.user.name}
      title={system.displayName}
      description="System identity, access, installation, and telemetry."
    >
      <div className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Identity</CardTitle>
            <CardDescription>
              Network observations support the durable Scout system record.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4 text-sm">
            <dl className="grid gap-3 sm:grid-cols-2">
              <div>
                <dt className="text-muted-foreground">Status</dt>
                <dd className="mt-1">
                  <Badge>{system.status}</Badge>
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground">System ID</dt>
                <dd className="mt-1 break-all font-mono text-xs">{system.id}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Hostname</dt>
                <dd className="mt-1">{system.hostname ?? "Not reported"}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Addresses</dt>
                <dd className="mt-1">{system.addresses.join(", ") || "Not reported"}</dd>
              </div>
            </dl>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Access</CardTitle>
            <CardDescription>Supported access evidence and trusted host identity.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4 text-sm">
            {system.evidence.length ? (
              <div className="space-y-3">
                {system.evidence.map((evidence) => (
                  <div
                    key={`${evidence.method}-${evidence.address}-${evidence.port}`}
                    className="rounded-md border p-3"
                  >
                    <div className="flex items-center justify-between gap-3">
                      <span className="font-medium">
                        {evidence.method.toUpperCase()} {evidence.address}:{evidence.port}
                      </span>
                      <Badge variant={evidence.current ? "secondary" : "outline"}>
                        {evidence.current ? "Open" : "Expired"}
                      </Badge>
                    </div>
                    {evidence.fingerprint ? (
                      <p className="mt-2 break-all font-mono text-xs text-muted-foreground">
                        {evidence.fingerprint}
                      </p>
                    ) : null}
                  </div>
                ))}
              </div>
            ) : (
              <p className="text-muted-foreground">
                No supported access evidence is available yet.
              </p>
            )}
          </CardContent>
        </Card>
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Agent</CardTitle>
            <CardDescription>
              Telemetry is reported by an agent installed on this host only.
            </CardDescription>
          </CardHeader>
          <CardContent className="text-sm">
            {system.agent ? (
              <dl className="grid gap-3 sm:grid-cols-4">
                <div>
                  <dt className="text-muted-foreground">Platform</dt>
                  <dd className="mt-1">{system.agent.platform}</dd>
                </div>
                <div>
                  <dt className="text-muted-foreground">Architecture</dt>
                  <dd className="mt-1">{system.agent.architecture}</dd>
                </div>
                <div>
                  <dt className="text-muted-foreground">Version</dt>
                  <dd className="mt-1">{system.agent.version}</dd>
                </div>
                <div>
                  <dt className="text-muted-foreground">Last telemetry</dt>
                  <dd className="mt-1">{system.agent.lastTelemetryAt ?? "Not reported"}</dd>
                </div>
              </dl>
            ) : (
              <p className="text-muted-foreground">
                No agent is enrolled. The access form will be available after SSH trust is
                confirmed.
              </p>
            )}
          </CardContent>
        </Card>
      </div>
    </AppShell>
  );
}
