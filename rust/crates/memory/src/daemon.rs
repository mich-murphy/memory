//! Daemon skeleton (slice 2; DESIGN.md 9, 10.3, 11.2-11.3, 12). Synchronous, generic over the
//! [`FileSystem`] and [`Store`] seams and over source enumeration. There is no real
//! filesystem adapter, no real store, no notify wiring and no coalescer timing here.
//!
//! Startup order (each step refuses with a closed [`StartError`]):
//! 1. [`DaemonLock::acquire`] first (12): nothing else is touched before the lock.
//! 2. [`StateDir::prepare`] (9.1): sweep `tmp/` and stale temp files, learn the uid.
//! 3. [`StateDir::vet_hints_dir`] (9.1).
//! 4. [`Config::load`]: an invalid config refuses to start.
//! 5. Read `index.json` (9.2): a missing or damaged index is rebuilt (9.3): locals start
//!    empty, the commit base is recovered per owner from the store, capture adopts.
//! 6. Read `status.json`: parked owners stay parked across restarts. A damaged or newer-schema
//!    file is treated as missing (9.2); the result is kept as a [`StateLoad`] on the daemon
//!    and logged by `run.rs` as a `state_load` event.
//!
//! The commit base (`current.json` ETag, owner state) is never persisted: every owner
//! recovers it from the store on its first round after a start (`recover_base`).
use crate::audit::AuditCounts;
use crate::capture::{
    Candidate, Captured, Env, Local, Locals, MAX_PIECE_BYTES, Source, SourceOutcome, Update,
    absent_into, attach_launches, capture_into,
};
use crate::commit::{
    Base, Commit, CommitEnd, Failure, KnownOurs, Landed, StoreSink, Winners, commit_staged,
    disposition_paced, recover_base,
};
use crate::config::{Config, ConfigError};
use crate::environment::{Clock, FileSystem, Store};
use crate::hook::utc_day;
use crate::state::{
    DaemonLock, Loaded, MissingReason, StateDir, StateError, StateFile, SyncPolicy, WRITER_VERSION,
};
use memory_core::coalescer::Signal;
use memory_core::disposition::Streak;
use memory_core::domain::{
    Agent, Disposition, ErrorClass, Owner, OwnerParkReason, ParkReason, RulesDigest,
    SnapshotFingerprint, SourceClass, SourceId, SourceState,
};
use memory_core::hint::{
    HintEvent, HintScan, LaunchHint, PathHint, RetentionFacts, past_retention, removable, scan,
};
use memory_core::manifest::{Change, Header, ManifestError, OwnerState, Timestamp, Writer};
use memory_core::pause::pause_for;
use memory_core::privacy::{PolicyContext, SanitizedLaunch, sanitize_launch};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, HashMap};
use std::ops::ControlFlow;
use std::path::{Path, PathBuf};
use std::time::Duration;

/// The derived index is written at most this often while dirty (DESIGN.md 9.2).
pub const INDEX_INTERVAL: Duration = Duration::from_secs(60);
/// Exit code of `memory daemon` for a config refusal.
pub const EXIT_CONFIG: u8 = 2;
const MAX_HINT_FILE: u64 = 64 << 20;

/// Why startup refused. Closed; no paths or values.
#[derive(Debug)]
pub enum StartError {
    /// Lock, state directory or hints directory refusal.
    State(StateError),
    /// Invalid or unreadable config, or a bad token file.
    Config(ConfigError),
}

impl StartError {
    /// Fixed machine-readable code.
    #[must_use]
    pub fn code(&self) -> &'static str {
        match self {
            Self::State(error) => error.code(),
            Self::Config(error) => error.code(),
        }
    }
    /// Process exit code: the state code (75 for a held lock) or [`EXIT_CONFIG`].
    #[must_use]
    pub fn exit_code(&self) -> u8 {
        match self {
            Self::State(error) => error.exit_code(),
            Self::Config(_) => EXIT_CONFIG,
        }
    }
}

/// Why [`Daemon::start_built`] refused.
#[derive(Debug)]
pub enum BuildError<B> {
    /// Startup itself refused.
    Start(StartError),
    /// The caller's builder refused.
    Build(B),
}

/// Failure of one round.
#[derive(Debug)]
pub enum RoundError {
    /// The index or status could not be written.
    State(StateError),
    /// The wall clock cannot be rendered as a manifest timestamp.
    Clock,
}

/// One source's persisted local knowledge.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SourceIndex {
    /// Append index.
    pub index: Option<SourceState>,
    /// Consecutive absences.
    pub missing: u32,
    /// Last snapshot fingerprint and rules.
    pub snapshot: Option<(SnapshotFingerprint, Option<RulesDigest>)>,
    /// A deletion was committed for the current absence.
    pub deletion_recorded: bool,
}

/// `index.json` body (schema `memory.index/1`): a derived cache.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct IndexBody {
    /// Local knowledge per source.
    pub sources: BTreeMap<SourceId, SourceIndex>,
    /// Hint day file name to the offset consumed so far.
    pub hints: BTreeMap<String, u64>,
}

/// Why an owner or source is parked.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum ParkedReason {
    /// Owner-wide.
    Owner(OwnerParkReason),
    /// Repeated deterministic source failure.
    Source(ParkReason),
}

/// One parked owner in the status file.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ParkedEntry {
    /// The parked owner.
    pub owner: Owner,
    /// Why.
    pub reason: ParkedReason,
    /// The parked source; absent when the whole owner is parked.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source: Option<SourceId>,
}

/// `status.json` body (schema `memory.status/1`). A file written by an earlier build, which had
/// an `events` object, fails `deny_unknown_fields` and loads as [`StateLoad::Damaged`].
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusBody {
    /// Manifest-style UTC timestamp of the last round.
    pub updated_at: String,
    /// Rounds run since the status file was first created.
    pub rounds: u64,
    /// Uploads are paused by a host-scoped failure (auth).
    pub host_paused: bool,
    /// Parked owners; they are skipped until an operator clears them.
    pub parked: Vec<ParkedEntry>,
    /// Failed attempts by error class since the status file was first created.
    #[serde(default)]
    pub failures: FailureCounts,
    /// Audit verdicts since the status file was first created.
    #[serde(default)]
    pub audit: AuditCounts,
}

/// How a state file read at startup came out (DESIGN.md 9.2). Closed; no content or path.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StateLoad {
    /// Verified and parsed.
    Loaded,
    /// The file does not exist (a first start).
    Missing,
    /// Present but unusable: unreadable, failed its checksum, short or extra bytes, bad header,
    /// unknown envelope version, or a verified body that does not parse.
    Damaged,
    /// Written by a build with a newer schema than this one understands.
    NewerSchema,
}

