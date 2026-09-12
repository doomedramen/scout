import { expect, test, type Page, type Route } from "@playwright/test";

const password = process.env.SCOUT_E2E_PASSWORD ?? "ScoutAa1";
const deviceID = "advanced-monitoring-device";
const siteID = "advanced-monitoring-site";
const incidentID = "advanced-monitoring-incident";
const observedAt = new Date(Date.now() - 5_000).toISOString();

const device = {
  id: deviceID,
  displayName: "Journey host",
  siteId: siteID,
  platform: "linux",
  architecture: "amd64",
  hostname: "journey-host",
  addresses: ["192.0.2.70"],
  lifecycle: "enrolled",
  agentVersion: "0.1.0",
  lastHeartbeat: observedAt,
  availability: "online",
  metricFreshness: {
    "cpu.utilization": "current",
    "memory.used_percent": "current",
    "filesystem.used_percent": "current",
  },
  currentMetrics: {
    "cpu.utilization": { value: 21.5, unit: "percent", availability: "current", observedAt },
    "memory.used_percent": { value: 42.25, unit: "percent", availability: "current", observedAt },
    "filesystem.used_percent": { value: 58.75, unit: "percent", availability: "current", observedAt },
  },
  collectorStates: [],
  revision: 1,
};

const site = {
  id: siteID,
  name: "Advanced monitoring site",
  addressContext: "fixture",
  createdAt: observedAt,
};

const incident = {
  id: incidentID,
  lineageId: "advanced-monitoring-rule",
  entityId: "sshd.service",
  deviceId: deviceID,
  siteId: siteID,
  status: "active",
  severity: "critical",
  ruleSnapshot: {
    name: "SSH service failed",
    kind: "state",
    condition: {
      state: "failed",
      servicePattern: "sshd.service",
      triggerSeconds: 0,
      clearSeconds: 60,
    },
  },
  openedAt: observedAt,
  observedAt,
  evidenceState: "fresh",
  evidence: { rawState: "failed", source: "systemd", observedAt },
  notificationSuppression: { suppressed: false, reasons: [] },
  revision: 1,
};

const transitions = [
  {
    id: "advanced-monitoring-transition-triggered",
    incidentId: incidentID,
    sequence: 1,
    kind: "triggered",
    occurredAt: observedAt,
    effectiveRevision: 1,
  },
];

const service = {
  id: "sshd.service",
  provider: "systemd",
  deviceId: deviceID,
  kind: "service",
  name: "sshd.service",
  status: "failed",
  labels: { mustRun: "true" },
  observedAt,
  expiresAt: new Date(Date.now() + 60_000).toISOString(),
};

const descriptor = {
  id: "systemd",
  provider: "systemd",
  version: "1",
  requiredPermissions: ["read-only systemd D-Bus access"],
  configSchema: { expectedRunning: "Exact unit names or comma-separated patterns" },
  entityLimit: 1000,
  interval: 30,
  deadline: 10,
};

function point(minutesAgo: number, value: number | null, availability: string, partial = false) {
  return {
    observedAt: new Date(Date.now() - minutesAgo * 60_000).toISOString(),
    value,
    availability,
    count: value === null ? 0 : 1,
    coverage: partial ? 0.5 : value === null ? 0 : 1,
    partial,
  };
}

const series = [
  {
    entityId: "host",
    metric: "cpu.utilization",
    unit: "percent",
    points: [point(5, 18, "current"), point(3, 21, "current"), point(1, 22, "current")],
  },
  {
    entityId: "host",
    metric: "memory.used_percent",
    unit: "percent",
    points: [point(5, 40, "current"), point(3, 42, "current"), point(1, 43, "current")],
  },
  {
    entityId: "host",
    metric: "filesystem.used_percent",
    unit: "percent",
    points: [point(5, 55, "current"), point(3, 56, "current"), point(1, 58, "current")],
  },
  {
    seriesId: "advanced-disk-read-a",
    entityId: "disk-a",
    metric: "disk.read_rate",
    unit: "bytes_per_second",
    points: [point(5, 4096, "current"), point(4, null, "unavailable", true), point(3, 8192, "current")],
  },
  {
    seriesId: "advanced-disk-read-b",
    entityId: "disk-b",
    metric: "disk.read_rate",
    unit: "bytes_per_second",
    points: [point(5, 16384, "current"), point(3, 20480, "current")],
  },
];

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}

