import { useEffect, useMemo, useState } from "react";
import { ArrowUpRight, Cpu, HardDrive, MemoryStick, Network, Radio, Search, Server } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, APIError, type Device } from "@/lib/api";
import { demoDevices, type Device as DemoDevice } from "@/demo";

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

function stateLabel(device: Device): string {
  if (device.lifecycle === "decommissioned" || device.availability === "revoked") return "Revoked";
  if (!device.agentVersion) return "Needs access";
  if (device.availability === "offline") return "Offline";
  if (device.availability === "connecting") return "Connecting";
  return "Online";
}

function demoToLive(item: DemoDevice, index: number): Device {
  const observedAt = new Date().toISOString();
  return {
    id: "demo-" + index,
    displayName: item.name,
    platform: "linux",
    architecture: "amd64",
    lifecycle: "enrolled",
    addresses: [item.address],
    agentVersion: item.version === "—" ? undefined : item.version,
    availability: item.status === "Offline" ? "offline" : "online",
    metricFreshness: {},
    currentMetrics: {
      "cpu.utilization": { value: item.cpu, unit: "percent", availability: "current", observedAt },
      "memory.used_percent": { value: item.memory, unit: "percent", availability: "current", observedAt },
      "filesystem.used_percent": { value: item.disk, unit: "percent", availability: "current", observedAt },
      "network.receive_rate": { value: item.network * 1024, unit: "bytes_per_second", availability: "current", observedAt },
      "network.transmit_rate": { value: 0, unit: "bytes_per_second", availability: "current", observedAt },
    },
    collectorStates: [],
    revision: 1,
  };
}

export function SystemsView({
  demo,
  query,
  onQuery,
  onSelect,
  onSetup,
}: {
  demo: boolean;
  query: string;
  onQuery: (value: string) => void;
  onSelect: (device: Device) => void;
  onSetup: () => void;
}) {
  const [items, setItems] = useState<Device[]>([]);
  const [loading, setLoading] = useState(!demo);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const [health, setHealth] = useState("");
  const [monitoringState, setMonitoringState] = useState("");

  useEffect(() => {
    if (demo) {
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError("");
    const params = new URLSearchParams();
    if (query.trim()) params.set("query", query.trim());
    if (health) params.set("health", health);
    if (monitoringState) params.set("monitoringState", monitoringState);
    api
      .devices(params.toString() ? "?" + params.toString() : "")
      .then((result) => {
        if (!cancelled) setItems(result.items);
      })
      .catch((caught) => {
        if (!cancelled) setError(caught instanceof APIError ? caught.message : "Could not load systems");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [demo, health, monitoringState, query, retry]);

  const devices = demo ? demoDevices.map(demoToLive) : items;
  const filtered = useMemo(
    () =>
      devices.filter((device) =>
        (
          device.displayName +
          " " +
          (device.hostname ?? "") +
          " " +
          device.addresses.join(" ") +
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

  return (
    <section className="systems-panel">
      <div className="panel-toolbar">
        <div className="summary" aria-label="System totals">
          <span><i className="dot healthy" aria-hidden="true" />{counts.online} online</span>
          <span><i className="dot muted" aria-hidden="true" />{counts.offline} offline</span>
          <span><i className="dot amber" aria-hidden="true" />{counts.access} needs access</span>
        </div>
        <div className="system-filters">
          <div className="search-field">
            <Search size={15} aria-hidden="true" />
            <Input aria-label="Filter systems" placeholder="Filter systems…" value={query} onChange={(event) => onQuery(event.target.value)} />
          </div>
          {!demo && (
            <>
              <label>
                <span className="sr-only">Health filter</span>
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
                <span className="sr-only">Monitoring filter</span>
                <select aria-label="Monitoring filter" value={monitoringState} onChange={(event) => setMonitoringState(event.target.value)}>
                  <option value="">All monitoring</option>
                  <option value="monitored">Monitored</option>
                  <option value="unmonitored">Unmonitored</option>
                  <option value="revoked">Revoked</option>
                </select>
              </label>
            </>
          )}
        </div>
      </div>
      {loading ? (
        <div className="empty" role="status">
          <Radio size={30} />
          <h2>Loading systems</h2>
          <p>Reading authenticated inventory…</p>
        </div>
      ) : error ? (
        <div className="empty" role="alert">
          <Radio size={30} />
          <h2>Systems unavailable</h2>
          <p>{error}</p>
          <Button variant="outline" onClick={() => setRetry((value) => value + 1)}>Retry</Button>
        </div>
      ) : (
        <>
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th><Server aria-hidden="true" />System</th>
                  <th><Cpu aria-hidden="true" />CPU</th>
                  <th><MemoryStick aria-hidden="true" />Memory</th>
                  <th><HardDrive aria-hidden="true" />Disk</th>
                  <th><Network aria-hidden="true" />Network</th>
                  <th><Radio aria-hidden="true" />State</th>
                  <th><span className="sr-only">Details</span></th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((device) => <SystemRow key={device.id} device={device} onSelect={onSelect} />)}
              </tbody>
            </table>
          </div>
          {!filtered.length && (
            <div className="empty">
              <div className="empty-icon"><Radio size={30} /></div>
              <h2>{hasFilters ? "No matching systems" : "Your network starts here"}</h2>
              <p>
                {hasFilters
                  ? "Try a hostname, IP address, status label, or clear the filters."
                  : "Connect your first Linux agent to start building a picture of your network."}
              </p>
              <Button
                variant="outline"
                onClick={() => {
                  if (hasFilters) {
                    onQuery("");
                    setHealth("");
                    setMonitoringState("");
                  } else {
                    onSetup();
                  }
                }}
              >
                {hasFilters ? "Clear filters" : "Agent setup"} <ArrowUpRight size={14} />
              </Button>
              {!hasFilters && !demo && <small>Operational data stays separate from demo mode.</small>}
            </div>
          )}
          <div className="table-footer">
            <span>{filtered.length} systems{demo ? " · illustrative data" : ""}</span>
            <span>One agent per device</span>
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
            <small>{device.addresses[0] ?? device.hostname ?? "Address unavailable"}</small>
          </span>
        </button>
      </td>
      <td>{cpu === null ? <span className="no-value">—</span> : <Meter value={cpu} />}</td>
      <td>{memory === null ? <span className="no-value">—</span> : <Meter value={memory} />}</td>
      <td>{disk === null ? <span className="no-value">—</span> : <Meter value={disk} />}</td>
      <td className="network-value">
        <span className={network === null ? "no-value" : ""} aria-label={network === null ? "Network unavailable" : formatRate(network)}>
          {formatRate(network)}
        </span>
      </td>
      <td>
        <Badge variant="outline" className={state === "Needs access" ? "access-label" : "version"}>{state}</Badge>
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
