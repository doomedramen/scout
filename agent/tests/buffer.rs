use std::time::Duration;

use scout_agent::buffer::TelemetryBuffer;
use scout_agent::protocol::{HostSnapshot, TelemetryPayload};
use tempfile::tempdir;

#[test]
fn telemetry_buffer_survives_restart_and_discards_oldest_when_bounded() {
    let directory = tempdir().unwrap();
    let path = directory.path().join("telemetry.queue");
    let buffer = TelemetryBuffer::new_with_limits(&path, 1_000, Duration::from_secs(60));
    let first = sample("first", 1);
    let second = sample("second", 2);
    assert_eq!(buffer.enqueue(first, 100).unwrap(), 0);
    assert_eq!(buffer.enqueue(second, 101).unwrap(), 0);

    let restarted = TelemetryBuffer::new_with_limits(&path, 1_000, Duration::from_secs(60));
    let pending = restarted.pending(102).unwrap();
    assert_eq!(pending.len(), 2);
    restarted.acknowledge("first").unwrap();
    assert_eq!(restarted.pending(102).unwrap().len(), 1);
    assert_eq!(restarted.pending(60_200).unwrap().len(), 0);
}

#[test]
fn telemetry_buffer_reports_drops_when_size_is_exceeded() {
    let directory = tempdir().unwrap();
    let buffer = TelemetryBuffer::new_with_limits(
        directory.path().join("queue"),
        500,
        Duration::from_secs(60),
    );
    assert_eq!(buffer.enqueue(sample("first", 1), 100).unwrap(), 0);
    assert!(buffer.enqueue(sample("second", 2), 101).unwrap() >= 1);
    let pending = buffer.pending(102).unwrap();
    assert_eq!(pending.len(), 1);
    assert_eq!(pending[0].batch_id, "second");
    assert_eq!(pending[0].dropped_samples, 1);
}

#[test]
fn telemetry_buffer_reports_expired_samples_after_restart() {
    let directory = tempdir().unwrap();
    let path = directory.path().join("queue");
    let buffer = TelemetryBuffer::new_with_limits(&path, 10_000, Duration::from_secs(60));

    assert_eq!(buffer.enqueue(sample("expired", 1), 100_000).unwrap(), 0);
    let restarted = TelemetryBuffer::new_with_limits(&path, 10_000, Duration::from_secs(60));
    assert_eq!(restarted.enqueue(sample("current", 2), 160_001).unwrap(), 1);

    let pending = restarted.pending(160_002).unwrap();
    assert_eq!(pending.len(), 1);
    assert_eq!(pending[0].batch_id, "current");
    assert_eq!(pending[0].dropped_samples, 1);
}

fn sample(batch_id: &str, value: u64) -> TelemetryPayload {
    TelemetryPayload {
        agent_id: "agent-1".to_string(),
        batch_id: batch_id.to_string(),
        observed_at: "2026-09-13T00:00:00Z".to_string(),
        dropped_samples: 0,
        host: HostSnapshot {
            hostname: Some("host".to_string()),
            operating_system: None,
            kernel_version: None,
            cpu_usage_percent: Some(value as f32),
            memory_used_bytes: Some(value),
            memory_total_bytes: Some(100),
            uptime_seconds: Some(value),
            filesystems: Vec::new(),
            interfaces: Vec::new(),
        },
    }
}
