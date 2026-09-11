import { FormEvent, useEffect, useState } from "react";
import { Globe2, Plus, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { APIError, api, type Scope, type Site } from "@/lib/api";

function values(value: string) {
  return value.split(",").map((item) => item.trim()).filter(Boolean);
}

export function ScopesView() {
  const [sites, setSites] = useState<Site[]>([]);
  const [scopes, setScopes] = useState<Scope[]>([]);
  const [siteName, setSiteName] = useState("");
  const [siteId, setSiteId] = useState("");
  const [ranges, setRanges] = useState("");
  const [exclusions, setExclusions] = useState("");
  const [ports, setPorts] = useState("22");
  const [methods, setMethods] = useState("tcp");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  async function refresh() {
    const [siteList, scopeList] = await Promise.all([api.sites(), api.scopes()]);
    setSites(siteList.items);
    setScopes(scopeList.items);
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
      await api.createScope({ siteId, ranges: values(ranges), exclusions: values(exclusions), methods: values(methods), ports: values(ports).map(Number), enabled: false, limits: { probesPerSecond: 10, concurrency: 16, targetBudget: 256 } });
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
      await api.updateScope(scope.id, { expectedRevision: scope.revision, ranges: scope.ranges, exclusions: scope.exclusions, methods: scope.allowedMethods, ports: scope.ports, credentialRef: scope.credentialRef, trustRef: scope.trustRef, limits: scope.limits ?? { probesPerSecond: 10, concurrency: 16, targetBudget: 256 }, enabled: !scope.enabled });
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Scope policy changed elsewhere; refresh and retry");
    } finally {
      setBusy(false);
    }
  }

  return <section className="workspace-grid scopes-view">
    <div className="section-heading"><div><Badge variant="outline"><ShieldCheck size={13} />Automatic scoped enrollment</Badge><h2>Sites and scopes</h2><p>Enabling a scope authorizes bounded discovery and enrollment. Exclusions always win and changes fence queued work.</p></div></div>
    {error && <p className="form-error" role="alert">{error}</p>}
    {message && <p className="form-success" role="status">{message}</p>}
    <div className="access-columns"><form className="form-panel" onSubmit={createSite}><div className="panel-title"><Globe2 size={17} /><h3>Create site</h3></div><label>Site name<Input required value={siteName} onChange={(event) => setSiteName(event.target.value)} /></label><Button type="submit" disabled={busy}><Plus size={15} />Add site</Button></form><form className="form-panel" onSubmit={createScope}><div className="panel-title"><ShieldCheck size={17} /><h3>Define bounded scope</h3></div><label>Site<select required value={siteId} onChange={(event) => setSiteId(event.target.value)}><option value="">Choose a site</option>{sites.map((site) => <option key={site.id} value={site.id}>{site.name}</option>)}</select></label><label>Ranges <span className="label-hint">CIDRs or literal addresses</span><Input required placeholder="192.0.2.0/24" value={ranges} onChange={(event) => setRanges(event.target.value)} /></label><label>Exclusions <span className="label-hint">optional, comma separated</span><Input placeholder="192.0.2.9" value={exclusions} onChange={(event) => setExclusions(event.target.value)} /></label><label>Methods<Input value={methods} onChange={(event) => setMethods(event.target.value)} /></label><label>Ports<Input value={ports} onChange={(event) => setPorts(event.target.value)} /></label><Button type="submit" disabled={busy || !siteId}>Save disabled scope</Button></form></div>
    <div className="data-panel"><div className="section-heading"><div><h3>Current policies</h3><p>Policy revisions are visible to agents and workers.</p></div><Badge variant="outline">{scopes.length}</Badge></div>{scopes.length ? <div className="scope-list">{scopes.map((scope) => <div className="scope-row" key={scope.id}><div><strong>{scope.ranges.join(", ")}</strong><small>{scope.ports.join(", ") || "observations only"} · {scope.allowedMethods.join(", ")} · revision {scope.revision}</small></div><div><Badge variant="outline" className={scope.enabled ? "enabled-label" : "access-label"}>{scope.enabled ? "Enabled" : "Disabled"}</Badge><Button size="sm" variant="outline" disabled={busy} onClick={() => toggle(scope)}>{scope.enabled ? "Pause scope" : "Enable scope"}</Button></div></div>)}</div> : <p className="empty-inline">Create a site before defining a scope.</p>}</div>
  </section>;
}
