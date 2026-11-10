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
//!    file is treated as missing (9.2); the daemon has no logging yet, so the event is kept
//!    as a [`StateLoad`] on the daemon and counted in [`StatusBody::events`].
//!
//! The commit base (`current.json` ETag, owner state) is never persisted: every owner
//! recovers it from the store on its first round after a start (`recover_base`).
use crate::capture::{
    Captured, Env, Local, Locals, MAX_PIECE_BYTES, Source, Update, attach_launches, capture,
};
use crate::commit::{
    Base, Commit, Failure, KnownOurs, Landed, commit_known, disposition_paced, recover_base,
};
use crate::config::{Config, ConfigError};
use crate::environment::{Clock, FileSystem, Store};
use crate::hook::{is_day_name, utc_day};
use crate::state::{
    DaemonLock, Loaded, MissingReason, StateDir, StateError, StateFile, SyncPolicy, WRITER_VERSION,
};
use memory_core::disposition::Streak;
use memory_core::domain::{
    Disposition, ErrorClass, Owner, OwnerParkReason, ParkReason, RulesDigest, SessionKey,
    SnapshotFingerprint, SourceId, SourceState,
};
use memory_core::hint::{HintScan, LaunchHint, scan};
use memory_core::manifest::{Header, ManifestError, Timestamp, Writer};
use memory_core::privacy::{PolicyContext, SanitizedLaunch, sanitize_launch};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, HashMap};
use std::io::Read;
use std::ops::ControlFlow;
use std::path::{Path, PathBuf};
use std::process::ExitCode;
use std::time::Duration;

/// The derived index is written at most this often while dirty (DESIGN.md 9.2).
pub const INDEX_INTERVAL: Duration = Duration::from_secs(60);
/// Exit code of `memory daemon` for a config refusal.
pub const EXIT_CONFIG: u8 = 2;
/// Exit code of `memory daemon` while no real adapters exist.
pub const EXIT_NO_ADAPTERS: u8 = 78;
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
}

/// `status.json` body (schema `memory.status/1`).
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
    /// Events DESIGN.md would log, counted because the daemon has no logging yet.
    #[serde(default)]
    pub events: EventCounts,
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

/// Counted events that DESIGN.md 9.2 and 7.6 say are logged (the daemon has no logging yet).
/// A damaged or newer-schema `status.json` restarts every counter, so the event it caused is
/// recorded in the replacement.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct EventCounts {
    /// `status.json` was present but damaged at startup; parked list and host pause were lost.
    pub status_damaged: u64,
    /// `status.json` had a newer schema at startup; parked list and host pause were lost.
    pub status_newer_schema: u64,
    /// `index.json` was present but damaged at startup and was rebuilt.
    pub index_damaged: u64,
    /// `index.json` had a newer schema at startup and was rebuilt.
    pub index_newer_schema: u64,
    /// Owner commits that proceeded without their pending launch records (the digest input
    /// could not be encoded); the records stay pending in the hint files.
    pub launch_attach_failed: u64,
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
    /// Owners parked by this round.
    pub parked: usize,
    /// Owners skipped because a retry delay (`not_before`) has not elapsed.
    pub deferred: usize,
    /// Failures of this round by class.
    pub failures: FailureCounts,
    /// Owners whose commit went ahead without their pending launch records because attaching
    /// them failed (DESIGN.md 7.6: the records stay pending in the hint files).
    pub launch_unattached: usize,
}