/// Failed attempts by [`ErrorClass`] (DESIGN.md 11.2, 11.3): `transient` is distinct from
/// `throttled`.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FailureCounts {
    /// Connection, timeout and ambiguous failures.
    pub offline: u64,
    /// 429 and 503 replies.
    pub throttled: u64,
    /// `BaseUnchanged` through every rebase retry.
    pub transient: u64,
    /// Authorization failures.
    pub destination_auth: u64,
    /// Local resource failures.
    pub local: u64,
    /// Deterministic failures.
    pub deterministic: u64,
}

impl FailureCounts {
    fn bump(&mut self, class: &ErrorClass) {
        let n = match class {
            ErrorClass::Offline => &mut self.offline,
            ErrorClass::Throttled => &mut self.throttled,
            ErrorClass::Transient => &mut self.transient,
            ErrorClass::DestinationAuth => &mut self.destination_auth,
            ErrorClass::Local(_) => &mut self.local,
            ErrorClass::Deterministic(_) => &mut self.deterministic,
        };
        *n = n.saturating_add(1);
    }
    fn add(&mut self, other: &Self) {
        self.offline = self.offline.saturating_add(other.offline);
        self.throttled = self.throttled.saturating_add(other.throttled);
        self.transient = self.transient.saturating_add(other.transient);
        self.destination_auth = self.destination_auth.saturating_add(other.destination_auth);
        self.local = self.local.saturating_add(other.local);
        self.deterministic = self.deterministic.saturating_add(other.deterministic);
    }
}

/// Counts of one round.
#[derive(Debug, Default, Clone, Copy, PartialEq, Eq)]
pub struct RoundReport {
    /// Owners whose commit landed.
    pub landed: usize,
    /// Owners with a retryable failure.
    pub failed: usize,
    /// Owners skipped because they are parked.
    pub skipped: usize,
    /// Owners or sources parked by this round.
    pub parked: usize,
    /// Owners skipped because their retry delay has not elapsed.
    pub deferred: usize,
    /// Owners whose capture a stop request ended before `current.json` was touched. Not a
    /// failure: no retry deadline, no streak change. Not logged and not stored.
    pub stopped: usize,
    /// Failures of this round by class.
    pub failures: FailureCounts,
    /// Audit verdicts reached in this round, whether or not the commits landed.
    pub audit: AuditCounts,
    /// Hint day files removed by this round's retention sweep.
    pub hints_removed: usize,
    /// Hint day files past the 7-day age that were kept (pending records, torn tail, cut
    /// read or a failed removal).
    pub hints_kept_old: usize,
    /// Deletions in commits that landed in this round.
    pub deleted: usize,
    /// Absence candidates not probed: unknown namespace, bad path or missing root.
    pub absence_skipped: usize,
}

/// How an absence candidate can be probed ([`Sources::locate_absent`]).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Probe {
    /// Stat this handle through the filesystem seam.
    Handle(u64),
    /// Not probed: unknown namespace, unparsable path, a root that is not a directory, or no
    /// resolver. Never counts toward deletion.
    Skip,
}

/// Monotonic time between `progress` log lines of a running round.
const PROGRESS_EVERY_NS: u64 = 60_000_000_000;

/// Source enumeration, supplied by the shell.
pub trait Sources {
    /// All currently known sources with their owner.
    fn enumerate(&mut self) -> Vec<(Owner, Source)>;
    /// Remember a path an event named and return its owner, or `None` when the path is not a
    /// source of a known root. Reads no file content. Default: nothing is recognised.
    fn note(&mut self, _path: &Path) -> Option<Owner> {
        None
    }
    /// Read one directory an event named: note each append source found under it and return
    /// the owners, and whether the bounded walk was cut short. Reads no file content. Default:
    /// nothing is found.
    fn read_dir(&mut self, _path: &Path) -> (Vec<Owner>, bool) {
        (Vec::new(), false)
    }
    /// Whether the last [`Sources::enumerate`] saw everything it could: not cut short by its
    /// entry bound and not refused as busy. Absence is only observed after a complete walk.
    /// Default: complete.
    fn walk_complete(&self) -> bool {
        true
    }
    /// Resolve a stored source (id and root-relative path) that the walk did not yield to a
    /// handle for a `stat`-only absence probe. Default: [`Probe::Skip`].
    fn locate_absent(&mut self, _id: &SourceId, _path: &str) -> Probe {
        Probe::Skip
    }
    /// The current sources of one owner. Default: [`Sources::enumerate`] filtered.
    fn of(&mut self, owner: &Owner) -> Vec<Source> {
        self.enumerate()
            .into_iter()
            .filter(|(o, _)| o == owner)
            .map(|(_, s)| s)
            .collect()
    }
}
impl<T: FnMut() -> Vec<(Owner, Source)>> Sources for T {
    fn enumerate(&mut self) -> Vec<(Owner, Source)> {
        self()
    }
}

/// How one per-owner capture ended ([`Daemon::capture_owner`]).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OwnerOutcome {
    /// A commit landed.
    Landed,
    /// Nothing to commit.
    Unchanged,
    /// The capture failed, or the owner or a source was parked by it.
    Failed,
    /// The owner's retry deadline has not passed.
    Deferred,
    /// The owner is parked.
    Skipped,
    /// The host is paused; nothing was attempted.
    Paused,
    /// A stop request ended the capture before `current.json` was touched.
    Stopped,
}

/// A hint record not signalled before: its owner, the signal it carries and the paths it names.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HintSignal {
    /// The session's owner.
    pub owner: Owner,
    /// [`Signal::SessionEnd`] for a flushing event, else [`Signal::Stop`].
    pub signal: Signal,
    /// Transcript and subagent transcript paths the record names (hints only).
    pub paths: Vec<PathBuf>,
}

/// Everything the daemon drives.
pub struct Parts<F, S, C, E> {
    /// Filesystem seam.
    pub fs: F,
    /// Object store; uploads take `environment::Published`.
    pub store: S,
    /// Clock seam.
    pub clock: C,
    /// Source enumeration.
    pub sources: E,
    /// Policy observation for capture.
    pub context: PolicyContext,
}

/// Start options that are not part of [`Parts`]. Simulation builds only (cargo feature `sim`).
#[cfg(feature = "sim")]
#[derive(Debug, Clone, Copy, Default)]
pub struct StartOptions {
    /// State write flushing; only simulation tests select [`SyncPolicy::None`].
    pub sync: SyncPolicy,
}

