import { type FormEvent, useEffect, useMemo, useState } from "react";
import {
  AlertTriangle,
  Boxes,
  CheckCircle2,
  Clock3,
  ExternalLink,
  RefreshCw,
  Save,
  ShieldCheck,
  XCircle,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  APIError,
  api,
  type CollectorConfig,
  type CollectorDescriptor,
  type Device,
  type Incident,
  type ServiceEntity,
} from "@/lib/api";

function configFor(descriptor: CollectorDescriptor, config: CollectorConfig | undefined): Record<string, string> {
  const values: Record<string, string> = {};
  for (const key of Object.keys(descriptor.configSchema)) values[key] = config?.config?.[key] ?? "";
  return values;
}

function expectedPatterns(value: string | undefined): string[] {
  return (value ?? "")
    .split(",")
    .map((pattern) => pattern.trim())
    .filter(Boolean);
}

function formatDate(value: string | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "—" : date.toLocaleString();
}

function normalizedState(value: string): string {
  const state = value.trim().toLowerCase();
  return ["active", "inactive", "failed", "transitional", "unavailable"].includes(state) ? state : "unavailable";
}

function stateLabel(value: string): string {
  return normalizedState(value).replace(/^./, (letter) => letter.toUpperCase());
}

function freshness(item: ServiceEntity): "fresh" | "expiring" | "expired" | "unknown" {
  const observed = new Date(item.observedAt).valueOf();
  const expires = new Date(item.expiresAt).valueOf();
  if (!Number.isFinite(observed) || !Number.isFinite(expires)) return "unknown";
  if (expires <= Date.now()) return "expired";
  if (expires - Date.now() <= 30_000) return "expiring";
  return "fresh";
}

function freshnessLabel(value: ReturnType<typeof freshness>): string {
  switch (value) {
    case "fresh":
      return "Fresh inventory";
    case "expiring":
      return "Expiring soon";
    case "expired":
      return "Expired inventory";
    default:
      return "Freshness unavailable";
  }
}

function deviceName(deviceId: string | undefined, devices: Device[]): string {
  if (!deviceId) return "Unassociated host";
  return devices.find((device) => device.id === deviceId)?.displayName ?? `Device ${deviceId.slice(0, 8)}`;
}

function serviceMatchesExactPattern(patterns: string[], serviceName: string): boolean {
  return patterns.includes(serviceName);
}

