import { expect, test, type Page } from "@playwright/test";

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

test("owner can configure a bounded scan and see local SSH evidence", async ({ page }) => {
  await signIn(page);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Scopes" })).toBeVisible();

  const siteName = `playwright-scan-${Date.now()}`;
  const scanPort = Number(new URL(page.url()).port);
  expect(scanPort).toBeGreaterThan(0);

  await page.getByRole("button", { name: "Scopes" }).click();
  await expect(page.getByRole("heading", { name: "Sites and scopes" })).toBeVisible();
  await page.getByLabel("Site name").fill(siteName);
  await page.getByRole("button", { name: "Add site" }).click();
  await page.getByRole("combobox").first().selectOption({ label: siteName });
  await page.getByLabel("Ranges CIDRs or literal addresses").fill("127.0.0.1");
  await page.getByLabel("Exclusions optional, comma separated").fill("192.0.2.2");
  await page.getByLabel("Ports").fill(String(scanPort));
  await page.getByRole("button", { name: "Save disabled scope" }).click();
  await expect(
    page.getByText("Scope saved disabled. Enable it only after reviewing access and exclusions."),
  ).toBeVisible();

  const scope = page.locator(".scope-row").filter({ hasText: "127.0.0.1" }).last();
  await expect(scope).toBeVisible();
  await scope.getByRole("button", { name: "Enable scope" }).click();
  await expect(scope.getByRole("button", { name: "Pause scope" })).toBeVisible();
  await scope.getByRole("button", { name: "Enable server scan" }).click();
  await expect(scope.getByRole("button", { name: "Pause server scan" })).toBeVisible();
  await scope.getByRole("button", { name: "Scan now" }).click();
  await expect(
    page.getByText("Scan queued. Results and discovered SSH services will appear here shortly."),
  ).toBeVisible();

  const scopesResponse = await page.request.get("/api/v1/scopes");
  expect(scopesResponse.status()).toBe(200);
  const scopes = (await scopesResponse.json()) as { items: Array<{ id: string; ranges: string[] }> };
  const createdScope = scopes.items.find((item) => item.ranges.includes("127.0.0.1"));
  expect(createdScope).toBeDefined();

  await expect
    .poll(
      async () => {
        const candidatesResponse = await page.request.get(
          `/api/v1/candidates?scopeId=${encodeURIComponent(createdScope?.id ?? "")}`,
        );
        if (candidatesResponse.status() !== 200) return "request-failed";
        const candidates = (await candidatesResponse.json()) as {
          items: Array<{ address: string; state: string }>;
        };
        return candidates.items.find((candidate) => candidate.address === "127.0.0.1")?.state ?? "pending";
      },
      { timeout: 15_000, intervals: [250, 500, 1000] },
    )
    .toBe("needs_credentials");

  await page.getByRole("button", { name: "Network" }).click();
  await expect(page.getByRole("heading", { name: "Found devices" })).toBeVisible();
  const foundDevices = page.getByRole("list", { name: "Found devices" });
  const foundHost = foundDevices.getByRole("listitem").filter({ hasText: "127.0.0.1" });
  await expect(foundHost).toHaveCount(1);
  await foundHost.getByRole("button").click();

  await expect(page.getByRole("heading", { name: "SSH access is the next step" })).toBeVisible();
  await expect(page.getByText(`127.0.0.1:${scanPort}`, { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Add SSH credentials" }).click();
  await expect(page.getByRole("heading", { name: /Prepare access for 127\.0\.0\.1/ })).toBeVisible();
  await expect(page.getByLabel("Exact targets")).toHaveValue(`127.0.0.1:${scanPort}`);
  await expect(page.getByLabel("Scope ID")).toHaveValue(createdScope?.id ?? "");

  await page.getByLabel("Secret").fill("fixture-secret");
  await page.getByRole("button", { name: "Store encrypted credential" }).click();
  await expect(
    page.getByText("Credential stored. The secret is write-only and will not be shown again."),
  ).toBeVisible();
  await page.getByLabel("Fingerprint").fill("SHA256:fixture");
  await page.getByRole("button", { name: "Record trusted identity" }).click();
  await expect(page.getByText("Trust record added. Scout will not accept a changed key automatically.")).toBeVisible();

  await expect
    .poll(
      async () => {
        const candidatesResponse = await page.request.get(
          `/api/v1/candidates?scopeId=${encodeURIComponent(createdScope?.id ?? "")}`,
        );
        if (candidatesResponse.status() !== 200) return "request-failed";
        const candidates = (await candidatesResponse.json()) as {
          items: Array<{ address: string; state: string }>;
        };
        return candidates.items.find((candidate) => candidate.address === "127.0.0.1")?.state ?? "pending";
      },
      { timeout: 15_000, intervals: [250, 500, 1000] },
    )
    .toBe("queued");
  const jobsResponse = await page.request.get("/api/v1/jobs");
  expect(jobsResponse.status()).toBe(200);
  const jobs = (await jobsResponse.json()) as { items: Array<{ kind: string }> };
  expect(jobs.items.filter((job) => job.kind === "enrollment")).toHaveLength(1);
});