struct Opened {
    lock: DaemonLock,
    state: StateDir,
    config: Config,
    index: IndexBody,
    index_load: StateLoad,
    status: StatusBody,
    status_load: StateLoad,
}

/// Read one state file and parse its body; every failure is a distinct [`StateLoad`], and the
/// caller treats anything but `Loaded` as missing (DESIGN.md 9.2).
fn load_state<T: DeserializeOwned>(state: &StateDir, file: StateFile) -> (Option<T>, StateLoad) {
    match state.read(file) {
        Loaded::Present(stored) => match serde_json::from_slice(&stored.body) {
            Ok(body) => (Some(body), StateLoad::Loaded),
            Err(_) => (None, StateLoad::Damaged),
        },
        Loaded::Missing(MissingReason::NotFound) => (None, StateLoad::Missing),
        Loaded::Missing(MissingReason::NewerSchema) => (None, StateLoad::NewerSchema),
        Loaded::Missing(_) => (None, StateLoad::Damaged),
    }
}

fn open(root: &Path, config_path: &Path, sync: SyncPolicy) -> Result<Opened, StartError> {
    let lock = DaemonLock::acquire(root).map_err(StartError::State)?;
    let state = StateDir::prepare_inner(&lock, sync).map_err(StartError::State)?;
    state.vet_hints_dir().map_err(StartError::State)?;
    let config = Config::load(config_path).map_err(StartError::Config)?;
    let (index, index_load) = load_state::<IndexBody>(&state, StateFile::Index);
    let (status, status_load) = load_state::<StatusBody>(&state, StateFile::Status);
    Ok(Opened {
        lock,
        state,
        config,
        index: index.unwrap_or_default(),
        index_load,
        status: status.unwrap_or_default(),
        status_load,
    })
}

/// What the owner knows of the remote commit base.
enum RemoteBase {
    /// Not read, or possibly stale: recover before the next commit.
    Unknown,
    /// Recovered: the owner has no `current.json` yet.
    Absent,
    /// Recovered or landed: the fence for the next commit.
    Known(Base),
}

/// Whether the owner takes a turn.
enum Turn {
    /// Runs at the next round.
    Ready,
    /// Monotonic time before which the owner is not retried (from `Disposition::Retry`).
    RetryAt(Duration),
    /// Skipped until unparked.
    Parked(ParkedReason),
}

struct OwnerRt {
    base: RemoteBase,
    streak: Streak,
    turn: Turn,
    /// Manifests built since the last landed commit (A6); memory only.
    known_ours: KnownOurs,
    /// Sources parked alone; the owner's other sources keep flowing.
    parked_sources: BTreeMap<SourceId, ParkReason>,
}

impl OwnerRt {
    fn new(parked: Option<ParkedReason>) -> Self {
        Self {
            parked_sources: BTreeMap::new(),
            base: RemoteBase::Unknown,
            streak: Streak::default(),
            turn: parked.map_or(Turn::Ready, Turn::Parked),
            known_ours: KnownOurs::new(),
        }
    }
}

struct PendingHint {
    end: u64,
    event: HintEvent,
    /// The paths noted for `owner`.
    paths: Vec<PathBuf>,
    /// The owner of the transcript path; `None` when the sources give no session owner of the
    /// record's agent.
    owner: Option<Owner>,
    /// A subagent transcript's own owner and path, when it differs from `owner`.
    other: Option<(Owner, PathBuf)>,
    launch: Option<SanitizedLaunch>,
    done: bool,
}

struct ScannedFile {
    name: String,
    cut: bool,
    len: u64,
    start: u64,
    scan: HintScan,
    records: Vec<PendingHint>,
}

/// The session owner of the record's agent that the sources give a hinted path, with the path.
/// Hook input is untrusted: another agent's or a shared owner is no owner.
fn hint_owner<E: Sources>(
    sources: &mut E,
    agent: &Agent,
    hint: &PathHint,
) -> Option<(Owner, PathBuf)> {
    let PathHint::Present { path } = hint else {
        return None;
    };
    let path = PathBuf::from(path.as_str());
    let owner = sources.note(&path)?;
    match &owner {
        Owner::Session(key) if key.agent == *agent => Some((owner, path)),
        _ => None,
    }
}

fn stamp_of(wall_ns: i128) -> Result<(Timestamp, String), ManifestError> {
    let secs = u64::try_from(wall_ns / 1_000_000_000).map_err(|_| ManifestError::BadTimestamp)?;
    let text = format!(
        "{}T{:02}:{:02}:{:02}Z",
        utc_day(secs),
        secs / 3600 % 24,
        secs / 60 % 60,
        secs % 60
    );
    Timestamp::try_from(text.clone()).map(|t| (t, text))
}

fn header_of(owner: &Owner, when: &Timestamp) -> Header {
    Header {
        owner: owner.clone(),
        captured_at: when.clone(),
        writer: Writer {
            name: "memory".to_owned(),
            version: WRITER_VERSION.to_owned(),
            commit: "unknown".to_owned(),
        },
        launch: Vec::new(),
    }
}

/// The daemon: locked, prepared and loaded.
pub struct Daemon<F, S, C, E> {
    _lock: DaemonLock,
    state: StateDir,
    fs: F,
    store: S,
    clock: C,
    sources: E,
    context: PolicyContext,
    locals: Locals,
    hints: BTreeMap<String, u64>,
    files: Vec<ScannedFile>,
    seen: BTreeMap<String, u64>,
    owners: HashMap<Owner, OwnerRt>,
    status: StatusBody,
    index_load: StateLoad,
    status_load: StateLoad,
    dirty: bool,
    last_index: Option<Duration>,
    last_status: Option<Duration>,
    draws: u64,
    /// True only while a full round runs: the one place absence is observed.
    absence: bool,
    stop: Box<dyn Fn() -> bool>,
    sleep: Box<dyn FnMut(Duration)>,
}

impl<F: FileSystem, S: Store, C: Clock, E: Sources> Daemon<F, S, C, E> {
    /// Startup in the documented order.
    ///
    /// # Errors
    /// A [`StartError`]; on a held lock nothing under the state root was touched.
    pub fn start(
        root: &Path,
        config_path: &Path,
        parts: Parts<F, S, C, E>,
    ) -> Result<Self, StartError> {
        Self::start_inner(root, config_path, parts, SyncPolicy::Full)
    }

