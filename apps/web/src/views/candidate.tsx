import { useEffect, useState } from "react";
import { ArrowLeft, CheckCircle2, KeyRound, RefreshCw, Server, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api, APIError, type Candidate, type CandidateDetail } from "@/lib/api";

function stateLabel(value: string): string {
  return value.replaceAll("_", " ");
}

function stateDescription(value: string): string {
  switch (value) {
    case "needs_credentials":
      return "SSH is reachable, but Scout has no matching credential for this host.";
    case "invalid_credentials":
      return "The stored SSH credential did not pass the access check.";
    case "needs_host_trust":
      return "The host identity must be explicitly trusted before installation.";
    case "needs_privilege":
      return "The SSH account can connect but cannot install the agent.";
    case "needs_server_connectivity":
      return "The host must be able to reach the Scout server after installation.";
    case "queued":
      return "Access is ready; the guarded enrollment job is waiting for a worker.";
    case "enrolling":
      return "The enrollment worker is installing and verifying the agent.";
    case "enrolled":
      return "The agent is enrolled. Open the system view to see live telemetry.";
    case "unsupported":
      return "A service was observed, but it is not an access method Scout can use yet.";
    case "unreachable":
      return "The host or route was unreachable during the latest scan.";
    case "stale":
      return "The latest evidence no longer confirms this entry point.";
    default:
      return "Scout is keeping this finding until current evidence and access are available.";
  }
}

export function CandidateView({
  selected,
  onBack,
  onOpenAccess,
}: {
  selected: Candidate;
  onBack: () => void;
  onOpenAccess: (candidate: Candidate) => void;
}) {
  const [detail, setDetail] = useState<CandidateDetail | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api
        .candidate(selected.id)
        .then((value) => {
          if (cancelled) return;
          setDetail(value);
          setError("");
        })
        .catch((caught) => {
          if (!cancelled) setError(caught instanceof APIError ? caught.message : "Could not load device evidence");
        });
    void load();
    const timer = window.setInterval(load, 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [retry, selected.id]);

  const candidate = detail?.candidate ?? selected;
  const canProvideAccess = ["needs_credentials", "invalid_credentials", "needs_host_trust"].includes(candidate.state);
  const openSSH = detail?.entryPoints.items.filter((item) => item.outcome === "open" && item.transport === "tcp") ?? [];

  return (
    <section className="workspace-grid candidate-detail-view" aria-labelledby="candidate-detail-title">
      <div className="candidate-detail-toolbar">
        <Button variant="ghost" onClick={onBack}>
          <ArrowLeft size={15} />
          Back to found devices
        </Button>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Refresh device evidence"
          onClick={() => setRetry((value) => value + 1)}
        >
          <RefreshCw size={16} />
        </Button>
      </div>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      <section className="data-panel candidate-hero">
        <div className="candidate-hero-icon" aria-hidden="true">
          <Server size={22} />
        </div>
        <div>
          <Badge variant="outline">Found device</Badge>
          <h2 id="candidate-detail-title">{candidate.displayName || candidate.hostname || candidate.address}</h2>
          <p>{candidate.address}</p>
        </div>
        <div className="candidate-hero-state">
          <Badge variant="outline">{stateLabel(candidate.state)}</Badge>
          <small>{stateDescription(candidate.state)}</small>
        </div>
      </section>

      {canProvideAccess && (
        <section className="action-panel" aria-labelledby="candidate-action-title">
          <div className="panel-title">
            <KeyRound size={17} />
            <div>
              <h3 id="candidate-action-title">SSH access is the next step</h3>
              <p>{stateDescription(candidate.state)}</p>
            </div>
          </div>
          <Button onClick={() => onOpenAccess(candidate)}>
            {candidate.state === "needs_host_trust" ? "Review access and trust" : "Add SSH credentials"}
          </Button>
        </section>
      )}

      {candidate.state === "enrolled" && (
        <section className="success-panel" role="status">
          <CheckCircle2 size={18} />
          <div>
            <strong>Agent enrolled</strong>
            <p>Telemetry will appear in Systems as the agent reports its first heartbeat.</p>
          </div>
        </section>
      )}

      <div className="candidate-detail-columns">
        <section className="data-panel" aria-labelledby="candidate-entry-points-title">
          <div className="section-heading">
            <div>
              <h3 id="candidate-entry-points-title">Observed services</h3>
              <p>Reachability evidence only; Scout does not store banners or packet contents.</p>
            </div>
            <Badge variant="outline">{candidate.entryPointCount ?? openSSH.length}</Badge>
          </div>
          {openSSH.length ? (
            <div className="evidence-table">
              {openSSH.map((item) => (
                <div key={item.id}>
                  <strong>{item.entryPointId === "ssh-default" ? "SSH" : item.entryPointId}</strong>
                  <span>
                    {item.address}:{item.port} · {item.outcome}
                  </span>
                  <small>
                    {item.scanner.kind} · {item.scanner.id} · observed {new Date(item.observedAt).toLocaleString()}
                  </small>
                </div>
              ))}
            </div>
          ) : (
            <p className="empty-inline">
              {candidate.entryPointCount
                ? "SSH evidence is available in the scan summary; details are loading."
                : "No retained service evidence."}
            </p>
          )}
        </section>

        <section className="data-panel" aria-labelledby="candidate-access-title">
          <div className="section-heading">
            <div>
              <h3 id="candidate-access-title">Access checklist</h3>
              <p>Only the current prerequisite is actionable.</p>
            </div>
            <ShieldCheck size={17} />
          </div>
          {detail?.accessRequests.length ? (
            <div className="evidence-table">
              {detail.accessRequests.map((request) => (
                <div key={request.id}>
                  <strong>{stateLabel(request.reasonCode)}</strong>
                  <span>{request.endpoint}</span>
                  <small>
                    {request.state} · updated {new Date(request.updatedAt).toLocaleString()}
                  </small>
                </div>
              ))}
            </div>
          ) : (
            <p className="empty-inline">No open access requests.</p>
          )}
        </section>
      </div>

      <section className="data-panel candidate-provenance" aria-labelledby="candidate-provenance-title">
        <div className="section-heading">
          <div>
            <h3 id="candidate-provenance-title">Evidence provenance</h3>
            <p>Separate scanner observations are retained while this candidate stays deduplicated.</p>
          </div>
          <Badge variant="outline">{candidate.provenance?.length ?? 0}</Badge>
        </div>
        {candidate.provenance?.length ? (
          <div className="compact-list">
            {candidate.provenance.map((item) => (
              <div key={`${item.scanner.kind}-${item.scanner.id}`}>
                <strong>{item.scanner.kind}</strong>
                <span>{item.scanner.id}</span>
                <small>
                  {item.outcome} · {new Date(item.lastObservedAt).toLocaleString()}
                </small>
              </div>
            ))}
          </div>
        ) : (
          <p className="empty-inline">No scanner provenance recorded.</p>
        )}
      </section>

      {detail?.enrollment && (
        <section className="data-panel" aria-labelledby="candidate-enrollment-title">
          <div className="section-heading">
            <div>
              <h3 id="candidate-enrollment-title">Enrollment</h3>
              <p>Guarded work is revalidated before every privileged step.</p>
            </div>
            <Badge variant="outline">{detail.enrollment.state}</Badge>
          </div>
          <p className="form-help">Job {detail.enrollment.jobId}</p>
        </section>
      )}
    </section>
  );
}
