import { useEffect, useState } from "react";
import { AlertTriangle, CheckCircle2, RefreshCw, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { APIError, api } from "@/lib/api";

export function RecoveryView({ onChanged }: { onChanged: () => void }) {
  const [recovery, setRecovery] = useState<{
    recoveryMode: boolean;
    enrollmentPaused: boolean;
    updatesPaused: boolean;
  } | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .recoveryStatus()
      .then((result) => setRecovery(result.workspace))
      .catch((caught) => setError(caught instanceof APIError ? caught.message : "Could not load recovery state"));
  }, []);

  async function reconcile() {
    setBusy(true);
    setError("");
    try {
      await api.reconcileRecovery();
      onChanged();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Recovery reconciliation failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="recovery-panel" aria-labelledby="recovery-title">
      <div className="recovery-icon">
        <AlertTriangle size={22} />
      </div>
      <div className="recovery-copy">
        <div className="recovery-heading">
          <h2 id="recovery-title">Recovery mode</h2>
          <Badge variant="outline">Authority paused</Badge>
        </div>
        <p>
          Restored workspaces pause enrollment and signed updates until the owner reviews keys, policy revisions, and
          revoked identities.
        </p>
        <div className="recovery-state" aria-live="polite">
          <span>
            <ShieldCheck size={14} /> {recovery?.recoveryMode ? "Restore review required" : "Reconciliation completed"}
          </span>
          <span>{recovery?.enrollmentPaused ? "Enrollment paused" : "Enrollment ready"}</span>
          <span>{recovery?.updatesPaused ? "Updates paused" : "Updates ready"}</span>
        </div>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        {recovery?.recoveryMode ? (
          <Button onClick={reconcile} disabled={busy}>
            <RefreshCw size={15} />
            {busy ? "Reconciling…" : "Reconcile restored workspace"}
          </Button>
        ) : (
          <p className="form-success">
            <CheckCircle2 size={14} /> Policy and identity review recorded. Keep enrollment and updates paused until
            ready.
          </p>
        )}
      </div>
    </section>
  );
}
