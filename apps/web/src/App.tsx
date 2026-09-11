import { lazy, Suspense, useEffect, useState } from "react";
import {
  Activity,
  BellRing,
  Bell,
  CircleHelp,
  Download,
  KeyRound,
  LayoutList,
  Network,
  Radio,
  ScanSearch,
  ServerCog,
  Terminal,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api, type Device, type Status } from "@/lib/api";
import { AccessView } from "@/views/access";
import { AgentSetupView } from "@/views/agent-setup";
import { EnrollmentView } from "@/views/enrollment";
import { IncidentsView } from "@/views/incidents";
import { NetworkView } from "@/views/network";
import { NotificationsView } from "@/views/notifications";
import { RecoveryView } from "@/views/recovery";
import { ScopesView } from "@/views/scopes";
import { ServicesView } from "@/views/services";
import { SetupView } from "@/views/setup";
import { SystemsView } from "@/views/systems";
import { UpdatesView } from "@/views/updates";

type Page =
  "Systems" | "Incidents" | "Network" | "Notifications" | "Updates" | "Access" | "Scopes" | "Enrollment" | "Services";

const pages = [
  { name: "Systems" as const, icon: LayoutList },
  { name: "Incidents" as const, icon: BellRing },
  { name: "Notifications" as const, icon: Bell },
  { name: "Network" as const, icon: Network },
  { name: "Scopes" as const, icon: ScanSearch },
  { name: "Enrollment" as const, icon: ServerCog },
  { name: "Access" as const, icon: KeyRound },
  { name: "Services" as const, icon: Terminal },
  { name: "Updates" as const, icon: Download },
];

const pageDetails: Record<Page, { title: string; description: string }> = {
  Systems: { title: "All systems", description: "Every machine. One clear view." },
  Incidents: { title: "Incidents", description: "See what needs attention, with evidence and history." },
  Network: { title: "Network map", description: "Understand how infrastructure connects, with evidence." },
  Notifications: { title: "Notifications", description: "Choose where Scout sends alerts and when it stays quiet." },
  Scopes: { title: "Scopes", description: "Define where Scout may observe and enroll." },
  Enrollment: { title: "Enrollment", description: "Bounded discovery and automatic enrollment progress." },
  Access: { title: "Access", description: "Resolve credentials and trust without exposing secrets." },
  Services: { title: "Services", description: "Provider health and associated service entities." },
  Updates: { title: "Agent updates", description: "Keep agents current, including on isolated networks." },
};

const LazyDeviceView = lazy(() => import("@/views/device").then(({ DeviceView }) => ({ default: DeviceView })));

export default function App() {
  const [auth, setAuth] = useState<"loading" | "signedOut" | "signedIn">("loading");
  const [page, setPage] = useState<Page>("Systems");
  const [demo, setDemo] = useState(false);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Device | null>(null);
  const [incidentFocus, setIncidentFocus] = useState("");
  const [help, setHelp] = useState(false);
  const [status, setStatus] = useState<Status | null>(null);
  const [statusError, setStatusError] = useState(false);

  useEffect(() => {
    let cancelled = false;
    api
      .owner()
      .then(() => {
        if (!cancelled) setAuth("signedIn");
      })
      .catch(() => {
        if (!cancelled) setAuth("signedOut");
      });
    api
      .status()
      .then((value) => {
        if (!cancelled) setStatus(value);
      })
      .catch(() => {
        if (!cancelled) setStatusError(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (auth !== "signedIn") {
    return (
      <>
        <header className="topbar auth-topbar">
          <a className="brand" href="/" onClick={(event) => event.preventDefault()}>
            <Radio size={24} />
            <span>Scout</span>
          </a>
          <Badge variant="outline">{status?.mode ?? "Self-hosted"}</Badge>
        </header>
        <SetupView onSignedIn={() => setAuth("signedIn")} />
      </>
    );
  }

  function navigate(next: Page) {
    setPage(next);
    setSelected(null);
    setQuery("");
    if (next !== "Incidents") setIncidentFocus("");
  }

  function openIncident(incidentId: string) {
    setIncidentFocus(incidentId);
    navigate("Incidents");
  }

  const detail = pageDetails[page];

  return (
    <div className="app-shell">
      <header className="topbar">
        <a
          href="#"
          className="brand"
          onClick={(event) => {
            event.preventDefault();
            navigate("Systems");
          }}
        >
          <Radio size={24} />
          <span>Scout</span>
        </a>
        <span className="workspace-name">Personal network</span>
        <div className="topbar-actions">
          <Badge variant="outline">{status?.mode ?? "Self-hosted"}</Badge>
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
            O
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
          {demo ? <Activity size={14} /> : <Radio size={14} />}
          {demo ? "Exit demo" : "Explore demo"}
        </Button>
      </div>
      <main>
        {status?.recoveryMode && <RecoveryView onChanged={() => window.location.reload()} />}
        {demo && (
          <div className="notice demo-notice">
            <Activity size={15} />
            <span>
              Demo workspace. Devices, metrics, and network relationships are illustrative and never enter operational
              storage.
            </span>
            <Badge variant="outline">Sample data</Badge>
          </div>
        )}
        {help && <AgentSetupView onClose={() => setHelp(false)} />}
        {selected && (page === "Systems" || page === "Network") ? (
          <Suspense
            fallback={
              <div className="empty" role="status">
                Loading host detail…
              </div>
            }
          >
            <LazyDeviceView selected={selected} demo={demo} onBack={() => setSelected(null)} onChanged={setSelected} />
          </Suspense>
        ) : (
          <>
            <div className="page-heading">
              <div>
                <h1>{detail.title}</h1>
                <p>{detail.description}</p>
              </div>
              {page === "Systems" && (
                <Button variant="outline" onClick={() => setHelp(true)}>
                  <Terminal size={15} />
                  Agent setup
                </Button>
              )}
            </div>
            {page === "Systems" && (
              <SystemsView
                demo={demo}
                query={query}
                onQuery={setQuery}
                onSelect={setSelected}
                onSetup={() => setHelp(true)}
              />
            )}
            {page === "Incidents" && (
              <IncidentsView focusIncidentId={incidentFocus} onFocusConsumed={() => setIncidentFocus("")} />
            )}
            {page === "Network" && <NetworkView demo={demo} onSelect={setSelected} />}
            {page === "Notifications" && <NotificationsView />}
            {page === "Scopes" && <ScopesView />}
            {page === "Enrollment" && <EnrollmentView />}
            {page === "Access" && <AccessView />}
            {page === "Services" && <ServicesView onOpenIncident={openIncident} />}
            {page === "Updates" && <UpdatesView />}
          </>
        )}
      </main>
      <footer className="footer">
        <span>Self-hosted · single owner</span>
        <div aria-live="polite">
          {statusError ? (
            <>
              <span className="dot amber" />
              API unavailable
            </>
          ) : (
            <>
              <span className={"dot " + (status?.database === "connected" ? "healthy" : "muted")} />
              {status ? "API " + status.database : "Connecting to API…"}
            </>
          )}
        </div>
      </footer>
    </div>
  );
}
