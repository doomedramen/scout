import { expect, test, type Page } from "@playwright/test";

const password = process.env.SCOUT_E2E_PASSWORD ?? "ScoutAa1";
const deviceID = "hardware-device";
const sensorID = "sensor-host-thermal-zone-0-temp";
const observedAt = new Date().toISOString();

const device = {
  id: deviceID,
  displayName: "Hardware fixture",
  platform: "linux",
  architecture: "amd64",
  hostname: "hardware-fixture",
  addresses: ["192.0.2.50"],
  lifecycle: "enrolled",
  availability: "online",
  metricFreshness: { "sensor.temperature": "current", "gpu.temperature": "current" },
  currentMetrics: {
    [`${sensorID}:sensor.temperature`]: {
      value: 48.5,
      unit: "celsius",
      availability: "current",
      observedAt,
    },
    "gpu-fixture:gpu.utilization": { value: 37, unit: "percent", availability: "current", observedAt },
    "gpu-fixture:gpu.memory.used": { value: 2147483648, unit: "bytes", availability: "current", observedAt },
    "gpu-fixture:gpu.temperature": { value: 55, unit: "celsius", availability: "current", observedAt },
    "gpu-fixture:gpu.power": { value: null, unit: "watts", availability: "unsupported", observedAt },
  },
  collectorStates: [],
  revision: 1,
};

const sensor = {
  id: sensorID,
  provider: "sensors",
  deviceId: deviceID,
  kind: "sensor",
  name: "Core temperature",
  status: "online",
  labels: {
    sensorType: "temperature",
    identityStable: "true",
    identitySource: "hwmon-path",
    "capability.temperature": "current",
    "capability.fault": "unavailable",
  },
  observedAt,
  expiresAt: new Date(Date.now() + 60_000).toISOString(),
};

const gpu = {
  id: "gpu-fixture",
  provider: "gpu",
  deviceId: deviceID,
  kind: "gpu",
  name: "Fixture GPU",
  status: "online",
  labels: {
    vendor: "NVIDIA",
    driver: "fixture-driver",
    identityStable: "true",
    identitySource: "uuid",
    powerScope: "gpu-board",
    "capability.utilization": "current",
    "capability.memory.used": "current",
    "capability.memory.capacity": "unavailable",
    "capability.temperature": "current",
    "capability.power": "unsupported",
  },
  observedAt,
  expiresAt: new Date(Date.now() + 60_000).toISOString(),
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

test("owner can inspect hardware fields and save exact sensor exclusions", async ({ page }) => {
  let savedExclusions = "";
  let patchBody: Record<string, unknown> | undefined;

  await page.route("**/api/v1/services**", async (route) => {
    const provider = new URL(route.request().url()).searchParams.get("provider");
    const items = provider === "sensors" ? [sensor] : provider === "gpu" ? [gpu] : [];
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items, nextCursor: null }),
    });
  });

  await page.route("**/api/v1/devices**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith(`/devices/${deviceID}/metrics`)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          from: new Date(Date.now() - 3_600_000).toISOString(),
          to: new Date().toISOString(),
          series: [
            {
              entityId: sensorID,
              metric: "sensor.temperature",
              unit: "celsius",
              points: [point(5, 46, "current"), point(4, null, "unavailable", true), point(3, 48.5, "current")],
            },
          ],
        }),
      });
      return;
    }
    if (url.pathname.endsWith(`/devices/${deviceID}/collectors/sensors`)) {
      patchBody = route.request().postDataJSON() as Record<string, unknown>;
      savedExclusions = String((patchBody.config as Record<string, unknown>)?.excludedIds ?? "");
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          deviceId: deviceID,
          collectorId: "sensors",
          provider: "sensors",
          enabled: true,
          config: { excludedIds: savedExclusions },
          revision: 2,
          health: "healthy",
        }),
      });
      return;
    }
    if (url.pathname.endsWith(`/devices/${deviceID}/collectors`)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          items: [
            {
              deviceId: deviceID,
              collectorId: "sensors",
              provider: "sensors",
              enabled: true,
              config: { excludedIds: savedExclusions },
              revision: savedExclusions ? 2 : 1,
              health: "healthy",
            },
          ],
          nextCursor: null,
        }),
      });
      return;
    }
    if (url.pathname.endsWith(`/devices/${deviceID}`)) {
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
  await page.getByRole("button", { name: "Hardware", exact: true }).click();

  await expect(page.getByRole("heading", { name: "Hardware telemetry" })).toBeVisible();
  await expect(page.locator(".hardware-card-heading p").first()).toHaveText("Hardware fixture");
  await expect(page.getByRole("heading", { name: "Core temperature" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Fixture GPU" })).toBeVisible();
  await expect(page.locator(".hardware-signal-tile strong", { hasText: "48.5 °C" })).toHaveCount(1);
  await expect(
    page.locator(".gpu-signal-grid .hardware-signal-tile strong", { hasText: "Unavailable" }).first(),
  ).toBeVisible();
  await expect(page.getByText("Gaps are left visible instead of being interpolated.", { exact: true })).toBeVisible();

  await page.getByLabel("Core temperature").check();
  await page.getByRole("button", { name: "Save exclusions" }).click();
  await expect(page.locator("p.form-success")).toContainText("Sensor exclusions saved");
  await expect(page.getByText("Excluded", { exact: true })).toBeVisible();
  expect(patchBody).toMatchObject({ provider: "sensors", enabled: true });
  expect(savedExclusions).toBe(sensorID);
});
