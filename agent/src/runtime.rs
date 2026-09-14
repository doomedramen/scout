use anyhow::{anyhow, Context, Result};
use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use chrono::{SecondsFormat, Utc};
use futures_util::StreamExt;
use reqwest::header::{HeaderMap, HeaderValue};
use reqwest::{Body, Client, Method, StatusCode};
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;
use tokio::sync::{mpsc, Semaphore};
use uuid::Uuid;

use crate::buffer::TelemetryBuffer;
use crate::collector::{HostCollector, SysinfoHostCollector};
use crate::identity::{load_or_create, Identity};
use crate::platform::{FilesystemUpdatePlatform, UpdatePlatform};
use crate::protocol::{
    enrollment_message, sign_request, verify_task, EnrollmentRequest, EnrollmentResponse,
    HeartbeatPayload, HeartbeatResponse, RelayTaskPayload, RelayTaskResult, ScanResult,
    ScanTaskPayload, ScanTaskResult, TaskEnvelope, TaskPayload, TelemetryPayload,
};
use crate::release::{inspect_release_manifest, verify_release_manifest};

const HEARTBEAT_INTERVAL: Duration = Duration::from_secs(15);
const TELEMETRY_INTERVAL: Duration = Duration::from_secs(30);

#[derive(Debug, Clone)]
pub struct AgentConfig {
    pub server_url: String,
    pub invitation: Option<String>,
    pub publisher_public_key: Option<String>,
    pub data_dir: PathBuf,
    pub once: bool,
    pub version: String,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
#[serde(rename_all = "camelCase")]
struct EnrollmentState {
    server_url: String,
    agent_id: String,
    system_id: String,
    #[serde(default)]
    control_public_key: Option<String>,
    #[serde(default)]
    publisher_public_key: Option<String>,
    #[serde(default)]
    task_generation: u64,
    #[serde(default)]
    release_sequence: u64,
}

impl AgentConfig {
    pub fn from_environment_and_args<I, S>(args: I) -> Result<Self>
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        let mut server_url = std::env::var("SCOUT_SERVER_URL").ok();
        let mut invitation = std::env::var("SCOUT_INVITATION").ok();
        let publisher_public_key = configured_publisher_public_key();
        let mut data_dir = std::env::var("SCOUT_DATA_DIR")
            .map(PathBuf::from)
            .unwrap_or_else(|_| PathBuf::from("/var/lib/scout-agent"));
        let mut once = false;
        let mut values = args.into_iter().map(Into::into).peekable();
        let _ = values.next();
        while let Some(argument) = values.next() {
            match argument.as_str() {
                "--server" => server_url = values.next(),
                "--invitation" => invitation = values.next(),
                "--data-dir" => {
                    data_dir = PathBuf::from(
                        values
                            .next()
                            .ok_or_else(|| anyhow!("--data-dir requires a value"))?,
                    )
                }
                "--once" => once = true,
                "--help" | "-h" => {
                    return Err(anyhow!(usage()));
                }
                unknown => return Err(anyhow!("unknown argument: {unknown}\n\n{}", usage())),
            }
        }
        let server_url = server_url
            .filter(|value| !value.trim().is_empty())
            .ok_or_else(|| anyhow!("--server or SCOUT_SERVER_URL is required"))?;
        Ok(Self {
            server_url: server_url.trim_end_matches('/').to_string(),
            invitation,
            publisher_public_key,
            data_dir,
            once,
            version: env!("CARGO_PKG_VERSION").to_string(),
        })
    }
}

