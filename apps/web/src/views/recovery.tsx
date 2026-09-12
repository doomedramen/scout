import { useEffect, useState, type FormEvent } from "react";
import { AlertTriangle, CheckCircle2, Gauge, RefreshCw, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { APIError, api, type MonitoringSettings, type MonitoringStatus, type RetentionPreview } from "@/lib/api";

type RecoveryState = {
  recoveryMode: boolean;
  enrollmentPaused: boolean;
  updatesPaused: boolean;
};

type RetentionDraft = MonitoringSettings["retention"];

function formatBytes(value: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let scaled = Math.max(value, 0);
  let unit = 0;
  while (scaled >= 1024 && unit < units.length - 1) {
    scaled /= 1024;
    unit += 1;
  }
  return `${scaled >= 10 || unit === 0 ? scaled.toFixed(0) : scaled.toFixed(1)} ${units[unit]}`;
}

function formatLag(seconds: number): string {
  if (seconds < 1) return "Current";
  if (seconds < 60) return `${Math.round(seconds)}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  return `${(seconds / 3600).toFixed(1)}h`;
}

function tierLabel(tier: RetentionPreview["affectedRanges"][number]["tier"]): string {
  if (tier === "fiveMinute") return "Five-minute history";
  if (tier === "hourly") return "Hourly history";
  return "Raw history";
}

function formatPolicyError(caught: unknown, fallback: string): string {
  return caught instanceof APIError ? caught.message : fallback;
}

export function RecoveryView({ onChanged }: { onChanged: () => void }) {
  const [recovery, setRecovery] = useState<RecoveryState | null>(null);
  const [settings, setSettings] = useState<MonitoringSettings | null>(null);
  const [status, setStatus] = useState<MonitoringStatus | null>(null);
  const [draft, setDraft] = useState<RetentionDraft | null>(null);
  const [preview, setPreview] = useState<RetentionPreview | null>(null);
  const [error, setError] = useState("");
  const [policyError, setPolicyError] = useState("");
  const [busy, setBusy] = useState(false);
  const [policyBusy, setPolicyBusy] = useState<"preview" | "apply" | "">("");

  useEffect(() => {
    let cancelled = false;
    api
      .recoveryStatus()
      .then((result) => {
        if (!cancelled) setRecovery(result.workspace);
      })
      .catch((caught) => {
        if (!cancelled) setError(formatPolicyError(caught, "Could not load recovery state"));
      });
    api
      .monitoringSettings()
      .then((result) => {
        if (cancelled) return;
        setSettings(result);
        setDraft(result.retention);
      })
      .catch((caught) => {
        if (!cancelled) setPolicyError(formatPolicyError(caught, "Could not load monitoring settings"));
      });
    api
      .monitoringStatus()
      .then((result) => {
        if (!cancelled) setStatus(result);
      })
      .catch((caught) => {
        if (!cancelled) setPolicyError(formatPolicyError(caught, "Could not load monitoring health"));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function reconcile() {
    setBusy(true);
    setError("");
    try {
      await api.reconcileRecovery();
      onChanged();
    } catch (caught) {
      setError(formatPolicyError(caught, "Recovery reconciliation failed"));
    } finally {
      setBusy(false);
    }
  }

  function changeRetention(field: keyof RetentionDraft, value: string) {
    setDraft((current) => (current ? { ...current, [field]: Number(value) } : current));
    setPreview(null);
    setPolicyError("");
  }

  function hasRetentionReduction(): boolean {
    if (!settings || !draft) return false;
    return (
      draft.rawDays < settings.retention.rawDays ||
      draft.fiveMinuteDays < settings.retention.fiveMinuteDays ||
      draft.hourlyDays < settings.retention.hourlyDays
    );
  }

  async function previewRetention(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!settings || !draft || !hasRetentionReduction()) return;
    setPolicyBusy("preview");
    setPolicyError("");
    try {
      const key = `retention-${settings.revision}-${draft.rawDays}-${draft.fiveMinuteDays}-${draft.hourlyDays}`;
      const result = await api.retentionPreview({ expectedRevision: settings.revision, retention: draft }, key);
      setPreview(result);
    } catch (caught) {
      setPolicyError(formatPolicyError(caught, "Could not preview retention change"));
    } finally {
      setPolicyBusy("");
    }
  }

  async function applyRetention() {
    if (!settings || !draft || !preview) return;
    setPolicyBusy("apply");
    setPolicyError("");
    try {
      const result = await api.updateMonitoringSettings({
        expectedRevision: settings.revision,
        retention: draft,
        retentionPreviewId: preview.previewId,
      });
      setSettings(result);
      setDraft(result.retention);
      setPreview(null);
      setStatus(await api.monitoringStatus());
    } catch (caught) {
      setPolicyError(formatPolicyError(caught, "Could not apply retention change"));
    } finally {
      setPolicyBusy("");
    }
  }

  return (
    <>
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
              <ShieldCheck size={14} />{" "}
              {recovery?.recoveryMode ? "Restore review required" : "Reconciliation completed"}
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

      <section className="recovery-monitoring" aria-labelledby="monitoring-health-title">
        <div className="recovery-monitoring-heading">
          <div>
            <Badge variant="outline">
              <Gauge size={13} /> Monitoring health
            </Badge>
            <h2 id="monitoring-health-title">History and collection</h2>
            <p>Queues and storage are bounded; gaps remain visible while rollups catch up.</p>
          </div>
          {status && (
            <span className={`monitoring-pressure ${status.storagePressure.state}`}>
              {status.storagePressure.percent.toFixed(0)}% used · {status.storagePressure.state}
            </span>
          )}
        </div>
        <dl className="monitoring-health-grid" aria-live="polite">
          <div>
            <dt>Evaluation</dt>
            <dd>
              {status ? `${formatLag(status.evaluationLagSeconds)} · ${status.queues.evaluation} queued` : "Loading…"}
            </dd>
          </div>
          <div>
            <dt>Rollups</dt>
            <dd>{status ? `${formatLag(status.rollupLagSeconds)} · ${status.queues.rollup} queued` : "Loading…"}</dd>
          </div>
          <div>
            <dt>Notifications</dt>
            <dd>
              {status
                ? `${status.queues.notifications} queued · ${status.counters.deliveryFailures} failed`
                : "Loading…"}
            </dd>
          </div>
          <div>
            <dt>Storage</dt>
            <dd>
              {status
                ? `${formatBytes(status.storagePressure.usedBytes)} / ${formatBytes(status.storagePressure.budgetBytes)}`
                : "Loading…"}
            </dd>
          </div>
        </dl>
        {status &&
          (status.counters.dropped > 0 || status.counters.truncated > 0 || status.counters.backpressure > 0) && (
            <p className="monitoring-health-note" role="status">
              {status.counters.dropped} dropped · {status.counters.truncated} truncated · {status.counters.backpressure}{" "}
              backpressure events
            </p>
          )}
      </section>

      <section className="retention-policy" aria-labelledby="retention-policy-title">
        <div className="retention-policy-heading">
          <div>
            <Badge variant="outline">Retention policy</Badge>
            <h2 id="retention-policy-title">Keep the history you need</h2>
            <p>Shortening retention is permanent. Scout previews the affected range before applying it.</p>
          </div>
          {settings && <small>Revision {settings.revision}</small>}
        </div>
        {policyError && (
          <p className="form-error" role="alert">
            {policyError}
          </p>
        )}
        <form className="retention-form" onSubmit={previewRetention}>
          <label>
            Raw data (days)
            <input
              type="number"
              min={1}
              max={settings?.retention.rawDays ?? 30}
              value={draft?.rawDays ?? ""}
              onChange={(event) => changeRetention("rawDays", event.target.value)}
              disabled={!settings || policyBusy !== ""}
            />
          </label>
          <label>
            Five-minute (days)
            <input
              type="number"
              min={1}
              max={settings?.retention.fiveMinuteDays ?? 90}
              value={draft?.fiveMinuteDays ?? ""}
              onChange={(event) => changeRetention("fiveMinuteDays", event.target.value)}
              disabled={!settings || policyBusy !== ""}
            />
          </label>
          <label>
            Hourly (days)
            <input
              type="number"
              min={1}
              max={settings?.retention.hourlyDays ?? 365}
              value={draft?.hourlyDays ?? ""}
              onChange={(event) => changeRetention("hourlyDays", event.target.value)}
              disabled={!settings || policyBusy !== ""}
            />
          </label>
          <Button type="submit" variant="outline" disabled={!settings || !hasRetentionReduction() || policyBusy !== ""}>
            {policyBusy === "preview" ? "Preparing preview…" : "Preview retention change"}
          </Button>
        </form>
        {preview && (
          <div className="retention-preview" role="status">
            <div>
              <strong>Preview ready</strong>
              <p>
                Approximately {preview.estimatedRows} row{preview.estimatedRows === 1 ? "" : "s"} in the affected range
                will be eligible for deletion. This preview expires at{" "}
                {new Date(preview.expiresAt).toLocaleTimeString()}.
              </p>
            </div>
            <div className="retention-ranges">
              {preview.affectedRanges.map((range) => (
                <span key={range.tier}>
                  {tierLabel(range.tier)}: {new Date(range.after).toLocaleDateString()} –{" "}
                  {new Date(range.before).toLocaleDateString()}
                </span>
              ))}
            </div>
            <Button onClick={applyRetention} disabled={policyBusy !== ""}>
              {policyBusy === "apply" ? "Applying…" : "Apply retention reduction"}
            </Button>
          </div>
        )}
      </section>
    </>
  );
}
