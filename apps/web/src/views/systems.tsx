import { useEffect, useMemo, useState } from "react";
import { ArrowUpRight, Cpu, HardDrive, MemoryStick, Network, Search, Server, SlidersHorizontal } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { isActionableAccessCandidate } from "@/lib/access";
import { api, APIError, type Candidate, type Device } from "@/lib/api";
import type { SystemsRouteData } from "../../app/lib/route-data";

function Meter({ value }: { value: number }) {
  return (
    <div className="meter" aria-label={value.toFixed(1) + " percent"}>
      <span>
        {value.toFixed(1)}
        <small>%</small>
      </span>
      <div className="track">
        <i
          style={{ width: Math.max(0, Math.min(100, value)) + "%" }}
          className={value >= 80 ? "warning" : ""}
          aria-hidden="true"
        />
      </div>
    </div>
  );
}

function liveValue(device: Device, metric: string): number | null {
  const value = device.currentMetrics?.[metric]?.value;
  return typeof value === "number" ? value : null;
}

function networkValue(device: Device): number | null {
  const receive = liveValue(device, "network.receive_rate");
  const transmit = liveValue(device, "network.transmit_rate");
  if (receive === null && transmit === null) return null;
  return (receive ?? 0) + (transmit ?? 0);
}

function formatRate(value: number | null): string {
  if (value === null) return "—";
  if (value < 1024) return value.toFixed(0) + " B/s";
  if (value < 1024 * 1024) return (value / 1024).toFixed(1) + " KiB/s";
  return (value / (1024 * 1024)).toFixed(1) + " MiB/s";
}

function shortDeviceID(id: string): string {
  return id.length > 12 ? `${id.slice(0, 8)}…${id.slice(-4)}` : id;
}

function deviceIdentity(device: Device): string {
  const macs = (device.identifiers ?? []).filter((item) => item.kind.toLowerCase() === "mac").map((item) => item.value);
  const address = device.addresses?.[0] ?? device.hostname ?? "IP unavailable";
  return `${address} · ID ${shortDeviceID(device.id)}${macs.length ? ` · MAC ${macs.join(", ")}` : ""}`;
}

function stateLabel(device: Device): string {
  if (device.lifecycle === "decommissioned" || device.availability === "revoked") return "Revoked";
  if (!device.agentVersion) return "Needs access";
  if (device.availability === "offline") return "Offline";
  if (device.availability === "connecting") return "Connecting";
  return "Online";
}

function isBootstrapPlaceholder(device: Device): boolean {
  return (
    !device.candidateId &&
    device.lifecycle === "candidate" &&
    !device.agentId &&
    !device.agentVersion &&
    (device.addresses ?? []).length === 0
  );
}

function candidateNeedsAccess(candidate: Candidate): boolean {
  return !candidate.deviceId && isActionableAccessCandidate(candidate);
}

function candidateDevice(candidate: Candidate): Device {
  return {
    id: `candidate:${candidate.id}`,
    candidateId: candidate.id,
    displayName: candidate.displayName || candidate.hostname || candidate.address,
    siteId: candidate.siteId,
    platform: "linux",
    architecture: "unknown",
    hostname: candidate.hostname,
    addresses: [candidate.address],
    lifecycle: "candidate",
    availability: "connecting",
    metricFreshness: {},
    collectorStates: [],
    revision: candidate.scopeRevision || 1,
  };
}