/// Source enumeration, supplied by the shell.
pub trait Sources {
    /// All currently known sources with their owner.
    fn enumerate(&mut self) -> Vec<(Owner, Source)>;
}
impl<T: FnMut() -> Vec<(Owner, Source)>> Sources for T {
    fn enumerate(&mut self) -> Vec<(Owner, Source)> {
        self()
    }
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

/// Count a startup load that lost state.
fn count_load(load: StateLoad, damaged: &mut u64, newer: &mut u64) {
    match load {
        StateLoad::Damaged => *damaged = damaged.saturating_add(1),
        StateLoad::NewerSchema => *newer = newer.saturating_add(1),
        StateLoad::Loaded | StateLoad::Missing => {}
    }
}

fn open(root: &Path, config_path: &Path, sync: SyncPolicy) -> Result<Opened, StartError> {
    let lock = DaemonLock::acquire(root).map_err(StartError::State)?;
    let state = StateDir::prepare_inner(&lock, sync).map_err(StartError::State)?;
    state.vet_hints_dir().map_err(StartError::State)?;
    let config = Config::load(config_path).map_err(StartError::Config)?;
    let (index, index_load) = load_state::<IndexBody>(&state, StateFile::Index);
    let (status, status_load) = load_state::<StatusBody>(&state, StateFile::Status);
    let mut status = status.unwrap_or_default();
    let events = &mut status.events;
    count_load(
        status_load,
        &mut events.status_damaged,
        &mut events.status_newer_schema,
    );
    count_load(
        index_load,
        &mut events.index_damaged,
        &mut events.index_newer_schema,
    );
    Ok(Opened {
        lock,
        state,
        config,
        index: index.unwrap_or_default(),
        index_load,
        status,
        status_load,
    })
}

/// Entry of `memory daemon`: startup, then an explicit refusal because the real filesystem
/// and store adapters do not exist yet. Prints nothing and never captures.
#[must_use]
pub fn main_entry() -> ExitCode {
    let Some(root) = crate::state::default_root() else {
        return ExitCode::from(1);
    };
    match open(&root, &root.join("config.toml"), SyncPolicy::Full) {
        Err(error) => ExitCode::from(error.exit_code()),
        Ok(opened) => {
            drop(opened);
            ExitCode::from(EXIT_NO_ADAPTERS)
        }
    }
}

struct OwnerRt {
    base: Option<Base>,
    needs_recover: bool,
    streak: Streak,
    parked: Option<ParkedReason>,
    /// Manifests built since the last landed commit (A6); memory only.
    known_ours: KnownOurs,
    /// Monotonic time before which the owner is not retried (from `Disposition::Retry`).
    not_before: Option<Duration>,
}

impl OwnerRt {
    fn new(parked: Option<ParkedReason>) -> Self {
        Self {
            base: None,
            needs_recover: true,
            streak: Streak::default(),
            parked,
            known_ours: KnownOurs::new(),
            not_before: None,
        }
    }
}

struct PendingHint {
    end: u64,
    owner: Owner,
    launch: Option<SanitizedLaunch>,
    done: bool,
}

struct ScannedFile {
    name: String,
    start: u64,
    scan: HintScan,
    records: Vec<PendingHint>,
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

/// Whether attaching launch records worked; a failure is counted, never silent.
fn note_attach(result: Result<(), ManifestError>, report: &mut RoundReport) -> bool {
    let attached = result.is_ok();
    report.launch_unattached += usize::from(!attached);
    attached
}

/// The daemon: locked, prepared and loaded.
pub struct Daemon<F, S, C, E> {
    _lock: DaemonLock,
    state: StateDir,
    config: Config,
    fs: F,
    store: S,
    clock: C,
    sources: E,
    context: PolicyContext,
    locals: Locals,
    hints: BTreeMap<String, u64>,
    owners: HashMap<Owner, OwnerRt>,
    status: StatusBody,
    index_load: StateLoad,
    status_load: StateLoad,
    dirty: bool,
    last_index: Option<Duration>,
    last_status: Option<Duration>,
    draws: u64,
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
        let opened = open(root, config_path, sync)?;
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
        let owners = opened
            .status
            .parked
            .iter()
            .map(|p| (p.owner.clone(), OwnerRt::new(Some(p.reason.clone()))))
            .collect();
        Ok(Self {
            _lock: opened.lock,
            state: opened.state,
            config: opened.config,
            fs: parts.fs,
            store: parts.store,
            clock: parts.clock,
            sources: parts.sources,
            context: parts.context,
            locals,
            hints: opened.index.hints,
            owners,
            status: opened.status,
            index_load: opened.index_load,
            status_load: opened.status_load,
            dirty: false,
            last_index: None,
            last_status: None,
            draws: 0,
        })
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
    /// and host pause were lost; the counters restarted and [`StatusBody::events`] says why.
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

    /// Jitter draw for the pure backoff: splitmix64 over a counter seeded from the clocks.
    fn next_draw(&mut self) -> u64 {
        self.draws = self.draws.wrapping_add(0x9E37_79B9_7F4A_7C15);
        let seed = self.draws ^ self.clock.monotonic_ns() ^ (self.clock.wall_ns() as u64);
        let mut z = seed;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
        z ^ (z >> 31)
    }

    fn apply(&mut self, updates: Vec<Update>) {
        self.dirty |= !updates.is_empty();
        for update in updates {
            update.apply(&mut self.locals);
        }
    }