    /// As [`Daemon::start`] with explicit [`StartOptions`] (feature `sim`, tests only).
    ///
    /// # Errors
    /// As [`Daemon::start`].
    #[cfg(feature = "sim")]
    pub fn start_with(
        root: &Path,
        config_path: &Path,
        parts: Parts<F, S, C, E>,
        options: StartOptions,
    ) -> Result<Self, StartError> {
        Self::start_inner(root, config_path, parts, options.sync)
    }

    fn start_inner(
        root: &Path,
        config_path: &Path,
        parts: Parts<F, S, C, E>,
        sync: SyncPolicy,
    ) -> Result<Self, StartError> {
        Self::start_built_inner(root, config_path, sync, |_| {
            Ok::<_, std::convert::Infallible>(parts)
        })
        .map_err(|error| match error {
            BuildError::Start(start) => start,
            BuildError::Build(never) => match never {},
        })
    }

    /// As [`Daemon::start`], but the [`Parts`] are built by `build` from the validated
    /// [`Config`] once the lock is held and the config is read (once). The closure runs after
    /// the lock, the state directory, the config and the two state loads (`index.json`,
    /// `status.json`); nothing is written and no network call is made before it. If it fails,
    /// the lock is released on return.
    ///
    /// # Errors
    /// [`BuildError::Start`] as [`Daemon::start`]; [`BuildError::Build`] carries `build`'s error.
    pub fn start_built<B>(
        root: &Path,
        config_path: &Path,
        build: impl FnOnce(&Config) -> Result<Parts<F, S, C, E>, B>,
    ) -> Result<Self, BuildError<B>> {
        Self::start_built_inner(root, config_path, SyncPolicy::Full, build)
    }

    fn start_built_inner<B>(
        root: &Path,
        config_path: &Path,
        sync: SyncPolicy,
        build: impl FnOnce(&Config) -> Result<Parts<F, S, C, E>, B>,
    ) -> Result<Self, BuildError<B>> {
        let opened = open(root, config_path, sync).map_err(BuildError::Start)?;
        let parts = build(&opened.config).map_err(BuildError::Build)?;
        let locals = opened
            .index
            .sources
            .into_iter()
            .map(|(id, s)| {
                let local = Local {
                    index: s.index,
                    missing: s.missing,
                    snapshot: s.snapshot,
                    deletion_recorded: s.deletion_recorded,
                };
                (id, local)
            })
            .collect();
        let mut owners: HashMap<Owner, OwnerRt> = HashMap::new();
        for p in &opened.status.parked {
            let rt = owners
                .entry(p.owner.clone())
                .or_insert_with(|| OwnerRt::new(None));
            match (&p.source, &p.reason) {
                (Some(id), ParkedReason::Source(reason)) => {
                    rt.parked_sources.insert(id.clone(), reason.clone());
                }
                _ => rt.turn = Turn::Parked(p.reason.clone()),
            }
        }
        Ok(Self {
            _lock: opened.lock,
            state: opened.state,
            fs: parts.fs,
            store: parts.store,
            clock: parts.clock,
            sources: parts.sources,
            context: parts.context,
            locals,
            hints: opened.index.hints,
            files: Vec::new(),
            seen: BTreeMap::new(),
            owners,
            status: opened.status,
            index_load: opened.index_load,
            status_load: opened.status_load,
            dirty: false,
            last_index: None,
            last_status: None,
            draws: 0,
            absence: false,
            stop: Box::new(|| false),
            sleep: Box::new(std::thread::sleep),
        })
    }

    /// Install the stop probe (`true` means a stop was requested). It is asked before each
    /// owner of a full round and inside each commit before each piece, the manifest and the
    /// publish part. Default: never stops.
    pub fn set_stop(&mut self, probe: impl Fn() -> bool + 'static) {
        self.stop = Box::new(probe);
    }

    /// Install the sleep used for commit pauses (decision D44). Default: a real
    /// `std::thread::sleep`. The stop probe is not consulted during a pause.
    pub fn set_sleep(&mut self, sleep: impl FnMut(Duration) + 'static) {
        self.sleep = Box::new(sleep);
    }

    /// True when the index was missing, damaged or of a newer schema at startup.
    pub const fn rebuilt(&self) -> bool {
        !matches!(self.index_load, StateLoad::Loaded)
    }
    /// How `index.json` loaded at startup.
    pub const fn index_load(&self) -> StateLoad {
        self.index_load
    }
    /// How `status.json` loaded at startup. `Damaged` and `NewerSchema` mean the parked list
    /// and host pause were lost; the counters restarted and `run.rs` logs a `state_load` event.
    pub const fn status_load(&self) -> StateLoad {
        self.status_load
    }
    /// The current status body.
    pub const fn status(&self) -> &StatusBody {
        &self.status
    }
    /// The filesystem seam (tests mutate sources through it).
    pub const fn fs_mut(&mut self) -> &mut F {
        &mut self.fs
    }
    /// The store seam.
    pub const fn store_mut(&mut self) -> &mut S {
        &mut self.store
    }
    /// The clock seam.
    pub const fn clock_mut(&mut self) -> &mut C {
        &mut self.clock
    }

    /// Jitter draw for the pure backoff.
    fn next_draw(&mut self) -> u64 {
        draw(&mut self.draws, &self.clock)
    }

    fn apply(&mut self, updates: Vec<Update>) {
        self.dirty |= !updates.is_empty();
        for update in updates {
            update.apply(&mut self.locals);
        }
    }

    fn scan_hints(&mut self) -> Vec<ScannedFile> {
        let Ok(names) = self.state.list_hint_days() else {
            return Vec::new();
        };
        let mut out = Vec::new();
        for name in names {
            let Ok(read) = self.state.read_hint_day(&name, MAX_HINT_FILE) else {
                continue;
            };
            let (bytes, cut) = (read.bytes, read.cut);
            let start = self.hints.get(&name).copied().unwrap_or(0);
            // An offset past the end means the file was replaced: start over.
            let Ok((start, found)) = scan(&bytes, start)
                .map(|s| (start, s))
                .or_else(|_| scan(&bytes, 0).map(|s| (0, s)))
            else {
                continue;
            };
            let records = found
                .records
                .iter()
                .map(|r| {
                    let launch = match &r.record.launch {
                        LaunchHint::Full(l) => {
                            let env: Vec<String> =
                                l.env().iter().map(|(k, v)| format!("{k}={v}")).collect();
                            Some(sanitize_launch(l.argv(), &env))
                        }
                        LaunchHint::None | LaunchHint::DigestOnly { .. } => None,
                    };
                    let agent = &r.record.agent;
                    let main = hint_owner(&mut self.sources, agent, &r.record.transcript);
                    let sub = hint_owner(&mut self.sources, agent, &r.record.agent_transcript);
                    let (owner, paths, other) = match (main, sub) {
                        (None, _) => (None, Vec::new(), None),
                        (Some((o, p)), Some((so, sp))) if so == o => (Some(o), vec![p, sp], None),
                        (Some((o, p)), sub) => (Some(o), vec![p], sub),
                    };
                    PendingHint {
                        end: r.end,
                        event: r.record.event,
                        paths,
                        owner,
                        other,
                        done: launch.is_none(),
                        launch,
                    }
                })
                .collect();
            out.push(ScannedFile {
                name,
                cut,
                len: u64::try_from(bytes.len()).unwrap_or(u64::MAX),
                start,
                scan: found,
                records,
            });
        }
        out
    }

