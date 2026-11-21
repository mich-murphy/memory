//! Daemon skeleton tests: temp dirs, in-memory filesystem, fake store. No real adapters.
use memory::capture::{Published, Source};
use memory::daemon::{Daemon, Parts, StartError, StartOptions};
use memory::environment::{Cause, Store, StoreError, Stored};
use memory::state::{DaemonLock, EXIT_LOCK_HELD, SyncPolicy};
use memory_core::domain::{
    Agent, ArchiveId, ETag, MachineId, Owner, SessionId, SessionKey, SourceClass, SourceId,
    SourceKind,
};
use memory_core::privacy::{PolicyContext, Profile, ResolvedSourceFacts, rules_digest};
use std::collections::BTreeSet;
use std::fs;
use std::os::unix::fs::{PermissionsExt, symlink};
use std::path::PathBuf;
use std::process::Command;
use std::sync::atomic::{AtomicU64, Ordering};

#[path = "common/fs.rs"]
mod mem_fs;
#[path = "common/store.rs"]
mod mem_store;
#[path = "common/clock.rs"]
mod test_clock;
use mem_fs::MemFs;
use mem_store::{EtagModel, MemStore};
use test_clock::{TestClock, WALL};

type R<T = ()> = Result<T, String>;
fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}

struct Tmp(PathBuf);
impl Tmp {
    fn new() -> R<Self> {
        static N: AtomicU64 = AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!(
            "memory-daemon-{}-{}",
            std::process::id(),
            N.fetch_add(1, Ordering::SeqCst)
        ));
        fs::create_dir_all(&path).map_err(e)?;
        fs::set_permissions(&path, fs::Permissions::from_mode(0o700)).map_err(e)?;
        Ok(Self(path))
    }
    fn root(&self) -> PathBuf {
        self.0.join("state")
    }
    fn config(&self, valid: bool) -> R<PathBuf> {
        let token = self.0.join("token");
        fs::write(&token, "x").map_err(e)?;
        fs::set_permissions(&token, fs::Permissions::from_mode(0o600)).map_err(e)?;
        let text = if valid {
            format!(
                "archive_id = \"a\"\nmachine_id = \"m\"\nendpoint = \"http://127.0.0.1:9\"\nbucket = \"bucket-1\"\nprefix = \"\"\ntoken_path = \"{}\"\n",
                token.display()
            )
        } else {
            "nonsense = 1\n".to_owned()
        };
        let path = self.0.join("config.toml");
        fs::write(&path, text).map_err(e)?;
        Ok(path)
    }
}
impl Drop for Tmp {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn owner() -> R<Owner> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
        machine: MachineId::try_from("m".to_owned()).map_err(e)?,
        agent: Agent::Claude,
        session: SessionId::try_from("s1".to_owned()).map_err(e)?,
    }))
}
fn source() -> R<(Owner, Source)> {
    let path = "projects/p/s1.jsonl".to_owned();
    Ok((
        owner()?,
        Source {
            handle: 1,
            id: SourceId::try_from("src1".to_owned()).map_err(e)?,
            kind: SourceKind::ClaudeTranscript,
            class: SourceClass::Append,
            path: path.clone(),
            facts: ResolvedSourceFacts {
                identities: vec![path],
                file_identity: Some(1),
                ancestor_identities: vec![],
                path_resolved: true,
                root_resolved: true,
                owner: Some("o".to_owned()),
                profile: Profile::Transcript,
            },
        },
    ))
}

type Sources = Box<dyn FnMut() -> Vec<(Owner, Source)>>;
fn parts(fs: MemFs, store: MemStore) -> Parts<MemFs, MemStore, TestClock, Sources> {
    let one = source().ok();
    let sources: Sources = Box::new(move || one.clone().into_iter().collect());
    Parts {
        fs,
        store,
        clock: TestClock::at(0),
        sources,
        context: PolicyContext {
            valid: true,
            revision: "r".to_owned(),
            rules: Some(rules_digest()),
            collector_paths: vec![],
            collector_identities: vec![],
        },
    }
}
fn failed<T>(r: Result<T, StartError>) -> R<StartError> {
    match r {
        Err(error) => Ok(error),
        Ok(_) => Err("start unexpectedly succeeded".to_owned()),
    }
}
fn files_with(bytes: &[u8]) -> MemFs {
    let mut fs = MemFs::default();
    fs.files.insert(1, bytes.to_vec());
    fs
}

#[test]
fn held_lock_refuses_before_tmp_is_swept() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let lock = DaemonLock::acquire(&tmp.root()).map_err(e)?;
    fs::create_dir_all(tmp.root().join("tmp")).map_err(e)?;
    let marker = tmp.root().join("tmp/keep");
    fs::write(&marker, "x").map_err(e)?;
    let error = failed(Daemon::start(
        &tmp.root(),
        &cfg,
        parts(MemFs::default(), MemStore::default()),
    ))?;
    assert_eq!(error.exit_code(), EXIT_LOCK_HELD);
    assert!(marker.exists());
    drop(lock);
    Ok(())
}

#[test]
fn hints_dir_symlink_refuses() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    fs::create_dir_all(tmp.root()).map_err(e)?;
    fs::set_permissions(tmp.root(), fs::Permissions::from_mode(0o700)).map_err(e)?;
    symlink(&tmp.0, tmp.root().join("hints")).map_err(e)?;
    let error = failed(Daemon::start(
        &tmp.root(),
        &cfg,
        parts(MemFs::default(), MemStore::default()),
    ))?;
    assert_eq!(error.code(), "state_symlink");
    Ok(())
}

#[test]
fn second_process_exits_lock_held() -> R {
    let tmp = Tmp::new()?;
    let _lock = DaemonLock::acquire(&tmp.root()).map_err(e)?;
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.arg("daemon")
        .env_clear()
        .env("MEMORY_STATE_DIR", tmp.root());
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    let out = cmd.output().map_err(e)?;
    assert_eq!(out.status.code(), Some(i32::from(EXIT_LOCK_HELD)));
    assert!(out.stdout.is_empty());
    Ok(())
}

// ====================================================================== Round 9
use hegel::{TestCase, generators as gs};
use memory::commit::manifest_key;
use memory::daemon::{IndexBody, ParkedReason, StateLoad, StatusBody};
use memory::state::{Loaded, StateDir, StateFile, read_durable};
use memory_core::domain::{OwnerParkReason, Sha256};
use memory_core::hint::{
    HintEvent, HintRecord, HintSchema, LaunchHint, LaunchRecord, PathHint, Reduction,
};
use memory_core::limits::current_key;
use memory_core::manifest::{
    Current, EpochRecord, Header, Manifest, ProjectedKind, StoredEncoding, Timestamp, Writer,
    build, fold, manifest_hash, parse_current, parse_manifest, recover_projection,
};
use memory_core::privacy::sanitize_launch;
use proptest::prelude::*;
use proptest::test_runner::{Config, TestRunner};
use proptest_state_machine::{ReferenceStateMachine, StateMachineTest};
use std::path::Path;

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
fn sha(bytes: &[u8]) -> Sha256 {
    manifest_hash(bytes)
}
fn len64(bytes: &[u8]) -> u64 {
    u64::try_from(bytes.len()).unwrap_or(u64::MAX)
}
fn contains(hay: &[u8], needle: &[u8]) -> bool {
    hay.windows(needle.len().max(1)).any(|w| w == needle)
}

fn owner_of(session: &str) -> R<Owner> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
        machine: MachineId::try_from("m".to_owned()).map_err(e)?,
        agent: Agent::Claude,
        session: SessionId::try_from(session.to_owned()).map_err(e)?,
    }))
}
fn src_of(session: &str, handle: u64) -> R<(Owner, Source)> {
    let path = format!("projects/p/{session}.jsonl");
    Ok((
        owner_of(session)?,
        Source {
            handle,
            id: SourceId::try_from(format!("src{handle}")).map_err(e)?,
            kind: SourceKind::ClaudeTranscript,
            class: SourceClass::Append,
            path: path.clone(),
            facts: ResolvedSourceFacts {
                identities: vec![path],
                file_identity: Some(u128::from(handle)),
                ancestor_identities: vec![],
                path_resolved: true,
                root_resolved: true,
                owner: Some("o".to_owned()),
                profile: Profile::Transcript,
            },
        },
    ))
}

#[derive(Clone, Debug)]
struct Foreign {
    manifest_key: String,
    manifest: Vec<u8>,
    current: Vec<u8>,
}
#[derive(Clone, Debug)]
enum Effect {
    /// Land the write, then answer with the error (a lost reply).
    LandThen(StoreError),
    /// Land the write, lose the reply, and the store stays unreachable (every request fails)
    /// until the test clears `blackout`: the writer cannot verify what landed.
    LandThenDark(StoreError),
    /// Another client rewrites `current.json` with the same bytes just before this request.
    Churn,
    /// A foreign writer advances the owner just before this request.
    Foreign(Foreign),
}
struct Rule {
    key: Option<String>,
    effect: Effect,
}
/// Where a simulated process death cuts a commit short.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum CrashAt {
    /// Pieces landed; the manifest PUT does not.
    Manifest,
    /// Pieces and manifest landed; the `current.json` PUT does not.
    Current,
}
/// A `current.json` write from an earlier attempt, delayed in the network (A6).
struct Late {
    body: Vec<u8>,
    /// Version of `current.json` the write was fenced on (`None`: first write).
    fence: Option<u64>,
}

/// The shared store with scripted faults on `current.json` that need state inside one PUT
/// (a held write that lands late, a crash point, a blackout). The faults that only act
/// around a call are done by decorating the store instead, as `Ghost` does.
#[derive(Default)]
struct FStore {
    store: MemStore,
    offline: bool,
    revoked: bool,
    rules: Vec<Rule>,
    put_log: Vec<String>,
    lost_fired: u64,
    /// While set, `current.json` PUTs that pass their fence are held, not landed (5xx reply).
    stall: bool,
    /// The first held write; it lands, fenced, just before the next `current.json` PUT.
    late: Option<Late>,
    late_landed: u64,
    crash_at: Option<CrashAt>,
    /// After a crash fired every request fails until the test replaces the process.
    crashed: Option<CrashAt>,
    /// After a held write the store is unreachable until the round ends.
    blackout: bool,
    /// `current.json` writes answered 412.
    refused_current: u64,
    /// Refusals of a `current.json` write that no late write explains: none landed just before.
    refused_unexplained: u64,
}
impl FStore {
    fn with_model(model: EtagModel) -> Self {
        Self {
            store: MemStore {
                model,
                ..MemStore::default()
            },
            ..Self::default()
        }
    }
    fn arm(&mut self, key: Option<String>, effect: Effect) {
        self.rules.push(Rule { key, effect });
    }
    fn peek(&self, key: &str) -> Option<&[u8]> {
        self.store.objects.get(key).map(|(b, _)| b.as_slice())
    }
    fn take_rule(&mut self, key: &str) -> Option<Effect> {
        if !key.ends_with("current.json") {
            return None;
        }
        let at = self
            .rules
            .iter()
            .position(|r| r.key.as_deref().is_none_or(|k| k == key))?;
        Some(self.rules.remove(at).effect)
    }
    /// The held write from an earlier attempt arrives; it lands only if its fence still holds.
    /// True when it landed.
    fn land_late(&mut self, key: &str) -> bool {
        let Some(late) = self.late.take() else {
            return false;
        };
        let now = self.store.objects.get(key).map(|(_, v)| *v);
        if now != late.fence {
            return false;
        }
        self.store.write(key, late.body);
        self.late_landed += 1;
        true
    }
    fn gate(&self) -> Result<(), StoreError> {
        if self.crashed.is_some() || self.blackout {
            Err(StoreError::Offline(Cause::Connect))
        } else if self.revoked {
            Err(StoreError::Auth)
        } else if self.offline {
            Err(StoreError::Offline(Cause::Connect))
        } else {
            Ok(())
        }
    }
    fn put(
        &mut self,
        key: &str,
        body: &Published,
        fence: Option<&ETag>,
    ) -> Result<ETag, StoreError> {
        self.put_log.push(key.to_owned());
        let effect = self.take_rule(key);
        if let Some(Effect::Foreign(f)) = &effect {
            if !self.store.objects.contains_key(&f.manifest_key) {
                self.store.write(&f.manifest_key, f.manifest.clone());
            }
            self.store.write(key, f.current.clone());
        }
        let is_current = key.ends_with("current.json");
        if matches!(effect, Some(Effect::Churn))
            && let Some((body, _)) = self.store.objects.get(key).cloned()
        {
            self.store.write(key, body);
        }
        if let Some(at) = self.crash_at {
            let hit = match at {
                CrashAt::Manifest => key.starts_with("manifests/"),
                CrashAt::Current => is_current,
            };
            if hit {
                self.crashed = Some(at);
                self.crash_at = None;
            }
        }
        self.gate()?;
        let late_landed = is_current && self.land_late(key);
        let existing = self.store.check(key, fence).inspect_err(|_| {
            self.refused_current += u64::from(is_current);
            self.refused_unexplained += u64::from(is_current && !late_landed);
        })?;
        if self.stall && is_current && self.late.is_none() {
            self.late = Some(Late {
                body: body.as_ref().to_vec(),
                fence: existing,
            });
            self.blackout = true;
            self.stall = false;
            return Err(StoreError::Offline(Cause::Connect));
        }
        let tag = MemStore::tag(self.store.write(key, body.as_ref().to_vec()))?;
        match effect {
            Some(Effect::LandThen(err)) => {
                self.lost_fired += 1;
                return Err(err);
            }
            Some(Effect::LandThenDark(err)) => {
                self.lost_fired += 1;
                self.blackout = true;
                return Err(err);
            }
            _ => {}
        }
        Ok(tag)
    }
}
impl Store for FStore {
    fn put_if_absent(&mut self, key: &str, body: Published) -> Result<ETag, StoreError> {
        self.put(key, &body, None)
    }
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &ETag,
        body: Published,
    ) -> Result<ETag, StoreError> {
        self.put(key, &body, Some(etag))
    }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        self.gate()?;
        let (b, v) = self.store.objects.get(key).ok_or(StoreError::NotFound)?;
        Ok(Stored {
            body: b.clone(),
            etag: MemStore::tag(*v)?,
        })
    }
}

// ---------------------------------------------------------------- independent reader

struct Chain {
    folded: memory_core::manifest::Folded,
    current: Current,
    manifests: Vec<Vec<u8>>,
}
fn read_chain(store: &FStore, owner: &Owner) -> R<Option<Chain>> {
    let Some(pointer) = store.peek(&current_key(owner)) else {
        return Ok(None);
    };
    let current = parse_current(pointer).map_err(e)?;
    let mut hash = current.manifest;
    let mut raw: Vec<(Sha256, Vec<u8>)> = Vec::new();
    loop {
        ensure(raw.len() <= 64, "chain longer than 64")?;
        let bytes = need(store.peek(&manifest_key(&hash)), "manifest missing")?;
        ensure(sha(bytes) == hash, "manifest hash mismatch")?;
        let projection = recover_projection(bytes).map_err(e)?;
        raw.push((hash, bytes.to_vec()));
        match projection.kind {
            ProjectedKind::Checkpoint => break,
            ProjectedKind::Delta => hash = need(projection.previous, "delta without previous")?,
        }
    }
    raw.reverse();
    let refs: Vec<&[u8]> = raw.iter().map(|(_, b)| b.as_slice()).collect();
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
    for (_, bytes) in &raw {
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
        manifests: raw.into_iter().map(|(_, b)| b).collect(),
    }))
}
fn reassemble(store: &FStore, record: &EpochRecord) -> R<Vec<u8>> {
    memory_reader::restore_epoch(record, |key| {
        store
            .peek(key)
            .map(<[u8]>::to_vec)
            .ok_or_else(|| "piece object missing".to_owned())
    })
    .map_err(|err| format!("restore failed: {err:?}"))
}
struct Observed {
    latest: Option<Vec<u8>>,
    epochs: usize,
    seq: u64,
    launches: usize,
    deleted: bool,
}
/// Safety: the chain is consistent, every object matches its descriptor, and every committed
/// epoch is a prefix of bytes the (single) source once held.
fn observe(store: &FStore, owner: &Owner, history: &[Vec<u8>]) -> R<Option<Observed>> {
    observe_masked(store, owner, history, &BTreeSet::new())
}
/// [`observe`] with the per-position exemption of [`Reference::masked`].
fn observe_masked(
    store: &FStore,
    owner: &Owner,
    history: &[Vec<u8>],
    masked: &BTreeSet<usize>,
) -> R<Option<Observed>> {
    let Some(chain) = read_chain(store, owner)? else {
        return Ok(None);
    };
    let mut latest = None;
    let mut epochs = 0;
    let mut deleted = false;
    for entry in chain.folded.state.sources.values() {
        epochs += entry.epochs.len();
        deleted |= entry.deleted_at.is_some();
        for record in &entry.epochs {
            let bytes = reassemble(store, record)?;
            ensure(
                held(history, masked, &bytes),
                "committed bytes were never held by the source",
            )?;
            latest = Some(bytes);
        }
    }
    let mut launches = 0;
    for bytes in &chain.manifests {
        launches += match parse_manifest(bytes).map_err(e)? {
            Manifest::Delta(d) => d.launch().len(),
            Manifest::Checkpoint(c) => c.launch().len(),
        };
    }
    Ok(Some(Observed {
        latest,
        epochs,
        seq: chain.current.seq,
        launches,
        deleted,
    }))
}
fn ts() -> R<Timestamp> {
    Timestamp::try_from("2026-11-07T10:30:00Z".to_owned()).map_err(e)
}
fn make_foreign(store: &FStore, owner: &Owner) -> R<Foreign> {
    let head = read_chain(store, owner)?;
    let header = Header {
        owner: owner.clone(),
        captured_at: ts()?,
        writer: Writer {
            name: "other".to_owned(),
            version: "0.1.0".to_owned(),
            commit: "abc".to_owned(),
        },
        launch: vec![],
    };
    let built = build(
        head.as_ref().map(|c| (&c.folded.position, &c.folded.state)),
        header,
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

// ---------------------------------------------------------------- daemon helpers

/// Fixed-list sources. `note` maps a transcript path to the owner of the session named by its
/// file stem (Claude, archive `a`, machine `m`); the stems `a` and `b` stand for `s1` and `s2`.
struct FSrcs(Box<dyn FnMut() -> Vec<(Owner, Source)> + Send>);
impl FSrcs {
    fn new(list: impl FnMut() -> Vec<(Owner, Source)> + Send + 'static) -> Self {
        Self(Box::new(list))
    }
}
impl memory::daemon::Sources for FSrcs {
    fn enumerate(&mut self) -> Vec<(Owner, Source)> {
        (self.0)()
    }
    fn note(&mut self, path: &std::path::Path) -> Option<Owner> {
        let stem = path.file_stem()?.to_str()?;
        let session = match stem {
            "a" => "s1",
            "b" => "s2",
            other => other,
        };
        owner_of(session).ok()
    }
}
type D = Daemon<MemFs, FStore, TestClock, FSrcs>;

fn boot(
    tmp: &Tmp,
    cfg: &Path,
    fs: MemFs,
    store: FStore,
    list: &[(Owner, Source)],
    wall: i128,
) -> R<D> {
    boot_sync(tmp, cfg, fs, store, list, wall, SyncPolicy::Full)
}
fn boot_sync(
    tmp: &Tmp,
    cfg: &Path,
    fs: MemFs,
    store: FStore,
    list: &[(Owner, Source)],
    wall: i128,
    sync: SyncPolicy,
) -> R<D> {
    let mut d = boot_default_sleep(tmp, cfg, parts_of(fs, store, list, wall), sync)?;
    d.set_sleep(|_| {});
    Ok(d)
}
fn parts_of(
    fs: MemFs,
    store: FStore,
    list: &[(Owner, Source)],
    wall: i128,
) -> Parts<MemFs, FStore, TestClock, FSrcs> {
    let list = list.to_vec();
    let sources = FSrcs::new(move || list.clone());
    Parts {
        fs,
        store,
        clock: TestClock {
            wall,
            ..TestClock::at(0)
        },
        sources,
        context: PolicyContext {
            valid: true,
            revision: "r".to_owned(),
            rules: Some(rules_digest()),
            collector_paths: vec![],
            collector_identities: vec![],
        },
    }
}
/// A daemon that keeps its default (real) commit-pause sleep.
fn boot_default_sleep(
    tmp: &Tmp,
    cfg: &Path,
    parts: Parts<MemFs, FStore, TestClock, FSrcs>,
    sync: SyncPolicy,
) -> R<D> {
    Daemon::start_with(&tmp.root(), cfg, parts, StartOptions { sync })
        .map_err(|x| x.code().to_owned())
}
/// Restart: move the fs and store out, end the daemon (flushed or not) and start a new one.
fn cycle_sync(
    mut d: D,
    tmp: &Tmp,
    cfg: &Path,
    list: &[(Owner, Source)],
    flush: bool,
    sync: SyncPolicy,
) -> R<D> {
    let fs = std::mem::take(d.fs_mut());
    let store = std::mem::take(d.store_mut());
    if flush {
        d.shutdown().map_err(e)?;
    } else {
        drop(d);
    }
    boot_sync(tmp, cfg, fs, store, list, WALL, sync)
}
fn file_state(tmp: &Tmp, file: StateFile) -> Loaded {
    read_durable(&tmp.root().join(file.name()), file.schema())
}
fn status_via_state_dir(tmp: &Tmp) -> R<StatusBody> {
    let lock = DaemonLock::acquire(&tmp.root()).map_err(e)?;
    let state = StateDir::prepare(&lock).map_err(e)?;
    match state.read(StateFile::Status) {
        Loaded::Present(s) => serde_json::from_slice(&s.body).map_err(e),
        Loaded::Missing(r) => Err(format!("status missing: {r:?}")),
    }
}
fn index_of(tmp: &Tmp) -> R<Option<IndexBody>> {
    match file_state(tmp, StateFile::Index) {
        Loaded::Present(s) => Ok(Some(serde_json::from_slice(&s.body).map_err(e)?)),
        Loaded::Missing(_) => Ok(None),
    }
}
fn bytes_in(d: &mut D, handle: u64, bytes: &[u8]) {
    d.fs_mut().files.insert(handle, bytes.to_vec());
}

// ---------------------------------------------------------------- targeted tests

#[test]
fn lost_reply_converges_without_duplicate_epoch() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let mut store = FStore::default();
    store.arm(None, Effect::LandThen(StoreError::Ambiguous(Cause::Reset)));
    let mut d = boot(&tmp, &cfg, files_with(b"hello\n"), store, &list, WALL)?;
    d.reconcile_once().map_err(e)?;
    assert_eq!(d.store_mut().lost_fired, 1, "the reply was lost once");
    d.reconcile_once().map_err(e)?;
    d.reconcile_once().map_err(e)?;
    let after_converge = d.store_mut().put_log.len();
    d.reconcile_once().map_err(e)?;
    assert_eq!(d.store_mut().put_log.len(), after_converge, "converged");
    let hist = vec![b"hello\n".to_vec()];
    let obs = need(observe(d.store_mut(), &owner_of("s1")?, &hist)?, "no chain")?;
    assert_eq!(obs.epochs, 1, "no duplicate epoch");
    assert_eq!(obs.seq, 0, "no duplicate commit");
    assert_eq!(obs.latest.as_deref(), Some(b"hello\n".as_slice()));
    Ok(())
}

#[test]
fn foreign_writer_parks_owner_only() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?, src_of("s2", 2)?];
    let (a, b) = (owner_of("s1")?, owner_of("s2")?);
    let mut fs = files_with(b"a1\n");
    fs.files.insert(2, b"b1\n".to_vec());
    let mut d = boot(&tmp, &cfg, fs, FStore::default(), &list, WALL)?;
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 2);
    let foreign = make_foreign(d.store_mut(), &a)?;
    d.store_mut()
        .arm(Some(current_key(&a)), Effect::Foreign(foreign.clone()));
    bytes_in(&mut d, 1, b"a1\na2\n");
    bytes_in(&mut d, 2, b"b1\nb2\n");
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.parked, report.landed), (1, 1));
    // Persist, then read the status back through StateDir::read.
    let mut d = d;
    let fs = std::mem::take(d.fs_mut());
    let store = std::mem::take(d.store_mut());
    d.shutdown().map_err(e)?;
    let status = status_via_state_dir(&tmp)?;
    assert_eq!(status.parked.len(), 1);
    let entry = need(status.parked.first(), "parked entry")?;
    assert_eq!(entry.owner, a);
    assert!(matches!(
        entry.reason,
        ParkedReason::Owner(OwnerParkReason::ForeignWriter { .. })
    ));
    // Only the parked owner changed: no PUT at all.
    let mut d = boot(&tmp, &cfg, fs, store, &list, WALL)?;
    let before = d.store_mut().put_log.len();
    bytes_in(&mut d, 1, b"a1\na2\na3\n");
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!(report.skipped, 1);
    assert_eq!(
        d.store_mut().put_log.len(),
        before,
        "no PUT for a parked owner"
    );
    // The other owner still commits; the parked owner's head stays the foreign one.
    bytes_in(&mut d, 2, b"b1\nb2\nb3\n");
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!(report.landed, 1);
    assert_eq!(
        d.store_mut().peek(&current_key(&a)),
        Some(foreign.current.as_slice())
    );
    let hist = vec![
        b"b1\n".to_vec(),
        b"b1\nb2\n".to_vec(),
        b"b1\nb2\nb3\n".to_vec(),
    ];
    let obs = need(observe(d.store_mut(), &b, &hist)?, "no chain for b")?;
    assert_eq!(obs.latest.as_deref(), Some(b"b1\nb2\nb3\n".as_slice()));
    Ok(())
}

