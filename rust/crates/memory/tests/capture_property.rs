//! Property 4 (DESIGN.md 15) driven by the REAL `memory::capture::capture` step followed by
//! `memory::commit::commit` against a deterministic fake store, in both Hegel and
//! proptest-state-machine.
//!
//! The filesystem is synthetic (an in-memory map behind the `FileSystem` seam) and the piece
//! encoding is the identity (no zstd yet). There is no real shell, R2 or daemon. This replaces
//! the stand-in planner of `commit_protocol.rs`; the store faults are the same scripted ones.
use hegel::{TestCase, generators as gs};
use memory::capture::{Captured, Env, Local, Locals, Published, Source, capture};
use memory::commit::{Base, Failure, Landed, commit, manifest_key, recover_base};
use memory::environment::{FileSystem, Stat, Store, StoreError, Stored};
use memory_core::domain::{
    Agent, ArchiveId, Birth, CommitOutcome, ETag, FileId, MachineId, Owner, SessionId, SessionKey,
    Sha256, SourceClass, SourceId, SourceKind,
};
use memory_core::limits::current_key;
use memory_core::manifest::{
    Current, EpochRecord, Header, OwnerState, ProjectedKind, StoredEncoding, Timestamp, Writer,
    build, fold, manifest_hash, parse_current, parse_manifest, recover_projection,
};
use memory_core::privacy::{PolicyContext, Profile, ResolvedSourceFacts, rules_digest};
use proptest::prelude::*;
use proptest::test_runner::{Config, TestRunner};
use proptest_state_machine::{ReferenceStateMachine, StateMachineTest};
use std::collections::BTreeMap;
use std::sync::atomic::{AtomicU64, Ordering};

type R<T = ()> = Result<T, String>;

fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}
fn need<T>(value: Option<T>, what: &str) -> R<T> {
    value.ok_or_else(|| what.to_owned())
}
fn ensure(condition: bool, what: &str) -> R {
    if condition {
        Ok(())
    } else {
        Err(what.to_owned())
    }
}

const SOURCES: u8 = 2;
const PIECE: usize = 4;
const SETTLE_ROUNDS: usize = 5;

fn sha(bytes: &[u8]) -> Sha256 {
    manifest_hash(bytes)
}
fn len64(bytes: &[u8]) -> u64 {
    u64::try_from(bytes.len()).unwrap_or(u64::MAX)
}
fn owner() -> R<Owner> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
        machine: MachineId::try_from("m".to_owned()).map_err(e)?,
        agent: Agent::Claude,
        session: SessionId::try_from("s1".to_owned()).map_err(e)?,
    }))
}
fn ts() -> R<Timestamp> {
    Timestamp::try_from("2026-11-07T10:30:00Z".to_owned()).map_err(e)
}
fn header(owner: &Owner, who: &str) -> R<Header> {
    Ok(Header {
        owner: owner.clone(),
        captured_at: ts()?,
        writer: Writer {
            name: who.to_owned(),
            version: "0.1.0".to_owned(),
            commit: "abc".to_owned(),
        },
        launch: vec![],
    })
}
fn src_id(i: u8) -> R<SourceId> {
    SourceId::try_from(format!("src{i}")).map_err(e)
}
fn handle(i: u8) -> u64 {
    u64::from(i) + 1
}

// ------------------------------------------------------------------ synthetic filesystem