async function signIn(page: Page): Promise<void> {
  const setupResponse = await page.request.post("/api/v1/setup", {
    data: {
      setupToken: process.env.SCOUT_E2E_SETUP_TOKEN ?? "playwright-setup",
      password,
    },
  });
  expect([201, 409]).toContain(setupResponse.status());

  const signInResponse = await page.request.post("/api/v1/sessions", { data: { password } });
  expect(signInResponse.status()).toBe(200);
}

async function installFixtures(page: Page): Promise<{ getIncident: () => typeof incident }> {
  let acknowledged = false;
  let savedExpectedRunning = "";

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;

    if (path === "/api/v1/alert-rules") {
      await json(route, { items: [], nextCursor: null });
      return;
    }
    if (path === "/api/v1/sites") {
      await json(route, { items: [site], nextCursor: null });
      return;
    }
    if (path.endsWith(`/incidents/${incidentID}/acknowledgment`) && request.method() === "POST") {
      acknowledged = true;
      await json(route, {
        ...incident,
        acknowledgedAt: new Date().toISOString(),
        acknowledgedBy: "owner",
        revision: 2,
      });
      return;
    }
    if (path.endsWith(`/incidents/${incidentID}/transitions`)) {
      await json(route, {
        items: acknowledged
          ? [
              ...transitions,
              {
                id: "advanced-monitoring-transition-acknowledged",
                incidentId: incidentID,
                sequence: 2,
                kind: "acknowledged",
                occurredAt: new Date().toISOString(),
                actor: "owner",
                effectiveRevision: 2,
              },
            ]
          : transitions,
        nextCursor: null,
      });
      return;
    }
    if (path.endsWith(`/incidents/${incidentID}`)) {
      await json(
        route,
        acknowledged ? { ...incident, acknowledgedAt: observedAt, acknowledgedBy: "owner", revision: 2 } : incident,
      );
      return;
    }
    if (path === "/api/v1/incidents") {
      await json(route, {
        items: [acknowledged ? { ...incident, acknowledgedAt: observedAt, revision: 2 } : incident],
        nextCursor: null,
      });
      return;
    }
    if (path === "/api/v1/services") {
      await json(route, { items: [service], nextCursor: null });
      return;
    }
    if (path === "/api/v1/collectors") {
      await json(route, { items: [descriptor], nextCursor: null });
      return;
    }
    if (path.endsWith(`/devices/${deviceID}/collectors/systemd`)) {
      if (request.method() === "PATCH") {
        const body = request.postDataJSON() as { config?: { expectedRunning?: string } };
        savedExpectedRunning = body.config?.expectedRunning ?? savedExpectedRunning;
      }
      await json(route, {
        deviceId: deviceID,
        collectorId: "systemd",
        provider: "systemd",
        enabled: true,
        config: { expectedRunning: savedExpectedRunning },
        revision: savedExpectedRunning ? 2 : 1,
        health: "healthy",
      });
      return;
    }
    if (path.endsWith(`/devices/${deviceID}/collectors`)) {
      await json(route, {
        items: [
          {
            deviceId: deviceID,
            collectorId: "systemd",
            provider: "systemd",
            enabled: true,
            config: { expectedRunning: savedExpectedRunning },
            revision: savedExpectedRunning ? 2 : 1,
            health: "healthy",
          },
        ],
        nextCursor: null,
      });
      return;
    }
    if (path.endsWith(`/devices/${deviceID}/metrics`)) {
      await json(route, {
        from: new Date(Date.now() - 3_600_000).toISOString(),
        to: new Date().toISOString(),
        series,
      });
      return;
    }
    if (path.endsWith(`/devices/${deviceID}`)) {
      await json(route, device);
      return;
    }
    if (path === "/api/v1/devices") {
      await json(route, { items: [device], nextCursor: null });
      return;
    }
    await route.fallback();
  });

  return { getIncident: () => (acknowledged ? { ...incident, acknowledgedAt: observedAt, revision: 2 } : incident) };
}

