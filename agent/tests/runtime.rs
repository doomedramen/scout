use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use chrono::{Duration, Utc};
use ed25519_dalek::{Signer, SigningKey};
use rand::rngs::OsRng;
use std::collections::HashMap;
use std::fs;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;

use scout_agent::protocol::{
    body_digest, task_message, HeartbeatResponse, RelayTaskPayload, TaskEnvelope, TaskPayload,
};
use scout_agent::release::{
    release_manifest_message, ReleaseArtifact, ReleaseManifestPayload, SignedReleaseManifest,
};
use scout_agent::runtime::{run, AgentConfig};

#[tokio::test]
async fn an_agent_bridges_a_signed_relay_task_without_buffering_the_target_connection(
) -> anyhow::Result<()> {
    let directory = tempfile::tempdir()?;
    let control_key = SigningKey::generate(&mut OsRng);
    let target_listener = TcpListener::bind("127.0.0.1:0").await?;
    let target_address = target_listener.local_addr()?;
    let target = tokio::spawn(async move {
        let (mut stream, _) = target_listener.accept().await?;
        stream.write_all(b"target-to-server").await?;
        let mut received = vec![0_u8; "server-to-target".len()];
        stream.read_exact(&mut received).await?;
        assert_eq!(received, b"server-to-target");
        stream.shutdown().await?;
        Ok::<(), std::io::Error>(())
    });

    let http_listener = TcpListener::bind("127.0.0.1:0").await?;
    let server_address = http_listener.local_addr()?;
    let relay_id = "11111111-1111-4111-8111-111111111111";
    let task = relay_task(relay_id, &control_key, target_address.port());
    let task_json = serde_json::to_vec(&HeartbeatResponse {
        ok: true,
        server_time: Utc::now().to_rfc3339(),
        control_public_key: URL_SAFE_NO_PAD.encode(control_key.verifying_key().to_bytes()),
        task: Some(task),
    })?;
    let server = tokio::spawn(async move {
        let mut handlers = Vec::new();
        for _ in 0..7 {
            let (mut stream, _) = http_listener.accept().await?;
            let task_json = task_json.clone();
            handlers.push(tokio::spawn(async move {
                let request = read_full_http_request(&mut stream).await?;
                assert!(request.headers.contains_key("x-scout-signature"));
                match request.path.as_str() {
                    "/api/agent/v1/heartbeat" => {
                        write_http_response(&mut stream, "application/json", &task_json).await?;
                    }
                    "/api/agent/v1/telemetry" => {
                        write_http_response(&mut stream, "application/json", b"{}").await?;
                    }
                    "/api/agent/v1/relay/open" => {
                        write_http_response(&mut stream, "application/json", b"{}").await?;
                    }
                    path if path.ends_with("/downstream") => {
                        write_http_response(
                            &mut stream,
                            "application/octet-stream",
                            b"server-to-target",
                        )
                        .await?;
                    }
                    path if path.ends_with("/upstream") => {
                        assert_eq!(request.body, b"target-to-server");
                        write_http_response(&mut stream, "application/json", b"{}").await?;
                    }
                    path if path.ends_with("/result") => {
                        let result: serde_json::Value = serde_json::from_slice(&request.body)?;
                        assert_eq!(result["kind"], "relay-connect");
                        write_http_response(&mut stream, "application/json", b"{}").await?;
                    }
                    path => panic!("unexpected relay request: {path}"),
                }
                Ok::<String, anyhow::Error>(request.path)
            }));
        }
        let mut paths = Vec::new();
        for handler in handlers {
            paths.push(handler.await??);
        }
        Ok::<Vec<String>, anyhow::Error>(paths)
    });

    fs::write(
        directory.path().join("enrollment.json"),
        serde_json::json!({
            "serverUrl": format!("http://{server_address}"),
            "agentId": "agent-1",
            "systemId": "scanner-system",
            "controlPublicKey": URL_SAFE_NO_PAD.encode(control_key.verifying_key().to_bytes())
        })
        .to_string(),
    )?;

    run(AgentConfig {
        server_url: format!("http://{server_address}"),
        invitation: None,
        publisher_public_key: None,
        data_dir: directory.path().to_path_buf(),
        once: true,
        version: "0.1.0".to_string(),
    })
    .await?;

    let paths = server.await??;
    target.await??;
    assert_eq!(paths[0], "/api/agent/v1/heartbeat");
    assert!(paths.iter().any(|path| path.ends_with("/upstream")));
    assert!(paths.iter().any(|path| path.ends_with("/downstream")));
    assert!(paths.iter().any(|path| path.ends_with("/result")));
    let enrollment: serde_json::Value = serde_json::from_str(&fs::read_to_string(
        directory.path().join("enrollment.json"),
    )?)?;
    assert_eq!(enrollment["taskGeneration"], 1);
    Ok(())
}

