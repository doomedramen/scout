use anyhow::{anyhow, Result};
use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use ed25519_dalek::{Signature, Verifier, VerifyingKey};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ReleaseArtifact {
    pub platform: String,
    pub architecture: String,
    pub version: String,
    pub sequence: u64,
    pub sha256: String,
    pub size: u64,
    pub minimum_protocol: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ReleaseManifestPayload {
    pub schema_version: u8,
    pub release_sequence: u64,
    pub artifacts: Vec<ReleaseArtifact>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SignedReleaseManifest {
    pub schema_version: u8,
    pub release_sequence: u64,
    pub artifacts: Vec<ReleaseArtifact>,
    pub signature: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct VerifiedRelease {
    pub version: String,
    pub sequence: u64,
    pub minimum_protocol: u64,
    pub sha256: String,
    pub size: u64,
}

pub fn release_manifest_message(payload: &ReleaseManifestPayload) -> String {
    let mut payload = payload.clone();
    payload.artifacts.sort_by(|left, right| {
        format!("{}/{}", left.platform, left.architecture)
            .cmp(&format!("{}/{}", right.platform, right.architecture))
    });
    serde_json::to_string(&payload).expect("release manifest contains serializable fields")
}

pub fn inspect_release_manifest(
    manifest_bytes: &[u8],
    publisher_public_key: &str,
    platform: &str,
    architecture: &str,
    current_sequence: u64,
) -> Result<Option<VerifiedRelease>> {
    let manifest: SignedReleaseManifest = serde_json::from_slice(manifest_bytes)
        .map_err(|error| anyhow!("decode release manifest: {error}"))?;
    if manifest.schema_version != 1 {
        return Err(anyhow!("release manifest schema is unsupported"));
    }
    let public_bytes = URL_SAFE_NO_PAD
        .decode(publisher_public_key)
        .map_err(|error| anyhow!("decode release publisher key: {error}"))?;
    let public_bytes: [u8; 32] = public_bytes
        .try_into()
        .map_err(|_| anyhow!("release publisher key has an invalid length"))?;
    let verifying_key = VerifyingKey::from_bytes(&public_bytes)
        .map_err(|error| anyhow!("release publisher key is invalid: {error}"))?;
    let signature_bytes = URL_SAFE_NO_PAD
        .decode(&manifest.signature)
        .map_err(|error| anyhow!("decode release signature: {error}"))?;
    let signature = Signature::from_slice(&signature_bytes)
        .map_err(|error| anyhow!("release signature is invalid: {error}"))?;
    let payload = ReleaseManifestPayload {
        schema_version: manifest.schema_version,
        release_sequence: manifest.release_sequence,
        artifacts: manifest.artifacts.clone(),
    };
    verifying_key
        .verify(release_manifest_message(&payload).as_bytes(), &signature)
        .map_err(|error| anyhow!("release manifest signature is invalid: {error}"))?;

    let metadata = manifest
        .artifacts
        .iter()
        .find(|entry| entry.platform == platform && entry.architecture == architecture)
        .ok_or_else(|| anyhow!("release has no artifact for {platform}/{architecture}"))?;
    if metadata.sequence > manifest.release_sequence {
        return Err(anyhow!("artifact sequence exceeds release sequence"));
    }
    if metadata.sequence <= current_sequence {
        return Ok(None);
    }
    if metadata.minimum_protocol > 1 {
        return Err(anyhow!("release requires an unsupported agent protocol"));
    }
    Ok(Some(VerifiedRelease {
        version: metadata.version.clone(),
        sequence: metadata.sequence,
        minimum_protocol: metadata.minimum_protocol,
        sha256: metadata.sha256.clone(),
        size: metadata.size,
    }))
}

pub fn verify_release_manifest(
    manifest_bytes: &[u8],
    publisher_public_key: &str,
    platform: &str,
    architecture: &str,
    current_sequence: u64,
    artifact: &[u8],
) -> Result<VerifiedRelease> {
    let verified = inspect_release_manifest(
        manifest_bytes,
        publisher_public_key,
        platform,
        architecture,
        current_sequence,
    )?
    .ok_or_else(|| anyhow!("release is not newer than installed sequence"))?;
    if verified.size != artifact.len() as u64 {
        return Err(anyhow!("release artifact size does not match manifest"));
    }
    if sha256_hex(artifact) != verified.sha256 {
        return Err(anyhow!("release artifact digest does not match manifest"));
    }
    Ok(verified)
}

fn sha256_hex(value: &[u8]) -> String {
    Sha256::digest(value)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
