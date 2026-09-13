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
