//! Commit protocol acceptance (DESIGN.md 7.4, 15 property 4): table tests for every
//! `CommitOutcome` branch and injected fault, and property 4 as a state machine in both Hegel
//! and proptest-state-machine over an independently written reference model.
//!
//! Everything runs against a deterministic in-memory fake store. There is no real planner or
//! shell yet: capture is modelled by a small stand-in planner (full-prefix hash, pieces of at
//! most 4 raw bytes) feeding memory-core's manifest builder. This is not R2, not prototype P2
//! and not the daemon.
use hegel::{TestCase, generators as gs};
use memory::capture::Published;
use memory::commit::{
    Base, Commit, Failure, GET_ATTEMPTS, KnownOurs, Landed, Upload, check_fence, commit,
    commit_known, disposition, manifest_key, recover_base,
};
use memory::environment::{Store, StoreError, Stored};
use memory_core::disposition::Streak;
use memory_core::domain::{
    Agent, ArchiveId, CommitOutcome, Disposition, ETag, ErrorClass, Fault, MachineId, Offset,
    Owner, OwnerParkReason, SessionId, SessionKey, Sha256, SourceClass, SourceId, SourceKind,
};
use memory_core::limits::current_key;
use memory_core::manifest::{
    Change, Current, EpochRecord, EpochStamp, EpochWriter, Header, Manifest, ManifestError,
    OwnerState, Piece, Position, ProjectedKind, SourceWrite, StoredEncoding, Timestamp, Writer,
    build, fold, manifest_hash, object_key, parse_current, parse_manifest, recover_projection,
};
use proptest::prelude::*;
use proptest::test_runner::{Config, TestRunner};
use proptest_state_machine::{ReferenceStateMachine, StateMachineTest};
use std::collections::BTreeMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Duration;

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

