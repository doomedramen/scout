import { defineConfig, devices } from "@playwright/test";

export const e2eDataDirectory = "/tmp/scout-playwright-data";
const e2ePort = process.env.PLAYWRIGHT_PORT ?? "18082";
const e2eBaseUrl = process.env.PLAYWRIGHT_BASE_URL ?? `http://127.0.0.1:${e2ePort}`;
const managesServer = !process.env.PLAYWRIGHT_BASE_URL && process.env.SCOUT_E2E_PACKAGED !== "1";
if (managesServer) process.env.SCOUT_E2E_WEB_SERVER = "1";

export default defineConfig({
  testDir: "tests/e2e",
  globalSetup: "./tests/e2e/global-setup.ts",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? "line" : "list",
  use: {
    baseURL: e2eBaseUrl,
    trace: "on-first-retry",
    ...devices["Desktop Chrome"],
  },
  webServer: process.env.PLAYWRIGHT_BASE_URL
    ? undefined
    : {
        command: "npm run e2e:server",
        url: `${e2eBaseUrl}/api/health/live`,
        reuseExistingServer: false,
        timeout: 120_000,
        env: {
          SCOUT_DATA_DIR: e2eDataDirectory,
          SCOUT_DISCOVERY_CIDR: "127.0.0.0/30",
          SCOUT_DISCOVERY_SOURCE_ADDRESS: "127.0.0.1",
          SCOUT_DISCOVERY_INTERFACE: "playwright-fixture",
          SCOUT_SSH_PORT: "18022",
          SCOUT_E2E_TARGET_ADDRESS: "127.0.0.1",
          SCOUT_E2E_TARGET_PORT: "18022",
          SCOUT_PUBLIC_URL: e2eBaseUrl,
          PORT: e2ePort,
        },
      },
});
