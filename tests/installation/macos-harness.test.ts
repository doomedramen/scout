import fs from "node:fs";

import { describe, expect, it } from "vitest";

import { renderInstallerScript } from "@/lib/server/installer";

describe("macOS lifecycle acceptance harness", () => {
  it("exercises the rendered installer with a real launchd daemon", () => {
    const script = fs.readFileSync("scripts/macos-launchd-acceptance.ts", "utf8");
    const renderedInstaller = renderInstallerScript({
      serverUrl: "http://127.0.0.1:1",
      callbackUrl: "http://127.0.0.1:1",
      invitation: "test-invitation",
    });

    expect(script).toContain("renderInstallerScript");
    expect(renderedInstaller).toContain("launchctl bootstrap system");
    expect(script).toContain('"kickstart", "-k"');
    expect(script).toContain('"bootout", "system"');
    expect(script).toContain("SCOUT_MACOS_AGENT_BINARY");
    expect(script).toContain(
      "const timeoutMs = Number(process.env.SCOUT_MACOS_INSTALL_TIMEOUT_MS ?? 120_000)",
    );
    expect(script).toContain('spawn("sh", ["-x", installerPath]');
    expect(script).toContain("127.0.0.1");
    expect(script).not.toContain("scout.lab.rtin.page");
    expect(script).not.toContain("192.168.1.");
  });

  it("runs on both supported GitHub macOS runners", () => {
    const workflow = fs.readFileSync(".github/workflows/quality.yml", "utf8");

    expect(workflow).toContain("macos-lifecycle:");
    expect(workflow).toContain("runner: macos-15-intel");
    expect(workflow).toContain("runner: macos-15");
    expect(workflow).toContain("macos-launchd-acceptance.ts");
    expect(workflow).toContain("SCOUT_MACOS_AGENT_BINARY");
  });
});