#[derive(Default)]
struct MemFs {
    files: BTreeMap<u64, Vec<u8>>,
    mtimes: BTreeMap<u64, i64>,
    clock: i64,
}
impl MemFs {
    /// Write a file and advance its mtime, as a real write would.
    fn set(&mut self, source: u64, bytes: &[u8]) {
        self.clock += 1_000;
        self.files.insert(source, bytes.to_vec());
        self.mtimes.insert(source, self.clock);
    }
}
impl FileSystem for MemFs {
    type Error = ();
    fn stat(&mut self, source: u64) -> Result<Option<Stat>, ()> {
        Ok(self.files.get(&source).map(|b| Stat {
            id: FileId {
                dev: 1,
                ino: source,
                birth: Birth::Unavailable,
            },
            size: len64(b),
            mtime_ns: self.mtimes.get(&source).copied().unwrap_or(0),
        }))
    }
    fn stat_len(&mut self, source: u64) -> Result<u64, ()> {
        Ok(self.stat(source)?.ok_or(())?.size)
    }
    fn read(&mut self, source: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, ()> {
        let bytes = self.files.get(&source).ok_or(())?;
        let start = usize::try_from(range.start).map_err(|_| ())?;
        let end = usize::try_from(range.end).map_err(|_| ())?.min(bytes.len());
        Ok(bytes.get(start..end).unwrap_or_default().to_vec())
    }
}

fn source(i: u8) -> R<Source> {
    let path = format!("projects/p/s{i}.jsonl");
    Ok(Source {
        handle: handle(i),
        id: src_id(i)?,
        kind: SourceKind::ClaudeTranscript,
        class: SourceClass::Append,
        path: path.clone(),
        facts: ResolvedSourceFacts {
            identities: vec![path],
            file_identity: Some(u128::from(handle(i))),
            ancestor_identities: vec![],
            path_resolved: true,
            root_resolved: true,
            owner: Some("o".to_owned()),
            profile: Profile::Transcript,
        },
    })
}
fn policy() -> PolicyContext {
    PolicyContext {
        valid: true,
        revision: "r".to_owned(),
        rules: Some(rules_digest()),
        collector_paths: vec![],
        collector_identities: vec![],
    }
}

// ------------------------------------------------------------------ fake store

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Kind {
    Object,
    Manifest,
    Current,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Op {
    Put(Kind),
    Get(Kind),
}
/// A foreign writer's documents, applied just before the next `current.json` PUT.
#[derive(Clone, Debug)]
struct Foreign {
    manifest_key: String,
    manifest: Vec<u8>,
    current: Vec<u8>,
}
#[derive(Clone, Debug)]
enum Effect {
    /// Reply with the error without landing anything.
    FailBefore(StoreError),
    /// Land the write, then lose the reply (or answer 5xx), optionally going offline after.
    LandThen { err: StoreError, then_offline: bool },
    /// A foreign writer advances the owner just before this request.
    Foreign(Foreign),
}
#[derive(Clone, Debug)]
struct Rule {
    op: Op,
    effect: Effect,
}
enum Fence<'a> {
    Absent,
    Match(&'a ETag),
}

/// Deterministic object store with S3/R2 conditional-write semantics and scripted faults.
#[derive(Default)]
struct FakeStore {
    objects: BTreeMap<String, (Vec<u8>, u64)>,
    version: u64,
    offline: bool,
    revoked: bool,
    rules: Vec<Rule>,
}

fn kind_of(key: &str) -> Kind {
    if key.starts_with("manifests/") {
        Kind::Manifest
    } else if key.ends_with("current.json") {
        Kind::Current
    } else {
        Kind::Object
    }
}
fn etag_of(version: u64) -> Result<ETag, StoreError> {
    ETag::try_from(format!("\"v{version}\"")).map_err(|_| StoreError::Server)
}

impl FakeStore {
    fn arm(&mut self, op: Op, effect: Effect) {
        self.rules.push(Rule { op, effect });
    }
    fn clear_transient(&mut self) {
        self.rules
            .retain(|r| matches!(r.effect, Effect::Foreign(_)));
    }
    fn foreign_armed(&self) -> bool {
        self.rules
            .iter()
            .any(|r| matches!(r.effect, Effect::Foreign(_)))
    }
    fn peek(&self, key: &str) -> Option<&[u8]> {
        self.objects.get(key).map(|(body, _)| body.as_slice())
    }
    fn write(&mut self, key: &str, body: Vec<u8>) {
        self.version += 1;
        self.objects.insert(key.to_owned(), (body, self.version));
    }
    fn remove(&mut self, key: &str) {
        self.objects.remove(key);
    }
    fn intercept(&mut self, op: Op) -> Option<Effect> {
        let index = self.rules.iter().position(|r| r.op == op)?;
        Some(self.rules.remove(index).effect)
    }
    fn side_effect(&mut self, key: &str, effect: Option<&Effect>) {
        if let Some(Effect::Foreign(f)) = effect {
            if !self.objects.contains_key(&f.manifest_key) {
                self.write(&f.manifest_key, f.manifest.clone());
            }
            self.write(key, f.current.clone());
        }
    }
    fn gate(&self) -> Result<(), StoreError> {
        if self.revoked {
            Err(StoreError::Auth)
        } else if self.offline {
            Err(StoreError::Offline)
        } else {
            Ok(())
        }
    }
    fn put(&mut self, key: &str, body: Vec<u8>, fence: &Fence<'_>) -> Result<ETag, StoreError> {
        let effect = self.intercept(Op::Put(kind_of(key)));
        self.side_effect(key, effect.as_ref());
        self.gate()?;
        if let Some(Effect::FailBefore(err)) = &effect {
            return Err(*err);
        }
        let existing = self.objects.get(key).map(|(_, v)| *v);
        let allowed = match (fence, existing) {
            (Fence::Absent, None) => true,
            (Fence::Absent, Some(_)) => false,
            (Fence::Match(_), None) => return Err(StoreError::NotFound),
            (Fence::Match(tag), Some(v)) => etag_of(v)? == **tag,
        };
        if !allowed {
            return Err(StoreError::PreconditionFailed);
        }
        self.write(key, body);
        let tag = etag_of(self.version)?;
        match effect {
            Some(Effect::LandThen { err, then_offline }) => {
                self.offline |= then_offline;
                Err(err)
            }
            _ => Ok(tag),
        }
    }
}

impl Store for FakeStore {
    fn put_if_absent(&mut self, key: &str, body: Published) -> Result<ETag, StoreError> {
        self.put(key, body.as_ref().to_vec(), &Fence::Absent)
    }
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &ETag,
        body: Published,
    ) -> Result<ETag, StoreError> {
        self.put(key, body.as_ref().to_vec(), &Fence::Match(etag))
    }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        let effect = self.intercept(Op::Get(kind_of(key)));
        self.side_effect(key, effect.as_ref());
        self.gate()?;
        if let Some(Effect::FailBefore(err) | Effect::LandThen { err, .. }) = &effect {
            return Err(*err);
        }
        let (body, version) = self.objects.get(key).ok_or(StoreError::NotFound)?;
        Ok(Stored {
            body: body.clone(),
            etag: etag_of(*version)?,
        })
    }
}

// ------------------------------------------------------------------ independent reader

struct Chain {
    folded: memory_core::manifest::Folded,
    current: Current,
    hashes: Vec<(u64, Sha256)>,
}

