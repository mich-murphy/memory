//! Watcher (slice 2; DESIGN.md sections 7.1, 10.1 and 10.2).
//!
//! Two layers. [`Scheduler`] is pure: it consumes [`Input`] values with a caller-supplied
//! monotonic time, drives the memory-core [`Coalescer`] and returns closed [`Action`]s. It
//! never reads a clock. [`FsWatcher`] is a thin shell over the notify recommended watcher
//! that translates notify events into [`Input`] values on an `std::sync::mpsc` channel.
//! Neither is wired into the daemon loop yet.
use memory_core::coalescer::Coalescer;
use memory_core::coalescer::{CoalescerError, Signal, WakeAction, Window, wake_action};
use memory_core::domain::Owner;
use notify::{Event, EventKind, RecommendedWatcher, RecursiveMode, Watcher, event::CreateKind};
use std::path::{Path, PathBuf};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Duration;

/// Everything the scheduler reacts to.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Input {
    /// A file or entry changed under a watched root.
    PathChanged {
        /// Absolute changed path.
        path: PathBuf,
    },
    /// A directory was created; its existing children produce no events (DESIGN.md 10.2).
    DirCreated {
        /// Absolute directory path.
        path: PathBuf,
    },
    /// The platform reported lost events.
    Rescan,
    /// The watcher reported an error.
    WatcherError,
    /// A hook hint for an owner.
    Hint {
        /// Owner the hint resolved to.
        owner: Owner,
        /// Signal semantics.
        signal: Signal,
    },
    /// The 60 second wake tick. `mono` is the monotonic time used for this tick.
    Tick {
        /// Wall clock reading.
        wall: Duration,
        /// Monotonic clock reading.
        mono: Duration,
    },
}

/// Work the shell must perform.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Action {
    /// Start one capture for this owner; the shell must call [`Scheduler::complete`].
    Capture {
        /// Owner to capture.
        owner: Owner,
    },
    /// Read this directory at once.
    ReadDir {
        /// Directory to read.
        path: PathBuf,
    },
    /// Run a full reconcile of every root.
    ReconcileAll,
}

/// Pure scheduler over per-owner coalescing. `map` resolves a path to its owner.
pub struct Scheduler<F: FnMut(&Path) -> Option<Owner>> {
    coalescer: Coalescer,
    map: F,
    owners: Vec<Owner>,
    last_tick: Option<(Duration, Duration)>,
    unmapped: u64,
    clock_errors: u64,
}

impl<F: FnMut(&Path) -> Option<Owner>> Scheduler<F> {
    /// Create a scheduler.
    pub fn new(window: Window, map: F) -> Self {
        Self {
            coalescer: Coalescer::new(window),
            map,
            owners: Vec::new(),
            last_tick: None,
            unmapped: 0,
            clock_errors: 0,
        }
    }

    /// Paths that mapped to no owner and were ignored.
    pub fn unmapped(&self) -> u64 {
        self.unmapped
    }

    /// Signals or polls dropped because an owner's monotonic clock regressed.
    pub fn clock_errors(&self) -> u64 {
        self.clock_errors
    }

    /// Apply one input at monotonic time `now` (ignored for `Tick`, which carries `mono`).
    pub fn handle(&mut self, input: Input, now: Duration) -> Vec<Action> {
        let mut actions = Vec::new();
        let now = match input {
            Input::Tick { mono, .. } => mono,
            _ => now,
        };
        match input {
            Input::PathChanged { path } => match (self.map)(&path) {
                Some(owner) => self.signal(owner, now, Signal::Changed),
                None => self.unmapped += 1,
            },
            Input::DirCreated { path } => actions.push(Action::ReadDir { path }),
            Input::Rescan | Input::WatcherError => actions.push(Action::ReconcileAll),
            Input::Hint { owner, signal } => self.signal(owner, now, signal),
            Input::Tick { wall, mono } => {
                if let Some((last_wall, last_mono)) = self.last_tick
                    && wake_action(
                        wall.saturating_sub(last_wall),
                        mono.saturating_sub(last_mono),
                    ) != WakeAction::None
                {
                    actions.push(Action::ReconcileAll);
                }
                self.last_tick = Some((wall, mono));
            }
        }
        self.drain(now, &mut actions);
        actions
    }

    /// Mark the owner's in-flight capture finished and emit any follow-up that is due.
    ///
    /// # Errors
    /// `NotInFlight` for an unmatched completion, `Backwards` for clock regression.
    pub fn complete(
        &mut self,
        owner: &Owner,
        now: Duration,
    ) -> Result<Vec<Action>, CoalescerError> {
        self.coalescer.complete(owner, now)?;
        let mut actions = Vec::new();
        self.drain(now, &mut actions);
        Ok(actions)
    }

    fn signal(&mut self, owner: Owner, now: Duration, signal: Signal) {
        if !self.owners.contains(&owner) {
            self.owners.push(owner.clone());
        }
        if self.coalescer.signal(owner, now, signal).is_err() {
            self.clock_errors += 1;
        }
    }

    fn drain(&mut self, now: Duration, actions: &mut Vec<Action>) {
        for owner in &self.owners {
            match self.coalescer.poll(owner, now) {
                Ok(true) => actions.push(Action::Capture {
                    owner: owner.clone(),
                }),
                Ok(false) => {}
                Err(_) => self.clock_errors += 1,
            }
        }
    }
}

/// Translate one notify result into scheduler inputs.
pub fn translate(result: notify::Result<Event>) -> Vec<Input> {
    let Ok(event) = result else {
        return vec![Input::WatcherError];
    };
    if event.need_rescan() {
        return vec![Input::Rescan];
    }
    match event.kind {
        EventKind::Create(CreateKind::Folder) => event
            .paths
            .into_iter()
            .map(|path| Input::DirCreated { path })
            .collect(),
        EventKind::Any | EventKind::Create(_) | EventKind::Modify(_) | EventKind::Remove(_) => {
            event
                .paths
                .into_iter()
                .map(|path| Input::PathChanged { path })
                .collect()
        }
        EventKind::Access(_) | EventKind::Other => Vec::new(),
    }
}

/// Recursive notify watcher over a set of roots.
pub struct FsWatcher {
    _watcher: RecommendedWatcher,
    rx: Receiver<Input>,
}

impl FsWatcher {
    /// Watch each root recursively.
    ///
    /// # Errors
    /// Returns the notify error if the watcher or any root watch cannot be created.
    pub fn new(roots: &[PathBuf]) -> notify::Result<Self> {
        let (tx, rx): (Sender<Input>, Receiver<Input>) = channel();
        let mut watcher = notify::recommended_watcher(move |result| {
            for input in translate(result) {
                let _ = tx.send(input);
            }
        })?;
        for root in roots {
            watcher.watch(root, RecursiveMode::Recursive)?;
        }
        Ok(Self {
            _watcher: watcher,
            rx,
        })
    }

    /// Wait up to `timeout` for the next input.
    pub fn recv_timeout(&self, timeout: Duration) -> Option<Input> {
        self.rx.recv_timeout(timeout).ok()
    }
}
