//! The two adapters of the event loop: the watcher as [`Inputs`] and the daemon as [`Work`]
//! (docs/rust-rewrite/implementation/29-event-loop.md, LOOP-2b).
use crate::assemble::ProductionDaemon;
use crate::driver::{Cause, Inputs, Next, Route, Work};
use crate::run::Failed;
use crate::watch::{FsWatcher, Received, StopFlag};
use memory_core::coalescer::Signal;
use memory_core::domain::Owner;
use std::path::{Path, PathBuf};
use std::time::Duration;

/// The watcher's inbox as the loop's input source.
pub struct WatchInputs {
    watcher: FsWatcher,
    flag: StopFlag,
}

impl WatchInputs {
    /// Wrap `watcher`; `flag` is the read-only stop view of the same inbox.
    #[must_use]
    pub const fn new(watcher: FsWatcher, flag: StopFlag) -> Self {
        Self { watcher, flag }
    }
}

impl Inputs for WatchInputs {
    fn next(&mut self, up_to: Duration) -> Next {
        match self.watcher.recv_timeout(up_to) {
            Received::Stop => Next::Stop,
            Received::Failed => Next::Failed,
            Received::Input(input) => Next::Input(input),
            Received::Timeout => Next::Timeout,
        }
    }

    fn stop_requested(&mut self) -> bool {
        self.flag.requested()
    }
}

/// The spellings of the hint directory a path may carry: as configured and canonical (FSEvents
/// reports canonical paths). Computed once.
#[must_use]
pub fn hint_dirs(configured: &Path) -> Vec<PathBuf> {
    let mut dirs = vec![configured.to_path_buf()];
    if let Ok(canonical) = std::fs::canonicalize(configured)
        && !dirs.contains(&canonical)
    {
        dirs.push(canonical);
    }
    dirs
}

/// Classify a changed path: under a hint directory spelling is [`Route::Hints`]; otherwise
/// `note` decides between an owner and nothing.
pub fn route(
    hint_dirs: &[PathBuf],
    path: &Path,
    note: impl FnOnce(&Path) -> Option<Owner>,
) -> Route {
    if hint_dirs.iter().any(|dir| path.starts_with(dir)) {
        return Route::Hints;
    }
    note(path).map_or(Route::Nothing, Route::Owner)
}

/// The daemon as the loop's work. Holds the daemon until [`Work::shutdown`] consumes it.
pub struct DaemonWork {
    daemon: Option<ProductionDaemon>,
    hint_dirs: Vec<PathBuf>,
}

impl DaemonWork {
    /// Wrap `daemon`; `hints` is the configured hint directory.
    #[must_use]
    pub fn new(daemon: ProductionDaemon, hints: &Path) -> Self {
        Self {
            daemon: Some(daemon),
            hint_dirs: hint_dirs(hints),
        }
    }
}

impl Work for DaemonWork {
    fn classify(&mut self, path: &Path) -> Route {
        let Some(daemon) = self.daemon.as_mut() else {
            return Route::Nothing;
        };
        route(&self.hint_dirs, path, |p| daemon.note_path(p))
    }

    fn hints(&mut self) -> Vec<(Owner, Signal)> {
        self.daemon.as_mut().map_or_else(Vec::new, |daemon| {
            daemon
                .refresh_hints()
                .into_iter()
                .map(|hint| (hint.owner, hint.signal))
                .collect()
        })
    }

    fn capture(&mut self, owner: &Owner) -> Result<(), Failed> {
        let daemon = self.daemon.as_mut().ok_or(Failed)?;
        let outcome = daemon.capture_owner(owner).map_err(|_| Failed)?;
        crate::log::capture_outcome(owner, outcome);
        Ok(())
    }

    fn reconcile(&mut self, cause: Cause) -> Result<(), Failed> {
        let daemon = self.daemon.as_mut().ok_or(Failed)?;
        let report = daemon.reconcile_once().map_err(|_| Failed)?;
        crate::log::round(cause, &report);
        Ok(())
    }

    fn next_retry(&self) -> Option<Duration> {
        self.daemon.as_ref().and_then(ProductionDaemon::next_retry)
    }

    fn retries_due(&mut self, _now: Duration) -> Vec<Owner> {
        self.daemon
            .as_ref()
            .map_or_else(Vec::new, ProductionDaemon::retries_due)
    }

    fn shutdown(&mut self) -> Result<(), Failed> {
        self.daemon
            .take()
            .ok_or(Failed)?
            .shutdown()
            .map_err(|_| Failed)
    }
}