pub async fn run(config: AgentConfig) -> Result<()> {
    let identity_path = config.data_dir.join("identity.json");
    let state_path = config.data_dir.join("enrollment.json");
    let identity = load_or_create(&identity_path).context("load agent identity")?;
    let client = Client::builder()
        .connect_timeout(Duration::from_secs(5))
        .timeout(Duration::from_secs(15))
        .build()
        .context("build HTTP client")?;
    let enrollment =
        load_enrollment(&state_path).filter(|state| state.server_url == config.server_url);
    let mut enrollment = match enrollment {
        Some(state) => state,
        None => {
            let invitation = config.invitation.as_deref().ok_or_else(|| {
                anyhow!("SCOUT_INVITATION or --invitation is required for first enrollment")
            })?;
            let state = enroll(&client, &identity, &config, invitation).await?;
            save_enrollment(&state_path, &state).context("save enrollment")?;
            state
        }
    };
    let configured_publisher_key = config.publisher_public_key.clone();
    if let Some(configured) = configured_publisher_key {
        if let Some(persisted) = enrollment.publisher_public_key.as_deref() {
            if persisted != configured {
                return Err(anyhow!(
                    "release publisher key changed after agent enrollment"
                ));
            }
        } else {
            enrollment.publisher_public_key = Some(configured);
            save_enrollment(&state_path, &enrollment).context("save release publisher key")?;
        }
    }

    let mut collector = SysinfoHostCollector::new();
    let telemetry_buffer = TelemetryBuffer::new(config.data_dir.join("telemetry.queue"));
    let mut dropped_samples = 0;
    let response =
        match send_heartbeat(&client, &identity, &enrollment, enrollment.release_sequence).await {
            Ok(response) => {
                apply_heartbeat_response(&state_path, &mut enrollment, &response)?;
                FilesystemUpdatePlatform::new(&config.data_dir)
                    .mark_healthy()
                    .map_err(|error| anyhow!("mark agent release healthy: {error}"))?;
                Some(response)
            }
            Err(error) => {
                eprintln!("scout-agent heartbeat failed: {error:#}");
                None
            }
        };
    let update_applied = if response.is_some() {
        match check_for_update(
            &client,
            &identity,
            &mut enrollment,
            &state_path,
            &config.data_dir,
        )
        .await
        {
            Ok(applied) => applied,
            Err(error) => {
                eprintln!("scout-agent release check failed: {error:#}");
                false
            }
        }
    } else {
        false
    };
    if let Err(error) = send_telemetry(
        &client,
        &identity,
        &enrollment,
        &mut collector,
        &telemetry_buffer,
        dropped_samples,
    )
    .await
    {
        dropped_samples = dropped_samples.saturating_add(1);
        eprintln!("scout-agent telemetry failed: {error:#}");
    }
    if let Some(task) = response.and_then(|response| response.task) {
        if let Err(error) =
            process_task(&client, &identity, &mut enrollment, &state_path, task).await
        {
            eprintln!("scout-agent task failed: {error:#}");
        }
    }
    if config.once || update_applied {
        return Ok(());
    }

    let mut heartbeat = tokio::time::interval(HEARTBEAT_INTERVAL);
    let mut telemetry = tokio::time::interval(TELEMETRY_INTERVAL);
    heartbeat.tick().await;
    telemetry.tick().await;
    loop {
        tokio::select! {
            _ = tokio::signal::ctrl_c() => return Ok(()),
            _ = heartbeat.tick() => {
                match send_heartbeat(&client, &identity, &enrollment, enrollment.release_sequence).await {
                    Ok(response) => {
                        if let Err(error) = apply_heartbeat_response(&state_path, &mut enrollment, &response) {
                            eprintln!("scout-agent state save failed: {error:#}");
                        }
                        if let Err(error) = FilesystemUpdatePlatform::new(&config.data_dir).mark_healthy() {
                            eprintln!("scout-agent release health save failed: {error}");
                        }
                        if let Some(task) = response.task {
                            if let Err(error) = process_task(&client, &identity, &mut enrollment, &state_path, task).await {
                                eprintln!("scout-agent task failed: {error:#}");
                            }
                        }
                        match check_for_update(&client, &identity, &mut enrollment, &state_path, &config.data_dir).await {
                            Ok(true) => return Ok(()),
                            Ok(false) => {}
                            Err(error) => eprintln!("scout-agent release check failed: {error:#}"),
                        }
                    }
                    Err(error) => eprintln!("scout-agent heartbeat failed: {error:#}"),
                }
            }
            _ = telemetry.tick() => {
                if let Err(error) = send_telemetry(&client, &identity, &enrollment, &mut collector, &telemetry_buffer, dropped_samples).await {
                    dropped_samples = dropped_samples.saturating_add(1);
                    eprintln!("scout-agent telemetry failed: {error:#}");
                } else {
                    dropped_samples = 0;
                }
            }
        }
    }
}