    fn settle(
        owner: &Owner,
        rt: &mut OwnerRt,
        result: &Result<CommitEnd, Failure>,
        at: Duration,
        now: Duration,
        draw: u64,
        report: &mut RoundReport,
    ) -> bool {
        let (disp, streak) = disposition_paced(result, false, at, &rt.streak, draw);
        rt.streak = streak;
        if let Err(failure) = result
            && (failure.base_may_be_stale() || matches!(failure, Failure::BaseUnchanged))
        {
            rt.base = RemoteBase::Unknown;
        }
        match disp {
            Disposition::Done => {
                report.landed += 1;
                rt.turn = Turn::Ready;
                false
            }
            Disposition::Retry { after, class } => {
                report.failed += 1;
                report.failures.bump(&class);
                if let Err(failure) = result {
                    crate::log::retry(
                        owner,
                        &class,
                        failure.code(),
                        matches!(failure, Failure::LandingUnknown(_)),
                        rt.streak.retries,
                        now.saturating_sub(at),
                        after,
                    );
                }
                rt.turn = Turn::RetryAt(at.saturating_add(after));
                false
            }
            Disposition::Cancelled => {
                report.stopped += 1;
                false
            }
            Disposition::PauseHost => {
                report.failed += 1;
                report.failures.bump(&ErrorClass::DestinationAuth);
                true
            }
            Disposition::Park { reason } => {
                rt.turn = Turn::Parked(ParkedReason::Source(reason));
                report.parked += 1;
                false
            }
            Disposition::ParkOwner { reason } => {
                rt.turn = Turn::Parked(ParkedReason::Owner(reason));
                report.parked += 1;
                false
            }
        }
    }

    /// The owner's turn: `Some(at)` when it should run now, else it is counted as skipped
    /// (parked) or deferred (retry delay not elapsed).
    fn due(&self, rt: &OwnerRt, report: &mut RoundReport) -> Option<Duration> {
        match &rt.turn {
            Turn::Parked(_) => {
                report.skipped += 1;
                None
            }
            Turn::Ready => Some(Duration::from_nanos(self.clock.monotonic_ns())),
            Turn::RetryAt(t) => {
                let at = Duration::from_nanos(self.clock.monotonic_ns());
                if at < *t {
                    report.deferred += 1;
                    None
                } else {
                    Some(at)
                }
            }
        }
    }

    /// Rebuild the commit base from the store when it is unknown. `Break(pause)` ends the turn
    /// with the failure settled; `pause` is true when uploads must pause.
    fn recover(
        &mut self,
        owner: &Owner,
        rt: &mut OwnerRt,
        at: Duration,
        report: &mut RoundReport,
    ) -> ControlFlow<bool, Option<Base>> {
        let recovered = match &rt.base {
            RemoteBase::Known(base) => return ControlFlow::Continue(Some(base.clone())),
            RemoteBase::Absent => return ControlFlow::Continue(None),
            RemoteBase::Unknown => recover_base(&mut self.store, owner),
        };
        match recovered {
            Ok(base) => {
                rt.base = base.clone().map_or(RemoteBase::Absent, RemoteBase::Known);
                ControlFlow::Continue(base)
            }
            Err(failure) => {
                let draw = self.next_draw();
                let now = Duration::from_nanos(self.clock.monotonic_ns());
                ControlFlow::Break(Self::settle(
                    owner,
                    rt,
                    &Err(failure),
                    at,
                    now,
                    draw,
                    report,
                ))
            }
        }
    }

    /// Capture the owner's sources against the remote state of its base.
    fn capture_sources(
        &mut self,
        owner: &Owner,
        sources: &[Source],
        base: Option<&Base>,
        when: &Timestamp,
    ) -> (Captured, Winners, Option<Failure>) {
        let env = Env {
            header: header_of(owner, when),
            context: &self.context,
            now_ns: i64::try_from(self.clock.wall_ns()).unwrap_or(i64::MAX),
            piece_len: MAX_PIECE_BYTES,
        };
        let empty = OwnerState::default();
        let remote = base.map_or(&empty, |b| &b.state);
        let stop = &self.stop;
        let mut probe = || stop();
        let mut sink = StoreSink::new(&mut self.store, &mut probe);
        let got = capture_into(&mut self.fs, sources, &self.locals, remote, &env, &mut sink);
        let staged = sink.finish();
        (got, staged.winners, staged.failed)
    }

    /// Stored live append entries of the owner that the walk did not yield and that are not
    /// parked, resolved for the absence probe, and the count of those that could not be.
    fn candidates(
        &mut self,
        walked: &[Source],
        rt: &OwnerRt,
        base: Option<&Base>,
    ) -> (Vec<Candidate>, usize) {
        let Some(base) = base else {
            return (Vec::new(), 0);
        };
        let mut found = Vec::new();
        let mut skipped = 0;
        for (id, entry) in &base.state.sources {
            if entry.deleted_at.is_some()
                || entry.class != SourceClass::Append
                || rt.parked_sources.contains_key(id)
                || walked.iter().any(|s| s.id == *id)
            {
                continue;
            }
            match self.sources.locate_absent(id, &entry.path) {
                Probe::Handle(handle) => found.push(Candidate {
                    handle,
                    id: id.clone(),
                }),
                Probe::Skip => skipped += 1,
            }
        }
        (found, skipped)
    }

    /// The `stat`-only absence probe of the candidates against the remote state of the base.
    fn absence_probe(
        &mut self,
        owner: &Owner,
        candidates: &[Candidate],
        base: Option<&Base>,
        when: &Timestamp,
    ) -> Captured {
        let env = Env {
            header: header_of(owner, when),
            context: &self.context,
            now_ns: i64::try_from(self.clock.wall_ns()).unwrap_or(i64::MAX),
            piece_len: MAX_PIECE_BYTES,
        };
        let empty = OwnerState::default();
        let remote = base.map_or(&empty, |b| &b.state);
        absent_into(&mut self.fs, candidates, &self.locals, remote, &env)
    }