#[tokio::test]
async fn an_invalid_task_does_not_advance_the_persisted_task_watermark() -> anyhow::Result<()> {
    let directory = tempfile::tempdir()?;
    let control_key = SigningKey::generate(&mut OsRng);
    let listener = TcpListener::bind("127.0.0.1:0").await?;
    let server_address = listener.local_addr()?;
    let mut task = relay_task("33333333-3333-4333-8333-333333333333", &control_key, 22);
    task.signature = "not-a-valid-signature".to_string();
    let heartbeat = serde_json::to_vec(&HeartbeatResponse {
        ok: true,
        server_time: Utc::now().to_rfc3339(),
        control_public_key: URL_SAFE_NO_PAD.encode(control_key.verifying_key().to_bytes()),
        task: Some(task),
    })?;

    let server = tokio::spawn(async move {
        for _ in 0..2 {
            let (mut stream, _) = listener.accept().await?;
            let request = read_full_http_request(&mut stream).await?;
            let body = if request.path == "/api/agent/v1/heartbeat" {
                heartbeat.clone()
            } else {
                br#"{}"#.to_vec()
            };
            write_http_response(&mut stream, "application/json", &body).await?;
        }
        Ok::<(), anyhow::Error>(())
    });

    fs::write(
        directory.path().join("enrollment.json"),
        serde_json::json!({
            "serverUrl": format!("http://{server_address}"),
            "agentId": "agent-1",
            "systemId": "system-1",
            "controlPublicKey": URL_SAFE_NO_PAD.encode(control_key.verifying_key().to_bytes()),
            "taskGeneration": 0
        })
        .to_string(),
    )?;

    run(AgentConfig {
        server_url: format!("http://{server_address}"),
        invitation: None,
        publisher_public_key: None,
        data_dir: directory.path().to_path_buf(),
        once: true,
        version: "0.1.0".to_string(),
    })
    .await?;

    server.await??;
    let enrollment: serde_json::Value = serde_json::from_str(&fs::read_to_string(
        directory.path().join("enrollment.json"),
    )?)?;
    assert_eq!(enrollment["taskGeneration"], 0);
    Ok(())
}

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

