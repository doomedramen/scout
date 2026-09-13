import { defineConfig, devices } from "@playwright/test";

export const e2eDataDirectory = "/tmp/scout-playwright-data";

export default defineConfig({
  testDir: "tests/e2e",
  globalSetup: "./tests/e2e/global-setup.ts",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? "line" : "list",
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? "http://127.0.0.1:18082",
    trace: "on-first-retry",
    ...devices["Desktop Chrome"],
  },
  webServer: process.env.PLAYWRIGHT_BASE_URL
    ? undefined
    : {
        command: "npm start",
        url: "http://127.0.0.1:18082/api/health/live",
        reuseExistingServer: false,
        timeout: 120_000,
        env: {
          SCOUT_DATA_DIR: e2eDataDirectory,
          SCOUT_DISABLE_DISCOVERY: "true",
          SCOUT_PUBLIC_URL: "http://127.0.0.1:18082",
          PORT: "18082",
        },
      },
});
