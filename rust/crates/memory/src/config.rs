//! `config.toml` (DESIGN.md 9.1): archive id, machine id, agent roots, window, bucket and token
//! path. Every table denies unknown fields. Validation errors are a closed set that never
//! echoes a configured value, and the token file is checked by metadata only, never read.
use memory_core::domain::{Agent, ArchiveId, MachineId};
use serde::Deserialize;
use std::io::Read;
use std::os::unix::fs::MetadataExt;
use std::path::{Path, PathBuf};
use std::time::Duration;

/// Largest `config.toml` the daemon reads.
pub const MAX_CONFIG_BYTES: u64 = 1 << 20;
/// Default quiet period (DESIGN.md 3.2).
pub const DEFAULT_QUIET_SECS: u64 = 60;
/// Default cap (DESIGN.md 3.2).
pub const DEFAULT_CAP_SECS: u64 = 300;
/// Longest accepted cap: one day.
pub const MAX_CAP_SECS: u64 = 86_400;

/// Closed set of configuration failures. No variant carries a configured value.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum ConfigError {
    /// The file could not be read.
    #[error("config_read: {0}")]
    Read(std::io::ErrorKind),
    /// Not valid TOML, an unknown field, a missing field or a wrongly typed field.
    #[error("config_parse")]
    Parse,
    /// `archive_id` is not a valid archive id.
    #[error("config_archive_id")]
    ArchiveId,
    /// `machine_id` is not a valid machine id.
    #[error("config_machine_id")]
    MachineId,
    /// `bucket` is not a valid bucket name.
    #[error("config_bucket")]
    Bucket,
    /// `window` is zero, inverted or too long.
    #[error("config_window")]
    Window,
    /// A root path is not absolute.
    #[error("config_root_not_absolute")]
    RootNotAbsolute,
    /// `token_path` is not absolute.
    #[error("config_token_path_not_absolute")]
    TokenPathNotAbsolute,
    /// The token file could not be inspected.
    #[error("config_token_unavailable: {0}")]
    TokenUnavailable(std::io::ErrorKind),
    /// The token path is not a regular file.
    #[error("config_token_not_file")]
    TokenNotFile,
    /// The token file is readable or writable by group or world.
    #[error("config_token_permissions")]
    TokenPermissions,
    /// `endpoint` is not an `https` URL, or an `http` URL for a non-loopback host, or carries
    /// userinfo, a query or a fragment.
    #[error("config_endpoint")]
    Endpoint,
    /// `region` is empty.
    #[error("config_region")]
    Region,
    /// `prefix` is not empty or a relative, `/`-terminated path of clean segments.
    #[error("config_prefix")]
    Prefix,
}

impl ConfigError {
    /// Fixed machine-readable code.
    #[must_use]
    pub const fn code(self) -> &'static str {
        match self {
            Self::Read(_) => "config_read",
            Self::Parse => "config_parse",
            Self::ArchiveId => "config_archive_id",
            Self::MachineId => "config_machine_id",
            Self::Bucket => "config_bucket",
            Self::Window => "config_window",
            Self::RootNotAbsolute => "config_root_not_absolute",
            Self::TokenPathNotAbsolute => "config_token_path_not_absolute",
            Self::TokenUnavailable(_) => "config_token_unavailable",
            Self::TokenNotFile => "config_token_not_file",
            Self::TokenPermissions => "config_token_permissions",
            Self::Endpoint => "config_endpoint",
            Self::Region => "config_region",
            Self::Prefix => "config_prefix",
        }
    }
}

/// One agent root to scan.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Root {
    /// Which agent writes under this root.
    pub agent: Agent,
    /// Absolute directory.
    pub path: PathBuf,
}

/// Coalescer window in seconds, `window = { quiet, cap }`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
struct RawWindow {
    #[serde(default = "default_quiet")]
    quiet: u64,
    #[serde(default = "default_cap")]
    cap: u64,
}

const fn default_quiet() -> u64 {
    DEFAULT_QUIET_SECS
}
const fn default_cap() -> u64 {
    DEFAULT_CAP_SECS
}

impl Default for RawWindow {
    fn default() -> Self {
        Self {
            quiet: DEFAULT_QUIET_SECS,
            cap: DEFAULT_CAP_SECS,
        }
    }
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct Raw {
    archive_id: String,
    machine_id: String,
    #[serde(default)]
    roots: Vec<Root>,
    #[serde(default)]
    window: RawWindow,
    endpoint: String,
    #[serde(default = "default_region")]
    region: String,
    #[serde(default)]
    prefix: String,
    bucket: String,
    token_path: PathBuf,
}

fn default_region() -> String {
    "auto".to_owned()
}

/// Longest accepted key prefix in bytes.
pub const MAX_PREFIX_BYTES: usize = 256;

fn valid_endpoint(text: &str) -> bool {
    let Ok(url) = text.parse::<url::Url>() else {
        return false;
    };
    if !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return false;
    }
    match (url.scheme(), url.host()) {
        ("https", Some(_)) => true,
        ("http", Some(url::Host::Domain(name))) => name == "localhost",
        ("http", Some(url::Host::Ipv4(ip))) => ip.is_loopback(),
        ("http", Some(url::Host::Ipv6(ip))) => ip.is_loopback(),
        _ => false,
    }
}

