import { lazy, Suspense, useEffect, useState } from "react";
import {
  BellRing,
  Bell,
  Cpu,
  Download,
  HardDrive,
  KeyRound,
  LayoutDashboard,
  LayoutList,
  Menu,
  Network,
  Plus,
  ScanSearch,
  ServerCog,
  Settings2,
  ShieldAlert,
  Terminal,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { NotificationMenu } from "@/components/notification-menu";
import { api, type Candidate, type Device, type Status } from "@/lib/api";
import { AccessView } from "@/views/access";
import { AgentSetupView } from "@/views/agent-setup";
import { CandidateView } from "@/views/candidate";
import { EnrollmentView } from "@/views/enrollment";
import { IncidentsView } from "@/views/incidents";
import { HardwareView } from "@/views/hardware";
import { NetworkView } from "@/views/network";
import { NotificationsView } from "@/views/notifications";
import { OverviewView } from "@/views/overview";
import { RecoveryView } from "@/views/recovery";
import { ScopesView } from "@/views/scopes";
import { ServicesView } from "@/views/services";
import { SettingsView } from "@/views/settings";
import { StorageView } from "@/views/storage";
import { SetupView } from "@/views/setup";
import { SystemsView } from "@/views/systems";
import { UpdatesView } from "@/views/updates";

type Page =
  | "Overview"
  | "Systems"
  | "Incidents"
  | "Network"
  | "Notifications"
  | "Updates"
  | "Access"
  | "Scopes"
  | "Enrollment"
  | "Services"
  | "Hardware"
  | "Storage"
  | "Settings";

const pages = [
  { name: "Overview" as const, icon: LayoutDashboard },
  { name: "Systems" as const, icon: LayoutList },
  { name: "Incidents" as const, icon: BellRing },
  { name: "Notifications" as const, icon: Bell },
  { name: "Network" as const, icon: Network },
  { name: "Scopes" as const, icon: ScanSearch },
  { name: "Enrollment" as const, icon: ServerCog },
  { name: "Access" as const, icon: KeyRound },
  { name: "Services" as const, icon: Terminal },
  { name: "Hardware" as const, icon: Cpu },
  { name: "Storage" as const, icon: HardDrive },
  { name: "Updates" as const, icon: Download },
];

const primaryPages = pages.filter(({ name }) => ["Overview", "Systems", "Incidents", "Network"].includes(name));
const settingsPages: Page[] = ["Scopes", "Enrollment", "Access", "Services", "Hardware", "Storage", "Updates"];

const pageSlugs: Record<Page, string> = {
  Overview: "overview",
  Systems: "systems",
  Incidents: "incidents",
  Network: "network",
  Notifications: "notifications",
  Scopes: "scopes",
  Enrollment: "enrollment",
  Access: "access",
  Services: "services",
  Hardware: "hardware",
  Storage: "storage",
  Updates: "updates",
  Settings: "settings",
};

const pageFromSlug = new Map(Object.entries(pageSlugs).map(([page, slug]) => [slug, page as Page]));

function pageFromHash(): Page {
  const slug = window.location.hash.replace(/^#\/?/, "").split("/")[0];
  return pageFromSlug.get(slug) ?? "Overview";
}

const pageDetails: Record<Page, { title: string }> = {
  Overview: { title: "Fleet overview" },
  Systems: { title: "All systems" },
  Incidents: { title: "Incidents" },
  Network: { title: "Network map" },
  Notifications: { title: "Notifications" },
  Scopes: { title: "Scopes" },
  Enrollment: { title: "Enrollment" },
  Access: { title: "Access" },
  Services: { title: "Services" },
  Hardware: {
    title: "Hardware telemetry",
  },
  Storage: {
    title: "Storage health",
  },
  Updates: { title: "Agent updates" },
  Settings: { title: "Settings" },
};

const LazyDeviceView = lazy(() => import("@/views/device").then(({ DeviceView }) => ({ default: DeviceView })));

export default function App() {
  const [auth, setAuth] = useState<"loading" | "signedOut" | "signedIn">("loading");
  const [page, setPage] = useState<Page>(() => pageFromHash());
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Device | null>(null);
  const [selectedCandidate, setSelectedCandidate] = useState<Candidate | null>(null);
  const [incidentFocus, setIncidentFocus] = useState("");
  const [agentSetupOpen, setAgentSetupOpen] = useState(false);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [manageRulesRequest, setManageRulesRequest] = useState(0);
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

  useEffect(() => {
    const onHashChange = () => {
      const hash = window.location.hash.replace(/^#\/?/, "").split("/");
      const next = pageFromSlug.get(hash[0]) ?? "Overview";
      setPage(next);
      setSelected(null);
      setSelectedCandidate(null);
      setIncidentFocus(hash[0] === "incidents" ? (hash[1] ?? "") : "");
      setMobileNavOpen(false);
    };
    window.addEventListener("hashchange", onHashChange);
    window.addEventListener("popstate", onHashChange);
    return () => {
      window.removeEventListener("hashchange", onHashChange);
      window.removeEventListener("popstate", onHashChange);
    };
  }, []);

  useEffect(() => {
    if (auth !== "signedIn") return;
    const parts = window.location.hash.replace(/^#\/?/, "").split("/");
    if (parts[1] === "candidate" && parts[2] && !selectedCandidate) {
      let cancelled = false;
      api
        .candidate(decodeURIComponent(parts[2]))
        .then((detail) => {
          if (!cancelled) setSelectedCandidate(detail.candidate);
        })
        .catch(() => {
          if (!cancelled) window.history.replaceState({}, "", "#/network");
        });
      return () => {
        cancelled = true;
      };
    }
    if (parts[1] !== "device" || !parts[2] || selected) return;
    let cancelled = false;
    api
      .device(decodeURIComponent(parts[2]))
      .then((device) => {
        if (!cancelled) setSelected(device);
      })
      .catch(() => {
        if (!cancelled) window.history.replaceState({}, "", `#/${parts[0] || "overview"}`);
      });
    return () => {
      cancelled = true;
    };
  }, [auth, page, selected, selectedCandidate]);

  if (auth !== "signedIn") {
    return (
      <>
        <header className="topbar auth-topbar">
          <a className="brand" href="/" onClick={(event) => event.preventDefault()}>
            <span>Scout</span>
          </a>
          <Badge variant="outline">{status?.mode ?? "Self-hosted"}</Badge>
        </header>
        <SetupView onSignedIn={() => setAuth("signedIn")} />
      </>
    );
  }

  function navigate(next: Page, detail?: { incidentId?: string }) {
    setPage(next);
    setSelected(null);
    setSelectedCandidate(null);
    setQuery("");
    const suffix = next === "Incidents" && detail?.incidentId ? `/${encodeURIComponent(detail.incidentId)}` : "";
    const hash = `#/${pageSlugs[next]}${suffix}`;
    if (window.location.hash !== hash) window.history.pushState({}, "", hash);
    if (next !== "Incidents") setIncidentFocus("");
    setMobileNavOpen(false);
  }

  function openIncident(incidentId: string) {
    setIncidentFocus(incidentId);
    navigate("Incidents", { incidentId });
  }

  function openCandidate(candidate: Candidate) {
    setPage("Network");
    setSelected(null);
    setSelectedCandidate(candidate);
    setMobileNavOpen(false);
    const hash = `#/network/candidate/${encodeURIComponent(candidate.id)}`;
    if (window.location.hash !== hash) window.history.pushState({}, "", hash);
  }

  function openAccess(candidate: Candidate) {
    setPage("Access");
    setSelected(null);
    setSelectedCandidate(candidate);
    setQuery("");
    setMobileNavOpen(false);
    const hash = `#/access/candidate/${encodeURIComponent(candidate.id)}`;
    if (window.location.hash !== hash) window.history.pushState({}, "", hash);
  }

  function openDevice(device: Device) {
    const targetPage = page === "Overview" ? "Systems" : page;
    setPage(targetPage);
    setSelected(device);
    const hash = `#/${pageSlugs[targetPage]}/device/${encodeURIComponent(device.id)}`;
    if (window.location.hash !== hash) window.history.pushState({}, "", hash);
  }

  const detail = pageDetails[page];
  const settingsActive = page === "Settings" || settingsPages.some((name) => name === page);

  return (
    <div className="app-shell">
      <header className="topbar">
        <Sheet open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
          <SheetTrigger asChild>
            <Button variant="ghost" size="icon" className="mobile-nav-trigger" aria-label="Open navigation">
              <Menu size={20} />
            </Button>
          </SheetTrigger>
          <SheetContent side="left" className="mobile-navigation-sheet">
            <SheetHeader className="sr-only">
              <SheetTitle>Navigation</SheetTitle>
              <SheetDescription>Primary routes and settings.</SheetDescription>
            </SheetHeader>
            <nav className="mobile-navigation-list" aria-label="Mobile navigation">
              {primaryPages.map(({ name, icon: Icon }) => (
                <button
                  type="button"
                  key={name}
                  className={page === name ? "active" : ""}
                  aria-current={page === name ? "page" : undefined}
                  onClick={() => navigate(name)}
                >
                  <Icon size={17} />
                  <span>{name}</span>
                </button>
              ))}
              <button
                type="button"
                className={settingsActive ? "active" : ""}
                aria-current={settingsActive ? "page" : undefined}
                onClick={() => navigate("Settings")}
              >
                <Settings2 size={17} />
                <span>Settings</span>
              </button>
            </nav>
          </SheetContent>
        </Sheet>
        <a
          href="#"
          className="brand"
          onClick={(event) => {
            event.preventDefault();
            navigate("Overview");
          }}
        >
          <span>Scout</span>
        </a>
        <span className="workspace-name">Personal network</span>
        <div className="topbar-actions">
          <Badge variant="outline">{status?.mode ?? "Self-hosted"}</Badge>
          <Button
            variant="ghost"
            size="icon"
            aria-label="Add agent manually"
            title="Add agent manually"
            aria-expanded={agentSetupOpen}
            onClick={() => setAgentSetupOpen(true)}
          >
            <Plus size={18} />
          </Button>
          <NotificationMenu active={page === "Notifications"} onViewAll={() => navigate("Notifications")} />
          <div className="avatar" title="Single-owner workspace">
            O
          </div>
        </div>
      </header>
      <div className="toolbar">
        <nav className="main-navigation" aria-label="Main navigation">
          {primaryPages.map(({ name, icon: Icon }) => (
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
          <button
            type="button"
            className={`settings-nav-item ${settingsActive ? "active" : ""}`}
            aria-current={settingsActive ? "page" : undefined}
            onClick={() => navigate("Settings")}
          >
            <Settings2 size={16} />
            <span>Settings</span>
          </button>
        </nav>
      </div>
      <main>
        {status?.recoveryMode && <RecoveryView onChanged={() => window.location.reload()} />}
        {agentSetupOpen && <AgentSetupView onClose={() => setAgentSetupOpen(false)} />}
        {selectedCandidate && page === "Network" ? (
          <CandidateView selected={selectedCandidate} onBack={() => navigate("Network")} onOpenAccess={openAccess} />
        ) : selected && (page === "Systems" || page === "Network") ? (
          <Suspense
            fallback={
              <div className="empty" role="status">
                Loading host detail…
              </div>
            }
          >
            <LazyDeviceView
              selected={selected}
              onBack={() => navigate(page)}
              onChanged={setSelected}
              onOpenIncident={openIncident}
            />
          </Suspense>
        ) : (
          <>
            {page !== "Overview" && page !== "Settings" && (
              <div className="page-heading">
                <div>
                  <h1>{detail.title}</h1>
                </div>
                {page === "Incidents" && (
                  <Button variant="outline" onClick={() => setManageRulesRequest((current) => current + 1)}>
                    <ShieldAlert data-icon="inline-start" />
                    Manage rules
                  </Button>
                )}
              </div>
            )}
            {page === "Overview" && (
              <OverviewView onNavigate={(next) => navigate(next)} onSelect={openDevice} onIncident={openIncident} />
            )}
            {page === "Systems" && <SystemsView query={query} onQuery={setQuery} onSelect={openDevice} />}
            {page === "Incidents" && (
              <IncidentsView
                focusIncidentId={incidentFocus}
                manageRulesRequest={manageRulesRequest}
                onFocusConsumed={() => setIncidentFocus("")}
              />
            )}
            {page === "Network" && <NetworkView onSelect={openDevice} onCandidateSelect={openCandidate} />}
            {page === "Notifications" && <NotificationsView />}
            {page === "Scopes" && <ScopesView />}
            {page === "Enrollment" && <EnrollmentView />}
            {page === "Access" && <AccessView candidate={selectedCandidate} />}
            {page === "Services" && <ServicesView onOpenIncident={openIncident} />}
            {page === "Hardware" && <HardwareView />}
            {page === "Storage" && <StorageView onOpenIncident={openIncident} />}
            {page === "Updates" && <UpdatesView />}
            {page === "Settings" && <SettingsView onNavigate={navigate} />}
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