    fn owner_of(&self, record: &memory_core::hint::HintRecord) -> Owner {
        Owner::Session(SessionKey {
            archive: self.config.archive_id.clone(),
            machine: self.config.machine_id.clone(),
            agent: record.agent.clone(),
            session: record.session.clone(),
        })
    }

    fn scan_hints(&self) -> Vec<ScannedFile> {
        let Ok(dir) = std::fs::read_dir(self.state.hints_dir()) else {
            return Vec::new();
        };
        let mut names: Vec<String> = dir
            .filter_map(|entry| entry.ok()?.file_name().into_string().ok())
            .filter(|name| name.strip_suffix(".jsonl").is_some_and(is_day_name))
            .collect();
        names.sort();
        let mut out = Vec::new();
        for name in names {
            let mut bytes = Vec::new();
            let read = std::fs::File::open(self.state.hints_dir().join(&name))
                .and_then(|file| file.take(MAX_HINT_FILE).read_to_end(&mut bytes));
            if read.is_err() {
                continue;
            }
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
                    PendingHint {
                        end: r.end,
                        owner: self.owner_of(&r.record),
                        done: launch.is_none(),
                        launch,
                    }
                })
                .collect();
            out.push(ScannedFile {
                name,
                start,
                scan: found,
                records,
            });
        }
        out
    }

    fn settle(
        rt: &mut OwnerRt,
        result: &Result<Landed, Failure>,
        at: Duration,
        draw: u64,
        report: &mut RoundReport,
    ) -> bool {
        let (disp, streak) = disposition_paced(result, false, at, &rt.streak, draw);
        rt.streak = streak;
        if let Err(failure) = result
            && (failure.base_may_be_stale() || matches!(failure, Failure::BaseUnchanged))
        {
            rt.base = None;
            rt.needs_recover = true;
        }
        match disp {
            Disposition::Done => {
                report.landed += 1;
                rt.not_before = None;
                false
            }
            Disposition::Retry { after, class } => {
                report.failed += 1;
                report.failures.bump(&class);
                rt.not_before = Some(at.saturating_add(after));
                false
            }
            Disposition::Cancelled => {
                report.failed += 1;
                false
            }
            Disposition::PauseHost => {
                report.failed += 1;
                report.failures.bump(&ErrorClass::DestinationAuth);
                true
            }
            Disposition::Park { reason } => {
                rt.parked = Some(ParkedReason::Source(reason));
                report.parked += 1;
                false
            }
            Disposition::ParkOwner { reason } => {
                rt.parked = Some(ParkedReason::Owner(reason));
                report.parked += 1;
                false
            }
        }
    }

    /// The owner's turn: `Some(at)` when it should run now, else it is counted as skipped
    /// (parked) or deferred (retry delay not elapsed).
    fn due(&self, rt: &OwnerRt, report: &mut RoundReport) -> Option<Duration> {
        if rt.parked.is_some() {
            report.skipped += 1;
            return None;
        }
        let at = Duration::from_nanos(self.clock.monotonic_ns());
        if rt.not_before.is_some_and(|t| at < t) {
            report.deferred += 1;
            return None;
        }
        Some(at)
    }

    /// Rebuild the commit base from the store when it is unknown. `Break(pause)` ends the turn
    /// with the failure settled; `pause` is true when uploads must pause.
    fn recover(
        &mut self,
        owner: &Owner,
        rt: &mut OwnerRt,
        at: Duration,
        report: &mut RoundReport,
    ) -> ControlFlow<bool> {
        if !rt.needs_recover {
            return ControlFlow::Continue(());
        }
        match recover_base(&mut self.store, owner) {
            Ok(base) => {
                rt.base = base;
                rt.needs_recover = false;
                ControlFlow::Continue(())
            }
            Err(failure) => {
                let draw = self.next_draw();
                ControlFlow::Break(Self::settle(rt, &Err(failure), at, draw, report))
            }
        }
    }

    /// Capture the owner's sources against the remote state of its base.
    fn capture_owner(
        &mut self,
        owner: &Owner,
        sources: &[Source],
        rt: &OwnerRt,
        when: &Timestamp,
    ) -> Captured {
        let env = Env {
            header: header_of(owner, when),
            context: &self.context,
            now_ns: i64::try_from(self.clock.wall_ns()).unwrap_or(i64::MAX),
            piece_len: MAX_PIECE_BYTES,
        };
        let remote = rt
            .base
            .as_ref()
            .map(|b| b.state.clone())
            .unwrap_or_default();
        capture(&mut self.fs, sources, &self.locals, &remote, &env)
    }

