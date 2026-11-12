//! The S3 token file: TOML with exactly `access_key_id` and `secret_access_key`.
//!
//! [`Config::load`](crate::config::Config::load) checks the file by metadata only. This module
//! is the one place that reads its contents. It checks the open handle (so the file checked is
//! the file read), bounds the read, and never lets a value reach an error or `Debug` output.
use serde::Deserialize;
use std::io::Read;
use std::os::unix::fs::MetadataExt;
use std::path::Path;

/// Largest token file read, in bytes.
pub const MAX_TOKEN_BYTES: u64 = 4096;

/// Closed set of token failures. No variant carries file content.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum TokenError {
    /// The file could not be opened, inspected or read.
    #[error("token_unavailable: {0}")]
    Unavailable(std::io::ErrorKind),
    /// The opened handle is not a regular file.
    #[error("token_not_file")]
    NotFile,
    /// The file is accessible by group or world.
    #[error("token_permissions")]
    Permissions,
    /// The file is larger than [`MAX_TOKEN_BYTES`].
    #[error("token_too_large")]
    TooLarge,
    /// Not UTF-8 TOML, an unknown, missing or wrongly typed field.
    #[error("token_parse")]
    Parse,
    /// A field is empty.
    #[error("token_empty")]
    Empty,
}

impl TokenError {
    /// Fixed machine-readable code.
    #[must_use]
    pub const fn code(self) -> &'static str {
        match self {
            Self::Unavailable(_) => "token_unavailable",
            Self::NotFile => "token_not_file",
            Self::Permissions => "token_permissions",
            Self::TooLarge => "token_too_large",
            Self::Parse => "token_parse",
            Self::Empty => "token_empty",
        }
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Raw {
    access_key_id: String,
    secret_access_key: String,
}

/// The two S3 credentials. Neither `Debug` nor any error prints them.
pub struct S3Credentials {
    access_key_id: String,
    secret_access_key: String,
}

impl S3Credentials {
    /// The access key id.
    #[must_use]
    pub fn access_key_id(&self) -> &str {
        &self.access_key_id
    }

    /// The secret access key.
    #[must_use]
    pub fn secret_access_key(&self) -> &str {
        &self.secret_access_key
    }
}

impl std::fmt::Debug for S3Credentials {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("S3Credentials").finish_non_exhaustive()
    }
}

/// Open, check and parse the token file.
///
/// # Errors
/// A closed [`TokenError`]; never the file's text.
pub fn read_token(path: &Path) -> Result<S3Credentials, TokenError> {
    let unavailable = |error: std::io::Error| TokenError::Unavailable(error.kind());
    let file = std::fs::File::open(path).map_err(unavailable)?;
    let meta = file.metadata().map_err(unavailable)?;
    if !meta.is_file() {
        return Err(TokenError::NotFile);
    }
    if meta.mode() & 0o077 != 0 {
        return Err(TokenError::Permissions);
    }
    let mut bytes = Vec::new();
    file.take(MAX_TOKEN_BYTES + 1)
        .read_to_end(&mut bytes)
        .map_err(unavailable)?;
    if bytes.len() as u64 > MAX_TOKEN_BYTES {
        return Err(TokenError::TooLarge);
    }
    let text = std::str::from_utf8(&bytes).map_err(|_| TokenError::Parse)?;
    let raw: Raw = toml::from_str(text).map_err(|_| TokenError::Parse)?;
    if raw.access_key_id.is_empty() || raw.secret_access_key.is_empty() {
        return Err(TokenError::Empty);
    }
    Ok(S3Credentials {
        access_key_id: raw.access_key_id,
        secret_access_key: raw.secret_access_key,
    })
}
