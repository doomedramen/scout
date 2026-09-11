import { type FormEvent, useEffect, useMemo, useState } from "react";
import { AlertTriangle, BellRing, Check, Clock3, Plus, RefreshCw, Save, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  APIError,
  api,
  type AlertCondition,
  type AlertRule,
  type AlertRuleInput,
  type Device,
  type Incident,
  type IncidentTransition,
  type Site,
} from "@/lib/api";

const metricOptions = [
  "cpu.utilization",
  "cpu.user_percent",
  "cpu.system_percent",
  "cpu.iowait_percent",
  "cpu.steal_percent",
  "load.1m",
  "load.5m",
  "load.15m",
  "memory.used",
  "memory.capacity",
  "memory.used_percent",
  "filesystem.used",
  "filesystem.capacity",
  "filesystem.used_percent",
  "host.uptime",
  "network.receive_rate",
  "network.transmit_rate",
  "swap.used",
  "swap.capacity",
  "disk.read_rate",
  "disk.write_rate",
  "disk.utilization",
  "disk.read_latency",
  "disk.write_latency",
  "smart.temperature",
  "smart.wear_percent",
  "smart.error_count",
  "zfs.pool.used",
  "zfs.pool.capacity",
  "zfs.dataset.used",
  "zfs.dataset.available",
  "zfs.pool.read_rate",
  "zfs.pool.write_rate",
  "sensor.temperature",
  "sensor.fan_speed",
  "gpu.utilization",
  "gpu.memory.used",
  "gpu.memory.capacity",
  "gpu.temperature",
  "gpu.power",
] as const;

const stateOptions = [
  { value: "host_offline", label: "Host offline", clear: "online" },
  { value: "service_failed", label: "Service failed", clear: "active" },
  { value: "service_required_inactive", label: "Required service inactive", clear: "active" },
  { value: "collector_degraded", label: "Collector degraded", clear: "healthy" },
  { value: "hardware_fault", label: "Hardware fault", clear: "healthy" },
] as const;

type ViewMode = "incidents" | "rules";

type RuleDraft = {
  name: string;
  targetKind: AlertRule["targetKind"];
  targetId: string;
  kind: AlertRule["kind"];
  severity: AlertRule["severity"];
  enabled: boolean;
  metric: string;
  entityId: string;
  operator: "gt" | "lt";
  triggerValue: string;
  clearValue: string;
  triggerSeconds: string;
  clearSeconds: string;
  state: string;
  servicePattern: string;
  collectorId: string;
  minimumConsecutiveSamples: string;
};

const emptyRuleDraft: RuleDraft = {
  name: "",
  targetKind: "fleet",
  targetId: "",
  kind: "numeric",
  severity: "warning",
  enabled: true,
  metric: "cpu.utilization",
  entityId: "",
  operator: "gt",
  triggerValue: "90",
  clearValue: "85",
  triggerSeconds: "300",
  clearSeconds: "120",
  state: "host_offline",
  servicePattern: "",
  collectorId: "",
  minimumConsecutiveSamples: "1",
};

function draftFromRule(rule: AlertRule): RuleDraft {
  const condition = rule.condition;
  const numeric = "metric" in condition;
  return {
    name: rule.name,
    targetKind: rule.targetKind,
    targetId: rule.targetId ?? "",
    kind: rule.kind,
    severity: rule.severity,
    enabled: rule.enabled,
    metric: numeric ? condition.metric : emptyRuleDraft.metric,
    entityId: condition.entityId ?? "",
    operator: numeric ? condition.operator : emptyRuleDraft.operator,
    triggerValue: numeric ? String(condition.triggerValue) : emptyRuleDraft.triggerValue,
    clearValue: numeric ? String(condition.clearValue) : emptyRuleDraft.clearValue,
    triggerSeconds: String(condition.triggerSeconds),
    clearSeconds: String(condition.clearSeconds),
    state: numeric ? emptyRuleDraft.state : condition.state,
    servicePattern: numeric ? "" : (condition.servicePattern ?? ""),
    collectorId: numeric ? "" : (condition.collectorId ?? ""),
    minimumConsecutiveSamples: numeric
      ? emptyRuleDraft.minimumConsecutiveSamples
      : String(condition.minimumConsecutiveSamples ?? 1),
  };
}

