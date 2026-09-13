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

async function openPage(page: Page, name: string): Promise<void> {
  const desktopLink = page.getByRole("button", { name, exact: true });
  if (await desktopLink.isVisible()) {
    await desktopLink.click();
    return;
  }
  await page.getByRole("button", { name: "Open navigation" }).click();
  await page.getByRole("navigation", { name: "Mobile navigation" }).getByRole("button", { name, exact: true }).click();
}

async function csrfToken(page: Page): Promise<string> {
  const cookie = (await page.context().cookies()).find((item) => item.name === "scout_csrf");
  return cookie ? decodeURIComponent(cookie.value) : "";
}

async function ownerPost(page: Page, path: string, data: unknown, idempotencyKey?: string) {
  return page.request.post(path, {
    data,
    headers: {
      "X-CSRF-Token": await csrfToken(page),
      ...(idempotencyKey ? { "Idempotency-Key": idempotencyKey } : {}),
    },
  });
}

async function waitForScanIdle(page: Page, scopeId: string): Promise<void> {
  await expect
    .poll(
      async () => {
        const response = await page.request.get(`/api/v1/scopes/${encodeURIComponent(scopeId)}/scan-status`);
        if (response.status() !== 200) return "request-failed";
        const status = (await response.json()) as { activeRun: { state: string } | null };
        return status.activeRun?.state ?? "idle";
      },
      { timeout: 15_000, intervals: [250, 500, 1000] },
    )
    .toBe("idle");
}