async fn enroll(
    client: &Client,
    identity: &Identity,
    config: &AgentConfig,
    invitation: &str,
) -> Result<EnrollmentState> {
    let request = EnrollmentRequest {
        invitation: invitation.to_string(),
        public_key: identity.public_key(),
        platform: platform_name().to_string(),
        architecture: architecture_name().to_string(),
        version: config.version.clone(),
        proof: URL_SAFE_NO_PAD.encode(
            identity.sign(
                enrollment_message(
                    invitation,
                    &identity.public_key(),
                    platform_name(),
                    architecture_name(),
                    &config.version,
                )
                .as_bytes(),
            ),
        ),
    };
    let response: EnrollmentResponse = client
        .post(format!("{}/api/agent/v1/enroll", config.server_url))
        .json(&request)
        .send()
        .await
        .context("enroll agent")?
        .error_for_status()
        .context("agent enrollment was rejected")?
        .json()
        .await
        .context("decode enrollment response")?;
    Ok(EnrollmentState {
        server_url: config.server_url.clone(),
        agent_id: response.agent_id,
        system_id: response.system_id,
        control_public_key: Some(response.control_public_key),
        publisher_public_key: config.publisher_public_key.clone(),
        task_generation: 0,
        release_sequence: 0,
    })
}

async fn check_for_update(
    client: &Client,
    identity: &Identity,
    enrollment: &mut EnrollmentState,
    state_path: &std::path::Path,
    data_dir: &std::path::Path,
) -> Result<bool> {
    let Some(publisher_public_key) = enrollment.publisher_public_key.as_deref() else {
        return Ok(false);
    };
    let manifest_path = "/api/agent/v1/releases/manifest";
    let manifest = send_signed_get(
        client,
        identity,
        &enrollment.server_url,
        &enrollment.agent_id,
        manifest_path,
    )
    .await?
    .bytes()
    .await
    .context("read signed release manifest")?;
    let Some(_) = inspect_release_manifest(
        &manifest,
        publisher_public_key,
        platform_name(),
        architecture_name(),
        enrollment.release_sequence,
    )?
    else {
        return Ok(false);
    };
    let artifact_path = format!(
        "/api/agent/v1/releases/artifact?platform={}&architecture={}",
        platform_name(),
        architecture_name()
    );
    let artifact = send_signed_get(
        client,
        identity,
        &enrollment.server_url,
        &enrollment.agent_id,
        &artifact_path,
    )
    .await?
    .bytes()
    .await
    .context("read signed agent artifact")?;
    let verified = verify_release_manifest(
        &manifest,
        publisher_public_key,
        platform_name(),
        architecture_name(),
        enrollment.release_sequence,
        &artifact,
    )?;
    let updater = FilesystemUpdatePlatform::new(data_dir);
    updater
        .stage(&verified.version, &artifact)
        .map_err(|error| anyhow!("stage verified agent release: {error}"))?;
    updater
        .activate(&verified.version)
        .map_err(|error| anyhow!("activate verified agent release: {error}"))?;
    enrollment.release_sequence = verified.sequence;
    save_enrollment(state_path, enrollment).context("save verified release watermark")?;
    Ok(true)
}

fn apply_heartbeat_response(
    state_path: &std::path::Path,
    enrollment: &mut EnrollmentState,
    response: &HeartbeatResponse,
) -> Result<()> {
    if !response.control_public_key.is_empty() {
        if let Some(current) = enrollment.control_public_key.as_deref() {
            if current != response.control_public_key {
                return Err(anyhow!("server control key changed after enrollment"));
            }
        }
    }
    if !response.control_public_key.is_empty() && enrollment.control_public_key.is_none() {
        enrollment.control_public_key = Some(response.control_public_key.clone());
        save_enrollment(state_path, enrollment).context("save control-plane key")?;
    }
    Ok(())
}

async fn send_heartbeat(
    client: &Client,
    identity: &Identity,
    enrollment: &EnrollmentState,
    release_sequence: u64,
) -> Result<HeartbeatResponse> {
    let payload = HeartbeatPayload {
        agent_id: enrollment.agent_id.clone(),
        observed_at: timestamp(),
        task_generation: enrollment.task_generation,
        release_sequence,
    };
    let body = serde_json::to_string(&payload)?;
    send_signed(
        client,
        identity,
        &enrollment.server_url,
        &enrollment.agent_id,
        "/api/agent/v1/heartbeat",
        body,
    )
    .await?
    .json::<HeartbeatResponse>()
    .await
    .context("decode heartbeat response")
}

fn record_task_watermark(
    state_path: &std::path::Path,
    enrollment: &mut EnrollmentState,
    generation: u64,
) -> Result<()> {
    if generation <= enrollment.task_generation {
        return Ok(());
    }
    enrollment.task_generation = generation;
    save_enrollment(state_path, enrollment).context("save task watermark")
}

