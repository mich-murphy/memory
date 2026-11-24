//! Capture wiring (DESIGN.md 7.3, 8): one synchronous step for one owner.
//!
//! observe (stat through [`FileSystem`]) → admit → [`plan`] (answering `NeedWindows` by
//! reading through the seam and re-planning) → read exactly the planned range → privacy
//! `transform` → `authorize` → slice into pieces → a [`Commit`] for [`crate::commit::commit`].
//!
//! # How the type system keeps unredacted bytes out of the store
//!
//! [`Commit`] and [`crate::commit::Upload`] are generic over the body. This module only ever builds
//! `Commit`. [`Published`] has a private field and its only piece constructor is a
//! private function that slices a `memory_core::privacy::AuthorizedBody`, a value only
//! `privacy::authorize` can produce (from a borrowed `AdmittedSource` and a consumed
//! `RedactedBytes`, neither of which has a public constructor, see the compile-fail tests in
//! memory-core).
//! `Store` uploads take [`Published`] (defined in `environment`), whose only constructors are
//! crate-private: a slice of an `AuthorizedBody` and the writer's own metadata.
//!
//! ```compile_fail
//! let _ = memory::capture::Published(vec![1, 2, 3]);
//! ```
//!
//! Pieces are stored zstd-3 encoded: `Published::piece` compresses the authorized slice and
//! the descriptor records the length and digest of the stored bytes; a compression failure is
//! [`SourceOutcome::Unavailable`].
//!
//! # Audit and stat refresh
//!
//! An indexed append source gets [`AuditEvidence`] when [`crate::audit::due`] says so: a fresh
//! stat-read-stat of `[0, size)` (transformed as a whole-stream capture transforms it) is hashed
//! and compared with the whole-stream digest of the last remote epoch, never with the index.
//! A first mismatch is `RecheckRequired`; the planner asks for a second independent read.
//! A fresh stat the planner asks to refresh is stored in the index, and a match stores the
//! audit time; both are local updates (they apply once the owner's commit landed, or at once
//! when it has none).
//!
//! An extension that cannot resume the digest re-reads `[0, to)` and first checks that the
//! re-read prefix `[0, from)` hashes to the committed digest; otherwise the stored pieces
//! before `from` would not reassemble to the digest the new manifest commits, and a new epoch
//! starts instead.
//!
//! # Absence
//!
//! [`absent_into`] is the probe of a full round for stored live sources (append or snapshot,
//! brief 48) the walk did not yield: `stat` only, no read and no admission. A miss is counted; the second consecutive miss
//! with a live remote entry commits `Change::Deleted`. A seam error is not an absence, and a
//! file that exists resets the count. [`capture_into`] never probes absence for sources the
//! walk did not yield; the daemon calls the probe only from `reconcile_once`.
//!
//! # Snapshot sources
//!
//! A snapshot (a Pi run record) is fingerprinted from its stat; an unchanged fingerprint and
//! rules end there without opening the content. Otherwise the source is replaced from a read of
//! the file. A piece whose transformed digest equals one of the last landed epoch's reuses that
//! descriptor with no PUT (digest only, never offset).
//!
//! Limitations: the whole-stream digest resumes from the index's `Sha256State` for
//! prefix-stable sources (otherwise it is recomputed from a fresh read of `[0, to)`). Snapshot
//! and missing-source planning never carry audit evidence (`NotDue`).
use crate::audit::{self, AuditCounts};
use crate::commit::{Collect, Commit, PieceSink};
use crate::environment::{FileSystem, PieceError};
use memory_core::domain::{
    AuditState, EpochPosition, Fingerprint, Observed, Offset, ParkReason, Plan, ReplayReason,
    RulesDigest, Sha256, Sha256State, SnapshotFingerprint, SourceClass, SourceId, SourceKind,
    SourceState, WithholdReason,
};
use memory_core::manifest::{
    Change, EpochStamp, EpochWriter, Header, LaunchRecord, OwnerState, Piece, SourceWrite,
    StoredEncoding, manifest_hash,
};
use memory_core::planner::{
    AppendBasis, AuditEvidence, HEAD_WINDOW_BYTES, Observation, PlanInput, Planning, Strategy,
    TAIL_WINDOW_BYTES, WindowEvidence, audit_eligible, plan, window_ranges,
};
use memory_core::privacy::{
    Admission, AdmittedSource, Authorization, AuthorizedBody, PolicyContext, Profile, Reason,
    ResolvedSourceFacts, SanitizedLaunch, UnknownReason, admit, authorize, transform_owned,
};
use std::collections::BTreeMap;

/// Largest raw piece (DESIGN.md 7.3).
pub const MAX_PIECE_BYTES: usize = 8 * 1024 * 1024;

pub use crate::environment::Published;

/// One located source, as the shell resolved it.
#[derive(Debug, Clone)]
pub struct Source {
    /// Handle understood by the [`FileSystem`] seam.
    pub handle: u64,
    /// Locator id.
    pub id: SourceId,
    /// Locator kind.
    pub kind: SourceKind,
    /// Locator class.
    pub class: SourceClass,
    /// Root-relative path recorded in the manifest.
    pub path: String,
    /// Privacy facts resolved by the shell.
    pub facts: ResolvedSourceFacts,
}

/// Rebuildable local knowledge of one source.
#[derive(Debug, Clone, Default)]
pub struct Local {
    /// Append index.
    pub index: Option<SourceState>,
    /// Consecutive missing observations.
    pub missing: u32,
    /// Last captured snapshot fingerprint and rules.
    pub snapshot: Option<(SnapshotFingerprint, Option<RulesDigest>)>,
    /// Pieces of the last landed snapshot epoch: a new piece with an equal digest reuses one.
    pub snapshot_pieces: Vec<Piece>,
    /// A deletion was committed for the current absence.
    pub deletion_recorded: bool,
    /// Last judgement that withheld a scanned file: the fingerprint and rules it was made
    /// under, and the reason (D54, brief 47). The same pair gives `Unchanged` without a read.
    pub withheld: Option<(Fingerprint, Option<RulesDigest>, WithholdReason)>,
}
/// Local knowledge per source.
pub type Locals = BTreeMap<SourceId, Local>;

