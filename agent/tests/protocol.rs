use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use chrono::{Duration, Utc};
use ed25519_dalek::{Signer, SigningKey};
use rand::rngs::OsRng;
use scout_agent::protocol::{
    body_digest, signed_request_message, task_message, verify_task, ScanTaskPayload, TaskEnvelope,
};

#[test]
fn signed_request_canonicalization_is_stable() {
    assert_eq!(
        body_digest("{\"ok\":true}"),
        "QGLtr3UPuAdOfoPgyQKMlOMkaKi28WFHdDKO8EUVD5M"
    );
    assert_eq!(
        signed_request_message(
            "post",
            "/api/agent/v1/heartbeat",
            "1700000000",
            "request-1",
            "{\"ok\":true}"
        ),
        "POST\n/api/agent/v1/heartbeat\n1700000000\nrequest-1\nQGLtr3UPuAdOfoPgyQKMlOMkaKi28WFHdDKO8EUVD5M"
    );
}

#[test]
fn task_envelopes_require_the_control_signature_and_current_payload() {
    let signing_key = SigningKey::generate(&mut OsRng);
    let now = Utc::now();
    let payload = ScanTaskPayload {
        cidr: "192.0.2.0/30".to_string(),
        port: 22,
        addresses: vec!["192.0.2.1".to_string(), "192.0.2.2".to_string()],
    };
    let payload_json = serde_json::to_string(&payload).unwrap();
    let mut task = TaskEnvelope {
        task_id: "task-1".to_string(),
        agent_id: "agent-1".to_string(),
        kind: "network-scan".to_string(),
        generation: 1,
        policy_version: 1,
        payload_digest: body_digest(&payload_json),
        issued_at: now.to_rfc3339(),
        expires_at: (now + Duration::minutes(2)).to_rfc3339(),
        deadline_at: (now + Duration::minutes(1)).to_rfc3339(),
        payload,
        signature: String::new(),
    };
    task.signature =
        URL_SAFE_NO_PAD.encode(signing_key.sign(task_message(&task).as_bytes()).to_bytes());
    let public_key = URL_SAFE_NO_PAD.encode(signing_key.verifying_key().to_bytes());

    assert!(verify_task(&task, &public_key, now));
    task.payload.addresses.push("192.0.2.3".to_string());
    assert!(!verify_task(&task, &public_key, now));
}