function numberValue(value: string): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function ruleInputFromDraft(draft: RuleDraft): AlertRuleInput {
  const condition: AlertCondition =
    draft.kind === "numeric"
      ? {
          metric: draft.metric,
          ...(draft.entityId.trim() ? { entityId: draft.entityId.trim() } : {}),
          operator: draft.operator,
          triggerValue: numberValue(draft.triggerValue),
          clearValue: numberValue(draft.clearValue),
          triggerSeconds: numberValue(draft.triggerSeconds),
          clearSeconds: numberValue(draft.clearSeconds),
        }
      : {
          state: draft.state,
          ...(draft.entityId.trim() ? { entityId: draft.entityId.trim() } : {}),
          ...(draft.servicePattern.trim() ? { servicePattern: draft.servicePattern.trim() } : {}),
          ...(draft.collectorId.trim() ? { collectorId: draft.collectorId.trim() } : {}),
          triggerSeconds: numberValue(draft.triggerSeconds),
          clearSeconds: numberValue(draft.clearSeconds),
          minimumConsecutiveSamples: numberValue(draft.minimumConsecutiveSamples) || 1,
        };
  return {
    name: draft.name.trim(),
    targetKind: draft.targetKind,
    ...(draft.targetKind === "fleet" ? {} : { targetId: draft.targetId }),
    kind: draft.kind,
    severity: draft.severity,
    enabled: draft.enabled,
    condition,
  };
}

function formatDuration(seconds: number): string {
  if (seconds === 0) return "immediately";
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}

function formatDate(value: string | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "—" : date.toLocaleString();
}