/// Walk `current.json` back to a checkpoint straight from store contents (no fault flags,
/// no driver code) and fold it with memory-core.
fn read_chain(store: &FakeStore, owner: &Owner) -> R<Option<Chain>> {
    let Some(pointer) = store.peek(&current_key(owner)) else {
        return Ok(None);
    };
    let current = parse_current(pointer).map_err(e)?;
    let mut hash = current.manifest;
    let mut raw: Vec<(Sha256, u64, Vec<u8>)> = Vec::new();
    loop {
        ensure(raw.len() <= 64, "chain longer than 64")?;
        let bytes = need(
            store.peek(&manifest_key(&hash)),
            "referenced manifest missing",
        )?;
        ensure(sha(bytes) == hash, "manifest hash mismatch")?;
        let projection = recover_projection(bytes).map_err(e)?;
        raw.push((hash, projection.seq, bytes.to_vec()));
        match projection.kind {
            ProjectedKind::Checkpoint => break,
            ProjectedKind::Delta => hash = need(projection.previous, "delta without previous")?,
        }
    }
    raw.reverse();
    let refs: Vec<&[u8]> = raw.iter().map(|(_, _, b)| b.as_slice()).collect();
    let folded = fold(&refs).map_err(e)?;
    ensure(
        folded.position.seq == current.seq,
        "current.seq != head seq",
    )?;
    ensure(
        folded.position.manifest == current.manifest,
        "current names another manifest",
    )?;
    ensure(folded.owner == *owner, "owner mismatch")?;
    for (_, _, bytes) in &raw {
        parse_manifest(bytes)
            .map_err(e)?
            .verify_encodings(|key| {
                store.peek(key).map(|b| StoredEncoding {
                    encoded_len: len64(b),
                    encoded_sha256: sha(b),
                })
            })
            .map_err(e)?;
    }
    Ok(Some(Chain {
        folded,
        current,
        hashes: raw.iter().map(|(h, s, _)| (*s, *h)).collect(),
    }))
}

/// Reassemble an epoch from stored objects. The piece encoding is the identity, so the stored
/// bytes are the raw bytes and both descriptors must match them.
fn reassemble(store: &FakeStore, record: &EpochRecord) -> R<Vec<u8>> {
    let mut out: Vec<u8> = Vec::new();
    for piece in &record.pieces {
        let stored = need(store.peek(&piece.object), "piece object missing")?;
        ensure(
            sha(stored) == piece.encoded_sha256 && len64(stored) == piece.encoded_len,
            "descriptor differs from stored encoding",
        )?;
        ensure(
            sha(stored) == piece.sha256 && len64(stored) == piece.len,
            "piece bytes differ from descriptor",
        )?;
        ensure(len64(&out) == piece.offset.get(), "piece offset gap")?;
        out.extend_from_slice(stored);
    }
    ensure(
        sha(&out) == record.sha256 && len64(&out) == record.size,
        "epoch digest differs",
    )?;
    Ok(out)
}

struct Observed {
    /// Latest epoch bytes per source; `None` for a deleted source.
    sources: BTreeMap<u8, Option<Vec<u8>>>,
    max_epochs: usize,
    seq: u64,
    hash: Sha256,
    hashes: Vec<(u64, Sha256)>,
}
type History = BTreeMap<u8, Vec<Vec<u8>>>;
type Disk = BTreeMap<u8, Vec<u8>>;

/// Safety: every referenced object exists and matches its descriptor, the chain is
/// consistent, and every committed epoch is a prefix of bytes the source once held.
fn observe(store: &FakeStore, owner: &Owner, history: &History) -> R<Option<Observed>> {
    let Some(chain) = read_chain(store, owner)? else {
        return Ok(None);
    };
    let mut sources = BTreeMap::new();
    let mut max_epochs = 0;
    for (id, entry) in &chain.folded.state.sources {
        let i = need(
            (0..SOURCES).find(|i| src_id(*i).is_ok_and(|s| s == *id)),
            "unknown source id",
        )?;
        max_epochs = max_epochs.max(entry.epochs.len());
        let versions = need(history.get(&i), "source has no history")?;
        let mut latest = None;
        for record in &entry.epochs {
            let bytes = reassemble(store, record)?;
            ensure(
                versions.iter().any(|v| v.starts_with(&bytes)),
                "1a violated: committed bytes were never held by the source",
            )?;
            latest = Some(bytes);
        }
        sources.insert(
            i,
            if entry.deleted_at.is_some() {
                None
            } else {
                latest
            },
        );
    }
    Ok(Some(Observed {
        sources,
        max_epochs,
        seq: chain.current.seq,
        hash: chain.current.manifest,
        hashes: chain.hashes,
    }))
}

fn make_foreign(store: &FakeStore, owner: &Owner, who: &str) -> R<Foreign> {
    let head = read_chain(store, owner)?;
    let built = build(
        head.as_ref().map(|c| (&c.folded.position, &c.folded.state)),
        header(owner, who)?,
        vec![],
    )
    .map_err(e)?;
    Ok(Foreign {
        manifest_key: manifest_key(&built.hash),
        manifest: built.bytes,
        current: Current::new(built.hash, built.position.seq, ts()?)
            .to_bytes()
            .map_err(e)?,
    })
}

// ------------------------------------------------------------------ coverage guard