fn hint_line(session: &str, env: &[&str]) -> R<Vec<u8>> {
    let argv = vec!["claude".to_owned()];
    let env: Vec<String> = env.iter().map(|s| (*s).to_owned()).collect();
    let launch = sanitize_launch(&argv, &env);
    let record = HintRecord {
        schema: HintSchema::V1,
        agent: Agent::Claude,
        event: HintEvent::SessionStart,
        session: SessionId::try_from(session.to_owned()).map_err(e)?,
        cwd: PathHint::Absent,
        transcript: PathHint::Present {
            path: HintPath::try_from(format!("/r/projects/p/{session}.jsonl")).map_err(e)?,
        },
        agent_transcript: PathHint::Absent,
        launch: LaunchHint::Full(LaunchRecord::from_sanitized(&launch)),
        reduction: Reduction::None,
    };
    let mut line = serde_json::to_vec(&record).map_err(e)?;
    line.push(b'\n');
    Ok(line)
}

#[test]
fn a_hint_launch_goes_to_the_owner_of_its_path_not_to_the_namesake() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?, src_of("s2", 2)?];
    let mut fs = files_with(b"hello\n");
    fs.files.insert(2, b"hello\n".to_vec());
    let mut d = boot(&tmp, &cfg, fs, FStore::default(), &list, WALL)?;
    let argv = vec!["claude".to_owned()];
    let launch = sanitize_launch(&argv, &["PATH=/usr/bin".to_owned()]);
    let record = HintRecord {
        schema: HintSchema::V1,
        agent: Agent::Claude,
        event: HintEvent::SessionStart,
        // The text of the namesake's session, but the transcript is the other session's.
        session: SessionId::try_from("s1".to_owned()).map_err(e)?,
        cwd: PathHint::Absent,
        transcript: PathHint::Present {
            path: HintPath::try_from("/r/projects/p/s2.jsonl".to_owned()).map_err(e)?,
        },
        agent_transcript: PathHint::Absent,
        launch: LaunchHint::Full(LaunchRecord::from_sanitized(&launch)),
        reduction: Reduction::None,
    };
    let mut line = serde_json::to_vec(&record).map_err(e)?;
    line.push(b'\n');
    fs::write(hint_path(&tmp, "2026-11-09.jsonl"), &line).map_err(e)?;
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 2);
    let hist = vec![b"hello\n".to_vec()];
    let two = need(
        observe(d.store_mut(), &owner_of("s2")?, &hist)?,
        "no chain s2",
    )?;
    let one = need(
        observe(d.store_mut(), &owner_of("s1")?, &hist)?,
        "no chain s1",
    )?;
    assert_eq!((one.launches, two.launches), (0, 1));
    Ok(())
}

#[test]
fn hint_launch_attached_once_and_offset_advances_after_commit() -> R {
    let canary = "zzcanary123";
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let store = FStore {
        offline: true,
        ..FStore::default()
    };
    let mut d = boot(&tmp, &cfg, files_with(b"hello\n"), store, &list, WALL)?;
    let name = "2026-11-09.jsonl";
    let line = hint_line(
        "s1",
        &["PATH=/usr/bin", &format!("MEMORY_CANARY_ZZ={canary}")],
    )?;
    fs::write(tmp.root().join("hints").join(name), &line).map_err(e)?;
    // A failed commit: the offset must not advance.
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.landed, report.failed), (0, 1));
    let offset = |tmp: &Tmp| -> R<u64> {
        Ok(index_of(tmp)?
            .and_then(|i| i.hints.get(name).copied())
            .unwrap_or(0))
    };
    assert_eq!(offset(&tmp)?, 0, "offset unchanged after a failed commit");
    // Success: the launch lands once and the offset covers the line.
    d.store_mut().offline = false;
    d.clock_mut().mono = 61_000_000_000;
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 1);
    assert_eq!(
        offset(&tmp)?,
        len64(&line),
        "offset advanced after the commit"
    );
    let o = owner_of("s1")?;
    let hist = vec![b"hello\n".to_vec(), b"hello\nmore\n".to_vec()];
    let obs = need(observe(d.store_mut(), &o, &hist)?, "no chain")?;
    assert_eq!(obs.launches, 1);
    // More bytes, another commit: still exactly one launch record.
    bytes_in(&mut d, 1, b"hello\nmore\n");
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 1);
    let obs = need(observe(d.store_mut(), &o, &hist)?, "no chain")?;
    assert_eq!(obs.launches, 1, "attached exactly once");
    assert!(
        d.store_mut()
            .store
            .objects
            .values()
            .all(|(b, _)| !contains(b, canary.as_bytes())),
        "canary must never be stored"
    );
    Ok(())
}

#[test]
fn auth_failure_pauses_host_and_recovers() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let store = FStore {
        revoked: true,
        ..FStore::default()
    };
    let mut d = boot(&tmp, &cfg, files_with(b"hello\n"), store, &list, WALL)?;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.landed, report.failed), (0, 1));
    assert!(d.status().host_paused);
    match file_state(&tmp, StateFile::Status) {
        Loaded::Present(s) => {
            let body: StatusBody = serde_json::from_slice(&s.body).map_err(e)?;
            assert!(body.host_paused, "PauseHost recorded in status.json");
        }
        Loaded::Missing(r) => return Err(format!("status missing {r:?}")),
    }
    d.store_mut().revoked = false;
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 1);
    assert!(!d.status().host_paused);
    Ok(())
}

#[test]
fn unrenderable_wall_clock_is_a_round_error() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    // Year > 9999 does not fit a manifest timestamp.
    let bad: i128 = 300_000_000_000 * 1_000_000_000;
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"x\n"),
        FStore::default(),
        &list,
        bad,
    )?;
    let result = d.reconcile_once();
    assert!(matches!(result, Err(memory::daemon::RoundError::Clock)));
    assert_eq!(d.status().rounds, 0);
    assert!(d.store_mut().put_log.is_empty());
    Ok(())
}

fn flip_last_byte(path: &Path) -> R {
    let mut bytes = fs::read(path).map_err(e)?;
    let last = need(bytes.last_mut(), "empty state file")?;
    *last ^= 0x01;
    fs::write(path, bytes).map_err(e)
}

#[test]
fn damaged_index_and_status_are_treated_as_missing() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    d.reconcile_once().map_err(e)?;
    d.reconcile_once().map_err(e)?;
    assert_eq!(d.status().rounds, 2);
    let fs = std::mem::take(d.fs_mut());
    let store = std::mem::take(d.store_mut());
    d.shutdown().map_err(e)?;
    flip_last_byte(&tmp.root().join("index.json"))?;
    flip_last_byte(&tmp.root().join("status.json"))?;
    assert!(matches!(
        file_state(&tmp, StateFile::Index),
        Loaded::Missing(_)
    ));
    assert!(matches!(
        file_state(&tmp, StateFile::Status),
        Loaded::Missing(_)
    ));
    let puts = store.put_log.len();
    let mut d = boot(&tmp, &cfg, fs, store, &list, WALL)?;
    assert!(d.rebuilt(), "damaged index rebuilds");
    assert_eq!(
        (d.index_load(), d.status_load()),
        (StateLoad::Damaged, StateLoad::Damaged)
    );
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 0);
    assert_eq!(d.status().rounds, 1, "status counters restarted");
    assert_eq!(d.store_mut().put_log.len(), puts, "no re-upload");
    Ok(())
}

/// Replace the first `from` in a state file's header with `to` (same length keeps the body).
fn patch_header(path: &Path, from: &str, to: &str) -> R {
    let text = fs::read(path).map_err(e)?;
    let head_end = need(text.iter().position(|b| *b == b'\n'), "header")?;
    let (head, rest) = text.split_at(head_end);
    let head = String::from_utf8(head.to_vec()).map_err(e)?;
    ensure(head.contains(from), "header lacks the text to replace")?;
    let mut out = head.replacen(from, to, 1).into_bytes();
    out.extend_from_slice(rest);
    fs::write(path, out).map_err(e)
}

/// A daemon that has run two rounds with an owner parked for a newer remote schema, then shut
/// down; returns what a restart needs.
fn parked_daemon(tmp: &Tmp, cfg: &Path, list: &[(Owner, Source)]) -> R<(MemFs, FStore)> {
    // s1 is parked for a newer remote schema; s2 commits, so the index is written too.
    let mut fs = files_with(b"hello\n");
    fs.files.insert(2, b"world\n".to_vec());
    let mut d = boot(tmp, cfg, fs, FStore::default(), list, WALL)?;
    d.store_mut()
        .store
        .write(&current_key(&owner_of("s1")?), NEWER.to_vec());
    assert_eq!(d.reconcile_once().map_err(e)?.parked, 1);
    d.reconcile_once().map_err(e)?;
    let fs = std::mem::take(d.fs_mut());
    let store = std::mem::take(d.store_mut());
    d.shutdown().map_err(e)?;
    Ok((fs, store))
}

/// DESIGN.md 9.2: a damaged or newer-schema `status.json` is treated as missing, so the parked
/// list and host pause are lost. Each case is a distinct open result; the log line each one
/// causes is checked on the real binary in `harness`. Kills: mapping every failure to `Missing`
/// and swapping the damaged and newer-schema classes.
#[test]
fn status_and_index_load_cases_are_distinct() -> R {
    let list = vec![src_of("s1", 1)?, src_of("s2", 2)?];
    // (what happens to status.json, expected result)
    type Damage = fn(&Tmp) -> R;
    let cases: [(&str, Damage, StateLoad); 4] = [
        (
            "checksum flip",
            |t| flip_last_byte(&t.root().join("status.json")),
            StateLoad::Damaged,
        ),
        (
            "newer schema",
            |t| {
                patch_header(
                    &t.root().join("status.json"),
                    "memory.status/1",
                    "memory.status/2",
                )
            },
            StateLoad::NewerSchema,
        ),
        (
            "valid envelope, unparsable body",
            |t| {
                let lock = DaemonLock::acquire(&t.root()).map_err(e)?;
                let state = StateDir::prepare(&lock).map_err(e)?;
                state
                    .write(StateFile::Status, b"{\"rounds\":\"x\"}")
                    .map_err(e)
            },
            StateLoad::Damaged,
        ),
        (
            "deleted",
            |t| fs::remove_file(t.root().join("status.json")).map_err(e),
            StateLoad::Missing,
        ),
    ];
    for (label, damage, load) in cases {
        let tmp = Tmp::new()?;
        let cfg = tmp.config(true)?;
        let (fs, store) = parked_daemon(&tmp, &cfg, &list)?;
        assert_eq!(
            status_via_state_dir(&tmp)?.parked.len(),
            1,
            "{label}: parked"
        );
        damage(&tmp)?;
        let mut d = boot(&tmp, &cfg, fs, store, &list, WALL)?;
        assert_eq!(d.status_load(), load, "{label}");
        assert_eq!(d.index_load(), StateLoad::Loaded, "{label}");
        assert!(
            d.status().parked.is_empty() && !d.status().host_paused,
            "{label}: the parked list is lost with the file"
        );
        // The replacement status is written and loads on the next start.
        d.reconcile_once().map_err(e)?;
        let fs = std::mem::take(d.fs_mut());
        let store = std::mem::take(d.store_mut());
        d.shutdown().map_err(e)?;
        let d = boot(&tmp, &cfg, fs, store, &list, WALL)?;
        assert_eq!(
            d.status_load(),
            StateLoad::Loaded,
            "{label}: replacement loads"
        );
    }
    // The index: damaged and newer-schema rebuild; a missing one (first start) does too.
    let cases: [(&str, Damage, StateLoad); 3] = [
        (
            "index checksum flip",
            |t| flip_last_byte(&t.root().join("index.json")),
            StateLoad::Damaged,
        ),
        (
            "index newer schema",
            |t| {
                patch_header(
                    &t.root().join("index.json"),
                    "memory.index/1",
                    "memory.index/2",
                )
            },
            StateLoad::NewerSchema,
        ),
        (
            "index deleted",
            |t| fs::remove_file(t.root().join("index.json")).map_err(e),
            StateLoad::Missing,
        ),
    ];
    for (label, damage, load) in cases {
        let tmp = Tmp::new()?;
        let cfg = tmp.config(true)?;
        let (fs, store) = parked_daemon(&tmp, &cfg, &list)?;
        damage(&tmp)?;
        let d = boot(&tmp, &cfg, fs, store, &list, WALL)?;
        assert_eq!(d.index_load(), load, "{label}");
        assert!(d.rebuilt(), "{label}");
        assert_eq!(d.status_load(), StateLoad::Loaded, "{label}");
        assert_eq!(
            d.status().parked.len(),
            1,
            "{label}: an index problem keeps the parked list"
        );
    }
    // A first start: both files are missing.
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    assert_eq!(
        (d.index_load(), d.status_load()),
        (StateLoad::Missing, StateLoad::Missing)
    );
    Ok(())
}

/// A `status.json` written by a build that still had the `events` object is refused by
/// `deny_unknown_fields`: it loads as damaged and its parked list is lost. No shim.
#[test]
fn a_status_with_an_events_object_loads_as_damaged() -> R {
    let list = vec![src_of("s1", 1)?];
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    {
        let lock = DaemonLock::acquire(&tmp.root()).map_err(e)?;
        let state = StateDir::prepare(&lock).map_err(e)?;
        let body = br#"{"updated_at":"2026-11-07T10:30:00Z","rounds":1,"host_paused":false,"parked":[],"failures":{"offline":0,"throttled":0,"transient":0,"destination_auth":0,"local":0,"deterministic":0},"events":{"status_damaged":0,"status_newer_schema":0,"index_damaged":0,"index_newer_schema":0}}"#;
        state.write(StateFile::Status, body).map_err(e)?;
    }
    let d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    assert_eq!(d.status_load(), StateLoad::Damaged);
    assert_eq!(d.status().rounds, 0);
    Ok(())
}

/// One round under a scoped subscriber: the report and the log lines without timestamps.
macro_rules! logged_round {
    ($d:expr) => {{
        let mut out = None;
        let text = memory::log::render(|| out = Some($d.reconcile_once()));
        let report = need(out, "no round ran")?.map_err(e)?;
        let lines: Vec<String> = text
            .lines()
            .map(|l| {
                l.split_once(' ')
                    .map(|(_, r)| r.to_owned())
                    .unwrap_or_default()
            })
            .collect();
        (report, lines)
    }};
}

#[test]
fn an_owner_is_logged_once_when_it_becomes_parked() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?, src_of("s2", 2)?];
    let a = owner_of("s1")?;
    let mut fs = files_with(b"a1\n");
    fs.files.insert(2, b"b1\n".to_vec());
    let mut d = boot(&tmp, &cfg, fs, FStore::default(), &list, WALL)?;
    let (report, lines) = logged_round!(d);
    assert_eq!(report.landed, 2);
    assert!(lines.is_empty(), "{lines:?}");
    let foreign = make_foreign(d.store_mut(), &a)?;
    d.store_mut()
        .arm(Some(current_key(&a)), Effect::Foreign(foreign));
    bytes_in(&mut d, 1, b"a1\na2\n");
    let (report, lines) = logged_round!(d);
    assert_eq!(report.parked, 1);
    assert_eq!(
        lines,
        [format!(
            "INFO parked owner={} reason=foreign_writer scope=owner",
            memory_core::limits::owner_prefix(&a)
        )]
    );
    for _ in 0..2 {
        bytes_in(&mut d, 1, b"a1\na2\na3\n");
        let (report, lines) = logged_round!(d);
        assert_eq!(report.skipped, 1);
        assert!(lines.is_empty(), "no repeat while parked: {lines:?}");
    }
    Ok(())
}

#[test]
fn a_source_park_is_logged_with_its_reason() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let o = owner_of("s1")?;
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    d.store_mut()
        .store
        .write(&current_key(&o), b"garbage".to_vec());
    let hour = 3_600_000_000_000;
    let mut seen = Vec::new();
    for at in [0, hour, 3 * hour] {
        d.clock_mut().mono = at;
        seen.push(logged_round!(d).1);
    }
    let want = format!(
        "INFO parked owner={} reason=repeated_schema_error scope=owner",
        memory_core::limits::owner_prefix(&o)
    );
    // Each failed round also logs one `retry` line (attempts 1 and 2); the park line is unchanged.
    let retries: Vec<usize> = seen
        .iter()
        .map(|l| {
            l.iter()
                .filter(|x| x.starts_with("INFO retry owner="))
                .count()
        })
        .collect();
    assert_eq!(retries, [1, 1, 0]);
    let seen: Vec<Vec<String>> = seen
        .into_iter()
        .map(|l| {
            l.into_iter()
                .filter(|x| !x.starts_with("INFO retry owner="))
                .collect()
        })
        .collect();
    assert_eq!(seen, [vec![], vec![], vec![want]]);
    d.clock_mut().mono = 3 * hour + 1;
    assert!(logged_round!(d).1.is_empty());
    Ok(())
}

#[test]
fn host_pause_and_resume_are_logged_on_the_change_only() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let store = FStore {
        revoked: true,
        ..FStore::default()
    };
    let mut d = boot(&tmp, &cfg, files_with(b"hello\n"), store, &list, WALL)?;
    let (_, lines) = logged_round!(d);
    assert_eq!(lines, ["INFO host_paused"]);
    assert!(d.status().host_paused);
    // Still revoked, after the retry delay: paused again, no new event.
    d.clock_mut().mono = 10_000_000_000_000;
    let (_, lines) = logged_round!(d);
    assert!(d.status().host_paused);
    assert!(lines.is_empty(), "{lines:?}");
    d.store_mut().revoked = false;
    d.clock_mut().mono = 20_000_000_000_000;
    let (report, lines) = logged_round!(d);
    assert_eq!(report.landed, 1);
    assert_eq!(lines, ["INFO host_resumed"]);
    d.clock_mut().mono = 30_000_000_000_000;
    assert!(logged_round!(d).1.is_empty());
    Ok(())
}

