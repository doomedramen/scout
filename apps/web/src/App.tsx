import { useEffect, useState } from "react";
import {
  Activity,
  ArrowLeft,
  ArrowUpRight,
  Check,
  ChevronRight,
  CircleHelp,
  Cpu,
  Download,
  HardDrive,
  LayoutList,
  MemoryStick,
  Network,
  Radio,
  Search,
  Server,
  ShieldCheck,
  Terminal,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { demoDevices, type Device } from "./demo";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";

type Page = "Systems" | "Network" | "Updates";
type Status = { mode: string; database: string; enrollmentAvailable: boolean };
const pages = [
  { name: "Systems" as const, icon: LayoutList },
  { name: "Network" as const, icon: Network },
  { name: "Updates" as const, icon: Download },
];

function Meter({ value }: { value: number }) {
  return (
    <div className="meter">
      <span>
        {value.toFixed(1)}
        <small>%</small>
      </span>
      <div className="track">
        <i
          style={{ width: `${value}%` }}
          className={value >= 80 ? "warning" : ""}
        />
      </div>
    </div>
  );
}

function Chart({
  title,
  detail,
  base,
  color,
  seed,
}: {
  title: string;
  detail: string;
  base: number;
  color: string;
  seed: number;
}) {
  const data = Array.from({ length: 61 }, (_, i) => ({
    minute: i - 60,
    usage:
      i === 60
        ? base
        : Number(
            Math.min(
              97,
              Math.max(
                2,
                base +
                  Math.sin(i * 0.78 + seed) * 5 +
                  Math.sin(i * 2.7) * 2 +
                  (i > 37 && i < 44 ? 24 : 0),
              ),
            ).toFixed(1),
          ),
  }));
  return (
    <section className="chart-panel">
      <div className="chart-heading">
        <div>
          <h3>{title}</h3>
          <p>{detail}</p>
        </div>
        <strong>
          {base.toFixed(1)}
          <small>%</small>
        </strong>
      </div>
      <ChartContainer
        config={{ usage: { label: title, color } }}
        className="h-[200px] w-full aspect-auto"
        aria-label={`${title}, illustrative history for the last hour`}
      >
        <AreaChart
          accessibilityLayer
          data={data}
          margin={{ top: 8, right: 12, left: 0, bottom: 4 }}
        >
          <CartesianGrid vertical={false} strokeDasharray="3 4" />
          <XAxis
            dataKey="minute"
            type="number"
            domain={[-60, 0]}
            ticks={[-60, -30, 0]}
            tickLine={false}
            axisLine={false}
            tickMargin={10}
            tickFormatter={(v) => (v === 0 ? "Now" : `${Math.abs(v)} min ago`)}
          />
          <YAxis
            domain={[0, 100]}
            ticks={[0, 50, 100]}
            width={37}
            tickLine={false}
            axisLine={false}
            tickFormatter={(v) => `${v}%`}
          />
          <ChartTooltip
            content={
              <ChartTooltipContent
                labelFormatter={(_, payload) => {
                  const minute = Number(payload[0]?.payload?.minute);
                  return Number.isFinite(minute)
                    ? minute === 0
                      ? "Now"
                      : `${Math.abs(minute)} minutes ago`
                    : "Sample time unavailable";
                }}
                formatter={(v) => (
                  <span className="tooltip-value">
                    {title}
                    <strong>{Number(v).toFixed(1)}%</strong>
                  </span>
                )}
              />
            }
          />
          <Area
            dataKey="usage"
            type="linear"
            stroke="var(--color-usage)"
            fill="var(--color-usage)"
            fillOpacity={0.2}
            strokeWidth={1.7}
            isAnimationActive={false}
          />
        </AreaChart>
      </ChartContainer>
    </section>
  );
}

export default function App() {
  const [page, setPage] = useState<Page>("Systems");
  const [demo, setDemo] = useState(false);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Device | null>(null);
  const [help, setHelp] = useState(false);
  const [status, setStatus] = useState<Status | null>(null);
  const [error, setError] = useState(false);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 5000);
    setError(false);
    fetch("/api/status", { signal: controller.signal })
      .then((r) => {
        if (!r.ok) throw new Error();
        return r.json();
      })
      .then((value: Status) => {
        if (typeof value.database !== "string" || value.mode !== "development")
          throw new Error();
        setStatus(value);
      })
      .catch(() => {
        if (!cancelled) {
          setStatus(null);
          setError(true);
        }
      })
      .finally(() => clearTimeout(timeout));
    let cancelled = false;
    return () => {
      cancelled = true;
      controller.abort();
      clearTimeout(timeout);
    };
  }, [retry]);
  const devices = demo ? demoDevices : [];
  const filtered = devices.filter((d) =>
    `${d.name} ${d.address} ${d.role} ${d.status}`
      .toLowerCase()
      .includes(query.toLowerCase()),
  );
  function navigate(next: Page) {
    setPage(next);
    setSelected(null);
    setQuery("");
  }
  return (
    <div className="app-shell">
      <header className="topbar">
        <a
          href="#"
          className="brand"
          onClick={(e) => {
            e.preventDefault();
            navigate("Systems");
          }}
        >
          <Radio size={24} />
          <span>Scout</span>
        </a>
        <span className="workspace-name">Personal network</span>
        <div className="topbar-actions">
          <Badge variant="outline">Development</Badge>
          <Button
            variant="ghost"
            size="icon"
            aria-label="Setup information"
            aria-expanded={help}
            onClick={() => setHelp(!help)}
          >
            <CircleHelp size={18} />
          </Button>
          <div className="avatar" title="Single-owner workspace">
            M
          </div>
        </div>
      </header>
      <div className="toolbar">
        <nav aria-label="Main navigation">
          {pages.map(({ name, icon: Icon }) => (
            <button
              key={name}
              className={page === name ? "active" : ""}
              aria-current={page === name ? "page" : undefined}
              onClick={() => navigate(name)}
            >
              <Icon size={16} />
              {name}
            </button>
          ))}
        </nav>
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            setDemo(!demo);
            setSelected(null);
            setQuery("");
          }}
        >
          {demo ? <X size={14} /> : <Activity size={14} />}{" "}
          {demo ? "Exit demo" : "Explore demo"}
        </Button>
      </div>
      <main>
        {demo && (
          <div className="notice demo-notice">
            <Activity size={15} />
            <span>
              Demo workspace. Devices, metrics, and network relationships are
              illustrative.
            </span>
            <Badge variant="outline">Sample data</Badge>
          </div>
        )}
        {help && (
          <section className="setup-panel">
            <div>
              <Terminal size={21} />
              <h2>Start with one Linux agent</h2>
            </div>
            <p>
              This development scaffold includes a local host collector. Secure
              enrollment and telemetry ingestion are next; no devices can be
              installed from this console yet.
            </p>
            <code>npm run snapshot -w @scout/agent</code>
            <p>
              Once enrollment is built, Scout will automatically expand within
              your configured network scopes and report devices needing access.
            </p>
            <Button variant="outline" size="sm" onClick={() => setHelp(false)}>
              Close
            </Button>
          </section>
        )}
        {selected ? (
          <>
            <Button
              className="back"
              variant="ghost"
              size="sm"
              onClick={() => navigate("Systems")}
            >
              <ArrowLeft size={15} />
              All systems
            </Button>
            <div className="page-heading">
              <div>
                <h1>{selected.name}</h1>
                <p>
                  <span
                    className={`dot ${selected.status === "Healthy" ? "healthy" : "muted"}`}
                  />
                  {selected.status} <span className="divider">/</span>{" "}
                  {selected.address} <span className="divider">/</span>{" "}
                  {selected.role}
                </p>
              </div>
              <Badge variant="outline">Last hour · demo</Badge>
            </div>
            {selected.status === "Healthy" ? (
              <div className="charts">
                <Chart
                  title="CPU usage"
                  detail="Average utilization across all cores"
                  base={selected.cpu}
                  color="#7392f5"
                  seed={1}
                />
                <Chart
                  title="Memory usage"
                  detail="Used memory as a share of total"
                  base={selected.memory}
                  color="#62b697"
                  seed={3}
                />
                <Chart
                  title="Disk usage"
                  detail="Used space on the root filesystem"
                  base={selected.disk}
                  color="#ac8fd9"
                  seed={7}
                />
                <section className="chart-panel host-info">
                  <h3>Agent</h3>
                  <dl>
                    <div>
                      <dt>Version</dt>
                      <dd>{selected.version}</dd>
                    </div>
                    <div>
                      <dt>Platform</dt>
                      <dd>Linux / amd64</dd>
                    </div>
                    <div>
                      <dt>Connection</dt>
                      <dd>Outbound to Scout server</dd>
                    </div>
                    <div>
                      <dt>Example network rate</dt>
                      <dd>{selected.network} MB/s</dd>
                    </div>
                  </dl>
                </section>
              </div>
            ) : (
              <div className="empty">
                <Server />
                <h2>
                  {selected.status === "Offline"
                    ? "Agent offline"
                    : "Agent not installed"}
                </h2>
                <p>
                  {selected.status === "Offline"
                    ? "Current metrics are unavailable. Historical metrics will remain accessible once storage is implemented."
                    : "Scout will request access when a discovered device cannot be enrolled automatically."}
                </p>
              </div>
            )}
          </>
        ) : (
          <>
            <div className="page-heading">
              <div>
                <h1>
                  {page === "Systems"
                    ? "All systems"
                    : page === "Network"
                      ? "Network map"
                      : "Agent updates"}
                </h1>
                <p>
                  {page === "Systems"
                    ? "Every machine. One clear view."
                    : page === "Network"
                      ? "Understand how your infrastructure connects."
                      : "Keep your fleet current, even without internet access."}
                </p>
              </div>
              {page === "Systems" && (
                <Button variant="outline" onClick={() => setHelp(true)}>
                  <Terminal size={15} />
                  Agent setup
                  <ChevronRight size={14} />
                </Button>
              )}
            </div>
            {page === "Systems" && (
              <section className="systems-panel">
                <div className="panel-toolbar">
                  <div className="summary">
                    <span>
                      <i className="dot healthy" />
                      {demo ? "5" : "0"} healthy
                    </span>
                    <span>
                      <i className="dot muted" />
                      {demo ? "1" : "0"} offline
                    </span>
                    <span>
                      <i className="dot amber" />
                      {demo ? "1" : "0"} needs access
                    </span>
                  </div>
                  <div className="search-field">
                    <Search size={15} />
                    <Input
                      aria-label="Filter systems"
                      placeholder="Filter systems…"
                      value={query}
                      onChange={(e) => setQuery(e.target.value)}
                    />
                  </div>
                </div>
                <div className="table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>
                          <Server />
                          System
                        </th>
                        <th>
                          <Cpu />
                          CPU
                        </th>
                        <th>
                          <MemoryStick />
                          Memory
                        </th>
                        <th>
                          <HardDrive />
                          Disk
                        </th>
                        <th>
                          <Network />
                          Network
                        </th>
                        <th>
                          <Radio />
                          Agent
                        </th>
                        <th>
                          <span className="sr-only">Details</span>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {filtered.map((d) => (
                        <tr key={d.name}>
                          <td>
                            <button
                              className="device-link"
                              onClick={() => setSelected(d)}
                            >
                              <i
                                className={`dot ${d.status === "Healthy" ? "healthy" : d.status === "Offline" ? "muted" : "amber"}`}
                              />
                              <span>
                                <strong>{d.name}</strong>
                                <small>{d.address}</small>
                              </span>
                            </button>
                          </td>
                          {[d.cpu, d.memory, d.disk].map((v, i) => (
                            <td key={i}>
                              {d.status === "Healthy" ? (
                                <Meter value={v} />
                              ) : (
                                <span className="no-value">—</span>
                              )}
                            </td>
                          ))}
                          <td className="network-value">
                            {d.status === "Healthy" ? (
                              <>
                                {d.network.toFixed(2)} <small>MB/s</small>
                              </>
                            ) : (
                              "—"
                            )}
                          </td>
                          <td>
                            <span
                              className={
                                d.status === "Needs access"
                                  ? "access-label"
                                  : "version"
                              }
                            >
                              {d.status === "Needs access"
                                ? "Needs access"
                                : d.version}
                            </span>
                          </td>
                          <td>
                            <Button
                              variant="ghost"
                              size="icon"
                              aria-label={`View ${d.name}`}
                              onClick={() => setSelected(d)}
                            >
                              <ArrowUpRight size={15} />
                            </Button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {!filtered.length && (
                  <div className="empty">
                    <div className="empty-icon">
                      <Radio size={30} />
                    </div>
                    <h2>
                      {query
                        ? "No matching systems"
                        : "Your network starts here"}
                    </h2>
                    <p>
                      {query
                        ? "Try a hostname, IP address, or device status."
                        : "Connect your first Linux agent to start building a picture of your network."}
                    </p>
                    <Button
                      variant="outline"
                      onClick={() => (query ? setQuery("") : setDemo(true))}
                    >
                      {query ? "Clear filter" : "Preview a populated network"}
                      <ChevronRight size={14} />
                    </Button>
                    {!query && (
                      <small>
                        Enrollment is not available in this development build.
                      </small>
                    )}
                  </div>
                )}
                <div className="table-footer">
                  <span>
                    {filtered.length} systems
                    {demo ? " · illustrative data" : ""}
                  </span>
                  <span>One agent per device</span>
                </div>
              </section>
            )}
            {page === "Network" && (
              <section className="network-panel">
                {demo ? (
                  <>
                    <div className="map-header">
                      <Badge variant="outline">10.20.0.0/24</Badge>
                      <span>
                        Illustrative interface membership · not physical cabling
                      </span>
                    </div>
                    <div className="map-root">
                      <Network size={22} />
                      <strong>Lab network</strong>
                      <span>7 observed devices</span>
                    </div>
                    <div className="map-devices">
                      {devices.map((d) => (
                        <button key={d.name} onClick={() => setSelected(d)}>
                          <Server size={20} />
                          <strong>{d.name}</strong>
                          <small>{d.address}</small>
                          <span
                            className={`dot ${d.status === "Healthy" ? "healthy" : d.status === "Offline" ? "muted" : "amber"}`}
                          />
                        </button>
                      ))}
                    </div>
                  </>
                ) : (
                  <div className="empty">
                    <Network size={32} />
                    <h2>No observations yet</h2>
                    <p>
                      Agents will contribute interfaces and neighbors. Every
                      relationship will include its source and age.
                    </p>
                    <Button variant="outline" onClick={() => setDemo(true)}>
                      Preview network map
                    </Button>
                  </div>
                )}
              </section>
            )}
            {page === "Updates" && (
              <section className="updates-panel">
                <div className="update-intro">
                  <ShieldCheck size={28} />
                  <div>
                    <h2>Updates, on your terms</h2>
                    <p>
                      Signed releases delivered through your Scout server. No
                      internet connection required on monitored devices.
                    </p>
                  </div>
                  <Badge variant="outline">Planned</Badge>
                </div>
                <div className="update-features">
                  {[
                    [
                      "Automatic or manual",
                      "Choose scheduled rollouts or push a version from the server.",
                    ],
                    [
                      "Offline-ready delivery",
                      "Import a signed bundle when the server is isolated, too.",
                    ],
                    [
                      "Verified rollback",
                      "Keep the previous verified version if startup checks fail.",
                    ],
                  ].map(([title, description]) => (
                    <div key={title}>
                      <Check size={17} />
                      <h3>{title}</h3>
                      <p>{description}</p>
                    </div>
                  ))}
                </div>
                <p className="implementation-note">
                  The update service is not implemented yet. No releases or
                  rollout actions are available.
                </p>
              </section>
            )}
          </>
        )}
        <footer className="footer">
          <span>
            <ShieldCheck size={14} />
            Self-hosted · single owner
          </span>
          <div aria-live="polite">
            {error ? (
              <>
                <span className="dot amber" />
                API unavailable{" "}
                <button onClick={() => setRetry(retry + 1)}>Retry</button>
              </>
            ) : (
              <>
                <span className={`dot ${status ? "healthy" : "muted"}`} />
                {status
                  ? `API connected · PostgreSQL ${status.database}`
                  : "Connecting to API…"}
              </>
            )}
          </div>
        </footer>
      </main>
    </div>
  );
}
