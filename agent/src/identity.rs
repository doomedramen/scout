use std::fs::{self, OpenOptions};
use std::io::{ErrorKind, Write};
use std::path::Path;

use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use ed25519_dalek::{Signer, SigningKey};
use rand::rngs::OsRng;
use serde::{Deserialize, Serialize};
use thiserror::Error;

#[derive(Debug, Error)]
pub enum IdentityError {
    #[error("unable to read agent identity: {0}")]
    Read(#[from] std::io::Error),
    #[error("agent identity is invalid: {0}")]
    Invalid(String),
    #[error("unable to encode agent identity: {0}")]
    Json(#[from] serde_json::Error),
}

#[derive(Debug, Serialize, Deserialize)]
struct StoredIdentity {
    version: u8,
    secret_key: String,
}

pub struct Identity {
    signing_key: SigningKey,
}

impl Identity {
    pub fn public_key(&self) -> String {
        URL_SAFE_NO_PAD.encode(self.signing_key.verifying_key().to_bytes())
    }

    pub fn signing_key(&self) -> &SigningKey {
        &self.signing_key
    }

    pub fn sign(&self, message: &[u8]) -> Vec<u8> {
        self.signing_key.sign(message).to_bytes().to_vec()
    }
}

pub fn load_or_create(path: &Path) -> Result<Identity, IdentityError> {
    match fs::read_to_string(path) {
        Ok(contents) => decode(&contents),
        Err(error) if error.kind() == ErrorKind::NotFound => create(path),
        Err(error) => Err(IdentityError::Read(error)),
    }
}

fn create(path: &Path) -> Result<Identity, IdentityError> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
        set_private_directory(parent)?;
    }

    let signing_key = SigningKey::generate(&mut OsRng);
    let stored = StoredIdentity {
        version: 1,
        secret_key: URL_SAFE_NO_PAD.encode(signing_key.to_bytes()),
    };
    let contents = serde_json::to_vec_pretty(&stored)?;
    let temp_path = path.with_extension(format!("tmp-{}", hex_suffix(&contents)));
    let result = write_new_file(&temp_path, path, &contents);
    match result {
        Ok(()) => Ok(Identity { signing_key }),
        Err(error) if error.kind() == ErrorKind::AlreadyExists => {
            let _ = fs::remove_file(&temp_path);
            let existing = fs::read_to_string(path)?;
            decode(&existing)
        }
        Err(error) => {
            let _ = fs::remove_file(&temp_path);
            Err(IdentityError::Read(error))
        }
    }
}

fn write_new_file(temp_path: &Path, path: &Path, contents: &[u8]) -> std::io::Result<()> {
    let mut file = OpenOptions::new();
    file.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        file.mode(0o600);
    }
    let mut handle = file.open(temp_path)?;
    handle.write_all(contents)?;
    handle.sync_all()?;
    match fs::hard_link(temp_path, path) {
        Ok(()) => {
            let _ = fs::remove_file(temp_path);
            Ok(())
        }
        Err(error) => Err(error),
    }
}

fn decode(contents: &str) -> Result<Identity, IdentityError> {
    let stored: StoredIdentity = serde_json::from_str(contents)?;
    if stored.version != 1 {
        return Err(IdentityError::Invalid(
            "unsupported identity version".to_string(),
        ));
    }
    let bytes = URL_SAFE_NO_PAD
        .decode(stored.secret_key)
        .map_err(|_| IdentityError::Invalid("secret key is not base64url".to_string()))?;
    let secret: [u8; 32] = bytes
        .try_into()
        .map_err(|_| IdentityError::Invalid("secret key must be 32 bytes".to_string()))?;
    Ok(Identity {
        signing_key: SigningKey::from_bytes(&secret),
    })
}

fn hex_suffix(contents: &[u8]) -> String {
    contents
        .iter()
        .take(8)
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

fn set_private_directory(path: &Path) -> std::io::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(0o700))?;
    }
    Ok(())
}