/// A local-state change to apply once the commit landed (or at once when there is none).
#[derive(Debug, Clone)]
pub enum Update {
    /// Absence count.
    Missing(SourceId, u32),
    /// Source seen: reset absence.
    Present(SourceId),
    /// Deletion committed: forget caches.
    Deleted(SourceId),
    /// New append index.
    Index(SourceId, Box<SourceState>),
    /// New snapshot fingerprint, rules and the epoch's pieces.
    Snapshot(
        SourceId,
        SnapshotFingerprint,
        Option<RulesDigest>,
        Vec<Piece>,
    ),
    /// A scanned file was judged and withheld under this fingerprint and rules.
    Withheld(SourceId, Fingerprint, Option<RulesDigest>, WithholdReason),
}
impl Update {
    /// Apply to the local state.
    pub fn apply(self, locals: &mut Locals) {
        match self {
            Self::Missing(id, n) => locals.entry(id).or_default().missing = n,
            Self::Present(id) => {
                let local = locals.entry(id).or_default();
                local.missing = 0;
                local.deletion_recorded = false;
            }
            Self::Deleted(id) => {
                let local = locals.entry(id).or_default();
                local.deletion_recorded = true;
                local.index = None;
                local.snapshot = None;
                local.snapshot_pieces.clear();
                local.withheld = None;
            }
            Self::Index(id, state) => locals.entry(id).or_default().index = Some(*state),
            Self::Snapshot(id, fp, rules, pieces) => {
                let local = locals.entry(id).or_default();
                local.snapshot = Some((fp, rules));
                local.snapshot_pieces = pieces;
                local.withheld = None;
            }
            Self::Withheld(id, fp, rules, reason) => {
                locals.entry(id).or_default().withheld = Some((fp, rules, reason));
            }
        }
    }
    /// Record the encodings that won an `If-None-Match` race in the pieces a snapshot keeps.
    pub fn adopt(&mut self, winners: &crate::commit::Winners) {
        if let Self::Snapshot(_, _, _, pieces) = self {
            for piece in pieces.iter_mut() {
                if let Some(won) = winners.get(piece.object()) {
                    *piece = piece.clone().adopt_stored(won);
                }
            }
        }
    }
}

/// Per-source result. Closed.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SourceOutcome {
    /// Nothing to publish.
    Unchanged,
    /// Admission or transform refused; nothing was read, uploaded or stored.
    Withheld(WithholdReason),
    /// Policy evidence missing; fail closed.
    Blocked(UnknownReason),
    /// The source changed between stat and read; retry later.
    Raced,
    /// The seam failed or evidence could not be completed; not an absence.
    Unavailable,
    /// A change and its pieces are in the commit.
    Planned,
    /// The audit keeps failing: nothing was uploaded and the index entry is unchanged. The
    /// shell leaves the source out of later captures.
    Parked(ParkReason),
}

/// Inputs shared by every source of one step.
pub struct Env<'a> {
    /// Manifest header for the commit.
    pub header: Header,
    /// Policy observation.
    pub context: &'a PolicyContext,
    /// Wall time of the observation.
    pub now_ns: i64,
    /// Raw piece size (at most [`MAX_PIECE_BYTES`]).
    pub piece_len: usize,
}

/// Result of one step.
#[derive(Debug)]
pub struct Captured {
    /// The commit request, when anything must be published.
    pub commit: Option<Commit>,
    /// Local updates to apply after the commit lands.
    pub updates: Vec<Update>,
    /// Outcome per source, in input order.
    pub outcomes: Vec<(SourceId, SourceOutcome)>,
    /// Audit verdicts reached in this step, whether or not the commit lands.
    pub audit: AuditCounts,
}

struct Acc<'a> {
    audit: AuditCounts,
    changes: Vec<Change>,
    updates: Vec<Update>,
    /// Where each encoded piece goes the moment it is produced.
    sink: &'a mut dyn PieceSink,
}

/// Run one capture step for one owner, collecting every encoded piece in the commit.
pub fn capture<F: FileSystem>(
    fs: &mut F,
    sources: &[Source],
    locals: &Locals,
    remote: &OwnerState,
    env: &Env<'_>,
) -> Captured {
    let mut sink = Collect::default();
    let mut got = capture_into(fs, sources, locals, remote, env, &mut sink);
    if let Some(commit) = &mut got.commit {
        commit.uploads = sink.0;
    }
    got
}

/// Run one capture step for one owner, handing each encoded piece to `sink` as it is produced.
/// The commit's `uploads` stay empty. Once the sink has refused a piece, later sources are
/// not read and end `Unavailable`; the sink holds the reason.
pub fn capture_into<F: FileSystem>(
    fs: &mut F,
    sources: &[Source],
    locals: &Locals,
    remote: &OwnerState,
    env: &Env<'_>,
    sink: &mut dyn PieceSink,
) -> Captured {
    let mut acc = Acc {
        audit: AuditCounts::default(),
        changes: Vec::new(),
        updates: Vec::new(),
        sink,
    };
    let mut outcomes = Vec::new();
    let none = Local::default();
    for src in sources {
        let local = locals.get(&src.id).unwrap_or(&none);
        let outcome = if acc.sink.halted() {
            SourceOutcome::Unavailable
        } else {
            step(fs, src, local, remote, env, &mut acc)
        };
        outcomes.push((src.id.clone(), outcome));
    }
    let commit = (!acc.changes.is_empty()).then(|| Commit {
        header: env.header.clone(),
        changes: acc.changes,
        uploads: Vec::new(),
    });
    Captured {
        commit,
        updates: acc.updates,
        outcomes,
        audit: acc.audit,
    }
}

/// A stored live source (append or snapshot, brief 48) the walk did not yield: probed for absence only.
#[derive(Debug, Clone)]
pub struct Candidate {
    /// Handle understood by the [`FileSystem`] seam.
    pub handle: u64,
    /// Locator id.
    pub id: SourceId,
}

/// The absence probe of a full round: per candidate, `stat` only (no read, no admission:
/// nothing leaves the host but the stored id, kind and a timestamp, all already in the
/// manifest). A candidate whose remote entry is not live is skipped. `None` counts the absence
/// as `step` does, and at the second consecutive miss records [`Change::Deleted`]; a seam
/// error is not an absence and changes nothing; a file that exists resets the count.
pub fn absent_into<F: FileSystem>(
    fs: &mut F,
    candidates: &[Candidate],
    locals: &Locals,
    remote: &OwnerState,
    env: &Env<'_>,
) -> Captured {
    let mut changes = Vec::new();
    let mut updates = Vec::new();
    let mut outcomes = Vec::new();
    let none = Local::default();
    for c in candidates {
        let local = locals.get(&c.id).unwrap_or(&none);
        let Some(entry) = remote.sources.get(&c.id).filter(|e| e.deleted_at.is_none()) else {
            continue;
        };
        let outcome = match fs.stat(c.handle) {
            Err(_) => SourceOutcome::Unavailable,
            Ok(Some(_)) => {
                if local.missing > 0 || local.deletion_recorded {
                    updates.push(Update::Present(c.id.clone()));
                }
                SourceOutcome::Unchanged
            }
            Ok(None) => {
                let planning = absence_plan(&c.id, local, false, None, &mut updates);
                if let Planning::Ready {
                    plan: Plan::Deleted,
                    ..
                } = planning
                {
                    changes.push(Change::Deleted {
                        id: c.id.clone(),
                        kind: entry.kind.clone(),
                        deleted_at: env.header.captured_at.clone(),
                    });
                    updates.push(Update::Deleted(c.id.clone()));
                    SourceOutcome::Planned
                } else {
                    SourceOutcome::Unchanged
                }
            }
        };
        outcomes.push((c.id.clone(), outcome));
    }
    let commit = (!changes.is_empty()).then(|| Commit {
        header: env.header.clone(),
        changes,
        uploads: Vec::new(),
    });
    Captured {
        commit,
        updates,
        outcomes,
        audit: AuditCounts::default(),
    }
}

