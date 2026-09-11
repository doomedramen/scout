import { FormEvent, useState } from "react";
import { KeyRound, LockKeyhole, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { APIError, api } from "@/lib/api";

export function SetupView({ onSignedIn }: { onSignedIn: () => void }) {
  const [setupMode, setSetupMode] = useState(true);
  const [setupToken, setSetupToken] = useState("");
  const [password, setPassword] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [recoveryCode, setRecoveryCode] = useState("");
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true); setError("");
    try {
      if (setupMode) {
        await api.setup(setupToken, password);
        setSetupMode(false); setMessage("Owner created. Sign in to continue.");
      } else {
        await api.signIn(password, totpCode || undefined, recoveryCode || undefined);
        onSignedIn();
      }
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Request failed. Check the server and try again.");
    } finally { setBusy(false); }
  }

  return <main className="auth-shell">
    <section className="auth-card" aria-labelledby="auth-title">
      <div className="auth-mark"><ShieldCheck size={25} /><Badge variant="outline">Self-hosted</Badge></div>
      <h1 id="auth-title">{setupMode ? "Protect your Scout workspace" : "Sign in to Scout"}</h1>
      <p>{setupMode ? "Create the single owner account. The setup token is provisioned locally and expires." : "Your session protects inventory, credentials, policies, and agent actions."}</p>
      <form onSubmit={submit}>
        {setupMode && <label>One-time setup token<Input required type="password" autoComplete="one-time-code" value={setupToken} onChange={(event) => setSetupToken(event.target.value)} /></label>}
        <label>Password<Input required minLength={12} type="password" autoComplete={setupMode ? "new-password" : "current-password"} value={password} onChange={(event) => setPassword(event.target.value)} /></label>
        {!setupMode && <>
          <label>Authenticator code <span className="label-hint">if enabled</span><Input inputMode="numeric" autoComplete="one-time-code" value={totpCode} onChange={(event) => setTotpCode(event.target.value)} /></label>
          <label>Recovery code <span className="label-hint">alternative</span><Input value={recoveryCode} onChange={(event) => setRecoveryCode(event.target.value)} /></label>
        </>}
        {error && <p className="form-error" role="alert">{error}</p>}
        {message && <p className="form-success" role="status">{message}</p>}
        <Button type="submit" disabled={busy}>{busy ? "Working…" : setupMode ? <><KeyRound size={15} /> Create owner</> : <><LockKeyhole size={15} /> Sign in</>}</Button>
      </form>
      <button className="auth-switch" type="button" onClick={() => { setSetupMode(!setupMode); setError(""); }}>{setupMode ? "Owner already exists? Sign in" : "First run? Set up owner"}</button>
    </section>
  </main>;
}
