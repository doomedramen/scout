import { useEffect, useMemo, useState } from "react";
import {
  AlertCircle,
  ArrowRight,
  CheckCircle2,
  Clock3,
  Network,
  RefreshCw,
  Server,
  ShieldAlert,
  UserRoundCheck,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { isActionableAccessCandidate, uniqueAccessCandidates } from "@/lib/access";
import { api, APIError, type Candidate, type Device, type Incident } from "@/lib/api";
import type { OverviewRouteData } from "../../app/lib/route-data";

type OverviewState = "online" | "offline" | "needs-access" | "revoked";

type FleetData = {
  devices: Device[];
  candidates: Candidate[];
  incidents: Incident[];
};

type FleetSource = "devices" | "candidates" | "incidents";
type FleetSourceErrors = Partial<Record<FleetSource, string>>;

async function loadAll<T>(load: (query: string) => Promise<{ items: T[]; nextCursor: string | null }>): Promise<T[]> {
  const all: T[] = [];
  let cursor = "";
  do {
    const query = cursor ? `?limit=500&cursor=${encodeURIComponent(cursor)}` : "?limit=500";
    const page = await load(query);
    all.push(...page.items);
    cursor = page.nextCursor ?? "";
  } while (cursor && all.length < 10_000);
  return all;
}

function value(device: Device, metric: string): number | null {
  const current = device.currentMetrics?.[metric]?.value;
  return typeof current === "number" ? current : null;
}

function stateFor(device: Device): OverviewState {
  if (device.lifecycle === "decommissioned" || device.availability === "revoked") return "revoked";
  if (!device.agentVersion) return "needs-access";
  return device.availability === "offline" ? "offline" : "online";
}

function stateLabel(state: OverviewState): string {
  if (state === "needs-access") return "Needs access";
  if (state === "offline") return "Offline";
  if (state === "revoked") return "Revoked";
  return "Online";
}

function stateClass(state: OverviewState): string {
  if (state === "needs-access") return "amber";
  if (state === "offline" || state === "revoked") return "muted";
  return "healthy";
}

function freshnessFor(device: Device): "current" | "stale" | "unknown" {
  const timestamps = Object.values(device.metricFreshness ?? {}).filter(Boolean);
  if (!timestamps.length) return device.availability === "online" ? "unknown" : "stale";
  const newest = Math.max(...timestamps.map((timestamp) => Date.parse(timestamp)).filter(Number.isFinite));
  if (!Number.isFinite(newest)) return "unknown";
  return Date.now() - newest < 5 * 60 * 1000 ? "current" : "stale";
}

function freshnessLabel(value: "current" | "stale" | "unknown"): string {
  if (value === "current") return "Current";
  if (value === "stale") return "Stale";
  return "Unknown";
}

function incidentTitle(incident: Incident): string {
  return incident.ruleSnapshot.name || "Monitoring condition";
}

function incidentSort(a: Incident, b: Incident): number {
  const severity = (value: Incident["severity"]) => (value === "critical" ? 0 : 1);
  return severity(a.severity) - severity(b.severity) || Date.parse(b.openedAt) - Date.parse(a.openedAt);
}

function sourceMessage(caught: unknown, fallback: string): string {
  return caught instanceof APIError ? caught.message : fallback;
}

export function OverviewView({
  initialData,
  onNavigate,
  onSelect,
  onOpenAccess,
  onIncident,
}: {
  initialData?: OverviewRouteData;
  onNavigate: (page: "Systems" | "Incidents" | "Network" | "Access") => void;
  onSelect: (device: Device) => void;
  onOpenAccess: (candidate: Candidate) => void;
  onIncident: (incidentId: string) => void;
}) {
  const [data, setData] = useState<FleetData>(() =>
    initialData
      ? { devices: initialData.devices, candidates: initialData.candidates, incidents: initialData.incidents }
      : { devices: [], candidates: [], incidents: [] },
  );
  const [sourceErrors, setSourceErrors] = useState<FleetSourceErrors>(() => initialData?.sourceErrors ?? {});
  const [loading, setLoading] = useState(!initialData);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    let cancelled = false;
    if (!initialData || retry > 0) setLoading(true);
    setError("");
    Promise.allSettled([
      loadAll((query) => api.devices(query)),
      loadAll((query) => api.candidates(query)),
      loadAll((query) =>
        api.incidents("?status=active&limit=500" + (query.includes("cursor=") ? `&${query.slice(1)}` : "")),
      ),
    ])
      .then(([devicesResult, candidatesResult, incidentsResult]) => {
        if (cancelled) return;
        const errors: FleetSourceErrors = {};
        if (devicesResult.status === "rejected")
          errors.devices = sourceMessage(devicesResult.reason, "Systems unavailable");
        if (candidatesResult.status === "rejected")
          errors.candidates = sourceMessage(candidatesResult.reason, "Access requests unavailable");
        if (incidentsResult.status === "rejected")
          errors.incidents = sourceMessage(incidentsResult.reason, "Incidents unavailable");
        setSourceErrors(errors);
        setData({
          devices: devicesResult.status === "fulfilled" ? devicesResult.value : [],
          candidates: candidatesResult.status === "fulfilled" ? candidatesResult.value : [],
          incidents: incidentsResult.status === "fulfilled" ? incidentsResult.value : [],
        });
        if (Object.keys(errors).length === 3) setError("Could not load fleet overview");
        else setError("");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [retry]);

  const devices = useMemo(() => data.devices.filter((device) => device.lifecycle !== "candidate"), [data.devices]);
  const accessCandidates = useMemo(
    () => uniqueAccessCandidates(data.candidates.filter(isActionableAccessCandidate)),
    [data.candidates],
  );
  const counts = useMemo(() => {
    const states = devices.map(stateFor);
    return {
      monitored: sourceErrors.devices ? null : states.filter((state) => state === "online").length,
      offline: sourceErrors.devices ? null : states.filter((state) => state === "offline").length,
      access: sourceErrors.candidates ? null : accessCandidates.length,
      revoked: sourceErrors.devices ? null : states.filter((state) => state === "revoked").length,
      stale: sourceErrors.devices ? null : devices.filter((device) => freshnessFor(device) === "stale").length,
    };
  }, [accessCandidates.length, devices, sourceErrors.candidates, sourceErrors.devices]);
  const incidents = useMemo(() => data.incidents.slice().sort(incidentSort), [data.incidents]);
  const attentionDevices = useMemo(
    () =>
      devices
        .filter((device) => stateFor(device) !== "online" || freshnessFor(device) === "stale")
        .sort((a, b) => stateFor(a).localeCompare(stateFor(b)) || a.displayName.localeCompare(b.displayName))
        .slice(0, 6),
    [devices],
  );
  const shortcuts = useMemo(() => {
    if (attentionDevices.length >= 6) return attentionDevices;
    const seen = new Set(attentionDevices.map((device) => device.id));
    return [
      ...attentionDevices,
      ...devices.filter((device) => !seen.has(device.id)).sort((a, b) => a.displayName.localeCompare(b.displayName)),
    ].slice(0, 6);
  }, [attentionDevices, devices]);

  if (loading) {
    return (
      <section className="overview" aria-busy="true">
        <div className="overview-loading" role="status">
          <Server aria-hidden="true" />
          <strong>Reading fleet health</strong>
          <span>Collecting current device, incident, and access state…</span>
        </div>
      </section>
    );
  }

  if (error) {
    return (
      <section className="overview">
        <div className="overview-error" role="alert">
          <AlertCircle aria-hidden="true" />
          <div>
            <h2>Fleet overview unavailable</h2>
            <p>{error}</p>
          </div>
          <Button variant="outline" onClick={() => setRetry((current) => current + 1)}>
            <RefreshCw data-icon="inline-start" />
            Retry
          </Button>
        </div>
      </section>
    );
  }

  return (
    <section className="overview" aria-labelledby="overview-title">
      <div className="overview-intro">
        <div>
          <h1 id="overview-title">Fleet status</h1>
        </div>
      </div>

      {Object.keys(sourceErrors).length > 0 && (
        <div className="notice stale-notice overview-partial-notice" role="status">
          <AlertCircle aria-hidden="true" />
          <span>Partial fleet data</span>
          <Button variant="outline" size="sm" onClick={() => setRetry((current) => current + 1)}>
            Retry
          </Button>
        </div>
      )}

      <div className="overview-summary-grid" aria-label="System totals">
        <button
          aria-label="Monitored fleet"
          className="overview-summary-card summary-primary"
          onClick={() => onNavigate("Systems")}
        >
          <span className="summary-card-label">
            <CheckCircle2 aria-hidden="true" /> Monitored
          </span>
          <strong>{counts.monitored ?? "—"}</strong>
          <small>online</small>
        </button>
        <button aria-label="Offline fleet" className="overview-summary-card" onClick={() => onNavigate("Systems")}>
          <span className="summary-card-label">
            <ShieldAlert aria-hidden="true" /> Offline
          </span>
          <strong>{counts.offline ?? "—"}</strong>
          <small>availability</small>
        </button>
        <button aria-label="Stale telemetry" className="overview-summary-card" onClick={() => onNavigate("Systems")}>
          <span className="summary-card-label">
            <Clock3 aria-hidden="true" /> Stale data
          </span>
          <strong>{counts.stale ?? "—"}</strong>
          <small>telemetry</small>
        </button>
        <button aria-label="Prerequisites" className="overview-summary-card" onClick={() => onNavigate("Systems")}>
          <span className="summary-card-label">
            <UserRoundCheck aria-hidden="true" /> Needs access
          </span>
          <strong>{counts.access ?? "—"}</strong>
          <small>pending</small>
        </button>
      </div>

      <div className="overview-grid">
        <Card className="overview-panel attention-panel">
          <CardHeader>
            <div className="overview-panel-heading">
              <div>
                <CardTitle>Incident queue</CardTitle>
              </div>
              <Badge variant={incidents.length ? "destructive" : "outline"}>{incidents.length}</Badge>
            </div>
          </CardHeader>
          <CardContent>
            {sourceErrors.incidents ? (
              <div className="overview-empty">
                <AlertCircle aria-hidden="true" />
                <span>Incidents unavailable.</span>
                <small>{sourceErrors.incidents}</small>
              </div>
            ) : incidents.length ? (
              <div className="overview-list" role="list" aria-label="Active incidents">
                {incidents.slice(0, 5).map((incident) => (
                  <button
                    className="overview-list-row"
                    key={incident.id}
                    role="listitem"
                    onClick={() => onIncident(incident.id)}
                  >
                    <span
                      className={`status-mark ${incident.severity === "critical" ? "critical" : "warning"}`}
                      aria-hidden="true"
                    />
                    <span className="overview-list-copy">
                      <strong>{incidentTitle(incident)}</strong>
                      <small>
                        {incident.deviceId} · {new Date(incident.openedAt).toLocaleString()}
                      </small>
                    </span>
                    <Badge variant={incident.severity === "critical" ? "destructive" : "outline"}>
                      {incident.severity}
                    </Badge>
                    <ArrowRight aria-hidden="true" />
                  </button>
                ))}
                {incidents.length > 5 && (
                  <Button variant="ghost" className="overview-more" onClick={() => onNavigate("Incidents")}>
                    View all incidents <ArrowRight data-icon="inline-end" />
                  </Button>
                )}
              </div>
            ) : (
              <div className="overview-empty">
                <CheckCircle2 aria-hidden="true" />
                <span>No active incidents.</span>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="overview-panel activity-panel">
          <CardHeader>
            <div className="overview-panel-heading">
              <div>
                <CardTitle>State summary</CardTitle>
              </div>
              <Network aria-hidden="true" />
            </div>
          </CardHeader>
          <CardContent>
            <div className="signal-rows">
              {(
                [
                  ["Online", counts.monitored, "healthy"],
                  ["Offline", counts.offline, "muted"],
                  ["Stale metrics", counts.stale, "amber"],
                  ["Needs access", counts.access, "amber"],
                ] as Array<[string, number | null, string]>
              ).map(([label, count, tone]) => (
                <button
                  key={label}
                  className="signal-row"
                  aria-label={label === "Needs access" ? "Prerequisites status" : `${label} status`}
                  onClick={() => onNavigate("Systems")}
                >
                  <span>
                    <i className={`dot ${tone}`} aria-hidden="true" /> {label}
                  </span>
                  <strong>{count ?? "—"}</strong>
                  <span className="signal-track" aria-hidden="true">
                    <i
                      className={tone}
                      style={{
                        width: `${count !== null && devices.length ? Math.min(100, (Number(count) / Math.max(devices.length, 1)) * 100) : 0}%`,
                      }}
                    />
                  </span>
                </button>
              ))}
            </div>
            {sourceErrors.devices ? (
              <div className="overview-empty compact">
                <AlertCircle aria-hidden="true" />
                <span>Systems unavailable.</span>
              </div>
            ) : (
              !devices.length && (
                <div className="overview-empty compact">
                  <Server aria-hidden="true" />
                  <span>No systems monitored yet.</span>
                  <small>
                    Found systems will appear after a network scan. Use + in the top navigation to add one manually.
                  </small>
                </div>
              )
            )}
          </CardContent>
        </Card>
      </div>

      <Card className="overview-panel shortcuts-panel">
        <CardHeader>
          <div className="overview-panel-heading">
            <div>
              <CardTitle>Systems</CardTitle>
            </div>
            <Button aria-label="Open system inventory" variant="ghost" size="sm" onClick={() => onNavigate("Systems")}>
              All systems <ArrowRight data-icon="inline-end" />
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {sourceErrors.devices ? (
            <div className="overview-empty">
              <AlertCircle aria-hidden="true" />
              <span>Systems unavailable.</span>
              <small>{sourceErrors.devices}</small>
            </div>
          ) : shortcuts.length ? (
            <div className="overview-shortcuts" role="list" aria-label="Systems">
              {shortcuts.map((device) => {
                const state = stateFor(device);
                const cpu = value(device, "cpu.utilization");
                const memory = value(device, "memory.used_percent");
                return (
                  <button key={device.id} className="shortcut-card" role="listitem" onClick={() => onSelect(device)}>
                    <span className={`shortcut-icon ${stateClass(state)}`} aria-hidden="true">
                      <Server />
                    </span>
                    <span className="shortcut-copy">
                      <strong title={device.displayName}>{device.displayName}</strong>
                      <small>{device.addresses?.[0] ?? device.hostname ?? "Address unavailable"}</small>
                    </span>
                    <Badge variant="outline" className={stateClass(state)}>
                      {stateLabel(state)}
                    </Badge>
                    <span className="shortcut-metrics">
                      <span>CPU {cpu === null ? "—" : `${cpu.toFixed(0)}%`}</span>
                      <span>MEM {memory === null ? "—" : `${memory.toFixed(0)}%`}</span>
                    </span>
                    <ArrowRight aria-hidden="true" />
                  </button>
                );
              })}
            </div>
          ) : (
            <div className="overview-empty">
              <Server aria-hidden="true" />
              <span>No systems monitored yet.</span>
              <small>
                Found systems will appear after a network scan. Use + in the top navigation to add one manually.
              </small>
            </div>
          )}
        </CardContent>
      </Card>

      {(sourceErrors.candidates || accessCandidates.length > 0) && (
        <Card className="overview-panel access-panel">
          <CardHeader>
            <div className="overview-panel-heading">
              <div>
                <CardTitle>Systems needing access</CardTitle>
              </div>
              <Badge variant="outline">{sourceErrors.candidates ? "—" : accessCandidates.length}</Badge>
            </div>
          </CardHeader>
          <CardContent>
            {sourceErrors.candidates ? (
              <div className="overview-empty">
                <AlertCircle aria-hidden="true" />
                <span>Access requests unavailable.</span>
                <small>{sourceErrors.candidates}</small>
              </div>
            ) : (
              <div className="access-strip">
                {accessCandidates.slice(0, 3).map((candidate) => (
                  <button key={candidate.id} onClick={() => onOpenAccess(candidate)}>
                    <UserRoundCheck aria-hidden="true" />
                    <span>
                      <strong>{candidate.displayName || candidate.hostname || candidate.address}</strong>
                      <small>
                        {candidate.address} · {candidate.state.replaceAll("_", " ")}
                      </small>
                    </span>
                    <ArrowRight aria-hidden="true" />
                  </button>
                ))}
                {accessCandidates.length > 3 && (
                  <Button variant="ghost" onClick={() => onNavigate("Systems")}>
                    View all <ArrowRight data-icon="inline-end" />
                  </Button>
                )}
              </div>
            )}
          </CardContent>
        </Card>
      )}
    </section>
  );
}