/// Attach sanitized launch evidence to a commit header (DESIGN.md 6.3, 8.4).
///
/// Only a [`SanitizedLaunch`] can become a `LaunchRecord` (`from_sanitized`), so raw argv or
/// environment cannot reach the manifest through this function. Records are de-duplicated by
/// digest, both against the header's existing records and within `launches`.
pub fn attach_launches(commit: &mut Commit, launches: &[SanitizedLaunch]) {
    let mut added: Vec<LaunchRecord> = Vec::new();
    for launch in launches {
        let record = LaunchRecord::from_sanitized(launch);
        let seen = commit
            .header
            .launch
            .iter()
            .chain(added.iter())
            .any(|r| r.digest() == record.digest());
        if !seen {
            added.push(record);
        }
    }
    commit.header.launch.extend(added);
}

fn withhold_of(reason: Reason) -> WithholdReason {
    match reason {
        Reason::Credential => WithholdReason::Credential,
        Reason::CollectorState => WithholdReason::CollectorState,
        Reason::UnknownKind => WithholdReason::UnknownKind,
        Reason::UnknownOwner => WithholdReason::UnknownOwner,
        Reason::MalformedProtected => WithholdReason::MalformedProtected,
        Reason::SecretInText => WithholdReason::SecretInText,
        Reason::SourceLimit => WithholdReason::SourceLimit,
        Reason::Binary => WithholdReason::Binary,
        Reason::ScrubFailed | Reason::ContextChanged => WithholdReason::ScrubFailed,
    }
}

/// Privacy transform, then the final authorization of the transformed bytes: the one place the
/// bytes capture would publish for `raw` are decided. `memory check` calls it too, so the check
/// compares against what capture stores rather than a reimplementation.
///
/// # Errors
/// [`SourceOutcome::Withheld`] when the transform or the authorization denies, and
/// [`SourceOutcome::Blocked`] when policy evidence is missing.
pub fn publishable(
    admitted: &AdmittedSource,
    raw: &[u8],
    context: &PolicyContext,
) -> Result<AuthorizedBody, SourceOutcome> {
    publishable_owned(admitted, raw.to_vec(), context)
}

/// [`publishable`] over a buffer the caller gives up, so no second copy of it is made.
///
/// # Errors
/// As [`publishable`].
pub fn publishable_owned(
    admitted: &AdmittedSource,
    raw: Vec<u8>,
    context: &PolicyContext,
) -> Result<AuthorizedBody, SourceOutcome> {
    let redacted =
        transform_owned(admitted, raw).map_err(|r| SourceOutcome::Withheld(withhold_of(r)))?;
    match authorize(admitted, redacted, context) {
        Authorization::Allow(body) => Ok(body),
        Authorization::Deny(r) => Err(SourceOutcome::Withheld(withhold_of(r))),
        Authorization::Unknown(u) => Err(SourceOutcome::Blocked(u)),
    }
}

fn hash_span(bytes: &[u8], range: &std::ops::Range<Offset>) -> Option<Sha256> {
    let start = usize::try_from(range.start.get()).ok()?;
    let end = usize::try_from(range.end.get()).ok()?;
    bytes.get(start..end).map(manifest_hash)
}

/// The first [`HEAD_WINDOW_BYTES`] and the last [`TAIL_WINDOW_BYTES`] bytes of a region that
/// is streamed through in chunks, so the window digests need no copy of the region itself.
#[derive(Default)]
struct Edges {
    head: Vec<u8>,
    tail: Vec<u8>,
    /// Bytes pushed so far.
    len: u64,
}

impl Edges {
    fn push(&mut self, chunk: &[u8]) {
        let head_cap = usize::try_from(HEAD_WINDOW_BYTES).unwrap_or(usize::MAX);
        let take = head_cap.saturating_sub(self.head.len()).min(chunk.len());
        self.head
            .extend_from_slice(chunk.get(..take).unwrap_or_default());
        let tail_cap = usize::try_from(TAIL_WINDOW_BYTES).unwrap_or(usize::MAX);
        let skip = chunk.len().saturating_sub(tail_cap);
        self.tail
            .extend_from_slice(chunk.get(skip..).unwrap_or_default());
        let excess = self.tail.len().saturating_sub(tail_cap);
        self.tail.drain(..excess);
        self.len = self
            .len
            .saturating_add(u64::try_from(chunk.len()).unwrap_or(u64::MAX));
    }
    /// Bytes `[lo, hi)` of the region, when they lie in the head or the tail.
    fn span(&self, lo: u64, hi: u64) -> Option<&[u8]> {
        let head_len = u64::try_from(self.head.len()).ok()?;
        if hi <= head_len {
            return self
                .head
                .get(usize::try_from(lo).ok()?..usize::try_from(hi).ok()?);
        }
        let tail_start = self.len.checked_sub(u64::try_from(self.tail.len()).ok()?)?;
        let lo = lo.checked_sub(tail_start)?;
        let hi = hi.checked_sub(tail_start)?;
        self.tail
            .get(usize::try_from(lo).ok()?..usize::try_from(hi).ok()?)
    }
    /// Window digests of a stream that is exactly this region.
    fn windows(&self) -> Option<WindowEvidence> {
        let (head, tail) = window_ranges(Offset::new(self.len));
        Some(WindowEvidence {
            head: manifest_hash(self.span(head.start.get(), head.end.get())?),
            tail: manifest_hash(self.span(tail.start.get(), tail.end.get())?),
        })
    }
}

/// Read exactly `range` through the seam. The seam's own error is not an absence and is
/// reported as `Unavailable` (its detail stays in the adapter: `FileSystem::Error` carries
/// no bound a closed `SourceOutcome` could keep); a short read is a racing shrink, `Raced`.
fn read_checked<F: FileSystem>(
    fs: &mut F,
    handle: u64,
    range: std::ops::Range<u64>,
) -> Result<Vec<u8>, SourceOutcome> {
    let want = range.end.saturating_sub(range.start);
    let bytes = fs.read(handle, range).map_err(|e| {
        if F::is_undecodable(&e) {
            SourceOutcome::Withheld(WithholdReason::Undecodable)
        } else {
            SourceOutcome::Unavailable
        }
    })?;
    if u64::try_from(bytes.len()) == Ok(want) {
        Ok(bytes)
    } else {
        Err(SourceOutcome::Raced)
    }
}