/// Coverage counters of the shapes a run must have produced (DESIGN.md 15).
struct Shapes {
    multi_epoch: AtomicU64,
    restart: AtomicU64,
    crash_after_manifest: AtomicU64,
    lost_reply: AtomicU64,
    delete_index: AtomicU64,
}
impl Shapes {
    const fn new() -> Self {
        Self {
            multi_epoch: AtomicU64::new(0),
            restart: AtomicU64::new(0),
            crash_after_manifest: AtomicU64::new(0),
            lost_reply: AtomicU64::new(0),
            delete_index: AtomicU64::new(0),
        }
    }
    fn counts(&self) -> Vec<(&'static str, u64)> {
        let load = |c: &AtomicU64| c.load(Ordering::Relaxed);
        vec![
            ("multi-epoch", load(&self.multi_epoch)),
            ("restart", load(&self.restart)),
            ("crash-after-manifest", load(&self.crash_after_manifest)),
            ("lost-reply", load(&self.lost_reply)),
            ("delete-index", load(&self.delete_index)),
        ]
    }
}
fn bump(counter: &AtomicU64) {
    counter.fetch_add(1, Ordering::Relaxed);
}
/// Fails when any required shape was never produced over the run.
fn require_all(counts: &[(&'static str, u64)]) -> Result<(), String> {
    let missing: Vec<_> = counts
        .iter()
        .filter(|(_, n)| *n == 0)
        .map(|(k, _)| *k)
        .collect();
    if missing.is_empty() && !counts.is_empty() {
        Ok(())
    } else {
        Err(format!("coverage guard: run never produced {missing:?}"))
    }
}
static HEGEL_SHAPES: Shapes = Shapes::new();
static PROPTEST_SHAPES: Shapes = Shapes::new();

// ------------------------------------------------------------------ system under test

#[derive(Debug, PartialEq)]
enum Report {
    Parked,
    Failed,
    Idle,
    Landed(CommitOutcome),
    Crashed { manifest: bool },
}
#[derive(Clone, Copy, PartialEq, Eq)]
enum CrashAt {
    BeforePut,
    AfterPiece,
    AfterManifest,
}

fn run_capture(
    fs: &mut MemFs,
    sources: &[Source],
    locals: &Locals,
    remote: &OwnerState,
    context: &PolicyContext,
    owner: &Owner,
) -> R<Captured> {
    let env = Env {
        header: header(owner, "memory")?,
        context,
        now_ns: 10_000_000_000,
        piece_len: PIECE,
    };
    Ok(capture(fs, sources, locals, remote, &env))
}

/// The shell's view: filesystem, store, locals, commit base, park flag.
struct Sut {
    fs: MemFs,
    store: FakeStore,
    owner: Owner,
    sources: Vec<Source>,
    locals: Locals,
    context: PolicyContext,
    base: Option<Base>,
    needs_recover: bool,
    parked: bool,
    head: Option<(u64, Sha256)>,
}

impl Sut {
    fn new() -> R<Self> {
        Ok(Self {
            fs: MemFs::default(),
            store: FakeStore::default(),
            owner: owner()?,
            sources: (0..SOURCES).map(source).collect::<R<Vec<_>>>()?,
            locals: Locals::new(),
            context: policy(),
            base: None,
            needs_recover: true,
            parked: false,
            head: None,
        })
    }
    /// Mirror the model's file for `src` into the synthetic filesystem.
    fn sync_file(&mut self, src: u8, after: &Reference) {
        match after.files.get(&src) {
            Some(bytes) => {
                if self.fs.files.get(&handle(src)) != Some(bytes) {
                    self.fs.set(handle(src), bytes);
                }
            }
            None => {
                self.fs.files.remove(&handle(src));
            }
        }
    }
    /// Recover the base from the store when needed. `Some(report)` means stop.
    fn ensure_base(&mut self) -> R<Option<Report>> {
        if self.parked {
            return Ok(Some(Report::Parked));
        }
        if self.needs_recover {
            match recover_base(&mut self.store, &self.owner) {
                Ok(base) => {
                    self.base = base;
                    self.needs_recover = false;
                }
                Err(Failure::NewerSchema) => {
                    self.parked = true;
                    return Ok(Some(Report::Parked));
                }
                Err(Failure::NotLanded(_)) => return Ok(Some(Report::Failed)),
                Err(other) => return Err(format!("recover: {other:?}")),
            }
        }
        Ok(None)
    }
    fn absorb(&mut self, result: Result<Landed, Failure>) -> R<Report> {
        match result {
            Ok(landed) => {
                self.base = Some(landed.base());
                Ok(Report::Landed(landed.outcome))
            }
            Err(Failure::NotLanded(_)) => Ok(Report::Failed),
            Err(Failure::LandingUnknown(_) | Failure::BaseUnchanged) => {
                self.base = None;
                self.needs_recover = true;
                // The commit may have landed. Local caches are deliberately kept: capture()
                // must itself ignore an index that disagrees with the recovered remote
                // position (parent fix, report 16).
                Ok(Report::Failed)
            }
            Err(Failure::Foreign { .. } | Failure::NewerSchema) => {
                self.parked = true;
                Ok(Report::Parked)
            }
            Err(other) => Err(format!("commit: {other:?}")),
        }
    }
    /// One real capture step then one real commit. Updates apply only after a landing (or at
    /// once when there is no commit), as `capture.rs` documents.
    fn reconcile(&mut self) -> R<Report> {
        if let Some(report) = self.ensure_base()? {
            return Ok(report);
        }
        let empty = OwnerState::default();
        let state = self.base.as_ref().map_or(&empty, |b| &b.state);
        let got = run_capture(
            &mut self.fs,
            &self.sources,
            &self.locals,
            state,
            &self.context,
            &self.owner,
        )?;
        let Some(request) = got.commit else {
            for update in got.updates {
                update.apply(&mut self.locals);
            }
            return Ok(Report::Idle);
        };
        let result = commit(&mut self.store, self.base.as_ref(), request, &mut |_| {});
        let landed = result.is_ok();
        let report = self.absorb(result)?;
        if landed {
            for update in got.updates {
                update.apply(&mut self.locals);
            }
        }
        Ok(report)
    }
    /// The process dies at `at`. Index, locals and park state survive; nothing is applied.
    fn crash(&mut self, at: CrashAt) -> R<Report> {
        let mut manifest = false;
        if at != CrashAt::BeforePut && !self.parked {
            let base = if self.needs_recover {
                recover_base(&mut self.store, &self.owner).ok()
            } else {
                Some(self.base.clone())
            };
            if let Some(base) = base {
                let empty = OwnerState::default();
                let state = base.as_ref().map_or(&empty, |b| &b.state);
                let got = run_capture(
                    &mut self.fs,
                    &self.sources,
                    &self.locals,
                    state,
                    &self.context,
                    &self.owner,
                )?;
                if let Some(request) = got.commit {
                    for upload in request.uploads {
                        let _ = self.store.put_if_absent(&upload.key, upload.body);
                    }
                    if at == CrashAt::AfterManifest {
                        let built = build(
                            base.as_ref().map(|b| (&b.position, &b.state)),
                            request.header,
                            request.changes,
                        )
                        .map_err(e)?;
                        manifest = self
                            .store
                            .put_if_absent(&manifest_key(&built.hash), Published::sim(built.bytes))
                            .is_ok();
                    }
                }
            }
        }
        Ok(Report::Crashed { manifest })
    }
    fn exec(&mut self, step: &Step, after: &Reference, shapes: &Shapes) -> R {
        match step {
            Step::Append { src, .. }
            | Step::Rewrite { src, .. }
            | Step::Truncate { src, .. }
            | Step::RemoveSource { src } => self.sync_file(*src, after),
            Step::Reconcile => {
                self.reconcile()?;
            }
            Step::CrashBeforePut => {
                self.crash(CrashAt::BeforePut)?;
            }
            Step::CrashAfterPiece => {
                self.crash(CrashAt::AfterPiece)?;
            }
            Step::CrashAfterManifest => {
                if let Report::Crashed { manifest: true } = self.crash(CrashAt::AfterManifest)? {
                    bump(&shapes.crash_after_manifest);
                }
            }
            Step::CommittedReplyLost { then_offline } => {
                self.store.arm(
                    Op::Put(Kind::Current),
                    Effect::LandThen {
                        err: StoreError::Ambiguous,
                        then_offline: *then_offline,
                    },
                );
                let report = self.reconcile()?;
                self.store.clear_transient();
                if matches!(report, Report::Landed(CommitOutcome::AlreadyOurs { .. })) {
                    bump(&shapes.lost_reply);
                }
            }
            Step::CommittedThen5xx { landed } => {
                let effect = if *landed {
                    Effect::LandThen {
                        err: StoreError::Server,
                        then_offline: false,
                    }
                } else {
                    Effect::FailBefore(StoreError::Server)
                };
                self.store.arm(Op::Put(Kind::Current), effect);
                self.reconcile()?;
                self.store.clear_transient();
            }
            Step::Restart => {
                // The process restarts: every local and the commit base are gone.
                self.locals = Locals::new();
                self.base = None;
                self.needs_recover = true;
                bump(&shapes.restart);
            }
            Step::DeleteIndex => {
                // The append indexes and snapshot caches are deleted; the rest survives.
                if self.store.peek(&current_key(&self.owner)).is_some() {
                    bump(&shapes.delete_index);
                }
                for local in self.locals.values_mut() {
                    let Local {
                        index, snapshot, ..
                    } = local;
                    *index = None;
                    *snapshot = None;
                }
            }
            Step::GoOffline => self.store.offline = true,
            Step::GoOnline => self.store.offline = false,
            Step::RevokeToken => self.store.revoked = true,
            Step::RestoreToken => self.store.revoked = false,
            Step::ForeignWriter => {
                if !self.store.foreign_armed() {
                    let foreign = make_foreign(&self.store, &self.owner, "foreign")?;
                    self.store
                        .arm(Op::Put(Kind::Current), Effect::Foreign(foreign));
                }
            }
            Step::Settle => {
                self.store.offline = false;
                self.store.revoked = false;
                self.store.clear_transient_keep_none();
                for _ in 0..SETTLE_ROUNDS {
                    self.reconcile()?;
                }
                if !after.foreign_seen {
                    self.liveness(&after.files, &after.history)?;
                }
            }
        }
        Ok(())
    }
    /// Liveness after settle: the latest epoch equals the current source bytes where the
    /// source exists, and a removed source that was ever committed is recorded deleted.
    fn liveness(&self, disk: &Disk, history: &History) -> R {
        let observed = observe(&self.store, &self.owner, history)?;
        for i in 0..SOURCES {
            let entry = observed.as_ref().and_then(|o| o.sources.get(&i));
            match (disk.get(&i), entry) {
                (Some(bytes), Some(Some(latest))) => {
                    ensure(bytes == latest, "liveness: latest epoch != source")?;
                }
                (Some(_), _) => return Err("liveness: existing source not captured".to_owned()),
                (None, Some(Some(_))) => {
                    return Err("liveness: removed source not recorded deleted".to_owned());
                }
                (None, _) => {}
            }
        }
        Ok(())
    }
    fn check(&mut self, reference: &Reference, shapes: &Shapes) -> R {
        let Some(observed) = observe(&self.store, &self.owner, &reference.history)? else {
            return Ok(());
        };
        if let Some((seq, hash)) = self.head {
            ensure(observed.seq >= seq, "current.json went backwards")?;
            ensure(
                observed
                    .hashes
                    .iter()
                    .find(|(s, _)| *s == seq)
                    .is_none_or(|(_, h)| *h == hash),
                "a committed manifest was replaced",
            )?;
        }
        self.head = Some((observed.seq, observed.hash));
        if observed.max_epochs >= 2 {
            bump(&shapes.multi_epoch);
        }
        Ok(())
    }
}
impl FakeStore {
    /// Settle clears transient faults; an armed foreign writer stays (it is not a fault).
    fn clear_transient_keep_none(&mut self) {
        self.clear_transient();
    }
}

// ------------------------------------------------------------------ reference model

#[derive(Clone, Debug)]
enum Step {
    Append { src: u8, seed: u8, n: u8 },
    Rewrite { src: u8, pos: u8, byte: u8 },
    Truncate { src: u8, len: u8 },
    RemoveSource { src: u8 },
    Reconcile,
    CrashBeforePut,
    CrashAfterPiece,
    CrashAfterManifest,
    CommittedReplyLost { then_offline: bool },
    CommittedThen5xx { landed: bool },
    Restart,
    DeleteIndex,
    GoOffline,
    GoOnline,
    RevokeToken,
    RestoreToken,
    ForeignWriter,
    Settle,
}

/// Independent expectation: file bytes and everything each source ever held. Updated by the
/// steps only; it never reads the driver, capture, commit or the store. A foreign writer
/// parks the owner at an unpredictable attempt, so liveness is waived once one was injected.
#[derive(Clone, Debug)]
struct Reference {
    files: Disk,
    history: History,
    foreign_seen: bool,
}

impl Reference {
    fn new() -> Self {
        Self {
            files: Disk::new(),
            history: History::new(),
            foreign_seen: false,
        }
    }
    fn record(&mut self, src: u8) {
        if let Some(bytes) = self.files.get(&src).cloned() {
            let versions = self.history.entry(src).or_default();
            if versions.last() != Some(&bytes) {
                versions.push(bytes);
            }
        }
    }
    fn apply(&mut self, step: &Step) {
        match step {
            Step::Append { src, seed, n } => {
                let add: Vec<u8> = (0..(*n).max(1)).map(|k| seed.wrapping_add(k) % 3).collect();
                self.files.entry(*src).or_default().extend(add);
                self.record(*src);
            }
            Step::Rewrite { src, pos, byte } => {
                if let Some(file) = self.files.get_mut(src)
                    && let Some(index) = usize::from(*pos).checked_rem(file.len())
                    && let Some(b) = file.get_mut(index)
                {
                    *b = b.wrapping_add(1 + byte % 2);
                }
                self.record(*src);
            }
            Step::Truncate { src, len } => {
                if let Some(file) = self.files.get_mut(src)
                    && let Some(k) = usize::from(*len).checked_rem(file.len())
                {
                    file.truncate(k + 1);
                }
                self.record(*src);
            }
            Step::RemoveSource { src } => {
                self.files.remove(src);
            }
            Step::ForeignWriter => self.foreign_seen = true,
            Step::Reconcile
            | Step::CrashBeforePut
            | Step::CrashAfterPiece
            | Step::CrashAfterManifest
            | Step::CommittedReplyLost { .. }
            | Step::CommittedThen5xx { .. }
            | Step::Restart
            | Step::DeleteIndex
            | Step::GoOffline
            | Step::GoOnline
            | Step::RevokeToken
            | Step::RestoreToken
            | Step::Settle => {}
        }
    }
}

// ------------------------------------------------------------------ Hegel state machine

struct World {
    reference: Reference,
    sut: Sut,
    shapes: &'static Shapes,
}
impl World {
    fn new(shapes: &'static Shapes) -> R<Self> {
        Ok(Self {
            reference: Reference::new(),
            sut: Sut::new()?,
            shapes,
        })
    }
    fn try_run(&mut self, step: &Step) -> R {
        self.reference.apply(step);
        self.sut.exec(step, &self.reference, self.shapes)?;
        self.sut.check(&self.reference, self.shapes)
    }
    fn run(&mut self, step: Step) {
        let result = self.try_run(&step);
        assert!(result.is_ok(), "{step:?}: {result:?}");
    }
}
fn draw_src(tc: &TestCase) -> u8 {
    tc.draw(gs::integers::<u8>().min_value(0).max_value(SOURCES - 1))
}

#[hegel::state_machine]
impl World {
    #[rule(weight = 3.0)]
    fn append(&mut self, tc: TestCase) {
        let src = draw_src(&tc);
        let seed = tc.draw(gs::integers::<u8>());
        let n = tc.draw(gs::integers::<u8>().min_value(1).max_value(9));
        self.run(Step::Append { src, seed, n });
    }
    #[rule(weight = 2.0)]
    fn rewrite(&mut self, tc: TestCase) {
        let src = draw_src(&tc);
        let pos = tc.draw(gs::integers::<u8>().max_value(24));
        let byte = tc.draw(gs::integers::<u8>());
        self.run(Step::Rewrite { src, pos, byte });
    }
    #[rule(weight = 2.0)]
    fn truncate(&mut self, tc: TestCase) {
        let src = draw_src(&tc);
        let len = tc.draw(gs::integers::<u8>().max_value(24));
        self.run(Step::Truncate { src, len });
    }
    #[rule]
    fn remove_source(&mut self, tc: TestCase) {
        let src = draw_src(&tc);
        self.run(Step::RemoveSource { src });
    }
    #[rule(weight = 4.0)]
    fn reconcile(&mut self, _: TestCase) {
        self.run(Step::Reconcile);
    }
    #[rule]
    fn crash_before_put(&mut self, _: TestCase) {
        self.run(Step::CrashBeforePut);
    }
    #[rule]
    fn crash_after_piece(&mut self, _: TestCase) {
        self.run(Step::CrashAfterPiece);
    }
    #[rule]
    fn crash_after_manifest(&mut self, _: TestCase) {
        self.run(Step::CrashAfterManifest);
    }
    #[rule(weight = 2.0)]
    fn committed_reply_lost(&mut self, tc: TestCase) {
        let then_offline = tc.draw(gs::booleans());
        self.run(Step::CommittedReplyLost { then_offline });
    }
    #[rule]
    fn committed_then_5xx(&mut self, tc: TestCase) {
        let landed = tc.draw(gs::booleans());
        self.run(Step::CommittedThen5xx { landed });
    }
    #[rule]
    fn restart(&mut self, _: TestCase) {
        self.run(Step::Restart);
    }
    #[rule]
    fn delete_index(&mut self, _: TestCase) {
        self.run(Step::DeleteIndex);
    }
    #[rule]
    fn go_offline(&mut self, _: TestCase) {
        self.run(Step::GoOffline);
    }
    #[rule]
    fn go_online(&mut self, _: TestCase) {
        self.run(Step::GoOnline);
    }
    #[rule]
    fn revoke_token(&mut self, _: TestCase) {
        self.run(Step::RevokeToken);
    }
    #[rule]
    fn restore_token(&mut self, _: TestCase) {
        self.run(Step::RestoreToken);
    }
    #[rule(weight = 0.5)]
    fn foreign_writer(&mut self, _: TestCase) {
        self.run(Step::ForeignWriter);
    }
    #[rule(weight = 2.0)]
    fn settle(&mut self, _: TestCase) {
        self.run(Step::Settle);
    }
    #[invariant]
    fn store_is_safe(&mut self, _: TestCase) {
        let result = self.sut.check(&self.reference, self.shapes);
        assert!(result.is_ok(), "{result:?}");
    }
}

#[test]
fn hegel_property4_capture_state_machine() {
    hegel::Hegel::new(|tc: TestCase| {
        let world = World::new(&HEGEL_SHAPES);
        assert!(world.is_ok(), "{:?}", world.as_ref().err());
        if let Ok(world) = world {
            hegel::stateful::machine(world).steps(40).run(tc);
        }
    })
    .run();
    let guard = require_all(&HEGEL_SHAPES.counts());
    assert!(guard.is_ok(), "{guard:?} {:?}", HEGEL_SHAPES.counts());
}

// ------------------------------------------------------------------ proptest state machine

fn step_strategy() -> BoxedStrategy<Step> {
    let src = 0..SOURCES;
    prop_oneof![
        3 => (src.clone(), any::<u8>(), 1..=9_u8)
            .prop_map(|(src, seed, n)| Step::Append { src, seed, n }),
        2 => (src.clone(), 0..=24_u8, any::<u8>())
            .prop_map(|(src, pos, byte)| Step::Rewrite { src, pos, byte }),
        2 => (src.clone(), 0..=24_u8).prop_map(|(src, len)| Step::Truncate { src, len }),
        1 => src.prop_map(|src| Step::RemoveSource { src }),
        4 => Just(Step::Reconcile),
        1 => Just(Step::CrashBeforePut),
        1 => Just(Step::CrashAfterPiece),
        1 => Just(Step::CrashAfterManifest),
        2 => any::<bool>().prop_map(|then_offline| Step::CommittedReplyLost { then_offline }),
        1 => any::<bool>().prop_map(|landed| Step::CommittedThen5xx { landed }),
        1 => Just(Step::Restart),
        1 => Just(Step::DeleteIndex),
        1 => Just(Step::GoOffline),
        1 => Just(Step::GoOnline),
        1 => Just(Step::RevokeToken),
        1 => Just(Step::RestoreToken),
        1 => Just(Step::ForeignWriter),
        2 => Just(Step::Settle),
    ]
    .boxed()
}

struct RefMachine;
impl ReferenceStateMachine for RefMachine {
    type State = Reference;
    type Transition = Step;
    fn init_state() -> BoxedStrategy<Reference> {
        Just(Reference::new()).boxed()
    }
    fn transitions(_: &Reference) -> BoxedStrategy<Step> {
        step_strategy()
    }
    fn apply(mut state: Reference, transition: &Step) -> Reference {
        state.apply(transition);
        state
    }
}

struct PtSut {
    sut: R<Sut>,
}
struct PtMachine;
impl StateMachineTest for PtMachine {
    type SystemUnderTest = PtSut;
    type Reference = RefMachine;
    fn init_test(_: &Reference) -> PtSut {
        let sut = Sut::new();
        assert!(sut.is_ok(), "{:?}", sut.as_ref().err());
        PtSut { sut }
    }
    fn apply(mut state: PtSut, after: &Reference, step: Step) -> PtSut {
        if let Ok(sut) = state.sut.as_mut() {
            let result = sut
                .exec(&step, after, &PROPTEST_SHAPES)
                .and_then(|()| sut.check(after, &PROPTEST_SHAPES));
            assert!(result.is_ok(), "{step:?}: {result:?}");
        }
        state
    }
    fn check_invariants(state: &PtSut, after: &Reference) {
        if let Ok(sut) = &state.sut {
            let result = observe(&sut.store, &sut.owner, &after.history);
            assert!(result.is_ok(), "{:?}", result.err());
        }
    }
    fn teardown(mut state: PtSut, after: Reference) {
        let mut last = after;
        last.apply(&Step::Settle);
        if let Ok(sut) = state.sut.as_mut() {
            let result = sut
                .exec(&Step::Settle, &last, &PROPTEST_SHAPES)
                .and_then(|()| sut.check(&last, &PROPTEST_SHAPES));
            assert!(result.is_ok(), "final settle: {result:?}");
        }
    }
}

#[test]
fn proptest_property4_capture_state_machine() {
    let mut runner = TestRunner::new(Config {
        failure_persistence: None,
        ..Config::default()
    });
    let strategy = <RefMachine as ReferenceStateMachine>::sequential_strategy(1..40);
    let result = runner.run(&strategy, |(initial, transitions, seen)| {
        PtMachine::test_sequential(Config::default(), initial, transitions, seen);
        Ok(())
    });
    assert!(result.is_ok(), "{result:?}");
    let guard = require_all(&PROPTEST_SHAPES.counts());
    assert!(guard.is_ok(), "{guard:?} {:?}", PROPTEST_SHAPES.counts());
}

// ------------------------------------------------------------------ guard and controls

#[test]
fn guard_has_negative_controls() {
    let zero = Shapes::new();
    assert!(require_all(&zero.counts()).is_err());
    assert!(require_all(&[]).is_err());
    for missing in 0..5 {
        let mut counts = vec![1_u64; 5];
        if let Some(c) = counts.get_mut(missing) {
            *c = 0;
        }
        let named: Vec<_> = zero
            .counts()
            .into_iter()
            .zip(counts)
            .map(|((name, _), n)| (name, n))
            .collect();
        let err = require_all(&named);
        assert!(err.is_err(), "missing shape {missing} must fail the guard");
    }
    let full: Vec<_> = zero.counts().into_iter().map(|(n, _)| (n, 1)).collect();
    assert!(require_all(&full).is_ok());
}

/// Scripted run through the same machinery the properties use.
fn drive(steps: &[Step], shapes: &Shapes) -> R<(Reference, Sut)> {
    let mut reference = Reference::new();
    let mut sut = Sut::new()?;
    for step in steps {
        reference.apply(step);
        sut.exec(step, &reference, shapes)?;
        sut.check(&reference, shapes)?;
    }
    Ok((reference, sut))
}
fn app(src: u8, seed: u8, n: u8) -> Step {
    Step::Append { src, seed, n }
}

#[test]
fn guard_shapes_arise_from_a_scripted_run_with_the_real_capture() -> R {
    let shapes = Shapes::new();
    let (reference, sut) = drive(
        &[
            app(0, 0, 6),
            Step::Reconcile,
            Step::Rewrite {
                src: 0,
                pos: 0,
                byte: 1,
            },
            Step::Reconcile,
            app(0, 1, 3),
            Step::CrashAfterManifest,
            app(0, 2, 3),
            Step::CommittedReplyLost {
                then_offline: false,
            },
            Step::DeleteIndex,
            Step::Restart,
            Step::RemoveSource { src: 0 },
            Step::Settle,
        ],
        &shapes,
    )?;
    require_all(&shapes.counts())?;
    let observed = need(
        observe(&sut.store, &sut.owner, &reference.history)?,
        "committed",
    )?;
    ensure(
        observed.sources.get(&0) == Some(&None),
        "removed source must be recorded deleted",
    )
}

#[test]
fn removed_source_needs_two_observations_then_is_deleted() -> R {
    let shapes = Shapes::new();
    let (reference, mut sut) = drive(&[app(0, 0, 5), Step::Reconcile], &shapes)?;
    let mut reference = reference;
    let step = Step::RemoveSource { src: 0 };
    reference.apply(&step);
    sut.exec(&step, &reference, &shapes)?;
    sut.reconcile()?;
    let one = need(
        observe(&sut.store, &sut.owner, &reference.history)?,
        "committed",
    )?;
    ensure(
        matches!(one.sources.get(&0), Some(Some(_))),
        "one absence must not delete",
    )?;
    sut.reconcile()?;
    let two = need(
        observe(&sut.store, &sut.owner, &reference.history)?,
        "committed",
    )?;
    ensure(two.sources.get(&0) == Some(&None), "second absence deletes")
}

#[test]
fn oracle_detects_tampered_store_contents() -> R {
    let shapes = Shapes::new();
    let steps = [app(0, 0, 8), Step::Reconcile];
    let key_of = |sut: &Sut, prefix: &str| -> R<String> {
        need(
            sut.store
                .objects
                .keys()
                .find(|k| k.starts_with(prefix))
                .cloned(),
            "key",
        )
    };
    // The untampered run passes (positive control).
    let (reference, mut sut) = drive(&steps, &shapes)?;
    sut.check(&reference, &shapes)?;
    sut.liveness(&reference.files, &reference.history)?;
    // A referenced object disappears.
    let (reference, mut sut) = drive(&steps, &shapes)?;
    let object = key_of(&sut, "objects/")?;
    sut.store.remove(&object);
    assert!(sut.check(&reference, &shapes).is_err());
    // The stored encoding no longer matches its descriptor.
    let (reference, mut sut) = drive(&steps, &shapes)?;
    let object = key_of(&sut, "objects/")?;
    sut.store.write(&object, vec![9, 9]);
    assert!(sut.check(&reference, &shapes).is_err());
    // current.json claims another seq.
    let (reference, mut sut) = drive(&steps, &shapes)?;
    let cur = need(sut.head, "head")?;
    let pointer = Current::new(cur.1, cur.0 + 5, ts()?)
        .to_bytes()
        .map_err(e)?;
    sut.store.write(&current_key(&sut.owner), pointer);
    assert!(sut.check(&reference, &shapes).is_err());
    // Committed bytes the source never held violate 1a.
    let (mut reference, mut sut) = drive(&steps, &shapes)?;
    reference.history.insert(0, vec![vec![5]]);
    assert!(sut.check(&reference, &shapes).is_err());
    // current.json goes backwards.
    let (reference, mut sut) = drive(&steps, &shapes)?;
    sut.head = Some((9, sha(b"later")));
    assert!(sut.check(&reference, &shapes).is_err());
    // A committed manifest is replaced at the same seq.
    let (reference, mut sut) = drive(&steps, &shapes)?;
    sut.head = Some((0, sha(b"other")));
    assert!(sut.check(&reference, &shapes).is_err());
    // Liveness: the source changed but the store did not follow.
    let (mut reference, sut) = drive(&steps, &shapes)?;
    reference.files.insert(0, vec![1, 1, 1]);
    assert!(sut.liveness(&reference.files, &reference.history).is_err());
    // Liveness: the source was removed but is not recorded deleted.
    let (mut reference, sut) = drive(&steps, &shapes)?;
    reference.files.remove(&0);
    assert!(sut.liveness(&reference.files, &reference.history).is_err());
    // Liveness: an existing source was never captured.
    let (mut reference, sut) = drive(&steps, &shapes)?;
    reference.files.insert(1, vec![2]);
    assert!(sut.liveness(&reference.files, &reference.history).is_err());
    Ok(())
}