test("owner can configure a bounded scan and see local SSH evidence", async ({ page }) => {
  await signIn(page);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Settings", exact: true })).toBeVisible();

  const siteName = `playwright-scan-${Date.now()}`;
  const scanPort = Number(new URL(page.url()).port);
  expect(scanPort).toBeGreaterThan(0);

  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: /^Scopes Sites/ }).click();
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

  const scopeRow = page.locator(".scope-row").filter({ hasText: "127.0.0.1" }).last();
  await expect(scopeRow).toBeVisible();
  await scopeRow.getByRole("button", { name: "Enable scope" }).click();
  await expect(scopeRow.getByRole("button", { name: "Pause scope" })).toBeVisible();
  await scopeRow.getByRole("button", { name: "Enable server scan" }).click();
  await expect(scopeRow.getByRole("button", { name: "Pause server scan" })).toBeVisible();
  await expect(scopeRow.getByText("Scan coverage")).toBeVisible();
  await expect(scopeRow.getByText("Server vantage")).toBeVisible();
  await expect(scopeRow.getByText(/Capabilities:.*tcp/)).toBeVisible();
  await scopeRow.getByRole("button", { name: "Pause server scan" }).click();
  await expect(scopeRow.getByRole("button", { name: "Enable server scan" })).toBeVisible();
  await expect(scopeRow.getByText(/Paused — new work is disabled and active runs are fenced safely\./)).toBeVisible();
  await scopeRow.getByRole("button", { name: "Enable server scan" }).click();
  await expect(scopeRow.getByRole("button", { name: "Pause server scan" })).toBeVisible();
  await scopeRow.getByRole("button", { name: "Scan now" }).click();
  await expect(page.getByText("Scan queued.", { exact: true })).toBeVisible();

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

  await page.getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: /^Scopes Sites/ }).click();
  const completedScopeRow = page.locator(".scope-row").filter({ hasText: "127.0.0.1" }).last();
  await expect(completedScopeRow.getByText("Last run")).toBeVisible();
  await expect(completedScopeRow.getByText("Outcome counts")).toBeVisible();

  await page.getByRole("button", { name: "Systems" }).click();
  const provisionalSystemRow = page
    .locator("tbody tr")
    .filter({ hasText: "127.0.0.1" })
    .filter({ hasText: "Needs access" })
    .last();
  await expect(provisionalSystemRow).toBeVisible();
  await expect(provisionalSystemRow).toContainText("Needs access");
  await provisionalSystemRow.getByRole("button", { name: /View / }).click();
  await expect(page.getByRole("heading", { name: "SSH found on this system" })).toBeVisible();
  await expect(page.getByText("Provide credentials and Scout will install the agent automatically.")).toBeVisible();
  await expect(page.getByLabel("Exact targets")).toHaveValue(`127.0.0.1:${scanPort}`);
  await page.getByRole("button", { name: "All systems" }).click();

  await page.getByRole("button", { name: "Network" }).click();
  await expect(page.getByRole("heading", { name: "Found devices" })).toBeVisible();
  const foundDevices = page.getByRole("list", { name: "Found devices" });
  const foundHost = foundDevices.getByRole("listitem").filter({ hasText: "127.0.0.1" });
  await expect(foundHost).toHaveCount(1);
  await expect(foundHost.getByText(/Scan coverage:/)).toBeVisible();
  await expect(foundHost.getByText(/Server vantage available/)).toBeVisible();
  await page.getByLabel("State").selectOption("needs_credentials");
  await expect(foundHost).toHaveCount(1);
  await page.getByLabel("State").selectOption("");
  const foundHostButton = foundHost.getByRole("button");
  await foundHostButton.focus();
  await expect(foundHostButton).toBeFocused();
  await page.keyboard.press("Enter");

  await expect(page.getByRole("heading", { name: "SSH access" })).toBeVisible();
  await expect(page.getByText(`127.0.0.1:${scanPort}`, { exact: true })).toBeVisible();
  const addCredentials = page.getByRole("button", { name: "Add SSH credentials" });
  await addCredentials.focus();
  await expect(addCredentials).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: /Prepare access for 127\.0\.0\.1/ })).toBeVisible();
  await expect(page.getByLabel("Exact targets")).toHaveValue(`127.0.0.1:${scanPort}`);
  await expect(page.getByLabel("Scope ID")).toHaveValue(createdScope?.id ?? "");

  const authenticationMethod = page.getByLabel("SSH authentication");
  await expect(authenticationMethod).toHaveValue("password");
  await expect(page.getByLabel("SSH username")).toBeVisible();
  await expect(page.getByLabel("SSH password")).toBeVisible();
  await expect(page.getByLabel("Secret")).toHaveCount(0);
  await authenticationMethod.selectOption("private_key");
  await expect(page.getByLabel("SSH private key")).toBeVisible();
  await authenticationMethod.selectOption("password");
  await expect(page.getByLabel("SSH password")).toBeVisible();
  await page.getByLabel("SSH username").fill("fixture");
  await page.getByLabel("SSH password").fill("fixture-password");
  await page.getByRole("button", { name: "Store encrypted credential" }).click();
  await expect(
    page.getByText("Credential stored. The secret is write-only and will not be shown again."),
  ).toBeVisible();
  const credentialsResponse = await page.request.get("/api/v1/credentials");
  expect(credentialsResponse.status()).toBe(200);
  const credentials = (await credentialsResponse.json()) as {
    items: Array<{ metadata: Record<string, string>; targets: string[] }>;
  };
  const scopedCredential = credentials.items.find((item) => item.targets.includes(`127.0.0.1:${scanPort}`));
  expect(scopedCredential?.metadata).toMatchObject({ authMethod: "password", username: "fixture" });
  expect(JSON.stringify(scopedCredential)).not.toContain("fixture-password");
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

  await page.getByRole("button", { name: "Systems" }).click();
  const systemRow = page.locator("tbody tr").filter({ hasText: "127.0.0.1" });
  await expect(systemRow).toBeVisible();
  await systemRow.getByRole("button", { name: /View / }).click();
  await expect(page.getByRole("heading", { name: "SSH found on this system" })).toBeVisible();
  await expect(
    page.getByText("Access is approved. Scout is installing and verifying the agent automatically."),
  ).toBeVisible();
  await expect(page.getByLabel("Exact targets")).toHaveValue(`127.0.0.1:${scanPort}`);

  const scopeResponse = await page.request.get(`/api/v1/scopes/${encodeURIComponent(createdScope?.id ?? "")}`);
  expect(scopeResponse.status()).toBe(200);
  const scanScope = (await scopeResponse.json()) as { scanPolicy: { revision: number } };
  await waitForScanIdle(page, createdScope?.id ?? "");
  const duplicateRun = await ownerPost(
    page,
    `/api/v1/scopes/${encodeURIComponent(createdScope?.id ?? "")}/scan-runs`,
    { expectedRevision: scanScope.scanPolicy.revision, scanner: { kind: "server", id: "control-server" } },
    `duplicate-evidence-${Date.now()}`,
  );
  expect(duplicateRun.status()).toBe(202);
  await waitForScanIdle(page, createdScope?.id ?? "");
  const duplicateCandidatesResponse = await page.request.get(
    `/api/v1/candidates?scopeId=${encodeURIComponent(createdScope?.id ?? "")}`,
  );
  expect(duplicateCandidatesResponse.status()).toBe(200);
  const duplicateCandidates = (await duplicateCandidatesResponse.json()) as {
    items: Array<{ id: string; address: string }>;
  };
  const sameAddress = duplicateCandidates.items.filter((candidate) => candidate.address === "127.0.0.1");
  expect(sameAddress).toHaveLength(1);
  const duplicateDetailResponse = await page.request.get(`/api/v1/candidates/${sameAddress[0].id}`);
  expect(duplicateDetailResponse.status()).toBe(200);
  const duplicateDetail = await duplicateDetailResponse.text();
  expect(duplicateDetail).not.toContain("fixture-secret");
  expect((JSON.parse(duplicateDetail) as { accessRequests: unknown[] }).accessRequests).toHaveLength(1);
});

