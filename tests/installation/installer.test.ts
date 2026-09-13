import { execFileSync } from "node:child_process";
import { unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { randomUUID } from "node:crypto";

import { describe, expect, it } from "vitest";

import { renderInstallerScript } from "@/lib/server/installer";

describe("agent installer", () => {
  it("renders a syntax-valid self-checking installer with an embedded server URL", () => {
    const script = renderInstallerScript({
      serverUrl: "https://scout.example.test:18443",
      callbackUrl: "http://192.0.2.5:18443",
      invitation: "oti-with-'-quote",
    });
    const filename = path.join(tmpdir(), `scout-installer-${randomUUID()}.sh`);
    writeFileSync(filename, script, { mode: 0o700 });
    try {
      execFileSync("sh", ["-n", filename]);
    } finally {
      // The file contains no credentials beyond the test invitation and is removed immediately.
      writeFileSync(filename, "", { mode: 0o600 });
      unlinkSync(filename);
    }
    expect(script).toContain("https://scout.example.test:18443");
    expect(script).toContain("SCOUT_OTI=${SCOUT_OTI:-'oti-with-'\\''-quote'}");
    expect(script).toContain("systemctl enable --now scout-agent.service");
    expect(script).toContain("launchctl bootstrap system");
  });
});
