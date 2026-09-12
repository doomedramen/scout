import { useCallback, useEffect, useMemo, useState } from "react";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  CircleAlert,
  CircleCheck,
  Clock3,
  Cpu,
  EyeOff,
  Fan,
  Gauge,
  RefreshCw,
  ShieldCheck,
  Thermometer,
  X,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ChartContainer, ChartDataQuality, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart";
import { APIError, api, type CollectorConfig, type Device, type MetricSeries, type ServiceEntity } from "@/lib/api";

type HardwareState = "online" | "fault" | "unavailable";

const gpuMetrics = [
  { metric: "gpu.utilization", label: "Utilization" },
  { metric: "gpu.memory.used", label: "Memory used" },
  { metric: "gpu.memory.capacity", label: "Memory capacity" },
  { metric: "gpu.temperature", label: "Temperature" },
  { metric: "gpu.power", label: "Power" },
];

function stateFor(item: ServiceEntity): HardwareState {
  const status = item.status.trim().toLowerCase();
  if (status === "fault" || item.labels?.hardwareFault === "true") return "fault";
  if (status === "online" || status === "healthy" || status === "active") return "online";
  return "unavailable";
}

function stateLabel(state: HardwareState): string {
  if (state === "fault") return "Hardware fault";
  if (state === "unavailable") return "Unavailable";
  return "Online";
}

function StateIcon({ state }: { state: HardwareState }) {
  if (state === "fault") return <CircleAlert size={16} aria-hidden="true" />;
  if (state === "unavailable") return <Clock3 size={16} aria-hidden="true" />;
  return <CircleCheck size={16} aria-hidden="true" />;
}

function formatDate(value: string | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "—" : date.toLocaleString();
}

