import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { generateKeyPairSync, randomUUID, sign } from "node:crypto";

import { afterEach, describe, expect, it } from "vitest";

import { GET as getAgentArtifact } from "@/app/api/v1/bootstrap/agent/artifact/route";
import { createAgentInvitation } from "@/lib/server/invitations";
import { closeDatabase, getDatabase } from "@/lib/server/db";
import packageJson from "../../package.json";
import { readAgentArtifact } from "@/lib/server/installer";
import { releaseManifestMessage, sha256Hex } from "@/lib/server/releases";

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

  it("lets release image builds retain the complete prebuilt artifact set", () => {
    const dockerfile = fs.readFileSync("packaging/containers/server.Dockerfile", "utf8");
    expect(dockerfile).toContain("ARG SCOUT_USE_PREBUILT_ARTIFACTS=0");
    expect(dockerfile).toContain("ENV SCOUT_USE_PREBUILT_ARTIFACTS=$SCOUT_USE_PREBUILT_ARTIFACTS");
    expect(fs.readFileSync(".dockerignore", "utf8")).toContain(
      "!agent-artifacts/release-manifest.json",
    );
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
    expect(fs.existsSync(path.join(output, "release-manifest.json"))).toBe(true);
    expect(fs.readFileSync(path.join(output, "publisher-public.key"), "utf8")).toMatch(
      /^[A-Za-z0-9_-]{43}\n?$/,
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
    expect(fs.existsSync(path.join(output, "release-manifest.json"))).toBe(true);
  });

  it("accepts a complete signed artifact set for packaged image builds", () => {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-agent-packaging-image-"));
    temporaryDirectories.push(directory);
    const output = path.join(directory, "agent-artifacts");
    fs.mkdirSync(output);
    for (const artifact of [
      "scout-agent-linux-x86_64",
      "scout-agent-linux-aarch64",
      "scout-agent-macos-x86_64",
      "scout-agent-macos-aarch64",
    ]) {
      fs.writeFileSync(path.join(output, artifact), "signed-artifact");
    }
    fs.writeFileSync(path.join(output, "release-manifest.json"), "signed-manifest");
    fs.writeFileSync(path.join(output, "publisher-public.key"), "publisher-key");

    execFileSync("sh", [path.resolve("scripts/build-local-agent.sh")], {
      cwd: directory,
      env: {
        ...process.env,
        SCOUT_USE_PREBUILT_ARTIFACTS: "1",
        SCOUT_AGENT_ARTIFACT_DIR: output,
        SCOUT_AGENT_BINARY: path.join(directory, "missing-agent-binary"),
      },
    });

    expect(fs.readdirSync(output).sort()).toEqual([
      "publisher-public.key",
      "release-manifest.json",
      "scout-agent-linux-aarch64",
      "scout-agent-linux-x86_64",
      "scout-agent-macos-aarch64",
      "scout-agent-macos-x86_64",
    ]);
  });

  it("provides a packaged-image Playwright runner", () => {
    const runner = fs.readFileSync("scripts/playwright-packaged.sh", "utf8");
    expect(runner).toContain("docker build");
    expect(runner).toContain("docker run");
    expect(runner).toContain("SCOUT_E2E_SETUP_TOKEN_FILE");
    expect(runner).toContain("SCOUT_DISCOVERY_CIDR=127.0.0.0/30");
    expect(runner).toContain("disposable discovery target");
    expect(runner).not.toContain("SCOUT_DISABLE_DISCOVERY=true");
    expect(runner).toContain("npm run test:e2e");
    expect(packageJson.scripts["test:e2e:packaged"]).toBe("./scripts/playwright-packaged.sh");
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
    writeSignedArtifact(directory, "linux", "x86_64", Buffer.from("verified-agent-binary"));
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

function writeSignedArtifact(
  directory: string,
  platform: "linux" | "macos",
  architecture: "x86_64" | "aarch64",
  artifact: Buffer,
) {
  const keys = generateKeyPairSync("ed25519");
  const publicKey = keys.publicKey
    .export({ format: "der", type: "spki" })
    .subarray(-32)
    .toString("base64url");
  const payload = {
    schemaVersion: 1 as const,
    releaseSequence: 1,
    artifacts: [
      {
        platform,
        architecture,
        version: "0.1.0",
        sequence: 1,
        sha256: sha256Hex(artifact),
        size: artifact.byteLength,
        minimumProtocol: 1,
      },
    ],
  };
  const signature = sign(
    null,
    Buffer.from(releaseManifestMessage(payload)),
    keys.privateKey,
  ).toString("base64url");
  fs.writeFileSync(path.join(directory, `scout-agent-${platform}-${architecture}`), artifact);
  fs.writeFileSync(path.join(directory, "publisher-public.key"), `${publicKey}\n`);
  fs.writeFileSync(
    path.join(directory, "release-manifest.json"),
    JSON.stringify({ ...payload, signature }),
  );
}
