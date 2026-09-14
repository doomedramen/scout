use std::fs::{self, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};
use std::time::Duration;

use thiserror::Error;

#[derive(Debug, Error)]
pub enum PlatformError {
    #[error("platform operation is unsupported: {0}")]
    Unsupported(String),
    #[error("platform operation failed: {0}")]
    Failed(String),
}

pub type PlatformResult<T> = Result<T, PlatformError>;

#[derive(Debug, Clone)]
pub struct AccessObservation {
    pub address: String,
    pub port: u16,
    pub open: bool,
}

pub trait NetworkScanner {
    fn probe(&self, address: &str, port: u16, timeout: Duration) -> PlatformResult<bool>;
}

pub trait ServiceManager {
    fn install(&self, executable: &str, arguments: &[String]) -> PlatformResult<()>;
    fn uninstall(&self) -> PlatformResult<()>;
    fn is_running(&self) -> PlatformResult<bool>;
}

pub trait Installer {
    fn install(&self, executable: &[u8], configuration: &str) -> PlatformResult<()>;
}

pub trait UpdatePlatform {
    fn stage(&self, version: &str, executable: &[u8]) -> PlatformResult<()>;
    fn activate(&self, version: &str) -> PlatformResult<()>;
    fn rollback(&self) -> PlatformResult<()>;
}

pub trait ServiceCollector {
    fn collect(&mut self) -> PlatformResult<Vec<String>>;
}

pub struct FilesystemUpdatePlatform {
    root: PathBuf,
}

impl FilesystemUpdatePlatform {
    pub fn new(root: impl Into<PathBuf>) -> Self {
        Self { root: root.into() }
    }

    fn release_path(&self, version: &str) -> PlatformResult<PathBuf> {
        validate_version(version)?;
        Ok(self.root.join("releases").join(version).join("scout-agent"))
    }

    fn activate_version(&self, version: &str, record_previous: bool) -> PlatformResult<()> {
        let release = self.release_path(version)?;
        if !release.is_file() {
            return Err(PlatformError::Failed(format!(
                "agent release does not exist: {version}"
            )));
        }
        fs::create_dir_all(&self.root).map_err(io_failure)?;
        let current = self.root.join("current");
        if record_previous && current.exists() {
            let previous = fs::read_link(&current).map_err(io_failure)?;
            let version = previous
                .file_name()
                .and_then(|value| value.to_str())
                .ok_or_else(|| {
                    PlatformError::Failed("current agent link is invalid".to_string())
                })?;
            write_atomic(&self.root.join("previous"), version.as_bytes())?;
        }
        let temporary = self
            .root
            .join(format!("current.tmp-{}", std::process::id()));
        let _ = fs::remove_file(&temporary);
        #[cfg(unix)]
        std::os::unix::fs::symlink(Path::new("releases").join(version), &temporary)
            .map_err(io_failure)?;
        #[cfg(not(unix))]
        return Err(PlatformError::Unsupported(
            "atomic agent links require a Unix platform".to_string(),
        ));
        fs::rename(&temporary, &current).map_err(io_failure)?;
        Ok(())
    }
}

impl UpdatePlatform for FilesystemUpdatePlatform {
    fn stage(&self, version: &str, executable: &[u8]) -> PlatformResult<()> {
        let release = self.release_path(version)?;
        if executable.is_empty() {
            return Err(PlatformError::Failed("agent release is empty".to_string()));
        }
        fs::create_dir_all(release.parent().expect("release path has a parent"))
            .map_err(io_failure)?;
        let temporary = release.with_extension(format!("tmp-{}", std::process::id()));
        let mut options = OpenOptions::new();
        options.write(true).create(true).truncate(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o755);
        }
        let mut file = options.open(&temporary).map_err(io_failure)?;
        file.write_all(executable).map_err(io_failure)?;
        file.sync_all().map_err(io_failure)?;
        fs::rename(&temporary, &release).map_err(io_failure)?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&release, fs::Permissions::from_mode(0o755)).map_err(io_failure)?;
        }
        Ok(())
    }

    fn activate(&self, version: &str) -> PlatformResult<()> {
        self.activate_version(version, true)
    }

    fn rollback(&self) -> PlatformResult<()> {
        let previous = fs::read_to_string(self.root.join("previous")).map_err(io_failure)?;
        self.activate_version(previous.trim(), false)
    }
}

pub fn render_systemd_unit(executable: &str, server_url: &str, data_dir: &str) -> String {
    format!(
        "[Unit]\nDescription=Scout host monitoring agent\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=simple\nExecStart={executable} --server {server_url} --data-dir {data_dir}\nRestart=always\nRestartSec=5\nNoNewPrivileges=true\nProtectSystem=strict\nProtectHome=true\nReadWritePaths={data_dir}\n\n[Install]\nWantedBy=multi-user.target\n"
    )
}

pub fn render_launchd_plist(executable: &str, server_url: &str, data_dir: &str) -> String {
    format!(
        "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n  <key>Label</key><string>page.rtin.scout-agent</string>\n  <key>ProgramArguments</key><array><string>{}</string><string>--server</string><string>{}</string><string>--data-dir</string><string>{}</string></array>\n  <key>RunAtLoad</key><true/>\n  <key>KeepAlive</key><true/>\n</dict></plist>\n",
        xml_escape(executable),
        xml_escape(server_url),
        xml_escape(data_dir),
    )
}

fn validate_version(version: &str) -> PlatformResult<()> {
    if version.is_empty()
        || version.len() > 64
        || !version
            .bytes()
            .all(|value| value.is_ascii_alphanumeric() || matches!(value, b'.' | b'-' | b'_'))
    {
        return Err(PlatformError::Failed(
            "agent release version is invalid".to_string(),
        ));
    }
    Ok(())
}

fn write_atomic(path: &Path, contents: &[u8]) -> PlatformResult<()> {
    let temporary = path.with_extension(format!("tmp-{}", std::process::id()));
    let mut file = OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .open(&temporary)
        .map_err(io_failure)?;
    file.write_all(contents).map_err(io_failure)?;
    file.sync_all().map_err(io_failure)?;
    fs::rename(temporary, path).map_err(io_failure)
}

fn io_failure(error: std::io::Error) -> PlatformError {
    PlatformError::Failed(error.to_string())
}

fn xml_escape(value: &str) -> String {
    value
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}