async fn send_telemetry(
    client: &Client,
    identity: &Identity,
    enrollment: &EnrollmentState,
    collector: &mut impl HostCollector,
    buffer: &TelemetryBuffer,
    dropped_samples: u64,
) -> Result<()> {
    let payload = TelemetryPayload {
        agent_id: enrollment.agent_id.clone(),
        batch_id: Uuid::new_v4().to_string(),
        observed_at: timestamp(),
        dropped_samples,
        host: collector.collect(),
    };
    buffer.enqueue(payload, Utc::now().timestamp_millis())?;
    for queued in buffer.pending(Utc::now().timestamp_millis())? {
        let batch_id = queued.batch_id.clone();
        let body = serde_json::to_string(&queued)?;
        send_signed(
            client,
            identity,
            &enrollment.server_url,
            &enrollment.agent_id,
            "/api/agent/v1/telemetry",
            body,
        )
        .await?;
        buffer.acknowledge(&batch_id)?;
    }
    Ok(())
}

async fn process_task(
    client: &Client,
    identity: &Identity,
    enrollment: &mut EnrollmentState,
    state_path: &std::path::Path,
    task: TaskEnvelope,
) -> Result<()> {
    let control_key = enrollment
        .control_public_key
        .as_deref()
        .ok_or_else(|| anyhow!("server task verification key is not enrolled"))?;
    if task.agent_id != enrollment.agent_id || !verify_task(&task, control_key, Utc::now()) {
        return Err(anyhow!("server task envelope is invalid or expired"));
    }
    if task.generation <= enrollment.task_generation {
        return Err(anyhow!(
            "server task envelope is stale or already processed"
        ));
    }
    let payload = task.payload.clone();
    match (task.kind.as_str(), payload) {
        ("network-scan", TaskPayload::Scan(payload)) => {
            process_scan_task(client, identity, enrollment, task.clone(), payload).await
        }
        ("relay-connect", TaskPayload::Relay(payload)) => {
            process_relay_task(client, identity, enrollment, task.clone(), payload).await
        }
        _ => Err(anyhow!("server task kind and payload do not match")),
    }?;
    record_task_watermark(state_path, enrollment, task.generation)
}

async fn process_scan_task(
    client: &Client,
    identity: &Identity,
    enrollment: &EnrollmentState,
    task: TaskEnvelope,
    payload: ScanTaskPayload,
) -> Result<()> {
    let semaphore = Arc::new(Semaphore::new(64));
    let mut handles = Vec::with_capacity(payload.addresses.len());
    for address in &payload.addresses {
        let address = address.clone();
        let permit = semaphore.clone().acquire_owned().await?;
        let port = payload.port;
        handles.push(tokio::spawn(async move {
            let outcome = match tokio::time::timeout(
                Duration::from_millis(800),
                TcpStream::connect((address.as_str(), port)),
            )
            .await
            {
                Ok(Ok(_)) => "open",
                Ok(Err(_)) => "closed",
                Err(_) => "timeout",
            };
            drop(permit);
            ScanResult {
                address,
                port,
                outcome: outcome.to_string(),
                mac_address: None,
                fingerprint: None,
            }
        }));
    }

    let mut results = Vec::with_capacity(handles.len());
    for handle in handles {
        results.push(handle.await?);
    }
    results.sort_by(|left, right| left.address.cmp(&right.address));
    let payload = ScanTaskResult {
        task_id: task.task_id.clone(),
        agent_id: enrollment.agent_id.clone(),
        generation: task.generation,
        payload_digest: task.payload_digest,
        results,
    };
    let body = serde_json::to_string(&payload)?;
    let path = format!("/api/agent/v1/tasks/{}/result", task.task_id);
    send_task_result(
        client,
        identity,
        &enrollment.server_url,
        &enrollment.agent_id,
        &path,
        body,
    )
    .await?;
    Ok(())
}

#[derive(Debug, serde::Serialize)]
#[serde(rename_all = "camelCase")]
struct RelayChannelOpenRequest {
    relay_id: String,
    agent_id: String,
    target_system_id: String,
    target_address: String,
    target_port: u16,
    direction: String,
    nonce: String,
    expires_at: String,
    signature: String,
}

