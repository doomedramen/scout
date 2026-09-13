import { expect, test, type Page, type Route } from "@playwright/test";

const password = process.env.SCOUT_E2E_PASSWORD ?? "ScoutAa1";

type StatusMode = "running" | "partial" | "stale";
type CandidateMode = "found" | "empty" | "error";

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

function scanRun(overrides: Record<string, unknown> = {}) {
  return {
    id: "run-fixture",
    scopeId: "scope-fixture",
    scopeRevision: 1,
    scanner: { kind: "server", id: "control-server" },
    trigger: "schedule",
    state: "running",
    scheduledAt: "2026-09-13T11:59:00Z",
    startedAt: "2026-09-13T11:59:05Z",
    finishedAt: null,
    assignmentExpiresAt: "2026-09-13T12:10:00Z",
    targetsPlanned: 10,
    attemptsPlanned: 10,
    attemptsCompleted: 4,
    outcomeCounts: {
      open: 1,
      closed: 3,
      filtered: 0,
      unreachable: 0,
      skipped: 0,
      scannerError: 0,
    },
    cancellationRequested: false,
    partialReason: null,
    errorCode: null,
    pageCount: 1,
    finalPageOrdinal: null,
    ...overrides,
  };
}

function scanStatus(mode: StatusMode) {
  if (mode === "running") {
    const active = scanRun();
    return {
      scopeId: "scope-fixture",
      policyRevision: 1,
      enabled: true,
      lastCompletedAt: "2026-09-13T11:45:00Z",
      nextScheduledAt: "2026-09-13T12:04:00Z",
      activeRun: active,
      lastRun: active,
      vantages: [
        {
          scanner: { kind: "server", id: "control-server" },
          state: "available",
          assigned: true,
          capabilities: ["scan-protocol:1", "tcp"],
          lastSeen: null,
          lastCompletedAt: null,
          nextScheduledAt: "2026-09-13T12:04:00Z",
          activeRun: active,
        },
        {
          scanner: { kind: "agent", id: "agent-fixture", deviceId: "device-scanner" },
          state: "available",
          assigned: true,
          capabilities: ["scan-protocol:1", "tcp"],
          lastSeen: "2026-09-13T11:58:00Z",
          lastCompletedAt: "2026-09-13T11:45:00Z",
          nextScheduledAt: "2026-09-13T12:04:00Z",
          activeRun: null,
        },
      ],
      coverageState: "partial",
      partialReason: null,
      candidateOutcomeCounts: { needs_credentials: 1 },
      retention: {
        evidenceBefore: "2026-08-14T12:00:00Z",
        runsBefore: "2026-06-15T12:00:00Z",
        lagSeconds: 0,
        blocked: false,
      },
      queue: { activeRuns: 1, pendingPages: 1, backpressure: false },
    };
  }

  const terminal =
    mode === "partial"
      ? scanRun({
          state: "partial",
          scheduledAt: "2026-09-13T11:50:00Z",
          startedAt: "2026-09-13T11:50:05Z",
          finishedAt: "2026-09-13T11:51:00Z",
          attemptsCompleted: 6,
          partialReason: "rate_limit",
        })
      : scanRun({
          state: "completed",
          scheduledAt: "2026-09-13T09:50:00Z",
          startedAt: "2026-09-13T09:50:05Z",
          finishedAt: "2026-09-13T09:51:00Z",
          attemptsCompleted: 10,
        });
  const nextScheduledAt = mode === "partial" ? "2026-09-13T12:05:00Z" : "2026-09-13T12:10:00Z";
  return {
    scopeId: "scope-fixture",
    policyRevision: 1,
    enabled: true,
    lastCompletedAt: terminal.finishedAt,
    nextScheduledAt,
    activeRun: null,
    lastRun: terminal,
    vantages: [
      {
        scanner: { kind: "server", id: "control-server" },
        state: "available",
        assigned: true,
        capabilities: ["scan-protocol:1", "tcp"],
        lastSeen: null,
        lastCompletedAt: terminal.finishedAt,
        nextScheduledAt,
        activeRun: null,
      },
      {
        scanner: { kind: "agent", id: "agent-fixture", deviceId: "device-scanner" },
        state: "available",
        assigned: true,
        capabilities: ["scan-protocol:1", "tcp"],
        lastSeen: "2026-09-13T11:58:00Z",
        lastCompletedAt: "2026-09-13T11:45:00Z",
        nextScheduledAt,
        activeRun: null,
      },
    ],
    coverageState: mode === "partial" ? "partial" : "stale",
    partialReason: mode === "partial" ? "rate_limit" : null,
    candidateOutcomeCounts: { needs_credentials: 1 },
    retention: {
      evidenceBefore: "2026-08-14T12:00:00Z",
      runsBefore: "2026-06-15T12:00:00Z",
      lagSeconds: 0,
      blocked: false,
    },
    queue: { activeRuns: 0, pendingPages: 0, backpressure: false },
  };
}