// ---------------------------------------------------------------- coverage guard

const SHAPE_NAMES: [&str; 13] = [
    "restart",
    "lost-reply",
    "delete-index",
    "rewrite",
    "rewrite-mid-file",
    "truncate",
    "remove-source",
    "revoke",
    "crash",
    "late-write",
    "multi-epoch",
    "big-file",
    "rename-replace",
];
const REQUIRED_NEW: [&str; 6] = [
    "rewrite",
    "truncate",
    "remove-source",
    "revoke",
    "crash",
    "late-write",
];
struct Shapes {
    n: [AtomicU64; SHAPE_NAMES.len()],
    /// Generator-efficiency counters: cases in which each shape was reached, and the totals
    /// at the end of the previous case. They assert nothing (see `end_case`).
    cases: AtomicU64,
    cases_with: [AtomicU64; SHAPE_NAMES.len()],
    last: [AtomicU64; SHAPE_NAMES.len()],
}
impl Shapes {
    const fn new() -> Self {
        Self {
            n: [const { AtomicU64::new(0) }; SHAPE_NAMES.len()],
            cases: AtomicU64::new(0),
            cases_with: [const { AtomicU64::new(0) }; SHAPE_NAMES.len()],
            last: [const { AtomicU64::new(0) }; SHAPE_NAMES.len()],
        }
    }
    /// A random case ended: count each shape it reached. When `SHAPE_RATES` names a file, the
    /// per-case counts so far are written there (replacing it), so a run can be measured.
    fn end_case(&self, label: &str) {
        bump(&self.cases);
        for ((n, last), with) in self.n.iter().zip(&self.last).zip(&self.cases_with) {
            let now = n.load(Ordering::Relaxed);
            if now != last.swap(now, Ordering::Relaxed) {
                bump(with);
            }
        }
        if let Some(path) = std::env::var_os("SHAPE_RATES") {
            let mut line = format!("{label} cases={}", self.cases.load(Ordering::Relaxed));
            for (name, with) in SHAPE_NAMES.iter().zip(&self.cases_with) {
                line.push_str(&format!(" {name}={}", with.load(Ordering::Relaxed)));
            }
            let _ = fs::write(path, line);
        }
    }
    fn hit(&self, name: &str) {
        if let Some(i) = SHAPE_NAMES.iter().position(|x| *x == name)
            && let Some(c) = self.n.get(i)
        {
            bump(c);
        }
    }
    fn counts(&self) -> Vec<(&'static str, u64)> {
        SHAPE_NAMES
            .iter()
            .zip(self.n.iter())
            .map(|(k, c)| (*k, c.load(Ordering::Relaxed)))
            .collect()
    }
}
fn bump(counter: &AtomicU64) {
    counter.fetch_add(1, Ordering::Relaxed);
}
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

#[test]
fn guard_has_negative_controls() {
    let zero = Shapes::new();
    assert!(require_all(&zero.counts()).is_err());
    assert!(require_all(&[]).is_err());
    // The guard names every new shape the phase requires.
    for need in REQUIRED_NEW {
        assert!(SHAPE_NAMES.contains(&need), "guard lost shape {need}");
    }
    // Each shape missing on its own (including each new shape) fails the guard.
    for missing in 0..SHAPE_NAMES.len() {
        let named: Vec<_> = SHAPE_NAMES
            .iter()
            .enumerate()
            .map(|(i, name)| (*name, u64::from(i != missing)))
            .collect();
        let err = require_all(&named);
        assert!(err.is_err(), "missing shape {missing}");
        assert!(
            err.err()
                .is_some_and(|m| m.contains(SHAPE_NAMES.get(missing).copied().unwrap_or(""))),
            "guard names the missing shape {missing}"
        );
    }
    // A shape that is only hit on the other side's statics does not count.
    zero.hit("crash");
    assert!(zero.counts().iter().any(|(k, n)| *k == "crash" && *n == 1));
    assert!(require_all(&zero.counts()).is_err());
    let full: Vec<_> = zero.counts().into_iter().map(|(n, _)| (n, 1)).collect();
    assert!(require_all(&full).is_ok());
}

// ---------------------------------------------------------------- model and system under test

#[derive(Clone, Debug)]
enum Step {
    Append {
        seed: u8,
        n: u8,
    },
    Bulk {
        seed: u8,
        kib: u8,
    },
    Rewrite {
        zone: u8,
        delta: u16,
    },
    /// A new file with a new identity replaces the path, with the same bytes or one byte changed.
    RenameReplace {
        change: bool,
        delta: u16,
    },
    Truncate {
        frac: u8,
    },
    RemoveSource,
    RevokeToken,
    RestoreToken,
    Crash {
        manifest: bool,
    },
    LateWrite {
        seed: u8,
        n: u8,
    },
    Round,
    DeleteIndex,
    Restart,
    /// The next `current.json` write lands but its reply is lost; with `dark` the store then
    /// stays unreachable, so the writer cannot verify the landing (LandingUnknown).
    LostReply {
        dark: bool,
    },
    Offline,
    Online,
    Settle,
}

const HEAD: usize = 4096;
const TAIL: usize = 65536;

/// The independent model: the bytes the source holds now and every version it ever held.
#[derive(Clone, Debug, Default)]
struct Reference {
    file: Vec<u8>,
    exists: bool,
    history: Vec<Vec<u8>>,
    /// The last step changed the source for real (not a no-op on an empty or absent file).
    effect: bool,
    /// The last step rewrote this position outside both windows.
    mid_pos: Option<usize>,
    /// The fake store derives ETags from content (see [`EtagModel`]).
    content_etag: bool,
    /// Positions ever changed by a same-size rewrite between the head and tail windows.
    /// DESIGN 1a covers only changes that alter identity or touch a window; only an audit
    /// finds such a rewrite (DESIGN 7.2) and reconcile rounds run none. At these positions,
    /// and only these, a committed byte may be stale, but it must still be a value the source
    /// held at that position. Never cleared: every committed epoch is rechecked on every step.
    masked: BTreeSet<usize>,
    /// Generator hints, never read by an oracle: the source changed since the last round
    /// (bytes a commit does not carry yet), some round ran with bytes on disk (something is
    /// probably committed), a lost reply is armed, and the token is revoked.
    dirty: bool,
    synced: bool,
    lost_armed: bool,
    revoked: bool,
}

/// `bytes[i]` is acceptable against `v[i]`: equal, or a masked position holding a value the
/// source held there at some point.
fn byte_ok(history: &[Vec<u8>], masked: &BTreeSet<usize>, i: usize, b: u8, v: u8) -> bool {
    b == v || (masked.contains(&i) && history.iter().any(|w| w.get(i) == Some(&b)))
}

/// 1a oracle: `bytes` is a prefix of some version the source held, except at masked positions.
fn held(history: &[Vec<u8>], masked: &BTreeSet<usize>, bytes: &[u8]) -> bool {
    history.iter().any(|v| {
        v.len() >= bytes.len()
            && bytes
                .iter()
                .zip(v)
                .enumerate()
                .all(|(i, (b, x))| byte_ok(history, masked, i, *b, *x))
    })
}
impl Reference {
    fn grow(&mut self, add: impl Iterator<Item = u8>) {
        let add: Vec<u8> = add.collect();
        self.exists = true;
        self.file.extend_from_slice(&add);
        self.history.push(self.file.clone());
    }
    /// Update the generator hints (see the fields) for `step`.
    fn hint(&mut self, step: &Step) {
        match step {
            Step::Append { .. }
            | Step::Bulk { .. }
            | Step::Rewrite { .. }
            | Step::RenameReplace { .. }
            | Step::Truncate { .. }
            | Step::RemoveSource
            | Step::LateWrite { .. } => self.dirty = true,
            Step::Round | Step::Settle | Step::Crash { .. } => {
                self.dirty = false;
                self.synced |= self.exists && !self.file.is_empty();
            }
            Step::LostReply { .. } => self.lost_armed = true,
            Step::RevokeToken => self.revoked = true,
            Step::RestoreToken => self.revoked = false,
            _ => {}
        }
        if matches!(step, Step::Round | Step::Settle) {
            self.lost_armed = false;
        }
        if matches!(step, Step::Settle) {
            self.revoked = false;
        }
    }
    fn apply(&mut self, step: &Step) {
        self.hint(step);
        self.effect = false;
        self.mid_pos = None;
        match step {
            Step::Append { seed, n } | Step::LateWrite { seed, n } => {
                self.grow((0..(*n).max(1)).map(|k| seed.wrapping_add(k) % 3));
            }
            Step::Bulk { seed, kib } => {
                let len = usize::from((*kib).max(1)) * 1024;
                self.grow((0..len).map(|k| seed.wrapping_add(u8::try_from(k % 251).unwrap_or(0))));
            }
            Step::Rewrite { zone, delta } => {
                let len = self.file.len();
                let d = usize::from(*delta);
                let pos = if !self.exists || len == 0 {
                    None
                } else {
                    Some(match zone % 3 {
                        0 => d % len.min(HEAD),
                        1 => len - 1 - d % len.min(TAIL),
                        _ if len > HEAD + TAIL => HEAD + d * 7 % (len - HEAD - TAIL),
                        _ => d % len,
                    })
                };
                if let Some(p) = pos
                    && let Some(b) = self.file.get_mut(p)
                {
                    *b = b.wrapping_add(1);
                    self.effect = true;
                    if zone % 3 == 2 && len > HEAD + TAIL {
                        self.mid_pos = Some(p);
                        self.masked.insert(p);
                    }
                    self.history.push(self.file.clone());
                }
            }
            Step::RenameReplace { change, delta } if self.exists => {
                // A new identity is always detected (DESIGN 1a), so no position is masked.
                self.effect = true;
                if *change
                    && let Some(i) = usize::from(*delta).checked_rem(self.file.len())
                    && let Some(b) = self.file.get_mut(i)
                {
                    *b = b.wrapping_add(1);
                    self.history.push(self.file.clone());
                }
            }
            Step::Truncate { frac } => {
                let len = self.file.len();
                let k = len * usize::from(*frac) / 255;
                if self.exists && k < len {
                    self.file.truncate(k);
                    self.effect = true;
                    self.history.push(self.file.clone());
                }
            }
            Step::RemoveSource if self.exists => {
                self.effect = true;
                self.exists = false;
                self.file.clear();
            }
            _ => {}
        }
    }
}

/// Only the simulation properties use `Sut`, and they run with [`SyncPolicy::None`] (A7).
struct Sut {
    tmp: Tmp,
    cfg: PathBuf,
    owner: Owner,
    list: Vec<(Owner, Source)>,
    d: Option<D>,
    mono: u64,
    ran: bool,
    index_expected: bool,
    lost_seen: u64,
    late_seen: u64,
    max_epochs: usize,
}
impl Sut {
    fn new(model: EtagModel) -> R<Self> {
        let tmp = Tmp::new()?;
        let cfg = tmp.config(true)?;
        let list = vec![src_of("s1", 1)?];
        let d = boot_sync(
            &tmp,
            &cfg,
            MemFs::default(),
            FStore::with_model(model),
            &list,
            WALL,
            SyncPolicy::None,
        )?;
        Ok(Self {
            tmp,
            cfg,
            owner: owner_of("s1")?,
            list,
            d: Some(d),
            mono: 0,
            ran: false,
            index_expected: false,
            lost_seen: 0,
            late_seen: 0,
            max_epochs: 0,
        })
    }
    fn daemon(&mut self) -> R<&mut D> {
        self.d.as_mut().ok_or_else(|| "no daemon".to_owned())
    }
    /// Process death and restart. A write still in flight from the dead process dies with its
    /// connection, and `known_ours` is in memory only (A6), so the pending late write is dropped.
    fn restart(&mut self) -> R {
        {
            let store = self.daemon()?.store_mut();
            store.late = None;
            store.crashed = None;
            store.crash_at = None;
            store.blackout = false;
            store.stall = false;
        }
        let d = need(self.d.take(), "no daemon")?;
        self.d = Some(cycle_sync(
            d,
            &self.tmp,
            &self.cfg,
            &self.list,
            false,
            SyncPolicy::None,
        )?);
        self.mono = 0;
        Ok(())
    }
    fn round(&mut self, shapes: &Shapes) -> R {
        self.round_tracking(shapes, true)
    }
    /// `track_index` is false for rounds that may land only an adopted earlier manifest (A6,
    /// `CommitEnd::Adopted`): that commit applies no local updates, so it need not dirty the
    /// index.
    fn round_tracking(&mut self, shapes: &Shapes, track_index: bool) -> R {
        self.mono += 61_000_000_000;
        let mono = self.mono;
        let d = self.daemon()?;
        d.clock_mut().mono = mono;
        let (before, revoked) = (d.store_mut().store.version, d.store_mut().revoked);
        let report = d.reconcile_once().map_err(e)?;
        if revoked {
            ensure(
                d.store_mut().store.version == before,
                "a write landed while the token was revoked",
            )?;
            if d.status().host_paused {
                shapes.hit("revoke");
            }
        }
        self.ran = true;
        self.index_expected |= track_index && report.landed > 0;
        Ok(())
    }
    /// Size of the newest committed epoch (0 when nothing is committed): what a rewrite of
    /// already-committed bytes must fall inside.
    fn committed_len(&mut self) -> R<u64> {
        let owner = self.owner.clone();
        let chain = read_chain(self.daemon()?.store_mut(), &owner)?;
        Ok(chain
            .and_then(|c| {
                c.folded
                    .state
                    .sources
                    .values()
                    .filter_map(|entry| entry.epochs.last().map(|r| r.size))
                    .max()
            })
            .unwrap_or(0))
    }
    fn sync_file(&mut self, after: &Reference) -> R {
        let d = self.daemon()?;
        if after.exists {
            d.fs_mut().set(1, &after.file);
        } else {
            d.fs_mut().files.remove(&1);
        }
        Ok(())
    }
    fn exec(&mut self, step: &Step, after: &Reference, shapes: &Shapes) -> R {
        match step {
            Step::Append { .. }
            | Step::Bulk { .. }
            | Step::Rewrite { .. }
            | Step::Truncate { .. }
            | Step::RemoveSource => {
                self.sync_file(after)?;
                if after.effect {
                    match step {
                        Step::Rewrite { .. } => {
                            shapes.hit("rewrite");
                            // Only a rewrite of bytes a commit already carries counts: before
                            // any commit there is nothing for a mid-file rewrite to go stale.
                            if let Some(p) = after.mid_pos
                                && self.committed_len()? > u64::try_from(p).unwrap_or(u64::MAX)
                            {
                                shapes.hit("rewrite-mid-file");
                            }
                        }
                        Step::Truncate { .. } => shapes.hit("truncate"),
                        Step::RemoveSource => {
                            let key = current_key(&self.owner);
                            if self.daemon()?.store_mut().peek(&key).is_some() {
                                shapes.hit("remove-source");
                            }
                        }
                        _ => {}
                    }
                }
                if after.file.len() > HEAD + TAIL {
                    shapes.hit("big-file");
                }
            }
            Step::RenameReplace { .. } => {
                let committed = self.committed_len()?;
                if after.exists {
                    let fs = self.daemon()?.fs_mut();
                    fs.replace(1, &after.file, fs.generation(1) + 1);
                }
                if after.effect && committed > 0 {
                    shapes.hit("rename-replace");
                }
            }
            Step::Round => self.round(shapes)?,
            Step::RevokeToken => self.daemon()?.store_mut().revoked = true,
            Step::RestoreToken => self.daemon()?.store_mut().revoked = false,
            Step::Crash { manifest } => {
                self.daemon()?.store_mut().crash_at = Some(if *manifest {
                    CrashAt::Manifest
                } else {
                    CrashAt::Current
                });
                self.round(shapes)?;
                let store = self.daemon()?.store_mut();
                store.crash_at = None;
                if store.crashed.take().is_some() {
                    self.restart()?;
                    shapes.hit("crash");
                }
            }
            Step::LateWrite { .. } => {
                // Attempt 1 commits what the source holds; its current.json write is held in the
                // network and the store goes dark, so the capture fails. Then new bytes arrive,
                // a later capture starts, and the held write lands before its PUT (A6).
                self.daemon()?.store_mut().stall = true;
                self.round_tracking(shapes, false)?;
                {
                    let store = self.daemon()?.store_mut();
                    store.stall = false;
                    store.blackout = false;
                }
                self.sync_file(after)?;
                self.mono += 140_000_000_000;
                self.round_tracking(shapes, false)?;
            }
            Step::DeleteIndex => {
                let _ = fs::remove_file(self.tmp.root().join("index.json"));
                self.restart()?;
                self.index_expected = false;
                shapes.hit("delete-index");
            }
            Step::Restart => {
                self.restart()?;
                shapes.hit("restart");
            }
            Step::LostReply { dark } => {
                let lost = StoreError::Ambiguous(Cause::Reset);
                let effect = if *dark {
                    Effect::LandThenDark(lost)
                } else {
                    Effect::LandThen(lost)
                };
                self.daemon()?.store_mut().arm(None, effect);
            }
            Step::Offline => self.daemon()?.store_mut().offline = true,
            Step::Online => {
                let store = self.daemon()?.store_mut();
                store.offline = false;
                store.blackout = false;
            }
            Step::Settle => {
                let store = self.daemon()?.store_mut();
                store.offline = false;
                store.revoked = false;
                store.blackout = false;
                store.stall = false;
                store.crash_at = None;
                store.rules.clear();
                // Liveness is promised only once the longest retry delay has passed: after a
                // run of failures the owner is deferred for up to 15 min (DESIGN.md 11.2), so
                // rounds inside that delay rightly do nothing.
                self.mono += 900_000_000_000;
                for _ in 0..4 {
                    self.round(shapes)?;
                }
            }
        }
        let (fired, late) = {
            let s = self.daemon()?.store_mut();
            (s.lost_fired, s.late_landed)
        };
        for _ in self.lost_seen..fired {
            shapes.hit("lost-reply");
        }
        for _ in self.late_seen..late {
            shapes.hit("late-write");
        }
        self.lost_seen = fired;
        self.late_seen = late;
        // A `current.json` write is refused only when another write got in first, and in this
        // model only a late write (A6) does: the refusal comes in the very PUT the held write
        // lands ahead of. That PUT can come steps later, when the round after the stall had
        // nothing to commit. Any other refusal means a round fenced on a stale base: a landing
        // it could not verify, or a rebase-exhausted fence, was not reset.
        ensure(
            self.daemon()?.store_mut().refused_unexplained == 0,
            "a current.json write was refused with no late write ahead of it (stale If-Match base)",
        )?;
        Ok(())
    }
    /// Every (size, digest) an epoch has had in any committed manifest of the chain.
    fn committed_positions(&mut self) -> R<Vec<(u64, Sha256)>> {
        let owner = self.owner.clone();
        let Some(chain) = read_chain(self.daemon()?.store_mut(), &owner)? else {
            return Ok(Vec::new());
        };
        let refs: Vec<&[u8]> = chain.manifests.iter().map(Vec::as_slice).collect();
        let mut out = Vec::new();
        for end in 1..=refs.len() {
            let prefix = refs.get(..end).unwrap_or_default();
            let folded = fold(prefix).map_err(e)?;
            for entry in folded.state.sources.values() {
                out.extend(entry.epochs.iter().map(|r| (r.size, r.sha256)));
            }
        }
        Ok(out)
    }
    fn check(&mut self, after: &Reference, settled: bool, shapes: &Shapes) -> R {
        if self.ran {
            match file_state(&self.tmp, StateFile::Status) {
                Loaded::Present(s) => {
                    serde_json::from_slice::<StatusBody>(&s.body).map_err(e)?;
                }
                Loaded::Missing(r) => return Err(format!("status unreadable: {r:?}")),
            }
        }
        if self.index_expected {
            ensure(
                matches!(file_state(&self.tmp, StateFile::Index), Loaded::Present(_)),
                "index unreadable",
            )?;
        }
        // The index never runs ahead of the store: whatever position it records on disk was
        // committed at some point (it may lag behind a newer commit, never lead one).
        if let Some(index) = index_of(&self.tmp)? {
            let committed = self.committed_positions()?;
            for state in index.sources.values().filter_map(|s| s.index.as_ref()) {
                let at = (state.position.captured_to.get(), state.position.whole);
                ensure(
                    at.0 == 0 || committed.contains(&at),
                    "index.json records a position that was never committed",
                )?;
            }
        }
        // No step here models a foreign writer, so an owner is never parked (A6).
        ensure(
            self.daemon()?.status().parked.is_empty(),
            "an owner was parked (false Foreign)",
        )?;
        let owner = self.owner.clone();
        let observed = observe_masked(
            self.daemon()?.store_mut(),
            &owner,
            &after.history,
            &after.masked,
        )?;
        if let Some(o) = &observed {
            if o.epochs > 1 || self.max_epochs > 1 {
                shapes.hit("multi-epoch");
            }
            self.max_epochs = self.max_epochs.max(o.epochs);
        }
        if settled && after.exists && !after.file.is_empty() {
            let o = need(observed, "no chain after settle")?;
            let latest = need(o.latest, "no epoch after settle")?;
            // Exact everywhere except masked positions (an unaudited mid-file rewrite).
            ensure(
                latest.len() == after.file.len()
                    && latest
                        .iter()
                        .zip(&after.file)
                        .enumerate()
                        .all(|(i, (b, x))| byte_ok(&after.history, &after.masked, i, *b, *x)),
                "latest epoch differs from the source bytes after settle",
            )?;
        } else if settled
            && !after.exists
            && let Some(o) = &observed
        {
            ensure(
                o.epochs == 0 || o.deleted,
                "removed source not recorded deleted after settle",
            )?;
        }
        Ok(())
    }
}