fn valid_prefix(prefix: &str) -> bool {
    if prefix.is_empty() {
        return true;
    }
    if prefix.len() > MAX_PREFIX_BYTES || prefix.starts_with('/') {
        return false;
    }
    let Some(body) = prefix.strip_suffix('/') else {
        return false;
    };
    body.split('/')
        .all(|segment| !segment.is_empty() && segment != "." && segment != "..")
}

/// Validated coalescer window.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Window {
    /// Quiet period that flushes a session.
    pub quiet: Duration,
    /// Cap since the first unflushed signal.
    pub cap: Duration,
}

/// A validated configuration.
#[derive(Clone, PartialEq, Eq)]
pub struct Config {
    /// Archive identity.
    pub archive_id: ArchiveId,
    /// Machine identity.
    pub machine_id: MachineId,
    /// Configured agent roots.
    pub roots: Vec<Root>,
    /// Coalescer window.
    pub window: Window,
    /// S3 endpoint URL: `https`, or `http` only for a loopback host. No userinfo, query or
    /// fragment.
    pub endpoint: String,
    /// Signing region, `auto` by default.
    pub region: String,
    /// Key prefix: empty, or relative and `/`-terminated.
    pub prefix: String,
    /// Bucket name.
    pub bucket: String,
    /// Absolute path of the token file.
    pub token_path: PathBuf,
}

impl std::fmt::Debug for Config {
    /// Omits the endpoint, which can identify the storage account.
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Config")
            .field("archive_id", &self.archive_id)
            .field("machine_id", &self.machine_id)
            .field("roots", &self.roots)
            .field("window", &self.window)
            .field("region", &self.region)
            .field("prefix", &self.prefix)
            .field("bucket", &self.bucket)
            .field("token_path", &self.token_path)
            .finish_non_exhaustive()
    }
}

fn valid_bucket(name: &str) -> bool {
    let bytes = name.as_bytes();
    let edge = |b: &u8| b.is_ascii_lowercase() || b.is_ascii_digit();
    (3..=63).contains(&bytes.len())
        && bytes.first().is_some_and(edge)
        && bytes.last().is_some_and(edge)
        && bytes.iter().all(|b| edge(b) || *b == b'-')
}

impl Config {
    /// Parse and validate text. Does not touch the filesystem; see [`Config::check_token`].
    ///
    /// # Errors
    /// A closed [`ConfigError`] naming the first failed rule, never the value.
    pub fn parse(text: &str) -> Result<Self, ConfigError> {
        let raw: Raw = toml::from_str(text).map_err(|_| ConfigError::Parse)?;
        let archive_id = ArchiveId::try_from(raw.archive_id).map_err(|_| ConfigError::ArchiveId)?;
        let machine_id = MachineId::try_from(raw.machine_id).map_err(|_| ConfigError::MachineId)?;
        if !valid_bucket(&raw.bucket) {
            return Err(ConfigError::Bucket);
        }
        if !valid_endpoint(&raw.endpoint) {
            return Err(ConfigError::Endpoint);
        }
        if raw.region.is_empty() {
            return Err(ConfigError::Region);
        }
        if !valid_prefix(&raw.prefix) {
            return Err(ConfigError::Prefix);
        }
        let RawWindow { quiet, cap } = raw.window;
        if quiet == 0 || cap < quiet || cap > MAX_CAP_SECS {
            return Err(ConfigError::Window);
        }
        if raw.roots.iter().any(|root| !root.path.is_absolute()) {
            return Err(ConfigError::RootNotAbsolute);
        }
        if !raw.token_path.is_absolute() {
            return Err(ConfigError::TokenPathNotAbsolute);
        }
        Ok(Self {
            archive_id,
            machine_id,
            roots: raw.roots,
            window: Window {
                quiet: Duration::from_secs(quiet),
                cap: Duration::from_secs(cap),
            },
            endpoint: raw.endpoint,
            region: raw.region,
            prefix: raw.prefix,
            bucket: raw.bucket,
            token_path: raw.token_path,
        })
    }

    /// Check the token file by metadata only: a regular file, no group or world access bits.
    /// The contents are never read.
    ///
    /// # Errors
    /// [`ConfigError::TokenUnavailable`], [`ConfigError::TokenNotFile`] or
    /// [`ConfigError::TokenPermissions`].
    pub fn check_token(&self) -> Result<(), ConfigError> {
        let meta = std::fs::metadata(&self.token_path)
            .map_err(|error| ConfigError::TokenUnavailable(error.kind()))?;
        if !meta.is_file() {
            return Err(ConfigError::TokenNotFile);
        }
        if meta.mode() & 0o077 != 0 {
            return Err(ConfigError::TokenPermissions);
        }
        Ok(())
    }

    /// Read, parse, validate and check the token. The daemon refuses to start on any error.
    ///
    /// # Errors
    /// [`ConfigError::Read`] or any error of [`Config::parse`] / [`Config::check_token`].
    pub fn load(path: &Path) -> Result<Self, ConfigError> {
        let file = std::fs::File::open(path).map_err(|error| ConfigError::Read(error.kind()))?;
        let mut text = String::new();
        file.take(MAX_CONFIG_BYTES)
            .read_to_string(&mut text)
            .map_err(|error| ConfigError::Read(error.kind()))?;
        let config = Self::parse(&text)?;
        config.check_token()?;
        Ok(config)
    }
}