fn sha(bytes: &[u8]) -> Sha256 {
    manifest_hash(bytes)
}
fn len64(bytes: &[u8]) -> u64 {
    u64::try_from(bytes.len()).unwrap_or(u64::MAX)
}
fn owner_named(session: &str) -> R<Owner> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
        machine: MachineId::try_from("m".to_owned()).map_err(e)?,
        agent: Agent::Claude,
        session: SessionId::try_from(session.to_owned()).map_err(e)?,
    }))
}
fn owner() -> R<Owner> {
    owner_named("s1")
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

/// Test-only encoding: the variant byte is the writer's "zstd version". Odd variants store
/// the bytes reversed, so two writers disagree on the stored encoding of the same raw piece.
fn encode(raw: &[u8], variant: u8) -> Vec<u8> {
    let mut out = vec![variant];
    if variant.is_multiple_of(2) {
        out.extend_from_slice(raw);
    } else {
        out.extend(raw.iter().rev());
    }
    out
}
fn decode(stored: &[u8]) -> Option<Vec<u8>> {
    let (variant, rest) = stored.split_first()?;
    Some(if variant.is_multiple_of(2) {
        rest.to_vec()
    } else {
        rest.iter().rev().copied().collect()
    })
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
/// A foreign writer's documents, applied just before the next matching request.
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
    /// Another client rewrites `current.json` unchanged, giving it a new ETag.
    Churn,
    /// A foreign writer advances the owner just before this request.
    Foreign(Foreign),
    /// The token is revoked just before this request.
    Revoke,
}
#[derive(Clone, Debug)]
struct Rule {
    op: Op,
    skip: u32,
    times: u32,
    effect: Effect,
}
enum Fence<'a> {
    Absent,
    Match(&'a ETag),
}

/// How the fake derives an ETag. R2 reports the MD5 of the body (P2: `etag_is_md5_of_body`), so
/// the content model is measured for distinct bodies; what R2 answers for an identical rewrite
/// is unmeasured, so the state machines run under both.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
enum EtagModel {
    /// Every write gets a new ETag, even an identical rewrite.
    #[default]
    PerWrite,
    /// The ETag is a function of the body: an identical rewrite keeps it.
    Content,
}
fn content_token(body: &[u8]) -> u64 {
    body.iter().fold(0xcbf2_9ce4_8422_2325_u64, |h, b| {
        (h ^ u64::from(*b)).wrapping_mul(0x0100_0000_01b3)
    })
}

/// Deterministic object store with S3/R2 conditional-write semantics and scripted faults.
/// `objects` holds the body and its ETag token; `version` counts writes.
#[derive(Default)]
struct FakeStore {
    objects: BTreeMap<String, (Vec<u8>, u64)>,
    version: u64,
    model: EtagModel,
    offline: bool,
    revoked: bool,
    rules: Vec<Rule>,
    log: Vec<(Op, String)>,
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
        self.arm_n(op, 0, 1, effect);
    }
    fn arm_n(&mut self, op: Op, skip: u32, times: u32, effect: Effect) {
        self.rules.push(Rule {
            op,
            skip,
            times,
            effect,
        });
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
    fn etag(&self, key: &str) -> R<ETag> {
        let (_, version) = need(self.objects.get(key), "no such key")?;
        etag_of(*version).map_err(e)
    }
    fn count(&self, op: Op) -> usize {
        self.log.iter().filter(|(o, _)| *o == op).count()
    }
    fn with_model(model: EtagModel) -> Self {
        Self {
            model,
            ..Self::default()
        }
    }
    fn write(&mut self, key: &str, body: Vec<u8>) -> u64 {
        self.version += 1;
        let token = match self.model {
            EtagModel::PerWrite => self.version,
            EtagModel::Content => content_token(&body),
        };
        self.objects.insert(key.to_owned(), (body, token));
        token
    }
    fn remove(&mut self, key: &str) {
        self.objects.remove(key);
    }
    fn intercept(&mut self, op: Op) -> Option<Effect> {
        let mut hit = None;
        for (index, rule) in self.rules.iter_mut().enumerate() {
            if rule.op != op {
                continue;
            }
            if rule.skip > 0 {
                rule.skip -= 1;
            } else {
                hit = Some(index);
            }
            break;
        }
        let index = hit?;
        let rule = self.rules.get_mut(index)?;
        let effect = rule.effect.clone();
        rule.times -= 1;
        if rule.times == 0 {
            self.rules.remove(index);
        }
        Some(effect)
    }
    fn side_effect(&mut self, key: &str, effect: Option<&Effect>) {
        match effect {
            Some(Effect::Revoke) => self.revoked = true,
            Some(Effect::Churn) => {
                if let Some((body, _)) = self.objects.get(key).cloned() {
                    self.write(key, body);
                }
            }
            Some(Effect::Foreign(f)) => {
                if !self.objects.contains_key(&f.manifest_key) {
                    self.write(&f.manifest_key, f.manifest.clone());
                }
                self.write(key, f.current.clone());
            }
            _ => {}
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
        let op = Op::Put(kind_of(key));
        self.log.push((op, key.to_owned()));
        let effect = self.intercept(op);
        self.side_effect(key, effect.as_ref());
        self.gate()?;
        if let Some(Effect::FailBefore(err)) = &effect {
            return Err(*err);
        }
        let existing = self.objects.get(key).map(|(_, v)| *v);
        let allowed = match (fence, existing) {
            (Fence::Absent, None) => true,
            (Fence::Absent, Some(_)) => false,
            // Measured on R2 (P2 `r2-os.jsonl`): If-Match on a missing key is 412, not 404.
            (Fence::Match(_), None) => false,
            (Fence::Match(tag), Some(v)) => etag_of(v)? == **tag,
        };
        if !allowed {
            return Err(StoreError::PreconditionFailed);
        }
        let tag = etag_of(self.write(key, body))?;
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
        let op = Op::Get(kind_of(key));
        self.log.push((op, key.to_owned()));
        let effect = self.intercept(op);
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

// ------------------------------------------------------------------ stand-in capture

type Disk = BTreeMap<u8, Vec<u8>>;

struct Plan {
    changes: Vec<Change>,
    uploads: Vec<Upload>,
}

fn stamp(n: u32, variant: u8) -> EpochStamp {
    EpochStamp {
        n,
        writer: EpochWriter {
            version: format!("0.1.{variant}"),
            commit: "abc".to_owned(),
        },
        rules: None,
    }
}
fn pieces_from(bytes: &[u8], from: usize, variant: u8, uploads: &mut Vec<Upload>) -> R<Vec<Piece>> {
    let tail = need(bytes.get(from..), "range")?;
    let mut out = Vec::new();
    let mut offset = from;
    for chunk in tail.chunks(PIECE) {
        let raw = sha(chunk);
        let stored = encode(chunk, variant);
        out.push(raw_piece(
            Offset::new(u64::try_from(offset).map_err(e)?),
            len64(chunk),
            raw,
            len64(&stored),
            sha(&stored),
        ));
        uploads.push(Upload {
            key: object_key(&raw),
            body: Published::sim(stored),
        });
        offset += chunk.len();
    }
    Ok(out)
}
fn write_of(
    i: u8,
    epoch: EpochStamp,
    bytes: &[u8],
    from: usize,
    variant: u8,
    uploads: &mut Vec<Upload>,
) -> R<SourceWrite> {
    Ok(SourceWrite {
        id: src_id(i)?,
        kind: SourceKind::ClaudeTranscript,
        path: format!("p/src{i}.jsonl"),
        class: SourceClass::Append,
        epoch,
        size: len64(bytes),
        sha256: sha(bytes),
        pieces: pieces_from(bytes, from, variant, uploads)?,
    })
}
/// Stand-in planner: a full-prefix hash decides between extending the epoch and starting
/// epoch n+1; a vanished source is recorded once as deleted.
fn plan(disk: &Disk, state: &OwnerState, variant: u8) -> R<Plan> {
    let mut changes = Vec::new();
    let mut uploads = Vec::new();
    for i in 0..SOURCES {
        let id = src_id(i)?;
        match (state.sources.get(&id), disk.get(&i)) {
            (None, None) => {}
            (Some(entry), None) => {
                if entry.deleted_at.is_none() {
                    changes.push(Change::Deleted {
                        id,
                        kind: SourceKind::ClaudeTranscript,
                        deleted_at: ts()?,
                    });
                }
            }
            (None, Some(bytes)) => changes.push(Change::Epoch(write_of(
                i,
                stamp(0, variant),
                bytes,
                0,
                variant,
                &mut uploads,
            )?)),
            (Some(entry), Some(bytes)) => {
                let last = need(entry.epochs.last(), "epoch")?;
                let n = usize::try_from(last.size).map_err(e)?;
                let same = entry.deleted_at.is_none()
                    && bytes.get(..n).is_some_and(|p| sha(p) == last.sha256);
                if same {
                    if bytes.len() > n {
                        changes.push(Change::Extend(write_of(
                            i,
                            last.epoch.clone(),
                            bytes,
                            n,
                            variant,
                            &mut uploads,
                        )?));
                    }
                } else {
                    changes.push(Change::Epoch(write_of(
                        i,
                        stamp(last.epoch.n + 1, variant),
                        bytes,
                        0,
                        variant,
                        &mut uploads,
                    )?));
                }
            }
        }
    }
    Ok(Plan { changes, uploads })
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
fn reassemble(store: &FakeStore, record: &EpochRecord) -> R<Vec<u8>> {
    let mut out: Vec<u8> = Vec::new();
    for piece in &record.pieces {
        let stored = need(store.peek(piece.object()), "piece object missing")?;
        ensure(
            sha(stored) == piece.encoded_sha256() && len64(stored) == piece.encoded_len(),
            "descriptor differs from stored encoding",
        )?;
        let raw = need(decode(stored), "undecodable piece")?;
        ensure(
            sha(&raw) == piece.sha256() && len64(&raw) == piece.len(),
            "piece bytes differ from descriptor",
        )?;
        ensure(len64(&out) == piece.offset().get(), "piece offset gap")?;
        out.extend(raw);
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

/// Safety (property 4): every referenced object exists and matches its descriptor, the chain
/// is consistent, and every committed epoch is a prefix of bytes the source held (1a).
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
static SCRATCH_SHAPES: Shapes = Shapes::new();

/// The shell's view: store, index (base + recovery flag), park flag and writer version.
struct Sut {
    store: FakeStore,
    owner: Owner,
    base: Option<Base>,
    needs_recover: bool,
    parked: bool,
    variant: u8,
    head: Option<(u64, Sha256)>,
}

impl Sut {
    fn new(model: EtagModel) -> R<Self> {
        Ok(Self {
            store: FakeStore::with_model(model),
            owner: owner()?,
            base: None,
            needs_recover: true,
            parked: false,
            variant: 0,
            head: None,
        })
    }
    fn restart(&mut self) {
        self.variant = self.variant.wrapping_add(1);
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
                Ok(Report::Failed)
            }
            Err(Failure::Foreign { .. } | Failure::NewerSchema) => {
                self.parked = true;
                Ok(Report::Parked)
            }
            Err(other) => Err(format!("commit: {other:?}")),
        }
    }
    fn reconcile(&mut self, disk: &Disk) -> R<Report> {
        if self.parked {
            return Ok(Report::Parked);
        }
        if self.needs_recover {
            match recover_base(&mut self.store, &self.owner) {
                Ok(base) => {
                    self.base = base;
                    self.needs_recover = false;
                }
                Err(Failure::NewerSchema) => {
                    self.parked = true;
                    return Ok(Report::Parked);
                }
                Err(Failure::NotLanded(_)) => return Ok(Report::Failed),
                Err(other) => return Err(format!("recover: {other:?}")),
            }
        }
        let empty = OwnerState::default();
        let state = self.base.as_ref().map_or(&empty, |b| &b.state);
        let plan = plan(disk, state, self.variant)?;
        if plan.changes.is_empty() {
            return Ok(Report::Idle);
        }
        let request = Commit {
            header: header(&self.owner, "memory")?,
            changes: plan.changes,
            uploads: plan.uploads,
        };
        let result = commit(&mut self.store, self.base.as_ref(), request, &mut |_| {});
        self.absorb(result)
    }
    /// The process dies at `at`. Index and park state survive, a temporary recovery does not.
    fn crash(&mut self, disk: &Disk, at: CrashAt) -> R<Report> {
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
                let plan = plan(disk, state, self.variant)?;
                if !plan.changes.is_empty() {
                    for upload in plan.uploads {
                        let _ = self.store.put_if_absent(&upload.key, upload.body);
                    }
                    if at == CrashAt::AfterManifest {
                        let built = build(
                            base.as_ref().map(|b| (&b.position, &b.state)),
                            header(&self.owner, "memory")?,
                            plan.changes,
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
        self.restart();
        Ok(Report::Crashed { manifest })
    }
    fn exec(&mut self, step: &Step, after: &Reference, shapes: &Shapes) -> R {
        let disk = &after.files;
        match step {
            Step::Append { .. }
            | Step::Rewrite { .. }
            | Step::Truncate { .. }
            | Step::RemoveSource { .. } => {}
            Step::Reconcile => {
                self.reconcile(disk)?;
            }
            Step::CrashBeforePut => {
                self.crash(disk, CrashAt::BeforePut)?;
            }
            Step::CrashAfterPiece => {
                self.crash(disk, CrashAt::AfterPiece)?;
            }
            Step::CrashAfterManifest => {
                if let Report::Crashed { manifest: true } =
                    self.crash(disk, CrashAt::AfterManifest)?
                {
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
                let report = self.reconcile(disk)?;
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
                self.reconcile(disk)?;
                self.store.clear_transient();
            }
            Step::Restart => {
                self.restart();
                bump(&shapes.restart);
            }
            Step::DeleteIndex => {
                if self.store.peek(&current_key(&self.owner)).is_some() {
                    bump(&shapes.delete_index);
                }
                self.base = None;
                self.needs_recover = true;
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
                self.store.clear_transient();
                for _ in 0..3 {
                    self.reconcile(disk)?;
                }
                if !after.parked {
                    self.liveness(disk, &after.history)?;
                }
            }
        }
        Ok(())
    }
    /// Liveness: after go-online, token restore and a final reconcile, the latest epoch of
    /// every source reassembles the file wherever it exists.
    fn liveness(&self, disk: &Disk, history: &History) -> R {
        let observed = observe(&self.store, &self.owner, history)?;
        for i in 0..SOURCES {
            let actual = observed
                .as_ref()
                .and_then(|o| o.sources.get(&i))
                .and_then(|s| s.as_ref());
            ensure(actual == disk.get(&i), "liveness: latest epoch != source")?;
        }
        Ok(())
    }
    fn check(&mut self, reference: &Reference, shapes: &Shapes) -> R {
        let observed = observe(&self.store, &self.owner, &reference.history)?;
        let Some(observed) = observed else {
            return ensure(
                reference.committed.values().all(Option::is_none),
                "nothing committed but the model expects commits",
            );
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
        for i in 0..SOURCES {
            let expected = reference.committed.get(&i).and_then(|c| c.as_ref());
            let actual = observed.sources.get(&i).and_then(|c| c.as_ref());
            ensure(
                expected == actual,
                "store disagrees with the reference model",
            )?;
        }
        Ok(())
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

/// Independent expectation: sources, everything they ever held, and what a successful
/// capture must have published. It never reads the driver, planner or store.
#[derive(Clone, Debug)]
struct Reference {
    files: Disk,
    history: History,
    committed: BTreeMap<u8, Option<Vec<u8>>>,
    online: bool,
    authorized: bool,
    parked: bool,
    foreign_armed: bool,
    /// ETag model of the store this run drives (see [`EtagModel`]).
    model: EtagModel,
}

impl Reference {
    fn with_model(model: EtagModel) -> Self {
        Self {
            model,
            files: Disk::new(),
            history: History::new(),
            committed: BTreeMap::new(),
            online: true,
            authorized: true,
            parked: false,
            foreign_armed: false,
        }
    }
    fn pending(&self) -> bool {
        (0..SOURCES).any(|i| self.files.get(&i) != self.committed.get(&i).and_then(|c| c.as_ref()))
    }
    fn publish(&mut self) {
        for i in 0..SOURCES {
            let now = self.files.get(&i).cloned();
            if now.is_some() || self.committed.contains_key(&i) {
                self.committed.insert(i, now);
            }
        }
    }
    /// A capture attempt: it reaches the `current.json` PUT only with something pending,
    /// network, token and no park. A racing foreign writer then parks the owner.
    fn attempt(&mut self, then_offline: bool) {
        if self.pending() && self.online && self.authorized && !self.parked {
            if self.foreign_armed {
                self.foreign_armed = false;
                self.parked = true;
            } else {
                self.publish();
                if then_offline {
                    self.online = false;
                }
            }
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
            Step::Reconcile => self.attempt(false),
            Step::CommittedReplyLost { then_offline } => self.attempt(*then_offline),
            Step::CommittedThen5xx { .. } => self.attempt(false),
            Step::CrashBeforePut
            | Step::CrashAfterPiece
            | Step::CrashAfterManifest
            | Step::Restart
            | Step::DeleteIndex => {}
            Step::GoOffline => self.online = false,
            Step::GoOnline => self.online = true,
            Step::RevokeToken => self.authorized = false,
            Step::RestoreToken => self.authorized = true,
            Step::ForeignWriter => self.foreign_armed = true,
            Step::Settle => {
                self.online = true;
                self.authorized = true;
                self.attempt(false);
            }
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
    fn new(shapes: &'static Shapes, model: EtagModel) -> R<Self> {
        Ok(Self {
            reference: Reference::with_model(model),
            sut: Sut::new(model)?,
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
fn hegel_property4_commit_state_machine() {
    let scripted = scripted_shapes(&HEGEL_SHAPES);
    assert!(scripted.is_ok(), "{scripted:?}");
    hegel::Hegel::new(|tc: TestCase| {
        let model = if tc.draw(gs::booleans()) {
            EtagModel::Content
        } else {
            EtagModel::PerWrite
        };
        let world = World::new(&HEGEL_SHAPES, model);
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
        any::<bool>()
            .prop_map(|content| {
                Reference::with_model(if content {
                    EtagModel::Content
                } else {
                    EtagModel::PerWrite
                })
            })
            .boxed()
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
    fn init_test(reference: &Reference) -> PtSut {
        let sut = Sut::new(reference.model);
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
fn proptest_property4_commit_state_machine() {
    let scripted = scripted_shapes(&PROPTEST_SHAPES);
    assert!(scripted.is_ok(), "{scripted:?}");
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

// ------------------------------------------------------------------ coverage guard

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

/// The script alone, with no random steps, produces every shape the guard requires.
#[test]
fn scripted_run_alone_produces_every_guarded_shape() -> R {
    let local = Shapes::new();
    scripted_shapes(&local)?;
    // Two models, one script: each shape is hit at least once per model.
    for (name, n) in local.counts() {
        assert!(n >= 2, "{name} hit {n} times over two models");
    }
    require_all(&local.counts())
}

/// Scripted run through the same machinery the properties use.
fn drive(steps: &[Step]) -> R<(Reference, Sut)> {
    drive_with(steps, &SCRATCH_SHAPES, EtagModel::PerWrite)
}
fn drive_with(steps: &[Step], shapes: &Shapes, model: EtagModel) -> R<(Reference, Sut)> {
    let mut reference = Reference::with_model(model);
    let mut sut = Sut::new(model)?;
    for step in steps {
        reference.apply(step);
        sut.exec(step, &reference, shapes)?;
        sut.check(&reference, shapes)?;
    }
    Ok((reference, sut))
}
/// One scripted run per ETag model that produces every guarded shape through the same oracle
/// the properties use, so the coverage guard of a property cannot fail by chance: the shapes are
/// forced here, and the random runs add frequency on top.
fn scripted_shapes(shapes: &Shapes) -> R {
    let script = [
        app(0, 0, 5),
        Step::Reconcile,
        // A rewrite inside committed bytes opens a second epoch.
        Step::Rewrite {
            src: 0,
            pos: 0,
            byte: 0,
        },
        Step::Reconcile,
        app(0, 1, 3),
        Step::CommittedReplyLost {
            then_offline: false,
        },
        app(0, 2, 3),
        Step::CrashAfterManifest,
        Step::Restart,
        Step::DeleteIndex,
        Step::Settle,
    ];
    for model in [EtagModel::PerWrite, EtagModel::Content] {
        drive_with(&script, shapes, model)?;
    }
    Ok(())
}
fn app(src: u8, seed: u8, n: u8) -> Step {
    Step::Append { src, seed, n }
}

#[test]
fn directed_crash_after_manifest_then_new_writer_version_adopts_winner_encoding() -> R {
    let (_, sut) = drive(&[
        app(0, 0, 5),
        Step::CrashAfterManifest,
        Step::Reconcile,
        Step::Settle,
    ])?;
    // The crashed attempt (writer variant 0) stored the pieces; the retry (variant 1) got 412s
    // and must describe the stored encoding, not its own: every committed descriptor equals
    // the winner's bytes, which carry variant byte 0.
    let chain = need(read_chain(&sut.store, &sut.owner)?, "chain")?;
    let mut pieces = 0;
    for entry in chain.folded.state.sources.values() {
        for record in &entry.epochs {
            for piece in &record.pieces {
                let stored = need(sut.store.peek(piece.object()), "stored piece")?;
                assert_eq!(
                    stored.first(),
                    Some(&0),
                    "the winner is the variant-0 encoding"
                );
                assert_eq!(piece.encoded_sha256(), sha(stored));
                assert_eq!(piece.encoded_len(), len64(stored));
                pieces += 1;
            }
        }
    }
    assert!(pieces > 0, "something was committed");
    Ok(())
}

#[test]
fn directed_delete_index_and_removed_source_recover_from_the_store() -> R {
    let (reference, sut) = drive(&[
        app(0, 0, 6),
        app(1, 1, 2),
        Step::Reconcile,
        Step::DeleteIndex,
        Step::RemoveSource { src: 1 },
        Step::Reconcile,
        Step::Restart,
        Step::DeleteIndex,
        Step::Settle,
    ])?;
    assert_eq!(reference.committed.get(&1), Some(&None));
    // The removal was committed, not just modelled: the folded store state marks source 1
    // deleted and keeps source 0 live.
    let chain = need(read_chain(&sut.store, &sut.owner)?, "chain")?;
    let deleted = |i: u8| -> R<bool> {
        let entry = need(chain.folded.state.sources.get(&src_id(i)?), "source entry")?;
        Ok(entry.deleted_at.is_some())
    };
    assert!(deleted(1)?);
    assert!(!deleted(0)?);
    Ok(())
}

#[test]
fn oracle_detects_tampered_store_contents() -> R {
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
    // A referenced object disappears.
    let (reference, mut sut) = drive(&steps)?;
    let object = key_of(&sut, "objects/")?;
    sut.store.remove(&object);
    assert!(sut.check(&reference, &SCRATCH_SHAPES).is_err());
    // The stored encoding no longer matches its descriptor.
    let (reference, mut sut) = drive(&steps)?;
    let object = key_of(&sut, "objects/")?;
    sut.store.write(&object, encode(&[9, 9], 0));
    assert!(sut.check(&reference, &SCRATCH_SHAPES).is_err());
    // current.json claims another seq.
    let (reference, mut sut) = drive(&steps)?;
    let cur = need(sut.head, "head")?;
    let pointer = Current::new(cur.1, cur.0 + 5, ts()?)
        .to_bytes()
        .map_err(e)?;
    sut.store.write(&current_key(&sut.owner), pointer);
    assert!(sut.check(&reference, &SCRATCH_SHAPES).is_err());
    // The model expects different committed bytes (1a / expectation).
    let (mut reference, mut sut) = drive(&steps)?;
    reference.committed.insert(0, Some(vec![7, 7, 7]));
    assert!(sut.check(&reference, &SCRATCH_SHAPES).is_err());
    // Committed bytes the source never held violate 1a.
    let (mut reference, mut sut) = drive(&steps)?;
    reference.history.insert(0, vec![vec![5]]);
    assert!(sut.check(&reference, &SCRATCH_SHAPES).is_err());
    // current.json goes backwards.
    let (reference, mut sut) = drive(&steps)?;
    sut.head = Some((9, sha(b"later")));
    assert!(sut.check(&reference, &SCRATCH_SHAPES).is_err());
    // A committed manifest is replaced at the same seq.
    let (reference, mut sut) = drive(&steps)?;
    sut.head = Some((0, sha(b"other")));
    assert!(sut.check(&reference, &SCRATCH_SHAPES).is_err());
    // Nothing committed while the model expects commits.
    let (reference, mut sut) = drive(&steps)?;
    sut.store.remove(&current_key(&sut.owner));
    assert!(sut.check(&reference, &SCRATCH_SHAPES).is_err());
    Ok(())
}

// ------------------------------------------------------------------ table tests

struct Rig {
    store: FakeStore,
    owner: Owner,
    disk: Disk,
    history: History,
    variant: u8,
    base: Option<Base>,
}
type Run = (Result<Landed, Failure>, Vec<u32>);

impl Rig {
    fn new() -> R<Self> {
        Ok(Self {
            store: FakeStore::default(),
            owner: owner()?,
            disk: Disk::new(),
            history: History::new(),
            variant: 0,
            base: None,
        })
    }
    fn put_file(&mut self, i: u8, bytes: &[u8]) {
        self.disk.insert(i, bytes.to_vec());
        self.history.entry(i).or_default().push(bytes.to_vec());
    }
    fn request(&self) -> R<Commit> {
        let empty = OwnerState::default();
        let state = self.base.as_ref().map_or(&empty, |b| &b.state);
        let plan = plan(&self.disk, state, self.variant)?;
        Ok(Commit {
            header: header(&self.owner, "memory")?,
            changes: plan.changes,
            uploads: plan.uploads,
        })
    }
    fn go_with(&mut self, request: Commit) -> Run {
        let mut pauses = Vec::new();
        let result = commit(&mut self.store, self.base.as_ref(), request, &mut |n| {
            pauses.push(n)
        });
        if let Ok(landed) = &result {
            self.base = Some(landed.base());
        }
        (result, pauses)
    }
    fn go(&mut self) -> R<Run> {
        let request = self.request()?;
        Ok(self.go_with(request))
    }
    fn landed(&mut self) -> R<Landed> {
        self.go()?.0.map_err(e)
    }
    fn failed(&mut self) -> R<(Failure, Vec<u32>)> {
        let (result, pauses) = self.go()?;
        match result {
            Err(f) => Ok((f, pauses)),
            Ok(l) => Err(format!("expected failure, landed {l:?}")),
        }
    }
    fn current_etag(&self) -> R<ETag> {
        self.store.etag(&current_key(&self.owner))
    }
    fn current_key(&self) -> String {
        current_key(&self.owner)
    }
    fn seeded(bytes: &[u8]) -> R<Self> {
        let mut rig = Self::new()?;
        rig.put_file(0, bytes);
        Ok(rig)
    }
    fn two_commits() -> R<Self> {
        let mut rig = Self::seeded(&[1, 2, 3, 4, 5])?;
        rig.landed()?;
        rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7]);
        rig.landed()?;
        Ok(rig)
    }
    fn assert_safe(&self) -> R {
        observe(&self.store, &self.owner, &self.history).map(|_| ())
    }
}

#[test]
fn first_write_uses_if_none_match_and_returns_committed() -> R {
    let mut rig = Rig::seeded(&[1, 2, 3, 4, 5])?;
    let landed = rig.landed()?;
    let etag = rig.current_etag()?;
    assert_eq!(
        landed.outcome,
        CommitOutcome::Committed { etag: etag.clone() }
    );
    assert_eq!(landed.etag, etag);
    assert_eq!(landed.built.position.seq, 0);
    assert_eq!(rig.store.count(Op::Put(Kind::Object)), 2);
    assert_eq!(rig.store.count(Op::Put(Kind::Manifest)), 1);
    assert_eq!(rig.store.count(Op::Put(Kind::Current)), 1);
    assert_eq!(rig.store.count(Op::Get(Kind::Current)), 0);
    rig.assert_safe()
}

#[test]
fn later_write_is_fenced_by_the_etag_and_chains_previous() -> R {
    let rig = Rig::two_commits()?;
    let base = need(rig.base.clone(), "base")?;
    assert_eq!(base.position.seq, 1);
    let Manifest::Delta(delta) = parse_manifest(need(
        rig.store.peek(&manifest_key(&base.position.manifest)),
        "manifest",
    )?)
    .map_err(e)?
    else {
        return Err("expected a delta".to_owned());
    };
    let first = rig
        .store
        .objects
        .keys()
        .filter(|k| k.starts_with("manifests/"))
        .count();
    assert_eq!(first, 2);
    assert_eq!(delta.seq(), 1);
    // Invariant 6: previous is the manifest named under the fenced ETag.
    let chain = need(read_chain(&rig.store, &rig.owner)?, "chain")?;
    assert_eq!(
        chain.hashes.first().map(|(_, h)| *h),
        Some(delta.previous())
    );
    rig.assert_safe()
}

#[test]
fn lost_reply_after_landing_is_already_ours() -> R {
    let mut rig = Rig::seeded(&[1, 2, 3, 4, 5])?;
    rig.store.arm(
        Op::Put(Kind::Current),
        Effect::LandThen {
            err: StoreError::Ambiguous,
            then_offline: false,
        },
    );
    let landed = rig.landed()?;
    let etag = rig.current_etag()?;
    assert_eq!(
        landed.outcome,
        CommitOutcome::AlreadyOurs { etag: etag.clone() }
    );
    assert_eq!(rig.store.count(Op::Get(Kind::Current)), 1);
    assert_eq!(rig.store.count(Op::Put(Kind::Current)), 1);
    // The verified ETag fences the next commit.
    rig.put_file(0, &[1, 2, 3, 4, 5, 9]);
    assert!(matches!(
        rig.landed()?.outcome,
        CommitOutcome::Committed { .. }
    ));
    rig.assert_safe()
}

#[test]
fn committed_then_5xx_is_already_ours_and_never_a_spurious_412() -> R {
    let mut rig = Rig::two_commits()?;
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    rig.store.arm(
        Op::Put(Kind::Current),
        Effect::LandThen {
            err: StoreError::Server,
            then_offline: false,
        },
    );
    let landed = rig.landed()?;
    assert!(matches!(landed.outcome, CommitOutcome::AlreadyOurs { .. }));
    assert_eq!(rig.store.count(Op::Put(Kind::Current)), 3);
    rig.assert_safe()
}

#[test]
fn unlanded_5xx_reads_base_unchanged_and_retries_from_step_three() -> R {
    let mut rig = Rig::two_commits()?;
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    let before = rig.store.count(Op::Put(Kind::Current));
    rig.store.arm(
        Op::Put(Kind::Current),
        Effect::FailBefore(StoreError::Server),
    );
    let (result, pauses) = rig.go()?;
    assert!(matches!(
        result.map_err(e)?.outcome,
        CommitOutcome::Committed { .. }
    ));
    assert_eq!(rig.store.count(Op::Put(Kind::Current)) - before, 2);
    assert_eq!(pauses, vec![1]);
    rig.assert_safe()
}

#[test]
fn etag_churn_is_base_unchanged_then_committed_with_the_fresh_etag() -> R {
    let mut rig = Rig::two_commits()?;
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    rig.store.arm(Op::Put(Kind::Current), Effect::Churn);
    let landed = rig.landed()?;
    assert_eq!(landed.etag, rig.current_etag()?);
    assert_eq!(rig.store.count(Op::Get(Kind::Current)), 1);
    rig.assert_safe()
}

#[test]
fn base_unchanged_retries_exactly_four_times_then_retries_the_capture() -> R {
    let mut rig = Rig::two_commits()?;
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    let head = need(rig.base.clone(), "base")?.position.manifest;
    let before = rig.store.count(Op::Put(Kind::Current));
    rig.store
        .arm_n(Op::Put(Kind::Current), 0, 100, Effect::Churn);
    let (failure, pauses) = rig.failed()?;
    assert_eq!(failure, Failure::BaseUnchanged);
    // Literal counts, not the constant: DESIGN.md 7.4 step 5 (line 619) says "up to 4 times",
    // so one first attempt plus 4 retries is 5 PUTs and 4 pauses.
    assert_eq!(rig.store.count(Op::Put(Kind::Current)) - before, 5);
    assert_eq!(pauses.len(), 4);
    assert_eq!(failure.commit_outcome(), Some(CommitOutcome::BaseUnchanged));
    assert!(!failure.base_may_be_stale());
    // Nothing advanced: current.json still names the previous head.
    let chain = need(read_chain(&rig.store, &rig.owner)?, "chain")?;
    assert_eq!(chain.current.manifest, head);
    let (disposition, _) = disposition(&Err(failure), false, Duration::ZERO, &Streak::default());
    // Exhausting the rebase retries is a transient failure (DESIGN.md 11.2), not throttling.
    assert!(
        matches!(
            disposition,
            Disposition::Retry {
                class: ErrorClass::Transient,
                ..
            }
        ),
        "{disposition:?}"
    );
    Ok(())
}

#[test]
fn foreign_writer_parks_the_owner_with_both_hashes() -> R {
    let mut rig = Rig::two_commits()?;
    let foreign = make_foreign(&rig.store, &rig.owner, "foreign")?;
    let theirs = sha(&foreign.manifest);
    rig.store
        .arm(Op::Put(Kind::Current), Effect::Foreign(foreign));
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    let (failure, _) = rig.failed()?;
    let Failure::Foreign { theirs: got, ours } = failure.clone() else {
        return Err(format!("expected foreign, got {failure:?}"));
    };
    assert_eq!(got, theirs);
    assert_ne!(ours, theirs);
    assert_eq!(
        failure.commit_outcome(),
        Some(CommitOutcome::Foreign { theirs })
    );
    let (d, _) = disposition(&Err(failure), false, Duration::ZERO, &Streak::default());
    assert_eq!(
        d,
        Disposition::ParkOwner {
            reason: OwnerParkReason::ForeignWriter { theirs, ours }
        }
    );
    // The foreign head stays; our manifest was never referenced.
    assert_eq!(
        need(read_chain(&rig.store, &rig.owner)?, "chain")?
            .current
            .manifest,
        theirs
    );
    rig.assert_safe()
}

#[test]
fn foreign_first_write_is_detected_through_if_none_match() -> R {
    let mut rig = Rig::seeded(&[1, 2, 3, 4, 5])?;
    let foreign = make_foreign(&rig.store, &rig.owner, "foreign")?;
    rig.store
        .arm(Op::Put(Kind::Current), Effect::Foreign(foreign));
    let (failure, _) = rig.failed()?;
    assert!(matches!(failure, Failure::Foreign { .. }));
    Ok(())
}

#[test]
fn foreign_manifest_with_a_missing_body_is_still_foreign() -> R {
    let mut rig = Rig::two_commits()?;
    let ghost = Foreign {
        manifest_key: "manifests/unrelated.json".to_owned(),
        manifest: vec![1],
        current: Current::new(sha(b"ghost"), 5, ts()?)
            .to_bytes()
            .map_err(e)?,
    };
    rig.store
        .arm(Op::Put(Kind::Current), Effect::Foreign(ghost));
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    let (failure, _) = rig.failed()?;
    assert!(matches!(failure, Failure::Foreign { .. }));
    Ok(())
}

#[test]
fn newer_manifest_schema_parks_instead_of_looping() -> R {
    let mut rig = Rig::two_commits()?;
    let body = br#"{"schema":"memory.manifest/2"}"#.to_vec();
    let theirs = sha(&body);
    let newer = Foreign {
        manifest_key: manifest_key(&theirs),
        manifest: body,
        current: Current::new(theirs, 9, ts()?).to_bytes().map_err(e)?,
    };
    rig.store
        .arm(Op::Put(Kind::Current), Effect::Foreign(newer));
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    let (failure, _) = rig.failed()?;
    assert_eq!(failure, Failure::NewerSchema);
    assert_eq!(failure.commit_outcome(), Some(CommitOutcome::NewerSchema));
    let (d, _) = disposition(&Err(failure), false, Duration::ZERO, &Streak::default());
    assert_eq!(
        d,
        Disposition::ParkOwner {
            reason: OwnerParkReason::NewerRemoteSchema
        }
    );
    Ok(())
}

#[test]
fn newer_current_schema_parks() -> R {
    let mut rig = Rig::two_commits()?;
    let newer = Foreign {
        manifest_key: "manifests/x.json".to_owned(),
        manifest: vec![0],
        current: br#"{"schema":"memory.current/2"}"#.to_vec(),
    };
    rig.store
        .arm(Op::Put(Kind::Current), Effect::Foreign(newer));
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    assert_eq!(rig.failed()?.0, Failure::NewerSchema);
    Ok(())
}

#[test]
fn unreadable_current_is_corrupt_and_deterministic() -> R {
    let mut rig = Rig::two_commits()?;
    let junk = Foreign {
        manifest_key: "manifests/x.json".to_owned(),
        manifest: vec![0],
        current: b"{".to_vec(),
    };
    rig.store.arm(Op::Put(Kind::Current), Effect::Foreign(junk));
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    let (failure, _) = rig.failed()?;
    assert_eq!(failure, Failure::Corrupt);
    let (d, streak) = disposition(&Err(failure), false, Duration::ZERO, &Streak::default());
    assert!(matches!(d, Disposition::Retry { .. }));
    assert_eq!(streak.fault, Some(Fault::Schema));
    Ok(())
}

#[test]
fn a_404_on_the_first_write_is_not_final_and_retries_if_none_match() -> R {
    let mut rig = Rig::seeded(&[1, 2, 3, 4, 5])?;
    rig.store.arm(
        Op::Put(Kind::Current),
        Effect::FailBefore(StoreError::Server),
    );
    let landed = rig.landed()?;
    assert!(matches!(landed.outcome, CommitOutcome::Committed { .. }));
    assert_eq!(
        rig.store.count(Op::Get(Kind::Current)),
        GET_ATTEMPTS as usize
    );
    assert_eq!(rig.store.count(Op::Put(Kind::Current)), 2);
    rig.assert_safe()
}

#[test]
fn a_404_under_a_fence_is_landing_unknown() -> R {
    let mut rig = Rig::two_commits()?;
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    let key = rig.current_key();
    rig.store.remove(&key);
    let before = rig.store.count(Op::Put(Kind::Current));
    let (failure, pauses) = rig.failed()?;
    assert_eq!(failure, Failure::LandingUnknown(ErrorClass::Offline));
    assert!(failure.base_may_be_stale());
    // Only a first write may retry step 3: under a fence there is exactly one PUT, then the
    // verification GETs (4 attempts, 3 pauses) and no second PUT.
    assert_eq!(rig.store.count(Op::Put(Kind::Current)) - before, 1);
    assert_eq!(rig.store.count(Op::Get(Kind::Current)), 4);
    assert_eq!(pauses.len(), 3);
    Ok(())
}

#[test]
fn a_persistent_404_on_the_first_write_gives_up_without_claiming_a_landing() -> R {
    let mut rig = Rig::seeded(&[1, 2, 3, 4, 5])?;
    rig.store.arm_n(
        Op::Put(Kind::Current),
        0,
        100,
        Effect::FailBefore(StoreError::Server),
    );
    let (failure, _) = rig.failed()?;
    assert_eq!(failure, Failure::LandingUnknown(ErrorClass::Offline));
    Ok(())
}

#[test]
fn verification_that_cannot_read_is_landing_unknown_with_its_class() -> R {
    let mut rig = Rig::two_commits()?;
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    rig.store.arm(
        Op::Put(Kind::Current),
        Effect::FailBefore(StoreError::Server),
    );
    rig.store.arm_n(
        Op::Get(Kind::Current),
        0,
        100,
        Effect::FailBefore(StoreError::Offline),
    );
    let (failure, pauses) = rig.failed()?;
    assert_eq!(failure, Failure::LandingUnknown(ErrorClass::Offline));
    assert_eq!(pauses.len(), GET_ATTEMPTS as usize - 1);
    let (d, _) = disposition(&Err(failure), false, Duration::ZERO, &Streak::default());
    assert!(matches!(
        d,
        Disposition::Retry {
            class: ErrorClass::Offline,
            ..
        }
    ));
    Ok(())
}

#[test]
fn auth_loss_during_verification_pauses_the_host() -> R {
    let mut rig = Rig::two_commits()?;
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    rig.store.arm(
        Op::Put(Kind::Current),
        Effect::FailBefore(StoreError::Server),
    );
    rig.store.arm(Op::Get(Kind::Current), Effect::Revoke);
    let (failure, _) = rig.failed()?;
    assert_eq!(
        failure,
        Failure::LandingUnknown(ErrorClass::DestinationAuth)
    );
    Ok(())
}

#[test]
fn offline_store_lands_nothing_and_retries_as_offline() -> R {
    let mut rig = Rig::seeded(&[1, 2, 3, 4, 5])?;
    rig.store.offline = true;
    let (failure, _) = rig.failed()?;
    assert_eq!(failure, Failure::NotLanded(ErrorClass::Offline));
    assert!(rig.store.objects.is_empty());
    let (d, streak) = disposition(&Err(failure), false, Duration::ZERO, &Streak::default());
    assert!(matches!(
        d,
        Disposition::Retry {
            class: ErrorClass::Offline,
            ..
        }
    ));
    assert_eq!(streak.retries, 1);
    Ok(())
}

#[test]
fn revoked_token_pauses_the_host_and_lands_nothing() -> R {
    let mut rig = Rig::seeded(&[1, 2, 3, 4, 5])?;
    rig.store.revoked = true;
    let (failure, _) = rig.failed()?;
    assert_eq!(failure, Failure::NotLanded(ErrorClass::DestinationAuth));
    let (d, _) = disposition(&Err(failure), false, Duration::ZERO, &Streak::default());
    assert!(matches!(d, Disposition::PauseHost));
    assert!(rig.store.objects.is_empty());
    Ok(())
}

#[test]
fn revocation_before_the_pointer_write_leaves_only_unreferenced_objects_then_recovers() -> R {
    let mut rig = Rig::two_commits()?;
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    let before = need(rig.base.clone(), "base")?;
    rig.store.arm(Op::Put(Kind::Current), Effect::Revoke);
    let (failure, _) = rig.failed()?;
    assert_eq!(failure, Failure::NotLanded(ErrorClass::DestinationAuth));
    assert_eq!(rig.store.count(Op::Put(Kind::Current)), 3);
    assert_eq!(
        need(read_chain(&rig.store, &rig.owner)?, "chain")?
            .current
            .manifest,
        before.position.manifest
    );
    rig.store.revoked = false;
    // Same inputs again: pieces and the manifest are 412 = success, the pointer lands.
    let landed = rig.landed()?;
    assert_eq!(landed.built.position.seq, 2);
    rig.assert_safe()
}

#[test]
fn ambiguous_piece_put_is_retried_as_412_success() -> R {
    let mut rig = Rig::seeded(&[1, 2, 3, 4, 5])?;
    rig.store.arm(
        Op::Put(Kind::Object),
        Effect::LandThen {
            err: StoreError::Ambiguous,
            then_offline: false,
        },
    );
    let (failure, _) = rig.failed()?;
    assert_eq!(failure, Failure::NotLanded(ErrorClass::Offline));
    assert!(rig.store.peek(&rig.current_key()).is_none());
    assert!(matches!(
        rig.landed()?.outcome,
        CommitOutcome::Committed { .. }
    ));
    rig.assert_safe()
}

#[test]
fn manifest_put_failure_lands_nothing_and_a_repeat_412_is_success() -> R {
    let mut rig = Rig::seeded(&[1, 2, 3, 4, 5])?;
    rig.store.arm(
        Op::Put(Kind::Manifest),
        Effect::LandThen {
            err: StoreError::Server,
            then_offline: false,
        },
    );
    let (failure, _) = rig.failed()?;
    assert_eq!(failure, Failure::NotLanded(ErrorClass::Offline));
    assert!(rig.store.peek(&rig.current_key()).is_none());
    rig.landed()?;
    rig.assert_safe()
}

#[test]
fn r2_a_412_dedup_adopts_the_encoding_that_actually_won() -> R {
    let raw = [1_u8, 2, 3, 4];
    let mut rig = Rig::seeded(&raw)?;
    // Another writer version stored the same raw piece with a different encoding.
    let winner = encode(&raw, 1);
    rig.store.write(&object_key(&sha(&raw)), winner.clone());
    let ours = encode(&raw, 0);
    assert_ne!(sha(&ours), sha(&winner));
    let landed = rig.landed()?;
    let Manifest::Checkpoint(checkpoint) = &landed.built.manifest else {
        return Err("first manifest is a checkpoint".to_owned());
    };
    let piece = need(
        checkpoint
            .sources()
            .first()
            .and_then(|s| s.epochs.first())
            .and_then(|r| r.pieces.first()),
        "piece",
    )?;
    assert_eq!(piece.encoded_sha256(), sha(&winner));
    assert_eq!(piece.encoded_len(), len64(&winner));
    // The stored object was not overwritten.
    assert_eq!(
        rig.store.peek(&object_key(&sha(&raw))),
        Some(winner.as_slice())
    );
    rig.assert_safe()
}

#[test]
fn a_failed_readback_after_412_lands_nothing() -> R {
    let raw = [1_u8, 2, 3, 4];
    let mut rig = Rig::seeded(&raw)?;
    rig.store.write(&object_key(&sha(&raw)), encode(&raw, 1));
    rig.store.arm(
        Op::Get(Kind::Object),
        Effect::FailBefore(StoreError::Server),
    );
    let (failure, _) = rig.failed()?;
    assert_eq!(failure, Failure::NotLanded(ErrorClass::Offline));
    Ok(())
}

#[test]
fn an_invalid_manifest_is_deterministic_and_never_reaches_the_store() -> R {
    let mut rig = Rig::new()?;
    let mut uploads = Vec::new();
    let write = write_of(0, stamp(0, 0), &[1, 2, 3], 0, 0, &mut uploads)?;
    let request = Commit {
        header: header(&rig.owner, "memory")?,
        changes: vec![Change::Extend(write)],
        uploads: vec![],
    };
    let (result, _) = rig.go_with(request);
    assert_eq!(result, Err(Failure::Invalid(ManifestError::MissingSource)));
    assert!(rig.store.objects.is_empty());
    let (d, _) = disposition(&result, false, Duration::ZERO, &Streak::default());
    assert!(matches!(d, Disposition::Retry { .. }));
    Ok(())
}

#[test]
fn invariant_6_fence_check() -> R {
    let rig = Rig::two_commits()?;
    let base = need(rig.base.clone(), "base")?;
    let manifest = parse_manifest(need(
        rig.store.peek(&manifest_key(&base.position.manifest)),
        "manifest",
    )?)
    .map_err(e)?;
    let Manifest::Delta(delta) = &manifest else {
        return Err("delta".to_owned());
    };
    assert_eq!(check_fence(&manifest, Some(delta.previous())), Ok(()));
    assert_eq!(
        check_fence(&manifest, Some(sha(b"other"))),
        Err(Failure::Invalid(ManifestError::BadLink))
    );
    assert_eq!(
        check_fence(&manifest, None),
        Err(Failure::Invalid(ManifestError::BadLink))
    );
    let first = parse_manifest(need(
        rig.store.peek(&manifest_key(&delta.previous())),
        "first",
    )?)
    .map_err(e)?;
    assert_eq!(check_fence(&first, None), Ok(()));
    assert!(check_fence(&first, Some(delta.previous())).is_err());
    Ok(())
}

#[test]
fn disposition_maps_every_commit_result() -> R {
    let mut rig = Rig::seeded(&[1, 2, 3])?;
    let landed = rig.landed()?;
    let (d, streak) = disposition(
        &Ok(landed),
        false,
        Duration::ZERO,
        &Streak {
            retries: 3,
            ..Streak::default()
        },
    );
    assert_eq!(d, Disposition::Done);
    assert_eq!(streak, Streak::default());
    // Draw 0 and no Retry-After: every jittered wait is its minimum. Each row pins the class the
    // scheduler sees and the streak the failure leaves behind.
    let prior = Streak::default();
    let bumped = Streak {
        retries: 1,
        ..Streak::default()
    };
    let retry = |class| Disposition::Retry {
        after: Duration::from_secs(1),
        class,
    };
    let deterministic = |streak_retries| Streak {
        deterministic: 1,
        fault: Some(Fault::Schema),
        first_at: Some(Duration::ZERO),
        retries: streak_retries,
    };
    for (failure, want, streak) in [
        (
            Failure::NotLanded(ErrorClass::Throttled),
            retry(ErrorClass::Throttled),
            bumped.clone(),
        ),
        (
            Failure::LandingUnknown(ErrorClass::Offline),
            retry(ErrorClass::Offline),
            bumped.clone(),
        ),
        (
            Failure::NotLanded(ErrorClass::DestinationAuth),
            Disposition::PauseHost,
            prior.clone(),
        ),
        (
            Failure::BaseUnchanged,
            retry(ErrorClass::Transient),
            bumped.clone(),
        ),
        (
            Failure::Invalid(ManifestError::BadLink),
            retry(ErrorClass::Deterministic(Fault::Schema)),
            deterministic(1),
        ),
        (
            Failure::Corrupt,
            retry(ErrorClass::Deterministic(Fault::Schema)),
            deterministic(1),
        ),
    ] {
        let label = format!("{failure:?}");
        let (d, next) = disposition(&Err(failure), false, Duration::ZERO, &prior);
        assert_eq!(d, want, "{label}");
        assert_eq!(next, streak, "{label}");
    }
    let (d, _) = disposition(
        &Err(Failure::NotLanded(ErrorClass::Offline)),
        true,
        Duration::ZERO,
        &prior,
    );
    assert_eq!(d, Disposition::Cancelled);
    assert_eq!(Failure::Corrupt.commit_outcome(), None);
    assert!(!Failure::Corrupt.base_may_be_stale());
    Ok(())
}

/// The class table feeds the disposition (DESIGN.md 11.2); the Display text is std's business.
#[test]
fn store_errors_classify() {
    let table = [
        (StoreError::Auth, ErrorClass::DestinationAuth),
        (StoreError::Throttled(None), ErrorClass::Throttled),
        (StoreError::Throttled(Some(7)), ErrorClass::Throttled),
        (StoreError::Server, ErrorClass::Offline),
        (StoreError::Offline, ErrorClass::Offline),
        (StoreError::Ambiguous, ErrorClass::Offline),
        (StoreError::NotFound, ErrorClass::Offline),
        (StoreError::PreconditionFailed, ErrorClass::Offline),
    ];
    for (error, class) in table {
        assert_eq!(error.class(), class, "{error:?}");
    }
}

/// DESIGN.md 11.2: a throttled reply's `Retry-After` reaches the scheduler. Kills: making
/// `Failure::retry_after` return `None`, and deleting its `Throttled` arm.
#[test]
fn a_throttled_reply_carries_its_retry_after_into_the_disposition() -> R {
    let mut rig = Rig::seeded(&[1, 2, 3])?;
    rig.store.arm(
        Op::Put(Kind::Object),
        Effect::FailBefore(StoreError::Throttled(Some(7))),
    );
    let (failure, _) = rig.failed()?;
    assert_eq!(
        failure,
        Failure::Throttled {
            retry_after: Some(Duration::from_secs(7))
        }
    );
    assert_eq!(failure.retry_after(), Some(Duration::from_secs(7)));
    let (d, _) = disposition(&Err(failure), false, Duration::ZERO, &Streak::default());
    assert_eq!(
        d,
        Disposition::Retry {
            after: Duration::from_secs(7),
            class: ErrorClass::Throttled
        }
    );
    // Without a Retry-After, the jittered minimum applies; other failures carry none.
    let mut rig = Rig::seeded(&[1, 2, 3])?;
    rig.store.arm(
        Op::Put(Kind::Object),
        Effect::FailBefore(StoreError::Throttled(None)),
    );
    let (failure, _) = rig.failed()?;
    assert_eq!(failure, Failure::Throttled { retry_after: None });
    let (d, _) = disposition(&Err(failure), false, Duration::ZERO, &Streak::default());
    assert_eq!(
        d,
        Disposition::Retry {
            after: Duration::from_secs(1),
            class: ErrorClass::Throttled
        }
    );
    for other in [
        Failure::NotLanded(ErrorClass::Offline),
        Failure::LandingUnknown(ErrorClass::Throttled),
        Failure::BaseUnchanged,
        Failure::Corrupt,
    ] {
        assert_eq!(other.retry_after(), None, "{other:?}");
    }
    Ok(())
}

// ------------------------------------------------------------------ recovery

#[test]
fn recover_base_reproduces_the_committed_base() -> R {
    let mut rig = Rig::two_commits()?;
    let recovered = recover_base(&mut rig.store, &rig.owner).map_err(e)?;
    assert_eq!(recovered, rig.base);
    // A fresh owner has nothing to recover.
    let other = owner_named("s2")?;
    assert_eq!(recover_base(&mut rig.store, &other), Ok(None));
    Ok(())
}

#[test]
fn recover_base_failures_are_classified() -> R {
    let rig = Rig::two_commits()?;
    let head = need(rig.base.clone(), "base")?.position.manifest;
    let key = manifest_key(&head);
    let recover = |store: &mut FakeStore| recover_base(store, &rig.owner);

    let mut store = clone_store(&rig.store);
    store.offline = true;
    assert_eq!(
        recover(&mut store),
        Err(Failure::NotLanded(ErrorClass::Offline))
    );

    let mut store = clone_store(&rig.store);
    store.remove(&key);
    assert_eq!(recover(&mut store), Err(Failure::Corrupt));

    let mut store = clone_store(&rig.store);
    store.write(&key, b"{\"changed\":true}".to_vec());
    assert_eq!(recover(&mut store), Err(Failure::Corrupt));

    let mut store = clone_store(&rig.store);
    store.arm(
        Op::Get(Kind::Manifest),
        Effect::FailBefore(StoreError::Server),
    );
    assert_eq!(
        recover(&mut store),
        Err(Failure::NotLanded(ErrorClass::Offline))
    );

    let mut store = clone_store(&rig.store);
    store.write(&rig.current_key(), b"{".to_vec());
    assert_eq!(recover(&mut store), Err(Failure::Corrupt));

    let mut store = clone_store(&rig.store);
    store.write(
        &rig.current_key(),
        br#"{"schema":"memory.current/2"}"#.to_vec(),
    );
    assert_eq!(recover(&mut store), Err(Failure::NewerSchema));

    let mut store = clone_store(&rig.store);
    let body = br#"{"schema":"memory.manifest/2"}"#.to_vec();
    let newer = sha(&body);
    store.write(&manifest_key(&newer), body);
    store.write(
        &rig.current_key(),
        Current::new(newer, 9, ts()?).to_bytes().map_err(e)?,
    );
    assert_eq!(recover(&mut store), Err(Failure::NewerSchema));

    // A current.json that names a manifest the store holds under another key.
    let mut store = clone_store(&rig.store);
    let first = need(
        rig.store
            .objects
            .keys()
            .find(|k| k.starts_with("manifests/")),
        "a manifest",
    )?;
    let stray = need(rig.store.peek(first), "its bytes")?.to_vec();
    store.write(&key, stray);
    assert_eq!(recover(&mut store), Err(Failure::Corrupt));

    // A chain that names another owner does not fold into this owner's base.
    let mut store = clone_store(&rig.store);
    let alien = build(None, header(&owner_named("s2")?, "x")?, vec![]).map_err(e)?;
    store.write(&manifest_key(&alien.hash), alien.bytes);
    store.write(
        &rig.current_key(),
        Current::new(alien.hash, 0, ts()?).to_bytes().map_err(e)?,
    );
    assert_eq!(recover(&mut store), Err(Failure::Corrupt));
    Ok(())
}

/// `recover_base` stops at the first manifest that does not hash to its key instead of walking
/// on. Kills: skipping the `manifest_hash(&bytes) != hash` check (the fold would still reject
/// the chain, but only after fetching the rest of it).
#[test]
fn recover_base_stops_at_the_first_manifest_that_does_not_match_its_key() -> R {
    let mut rig = Rig::seeded(&[1, 2, 3])?;
    let mut hashes = vec![rig.landed()?.built.hash];
    for k in 4..=6_u8 {
        rig.put_file(0, &(1..=k).collect::<Vec<u8>>());
        hashes.push(rig.landed()?.built.hash);
    }
    // Seq 0..3. Under the key of seq 2 sit the bytes of seq 1: a valid delta that links on to
    // seq 0, so a walk that does not check the hash would carry on fetching.
    let two = need(hashes.get(2), "seq 2")?;
    let one = need(hashes.get(1), "seq 1")?;
    let valid_other = need(rig.store.peek(&manifest_key(one)), "seq 1 bytes")?.to_vec();
    let mut store = clone_store(&rig.store);
    store.write(&manifest_key(two), valid_other);
    assert_eq!(recover_base(&mut store, &rig.owner), Err(Failure::Corrupt));
    assert_eq!(
        store.count(Op::Get(Kind::Manifest)),
        2,
        "head and the mismatched manifest, nothing further"
    );
    // The untampered store still recovers.
    let mut store = clone_store(&rig.store);
    assert_eq!(recover_base(&mut store, &rig.owner), Ok(rig.base.clone()));
    Ok(())
}

/// Checkpoints sit at `seq % 64 == 0` (DESIGN.md 6.3), so the longest valid walk is the
/// checkpoint at seq 0 plus 63 deltas (head seq 63): 64 manifest GETs, DESIGN.md's "at most 64
/// manifest GETs". Seq 64 is itself a checkpoint. Kills: a cap below 64 (`> 63`).
#[test]
fn recover_base_recovers_the_longest_valid_chain_of_sixty_four_manifests() -> R {
    let owner = owner()?;
    let mut store = FakeStore::default();
    let mut built = build(None, header(&owner, "x")?, vec![]).map_err(e)?;
    store.write(&manifest_key(&built.hash), built.bytes.clone());
    for _ in 0..63 {
        built = build(
            Some((&built.position, &built.state)),
            header(&owner, "x")?,
            vec![],
        )
        .map_err(e)?;
        store.write(&manifest_key(&built.hash), built.bytes.clone());
    }
    assert_eq!(built.position.seq, 63);
    store.write(
        &current_key(&owner),
        Current::new(built.hash, 63, ts()?).to_bytes().map_err(e)?,
    );
    let base = recover_base(&mut store, &owner)
        .map_err(e)?
        .ok_or("no base")?;
    assert_eq!(base.position.seq, 63);
    assert_eq!(store.count(Op::Get(Kind::Manifest)), 64);
    Ok(())
}

/// A chain of 65 manifests (checkpoint plus 64 deltas) cannot be valid: it is abandoned after
/// exactly 64 manifest GETs, before the 65th is fetched. The chain is forged from `build` with
/// repeated positions, which `fold` would reject as non-contiguous; the point is how much of a
/// hostile store is fetched. Kills: removing the cap, or moving it up by one (`> 64`, which
/// fetches 65) or down (`>= 63`, which fetches 63).
#[test]
fn recover_base_gives_up_after_sixty_four_gets_when_the_chain_is_one_too_long() -> R {
    for deltas in [64_usize, 70] {
        let owner = owner()?;
        let mut store = FakeStore::default();
        let root = build(None, header(&owner, "x")?, vec![]).map_err(e)?;
        store.write(&manifest_key(&root.hash), root.bytes.clone());
        let mut previous = root.hash;
        for _ in 0..deltas {
            let base = Position {
                seq: 1,
                manifest: previous,
                checkpoint: root.hash,
            };
            let delta = build(
                Some((&base, &OwnerState::default())),
                header(&owner, "x")?,
                vec![],
            )
            .map_err(e)?;
            store.write(&manifest_key(&delta.hash), delta.bytes);
            previous = delta.hash;
        }
        store.write(
            &current_key(&owner),
            Current::new(previous, 2, ts()?).to_bytes().map_err(e)?,
        );
        assert_eq!(recover_base(&mut store, &owner), Err(Failure::Corrupt));
        assert_eq!(store.count(Op::Get(Kind::Manifest)), 64, "{deltas} deltas");
    }
    Ok(())
}

fn clone_store(store: &FakeStore) -> FakeStore {
    FakeStore {
        objects: store.objects.clone(),
        version: store.version,
        model: store.model,
        ..FakeStore::default()
    }
}

#[test]
fn seventy_commits_cross_a_checkpoint_and_still_recover() -> R {
    let mut rig = Rig::seeded(&[1])?;
    let mut content = vec![1_u8];
    rig.landed()?;
    for n in 1..=70_u64 {
        content.push(u8::try_from(n % 3).map_err(e)?);
        rig.put_file(0, &content.clone());
        let landed = rig.landed()?;
        assert_eq!(landed.built.position.seq, n);
        assert_eq!(
            matches!(landed.built.manifest, Manifest::Checkpoint(_)),
            n % 64 == 0
        );
    }
    let recovered = recover_base(&mut rig.store, &rig.owner).map_err(e)?;
    assert_eq!(recovered, rig.base);
    rig.assert_safe()
}

// ------------------------------------------------------------------ A6 known_ours

/// Capture A builds M1 and fails (its `current.json` PUT and every verification GET are
/// refused). Capture B builds M2. M1's delayed write then lands just before B's PUT.
fn late_write(
    known_for_b: Option<KnownOurs>,
) -> R<(Rig, Result<Landed, Failure>, Sha256, KnownOurs)> {
    let mut rig = Rig::two_commits()?;
    let mut known = KnownOurs::new();
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
    rig.store.arm(
        Op::Put(Kind::Current),
        Effect::FailBefore(StoreError::Offline),
    );
    rig.store.arm_n(
        Op::Get(Kind::Current),
        0,
        GET_ATTEMPTS,
        Effect::FailBefore(StoreError::Offline),
    );
    let request = rig.request()?;
    let a = commit_known(
        &mut rig.store,
        rig.base.as_ref(),
        request,
        &mut known,
        &mut |_| {},
    );
    assert!(matches!(a, Err(Failure::LandingUnknown(_))), "{a:?}");
    assert_eq!(known.len(), 1);
    let m1 = need(known.keys().next().copied(), "m1")?;
    let seq = need(known.get(&m1), "built")?.position.seq;
    let foreign = Foreign {
        manifest_key: manifest_key(&m1),
        manifest: need(rig.store.peek(&manifest_key(&m1)), "m1 bytes")?.to_vec(),
        current: Current::new(m1, seq, ts()?).to_bytes().map_err(e)?,
    };
    rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8, 9]);
    rig.store
        .arm(Op::Put(Kind::Current), Effect::Foreign(foreign));
    let mut known_b = known_for_b.unwrap_or(known);
    let request = rig.request()?;
    let b = commit_known(
        &mut rig.store,
        rig.base.as_ref(),
        request,
        &mut known_b,
        &mut |_| {},
    );
    Ok((rig, b, m1, known_b))
}

#[test]
fn late_write_of_an_earlier_failed_capture_is_adopted_and_followed_up() -> R {
    let (mut rig, b, m1, known) = late_write(None)?;
    let landed = b.map_err(e)?;
    // Adopting the earlier attempt's manifest is a landing: the set is cleared (A6).
    assert!(
        known.is_empty(),
        "known_ours kept {} manifests",
        known.len()
    );
    assert!(matches!(landed.outcome, CommitOutcome::AlreadyOurs { .. }));
    assert!(landed.follow_up);
    assert_eq!(landed.built.hash, m1);
    rig.base = Some(landed.base());
    // Follow-up capture uploads the newer bytes on top of the adopted manifest.
    let again = rig.landed()?;
    assert!(matches!(again.outcome, CommitOutcome::Committed { .. }));
    assert!(!again.follow_up);
    rig.assert_safe()
}

/// A6: `known_ours` holds the manifests built since the last landed commit. Kills: removing the
/// `known.clear()` after a landed `Committed` or after a verified `AlreadyOurs` (the adopted
/// case is asserted in the late-write test above).
#[test]
fn known_ours_is_cleared_whenever_a_commit_lands() -> R {
    for lost_reply in [false, true] {
        let mut rig = Rig::two_commits()?;
        let mut known = KnownOurs::new();
        rig.put_file(0, &[1, 2, 3, 4, 5, 6, 7, 8]);
        // Attempt 1 cannot reach the store at the pointer write, nor read it back.
        rig.store.arm(
            Op::Put(Kind::Current),
            Effect::FailBefore(StoreError::Offline),
        );
        rig.store.arm_n(
            Op::Get(Kind::Current),
            0,
            GET_ATTEMPTS,
            Effect::FailBefore(StoreError::Offline),
        );
        let request = rig.request()?;
        let first = commit_known(
            &mut rig.store,
            rig.base.as_ref(),
            request,
            &mut known,
            &mut |_| {},
        );
        assert!(
            matches!(first, Err(Failure::LandingUnknown(_))),
            "{first:?}"
        );
        assert_eq!(
            known.len(),
            1,
            "the failed attempt's manifest is remembered"
        );
        if lost_reply {
            rig.store.arm(
                Op::Put(Kind::Current),
                Effect::LandThen {
                    err: StoreError::Ambiguous,
                    then_offline: false,
                },
            );
        }
        let request = rig.request()?;
        let landed = commit_known(
            &mut rig.store,
            rig.base.as_ref(),
            request,
            &mut known,
            &mut |_| {},
        )
        .map_err(e)?;
        let kind_ok = if lost_reply {
            matches!(landed.outcome, CommitOutcome::AlreadyOurs { .. })
        } else {
            matches!(landed.outcome, CommitOutcome::Committed { .. })
        };
        assert!(kind_ok, "{:?}", landed.outcome);
        assert!(known.is_empty(), "lost_reply={lost_reply}: set not cleared");
    }
    Ok(())
}

#[test]
fn late_write_of_an_unknown_manifest_is_foreign() -> R {
    let (_, b, _, _) = late_write(Some(KnownOurs::new()))?;
    assert!(matches!(b, Err(Failure::Foreign { .. })), "{b:?}");
    Ok(())
}

#[expect(
    clippy::panic,
    reason = "test fixture: a literal piece that fails to parse is a broken test, not a runtime path"
)]
fn raw_piece(
    offset: Offset,
    len: u64,
    sha: Sha256,
    encoded_len: u64,
    encoded_sha: Sha256,
) -> Piece {
    let h = String::from(sha);
    let v = serde_json::json!({"offset": offset.get(), "len": len, "sha256": h,
        "object": format!("objects/{h}.zst"), "encoding": "zstd-3",
        "encoded_len": encoded_len, "encoded_sha256": String::from(encoded_sha)});
    match serde_json::from_value(v) {
        Ok(p) => p,
        Err(_) => panic!("piece"),
    }
}