    /// Attach the owner's pending launch records (DESIGN.md 7.6). False when that failed: the
    /// commit still goes ahead without them, they stay pending, and the report counts it.
    fn attach_pending(
        request: &mut Commit,
        hints: &[ScannedFile],
        owner: &Owner,
        report: &mut RoundReport,
    ) -> bool {
        let launches: Vec<SanitizedLaunch> = hints
            .iter()
            .flat_map(|f| f.records.iter())
            .filter(|r| !r.done && r.owner == *owner)
            .filter_map(|r| r.launch.clone())
            .collect();
        note_attach(attach_launches(request, &launches), report)
    }

    /// Commit, then make the capture durable locally: updates and hint progress apply only once
    /// the commit has landed.
    fn land(
        &mut self,
        owner: &Owner,
        rt: &mut OwnerRt,
        request: Commit,
        got: Captured,
        hints: &mut [ScannedFile],
        attached: bool,
    ) -> Result<Landed, Failure> {
        let result = commit_known(
            &mut self.store,
            rt.base.as_ref(),
            request,
            &mut rt.known_ours,
            &mut |_| {},
        );
        if let Ok(landed) = &result {
            // Remote is authoritative: updates apply only once the commit is durable.
            // An adopted earlier manifest (A6 `follow_up`) did not carry this capture's
            // bytes: keep the base, apply nothing, and the next round captures again.
            rt.base = Some(landed.base());
            if !landed.follow_up {
                self.apply(got.updates);
            }
            if attached && !landed.follow_up {
                for record in hints.iter_mut().flat_map(|f| f.records.iter_mut()) {
                    record.done |= record.owner == *owner;
                }
            }
        }
        result
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
        if let ControlFlow::Break(pause) = self.recover(owner, rt, at, report) {
            return pause;
        }
        let mut got = self.capture_owner(owner, sources, rt, when);
        let Some(mut request) = got.commit.take() else {
            self.apply(got.updates);
            return false;
        };
        let attached = Self::attach_pending(&mut request, hints, owner, report);
        let result = self.land(owner, rt, request, got, hints, attached);
        let draw = self.next_draw();
        Self::settle(rt, &result, at, draw, report)
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
        for (owner, source) in self.sources.enumerate() {
            match groups.iter_mut().find(|(o, _)| *o == owner) {
                Some((_, list)) => list.push(source),
                None => groups.push((owner, vec![source])),
            }
        }
        let mut files = self.scan_hints();
        let mut report = RoundReport::default();
        self.status.host_paused = false;
        for (owner, sources) in &groups {
            let mut rt = self
                .owners
                .remove(owner)
                .unwrap_or_else(|| OwnerRt::new(None));
            let pause = self.run_owner(owner, sources, &mut rt, &mut files, &when, &mut report);
            self.owners.insert(owner.clone(), rt);
            if pause {
                self.status.host_paused = true;
                break;
            }
        }
        self.advance_hints(&files);
        self.status.failures.add(&report.failures);
        self.status.events.launch_attach_failed = self
            .status
            .events
            .launch_attach_failed
            .saturating_add(report.launch_unattached as u64);
        self.status.rounds += 1;
        self.status.updated_at = text;
        let mut parked: Vec<ParkedEntry> = self
            .owners
            .iter()
            .filter_map(|(owner, rt)| {
                rt.parked.clone().map(|reason| ParkedEntry {
                    owner: owner.clone(),
                    reason,
                })
            })
            .collect();
        parked.sort_by_key(|p| format!("{:?}", p.owner));
        self.status.parked = parked;
        // The parked list and host pause are authoritative (DESIGN.md 11.3): a change is made
        // durable at once. Counters and `updated_at` follow the 60 s rule of 9.2.
        let authoritative = before != (self.status.host_paused, self.status.parked.clone());
        self.write_status(authoritative)
            .map_err(RoundError::State)?;
        self.write_index(false).map_err(RoundError::State)?;
        Ok(report)
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_failed_launch_attachment_is_counted_and_reported_unattached() {
        let mut report = RoundReport::default();
        assert!(note_attach(Ok(()), &mut report));
        assert_eq!(report.launch_unattached, 0);
        assert!(!note_attach(Err(ManifestError::Encode), &mut report));
        assert!(!note_attach(Err(ManifestError::Encode), &mut report));
        assert_eq!(report.launch_unattached, 2);
    }
}
