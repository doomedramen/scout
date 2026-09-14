use std::fs;

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
