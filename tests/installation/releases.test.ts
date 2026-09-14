import { generateKeyPairSync, sign } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import {
  releaseManifestMessage,
  sha256Hex,
  verifyReleaseManifest,
  readVerifiedAgentArtifact,
} from "@/lib/server/releases";

const directories: string[] = [];

afterEach(() => {
  delete process.env.SCOUT_AGENT_ARTIFACT_DIR;
  for (const directory of directories.splice(0))
    fs.rmSync(directory, { recursive: true, force: true });
});

describe("verified agent releases", () => {
  it("accepts a signed compatible manifest and verifies its artifact digest", () => {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-release-"));
    directories.push(directory);
    const artifact = Buffer.from("agent-release");
    const keys = generateKeyPairSync("ed25519");
    const publicKey = keys.publicKey
      .export({ format: "der", type: "spki" })
      .subarray(-32)
      .toString("base64url");
    const payload = {
      schemaVersion: 1 as const,
      releaseSequence: 3,
      artifacts: [
        {
          platform: "linux" as const,
          architecture: "x86_64" as const,
          version: "0.1.0",
          sequence: 3,
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
    fs.writeFileSync(path.join(directory, "release-manifest.json"), JSON.stringify(manifest));
    fs.writeFileSync(path.join(directory, "publisher-public.key"), publicKey);
    fs.writeFileSync(path.join(directory, "scout-agent-linux-x86_64"), artifact);
    process.env.SCOUT_AGENT_ARTIFACT_DIR = directory;

    expect(verifyReleaseManifest(manifest, publicKey)).toEqual(payload);
    expect(readVerifiedAgentArtifact("linux", "x86_64")?.toString()).toBe("agent-release");
  });

  it("rejects changed signatures, artifact bytes, and release metadata", () => {
    const keys = generateKeyPairSync("ed25519");
    const payload = {
      schemaVersion: 1 as const,
      releaseSequence: 3,
      artifacts: [
        {
          platform: "linux" as const,
          architecture: "x86_64" as const,
          version: "0.1.0",
          sequence: 3,
          sha256: sha256Hex(Buffer.from("agent-release")),
          size: 13,
          minimumProtocol: 1,
        },
      ],
    };
    const signature = sign(
      null,
      Buffer.from(releaseManifestMessage(payload)),
      keys.privateKey,
    ).toString("base64url");
    const manifest = { ...payload, signature };
    const publicKey = keys.publicKey
      .export({ format: "der", type: "spki" })
      .subarray(-32)
      .toString("base64url");

    expect(() =>
      verifyReleaseManifest({ ...manifest, signature: `${signature}x` }, publicKey),
    ).toThrow(/signature/i);
    expect(() =>
      verifyReleaseManifest({ ...manifest, releaseSequence: 4, signature }, publicKey),
    ).toThrow(/signature/i);
  });

  it("fails closed when the packaged artifact does not match its signed digest", () => {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-release-tamper-"));
    directories.push(directory);
    const keys = generateKeyPairSync("ed25519");
    const publicKey = keys.publicKey
      .export({ format: "der", type: "spki" })
      .subarray(-32)
      .toString("base64url");
    const artifact = Buffer.from("agent-release");
    const payload = {
      schemaVersion: 1 as const,
      releaseSequence: 3,
      artifacts: [
        {
          platform: "linux" as const,
          architecture: "x86_64" as const,
          version: "0.1.0",
          sequence: 3,
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
    fs.writeFileSync(path.join(directory, "release-manifest.json"), JSON.stringify(manifest));
    fs.writeFileSync(path.join(directory, "publisher-public.key"), publicKey);
    fs.writeFileSync(path.join(directory, "scout-agent-linux-x86_64"), "tampered");
    process.env.SCOUT_AGENT_ARTIFACT_DIR = directory;

    expect(() => readVerifiedAgentArtifact("linux", "x86_64")).toThrow(/digest|size/i);
  });
});
