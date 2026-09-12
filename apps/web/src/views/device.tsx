import { useEffect, useMemo, useState } from "react";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  AlertTriangle,
  ArrowLeft,
  ChartNoAxesCombined,
  CheckCircle2,
  HardDrive,
  MemoryStick,
  Server,
  ShieldCheck,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ChartContainer, ChartDataQuality, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart";
import { api, APIError, type Device, type MetricSeries } from "@/lib/api";

type RangeKey = "1h" | "6h" | "24h" | "7d";
type ChartPoint = {
  time: number;
  value: number | null;
  availability: string;
  min?: number | null;
  max?: number | null;
  count?: number;
  coverage?: number;
  partial?: boolean;
};

type HistoryData = {
  data: ChartPoint[];
  resolutionSeconds?: number;
  partial: boolean;
};

type ChartScale = {
  domain: [number | "auto", number | "auto"];
  ticks?: number[];
};

const summaryMetrics = new Set(["cpu.utilization", "memory.used_percent", "filesystem.used_percent"]);

const diagnosticDetails: Record<string, string> = {
  "cpu.user_percent": "CPU time excluding guest time already counted by Linux",
  "cpu.system_percent": "Kernel time across all cores",
  "cpu.iowait_percent": "Time waiting for block I/O",
  "cpu.steal_percent": "Time taken by the hypervisor",
  "load.1m": "Runnable-task load over one minute",
  "load.5m": "Runnable-task load over five minutes",
  "load.15m": "Runnable-task load over fifteen minutes",
  "swap.used": "Swap space currently in use",
  "swap.capacity": "Configured swap capacity; zero is valid",
  "disk.read_rate": "Completed reads converted from 512-byte sectors",
  "disk.write_rate": "Completed writes converted from 512-byte sectors",
  "disk.utilization": "Time spent servicing I/O during the sample interval",
  "disk.read_latency": "Average time per completed read operation",
  "disk.write_latency": "Average time per completed write operation",
};

const ranges: Array<{ key: RangeKey; label: string; minutes: number; maxPoints: number }> = [
  { key: "1h", label: "Last hour", minutes: 60, maxPoints: 240 },
  { key: "6h", label: "Last 6 hours", minutes: 360, maxPoints: 360 },
  { key: "24h", label: "Last 24 hours", minutes: 1440, maxPoints: 480 },
  { key: "7d", label: "Last 7 days", minutes: 10080, maxPoints: 600 },
];

function currentValue(device: Device, metric: string): number | null {
  const item = device.currentMetrics?.[metric];
  const freshness = device.metricFreshness[metric];
  if (!item || item.value === null || (freshness && freshness !== "current")) return null;
  return typeof item.value === "number" ? item.value : null;
}

function identifierValues(device: Device, kind: string): string[] {
  return (device.identifiers ?? [])
    .filter((item) => item.kind.toLowerCase() === kind)
    .map((item) => item.value)
    .filter((value, index, values) => values.indexOf(value) === index);
}

function demoSeries(base: number, seed: number): ChartPoint[] {
  return Array.from({ length: 61 }, (_, index) => ({
    time: index - 60,
    value:
      index === 60
        ? base
        : Number(
            Math.min(
              97,
              Math.max(2, base + Math.sin(index * 0.78 + seed) * 5 + (index > 37 && index < 44 ? 24 : 0)),
            ).toFixed(1),
          ),
    availability: "current",
  }));
}

function seriesFor(series: MetricSeries[], metric: string, entityId = "host"): MetricSeries | undefined {
  return (
    series.find((item) => item.metric === metric && (item.entityId ?? "host") === entityId) ??
    series.find((item) => item.metric === metric)
  );
}

function seriesPoints(series: MetricSeries[], metric: string, entityId = "host"): ChartPoint[] {
  const selected = seriesFor(series, metric, entityId);
  return (selected?.points ?? []).map((point) => ({
    time: new Date(point.observedAt).getTime(),
    value: point.availability === "current" ? point.value : null,
    availability: point.availability,
    min: point.min,
    max: point.max,
    count: point.count,
    coverage: point.coverage,
    partial: point.partial,
  }));
}

