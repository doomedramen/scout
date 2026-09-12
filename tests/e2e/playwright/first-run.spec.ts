import { expect, test } from "@playwright/test";

test("development owner can complete the first-run access workflow", async ({ page }) => {
  const setupToken = process.env.SCOUT_E2E_SETUP_TOKEN ?? "playwright-setup";
  const password = "ScoutAa1";
  const siteName = "playwright-lab";

  await page.goto("/");
  await page.getByLabel("One-time setup token").fill(setupToken);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Create owner" }).click();

  const signInHeading = page.getByRole("heading", { name: "Sign in to Scout" });
  const setupError = page.getByRole("alert");
  await expect(signInHeading.or(setupError)).toBeVisible();
  if (await setupError.isVisible()) {
    await expect(setupError).toContainText("Owner setup could not be completed");
    await page.getByRole("button", { name: "Owner already exists? Sign in" }).click();
  }
  await expect(signInHeading).toBeVisible();
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("button", { name: "Systems" })).toBeVisible();

  await page.getByRole("button", { name: "Access" }).click();
  await expect(page.getByText("Development bypass", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Scopes" }).click();
  await expect(page.getByRole("heading", { name: "Sites and scopes" })).toBeVisible();
  await page.getByLabel("Site name").fill(siteName);
  await page.getByRole("button", { name: "Add site" }).click();

  await page.getByRole("combobox").selectOption({ label: siteName });
  await page.getByLabel("Ranges CIDRs or literal addresses").fill("192.0.2.0/24");
  await page.getByRole("button", { name: "Save disabled scope" }).click();
  await expect(
    page.getByText("Scope saved disabled. Enable it only after reviewing access and exclusions."),
  ).toBeVisible();
  const createdScope = page.locator(".scope-row").filter({ hasText: "192.0.2.0/24" }).last();
  await createdScope.getByRole("button", { name: "Enable scope" }).click();
  await expect(createdScope.getByRole("button", { name: "Pause scope" })).toBeVisible();

  await page.getByRole("button", { name: "Systems" }).click();
  await page.getByRole("button", { name: "Agent setup" }).first().click();
  await expect(page.getByRole("heading", { name: "Install the first Linux agent" })).toBeVisible();
  await page.getByRole("button", { name: "Create one-time invitation" }).click();
  await expect(page.getByText("Invitation created. It is single-use and expires in five minutes.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Native Linux (recommended)" })).toBeVisible();
  await expect(page.getByText(/api\/v1\/bootstrap\/agent\/install\.sh/)).toBeVisible();
  const nativeRecipe = page.locator(".agent-recipe").first();
  await expect(nativeRecipe).toContainText("SCOUT_OTI=");
  await expect(nativeRecipe).toContainText("bash -c");
  await expect(nativeRecipe).not.toContainText("sudo /");

  for (let restart = 0; restart < 3; restart += 1) {
    await page.getByRole("button", { name: "Close agent setup" }).click();
    await page.reload();
    await expect(page.getByRole("button", { name: "Systems" })).toBeVisible();
    await page.getByRole("button", { name: "Agent setup" }).first().click();
    await expect(page.getByRole("heading", { name: "Install the first Linux agent" })).toBeVisible();
    const pendingDevice = page.getByLabel("Existing pending device");
    await expect(pendingDevice).toBeVisible();
    await expect(pendingDevice).not.toHaveValue("");
    await page.getByRole("button", { name: "Create invitation for existing device" }).click();
    await expect(page.getByText("Invitation created. It is single-use and expires in five minutes.")).toBeVisible();
  }

  await page.getByRole("button", { name: "Close agent setup" }).click();
  await page.reload();
  await expect(page.getByLabel("System totals")).toContainText("1 needs access");

  await page.getByRole("button", { name: "Incidents", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Incidents and alert rules" })).toBeVisible();
  await expect(page.getByText("No incidents match these filters.")).toBeVisible();
  await page.getByRole("tab", { name: /Rules/ }).click();
  await expect(page.getByRole("heading", { name: "Rule editor" })).toBeVisible();
  await expect(page.getByText("Automatic defaults", { exact: true })).toBeVisible();
  await page.getByLabel("Rule name").fill("Playwright CPU baseline");
  await page.getByRole("button", { name: "Create rule" }).click();
  await expect(page.getByText("Alert rule created.")).toBeVisible();
  await expect(page.getByText("Playwright CPU baseline", { exact: true })).toBeVisible();
});
