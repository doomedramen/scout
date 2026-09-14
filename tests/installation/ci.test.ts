import fs from "node:fs";

import { describe, expect, it } from "vitest";

describe("release CI", () => {
  it("checks shell, workflow, and container tooling", () => {
    const workflow = fs.readFileSync(".github/workflows/quality.yml", "utf8");

    expect(workflow).toContain("shellcheck");
    expect(workflow).toContain("shfmt");
    expect(workflow).toContain("actionlint");
    expect(workflow).toContain("hadolint");
  });

  it("cancels superseded quality runs on the same ref", () => {
    const workflow = fs.readFileSync(".github/workflows/quality.yml", "utf8");

    expect(workflow).toContain("concurrency:");
    expect(workflow).toContain("group: quality-${{ github.ref }}");
    expect(workflow).toContain("cancel-in-progress: true");
  });

  it("builds the complete native release and publishes only the server image", () => {
    const workflow = fs.readFileSync(".github/workflows/release.yml", "utf8");

    expect(workflow).toContain("release_config:");
    expect(workflow).toContain("needs.release_config.outputs.enabled");
    expect(workflow).toContain("scout-agent-linux-x86_64");
    expect(workflow).toContain("scout-agent-linux-aarch64");
    expect(workflow).toContain("scout-agent-macos-x86_64");
    expect(workflow).toContain("scout-agent-macos-aarch64");
    expect(workflow).toContain("SCOUT_RELEASE_SIGNING_KEY");
    expect(workflow).toContain("SCOUT_USE_PREBUILT_ARTIFACTS=1");
    expect(workflow).toContain("ghcr.io/doomedramen/scout");
    expect(workflow).not.toContain("scout-agent:");
  });

  it("promotes an already-published immutable image digest", () => {
    const workflow = fs.readFileSync(".github/workflows/promote.yml", "utf8");

    expect(workflow).toContain("source_sha");
    expect(workflow).toContain("imagetools create");
    expect(workflow).toContain("latest");
    expect(workflow).toContain("ghcr.io/doomedramen/scout");
  });

  it("runs Playwright against the packaged Docker image", () => {
    const workflow = fs.readFileSync(".github/workflows/quality.yml", "utf8");

    expect(workflow).toContain("playwright install --with-deps chromium");
    expect(workflow).toContain("npm run test:e2e:packaged");
  });

  it("tests the release image before publishing its tags", () => {
    const workflow = fs.readFileSync(".github/workflows/release.yml", "utf8");
    const packagedScript = fs.readFileSync("scripts/playwright-packaged.sh", "utf8");

    expect(workflow).toContain("load: true");
    expect(workflow).toContain('SCOUT_PACKAGED_E2E_SKIP_BUILD: "1"');
    expect(workflow).toContain("docker push");
    expect(packagedScript).toContain("SCOUT_PACKAGED_E2E_SKIP_BUILD");
  });

  it("uses a runner-native agent target for the web build", () => {
    const workflow = fs.readFileSync(".github/workflows/quality.yml", "utf8");

    expect(workflow).toContain("SCOUT_AGENT_TARGET: x86_64-unknown-linux-gnu");
  });

  it("keeps the native SSH dependency external to the Next server bundle", () => {
    const nextConfig = fs.readFileSync("next.config.ts", "utf8");

    expect(nextConfig).toContain("serverExternalPackages");
    expect(nextConfig).toContain('"ssh2"');
  });
});
