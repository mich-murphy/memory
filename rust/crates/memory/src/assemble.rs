//! Start a production-typed daemon from a state root and `config.toml` alone.
//!
//! Store, clock, sources and policy context are all built here from the config and the token
//! file; nothing is injected. The daemon lock is held before the token file is read and before
//! any network call, and a refusal releases it.
use crate::clock::SystemClock;
use crate::config::Config;
use crate::daemon::{BuildError, Daemon, EXIT_CONFIG, Parts, StartError};
use crate::realfs::WalkLimits;
use crate::resolve::{SharedFs, WalkSources, pack};
use crate::roots::{RootsError, locator_roots};
use crate::s3::{S3ConfigError, S3Store, TimeoutPolicy};
use crate::token::{TokenError, read_token};
use memory_core::privacy::{PolicyContext, rules_digest};
use std::path::Path;

/// The policy revision bound into every capture. Fixed: it is hashed into the binding, so it
/// changes only when the built-in policy does.
pub const POLICY_REVISION: &str = "builtin-1";

/// The production daemon.
pub type ProductionDaemon = Daemon<SharedFs, S3Store, SystemClock, WalkSources>;

/// Why assembly refused. Closed; carries no path, key or endpoint.
#[derive(Debug)]
pub enum AssembleError {
    /// Lock, state, config or config-token metadata refusal.
    Start(StartError),
    /// `prefix` is empty.
    PrefixRequired,
    /// The configured roots are unusable.
    Roots(RootsError),
    /// The token file was refused.
    Token(TokenError),
    /// The S3 client could not be built.
    Store(S3ConfigError),
}

impl AssembleError {
    /// Fixed machine-readable code.
    #[must_use]
    pub fn code(&self) -> &'static str {
        match self {
            Self::Start(error) => error.code(),
            Self::PrefixRequired => "config_prefix_required",
            Self::Roots(error) => error.code(),
            Self::Token(error) => error.code(),
            Self::Store(_) => "config_store",
        }
    }

    /// Process exit code: the startup code, otherwise [`EXIT_CONFIG`].
    #[must_use]
    pub fn exit_code(&self) -> u8 {
        match self {
            Self::Start(error) => error.exit_code(),
            _ => EXIT_CONFIG,
        }
    }
}

/// Config path, token path and state root, as given and canonicalised, with their identities.
fn collector(paths: [&Path; 3]) -> (Vec<String>, Vec<u128>) {
    let mut names: Vec<String> = Vec::new();
    let mut identities: Vec<u128> = Vec::new();
    for path in paths {
        let canonical = std::fs::canonicalize(path).ok();
        for candidate in std::iter::once(path).chain(canonical.as_deref()) {
            let name = candidate.to_string_lossy().into_owned();
            if !names.contains(&name) {
                names.push(name);
            }
            if let Ok(meta) = std::fs::metadata(candidate) {
                let id = pack(&meta);
                if !identities.contains(&id) {
                    identities.push(id);
                }
            }
        }
    }
    (names, identities)
}

/// Build the S3 store from the config and the token file: the one place both the daemon and
/// `memory restore` construct it. The token is read here, after the config's own checks.
/// No network call is made and the prefix is not judged (the daemon refuses an empty one
/// itself; reading a bucket is not a production write).
///
/// # Errors
/// [`AssembleError::Token`] or [`AssembleError::Store`].
pub fn build_store(config: &Config) -> Result<S3Store, AssembleError> {
    let credentials = read_token(&config.token_path).map_err(AssembleError::Token)?;
    S3Store::new(
        &config.endpoint,
        &config.bucket,
        &config.region,
        credentials.access_key_id(),
        credentials.secret_access_key(),
        &config.prefix,
        TimeoutPolicy::PRODUCTION,
    )
    .map_err(AssembleError::Store)
}

fn build(
    root: &Path,
    config_path: &Path,
    config: &Config,
) -> Result<Parts<SharedFs, S3Store, SystemClock, WalkSources>, AssembleError> {
    // No production write until the A13.2 prerequisites (tracing, hint retention, the audit)
    // exist: an empty prefix is the production bucket root, so only a prefixed run may start.
    if config.prefix.is_empty() {
        return Err(AssembleError::PrefixRequired);
    }
    let roots = locator_roots(config).map_err(AssembleError::Roots)?;
    let store = build_store(config)?;
    let fs = SharedFs::new();
    let sources = WalkSources::new(roots, WalkLimits::default(), fs.clone());
    let (collector_paths, collector_identities) =
        collector([config_path, &config.token_path, root]);
    Ok(Parts {
        fs,
        store,
        clock: SystemClock::new(),
        sources,
        context: PolicyContext {
            valid: true,
            revision: POLICY_REVISION.to_owned(),
            rules: Some(rules_digest()),
            collector_paths,
            collector_identities,
        },
    })
}

/// Start a daemon from `root` (the state root) and `config_path` alone.
///
/// The config is read once. The lock is taken first; the token file is read only after it, and
/// no network call is made here. An empty `prefix` is refused with
/// [`AssembleError::PrefixRequired`] (exit [`EXIT_CONFIG`]) so that no production write can
/// happen before the A13.2 prerequisites exist.
///
/// # Errors
/// An [`AssembleError`]; the lock is released on every refusal.
pub fn start_from_config(
    root: &Path,
    config_path: &Path,
) -> Result<ProductionDaemon, AssembleError> {
    Daemon::start_built(root, config_path, |config| build(root, config_path, config)).map_err(
        |error| match error {
            BuildError::Start(start) => AssembleError::Start(start),
            BuildError::Build(inner) => inner,
        },
    )
}
