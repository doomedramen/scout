import fs from "node:fs";

import { expect, test } from "@playwright/test";

import { e2eDataDirectory } from "../../playwright.config";

test("owner setup lands on Systems and future visits use sign-in", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("Protect your Scout workspace", { exact: true })).toBeVisible();

  await page
    .getByLabel("One-time setup token")
    .fill(fs.readFileSync(`${e2eDataDirectory}/setup-token`, "utf8"));
  await page.getByLabel("Username").fill("owner");
  await page.getByLabel("Password").fill("ValidPass1");
  await page.getByRole("button", { name: "Create owner account" }).click();

  await expect(page).toHaveURL(/\/sign-in\?created=1$/);
  await page.getByLabel("Username").fill("owner");
  await page.getByLabel("Password").fill("ValidPass1");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/systems$/);
  await expect(page.getByRole("heading", { name: "Systems", exact: true })).toBeVisible();
  await expect(page.getByText("No systems found yet")).toBeVisible();

  await page.context().clearCookies();
  await page.goto("/");
  await expect(page.getByText("Sign in to Scout", { exact: true })).toBeVisible();
  await expect(page.getByText("Protect your Scout workspace", { exact: true })).toHaveCount(0);
});
