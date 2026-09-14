import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";

const artifactDirectory = process.env.SCOUT_AGENT_ARTIFACT_DIR || "agent-artifacts";
const platform = process.env.SCOUT_AGENT_PLATFORM;
const architecture = process.env.SCOUT_AGENT_ARCHITECTURE;
const version = process.env.SCOUT_AGENT_VERSION || "0.1.0";
const releaseSequence = Number(process.env.SCOUT_RELEASE_SEQUENCE || "1");
const minimumProtocol = Number(process.env.SCOUT_AGENT_MINIMUM_PROTOCOL || "1");

if (!platform || !architecture)
  fail("SCOUT_AGENT_PLATFORM and SCOUT_AGENT_ARCHITECTURE are required");
if (!Number.isSafeInteger(releaseSequence) || releaseSequence < 1)
  fail("SCOUT_RELEASE_SEQUENCE must be a positive integer");
if (!Number.isSafeInteger(minimumProtocol) || minimumProtocol < 1)
  fail("SCOUT_AGENT_MINIMUM_PROTOCOL must be a positive integer");

const artifactFilename = `scout-agent-${platform}-${architecture}`;
const artifactPath = path.join(artifactDirectory, artifactFilename);
if (!fs.existsSync(artifactPath)) fail(`agent artifact not found: ${artifactPath}`);

const signingKey = loadSigningKey();
const publicKey = crypto.createPublicKey(signingKey).export({ format: "der", type: "spki" });
const publicKeyRaw = publicKey.subarray(-32).toString("base64url");
const existing = loadExistingManifest(signingKey);
const artifact = fs.readFileSync(artifactPath);
const artifacts = existing.filter(
  (entry) => !(entry.platform === platform && entry.architecture === architecture),
);
artifacts.push({
  platform,
  architecture,
  version,
  sequence: releaseSequence,
  sha256: crypto.createHash("sha256").update(artifact).digest("hex"),
  size: artifact.byteLength,
  minimumProtocol,
});
const payload = {
  schemaVersion: 1,
  releaseSequence,
  artifacts: artifacts.sort((left, right) =>
    `${left.platform}/${left.architecture}`.localeCompare(
      `${right.platform}/${right.architecture}`,
    ),
  ),
};
const signature = crypto
  .sign(null, Buffer.from(JSON.stringify(payload), "utf8"), signingKey)
  .toString("base64url");

fs.mkdirSync(artifactDirectory, { recursive: true, mode: 0o755 });
writeAtomically(path.join(artifactDirectory, "publisher-public.key"), `${publicKeyRaw}\n`, 0o644);
writeAtomically(
  path.join(artifactDirectory, "release-manifest.json"),
  `${JSON.stringify({ ...payload, signature })}\n`,
  0o644,
);

function loadSigningKey() {
  const configured = process.env.SCOUT_RELEASE_SIGNING_KEY?.trim();
  if (configured) {
    const seed = Buffer.from(configured, "base64url");
    if (seed.length !== 32) fail("SCOUT_RELEASE_SIGNING_KEY must contain a base64url Ed25519 seed");
    return crypto.createPrivateKey({
      key: Buffer.concat([Buffer.from("302e020100300506032b657004220420", "hex"), seed]),
      format: "der",
      type: "pkcs8",
    });
  }
  return crypto.generateKeyPairSync("ed25519").privateKey;
}

function loadExistingManifest(signingKey) {
  if (!process.env.SCOUT_RELEASE_SIGNING_KEY) return [];
  const filename = path.join(artifactDirectory, "release-manifest.json");
  if (!fs.existsSync(filename)) return [];
  try {
    const manifest = JSON.parse(fs.readFileSync(filename, "utf8"));
    const publicKey = crypto.createPublicKey(signingKey);
    const publicKeyRaw = publicKey.export({ format: "der", type: "spki" }).subarray(-32);
    const signedBytes = Buffer.from(
      JSON.stringify({
        schemaVersion: manifest.schemaVersion,
        releaseSequence: manifest.releaseSequence,
        artifacts: manifest.artifacts,
      }),
      "utf8",
    );
    const valid = crypto.verify(
      null,
      signedBytes,
      publicKey,
      Buffer.from(manifest.signature, "base64url"),
    );
    if (
      valid &&
      manifest.schemaVersion === 1 &&
      manifest.releaseSequence === releaseSequence &&
      fs.readFileSync(path.join(artifactDirectory, "publisher-public.key"), "utf8").trim() ===
        publicKeyRaw.toString("base64url")
    ) {
      return Array.isArray(manifest.artifacts) ? manifest.artifacts : [];
    }
  } catch {
    // A fresh build replaces an invalid local manifest.
  }
  return [];
}

function writeAtomically(filename, value, mode) {
  const temporary = `${filename}.tmp-${process.pid}`;
  fs.writeFileSync(temporary, value, { mode });
  fs.renameSync(temporary, filename);
  fs.chmodSync(filename, mode);
}

function fail(message) {
  console.error(`scout release manifest: ${message}`);
  process.exit(1);
}