    /// Attach the owner's pending launch records (DESIGN.md 7.6).
    fn attach_pending(request: &mut Commit, hints: &[ScannedFile], owner: &Owner) {
        let launches: Vec<SanitizedLaunch> = hints
            .iter()
            .flat_map(|f| f.records.iter())
            .filter(|r| !r.done && r.owner.as_ref() == Some(owner))
            .filter_map(|r| r.launch.clone())
            .collect();
        attach_launches(request, &launches);
    }

    /// Commit, then make the capture durable locally: updates and hint progress apply only once
    /// the commit has landed.
    fn land(
        &mut self,
        rt: &mut OwnerRt,
        base: Option<&Base>,
        request: Commit,
        staged: (Captured, Winners),
        hints: &mut [ScannedFile],
        owner: &Owner,
    ) -> Result<CommitEnd, Failure> {
        let (got, winners) = staged;
        let stop = &self.stop;
        let (sleep, draws, clock) = (&mut self.sleep, &mut self.draws, &self.clock);
        let result = commit_staged(
            &mut self.store,
            base,
            request,
            winners,
            &mut rt.known_ours,
            &mut |wait| sleep(pause_for(wait, draw(draws, clock))),
            &mut || stop(),
        );
        if let Ok(end) = &result {
            // Remote is authoritative: updates apply only once the commit is durable.
            // An adopted earlier manifest (A6) did not carry this capture's bytes: keep the
            // base, apply nothing, and the next round captures again.
            rt.base = RemoteBase::Known(end.base());
            if let CommitEnd::Landed(landed) = end {
                self.apply_landed(landed, got.updates, hints, owner);
            }
        }
        result
    }

    /// The local effects of a commit that landed. Taking `&Landed` is the proof that this
    /// capture's bytes are durable, and with them the owner's pending launch records.
    fn apply_landed(
        &mut self,
        _landed: &Landed,
        updates: Vec<Update>,
        hints: &mut [ScannedFile],
        owner: &Owner,
    ) {
        self.apply(updates);
        for record in hints.iter_mut().flat_map(|f| f.records.iter_mut()) {
            record.done |= record.owner.as_ref() == Some(owner);
        }
    }

    /// One owner's turn: gate, recover, capture, attach launches, commit, settle. Returns true
    /// when uploads must pause (host-scoped failure).
    fn run_owner(
        &mut self,
        owner: &Owner,
        sources: &[Source],
        rt: &mut OwnerRt,
        hints: &mut [ScannedFile],
        when: &Timestamp,
        report: &mut RoundReport,
    ) -> bool {
        let Some(at) = self.due(rt, report) else {
            return false;
        };
        let base = match self.recover(owner, rt, at, report) {
            ControlFlow::Break(pause) => return pause,
            ControlFlow::Continue(base) => base,
        };
        let (candidates, skipped) = if self.absence {
            self.candidates(sources, rt, base.as_ref())
        } else {
            (Vec::new(), 0)
        };
        report.absence_skipped += skipped;
        // Only an owner with a parked source pays for a filtered copy of its list.
        let live: Vec<Source>;
        let sources = if rt.parked_sources.is_empty() {
            sources
        } else {
            live = sources
                .iter()
                .filter(|s| !rt.parked_sources.contains_key(&s.id))
                .cloned()
                .collect();
            &live
        };
        let (mut got, winners, failed) = self.capture_sources(owner, sources, base.as_ref(), when);
        if !candidates.is_empty() {
            let gone = self.absence_probe(owner, &candidates, base.as_ref(), when);
            got.updates.extend(gone.updates);
            got.outcomes.extend(gone.outcomes);
            if let Some(extra) = gone.commit {
                match &mut got.commit {
                    Some(commit) => commit.changes.extend(extra.changes),
                    None => got.commit = Some(extra),
                }
            }
        }
        report.audit.add(&got.audit);
        // A source park takes effect whether or not the owner's commit lands.
        for (id, outcome) in &got.outcomes {
            if let SourceOutcome::Parked(reason) = outcome {
                rt.parked_sources.insert(id.clone(), reason.clone());
                report.parked += 1;
            }
        }
        if let Some(failure) = failed {
            // A piece was not stored: the owner's commit is not built (as when its upload failed).
            let draw = self.next_draw();
            let now = Duration::from_nanos(self.clock.monotonic_ns());
            return Self::settle(owner, rt, &Err(failure), at, now, draw, report);
        }
        let Some(mut request) = got.commit.take() else {
            self.apply(got.updates);
            return false;
        };
        Self::attach_pending(&mut request, hints, owner);
        let deleted = request
            .changes
            .iter()
            .filter(|c| matches!(c, Change::Deleted { .. }))
            .count();
        let result = self.land(rt, base.as_ref(), request, (got, winners), hints, owner);
        if matches!(result, Ok(CommitEnd::Landed(_))) {
            report.deleted += deleted;
        }
        let draw = self.next_draw();
        let now = Duration::from_nanos(self.clock.monotonic_ns());
        Self::settle(owner, rt, &result, at, now, draw, report)
    }

    fn advance_hints(&mut self, files: &[ScannedFile]) {
        for file in files {
            let done = file.records.iter().take_while(|r| r.done).count();
            let offset = if done == file.records.len() {
                file.scan.next_offset
            } else if done == 0 {
                file.start
            } else {
                file.records.get(done - 1).map_or(file.start, |r| r.end)
            };
            if self.hints.get(&file.name) != Some(&offset) {
                self.hints.insert(file.name.clone(), offset);
                self.dirty = true;
            }
        }
    }

    /// Retention sweep (DESIGN.md 9.1) over this round's scan, then drop offset entries of
    /// files that no longer exist. A failed removal keeps the entry and is retried next round.
    /// A removed file also leaves the scan, so a later per-owner capture cannot restore its entry.
    fn sweep_hints(
        &mut self,
        files: &mut Vec<ScannedFile>,
        wall_ns: i128,
        report: &mut RoundReport,
    ) {
        let secs = i64::try_from(wall_ns.div_euclid(1_000_000_000)).unwrap_or(-1);
        let mut removed: Vec<String> = Vec::new();
        for file in files.iter() {
            let day = file.name.strip_suffix(".jsonl").unwrap_or(&file.name);
            if !past_retention(day, secs) {
                continue;
            }
            let facts = RetentionFacts {
                whole: !file.cut,
                offset: self.hints.get(&file.name).copied().unwrap_or(0),
                next_offset: file.scan.next_offset,
                len: file.len,
                pending_bytes: file.scan.pending_bytes,
            };
            if removable(day, secs, &facts) && self.state.remove_hint_day(&file.name).is_ok() {
                self.hints.remove(&file.name);
                self.seen.remove(&file.name);
                removed.push(file.name.clone());
                self.dirty = true;
                report.hints_removed += 1;
            } else {
                report.hints_kept_old += 1;
            }
        }
        files.retain(|f| !removed.contains(&f.name));
        if let Ok(listed) = self.state.list_hint_days() {
            let before = self.hints.len();
            self.hints.retain(|name, _| listed.contains(name));
            self.dirty |= self.hints.len() != before;
        }
    }

