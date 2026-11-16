//! Watcher (slice 2; DESIGN.md sections 7.1, 10.1 and 10.2).
//!
//! Two layers. [`Scheduler`] is pure: it consumes [`Input`] values with a caller-supplied
//! monotonic time, drives the memory-core [`Coalescer`] and returns closed [`Action`]s. It
//! never reads a clock. [`FsWatcher`] is a thin shell over the notify recommended watcher
//! that translates notify events into [`Input`] values on an bounded `std::sync::mpsc` queue.
//! Neither is wired into the daemon loop yet.
use memory_core::coalescer::Coalescer;
use memory_core::coalescer::{CoalescerError, Signal, WakeAction, Window, wake_action};
use memory_core::domain::Owner;
use notify::event::{CreateKind, ModifyKind};
use notify::{Event, EventKind, RecommendedWatcher, RecursiveMode, Watcher};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::mpsc::{Receiver, SyncSender, TrySendError, sync_channel};
use std::time::{Duration, Instant};

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

/// Translate one notify result into scheduler inputs, asking the filesystem whether a
/// created or renamed-in path is a directory.
pub fn translate(result: notify::Result<Event>) -> Vec<Input> {
    translate_with(result, is_real_dir)
}

/// A real directory. A symlink, even to a directory, is not one: the walk never follows
/// symlinks (DESIGN.md 7.1, 8.2).
fn is_real_dir(path: &Path) -> bool {
    std::fs::symlink_metadata(path).is_ok_and(|m| m.is_dir())
}

/// [`translate`] with the directory test injected, so the mapping is pure.
///
/// A directory that appears must become [`Input::DirCreated`] so its existing children are
/// read (DESIGN.md 10.2). FSEvents reports a directory moved into a root as one rename
/// event with no create flag and none for its children (W4 Q3: 1000 of 1000 children
/// unreported), and notify's rename kinds carry no is-dir bit, so `is_dir` decides. A rename
/// or create of a file, or a rename-from of a path that no longer exists, stays
/// [`Input::PathChanged`].
pub fn translate_with(result: notify::Result<Event>, is_dir: impl Fn(&Path) -> bool) -> Vec<Input> {
    let Ok(event) = result else {
        return vec![Input::WatcherError];
    };
    if event.need_rescan() {
        return vec![Input::Rescan];
    }
    let appears = matches!(
        event.kind,
        EventKind::Create(_) | EventKind::Modify(ModifyKind::Name(_))
    );
    match event.kind {
        EventKind::Any | EventKind::Create(_) | EventKind::Modify(_) | EventKind::Remove(_) => {
            event
                .paths
                .into_iter()
                .map(|path| {
                    if matches!(event.kind, EventKind::Create(CreateKind::Folder))
                        || (appears && is_dir(&path))
                    {
                        Input::DirCreated { path }
                    } else {
                        Input::PathChanged { path }
                    }
                })
                .collect()
        }
        EventKind::Access(_) | EventKind::Other => Vec::new(),
    }
}

/// Capacity of the watcher's input queue. Far above the 15 events a quiet host produces
/// per second; a burst beyond it is an overflow, handled as a lost-event rescan.
const INBOX_CAPACITY: usize = 65_536;

/// What travels on the queue: a real input, or a content-free wake for a blocked receiver.
enum Item {
    Input(Input),
    Wake,
}

/// State shared by both halves and every stop handle.
struct Shared {
    /// Set when an input was dropped because the queue was full.
    overflow: AtomicBool,
    /// Set by a stop request. Sticky.
    stop: AtomicBool,
    /// Set when the last stop handle was dropped. Sticky.
    failed: AtomicBool,
    /// Live stop handles.
    handles: AtomicUsize,
}

/// Producer half of the bounded input queue. When the queue is full the input is dropped and
/// an overflow flag is set; the consumer then yields one [`Input::Rescan`], which is the
/// "producer outruns consumer, flag set" row of DESIGN.md 10.4.
#[derive(Clone)]
pub struct InboxTx {
    tx: SyncSender<Item>,
    shared: Arc<Shared>,
}

/// Consumer half of the bounded input queue.
pub struct InboxRx {
    rx: Receiver<Item>,
    wake: SyncSender<Item>,
    shared: Arc<Shared>,
}

