import { generateKeyPairSync, sign } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import { releaseManifestMessage, sha256Hex } from "@/lib/server/releases";
import { closeDatabase, getDatabase } from "@/lib/server/db";
import { authSecret, controlSigningKey, credentialKey } from "@/lib/server/keys";
import { readiness } from "@/lib/server/runtime";

const directories: string[] = [];

afterEach(() => {
  closeDatabase();
  delete process.env.SCOUT_AGENT_ARTIFACT_DIR;
  delete process.env.SCOUT_DATABASE_URL;
  delete process.env.SCOUT_DATA_DIR;
  for (const directory of directories.splice(0)) {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

describe("readiness", () => {
  it("requires every embedded signed release artifact to verify", () => {
    const { artifactPath, artifact } = prepareFixture();

    const healthy = readiness();
    expect(healthy.ok).toBe(true);
    expect(healthy.checks.embeddedArtifacts).toBe(true);

    fs.writeFileSync(artifactPath, Buffer.concat([artifact, Buffer.from("tampered")]));

    const unhealthy = readiness();
    expect(unhealthy.ok).toBe(false);
    expect(unhealthy.checks.embeddedArtifacts).toBe(false);
  });
});

function prepareFixture() {
  const dataDirectory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-readiness-data-"));
  const artifactDirectory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-readiness-artifacts-"));
  directories.push(dataDirectory, artifactDirectory);
  process.env.SCOUT_DATA_DIR = dataDirectory;
  process.env.SCOUT_AGENT_ARTIFACT_DIR = artifactDirectory;

  const { sqlite } = getDatabase();
  authSecret();
  credentialKey();
  controlSigningKey();
  sqlite
    .prepare(
      "INSERT INTO app_setting (key, value, updated_at) VALUES ('scheduler_heartbeat', ?, ?)",
    )
    .run(String(Date.now()), Date.now());

  const artifact = Buffer.from("healthy-agent");
  const keys = generateKeyPairSync("ed25519");
  const publisherPublicKey = keys.publicKey
    .export({ format: "der", type: "spki" })
    .subarray(-32)
    .toString("base64url");
  const payload = {
    schemaVersion: 1 as const,
    releaseSequence: 1,
    artifacts: [
      {
        platform: "linux" as const,
        architecture: "x86_64" as const,
        version: "0.1.0",
        sequence: 1,
        sha256: sha256Hex(artifact),
        size: artifact.byteLength,
        minimumProtocol: 1,
      },
    ],
  };
  const manifest = {
    ...payload,
    signature: sign(null, Buffer.from(releaseManifestMessage(payload)), keys.privateKey).toString(
      "base64url",
    ),
  };
  const artifactPath = path.join(artifactDirectory, "scout-agent-linux-x86_64");
  fs.writeFileSync(artifactPath, artifact);
  fs.writeFileSync(path.join(artifactDirectory, "publisher-public.key"), publisherPublicKey);
  fs.writeFileSync(path.join(artifactDirectory, "release-manifest.json"), JSON.stringify(manifest));
  return { artifactPath, artifact };
}