    fn index_body(&self) -> IndexBody {
        IndexBody {
            sources: self
                .locals
                .iter()
                .map(|(id, l)| {
                    let entry = SourceIndex {
                        index: l.index.clone(),
                        missing: l.missing,
                        snapshot: l.snapshot.clone(),
                        deletion_recorded: l.deletion_recorded,
                    };
                    (id.clone(), entry)
                })
                .collect(),
            hints: self.hints.clone(),
        }
    }

    fn write_index(&mut self, force: bool) -> Result<(), StateError> {
        let now = Duration::from_nanos(self.clock.monotonic_ns());
        let due = self
            .last_index
            .is_none_or(|last| now.saturating_sub(last) >= INDEX_INTERVAL);
        if self.dirty && (force || due) {
            let body = serde_json::to_vec(&self.index_body())
                .map_err(|_| StateError::Io(std::io::ErrorKind::InvalidData))?;
            self.state.write(StateFile::Index, &body)?;
            self.dirty = false;
            self.last_index = Some(now);
        }
        Ok(())
    }

    /// One safety-reconcile round (DESIGN.md 10.3): per owner, capture then commit.
    ///
    /// # Errors
    /// [`RoundError`] for an unusable clock or an unwritable index or status.
    pub fn reconcile_once(&mut self) -> Result<RoundReport, RoundError> {
        let (when, text) = stamp_of(self.clock.wall_ns()).map_err(|_| RoundError::Clock)?;
        let before = (self.status.host_paused, self.status.parked.clone());
        let mut groups: Vec<(Owner, Vec<Source>)> = Vec::new();
        let mut group_of: HashMap<Owner, usize> = HashMap::new();
        let walked = self.sources.enumerate();
        // Absence is observed only after a complete walk (DESIGN.md 6).
        self.absence = self.sources.walk_complete();
        for (owner, source) in walked {
            let index = *group_of.entry(owner.clone()).or_insert(groups.len());
            match groups.get_mut(index) {
                Some((_, list)) => list.push(source),
                None => groups.push((owner, vec![source])),
            }
        }
        self.rescan_hints();
        let mut files = std::mem::take(&mut self.files);
        let mut report = RoundReport::default();
        self.status.host_paused = false;
        let started_ns = self.clock.monotonic_ns();
        let mut last_progress_ns = started_ns;
        for (done, (owner, sources)) in groups.iter().enumerate() {
            if (self.stop)() {
                // A round cut short re-probed nothing more: it cannot resume a paused host.
                self.status.host_paused |= before.0;
                break;
            }
            let mut rt = self
                .owners
                .remove(owner)
                .unwrap_or_else(|| OwnerRt::new(None));
            let was_parked = matches!(rt.turn, Turn::Parked(_));
            let known: Vec<SourceId> = rt.parked_sources.keys().cloned().collect();
            let pause = self.run_owner(owner, sources, &mut rt, &mut files, &when, &mut report);
            Self::log_parks(owner, &rt, was_parked, &known);
            self.owners.insert(owner.clone(), rt);
            let now_ns = self.clock.monotonic_ns();
            if now_ns.saturating_sub(last_progress_ns) >= PROGRESS_EVERY_NS {
                last_progress_ns = now_ns;
                crate::log::progress(
                    done + 1,
                    groups.len(),
                    report.landed,
                    report.failed,
                    now_ns.saturating_sub(started_ns) / 1_000_000_000,
                );
            }
            if pause {
                self.status.host_paused = true;
                break;
            }
        }
        self.absence = false;
        if self.status.host_paused != before.0 {
            if before.0 {
                crate::log::host_resumed();
            } else {
                crate::log::host_paused();
            }
        }
        self.advance_hints(&files);
        self.sweep_hints(&mut files, self.clock.wall_ns(), &mut report);
        self.files = files;
        self.status.failures.add(&report.failures);
        self.status.audit.add(&report.audit);
        self.status.rounds += 1;
        self.status.updated_at = text;
        self.rebuild_parked();
        // The parked list and host pause are authoritative (DESIGN.md 11.3): a change is made
        // durable at once. Counters and `updated_at` follow the 60 s rule of 9.2.
        let authoritative = before != (self.status.host_paused, self.status.parked.clone());
        self.write_status(authoritative)
            .map_err(RoundError::State)?;
        self.write_index(false).map_err(RoundError::State)?;
        Ok(report)
    }

    /// Log the parks this turn caused: the owner once, each newly parked source once.
    fn log_parks(owner: &Owner, rt: &OwnerRt, was_parked: bool, known: &[SourceId]) {
        if !was_parked && let Turn::Parked(reason) = &rt.turn {
            crate::log::parked(owner, reason, crate::log::Scope::Owner);
        }
        for (id, reason) in &rt.parked_sources {
            if !known.contains(id) {
                let reason = ParkedReason::Source(reason.clone());
                crate::log::parked(owner, &reason, crate::log::Scope::Source);
            }
        }
    }

    /// Recompute the sorted parked list of the status from the owners.
    fn rebuild_parked(&mut self) {
        let mut parked: Vec<ParkedEntry> = Vec::new();
        for (owner, rt) in &self.owners {
            if let Turn::Parked(reason) = &rt.turn {
                parked.push(ParkedEntry {
                    owner: owner.clone(),
                    reason: reason.clone(),
                    source: None,
                });
            }
            for (id, reason) in &rt.parked_sources {
                parked.push(ParkedEntry {
                    owner: owner.clone(),
                    reason: ParkedReason::Source(reason.clone()),
                    source: Some(id.clone()),
                });
            }
        }
        parked.sort_by_key(|p| (format!("{:?}", p.owner), p.source.clone()));
        self.status.parked = parked;
    }

    /// Re-read every hint day file into the daemon's in-memory scan.
    fn rescan_hints(&mut self) {
        self.files = self.scan_hints();
    }

