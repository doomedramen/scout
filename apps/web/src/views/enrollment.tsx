import { useEffect, useState } from "react";
import { Crosshair, RefreshCw, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { APIError, api, type Candidate, type Job, type Scope } from "@/lib/api";

export function EnrollmentView() {
  const [scopes, setScopes] = useState<Scope[]>([]);
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [scopeId, setScopeId] = useState("");
  const [budget, setBudget] = useState("256");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  async function refresh() {
    const [scopeList, candidateList, jobList] = await Promise.all([api.scopes(), api.candidates(), api.jobs("?kind=enrollment")]);
    setScopes(scopeList.items);
    setCandidates(candidateList.items);
    setJobs(jobList.items);
    if (!scopeId && scopeList.items[0]) setScopeId(scopeList.items[0].id);
  }

  useEffect(() => {
    refresh().catch((caught) => setError(caught instanceof APIError ? caught.message : "Could not load enrollment state"));
  }, []);

  async function discover() {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const result = await api.discover(scopeId, { targetBudget: Number(budget) || 256, source: "control-local" });
      setMessage(`${result.items.length} bounded candidate observations reconciled.`);
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Discovery could not run");
    } finally {
      setBusy(false);
    }
  }

  async function enqueue(candidate: Candidate) {
    setBusy(true);
    setError("");
    try {
      const result = await api.enqueueCandidate(candidate.id);
      setMessage(result.eligible ? "Enrollment job queued; worker will revalidate scope and trust." : `Not queued: ${result.reason ?? "missing prerequisite"}.`);
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Candidate could not be queued");
    } finally {
      setBusy(false);
    }
  }

  return <section className="workspace-grid enrollment-view"><div className="section-heading"><div><Badge variant="outline"><ShieldCheck size={13} />No routine per-device approval</Badge><h2>Discovery and enrollment</h2><p>Every candidate is bounded by an enabled scope, exclusions, trust, and target-specific credentials. Workers never receive ordinary agent capabilities.</p></div><Button variant="ghost" size="icon" aria-label="Refresh enrollment state" onClick={() => refresh().catch(() => setError("Could not refresh enrollment state"))}><RefreshCw size={16} /></Button></div>{error && <p className="form-error" role="alert">{error}</p>}{message && <p className="form-success" role="status">{message}</p>}<div className="form-panel discovery-launch"><div className="panel-title"><Crosshair size={17} /><h3>Run bounded discovery</h3></div><label>Scope<select value={scopeId} onChange={(event) => setScopeId(event.target.value)}><option value="">Choose an enabled scope</option>{scopes.map((scope) => <option key={scope.id} value={scope.id}>{scope.ranges.join(", ")} · {scope.enabled ? "enabled" : "disabled"}</option>)}</select></label><label>Target budget<Input inputMode="numeric" value={budget} onChange={(event) => setBudget(event.target.value)} /></label><Button disabled={busy || !scopeId || !scopes.find((scope) => scope.id === scopeId)?.enabled} onClick={discover}>Discover configured targets</Button><small>Discovery uses the configured ports and finite budget; it does not enumerate unbounded IPv6 space.</small></div><div className="data-panel"><div className="section-heading"><div><h3>Candidate sightings</h3><p>Duplicate vantages reconcile into one candidate. Excluded targets remain visible but never queue.</p></div><Badge variant="outline">{candidates.length}</Badge></div>{candidates.length ? <div className="scope-list">{candidates.map((candidate) => <div className="scope-row" key={candidate.id}><div><strong>{candidate.hostname || candidate.address}</strong><small>{candidate.address} · {candidate.source} · expires {new Date(candidate.expiresAt).toLocaleString()}</small></div><div><Badge variant="outline" className={candidate.excluded ? "access-label" : "version"}>{candidate.state}</Badge>{!candidate.excluded && candidate.state !== "expired" && <Button size="sm" variant="outline" disabled={busy} onClick={() => enqueue(candidate)}>Queue eligible work</Button>}</div></div>)}</div> : <p className="empty-inline">No candidates observed.</p>}</div><div className="data-panel"><div className="section-heading"><div><h3>Enrollment jobs</h3><p>Leases expire and stale worker reports are rejected.</p></div><Badge variant="outline">{jobs.length}</Badge></div>{jobs.length ? <div className="compact-list">{jobs.map((job) => <div key={job.id}><strong>{job.state}</strong><span>{job.destination || job.deviceId}</span><small>attempt {job.attempts} · epoch {job.epoch}</small></div>)}</div> : <p className="empty-inline">No enrollment jobs.</p>}</div></section>;
}
