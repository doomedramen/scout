import { defineConfig, devices } from "@playwright/test";

const externalURL = process.env.SCOUT_E2E_URL;
const baseURL = externalURL ?? "http://127.0.0.1:18081";
const browserChannel = process.env.SCOUT_E2E_BROWSER_CHANNEL ?? (process.env.CI ? undefined : "chrome");

export default defineConfig({
  testDir: "./tests/e2e/playwright",
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
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
    : [
        {
          command:
            "SCOUT_PRODUCTION=false SCOUT_AUTO_ENROLLMENT=false SCOUT_LISTEN=127.0.0.1:18082 SCOUT_SETUP_TOKEN=playwright-setup ./apps/server/dist/scout-server",
          url: "http://127.0.0.1:18082/api/status",
          reuseExistingServer: false,
          timeout: 120_000,
        },
        {
          // Exercise recovery when a deployment's explicit origin is stale but its listener is correct.
          command:
            "SCOUT_API_ORIGIN=http://127.0.0.1:18083 SCOUT_LISTEN=127.0.0.1:18082 HOSTNAME=127.0.0.1 PORT=18081 npm run start --workspace=@scout/web",
          url: `${baseURL}/api/status`,
          reuseExistingServer: false,
          timeout: 120_000,
        },
      ],
});
