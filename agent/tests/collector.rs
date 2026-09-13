use scout_agent::collector::{HostCollector, SysinfoHostCollector};

#[test]
fn host_collector_reports_only_local_host_fields() {
    let mut collector = SysinfoHostCollector::new();
    let snapshot = collector.collect();
    assert!(snapshot.hostname.is_some());
    assert!(snapshot.uptime_seconds.is_some());
    assert!(snapshot
        .filesystems
        .iter()
        .all(|disk| !disk.mount_point.is_empty()));
}