export function SystemsView({
  initialData,
  query,
  onQuery,
  onSelect,
}: {
  initialData?: SystemsRouteData;
  query: string;
  onQuery: (value: string) => void;
  onSelect: (device: Device) => void;
}) {
  const [items, setItems] = useState<Device[]>(() => initialData?.devices ?? []);
  const [candidates, setCandidates] = useState<Candidate[]>(() => initialData?.candidates ?? []);
  const [loading, setLoading] = useState(!initialData);
  const [error, setError] = useState(() => initialData?.error ?? "");
  const [retry, setRetry] = useState(0);
  const [health, setHealth] = useState("");
  const [monitoringState, setMonitoringState] = useState("");
  const [filtersOpen, setFiltersOpen] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setError("");
    const params = new URLSearchParams();
    if (query.trim()) params.set("query", query.trim());
    if (health) params.set("health", health);
    if (monitoringState) params.set("monitoringState", monitoringState);
    const load = (initial: boolean) => {
      if (initial) setLoading(true);
      Promise.all([api.devices(params.toString() ? "?" + params.toString() : ""), api.candidates("?limit=500")])
        .then(([deviceResult, candidateResult]) => {
          if (cancelled) return;
          setItems(deviceResult.items);
          setCandidates(candidateResult.items);
          setError("");
        })
        .catch((caught) => {
          if (!cancelled) setError(caught instanceof APIError ? caught.message : "Could not load systems");
        })
        .finally(() => {
          if (!cancelled && initial) setLoading(false);
        });
    };
    if (!initialData || retry > 0 || query || health || monitoringState) load(true);
    const timer = window.setInterval(() => load(false), 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [health, monitoringState, query, retry]);

  const devices = useMemo(() => {
    // Pending invitation records are useful inventory state. Keep them visible as "Needs access"
    // so an owner can resume setup after a refresh instead of losing the recovery path.
    const realDevices = items;
    const provisionalDevices = candidates
      .filter(candidateNeedsAccess)
      .filter((candidate) => {
        if (health && health !== "needs_access") return false;
        return monitoringState !== "monitored" && monitoringState !== "revoked";
      })
      .map(candidateDevice);
    const realIDs = new Set(realDevices.map((device) => device.id));
    return [...realDevices, ...provisionalDevices.filter((device) => !realIDs.has(device.id))];
  }, [candidates, health, items, monitoringState]);
  const filtered = useMemo(
    () =>
      devices.filter((device) =>
        (
          device.displayName +
          " " +
          (device.hostname ?? "") +
          " " +
          (device.addresses ?? []).join(" ") +
          " " +
          stateLabel(device) +
          " " +
          device.platform
        )
          .toLowerCase()
          .includes(query.toLowerCase()),
      ),
    [devices, query],
  );
  const counts = {
    online: devices.filter((device) => stateLabel(device) === "Online").length,
    offline: devices.filter((device) => stateLabel(device) === "Offline").length,
    access: devices.filter((device) => stateLabel(device) === "Needs access").length,
  };
  const hasFilters = Boolean(query || health || monitoringState);
  const secondaryFilterCount = [health, monitoringState].filter(Boolean).length;

  function clearFilters() {
    onQuery("");
    setHealth("");
    setMonitoringState("");
  }

  return (
    <section className="systems-panel">
      <div className="panel-toolbar">
        <div className="summary" aria-label="System totals">
          <span>
            <i className="dot healthy" aria-hidden="true" />
            {counts.online} online
          </span>
          <span>
            <i className="dot muted" aria-hidden="true" />
            {counts.offline} offline
          </span>
          <span>
            <i className="dot amber" aria-hidden="true" />
            {counts.access} needs access
          </span>
        </div>
        <div className="system-filters">
          <div className="search-field">
            <Search size={15} aria-hidden="true" />
            <Input
              aria-label="Filter systems"
              placeholder="Filter systems…"
              value={query}
              onChange={(event) => onQuery(event.target.value)}
            />
          </div>
          <Sheet open={filtersOpen} onOpenChange={setFiltersOpen}>
            <SheetTrigger asChild>
              <Button type="button" variant="outline" className="system-filter-trigger">
                <SlidersHorizontal size={15} />
                Filters{secondaryFilterCount ? ` (${secondaryFilterCount})` : ""}
              </Button>
            </SheetTrigger>
            <SheetContent side="right" className="filter-sheet">
              <SheetHeader>
                <SheetTitle>System filters</SheetTitle>
              </SheetHeader>
              <div className="filter-sheet-fields">
                <label>
                  Health
                  <select aria-label="Health filter" value={health} onChange={(event) => setHealth(event.target.value)}>
                    <option value="">All health</option>
                    <option value="healthy">Healthy</option>
                    <option value="degraded">Degraded</option>
                    <option value="offline">Offline</option>
                    <option value="needs_access">Needs access</option>
                    <option value="revoked">Revoked</option>
                  </select>
                </label>
                <label>
                  Monitoring
                  <select
                    aria-label="Monitoring filter"
                    value={monitoringState}
                    onChange={(event) => setMonitoringState(event.target.value)}
                  >
                    <option value="">All monitoring</option>
                    <option value="monitored">Monitored</option>
                    <option value="unmonitored">Unmonitored</option>
                    <option value="revoked">Revoked</option>
                  </select>
                </label>
              </div>
              <SheetFooter>
                <Button type="button" variant="ghost" onClick={clearFilters} disabled={!hasFilters}>
                  Clear filters
                </Button>
              </SheetFooter>
            </SheetContent>
          </Sheet>
        </div>
      </div>
      {loading ? (
        <div className="empty" role="status">
          <Server size={30} />
          <h2>Loading systems</h2>
        </div>
      ) : error ? (
        <div className="empty" role="alert">
          <Server size={30} />
          <h2>Systems unavailable</h2>
          <p>{error}</p>
          <Button variant="outline" onClick={() => setRetry((value) => value + 1)}>
            Retry
          </Button>
        </div>
      ) : (
        <>
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>
                    <Server aria-hidden="true" />
                    System
                  </th>
                  <th>
                    <Cpu aria-hidden="true" />
                    CPU
                  </th>
                  <th>
                    <MemoryStick aria-hidden="true" />
                    Memory
                  </th>
                  <th>
                    <HardDrive aria-hidden="true" />
                    Disk
                  </th>
                  <th>
                    <Network aria-hidden="true" />
                    Network
                  </th>
                  <th>State</th>
                  <th>
                    <span className="sr-only">Details</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((device) => (
                  <SystemRow key={device.id} device={device} onSelect={onSelect} />
                ))}
              </tbody>
            </table>
          </div>
          {!filtered.length && (
            <div className="empty">
              <div className="empty-icon">
                <Server size={30} />
              </div>
              <h2>{hasFilters ? "No matching systems" : "No systems monitored"}</h2>
              {!hasFilters && (
                <p>Found systems appear after a network scan. Use + in the top navigation to add one manually.</p>
              )}
              {hasFilters && (
                <Button variant="outline" onClick={clearFilters}>
                  Clear filters <ArrowUpRight size={14} />
                </Button>
              )}
            </div>
          )}
          <div className="table-footer">
            <span>{filtered.length} systems</span>
          </div>
        </>
      )}
    </section>
  );
}

