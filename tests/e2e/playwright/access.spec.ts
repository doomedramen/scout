import { expect, test, type Page } from "@playwright/test";

const password = process.env.SCOUT_E2E_PASSWORD ?? "ScoutAa1";

async function signIn(page: Page): Promise<void> {
  const setup = await page.request.post("/api/v1/setup", {
    data: { setupToken: process.env.SCOUT_E2E_SETUP_TOKEN ?? "playwright-setup", password },
  });
  expect([201, 409]).toContain(setup.status());
  const response = await page.request.post("/api/v1/sessions", { data: { password } });
  expect(response.status()).toBe(200);
}

test("access errors stay safe and the credential action is keyboard reachable", async ({ page }) => {
  await signIn(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: /^Access Credentials/ }).click();
  await expect(page.getByRole("heading", { name: "Resolve prerequisites" })).toBeVisible();

  const secret = "never-rendered-fixture-secret";
  await page.getByLabel("Credential type").fill("");
  await page.getByLabel("Secret").fill(secret);
  await page.getByLabel("Exact targets").fill("192.0.2.10:22");
  const submit = page.getByRole("button", { name: "Store encrypted credential" });
  await submit.focus();
  await expect(submit).toBeFocused();
  await page.keyboard.press("Enter");

  const alert = page.locator(".form-error");
  await expect(alert).toContainText("Request failed validation");
  await expect(alert).not.toContainText(secret);
  await expect(page.locator("body")).not.toContainText(secret);
});

test("SSH credentials ask for a username and password by default", async ({ page }) => {
  await signIn(page);
  await page.goto("/access");
  await expect(page.getByRole("heading", { name: "Resolve prerequisites" })).toBeVisible();
  await expect(page.getByLabel("Credential type")).toHaveValue("ssh");
  await expect(page.getByLabel("SSH authentication")).toHaveValue("password");
  await expect(page.getByLabel("SSH username")).toBeVisible();
  await expect(page.getByLabel("SSH password")).toBeVisible();
  await expect(page.getByLabel("Secret")).toHaveCount(0);
});

test("filled SSH password credentials can be stored", async ({ page }) => {
  await signIn(page);
  await page.goto("/access");

  await page.getByLabel("SSH username").fill("root");
  await page.getByLabel("SSH password").fill("playwright-password-fixture");
  await page.getByLabel("Exact targets").fill("192.0.2.10:22");

  const submit = page.getByRole("button", { name: "Store encrypted credential" });
  await expect(submit).toBeEnabled();
  await submit.click();
  await expect(page.getByRole("status")).toContainText("Credential stored");
  await expect(page.locator("body")).not.toContainText("playwright-password-fixture");
});
