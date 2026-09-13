"use client";

import { lazy, Suspense, useEffect, useRef, useState } from "react";
import dynamic from "next/dynamic";
import { useRouter } from "next/navigation";
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

export type Route = string[];

type AppProps = {
  route: Route;
  initialAuth: "signedOut" | "signedIn";
  initialStatus: Status | null;
  initialStatusError: boolean;
};

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

function pageFromRoute(route: Route): Page {
  return pageFromSlug.get(route[0] ?? "") ?? "Overview";
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

const viewLoading = () => (
  <div className="empty" role="status">
    Loading view…
  </div>
);
const AccessView = dynamic(() => import("@/views/access").then(({ AccessView }) => AccessView), {
  loading: viewLoading,
});
const AgentSetupView = dynamic(() => import("@/views/agent-setup").then(({ AgentSetupView }) => AgentSetupView), {
  loading: viewLoading,
});
const CandidateView = dynamic(() => import("@/views/candidate").then(({ CandidateView }) => CandidateView), {
  loading: viewLoading,
});
const EnrollmentView = dynamic(() => import("@/views/enrollment").then(({ EnrollmentView }) => EnrollmentView), {
  loading: viewLoading,
});
const IncidentsView = dynamic(() => import("@/views/incidents").then(({ IncidentsView }) => IncidentsView), {
  loading: viewLoading,
});
const HardwareView = dynamic(() => import("@/views/hardware").then(({ HardwareView }) => HardwareView), {
  loading: viewLoading,
});
const NetworkView = dynamic(() => import("@/views/network").then(({ NetworkView }) => NetworkView), {
  loading: viewLoading,
});
const NotificationsView = dynamic(
  () => import("@/views/notifications").then(({ NotificationsView }) => NotificationsView),
  {
    loading: viewLoading,
  },
);
const OverviewView = dynamic(() => import("@/views/overview").then(({ OverviewView }) => OverviewView), {
  loading: viewLoading,
});
const RecoveryView = dynamic(() => import("@/views/recovery").then(({ RecoveryView }) => RecoveryView), {
  loading: viewLoading,
});
const ScopesView = dynamic(() => import("@/views/scopes").then(({ ScopesView }) => ScopesView), {
  loading: viewLoading,
});
const ServicesView = dynamic(() => import("@/views/services").then(({ ServicesView }) => ServicesView), {
  loading: viewLoading,
});
const SettingsView = dynamic(() => import("@/views/settings").then(({ SettingsView }) => SettingsView), {
  loading: viewLoading,
});
const StorageView = dynamic(() => import("@/views/storage").then(({ StorageView }) => StorageView), {
  loading: viewLoading,
});
const SetupView = dynamic(() => import("@/views/setup").then(({ SetupView }) => SetupView), { loading: viewLoading });
const SystemsView = dynamic(() => import("@/views/systems").then(({ SystemsView }) => SystemsView), {
  loading: viewLoading,
});
const UpdatesView = dynamic(() => import("@/views/updates").then(({ UpdatesView }) => UpdatesView), {
  loading: viewLoading,
});

export default function App({ route, initialAuth, initialStatus, initialStatusError }: AppProps) {
  const router = useRouter();
  const routeKey = route.join("/");
  const previousRouteKey = useRef(routeKey);
  const [auth, setAuth] = useState<"signedOut" | "signedIn">(initialAuth);
  const [page, setPage] = useState<Page>(() => pageFromRoute(route));
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Device | null>(null);
  const [selectedCandidate, setSelectedCandidate] = useState<Candidate | null>(null);
  const [incidentFocus, setIncidentFocus] = useState("");
  const [agentSetupOpen, setAgentSetupOpen] = useState(false);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [manageRulesRequest, setManageRulesRequest] = useState(0);
  const [status, setStatus] = useState<Status | null>(initialStatus);
  const [statusError, setStatusError] = useState(initialStatusError);

  useEffect(() => {
    let cancelled = false;
    api
      .status()
      .then((value) => {
        if (!cancelled) {
          setStatus(value);
          setStatusError(false);
        }
      })
      .catch(() => {
        if (!cancelled) setStatusError(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (previousRouteKey.current === routeKey) return;
    previousRouteKey.current = routeKey;
    setPage(pageFromRoute(route));
    const detailID = route[2] ? decodeURIComponent(route[2]) : "";
    if (route[1] !== "device" || selected?.id !== detailID) setSelected(null);
    if (route[1] !== "candidate" || selectedCandidate?.id !== detailID) setSelectedCandidate(null);
    setIncidentFocus(route[0] === "incidents" ? (route[1] ?? "") : "");
    setMobileNavOpen(false);
  }, [route, routeKey, selected?.id, selectedCandidate?.id]);

  useEffect(() => {
    if (auth !== "signedIn") return;
    const parts = route;
    if (parts[1] === "candidate" && parts[2] && !selectedCandidate) {
      let cancelled = false;
      api
        .candidate(decodeURIComponent(parts[2]))
        .then((detail) => {
          if (!cancelled) setSelectedCandidate(detail.candidate);
        })
        .catch(() => {
          if (!cancelled) router.replace("/network");
        });
      return () => {
        cancelled = true;
      };
    }
    if (parts[1] !== "device" || !parts[2] || selected) return;
    let cancelled = false;
    const deviceID = decodeURIComponent(parts[2]);
    if (deviceID.startsWith("candidate:")) {
      api
        .candidate(deviceID.slice("candidate:".length))
        .then(({ candidate }) => {
          if (cancelled) return;
          setSelected({
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
          });
        })
        .catch(() => {
          if (!cancelled) router.replace(`/${parts[0] || "overview"}`);
        });
      return () => {
        cancelled = true;
      };
    }
    api
      .device(deviceID)
      .then((device) => {
        if (!cancelled) setSelected(device);
      })
      .catch(() => {
        if (!cancelled) router.replace(`/${parts[0] || "overview"}`);
      });
    return () => {
      cancelled = true;
    };
  }, [auth, page, route, router, selected, selectedCandidate]);

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
    router.push(`/${pageSlugs[next]}${suffix}`);
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
    router.push(`/network/candidate/${encodeURIComponent(candidate.id)}`);
  }

  function openAccess(candidate: Candidate) {
    setPage("Access");
    setSelected(null);
    setSelectedCandidate(candidate);
    setQuery("");
    setMobileNavOpen(false);
    router.push(`/access/candidate/${encodeURIComponent(candidate.id)}`);
  }

  function openDevice(device: Device) {
    const targetPage = page === "Overview" ? "Systems" : page;
    setPage(targetPage);
    setSelected(device);
    router.push(`/${pageSlugs[targetPage]}/device/${encodeURIComponent(device.id)}`);
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
        {status?.recoveryMode && <RecoveryView onChanged={() => router.refresh()} />}
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
