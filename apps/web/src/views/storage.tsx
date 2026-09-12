import { useCallback, useEffect, useMemo, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  Clock3,
  Database,
  ExternalLink,
  HardDrive,
  RefreshCw,
  XCircle,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { APIError, api, type Device, type Incident, type ServiceEntity } from "@/lib/api";

type StorageState = "healthy" | "fault" | "scrubbing" | "unavailable";

function stateFor(item: ServiceEntity): StorageState {
  const health = item.labels?.healthState?.toLowerCase();
  const faultBearing = item.provider === "smart" || item.kind === "zfs_pool";
  if (faultBearing && (item.labels?.hardwareFault === "true" || item.status.toLowerCase() === "fault")) return "fault";
  if (faultBearing && ["degraded", "faulted", "unavail", "suspended"].includes(health ?? "")) return "fault";
  if (item.status.toLowerCase() === "scrubbing") return "scrubbing";
  if (item.status.toLowerCase() === "online" || item.status.toLowerCase() === "healthy" || health === "online") {
    return "healthy";
  }
  return "unavailable";
}

function stateLabel(state: StorageState): string {
  switch (state) {
    case "fault":
      return "Explicit fault";
    case "scrubbing":
      return "Scrubbing · healthy";
    case "unavailable":
      return "Unavailable";
    default:
      return "Healthy";
  }
}

function stateDescription(state: StorageState): string {
  switch (state) {
    case "fault":
      return "The collector reported a supported hardware or pool fault.";
    case "scrubbing":
      return "A scrub is in progress; this is not treated as a fault by itself.";
    case "unavailable":
      return "The collector could not provide a current health predicate.";
    default:
      return "The latest supported health predicate is healthy.";
  }
}

function formatDate(value: string | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "—" : date.toLocaleString();
}

function formatBytes(value: number | null): string {
  if (value === null) return "Unavailable";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let scaled = Math.abs(value);
  let unit = 0;
  while (scaled >= 1024 && unit < units.length - 1) {
    scaled /= 1024;
    unit += 1;
  }
  const sign = value < 0 ? "-" : "";
  return `${sign}${scaled >= 10 || unit === 0 ? scaled.toFixed(0) : scaled.toFixed(1)} ${units[unit]}`;
}

function formatMetric(value: number | null, unit: string): string {
  if (value === null) return "Unavailable";
  if (unit === "bytes") return formatBytes(value);
  if (unit === "bytes_per_second") return `${formatBytes(value)}/s`;
  if (unit === "percent") return `${value.toFixed(1)}%`;
  if (unit === "celsius") return `${value.toFixed(1)} °C`;
  if (unit === "count") return value.toFixed(0);
  return value.toFixed(2);
}

function label(item: ServiceEntity, key: string): string | undefined {
  const value = item.labels?.[key]?.trim();
  return value || undefined;
}

function deviceName(deviceId: string | undefined, devices: Device[]): string {
  if (!deviceId) return "Unassociated host";
  return devices.find((device) => device.id === deviceId)?.displayName ?? `Device ${deviceId.slice(0, 8)}`;
}

function currentMetric(
  device: Device | undefined,
  item: ServiceEntity,
  metric: string,
): { value: number | null; unit: string } {
  const current = device?.currentMetrics?.[`${item.id}:${metric}`];
  if (!current || current.availability !== "current" || current.value === null) {
    return { value: null, unit: current?.unit ?? "" };
  }
  return { value: current.value, unit: current.unit };
}

function StateIcon({ state }: { state: StorageState }) {
  if (state === "fault") return <XCircle size={17} aria-hidden="true" />;
  if (state === "unavailable") return <Clock3 size={17} aria-hidden="true" />;
  if (state === "scrubbing") return <RefreshCw size={17} aria-hidden="true" />;
  return <CheckCircle2 size={17} aria-hidden="true" />;
}

function MetricTile({ name, value, unit }: { name: string; value: number | null; unit: string }) {
  return (
    <div className="storage-metric">
      <span>{name}</span>
      <strong>{formatMetric(value, unit)}</strong>
    </div>
  );
}

export function StorageView({
  onOpenIncident,
  deviceId,
}: {
  onOpenIncident?: (incidentId: string) => void;
  deviceId?: string;
}) {
  const [items, setItems] = useState<ServiceEntity[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [filterDeviceId, setFilterDeviceId] = useState(deviceId ?? "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async (showBusy = false) => {
    if (showBusy) setBusy(true);
    try {
      const [storage, deviceList, incidentList] = await Promise.all([
        api.storage(),
        api.devices(),
        api.incidents("?status=active&limit=500"),
      ]);
      setItems(storage.items);
      setDevices(deviceList.items);
      setIncidents(incidentList.items);
      setError("");
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Could not load storage health");
    } finally {
      if (showBusy) setBusy(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), 5_000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  useEffect(() => {
    setFilterDeviceId(deviceId ?? "");
  }, [deviceId]);

  const visibleItems = useMemo(
    () =>
      items
        .filter((item) => !filterDeviceId || item.deviceId === filterDeviceId)
        .sort((left, right) => left.name.localeCompare(right.name)),
    [filterDeviceId, items],
  );
  const smartItems = useMemo(() => visibleItems.filter((item) => item.provider === "smart"), [visibleItems]);
  const pools = useMemo(() => visibleItems.filter((item) => item.kind === "zfs_pool"), [visibleItems]);
  const datasets = useMemo(() => visibleItems.filter((item) => item.kind === "zfs_dataset"), [visibleItems]);
  const devicesByID = useMemo(() => new Map(devices.map((device) => [device.id, device])), [devices]);
  const incidentsByEntity = useMemo(() => {
    const result = new Map<string, Incident[]>();
    for (const incident of incidents) {
      const key = `${incident.deviceId}\u0000${incident.entityId}`;
      result.set(key, [...(result.get(key) ?? []), incident]);
    }
    return result;
  }, [incidents]);

  function relatedIncident(item: ServiceEntity): Incident | undefined {
    if (!item.deviceId) return undefined;
    return incidentsByEntity.get(`${item.deviceId}\u0000${item.id}`)?.[0];
  }

  function renderIncident(item: ServiceEntity) {
    const incident = relatedIncident(item);
    if (!incident) return <span className="storage-clear-label">No active incident</span>;
    if (!onOpenIncident)
      return (
        <Badge className="storage-fault-badge">
          <AlertTriangle size={13} />
          Active incident
        </Badge>
      );
    return (
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => onOpenIncident(incident.id)}
        aria-label={`Open storage incident for ${item.name}`}
      >
        <AlertTriangle size={14} />
        Active incident
        <ExternalLink size={13} />
      </Button>
    );
  }

  function renderCard(item: ServiceEntity, section: "smart" | "pool" | "dataset") {
    const device = devicesByID.get(item.deviceId ?? "");
    const state = stateFor(item);
    const identity = label(item, "wwn") ?? label(item, "serial") ?? label(item, "guid") ?? "identity unavailable";
    return (
      <article className="storage-card" key={`${item.provider}-${item.deviceId ?? ""}-${item.id}`}>
        <div className={`storage-card-state ${state}`} aria-label={stateLabel(state)} title={stateDescription(state)}>
          <StateIcon state={state} />
          <span>{stateLabel(state)}</span>
        </div>
        <div className="storage-card-heading">
          <div className="storage-card-icon">
            {section === "smart" ? <HardDrive size={17} /> : <Database size={17} />}
          </div>
          <div>
            <h3>{item.name}</h3>
            <p>
              {deviceName(item.deviceId, devices)} · {item.provider.toUpperCase()}
            </p>
          </div>
        </div>
        {section === "smart" && (
          <div className="storage-metric-grid">
            <MetricTile {...currentMetric(device, item, "smart.temperature")} name="Temperature" />
            <MetricTile {...currentMetric(device, item, "smart.wear_percent")} name="Wear" />
            <MetricTile {...currentMetric(device, item, "smart.error_count")} name="Errors" />
          </div>
        )}
        {section === "pool" && (
          <div className="storage-metric-grid">
            <MetricTile {...currentMetric(device, item, "zfs.pool.used")} name="Allocated" />
            <MetricTile {...currentMetric(device, item, "zfs.pool.capacity")} name="Pool capacity" />
            <MetricTile {...currentMetric(device, item, "zfs.pool.read_rate")} name="Read rate" />
            <MetricTile {...currentMetric(device, item, "zfs.pool.write_rate")} name="Write rate" />
          </div>
        )}
        {section === "dataset" && (
          <div className="storage-metric-grid">
            <MetricTile {...currentMetric(device, item, "zfs.dataset.used")} name="Used" />
            <MetricTile {...currentMetric(device, item, "zfs.dataset.available")} name="Available" />
          </div>
        )}
        <dl className="storage-card-details">
          <div>
            <dt>Identity</dt>
            <dd>{identity}</dd>
          </div>
          <div>
            <dt>Observed</dt>
            <dd>{formatDate(item.observedAt)}</dd>
          </div>
          {label(item, "scrubState") && (
            <div>
              <dt>Scrub</dt>
              <dd>
                {label(item, "scrubState")}
                {label(item, "scrubProgress") ? ` · ${label(item, "scrubProgress")}` : ""}
              </dd>
            </div>
          )}
          {label(item, "devicePath") && (
            <div>
              <dt>Path</dt>
              <dd>{label(item, "devicePath")}</dd>
            </div>
          )}
        </dl>
        <div className="storage-card-footer">
          <span>{stateDescription(state)}</span>
          {renderIncident(item)}
        </div>
      </article>
    );
  }

  return (
    <section className="workspace-grid storage-view">
      <div className="section-heading">
        <div>
          <Badge variant="outline">
            <HardDrive size={13} />
            Explicit health evidence
          </Badge>
          <h2>Storage health</h2>
        </div>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Refresh storage health"
          disabled={busy}
          onClick={() => void refresh(true)}
        >
          <RefreshCw size={16} />
        </Button>
      </div>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      <div className="filter-row storage-filter-row">
        {!deviceId && (
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
        <div className="storage-summary" aria-label="Storage health summary">
          <Badge variant="outline">{visibleItems.length} entities</Badge>
          <Badge
            variant="outline"
            className={visibleItems.some((item) => stateFor(item) === "fault") ? "storage-fault-badge" : ""}
          >
            {visibleItems.filter((item) => stateFor(item) === "fault").length} explicit faults
          </Badge>
          <Badge variant="outline">
            {visibleItems.filter((item) => stateFor(item) === "unavailable").length} unavailable
          </Badge>
        </div>
      </div>
      <div className="storage-scope-note" role="note">
        <Database size={16} />
        <span>Pool and dataset capacity stay separate.</span>
      </div>
      {smartItems.length > 0 && (
        <section className="storage-section" aria-labelledby="smart-storage-title">
          <div className="storage-section-heading">
            <div>
              <h3 id="smart-storage-title">SMART devices</h3>
            </div>
            <Badge variant="outline">{smartItems.length}</Badge>
          </div>
          <div className="storage-card-grid">{smartItems.map((item) => renderCard(item, "smart"))}</div>
        </section>
      )}
      {pools.length > 0 && (
        <section className="storage-section" aria-labelledby="zfs-pools-title">
          <div className="storage-section-heading">
            <div>
              <h3 id="zfs-pools-title">ZFS pools · physical capacity</h3>
            </div>
            <Badge variant="outline">{pools.length}</Badge>
          </div>
          <div className="storage-card-grid">{pools.map((item) => renderCard(item, "pool"))}</div>
        </section>
      )}
      {datasets.length > 0 && (
        <section className="storage-section" aria-labelledby="zfs-datasets-title">
          <div className="storage-section-heading">
            <div>
              <h3 id="zfs-datasets-title">ZFS datasets · usable space</h3>
            </div>
            <Badge variant="outline">{datasets.length}</Badge>
          </div>
          <div className="storage-card-grid">{datasets.map((item) => renderCard(item, "dataset"))}</div>
        </section>
      )}
      {!visibleItems.length && (
        <div className="empty">
          <HardDrive size={30} />
          <h2>No storage entities observed</h2>
          <p>Enable SMART or ZFS telemetry.</p>
        </div>
      )}
    </section>
  );
}
