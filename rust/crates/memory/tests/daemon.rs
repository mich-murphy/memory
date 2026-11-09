//! Daemon skeleton tests: temp dirs, in-memory filesystem, fake store. No real adapters.
use memory::capture::{Published, Source};
use memory::daemon::{Daemon, Parts, StartError};
use memory::environment::{Clock, FileSystem, Stat, Store, StoreError, Stored};
use memory::state::{DaemonLock, EXIT_LOCK_HELD};
use memory_core::domain::{
    Agent, ArchiveId, Birth, ETag, FileId, MachineId, Owner, SessionId, SessionKey, SourceClass,
    SourceId, SourceKind,
};
use memory_core::privacy::{PolicyContext, Profile, ResolvedSourceFacts, rules_digest};
use std::collections::BTreeMap;
use std::fs;
use std::os::unix::fs::{PermissionsExt, symlink};
use std::path::PathBuf;
use std::process::Command;
use std::sync::atomic::{AtomicU64, Ordering};

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
                "archive_id = \"a\"\nmachine_id = \"m\"\nbucket = \"bucket-1\"\ntoken_path = \"{}\"\n",
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

#[derive(Default)]
struct MemFs {
    files: BTreeMap<u64, Vec<u8>>,
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
            size: u64::try_from(b.len()).unwrap_or(0),
            mtime_ns: 1_000,
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

#[derive(Default)]
struct MemStore {
    objects: BTreeMap<String, (Vec<u8>, u64)>,
    version: u64,
    puts: u64,
}
impl MemStore {
    fn tag(v: u64) -> Result<ETag, StoreError> {
        ETag::try_from(format!("\"v{v}\"")).map_err(|_| StoreError::Server)
    }
    fn put(
        &mut self,
        key: &str,
        body: &Published,
        fence: Option<&ETag>,
    ) -> Result<ETag, StoreError> {
        self.puts += 1;
        let existing = self.objects.get(key).map(|(_, v)| *v);
        match (fence, existing) {
            (None, Some(_)) => return Err(StoreError::PreconditionFailed),
            (Some(_), None) => return Err(StoreError::NotFound),
            (Some(f), Some(v)) if &Self::tag(v)? != f => {
                return Err(StoreError::PreconditionFailed);
            }
            _ => {}
        }
        self.version += 1;
        self.objects
            .insert(key.to_owned(), (body.as_ref().to_vec(), self.version));
        Self::tag(self.version)
    }
}
impl Store for MemStore {
    type Body = Published;
    fn document(bytes: Vec<u8>) -> Published {
        Published::document(bytes)
    }
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
    fn get(&mut self, key: &str) -> Result<Stored<Published>, StoreError> {
        let (b, v) = self.objects.get(key).ok_or(StoreError::NotFound)?;
        Ok(Stored {
            body: Published::document(b.clone()),
            etag: Self::tag(*v)?,
        })
    }
}

struct Clk(u64);
impl Clock for Clk {
    fn monotonic_ns(&self) -> u64 {
        self.0
    }
    fn wall_ns(&self) -> i128 {
        1_794_000_000_000_000_000
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
fn parts(fs: MemFs, store: MemStore) -> Parts<MemFs, MemStore, Clk, Sources> {
    let one = source().ok();
    let sources: Sources = Box::new(move || one.clone().into_iter().collect());
    Parts {
        fs,
        store,
        clock: Clk(0),
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
fn invalid_config_refuses() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(false)?;
    let error = failed(Daemon::start(
        &tmp.root(),
        &cfg,
        parts(MemFs::default(), MemStore::default()),
    ))?;
    assert_eq!(error.exit_code(), memory::daemon::EXIT_CONFIG);
    Ok(())
}

#[test]
fn first_round_commits_and_persists_then_rebuild_does_not_reupload() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let mut d = Daemon::start(
        &tmp.root(),
        &cfg,
        parts(files_with(b"hello\n"), MemStore::default()),
    )
    .map_err(|x| x.code().to_owned())?;
    assert!(d.rebuilt());
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!(report.landed, 1);
    assert_eq!(d.status().rounds, 1);
    d.shutdown().map_err(e)?;
    assert!(tmp.root().join("index.json").exists());
    assert!(tmp.root().join("status.json").exists());
    // Delete the index, restart against the same store contents.
    fs::remove_file(tmp.root().join("index.json")).map_err(e)?;
    let mut store = MemStore::default();
    {
        let mut first = Daemon::start(
            &tmp.root(),
            &cfg,
            parts(files_with(b"hello\n"), MemStore::default()),
        )
        .map_err(|x| x.code().to_owned())?;
        first.reconcile_once().map_err(e)?;
        std::mem::swap(&mut store, first.store_mut());
        first.shutdown().map_err(e)?;
    }
    fs::remove_file(tmp.root().join("index.json")).map_err(e)?;
    let before = store.puts;
    let mut d = Daemon::start(&tmp.root(), &cfg, parts(files_with(b"hello\n"), store))
        .map_err(|x| x.code().to_owned())?;
    assert!(d.rebuilt());
    d.reconcile_once().map_err(e)?;
    assert_eq!(
        d.store_mut().puts,
        before,
        "no re-upload of unchanged bytes"
    );
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
use memory::daemon::{IndexBody, ParkedReason, StatusBody};
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

const WALL: i128 = 1_794_000_000_000_000_000;

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

struct FClk {
    mono: u64,
    wall: i128,
}
impl Clock for FClk {
    fn monotonic_ns(&self) -> u64 {
        self.mono
    }
    fn wall_ns(&self) -> i128 {
        self.wall
    }
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
    /// A foreign writer advances the owner just before this request.
    Foreign(Foreign),
}
struct Rule {
    key: Option<String>,
    effect: Effect,
}

/// Fake store with S3/R2 conditional-write semantics and scripted faults on `current.json`.
#[derive(Default)]
struct FStore {
    objects: BTreeMap<String, (Vec<u8>, u64)>,
    version: u64,
    offline: bool,
    revoked: bool,
    rules: Vec<Rule>,
    put_log: Vec<String>,
    lost_fired: u64,
}
impl FStore {
    fn tag(v: u64) -> Result<ETag, StoreError> {
        ETag::try_from(format!("\"v{v}\"")).map_err(|_| StoreError::Server)
    }
    fn arm(&mut self, key: Option<String>, effect: Effect) {
        self.rules.push(Rule { key, effect });
    }
    fn peek(&self, key: &str) -> Option<&[u8]> {
        self.objects.get(key).map(|(b, _)| b.as_slice())
    }
    fn write(&mut self, key: &str, body: Vec<u8>) {
        self.version += 1;
        self.objects.insert(key.to_owned(), (body, self.version));
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
    fn gate(&self) -> Result<(), StoreError> {
        if self.revoked {
            Err(StoreError::Auth)
        } else if self.offline {
            Err(StoreError::Offline)
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
            if !self.objects.contains_key(&f.manifest_key) {
                self.write(&f.manifest_key, f.manifest.clone());
            }
            self.write(key, f.current.clone());
        }
        self.gate()?;
        let existing = self.objects.get(key).map(|(_, v)| *v);
        match (fence, existing) {
            (None, Some(_)) => return Err(StoreError::PreconditionFailed),
            (Some(_), None) => return Err(StoreError::NotFound),
            (Some(f), Some(v)) if &Self::tag(v)? != f => {
                return Err(StoreError::PreconditionFailed);
            }
            _ => {}
        }
        self.write(key, body.as_ref().to_vec());
        let tag = Self::tag(self.version)?;
        if let Some(Effect::LandThen(err)) = effect {
            self.lost_fired += 1;
            return Err(err);
        }
        Ok(tag)
    }
}
impl Store for FStore {
    type Body = Published;
    fn document(bytes: Vec<u8>) -> Published {
        Published::document(bytes)
    }
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
    fn get(&mut self, key: &str) -> Result<Stored<Published>, StoreError> {
        self.gate()?;
        let (b, v) = self.objects.get(key).ok_or(StoreError::NotFound)?;
        Ok(Stored {
            body: Published::document(b.clone()),
            etag: Self::tag(*v)?,
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
    latest: Option<Vec<u8>>,
    epochs: usize,
    seq: u64,
    launches: usize,
}
/// Safety: the chain is consistent, every object matches its descriptor, and every committed
/// epoch is a prefix of bytes the (single) source once held.
fn observe(store: &FStore, owner: &Owner, history: &[Vec<u8>]) -> R<Option<Observed>> {
    let Some(chain) = read_chain(store, owner)? else {
        return Ok(None);
    };
    let mut latest = None;
    let mut epochs = 0;
    for entry in chain.folded.state.sources.values() {
        epochs += entry.epochs.len();
        for record in &entry.epochs {
            let bytes = reassemble(store, record)?;
            ensure(
                history.iter().any(|v| v.starts_with(&bytes)),
                "committed bytes were never held by the source",
            )?;
            latest = Some(bytes);
        }
    }
    let mut launches = 0;
    for bytes in &chain.manifests {
        launches += match parse_manifest(bytes).map_err(e)? {
            Manifest::Delta(d) => d.launch.len(),
            Manifest::Checkpoint(c) => c.launch.len(),
        };
    }
    Ok(Some(Observed {
        latest,
        epochs,
        seq: chain.current.seq,
        launches,
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

type FSrcs = Box<dyn FnMut() -> Vec<(Owner, Source)> + Send>;
type D = Daemon<MemFs, FStore, FClk, FSrcs>;

fn boot(
    tmp: &Tmp,
    cfg: &Path,
    fs: MemFs,
    store: FStore,
    list: &[(Owner, Source)],
    wall: i128,
) -> R<D> {
    let list = list.to_vec();
    let sources: FSrcs = Box::new(move || list.clone());
    let parts = Parts {
        fs,
        store,
        clock: FClk { mono: 0, wall },
        sources,
        context: PolicyContext {
            valid: true,
            revision: "r".to_owned(),
            rules: Some(rules_digest()),
            collector_paths: vec![],
            collector_identities: vec![],
        },
    };
    Daemon::start(&tmp.root(), cfg, parts).map_err(|x| x.code().to_owned())
}
/// Restart: move the fs and store out, end the daemon (flushed or not) and start a new one.
fn cycle(mut d: D, tmp: &Tmp, cfg: &Path, list: &[(Owner, Source)], flush: bool) -> R<D> {
    let fs = std::mem::take(d.fs_mut());
    let store = std::mem::take(d.store_mut());
    if flush {
        d.shutdown().map_err(e)?;
    } else {
        drop(d);
    }
    boot(tmp, cfg, fs, store, list, WALL)
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
    store.arm(None, Effect::LandThen(StoreError::Ambiguous));
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
        transcript: PathHint::Absent,
        agent_transcript: PathHint::Absent,
        launch: LaunchHint::Full(LaunchRecord::from_sanitized(&launch)),
        reduction: Reduction::None,
    };
    let mut line = serde_json::to_vec(&record).map_err(e)?;
    line.push(b'\n');
    Ok(line)
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
    assert_eq!(d.reconcile_once().map_err(e)?.landed, 0);
    assert_eq!(d.status().rounds, 1, "status counters restarted");
    assert_eq!(d.store_mut().put_log.len(), puts, "no re-upload");
    Ok(())
}

// ---------------------------------------------------------------- coverage guard

struct Shapes {
    restart: AtomicU64,
    lost_reply: AtomicU64,
    delete_index: AtomicU64,
}
impl Shapes {
    const fn new() -> Self {
        Self {
            restart: AtomicU64::new(0),
            lost_reply: AtomicU64::new(0),
            delete_index: AtomicU64::new(0),
        }
    }
    fn counts(&self) -> Vec<(&'static str, u64)> {
        let load = |c: &AtomicU64| c.load(Ordering::Relaxed);
        vec![
            ("restart", load(&self.restart)),
            ("lost-reply", load(&self.lost_reply)),
            ("delete-index", load(&self.delete_index)),
        ]
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
    for missing in 0..3 {
        let mut counts = vec![1_u64; 3];
        if let Some(c) = counts.get_mut(missing) {
            *c = 0;
        }
        let named: Vec<_> = zero
            .counts()
            .into_iter()
            .zip(counts)
            .map(|((name, _), n)| (name, n))
            .collect();
        assert!(require_all(&named).is_err(), "missing shape {missing}");
    }
    let full: Vec<_> = zero.counts().into_iter().map(|(n, _)| (n, 1)).collect();
    assert!(require_all(&full).is_ok());
}

// ---------------------------------------------------------------- model and system under test

#[derive(Clone, Debug)]
enum Step {
    Append { seed: u8, n: u8 },
    Round,
    DeleteIndex,
    Restart,
    LostReply,
    Offline,
    Online,
    Settle,
}

#[derive(Clone, Debug, Default)]
struct Reference {
    file: Vec<u8>,
    history: Vec<Vec<u8>>,
}
impl Reference {
    fn apply(&mut self, step: &Step) {
        if let Step::Append { seed, n } = step {
            let add: Vec<u8> = (0..(*n).max(1)).map(|k| seed.wrapping_add(k) % 3).collect();
            self.file.extend(add);
            self.history.push(self.file.clone());
        }
    }
}

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
}
impl Sut {
    fn new() -> R<Self> {
        let tmp = Tmp::new()?;
        let cfg = tmp.config(true)?;
        let list = vec![src_of("s1", 1)?];
        let d = boot(&tmp, &cfg, MemFs::default(), FStore::default(), &list, WALL)?;
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
        })
    }
    fn daemon(&mut self) -> R<&mut D> {
        self.d.as_mut().ok_or_else(|| "no daemon".to_owned())
    }
    fn restart(&mut self) -> R {
        let d = need(self.d.take(), "no daemon")?;
        self.d = Some(cycle(d, &self.tmp, &self.cfg, &self.list, false)?);
        self.mono = 0;
        Ok(())
    }
    fn round(&mut self) -> R {
        self.mono += 61_000_000_000;
        let mono = self.mono;
        let d = self.daemon()?;
        d.clock_mut().mono = mono;
        let report = d.reconcile_once().map_err(e)?;
        self.ran = true;
        self.index_expected |= report.landed > 0;
        Ok(())
    }
    fn exec(&mut self, step: &Step, after: &Reference, shapes: &Shapes) -> R {
        match step {
            Step::Append { .. } => {
                let bytes = after.file.clone();
                bytes_in(self.daemon()?, 1, &bytes);
            }
            Step::Round => self.round()?,
            Step::DeleteIndex => {
                let _ = fs::remove_file(self.tmp.root().join("index.json"));
                self.restart()?;
                self.index_expected = false;
                bump(&shapes.delete_index);
            }
            Step::Restart => {
                self.restart()?;
                bump(&shapes.restart);
            }
            Step::LostReply => self
                .daemon()?
                .store_mut()
                .arm(None, Effect::LandThen(StoreError::Ambiguous)),
            Step::Offline => self.daemon()?.store_mut().offline = true,
            Step::Online => self.daemon()?.store_mut().offline = false,
            Step::Settle => {
                let store = self.daemon()?.store_mut();
                store.offline = false;
                store.rules.clear();
                for _ in 0..4 {
                    self.round()?;
                }
            }
        }
        let fired = self.daemon()?.store_mut().lost_fired;
        for _ in self.lost_seen..fired {
            bump(&shapes.lost_reply);
        }
        self.lost_seen = fired;
        Ok(())
    }
    fn check(&mut self, after: &Reference, settled: bool) -> R {
        if self.ran {
            match file_state(&self.tmp, StateFile::Status) {
                Loaded::Present(s) => {
                    serde_json::from_slice::<StatusBody>(&s.body).map_err(e)?;
                }
                Loaded::Missing(r) => return Err(format!("status unreadable: {r:?}")),
            }
        }
        if self.index_expected {
            match file_state(&self.tmp, StateFile::Index) {
                Loaded::Present(s) => {
                    serde_json::from_slice::<IndexBody>(&s.body).map_err(e)?;
                }
                Loaded::Missing(r) => return Err(format!("index unreadable: {r:?}")),
            }
        }
        let owner = self.owner.clone();
        let observed = observe(self.daemon()?.store_mut(), &owner, &after.history)?;
        if settled && !after.file.is_empty() {
            let o = need(observed, "no chain after settle")?;
            ensure(
                o.latest.as_deref() == Some(after.file.as_slice()),
                "latest epoch differs from the source bytes after settle",
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
    fn new(shapes: &'static Shapes) -> R<Self> {
        Ok(Self {
            reference: Reference::default(),
            sut: Sut::new()?,
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
                    .check(&self.reference, matches!(step, Step::Settle))
            });
        assert!(result.is_ok(), "{step:?}: {result:?}");
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
    fn lost_reply(&mut self, _: TestCase) {
        self.run(Step::LostReply);
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
        let world = World::new(&HEGEL_SHAPES);
        assert!(world.is_ok(), "{:?}", world.as_ref().err());
        if let Ok(world) = world {
            hegel::stateful::machine(world).steps(8).run(tc);
        }
    })
    .run();
    let guard = require_all(&HEGEL_SHAPES.counts());
    assert!(guard.is_ok(), "{guard:?} {:?}", HEGEL_SHAPES.counts());
}

fn step_strategy() -> BoxedStrategy<Step> {
    prop_oneof![
        3 => (any::<u8>(), 1..=9_u8).prop_map(|(seed, n)| Step::Append { seed, n }),
        4 => Just(Step::Round),
        1 => Just(Step::DeleteIndex),
        1 => Just(Step::Restart),
        2 => Just(Step::LostReply),
        1 => Just(Step::Offline),
        1 => Just(Step::Online),
        2 => Just(Step::Settle),
    ]
    .boxed()
}
struct RefMachine;
impl ReferenceStateMachine for RefMachine {
    type State = Reference;
    type Transition = Step;
    fn init_state() -> BoxedStrategy<Reference> {
        Just(Reference::default()).boxed()
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
                .and_then(|()| sut.check(after, matches!(step, Step::Settle)));
            assert!(result.is_ok(), "{step:?}: {result:?}");
        }
        state
    }
    fn teardown(mut state: PtSut, after: Reference) {
        if let Ok(sut) = state.sut.as_mut() {
            let result = sut
                .exec(&Step::Settle, &after, &PROPTEST_SHAPES)
                .and_then(|()| sut.check(&after, true));
            assert!(result.is_ok(), "final settle: {result:?}");
        }
    }
}
#[test]
fn proptest_daemon_state_machine() {
    let mut runner = TestRunner::new(Config {
        failure_persistence: None,
        ..Config::default()
    });
    let strategy = <RefMachine as ReferenceStateMachine>::sequential_strategy(1..9);
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
    let sources: FSrcs = Box::new(move || shared.lock().map(|g| g.clone()).unwrap_or_default());
    let parts = Parts {
        fs,
        store: FStore::default(),
        clock: FClk {
            mono: 0,
            wall: WALL,
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
    Daemon::start(&tmp.root(), cfg, parts).map_err(|x| x.code().to_owned())
}
fn hint_with(session: &str, launch: LaunchHint) -> R<Vec<u8>> {
    let record = HintRecord {
        schema: HintSchema::V1,
        agent: Agent::Claude,
        event: HintEvent::SessionStart,
        session: SessionId::try_from(session.to_owned()).map_err(e)?,
        cwd: PathHint::Absent,
        transcript: PathHint::Absent,
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
    let base =
        "archive_id = \"a\"\nmachine_id = \"m\"\nbucket = \"bucket-1\"\ntoken_path = \"/t\"\n";
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
    d.store_mut().write(&current_key(&o2), NEWER.to_vec());
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
        }]
    );
    d.clock_mut().mono = 20_000_000_000;
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!(report.skipped, 1);
    assert_eq!((d.status().rounds, disk_status(&tmp)?.rounds), (3, 2));
    Ok(())
}

#[test]
fn two_parked_owners_are_listed_sorted() -> R {
    let tmp = Tmp::new()?;
    let cfg = tmp.config(true)?;
    let list = vec![src_of("s2", 2)?, src_of("s1", 1)?];
    let mut fs = files_with(b"a\n");
    fs.files.insert(2, b"b\n".to_vec());
    let mut d = boot(&tmp, &cfg, fs, FStore::default(), &list, WALL)?;
    let (a, b) = (owner_of("s1")?, owner_of("s2")?);
    d.store_mut().write(&current_key(&a), NEWER.to_vec());
    d.store_mut().write(&current_key(&b), NEWER.to_vec());
    let report = d.reconcile_once().map_err(e)?;
    assert_eq!((report.parked, report.landed), (2, 0));
    let owners: Vec<Owner> = d.status().parked.iter().map(|p| p.owner.clone()).collect();
    assert_eq!(owners, vec![a, b]);
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
    d.store_mut().write(&current_key(&o), b"garbage".to_vec());
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

/// Store that drops `current.json` writes without landing them and reports an ambiguous error.
#[derive(Default)]
struct Ghost {
    inner: MemStore,
    drop_current: bool,
}
impl Store for Ghost {
    type Body = Published;
    fn document(bytes: Vec<u8>) -> Published {
        Published::document(bytes)
    }
    fn put_if_absent(&mut self, key: &str, body: Published) -> Result<ETag, StoreError> {
        if self.drop_current && key.ends_with("current.json") {
            return Err(StoreError::Ambiguous);
        }
        self.inner.put_if_absent(key, body)
    }
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &ETag,
        body: Published,
    ) -> Result<ETag, StoreError> {
        self.inner.put_if_match(key, etag, body)
    }
    fn get(&mut self, key: &str) -> Result<Stored<Published>, StoreError> {
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
        clock: Clk(0),
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
    let r = d.reconcile_once().map_err(e)?;
    assert_eq!((r.landed, r.failed), (0, 1));
    d.store_mut().drop_current = false;
    let r = d.reconcile_once().map_err(e)?;
    assert_eq!((r.landed, r.failed), (1, 0));
    assert_eq!(d.root(), tmp.root());
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
