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
      requireHttpTrustPin: true,
      publisherPublicKey: "publisher-key",
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
    expect(script).toContain("SCOUT_AGENT_VERSION=${SCOUT_AGENT_VERSION:-'0.1.0'}");
    expect(script).toContain("scout-agent-install.lock");
    expect(script).toContain('release_dir="$SCOUT_AGENT_ROOT/releases/$SCOUT_AGENT_VERSION"');
    expect(script).toContain(
      'exec "${SCOUT_AGENT_ROOT:-/var/lib/scout-agent}/current/scout-agent" "$@"',
    );
    expect(script).toContain("scout-agent-launcher");
    expect(script).toContain("systemctl enable --now scout-agent.service");
    expect(script).toContain("runuser_binary=$(command -v runuser");
    expect(script).toContain("/usr/sbin/runuser");
    expect(script).toContain(
      '"$runuser_binary" -u scout-agent -- env SCOUT_SERVER_URL="$SCOUT_SERVER_URL"',
    );
    expect(script).toContain("launchctl bootstrap system");
    expect(script).toContain("SCOUT_TRUST_PIN");
    expect(script).toContain("SCOUT_RELEASE_PUBLISHER_PUBLIC_KEY");
    expect(script).toContain("SCOUT_REQUIRE_TRUST_PIN=1");
    expect(script).toContain("Scout callback is unreachable at $SCOUT_CALLBACK_URL");
    expect(script).toContain("run_linux_agent_once()");
  });

  it("requires the out-of-band server pin before an HTTP manual bootstrap", () => {
    const script = renderInstallerScript({
      serverUrl: "http://scout.example.test:8080",
      callbackUrl: "http://scout.example.test:8080",
      invitation: "oti",
      requireHttpTrustPin: true,
    });

    expect(script).toContain('case "$SCOUT_SERVER_URL" in');
    expect(script).toContain('fail "SCOUT_TRUST_PIN is required for HTTP bootstrap"');
    expect(script).toContain("/api/v1/bootstrap/fingerprint");
  });
});