struct World {
    reference: Reference,
    sut: Sut,
    shapes: &'static Shapes,
}
impl World {
    fn new(shapes: &'static Shapes, model: EtagModel) -> R<Self> {
        Ok(Self {
            reference: Reference {
                content_etag: model == EtagModel::Content,
                ..Reference::default()
            },
            sut: Sut::new(model)?,
            shapes,
        })
    }
    fn run(&mut self, step: Step) {
        self.reference.apply(&step);
        let result = self
            .sut
            .exec(&step, &self.reference, self.shapes)
            .and_then(|()| {
                self.sut
                    .check(&self.reference, matches!(step, Step::Settle), self.shapes)
            });
        assert!(result.is_ok(), "{step:?}: {result:?}");
    }
}

fn model_of(reference: &Reference) -> EtagModel {
    EtagModel::from(reference.content_etag)
}

/// Run `steps` through the same model, system and oracle the properties use.
fn drive_script(steps: &[Step], shapes: &Shapes, model: EtagModel) -> R {
    let mut reference = Reference {
        content_etag: model == EtagModel::Content,
        ..Reference::default()
    };
    let mut sut = Sut::new(model)?;
    for step in steps {
        reference.apply(step);
        sut.exec(step, &reference, shapes)?;
        sut.check(&reference, matches!(step, Step::Settle), shapes)?;
    }
    Ok(())
}

/// One scripted run per ETag model that forces every guarded shape, `rewrite-mid-file` included
/// (a rewrite between the windows of bytes a commit already carries), so a property's coverage
/// guard cannot fail by chance: the shapes are forced here and the random runs add frequency.
fn scripted_shapes(shapes: &Shapes) -> R {
    let append = |seed| Step::Append { seed, n: 3 };
    let script = [
        Step::Bulk { seed: 1, kib: 80 },
        Step::Round,
        Step::Rewrite { zone: 2, delta: 3 },
        Step::Round,
        Step::Rewrite { zone: 0, delta: 1 },
        Step::Round,
        Step::Truncate { frac: 100 },
        Step::Round,
        Step::RenameReplace {
            change: false,
            delta: 0,
        },
        Step::Round,
        Step::RenameReplace {
            change: true,
            delta: 5,
        },
        Step::Round,
        Step::LostReply { dark: false },
        append(2),
        Step::Round,
        // The reply is lost and the store goes dark: the landing cannot be verified.
        Step::LostReply { dark: true },
        append(7),
        Step::Round,
        Step::Online,
        Step::Round,
        append(3),
        Step::Crash { manifest: true },
        append(4),
        Step::Crash { manifest: false },
        append(5),
        Step::RevokeToken,
        Step::Round,
        Step::RestoreToken,
        Step::LateWrite { seed: 6, n: 4 },
        Step::DeleteIndex,
        Step::Restart,
        Step::Settle,
        Step::RemoveSource,
        Step::Settle,
    ];
    for model in [EtagModel::PerWrite, EtagModel::Content] {
        drive_script(&script, shapes, model)?;
    }
    Ok(())
}

/// The script alone, with no random steps, produces every shape the guard requires.
#[test]
fn scripted_run_alone_produces_every_guarded_shape() -> R {
    let local = Shapes::new();
    scripted_shapes(&local)?;
    // Two models, one script: every shape is hit at least once per model.
    for (name, n) in local.counts() {
        assert!(n >= 2, "{name} hit {n} times over two models");
    }
    require_all(&local.counts())
}

/// State-aware generation: the guarded shapes need bytes on disk that a commit does not carry
/// yet (a crash, a lost reply, a revoked token, a late write), or bytes a commit already
/// carries (a mid-file rewrite, a removal, a rename, a second epoch). These helpers act on the
/// state the model is in, instead of drawing steps that usually have nothing to act on; they
/// never filter or discard a case.
impl World {
    fn has_bytes(&self) -> bool {
        self.reference.exists && !self.reference.file.is_empty()
    }
    fn draw_append(&mut self, tc: &TestCase) {
        let seed = tc.draw(gs::integers::<u8>());
        let n = tc.draw(gs::integers::<u8>().min_value(1).max_value(9));
        self.run(Step::Append { seed, n });
    }
    /// Bytes on disk (an append creates them if there are none).
    fn with_bytes(&mut self, tc: &TestCase) {
        if !self.has_bytes() {
            self.draw_append(tc);
        }
    }
    /// Bytes on disk that the next round would commit.
    fn with_pending(&mut self, tc: &TestCase) {
        if !self.has_bytes() || !self.reference.dirty {
            self.draw_append(tc);
        }
    }
    /// Bytes on disk that the newest commit carries in full (a round commits them).
    fn with_committed(&mut self, tc: &TestCase) {
        self.with_bytes(tc);
        let committed = self.sut.committed_len();
        assert!(committed.is_ok(), "{committed:?}");
        if committed.unwrap_or(0) < u64::try_from(self.reference.file.len()).unwrap_or(u64::MAX) {
            self.run(Step::Round);
        }
    }
    /// A committed file between the head and tail windows.
    fn with_committed_big(&mut self, tc: &TestCase) {
        if self.reference.file.len() <= HEAD + TAIL {
            let seed = tc.draw(gs::integers::<u8>());
            let kib = tc.draw(gs::integers::<u8>().min_value(69).max_value(90));
            self.run(Step::Bulk { seed, kib });
        }
        self.with_committed(tc);
    }
}

#[hegel::state_machine]
impl World {
    #[rule(weight = 3.0)]
    fn append(&mut self, tc: TestCase) {
        let seed = tc.draw(gs::integers::<u8>());
        let n = tc.draw(gs::integers::<u8>().min_value(1).max_value(9));
        self.run(Step::Append { seed, n });
    }
    #[rule(weight = 2.0)]
    fn bulk(&mut self, tc: TestCase) {
        let seed = tc.draw(gs::integers::<u8>());
        // Half the time large enough (> HEAD + TAIL) that a rewrite can fall between windows.
        let big = tc.draw(gs::booleans());
        let kib = if big {
            tc.draw(gs::integers::<u8>().min_value(69).max_value(90))
        } else {
            tc.draw(gs::integers::<u8>().min_value(1).max_value(8))
        };
        self.run(Step::Bulk { seed, kib });
    }
    #[rule(weight = 3.0)]
    fn rewrite(&mut self, tc: TestCase) {
        self.with_bytes(&tc);
        let zone = tc.draw(gs::integers::<u8>().min_value(0).max_value(2));
        let delta = tc.draw(gs::integers::<u16>());
        self.run(Step::Rewrite { zone, delta });
    }
    /// A rewrite between the windows of bytes a commit already carries.
    #[rule(weight = 3.0)]
    fn rewrite_mid_file(&mut self, tc: TestCase) {
        self.with_committed_big(&tc);
        let delta = tc.draw(gs::integers::<u16>());
        self.run(Step::Rewrite { zone: 2, delta });
    }
    #[rule]
    fn rename_replace(&mut self, tc: TestCase) {
        self.with_committed(&tc);
        let change = tc.draw(gs::booleans());
        let delta = tc.draw(gs::integers::<u16>());
        self.run(Step::RenameReplace { change, delta });
    }
    #[rule(weight = 2.0)]
    fn truncate(&mut self, tc: TestCase) {
        self.with_bytes(&tc);
        let frac = tc.draw(gs::integers::<u8>());
        self.run(Step::Truncate { frac });
    }
    /// A new epoch: a changed file replaces committed bytes, then a round commits it.
    #[rule(weight = 2.0)]
    fn replace_committed(&mut self, tc: TestCase) {
        self.with_committed(&tc);
        let delta = tc.draw(gs::integers::<u16>());
        self.run(Step::RenameReplace {
            change: true,
            delta,
        });
        self.run(Step::Round);
    }
    #[rule]
    fn remove_source(&mut self, tc: TestCase) {
        self.with_committed(&tc);
        self.run(Step::RemoveSource);
    }
    #[rule]
    fn revoke_token(&mut self, tc: TestCase) {
        self.with_pending(&tc);
        self.run(Step::RevokeToken);
        self.run(Step::Round);
    }
    #[rule]
    fn restore_token(&mut self, _: TestCase) {
        self.run(Step::RestoreToken);
    }
    #[rule(weight = 2.0)]
    fn crash(&mut self, tc: TestCase) {
        self.with_pending(&tc);
        let manifest = tc.draw(gs::booleans());
        self.run(Step::Crash { manifest });
    }
    #[rule(weight = 2.0)]
    fn late_write(&mut self, tc: TestCase) {
        self.with_pending(&tc);
        let seed = tc.draw(gs::integers::<u8>());
        let n = tc.draw(gs::integers::<u8>().min_value(1).max_value(9));
        self.run(Step::LateWrite { seed, n });
    }
    #[rule(weight = 4.0)]
    fn round(&mut self, _: TestCase) {
        self.run(Step::Round);
    }
    #[rule]
    fn delete_index(&mut self, _: TestCase) {
        self.run(Step::DeleteIndex);
    }
    #[rule]
    fn restart(&mut self, _: TestCase) {
        self.run(Step::Restart);
    }
    #[rule(weight = 2.0)]
    fn lost_reply(&mut self, tc: TestCase) {
        self.with_pending(&tc);
        let dark = tc.draw(gs::booleans());
        self.run(Step::LostReply { dark });
        self.run(Step::Round);
    }
    #[rule]
    fn offline(&mut self, _: TestCase) {
        self.run(Step::Offline);
    }
    #[rule]
    fn online(&mut self, _: TestCase) {
        self.run(Step::Online);
    }
    #[rule(weight = 2.0)]
    fn settle(&mut self, _: TestCase) {
        self.run(Step::Settle);
    }
}

#[test]
fn hegel_daemon_state_machine() {
    hegel::Hegel::new(|tc: TestCase| {
        let model = EtagModel::from(tc.draw(gs::booleans()));
        let world = World::new(&HEGEL_SHAPES, model);
        assert!(world.is_ok(), "{:?}", world.as_ref().err());
        if let Ok(world) = world {
            hegel::stateful::machine(world).steps(12).run(tc);
        }
        HEGEL_SHAPES.end_case("hegel");
    })
    .run();
    let guard = require_all(&HEGEL_SHAPES.counts());
    assert!(guard.is_ok(), "{guard:?} {:?}", HEGEL_SHAPES.counts());
}

/// The steps to draw from in `state`: the usual mix, plus extra weight on what acts on the
/// state (no filtering or assuming). The guarded shapes need bytes on disk that a commit does
/// not carry yet (a crash, a lost reply, a revoked token, a late write) or bytes it already
/// carries (a mid-file rewrite, a removal, a rename), and a mid-file rewrite needs a file between
/// the windows (more than `HEAD + TAIL` bytes).
fn step_strategy(state: &Reference) -> BoxedStrategy<Step> {
    let has = state.exists && !state.file.is_empty();
    let big = state.file.len() > HEAD + TAIL;
    let committed = has && state.synced && !state.dirty;
    let pending = has && state.dirty;
    let append = || {
        (any::<u8>(), 1..=9_u8)
            .prop_map(|(seed, n)| Step::Append { seed, n })
            .boxed()
    };
    let bulk_big = || {
        (any::<u8>(), 69..=90_u8)
            .prop_map(|(seed, kib)| Step::Bulk { seed, kib })
            .boxed()
    };
    let mut steps: Vec<(u32, BoxedStrategy<Step>)> = vec![
        (3, append()),
        (
            1,
            (any::<u8>(), 1..=90_u8)
                .prop_map(|(seed, kib)| Step::Bulk { seed, kib })
                .boxed(),
        ),
        (
            3,
            (0..=2_u8, any::<u16>())
                .prop_map(|(zone, delta)| Step::Rewrite { zone, delta })
                .boxed(),
        ),
        (
            1,
            (any::<bool>(), any::<u16>())
                .prop_map(|(change, delta)| Step::RenameReplace { change, delta })
                .boxed(),
        ),
        (
            2,
            any::<u8>().prop_map(|frac| Step::Truncate { frac }).boxed(),
        ),
        (1, Just(Step::RemoveSource).boxed()),
        (1, Just(Step::RevokeToken).boxed()),
        (1, Just(Step::RestoreToken).boxed()),
        (
            2,
            any::<bool>()
                .prop_map(|manifest| Step::Crash { manifest })
                .boxed(),
        ),
        (
            2,
            (any::<u8>(), 1..=9_u8)
                .prop_map(|(seed, n)| Step::LateWrite { seed, n })
                .boxed(),
        ),
        (4, Just(Step::Round).boxed()),
        (1, Just(Step::DeleteIndex).boxed()),
        (1, Just(Step::Restart).boxed()),
        (
            2,
            any::<bool>()
                .prop_map(|dark| Step::LostReply { dark })
                .boxed(),
        ),
        (1, Just(Step::Offline).boxed()),
        (1, Just(Step::Online).boxed()),
        (2, Just(Step::Settle).boxed()),
    ];
    if !has {
        steps.push((8, append()));
        steps.push((3, bulk_big()));
    }
    if has && !big {
        steps.push((5, bulk_big()));
    }
    if pending {
        // Bytes a commit does not carry yet: the shapes that need them, and a round.
        steps.push((5, Just(Step::Round).boxed()));
        steps.push((
            3,
            any::<bool>()
                .prop_map(|manifest| Step::Crash { manifest })
                .boxed(),
        ));
        steps.push((
            3,
            (any::<u8>(), 1..=9_u8)
                .prop_map(|(seed, n)| Step::LateWrite { seed, n })
                .boxed(),
        ));
        if !state.lost_armed {
            steps.push((
                3,
                any::<bool>()
                    .prop_map(|dark| Step::LostReply { dark })
                    .boxed(),
            ));
        }
        if !state.revoked {
            steps.push((3, Just(Step::RevokeToken).boxed()));
        }
    }
    if state.lost_armed || state.revoked {
        steps.push((6, Just(Step::Round).boxed()));
    }
    if committed {
        steps.push((
            3,
            (any::<bool>(), any::<u16>())
                .prop_map(|(change, delta)| Step::RenameReplace { change, delta })
                .boxed(),
        ));
        steps.push((2, Just(Step::RemoveSource).boxed()));
        steps.push((
            2,
            any::<u8>().prop_map(|frac| Step::Truncate { frac }).boxed(),
        ));
        if big {
            steps.push((
                10,
                any::<u16>()
                    .prop_map(|delta| Step::Rewrite { zone: 2, delta })
                    .boxed(),
            ));
        }
    }
    proptest::strategy::Union::new_weighted(steps).boxed()
}
struct RefMachine;
impl ReferenceStateMachine for RefMachine {
    type State = Reference;
    type Transition = Step;
    fn init_state() -> BoxedStrategy<Reference> {
        any::<bool>()
            .prop_map(|content_etag| Reference {
                content_etag,
                ..Reference::default()
            })
            .boxed()
    }
    fn transitions(state: &Reference) -> BoxedStrategy<Step> {
        step_strategy(state)
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
        let sut = Sut::new(model_of(reference));
        assert!(sut.is_ok(), "{:?}", sut.as_ref().err());
        PtSut { sut }
    }
    fn apply(mut state: PtSut, after: &Reference, step: Step) -> PtSut {
        if let Ok(sut) = state.sut.as_mut() {
            let result = sut
                .exec(&step, after, &PROPTEST_SHAPES)
                .and_then(|()| sut.check(after, matches!(step, Step::Settle), &PROPTEST_SHAPES));
            assert!(result.is_ok(), "{step:?}: {result:?}");
        }
        state
    }
    fn teardown(mut state: PtSut, after: Reference) {
        if let Ok(sut) = state.sut.as_mut() {
            let result = sut
                .exec(&Step::Settle, &after, &PROPTEST_SHAPES)
                .and_then(|()| sut.check(&after, true, &PROPTEST_SHAPES));
            assert!(result.is_ok(), "final settle: {result:?}");
        }
        PROPTEST_SHAPES.end_case("proptest");
    }
}
#[test]
fn proptest_daemon_state_machine() {
    let mut runner = TestRunner::new(Config {
        failure_persistence: None,
        ..Config::default()
    });
    let strategy = <RefMachine as ReferenceStateMachine>::sequential_strategy(1..13);
    let result = runner.run(&strategy, |(initial, transitions, seen)| {
        PtMachine::test_sequential(Config::default(), initial, transitions, seen);
        Ok(())
    });
    assert!(result.is_ok(), "{result:?}");
    let guard = require_all(&PROPTEST_SHAPES.counts());
    assert!(guard.is_ok(), "{guard:?} {:?}", PROPTEST_SHAPES.counts());
}

// ====================================================================== Coverage round
use memory::config::Config as MemConfig;
use memory::daemon::RoundError;
use memory::state::{MAX_BODY_BYTES, StateError};
use memory_core::domain::ParkReason;
use std::io::ErrorKind;
use std::sync::{Arc, Mutex};
use std::time::Duration;

type Shared = Arc<Mutex<Vec<(Owner, Source)>>>;

fn set_mode(path: &Path, mode: u32) -> R {
    fs::set_permissions(path, fs::Permissions::from_mode(mode)).map_err(e)
}
fn disk_status(tmp: &Tmp) -> R<StatusBody> {
    match file_state(tmp, StateFile::Status) {
        Loaded::Present(s) => serde_json::from_slice(&s.body).map_err(e),
        Loaded::Missing(r) => Err(format!("status missing: {r:?}")),
    }
}
fn boot_shared(tmp: &Tmp, cfg: &Path, fs: MemFs, list: &Shared) -> R<D> {
    let shared = Arc::clone(list);
    let sources = FSrcs::new(move || shared.lock().map(|g| g.clone()).unwrap_or_default());
    let parts = Parts {
        fs,
        store: FStore::default(),
        clock: TestClock::at(0),
        sources,
        context: PolicyContext {
            valid: true,
            revision: "r".to_owned(),
            rules: Some(rules_digest()),
            collector_paths: vec![],
            collector_identities: vec![],
        },
    };
    let mut d = Daemon::start(&tmp.root(), cfg, parts).map_err(|x| x.code().to_owned())?;
    d.set_sleep(|_| {});
    Ok(d)
}
fn hint_with(session: &str, launch: LaunchHint) -> R<Vec<u8>> {
    let record = HintRecord {
        schema: HintSchema::V1,
        agent: Agent::Claude,
        event: HintEvent::SessionStart,
        session: SessionId::try_from(session.to_owned()).map_err(e)?,
        cwd: PathHint::Absent,
        transcript: PathHint::Present {
            path: HintPath::try_from(format!("/r/projects/p/{session}.jsonl")).map_err(e)?,
        },
        agent_transcript: PathHint::Absent,
        launch,
        reduction: Reduction::None,
    };
    let mut line = serde_json::to_vec(&record).map_err(e)?;
    line.push(b'\n');
    Ok(line)
}
const NEWER: &[u8] = b"{\"schema\":\"memory.current/2\"}";
fn hint_offset(tmp: &Tmp, name: &str) -> R<u64> {
    Ok(index_of(tmp)?
        .and_then(|i| i.hints.get(name).copied())
        .unwrap_or(0))
}

#[test]
fn start_error_code_table() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(false)?;
    let error = failed(Daemon::start(
        &tmp.root(),
        &cfg,
        parts(MemFs::default(), MemStore::default()),
    ))?;
    assert_eq!((error.code(), error.exit_code()), ("config_parse", 2));

    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    fs::create_dir_all(tmp.root()).map_err(e)?;
    set_mode(&tmp.root(), 0o700)?;
    fs::write(tmp.root().join("tmp"), "x").map_err(e)?;
    let error = failed(Daemon::start(
        &tmp.root(),
        &cfg,
        parts(MemFs::default(), MemStore::default()),
    ))?;
    assert_eq!(
        (error.code(), error.exit_code()),
        ("state_not_directory", 1)
    );
    Ok(())
}

#[test]
fn window_defaults_fill_the_missing_half() -> R {
    let base = "archive_id = \"a\"\nmachine_id = \"m\"\nendpoint = \"http://127.0.0.1:9\"\nbucket = \"bucket-1\"\nprefix = \"\"\ntoken_path = \"/t\"\n";
    let q = MemConfig::parse(&format!("{base}window = {{ quiet = 10 }}\n")).map_err(e)?;
    assert_eq!(
        (q.window.quiet, q.window.cap),
        (Duration::from_secs(10), Duration::from_secs(300))
    );
    let c = MemConfig::parse(&format!("{base}window = {{ cap = 120 }}\n")).map_err(e)?;
    assert_eq!(
        (c.window.quiet, c.window.cap),
        (Duration::from_secs(60), Duration::from_secs(120))
    );
    Ok(())
}

