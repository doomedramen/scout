import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { randomUUID } from "node:crypto";

import { afterEach, describe, expect, it } from "vitest";

import { GET as getAgentArtifact } from "@/app/api/v1/bootstrap/agent/artifact/route";
import { createAgentInvitation } from "@/lib/server/invitations";
import { closeDatabase, getDatabase } from "@/lib/server/db";
import packageJson from "../../package.json";
import { readAgentArtifact } from "@/lib/server/installer";

const temporaryDirectories: string[] = [];

afterEach(() => {
  delete process.env.SCOUT_AGENT_ARTIFACT_DIR;
  delete process.env.SCOUT_DATABASE_URL;
  closeDatabase();
  for (const directory of temporaryDirectories.splice(0)) {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

describe("agent packaging", () => {
  it("stages an agent before the server production build", () => {
    expect(packageJson.scripts.build).toContain("npm run agent:build");
  });

  it("stages a prebuilt agent binary for image builds", () => {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-agent-packaging-"));
    temporaryDirectories.push(directory);
    const binary = path.join(directory, `scout-agent-${randomUUID()}`);
    const output = path.join(directory, "agent-artifacts");
    fs.writeFileSync(binary, "verified-agent-binary");

    execFileSync("sh", [path.resolve("scripts/build-local-agent.sh")], {
      cwd: directory,
      env: {
        ...process.env,
        SCOUT_AGENT_BINARY: binary,
        SCOUT_AGENT_PLATFORM: "linux",
        SCOUT_AGENT_ARCHITECTURE: "x86_64",
      },
    });

    expect(fs.readFileSync(path.join(output, "scout-agent-linux-x86_64"), "utf8")).toBe(
      "verified-agent-binary",
    );
  });

  it("stages a prebuilt agent for the requested macOS architecture", () => {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-agent-packaging-macos-"));
    temporaryDirectories.push(directory);
    const binary = path.join(directory, `scout-agent-${randomUUID()}`);
    const output = path.join(directory, "agent-artifacts");
    fs.writeFileSync(binary, "verified-macos-agent-binary");

    execFileSync("sh", [path.resolve("scripts/build-local-agent.sh")], {
      cwd: directory,
      env: {
        ...process.env,
        SCOUT_AGENT_BINARY: binary,
        SCOUT_AGENT_PLATFORM: "macos",
        SCOUT_AGENT_ARCHITECTURE: "aarch64",
      },
    });

    expect(fs.readFileSync(path.join(output, "scout-agent-macos-aarch64"), "utf8")).toBe(
      "verified-macos-agent-binary",
    );
  });

  it("reads the packaged artifact when the server runs outside the repository root", () => {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-agent-runtime-"));
    temporaryDirectories.push(directory);
    fs.writeFileSync(path.join(directory, "scout-agent-linux-x86_64"), "verified-agent-binary");
    process.env.SCOUT_AGENT_ARTIFACT_DIR = directory;

    expect(readAgentArtifact("linux", "x86_64")?.toString()).toBe("verified-agent-binary");
  });

  it("serves the staged artifact through the invitation-authorized bootstrap route", async () => {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-agent-route-"));
    temporaryDirectories.push(directory);
    fs.writeFileSync(path.join(directory, "scout-agent-linux-x86_64"), "verified-agent-binary");
    process.env.SCOUT_AGENT_ARTIFACT_DIR = directory;
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    const { sqlite } = getDatabase();
    const systemId = randomUUID();
    const now = Date.now();
    sqlite
      .prepare("INSERT INTO system (id, display_name, created_at, updated_at) VALUES (?, ?, ?, ?)")
      .run(systemId, "artifact-test", now, now);
    const invitation = createAgentInvitation(systemId, now);

    const response = await getAgentArtifact(
      new Request(
        "http://localhost/api/v1/bootstrap/agent/artifact?platform=linux&architecture=x86_64",
        { headers: { "x-scout-oti": invitation.token } },
      ),
    );

    expect(response.status).toBe(200);
    expect(Buffer.from(await response.arrayBuffer()).toString()).toBe("verified-agent-binary");
  });
});