    /// Refresh the in-memory hint scan and return the records not signalled before. The
    /// "seen" offsets live in memory only and start at the stored offset, so after a restart
    /// the uncommitted records signal once more. The paths a record names are passed through
    /// [`Sources::note`] when the scan is built; a record's owner is the owner its transcript
    /// path has there.
    pub fn refresh_hints(&mut self) -> Vec<HintSignal> {
        self.rescan_hints();
        let mut fresh = Vec::new();
        for file in &self.files {
            let seen = self.seen.entry(file.name.clone()).or_insert(file.start);
            if *seen > file.len {
                *seen = file.start;
            }
            for r in &file.records {
                if r.end > *seen {
                    let signal = if r.event.flushes() {
                        Signal::SessionEnd
                    } else {
                        Signal::Stop
                    };
                    if let Some(owner) = &r.owner {
                        fresh.push(HintSignal {
                            owner: owner.clone(),
                            signal,
                            paths: r.paths.clone(),
                        });
                    }
                    if let Some((owner, path)) = &r.other {
                        fresh.push(HintSignal {
                            owner: owner.clone(),
                            signal,
                            paths: vec![path.clone()],
                        });
                    }
                }
            }
            if let Some(last) = file.records.last() {
                *seen = (*seen).max(last.end);
            }
        }
        let files = &self.files;
        self.seen.retain(|n, _| files.iter().any(|f| f.name == *n));
        fresh
    }

    /// Remember a path an event named; the owner it belongs to, if it is a source.
    pub fn note_path(&mut self, path: &Path) -> Option<Owner> {
        self.sources.note(path)
    }

    /// Read one directory an event named (see [`Sources::read_dir`]).
    pub fn read_dir(&mut self, path: &Path) -> (Vec<Owner>, bool) {
        self.sources.read_dir(path)
    }

    /// The earliest retry deadline among owners (monotonic), if any.
    pub fn next_retry(&self) -> Option<Duration> {
        self.owners
            .values()
            .filter_map(|rt| match rt.turn {
                Turn::RetryAt(t) => Some(t),
                _ => None,
            })
            .min()
    }

    /// Owners whose retry deadline has passed at the current monotonic time, in a fixed order.
    pub fn retries_due(&self) -> Vec<Owner> {
        let now = Duration::from_nanos(self.clock.monotonic_ns());
        let mut due: Vec<Owner> = self
            .owners
            .iter()
            .filter(|(_, rt)| matches!(rt.turn, Turn::RetryAt(t) if t <= now))
            .map(|(o, _)| o.clone())
            .collect();
        due.sort_by_key(|o| format!("{o:?}"));
        due
    }

    /// Capture one owner from its own sources (no walk). Unlike a full round it neither sweeps
    /// hints, resets a host pause, nor counts a round: only a full round re-probes a paused
    /// host (DESIGN.md 11.1).
    ///
    /// # Errors
    /// [`RoundError`] for an unusable clock or an unwritable index or status.
    pub fn capture_owner(&mut self, owner: &Owner) -> Result<OwnerOutcome, RoundError> {
        if self.status.host_paused {
            return Ok(OwnerOutcome::Paused);
        }
        match self.owners.get(owner).map(|rt| &rt.turn) {
            Some(Turn::Parked(_)) => return Ok(OwnerOutcome::Skipped),
            Some(Turn::RetryAt(t)) if Duration::from_nanos(self.clock.monotonic_ns()) < *t => {
                return Ok(OwnerOutcome::Deferred);
            }
            _ => {}
        }
        let (when, text) = stamp_of(self.clock.wall_ns()).map_err(|_| RoundError::Clock)?;
        let sources = self.sources.of(owner);
        if sources.is_empty() {
            return Ok(OwnerOutcome::Unchanged);
        }
        let before = (self.status.host_paused, self.status.parked.clone());
        let mut files = std::mem::take(&mut self.files);
        let mut report = RoundReport::default();
        let mut rt = self
            .owners
            .remove(owner)
            .unwrap_or_else(|| OwnerRt::new(None));
        let was_parked = matches!(rt.turn, Turn::Parked(_));
        let known: Vec<SourceId> = rt.parked_sources.keys().cloned().collect();
        let pause = self.run_owner(owner, &sources, &mut rt, &mut files, &when, &mut report);
        Self::log_parks(owner, &rt, was_parked, &known);
        self.owners.insert(owner.clone(), rt);
        if pause {
            self.status.host_paused = true;
            crate::log::host_paused();
        }
        self.advance_hints(&files);
        self.files = files;
        self.status.failures.add(&report.failures);
        self.status.audit.add(&report.audit);
        self.status.updated_at = text;
        self.rebuild_parked();
        let authoritative = before != (self.status.host_paused, self.status.parked.clone());
        self.write_status(authoritative)
            .map_err(RoundError::State)?;
        self.write_index(false).map_err(RoundError::State)?;
        Ok(if report.stopped > 0 {
            OwnerOutcome::Stopped
        } else if report.landed > 0 {
            OwnerOutcome::Landed
        } else if report.failed > 0 || report.parked > 0 {
            OwnerOutcome::Failed
        } else {
            OwnerOutcome::Unchanged
        })
    }

    fn write_status(&mut self, force: bool) -> Result<(), StateError> {
        let now = Duration::from_nanos(self.clock.monotonic_ns());
        let due = self
            .last_status
            .is_none_or(|last| now.saturating_sub(last) >= INDEX_INTERVAL);
        if force || due {
            let body = serde_json::to_vec(&self.status)
                .map_err(|_| StateError::Io(std::io::ErrorKind::InvalidData))?;
            self.state.write(StateFile::Status, &body)?;
            self.last_status = Some(now);
        }
        Ok(())
    }

    /// Flush the status (if any round ran) and the index if dirty (DESIGN.md 9.2: at
    /// shutdown), then release the lock.
    ///
    /// # Errors
    /// [`StateError`] when the index cannot be written.
    pub fn shutdown(mut self) -> Result<(), StateError> {
        if self.last_status.is_some() {
            self.write_status(true)?;
        }
        self.write_index(true)
    }

    /// Path of the state root.
    pub fn root(&self) -> PathBuf {
        self.state.root().to_path_buf()
    }
}

/// Jitter draw: splitmix64 over a counter seeded from the clocks.
fn draw<C: Clock>(draws: &mut u64, clock: &C) -> u64 {
    *draws = draws.wrapping_add(0x9E37_79B9_7F4A_7C15);
    let seed = *draws ^ clock.monotonic_ns() ^ (clock.wall_ns() as u64);
    let mut z = seed;
    z = (z ^ (z >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
    z ^ (z >> 31)
}