#[test]
fn state_api_failure_table() -> R {
    // Root cannot be created under an unwritable parent.
    let tmp = Tmp::new()?;
    let parent = tmp.0.join("ro");
    fs::create_dir(&parent).map_err(e)?;
    set_mode(&parent, 0o500)?;
    let got = DaemonLock::acquire(&parent.join("state")).err();
    set_mode(&parent, 0o700)?;
    assert_eq!(got, Some(StateError::Io(ErrorKind::PermissionDenied)));

    // An unreadable tmp/ makes prepare fail with Io.
    let tmp = Tmp::new()?;
    let lock = DaemonLock::acquire(&tmp.root()).map_err(e)?;
    fs::create_dir(tmp.root().join("tmp")).map_err(e)?;
    set_mode(&tmp.root().join("tmp"), 0o000)?;
    let got = StateDir::prepare(&lock).err();
    set_mode(&tmp.root().join("tmp"), 0o700)?;
    assert_eq!(got, Some(StateError::Io(ErrorKind::PermissionDenied)));

    // Oversized body and unwritable root.
    let state = StateDir::prepare(&lock).map_err(e)?;
    let big = vec![0u8; MAX_BODY_BYTES + 1];
    assert_eq!(
        state.write(StateFile::Index, &big).err(),
        Some(StateError::TooLarge)
    );
    set_mode(&tmp.root(), 0o500)?;
    let id = MachineId::try_from("m".to_owned()).map_err(e)?;
    let got = state.create_machine_id(&id).err();
    set_mode(&tmp.root(), 0o700)?;
    assert_eq!(got, Some(StateError::Io(ErrorKind::PermissionDenied)));
    Ok(())
}

#[test]
fn unwritable_state_dir_is_a_round_error_on_first_status_write() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    set_mode(&tmp.root(), 0o500)?;
    let result = d.reconcile_once();
    set_mode(&tmp.root(), 0o700)?;
    assert!(matches!(
        result,
        Err(RoundError::State(StateError::Io(
            ErrorKind::PermissionDenied
        )))
    ));
    Ok(())
}

#[test]
fn unwritable_index_is_a_round_error_when_status_is_not_due() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let shared: Shared = Arc::new(Mutex::new(Vec::new()));
    let mut d = boot_shared(&tmp, &cfg, files_with(b"hello\n"), &shared)?;
    d.reconcile_once().map_err(e)?;
    assert!(!tmp.root().join("index.json").exists());
    shared.lock().map_err(e)?.push(src_of("s1", 1)?);
    set_mode(&tmp.root(), 0o500)?;
    d.clock_mut().mono = 30_000_000_000;
    let result = d.reconcile_once();
    set_mode(&tmp.root(), 0o700)?;
    assert!(matches!(
        result,
        Err(RoundError::State(StateError::Io(
            ErrorKind::PermissionDenied
        )))
    ));
    Ok(())
}

#[test]
fn status_inside_interval_is_deferred_then_flushed_at_shutdown() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    d.reconcile_once().map_err(e)?;
    assert_eq!(disk_status(&tmp)?.rounds, 1);
    let on_disk = fs::read(tmp.root().join("status.json")).map_err(e)?;
    d.clock_mut().mono = 30_000_000_000;
    d.reconcile_once().map_err(e)?;
    assert_eq!(d.status().rounds, 2);
    assert_eq!(
        fs::read(tmp.root().join("status.json")).map_err(e)?,
        on_disk
    );
    d.clock_mut().mono = 60_000_000_000;
    d.reconcile_once().map_err(e)?;
    assert_eq!(disk_status(&tmp)?.rounds, 3);
    d.clock_mut().mono = 61_000_000_000;
    d.reconcile_once().map_err(e)?;
    assert_eq!((d.status().rounds, disk_status(&tmp)?.rounds), (4, 3));
    d.shutdown().map_err(e)?;
    assert_eq!(status_via_state_dir(&tmp)?.rounds, 4);
    Ok(())
}

#[test]
fn authoritative_park_is_written_inside_the_interval() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let shared: Shared = Arc::new(Mutex::new(vec![src_of("s1", 1)?]));
    let mut fs = files_with(b"hello\n");
    fs.files.insert(2, b"world\n".to_vec());
    let mut d = boot_shared(&tmp, &cfg, fs, &shared)?;
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 1);
    assert_eq!(disk_status(&tmp)?.rounds, 1);
    shared.lock().map_err(e)?.push(src_of("s2", 2)?);
    let o2 = owner_of("s2")?;
    d.store_mut().store.write(&current_key(&o2), NEWER.to_vec());
    d.clock_mut().mono = 10_000_000_000;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.parked, report.landed), (1, 0));
    let disk = disk_status(&tmp)?;
    assert_eq!(disk.rounds, 2, "park written at once");
    assert_eq!(
        disk.parked,
        vec![memory::daemon::ParkedEntry {
            owner: o2,
            reason: ParkedReason::Owner(OwnerParkReason::NewerRemoteSchema),
            source: None,
        }]
    );
    d.clock_mut().mono = 20_000_000_000;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!(report.skipped, 1);
    assert_eq!((d.status().rounds, disk_status(&tmp)?.rounds), (3, 2));
    Ok(())
}

#[test]
fn parked_owners_are_listed_sorted() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    // Eight owners, sources listed in reverse: with two, an unsorted HashMap order passes half
    // of the runs; with eight it passes one in 40,320.
    let names: Vec<String> = (1..=8).map(|i| format!("s{i}")).collect();
    let mut list = Vec::new();
    let mut fs = MemFs::default();
    for (i, name) in names.iter().enumerate().rev() {
        let handle = u64::try_from(i + 1).map_err(e)?;
        list.push(src_of(name, handle)?);
        fs.files
            .insert(handle, format!("data {name}\n").into_bytes());
    }
    let mut d = boot(&tmp, &cfg, fs, FStore::default(), &list, WALL)?;
    for name in &names {
        d.store_mut()
            .store
            .write(&current_key(&owner_of(name)?), NEWER.to_vec());
    }
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.parked, report.landed), (8, 0));
    let owners: Vec<Owner> = d.status().parked.iter().map(|p| p.owner.clone()).collect();
    let expected: Vec<Owner> = names.iter().map(|n| owner_of(n)).collect::<R<_>>()?;
    assert_eq!(owners, expected);
    assert!(
        d.status()
            .parked
            .iter()
            .all(|p| p.reason == ParkedReason::Owner(OwnerParkReason::NewerRemoteSchema))
    );
    Ok(())
}

#[test]
fn corrupt_remote_parks_the_source_after_three_hours() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let o = owner_of("s1")?;
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    d.store_mut()
        .store
        .write(&current_key(&o), b"garbage".to_vec());
    let hour = 3_600_000_000_000;
    let mut seen = Vec::new();
    for at in [0, hour, 3 * hour] {
        d.clock_mut().mono = at;
        let r = d.reconcile_once().map_err(e)?;
        seen.push((r.failed, r.parked));
    }
    assert_eq!(seen, vec![(1, 0), (1, 0), (0, 1)]);
    assert_eq!(
        d.status().parked.first().map(|p| p.reason.clone()),
        Some(ParkedReason::Source(ParkReason::RepeatedSchemaError))
    );
    d.clock_mut().mono = 3 * hour + 1;
    assert_eq!(d.reconcile_once().map_err(e)?.skipped, 1);
    assert!(
        d.store_mut().put_log.is_empty(),
        "nothing was ever uploaded"
    );
    Ok(())
}

#[test]
fn two_sources_of_one_owner_commit_once() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let first = src_of("s1", 1)?;
    let (o, mut second) = src_of("s1", 2)?;
    second.path = "projects/p/s1b.jsonl".to_owned();
    second.facts.identities = vec![second.path.clone()];
    let mut fs = files_with(b"one\n");
    fs.files.insert(2, b"two\n".to_vec());
    let list = vec![first, (o, second)];
    let mut d = boot(&tmp, &cfg, fs, FStore::default(), &list, WALL)?;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.landed, report.failed, report.parked), (1, 0, 0));
    Ok(())
}

/// Store that drops `current.json` writes (first or fenced) without landing them and reports an
/// ambiguous error.
#[derive(Default)]
struct Ghost {
    inner: MemStore,
    drop_current: bool,
}
impl Store for Ghost {
    fn put_if_absent(&mut self, key: &str, body: Published) -> Result<ETag, StoreError> {
        if self.drop_current && key.ends_with("current.json") {
            return Err(StoreError::Ambiguous(Cause::Reset));
        }
        self.inner.put_if_absent(key, body)
    }
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &ETag,
        body: Published,
    ) -> Result<ETag, StoreError> {
        if self.drop_current && key.ends_with("current.json") {
            return Err(StoreError::Ambiguous(Cause::Reset));
        }
        self.inner.put_if_match(key, etag, body)
    }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        self.inner.get(key)
    }
}

#[test]
fn unlanded_first_write_resets_the_base_and_converges() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let one = source().ok();
    let sources: Sources = Box::new(move || one.clone().into_iter().collect());
    let parts = Parts {
        fs: files_with(b"hello\n"),
        store: Ghost {
            inner: MemStore::default(),
            drop_current: true,
        },
        clock: TestClock::at(0),
        sources,
        context: PolicyContext {
            valid: true,
            revision: "r".to_owned(),
            rules: Some(rules_digest()),
            collector_paths: vec![],
            collector_identities: vec![],
        },
    };
    let mut d = Daemon::start(&tmp.root(), &cfg, parts).map_err(|x| x.code().to_owned())?;
    d.set_sleep(|_| {});
    let r = d.reconcile_once().map_err(e)?;
    assert_eq!((r.landed, r.failed), (0, 1));
    d.store_mut().drop_current = false;
    d.clock_mut().mono = 61_000_000_000;
    let r = d.reconcile_once().map_err(e)?;
    assert_eq!((r.landed, r.failed), (1, 0));
    assert_eq!(d.root(), tmp.root());
    // A later write that does not land either (the base is now `Some`): it fails, and a later
    // round lands it with the bytes of the file as it is by then.
    d.fs_mut().files.insert(1, b"hello\nworld\n".to_vec());
    d.store_mut().drop_current = true;
    d.clock_mut().mono = 122_000_000_000;
    let r = d.reconcile_once().map_err(e)?;
    assert_eq!((r.landed, r.failed), (0, 1));
    d.store_mut().drop_current = false;
    d.clock_mut().mono = 183_000_000_000;
    let r = d.reconcile_once().map_err(e)?;
    assert_eq!((r.landed, r.failed), (1, 0));
    let stored = d
        .store_mut()
        .get(&current_key(&owner()?))
        .map_err(|x| format!("{x:?}"))?;
    assert!(!stored.body.is_empty());
    Ok(())
}

#[test]
fn hint_partial_progress_commits_prefix_and_torn_tail_waits() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let store = FStore {
        offline: true,
        ..FStore::default()
    };
    let mut d = boot(&tmp, &cfg, files_with(b"hello\n"), store, &list, WALL)?;
    let name = "2026-11-09.jsonl";
    let junk = b"not json\n".to_vec();
    let a = hint_with("s9", LaunchHint::None)?;
    let b = hint_line("s1", &["ANTHROPIC_MODEL=claude-x"])?;
    let mut bytes = junk.clone();
    bytes.extend(&a);
    bytes.extend(&b);
    bytes.extend(b"{\"torn");
    fs::write(tmp.root().join("hints").join(name), &bytes).map_err(e)?;
    let r = d.reconcile_once().map_err(e)?;
    assert_eq!((r.landed, r.failed), (0, 1));
    assert_eq!(hint_offset(&tmp, name)?, len64(&junk) + len64(&a));
    d.store_mut().offline = false;
    d.clock_mut().mono = 61_000_000_000;
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 1);
    assert_eq!(
        hint_offset(&tmp, name)?,
        len64(&junk) + len64(&a) + len64(&b),
        "the torn tail is never consumed"
    );
    Ok(())
}

#[test]
fn replaced_hint_file_restarts_and_unreadable_entries_are_skipped() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    let hints = tmp.root().join("hints");
    fs::create_dir(hints.join("2026-11-08.jsonl")).map_err(e)?;
    fs::write(hints.join("notes.txt"), "x").map_err(e)?;
    let name = "2026-11-09.jsonl";
    let a = hint_with("s9", LaunchHint::None)?;
    let b = hint_with("s8", LaunchHint::None)?;
    let mut both = a.clone();
    both.extend(&b);
    fs::write(hints.join(name), &both).map_err(e)?;
    d.reconcile_once().map_err(e)?;
    assert_eq!(hint_offset(&tmp, name)?, len64(&a) + len64(&b));
    assert_eq!(hint_offset(&tmp, "2026-11-08.jsonl")?, 0);
    fs::write(hints.join(name), &a).map_err(e)?;
    d.clock_mut().mono = 61_000_000_000;
    d.reconcile_once().map_err(e)?;
    assert_eq!(
        hint_offset(&tmp, name)?,
        len64(&a),
        "offset past the end restarts"
    );
    Ok(())
}

#[test]
fn missing_hints_dir_is_an_empty_scan() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    fs::remove_dir(tmp.root().join("hints")).map_err(e)?;
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 1);
    Ok(())
}

#[test]
fn shutdown_table() -> R {
    // No round: no status file, the root is reported.
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    assert_eq!(d.root(), tmp.root());
    d.shutdown().map_err(e)?;
    assert!(!tmp.root().join("status.json").exists());
    assert!(!tmp.root().join("index.json").exists(), "nothing was dirty");
    // After a round, an unwritable root fails the flush.
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    d.reconcile_once().map_err(e)?;
    set_mode(&tmp.root(), 0o500)?;
    let got = d.shutdown();
    set_mode(&tmp.root(), 0o700)?;
    assert_eq!(got, Err(StateError::Io(ErrorKind::PermissionDenied)));
    Ok(())
}

#[test]
fn prepare_sweeps_tmp_entries_and_stale_root_temp_files() -> R {
    let tmp = Tmp::new()?;
    let lock = DaemonLock::acquire(&tmp.root()).map_err(e)?;
    let t = tmp.root().join("tmp");
    fs::create_dir_all(t.join("nested/deeper")).map_err(e)?;
    fs::write(t.join("nested/deeper/x"), "x").map_err(e)?;
    fs::write(t.join("leftover"), "x").map_err(e)?;
    let stale = tmp.root().join(".index.json.tmp-1-1");
    let keep = tmp.root().join("keep.json");
    let dotted = tmp.root().join(".hidden");
    for p in [&stale, &keep, &dotted] {
        fs::write(p, "x").map_err(e)?;
    }
    StateDir::prepare(&lock).map_err(e)?;
    let left: Vec<_> = fs::read_dir(&t).map_err(e)?.collect();
    assert!(left.is_empty(), "tmp/ is emptied");
    assert!(!stale.exists());
    assert!(keep.exists() && dotted.exists(), "only stale temp files go");
    Ok(())
}

#[test]
fn retry_delay_defers_the_owner_until_the_clock_passes_it() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let store = FStore {
        offline: true,
        ..FStore::default()
    };
    let mut d = boot(&tmp, &cfg, files_with(b"hello\n"), store, &list, WALL)?;
    let r = d.reconcile_once().map_err(e)?;
    assert_eq!((r.landed, r.failed, r.deferred), (0, 1, 0));
    assert_eq!(r.failures.offline, 1);
    assert_eq!(r.failures.throttled, 0);
    d.store_mut().offline = false;
    let puts = d.store_mut().put_log.len();
    let r = d.reconcile_once().map_err(e)?;
    assert_eq!((r.landed, r.failed, r.deferred), (0, 0, 1));
    assert_eq!(
        d.store_mut().put_log.len(),
        puts,
        "no store call while deferred"
    );
    d.clock_mut().mono = 61_000_000_000;
    let r = d.reconcile_once().map_err(e)?;
    assert_eq!((r.landed, r.failed, r.deferred), (1, 0, 0));
    assert_eq!(d.status().failures.offline, 1);
    Ok(())
}

// ---------------------------------------------------------------- surviving mutants (round 3)

/// The round's report and the store's refusal counter after one reconcile at `mono_secs`.
fn round_at(d: &mut D, mono_secs: u64) -> R<(memory::daemon::RoundReport, u64)> {
    d.clock_mut().mono = mono_secs * 1_000_000_000;
    let report = d.reconcile_once().map_err(e)?;
    let refused = d.store_mut().refused_current;
    Ok((report, refused))
}

fn latest_bytes(d: &mut D, session: &str, history: &[&[u8]]) -> R<Vec<u8>> {
    let history: Vec<Vec<u8>> = history.iter().map(|h| h.to_vec()).collect();
    let observed = observe(d.store_mut(), &owner_of(session)?, &history)?;
    need(need(observed, "no chain")?.latest, "no epoch")
}

/// A landing that cannot be verified (the write landed, the reply was lost, the store went dark)
/// resets the base: the next round recovers it from the store and finds nothing left to write.
/// Kills D2 (a stale base kept after `LandingUnknown`): the stale round would capture again and
/// have its fenced write refused (412) or re-upload.
#[test]
fn an_unverifiable_landing_resets_the_base_so_the_next_round_writes_nothing() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    assert_eq!(round_at(&mut d, 0)?.0.landed, 1);
    bytes_in(&mut d, 1, b"hello\nworld\n");
    d.store_mut().arm(
        None,
        Effect::LandThenDark(StoreError::Ambiguous(Cause::Reset)),
    );
    let (report, _) = round_at(&mut d, 61)?;
    assert_eq!((report.landed, report.failed), (0, 1));
    assert_eq!(report.failures.offline, 1);
    d.store_mut().blackout = false;
    let puts = d.store_mut().put_log.len();
    let (report, refused) = round_at(&mut d, 122)?;
    assert_eq!((report.landed, report.failed, report.deferred), (0, 0, 0));
    assert_eq!(refused, 0, "no write was fenced on a stale base");
    assert_eq!(d.store_mut().put_log.len(), puts, "nothing left to write");
    assert_eq!(
        latest_bytes(&mut d, "s1", &[b"hello\n", b"hello\nworld\n"])?,
        b"hello\nworld\n"
    );
    Ok(())
}

/// `BaseUnchanged` through every rebase retry is `Transient` (DESIGN.md 11.2): counted apart from
/// `throttled`, and it resets the base (the ETag it held is stale). Kills D13 (`Transient`
/// counted as `throttled`) and D2 (stale base kept after `BaseUnchanged`).
#[test]
fn rebase_exhaustion_counts_transient_not_throttled_and_resets_the_base() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::with_model(EtagModel::PerWrite),
        &list,
        WALL,
    )?;
    assert_eq!(round_at(&mut d, 0)?.0.landed, 1);
    bytes_in(&mut d, 1, b"hello\nworld\n");
    // One first attempt and four retries, each refused because the ETag moved under it.
    for _ in 0..5 {
        d.store_mut()
            .arm(Some(current_key(&owner_of("s1")?)), Effect::Churn);
    }
    let (report, refused) = round_at(&mut d, 61)?;
    assert_eq!((report.landed, report.failed), (0, 1));
    assert_eq!(refused, 5);
    assert_eq!(report.failures.transient, 1);
    assert_eq!(report.failures.throttled, 0);
    assert_eq!(d.status().failures.transient, 1);
    assert_eq!(d.status().failures.throttled, 0);
    let (report, refused) = round_at(&mut d, 122)?;
    assert_eq!((report.landed, report.failed), (1, 0));
    assert_eq!(
        refused, 5,
        "the next round fenced on a fresh ETag: no new 412"
    );
    assert_eq!(
        latest_bytes(&mut d, "s1", &[b"hello\n", b"hello\nworld\n"])?,
        b"hello\nworld\n"
    );
    Ok(())
}

/// A6: a follow-up commit (the late write of an earlier capture was adopted) carries none of its
/// own capture, so it applies no local updates. Kills D8: the index would claim the 7 bytes that
/// never landed.
#[test]
fn a_follow_up_applies_no_local_updates_for_a_manifest_that_never_landed() -> R {
    let model = EtagModel::PerWrite;
    let mut reference = Reference::default();
    let mut sut = Sut::new(model)?;
    let shapes = Shapes::new();
    // Attempt 1 commits the 3 bytes held now and is delayed; 4 more arrive; attempt 2 meets the
    // late write of attempt 1 and adopts it.
    for step in [
        Step::Append { seed: 1, n: 3 },
        Step::LateWrite { seed: 2, n: 4 },
    ] {
        reference.apply(&step);
        sut.exec(&step, &reference, &shapes)?;
        sut.check(&reference, false, &shapes)?;
    }
    assert_eq!(sut.daemon()?.store_mut().late_landed, 1);
    let claimed: Vec<u64> = index_of(&sut.tmp)?
        .map(|i| {
            i.sources
                .values()
                .filter_map(|s| s.index.as_ref().map(|x| x.position.captured_to.get()))
                .collect()
        })
        .unwrap_or_default();
    assert!(
        claimed.iter().all(|c| *c <= 3),
        "the index claims bytes that never landed: {claimed:?}"
    );
    // The next rounds capture the rest, and only then does the index move to 7.
    for _ in 0..2 {
        sut.round(&shapes)?;
    }
    let owner = sut.owner.clone();
    let observed = need(
        observe(sut.daemon()?.store_mut(), &owner, &reference.history)?,
        "chain",
    )?;
    assert_eq!(observed.latest.as_ref().map(Vec::len), Some(7));
    Ok(())
}

fn captured_to_on_disk(tmp: &Tmp) -> R<Option<u64>> {
    Ok(index_of(tmp)?.and_then(|i| {
        i.sources
            .values()
            .find_map(|s| s.index.as_ref().map(|x| x.position.captured_to.get()))
    }))
}

