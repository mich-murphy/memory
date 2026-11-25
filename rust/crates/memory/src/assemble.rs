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
use memory_core::coalescer::{Window, WindowSeconds};
use memory_core::privacy::{PolicyContext, rules_digest};
use std::path::{Path, PathBuf};

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
    /// The configured roots are unusable.
    Roots(RootsError),
    /// The token file was refused.
    Token(TokenError),
    /// The S3 client could not be built.
    Store(S3ConfigError),
    /// The validated window was refused by the coalescer (cannot happen after config checks).
    Window,
}

impl AssembleError {
    /// Fixed machine-readable code.
    #[must_use]
    pub fn code(&self) -> &'static str {
        match self {
            Self::Start(error) => error.code(),
            Self::Roots(error) => error.code(),
            Self::Token(error) => error.code(),
            Self::Store(_) => "config_store",
            Self::Window => "config_window",
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
/// No network call is made and the prefix is not judged (the config already checked it; an
/// empty one is the bucket root).
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

/// The policy context the daemon captures under: valid, the built-in revision and rules, and
/// the identities of the config file, the token file and the state root. `memory check` builds
/// the same context so it judges sources as capture does.
#[must_use]
pub fn policy_context(root: &Path, config_path: &Path, config: &Config) -> PolicyContext {
    let (collector_paths, collector_identities) =
        collector([config_path, &config.token_path, root]);
    PolicyContext {
        valid: true,
        revision: POLICY_REVISION.to_owned(),
        rules: Some(rules_digest()),
        collector_paths,
        collector_identities,
    }
}

fn build(
    root: &Path,
    config_path: &Path,
    config: &Config,
) -> Result<Parts<SharedFs, S3Store, SystemClock, WalkSources>, AssembleError> {
    let roots = locator_roots(config).map_err(AssembleError::Roots)?;
    let store = build_store(config)?;
    let fs = SharedFs::new();
    let sources = WalkSources::new(roots, WalkLimits::default(), fs.clone());
    Ok(Parts {
        fs,
        store,
        clock: SystemClock::new(),
        sources,
        context: policy_context(root, config_path, config),
    })
}

/// A started daemon with what the event loop needs next to it.
pub struct Wired {
    /// The daemon.
    pub daemon: ProductionDaemon,
    /// The configured root directories, as configured.
    pub roots: Vec<PathBuf>,
    /// The coalescer window from `config.toml`.
    pub window: Window,
    /// A copy of the daemon's own clock: the same monotonic origin, so the instants of
    /// [`Daemon::next_retry`] and the driver's readings are comparable.
    pub clock: SystemClock,
}

/// Start a daemon from `root` (the state root) and `config_path` alone, as
/// [`start_from_config`], and return it with the roots, the window and the daemon's clock.
///
/// # Errors
/// An [`AssembleError`]; the lock is released on every refusal.
pub fn start_wired(root: &Path, config_path: &Path) -> Result<Wired, AssembleError> {
    let mut seam = None;
    let daemon = Daemon::start_built(root, config_path, |config| {
        let parts = build(root, config_path, config)?;
        let window = Window::try_from(WindowSeconds {
            quiet: config.window.quiet.as_secs(),
            cap: config.window.cap.as_secs(),
        })
        .map_err(|_| AssembleError::Window)?;
        let roots = config.roots.iter().map(|r| r.path.clone()).collect();
        seam = Some((roots, window, parts.clock));
        Ok(parts)
    })
    .map_err(|error| match error {
        BuildError::Start(start) => AssembleError::Start(start),
        BuildError::Build(inner) => inner,
    })?;
    let (roots, window, clock) = seam.ok_or(AssembleError::Window)?;
    Ok(Wired {
        daemon,
        roots,
        window,
        clock,
    })
}

/// Start a daemon from `root` (the state root) and `config_path` alone.
///
/// The config is read once. The lock is taken first; the token file is read only after it, and
/// no network call is made here. An empty `prefix` is the bucket root and is accepted.
///
/// # Errors
/// An [`AssembleError`]; the lock is released on every refusal.
pub fn start_from_config(
    root: &Path,
    config_path: &Path,
) -> Result<ProductionDaemon, AssembleError> {
    start_wired(root, config_path).map(|wired| wired.daemon)
}