function SystemRow({ device, onSelect }: { device: Device; onSelect: (device: Device) => void }) {
  const state = stateLabel(device);
  const cpu = liveValue(device, "cpu.utilization");
  const memory = liveValue(device, "memory.used_percent");
  const disk = liveValue(device, "filesystem.used_percent");
  const network = networkValue(device);
  const stateClass = state === "Online" ? "healthy" : state === "Needs access" ? "amber" : "muted";
  return (
    <tr>
      <td>
        <button className="device-link" onClick={() => onSelect(device)}>
          <i className={"dot " + stateClass} aria-hidden="true" />
          <span>
            <strong>{device.displayName}</strong>
            <small title={device.id}>{deviceIdentity(device)}</small>
          </span>
        </button>
      </td>
      <td>{cpu === null ? <span className="no-value">—</span> : <Meter value={cpu} />}</td>
      <td>{memory === null ? <span className="no-value">—</span> : <Meter value={memory} />}</td>
      <td>{disk === null ? <span className="no-value">—</span> : <Meter value={disk} />}</td>
      <td className="network-value">
        <span
          className={network === null ? "no-value" : ""}
          aria-label={network === null ? "Network unavailable" : formatRate(network)}
        >
          {formatRate(network)}
        </span>
      </td>
      <td>
        <Badge variant="outline" className={state === "Needs access" ? "access-label" : "version"}>
          {state}
        </Badge>
        {device.agentVersion && <small className="version-detail">v{device.agentVersion}</small>}
      </td>
      <td>
        <Button variant="ghost" size="icon" aria-label={"View " + device.displayName} onClick={() => onSelect(device)}>
          <ArrowUpRight size={15} />
        </Button>
      </td>
    </tr>
  );
}