/// DESIGN.md 9.2: the index is flushed at shutdown even when it is dirty inside the 60 s
/// interval. Kills D18 (shutdown skipping the index flush).
#[test]
fn shutdown_flushes_an_index_that_is_dirty_inside_the_interval() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    assert_eq!(round_at(&mut d, 0)?.0.landed, 1);
    assert_eq!(
        captured_to_on_disk(&tmp)?,
        Some(6),
        "first write is due at once"
    );
    d.fs_mut().set(1, b"hello\nworld\n");
    assert_eq!(round_at(&mut d, 30)?.0.landed, 1);
    assert_eq!(
        captured_to_on_disk(&tmp)?,
        Some(6),
        "inside the interval the index is not rewritten"
    );
    d.shutdown().map_err(e)?;
    assert_eq!(captured_to_on_disk(&tmp)?, Some(12), "shutdown flushed it");
    Ok(())
}

/// Found by `hegel_daemon_state_machine` at a random seed: after twelve failed rounds while
/// offline the owner's retry delay is longer than four rounds, so a settle that did not wait
/// it out saw no commit. The daemon retries within the 15 min cap of DESIGN.md 11.2.
#[test]
fn settle_converges_after_a_long_offline_run_once_the_retry_delay_passes() -> R {
    for model in [EtagModel::PerWrite, EtagModel::Content] {
        let shapes: &'static Shapes = Box::leak(Box::new(Shapes::new()));
        let mut world = World::new(shapes, model)?;
        world.run(Step::Offline);
        for _ in 0..6 {
            world.run(Step::LateWrite { seed: 0, n: 1 });
        }
        world.run(Step::Settle);
    }
    Ok(())
}

/// Found by `proptest_daemon_state_machine` at random seeds: the bytes after the stall equal
/// what is already committed, so the round after it has nothing to write and the held write
/// stays in flight. It lands ahead of a later round's write, which is refused, adopted (A6) and
/// followed by a normal commit.
#[test]
fn a_held_write_that_lands_steps_later_is_adopted_and_the_store_converges() -> R {
    for model in [EtagModel::PerWrite, EtagModel::Content] {
        let shapes: &'static Shapes = Box::leak(Box::new(Shapes::new()));
        let mut world = World::new(shapes, model)?;
        for step in [
            Step::Append { seed: 108, n: 4 },
            Step::Round,
            Step::Truncate { frac: 64 },
            // Regrows to the committed bytes: the second round of this step is `Unchanged`.
            Step::LateWrite { seed: 67, n: 3 },
            Step::Append { seed: 0, n: 1 },
            Step::Settle,
        ] {
            world.run(step);
        }
        let store = world.sut.daemon()?.store_mut();
        assert_eq!(store.late_landed, 1);
        assert_eq!(store.refused_current, 1);
    }
    Ok(())
}

// ------------------------------------------------------------------ hint retention

const OLD8: &str = "2026-10-29.jsonl";
const OLD7: &str = "2026-10-30.jsonl";

fn hint_path(tmp: &Tmp, name: &str) -> PathBuf {
    tmp.root().join("hints").join(name)
}
fn hint_boot(tmp: &Tmp, store: FStore) -> R<D> {
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    boot(tmp, &cfg, files_with(b"hello\n"), store, &list, WALL)
}
fn index_has(tmp: &Tmp, name: &str) -> R<bool> {
    Ok(index_of(tmp)?.is_some_and(|i| i.hints.contains_key(name)))
}

#[test]
fn old_consumed_hint_file_is_removed_and_stays_gone() -> R {
    let tmp = Tmp::new()?;
    let mut d = hint_boot(&tmp, FStore::default())?;
    fs::write(hint_path(&tmp, OLD8), hint_line("s1", &["PATH=/usr/bin"])?).map_err(e)?;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.landed, report.hints_removed), (1, 1), "{report:?}");
    assert_eq!(report.hints_kept_old, 0);
    assert!(!hint_path(&tmp, OLD8).exists());
    assert!(!index_has(&tmp, OLD8)?, "entry gone from the written index");
    d.shutdown().map_err(e)?;
    let mut d = hint_boot(&tmp, FStore::default())?;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!(report.hints_removed, 0);
    assert!(!hint_path(&tmp, OLD8).exists());
    assert!(!index_has(&tmp, OLD8)?);
    Ok(())
}

#[test]
fn seven_day_yesterday_and_today_files_are_kept() -> R {
    let tmp = Tmp::new()?;
    let mut d = hint_boot(&tmp, FStore::default())?;
    let names = [OLD7, "2026-11-05.jsonl", "2026-11-06.jsonl"];
    for name in names {
        fs::write(hint_path(&tmp, name), hint_line("s1", &["PATH=/usr/bin"])?).map_err(e)?;
    }
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.hints_removed, report.hints_kept_old), (0, 0));
    for name in names {
        assert!(hint_path(&tmp, name).exists(), "{name}");
    }
    Ok(())
}

#[test]
fn old_file_with_uncommitted_owner_is_kept_and_counted() -> R {
    let tmp = Tmp::new()?;
    let mut d = hint_boot(&tmp, FStore::default())?;
    // Session s2 has no source, so its record can never commit.
    fs::write(hint_path(&tmp, OLD8), hint_line("s2", &["PATH=/usr/bin"])?).map_err(e)?;
    for round in 1..=2 {
        let report = d.reconcile_once().map_err(e)?;
        assert_eq!(
            (report.hints_removed, report.hints_kept_old),
            (0, 1),
            "{round}"
        );
    }
    assert!(hint_path(&tmp, OLD8).exists());
    Ok(())
}

#[test]
fn old_file_with_torn_tail_is_kept() -> R {
    let tmp = Tmp::new()?;
    let mut d = hint_boot(&tmp, FStore::default())?;
    let mut bytes = hint_line("s1", &["PATH=/usr/bin"])?;
    bytes.extend_from_slice(b"{\"schema\":\"memory.hint/1\",\"ag");
    fs::write(hint_path(&tmp, OLD8), &bytes).map_err(e)?;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.hints_removed, report.hints_kept_old), (0, 1));
    assert!(hint_path(&tmp, OLD8).exists());
    Ok(())
}

#[test]
fn failed_hint_removal_keeps_the_entry_and_is_retried() -> R {
    let tmp = Tmp::new()?;
    let mut d = hint_boot(&tmp, FStore::default())?;
    fs::write(hint_path(&tmp, OLD8), hint_line("s1", &["PATH=/usr/bin"])?).map_err(e)?;
    let dir = tmp.root().join("hints");
    set_mode(&dir, 0o500)?;
    let first = d.reconcile_once();
    set_mode(&dir, 0o700)?;
    let report = first.map_err(e)?;
    assert_eq!((report.hints_removed, report.hints_kept_old), (0, 1));
    assert!(hint_path(&tmp, OLD8).exists());
    d.clock_mut().mono = 61_000_000_000;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!(report.hints_removed, 1);
    assert!(!hint_path(&tmp, OLD8).exists());
    assert!(!index_has(&tmp, OLD8)?);
    Ok(())
}

#[test]
fn stale_hint_offset_entry_is_dropped() -> R {
    let tmp = Tmp::new()?;
    let mut d = hint_boot(&tmp, FStore::default())?;
    let name = "2026-11-06.jsonl";
    fs::write(hint_path(&tmp, name), hint_line("s1", &["PATH=/usr/bin"])?).map_err(e)?;
    d.reconcile_once().map_err(e)?;
    d.shutdown().map_err(e)?;
    assert!(index_has(&tmp, name)?);
    fs::remove_file(hint_path(&tmp, name)).map_err(e)?;
    let mut d = hint_boot(&tmp, FStore::default())?;
    d.reconcile_once().map_err(e)?;
    d.shutdown().map_err(e)?;
    assert!(!index_has(&tmp, name)?);
    Ok(())
}

#[test]
fn hint_record_in_the_chain_stays_pending_after_index_loss() -> R {
    let tmp = Tmp::new()?;
    let name = "2026-11-06.jsonl";
    let mut d = hint_boot(&tmp, FStore::default())?;
    fs::write(hint_path(&tmp, name), hint_line("s1", &["PATH=/usr/bin"])?).map_err(e)?;
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 1);
    let store = std::mem::take(&mut d.store_mut().store);
    d.shutdown().map_err(e)?;
    fs::remove_file(tmp.root().join("index.json")).map_err(e)?;
    let fstore = FStore {
        store,
        ..FStore::default()
    };
    let mut d = hint_boot(&tmp, fstore)?;
    d.reconcile_once().map_err(e)?;
    d.clock_mut().wall = WALL + 9 * 86_400 * 1_000_000_000;
    d.clock_mut().mono = 61_000_000_000;
    let report = d.reconcile_once().map_err(e)?;
    // Finding (DESIGN 9.3 step 4): the chain carries this record's digest, but the recovered
    // base (`OwnerState`) holds no launch digests and the owner has no commit, so the record
    // is never marked done and the old file is kept. Closing it needs commit.rs/manifest code.
    assert_eq!((report.landed, report.hints_removed), (0, 0), "{report:?}");
    assert_eq!(report.hints_kept_old, 1);
    assert!(hint_path(&tmp, name).exists());
    Ok(())
}

/// The daemon reads at most 64 MiB of a day file (`MAX_HINT_FILE`).
const HINT_BOUND: usize = 64 << 20;

/// `lines` junk lines of exactly 1024 bytes each (newline included): cheap to scan, no record.
fn junk_lines(lines: usize) -> Vec<u8> {
    let mut line = vec![b'x'; 1023];
    line.push(b'\n');
    line.repeat(lines)
}

#[test]
fn old_hint_file_longer_than_the_read_bound_is_kept() -> R {
    let tmp = Tmp::new()?;
    let mut d = hint_boot(&tmp, FStore::default())?;
    // The first 64 MiB end exactly on a line boundary, so the scan of the bytes read is
    // fully consumed with no torn tail; only the cut read keeps the file.
    let lines = HINT_BOUND / 1024 + 1;
    let bytes = junk_lines(lines);
    let full = u64::try_from(bytes.len()).map_err(e)?;
    fs::write(hint_path(&tmp, OLD8), &bytes).map_err(e)?;
    drop(bytes);
    for round in 1..=3 {
        if round > 1 {
            d.clock_mut().mono = 61_000_000_000 * u64::try_from(round).map_err(e)?;
        }
        let report = d.reconcile_once().map_err(e)?;
        assert_eq!(
            (report.hints_removed, report.hints_kept_old),
            (0, 1),
            "{round}: {report:?}"
        );
        let len = fs::metadata(hint_path(&tmp, OLD8)).map_err(e)?.len();
        assert_eq!(len, full, "{round}: file untouched at its full length");
    }
    Ok(())
}

#[test]
fn old_hint_file_within_the_read_bound_and_consumed_is_removed() -> R {
    let tmp = Tmp::new()?;
    let mut d = hint_boot(&tmp, FStore::default())?;
    fs::write(hint_path(&tmp, OLD8), junk_lines(HINT_BOUND / 1024)).map_err(e)?;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!(
        (report.hints_removed, report.hints_kept_old),
        (1, 0),
        "{report:?}"
    );
    assert!(!hint_path(&tmp, OLD8).exists());
    Ok(())
}

#[test]
fn a_swept_hint_file_does_not_return_through_an_owner_capture() -> R {
    let tmp = Tmp::new()?;
    let mut d = hint_boot(&tmp, FStore::default())?;
    fs::write(hint_path(&tmp, OLD8), hint_line("s1", &["PATH=/usr/bin"])?).map_err(e)?;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!(report.hints_removed, 1, "{report:?}");
    assert!(!hint_path(&tmp, OLD8).exists());
    bytes_in(&mut d, 1, b"hello\nmore\n");
    let (out, _) = cap(&mut d, &owner_of("s1")?)?;
    assert_eq!(out, OwnerOutcome::Landed);
    d.shutdown().map_err(e)?;
    assert!(!index_has(&tmp, OLD8)?, "removed file back in the index");
    Ok(())
}

#[test]
fn owner_capture_refreshes_the_status_stamp_and_early_returns_do_not() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let o = owner_of("s1")?;
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"x\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    d.reconcile_once().map_err(e)?;
    let t1 = d.status().updated_at.clone();
    assert_eq!(disk_status(&tmp)?.updated_at, t1);
    d.clock_mut().wall = WALL + 3_600_000_000_000;
    d.clock_mut().mono = 61_000_000_000;
    bytes_in(&mut d, 1, b"x\ny\n");
    assert_eq!(cap(&mut d, &o)?.0, OwnerOutcome::Landed);
    let on_disk = disk_status(&tmp)?;
    assert_ne!(on_disk.updated_at, t1, "status carries the old stamp");
    assert_eq!(on_disk.updated_at, d.status().updated_at);
    assert_eq!(on_disk.rounds, 1);
    Ok(())
}

// ------------------------------------------------------------------ per-owner capture
use memory::daemon::OwnerOutcome;
use memory_core::coalescer::Signal;
use memory_core::hint::HintPath;

fn logged<T>(f: impl FnOnce() -> T) -> R<(T, Vec<String>)> {
    let mut out = None;
    let text = memory::log::render(|| out = Some(f()));
    let lines = text
        .lines()
        .map(|l| {
            l.split_once(' ')
                .map(|(_, r)| r.to_owned())
                .unwrap_or_default()
        })
        .collect();
    Ok((need(out, "closure did not run")?, lines))
}
fn cap(d: &mut D, o: &Owner) -> R<(OwnerOutcome, Vec<String>)> {
    let (r, lines) = logged(|| d.capture_owner(o))?;
    Ok((r.map_err(e)?, lines))
}
fn two_files() -> MemFs {
    let mut fs = files_with(b"one\n");
    fs.files.insert(2, b"two\n".to_vec());
    fs
}

#[test]
fn owner_capture_commits_what_a_round_commits() -> R {
    let list = vec![src_of("s1", 1)?, src_of("s2", 2)?];
    let (t1, t2) = (Tmp::new()?, Tmp::new()?);
    let (c1, c2) = (t1.config(true)?, t2.config(true)?);
    let mut full = boot(&t1, &c1, two_files(), FStore::default(), &list, WALL)?;
    let mut one = boot(&t2, &c2, two_files(), FStore::default(), &list, WALL)?;
    assert_eq!(full.reconcile_once().map_err(e)?.landed, 2);
    let (o1, o2) = (owner_of("s1")?, owner_of("s2")?);
    assert_eq!(cap(&mut one, &o1)?.0, OwnerOutcome::Landed);
    let key = current_key(&o1);
    let stored = |d: &mut D| d.store_mut().peek(&key).map(<[u8]>::to_vec);
    assert!(stored(&mut one).is_some());
    assert_eq!(stored(&mut one), stored(&mut full));
    assert!(one.store_mut().peek(&current_key(&o2)).is_none());
    assert_eq!(one.status().rounds, 0);
    one.shutdown().map_err(e)?;
    full.shutdown().map_err(e)?;
    let (i1, i2) = (
        need(index_of(&t1)?, "index 1")?,
        need(index_of(&t2)?, "index 2")?,
    );
    let id = list.first().map(|l| l.1.id.clone()).ok_or("no source")?;
    assert_eq!(
        format!("{:?}", i1.sources.get(&id)),
        format!("{:?}", i2.sources.get(&id))
    );
    assert!(i1.sources.get(&id).is_some_and(|s| s.index.is_some()));
    assert_eq!(i2.sources.len(), 1, "the other owner's source is untouched");
    Ok(())
}

#[test]
fn parked_owner_is_skipped_and_nothing_is_sent() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let o = owner_of("s1")?;
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"x\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    d.store_mut().store.write(&current_key(&o), NEWER.to_vec());
    assert_eq!(d.reconcile_once().map_err(e)?.parked, 1);
    let rounds = d.status().rounds;
    let stamp = d.status().updated_at.clone();
    d.clock_mut().wall = WALL + 3_600_000_000_000;
    let (out, lines) = cap(&mut d, &o)?;
    assert_eq!(out, OwnerOutcome::Skipped);
    assert_eq!(d.status().updated_at, stamp);
    assert!(lines.is_empty(), "{lines:?}");
    assert!(d.store_mut().put_log.is_empty());
    assert_eq!(d.status().rounds, rounds);
    Ok(())
}

#[test]
fn retry_deadline_defers_then_the_capture_runs() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let o = owner_of("s1")?;
    let store = FStore {
        offline: true,
        ..FStore::default()
    };
    let mut d = boot(&tmp, &cfg, files_with(b"x\n"), store, &list, WALL)?;
    assert_eq!(d.next_retry(), None);
    assert_eq!(cap(&mut d, &o)?.0, OwnerOutcome::Failed);
    let t = need(d.next_retry(), "a retry deadline")?;
    let at = u64::try_from(t.as_nanos()).map_err(e)?;
    let before = at.checked_sub(1).ok_or("deadline at zero")?;
    assert!(d.retries_due().is_empty());
    d.store_mut().offline = false;
    d.clock_mut().mono = before;
    let stamp = d.status().updated_at.clone();
    d.clock_mut().wall = WALL + 3_600_000_000_000;
    assert_eq!(cap(&mut d, &o)?.0, OwnerOutcome::Deferred);
    assert_eq!(d.status().updated_at, stamp);
    assert!(d.store_mut().put_log.is_empty());
    assert!(d.store_mut().store.objects.is_empty());
    assert!(d.retries_due().is_empty());
    d.clock_mut().mono = at;
    assert_eq!(d.retries_due(), vec![o.clone()]);
    assert_eq!(cap(&mut d, &o)?.0, OwnerOutcome::Landed);
    assert_eq!(d.next_retry(), None);
    assert_eq!(d.status().rounds, 0);
    Ok(())
}

#[test]
fn next_retry_is_the_earliest_deadline_among_owners() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?, src_of("s2", 2)?];
    let (o1, o2) = (owner_of("s1")?, owner_of("s2")?);
    let store = FStore {
        offline: true,
        ..FStore::default()
    };
    let mut d = boot(&tmp, &cfg, two_files(), store, &list, WALL)?;
    assert_eq!(cap(&mut d, &o1)?.0, OwnerOutcome::Failed);
    let first = need(d.next_retry(), "a retry deadline")?;
    // The second owner fails after the first deadline has passed, so its own is later.
    let at = u64::try_from(first.as_nanos()).map_err(e)?;
    d.clock_mut().mono = at + 1_000_000_000;
    assert_eq!(cap(&mut d, &o2)?.0, OwnerOutcome::Failed);
    assert_eq!(d.next_retry(), Some(first), "the earliest, not the latest");
    assert!(d.retries_due().contains(&o1));
    Ok(())
}

#[test]
fn owner_capture_auth_failure_pauses_and_is_written_at_once() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?, src_of("s2", 2)?];
    let (o1, o2) = (owner_of("s1")?, owner_of("s2")?);
    let mut d = boot(&tmp, &cfg, two_files(), FStore::default(), &list, WALL)?;
    // A first write of status.json, so the pause below is inside the 60 s interval.
    assert_eq!(cap(&mut d, &o2)?.0, OwnerOutcome::Landed);
    assert!(!disk_status(&tmp)?.host_paused);
    d.store_mut().revoked = true;
    let (out, lines) = cap(&mut d, &o1)?;
    assert_eq!(out, OwnerOutcome::Failed);
    assert_eq!(lines, ["INFO host_paused"]);
    assert!(d.status().host_paused);
    assert!(disk_status(&tmp)?.host_paused, "written at once");
    d.store_mut().revoked = false;
    let (out, lines) = cap(&mut d, &o1)?;
    assert_eq!(out, OwnerOutcome::Paused);
    assert!(lines.is_empty());
    assert!(
        d.store_mut()
            .store
            .objects
            .keys()
            .all(|k| !k.contains("s1"))
    );
    assert_eq!(d.status().rounds, 0);
    Ok(())
}

#[test]
fn a_pause_from_a_round_survives_owner_captures_until_the_next_round() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let o = owner_of("s1")?;
    let store = FStore {
        revoked: true,
        ..FStore::default()
    };
    let mut d = boot(&tmp, &cfg, files_with(b"x\n"), store, &list, WALL)?;
    d.reconcile_once().map_err(e)?;
    assert!(d.status().host_paused);
    d.store_mut().revoked = false;
    let stamp = d.status().updated_at.clone();
    d.clock_mut().wall = WALL + 3_600_000_000_000;
    for _ in 0..2 {
        let (out, lines) = cap(&mut d, &o)?;
        assert_eq!(out, OwnerOutcome::Paused);
        assert_eq!(d.status().updated_at, stamp);
        assert!(lines.is_empty(), "{lines:?}");
        assert!(d.status().host_paused, "an owner capture does not resume");
        assert_eq!(d.status().rounds, 1);
    }
    assert!(d.store_mut().store.objects.is_empty());
    let (report, lines) = logged_round!(d);
    assert_eq!(report.landed, 1);
    assert!(!d.status().host_paused);
    assert_eq!(lines, ["INFO host_resumed"]);
    assert_eq!(d.status().rounds, 2);
    Ok(())
}

#[test]
fn owner_capture_park_is_written_at_once_and_logged_once() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?, src_of("s2", 2)?];
    let (o1, o2) = (owner_of("s1")?, owner_of("s2")?);
    let mut d = boot(&tmp, &cfg, two_files(), FStore::default(), &list, WALL)?;
    assert_eq!(cap(&mut d, &o2)?.0, OwnerOutcome::Landed);
    assert!(disk_status(&tmp)?.parked.is_empty());
    d.store_mut().store.write(&current_key(&o1), NEWER.to_vec());
    let (out, lines) = cap(&mut d, &o1)?;
    assert_eq!(out, OwnerOutcome::Failed);
    assert_eq!(lines.len(), 1, "{lines:?}");
    assert!(
        lines
            .iter()
            .all(|l| l.starts_with("INFO parked owner=") && l.ends_with("scope=owner")),
        "{lines:?}"
    );
    assert_eq!(
        disk_status(&tmp)?.parked,
        vec![memory::daemon::ParkedEntry {
            owner: o1.clone(),
            reason: ParkedReason::Owner(OwnerParkReason::NewerRemoteSchema),
            source: None,
        }]
    );
    let (out, lines) = cap(&mut d, &o1)?;
    assert_eq!((out, lines.is_empty()), (OwnerOutcome::Skipped, true));
    assert_eq!(d.status().rounds, 0);
    Ok(())
}