fn relay_channel_message(input: &RelayChannelOpenRequest) -> String {
    [
        input.relay_id.as_str(),
        input.agent_id.as_str(),
        input.target_system_id.as_str(),
        input.target_address.as_str(),
        &input.target_port.to_string(),
        input.direction.as_str(),
        input.nonce.as_str(),
        input.expires_at.as_str(),
    ]
    .join("\n")
}

async fn process_relay_task(
    client: &Client,
    identity: &Identity,
    enrollment: &EnrollmentState,
    task: TaskEnvelope,
    payload: RelayTaskPayload,
) -> Result<()> {
    let target = tokio::time::timeout(
        Duration::from_secs(5),
        TcpStream::connect((payload.target_address.as_str(), payload.port)),
    )
    .await
    .context("connect to the relay target")??;

    let upstream_signature =
        open_relay_channel(client, identity, enrollment, &payload, "upstream").await?;
    let downstream_signature =
        open_relay_channel(client, identity, enrollment, &payload, "downstream").await?;
    bridge_relay(
        identity,
        enrollment,
        &payload.relay_id,
        &upstream_signature,
        &downstream_signature,
        target,
    )
    .await?;

    let result = RelayTaskResult {
        task_id: task.task_id.clone(),
        agent_id: enrollment.agent_id.clone(),
        generation: task.generation,
        payload_digest: task.payload_digest,
        kind: "relay-connect".to_string(),
    };
    let path = format!("/api/agent/v1/tasks/{}/result", task.task_id);
    send_task_result(
        client,
        identity,
        &enrollment.server_url,
        &enrollment.agent_id,
        &path,
        serde_json::to_string(&result)?,
    )
    .await?;
    Ok(())
}

async fn open_relay_channel(
    client: &Client,
    identity: &Identity,
    enrollment: &EnrollmentState,
    payload: &RelayTaskPayload,
    direction: &str,
) -> Result<String> {
    let mut unsigned = RelayChannelOpenRequest {
        relay_id: payload.relay_id.clone(),
        agent_id: enrollment.agent_id.clone(),
        target_system_id: payload.target_system_id.clone(),
        target_address: payload.target_address.clone(),
        target_port: payload.port,
        direction: direction.to_string(),
        nonce: payload.nonce.clone(),
        expires_at: payload.expires_at.clone(),
        signature: String::new(),
    };
    unsigned.signature =
        URL_SAFE_NO_PAD.encode(identity.sign(relay_channel_message(&unsigned).as_bytes()));
    let signature = unsigned.signature.clone();
    send_signed(
        client,
        identity,
        &enrollment.server_url,
        &enrollment.agent_id,
        "/api/agent/v1/relay/open",
        serde_json::to_string(&unsigned)?,
    )
    .await?;
    Ok(signature)
}

async fn bridge_relay(
    identity: &Identity,
    enrollment: &EnrollmentState,
    relay_id: &str,
    upstream_signature: &str,
    downstream_signature: &str,
    target: TcpStream,
) -> Result<()> {
    let streaming_client = Client::builder()
        .connect_timeout(Duration::from_secs(5))
        .build()
        .context("build relay HTTP client")?;
    let (mut target_reader, mut target_writer) = target.into_split();
    let (sender, receiver) = mpsc::channel::<std::result::Result<Vec<u8>, std::io::Error>>(16);
    let upstream_path = format!("/api/agent/v1/relay/{relay_id}/upstream");
    let downstream_path = format!("/api/agent/v1/relay/{relay_id}/downstream");
    let upstream_headers = signed_stream_headers(
        identity,
        &enrollment.agent_id,
        "POST",
        &upstream_path,
        upstream_signature,
    )?;
    let downstream_headers = signed_stream_headers(
        identity,
        &enrollment.agent_id,
        "GET",
        &downstream_path,
        downstream_signature,
    )?;
    let upstream_url = format!("{}{upstream_path}", enrollment.server_url);
    let downstream_url = format!("{}{downstream_path}", enrollment.server_url);
    let body_stream = futures_util::stream::unfold(receiver, |mut receiver| async move {
        receiver.recv().await.map(|chunk| (chunk, receiver))
    });

    let upstream_client = streaming_client.clone();
    let upstream = async move {
        let response = upstream_client
            .post(upstream_url)
            .headers(upstream_headers)
            .header("content-type", "application/octet-stream")
            .body(Body::wrap_stream(body_stream))
            .send()
            .await
            .context("send relay upstream stream")?;
        if !response.status().is_success() {
            return Err(anyhow!(
                "relay upstream stream was rejected with {}",
                response.status()
            ));
        }
        Ok::<(), anyhow::Error>(())
    };

    let read_target = async move {
        let mut buffer = [0_u8; 16 * 1024];
        loop {
            let count = target_reader
                .read(&mut buffer)
                .await
                .context("read relay target")?;
            if count == 0 {
                break;
            }
            sender
                .send(Ok(buffer[..count].to_vec()))
                .await
                .map_err(|_| anyhow!("relay upstream stream closed while target was readable"))?;
        }
        Ok::<(), anyhow::Error>(())
    };

    let downstream_client = streaming_client;
    let downstream = async move {
        let response = downstream_client
            .get(downstream_url)
            .headers(downstream_headers)
            .send()
            .await
            .context("open relay downstream stream")?;
        if !response.status().is_success() {
            return Err(anyhow!(
                "relay downstream stream was rejected with {}",
                response.status()
            ));
        }
        let mut stream = response.bytes_stream();
        while let Some(chunk) = stream.next().await {
            let chunk = chunk.context("read relay downstream stream")?;
            target_writer
                .write_all(&chunk)
                .await
                .context("write relay target")?;
        }
        target_writer
            .shutdown()
            .await
            .context("close relay target write side")?;
        Ok::<(), anyhow::Error>(())
    };

    let (read_result, upstream_result, downstream_result) =
        tokio::join!(read_target, upstream, downstream);
    read_result?;
    upstream_result?;
    downstream_result?;
    Ok(())
}