function historyForSeries(series: MetricSeries[], metric: string, entityId = "host"): HistoryData {
  const selected = seriesFor(series, metric, entityId);
  const data = seriesPoints(series, metric, entityId);
  return {
    data,
    resolutionSeconds: selected?.resolutionSeconds,
    partial: Boolean(selected?.points.some((point) => point.partial)),
  };
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

function formatRate(value: number): string {
  return `${formatBytes(value)}/s`;
}

function formatResolution(seconds: number): string {
  if (seconds >= 3600 && seconds % 3600 === 0) return `${seconds / 3600}-hour`;
  if (seconds >= 60 && seconds % 60 === 0) return `${seconds / 60}-minute`;
  return `${seconds}-second`;
}

function formatMetricValue(value: number | null, unit: string): string {
  if (value === null) return "—";
  if (unit === "percent") return `${value.toFixed(1)}%`;
  if (unit === "bytes") return formatBytes(value);
  if (unit === "bytes_per_second") return formatRate(value);
  if (unit === "milliseconds") return `${value.toFixed(1)} ms`;
  if (unit === "seconds") return `${value.toFixed(1)} s`;
  if (unit === "count") return value >= 10 ? value.toFixed(0) : value.toFixed(2);
  return value.toFixed(2);
}

function metricLabel(metric: string): string {
  const labels: Record<string, string> = {
    "cpu.utilization": "CPU usage",
    "memory.used_percent": "Memory usage",
    "filesystem.used_percent": "Disk usage",
  };
  if (labels[metric]) return labels[metric];
  return metric
    .split(".")
    .map((part) => part.replaceAll("_", " "))
    .join(" · ")
    .replace(/^./, (letter) => letter.toUpperCase());
}

function metricDetail(metric: string): string {
  return diagnosticDetails[metric] ?? "Reported by the selected host collector";
}

function chartScale(unit: string): ChartScale {
  if (unit === "percent") return { domain: [0, 100], ticks: [0, 50, 100] };
  if (unit === "bytes" || unit === "bytes_per_second") return { domain: [0, "auto"] };
  return { domain: [0, "auto"] };
}

function chartCurrent(data: ChartPoint[]): number | null {
  const last = data[data.length - 1];
  return last?.availability === "current" && last.value !== null ? last.value : null;
}

function entityLabel(entityId: string | undefined): string {
  if (!entityId || entityId === "host") return "Host";
  return entityId;
}

function colorForMetric(metric: string): string {
  if (metric.startsWith("cpu.")) return "#7392f5";
  if (metric.startsWith("load.")) return "#c69b64";
  if (metric.startsWith("swap.")) return "#62b697";
  if (metric.startsWith("disk.")) return "#ac8fd9";
  return "#8db7d8";
}

export function DeviceView({
  selected,
  demo,
  onBack,
  onChanged,
}: {
  selected: Device;
  demo: boolean;
  onBack: () => void;
  onChanged?: (device: Device) => void;
}) {
  const [device, setDevice] = useState(selected);
  const [series, setSeries] = useState<MetricSeries[]>([]);
  const [range, setRange] = useState<RangeKey>("1h");
  const [reload, setReload] = useState(0);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(!demo);
  const [operationError, setOperationError] = useState("");
  const [operationMessage, setOperationMessage] = useState("");
  const [operationBusy, setOperationBusy] = useState(false);
  const [uninstall, setUninstall] = useState(false);
  const [reason, setReason] = useState("");
  const [diagnosticMetric, setDiagnosticMetric] = useState("");
  const [diagnosticEntity, setDiagnosticEntity] = useState("");

  const selectedRange = ranges.find((item) => item.key === range) ?? ranges[0];

  useEffect(() => {
    setDevice(selected);
    setSeries([]);
    setError("");
    setOperationError("");
    setOperationMessage("");
  }, [selected]);

  useEffect(() => {
    if (demo) {
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError("");
    const from = new Date(Date.now() - selectedRange.minutes * 60 * 1000).toISOString();
    const query = "?from=" + encodeURIComponent(from) + "&maxPoints=" + String(selectedRange.maxPoints);
    Promise.all([api.device(selected.id), api.metrics(selected.id, query)])
      .then(([detail, metrics]) => {
        if (cancelled) return;
        setDevice(detail);
        setSeries(metrics.series);
      })
      .catch((caught) => {
        if (!cancelled) setError(caught instanceof APIError ? caught.message : "Could not load host history");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [demo, reload, selected.id, selectedRange.maxPoints, selectedRange.minutes]);

  const cpu = currentValue(device, "cpu.utilization");
  const memory = currentValue(device, "memory.used_percent");
  const disk = currentValue(device, "filesystem.used_percent");
  const chartData = useMemo(() => {
    if (demo) {
      return {
        cpu: { data: demoSeries(cpu ?? 0, 1), partial: false } satisfies HistoryData,
        memory: { data: demoSeries(memory ?? 0, 3), partial: false } satisfies HistoryData,
        disk: { data: demoSeries(disk ?? 0, 7), partial: false } satisfies HistoryData,
      };
    }
    return {
      cpu: historyForSeries(series, "cpu.utilization"),
      memory: historyForSeries(series, "memory.used_percent"),
      disk: historyForSeries(series, "filesystem.used_percent"),
    };
  }, [cpu, demo, disk, memory, series]);

  const diagnosticSeries = useMemo(() => series.filter((item) => !summaryMetrics.has(item.metric)), [series]);
  const diagnosticMetrics = useMemo(
    () => [...new Set(diagnosticSeries.map((item) => item.metric))].sort(),
    [diagnosticSeries],
  );
  const diagnosticEntities = useMemo(
    () =>
      diagnosticSeries
        .filter((item) => item.metric === diagnosticMetric)
        .map((item) => item.entityId ?? "host")
        .filter((entityId, index, entities) => entities.indexOf(entityId) === index)
        .sort(),
    [diagnosticMetric, diagnosticSeries],
  );

  useEffect(() => {
    setDiagnosticMetric((current) => (diagnosticMetrics.includes(current) ? current : (diagnosticMetrics[0] ?? "")));
  }, [diagnosticMetrics]);

  useEffect(() => {
    setDiagnosticEntity((current) => (diagnosticEntities.includes(current) ? current : (diagnosticEntities[0] ?? "")));
  }, [diagnosticEntities]);

  const selectedDiagnosticSeries = seriesFor(diagnosticSeries, diagnosticMetric, diagnosticEntity);
  const diagnosticHistory = selectedDiagnosticSeries
    ? historyForSeries([selectedDiagnosticSeries], diagnosticMetric, diagnosticEntity)
    : ({ data: [], partial: false } satisfies HistoryData);

  async function decommissionDevice() {
    setOperationBusy(true);
    setOperationError("");
    setOperationMessage("");
    try {
      const result = await api.decommission(device.id, { uninstall, reason: reason.trim() });
      setDevice(result.device);
      setReason("");
      setUninstall(false);
      setOperationMessage(
        result.uninstall === "not_requested"
          ? "The identity was revoked and the device is excluded from future discovery."
          : "The device is revoked. Uninstall is " +
              (result.uninstall.confirmed ? "confirmed." : "queued and not confirmed."),
      );
      onChanged?.(result.device);
    } catch (caught) {
      setOperationError(caught instanceof APIError ? caught.message : "Decommissioning could not be completed");
    } finally {
      setOperationBusy(false);
    }
  }

  async function reenableDevice() {
    setOperationBusy(true);
    setOperationError("");
    setOperationMessage("");
    try {
      const updated = await api.reenable(device.id, device.revision);
      setDevice(updated);
      setOperationMessage(
        "Device re-enabled as a candidate. Scope and access checks must pass before monitoring resumes.",
      );
      onChanged?.(updated);
    } catch (caught) {
      setOperationError(caught instanceof APIError ? caught.message : "Device could not be re-enabled");
    } finally {
      setOperationBusy(false);
    }
  }

  const isDecommissioned = device.lifecycle === "decommissioned" || device.availability === "revoked";
  const hasAgent = Boolean(device.agentVersion);

  return (
    <>
      <Button className="back" variant="ghost" size="sm" onClick={onBack}>
        <ArrowLeft size={15} />
        All systems
      </Button>
      <div className="page-heading">
        <div>
          <h1>{device.displayName}</h1>
          <p>
            <span className={"dot " + (device.availability === "online" ? "healthy" : "muted")} />
            {device.availability} <span className="divider">/</span>{" "}
            {(device.addresses ?? [])[0] ?? "Address unavailable"} <span className="divider">/</span> {device.platform}{" "}
            {device.architecture}
          </p>
        </div>
        <label className="range-select">
          <span className="sr-only">History range</span>
          <select
            aria-label="History range"
            value={range}
            onChange={(event) => setRange(event.target.value as RangeKey)}
          >
            {ranges.map((item) => (
              <option key={item.key} value={item.key}>
                {demo ? item.label + " · demo" : item.label}
              </option>
            ))}
          </select>
        </label>
      </div>
      {error && (
        <div className="notice error-notice" role="alert">
          <ShieldCheck size={15} />
          <span>{error}</span>
          <Button variant="outline" size="sm" onClick={() => setReload((value) => value + 1)}>
            Retry
          </Button>
        </div>
      )}
      {device.availability !== "online" && hasAgent && (
        <div className="notice stale-notice" role="status">
          <Server size={15} />
          <span>Agent {device.availability}; retained measurements are historical and are not current.</span>
        </div>
      )}
      <section className="chart-panel host-info device-identity" aria-labelledby="device-identity-heading">
        <div className="chart-heading">
          <div>
            <h3 id="device-identity-heading">Device identity</h3>
            <p>The stable Scout record identifies this host; network addresses are supporting evidence only.</p>
          </div>
        </div>
        <dl>
          <div>
            <dt>Device ID</dt>
            <dd title={device.id}>{device.id}</dd>
          </div>
          <div>
            <dt>Hostname</dt>
            <dd>{device.hostname || "Not reported"}</dd>
          </div>
          <div>
            <dt>Known addresses</dt>
            <dd>{device.addresses?.length ? device.addresses.join(", ") : "Not reported"}</dd>
          </div>
          <div>
            <dt>Known MAC addresses</dt>
            <dd>{identifierValues(device, "mac").join(", ") || "Not reported"}</dd>
          </div>
          <div>
            <dt>Agent identity</dt>
            <dd title={device.agentId}>{device.agentId || "Not enrolled"}</dd>
          </div>
        </dl>
      </section>
      {hasAgent && !isDecommissioned ? (
        <div className="charts">
          <MetricChart
            title="CPU usage"
            detail="Utilization across all cores"
            unit="percent"
            color="#7392f5"
            data={chartData.cpu.data}
            current={cpu}
            loading={loading}
            partialRange={chartData.cpu.partial}
            rangeMinutes={selectedRange.minutes}
            resolutionSeconds={chartData.cpu.resolutionSeconds}
          />
          <MetricChart
            title="Memory usage"
            detail="Used memory as a share of total"
            unit="percent"
            color="#62b697"
            data={chartData.memory.data}
            current={memory}
            loading={loading}
            partialRange={chartData.memory.partial}
            rangeMinutes={selectedRange.minutes}
            resolutionSeconds={chartData.memory.resolutionSeconds}
          />
          <MetricChart
            title="Disk usage"
            detail="Used space on the root filesystem"
            unit="percent"
            color="#ac8fd9"
            data={chartData.disk.data}
            current={disk}
            loading={loading}
            partialRange={chartData.disk.partial}
            rangeMinutes={selectedRange.minutes}
            resolutionSeconds={chartData.disk.resolutionSeconds}
          />
          <section className="chart-panel host-info">
            <h3>Agent</h3>
            <dl>
              <div>
                <dt>Version</dt>
                <dd>{device.agentVersion}</dd>
              </div>
              <div>
                <dt>Platform</dt>
                <dd>
                  {device.platform} / {device.architecture}
                </dd>
              </div>
              <div>
                <dt>Availability</dt>
                <dd>{device.availability}</dd>
              </div>
              <div>
                <dt>Last heartbeat</dt>
                <dd>{device.lastHeartbeat ? new Date(device.lastHeartbeat).toLocaleString() : "Not received"}</dd>
              </div>
            </dl>
          </section>
        </div>
      ) : (
        <div className="empty">
          <Server />
          <h2>
            {isDecommissioned
              ? "Agent revoked"
              : device.availability === "offline"
                ? "Agent offline"
                : "Agent not installed"}
          </h2>
          <p>
            {isDecommissioned
              ? "History is retained, but this identity cannot report or be rediscovered until the owner explicitly re-enables it."
              : device.availability === "offline"
                ? "Current metrics are unavailable. Historical values remain accessible with gaps."
                : "This candidate needs an authorized, trusted enrollment."}
          </p>
        </div>
      )}
      {hasAgent && !isDecommissioned && !demo && diagnosticMetrics.length > 0 && (
        <section className="diagnostic-board" aria-labelledby="diagnostic-board-heading">
          <div className="diagnostic-board-heading">
            <div>
              <Badge variant="outline">
                <ChartNoAxesCombined size={13} />
                Host diagnostics
              </Badge>
              <h2 id="diagnostic-board-heading">Inspect a signal</h2>
              <p>Choose a metric and entity. Counter resets, missing samples, and unavailable fields stay visible.</p>
            </div>
            <small>{diagnosticSeries.length} returned series</small>
          </div>
          <div className="diagnostic-controls">
            <label>
              Metric
              <select
                aria-label="Diagnostic metric"
                value={diagnosticMetric}
                onChange={(event) => setDiagnosticMetric(event.target.value)}
              >
                {diagnosticMetrics.map((metric) => (
                  <option key={metric} value={metric}>
                    {metricLabel(metric)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Entity
              <select
                aria-label="Diagnostic entity"
                value={diagnosticEntity}
                onChange={(event) => setDiagnosticEntity(event.target.value)}
              >
                {diagnosticEntities.map((entity) => (
                  <option key={entity} value={entity}>
                    {entityLabel(entity)}
                  </option>
                ))}
              </select>
            </label>
            {selectedDiagnosticSeries && (
              <Badge variant="outline">
                {selectedDiagnosticSeries.unit} · {diagnosticHistory.data.length} samples
              </Badge>
            )}
          </div>
          {selectedDiagnosticSeries ? (
            <MetricChart
              title={metricLabel(selectedDiagnosticSeries.metric)}
              detail={metricDetail(selectedDiagnosticSeries.metric)}
              unit={selectedDiagnosticSeries.unit}
              color={colorForMetric(selectedDiagnosticSeries.metric)}
              data={diagnosticHistory.data}
              current={chartCurrent(diagnosticHistory.data)}
              loading={loading}
              partialRange={diagnosticHistory.partial}
              rangeMinutes={selectedRange.minutes}
              resolutionSeconds={diagnosticHistory.resolutionSeconds}
            />
          ) : (
            <div className="chart-empty" role="status">
              Select a diagnostic metric with returned history.
            </div>
          )}
        </section>
      )}
      {!demo && (
        <section className={"danger-panel " + (isDecommissioned ? "danger-panel-muted" : "")}>
          <div className="panel-title">
            {isDecommissioned ? <CheckCircle2 size={17} /> : <AlertTriangle size={17} />}
            <div>
              <h3>{isDecommissioned ? "Device is decommissioned" : "Decommission device"}</h3>
              <p>
                {isDecommissioned
                  ? "Re-enable is explicit and revalidates the active scope and access prerequisites."
                  : "Revokes the agent identity, excludes the device, and cancels active enrollment work. History is retained."}
              </p>
            </div>
          </div>
          {operationError && (
            <p className="form-error" role="alert">
              {operationError}
            </p>
          )}
          {operationMessage && (
            <p className="form-success" role="status">
              {operationMessage}
            </p>
          )}
          {isDecommissioned ? (
            <Button variant="outline" disabled={operationBusy} onClick={reenableDevice}>
              Re-enable after policy review
            </Button>
          ) : (
            <div className="operation-form">
              <label>
                Reason
                <textarea
                  required
                  minLength={3}
                  value={reason}
                  onChange={(event) => setReason(event.target.value)}
                  placeholder="Retired host or owner correction"
                />
              </label>
              <label className="checkbox-label">
                <input type="checkbox" checked={uninstall} onChange={(event) => setUninstall(event.target.checked)} />
                <span>Request optional agent uninstall</span>
              </label>
              <small>
                Uninstall is a separate best-effort operation; Scout only reports removal after target confirmation.
              </small>
              <Button
                variant="destructive"
                disabled={operationBusy || reason.trim().length < 3}
                onClick={decommissionDevice}
              >
                {operationBusy ? "Decommissioning…" : "Revoke and exclude device"}
              </Button>
            </div>
          )}
        </section>
      )}
    </>
  );
}

function MetricChart({
  title,
  detail,
  unit,
  color,
  data,
  current,
  loading,
  partialRange,
  rangeMinutes,
  resolutionSeconds,
}: {
  title: string;
  detail: string;
  unit: string;
  color: string;
  data: ChartPoint[];
  current: number | null;
  loading: boolean;
  partialRange: boolean;
  rangeMinutes: number;
  resolutionSeconds?: number;
}) {
  const gapCount = data.filter((point) => point.value === null || point.availability !== "current").length;
  const partialCount = data.filter((point) => point.partial).length;
  const hasRange = data.some((point) => point.min != null || point.max != null);
  const scale = chartScale(unit);
  const chartId = title.toLowerCase().replaceAll(" ", "-");
  return (
    <section className="chart-panel" aria-labelledby={chartId + "-heading"}>
      <div className="chart-heading">
        <div>
          <h3 id={chartId + "-heading"}>{title}</h3>
          <p>
            {detail}
            {gapCount ? " · gaps indicate unavailable samples" : ""}
          </p>
        </div>
        <div className="chart-heading-side">
          <strong>{formatMetricValue(current, unit)}</strong>
          {resolutionSeconds && <small>{formatResolution(resolutionSeconds)} resolution</small>}
        </div>
      </div>
      {loading && !data.length ? (
        <div className="chart-empty" role="status">
          Loading samples…
        </div>
      ) : !data.length ? (
        <div className="chart-empty" role="status">
          No {title.toLowerCase()} samples in this range.
        </div>
      ) : (
        <>
          <ChartContainer
            config={{ usage: { label: title, color } }}
            className="h-[200px] w-full aspect-auto"
            aria-label={
              title +
              " history in " +
              unit +
              " over the last " +
              rangeMinutes +
              " minutes" +
              (gapCount ? ", including unavailable gaps" : "")
            }
          >
            <AreaChart accessibilityLayer data={data} margin={{ top: 8, right: 12, left: 0, bottom: 4 }}>
              <CartesianGrid vertical={false} strokeDasharray="3 4" />
              <XAxis
                dataKey="time"
                type="number"
                domain={["dataMin", "dataMax"]}
                ticks={[data[0]?.time ?? 0, data[data.length - 1]?.time ?? 1]}
                tickLine={false}
                axisLine={false}
                tickFormatter={(value) =>
                  new Date(value).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
                }
              />
              <YAxis
                domain={scale.domain}
                ticks={scale.ticks}
                width={37}
                tickLine={false}
                axisLine={false}
                tickFormatter={(value) => formatMetricValue(Number(value), unit)}
              />
              <ChartTooltip
                content={
                  <ChartTooltipContent
                    labelFormatter={(value) => new Date(Number(value)).toLocaleString()}
                    formatter={(value) =>
                      value == null ? (
                        <span>Unavailable</span>
                      ) : (
                        <span>
                          {title}
                          <strong>{formatMetricValue(Number(value), unit)}</strong>
                        </span>
                      )
                    }
                  />
                }
              />
              <Area
                dataKey="value"
                type="linear"
                stroke="var(--color-usage)"
                fill="var(--color-usage)"
                fillOpacity={0.2}
                strokeWidth={1.7}
                connectNulls={false}
                isAnimationActive={false}
              />
            </AreaChart>
          </ChartContainer>
          <ChartDataQuality gapCount={gapCount} partial={partialRange} />
          {partialCount > 0 && (
            <p className="chart-metadata" role="status">
              {partialCount} partial bucket{partialCount === 1 ? "" : "s"}; values are shown only for the observed
              portion.
            </p>
          )}
          <details className="chart-table">
            <summary>View tabular samples</summary>
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>Observed</th>
                    <th>Value</th>
                    {hasRange && <th>Range</th>}
                    <th>Samples</th>
                    <th>Coverage</th>
                    <th>Bucket</th>
                    <th>Availability</th>
                  </tr>
                </thead>
                <tbody>
                  {data.map((point, index) => (
                    <tr key={point.time + "-" + point.availability + "-" + index}>
                      <td>{new Date(point.time).toLocaleString()}</td>
                      <td>
                        {point.value === null || point.availability !== "current"
                          ? "Unavailable"
                          : formatMetricValue(point.value, unit)}
                      </td>
                      {hasRange && (
                        <td>
                          {point.min != null && point.max != null
                            ? `${formatMetricValue(point.min, unit)} – ${formatMetricValue(point.max, unit)}`
                            : "—"}
                        </td>
                      )}
                      <td>{point.count ?? (point.value === null ? 0 : 1)}</td>
                      <td>{point.coverage == null ? "—" : `${Math.round(point.coverage * 100)}%`}</td>
                      <td>{point.partial ? "Partial" : "Complete"}</td>
                      <td>{point.availability}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </details>
        </>
      )}
    </section>
  );
}

export function metricIcon(metric: string) {
  if (metric.includes("memory")) return MemoryStick;
  if (metric.includes("disk") || metric.includes("filesystem")) return HardDrive;
  return ChartNoAxesCombined;
}