#[test]
fn owner_capture_attaches_the_launch_and_advances_the_hint_offset() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let o = owner_of("s1")?;
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    let name = "2026-11-09.jsonl";
    let line = hint_line("s1", &["PATH=/usr/bin"])?;
    fs::write(tmp.root().join("hints").join(name), &line).map_err(e)?;
    assert_eq!(d.refresh_hints().len(), 1);
    assert_eq!(cap(&mut d, &o)?.0, OwnerOutcome::Landed);
    assert_eq!(hint_offset(&tmp, name)?, len64(&line));
    let obs = need(
        observe(d.store_mut(), &o, &[b"hello\n".to_vec()])?,
        "no chain",
    )?;
    assert_eq!(obs.launches, 1);
    assert_eq!(d.status().rounds, 0);
    Ok(())
}

fn hint_event(session: &str, event: HintEvent, transcript: &str) -> R<Vec<u8>> {
    let path = HintPath::try_from(transcript.to_owned()).map_err(e)?;
    let record = HintRecord {
        schema: HintSchema::V1,
        agent: Agent::Claude,
        event,
        session: SessionId::try_from(session.to_owned()).map_err(e)?,
        cwd: PathHint::Absent,
        transcript: PathHint::Present { path },
        agent_transcript: PathHint::Absent,
        launch: LaunchHint::None,
        reduction: Reduction::None,
    };
    let mut line = serde_json::to_vec(&record).map_err(e)?;
    line.push(b'\n');
    Ok(line)
}

#[test]
fn hint_refresh_returns_each_record_once_with_its_signal() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"x\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    let file = tmp.root().join("hints").join("2026-11-09.jsonl");
    assert!(d.refresh_hints().is_empty());
    let a = hint_event("s1", HintEvent::TurnEnd, "/x/a.jsonl")?;
    fs::write(&file, &a).map_err(e)?;
    let got = d.refresh_hints();
    assert_eq!(
        got,
        vec![memory::daemon::HintSignal {
            owner: owner_of("s1")?,
            signal: Signal::Stop,
            paths: vec![PathBuf::from("/x/a.jsonl")],
        }]
    );
    assert!(d.refresh_hints().is_empty(), "already signalled");
    let mut both = a;
    both.extend(hint_event("s2", HintEvent::SessionEnd, "/x/b.jsonl")?);
    fs::write(&file, &both).map_err(e)?;
    let got = d.refresh_hints();
    assert_eq!(got.len(), 1);
    assert_eq!(
        got.first().map(|s| (s.owner.clone(), s.signal)),
        Some((owner_of("s2")?, Signal::SessionEnd))
    );
    assert!(d.refresh_hints().is_empty());
    // A full round does not make a signalled record signal again.
    d.reconcile_once().map_err(e)?;
    assert!(d.refresh_hints().is_empty());
    Ok(())
}

#[test]
fn an_uncommitted_hint_signals_once_more_after_a_restart() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"x\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    let file = tmp.root().join("hints").join("2026-11-09.jsonl");
    fs::write(&file, hint_event("s1", HintEvent::TurnEnd, "/x/a.jsonl")?).map_err(e)?;
    assert_eq!(d.refresh_hints().len(), 1);
    let mut d = cycle_sync(d, &tmp, &cfg, &list, false, SyncPolicy::Full)?;
    assert_eq!(d.refresh_hints().len(), 1, "the offset never advanced");
    assert!(d.refresh_hints().is_empty());
    Ok(())
}

// ------------------------------------------------------------------ stop requests

use std::cell::Cell;
use std::rc::Rc;

/// The store of `FStore` that logs every request and raises a stop flag when it receives the
/// `n`th PUT whose key starts with (or, for `current.json`, ends with) a pattern.
#[derive(Default)]
struct Hooked {
    inner: FStore,
    flag: Rc<Cell<bool>>,
    on: Option<(&'static str, usize)>,
    seen: usize,
    log: Vec<String>,
}
impl Hooked {
    fn note(&mut self, op: &str, key: &str) {
        self.log.push(format!("{op} {key}"));
        if op != "PUT" {
            return;
        }
        let Some((pattern, n)) = self.on else {
            return;
        };
        let hit = if pattern == "current.json" {
            key.ends_with(pattern)
        } else {
            key.starts_with(pattern)
        };
        if hit {
            self.seen += 1;
            if self.seen == n {
                self.flag.set(true);
            }
        }
    }
    fn count(&self, prefix: &str) -> usize {
        self.log.iter().filter(|l| l.starts_with(prefix)).count()
    }
}
impl Store for Hooked {
    fn put_if_absent(&mut self, key: &str, body: Published) -> Result<ETag, StoreError> {
        self.note("PUT", key);
        self.inner.put_if_absent(key, body)
    }
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &ETag,
        body: Published,
    ) -> Result<ETag, StoreError> {
        self.note("PUT", key);
        self.inner.put_if_match(key, etag, body)
    }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        self.note("GET", key);
        self.inner.get(key)
    }
}

type H = Daemon<MemFs, Hooked, TestClock, FSrcs>;

fn boot_h(tmp: &Tmp, cfg: &Path, fs: MemFs, store: Hooked, list: &[(Owner, Source)]) -> R<H> {
    let list = list.to_vec();
    let sources = FSrcs::new(move || list.clone());
    let flag = store.flag.clone();
    let parts = Parts {
        fs,
        store,
        clock: TestClock {
            wall: WALL,
            ..TestClock::at(0)
        },
        sources,
        context: PolicyContext {
            valid: true,
            revision: "r".to_owned(),
            rules: Some(rules_digest()),
            collector_paths: vec![],
            collector_identities: vec![],
        },
    };
    let mut d = Daemon::start_with(
        &tmp.root(),
        cfg,
        parts,
        StartOptions {
            sync: SyncPolicy::Full,
        },
    )
    .map_err(|x| x.code().to_owned())?;
    d.set_stop(move || flag.get());
    d.set_sleep(|_| {});
    Ok(d)
}
/// Restart without a stop: the files and the store content move to a new daemon.
fn restart_h(mut d: H, tmp: &Tmp, cfg: &Path, list: &[(Owner, Source)]) -> R<H> {
    let fs = std::mem::take(d.fs_mut());
    let inner = std::mem::take(&mut d.store_mut().inner);
    d.shutdown().map_err(e)?;
    boot_h(
        tmp,
        cfg,
        fs,
        Hooked {
            inner,
            ..Hooked::default()
        },
        list,
    )
}
fn hooked(on: Option<(&'static str, usize)>) -> Hooked {
    Hooked {
        on,
        ..Hooked::default()
    }
}
/// Three pieces of 8 MiB, 8 MiB and 1 MiB, each with its own letter so no two share an object.
fn three_pieces() -> Vec<u8> {
    let mut out = Vec::new();
    for i in 0..(17 * 1024_usize) {
        let letter = b'a' + u8::try_from(i / 8192).unwrap_or(0);
        out.extend(std::iter::repeat_n(letter, 1023));
        out.push(b'\n');
    }
    out
}
fn index_advanced(tmp: &Tmp, list: &[(Owner, Source)]) -> R<bool> {
    let id = list.first().map(|l| l.1.id.clone()).ok_or("no source")?;
    Ok(index_of(tmp)?.is_some_and(|i| i.sources.get(&id).is_some_and(|s| s.index.is_some())))
}

#[test]
fn a_stop_after_piece_one_leaves_one_piece_and_the_next_start_captures_it_all() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let o = owner_of("s1")?;
    let big = three_pieces();
    let mut d = boot_h(
        &tmp,
        &cfg,
        files_with(&big),
        hooked(Some(("objects/", 1))),
        &list,
    )?;
    let failures = format!("{:?}", d.status().failures);
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.failed, report.stopped, report.landed), (0, 1, 0));
    assert_eq!(d.store_mut().count("PUT objects/"), 1);
    assert_eq!(d.store_mut().count("PUT manifests/"), 0);
    assert_eq!(d.store_mut().count("PUT") - 1, 0);
    assert!(d.store_mut().inner.peek(&current_key(&o)).is_none());
    assert_eq!(d.store_mut().log.len(), 2, "one recovery GET, one piece");
    assert_eq!(d.next_retry(), None);
    assert_eq!(format!("{:?}", d.status().failures), failures);
    let d = restart_h(d, &tmp, &cfg, &list)?;
    assert!(!index_advanced(&tmp, &list)?);
    let mut d = d;
    let again = d.reconcile_once().map_err(e)?;
    assert_eq!((again.landed, again.stopped), (1, 0));
    let seen = observe(&d.store_mut().inner, &o, std::slice::from_ref(&big))?;
    assert_eq!(need(seen, "observed")?.latest, Some(big));
    d.shutdown().map_err(e)?;
    assert!(index_advanced(&tmp, &list)?);
    Ok(())
}

#[test]
fn a_stop_during_the_current_write_still_lands_and_advances_the_index() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let o = owner_of("s1")?;
    let mut d = boot_h(
        &tmp,
        &cfg,
        files_with(b"x\n"),
        hooked(Some(("current.json", 1))),
        &list,
    )?;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.landed, report.stopped, report.failed), (1, 0, 0));
    assert!(d.store_mut().inner.peek(&current_key(&o)).is_some());
    d.shutdown().map_err(e)?;
    assert!(index_advanced(&tmp, &list)?);
    Ok(())
}

#[test]
fn a_round_stopped_when_owner_two_lands_never_touches_owners_three_to_five() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![
        src_of("s1", 1)?,
        src_of("s2", 2)?,
        src_of("s3", 3)?,
        src_of("s4", 4)?,
        src_of("s5", 5)?,
    ];
    let mut fs = MemFs::default();
    for h in 1..=5_u64 {
        fs.files.insert(h, format!("owner {h}\n").into_bytes());
    }
    let mut d = boot_h(&tmp, &cfg, fs, hooked(Some(("current.json", 2))), &list)?;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.landed, report.failed, report.stopped), (2, 0, 0));
    for (n, expected) in [
        ("s1", true),
        ("s2", true),
        ("s3", false),
        ("s4", false),
        ("s5", false),
    ] {
        let key = current_key(&owner_of(n)?);
        assert_eq!(d.store_mut().inner.peek(&key).is_some(), expected, "{n}");
        assert_eq!(
            d.store_mut().log.iter().any(|l| l.ends_with(&key)),
            expected,
            "{n}"
        );
    }
    assert_eq!(d.store_mut().count("PUT objects/"), 2);
    assert_eq!(d.store_mut().count("PUT manifests/"), 2);
    assert_eq!(d.status().rounds, 1);
    Ok(())
}

#[test]
fn capture_owner_stopped_mid_pieces_is_stopped_and_captures_again_without_a_stop() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let o = owner_of("s1")?;
    let big = three_pieces();
    let mut d = boot_h(
        &tmp,
        &cfg,
        files_with(&big),
        hooked(Some(("objects/", 1))),
        &list,
    )?;
    assert_eq!(cap_h(&mut d, &o)?, OwnerOutcome::Stopped);
    assert_eq!(d.next_retry(), None);
    d.store_mut().flag.set(false);
    d.store_mut().on = None;
    assert_eq!(cap_h(&mut d, &o)?, OwnerOutcome::Landed);
    let gets = d
        .store_mut()
        .log
        .iter()
        .filter(|l| l.starts_with("GET") && l.ends_with("current.json"))
        .count();
    assert_eq!(gets, 1, "the base survived the stop: no second recovery");
    let seen = observe(&d.store_mut().inner, &o, std::slice::from_ref(&big))?;
    assert_eq!(need(seen, "observed")?.latest, Some(big));
    Ok(())
}
fn cap_h(d: &mut H, o: &Owner) -> R<OwnerOutcome> {
    d.capture_owner(o).map_err(e)
}

#[test]
fn a_stop_before_the_round_runs_no_owner_and_sends_no_request() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?, src_of("s2", 2)?];
    let mut d = boot_h(&tmp, &cfg, two_files(), hooked(None), &list)?;
    d.store_mut().flag.set(true);
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!(
        (
            report.landed,
            report.failed,
            report.stopped,
            report.deferred
        ),
        (0, 0, 0, 0)
    );
    assert!(d.store_mut().log.is_empty());
    Ok(())
}

#[test]
fn a_round_cut_short_by_a_stop_does_not_resume_a_paused_host() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let store = Hooked {
        inner: FStore {
            revoked: true,
            ..FStore::default()
        },
        ..Hooked::default()
    };
    let mut d = boot_h(&tmp, &cfg, files_with(b"x\n"), store, &list)?;
    d.reconcile_once().map_err(e)?;
    assert!(d.status().host_paused);
    d.store_mut().inner.revoked = false;
    d.store_mut().flag.set(true);
    let requests = d.store_mut().log.len();
    let (report, lines) = logged_round!(d);
    assert_eq!(report.landed, 0);
    assert!(lines.is_empty(), "{lines:?}");
    assert!(
        d.status().host_paused,
        "no owner ran: nothing was re-probed"
    );
    assert!(disk_status(&tmp)?.host_paused);
    assert_eq!(d.store_mut().log.len(), requests);
    d.store_mut().flag.set(false);
    let (report, lines) = logged_round!(d);
    assert_eq!(report.landed, 1);
    assert_eq!(lines, ["INFO host_resumed"]);
    assert!(!d.status().host_paused);
    Ok(())
}

/// A lost `current.json` reply followed by an unreadable store pauses between the four
/// verification GETs: three sleeps, each within its ceiling, drawn (not constant).
#[test]
fn verification_pauses_sleep_once_each_within_the_ceiling_and_a_healthy_commit_none() -> R {
    use memory_core::pause::{Wait, ceiling};
    use std::cell::RefCell;
    use std::rc::Rc;
    use std::time::Duration;
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?];
    let mut d = boot(
        &tmp,
        &cfg,
        files_with(b"hello\n"),
        FStore::default(),
        &list,
        WALL,
    )?;
    let slept: Rc<RefCell<Vec<Duration>>> = Rc::default();
    let sink = Rc::clone(&slept);
    d.set_sleep(move |t| sink.borrow_mut().push(t));
    assert_eq!(round_at(&mut d, 0)?.0.landed, 1);
    assert!(slept.borrow().is_empty(), "a healthy commit does not sleep");
    bytes_in(&mut d, 1, b"hello\nworld\n");
    d.store_mut().arm(
        None,
        Effect::LandThenDark(StoreError::Ambiguous(Cause::Reset)),
    );
    let (report, _) = round_at(&mut d, 61)?;
    assert_eq!(report.failed, 1);
    let got = slept.borrow().clone();
    assert_eq!(got.len(), 3, "one sleep per pause");
    for (t, n) in got.iter().zip(0..) {
        assert!(*t <= ceiling(Wait::Verify(n)), "{t:?} above ceiling {n}");
    }
    // Each pause as a fraction of its own ceiling: a constant draw gives one fraction for all
    // three (the ceilings alone would make the durations differ).
    let fractions: Vec<f64> = got
        .iter()
        .zip(0..)
        .map(|(t, n)| t.as_secs_f64() / ceiling(Wait::Verify(n)).as_secs_f64())
        .collect();
    let low = fractions.iter().copied().fold(f64::INFINITY, f64::min);
    let high = fractions.iter().copied().fold(f64::NEG_INFINITY, f64::max);
    assert!(high - low > 0.01, "the draws differ: {got:?} {fractions:?}");
    Ok(())
}

/// The default commit-pause sleep is a real sleep (decision D44): production cannot forget to
/// wire it. One `Churn` gives exactly one rebase pause (ceiling 1 s). The test clock makes the
/// jitter draw deterministic, so a recording daemon learns the pause, and an identical daemon
/// left with its default sleep must take at least that long.
#[test]
fn the_default_commit_pause_sleep_is_a_real_sleep() -> R {
    use std::cell::RefCell;
    use std::rc::Rc;
    use std::time::{Duration, Instant};
    let run = |record: Option<Rc<RefCell<Vec<Duration>>>>| -> R<Duration> {
        let tmp = Tmp::new()?;
        let cfg = tmp.config(true)?;
        let list = vec![src_of("s1", 1)?];
        let parts = parts_of(
            files_with(b"hello\n"),
            FStore::with_model(EtagModel::PerWrite),
            &list,
            WALL,
        );
        let mut d = boot_default_sleep(&tmp, &cfg, parts, SyncPolicy::Full)?;
        if let Some(sink) = record {
            d.set_sleep(move |t| sink.borrow_mut().push(t));
        }
        assert_eq!(round_at(&mut d, 0)?.0.landed, 1);
        bytes_in(&mut d, 1, b"hello\nworld\n");
        d.store_mut()
            .arm(Some(current_key(&owner_of("s1")?)), Effect::Churn);
        let started = Instant::now();
        let (report, refused) = round_at(&mut d, 61)?;
        let took = started.elapsed();
        assert_eq!((report.landed, refused), (1, 1));
        Ok(took)
    };
    let slept: Rc<RefCell<Vec<Duration>>> = Rc::default();
    run(Some(Rc::clone(&slept)))?;
    let pauses = slept.borrow().clone();
    assert_eq!(pauses.len(), 1, "one rebase pause: {pauses:?}");
    let pause = pauses.first().copied().ok_or("no pause")?;
    assert!(
        pause >= Duration::from_millis(20),
        "the fixed draw must give a measurable pause: {pause:?}"
    );
    let took = run(None)?;
    assert!(
        took >= pause,
        "default sleep took {took:?}, pause {pause:?}"
    );
    Ok(())
}

// ------------------------------------------------------------------ round progress
/// A monotonic clock the test moves through a shared cell, also while a round runs.
struct SharedClock(Rc<Cell<u64>>);
impl memory::environment::Clock for SharedClock {
    fn monotonic_ns(&self) -> u64 {
        self.0.get()
    }
    fn wall_ns(&self) -> i128 {
        WALL
    }
}

/// How often one owner's turn asks the stop probe (the loop once, the commit steps after it);
/// the exact elapsed values below fail if this drifts.
const PROBES_PER_OWNER: u64 = 4;

/// A round of three owners in which the monotonic clock moves `step_s` seconds per owner's turn
/// (spread over the stop probe's calls); the round's log lines without timestamps.
fn progress_round(step_s: u64) -> R<Vec<String>> {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s1", 1)?, src_of("s2", 2)?, src_of("s3", 3)?];
    let mut fs = files_with(b"a1\n");
    fs.files.insert(2, b"b1\n".to_vec());
    fs.files.insert(3, b"c1\n".to_vec());
    let now = Rc::new(Cell::new(5_000_000_000));
    let sources = list.clone();
    let parts = Parts {
        fs,
        store: FStore::default(),
        clock: SharedClock(now.clone()),
        sources: FSrcs::new(move || sources.clone()),
        context: PolicyContext {
            valid: true,
            revision: "r".to_owned(),
            rules: Some(rules_digest()),
            collector_paths: vec![],
            collector_identities: vec![],
        },
    };
    let mut d = Daemon::start_with(
        &tmp.root(),
        &cfg,
        parts,
        StartOptions {
            sync: SyncPolicy::Full,
        },
    )
    .map_err(|x| x.code().to_owned())?;
    d.set_sleep(|_| {});
    d.set_stop(move || {
        now.set(now.get() + step_s * 1_000_000_000 / PROBES_PER_OWNER);
        false
    });
    let (report, lines) = logged_round!(d);
    assert_eq!(report.landed, 3);
    Ok(lines)
}

fn progress_lines(lines: &[String]) -> Vec<&str> {
    lines
        .iter()
        .map(String::as_str)
        .filter(|l| l.starts_with("INFO progress"))
        .collect()
}

#[test]
fn a_round_logs_a_progress_line_after_each_owner_when_61_seconds_pass_per_owner() -> R {
    let lines = progress_round(61)?;
    assert_eq!(
        progress_lines(&lines),
        [
            "INFO progress done=1 total=3 landed=1 failed=0 elapsed_s=61",
            "INFO progress done=2 total=3 landed=2 failed=0 elapsed_s=122",
            "INFO progress done=3 total=3 landed=3 failed=0 elapsed_s=183",
        ]
    );
    for line in progress_lines(&lines) {
        let keys: Vec<&str> = line
            .split(' ')
            .skip(2)
            .filter_map(|w| w.split_once('=').map(|(k, _)| k))
            .collect();
        assert_eq!(keys, ["done", "total", "landed", "failed", "elapsed_s"]);
        assert!(!line.contains("owner") && !line.contains('/'), "{line}");
    }
    Ok(())
}

#[test]
fn a_round_under_60_seconds_logs_no_progress() -> R {
    let lines = progress_round(19)?;
    assert!(progress_lines(&lines).is_empty(), "{lines:?}");
    Ok(())
}

#[test]
fn progress_is_spaced_60_seconds_from_the_last_line_not_the_round_start() -> R {
    // 30 s per owner: 30 (none), 60 (line), 90 (30 s since the line: none).
    let lines = progress_round(30)?;
    assert_eq!(
        progress_lines(&lines),
        ["INFO progress done=2 total=3 landed=2 failed=0 elapsed_s=60"]
    );
    Ok(())
}

