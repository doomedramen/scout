import fs from "node:fs";
import { randomUUID } from "node:crypto";

import { expect, test } from "@playwright/test";

import { e2eDataDirectory } from "../../playwright.config";

process.env.SCOUT_DATA_DIR = e2eDataDirectory;
delete process.env.SCOUT_DATABASE_URL;

function setupTokenPath(): string {
  return process.env.SCOUT_E2E_SETUP_TOKEN_FILE ?? `${e2eDataDirectory}/setup-token`;
}

test("owner setup lands on Systems and future visits use sign-in", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("Protect your Scout workspace", { exact: true })).toBeVisible();

  await page.getByLabel("One-time setup token").fill(fs.readFileSync(setupTokenPath(), "utf8"));
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
  await expect
    .poll(() => page.locator("html").evaluate((element) => getComputedStyle(element).fontFamily))
    .not.toBe("Times");

  await page.context().clearCookies();
  await page.goto("/");
  await expect(page.getByText("Sign in to Scout", { exact: true })).toBeVisible();
  await expect(page.getByText("Protect your Scout workspace", { exact: true })).toHaveCount(0);
});

test("retry installation reloads the current job state", async ({ page }) => {
  test.skip(
    process.env.SCOUT_E2E_PACKAGED === "1",
    "this fixture intentionally uses the in-process test database",
  );
  await page.goto("/sign-in");
  await page.getByLabel("Username").fill("owner");
  await page.getByLabel("Password").fill("ValidPass1");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/systems$/);

  const { closeDatabase, getDatabase } = await import("@/lib/server/db");
  const { createAccessGrant } = await import("@/lib/server/access");
  const { sqlite } = getDatabase();
  const systemId = randomUUID();
  const now = Date.now();
  sqlite
    .prepare(
      "INSERT INTO system (id, display_name, status, created_at, updated_at) VALUES (?, ?, 'blocked', ?, ?)",
    )
    .run(systemId, "retry-fixture", now, now);
  sqlite
    .prepare(
      "INSERT INTO access_evidence (id, system_id, method, address, port, outcome, fingerprint, source, observed_at, expires_at) VALUES (?, ?, 'ssh', '127.0.0.1', 9, 'open', ?, 'server-scan', ?, ?)",
    )
    .run(randomUUID(), systemId, "SHA256:retry-fixture", now, now + 1_800_000);
  const grant = createAccessGrant(
    {
      systemId,
      method: "ssh",
      username: "fixture",
      authType: "password",
      secret: "FixturePass1",
      passphrase: null,
      fingerprint: "SHA256:retry-fixture",
      trust: true,
      idempotencyKey: randomUUID(),
    },
    now,
  );
  sqlite
    .prepare(
      "UPDATE enrollment_job SET status = 'failed', stage = 'installation', error_code = 'installer', error_message = 'fixture failure' WHERE id = ?",
    )
    .run(grant.jobId);
  sqlite.prepare("UPDATE system SET status = 'blocked' WHERE id = ?").run(systemId);
  sqlite
    .prepare(
      "INSERT INTO app_setting (key, value, updated_at) VALUES ('authority_paused', 'true', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at",
    )
    .run(now);
  closeDatabase();

  await page.goto(`/systems/${systemId}`);
  await expect(page.getByText("Installation failed", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Retry installation", exact: true }).click();
  await expect(page.getByText("Installation in progress: Queued", { exact: true })).toBeVisible();
});
