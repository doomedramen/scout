import { FormEvent, useEffect, useState } from "react";
import { KeyRound, RefreshCw, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  APIError,
  api,
  type AccessRequest,
  type Candidate,
  type CandidateDetail,
  type Credential,
  type Owner,
  type TrustRecord,
} from "@/lib/api";

function splitValues(value: string) {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export function AccessView({ candidate }: { candidate?: Candidate | null } = {}) {
  const [development, setDevelopment] = useState(false);
  const [candidateDetail, setCandidateDetail] = useState<CandidateDetail | null>(null);
  const [owner, setOwner] = useState<Owner | null>(null);
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
  const [securityBusy, setSecurityBusy] = useState(false);
  const [mfaPassword, setMfaPassword] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [mfaSecret, setMfaSecret] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  const focusedCandidate = candidateDetail?.candidate ?? candidate;

  useEffect(() => {
    if (!candidate) {
      setCandidateDetail(null);
      return;
    }
    let cancelled = false;
    setCandidateDetail(null);
    const defaultEndpoint = `${candidate.address}:22`;
    setTargets(defaultEndpoint);
    setEndpoint(defaultEndpoint);
    setTrustScope(candidate.scopeId);
    setTrustHost(candidate.address);
    setTrustEndpoint(defaultEndpoint);
    void api
      .candidate(candidate.id)
      .then((result) => {
        if (!cancelled) setCandidateDetail(result);
      })
      .catch(() => {
        if (!cancelled) setCandidateDetail(null);
      });
    return () => {
      cancelled = true;
    };
  }, [candidate?.id]);

  useEffect(() => {
    if (!focusedCandidate) return;
    const observed = candidateDetail?.entryPoints.items.find(
      (item) => item.outcome === "open" && item.transport === "tcp",
    );
    const observedEndpoint = observed ? `${observed.address}:${observed.port}` : `${focusedCandidate.address}:22`;
    setTargets(observedEndpoint);
    setEndpoint(observedEndpoint);
    setTrustScope(focusedCandidate.scopeId);
    setTrustHost(focusedCandidate.address);
    setTrustEndpoint(observedEndpoint);
  }, [candidateDetail, focusedCandidate]);

  async function refresh() {
    const [statusInfo, ownerInfo, requestList, credentialList, trustList] = await Promise.all([
      api.status(),
      api.owner(),
      api.accessRequests("open"),
      api.credentials(),
      api.trust(),
    ]);
    setDevelopment(statusInfo.mode === "development");
    setOwner(ownerInfo);
    setRequests(requestList.items);
    setCredentials(credentialList.items);
    setTrust(trustList.items);
  }

  async function beginMFA(event: FormEvent) {
    event.preventDefault();
    setSecurityBusy(true);
    setError("");
    setMessage("");
    try {
      const result = await api.beginMFA(mfaPassword);
      setMfaSecret(result.secret);
      setMfaPassword("");
      setMessage("MFA secret generated. Add it to an authenticator, then confirm with the six-digit code.");
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "MFA setup could not be started");
    } finally {
      setSecurityBusy(false);
    }
  }

  async function confirmMFA(event: FormEvent) {
    event.preventDefault();
    setSecurityBusy(true);
    setError("");
    setMessage("");
    try {
      const result = await api.confirmMFA(mfaCode);
      setOwner((current) => (current ? { ...current, mfaEnabled: true } : current));
      setMfaSecret("");
      setMfaCode("");
      setRecoveryCodes(result.recoveryCodes);
      setMessage("MFA enabled. Sensitive actions are unlocked for five minutes; save the recovery codes now.");
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "MFA code could not be confirmed");
    } finally {
      setSecurityBusy(false);
    }
  }

  async function reauthenticate(event: FormEvent) {
    event.preventDefault();
    setSecurityBusy(true);
    setError("");
    setMessage("");
    try {
      await api.reauth(mfaPassword, mfaCode);
      setMfaPassword("");
      setMfaCode("");
      setMessage("Sensitive actions unlocked for five minutes.");
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Re-authentication failed");
    } finally {
      setSecurityBusy(false);
    }
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
      await api.createCredential({
        kind: credentialKind,
        secret,
        targets: splitValues(targets),
        allowedUse: ["enrollment"],
        endpoint,
        scopeId: focusedCandidate?.scopeId,
        expectedScopeRevision: focusedCandidate?.scopeRevision,
      });
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
      await api.createTrust({
        scopeId: trustScope || undefined,
        host: trustHost,
        endpoint: trustEndpoint,
        fingerprint,
      });
      setMessage("Trust record added. Scout will not accept a changed key automatically.");
      setFingerprint("");
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Trust record could not be stored");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="workspace-grid access-view">
      {focusedCandidate && (
        <section className="action-panel access-focus-panel" aria-labelledby="access-focus-title">
          <div className="panel-title">
            <KeyRound size={17} />
            <div>
              <h2 id="access-focus-title">
                Prepare access for {focusedCandidate.displayName || focusedCandidate.address}
              </h2>
              <p>
                Scout found an SSH entry point at <strong>{endpoint || `${focusedCandidate.address}:22`}</strong>. Store
                a target-bound credential below and Scout will re-evaluate this device automatically.
              </p>
            </div>
          </div>
          <Badge variant="outline">{focusedCandidate.state.replaceAll("_", " ")}</Badge>
        </section>
      )}
      <div className="section-heading">
        <div>
          <Badge variant="outline">
            <ShieldCheck size={13} />
            Owner-controlled access
          </Badge>
          <h2>Resolve prerequisites</h2>
          <p>
            Credentials are encrypted, target-bound, and never returned by listing endpoints. Host trust changes remain
            explicit and audited.
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Refresh access state"
          onClick={() => refresh().catch(() => setError("Could not refresh access state"))}
        >
          <RefreshCw size={16} />
        </Button>
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
      <section className="data-panel security-panel" aria-labelledby="owner-security-title">
        <div className="section-heading">
          <div>
            <h3 id="owner-security-title">Owner security</h3>
            <p>
              {development
                ? "Development mode keeps authentication and CSRF protection but bypasses the recent-MFA ceremony."
                : "Scope changes, credentials, discovery, and agent invitations require recent MFA."}
            </p>
          </div>
          <Badge variant="outline">
            {development ? "Development bypass" : owner?.mfaEnabled ? "MFA enabled" : "MFA required"}
          </Badge>
        </div>
        {development ? (
          <p className="form-help">Production deployments still require MFA before sensitive changes.</p>
        ) : !owner ? (
          <p className="empty-inline">Loading owner security state…</p>
        ) : owner.mfaEnabled ? (
          <form className="security-form" onSubmit={reauthenticate}>
            <p className="form-help">Re-authenticate to unlock sensitive changes for five minutes.</p>
            <div className="form-inline security-form-fields">
              <label>
                Owner password
                <Input
                  required
                  type="password"
                  autoComplete="current-password"
                  value={mfaPassword}
                  onChange={(event) => setMfaPassword(event.target.value)}
                />
              </label>
              <label>
                Authenticator code
                <Input
                  required
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  value={mfaCode}
                  onChange={(event) => setMfaCode(event.target.value)}
                />
              </label>
              <Button type="submit" disabled={securityBusy}>
                {securityBusy ? "Checking…" : "Unlock sensitive actions"}
              </Button>
            </div>
          </form>
        ) : !mfaSecret ? (
          <form className="security-form" onSubmit={beginMFA}>
            <p className="form-help">Set up an authenticator once before storing credentials or enrolling agents.</p>
            <div className="form-inline security-form-fields">
              <label>
                Owner password
                <Input
                  required
                  type="password"
                  autoComplete="current-password"
                  value={mfaPassword}
                  onChange={(event) => setMfaPassword(event.target.value)}
                />
              </label>
              <Button type="submit" disabled={securityBusy}>
                {securityBusy ? "Preparing…" : "Set up MFA"}
              </Button>
            </div>
          </form>
        ) : (
          <div className="security-form">
            <p className="form-help">Add this secret to your authenticator app. It is shown only during setup.</p>
            <code className="mfa-secret">{mfaSecret}</code>
            <form className="form-inline security-form-fields" onSubmit={confirmMFA}>
              <label>
                Authenticator code
                <Input
                  required
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  value={mfaCode}
                  onChange={(event) => setMfaCode(event.target.value)}
                />
              </label>
              <Button type="submit" disabled={securityBusy}>
                {securityBusy ? "Confirming…" : "Confirm MFA"}
              </Button>
            </form>
          </div>
        )}
        {recoveryCodes.length > 0 && (
          <div className="recovery-codes" role="status">
            <strong>Save these recovery codes</strong>
            <pre>{recoveryCodes.join("\n")}</pre>
          </div>
        )}
      </section>
      <div className="access-columns">
        <form className="form-panel" onSubmit={createCredential}>
          <div className="panel-title">
            <KeyRound size={17} />
            <h3>Write-only credential</h3>
          </div>
          <label>
            Credential type
            <Input value={credentialKind} onChange={(event) => setCredentialKind(event.target.value)} />
          </label>
          <label>
            Secret
            <Input
              required
              type="password"
              autoComplete="new-password"
              value={secret}
              onChange={(event) => setSecret(event.target.value)}
            />
          </label>
          <label>
            Exact targets <span className="label-hint">host:port, comma separated</span>
            <Input
              required
              placeholder="192.0.2.10:22"
              value={targets}
              onChange={(event) => setTargets(event.target.value)}
            />
          </label>
          <label>
            Endpoint metadata <span className="label-hint">optional</span>
            <Input value={endpoint} onChange={(event) => setEndpoint(event.target.value)} />
          </label>
          <Button type="submit" disabled={busy}>
            Store encrypted credential
          </Button>
        </form>
        <form className="form-panel" onSubmit={createTrust}>
          <div className="panel-title">
            <ShieldCheck size={17} />
            <h3>Known host trust</h3>
          </div>
          <label>
            Scope ID <span className="label-hint">optional</span>
            <Input value={trustScope} onChange={(event) => setTrustScope(event.target.value)} />
          </label>
          <label>
            Host
            <Input
              required
              placeholder="192.0.2.10"
              value={trustHost}
              onChange={(event) => setTrustHost(event.target.value)}
            />
          </label>
          <label>
            Exact endpoint
            <Input
              required
              placeholder="192.0.2.10:22"
              value={trustEndpoint}
              onChange={(event) => setTrustEndpoint(event.target.value)}
            />
          </label>
          <label>
            Fingerprint
            <Input
              required
              placeholder="SHA256:…"
              value={fingerprint}
              onChange={(event) => setFingerprint(event.target.value)}
            />
          </label>
          <Button type="submit" variant="outline" disabled={busy}>
            Record trusted identity
          </Button>
        </form>
      </div>
      <div className="data-panel">
        <div className="section-heading">
          <div>
            <h3>Open access requests</h3>
            <p>Specific missing prerequisites; no command output or secrets.</p>
          </div>
          <Badge variant="outline">{requests.length}</Badge>
        </div>
        {requests.length ? (
          <div className="compact-list">
            {requests.map((item) => (
              <div key={item.id}>
                <strong>{item.reasonCode.replaceAll("_", " ")}</strong>
                <span>{item.safeDetails.target ?? "Target unavailable"}</span>
                <small>{new Date(item.lastAttempt).toLocaleString()}</small>
              </div>
            ))}
          </div>
        ) : (
          <p className="empty-inline">No unresolved access requests.</p>
        )}
      </div>
      <div className="access-columns">
        <div className="data-panel">
          <div className="section-heading">
            <h3>Stored metadata</h3>
            <Badge variant="outline">{credentials.length}</Badge>
          </div>
          {credentials.length ? (
            <div className="compact-list">
              {credentials.map((item) => (
                <div key={item.id}>
                  <strong>{item.kind}</strong>
                  <span>{item.targets.join(", ") || "No targets"}</span>
                  <small>
                    revision {item.revision}
                    {item.revokedAt ? " · revoked" : ""}
                  </small>
                </div>
              ))}
            </div>
          ) : (
            <p className="empty-inline">No credentials stored.</p>
          )}
        </div>
        <div className="data-panel">
          <div className="section-heading">
            <h3>Trusted identities</h3>
            <Badge variant="outline">{trust.length}</Badge>
          </div>
          {trust.length ? (
            <div className="compact-list">
              {trust.map((item) => (
                <div key={item.id}>
                  <strong>{item.host}</strong>
                  <span>{item.endpoint}</span>
                  <small>
                    {item.fingerprint} · revision {item.revision}
                  </small>
                </div>
              ))}
            </div>
          ) : (
            <p className="empty-inline">No trusted identities stored.</p>
          )}
        </div>
      </div>
    </section>
  );
}
