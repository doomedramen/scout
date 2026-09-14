"use client";

import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Activity, HardDrive, LoaderCircle, MemoryStick, RefreshCw } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

type HostMetrics = {
  cpuUsagePercent?: number | null;
  memoryUsedBytes?: number | null;
  memoryTotalBytes?: number | null;
  uptimeSeconds?: number | null;
};

type MetricPayload = {
  droppedSamples?: number;
  host?: HostMetrics;
};

type MetricSample = {
  observedAt: string;
  receivedAt: string;
  payload: MetricPayload;
};

type MetricsResponse = {
  current: {
    observedAt: string;
    receivedAt: string;
    droppedSamples: number;
  } | null;
  droppedSamples: number;
  samples: MetricSample[];
  rollups: Array<{ bucketStart: string; payload: MetricPayload }>;
};

function asMetrics(value: unknown): MetricsResponse {
  if (!value || typeof value !== "object") throw new Error("Metrics response is invalid.");
  const candidate = value as Partial<MetricsResponse>;
  if (!Array.isArray(candidate.samples) || !Array.isArray(candidate.rollups))
    throw new Error("Metrics response is invalid.");
  return {
    current: candidate.current ?? null,
    droppedSamples: typeof candidate.droppedSamples === "number" ? candidate.droppedSamples : 0,
    samples: candidate.samples as MetricSample[],
    rollups: candidate.rollups as MetricsResponse["rollups"],
  };
}

function number(value: number | null | undefined, suffix = ""): string {
  return typeof value === "number" && Number.isFinite(value) ? `${value.toFixed(1)}${suffix}` : "—";
}

function bytes(value: number | null | undefined): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "—";
  if (value < 1_000) return `${value} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let current = value;
  let unit = -1;
  while (current >= 1_000 && unit < units.length - 1) {
    current /= 1_000;
    unit += 1;
  }
  return `${current.toFixed(current >= 10 ? 0 : 1)} ${units[unit]}`;
}

function age(value: string | null): string {
  if (!value) return "Not reported";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "Unknown" : parsed.toLocaleString();
}

function host(sample: MetricSample | null): HostMetrics {
  return sample?.payload.host ?? {};
}

export function MetricsPanel({ systemId }: { systemId: string }) {
  const [metrics, setMetrics] = useState<MetricsResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const response = await fetch(`/api/v1/systems/${systemId}/metrics`, {
          cache: "no-store",
          signal: signal ?? AbortSignal.timeout(8_000),
        });
        const payload: unknown = await response.json().catch(() => null);
        if (!response.ok) {
          const message =
            payload && typeof payload === "object" && "error" in payload
              ? String((payload as { error?: { message?: string } }).error?.message ?? "")
              : "";
          throw new Error(message || "Metrics could not be loaded.");
        }
        setMetrics(asMetrics(payload));
        setError("");
      } catch (reason) {
        if (reason instanceof DOMException && reason.name === "AbortError") return;
        setError(reason instanceof Error ? reason.message : "Metrics could not be loaded.");
      } finally {
        setLoading(false);
      }
    },
    [systemId],
  );

  useEffect(() => {
    const controller = new AbortController();
    const initialLoad = window.setTimeout(() => void load(controller.signal), 0);
    const timer = window.setInterval(() => void load(), 15_000);
    return () => {
      window.clearTimeout(initialLoad);
      controller.abort();
      window.clearInterval(timer);
    };
  }, [load]);

  const latest = metrics?.samples.at(-1) ?? null;
  const latestHost = host(latest);
  const history = metrics?.samples.slice(-8).reverse() ?? [];

  return (
    <Card className="lg:col-span-2">
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div>
          <CardTitle>Telemetry</CardTitle>
          <CardDescription>Current host measurements and recent history.</CardDescription>
        </div>
        {metrics ? (
          <Badge variant="outline">Updated {age(metrics.current?.receivedAt ?? null)}</Badge>
        ) : null}
      </CardHeader>
      <CardContent className="space-y-6">
        {loading && !metrics ? (
          <div className="flex items-center gap-2 text-sm text-muted-foreground" role="status">
            <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />
            Loading telemetry…
          </div>
        ) : null}
        {error ? (
          <Alert variant="destructive">
            <AlertTitle>Telemetry unavailable</AlertTitle>
            <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
              <span>{error}</span>
              <Button variant="outline" size="sm" onClick={() => void load()}>
                <RefreshCw data-icon="inline-start" /> Retry
              </Button>
            </AlertDescription>
          </Alert>
        ) : null}
        {!loading && !error && !latest ? (
          <div className="rounded-lg border border-dashed px-6 py-10 text-center text-sm text-muted-foreground">
            <Activity className="mx-auto mb-3 size-6" aria-hidden="true" />
            Waiting for the first telemetry sample from this host.
          </div>
        ) : null}
        {latest ? (
          <>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <MetricCard
                icon={<Activity aria-hidden="true" />}
                label="CPU"
                value={number(latestHost.cpuUsagePercent, "%")}
              />
              <MetricCard
                icon={<MemoryStick aria-hidden="true" />}
                label="Memory"
                value={`${bytes(latestHost.memoryUsedBytes)} / ${bytes(latestHost.memoryTotalBytes)}`}
              />
              <MetricCard
                icon={<HardDrive aria-hidden="true" />}
                label="Uptime"
                value={duration(latestHost.uptimeSeconds)}
              />
              <MetricCard
                icon={<RefreshCw aria-hidden="true" />}
                label="Dropped samples"
                value={String(metrics?.droppedSamples ?? 0)}
              />
            </div>
            <div>
              <h3 className="font-medium">Recent samples</h3>
              <div className="mt-3 divide-y rounded-lg border">
                {history.map((sample) => {
                  const sampleHost = host(sample);
                  return (
                    <div
                      key={`${sample.observedAt}-${sample.receivedAt}`}
                      className="grid gap-2 px-4 py-3 text-sm sm:grid-cols-[1fr_auto_auto] sm:items-center"
                    >
                      <span className="text-muted-foreground">{age(sample.observedAt)}</span>
                      <span>CPU {number(sampleHost.cpuUsagePercent, "%")}</span>
                      <span>
                        Memory {bytes(sampleHost.memoryUsedBytes)} /{" "}
                        {bytes(sampleHost.memoryTotalBytes)}
                      </span>
                    </div>
                  );
                })}
              </div>
            </div>
          </>
        ) : null}
      </CardContent>
    </Card>
  );
}

function MetricCard({ icon, label, value }: { icon: ReactNode; label: string; value: string }) {
  return (
    <div className="rounded-lg border p-4">
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <span className="size-4">{icon}</span>
        {label}
      </div>
      <p className="mt-2 text-xl font-semibold tabular-nums">{value}</p>
    </div>
  );
}

function duration(value: number | null | undefined): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "—";
  const days = Math.floor(value / 86_400);
  const hours = Math.floor((value % 86_400) / 3_600);
  const minutes = Math.floor((value % 3_600) / 60);
  if (days) return `${days}d ${hours}h`;
  if (hours) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}