/// Bytes `range` of the transformed stream whose first `from` bytes are still on disk
/// (identity transform) and whose remainder is the region `delta` (kept as its edges); reads
/// only the on-disk part.
fn stream_span<F: FileSystem>(
    fs: &mut F,
    handle: u64,
    from: u64,
    delta: &Edges,
    range: &std::ops::Range<Offset>,
    known: &[(u64, Vec<u8>)],
) -> Result<Vec<u8>, SourceOutcome> {
    let (start, end) = (range.start.get(), range.end.get());
    let disk_end = end.min(from);
    let mut out = if start < disk_end {
        let cached = known.iter().find_map(|(at, bytes)| {
            let lo = usize::try_from(start.checked_sub(*at)?).ok()?;
            let hi = usize::try_from(disk_end.checked_sub(*at)?).ok()?;
            bytes.get(lo..hi)
        });
        match cached {
            Some(hit) => hit.to_vec(),
            None => read_checked(fs, handle, start..disk_end)?,
        }
    } else {
        Vec::new()
    };
    if end > from {
        let (lo, hi) = (start.max(from) - from, end - from);
        out.extend_from_slice(delta.span(lo, hi).ok_or(SourceOutcome::Raced)?);
    }
    Ok(out)
}

/// Planner input for a source whose stat found nothing; counts the absence unless its
/// deletion was already committed.
fn absence_plan(
    id: &SourceId,
    local: &Local,
    recorded: bool,
    rules: Option<RulesDigest>,
    updates: &mut Vec<Update>,
) -> Planning {
    let observation = if recorded {
        Observation::DeletionRecorded
    } else {
        let consecutive = local.missing.saturating_add(1);
        updates.push(Update::Missing(id.clone(), consecutive));
        Observation::missing(consecutive)
    };
    plan(&PlanInput {
        withheld: None,
        observation,
        strategy: Strategy::Append(AppendBasis::Fresh),
        rules,
        audit: AuditEvidence::NotDue,
    })
}

fn step<F: FileSystem>(
    fs: &mut F,
    src: &Source,
    local: &Local,
    remote: &OwnerState,
    env: &Env<'_>,
    acc: &mut Acc<'_>,
) -> SourceOutcome {
    let admitted = match admit(env.context, src.facts.clone()) {
        Admission::Allow(a) => a,
        Admission::Deny(reason) => return SourceOutcome::Withheld(withhold_of(reason)),
        Admission::Unknown(u) => return SourceOutcome::Blocked(u),
    };
    let entry = remote.sources.get(&src.id);
    // The remote entry is authoritative (DESIGN.md 9.1, 9.3). The local flag is a cache that
    // can be stale after a lost reply, so it counts only when the remote has no entry.
    let recorded = entry.map_or(local.deletion_recorded, |e| e.deleted_at.is_some());
    let live = entry.filter(|e| e.deleted_at.is_none());
    let rules = matches!(
        src.facts.profile,
        Profile::Config | Profile::TextAsset | Profile::Scanned
    )
    .then_some(env.context.rules)
    .flatten()
    .map(|r| RulesDigest::new(Sha256::from_bytes(r)));
    let stat = match fs.stat(src.handle) {
        Ok(stat) => stat,
        Err(e) if F::is_undecodable(&e) => {
            return SourceOutcome::Withheld(WithholdReason::Undecodable);
        }
        Err(_) => return SourceOutcome::Unavailable,
    };
    let Some(stat) = stat else {
        let planning = absence_plan(&src.id, local, recorded, rules, &mut acc.updates);
        if let Planning::Ready {
            plan: Plan::Deleted,
            ..
        } = planning
            && live.is_some()
        {
            acc.changes.push(Change::Deleted {
                id: src.id.clone(),
                kind: src.kind.clone(),
                deleted_at: env.header.captured_at.clone(),
            });
            acc.updates.push(Update::Deleted(src.id.clone()));
            return SourceOutcome::Planned;
        }
        return SourceOutcome::Unchanged;
    };
    // A present file resets the absence count before any early return below, so a cached
    // withhold never leaves an earlier miss standing (a later single miss would delete).
    acc.updates.push(Update::Present(src.id.clone()));
    // D54, brief 47: a scanned file is judged once per fingerprint and rules; a cached
    // withhold costs no read. Over the cap it is withheld from its stat, before any read.
    let scanned = src.facts.profile == Profile::Scanned;
    let judged = Fingerprint {
        id: stat.id.clone(),
        size: stat.size,
        mtime_ns: stat.mtime_ns,
    };
    if scanned {
        if local
            .withheld
            .as_ref()
            .is_some_and(|(f, r, _)| *f == judged && *r == rules)
        {
            return SourceOutcome::Unchanged;
        }
        if stat.size > memory_core::privacy::MAX_SCANNED_BYTES {
            let reason = WithholdReason::SourceLimit;
            acc.updates.push(Update::Withheld(
                src.id.clone(),
                judged,
                rules,
                reason.clone(),
            ));
            return SourceOutcome::Withheld(reason);
        }
    }
    let observed = Observed {
        id: stat.id.clone(),
        size: stat.size,
        mtime_ns: stat.mtime_ns,
        observed_at_ns: env.now_ns,
    };
    let next_epoch = entry
        .and_then(|e| e.epochs.last())
        .map_or(0, |l| l.epoch.n.saturating_add(1));
    let job = Job {
        src,
        admitted,
        observed: &observed,
        env,
        rules,
        next_epoch,
        previous: audit::never(),
        reuse: if src.class == SourceClass::Snapshot {
            &local.snapshot_pieces
        } else {
            &[]
        },
    };
    match src.class {
        SourceClass::Snapshot => {
            let plain = Fingerprint {
                id: stat.id,
                size: stat.size,
                mtime_ns: stat.mtime_ns,
            };
            let outcome = snapshot(fs, job, local, plain, acc);
            if scanned && let SourceOutcome::Withheld(reason) = &outcome {
                acc.updates.push(Update::Withheld(
                    src.id.clone(),
                    judged,
                    rules,
                    reason.clone(),
                ));
            }
            outcome
        }
        SourceClass::Append => {
            let remote_pos = live.and_then(|e| e.epochs.last()).map(|l| EpochPosition {
                epoch: l.epoch.n,
                captured_to: Offset::new(l.size),
                whole: l.sha256,
                rules: l.epoch.rules,
            });
            append(fs, job, local, remote_pos, acc)
        }
    }
}