fn signed_stream_headers(
    identity: &Identity,
    agent_id: &str,
    method: &str,
    path: &str,
    relay_signature: &str,
) -> Result<HeaderMap> {
    let timestamp = Utc::now().timestamp().to_string();
    let request_id = Uuid::new_v4().to_string();
    let signature = sign_request(
        identity.signing_key(),
        method,
        path,
        &timestamp,
        &request_id,
        "",
    );
    let mut headers = HeaderMap::new();
    headers.insert("x-scout-agent-id", HeaderValue::from_str(agent_id)?);
    headers.insert("x-scout-timestamp", HeaderValue::from_str(&timestamp)?);
    headers.insert("x-scout-request-id", HeaderValue::from_str(&request_id)?);
    headers.insert("x-scout-signature", HeaderValue::from_str(&signature)?);
    headers.insert(
        "x-scout-relay-signature",
        HeaderValue::from_str(relay_signature)?,
    );
    Ok(headers)
}

async fn send_signed(
    client: &Client,
    identity: &Identity,
    server_url: &str,
    agent_id: &str,
    path: &str,
    body: String,
) -> Result<reqwest::Response> {
    send_signed_method(client, identity, server_url, agent_id, "POST", path, body).await
}

async fn send_task_result(
    client: &Client,
    identity: &Identity,
    server_url: &str,
    agent_id: &str,
    path: &str,
    body: String,
) -> Result<()> {
    let response =
        send_signed_method_raw(client, identity, server_url, agent_id, "POST", path, body).await?;
    if response.status().is_success() || response.status() == StatusCode::CONFLICT {
        return Ok(());
    }
    response
        .error_for_status()
        .context("signed task result was rejected")?;
    Ok(())
}

async fn send_signed_get(
    client: &Client,
    identity: &Identity,
    server_url: &str,
    agent_id: &str,
    path: &str,
) -> Result<reqwest::Response> {
    send_signed_method(
        client,
        identity,
        server_url,
        agent_id,
        "GET",
        path,
        String::new(),
    )
    .await
}

async fn send_signed_method(
    client: &Client,
    identity: &Identity,
    server_url: &str,
    agent_id: &str,
    method: &str,
    path: &str,
    body: String,
) -> Result<reqwest::Response> {
    let response =
        send_signed_method_raw(client, identity, server_url, agent_id, method, path, body).await?;
    response
        .error_for_status()
        .context("signed agent request was rejected")
}

