import Link from "next/link";
import { Activity, ArrowRight, CircleAlert, Laptop, Radar, Server, WifiOff } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import type { FleetSnapshot, SystemState } from "@/lib/server/systems";

const stateLabels: Record<SystemState, string> = {
  "needs-access": "Needs access",
  "needs-trust": "Needs trust",
  installing: "Installing",
  online: "Online",
  stale: "Stale",
  offline: "Offline",
  blocked: "Blocked",
  revoked: "Revoked",
  excluded: "Excluded",
};

function stateVariant(state: SystemState): "default" | "secondary" | "destructive" | "outline" {
  if (state === "online") return "default";
  if (state === "blocked" || state === "offline") return "destructive";
  if (state === "needs-access" || state === "needs-trust") return "secondary";
  return "outline";
}

function StateIcon({ state }: { state: SystemState }) {
  if (state === "online") return <Activity className="size-4" aria-hidden="true" />;
  if (state === "offline") return <WifiOff className="size-4" aria-hidden="true" />;
  if (state === "needs-access" || state === "needs-trust")
    return <CircleAlert className="size-4" aria-hidden="true" />;
  return <Server className="size-4" aria-hidden="true" />;
}

function SummaryCard({ label, value, detail }: { label: string; value: number; detail: string }) {
  return (
    <Card size="sm">
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        <CardTitle className="text-2xl">{value}</CardTitle>
      </CardHeader>
      <CardContent className="text-xs text-muted-foreground">{detail}</CardContent>
    </Card>
  );
}

export function SystemsView({ snapshot, manual }: { snapshot: FleetSnapshot; manual: boolean }) {
  return (
    <div className="space-y-8">
      <section className="flex flex-col justify-between gap-4 sm:flex-row sm:items-end">
        <div>
          <p className="text-sm font-medium text-muted-foreground">Fleet</p>
          <h1 className="mt-1 font-heading text-3xl font-semibold tracking-tight">Systems</h1>
          <p className="mt-2 max-w-2xl text-muted-foreground">
            Scout finds reachable systems automatically, then keeps their host telemetry in one
            place.
          </p>
        </div>
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Radar className="size-4" aria-hidden="true" />
          <span>Discovery will appear here after owner setup.</span>
        </div>
      </section>

      {manual ? (
        <Card id="manual-installation">
          <CardHeader>
            <CardTitle>Manual installation</CardTitle>
            <CardDescription>
              Use this fallback when automatic SSH installation is not available.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-muted-foreground">
              Select a discovered system for the normal flow. Manual bootstrap artifacts and the
              one-line command will be available here when a Scout invitation is ready.
            </p>
          </CardContent>
        </Card>
      ) : null}

      <section aria-label="Fleet summary" className="grid gap-4 sm:grid-cols-2 lg:grid-cols-5">
        <SummaryCard
          label="Monitored"
          value={snapshot.summary.monitored}
          detail="Systems with an enrolled agent"
        />
        <SummaryCard
          label="Online"
          value={snapshot.summary.online}
          detail="Heartbeat and telemetry are current"
        />
        <SummaryCard
          label="Stale"
          value={snapshot.summary.stale}
          detail="Agent is present; telemetry needs attention"
        />
        <SummaryCard
          label="Offline"
          value={snapshot.summary.offline}
          detail="No recent agent heartbeat"
        />
        <SummaryCard
          label="Needs access"
          value={snapshot.summary.needsAccess}
          detail="Open supported access evidence"
        />
      </section>

      <Card>
        <CardHeader>
          <CardTitle>Systems</CardTitle>
          <CardDescription>
            Monitored hosts and current candidates with actionable access evidence.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {snapshot.systems.length === 0 ? (
            <div className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed px-6 py-16 text-center">
              <Laptop className="size-8 text-muted-foreground" aria-hidden="true" />
              <div>
                <h2 className="font-medium">No systems found yet</h2>
                <p className="mt-1 max-w-md text-sm text-muted-foreground">
                  Owner setup authorizes bounded discovery. Scout will list systems here only when
                  it confirms a supported access method.
                </p>
              </div>
              <Link
                href="/network"
                className="inline-flex items-center gap-1 text-sm font-medium underline underline-offset-4"
              >
                Review network discovery <ArrowRight className="size-4" aria-hidden="true" />
              </Link>
            </div>
          ) : (
            <div className="divide-y">
              {snapshot.systems.map((system) => (
                <Link
                  key={system.id}
                  href={`/systems/${system.id}`}
                  className="flex flex-col gap-3 py-4 first:pt-0 last:pb-0 hover:bg-muted/40 sm:flex-row sm:items-center sm:justify-between sm:px-3"
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <StateIcon state={system.status} />
                      <span className="truncate font-medium">{system.displayName}</span>
                      <Badge variant={stateVariant(system.status)}>
                        {stateLabels[system.status]}
                      </Badge>
                    </div>
                    <p className="mt-1 truncate text-sm text-muted-foreground">
                      {system.hostname ?? (system.addresses.join(", ") || "Identity not reported")}
                    </p>
                  </div>
                  <div className="flex items-center gap-3 text-sm text-muted-foreground">
                    {system.accessMethods.length ? (
                      <span>{system.accessMethods.join(", ")}</span>
                    ) : null}
                    <ArrowRight className="size-4" aria-hidden="true" />
                  </div>
                </Link>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <p className="text-xs text-muted-foreground">
        Updated {new Date(snapshot.generatedAt).toLocaleString()}
      </p>
      <Separator />
    </div>
  );
}
