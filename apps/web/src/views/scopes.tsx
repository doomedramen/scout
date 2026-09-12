import { FormEvent, useEffect, useState } from "react";
import { Globe2, Plus, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { APIError, api, type Device, type Scope, type Site } from "@/lib/api";

function values(value: string) {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export function ScopesView() {
  const [sites, setSites] = useState<Site[]>([]);
  const [scopes, setScopes] = useState<Scope[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [siteName, setSiteName] = useState("");
  const [siteId, setSiteId] = useState("");
  const [ranges, setRanges] = useState("");
  const [exclusions, setExclusions] = useState("");
  const [ports, setPorts] = useState("22");
  const [methods, setMethods] = useState("tcp");
  const [scheduleMinutes, setScheduleMinutes] = useState("5");
  const [targetBudget, setTargetBudget] = useState("256");
  const [agentIds, setAgentIds] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  async function refresh() {
    const [siteList, scopeList, deviceList] = await Promise.all([api.sites(), api.scopes(), api.devices()]);
    setSites(siteList.items);
    setScopes(scopeList.items);
    setDevices(deviceList.items);
    if (!siteId && siteList.items[0]) setSiteId(siteList.items[0].id);
  }

  useEffect(() => {
    refresh().catch((caught) => setError(caught instanceof APIError ? caught.message : "Could not load scope policy"));
  }, []);

  async function createSite(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const site = await api.createSite(siteName);
      setSiteName("");
      setSiteId(site.id);
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Site could not be created");
    } finally {
      setBusy(false);
    }
  }

  async function createScope(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const scanPorts = values(ports)
        .map(Number)
        .filter((port) => Number.isInteger(port) && port > 0 && port <= 65535);
      const configuredPorts = scanPorts.length ? scanPorts : [22];
      const boundedTargetBudget = Math.max(1, Math.min(4096, Number(targetBudget) || 256));
      await api.createScope({
        siteId,
        ranges: values(ranges),
        exclusions: values(exclusions),
        methods: values(methods),
        ports: values(ports).map(Number),
        enabled: false,
        limits: { probesPerSecond: 10, concurrency: 16, targetBudget: 256 },
        scanPolicy: {
          serverEnabled: false,
          agentIds,
          scheduleSeconds: Math.max(60, (Number(scheduleMinutes) || 5) * 60),
          entryPoints: configuredPorts.map((port) => ({
            id: `ssh-${port}`,
            name: `SSH ${port}`,
            transport: "tcp" as const,
            port,
            accessMethod: "ssh" as const,
            enabled: true,
          })),
          limits: {
            probesPerSecond: 10,
            concurrency: 16,
            targetBudget: boundedTargetBudget,
            attemptBudget: boundedTargetBudget * configuredPorts.length,
            timeoutMilliseconds: 2000,
            runDeadlineSeconds: 600,
            resultPageSize: 1000,
          },
        },
      });
      setMessage("Scope saved disabled. Enable it only after reviewing access and exclusions.");
      setRanges("");
      setExclusions("");
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Scope could not be created");
    } finally {
      setBusy(false);
    }
  }

  async function toggle(scope: Scope) {
    setBusy(true);
    setError("");
    try {
      await api.updateScope(scope.id, {
        expectedRevision: scope.revision,
        ranges: scope.ranges,
        exclusions: scope.exclusions,
        methods: scope.allowedMethods,
        ports: scope.ports,
        credentialRef: scope.credentialRef,
        trustRef: scope.trustRef,
        limits: scope.limits ?? { probesPerSecond: 10, concurrency: 16, targetBudget: 256 },
        enabled: !scope.enabled,
      });
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Scope policy changed elsewhere; refresh and retry");
    } finally {
      setBusy(false);
    }
  }

  async function toggleServerScan(scope: Scope) {
    if (!scope.scanPolicy) return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await api.updateScope(scope.id, {
        expectedRevision: scope.revision,
        scanPolicy: {
          expectedRevision: scope.scanPolicy.revision,
          serverEnabled: !scope.scanPolicy.serverEnabled,
        },
      });
      setMessage(scope.scanPolicy.serverEnabled ? "Server scanning paused." : "Server scanning enabled.");
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Scan policy changed elsewhere; refresh and retry");
    } finally {
      setBusy(false);
    }
  }

  async function scanNow(scope: Scope) {
    if (!scope.scanPolicy) return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await api.startScan(
        scope.id,
        scope.scanPolicy.revision,
        { kind: "server", id: "control-server" },
        crypto.randomUUID(),
      );
      setMessage("Scan queued.");
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Scan could not be queued");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="workspace-grid scopes-view">
      <div className="section-heading">
        <div>
          <Badge variant="outline">
            <ShieldCheck size={13} />
            Automatic scoped enrollment
          </Badge>
          <h2>Sites and scopes</h2>
          <p>Boundaries and exclusions apply to every scan.</p>
        </div>
      </div>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {message && (
        <p className="form-success" role="status">
          {message}
        </p>
      )}
      <div className="access-columns">
        <form className="form-panel" onSubmit={createSite}>
          <div className="panel-title">
            <Globe2 size={17} />
            <h3>Create site</h3>
          </div>
          <label>
            Site name
            <Input required value={siteName} onChange={(event) => setSiteName(event.target.value)} />
          </label>
          <Button type="submit" disabled={busy}>
            <Plus size={15} />
            Add site
          </Button>
        </form>
        <form className="form-panel" onSubmit={createScope}>
          <div className="panel-title">
            <ShieldCheck size={17} />
            <h3>Define bounded scope</h3>
          </div>
          <label>
            Site
            <select required value={siteId} onChange={(event) => setSiteId(event.target.value)}>
              <option value="">Choose a site</option>
              {sites.map((site) => (
                <option key={site.id} value={site.id}>
                  {site.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            Ranges <span className="label-hint">CIDRs or literal addresses</span>
            <Input
              required
              placeholder="192.0.2.0/24"
              value={ranges}
              onChange={(event) => setRanges(event.target.value)}
            />
          </label>
          <label>
            Exclusions <span className="label-hint">optional, comma separated</span>
            <Input placeholder="192.0.2.9" value={exclusions} onChange={(event) => setExclusions(event.target.value)} />
          </label>
          <label>
            Methods
            <Input value={methods} onChange={(event) => setMethods(event.target.value)} />
          </label>
          <label>
            Ports
            <Input value={ports} onChange={(event) => setPorts(event.target.value)} />
          </label>
          <label>
            Scan schedule <span className="label-hint">minutes between runs</span>
            <Input
              inputMode="numeric"
              min={1}
              value={scheduleMinutes}
              onChange={(event) => setScheduleMinutes(event.target.value)}
            />
          </label>
          <label>
            Scan target budget
            <Input
              inputMode="numeric"
              min={1}
              value={targetBudget}
              onChange={(event) => setTargetBudget(event.target.value)}
            />
          </label>
          <label>
            Assigned scan agents <span className="label-hint">optional; enrolled devices only</span>
            <select
              multiple
              size={Math.min(4, Math.max(2, devices.filter((device) => device.agentId).length))}
              value={agentIds}
              onChange={(event) => setAgentIds(Array.from(event.target.selectedOptions, (option) => option.value))}
            >
              {devices
                .filter((device) => device.agentId)
                .map((device) => (
                  <option key={device.agentId} value={device.agentId}>
                    {device.displayName} · {device.addresses?.[0] ?? "address unavailable"}
                  </option>
                ))}
            </select>
            {!devices.some((device) => device.agentId) && <span className="label-hint">No enrolled agents yet.</span>}
          </label>
          <Button type="submit" disabled={busy || !siteId}>
            Save disabled scope
          </Button>
        </form>
      </div>
      <div className="data-panel">
        <div className="section-heading">
          <div>
            <h3>Current policies</h3>
          </div>
          <Badge variant="outline">{scopes.length}</Badge>
        </div>
        {scopes.length ? (
          <div className="scope-list">
            {scopes.map((scope) => (
              <div className="scope-row" key={scope.id}>
                <div>
                  <strong>{scope.ranges.join(", ")}</strong>
                  <small>
                    {scope.ports.join(", ") || "observations only"} · {scope.allowedMethods.join(", ")} · revision{" "}
                    {scope.revision}
                  </small>
                  <small>
                    Server scan {scope.scanPolicy?.serverEnabled ? "enabled" : "disabled"} · every{" "}
                    {Math.round((scope.scanPolicy?.scheduleSeconds ?? 300) / 60)} min
                  </small>
                  <small>{scope.scanPolicy?.agentIds.length ?? 0} assigned scan agent(s)</small>
                </div>
                <div>
                  <Badge variant="outline" className={scope.enabled ? "enabled-label" : "access-label"}>
                    {scope.enabled ? "Enabled" : "Disabled"}
                  </Badge>
                  <Button size="sm" variant="outline" disabled={busy} onClick={() => toggle(scope)}>
                    {scope.enabled ? "Pause scope" : "Enable scope"}
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy || !scope.enabled || !scope.scanPolicy}
                    onClick={() => toggleServerScan(scope)}
                  >
                    {scope.scanPolicy?.serverEnabled ? "Pause server scan" : "Enable server scan"}
                  </Button>
                  <Button
                    size="sm"
                    disabled={busy || !scope.enabled || !scope.scanPolicy?.serverEnabled}
                    onClick={() => scanNow(scope)}
                  >
                    Scan now
                  </Button>
                </div>
              </div>
            ))}
          </div>
        ) : (
          <p className="empty-inline">Create a site before defining a scope.</p>
        )}
      </div>
    </section>
  );
}
