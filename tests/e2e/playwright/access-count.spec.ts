import { expect, test, type Page, type Route } from "@playwright/test";

const password = process.env.SCOUT_E2E_PASSWORD ?? "ScoutAa1";

async function signIn(page: Page): Promise<void> {
  const setup = await page.request.post("/api/v1/setup", {
    data: {
      setupToken: process.env.SCOUT_E2E_SETUP_TOKEN ?? "playwright-setup",
      password,
    },
  });
  expect([201, 409]).toContain(setup.status());

  const signInResponse = await page.request.post("/api/v1/sessions", { data: { password } });
  expect(signInResponse.status()).toBe(200);
}

function candidate(address: string, state: string) {
  return {
    id: `candidate-${address.replaceAll(".", "-")}`,
    siteId: "site-fixture",
    scopeId: "scope-fixture",
    address,
    displayName: address,
    state,
    coverageState: state === "stale" ? "stale" : "current",
    entryPointCount: 1,
    entryPointIds: ["ssh-default"],
    preferredAccessMethod: "ssh",
    excluded: false,
  };
}

test("overview counts only current actionable access candidates", async ({ page }) => {
  await page.route("**/api/v1/candidates**", async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [candidate("192.0.2.10", "stale"), candidate("192.0.2.11", "needs_credentials")],
        nextCursor: null,
      }),
    });
  });

  await signIn(page);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Prerequisites", exact: true }).locator("strong")).toHaveText("1");
  await expect(page.getByText("Systems needing access")).toBeVisible();
  await expect(page.getByText("192.0.2.11", { exact: true })).toBeVisible();
  await expect(page.getByText("192.0.2.10", { exact: true })).toHaveCount(0);
});
