import { FormEvent, useEffect, useState } from "react";
import { KeyRound, RefreshCw, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { APIError, api, type AccessRequest, type Credential, type TrustRecord } from "@/lib/api";

function splitValues(value: string) {
  return value.split(",").map((item) => item.trim()).filter(Boolean);
}

export function AccessView() {
  const [requests, setRequests] = useState<AccessRequest[]>([]);
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [trust, setTrust] = useState<TrustRecord[]>([]);
  const [credentialKind, setCredentialKind] = useState("ssh");
  const [secret, setSecret] = useState("");
  const [targets, setTargets] = useState("");
  const [endpoint, setEndpoint] = useState("");
  const [trustScope, setTrustScope] = useState("");
  const [trustHost, setTrustHost] = useState("");
  const [trustEndpoint, setTrustEndpoint] = useState("");
  const [fingerprint, setFingerprint] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  async function refresh() {
    const [requestList, credentialList, trustList] = await Promise.all([api.accessRequests("open"), api.credentials(), api.trust()]);
    setRequests(requestList.items);
    setCredentials(credentialList.items);
    setTrust(trustList.items);
  }

  useEffect(() => {
    refresh().catch((caught) => setError(caught instanceof APIError ? caught.message : "Could not load access state"));
  }, []);

  async function createCredential(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await api.createCredential({ kind: credentialKind, secret, targets: splitValues(targets), allowedUse: ["enrollment"], endpoint });
      setSecret("");
      setMessage("Credential stored. The secret is write-only and will not be shown again.");
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Credential could not be stored");
    } finally {
      setBusy(false);
    }
  }

  async function createTrust(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await api.createTrust({ scopeId: trustScope || undefined, host: trustHost, endpoint: trustEndpoint, fingerprint });
      setMessage("Trust record added. Scout will not accept a changed key automatically.");
      setFingerprint("");
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Trust record could not be stored");
    } finally {
      setBusy(false);
    }
  }

  return <section className="workspace-grid access-view">
    <div className="section-heading"><div><Badge variant="outline"><ShieldCheck size={13} />Owner-controlled access</Badge><h2>Resolve prerequisites</h2><p>Credentials are encrypted, target-bound, and never returned by listing endpoints. Host trust changes remain explicit and audited.</p></div><Button variant="ghost" size="icon" aria-label="Refresh access state" onClick={() => refresh().catch(() => setError("Could not refresh access state"))}><RefreshCw size={16} /></Button></div>
    {error && <p className="form-error" role="alert">{error}</p>}
    {message && <p className="form-success" role="status">{message}</p>}
    <div className="access-columns">
      <form className="form-panel" onSubmit={createCredential}>
        <div className="panel-title"><KeyRound size={17} /><h3>Write-only credential</h3></div>
        <label>Credential type<Input value={credentialKind} onChange={(event) => setCredentialKind(event.target.value)} /></label>
        <label>Secret<Input required type="password" autoComplete="new-password" value={secret} onChange={(event) => setSecret(event.target.value)} /></label>
        <label>Exact targets <span className="label-hint">host:port, comma separated</span><Input required placeholder="192.0.2.10:22" value={targets} onChange={(event) => setTargets(event.target.value)} /></label>
        <label>Endpoint metadata <span className="label-hint">optional</span><Input value={endpoint} onChange={(event) => setEndpoint(event.target.value)} /></label>
        <Button type="submit" disabled={busy}>Store encrypted credential</Button>
      </form>
      <form className="form-panel" onSubmit={createTrust}>
        <div className="panel-title"><ShieldCheck size={17} /><h3>Known host trust</h3></div>
        <label>Scope ID <span className="label-hint">optional</span><Input value={trustScope} onChange={(event) => setTrustScope(event.target.value)} /></label>
        <label>Host<Input required placeholder="192.0.2.10" value={trustHost} onChange={(event) => setTrustHost(event.target.value)} /></label>
        <label>Exact endpoint<Input required placeholder="192.0.2.10:22" value={trustEndpoint} onChange={(event) => setTrustEndpoint(event.target.value)} /></label>
        <label>Fingerprint<Input required placeholder="SHA256:…" value={fingerprint} onChange={(event) => setFingerprint(event.target.value)} /></label>
        <Button type="submit" variant="outline" disabled={busy}>Record trusted identity</Button>
      </form>
    </div>
    <div className="data-panel"><div className="section-heading"><div><h3>Open access requests</h3><p>Specific missing prerequisites; no command output or secrets.</p></div><Badge variant="outline">{requests.length}</Badge></div>{requests.length ? <div className="compact-list">{requests.map((item) => <div key={item.id}><strong>{item.reasonCode.replaceAll("_", " ")}</strong><span>{item.safeDetails.target ?? "Target unavailable"}</span><small>{new Date(item.lastAttempt).toLocaleString()}</small></div>)}</div> : <p className="empty-inline">No unresolved access requests.</p>}</div>
    <div className="access-columns"><div className="data-panel"><div className="section-heading"><h3>Stored metadata</h3><Badge variant="outline">{credentials.length}</Badge></div>{credentials.length ? <div className="compact-list">{credentials.map((item) => <div key={item.id}><strong>{item.kind}</strong><span>{item.targets.join(", ") || "No targets"}</span><small>revision {item.revision}{item.revokedAt ? " · revoked" : ""}</small></div>)}</div> : <p className="empty-inline">No credentials stored.</p>}</div><div className="data-panel"><div className="section-heading"><h3>Trusted identities</h3><Badge variant="outline">{trust.length}</Badge></div>{trust.length ? <div className="compact-list">{trust.map((item) => <div key={item.id}><strong>{item.host}</strong><span>{item.endpoint}</span><small>{item.fingerprint} · revision {item.revision}</small></div>)}</div> : <p className="empty-inline">No trusted identities stored.</p>}</div></div>
  </section>;
}
