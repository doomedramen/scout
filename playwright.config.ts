import { defineConfig, devices } from "@playwright/test";

const externalURL = process.env.SCOUT_E2E_URL;
const baseURL = externalURL ?? "http://127.0.0.1:18082";
const browserChannel = process.env.SCOUT_E2E_BROWSER_CHANNEL ?? (process.env.CI ? undefined : "chrome");

export default defineConfig({
  testDir: "./tests/e2e/playwright",
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    ...devices["Desktop Chrome"],
    baseURL,
    channel: browserChannel,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  webServer: externalURL
    ? undefined
    : {
        command:
          "SCOUT_PRODUCTION=false SCOUT_AUTO_ENROLLMENT=false SCOUT_LISTEN=127.0.0.1:18082 SCOUT_SETUP_TOKEN=playwright-setup SCOUT_WEB_DIR=apps/web/dist go run ./apps/server",
        url: `${baseURL}/api/status`,
        reuseExistingServer: false,
        timeout: 120_000,
      },
});