#[tokio::test]
async fn an_enrolled_agent_does_not_download_an_already_installed_release() -> anyhow::Result<()> {
    let directory = tempfile::tempdir().expect("temporary agent directory");
    let signing_key = SigningKey::generate(&mut OsRng);
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
    let artifact = b"already-installed-agent";
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
    })?;
    let publisher_public_key = URL_SAFE_NO_PAD.encode(signing_key.verifying_key().to_bytes());
    let listener = TcpListener::bind("127.0.0.1:0").await?;
    let server_address = listener.local_addr()?;
    let server = tokio::spawn(async move {
        let mut paths = Vec::new();
        for _ in 0..3 {
            let (mut stream, _) = listener.accept().await?;
            let request = read_http_request(&mut stream).await?;
            let path = request
                .lines()
                .next()
                .and_then(|line| line.split_whitespace().nth(1))
                .unwrap_or_default()
                .to_string();
            paths.push(path.clone());
            let (content_type, body) = match path.as_str() {
                "/api/agent/v1/heartbeat" => (
                    "application/json",
                    br#"{"ok":true,"serverTime":"2026-09-14T00:00:00Z","controlPublicKey":"","task":null}"#.to_vec(),
                ),
                "/api/agent/v1/releases/manifest" => ("application/json", manifest.clone()),
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

    fs::write(
        directory.path().join("enrollment.json"),
        serde_json::json!({
            "serverUrl": format!("http://{server_address}"),
            "agentId": "agent-1",
            "systemId": "system-1",
            "controlPublicKey": "",
            "publisherPublicKey": publisher_public_key,
            "releaseSequence": 2
        })
        .to_string(),
    )?;

    run(AgentConfig {
        server_url: format!("http://{server_address}"),
        invitation: None,
        publisher_public_key: Some(publisher_public_key),
        data_dir: directory.path().to_path_buf(),
        once: true,
        version: "0.2.0".to_string(),
    })
    .await?;

    let paths = server.await??;
    assert_eq!(
        paths,
        vec![
            "/api/agent/v1/heartbeat".to_string(),
            "/api/agent/v1/releases/manifest".to_string(),
            "/api/agent/v1/telemetry".to_string(),
        ]
    );
    assert!(!directory.path().join("current/scout-agent").exists());
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

fn relay_task(relay_id: &str, control_key: &SigningKey, target_port: u16) -> TaskEnvelope {
    let now = Utc::now();
    let payload = TaskPayload::Relay(RelayTaskPayload {
        cidr: "127.0.0.0/8".to_string(),
        port: target_port,
        addresses: vec!["127.0.0.1".to_string()],
        relay_id: relay_id.to_string(),
        target_system_id: "target-system".to_string(),
        target_address: "127.0.0.1".to_string(),
        nonce: "n".repeat(43),
        expires_at: (now + Duration::minutes(2)).to_rfc3339(),
    });
    let payload_digest = body_digest(&serde_json::to_string(&payload).unwrap());
    let mut task = TaskEnvelope {
        task_id: "22222222-2222-4222-8222-222222222222".to_string(),
        agent_id: "agent-1".to_string(),
        kind: "relay-connect".to_string(),
        generation: 1,
        policy_version: 1,
        payload_digest,
        issued_at: now.to_rfc3339(),
        expires_at: (now + Duration::minutes(2)).to_rfc3339(),
        deadline_at: (now + Duration::minutes(1)).to_rfc3339(),
        payload,
        signature: String::new(),
    };
    task.signature =
        URL_SAFE_NO_PAD.encode(control_key.sign(task_message(&task).as_bytes()).to_bytes());
    task
}

#[derive(Debug)]
struct HttpRequest {
    path: String,
    headers: HashMap<String, String>,
    body: Vec<u8>,
}

async fn read_full_http_request(
    stream: &mut tokio::net::TcpStream,
) -> std::io::Result<HttpRequest> {
    let mut bytes = Vec::new();
    let mut chunk = [0_u8; 4096];
    let header_end = loop {
        if let Some(position) = bytes.windows(4).position(|window| window == b"\r\n\r\n") {
            break position + 4;
        }
        let read = stream.read(&mut chunk).await?;
        if read == 0 {
            return Err(std::io::Error::new(
                std::io::ErrorKind::UnexpectedEof,
                "HTTP headers ended unexpectedly",
            ));
        }
        bytes.extend_from_slice(&chunk[..read]);
    };
    let header_text = String::from_utf8_lossy(&bytes[..header_end]);
    let mut lines = header_text.split("\r\n");
    let path = lines
        .next()
        .and_then(|line| line.split_whitespace().nth(1))
        .unwrap_or_default()
        .to_string();
    let headers = lines
        .filter_map(|line| line.split_once(':'))
        .map(|(name, value)| (name.to_ascii_lowercase(), value.trim().to_string()))
        .collect::<HashMap<_, _>>();
    let mut body = bytes[header_end..].to_vec();
    if headers
        .get("transfer-encoding")
        .is_some_and(|value| value.eq_ignore_ascii_case("chunked"))
    {
        let mut decoded = Vec::new();
        let mut cursor = 0;
        loop {
            let line_end = loop {
                if let Some(position) = body[cursor..]
                    .windows(2)
                    .position(|window| window == b"\r\n")
                {
                    break cursor + position;
                }
                let read = stream.read(&mut chunk).await?;
                if read == 0 {
                    return Err(std::io::Error::new(
                        std::io::ErrorKind::UnexpectedEof,
                        "chunked HTTP body ended unexpectedly",
                    ));
                }
                body.extend_from_slice(&chunk[..read]);
            };
            let size = usize::from_str_radix(
                String::from_utf8_lossy(&body[cursor..line_end])
                    .split(';')
                    .next()
                    .unwrap_or_default()
                    .trim(),
                16,
            )
            .map_err(|_| std::io::Error::new(std::io::ErrorKind::InvalidData, "invalid chunk"))?;
            cursor = line_end + 2;
            while body.len() < cursor + size + 2 {
                let read = stream.read(&mut chunk).await?;
                if read == 0 {
                    return Err(std::io::Error::new(
                        std::io::ErrorKind::UnexpectedEof,
                        "chunked HTTP data ended unexpectedly",
                    ));
                }
                body.extend_from_slice(&chunk[..read]);
            }
            if size == 0 {
                break;
            }
            decoded.extend_from_slice(&body[cursor..cursor + size]);
            cursor += size + 2;
        }
        body = decoded;
    } else if let Some(length) = headers.get("content-length") {
        let length = length.parse::<usize>().map_err(|_| {
            std::io::Error::new(std::io::ErrorKind::InvalidData, "invalid content length")
        })?;
        while body.len() < length {
            let read = stream.read(&mut chunk).await?;
            if read == 0 {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::UnexpectedEof,
                    "HTTP body ended unexpectedly",
                ));
            }
            body.extend_from_slice(&chunk[..read]);
        }
        body.truncate(length);
    } else {
        body.clear();
    }
    Ok(HttpRequest {
        path,
        headers,
        body,
    })
}

async fn write_http_response(
    stream: &mut tokio::net::TcpStream,
    content_type: &str,
    body: &[u8],
) -> std::io::Result<()> {
    let headers = format!(
        "HTTP/1.1 200 OK\r\nContent-Type: {content_type}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
        body.len()
    );
    stream.write_all(headers.as_bytes()).await?;
    stream.write_all(body).await
}

fn sha256_hex(value: &[u8]) -> String {
    use sha2::{Digest, Sha256};
    Sha256::digest(value)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