function formatBytes(value: number): string {
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

function formatValue(value: number | null, unit: string): string {
  if (value === null) return "Unavailable";
  if (unit === "bytes") return formatBytes(value);
  if (unit === "percent") return `${value.toFixed(1)}%`;
  if (unit === "celsius") return `${value.toFixed(1)} °C`;
  if (unit === "rpm") return `${value.toFixed(0)} RPM`;
  if (unit === "watts") return `${value.toFixed(2)} W`;
  return value.toFixed(2);
}

function metricName(item: ServiceEntity): string {
  if (item.provider === "gpu") return "gpu.temperature";
  return item.labels?.sensorType === "fan" ? "sensor.fan_speed" : "sensor.temperature";
}

function currentMetric(
  device: Device | undefined,
  item: ServiceEntity,
  metric: string,
): { value: number | null; unit: string; availability: string; observedAt?: string } {
  const current = device?.currentMetrics?.[`${item.id}:${metric}`];
  if (!current || current.availability !== "current" || current.value === null) {
    return {
      value: null,
      unit: current?.unit ?? metricUnit(metric),
      availability: current?.availability ?? "unavailable",
      observedAt: current?.observedAt,
    };
  }
  return {
    value: current.value,
    unit: current.unit,
    availability: current.availability,
    observedAt: current.observedAt,
  };
}

function metricUnit(metric: string): string {
  if (metric.endsWith("temperature")) return "celsius";
  if (metric.endsWith("fan_speed")) return "rpm";
  if (metric.endsWith("utilization")) return "percent";
  if (metric.endsWith("power")) return "watts";
  return "bytes";
}

function capability(item: ServiceEntity, metric: string): string {
  const key = `capability.${metric.replace(/^.*?\./, "")}`;
  return item.labels?.[key] ?? "unavailable";
}

function label(item: ServiceEntity, key: string): string | undefined {
  const value = item.labels?.[key]?.trim();
  return value || undefined;
}

function deviceName(deviceID: string | undefined, devices: Device[]): string {
  if (!deviceID) return "Unassociated host";
  return devices.find((device) => device.id === deviceID)?.displayName ?? `Device ${deviceID.slice(0, 8)}`;
}

function sensorTitle(item: ServiceEntity): string {
  return item.provider === "gpu"
    ? "GPU signal"
    : item.labels?.sensorType === "fan"
      ? "Fan signal"
      : "Temperature signal";
}

function metricLabel(metric: string): string {
  const known: Record<string, string> = {
    "sensor.temperature": "Temperature",
    "sensor.fan_speed": "Fan speed",
    "gpu.utilization": "GPU utilization",
    "gpu.memory.used": "GPU memory used",
    "gpu.memory.capacity": "GPU memory capacity",
    "gpu.temperature": "GPU temperature",
    "gpu.power": "GPU power",
  };
  return known[metric] ?? metric;
}

function freshness(item: ServiceEntity): string {
  const expires = new Date(item.expiresAt).valueOf();
  if (!Number.isFinite(expires)) return "Freshness unavailable";
  if (expires <= Date.now()) return "Expired";
  if (expires - Date.now() <= 30_000) return "Expiring soon";
  return "Fresh";
}

function parseExclusions(config: CollectorConfig | undefined): string[] {
  return (config?.config?.excludedIds ?? "")
    .split(",")
    .map((value) => value.trim())
    .filter(Boolean);
}

function metricKey(entityID: string, metric: string): string {
  return `${entityID}\u0000${metric}`;
}

function metricFromKey(value: string): { entityID: string; metric: string } | undefined {
  const [entityID, metric] = value.split("\u0000");
  if (!entityID || !metric) return undefined;
  return { entityID, metric };
}

type ChartPoint = {
  time: number;
  value: number | null;
  availability: string;
  partial?: boolean;
};

function chartPoints(series: MetricSeries[], entityID: string, metric: string): ChartPoint[] {
  const selected = series.find((item) => item.entityId === entityID && item.metric === metric);
  return (selected?.points ?? []).map((point) => ({
    time: new Date(point.observedAt).getTime(),
    value: point.availability === "current" ? point.value : null,
    availability: point.availability,
    partial: point.partial,
  }));
}

function SignalTile({
  name,
  item,
  device,
  metric,
}: {
  name: string;
  item: ServiceEntity;
  device: Device | undefined;
  metric: string;
}) {
  const current = currentMetric(device, item, metric);
  const available = current.availability === "current" && current.value !== null;
  return (
    <div className="hardware-signal-tile">
      <span>{name}</span>
      <strong>{formatValue(current.value, current.unit)}</strong>
      <small className={available ? "hardware-available" : "hardware-unavailable"}>
        {available ? "Available" : "Unavailable"}
      </small>
    </div>
  );
}

function SensorCard({
  item,
  device,
  excluded,
}: {
  item: ServiceEntity;
  device: Device | undefined;
  excluded: boolean;
}) {
  const metric = metricName(item);
  const state = stateFor(item);
  const current = currentMetric(device, item, metric);
  const sensorType = item.labels?.sensorType === "fan" ? "fan" : "temperature";
  return (
    <article className={`hardware-card ${state}`}>
      <div className="hardware-card-topline">
        <div className={`hardware-state ${state}`} aria-label={stateLabel(state)}>
          <StateIcon state={state} />
          <span>{stateLabel(state)}</span>
        </div>
        {excluded && (
          <Badge variant="outline" className="hardware-excluded">
            <EyeOff size={12} /> Excluded
          </Badge>
        )}
      </div>
      <div className="hardware-card-heading">
        <div className="hardware-card-icon">{sensorType === "fan" ? <Fan size={18} /> : <Thermometer size={18} />}</div>
        <div>
          <h3>{item.name}</h3>
          <p>{deviceName(item.deviceId, device ? [device] : [])}</p>
        </div>
      </div>
      <div className="hardware-signal-grid">
        <SignalTile
          name={sensorType === "fan" ? "Fan speed" : "Temperature"}
          item={item}
          device={device}
          metric={metric}
        />
        <div className="hardware-signal-tile">
          <span>Fault flag</span>
          <strong>{item.labels?.hardwareFault === "true" ? "Reported" : "None reported"}</strong>
          <small
            className={item.labels?.["capability.fault"] === "current" ? "hardware-available" : "hardware-unavailable"}
          >
            {item.labels?.["capability.fault"] === "current" ? "Hardware alarm" : "No alarm evidence"}
          </small>
        </div>
      </div>
      <dl className="hardware-card-details">
        <div>
          <dt>Capability</dt>
          <dd>{capability(item, sensorType === "fan" ? "sensor.fan_speed" : "sensor.temperature")}</dd>
        </div>
        <div>
          <dt>Identity</dt>
          <dd>
            {item.labels?.identityStable === "true" ? "Stable" : "Unstable"} ·{" "}
            {item.labels?.identitySource ?? "unknown"}
          </dd>
        </div>
        <div>
          <dt>Observed</dt>
          <dd>{formatDate(item.observedAt || current.observedAt)}</dd>
        </div>
        <div>
          <dt>Freshness</dt>
          <dd>{freshness(item)}</dd>
        </div>
      </dl>
    </article>
  );
}

function GPUCard({ item, device }: { item: ServiceEntity; device: Device | undefined }) {
  const state = stateFor(item);
  return (
    <article className={`hardware-card ${state}`}>
      <div className="hardware-card-topline">
        <div className={`hardware-state ${state}`} aria-label={stateLabel(state)}>
          <StateIcon state={state} />
          <span>{stateLabel(state)}</span>
        </div>
        <Badge variant="outline">{label(item, "vendor") ?? "GPU"}</Badge>
      </div>
      <div className="hardware-card-heading">
        <div className="hardware-card-icon">
          <Cpu size={18} />
        </div>
        <div>
          <h3>{item.name}</h3>
          <p>
            {deviceName(item.deviceId, device ? [device] : [])}
            {label(item, "driver") ? ` · ${label(item, "driver")}` : ""}
          </p>
        </div>
      </div>
      <div className="hardware-signal-grid gpu-signal-grid">
        {gpuMetrics.map(({ metric, label: metricTitle }) => (
          <SignalTile key={metric} name={metricTitle} item={item} device={device} metric={metric} />
        ))}
      </div>
      <dl className="hardware-card-details">
        <div>
          <dt>Capabilities</dt>
          <dd>{gpuMetrics.filter(({ metric }) => capability(item, metric) === "current").length}/5 fields current</dd>
        </div>
        <div>
          <dt>Power scope</dt>
          <dd>{label(item, "powerScope") ?? "Unavailable"}</dd>
        </div>
        <div>
          <dt>Identity</dt>
          <dd>
            {item.labels?.identityStable === "true" ? "Stable" : "Unstable"} ·{" "}
            {item.labels?.identitySource ?? "unknown"}
          </dd>
        </div>
        <div>
          <dt>Observed</dt>
          <dd>{formatDate(item.observedAt)}</dd>
        </div>
      </dl>
    </article>
  );
}

export function HardwareView({ deviceId }: { deviceId?: string } = {}) {
  const [sensors, setSensors] = useState<ServiceEntity[]>([]);
  const [gpus, setGPUs] = useState<ServiceEntity[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [configs, setConfigs] = useState<CollectorConfig[]>([]);
  const [selectedDeviceID, setSelectedDeviceID] = useState(deviceId ?? "");
  const [excludedIDs, setExcludedIDs] = useState<string[]>([]);
  const [chartSelection, setChartSelection] = useState("");
  const [series, setSeries] = useState<MetricSeries[]>([]);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const [sensorList, gpuList, deviceList] = await Promise.all([
        api.services("sensors"),
        api.services("gpu"),
        api.devices(),
      ]);
      setSensors(sensorList.items);
      setGPUs(gpuList.items);
      setDevices(deviceList.items);
      setSelectedDeviceID((current) =>
        deviceId && deviceList.items.some((device) => device.id === deviceId)
          ? deviceId
          : current && deviceList.items.some((device) => device.id === current)
            ? current
            : (deviceList.items[0]?.id ?? ""),
      );
      setError("");
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Could not load hardware telemetry");
    } finally {
      setLoading(false);
    }
  }, []);

  const refreshConfig = useCallback(async () => {
    if (!selectedDeviceID) {
      setConfigs([]);
      setExcludedIDs([]);
      return;
    }
    try {
      const result = await api.collectors(selectedDeviceID);
      setConfigs(result.items);
      setExcludedIDs(parseExclusions(result.items.find((config) => config.collectorId === "sensors")));
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Could not load hardware settings");
    }
  }, [selectedDeviceID]);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), 5_000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  useEffect(() => {
    if (deviceId) setSelectedDeviceID(deviceId);
  }, [deviceId]);

  useEffect(() => {
    void refreshConfig();
    setSeries([]);
  }, [refreshConfig]);

  useEffect(() => {
    if (!selectedDeviceID) return;
    let cancelled = false;
    const from = new Date(Date.now() - 60 * 60 * 1000).toISOString();
    api
      .metrics(selectedDeviceID, `?from=${encodeURIComponent(from)}&maxPoints=240`)
      .then((result) => {
        if (!cancelled) setSeries(result.series);
      })
      .catch((caught) => {
        if (!cancelled) setError(caught instanceof APIError ? caught.message : "Could not load hardware history");
      });
    return () => {
      cancelled = true;
    };
  }, [selectedDeviceID, sensors.length, gpus.length]);

  const selectedDevice = useMemo(
    () => devices.find((device) => device.id === selectedDeviceID),
    [devices, selectedDeviceID],
  );
  const visibleSensors = useMemo(
    () => sensors.filter((item) => item.deviceId === selectedDeviceID),
    [selectedDeviceID, sensors],
  );
  const visibleGPUs = useMemo(
    () => gpus.filter((item) => item.deviceId === selectedDeviceID),
    [gpus, selectedDeviceID],
  );
  const configByCollector = useMemo(() => new Map(configs.map((config) => [config.collectorId, config])), [configs]);
  const chartOptions = useMemo(
    () => [
      ...visibleSensors.map((item) => ({
        value: metricKey(item.id, metricName(item)),
        label: `${item.name} · ${metricLabel(metricName(item))}`,
      })),
      ...visibleGPUs.flatMap((item) =>
        gpuMetrics.map(({ metric }) => ({
          value: metricKey(item.id, metric),
          label: `${item.name} · ${metricLabel(metric)}`,
        })),
      ),
    ],
    [visibleGPUs, visibleSensors],
  );

  useEffect(() => {
    if (!chartOptions.some((option) => option.value === chartSelection))
      setChartSelection(chartOptions[0]?.value ?? "");
  }, [chartOptions, chartSelection]);

  const selectedChart = metricFromKey(chartSelection);
  const selectedChartSeries = selectedChart
    ? series.find((item) => item.entityId === selectedChart.entityID && item.metric === selectedChart.metric)
    : undefined;
  const selectedChartPoints = selectedChart ? chartPoints(series, selectedChart.entityID, selectedChart.metric) : [];
  const selectedChartEntity = selectedChart
    ? [...visibleSensors, ...visibleGPUs].find((item) => item.id === selectedChart.entityID)
    : undefined;
  const unavailableCount = selectedChartPoints.filter((point) => point.value === null).length;

  function toggleExclusion(id: string, checked: boolean) {
    setExcludedIDs((current) => (checked ? [...new Set([...current, id])] : current.filter((value) => value !== id)));
  }

  async function saveExclusions() {
    if (!selectedDeviceID) return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const current = configByCollector.get("sensors");
      await api.updateCollector(selectedDeviceID, "sensors", {
        provider: "sensors",
        enabled: current?.enabled ?? true,
        config: { ...(current?.config ?? {}), excludedIds: excludedIDs.join(",") },
        credentialRef: current?.credentialRef,
        expectedRevision: current?.revision,
      });
      setMessage("Sensor exclusions saved. Exact stable IDs will be applied on the next collector run.");
      await refreshConfig();
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Sensor exclusions could not be saved");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="workspace-grid hardware-view">
      <div className="section-heading">
        <div>
          <Badge variant="outline">
            <ShieldCheck size={13} /> Hardware telemetry
          </Badge>
          <h2>Hardware</h2>
        </div>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Refresh hardware telemetry"
          disabled={loading}
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
      <div className="filter-row hardware-filter-row">
        {!deviceId && (
          <label>
            Device
            <select
              aria-label="Hardware device"
              value={selectedDeviceID}
              onChange={(event) => setSelectedDeviceID(event.target.value)}
            >
              {!devices.length && <option value="">No enrolled devices</option>}
              {devices.map((device) => (
                <option key={device.id} value={device.id}>
                  {device.displayName}
                </option>
              ))}
            </select>
          </label>
        )}
        <div className="hardware-summary" aria-label="Hardware summary">
          <Badge variant="outline">{visibleSensors.length} sensors</Badge>
          <Badge variant="outline">{visibleGPUs.length} GPUs</Badge>
          <Badge variant="outline">{excludedIDs.length} exclusions</Badge>
        </div>
      </div>
      <div className="hardware-scope-note" role="note">
        <Gauge size={16} />
        <span>
          No thermal, fan, utilization, or wear thresholds are created automatically. Configure an explicit owner rule
          only for a field you want to alert on.
        </span>
      </div>
      <section className="hardware-settings-panel" aria-labelledby="sensor-exclusions-title">
        <div className="section-heading">
          <div>
            <h3 id="sensor-exclusions-title">Sensor exclusions</h3>
            <p>Exclude stable sensor IDs.</p>
          </div>
          <Button type="button" size="sm" disabled={!selectedDeviceID || busy} onClick={() => void saveExclusions()}>
            {busy ? "Saving…" : "Save exclusions"}
          </Button>
        </div>
        {visibleSensors.length ? (
          <div className="hardware-exclusion-list">
            {visibleSensors.map((item) => {
              const checked = excludedIDs.includes(item.id);
              return (
                <label className="hardware-exclusion-row" key={item.id}>
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={(event) => toggleExclusion(item.id, event.target.checked)}
                  />
                  <span>
                    <strong>{item.name}</strong>
                    <small>
                      {item.id.slice(0, 20)}… · {label(item, "identitySource") ?? "identity unavailable"}
                    </small>
                  </span>
                </label>
              );
            })}
          </div>
        ) : (
          <p className="empty-inline">No sensor channels observed on this device.</p>
        )}
        {excludedIDs.length > 0 && (
          <div className="hardware-excluded-list" aria-label="Saved sensor exclusions">
            <span>Saved IDs</span>
            {excludedIDs.map((id) => (
              <Badge variant="outline" key={id}>
                {id.slice(0, 20)}…
                <button type="button" aria-label={`Remove exclusion ${id}`} onClick={() => toggleExclusion(id, false)}>
                  <X size={12} />
                </button>
              </Badge>
            ))}
          </div>
        )}
      </section>
      {visibleSensors.length > 0 && (
        <section className="hardware-section" aria-labelledby="sensors-title">
          <div className="hardware-section-heading">
            <div>
              <h3 id="sensors-title">Temperature and fan channels</h3>
            </div>
            <Badge variant="outline">{visibleSensors.length}</Badge>
          </div>
          <div className="hardware-card-grid">
            {visibleSensors.map((item) => (
              <SensorCard key={item.id} item={item} device={selectedDevice} excluded={excludedIDs.includes(item.id)} />
            ))}
          </div>
        </section>
      )}
      {visibleGPUs.length > 0 && (
        <section className="hardware-section" aria-labelledby="gpus-title">
          <div className="hardware-section-heading">
            <div>
              <h3 id="gpus-title">GPUs</h3>
            </div>
            <Badge variant="outline">{visibleGPUs.length}</Badge>
          </div>
          <div className="hardware-card-grid">
            {visibleGPUs.map((item) => (
              <GPUCard key={item.id} item={item} device={selectedDevice} />
            ))}
          </div>
        </section>
      )}
      {chartOptions.length > 0 && (
        <section className="hardware-chart-panel" aria-label="Hardware history">
          <div className="section-heading">
            <div>
              <h3>Recent hardware history</h3>
              <p>Gaps remain visible.</p>
            </div>
            <label className="hardware-chart-selector">
              Signal
              <select
                aria-label="Hardware signal"
                value={chartSelection}
                onChange={(event) => setChartSelection(event.target.value)}
              >
                {chartOptions.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>
          </div>
          {selectedChart && selectedChartEntity && (
            <>
              <div className="hardware-chart-heading">
                <strong>{metricLabel(selectedChart.metric)}</strong>
                <span>
                  {selectedChartSeries?.resolutionSeconds
                    ? `${selectedChartSeries.resolutionSeconds}-second resolution`
                    : "History"}{" "}
                  · observed {formatDate(selectedChartEntity.observedAt)}
                </span>
              </div>
              {selectedChartPoints.length ? (
                <ChartContainer
                  config={{ value: { label: metricLabel(selectedChart.metric), color: "#8ba9f6" } }}
                  className="hardware-chart"
                >
                  <AreaChart
                    accessibilityLayer
                    data={selectedChartPoints}
                    margin={{ top: 8, right: 12, left: 0, bottom: 4 }}
                  >
                    <CartesianGrid className="grid-lines" vertical={false} />
                    <XAxis
                      dataKey="time"
                      type="number"
                      domain={["dataMin", "dataMax"]}
                      tickFormatter={(value) =>
                        new Date(value).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
                      }
                    />
                    <YAxis width={48} tickFormatter={(value) => String(value)} />
                    <ChartTooltip
                      content={
                        <ChartTooltipContent labelFormatter={(value) => new Date(Number(value)).toLocaleString()} />
                      }
                    />
                    <Area
                      dataKey="value"
                      type="monotone"
                      stroke="var(--color-value)"
                      fill="var(--color-value)"
                      fillOpacity={0.14}
                      connectNulls={false}
                      name={metricLabel(selectedChart.metric)}
                    />
                  </AreaChart>
                </ChartContainer>
              ) : (
                <div className="chart-empty">No history has arrived for this signal yet.</div>
              )}
              <ChartDataQuality
                gapCount={unavailableCount}
                partial={Boolean(selectedChartSeries?.points.some((point) => point.partial))}
              />
              {selectedChartPoints.length > 0 && (
                <details className="chart-table">
                  <summary>View tabular samples</summary>
                  <table>
                    <thead>
                      <tr>
                        <th>Observed</th>
                        <th>Value</th>
                        <th>Availability</th>
                      </tr>
                    </thead>
                    <tbody>
                      {selectedChartPoints.map((point) => (
                        <tr key={`${point.time}-${point.availability}`}>
                          <td>{new Date(point.time).toLocaleString()}</td>
                          <td>
                            {formatValue(point.value, selectedChartSeries?.unit ?? metricUnit(selectedChart.metric))}
                          </td>
                          <td>{point.availability}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </details>
              )}
            </>
          )}
        </section>
      )}
      {!visibleSensors.length && !visibleGPUs.length && (
        <div className="empty">
          <Cpu size={30} />
          <h2>No hardware entities observed</h2>
          <p>Enable hardware telemetry on an enrolled device.</p>
        </div>
      )}
    </section>
  );
}
