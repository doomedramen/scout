import { expect, test, type Page } from "@playwright/test";

const password = process.env.SCOUT_E2E_PASSWORD ?? "ScoutAa1";

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

function historyDevice() {
  const observedAt = new Date().toISOString();
  return {
    id: "history-ui-device",
    displayName: "History fixture",
    platform: "linux",
    architecture: "amd64",
    lifecycle: "enrolled",
    addresses: ["192.0.2.60"],
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
}

function historySeries() {
  const now = Date.now();
  const point = (minutesAgo: number, value: number | null, partial = false) => ({
    observedAt: new Date(now - minutesAgo * 60_000).toISOString(),
    value,
    availability: value === null ? "unavailable" : "current",
    count: value === null ? 0 : 1,
    coverage: partial ? 0.5 : value === null ? 0 : 1,
    partial,
  });
  return [
    {
      seriesId: "history-ui-cpu",
      entityId: "host",
      metric: "cpu.utilization",
      unit: "percent",
      resolutionSeconds: 300,
      points: [point(60, 12), point(30, null), point(0, 24, true)],
    },
  ];
}

test("owner can inspect effective history resolution and partial coverage", async ({ page }) => {
  const device = historyDevice();
  await page.route("**/api/v1/devices**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/metrics")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ series: historySeries() }),
      });
      return;
    }
    if (url.pathname.endsWith("/" + device.id)) {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(device) });
      return;
    }
    if (url.pathname.endsWith("/devices")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ items: [device], nextCursor: null }),
      });
      return;
    }
    await route.fallback();
  });

  await signIn(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Systems" }).click();
  await expect(page.getByText("History fixture", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "View History fixture" }).click();

  const chart = page.getByRole("heading", { name: "CPU usage" }).locator("..", { has: page.locator("h3") });
  await expect(page.getByText("5-minute resolution", { exact: true }).first()).toBeVisible();
  await expect(page.getByText(/Partial range/).first()).toBeVisible();
  await expect(page.getByText(/partial bucket/).first()).toBeVisible();
  await page.getByText("View tabular samples").first().click();
  await expect(page.getByRole("columnheader", { name: "Coverage" }).first()).toBeVisible();
  await expect(page.getByRole("cell", { name: "50%", exact: true }).first()).toBeVisible();
  await expect(chart).toBeVisible();
});

test("owner can preview and apply a retention reduction during recovery review", async ({ page }) => {
  const settings = {
    revision: 1,
    defaultsVersion: "002",
    notificationsPaused: true,
    retention: { rawDays: 30, fiveMinuteDays: 90, hourlyDays: 365 },
    diskBudgetBytes: 500 * 1024 * 1024 * 1024,
    updatedAt: new Date().toISOString(),
  };
  await page.route("**/api/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        mode: "development",
        database: "connected",
        enrollmentAvailable: false,
        recoveryMode: true,
      }),
    });
  });
  await page.route("**/api/v1/recovery/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        workspace: { recoveryMode: true, enrollmentPaused: true, updatesPaused: true },
        telemetry: {},
      }),
    });
  });
  await page.route("**/api/v1/monitoring/settings", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(settings) });
      return;
    }
    const updated = { ...settings, revision: 2, retention: { ...settings.retention, rawDays: 7 } };
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(updated) });
  });
  await page.route("**/api/v1/monitoring/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        evaluationLagSeconds: 0,
        rollupLagSeconds: 12,
        queues: { evaluation: 0, notifications: 2, rollup: 3 },
        counters: { dropped: 0, truncated: 0, backpressure: 0, deliveryFailures: 1, activeAdmissionFailures: 0 },
        storagePressure: { usedBytes: 1024, budgetBytes: settings.diskBudgetBytes, percent: 0, state: "normal" },
        lastSuccessfulJobs: { evaluation: null, rollup: new Date().toISOString(), retention: null },
      }),
    });
  });
  await page.route("**/api/v1/monitoring/retention-preview", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        previewId: "history-preview-id",
        expectedRevision: 1,
        retention: { rawDays: 7, fiveMinuteDays: 90, hourlyDays: 365 },
        affectedRanges: [
          {
            tier: "raw",
            before: new Date(Date.now() - 30 * 86400000).toISOString(),
            after: new Date(Date.now() - 7 * 86400000).toISOString(),
          },
        ],
        estimatedRows: 12,
        irreversible: true,
        expiresAt: new Date(Date.now() + 300000).toISOString(),
      }),
    });
  });

  await signIn(page);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Recovery mode" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "History and collection" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Keep the history you need" })).toBeVisible();

  await page.getByLabel("Raw data (days)").fill("7");
  await page.getByRole("button", { name: "Preview retention change" }).click();
  await expect(page.getByText("Preview ready", { exact: true })).toBeVisible();
  await expect(page.getByText(/Approximately 12 rows/)).toBeVisible();
  await page.getByRole("button", { name: "Apply retention reduction" }).click();
  await expect(page.getByText("Revision 2", { exact: true })).toBeVisible();
});
