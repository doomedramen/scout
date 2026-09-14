import { createHash, createPublicKey, verify, type KeyObject } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

import { z } from "zod";

import {
  artifactName,
  artifactPath,
  type InstallerArchitecture,
  type InstallerPlatform,
} from "@/lib/server/installer";

const RAW_ED25519_PUBLIC_KEY_PREFIX = Buffer.from("302a300506032b6570032100", "hex");
const MAX_RELEASE_SEQUENCE = Number.MAX_SAFE_INTEGER;

const releaseArtifactInput = z.object({
  platform: z.enum(["linux", "macos"]),
  architecture: z.enum(["x86_64", "aarch64"]),
  version: z.string().min(1).max(64),
  sequence: z.number().int().positive().max(MAX_RELEASE_SEQUENCE),
  sha256: z.string().regex(/^[a-f0-9]{64}$/),
  size: z
    .number()
    .int()
    .positive()
    .max(512 * 1024 * 1024),
  minimumProtocol: z.number().int().positive().max(1024),
});

export const releaseManifestInput = z.object({
  schemaVersion: z.literal(1),
  releaseSequence: z.number().int().positive().max(MAX_RELEASE_SEQUENCE),
  artifacts: z.array(releaseArtifactInput).min(1).max(8),
  signature: z.string().regex(/^[A-Za-z0-9_-]{86}$/),
});

export type ReleaseArtifact = z.infer<typeof releaseArtifactInput>;
export type ReleaseManifestPayload = Omit<z.infer<typeof releaseManifestInput>, "signature">;
export type SignedReleaseManifest = z.infer<typeof releaseManifestInput>;

const ARTIFACT_DIRECTORY = "agent-artifacts";
const MANIFEST_FILENAME = "release-manifest.json";
const PUBLISHER_KEY_FILENAME = "publisher-public.key";

export function sha256Hex(value: Buffer): string {
  return createHash("sha256").update(value).digest("hex");
}

/**
 * Return stable bytes for signing. Artifact order is not significant in the
 * manifest, so signatures remain stable when a build matrix finishes in a
 * different order.
 */
export function releaseManifestMessage(payload: ReleaseManifestPayload): string {
  const artifacts = [...payload.artifacts].sort((left, right) =>
    `${left.platform}/${left.architecture}`.localeCompare(
      `${right.platform}/${right.architecture}`,
    ),
  );
  return JSON.stringify({
    schemaVersion: payload.schemaVersion,
    releaseSequence: payload.releaseSequence,
    artifacts,
  });
}

export function verifyReleaseManifest(
  input: unknown,
  publisherPublicKey: string | KeyObject,
): ReleaseManifestPayload {
  const manifest = releaseManifestInput.parse(input);
  const publicKey =
    typeof publisherPublicKey === "string"
      ? publicKeyFromRaw(publisherPublicKey)
      : publisherPublicKey;
  const valid = verify(
    null,
    Buffer.from(releaseManifestMessage(manifest), "utf8"),
    publicKey,
    Buffer.from(manifest.signature, "base64url"),
  );
  if (!valid) throw new Error("Release manifest signature is invalid.");
  return {
    schemaVersion: manifest.schemaVersion,
    releaseSequence: manifest.releaseSequence,
    artifacts: manifest.artifacts,
  };
}

/**
 * Read one artifact only after validating publisher signature, target metadata,
 * byte length, and SHA-256 digest. Missing artifacts remain a normal null result;
 * malformed or untrusted release state fails closed.
 */
export function readVerifiedAgentArtifact(
  platform: InstallerPlatform,
  architecture: InstallerArchitecture,
): Buffer | null {
  const binary = readOptionalFile(artifactPath(platform, architecture));
  if (binary === null) return null;

  const directory = artifactDirectory();
  const manifest = readManifest(directory);
  const publisherPublicKey = readPublisherPublicKey(directory);
  const payload = verifyReleaseManifest(manifest, publisherPublicKey);
  const metadata = payload.artifacts.find(
    (artifact) => artifact.platform === platform && artifact.architecture === architecture,
  );
  if (!metadata)
    throw new Error(`Release manifest has no artifact for ${platform}/${architecture}.`);
  if (metadata.size !== binary.byteLength)
    throw new Error(`Agent artifact size does not match its signed release metadata.`);
  if (sha256Hex(binary) !== metadata.sha256)
    throw new Error(`Agent artifact digest does not match its signed release metadata.`);
  if (metadata.sequence > payload.releaseSequence)
    throw new Error("Agent artifact sequence exceeds release manifest sequence.");
  return binary;
}

export function readReleaseManifest(): SignedReleaseManifest {
  return readManifest(artifactDirectory());
}

export function readVerifiedReleaseManifest(): SignedReleaseManifest {
  const directory = artifactDirectory();
  const manifest = readManifest(directory);
  verifyReleaseManifest(manifest, readPublisherPublicKey(directory));
  return manifest;
}

export function artifactDirectory(): string {
  return process.env.SCOUT_AGENT_ARTIFACT_DIR ?? path.join(process.cwd(), ARTIFACT_DIRECTORY);
}

export function releaseManifestPath(directory = artifactDirectory()): string {
  return path.join(directory, MANIFEST_FILENAME);
}

export function publisherPublicKeyPath(directory = artifactDirectory()): string {
  return path.join(directory, PUBLISHER_KEY_FILENAME);
}

function readManifest(directory: string): SignedReleaseManifest {
  const raw = fs.readFileSync(releaseManifestPath(directory), "utf8");
  return releaseManifestInput.parse(JSON.parse(raw));
}

export function readPublisherPublicKey(directory = artifactDirectory()): string {
  const configured = process.env.SCOUT_RELEASE_PUBLISHER_PUBLIC_KEY?.trim();
  const encoded = configured || fs.readFileSync(publisherPublicKeyPath(directory), "utf8").trim();
  publicKeyFromRaw(encoded);
  return encoded;
}

function publicKeyFromRaw(encoded: string): KeyObject {
  const raw = Buffer.from(encoded, "base64url");
  if (raw.length !== 32) throw new Error("Release publisher public key has an invalid length.");
  return createPublicKey({
    key: Buffer.concat([RAW_ED25519_PUBLIC_KEY_PREFIX, raw]),
    format: "der",
    type: "spki",
  });
}

function readOptionalFile(filename: string): Buffer | null {
  try {
    return fs.readFileSync(filename);
  } catch (error) {
    if (error instanceof Error && (error as NodeJS.ErrnoException).code === "ENOENT") return null;
    throw error;
  }
}

export function artifactFilename(
  platform: InstallerPlatform,
  architecture: InstallerArchitecture,
): string {
  return artifactName(platform, architecture);
}
