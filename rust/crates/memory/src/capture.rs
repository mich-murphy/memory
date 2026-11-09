//! Capture wiring (DESIGN.md 7.3, 8): one synchronous step for one owner.
//!
//! observe (stat through [`FileSystem`]) → admit → [`plan`] (answering `NeedWindows` by
//! reading through the seam and re-planning) → read exactly the planned range → privacy
//! `transform` → `authorize` → slice into pieces → a [`Commit`] for [`crate::commit::commit`].
//!
//! # How the type system keeps unredacted bytes out of the store
//!
//! [`Commit`] and [`Upload`] are generic over the body. This module only ever builds
//! `Commit<Published>`. [`Published`] has a private field and its only piece constructor is a
//! private function that slices a `memory_core::privacy::AuthorizedBody`, a value only
//! `privacy::authorize` can produce (it consumes an `AdmittedSource` and a `RedactedBytes`,
//! neither of which has a public constructor, see the compile-fail tests in memory-core).
//! A production store adapter binds `Store::Body = Published`, so it cannot accept a raw
//! `Vec<u8>` piece. [`Published::document`] is the one public constructor; it exists because
//! `Store::document` must wrap the writer's own manifests and `current.json`, and this module
//! never calls it for a piece.
//!
//! ```compile_fail
//! let _ = memory::capture::Published(vec![1, 2, 3]);
//! ```
//!
//! Limitations: piece encoding is the identity (no zstd yet; `encoded_*` equal the raw
//! fields), the whole-stream digest is recomputed from a fresh read of `[0, to)` instead of a
//! resumable hash, `Refresh` of a racy stat is not persisted, and audit evidence is always
//! `NotDue`.
use crate::commit::{Commit, Upload};
use crate::environment::FileSystem;
use memory_core::domain::{
    AuditState, EpochPosition, Fingerprint, Observed, Offset, Plan, RulesDigest, Sha256,
    SnapshotFingerprint, SourceClass, SourceId, SourceKind, SourceState, WithholdReason,
};
use memory_core::manifest::{
    Change, EpochStamp, EpochWriter, Header, LaunchRecord, ManifestError, OwnerState, Piece,
    SourceWrite, manifest_hash, object_key,
};
use memory_core::planner::{
    AuditEvidence, Observation, PlanInput, Planning, Strategy, WindowEvidence, plan, window_ranges,
};
use memory_core::privacy::{
    Admission, AdmittedSource, Authorization, AuthorizedBody, PolicyContext, Profile, Reason,
    ResolvedSourceFacts, SanitizedLaunch, UnknownReason, admit, authorize, transform,
};
use std::collections::BTreeMap;

/// Largest raw piece (DESIGN.md 7.3).
pub const MAX_PIECE_BYTES: usize = 8 * 1024 * 1024;

/// The store body: bytes that passed privacy, or one of the writer's own documents.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Published(Vec<u8>);

impl Published {
    /// Wrap a metadata document (manifest, `current.json`). Never used for pieces here.
    pub fn document(bytes: Vec<u8>) -> Self {
        Self(bytes)
    }
    fn piece(body: &AuthorizedBody, start: usize, end: usize) -> Option<Self> {
        body.bytes().get(start..end).map(|b| Self(b.to_vec()))
    }
}
impl AsRef<[u8]> for Published {
    fn as_ref(&self) -> &[u8] {
        &self.0
    }
}

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
    /// A deletion was committed for the current absence.
    pub deletion_recorded: bool,
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
    /// New snapshot fingerprint.
    Snapshot(SourceId, SnapshotFingerprint, Option<RulesDigest>),
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
            }
            Self::Index(id, state) => locals.entry(id).or_default().index = Some(*state),
            Self::Snapshot(id, fp, rules) => {
                locals.entry(id).or_default().snapshot = Some((fp, rules))
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
    pub commit: Option<Commit<Published>>,
    /// Local updates to apply after the commit lands.
    pub updates: Vec<Update>,
    /// Outcome per source, in input order.
    pub outcomes: Vec<(SourceId, SourceOutcome)>,
}

#[derive(Default)]
struct Acc {
    changes: Vec<Change>,
    uploads: Vec<Upload<Published>>,
    updates: Vec<Update>,
}