async function expectNoHorizontalOverflow(page: Page): Promise<void> {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
}

test("owner can keyboard-inspect incidents, services, and history at mobile and desktop widths", async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 800 });
  const fixtureState = await installFixtures(page);
  await signIn(page);
  await page.goto("/");

  await page.getByRole("button", { name: "Open navigation" }).click();
  const incidentsNav = page.getByRole("button", { name: "Incidents", exact: true });
  await incidentsNav.focus();
  await expect(incidentsNav).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "Incidents and alert rules" })).toBeVisible();

  const incidentRow = page.getByRole("button", { name: /SSH service failed/ }).first();
  await incidentRow.focus();
  await expect(incidentRow).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "SSH service failed" })).toBeVisible();
  await expect(page.getByText("Evidence fresh", { exact: true })).toBeVisible();
  await expect(page.getByText("Raw state", { exact: true })).toBeVisible();

  const acknowledge = page.getByRole("button", { name: "Acknowledge incident" });
  await acknowledge.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("status")).toContainText("Incident acknowledged.");
  await expect(page.getByRole("button", { name: "Acknowledged", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Incident history" })).toBeVisible();
  await expectNoHorizontalOverflow(page);

  await page.getByRole("button", { name: "Open navigation" }).click();
  const servicesNav = page.getByRole("button", { name: "Settings", exact: true });
  await servicesNav.focus();
  await page.keyboard.press("Enter");
  const serviceDestination = page.getByRole("button", { name: /^Services Collectors/ });
  await serviceDestination.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("h1")).toHaveText("Services");
  await expect(page.getByText("sshd.service", { exact: true })).toBeVisible();
  await expect(page.getByText("Failed", { exact: true })).toBeVisible();
  await expect(page.getByText("Fresh inventory", { exact: true })).toBeVisible();

  const openServiceIncident = page.getByRole("button", { name: "Open incident for sshd.service" });
  await openServiceIncident.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "SSH service failed" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Incident history" })).toBeVisible();

  await page.getByRole("button", { name: "Open navigation" }).click();
  const systemsNav = page.getByRole("button", { name: "Systems", exact: true });
  await systemsNav.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByText("Journey host", { exact: true })).toBeVisible();
  const viewDevice = page.getByRole("button", { name: "View Journey host" });
  await viewDevice.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "Journey host" })).toBeVisible();

  const diagnosticBoard = page.getByRole("region", { name: "Inspect a signal" });
  const metric = page.getByLabel("Diagnostic metric");
  const entity = page.getByLabel("Diagnostic entity");
  await metric.focus();
  await expect(metric).toBeFocused();
  await metric.selectOption("disk.read_rate");
  await entity.selectOption("disk-a");
  await expect(diagnosticBoard.getByRole("heading", { name: /Disk · read rate/ })).toBeVisible();
  await expect(diagnosticBoard.getByText("Partial range · 1 unavailable sample", { exact: true })).toBeVisible();

  const tableSummary = diagnosticBoard.getByText("View tabular samples", { exact: true });
  await tableSummary.focus();
  await page.keyboard.press("Enter");
  await expect(diagnosticBoard.getByRole("columnheader", { name: "Observed" })).toBeVisible();
  await expect(diagnosticBoard.getByRole("cell", { name: "Unavailable", exact: true })).toBeVisible();
  await expectNoHorizontalOverflow(page);

  await page.setViewportSize({ width: 1440, height: 900 });
  await expectNoHorizontalOverflow(page);
  expect(fixtureState.getIncident().revision).toBe(2);
});