/// A snapshot source: replace the stored object when its fingerprint or the rules changed.
///
/// The fingerprint is the stat (`plain`), taken before anything is opened; an unchanged one
/// ends here. Otherwise the file is read and the object replaced.
fn snapshot<F: FileSystem>(
    fs: &mut F,
    job: Job<'_>,
    local: &Local,
    plain: Fingerprint,
    acc: &mut Acc<'_>,
) -> SourceOutcome {
    let fp = SnapshotFingerprint::Plain(plain);
    let planning = plan(&PlanInput {
        withheld: None,
        observation: Observation::Present(job.observed.clone()),
        strategy: Strategy::Snapshot {
            previous: local.snapshot.as_ref().map(|(f, _)| f.clone()),
            current: fp.clone(),
            previous_rules: local.snapshot.as_ref().and_then(|(_, r)| *r),
        },
        rules: job.rules,
        audit: AuditEvidence::NotDue,
    });
    match planning {
        Planning::Ready {
            plan: Plan::Snapshot | Plan::Replay { .. },
            ..
        } => {}
        Planning::Ready { .. } => return SourceOutcome::Unchanged,
        Planning::NeedWindows | Planning::NeedAuditRecheck => return SourceOutcome::Unavailable,
    }
    match job.write(fs, Mode::Replace, acc) {
        Ok(()) => {
            let pieces = match acc.changes.last() {
                Some(Change::Replace(w)) => w.pieces.clone(),
                _ => Vec::new(),
            };
            acc.updates
                .push(Update::Snapshot(job.src.id.clone(), fp, job.rules, pieces));
            SourceOutcome::Planned
        }
        Err(outcome) => outcome,
    }
}

/// A fresh stat as an [`Observed`], or `None` when the source is absent or unreadable.
fn observe_now<F: FileSystem>(fs: &mut F, handle: u64, at_ns: i64) -> Option<Observed> {
    let stat = fs.stat(handle).ok()??;
    Some(Observed {
        id: stat.id,
        size: stat.size,
        mtime_ns: stat.mtime_ns,
        observed_at_ns: at_ns,
    })
}

/// Verified on-disk byte segments `(start, bytes)` that need not be read again.
type Known = Vec<(u64, Vec<u8>)>;
/// Borrowed [`Known`] segments.
type Segments<'a> = &'a [(u64, Vec<u8>)];

/// What a write produced before its change and index entry are pushed.
struct Done {
    pieces: Vec<Piece>,
    whole: Sha256,
    whole_state: Option<Sha256State>,
    stream_len: u64,
    /// Windows of the region read in memory (the whole stream, or a resumed delta).
    edges: Edges,
}

enum Mode {
    /// Extend from the offset, whose committed whole-stream digest is the second field; the
    /// state, when present, is the resumable digest of `[0, from)`.
    /// `Known` are on-disk segments `(start, bytes)` already read and verified by the planner.
    Extend(Offset, Sha256, Option<Sha256State>, Known),
    /// A new epoch, storing this audit bookkeeping ([`audit::mismatch`] when an audit mismatch
    /// started it, [`audit::never`] otherwise).
    Epoch(AuditState),
    Replace,
}

struct Job<'a> {
    src: &'a Source,
    admitted: AdmittedSource,
    observed: &'a Observed,
    env: &'a Env<'a>,
    rules: Option<RulesDigest>,
    next_epoch: u32,
    /// Audit bookkeeping of the index this capture continues (append sources).
    previous: AuditState,
    /// Pieces of the last landed epoch of a snapshot source; matched by digest only.
    reuse: &'a [Piece],
}

