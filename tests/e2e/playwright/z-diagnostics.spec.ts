import { expect, test, type Page } from "@playwright/test";

const password = process.env.SCOUT_E2E_PASSWORD ?? "ScoutAa1";
const deviceID = "diagnostics-device";

function diagnosticDevice() {
  const observedAt = new Date().toISOString();
  return {
    id: deviceID,
    displayName: "Diagnostics fixture",
    platform: "linux",
    architecture: "amd64",
    lifecycle: "enrolled",
    addresses: ["192.0.2.42"],
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

function diagnosticSeries() {
  const now = Date.now();
  const point = (offsetMinutes: number, value: number | null, availability: string, min?: number, max?: number) => ({
    observedAt: new Date(now - offsetMinutes * 60_000).toISOString(),
    value,
    availability,
    ...(min === undefined ? {} : { min }),
    ...(max === undefined ? {} : { max }),
  });
  return [
    {
      entityId: "host",
      metric: "cpu.user_percent",
      unit: "percent",
      points: [point(5, 11, "current"), point(4, 13, "current"), point(3, 12, "current")],
    },
    {
      entityId: "host",
      metric: "load.1m",
      unit: "count",
      points: [point(5, 1.25, "current"), point(4, 1.5, "current")],
    },
    {
      seriesId: "disk-read-disk-a",
      entityId: "disk-a",
      metric: "disk.read_rate",
      unit: "bytes_per_second",
      points: [
        point(5, 4096, "current", 2048, 6144),
        point(4, null, "unavailable"),
        point(3, 8192, "current", 4096, 10240),
      ],
    },
    {
      seriesId: "disk-read-disk-b",
      entityId: "disk-b",
      metric: "disk.read_rate",
      unit: "bytes_per_second",
      points: [point(5, 16384, "current", 8192, 24576), point(3, 20480, "current", 12288, 28672)],
    },
  ];
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

test("owner can inspect per-entity diagnostic units and honest gaps", async ({ page }) => {
  const device = diagnosticDevice();
  const series = diagnosticSeries();

  await page.route("**/api/v1/devices**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/metrics")) {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ series }) });
      return;
    }
    if (url.pathname.endsWith("/" + deviceID)) {
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
  await expect(page.getByText("Diagnostics fixture", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "View Diagnostics fixture" }).click();

  await expect(page.getByRole("heading", { name: "Diagnostics fixture" })).toBeVisible();
  const board = page.getByRole("region", { name: "Inspect a signal" });
  await expect(board).toBeVisible();
  const metric = page.getByLabel("Diagnostic metric");
  const entity = page.getByLabel("Diagnostic entity");
  await metric.focus();
  await expect(metric).toBeFocused();
  await metric.selectOption("disk.read_rate");
  await entity.selectOption("disk-b");
  await expect(board.getByRole("heading", { name: "Disk · read rate" })).toBeVisible();
  await expect(board.getByText("bytes_per_second · 2 samples", { exact: true })).toBeVisible();
  await board.getByText("View tabular samples", { exact: true }).click();
  await expect(board.getByRole("cell", { name: "20 KiB/s", exact: true })).toBeVisible();
  await expect(board.getByText("Unavailable", { exact: true })).toHaveCount(0);

  await entity.selectOption("disk-a");
  await expect(board.getByText("Partial range · 1 unavailable sample", { exact: true })).toBeVisible();
  await expect(board.getByText("Gaps remain visible as unavailable; no value is interpolated.")).toBeVisible();
  await expect(board.getByText("Unavailable", { exact: true })).toBeVisible();
  await expect(board.getByText("2.0 KiB/s – 6.0 KiB/s", { exact: true })).toBeVisible();
  await expect(metric).toHaveAttribute("aria-label", "Diagnostic metric");
  await expect(entity).toHaveAttribute("aria-label", "Diagnostic entity");
});