/// Run one capture step for one owner.
pub fn capture<F: FileSystem>(
    fs: &mut F,
    sources: &[Source],
    locals: &Locals,
    remote: &OwnerState,
    env: &Env<'_>,
) -> Captured {
    let mut acc = Acc::default();
    let mut outcomes = Vec::new();
    let none = Local::default();
    for src in sources {
        let local = locals.get(&src.id).unwrap_or(&none);
        let outcome = step(fs, src, local, remote, env, &mut acc);
        outcomes.push((src.id.clone(), outcome));
    }
    let commit = (!acc.changes.is_empty()).then(|| Commit {
        header: env.header.clone(),
        changes: acc.changes,
        uploads: acc.uploads,
    });
    Captured {
        commit,
        updates: acc.updates,
        outcomes,
    }
}

/// Attach sanitized launch evidence to a commit header (DESIGN.md 6.3, 8.4).
///
/// Only a [`SanitizedLaunch`] can become a `LaunchRecord` (`from_sanitized`), so raw argv or
/// environment cannot reach the manifest through this function. Records are de-duplicated by
/// digest, both against the header's existing records and within `launches`.
///
/// # Errors
/// [`ManifestError`] if a record's digest input cannot be encoded; the header is then left
/// unchanged.
pub fn attach_launches(
    commit: &mut Commit<Published>,
    launches: &[SanitizedLaunch],
) -> Result<(), ManifestError> {
    let mut added: Vec<LaunchRecord> = Vec::new();
    for launch in launches {
        let record = LaunchRecord::from_sanitized(launch)?;
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
    Ok(())
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
        Reason::ScrubFailed | Reason::ContextChanged => WithholdReason::ScrubFailed,
    }
}

fn hash_span(bytes: &[u8], range: &std::ops::Range<Offset>) -> Option<Sha256> {
    let start = usize::try_from(range.start.get()).ok()?;
    let end = usize::try_from(range.end.get()).ok()?;
    bytes.get(start..end).map(manifest_hash)
}

fn windows_of(stream: &[u8]) -> Option<WindowEvidence> {
    let (head, tail) = window_ranges(Offset::new(u64::try_from(stream.len()).ok()?));
    Some(WindowEvidence {
        head: hash_span(stream, &head)?,
        tail: hash_span(stream, &tail)?,
    })
}

fn read_exact<F: FileSystem>(fs: &mut F, handle: u64, len: u64) -> Result<Vec<u8>, SourceOutcome> {
    let bytes = fs
        .read(handle, 0..len)
        .map_err(|_| SourceOutcome::Unavailable)?;
    if u64::try_from(bytes.len()) == Ok(len) {
        Ok(bytes)
    } else {
        Err(SourceOutcome::Raced)
    }
}

fn step<F: FileSystem>(
    fs: &mut F,
    src: &Source,
    local: &Local,
    remote: &OwnerState,
    env: &Env<'_>,
    acc: &mut Acc,
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
    let rules = matches!(src.facts.profile, Profile::Config | Profile::TextAsset)
        .then_some(env.context.rules)
        .flatten()
        .map(|r| RulesDigest(Sha256::from_bytes(r)));
    let stat = match fs.stat(src.handle) {
        Ok(stat) => stat,
        Err(_) => return SourceOutcome::Unavailable,
    };
    let Some(stat) = stat else {
        let consecutive = local.missing.saturating_add(1);
        let observation = if recorded {
            Observation::DeletionRecorded
        } else {
            acc.updates
                .push(Update::Missing(src.id.clone(), consecutive));
            Observation::Missing { consecutive }
        };
        let planning = plan(&PlanInput {
            withheld: None,
            observation,
            strategy: Strategy::Append {
                index: None,
                remote: None,
                adopt_digest: None,
                windows: None,
            },
            rules,
            audit: AuditEvidence::NotDue,
        });
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
    let observed = Observed {
        id: stat.id.clone(),
        size: stat.size,
        mtime_ns: stat.mtime_ns,
        observed_at_ns: env.now_ns,
    };
    acc.updates.push(Update::Present(src.id.clone()));
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
    };
    match src.class {
        SourceClass::Snapshot => {
            let fp = SnapshotFingerprint::Plain(Fingerprint {
                id: stat.id,
                size: stat.size,
                mtime_ns: stat.mtime_ns,
            });
            let planning = plan(&PlanInput {
                withheld: None,
                observation: Observation::Present(observed.clone()),
                strategy: Strategy::Snapshot {
                    previous: local.snapshot.as_ref().map(|(f, _)| f.clone()),
                    current: fp.clone(),
                    previous_rules: local.snapshot.as_ref().and_then(|(_, r)| *r),
                },
                rules,
                audit: AuditEvidence::NotDue,
            });
            match planning {
                Planning::Ready {
                    plan: Plan::Snapshot | Plan::Replay { .. },
                    ..
                } => match job.write(fs, Mode::Replace, acc) {
                    Ok(()) => {
                        acc.updates
                            .push(Update::Snapshot(src.id.clone(), fp, rules));
                        SourceOutcome::Planned
                    }
                    Err(outcome) => outcome,
                },
                Planning::Ready { .. } => SourceOutcome::Unchanged,
                Planning::NeedWindows | Planning::NeedAuditRecheck => SourceOutcome::Unavailable,
            }
        }
        SourceClass::Append => {
            let remote_pos = live.and_then(|e| e.epochs.last()).map(|l| EpochPosition {
                epoch: l.epoch.n,
                captured_to: Offset::new(l.size),
                whole: l.sha256,
                rules: l.epoch.rules,
            });
            append(fs, &job, local, remote_pos, live.is_some(), acc)
        }
    }
}

