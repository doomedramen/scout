use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use ed25519_dalek::{Signature, Signer, SigningKey, Verifier, VerifyingKey};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct EnrollmentRequest {
    pub invitation: String,
    pub public_key: String,
    pub platform: String,
    pub architecture: String,
    pub version: String,
    pub proof: String,
}

pub fn enrollment_message(
    invitation: &str,
    public_key: &str,
    platform: &str,
    architecture: &str,
    version: &str,
) -> String {
    [invitation, public_key, platform, architecture, version].join("\n")
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct EnrollmentResponse {
    pub agent_id: String,
    pub system_id: String,
    pub server_time: String,
    pub heartbeat_interval_seconds: u64,
    pub telemetry_interval_seconds: u64,
    pub control_public_key: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct HeartbeatPayload {
    pub agent_id: String,
    pub observed_at: String,
    pub task_generation: u64,
    pub release_sequence: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FilesystemMetric {
    pub mount_point: String,
    pub total_bytes: Option<u64>,
    pub available_bytes: Option<u64>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct InterfaceMetric {
    pub name: String,
    pub mac_address: Option<String>,
    pub addresses: Vec<String>,
    pub received_bytes: Option<u64>,
    pub transmitted_bytes: Option<u64>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct HostSnapshot {
    pub hostname: Option<String>,
    pub operating_system: Option<String>,
    pub kernel_version: Option<String>,
    pub cpu_usage_percent: Option<f32>,
    pub memory_used_bytes: Option<u64>,
    pub memory_total_bytes: Option<u64>,
    pub uptime_seconds: Option<u64>,
    pub filesystems: Vec<FilesystemMetric>,
    pub interfaces: Vec<InterfaceMetric>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TelemetryPayload {
    pub agent_id: String,
    pub batch_id: String,
    pub observed_at: String,
    pub dropped_samples: u64,
    pub host: HostSnapshot,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ScanTaskPayload {
    pub cidr: String,
    pub port: u16,
    pub addresses: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RelayTaskPayload {
    pub cidr: String,
    pub port: u16,
    pub addresses: Vec<String>,
    pub relay_id: String,
    pub target_system_id: String,
    pub target_address: String,
    pub nonce: String,
    pub expires_at: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum TaskPayload {
    Relay(RelayTaskPayload),
    Scan(ScanTaskPayload),
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TaskEnvelope {
    pub task_id: String,
    pub agent_id: String,
    pub kind: String,
    pub generation: u64,
    pub policy_version: u64,
    pub payload_digest: String,
    pub issued_at: String,
    pub expires_at: String,
    pub deadline_at: String,
    pub payload: TaskPayload,
    pub signature: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct HeartbeatResponse {
    pub ok: bool,
    pub server_time: String,
    pub control_public_key: String,
    pub task: Option<TaskEnvelope>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ScanResult {
    pub address: String,
    pub port: u16,
    pub outcome: String,
    pub mac_address: Option<String>,
    pub fingerprint: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ScanTaskResult {
    pub task_id: String,
    pub agent_id: String,
    pub generation: u64,
    pub payload_digest: String,
    pub results: Vec<ScanResult>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RelayTaskResult {
    pub task_id: String,
    pub agent_id: String,
    pub generation: u64,
    pub payload_digest: String,
    pub kind: String,
}

pub fn body_digest(body: &str) -> String {
    URL_SAFE_NO_PAD.encode(Sha256::digest(body.as_bytes()))
}

pub fn signed_request_message(
    method: &str,
    path: &str,
    timestamp: &str,
    request_id: &str,
    body: &str,
) -> String {
    [
        method.to_uppercase(),
        path.to_string(),
        timestamp.to_string(),
        request_id.to_string(),
        body_digest(body),
    ]
    .join("\n")
}

pub fn sign_request(
    signing_key: &SigningKey,
    method: &str,
    path: &str,
    timestamp: &str,
    request_id: &str,
    body: &str,
) -> String {
    let message = signed_request_message(method, path, timestamp, request_id, body);
    URL_SAFE_NO_PAD.encode(signing_key.sign(message.as_bytes()).to_bytes())
}

pub fn signature_from_base64(value: &str) -> Option<Signature> {
    let bytes = URL_SAFE_NO_PAD.decode(value).ok()?;
    Signature::from_slice(&bytes).ok()
}

pub fn task_message(task: &TaskEnvelope) -> String {
    [
        task.task_id.clone(),
        task.agent_id.clone(),
        task.kind.clone(),
        task.generation.to_string(),
        task.policy_version.to_string(),
        task.payload_digest.clone(),
        task.issued_at.clone(),
        task.expires_at.clone(),
        task.deadline_at.clone(),
    ]
    .join("\n")
}

pub fn verify_task(
    task: &TaskEnvelope,
    control_public_key: &str,
    now: chrono::DateTime<chrono::Utc>,
) -> bool {
    let payload_matches_kind = matches!(
        (&task.kind[..], &task.payload),
        ("network-scan", TaskPayload::Scan(_)) | ("relay-connect", TaskPayload::Relay(_))
    );
    if !payload_matches_kind || task.agent_id.is_empty() {
        return false;
    }
    let public_bytes = match URL_SAFE_NO_PAD.decode(control_public_key) {
        Ok(bytes) => bytes,
        Err(_) => return false,
    };
    let public_bytes: [u8; 32] = match public_bytes.try_into() {
        Ok(bytes) => bytes,
        Err(_) => return false,
    };
    let verifying_key = match VerifyingKey::from_bytes(&public_bytes) {
        Ok(key) => key,
        Err(_) => return false,
    };
    let signature = match signature_from_base64(&task.signature) {
        Some(signature) => signature,
        None => return false,
    };
    let issued_at = match chrono::DateTime::parse_from_rfc3339(&task.issued_at) {
        Ok(value) => value.with_timezone(&chrono::Utc),
        Err(_) => return false,
    };
    let expires_at = match chrono::DateTime::parse_from_rfc3339(&task.expires_at) {
        Ok(value) => value.with_timezone(&chrono::Utc),
        Err(_) => return false,
    };
    let deadline_at = match chrono::DateTime::parse_from_rfc3339(&task.deadline_at) {
        Ok(value) => value.with_timezone(&chrono::Utc),
        Err(_) => return false,
    };
    if issued_at > now || expires_at <= now || deadline_at <= now {
        return false;
    }
    let payload = match serde_json::to_string(&task.payload) {
        Ok(payload) => payload,
        Err(_) => return false,
    };
    if body_digest(&payload) != task.payload_digest {
        return false;
    }
    verifying_key
        .verify(task_message(task).as_bytes(), &signature)
        .is_ok()
}