export function ServicesView({
  onOpenIncident,
  deviceId: scopedDeviceId,
}: {
  onOpenIncident?: (incidentId: string) => void;
  deviceId?: string;
}) {
  const [items, setItems] = useState<ServiceEntity[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [descriptors, setDescriptors] = useState<CollectorDescriptor[]>([]);
  const [configs, setConfigs] = useState<CollectorConfig[]>([]);
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [provider, setProvider] = useState("");
  const [deviceId, setDeviceId] = useState(scopedDeviceId ?? "");
  const [filterDeviceId, setFilterDeviceId] = useState(scopedDeviceId ?? "");
  const [drafts, setDrafts] = useState<Record<string, Record<string, string>>>({});
  const [enabled, setEnabled] = useState<Record<string, boolean>>({});
  const [credentialRefs, setCredentialRefs] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  async function refreshEntities() {
    const result = await api.services(provider);
    setItems(result.items);
  }

  async function refreshCatalog() {
    const [descriptorList, deviceList] = await Promise.all([api.collectorDescriptors(), api.devices()]);
    setDescriptors(descriptorList.items);
    setDevices(deviceList.items);
    if (scopedDeviceId && deviceList.items.some((device) => device.id === scopedDeviceId)) {
      setDeviceId(scopedDeviceId);
    } else if (!deviceId && deviceList.items[0]) {
      setDeviceId(deviceList.items[0].id);
    }
  }

  async function refreshIncidents() {
    const result = await api.incidents("?status=active&limit=500");
    setIncidents(result.items);
  }

  async function refreshConfigs() {
    if (!deviceId) {
      setConfigs([]);
      return;
    }
    const result = await api.collectors(deviceId);
    setConfigs(result.items);
  }

  useEffect(() => {
    setError("");
    Promise.all([refreshEntities(), refreshCatalog(), refreshIncidents()]).catch((caught) => {
      setError(caught instanceof APIError ? caught.message : "Could not load service collectors");
    });
  }, [provider]);

  useEffect(() => {
    refreshConfigs().catch((caught) =>
      setError(caught instanceof APIError ? caught.message : "Could not load collector configuration"),
    );
  }, [deviceId]);

  useEffect(() => {
    if (!scopedDeviceId) return;
    setDeviceId(scopedDeviceId);
    setFilterDeviceId(scopedDeviceId);
  }, [scopedDeviceId]);

  const configByCollector = useMemo(() => new Map(configs.map((config) => [config.collectorId, config])), [configs]);

  const visibleItems = useMemo(
    () =>
      items
        .filter((item) => !filterDeviceId || item.deviceId === filterDeviceId)
        .sort((left, right) => left.name.localeCompare(right.name)),
    [filterDeviceId, items],
  );

  const systemdItems = useMemo(
    () =>
      items
        .filter((item) => item.provider === "systemd" && item.deviceId === deviceId)
        .sort((a, b) => a.name.localeCompare(b.name)),
    [deviceId, items],
  );

  const incidentsByEntity = useMemo(() => {
    const result = new Map<string, Incident[]>();
    for (const incident of incidents) {
      const key = `${incident.deviceId}\u0000${incident.entityId}`;
      result.set(key, [...(result.get(key) ?? []), incident]);
    }
    return result;
  }, [incidents]);

  function incidentsFor(item: ServiceEntity): Incident[] {
    if (!item.deviceId) return [];
    return [
      ...(incidentsByEntity.get(`${item.deviceId}\u0000${item.id}`) ?? []),
      ...(item.name !== item.id ? (incidentsByEntity.get(`${item.deviceId}\u0000${item.name}`) ?? []) : []),
    ];
  }

  function draftFor(descriptor: CollectorDescriptor) {
    return drafts[descriptor.id] ?? configFor(descriptor, configByCollector.get(descriptor.id));
  }

  function setDraft(descriptorId: string, key: string, value: string) {
    setDrafts((current) => ({
      ...current,
      [descriptorId]: { ...(current[descriptorId] ?? {}), [key]: value },
    }));
  }

  function toggleExpectedService(descriptor: CollectorDescriptor, serviceName: string, checked: boolean) {
    const values = draftFor(descriptor);
    const patterns = expectedPatterns(values.expectedRunning);
    const next = checked
      ? [...new Set([...patterns, serviceName])]
      : patterns.filter((pattern) => pattern !== serviceName);
    setDraft(descriptor.id, "expectedRunning", next.join(","));
  }

  async function saveCollector(event: FormEvent<HTMLFormElement>, descriptor: CollectorDescriptor) {
    event.preventDefault();
    const current = configByCollector.get(descriptor.id);
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await api.updateCollector(deviceId, descriptor.id, {
        provider: descriptor.provider,
        enabled: enabled[descriptor.id] ?? current?.enabled ?? false,
        config: draftFor(descriptor),
        credentialRef: credentialRefs[descriptor.id] || current?.credentialRef || undefined,
        expectedRevision: current?.revision || undefined,
      });
      setMessage(
        descriptor.id === "systemd"
          ? "Systemd selection saved. Failed units remain monitored automatically."
          : descriptor.provider + " collector configuration saved. Secrets stay in the credential broker.",
      );
      await refreshConfigs();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Collector configuration could not be saved");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="workspace-grid services-view">
      <div className="section-heading">
        <div>
          <Badge variant="outline">
            <ShieldCheck size={13} />
            Extensible collectors
          </Badge>
          <h2>Services</h2>
          <p>Collector health and service state.</p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Refresh services"
          onClick={() =>
            Promise.all([refreshEntities(), refreshCatalog(), refreshConfigs(), refreshIncidents()]).catch(() =>
              setError("Could not refresh services"),
            )
          }
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
      <div className="filter-row">
        <label>
          Provider
          <select value={provider} onChange={(event) => setProvider(event.target.value)}>
            <option value="">All providers</option>
            <option value="systemd">Systemd</option>
            <option value="docker">Docker</option>
            <option value="proxmox">Proxmox</option>
            <option value="fake">Fixture provider</option>
          </select>
        </label>
        {!scopedDeviceId && (
          <label>
            Device
            <select value={filterDeviceId} onChange={(event) => setFilterDeviceId(event.target.value)}>
              <option value="">All devices</option>
              {devices.map((device) => (
                <option key={device.id} value={device.id}>
                  {device.displayName}
                </option>
              ))}
            </select>
          </label>
        )}
        <div className="service-summary" aria-label="Service inventory summary">
          <Badge variant="outline">{visibleItems.length} observed</Badge>
          <Badge
            variant="outline"
            className={
              visibleItems.some((item) => normalizedState(item.status) === "failed") ? "service-state-badge failed" : ""
            }
          >
            {visibleItems.filter((item) => normalizedState(item.status) === "failed").length} failed
          </Badge>
        </div>
      </div>
      <div className="data-panel">
        <div className="section-heading">
          <div>
            <h3>Collector configuration</h3>
            <p>Credential references only. Provider secrets stay server-side.</p>
          </div>
          <Badge variant="outline">{descriptors.length}</Badge>
        </div>
        {!devices.length ? (
          <p className="empty-inline">Enroll a device before configuring service collectors.</p>
        ) : (
          <>
            {!scopedDeviceId && (
              <label className="device-selector">
                Device
                <select value={deviceId} onChange={(event) => setDeviceId(event.target.value)}>
                  {devices.map((device) => (
                    <option key={device.id} value={device.id}>
                      {device.displayName}
                    </option>
                  ))}
                </select>
              </label>
            )}
            <div className="collector-list">
              {descriptors
                .filter((descriptor) => descriptor.id !== "host")
                .map((descriptor) => {
                  const current = configByCollector.get(descriptor.id);
                  const values = draftFor(descriptor);
                  const collectorEnabled = enabled[descriptor.id] ?? current?.enabled ?? false;
                  return (
                    <form
                      className="collector-row"
                      key={descriptor.id}
                      onSubmit={(event) => saveCollector(event, descriptor)}
                    >
                      <div className="collector-heading">
                        <div>
                          <strong>{descriptor.provider}</strong>
                          <small>
                            {descriptor.id} · schema {descriptor.version}
                          </small>
                        </div>
                        <Badge
                          variant="outline"
                          className={current?.health === "degraded" ? "access-label" : "enabled-label"}
                        >
                          {current?.health ?? "detected"}
                        </Badge>
                      </div>
                      <label className="checkbox-label">
                        <input
                          type="checkbox"
                          checked={collectorEnabled}
                          onChange={(event) =>
                            setEnabled((state) => ({ ...state, [descriptor.id]: event.target.checked }))
                          }
                        />
                        <span>Enable {descriptor.provider} collection</span>
                      </label>
                      {Object.entries(descriptor.configSchema).map(([key, description]) =>
                        descriptor.id === "systemd" && key === "expectedRunning" ? (
                          <fieldset className="must-run-selector" key={key}>
                            <legend>Expected to stay active</legend>
                            <p>Warn after 60s inactivity. Failed units always alert.</p>
                            <label>
                              Service patterns
                              <span className="label-hint">{description}</span>
                              <Input
                                aria-label="Systemd expected-running patterns"
                                value={values[key] ?? ""}
                                onChange={(event) => setDraft(descriptor.id, key, event.target.value)}
                                placeholder="scout-agent.service, backup-*.service"
                              />
                            </label>
                            {systemdItems.length ? (
                              <div className="must-run-list">
                                {systemdItems.map((item) => {
                                  const patterns = expectedPatterns(values.expectedRunning);
                                  const checked = serviceMatchesExactPattern(patterns, item.name);
                                  return (
                                    <label className="checkbox-label" key={item.id}>
                                      <input
                                        type="checkbox"
                                        checked={checked}
                                        onChange={(event) =>
                                          toggleExpectedService(descriptor, item.name, event.target.checked)
                                        }
                                      />
                                      <span>
                                        {item.name}
                                        <small>
                                          {stateLabel(item.status)} · observed {formatDate(item.observedAt)}
                                        </small>
                                      </span>
                                    </label>
                                  );
                                })}
                              </div>
                            ) : (
                              <small className="empty-inline">
                                No loaded systemd units observed on this device yet.
                              </small>
                            )}
                          </fieldset>
                        ) : (
                          <label key={key}>
                            {key}
                            <span className="label-hint">{description}</span>
                            <Input
                              value={values[key] ?? ""}
                              onChange={(event) => setDraft(descriptor.id, key, event.target.value)}
                            />
                          </label>
                        ),
                      )}
                      <label>
                        Credential reference <span className="label-hint">optional; value is never a secret</span>
                        <Input
                          value={credentialRefs[descriptor.id] ?? current?.credentialRef ?? ""}
                          onChange={(event) =>
                            setCredentialRefs((state) => ({ ...state, [descriptor.id]: event.target.value }))
                          }
                        />
                      </label>
                      {current?.diagnostic && (
                        <p className="collector-diagnostic" role="status">
                          {current.diagnostic}
                        </p>
                      )}
                      <Button type="submit" size="sm" variant="outline" disabled={busy || !deviceId}>
                        <Save size={14} />
                        Save collector
                      </Button>
                    </form>
                  );
                })}
            </div>
          </>
        )}
      </div>
      {visibleItems.length ? (
        <section className="service-inventory" aria-labelledby="service-inventory-title">
          <div className="service-inventory-heading">
            <div>
              <Badge variant="outline">
                <Boxes size={13} />
                Observed inventory
              </Badge>
              <h3 id="service-inventory-title">Service state</h3>
              <p>Read-only source evidence.</p>
            </div>
            <small>{visibleItems.length} loaded entities</small>
          </div>
          <div className="service-inventory-list">
            {visibleItems.map((item) => {
              const state = normalizedState(item.status);
              const itemFreshness = freshness(item);
              const related = incidentsFor(item);
              return (
                <article
                  className="service-inventory-row"
                  key={item.provider + "-" + (item.deviceId ?? "") + "-" + item.id}
                >
                  <div className={`service-state ${state}`}>
                    {state === "active" ? (
                      <CheckCircle2 size={16} />
                    ) : state === "failed" ? (
                      <XCircle size={16} />
                    ) : (
                      <Clock3 size={16} />
                    )}
                    <span>{stateLabel(state)}</span>
                  </div>
                  <div className="service-identity">
                    <strong>{item.name}</strong>
                    <small>
                      {item.provider} · {item.kind}
                      {item.clusterId ? " · " + item.clusterId : ""}
                    </small>
                  </div>
                  <div className="service-host">
                    <span>{deviceName(item.deviceId, devices)}</span>
                    {item.labels?.mustRun === "true" && <small>Expected running</small>}
                  </div>
                  <div className={`service-freshness ${itemFreshness}`}>
                    <strong>{freshnessLabel(itemFreshness)}</strong>
                    <small>Observed {formatDate(item.observedAt)}</small>
                    <small>Expires {formatDate(item.expiresAt)}</small>
                  </div>
                  <div className="service-incident-cell">
                    {related.length && onOpenIncident ? (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => onOpenIncident(related[0].id)}
                        aria-label={`Open incident for ${item.name}`}
                      >
                        <AlertTriangle size={14} />
                        {related.length} {related.length === 1 ? "incident" : "incidents"}
                        <ExternalLink size={13} />
                      </Button>
                    ) : related.length ? (
                      <Badge variant="outline" className="service-state-badge failed">
                        <AlertTriangle size={13} />
                        {related.length} active
                      </Badge>
                    ) : (
                      <span className="service-clear-label">No active incident</span>
                    )}
                  </div>
                </article>
              );
            })}
          </div>
        </section>
      ) : (
        <div className="empty">
          <Boxes size={30} />
          <h2>No observed service entities</h2>
          <p>Enable the systemd collector.</p>
        </div>
      )}
    </section>
  );
}