async fn send_signed_method_raw(
    client: &Client,
    identity: &Identity,
    server_url: &str,
    agent_id: &str,
    method: &str,
    path: &str,
    body: String,
) -> Result<reqwest::Response> {
    let timestamp = Utc::now().timestamp().to_string();
    let request_id = Uuid::new_v4().to_string();
    let signature = sign_request(
        identity.signing_key(),
        method,
        path,
        &timestamp,
        &request_id,
        &body,
    );
    let mut headers = HeaderMap::new();
    headers.insert("x-scout-agent-id", HeaderValue::from_str(agent_id)?);
    headers.insert("x-scout-timestamp", HeaderValue::from_str(&timestamp)?);
    headers.insert("x-scout-request-id", HeaderValue::from_str(&request_id)?);
    headers.insert("x-scout-signature", HeaderValue::from_str(&signature)?);
    let request = client
        .request(
            Method::from_bytes(method.as_bytes())?,
            format!("{server_url}{path}"),
        )
        .headers(headers)
        .header("content-type", "application/json");
    if method == "GET" {
        request.send().await
    } else {
        request.body(body).send().await
    }
    .context("send signed agent request")
}

fn configured_publisher_public_key() -> Option<String> {
    std::env::var("SCOUT_RELEASE_PUBLISHER_PUBLIC_KEY")
        .ok()
        .map(|value| value.trim().to_string())
        .filter(|value| !value.is_empty())
}

fn load_enrollment(path: &std::path::Path) -> Option<EnrollmentState> {
    std::fs::read_to_string(path)
        .ok()
        .and_then(|contents| serde_json::from_str(&contents).ok())
}

fn save_enrollment(path: &std::path::Path, state: &EnrollmentState) -> Result<()> {
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent)?;
    }
    let contents = serde_json::to_vec_pretty(state)?;
    let temporary = path.with_extension(format!("tmp-{}", Uuid::new_v4()));
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options.open(&temporary)?;
    use std::io::Write;
    if let Err(error) = file.write_all(&contents).and_then(|_| file.sync_all()) {
        drop(file);
        let _ = std::fs::remove_file(&temporary);
        return Err(error.into());
    }
    drop(file);
    if let Err(error) = std::fs::rename(&temporary, path) {
        let _ = std::fs::remove_file(&temporary);
        return Err(error.into());
    }
    #[cfg(unix)]
    if let Some(parent) = path.parent() {
        std::fs::File::open(parent)?.sync_all()?;
    }
    Ok(())
}

fn timestamp() -> String {
    Utc::now().to_rfc3339_opts(SecondsFormat::Millis, true)
}

fn platform_name() -> &'static str {
    if cfg!(target_os = "macos") {
        "macos"
    } else {
        "linux"
    }
}

fn architecture_name() -> &'static str {
    if cfg!(target_arch = "aarch64") {
        "aarch64"
    } else {
        "x86_64"
    }
}