impl Job<'_> {
    /// Read, transform, authorize and slice; hands each piece to the sink and pushes the change.
    ///
    /// A prefix-stable source (identity transform) streams: one piece is read, authorized,
    /// hashed, encoded and handed to the sink at a time. Any other source is read whole.
    fn write<F: FileSystem>(
        &self,
        fs: &mut F,
        mode: Mode,
        acc: &mut Acc<'_>,
    ) -> Result<(), SourceOutcome> {
        if self.admitted.prefix_stable() {
            self.write_streamed(fs, mode, acc)
        } else {
            self.write_whole(fs, mode, acc)
        }
    }
    /// A mismatching re-read prefix: restart as an epoch (no audit failure for a source that
    /// is not prefix-stable), or count the mismatch and park or restart.
    fn prefix_mismatch<F: FileSystem>(
        &self,
        fs: &mut F,
        acc: &mut Acc<'_>,
    ) -> Result<(), SourceOutcome> {
        if !self.admitted.prefix_stable() {
            return self.write(fs, Mode::Epoch(audit::never()), acc);
        }
        match audit::mismatch(&self.previous, self.env.now_ns) {
            audit::Mismatch::Park(reason) => Err(SourceOutcome::Parked(reason)),
            audit::Mismatch::Epoch(state) => {
                acc.audit.mismatch();
                self.write(fs, Mode::Epoch(state), acc)
            }
        }
    }
    fn range_of(&self, mode: &Mode) -> (u64, u64, Option<u32>) {
        match mode {
            Mode::Extend(from, ..) => (from.get(), from.get().max(self.observed.size), None),
            Mode::Epoch(_) | Mode::Replace => (0, self.observed.size, Some(self.next_epoch)),
        }
    }
    /// The whole-input path: the range is read as one buffer.
    fn write_whole<F: FileSystem>(
        &self,
        fs: &mut F,
        mode: Mode,
        acc: &mut Acc<'_>,
    ) -> Result<(), SourceOutcome> {
        let (from, to, epoch) = self.range_of(&mode);
        let resume = self.resume_state(&mode, from, to);
        let raw = match resume {
            Some(_) => read_checked(fs, self.src.handle, from..to)?,
            None => read_checked(fs, self.src.handle, 0..to)?,
        };
        let body = self.authorized_body(&raw)?;
        // Re-read prefix: the stored pieces before `from` are the bytes of an earlier read, so
        // the digest committed here is true of them only if this read reproduces them.
        if let (Mode::Extend(_, committed, ..), None) = (&mode, &resume) {
            let prefix = Offset::new(0)..Offset::new(from);
            if hash_span(body.bytes(), &prefix) != Some(*committed) {
                return self.prefix_mismatch(fs, acc);
            }
        }
        let (base, first) = if resume.is_some() {
            (Offset::new(from), 0)
        } else {
            (
                Offset::new(0),
                usize::try_from(from).map_err(|_| SourceOutcome::Raced)?,
            )
        };
        let stream_len = base
            .get()
            .checked_add(u64::try_from(body.bytes().len()).map_err(|_| SourceOutcome::Raced)?)
            .ok_or(SourceOutcome::Raced)?;
        let pieces = self.slice(&body, base, first, acc)?;
        let (whole, whole_state) = self.whole_digest(resume.as_ref(), body.bytes())?;
        let mut edges = Edges::default();
        edges.push(body.bytes());
        self.finish(
            fs,
            mode,
            resume.is_some().then_some(from),
            epoch,
            Done {
                pieces,
                whole,
                whole_state,
                stream_len,
                edges,
            },
            acc,
        )
    }
    /// The streaming path for a prefix-stable source: at most `piece_len` bytes are read at a
    /// time. A re-read prefix is hashed against the committed digest chunk by chunk and not
    /// uploaded; the new bytes are authorized, hashed, encoded and handed over per piece.
    fn write_streamed<F: FileSystem>(
        &self,
        fs: &mut F,
        mode: Mode,
        acc: &mut Acc<'_>,
    ) -> Result<(), SourceOutcome> {
        let (handle, step) = (self.src.handle, self.step());
        let (from, to, epoch) = self.range_of(&mode);
        let resume = self.resume_state(&mode, from, to);
        let mut edges = Edges::default();
        let mut authorized = false;
        let mut whole = resume.clone().unwrap_or_else(Sha256State::empty);
        if let (Mode::Extend(_, committed, ..), None) = (&mode, &resume) {
            let mut at = 0_u64;
            while at < from {
                let end = at.saturating_add(step).min(from);
                let raw = read_checked(fs, handle, at..end)?;
                let body = self.authorized_owned(raw)?;
                authorized = true;
                whole = whole
                    .extend(body.bytes())
                    .map_err(|_| SourceOutcome::Raced)?;
                edges.push(body.bytes());
                at = end;
            }
            if whole.digest() != Ok(*committed) {
                // The whole-input path reads the new bytes before it compares: a read that
                // fails or comes up short there ends the source before the mismatch counts.
                let mut at = from;
                while at < to {
                    let end = at.saturating_add(step).min(to);
                    read_checked(fs, handle, at..end)?;
                    at = end;
                }
                return self.prefix_mismatch(fs, acc);
            }
        }
        let mut pieces = Vec::new();
        let mut at = from;
        while at < to {
            let end = at.saturating_add(step).min(to);
            let raw = read_checked(fs, handle, at..end)?;
            let body = self.authorized_owned(raw)?;
            authorized = true;
            let len = body.bytes().len();
            whole = whole
                .extend(body.bytes())
                .map_err(|_| SourceOutcome::Raced)?;
            edges.push(body.bytes());
            // A piece whose transformed digest equals one of the last epoch's is already stored:
            // its encoding is reused and nothing is encoded or put. Digest only, never offset.
            let digest = manifest_hash(body.bytes());
            let previous = self.reuse.iter().find(|p| p.sha256() == digest);
            let described = if let Some(previous) = previous {
                let stored = StoredEncoding {
                    encoded_len: previous.encoded_len(),
                    encoded_sha256: previous.encoded_sha256(),
                };
                Piece::from_authorized(&body, Offset::new(at), 0, len, &stored)
                    .map_err(|_| SourceOutcome::Raced)?
            } else {
                let (piece, stored) = Published::piece(&body, 0, len).map_err(|e| match e {
                    PieceError::Range => SourceOutcome::Raced,
                    PieceError::Encode => SourceOutcome::Unavailable,
                })?;
                let described = Piece::from_authorized(&body, Offset::new(at), 0, len, &stored)
                    .map_err(|_| SourceOutcome::Raced)?;
                acc.sink
                    .put(described.object(), piece)
                    .map_err(|_| SourceOutcome::Unavailable)?;
                described
            };
            pieces.push(described);
            at = end;
        }
        if !authorized {
            self.authorized_body(&[])?;
        }
        self.finish(
            fs,
            mode,
            resume.is_some().then_some(from),
            epoch,
            Done {
                pieces,
                whole: whole.digest().map_err(|_| SourceOutcome::Raced)?,
                whole_state: Some(whole),
                stream_len: to,
                edges,
            },
            acc,
        )
    }
    /// Push the change and the index entry once every piece is handed over.
    fn finish<F: FileSystem>(
        &self,
        fs: &mut F,
        mode: Mode,
        resumed_from: Option<u64>,
        epoch: Option<u32>,
        done: Done,
        acc: &mut Acc<'_>,
    ) -> Result<(), SourceOutcome> {
        let src = self.src;
        let Done {
            pieces,
            whole,
            whole_state,
            stream_len,
            edges,
        } = done;
        let write = SourceWrite {
            id: src.id.clone(),
            kind: src.kind.clone(),
            path: src.path.clone(),
            class: src.class.clone(),
            epoch: self.stamp(epoch),
            size: stream_len,
            sha256: whole,
            pieces,
        };
        let audit = match &mode {
            Mode::Extend(..) => self.previous.clone(),
            Mode::Epoch(state) => state.clone(),
            Mode::Replace => audit::never(),
        };
        acc.changes.push(match mode {
            Mode::Extend(..) => Change::Extend(write),
            Mode::Epoch(_) => Change::Epoch(write),
            Mode::Replace => Change::Replace(write),
        });
        if src.class == SourceClass::Append {
            let known: &[(u64, Vec<u8>)] = match &mode {
                Mode::Extend(_, _, _, known) => known,
                Mode::Epoch(_) | Mode::Replace => &[],
            };
            let resumed = resumed_from.map(|from| (from, known));
            let windows = self.windows(fs, resumed, &edges, stream_len)?;
            let position = EpochPosition {
                epoch: epoch.unwrap_or(self.next_epoch.saturating_sub(1)),
                captured_to: Offset::new(stream_len),
                whole,
                rules: self.rules,
            };
            let state = self.state(position, windows, whole_state, audit);
            acc.updates
                .push(Update::Index(src.id.clone(), Box::new(state)));
        }
        Ok(())
    }
    /// A delta read is valid only when the transform is prefix-stable and a verified
    /// resumable state for `[0, from)` exists; otherwise the whole stream is read again.
    fn resume_state(&self, mode: &Mode, from: u64, to: u64) -> Option<Sha256State> {
        match mode {
            Mode::Extend(_, _, Some(state), _) if self.admitted.prefix_stable() && from <= to => {
                Some(state.clone())
            }
            _ => None,
        }
    }
    /// Bytes per read of a streamed source: one piece.
    fn step(&self) -> u64 {
        u64::try_from(self.env.piece_len.clamp(1, MAX_PIECE_BYTES)).unwrap_or(u64::MAX)
    }
    /// One independent audit read: stat, read `[0, size)`, stat again, and only when the source
    /// held still (identity, size and mtime equal to the captured stat throughout) transform it
    /// as a whole-stream capture transforms it and hash it with a fresh hasher. `None` is "no
    /// verdict": the source moved, or the seam or the transform failed.
    ///
    /// A prefix-stable source is read, authorized and hashed one chunk (at most `piece_len`
    /// bytes) at a time; its transform is the identity, so this is the digest of the whole
    /// transformed stream. Any other source is read whole.
    fn audit_read<F: FileSystem>(&self, fs: &mut F, state: &SourceState) -> Option<Sha256> {
        let handle = self.src.handle;
        let before = observe_now(fs, handle, self.env.now_ns)?;
        if !audit_eligible(&state.observed, &before, &before) {
            return None;
        }
        let read = if self.admitted.prefix_stable() {
            Audited::Digest(self.stream_digest(fs, before.size)?)
        } else {
            Audited::Raw(read_checked(fs, handle, 0..before.size).ok()?)
        };
        let after = observe_now(fs, handle, self.env.now_ns)?;
        if !audit_eligible(&state.observed, &before, &after) {
            return None;
        }
        match read {
            Audited::Digest(digest) => Some(digest),
            Audited::Raw(raw) => Some(manifest_hash(self.authorized_body(&raw).ok()?.bytes())),
        }
    }
    /// Digest of the first `size` bytes of a prefix-stable source, authorized chunk by chunk.
    fn stream_digest<F: FileSystem>(&self, fs: &mut F, size: u64) -> Option<Sha256> {
        let (handle, step) = (self.src.handle, self.step());
        let mut state = Sha256State::empty();
        let mut at = 0_u64;
        while at < size {
            let end = at.saturating_add(step).min(size);
            let raw = read_checked(fs, handle, at..end).ok()?;
            let body = self.authorized_owned(raw).ok()?;
            state = state.extend(body.bytes()).ok()?;
            at = end;
        }
        state.digest().ok()
    }
    /// Privacy transform, then the final authorization of the transformed bytes.
    fn authorized_body(&self, raw: &[u8]) -> Result<AuthorizedBody, SourceOutcome> {
        publishable(&self.admitted, raw, self.env.context)
    }
    /// As [`Self::authorized_body`] for a buffer that is not needed afterwards: no copy is made.
    fn authorized_owned(&self, raw: Vec<u8>) -> Result<AuthorizedBody, SourceOutcome> {
        publishable_owned(&self.admitted, raw, self.env.context)
    }
    /// Slice `body` from `at` into pieces of the configured size; `base` is the stream
    /// offset of `body[0]`. Pushes each piece's upload and returns the descriptors.
    fn slice(
        &self,
        body: &AuthorizedBody,
        base: Offset,
        mut at: usize,
        acc: &mut Acc<'_>,
    ) -> Result<Vec<Piece>, SourceOutcome> {
        let body_len = body.bytes().len();
        let step = self.env.piece_len.clamp(1, MAX_PIECE_BYTES);
        let mut pieces = Vec::new();
        while at < body_len {
            let end = at.saturating_add(step).min(body_len);
            let (piece, stored) = Published::piece(body, at, end).map_err(|e| match e {
                PieceError::Range => SourceOutcome::Raced,
                PieceError::Encode => SourceOutcome::Unavailable,
            })?;
            let described = Piece::from_authorized(body, base, at, end, &stored)
                .map_err(|_| SourceOutcome::Raced)?;
            pieces.push(described.clone());
            acc.sink
                .put(described.object(), piece)
                .map_err(|_| SourceOutcome::Unavailable)?;
            at = end;
        }
        Ok(pieces)
    }
    /// Whole-stream digest and its resumable state: continued from `resume`, otherwise
    /// recomputed from the whole body (kept only for prefix-stable sources).
    fn whole_digest(
        &self,
        resume: Option<&Sha256State>,
        body: &[u8],
    ) -> Result<(Sha256, Option<Sha256State>), SourceOutcome> {
        match resume {
            Some(state) => {
                let next = state.extend(body).map_err(|_| SourceOutcome::Raced)?;
                Ok((next.digest().map_err(|_| SourceOutcome::Raced)?, Some(next)))
            }
            None => Ok((manifest_hash(body), self.whole_state_of(body))),
        }
    }
    /// Resumable digest state over `bytes`, for prefix-stable sources only.
    fn whole_state_of(&self, bytes: &[u8]) -> Option<Sha256State> {
        if self.admitted.prefix_stable() {
            Sha256State::empty().extend(bytes).ok()
        } else {
            None
        }
    }
    /// Head and tail window digests of the new stream. A resumed write (`Some((from, known))`)
    /// holds only the delta's edges, so the windows are assembled from verified on-disk
    /// segments and the delta; otherwise `edges` are those of the whole stream.
    fn windows<F: FileSystem>(
        &self,
        fs: &mut F,
        resumed: Option<(u64, Segments<'_>)>,
        edges: &Edges,
        stream_len: u64,
    ) -> Result<WindowEvidence, SourceOutcome> {
        let (from, known) = resumed.unwrap_or((0, &[]));
        let handle = self.src.handle;
        let (head, tail) = window_ranges(Offset::new(stream_len));
        let head = stream_span(fs, handle, from, edges, &head, known)?;
        let tail = stream_span(fs, handle, from, edges, &tail, known)?;
        Ok(WindowEvidence {
            head: manifest_hash(&head),
            tail: manifest_hash(&tail),
        })
    }
    /// The append index entry for this capture.
    fn state(
        &self,
        position: EpochPosition,
        windows: WindowEvidence,
        whole_state: Option<Sha256State>,
        audit: AuditState,
    ) -> SourceState {
        SourceState {
            observed: self.observed.clone(),
            position,
            head: windows.head,
            tail: windows.tail,
            audit,
            whole_state,
        }
    }
    fn stamp(&self, epoch: Option<u32>) -> EpochStamp {
        EpochStamp {
            n: epoch.unwrap_or(self.next_epoch.saturating_sub(1)),
            writer: EpochWriter {
                version: self.env.header.writer.version.clone(),
                commit: self.env.header.writer.commit.clone(),
            },
            rules: self.rules,
        }
    }
}

