use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use ed25519_dalek::{Signer, SigningKey};
use rand::rngs::OsRng;
use std::fs;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;

use scout_agent::release::{
    release_manifest_message, ReleaseArtifact, ReleaseManifestPayload, SignedReleaseManifest,
};
use scout_agent::runtime::{run, AgentConfig};

#[tokio::test]
async fn an_enrolled_agent_starts_and_buffers_telemetry_when_scout_is_unavailable() {
    let directory = tempfile::tempdir().expect("temporary agent directory");
    fs::write(
        directory.path().join("enrollment.json"),
        r#"{
          "serverUrl": "http://127.0.0.1:1",
          "agentId": "agent-1",
          "systemId": "system-1",
          "controlPublicKey": ""
        }"#,
    )
    .expect("write enrollment state");

    let result = run(AgentConfig {
        server_url: "http://127.0.0.1:1".to_string(),
        invitation: None,
        publisher_public_key: None,
        data_dir: directory.path().to_path_buf(),
        once: true,
        version: "0.1.0".to_string(),
    })
    .await;

    assert!(
        result.is_ok(),
        "offline startup should not fail: {result:?}"
    );
    let queued = fs::read_to_string(directory.path().join("telemetry.queue"))
        .expect("offline telemetry should be buffered");
    assert!(!queued.trim().is_empty());
}

#[tokio::test]
async fn an_enrolled_agent_applies_a_verified_release_before_reporting_telemetry(
) -> anyhow::Result<()> {
    let directory = tempfile::tempdir().expect("temporary agent directory");
    let signing_key = SigningKey::generate(&mut OsRng);
    let artifact = b"updated-agent";
    let platform = if cfg!(target_os = "macos") {
        "macos"
    } else {
        "linux"
    };
    let architecture = if cfg!(target_arch = "aarch64") {
        "aarch64"
    } else {
        "x86_64"
    };
    let payload = ReleaseManifestPayload {
        schema_version: 1,
        release_sequence: 2,
        artifacts: vec![ReleaseArtifact {
            platform: platform.to_string(),
            architecture: architecture.to_string(),
            version: "0.2.0".to_string(),
            sequence: 2,
            sha256: sha256_hex(artifact),
            size: artifact.len() as u64,
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
    .expect("serialize release manifest");
    let publisher_public_key = URL_SAFE_NO_PAD.encode(signing_key.verifying_key().to_bytes());
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind test server");
    let server_address = listener.local_addr().expect("test server address");
    let server = tokio::spawn(async move {
        let mut paths = Vec::new();
        for _ in 0..5 {
            let (mut stream, _) = listener.accept().await?;
            let request = read_http_request(&mut stream).await?;
            let request_line = request.lines().next().unwrap_or_default();
            let mut parts = request_line.split_whitespace();
            let _method = parts.next().unwrap_or_default();
            let path = parts.next().unwrap_or_default().to_string();
            paths.push(path.clone());
            let (content_type, body) = match path.as_str() {
                "/api/agent/v1/enroll" => (
                    "application/json",
                    br#"{"agentId":"agent-1","systemId":"system-1","serverTime":"2026-09-14T00:00:00Z","heartbeatIntervalSeconds":15,"telemetryIntervalSeconds":30,"controlPublicKey":""}"#.to_vec(),
                ),
                "/api/agent/v1/heartbeat" => (
                    "application/json",
                    br#"{"ok":true,"serverTime":"2026-09-14T00:00:00Z","controlPublicKey":"","task":null}"#.to_vec(),
                ),
                "/api/agent/v1/releases/manifest" => {
                    ("application/json", manifest.clone())
                }
                path if path
                    == format!(
                        "/api/agent/v1/releases/artifact?platform={platform}&architecture={architecture}"
                    ) =>
                {
                    ("application/octet-stream", artifact.to_vec())
                }
                "/api/agent/v1/telemetry" => ("application/json", br#"{}"#.to_vec()),
                _ => ("text/plain", b"not found".to_vec()),
            };
            let status = if content_type == "text/plain" {
                "404 Not Found"
            } else {
                "200 OK"
            };
            let response = format!(
                "HTTP/1.1 {status}\r\nContent-Type: {content_type}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                body.len()
            );
            stream.write_all(response.as_bytes()).await?;
            stream.write_all(&body).await?;
        }
        Ok::<Vec<String>, std::io::Error>(paths)
    });

    run(AgentConfig {
        server_url: format!("http://{server_address}"),
        invitation: Some("i".repeat(64)),
        publisher_public_key: Some(publisher_public_key),
        data_dir: directory.path().to_path_buf(),
        once: true,
        version: "0.1.0".to_string(),
    })
    .await
    .expect("agent should apply the verified release");

    let paths = server
        .await
        .expect("test server task")
        .expect("test server");
    assert_eq!(
        paths,
        vec![
            "/api/agent/v1/enroll".to_string(),
            "/api/agent/v1/heartbeat".to_string(),
            "/api/agent/v1/releases/manifest".to_string(),
            format!(
                "/api/agent/v1/releases/artifact?platform={platform}&architecture={architecture}"
            ),
            "/api/agent/v1/telemetry".to_string(),
        ]
    );
    assert_eq!(
        fs::read(directory.path().join("current/scout-agent")).expect("active release"),
        artifact
    );
    let state: serde_json::Value = serde_json::from_str(&fs::read_to_string(
        directory.path().join("enrollment.json"),
    )?)?;
    assert_eq!(state["releaseSequence"], 2);
    Ok(())
}

async fn read_http_request(stream: &mut tokio::net::TcpStream) -> std::io::Result<String> {
    let mut bytes = Vec::new();
    let mut chunk = [0_u8; 4096];
    loop {
        let read = stream.read(&mut chunk).await?;
        if read == 0 {
            break;
        }
        bytes.extend_from_slice(&chunk[..read]);
        if bytes.windows(4).any(|window| window == b"\r\n\r\n") {
            break;
        }
    }
    Ok(String::from_utf8_lossy(&bytes).into_owned())
}

fn sha256_hex(value: &[u8]) -> String {
    use sha2::{Digest, Sha256};
    Sha256::digest(value)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