test("owner keeps unsupported services observable without requesting irrelevant access", async ({ page }) => {
  await signIn(page);
  await page.goto("/");
  const scanPort = Number(new URL(page.url()).port);
  const siteResponse = await ownerPost(page, "/api/v1/sites", { name: `playwright-unsupported-${Date.now()}` });
  expect(siteResponse.status()).toBe(201);
  const site = (await siteResponse.json()) as { id: string };
  const scopeResponse = await ownerPost(page, "/api/v1/scopes", {
    siteId: site.id,
    ranges: ["127.0.0.1"],
    exclusions: [],
    methods: ["tcp"],
    ports: [scanPort],
    enabled: true,
    limits: { probesPerSecond: 10, concurrency: 1, targetBudget: 1 },
    scanPolicy: {
      serverEnabled: true,
      agentIds: [],
      scheduleSeconds: 300,
      entryPoints: [{ id: "http-fixture", name: "HTTP fixture", transport: "tcp", port: scanPort, enabled: true }],
      limits: {
        probesPerSecond: 10,
        concurrency: 1,
        targetBudget: 1,
        attemptBudget: 1,
        timeoutMilliseconds: 1000,
        runDeadlineSeconds: 60,
        resultPageSize: 10,
      },
    },
  });
  expect(scopeResponse.status()).toBe(201);
  const scope = (await scopeResponse.json()) as { id: string; scanPolicy: { revision: number } };
  const runResponse = await ownerPost(page, `/api/v1/scopes/${scope.id}/scan-runs`, {
    expectedRevision: scope.scanPolicy.revision,
    scanner: { kind: "server", id: "control-server" },
  });
  expect(runResponse.status()).toBe(202);

  await expect
    .poll(
      async () => {
        const response = await page.request.get(`/api/v1/candidates?scopeId=${encodeURIComponent(scope.id)}`);
        if (response.status() !== 200) return "request-failed";
        const body = (await response.json()) as { items: Array<{ address: string; state: string }> };
        return body.items.find((candidate) => candidate.address === "127.0.0.1")?.state ?? "pending";
      },
      { timeout: 15_000, intervals: [250, 500, 1000] },
    )
    .toBe("unsupported");

  await page.getByRole("button", { name: "Network" }).click();
  const foundDevices = page.getByRole("list", { name: "Found devices" });
  const foundHost = foundDevices
    .getByRole("listitem")
    .filter({ hasText: "127.0.0.1" })
    .filter({ hasText: "unsupported" });
  await expect(foundHost).toHaveCount(1);
  const foundHostButton = foundHost.getByRole("button");
  await foundHostButton.focus();
  await expect(foundHostButton).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "Observed services" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "SSH access is the next step" })).not.toBeVisible();
  const candidatesResponse = await page.request.get(`/api/v1/candidates?scopeId=${encodeURIComponent(scope.id)}`);
  expect(candidatesResponse.status()).toBe(200);
  const candidates = (await candidatesResponse.json()) as { items: Array<{ id: string; address: string }> };
  const unsupported = candidates.items.find((candidate) => candidate.address === "127.0.0.1");
  expect(unsupported).toBeDefined();
  const detailResponse = await page.request.get(`/api/v1/candidates/${encodeURIComponent(unsupported?.id ?? "")}`);
  expect(detailResponse.status()).toBe(200);
  const detail = (await detailResponse.json()) as { accessRequests: unknown[] };
  expect(detail.accessRequests).toHaveLength(0);
});

for (const viewport of [
  { width: 360, height: 800 },
  { width: 768, height: 900 },
  { width: 1440, height: 1000 },
]) {
  test(`scan views remain usable at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await signIn(page);
    await page.goto("/");
    const manualAgentButton = page.getByRole("button", { name: "Add agent manually" });
    await expect(manualAgentButton).toBeVisible();
    await manualAgentButton.click();
    await expect(page.getByRole("heading", { name: "Install the first Linux agent" })).toBeVisible();
    await page.getByRole("button", { name: "Close agent setup" }).click();
    await openPage(page, "Network");
    await expect(page.getByRole("heading", { name: "Found devices" })).toBeVisible();
    const fitsViewport = await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1);
    expect(fitsViewport).toBe(true);
    await openPage(page, "Settings");
    await page.getByRole("button", { name: /^Scopes Sites/ }).click();
    await expect(page.getByRole("heading", { name: "Sites and scopes" })).toBeVisible();
  });
}
