import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";

import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "../..");
const harness = path.join(root, "scripts/qemu-linux-acceptance.sh");

describe("Linux QEMU acceptance harness", () => {
  it("is a syntax-valid disposable target runner", () => {
    expect(existsSync(harness)).toBe(true);
    execFileSync("sh", ["-n", harness]);
  });

  it("uses an isolated configured probe and never targets the real LAN", () => {
    const source = readFileSync(harness, "utf8");
    expect(source).toContain("SCOUT_DISCOVERY_CIDR");
    expect(source).toContain("host.docker.internal");
    expect(source).toContain("10.0.2.2");
    expect(source).toContain("--retry-all-errors");
    expect(source).toContain("trap cleanup EXIT INT TERM");
    expect(source).not.toContain("192.168.1.0/24");
  });
});