enum Mode {
    Extend(Offset),
    Epoch,
    Replace,
}

struct Job<'a> {
    src: &'a Source,
    admitted: AdmittedSource,
    observed: &'a Observed,
    env: &'a Env<'a>,
    rules: Option<RulesDigest>,
    next_epoch: u32,
}

impl Job<'_> {
    /// Read, transform, authorize and slice; pushes the change and its uploads.
    fn write<F: FileSystem>(
        &self,
        fs: &mut F,
        mode: Mode,
        acc: &mut Acc,
    ) -> Result<(), SourceOutcome> {
        let src = self.src;
        let (from, to, epoch) = match mode {
            Mode::Extend(from) => (from.get(), from.get().max(self.observed.size), None),
            Mode::Epoch | Mode::Replace => (0, self.observed.size, Some(self.next_epoch)),
        };
        let to = if matches!(mode, Mode::Extend(_)) {
            self.observed.size.max(to)
        } else {
            to
        };
        let raw = read_exact(fs, src.handle, to)?;
        let redacted =
            transform(&self.admitted, &raw).map_err(|r| SourceOutcome::Withheld(withhold_of(r)))?;
        let body = match authorize(self.admitted_clone()?, redacted, self.env.context) {
            Authorization::Allow(body) => body,
            Authorization::Deny(r) => return Err(SourceOutcome::Withheld(withhold_of(r))),
            Authorization::Unknown(u) => return Err(SourceOutcome::Blocked(u)),
        };
        let stream_len = body.bytes().len();
        let start = usize::try_from(from).map_err(|_| SourceOutcome::Raced)?;
        let mut pieces = Vec::new();
        let mut at = start;
        let step = self.env.piece_len.clamp(1, MAX_PIECE_BYTES);
        while at < stream_len {
            let end = at.saturating_add(step).min(stream_len);
            let piece = Published::piece(&body, at, end).ok_or(SourceOutcome::Raced)?;
            let chunk = piece.as_ref();
            let sha = manifest_hash(chunk);
            let len = u64::try_from(chunk.len()).map_err(|_| SourceOutcome::Raced)?;
            pieces.push(Piece::new(
                Offset::new(u64::try_from(at).map_err(|_| SourceOutcome::Raced)?),
                len,
                sha,
                len,
                sha,
            ));
            acc.uploads.push(Upload {
                key: object_key(&sha),
                body: piece,
            });
            at = end;
        }
        let whole = manifest_hash(body.bytes());
        let write = SourceWrite {
            id: src.id.clone(),
            kind: src.kind.clone(),
            path: src.path.clone(),
            class: src.class.clone(),
            epoch: self.stamp(epoch),
            size: u64::try_from(stream_len).map_err(|_| SourceOutcome::Raced)?,
            sha256: whole,
            pieces,
        };
        acc.changes.push(match mode {
            Mode::Extend(_) => Change::Extend(write),
            Mode::Epoch => Change::Epoch(write),
            Mode::Replace => Change::Replace(write),
        });
        if src.class == SourceClass::Append {
            let w = windows_of(body.bytes()).ok_or(SourceOutcome::Raced)?;
            let n = epoch.unwrap_or(self.next_epoch.saturating_sub(1));
            acc.updates.push(Update::Index(
                src.id.clone(),
                Box::new(SourceState {
                    observed: self.observed.clone(),
                    trust: self.observed.trust(),
                    position: EpochPosition {
                        epoch: n,
                        captured_to: Offset::new(u64::try_from(stream_len).unwrap_or(u64::MAX)),
                        whole,
                        rules: self.rules,
                    },
                    head: w.head,
                    tail: w.tail,
                    audit: AuditState {
                        last_checked_at_ns: None,
                        consecutive_failures: 0,
                    },
                }),
            ));
        }
        Ok(())
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
    /// `authorize` consumes the admission, so re-admit from the same facts and context.
    fn admitted_clone(&self) -> Result<AdmittedSource, SourceOutcome> {
        match admit(self.env.context, self.src.facts.clone()) {
            Admission::Allow(a) => Ok(a),
            Admission::Deny(r) => Err(SourceOutcome::Withheld(withhold_of(r))),
            Admission::Unknown(u) => Err(SourceOutcome::Blocked(u)),
        }
    }
}