function titleCase(value: string): string {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function targetLabel(
  kind: AlertRule["targetKind"],
  targetId: string | undefined,
  sites: Site[],
  devices: Device[],
): string {
  if (kind === "fleet") return "Entire fleet";
  if (kind === "site") return `Site · ${sites.find((site) => site.id === targetId)?.name ?? targetId ?? "unknown"}`;
  return `Device · ${devices.find((device) => device.id === targetId)?.displayName ?? targetId ?? "unknown"}`;
}

function conditionSummary(rule: AlertRule): string {
  if ("metric" in rule.condition) {
    const condition = rule.condition;
    return `${condition.metric} ${condition.operator === "gt" ? ">" : "<"} ${condition.triggerValue} for ${formatDuration(condition.triggerSeconds)}`;
  }
  return `${titleCase(rule.condition.state)} for ${formatDuration(rule.condition.triggerSeconds)}`;
}

function deviceLabel(deviceId: string, devices: Device[]): string {
  return devices.find((device) => device.id === deviceId)?.displayName ?? `Device ${deviceId.slice(0, 8)}`;
}

function caughtMessage(caught: unknown, fallback: string): string {
  return caught instanceof APIError ? (caught.details?.message ?? caught.message) : fallback;
}

function evidenceText(incident: Incident): string {
  if (incident.evidenceState === "fresh") return "The latest accepted sample supports this incident.";
  if (incident.evidenceState === "unknown")
    return "Scout cannot confirm the current state; the incident remains visible.";
  return "This condition is not supported by the current evidence source.";
}

export function IncidentsView({
  focusIncidentId = "",
  onFocusConsumed,
}: {
  focusIncidentId?: string;
  onFocusConsumed?: () => void;
}) {
  const [mode, setMode] = useState<ViewMode>("incidents");
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [rules, setRules] = useState<AlertRule[]>([]);
  const [sites, setSites] = useState<Site[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [statusFilter, setStatusFilter] = useState<"" | Incident["status"]>("active");
  const [severityFilter, setSeverityFilter] = useState<"" | AlertRule["severity"]>("");
  const [ackFilter, setAckFilter] = useState<"" | "true" | "false">("");
  const [selectedIncidentId, setSelectedIncidentId] = useState("");
  const [incidentDetail, setIncidentDetail] = useState<Incident | null>(null);
  const [transitions, setTransitions] = useState<IncidentTransition[]>([]);
  const [selectedRuleId, setSelectedRuleId] = useState("");
  const [ruleDraft, setRuleDraft] = useState<RuleDraft>(emptyRuleDraft);
  const [busy, setBusy] = useState<"refresh" | "acknowledge" | "save" | "retire" | "">("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  const selectedRule = useMemo(() => rules.find((rule) => rule.id === selectedRuleId), [rules, selectedRuleId]);

  async function refresh() {
    setBusy("refresh");
    setError("");
    try {
      const query = new URLSearchParams();
      if (statusFilter) query.set("status", statusFilter);
      if (severityFilter) query.set("severity", severityFilter);
      if (ackFilter) query.set("acknowledged", ackFilter);
      const suffix = query.toString() ? `?${query.toString()}` : "";
      const [incidentList, ruleList, siteList, deviceList] = await Promise.all([
        api.incidents(suffix),
        api.alertRules(),
        api.sites(),
        api.devices(),
      ]);
      setIncidents(incidentList.items);
      setRules(ruleList.items);
      setSites(siteList.items);
      setDevices(deviceList.items);
      const focusedIncident = focusIncidentId && incidentList.items.find((incident) => incident.id === focusIncidentId);
      setSelectedIncidentId((current) =>
        focusedIncident
          ? focusedIncident.id
          : current && incidentList.items.some((incident) => incident.id === current)
            ? current
            : (incidentList.items[0]?.id ?? ""),
      );
      setSelectedRuleId((current) =>
        current && ruleList.items.some((rule) => rule.id === current) ? current : (ruleList.items[0]?.id ?? ""),
      );
      if (focusedIncident) {
        setMode("incidents");
        onFocusConsumed?.();
      }
    } catch (caught) {
      setError(caughtMessage(caught, "Could not load incident monitoring"));
    } finally {
      setBusy("");
    }
  }

  useEffect(() => {
    void refresh();
  }, [statusFilter, severityFilter, ackFilter, focusIncidentId]);

  useEffect(() => {
    let cancelled = false;
    if (!selectedIncidentId) {
      setIncidentDetail(null);
      setTransitions([]);
      return () => {
        cancelled = true;
      };
    }
    Promise.all([api.incident(selectedIncidentId), api.incidentTransitions(selectedIncidentId)])
      .then(([detail, transitionPage]) => {
        if (!cancelled) {
          setIncidentDetail(detail);
          setTransitions(transitionPage.items);
        }
      })
      .catch((caught) => {
        if (!cancelled) setError(caughtMessage(caught, "Could not load incident detail"));
      });
    return () => {
      cancelled = true;
    };
  }, [selectedIncidentId]);

  useEffect(() => {
    setRuleDraft(selectedRule ? draftFromRule(selectedRule) : emptyRuleDraft);
  }, [selectedRule]);

  async function acknowledge() {
    if (!incidentDetail || incidentDetail.status !== "active" || incidentDetail.acknowledgedAt) return;
    setBusy("acknowledge");
    setError("");
    setMessage("");
    try {
      const [updated, transitionPage] = await Promise.all([
        api.acknowledgeIncident(incidentDetail.id, incidentDetail.revision),
        api.incidentTransitions(incidentDetail.id),
      ]);
      setIncidentDetail(updated);
      setTransitions(transitionPage.items);
      setIncidents((current) => current.map((incident) => (incident.id === updated.id ? updated : incident)));
      setMessage("Incident acknowledged. Monitoring continues until evidence recovers or the rule changes.");
    } catch (caught) {
      setError(caughtMessage(caught, "Incident could not be acknowledged; refresh and retry"));
    } finally {
      setBusy("");
    }
  }

  async function saveRule(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy("save");
    setError("");
    setMessage("");
    try {
      const input = ruleInputFromDraft(ruleDraft);
      const saved = selectedRule
        ? await api.updateAlertRule(selectedRule.id, {
            expectedRevision: selectedRule.revision,
            name: input.name,
            severity: input.severity,
            enabled: input.enabled,
            condition: input.condition,
          })
        : await api.createAlertRule(input);
      setRules((current) => {
        const existing = current.some((rule) => rule.id === saved.id);
        return existing ? current.map((rule) => (rule.id === saved.id ? saved : rule)) : [saved, ...current];
      });
      setSelectedRuleId(saved.id);
      setRuleDraft(draftFromRule(saved));
      setMessage(selectedRule ? "Alert rule updated." : "Alert rule created.");
    } catch (caught) {
      setError(caughtMessage(caught, "Alert rule could not be saved"));
    } finally {
      setBusy("");
    }
  }

  async function retireRule() {
    if (!selectedRule || !window.confirm(`Retire “${selectedRule.name}”? Existing incident history stays available.`)) {
      return;
    }
    setBusy("retire");
    setError("");
    setMessage("");
    try {
      await api.retireAlertRule(selectedRule.id, selectedRule.revision);
      setRules((current) => current.filter((rule) => rule.id !== selectedRule.id));
      setSelectedRuleId("");
      setMessage("Alert rule retired. Existing incidents remain immutable history.");
    } catch (caught) {
      setError(caughtMessage(caught, "Alert rule could not be retired; refresh and retry"));
    } finally {
      setBusy("");
    }
  }

  function newRule() {
    setSelectedRuleId("");
    setRuleDraft(emptyRuleDraft);
    setError("");
    setMessage("");
  }

  return (
    <section className="workspace-grid incidents-view">
      <div className="section-heading">
        <div>
          <Badge variant="outline">
            <BellRing size={13} />
            Evidence-led monitoring
          </Badge>
          <h2>Incidents and alert rules</h2>
          <p>
            Investigate durable incident episodes, acknowledge owner response, and keep every rule revision visible.
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Refresh incident monitoring"
          disabled={busy === "refresh"}
          onClick={() => void refresh()}
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
      <div className="incident-tabs" role="tablist" aria-label="Incident monitoring views">
        <button
          id="incidents-tab"
          role="tab"
          aria-selected={mode === "incidents"}
          className={mode === "incidents" ? "active" : ""}
          onClick={() => setMode("incidents")}
        >
          <AlertTriangle size={15} />
          Incidents
          <Badge variant="outline">{incidents.length}</Badge>
        </button>
        <button
          id="rules-tab"
          role="tab"
          aria-selected={mode === "rules"}
          className={mode === "rules" ? "active" : ""}
          onClick={() => setMode("rules")}
        >
          <ShieldCheck size={15} />
          Rules
          <Badge variant="outline">{rules.length}</Badge>
        </button>
      </div>

      {mode === "incidents" ? (
        <div className="incident-layout" role="tabpanel" aria-labelledby="incidents-tab">
          <section className="data-panel incident-rail" aria-labelledby="incident-queue-title">
            <div className="section-heading">
              <div>
                <h3 id="incident-queue-title">Attention queue</h3>
                <p>Episodes stay visible until evidence or policy closes them.</p>
              </div>
              <Badge variant="outline">{incidents.length}</Badge>
            </div>
            <div className="incident-filters">
              <label>
                Status
                <select
                  value={statusFilter}
                  onChange={(event) => setStatusFilter(event.target.value as typeof statusFilter)}
                >
                  <option value="active">Active</option>
                  <option value="">All statuses</option>
                  <option value="resolved">Resolved</option>
                  <option value="closed">Closed</option>
                </select>
              </label>
              <label>
                Severity
                <select
                  value={severityFilter}
                  onChange={(event) => setSeverityFilter(event.target.value as typeof severityFilter)}
                >
                  <option value="">All severities</option>
                  <option value="critical">Critical</option>
                  <option value="warning">Warning</option>
                </select>
              </label>
              <label>
                Acknowledgment
                <select value={ackFilter} onChange={(event) => setAckFilter(event.target.value as typeof ackFilter)}>
                  <option value="">Any response</option>
                  <option value="false">Needs acknowledgment</option>
                  <option value="true">Acknowledged</option>
                </select>
              </label>
            </div>
            {incidents.length ? (
              <div className="incident-list">
                {incidents.map((incident) => (
                  <button
                    type="button"
                    className={`incident-row ${selectedIncidentId === incident.id ? "selected" : ""}`}
                    aria-pressed={selectedIncidentId === incident.id}
                    key={incident.id}
                    onClick={() => setSelectedIncidentId(incident.id)}
                  >
                    <span className={`incident-marker ${incident.severity}`} aria-hidden="true" />
                    <span className="incident-row-copy">
                      <span className="incident-row-heading">
                        <strong>{incident.ruleSnapshot.name}</strong>
                        <Badge variant="outline" className={`severity-label ${incident.severity}`}>
                          {incident.severity}
                        </Badge>
                      </span>
                      <small>
                        {deviceLabel(incident.deviceId, devices)} · {titleCase(incident.status)} ·{" "}
                        {incident.evidenceState}
                      </small>
                    </span>
                    <span className="incident-row-time">{formatDate(incident.observedAt)}</span>
                  </button>
                ))}
              </div>
            ) : (
              <div className="incident-empty">
                <Check size={22} />
                <strong>No incidents match these filters.</strong>
                <p>When monitoring has evidence to act on, Scout will keep the episode here until it is resolved.</p>
              </div>
            )}
          </section>

          <section className="data-panel incident-detail" aria-labelledby="incident-detail-title">
            {incidentDetail ? (
              <>
                <div className="incident-detail-heading">
                  <div>
                    <div className="incident-detail-kicker">
                      <span className={`incident-marker ${incidentDetail.severity}`} aria-hidden="true" />
                      <span>{incidentDetail.severity} incident</span>
                      <span className="incident-revision">revision {incidentDetail.revision}</span>
                    </div>
                    <h3 id="incident-detail-title">{incidentDetail.ruleSnapshot.name}</h3>
                    <p>
                      {deviceLabel(incidentDetail.deviceId, devices)} · entity {incidentDetail.entityId}
                    </p>
                  </div>
                  <Badge variant="outline" className={`incident-status ${incidentDetail.status}`}>
                    {titleCase(incidentDetail.status)}
                  </Badge>
                </div>
                <div className={`evidence-banner ${incidentDetail.evidenceState}`}>
                  <div>
                    <span className="evidence-label">Evidence {incidentDetail.evidenceState}</span>
                    <strong>{evidenceText(incidentDetail)}</strong>
                  </div>
                  <Badge variant="outline">
                    <Clock3 size={13} />
                    observed {formatDate(incidentDetail.observedAt)}
                  </Badge>
                </div>
                <dl className="incident-facts">
                  <div>
                    <dt>Opened</dt>
                    <dd>{formatDate(incidentDetail.openedAt)}</dd>
                  </div>
                  <div>
                    <dt>Condition</dt>
                    <dd>
                      {conditionSummary({
                        ...incidentDetail.ruleSnapshot,
                        id: "",
                        revision: 0,
                        severity: incidentDetail.severity,
                        enabled: true,
                        targetKind: "fleet",
                        createdAt: "",
                        updatedAt: "",
                      })}
                    </dd>
                  </div>
                  <div>
                    <dt>Owner response</dt>
                    <dd>
                      {incidentDetail.acknowledgedAt
                        ? `Acknowledged ${formatDate(incidentDetail.acknowledgedAt)}`
                        : "Not acknowledged"}
                    </dd>
                  </div>
                  <div>
                    <dt>Notifications</dt>
                    <dd>{incidentDetail.notificationSuppression.suppressed ? "Suppressed" : "Not configured"}</dd>
                  </div>
                </dl>
                {incidentDetail.evidence && (
                  <div className="incident-evidence-block">
                    <div className="panel-title">
                      <AlertTriangle size={16} />
                      <h3>Observed evidence</h3>
                    </div>
                    <dl className="evidence-grid">
                      {incidentDetail.evidence.value !== undefined && (
                        <div>
                          <dt>Value</dt>
                          <dd>
                            {incidentDetail.evidence.value ?? "—"} {incidentDetail.evidence.unit ?? ""}
                          </dd>
                        </div>
                      )}
                      {incidentDetail.evidence.threshold !== undefined && (
                        <div>
                          <dt>Threshold</dt>
                          <dd>{incidentDetail.evidence.threshold ?? "—"}</dd>
                        </div>
                      )}
                      {incidentDetail.evidence.rawState && (
                        <div>
                          <dt>Raw state</dt>
                          <dd>{titleCase(incidentDetail.evidence.rawState)}</dd>
                        </div>
                      )}
                      <div>
                        <dt>Source</dt>
                        <dd>{incidentDetail.evidence.source ?? "Unknown"}</dd>
                      </div>
                    </dl>
                  </div>
                )}
                <div className="incident-response">
                  <div>
                    <strong>Acknowledgment is not recovery</strong>
                    <p>
                      Record that the owner has seen the episode. Scout keeps evaluating evidence in the background.
                    </p>
                  </div>
                  <Button
                    variant="outline"
                    disabled={
                      busy === "acknowledge" ||
                      incidentDetail.status !== "active" ||
                      Boolean(incidentDetail.acknowledgedAt)
                    }
                    onClick={() => void acknowledge()}
                  >
                    <Check size={15} />
                    {incidentDetail.acknowledgedAt ? "Acknowledged" : "Acknowledge incident"}
                  </Button>
                </div>
                <div className="incident-history">
                  <div className="section-heading">
                    <div>
                      <h3>Incident history</h3>
                      <p>Append-only transitions show what Scout knew and when.</p>
                    </div>
                    <Badge variant="outline">{transitions.length}</Badge>
                  </div>
                  {transitions.length ? (
                    <ol className="incident-timeline">
                      {transitions.map((transition) => (
                        <li key={transition.id}>
                          <span className="timeline-marker" aria-hidden="true">
                            <Check size={12} />
                          </span>
                          <div>
                            <strong>{titleCase(transition.kind)}</strong>
                            <small>
                              {formatDate(transition.occurredAt)} · revision {transition.effectiveRevision}
                              {transition.actor ? ` · ${transition.actor}` : ""}
                            </small>
                          </div>
                        </li>
                      ))}
                    </ol>
                  ) : (
                    <p className="empty-inline">No transitions recorded.</p>
                  )}
                </div>
              </>
            ) : (
              <div className="incident-empty detail-empty">
                <BellRing size={25} />
                <strong>Select an incident to inspect its evidence.</strong>
                <p>
                  Scout keeps alert context and owner actions together so an acknowledgment never hides the episode.
                </p>
              </div>
            )}
          </section>
        </div>
      ) : (
        <div className="rules-layout" role="tabpanel" aria-labelledby="rules-tab">
          <section className="data-panel rule-list-panel" aria-labelledby="rule-list-title">
            <div className="section-heading">
              <div>
                <h3 id="rule-list-title">Alert rules</h3>
                <p>Fleet defaults and owner-authored rules share the same revisioned evaluator.</p>
              </div>
              <Button size="sm" variant="outline" onClick={newRule}>
                <Plus size={14} />
                New rule
              </Button>
            </div>
            {rules.length ? (
              <div className="rule-list">
                {rules.map((rule) => (
                  <button
                    type="button"
                    className={`rule-row ${selectedRuleId === rule.id ? "selected" : ""}`}
                    aria-pressed={selectedRuleId === rule.id}
                    key={rule.id}
                    onClick={() => setSelectedRuleId(rule.id)}
                  >
                    <span className="rule-row-copy">
                      <span className="rule-row-heading">
                        <strong>{rule.name}</strong>
                        <Badge variant="outline" className={`severity-label ${rule.severity}`}>
                          {rule.severity}
                        </Badge>
                      </span>
                      <small>
                        {targetLabel(rule.targetKind, rule.targetId, sites, devices)} · {conditionSummary(rule)}
                      </small>
                    </span>
                    <Badge variant="outline" className={rule.enabled ? "enabled-label" : "access-label"}>
                      {rule.enabled ? "Enabled" : "Disabled"}
                    </Badge>
                  </button>
                ))}
              </div>
            ) : (
              <p className="empty-inline">No custom rules yet. The automatic baseline is explained below.</p>
            )}
          </section>

          <form className="form-panel rule-editor" onSubmit={saveRule}>
            <div className="section-heading">
              <div>
                <Badge variant="outline">
                  <ShieldCheck size={13} />
                  {selectedRule ? `Revision ${selectedRule.revision}` : "Owner authored"}
                </Badge>
                <h3>Rule editor</h3>
                <p>Start with a fleet rule. Site and device targets narrow scope without changing lineage.</p>
              </div>
              {selectedRule && (
                <Button type="button" variant="ghost" size="sm" onClick={newRule} disabled={busy !== ""}>
                  Clear
                </Button>
              )}
            </div>
            <div className="rule-editor-fields">
              <label>
                Rule name
                <Input
                  required
                  maxLength={120}
                  value={ruleDraft.name}
                  onChange={(event) => setRuleDraft({ ...ruleDraft, name: event.target.value })}
                />
              </label>
              <div className="rule-editor-grid">
                <label>
                  Target
                  <select
                    value={ruleDraft.targetKind}
                    disabled={Boolean(selectedRule)}
                    onChange={(event) =>
                      setRuleDraft({
                        ...ruleDraft,
                        targetKind: event.target.value as RuleDraft["targetKind"],
                        targetId: "",
                      })
                    }
                  >
                    <option value="fleet">Entire fleet</option>
                    <option value="site">Site</option>
                    <option value="device">Device</option>
                  </select>
                </label>
                {ruleDraft.targetKind !== "fleet" && (
                  <label>
                    Target {ruleDraft.targetKind === "site" ? "site" : "device"}
                    <select
                      required
                      value={ruleDraft.targetId}
                      disabled={Boolean(selectedRule)}
                      onChange={(event) => setRuleDraft({ ...ruleDraft, targetId: event.target.value })}
                    >
                      <option value="">Choose a {ruleDraft.targetKind}</option>
                      {ruleDraft.targetKind === "site"
                        ? sites.map((site) => (
                            <option key={site.id} value={site.id}>
                              {site.name}
                            </option>
                          ))
                        : devices.map((device) => (
                            <option key={device.id} value={device.id}>
                              {device.displayName}
                            </option>
                          ))}
                    </select>
                  </label>
                )}
                <label>
                  Condition type
                  <select
                    value={ruleDraft.kind}
                    disabled={Boolean(selectedRule)}
                    onChange={(event) => setRuleDraft({ ...ruleDraft, kind: event.target.value as RuleDraft["kind"] })}
                  >
                    <option value="numeric">Numeric metric</option>
                    <option value="state">State change</option>
                  </select>
                </label>
                <label>
                  Severity
                  <select
                    value={ruleDraft.severity}
                    onChange={(event) =>
                      setRuleDraft({ ...ruleDraft, severity: event.target.value as RuleDraft["severity"] })
                    }
                  >
                    <option value="warning">Warning</option>
                    <option value="critical">Critical</option>
                  </select>
                </label>
              </div>
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={ruleDraft.enabled}
                  onChange={(event) => setRuleDraft({ ...ruleDraft, enabled: event.target.checked })}
                />
                <span>Enable rule immediately</span>
              </label>
              <div className="rule-condition-heading">
                <div>
                  <h4>{ruleDraft.kind === "numeric" ? "Metric condition" : "State condition"}</h4>
                  <p>Durations bound how long evidence must persist before opening or clearing an episode.</p>
                </div>
              </div>
              {ruleDraft.kind === "numeric" ? (
                <div className="rule-editor-grid numeric-condition">
                  <label>
                    Metric
                    <select
                      value={ruleDraft.metric}
                      onChange={(event) => setRuleDraft({ ...ruleDraft, metric: event.target.value })}
                    >
                      {metricOptions.map((metric) => (
                        <option key={metric} value={metric}>
                          {metric}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label>
                    Operator
                    <select
                      value={ruleDraft.operator}
                      onChange={(event) =>
                        setRuleDraft({ ...ruleDraft, operator: event.target.value as RuleDraft["operator"] })
                      }
                    >
                      <option value="gt">Greater than</option>
                      <option value="lt">Less than</option>
                    </select>
                  </label>
                  <label>
                    Trigger value
                    <Input
                      required
                      type="number"
                      step="any"
                      value={ruleDraft.triggerValue}
                      onChange={(event) => setRuleDraft({ ...ruleDraft, triggerValue: event.target.value })}
                    />
                  </label>
                  <label>
                    Clear value <span className="label-hint">hysteresis</span>
                    <Input
                      required
                      type="number"
                      step="any"
                      value={ruleDraft.clearValue}
                      onChange={(event) => setRuleDraft({ ...ruleDraft, clearValue: event.target.value })}
                    />
                  </label>
                </div>
              ) : (
                <div className="rule-editor-grid state-condition">
                  <label>
                    Trigger state
                    <select
                      value={ruleDraft.state}
                      onChange={(event) => setRuleDraft({ ...ruleDraft, state: event.target.value })}
                    >
                      {stateOptions.map((state) => (
                        <option key={state.value} value={state.value}>
                          {state.label}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label>
                    Consecutive samples
                    <Input
                      required
                      type="number"
                      min="1"
                      max="10"
                      value={ruleDraft.minimumConsecutiveSamples}
                      onChange={(event) =>
                        setRuleDraft({ ...ruleDraft, minimumConsecutiveSamples: event.target.value })
                      }
                    />
                  </label>
                  <label>
                    Service pattern <span className="label-hint">optional</span>
                    <Input
                      value={ruleDraft.servicePattern}
                      onChange={(event) => setRuleDraft({ ...ruleDraft, servicePattern: event.target.value })}
                    />
                  </label>
                  <label>
                    Collector id <span className="label-hint">optional</span>
                    <Input
                      value={ruleDraft.collectorId}
                      onChange={(event) => setRuleDraft({ ...ruleDraft, collectorId: event.target.value })}
                    />
                  </label>
                  <small className="rule-condition-note">
                    Clears when the source returns to{" "}
                    {stateOptions.find((state) => state.value === ruleDraft.state)?.clear ?? "healthy"}.
                  </small>
                </div>
              )}
              <div className="rule-editor-grid">
                <label>
                  Trigger after (seconds)
                  <Input
                    required
                    type="number"
                    min="0"
                    max="86400"
                    value={ruleDraft.triggerSeconds}
                    onChange={(event) => setRuleDraft({ ...ruleDraft, triggerSeconds: event.target.value })}
                  />
                </label>
                <label>
                  Clear after (seconds)
                  <Input
                    required
                    type="number"
                    min="0"
                    max="86400"
                    value={ruleDraft.clearSeconds}
                    onChange={(event) => setRuleDraft({ ...ruleDraft, clearSeconds: event.target.value })}
                  />
                </label>
                <label>
                  Entity id <span className="label-hint">optional; provider-specific</span>
                  <Input
                    value={ruleDraft.entityId}
                    onChange={(event) => setRuleDraft({ ...ruleDraft, entityId: event.target.value })}
                  />
                </label>
              </div>
            </div>
            <div className="rule-editor-actions">
              {selectedRule && (
                <Button type="button" variant="ghost" disabled={busy !== ""} onClick={() => void retireRule()}>
                  Retire rule
                </Button>
              )}
              <Button type="submit" disabled={busy !== ""}>
                <Save size={15} />
                {busy === "save" ? "Saving…" : selectedRule ? "Save revision" : "Create rule"}
              </Button>
            </div>
          </form>
        </div>
      )}

      <section className="data-panel defaults-panel" aria-labelledby="automatic-defaults-title">
        <div className="section-heading">
          <div>
            <Badge variant="outline">
              <ShieldCheck size={13} />
              Automatic defaults
            </Badge>
            <h3 id="automatic-defaults-title">A useful baseline starts enabled</h3>
            <p>
              Scout seeds owner-visible fleet rules for every enrolled host. They are explicit, revisioned, and safe to
              tune.
            </p>
          </div>
        </div>
        <div className="defaults-list">
          <div>
            <strong>Host offline</strong>
            <span>Critical · offline for 90s · clears when online</span>
          </div>
          <div>
            <strong>CPU, memory, and filesystem high</strong>
            <span>Warning · above 90% for 5m · clears below 85% for 2m</span>
          </div>
          <div>
            <strong>Failed units and storage faults</strong>
            <span>Critical state evidence · closes only when the source recovers or policy changes</span>
          </div>
          <div>
            <strong>Degraded collectors</strong>
            <span>Warning · requires two consecutive samples · host monitoring remains independent</span>
          </div>
        </div>
        <p className="form-help">
          Defaults do not send external notifications until a destination is explicitly configured.
        </p>
      </section>
    </section>
  );
}