/// What a receive returned, in the order it is checked.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Received {
    /// A stop was requested. Sticky: every later receive reports it first.
    Stop,
    /// Every stop handle was dropped without a stop: nothing can request one any more.
    /// Sticky, and reported after [`Received::Stop`].
    Failed,
    /// The next input; a pending overflow comes first as [`Input::Rescan`].
    Input(Input),
    /// Nothing arrived within the timeout.
    Timeout,
}

/// Producer-side handle that requests a stop of the receiver. Dropping the last handle without
/// a stop makes the receiver report [`Received::Failed`].
pub struct StopHandle {
    tx: SyncSender<Item>,
    shared: Arc<Shared>,
}

/// Read-only view of the stop request. Not counted as a stop source: holding one does not
/// hide a failed source, it cannot request a stop, and dropping it has no effect.
#[derive(Clone)]
pub struct StopFlag {
    shared: Arc<Shared>,
}

impl StopFlag {
    /// Whether a stop has been requested. Sticky; reads nothing from the queue.
    #[must_use]
    pub fn requested(&self) -> bool {
        self.shared.stop.load(Ordering::SeqCst)
    }
}

/// A bounded queue of watcher inputs with overflow reported as [`Input::Rescan`].
pub fn inbox(capacity: usize) -> (InboxTx, InboxRx) {
    let (tx, rx) = sync_channel(capacity);
    let shared = Arc::new(Shared {
        overflow: AtomicBool::new(false),
        stop: AtomicBool::new(false),
        failed: AtomicBool::new(false),
        handles: AtomicUsize::new(0),
    });
    (
        InboxTx {
            tx: tx.clone(),
            shared: Arc::clone(&shared),
        },
        InboxRx {
            rx,
            wake: tx,
            shared,
        },
    )
}

impl InboxTx {
    /// Queue an input, or record an overflow when the queue is full.
    pub fn push(&self, input: Input) {
        if let Err(TrySendError::Full(_)) = self.tx.try_send(Item::Input(input)) {
            self.shared.overflow.store(true, Ordering::SeqCst);
        }
    }
}

impl StopHandle {
    /// Request a stop: set the flag first, then wake a blocked receiver. If the queue is full
    /// the wake is not sent; the receiver is not blocked then, because the queue is non-empty.
    pub fn stop(&self) {
        self.shared.stop.store(true, Ordering::SeqCst);
        let _ = self.tx.try_send(Item::Wake);
    }
}

impl Clone for StopHandle {
    fn clone(&self) -> Self {
        self.shared.handles.fetch_add(1, Ordering::SeqCst);
        Self {
            tx: self.tx.clone(),
            shared: Arc::clone(&self.shared),
        }
    }
}

impl Drop for StopHandle {
    fn drop(&mut self) {
        if self.shared.handles.fetch_sub(1, Ordering::SeqCst) == 1 {
            self.shared.failed.store(true, Ordering::SeqCst);
            let _ = self.tx.try_send(Item::Wake);
        }
    }
}

impl InboxRx {
    /// A new handle that can request a stop of this receiver. The receiver reports failure
    /// once every handle handed out has been dropped (and stays failed).
    #[must_use]
    pub fn stop_handle(&self) -> StopHandle {
        self.shared.handles.fetch_add(1, Ordering::SeqCst);
        StopHandle {
            tx: self.wake.clone(),
            shared: Arc::clone(&self.shared),
        }
    }

    /// A read-only view of the stop request; see [`StopFlag`].
    #[must_use]
    pub fn stop_flag(&self) -> StopFlag {
        StopFlag {
            shared: Arc::clone(&self.shared),
        }
    }

    /// Wait up to `timeout`. Order: stop, failed stop source, a pending overflow (one
    /// [`Input::Rescan`]), queued inputs, timeout.
    ///
    /// After a stop nothing else is delivered here: the overflow flag and the queue are left
    /// as they are, because the loop shuts down. A caller that wants to keep draining anyway
    /// uses [`InboxRx::recv_ignoring_stop`].
    pub fn recv_timeout(&self, timeout: Duration) -> Received {
        self.receive(timeout, true)
    }

    /// As [`InboxRx::recv_timeout`] but never reports stop or failure: overflow, queued
    /// inputs, timeout.
    pub fn recv_ignoring_stop(&self, timeout: Duration) -> Received {
        self.receive(timeout, false)
    }

