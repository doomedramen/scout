use std::fs;

use scout_agent::platform::{
    render_launchd_plist, render_systemd_unit, FilesystemUpdatePlatform, UpdatePlatform,
};
use tempfile::tempdir;

#[test]
fn versioned_updates_switch_atomically_and_roll_back_to_last_version() {
    let directory = tempdir().unwrap();
    let platform = FilesystemUpdatePlatform::new(directory.path());

    platform.stage("1.0.0", b"one").unwrap();
    platform.activate("1.0.0").unwrap();
    platform.stage("1.1.0", b"two").unwrap();
    platform.activate("1.1.0").unwrap();
    assert_eq!(
        fs::read(directory.path().join("current/scout-agent")).unwrap(),
        b"two"
    );

    platform.rollback().unwrap();
    assert_eq!(
        fs::read(directory.path().join("current/scout-agent")).unwrap(),
        b"one"
    );
    assert!(platform.stage("../escape", b"bad").is_err());
}

#[test]
fn a_release_stays_rollbackable_until_the_first_healthy_heartbeat() {
    let directory = tempdir().unwrap();
    let platform = FilesystemUpdatePlatform::new(directory.path());

    platform.stage("1.0.0", b"one").unwrap();
    platform.activate("1.0.0").unwrap();
    platform.stage("1.1.0", b"two").unwrap();
    platform.activate("1.1.0").unwrap();
    assert!(directory.path().join("previous").exists());

    platform.mark_healthy().unwrap();
    assert!(!directory.path().join("previous").exists());
}

#[test]
fn service_descriptors_keep_runtime_configuration_in_one_place() {
    let systemd = render_systemd_unit(
        "/usr/local/lib/scout-agent/scout-agent-launcher",
        "https://scout.example.test",
        "/var/lib/scout-agent",
    );
    assert!(systemd.contains("Restart=always"));
    assert!(systemd.contains("ProtectSystem=strict"));
    assert!(systemd.contains("scout-agent-launcher --server https://scout.example.test"));

    let launchd = render_launchd_plist(
        "/usr/local/lib/scout-agent/scout-agent-launcher",
        "https://scout.example.test",
        "/Library/Application Support/ScoutAgent",
    );
    assert!(launchd.contains("page.rtin.scout-agent"));
    assert!(launchd.contains("<key>KeepAlive</key><true/>"));
    assert!(launchd.contains("/Library/Application Support/ScoutAgent"));
}
