use std::fs::{self, OpenOptions};
use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};
use std::time::Duration;

use anyhow::{Context, Result};

use crate::protocol::TelemetryPayload;

const MAX_BYTES: usize = 32 * 1024 * 1024;
const MAX_AGE: Duration = Duration::from_secs(24 * 60 * 60);

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
struct QueuedSample {
    queued_at: i64,
    payload: TelemetryPayload,
}

pub struct TelemetryBuffer {
    path: PathBuf,
    max_bytes: usize,
    max_age: Duration,
}

impl TelemetryBuffer {
    pub fn new(path: impl Into<PathBuf>) -> Self {
        Self {
            path: path.into(),
            max_bytes: MAX_BYTES,
            max_age: MAX_AGE,
        }
    }

    pub fn new_with_limits(path: impl Into<PathBuf>, max_bytes: usize, max_age: Duration) -> Self {
        Self {
            path: path.into(),
            max_bytes,
            max_age,
        }
    }

    /// Append a sample and return the number of older samples discarded.
    pub fn enqueue(&self, payload: TelemetryPayload, now: i64) -> Result<u64> {
        let mut entries = self.read()?;
        let cutoff = now.saturating_sub(self.max_age.as_millis() as i64);
        let before = entries.len();
        entries.retain(|entry| entry.queued_at > cutoff);
        let mut dropped = (before - entries.len()) as u64;
        entries.push(QueuedSample {
            queued_at: now,
            payload,
        });

        while serialized_size(&entries)? > self.max_bytes {
            if entries.is_empty() {
                break;
            }
            entries.remove(0);
            dropped += 1;
        }
        self.write(&entries)?;
        Ok(dropped)
    }

    pub fn pending(&self, now: i64) -> Result<Vec<TelemetryPayload>> {
        let cutoff = now.saturating_sub(self.max_age.as_millis() as i64);
        Ok(self
            .read()?
            .into_iter()
            .filter(|entry| entry.queued_at > cutoff)
            .map(|entry| entry.payload)
            .collect())
    }

    pub fn acknowledge(&self, batch_id: &str) -> Result<()> {
        let mut entries = self.read()?;
        if let Some(index) = entries
            .iter()
            .position(|entry| entry.payload.batch_id == batch_id)
        {
            entries.remove(index);
            self.write(&entries)?;
        }
        Ok(())
    }

    fn read(&self) -> Result<Vec<QueuedSample>> {
        let file = match fs::File::open(&self.path) {
            Ok(file) => file,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(Vec::new()),
            Err(error) => return Err(error).context("open telemetry buffer"),
        };
        BufReader::new(file)
            .lines()
            .map(|line| {
                let line = line.context("read telemetry buffer")?;
                serde_json::from_str(&line).context("decode telemetry buffer")
            })
            .collect()
    }

    fn write(&self, entries: &[QueuedSample]) -> Result<()> {
        if let Some(parent) = self.path.parent() {
            fs::create_dir_all(parent)?;
            set_private_directory(parent)?;
        }
        let temporary = self
            .path
            .with_extension(format!("tmp-{}", std::process::id()));
        let mut options = OpenOptions::new();
        options.write(true).create(true).truncate(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options.open(&temporary)?;
        for entry in entries {
            serde_json::to_writer(&mut file, entry)?;
            file.write_all(b"\n")?;
        }
        file.sync_all()?;
        fs::rename(temporary, &self.path)?;
        Ok(())
    }
}

fn serialized_size(entries: &[QueuedSample]) -> Result<usize> {
    entries.iter().try_fold(0, |size, entry| {
        Ok(size + serde_json::to_vec(entry)?.len() + 1)
    })
}

fn set_private_directory(path: &Path) -> std::io::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(0o700))?;
    }
    Ok(())
}
