use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use anyhow::{anyhow, Context, Result};
use chrono::{SecondsFormat, Utc};
use reqwest::header::{HeaderMap, HeaderValue};
use reqwest::Client;
use tokio::net::TcpStream;
use tokio::sync::Semaphore;
use uuid::Uuid;

use crate::buffer::TelemetryBuffer;
use crate::collector::{HostCollector, SysinfoHostCollector};
use crate::identity::{load_or_create, Identity};
use crate::protocol::{
    sign_request, verify_task, EnrollmentRequest, EnrollmentResponse, HeartbeatPayload,
    HeartbeatResponse, ScanResult, ScanTaskResult, TaskEnvelope, TelemetryPayload,
};

const HEARTBEAT_INTERVAL: Duration = Duration::from_secs(15);
const TELEMETRY_INTERVAL: Duration = Duration::from_secs(30);

#[derive(Debug, Clone)]
pub struct AgentConfig {
    pub server_url: String,
    pub invitation: Option<String>,
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
}

impl AgentConfig {
    pub fn from_environment_and_args<I, S>(args: I) -> Result<Self>
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        let mut server_url = std::env::var("SCOUT_SERVER_URL").ok();
        let mut invitation = std::env::var("SCOUT_INVITATION").ok();
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

    let mut collector = SysinfoHostCollector::new();
    let telemetry_buffer = TelemetryBuffer::new(config.data_dir.join("telemetry.queue"));
    let response = send_heartbeat(&client, &identity, &enrollment, 0, 0).await?;
    apply_heartbeat_response(&state_path, &mut enrollment, &response)?;
    send_telemetry(
        &client,
        &identity,
        &enrollment,
        &mut collector,
        &telemetry_buffer,
        0,
    )
    .await?;
    if let Some(task) = response.task {
        process_task(&client, &identity, &enrollment, task).await?;
    }
    if config.once {
        return Ok(());
    }

    let mut heartbeat = tokio::time::interval(HEARTBEAT_INTERVAL);
    let mut telemetry = tokio::time::interval(TELEMETRY_INTERVAL);
    heartbeat.tick().await;
    telemetry.tick().await;
    let mut dropped_samples = 0;
    loop {
        tokio::select! {
            _ = tokio::signal::ctrl_c() => return Ok(()),
            _ = heartbeat.tick() => {
                match send_heartbeat(&client, &identity, &enrollment, 0, 0).await {
                    Ok(response) => {
                        if let Err(error) = apply_heartbeat_response(&state_path, &mut enrollment, &response) {
                            eprintln!("scout-agent state save failed: {error:#}");
                        }
                        if let Some(task) = response.task {
                            if let Err(error) = process_task(&client, &identity, &enrollment, task).await {
                                eprintln!("scout-agent task failed: {error:#}");
                            }
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
    })
}

fn apply_heartbeat_response(
    state_path: &std::path::Path,
    enrollment: &mut EnrollmentState,
    response: &HeartbeatResponse,
) -> Result<()> {
    if !response.control_public_key.is_empty()
        && enrollment.control_public_key.as_deref() != Some(response.control_public_key.as_str())
    {
        enrollment.control_public_key = Some(response.control_public_key.clone());
        save_enrollment(state_path, enrollment).context("save control-plane key")?;
    }
    Ok(())
}

async fn send_heartbeat(
    client: &Client,
    identity: &Identity,
    enrollment: &EnrollmentState,
    task_generation: u64,
    release_sequence: u64,
) -> Result<HeartbeatResponse> {
    let payload = HeartbeatPayload {
        agent_id: enrollment.agent_id.clone(),
        observed_at: timestamp(),
        task_generation,
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
    enrollment: &EnrollmentState,
    task: TaskEnvelope,
) -> Result<()> {
    let control_key = enrollment
        .control_public_key
        .as_deref()
        .ok_or_else(|| anyhow!("server task verification key is not enrolled"))?;
    if task.agent_id != enrollment.agent_id || !verify_task(&task, control_key, Utc::now()) {
        return Err(anyhow!("server task envelope is invalid or expired"));
    }

    let semaphore = Arc::new(Semaphore::new(64));
    let mut handles = Vec::with_capacity(task.payload.addresses.len());
    for address in &task.payload.addresses {
        let address = address.clone();
        let permit = semaphore.clone().acquire_owned().await?;
        let port = task.payload.port;
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
    send_signed(
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

async fn send_signed(
    client: &Client,
    identity: &Identity,
    server_url: &str,
    agent_id: &str,
    path: &str,
    body: String,
) -> Result<reqwest::Response> {
    let timestamp = Utc::now().timestamp().to_string();
    let request_id = Uuid::new_v4().to_string();
    let signature = sign_request(
        identity.signing_key(),
        "POST",
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
    let response = client
        .post(format!("{server_url}{path}"))
        .headers(headers)
        .header("content-type", "application/json")
        .body(body)
        .send()
        .await
        .context("send signed agent request")?
        .error_for_status()
        .context("signed agent request was rejected")?;
    Ok(response)
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
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create(true).truncate(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options.open(path)?;
    use std::io::Write;
    file.write_all(&contents)?;
    file.sync_all()?;
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