async function installRoutes(page: Page, getStatusMode: () => StatusMode, getCandidateMode: () => CandidateMode) {
  await page.route("**/api/v1/topology", async (route: Route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        nodes: [
          {
            id: "device-scanner",
            label: "Scanner host",
            addresses: ["192.0.2.10"],
            availability: "online",
            lifecycle: "enrolled",
          },
        ],
        relationships: [],
        nextCursor: null,
      }),
    });
  });
  await page.route("**/api/v1/sites", async (route: Route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: "site-fixture",
            name: "Fixture network",
            addressContext: "192.0.2.0/24",
            createdAt: "2026-09-13T10:00:00Z",
          },
        ],
        nextCursor: null,
      }),
    });
  });
  await page.route("**/api/v1/scopes", async (route: Route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: "scope-fixture",
            siteId: "site-fixture",
            ranges: ["192.0.2.0/24"],
            exclusions: [],
            allowedMethods: ["tcp"],
            ports: [22],
            enabled: true,
            revision: 1,
            scanPolicy: {
              revision: 1,
              enabled: true,
              serverEnabled: true,
              agentIds: ["agent-fixture"],
              scheduleSeconds: 300,
              entryPoints: [
                { id: "ssh-22", name: "SSH 22", transport: "tcp", port: 22, accessMethod: "ssh", enabled: true },
              ],
              limits: {
                probesPerSecond: 10,
                concurrency: 16,
                targetBudget: 256,
                attemptBudget: 256,
                timeoutMilliseconds: 2000,
                runDeadlineSeconds: 600,
                resultPageSize: 100,
              },
            },
          },
        ],
        nextCursor: null,
      }),
    });
  });
  await page.route("**/api/v1/devices*", async (route: Route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: "device-scanner",
            displayName: "Scanner host",
            siteId: "site-fixture",
            platform: "linux",
            architecture: "amd64",
            hostname: "scanner",
            addresses: ["192.0.2.10"],
            agentId: "agent-fixture",
            lifecycle: "enrolled",
            availability: "online",
            metricFreshness: {},
            collectorStates: [],
            revision: 1,
            agentVersion: "1.0.0",
          },
        ],
        nextCursor: null,
      }),
    });
  });
  await page.route("**/api/v1/candidates*", async (route: Route) => {
    const mode = getCandidateMode();
    if (mode === "error") {
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "unavailable",
            message: "Found devices unavailable",
            requestId: "request-fixture",
            retryable: true,
          },
        }),
      });
      return;
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        items:
          mode === "empty"
            ? []
            : [
                {
                  id: "candidate-fixture",
                  siteId: "site-fixture",
                  scopeId: "scope-fixture",
                  address: "192.0.2.44",
                  hostname: "nas",
                  source: "server-scan",
                  displayName: "nas",
                  state: "needs_credentials",
                  coverageState: "partial",
                  entryPointCount: 1,
                  scopeRevision: 1,
                  firstSeen: "2026-09-13T11:40:00Z",
                  lastSeen: "2026-09-13T11:59:00Z",
                  expiresAt: "2026-09-14T11:59:00Z",
                  excluded: false,
                  entryPointIds: ["ssh-22"],
                  preferredAccessMethod: "ssh",
                  lastScannedAt: "2026-09-13T11:59:00Z",
                  action: { kind: "assign_credentials", label: "Add SSH credentials" },
                },
              ],
        nextCursor: null,
      }),
    });
  });
  await page.route("**/api/v1/scopes/scope-fixture/scan-status", async (route: Route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(scanStatus(getStatusMode())) });
  });
}

test("owner can inspect active, partial, and stale scan coverage", async ({ page }) => {
  let statusMode: StatusMode = "running";
  let candidateMode: CandidateMode = "found";
  await signIn(page);
  await installRoutes(
    page,
    () => statusMode,
    () => candidateMode,
  );

  await page.goto("/network");
  await expect(page.getByRole("heading", { name: "Found devices" })).toBeVisible();
  const foundHost = page
    .getByRole("list", { name: "Found devices" })
    .getByRole("listitem")
    .filter({ hasText: "192.0.2.44" });
  await expect(foundHost).toContainText("Scan coverage: Running");
  await expect(foundHost).toContainText("Active Running run · 4/10 attempts");
  await expect(foundHost).toContainText("Server vantage available");

  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: /^Scopes Sites/ }).click();
  const scopeRow = page.locator(".scope-row").filter({ hasText: "192.0.2.0/24" });
  await expect(scopeRow).toBeVisible();
  await expect(scopeRow).toContainText("Server vantage");
  await expect(scopeRow).toContainText("Agent · Scanner host");
  await expect(scopeRow).toContainText("Last completed");
  await expect(scopeRow).toContainText("next scheduled");

  statusMode = "partial";
  await page.goto("/network");
  await expect(foundHost).toContainText("Scan coverage: Partial");
  await expect(foundHost).toContainText("completed");

  statusMode = "stale";
  await page.goto("/scopes");
  const staleScopeRow = page.locator(".scope-row").filter({ hasText: "192.0.2.0/24" });
  await expect(staleScopeRow).toContainText("Scan coverage");
  await expect(staleScopeRow).toContainText("Evidence is stale until a complete run succeeds.");
});

test("network scan keeps empty and unavailable results explicit", async ({ page }) => {
  let statusMode: StatusMode = "stale";
  let candidateMode: CandidateMode = "empty";
  await signIn(page);
  await installRoutes(
    page,
    () => statusMode,
    () => candidateMode,
  );

  await page.goto("/network");
  await expect(page.getByRole("heading", { name: "Found devices" })).toBeVisible();
  await expect(page.getByText("No devices found.", { exact: true })).toBeVisible();

  candidateMode = "error";
  await page.reload();
  await expect(page.locator(".empty-inline[role='alert']")).toContainText("Found devices unavailable");
});