/// What an audit read produced before the second stat.
enum Audited {
    /// The digest of a streamed prefix-stable source.
    Digest(Sha256),
    /// The raw bytes of any other source, authorized after the second stat.
    Raw(Vec<u8>),
}

/// The adopted remote prefix, streamed: only its windows and resumable digest are kept.
struct Adopted {
    digest: Sha256,
    state: Sha256State,
    windows: Option<WindowEvidence>,
}

/// What the planning rounds of one append source settled on.
struct Resolved {
    plan: Plan,
    /// The remote position adopted by this plan, with `prefix` its verified evidence.
    adopted: Option<EpochPosition>,
    prefix: Option<Adopted>,
    /// Head and tail segments read at the old boundary, reusable by the write.
    known: Known,
    /// The planner asked for the fresh stat to be stored.
    refresh_stat: bool,
    /// The audit compared equal to the committed digest in this round.
    matched: bool,
}

/// Head and tail windows at the old committed boundary, and the bytes they were read from.
fn read_windows<F: FileSystem>(
    fs: &mut F,
    handle: u64,
    captured_to: Offset,
) -> Result<(WindowEvidence, Known), SourceOutcome> {
    let (head, tail) = window_ranges(captured_to);
    let head_bytes = read_checked(fs, handle, head.start.get()..head.end.get())?;
    let tail_bytes = read_checked(fs, handle, tail.start.get()..tail.end.get())?;
    let evidence = WindowEvidence {
        head: manifest_hash(&head_bytes),
        tail: manifest_hash(&tail_bytes),
    };
    Ok((
        evidence,
        vec![
            (head.start.get(), head_bytes),
            (tail.start.get(), tail_bytes),
        ],
    ))
}

/// Read `[0, captured_to)` raw (as the adopt check hashes it) one chunk at a time, keeping its
/// digest, resumable state and windows but no more than the window bytes.
fn read_adopted<F: FileSystem>(
    fs: &mut F,
    handle: u64,
    captured_to: u64,
    step: u64,
) -> Result<Adopted, SourceOutcome> {
    let mut state = Sha256State::empty();
    let mut edges = Edges::default();
    let mut at = 0_u64;
    while at < captured_to {
        let end = at.saturating_add(step).min(captured_to);
        let raw = read_checked(fs, handle, at..end)?;
        state = state.extend(&raw).map_err(|_| SourceOutcome::Raced)?;
        edges.push(&raw);
        at = end;
    }
    Ok(Adopted {
        digest: state.digest().map_err(|_| SourceOutcome::Raced)?,
        windows: edges.windows(),
        state,
    })
}