// ---------------------------------------------------------------- absence (brief 43)

/// `MemFs` whose `stat` can be made to fail with a non-`NotFound` error.
#[derive(Default)]
struct FlakyFs {
    mem: MemFs,
    fail: Arc<std::sync::atomic::AtomicBool>,
}
impl memory::environment::FileSystem for FlakyFs {
    type Error = ();
    fn stat(&mut self, source: u64) -> Result<Option<memory::environment::Stat>, ()> {
        if self.fail.load(Ordering::SeqCst) {
            return Err(());
        }
        memory::environment::FileSystem::stat(&mut self.mem, source)
    }
    fn read(&mut self, source: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, ()> {
        memory::environment::FileSystem::read(&mut self.mem, source, range)
    }
}

/// A walk that can report itself incomplete or its root as missing; resolves `src<n>` to handle n.
#[derive(Clone)]
struct Gate {
    list: Shared,
    complete: Arc<std::sync::atomic::AtomicBool>,
    root: Arc<std::sync::atomic::AtomicBool>,
}
impl memory::daemon::Sources for Gate {
    fn enumerate(&mut self) -> Vec<(Owner, Source)> {
        self.list.lock().map(|g| g.clone()).unwrap_or_default()
    }
    fn walk_complete(&self) -> bool {
        self.complete.load(Ordering::SeqCst)
    }
    fn locate_absent(&mut self, id: &SourceId, _path: &str) -> memory::daemon::Probe {
        if !self.root.load(Ordering::SeqCst) {
            return memory::daemon::Probe::Skip;
        }
        id.as_str()
            .strip_prefix("src")
            .and_then(|n| n.parse::<u64>().ok())
            .map_or(memory::daemon::Probe::Skip, memory::daemon::Probe::Handle)
    }
}
type AD = Daemon<FlakyFs, FStore, TestClock, Gate>;

struct Rig {
    tmp: Tmp,
    cfg: PathBuf,
    d: AD,
    gate: Gate,
    fail: Arc<std::sync::atomic::AtomicBool>,
}

const HELLO: &[u8] = b"hello\n";
const WORLD: &[u8] = b"world\n";
const FRESH: &[u8] = b"fresh\n";
const SNAP: &[u8] = b"snap\n";

fn start_ad(tmp: &Tmp, cfg: &Path, fs: FlakyFs, store: FStore, gate: &Gate) -> R<AD> {
    let parts = Parts {
        fs,
        store,
        clock: TestClock::at(0),
        sources: gate.clone(),
        context: PolicyContext {
            valid: true,
            revision: "r".to_owned(),
            rules: Some(rules_digest()),
            collector_paths: vec![],
            collector_identities: vec![],
        },
    };
    let mut d = Daemon::start(&tmp.root(), cfg, parts).map_err(|x| x.code().to_owned())?;
    d.set_sleep(|_| {});
    Ok(d)
}

/// Owner `s1` with two captured files (handles 1 and 2); the first round has landed.
fn rig() -> R<Rig> {
    use std::sync::atomic::AtomicBool;
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let fail = Arc::new(AtomicBool::new(false));
    let mut fs = FlakyFs {
        fail: Arc::clone(&fail),
        ..FlakyFs::default()
    };
    fs.mem.files.insert(1, HELLO.to_vec());
    fs.mem.files.insert(2, WORLD.to_vec());
    let gate = Gate {
        list: Arc::new(Mutex::new(vec![src_of("s1", 1)?, src_of("s1", 2)?])),
        complete: Arc::new(AtomicBool::new(true)),
        root: Arc::new(AtomicBool::new(true)),
    };
    let mut d = start_ad(&tmp, &cfg, fs, FStore::default(), &gate)?;
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 1);
    Ok(Rig {
        tmp,
        cfg,
        d,
        gate,
        fail,
    })
}
fn remove_file(r: &mut Rig, handle: u64) {
    r.d.fs_mut().mem.files.remove(&handle);
    if let Ok(mut list) = r.gate.list.lock() {
        list.retain(|(_, s)| s.handle != handle);
    }
}
fn deleted_and_epochs(d: &mut AD) -> R<(bool, usize)> {
    // Brief 48: SNAP is the bytes of the snapshot sources below.
    let history = [
        HELLO.to_vec(),
        WORLD.to_vec(),
        FRESH.to_vec(),
        SNAP.to_vec(),
    ];
    let seen = need(
        observe(d.store_mut(), &owner_of("s1")?, &history)?,
        "no chain",
    )?;
    Ok((seen.deleted, seen.epochs))
}
fn rounds_never_delete(r: &mut Rig, n: usize) -> R<usize> {
    let mut skipped = 0;
    for _ in 0..n {
        let report = r.d.reconcile_once().map_err(e)?;
        assert_eq!(report.deleted, 0);
        skipped += report.absence_skipped;
        assert!(
            !deleted_and_epochs(&mut r.d)?.0,
            "deleted by a guarded round"
        );
    }
    Ok(skipped)
}

#[test]
fn a_deleted_file_is_recorded_on_the_second_full_round_only() -> R {
    let mut r = rig()?;
    remove_file(&mut r, 1);
    let first = r.d.reconcile_once().map_err(e)?;
    assert_eq!((first.landed, first.deleted), (0, 0));
    assert!(!deleted_and_epochs(&mut r.d)?.0);
    let second = r.d.reconcile_once().map_err(e)?;
    assert_eq!((second.landed, second.deleted), (1, 1));
    assert!(deleted_and_epochs(&mut r.d)?.0);
    let third = r.d.reconcile_once().map_err(e)?;
    assert_eq!((third.landed, third.deleted), (0, 0), "recorded once");
    Ok(())
}

#[test]
fn a_file_back_before_the_second_round_is_not_deleted() -> R {
    let mut r = rig()?;
    remove_file(&mut r, 1);
    assert_eq!(r.d.reconcile_once().map_err(e)?.deleted, 0);
    r.d.fs_mut().mem.files.insert(1, HELLO.to_vec());
    if let Ok(mut list) = r.gate.list.lock() {
        list.push(src_of("s1", 1)?);
    }
    assert_eq!(r.d.reconcile_once().map_err(e)?.deleted, 0);
    remove_file(&mut r, 1);
    // The count restarted: one absent round is not two.
    assert_eq!(r.d.reconcile_once().map_err(e)?.deleted, 0);
    assert!(!deleted_and_epochs(&mut r.d)?.0);
    Ok(())
}

#[test]
fn a_missing_root_deletes_nothing() -> R {
    let mut r = rig()?;
    r.gate.root.store(false, Ordering::SeqCst);
    remove_file(&mut r, 1);
    assert_eq!(rounds_never_delete(&mut r, 3)?, 3);
    Ok(())
}

#[test]
fn a_truncated_or_busy_walk_deletes_nothing() -> R {
    let mut r = rig()?;
    r.gate.complete.store(false, Ordering::SeqCst);
    remove_file(&mut r, 1);
    rounds_never_delete(&mut r, 3)?;
    Ok(())
}

#[test]
fn a_stat_error_is_not_an_absence() -> R {
    let mut r = rig()?;
    remove_file(&mut r, 1);
    r.fail.store(true, Ordering::SeqCst);
    rounds_never_delete(&mut r, 3)?;
    Ok(())
}

#[test]
fn a_parked_source_is_never_a_candidate() -> R {
    let mut r = rig()?;
    let (tmp, cfg, gate) = (r.tmp, r.cfg, r.gate);
    let fs = std::mem::take(r.d.fs_mut());
    let store = std::mem::take(r.d.store_mut());
    r.d.shutdown().map_err(e)?;
    {
        let lock = DaemonLock::acquire(&tmp.root()).map_err(e)?;
        let state = StateDir::prepare(&lock).map_err(e)?;
        let body = StatusBody {
            parked: vec![memory::daemon::ParkedEntry {
                owner: owner_of("s1")?,
                reason: ParkedReason::Source(ParkReason::RepeatedAuditMismatch),
                source: Some(SourceId::try_from("src1".to_owned()).map_err(e)?),
            }],
            ..StatusBody::default()
        };
        state
            .write(StateFile::Status, &serde_json::to_vec(&body).map_err(e)?)
            .map_err(e)?;
    }
    let d = start_ad(&tmp, &cfg, fs, store, &gate)?;
    let mut r = Rig {
        tmp,
        cfg,
        d,
        gate,
        fail: r.fail,
    };
    assert_eq!(r.d.status().parked.len(), 1);
    remove_file(&mut r, 1);
    rounds_never_delete(&mut r, 3)?;
    Ok(())
}

#[test]
fn a_file_recreated_after_its_deletion_is_a_new_live_epoch() -> R {
    let mut r = rig()?;
    remove_file(&mut r, 1);
    r.d.reconcile_once().map_err(e)?;
    assert_eq!(r.d.reconcile_once().map_err(e)?.deleted, 1);
    assert_eq!(deleted_and_epochs(&mut r.d)?, (true, 2));
    r.d.fs_mut().mem.files.insert(1, FRESH.to_vec());
    if let Ok(mut list) = r.gate.list.lock() {
        list.push(src_of("s1", 1)?);
    }
    let back = r.d.reconcile_once().map_err(e)?;
    assert_eq!((back.landed, back.deleted), (1, 0));
    assert_eq!(deleted_and_epochs(&mut r.d)?, (false, 3));
    Ok(())
}

#[test]
fn capture_owner_never_counts_absence() -> R {
    let mut r = rig()?;
    remove_file(&mut r, 1);
    let owner = owner_of("s1")?;
    for _ in 0..3 {
        r.d.capture_owner(&owner).map_err(e)?;
    }
    assert!(!deleted_and_epochs(&mut r.d)?.0);
    // Had the captures counted, this first full round would be the second miss.
    let first = r.d.reconcile_once().map_err(e)?;
    assert_eq!(first.deleted, 0);
    assert!(!deleted_and_epochs(&mut r.d)?.0);
    Ok(())
}

#[test]
fn an_owner_whose_every_file_is_gone_is_recorded_deleted_then_leaves_the_set() -> R {
    let mut r = rig()?;
    remove_file(&mut r, 1);
    remove_file(&mut r, 2);
    let first = r.d.reconcile_once().map_err(e)?;
    assert_eq!((first.landed, first.deleted), (0, 0));
    let second = r.d.reconcile_once().map_err(e)?;
    assert_eq!((second.landed, second.deleted), (1, 2));
    assert!(deleted_and_epochs(&mut r.d)?.0);
    let (tmp, d) = (r.tmp, r.d);
    d.shutdown().map_err(e)?;
    let index = need(index_of(&tmp)?, "no index")?;
    assert!(index.owners.is_empty(), "{:?}", index.owners);
    Ok(())
}

// ---------------------------------------------------------------- snapshot absence (brief 48)

/// A Claude file-history snapshot source (handle 3) of owner `s1`.
fn snap_of(handle: u64) -> R<(Owner, Source)> {
    let (owner, mut s) = src_of("s1", handle)?;
    s.kind = SourceKind::ClaudeFileHistory;
    s.class = SourceClass::Snapshot;
    s.path = format!("file-history/s1/{handle:016x}@v1");
    s.facts.profile = Profile::Scanned;
    s.facts.identities = vec![s.path.clone()];
    Ok((owner, s))
}

/// Owner `s1` holding only one captured snapshot file (handle 3); the first round has landed.
fn snap_rig() -> R<Rig> {
    use std::sync::atomic::AtomicBool;
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let fail = Arc::new(AtomicBool::new(false));
    let mut fs = FlakyFs {
        fail: Arc::clone(&fail),
        ..FlakyFs::default()
    };
    fs.mem.files.insert(3, SNAP.to_vec());
    let gate = Gate {
        list: Arc::new(Mutex::new(vec![snap_of(3)?])),
        complete: Arc::new(AtomicBool::new(true)),
        root: Arc::new(AtomicBool::new(true)),
    };
    let mut d = start_ad(&tmp, &cfg, fs, FStore::default(), &gate)?;
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 1);
    assert!(!deleted_and_epochs(&mut d)?.0);
    Ok(Rig {
        tmp,
        cfg,
        d,
        gate,
        fail,
    })
}

#[test]
fn a_deleted_snapshot_is_recorded_on_the_second_full_round_only() -> R {
    // Brief 48: D52 applies to snapshot entries unchanged.
    let mut r = snap_rig()?;
    remove_file(&mut r, 3);
    let first = r.d.reconcile_once().map_err(e)?;
    assert_eq!((first.landed, first.deleted), (0, 0));
    assert!(!deleted_and_epochs(&mut r.d)?.0);
    let second = r.d.reconcile_once().map_err(e)?;
    assert_eq!((second.landed, second.deleted), (1, 1));
    assert!(deleted_and_epochs(&mut r.d)?.0);
    let third = r.d.reconcile_once().map_err(e)?;
    assert_eq!((third.landed, third.deleted), (0, 0), "recorded once");
    Ok(())
}

#[test]
fn a_snapshot_back_before_the_second_round_is_not_deleted() -> R {
    let mut r = snap_rig()?;
    remove_file(&mut r, 3);
    assert_eq!(r.d.reconcile_once().map_err(e)?.deleted, 0);
    r.d.fs_mut().mem.files.insert(3, SNAP.to_vec());
    if let Ok(mut list) = r.gate.list.lock() {
        list.push(snap_of(3)?);
    }
    assert_eq!(r.d.reconcile_once().map_err(e)?.deleted, 0);
    remove_file(&mut r, 3);
    assert_eq!(
        r.d.reconcile_once().map_err(e)?.deleted,
        0,
        "count restarted"
    );
    assert!(!deleted_and_epochs(&mut r.d)?.0);
    Ok(())
}

#[test]
fn a_snapshot_is_not_deleted_by_a_guarded_round() -> R {
    // Incomplete walk, missing root and stat error each delete nothing.
    let mut r = snap_rig()?;
    remove_file(&mut r, 3);
    r.gate.complete.store(false, Ordering::SeqCst);
    rounds_never_delete(&mut r, 3)?;
    r.gate.complete.store(true, Ordering::SeqCst);
    r.gate.root.store(false, Ordering::SeqCst);
    assert_eq!(rounds_never_delete(&mut r, 3)?, 3);
    r.gate.root.store(true, Ordering::SeqCst);
    r.fail.store(true, Ordering::SeqCst);
    rounds_never_delete(&mut r, 3)?;
    Ok(())
}

#[test]
fn an_owner_with_only_snapshots_stays_in_the_set_until_they_are_deleted() -> R {
    let mut r = snap_rig()?;
    remove_file(&mut r, 3);
    assert_eq!(r.d.reconcile_once().map_err(e)?.deleted, 0);
    let (tmp, d) = (r.tmp, r.d);
    d.shutdown().map_err(e)?;
    let index = need(index_of(&tmp)?, "no index")?;
    assert_eq!(index.owners, [owner_of("s1")?], "left the set while live");

    let mut r = snap_rig()?;
    remove_file(&mut r, 3);
    r.d.reconcile_once().map_err(e)?;
    assert_eq!(r.d.reconcile_once().map_err(e)?.deleted, 1);
    let (tmp, d) = (r.tmp, r.d);
    d.shutdown().map_err(e)?;
    let index = need(index_of(&tmp)?, "no index")?;
    assert!(index.owners.is_empty(), "{:?}", index.owners);
    Ok(())
}

#[test]
fn capture_owner_never_counts_absence_for_a_snapshot() -> R {
    let mut r = snap_rig()?;
    remove_file(&mut r, 3);
    let owner = owner_of("s1")?;
    for _ in 0..3 {
        r.d.capture_owner(&owner).map_err(e)?;
    }
    assert!(!deleted_and_epochs(&mut r.d)?.0);
    assert_eq!(r.d.reconcile_once().map_err(e)?.deleted, 0);
    assert!(!deleted_and_epochs(&mut r.d)?.0);
    Ok(())
}

#[test]
fn a_recreated_snapshot_is_a_new_live_epoch() -> R {
    // Brief 48: the next epoch after a deletion, with a full upload (local pieces were cleared).
    let mut r = snap_rig()?;
    remove_file(&mut r, 3);
    r.d.reconcile_once().map_err(e)?;
    assert_eq!(r.d.reconcile_once().map_err(e)?.deleted, 1);
    assert_eq!(deleted_and_epochs(&mut r.d)?, (true, 1));
    r.d.fs_mut().mem.files.insert(3, FRESH.to_vec());
    if let Ok(mut list) = r.gate.list.lock() {
        list.push(snap_of(3)?);
    }
    let back = r.d.reconcile_once().map_err(e)?;
    assert_eq!((back.landed, back.deleted), (1, 0));
    assert_eq!(deleted_and_epochs(&mut r.d)?, (false, 1));
    // `Replace` clears the old epochs and takes the next epoch number.
    Ok(())
}

/// Owner `s1` with one scanned file (handle 3) that the privacy judgement withholds.
fn withheld_rig() -> R<Rig> {
    let mut r = snap_rig()?;
    // Reuse the rig plumbing with a new owner state: a fresh file that is withheld.
    let secret: &[u8] = concat!("key = ", "AKIA", "EXAMPLEEXAMPLE00", "\n").as_bytes();
    r.d.fs_mut().mem.files.insert(4, secret.to_vec());
    if let Ok(mut list) = r.gate.list.lock() {
        list.push(snap_of(4)?);
    }
    let first = r.d.reconcile_once().map_err(e)?;
    assert_eq!((first.landed, first.withheld), (0, 1));
    Ok(r)
}

fn rig_index_has(r: Rig, id: &str) -> R<bool> {
    let (tmp, d) = (r.tmp, r.d);
    d.shutdown().map_err(e)?;
    let index = need(index_of(&tmp)?, "no index")?;
    Ok(index.sources.keys().any(|k| k.as_str() == id))
}

#[test]
fn a_withheld_only_record_is_dropped_after_two_misses_and_builds_no_change() -> R {
    let mut r = withheld_rig()?;
    remove_file(&mut r, 4);
    let first = r.d.reconcile_once().map_err(e)?;
    assert_eq!((first.landed, first.deleted), (0, 0));
    assert!(rig_index_has(r, "src4")?, "dropped after one miss");

    let mut r = withheld_rig()?;
    remove_file(&mut r, 4);
    r.d.reconcile_once().map_err(e)?;
    let second = r.d.reconcile_once().map_err(e)?;
    assert_eq!((second.landed, second.deleted), (0, 0), "no change built");
    assert!(!deleted_and_epochs(&mut r.d)?.0);
    assert!(!rig_index_has(r, "src4")?, "kept forever");
    Ok(())
}

#[test]
fn a_withheld_file_back_before_the_second_round_keeps_its_record() -> R {
    let mut r = withheld_rig()?;
    let secret: Vec<u8> = r.d.fs_mut().mem.files.get(&4).cloned().unwrap_or_default();
    remove_file(&mut r, 4);
    r.d.reconcile_once().map_err(e)?;
    r.d.fs_mut().mem.files.insert(4, secret);
    if let Ok(mut list) = r.gate.list.lock() {
        list.push(snap_of(4)?);
    }
    r.d.reconcile_once().map_err(e)?;
    remove_file(&mut r, 4);
    r.d.reconcile_once().map_err(e)?;
    assert!(rig_index_has(r, "src4")?, "count did not restart");
    Ok(())
}

#[test]
fn the_owner_set_survives_a_restart() -> R {
    let r = rig()?;
    let (tmp, cfg, gate, fail) = (r.tmp, r.cfg, r.gate, r.fail);
    let mut d = r.d;
    let fs = std::mem::take(d.fs_mut());
    let store = std::mem::take(d.store_mut());
    d.shutdown().map_err(e)?;
    let index = need(index_of(&tmp)?, "no index")?;
    assert_eq!(index.owners, [owner_of("s1")?]);
    // Restart with both files gone: the owner is still visited and recorded deleted.
    let mut fs = fs;
    fs.mem.files.clear();
    if let Ok(mut list) = gate.list.lock() {
        list.clear();
    }
    let d = start_ad(&tmp, &cfg, fs, store, &gate)?;
    let mut r = Rig {
        tmp,
        cfg,
        d,
        gate,
        fail,
    };
    assert_eq!(r.d.reconcile_once().map_err(e)?.deleted, 0);
    assert_eq!(r.d.reconcile_once().map_err(e)?.deleted, 2);
    Ok(())
}

#[test]
fn an_index_without_owners_still_loads() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    {
        let lock = DaemonLock::acquire(&tmp.root()).map_err(e)?;
        let state = StateDir::prepare(&lock).map_err(e)?;
        state
            .write(StateFile::Index, br#"{"sources":{},"hints":{}}"#)
            .map_err(e)?;
    }
    let gate = Gate {
        list: Arc::new(Mutex::new(Vec::new())),
        complete: Arc::new(std::sync::atomic::AtomicBool::new(true)),
        root: Arc::new(std::sync::atomic::AtomicBool::new(true)),
    };
    let d = start_ad(&tmp, &cfg, FlakyFs::default(), FStore::default(), &gate)?;
    assert_eq!(d.index_load(), StateLoad::Loaded);
    assert!(!d.rebuilt());
    Ok(())
}

/// D54, brief 47: an index written before `withheld` existed loads with `None`.
#[test]
fn an_index_without_the_withheld_field_loads() -> Result<(), serde_json::Error> {
    let old = r#"{"sources":{"s":{"index":null,"missing":0,"snapshot":null,"deletion_recorded":false}},"hints":{}}"#;
    let body: memory::daemon::IndexBody = serde_json::from_str(old)?;
    assert!(body.sources.values().all(|s| s.withheld.is_none()));
    Ok(())
}