fn append<F: FileSystem>(
    fs: &mut F,
    job: &Job<'_>,
    local: &Local,
    remote: Option<EpochPosition>,
    live: bool,
    acc: &mut Acc,
) -> SourceOutcome {
    let src = job.src;
    // The remote position is authoritative and the index is a deletable cache (DESIGN.md
    // 9.1, 9.3). An index that disagrees with the committed position (for example after a
    // lost reply whose commit did land) is ignored, so the planner adopts or replays.
    let index = local
        .index
        .clone()
        .filter(|state| remote.as_ref() == Some(&state.position));
    let mut adopt = None;
    let mut windows = None;
    let mut prefix = None;
    let mut rounds = 0_u8;
    let (decision, adopted) = loop {
        rounds += 1;
        if rounds > 4 {
            return SourceOutcome::Unavailable;
        }
        let planning = plan(&PlanInput {
            withheld: None,
            observation: Observation::Present(job.observed.clone()),
            strategy: Strategy::Append {
                index: index.clone(),
                remote: remote.clone(),
                adopt_digest: adopt,
                windows: windows.clone(),
            },
            rules: job.rules,
            audit: AuditEvidence::NotDue,
        });
        match planning {
            Planning::NeedWindows => {
                let Some(state) = &index else {
                    return SourceOutcome::Unavailable;
                };
                let (head, tail) = window_ranges(state.position.captured_to);
                let mut digests = Vec::new();
                for range in [head, tail] {
                    let len = range.end.get().saturating_sub(range.start.get());
                    let Ok(bytes) = fs.read(src.handle, range.start.get()..range.end.get()) else {
                        return SourceOutcome::Unavailable;
                    };
                    if u64::try_from(bytes.len()) != Ok(len) {
                        return SourceOutcome::Raced;
                    }
                    digests.push(manifest_hash(&bytes));
                }
                let (Some(h), Some(t)) = (digests.first(), digests.get(1)) else {
                    return SourceOutcome::Unavailable;
                };
                windows = Some(WindowEvidence { head: *h, tail: *t });
            }
            Planning::NeedAuditRecheck => return SourceOutcome::Unavailable,
            Planning::Ready {
                plan: Plan::Adopt { remote },
                ..
            } => match read_exact(fs, src.handle, remote.captured_to.get()) {
                Ok(bytes) => {
                    adopt = Some(manifest_hash(&bytes));
                    prefix = Some(bytes);
                }
                Err(outcome) => return outcome,
            },
            Planning::Ready { plan, adopted, .. } => break (plan, adopted),
        }
    };
    if let (Some(pos), Some(bytes)) = (adopted, &prefix)
        && let Some(w) = windows_of(bytes)
    {
        acc.updates.push(Update::Index(
            src.id.clone(),
            Box::new(SourceState {
                observed: job.observed.clone(),
                trust: job.observed.trust(),
                position: pos,
                head: w.head,
                tail: w.tail,
                audit: AuditState {
                    last_checked_at_ns: None,
                    consecutive_failures: 0,
                },
            }),
        ));
    }
    let mode = match decision {
        Plan::Append { from, .. } if live => Mode::Extend(from),
        Plan::Replay { .. } => Mode::Epoch,
        Plan::Withheld { reason } => return SourceOutcome::Withheld(reason),
        _ => return SourceOutcome::Unchanged,
    };
    match job.write(fs, mode, acc) {
        Ok(()) => SourceOutcome::Planned,
        Err(outcome) => outcome,
    }
}