/// Planning rounds an append source may need. An indexed source can ask for the audit recheck,
/// then the windows, then decide (3); an unindexed one asks for the adopted prefix, then
/// decides (2), and has no audit. The planner never asks for more than 3, so the fourth round
/// is slack and a fourth answer is a fault, not work.
const MAX_ROUNDS: usize = 4;

/// Plan, answering the planner's evidence requests through the seam, until it decides.
fn resolve<F: FileSystem>(
    fs: &mut F,
    job: &Job<'_>,
    index: Option<&SourceState>,
    remote: Option<&EpochPosition>,
    counts: &mut AuditCounts,
) -> Result<Resolved, SourceOutcome> {
    let handle = job.src.handle;
    let mut adopt = None;
    let mut windows = None;
    let mut prefix = None;
    let mut known = Known::new();
    let mut evidence = AuditEvidence::NotDue;
    let mut first_read = None;
    if let (Some(state), Some(remote)) = (index, remote)
        && audit::due(&state.audit, state.observed.observed_at_ns, job.env.now_ns)
    {
        // Cheap pre-check: a changed stat means no audit compares the captured input and no
        // byte is read.
        if audit_eligible(&state.observed, job.observed, job.observed) {
            first_read = job.audit_read(fs, state);
            evidence = audit::first(remote.whole, first_read);
        } else {
            evidence = AuditEvidence::Ineligible;
        }
        counts.verdict(evidence);
    }
    for _ in 0..MAX_ROUNDS {
        let planning = plan(&PlanInput {
            withheld: None,
            observation: Observation::Present(job.observed.clone()),
            strategy: Strategy::Append(match (index, remote) {
                (Some(state), _) => AppendBasis::Indexed {
                    state: Box::new(state.clone()),
                    windows: windows.clone(),
                },
                (None, Some(remote)) => AppendBasis::Unindexed {
                    remote: remote.clone(),
                    adopt_digest: adopt,
                },
                (None, None) => AppendBasis::Fresh,
            }),
            rules: job.rules,
            audit: evidence,
        });
        match planning {
            Planning::NeedWindows => {
                let state = index.ok_or(SourceOutcome::Unavailable)?;
                let (evidence, segments) = read_windows(fs, handle, state.position.captured_to)?;
                windows = Some(evidence);
                known = segments;
            }
            Planning::NeedAuditRecheck => {
                let (Some(state), Some(remote), Some(first)) = (index, remote, first_read) else {
                    return Err(SourceOutcome::Unavailable);
                };
                evidence = audit::second(remote.whole, first, job.audit_read(fs, state));
                counts.verdict(evidence);
            }
            Planning::Ready {
                plan: Plan::Adopt { remote },
                ..
            } => {
                let read = read_adopted(fs, handle, remote.captured_to.get(), job.step())?;
                adopt = Some(read.digest);
                prefix = Some(read);
            }
            Planning::Ready {
                plan,
                adopted,
                refresh_stat,
            } => {
                return Ok(Resolved {
                    plan,
                    adopted,
                    prefix,
                    known,
                    refresh_stat,
                    matched: evidence == AuditEvidence::Matched,
                });
            }
        }
    }
    Err(SourceOutcome::Unavailable)
}

fn append<F: FileSystem>(
    fs: &mut F,
    mut job: Job<'_>,
    local: &Local,
    remote: Option<EpochPosition>,
    acc: &mut Acc<'_>,
) -> SourceOutcome {
    let src = job.src;
    // The remote position is authoritative and the index is a deletable cache (DESIGN.md
    // 9.1, 9.3). An index that disagrees with the committed position (for example after a
    // lost reply whose commit did land) is ignored, so the planner adopts or replays.
    let index = local
        .index
        .clone()
        .filter(|state| remote.as_ref() == Some(&state.position))
        // A new FileId is not new content (brief 39): drop the index entry so the planner takes
        // the Unindexed basis, hashes `[0, captured_to)` and compares it with the remote whole
        // digest. Equal continues the epoch and the stat refresh records the new id; different
        // is a new epoch (AdoptMismatch). It also keeps the audit off the old basis.
        .filter(|state| state.observed.id == job.observed.id);
    job.previous = index
        .as_ref()
        .map_or_else(audit::never, |state| state.audit.clone());
    let resolved = match resolve(fs, &job, index.as_ref(), remote.as_ref(), &mut acc.audit) {
        Ok(resolved) => resolved,
        Err(outcome) => return outcome,
    };
    if let (Some(position), Some(read)) = (resolved.adopted, resolved.prefix)
        && let Some(windows) = read.windows
    {
        let whole = job.admitted.prefix_stable().then_some(read.state);
        let state = job.state(position, windows, whole, audit::never());
        acc.updates
            .push(Update::Index(src.id.clone(), Box::new(state)));
    }
    if resolved.matched {
        job.previous = audit::matched(job.env.now_ns);
    }
    if let (Plan::Unchanged, Some(state)) = (&resolved.plan, &index)
        && (resolved.matched || resolved.refresh_stat)
    {
        let next = audit::refreshed(
            state,
            job.observed,
            resolved.refresh_stat,
            job.previous.clone(),
        );
        acc.updates
            .push(Update::Index(src.id.clone(), Box::new(next)));
    }
    // An append plan exists only for a live remote position (indexed or unindexed basis).
    let mode = match resolved.plan {
        Plan::Append { from, .. } => Mode::Extend(
            from,
            match remote.as_ref() {
                Some(position) => position.whole,
                None => return SourceOutcome::Unavailable,
            },
            index
                .as_ref()
                .filter(|state| state.position.captured_to == from)
                .and_then(|state| {
                    state
                        .whole_state
                        .clone()
                        .filter(|st| st.digest() == Ok(state.position.whole))
                }),
            resolved.known,
        ),
        Plan::Replay { reason } => {
            if reason == ReplayReason::AuditMismatch {
                match audit::mismatch(&job.previous, job.env.now_ns) {
                    audit::Mismatch::Park(reason) => return SourceOutcome::Parked(reason),
                    audit::Mismatch::Epoch(state) => {
                        acc.audit.mismatch();
                        Mode::Epoch(state)
                    }
                }
            } else {
                Mode::Epoch(audit::never())
            }
        }
        Plan::Withheld { reason } => return SourceOutcome::Withheld(reason),
        _ => return SourceOutcome::Unchanged,
    };
    match job.write(fs, mode, acc) {
        Ok(()) => SourceOutcome::Planned,
        Err(outcome) => outcome,
    }
}
