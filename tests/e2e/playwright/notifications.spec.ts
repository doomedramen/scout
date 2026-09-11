import { createServer, type Server } from "node:http";
import { expect, test, type Page } from "@playwright/test";

const password = process.env.SCOUT_E2E_PASSWORD ?? "ScoutAa1";

type Receiver = {
  url: string;
  requests: Array<{ path: string; authorization: string }>;
  server: Server;
};

let activeReceiver: Receiver | null = null;

test.afterEach(() => {
  activeReceiver?.server.close();
  activeReceiver = null;
});

async function startReceiver(): Promise<Receiver> {
  const requests: Receiver["requests"] = [];
  const server = createServer((request, response) => {
    request.resume();
    requests.push({ path: request.url ?? "", authorization: request.headers.authorization ?? "" });
    response.writeHead(200, { "content-type": "application/json" });
    response.end(JSON.stringify({ id: "playwright-accepted" }));
  });
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve());
  });
  const address = server.address();
  if (!address || typeof address === "string") {
    server.close();
    throw new Error("Could not determine the disposable receiver port");
  }
  return { url: `http://127.0.0.1:${address.port}`, requests, server };
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

  await page.goto("/");
  await expect(page.getByRole("button", { name: "Notifications" })).toBeVisible();
}

function localDateTime(offsetMinutes: number): string {
  const value = new Date(Date.now() + offsetMinutes * 60_000);
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())}T${pad(value.getHours())}:${pad(value.getMinutes())}`;
}

test("owner can configure ntfy delivery and timezone-aware quiet windows", async ({ page }) => {
  const receiver = await startReceiver();
  activeReceiver = receiver;

  const destinationName = `Playwright receiver ${Date.now()}`;
  const topic = `playwright_topic_${Date.now()}`;
  await signIn(page);
  await page.getByRole("button", { name: "Notifications" }).click();
  await expect(page.getByRole("heading", { name: "Alert delivery" })).toBeVisible();

  await page.getByLabel("Destination name").fill(destinationName);
  await page.getByLabel("ntfy server URL").fill(receiver.url);
  await page.getByLabel("Topic").fill(topic);
  await page.getByLabel("Access token").fill("playwright-secret");
  await page.getByLabel("Allow private HTTP for a local ntfy server").check();
  await page.getByRole("button", { name: "Save destination" }).click();
  await expect(page.getByRole("status")).toContainText("ntfy destination saved");
  await expect(page.getByText(destinationName, { exact: true })).toBeVisible();
  await expect(page.locator("body")).not.toContainText(topic);
  await expect(page.locator("body")).not.toContainText("playwright-secret");

  await page.getByRole("button", { name: "Send test" }).click();
  await expect(page.getByRole("status")).toContainText("accepted the test notification");
  await expect.poll(() => receiver.requests.length).toBe(1);
  expect(receiver.requests[0]).toMatchObject({ path: `/${topic}`, authorization: "Bearer playwright-secret" });

  const destinationTestID = await page.locator('[data-testid^="destination-"]').first().getAttribute("data-testid");
  expect(destinationTestID).toBeTruthy();
  const destinationID = destinationTestID?.slice("destination-".length);
  await page.getByRole("button", { name: "Pause", exact: true }).first().click();
  await expect(page.getByText("Paused", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Send test" })).toHaveCount(0);

  const disabledTestStatus = await page.evaluate(async (id) => {
    const csrfCookie = document.cookie.split("; ").find((value) => value.startsWith("scout_csrf="));
    const csrfToken = csrfCookie ? decodeURIComponent(csrfCookie.slice("scout_csrf=".length)) : "";
    const response = await fetch(`/api/v1/notification-destinations/${encodeURIComponent(id ?? "")}/test`, {
      method: "POST",
      credentials: "same-origin",
      headers: { "content-type": "application/json", "x-csrf-token": csrfToken },
      body: JSON.stringify({ expectedRevision: 1 }),
    });
    return response.status;
  }, destinationID);
  expect(disabledTestStatus).toBe(409);
  expect(receiver.requests).toHaveLength(1);
  await page.getByRole("button", { name: "Enable", exact: true }).first().click();
  await expect(page.getByText("Enabled", { exact: true })).toBeVisible();

  await page.getByLabel("Window name").fill("Nightly quiet hours");
  await page.getByLabel("Timezone").fill("Europe/London");
  await page.getByLabel("Start local time").fill("22:00");
  await page.getByLabel("End local time").fill("06:00");
  await page.getByRole("button", { name: "Save quiet window" }).click();
  await expect(page.getByText("Nightly quiet hours", { exact: true })).toBeVisible();
  await expect(page.getByText(/overnight/)).toBeVisible();

  await page.getByLabel("Window name").fill("Overlapping maintenance");
  await page.getByLabel("Start local time").fill("23:00");
  await page.getByLabel("End local time").fill("02:00");
  await page.getByRole("button", { name: "Save quiet window" }).click();
  await expect(page.getByText("Overlapping maintenance", { exact: true })).toBeVisible();
  await expect(page.getByText("Quiet windows", { exact: true })).toBeVisible();

  await page.getByLabel("Window name").fill("DST release window");
  await page.getByLabel("Timezone").fill("America/New_York");
  await page.getByLabel("Start local time").fill("01:30");
  await page.getByLabel("End local time").fill("03:30");
  await page.getByRole("button", { name: "Save quiet window" }).click();
  await expect(page.getByText("DST release window", { exact: true })).toBeVisible();
  await expect(page.getByText(/America\/New_York/)).toBeVisible();

  await page.getByLabel("Schedule type").selectOption("oneTime");
  await page.getByLabel("Window name").fill("One-time resolved-maintenance check");
  await page.getByLabel("Starts at").fill(localDateTime(30));
  await page.getByLabel("Ends at").fill(localDateTime(90));
  await page.getByRole("button", { name: "Save quiet window" }).click();
  await expect(page.getByText("One-time resolved-maintenance check", { exact: true })).toBeVisible();

  await page.getByRole("region", { name: "Delivery status" }).getByRole("combobox").selectOption("suppressed");
  await expect(page.getByText("No delivery records match this filter.", { exact: true })).toBeVisible();
});