fn usage() -> &'static str {
    "Usage: scout-agent --server URL --invitation TOKEN [--data-dir PATH] [--once]"
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::protocol::{body_digest, task_message};
    use ed25519_dalek::{Signer, SigningKey};
    use rand::rngs::OsRng;
    use tokio::net::TcpListener;

    #[tokio::test]
    async fn a_signed_replayed_task_is_rejected_before_side_effects() -> Result<()> {
        let directory = tempfile::tempdir()?;
        let control_key = SigningKey::generate(&mut OsRng);
        let now = Utc::now();
        let payload = TaskPayload::Scan(ScanTaskPayload {
            cidr: "127.0.0.0/8".to_string(),
            port: 22,
            addresses: Vec::new(),
        });
        let mut task = TaskEnvelope {
            task_id: "44444444-4444-4444-8444-444444444444".to_string(),
            agent_id: "agent-1".to_string(),
            kind: "network-scan".to_string(),
            generation: 1,
            policy_version: 1,
            payload_digest: body_digest(&serde_json::to_string(&payload)?),
            issued_at: now.to_rfc3339(),
            expires_at: (now + chrono::Duration::minutes(2)).to_rfc3339(),
            deadline_at: (now + chrono::Duration::minutes(1)).to_rfc3339(),
            payload,
            signature: String::new(),
        };
        task.signature =
            URL_SAFE_NO_PAD.encode(control_key.sign(task_message(&task).as_bytes()).to_bytes());
        let mut enrollment = EnrollmentState {
            server_url: "http://127.0.0.1:1".to_string(),
            agent_id: "agent-1".to_string(),
            system_id: "system-1".to_string(),
            control_public_key: Some(
                URL_SAFE_NO_PAD.encode(control_key.verifying_key().to_bytes()),
            ),
            publisher_public_key: None,
            task_generation: 1,
            release_sequence: 0,
        };

        let error = process_task(
            &Client::new(),
            &load_or_create(&directory.path().join("identity.json"))?,
            &mut enrollment,
            &directory.path().join("enrollment.json"),
            task,
        )
        .await
        .expect_err("a replayed task must be rejected");

        assert_eq!(
            error.to_string(),
            "server task envelope is stale or already processed"
        );
        assert_eq!(enrollment.task_generation, 1);
        assert!(!directory.path().join("enrollment.json").exists());
        Ok(())
    }

    #[test]
    fn saving_enrollment_replaces_the_live_file_atomically() -> Result<()> {
        let directory = tempfile::tempdir()?;
        let path = directory.path().join("enrollment.json");
        let snapshot = directory.path().join("enrollment.snapshot.json");
        let initial = EnrollmentState {
            server_url: "http://127.0.0.1:1".to_string(),
            agent_id: "agent-1".to_string(),
            system_id: "system-1".to_string(),
            control_public_key: None,
            publisher_public_key: None,
            task_generation: 1,
            release_sequence: 0,
        };
        save_enrollment(&path, &initial)?;
        std::fs::hard_link(&path, &snapshot)?;

        let updated = EnrollmentState {
            task_generation: 2,
            ..initial.clone()
        };
        save_enrollment(&path, &updated)?;

        assert_eq!(
            load_enrollment(&path)
                .expect("updated enrollment")
                .task_generation,
            2
        );
        assert_eq!(
            load_enrollment(&snapshot)
                .expect("unchanged enrollment snapshot")
                .task_generation,
            1
        );
        assert_eq!(
            directory
                .path()
                .read_dir()?
                .filter_map(Result::ok)
                .filter(|entry| {
                    entry
                        .file_name()
                        .to_string_lossy()
                        .starts_with("enrollment.tmp-")
                })
                .count(),
            0
        );
        Ok(())
    }

    #[tokio::test]
    async fn a_failed_task_result_can_be_retried_with_the_same_lease() -> Result<()> {
        let directory = tempfile::tempdir()?;
        let identity = load_or_create(&directory.path().join("identity.json"))?;
        let control_key = SigningKey::generate(&mut OsRng);
        let listener = TcpListener::bind("127.0.0.1:0").await?;
        let server_url = format!("http://{}", listener.local_addr()?);
        let server = tokio::spawn(async move {
            for status in [500, 409] {
                let (mut stream, _) = listener.accept().await?;
                let mut request = [0_u8; 4096];
                let _ = stream.read(&mut request).await?;
                let response = format!(
                    "HTTP/1.1 {status} Test\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
                );
                stream.write_all(response.as_bytes()).await?;
            }
            Ok::<(), std::io::Error>(())
        });
        let now = Utc::now();
        let payload = TaskPayload::Scan(ScanTaskPayload {
            cidr: "127.0.0.0/8".to_string(),
            port: 22,
            addresses: Vec::new(),
        });
        let mut task = TaskEnvelope {
            task_id: "55555555-5555-4555-8555-555555555555".to_string(),
            agent_id: "agent-1".to_string(),
            kind: "network-scan".to_string(),
            generation: 1,
            policy_version: 1,
            payload_digest: body_digest(&serde_json::to_string(&payload)?),
            issued_at: now.to_rfc3339(),
            expires_at: (now + chrono::Duration::minutes(2)).to_rfc3339(),
            deadline_at: (now + chrono::Duration::minutes(1)).to_rfc3339(),
            payload,
            signature: String::new(),
        };
        task.signature =
            URL_SAFE_NO_PAD.encode(control_key.sign(task_message(&task).as_bytes()).to_bytes());
        let mut enrollment = EnrollmentState {
            server_url,
            agent_id: "agent-1".to_string(),
            system_id: "system-1".to_string(),
            control_public_key: Some(
                URL_SAFE_NO_PAD.encode(control_key.verifying_key().to_bytes()),
            ),
            publisher_public_key: None,
            task_generation: 0,
            release_sequence: 0,
        };
        let state_path = directory.path().join("enrollment.json");

        process_task(
            &Client::new(),
            &identity,
            &mut enrollment,
            &state_path,
            task.clone(),
        )
        .await
        .expect_err("a failed result must not advance the watermark");
        assert_eq!(enrollment.task_generation, 0);
        assert!(!state_path.exists());

        process_task(
            &Client::new(),
            &identity,
            &mut enrollment,
            &state_path,
            task,
        )
        .await?;
        assert_eq!(enrollment.task_generation, 1);
        server.await??;
        Ok(())
    }
}
