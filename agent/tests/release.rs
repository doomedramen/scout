use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use ed25519_dalek::{Signer, SigningKey};
use rand::rngs::OsRng;
use scout_agent::release::{
    release_manifest_message, verify_release_manifest, ReleaseArtifact, ReleaseManifestPayload,
    SignedReleaseManifest,
};

#[test]
fn signed_release_requires_matching_platform_bytes_and_newer_sequence() {
    let signing_key = SigningKey::generate(&mut OsRng);
    let artifact = b"agent-release";
    let payload = ReleaseManifestPayload {
        schema_version: 1,
        release_sequence: 3,
        artifacts: vec![ReleaseArtifact {
            platform: "linux".to_string(),
            architecture: "x86_64".to_string(),
            version: "0.1.0".to_string(),
            sequence: 3,
            sha256: sha256_hex(artifact),
            size: artifact.len() as u64,
            minimum_protocol: 1,
        }],
    };
    let signature = URL_SAFE_NO_PAD.encode(
        signing_key
            .sign(release_manifest_message(&payload).as_bytes())
            .to_bytes(),
    );
    let manifest = serde_json::to_vec(&SignedReleaseManifest {
        schema_version: payload.schema_version,
        release_sequence: payload.release_sequence,
        artifacts: payload.artifacts.clone(),
        signature,
    })
    .unwrap();
    let public_key = URL_SAFE_NO_PAD.encode(signing_key.verifying_key().to_bytes());

    let verified =
        verify_release_manifest(&manifest, &public_key, "linux", "x86_64", 2, artifact).unwrap();
    assert_eq!(verified.version, "0.1.0");
    assert_eq!(verified.sequence, 3);
    assert!(
        verify_release_manifest(&manifest, &public_key, "macos", "x86_64", 2, artifact).is_err()
    );
    assert!(
        verify_release_manifest(&manifest, &public_key, "linux", "x86_64", 3, artifact).is_err()
    );
}

#[test]
fn release_verification_rejects_tampering_and_wrong_digest() {
    let signing_key = SigningKey::generate(&mut OsRng);
    let payload = ReleaseManifestPayload {
        schema_version: 1,
        release_sequence: 1,
        artifacts: vec![ReleaseArtifact {
            platform: "linux".to_string(),
            architecture: "aarch64".to_string(),
            version: "0.1.0".to_string(),
            sequence: 1,
            sha256: sha256_hex(b"expected"),
            size: 8,
            minimum_protocol: 1,
        }],
    };
    let manifest = serde_json::to_vec(&SignedReleaseManifest {
        schema_version: payload.schema_version,
        release_sequence: payload.release_sequence,
        artifacts: payload.artifacts.clone(),
        signature: URL_SAFE_NO_PAD.encode(
            signing_key
                .sign(release_manifest_message(&payload).as_bytes())
                .to_bytes(),
        ),
    })
    .unwrap();
    let public_key = URL_SAFE_NO_PAD.encode(signing_key.verifying_key().to_bytes());

    assert!(
        verify_release_manifest(&manifest, &public_key, "linux", "aarch64", 0, b"changed").is_err()
    );
    let mut tampered = manifest;
    tampered[0] = b'X';
    assert!(
        verify_release_manifest(&tampered, &public_key, "linux", "aarch64", 0, b"expected")
            .is_err()
    );
}

fn sha256_hex(value: &[u8]) -> String {
    use sha2::{Digest, Sha256};
    Sha256::digest(value)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