    fn receive(&self, timeout: Duration, honour_stop: bool) -> Received {
        let start = Instant::now();
        loop {
            if honour_stop {
                if self.shared.stop.load(Ordering::SeqCst) {
                    return Received::Stop;
                }
                if self.shared.failed.load(Ordering::SeqCst) {
                    return Received::Failed;
                }
            }
            if self.shared.overflow.swap(false, Ordering::SeqCst) {
                return Received::Input(Input::Rescan);
            }
            match self
                .rx
                .recv_timeout(timeout.saturating_sub(start.elapsed()))
            {
                Ok(Item::Input(input)) => return Received::Input(input),
                Ok(Item::Wake) => {}
                Err(_) => return Received::Timeout,
            }
        }
    }
}

/// Recursive notify watcher over a set of roots.
pub struct FsWatcher {
    _watcher: RecommendedWatcher,
    rx: InboxRx,
    skipped: usize,
}

impl FsWatcher {
    /// Watch each existing root recursively. A root that does not exist is skipped and
    /// counted ([`FsWatcher::skipped_roots`]); the safety reconcile finds the directory
    /// once it exists (amendment A13.6). With every root missing the watcher still starts
    /// and watches nothing.
    ///
    /// # Errors
    /// Returns the notify error if the watcher cannot be created or a root that exists
    /// cannot be watched (permission denied, not a directory). Only a missing root is skipped.
    pub fn new(roots: &[PathBuf]) -> notify::Result<Self> {
        Self::build(roots, None)
    }

    /// As [`FsWatcher::new`], and also watch `dir` non-recursively (the hint directory).
    /// Events there arrive as ordinary inputs with paths under `dir`.
    ///
    /// # Errors
    /// As [`FsWatcher::new`]; and `dir` must exist and be a directory (a missing `dir` is
    /// an error, not skipped).
    pub fn with_dir(roots: &[PathBuf], dir: &Path) -> notify::Result<Self> {
        Self::build(roots, Some(dir))
    }

    fn build(roots: &[PathBuf], extra: Option<&Path>) -> notify::Result<Self> {
        if let Some(dir) = extra {
            match std::fs::metadata(dir) {
                Ok(meta) if meta.is_dir() => {}
                Ok(_) => {
                    return Err(notify::Error::generic("watch directory is not a directory")
                        .add_path(dir.to_path_buf()));
                }
                Err(io) => return Err(notify::Error::io(io).add_path(dir.to_path_buf())),
            }
        }
        let (tx, rx) = inbox(INBOX_CAPACITY);
        let mut watcher = notify::recommended_watcher(move |result| {
            for input in translate(result) {
                tx.push(input);
            }
        })?;
        let mut skipped = 0;
        for root in roots {
            // FSEvents accepts a regular file as a root, so check the kind ourselves
            // (following a symlinked root, as the operating system does for the watch).
            match std::fs::metadata(root) {
                Ok(meta) if meta.is_dir() => {}
                Ok(_) => {
                    return Err(notify::Error::generic("watch root is not a directory")
                        .add_path(root.clone()));
                }
                Err(io) if io.kind() == std::io::ErrorKind::NotFound => {
                    skipped += 1;
                    continue;
                }
                Err(io) => return Err(notify::Error::io(io).add_path(root.clone())),
            }
            watcher.watch(root, RecursiveMode::Recursive)?;
        }
        if let Some(dir) = extra {
            watcher.watch(dir, RecursiveMode::NonRecursive)?;
        }
        Ok(Self {
            _watcher: watcher,
            rx,
            skipped,
        })
    }

    /// How many configured roots did not exist when the watcher started.
    #[must_use]
    pub fn skipped_roots(&self) -> usize {
        self.skipped
    }

    /// A read-only view of the stop request of this watcher's inbox.
    #[must_use]
    pub fn stop_flag(&self) -> StopFlag {
        self.rx.stop_flag()
    }

    /// A handle that requests a stop of this watcher's inbox.
    #[must_use]
    pub fn stop_handle(&self) -> StopHandle {
        self.rx.stop_handle()
    }

    /// Wait up to `timeout` for the next input; see [`InboxRx::recv_timeout`].
    pub fn recv_timeout(&self, timeout: Duration) -> Received {
        self.rx.recv_timeout(timeout)
    }
}
