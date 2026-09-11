import { type FormEvent, useEffect, useMemo, useState } from "react";
import { Boxes, RefreshCw, Save, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  APIError,
  api,
  type CollectorConfig,
  type CollectorDescriptor,
  type Device,
  type ServiceEntity,
} from "@/lib/api";

function configFor(descriptor: CollectorDescriptor, config: CollectorConfig | undefined): Record<string, string> {
  const values: Record<string, string> = {};
  for (const key of Object.keys(descriptor.configSchema)) values[key] = config?.config?.[key] ?? "";
  return values;
}

export function ServicesView() {
  const [items, setItems] = useState<ServiceEntity[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [descriptors, setDescriptors] = useState<CollectorDescriptor[]>([]);
  const [configs, setConfigs] = useState<CollectorConfig[]>([]);
  const [provider, setProvider] = useState("");
  const [deviceId, setDeviceId] = useState("");
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
    if (!deviceId && deviceList.items[0]) setDeviceId(deviceList.items[0].id);
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
    Promise.all([refreshEntities(), refreshCatalog()]).catch((caught) => {
      setError(caught instanceof APIError ? caught.message : "Could not load service collectors");
    });
  }, [provider]);

  useEffect(() => {
    refreshConfigs().catch((caught) =>
      setError(caught instanceof APIError ? caught.message : "Could not load collector configuration"),
    );
  }, [deviceId]);

  const configByCollector = useMemo(() => new Map(configs.map((config) => [config.collectorId, config])), [configs]);

  function draftFor(descriptor: CollectorDescriptor) {
    return drafts[descriptor.id] ?? configFor(descriptor, configByCollector.get(descriptor.id));
  }

  function setDraft(descriptorId: string, key: string, value: string) {
    setDrafts((current) => ({
      ...current,
      [descriptorId]: { ...(current[descriptorId] ?? {}), [key]: value },
    }));
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
      setMessage(descriptor.provider + " collector configuration saved. Secrets stay in the credential broker.");
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
          <p>
            Provider adapters share bounded lifecycle, health, expiry, and redaction rules. A failed adapter cannot
            block host monitoring.
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Refresh services"
          onClick={() =>
            Promise.all([refreshEntities(), refreshCatalog(), refreshConfigs()]).catch(() =>
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
            <option value="docker">Docker</option>
            <option value="proxmox">Proxmox</option>
            <option value="fake">Fixture provider</option>
          </select>
        </label>
        <Badge variant="outline">{items.length} active entities</Badge>
      </div>
      <div className="data-panel">
        <div className="section-heading">
          <div>
            <h3>Collector configuration</h3>
            <p>
              Use a credential reference, never a provider secret. Configuration keys are metadata and are bounded by
              the server.
            </p>
          </div>
          <Badge variant="outline">{descriptors.length}</Badge>
        </div>
        {!devices.length ? (
          <p className="empty-inline">Enroll a device before configuring service collectors.</p>
        ) : (
          <>
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
                      {Object.entries(descriptor.configSchema).map(([key, description]) => (
                        <label key={key}>
                          {key}
                          <span className="label-hint">{description}</span>
                          <Input
                            value={values[key] ?? ""}
                            onChange={(event) => setDraft(descriptor.id, key, event.target.value)}
                          />
                        </label>
                      ))}
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
      {items.length ? (
        <div className="service-grid">
          {items.map((item) => (
            <article className="service-card" key={item.provider + "-" + item.id}>
              <div className="service-card-icon">
                <Boxes size={20} />
              </div>
              <div>
                <h3>{item.name}</h3>
                <p>
                  {item.provider} · {item.kind}
                  {item.clusterId ? " · " + item.clusterId : ""}
                </p>
                <small>
                  {item.status} · observed {new Date(item.observedAt).toLocaleString()}
                </small>
              </div>
              <Badge variant="outline">{item.deviceId ? "Associated" : "Unassociated"}</Badge>
            </article>
          ))}
        </div>
      ) : (
        <div className="empty">
          <Boxes size={30} />
          <h2>No service entities</h2>
          <p>
            Configure a collector on an enrolled device to see provider entities here. Host metrics remain independent.
          </p>
        </div>
      )}
    </section>
  );
}
