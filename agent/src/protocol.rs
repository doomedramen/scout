use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use ed25519_dalek::{Signature, Signer, SigningKey};
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
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct EnrollmentResponse {
    pub agent_id: String,
    pub system_id: String,
    pub server_time: String,
    pub heartbeat_interval_seconds: u64,
    pub telemetry_interval_seconds: u64,
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
