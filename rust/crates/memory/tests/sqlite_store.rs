//! Codex SQLite stores on the real filesystem (brief 45, step 2): the consistent copy, the
//! database-plus-`-wal` fingerprint and the removal of the temporary copy. Synthetic databases
//! in temporary directories and the in-memory store only.
use memory::capture::{Env, Locals, Source, SourceOutcome, attach_launches, capture};
use memory::commit::{Base, commit};
use memory::environment::{Backup, FileSystem, Stat};
use memory::realfs::{Encoding, RealFs};
use memory_core::domain::{
    Agent, ArchiveId, Birth, FileId, Fingerprint, MachineId, Owner, SnapshotFingerprint,
    SourceClass, SourceId, SourceKind, WalFingerprint,
};
use memory_core::manifest::manifest_hash;
use memory_core::manifest::{Header, OwnerState, Timestamp, Writer};
use memory_core::privacy::{PolicyContext, Profile, ResolvedSourceFacts, rules_digest};
use proptest::prelude::*;
use rusqlite::Connection;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

#[path = "common/store.rs"]
mod mem_store;
use mem_store::MemStore;

type R<T = ()> = Result<T, String>;
fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}

static NEXT: AtomicU64 = AtomicU64::new(0);

struct Dir(PathBuf);
impl Dir {
    fn new() -> R<Self> {
        let p = std::env::temp_dir().join(format!(
            "sqlite-store-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::SeqCst)
        ));
        fs::create_dir_all(p.join("tmp")).map_err(e)?;
        Ok(Self(p))
    }
    fn db(&self) -> PathBuf {
        self.0.join("state_5.sqlite")
    }
    fn tmp(&self) -> PathBuf {
        self.0.join("tmp")
    }
    fn tmp_entries(&self) -> usize {
        fs::read_dir(self.tmp()).map_or(usize::MAX, Iterator::count)
    }
}
impl Drop for Dir {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn header() -> R<Header> {
    Ok(Header {
        owner: Owner::Shared {
            archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
            machine: MachineId::try_from("m".to_owned()).map_err(e)?,
            agent: Agent::Codex,
        },
        captured_at: Timestamp::try_from("2026-11-07T10:30:00Z".to_owned()).map_err(e)?,
        writer: Writer {
            name: "memory".to_owned(),
            version: "0.1.0".to_owned(),
            commit: "abc".to_owned(),
        },
        launch: vec![],
    })
}

struct Rig {
    fs: RealFs,
    store: MemStore,
    source: Source,
    locals: Locals,
    base: Option<Base>,
    context: PolicyContext,
    now_ns: i64,
    piece_len: usize,
}

impl Rig {
    fn new(dir: &Dir, fs: RealFs) -> R<Self> {
        let mut fs = fs;
        let handle = fs.register_encoded(dir.db(), Encoding::Sqlite);
        let path = "state_5.sqlite".to_owned();
        Ok(Self {
            fs,
            store: MemStore::default(),
            source: Source {
                handle,
                id: SourceId::try_from("stores:state_5.sqlite".to_owned()).map_err(e)?,
                kind: SourceKind::CodexStore,
                class: SourceClass::Snapshot,
                path: path.clone(),
                facts: ResolvedSourceFacts {
                    identities: vec![path],
                    file_identity: Some(7),
                    ancestor_identities: vec![],
                    path_resolved: true,
                    root_resolved: true,
                    owner: Some("o".to_owned()),
                    profile: Profile::OtherKnown,
                },
            },
            locals: Locals::new(),
            base: None,
            now_ns: 10_000_000_000,
            piece_len: 8 * 1024 * 1024,
            context: PolicyContext {
                valid: true,
                revision: "r".to_owned(),
                rules: Some(rules_digest()),
                collector_paths: vec![],
                collector_identities: vec![],
            },
        })
    }
    /// Move the clock past the snapshot interval.
    fn later(&mut self) {
        self.now_ns += 7 * 3600 * 1_000_000_000;
    }
    fn cycle(&mut self) -> R<SourceOutcome> {
        let empty = OwnerState::default();
        let state = self.base.as_ref().map_or(&empty, |b| &b.state);
        let env = Env {
            header: header()?,
            context: &self.context,
            now_ns: self.now_ns,
            piece_len: self.piece_len,
        };
        let got = capture(
            &mut self.fs,
            std::slice::from_ref(&self.source),
            &self.locals,
            state,
            &env,
        );
        if let Some(mut request) = got.commit {
            attach_launches(&mut request, &[]);
            let landed =
                commit(&mut self.store, self.base.as_ref(), request, &mut |_| {}).map_err(e)?;
            self.base = Some(landed.base());
        }
        for update in got.updates {
            update.apply(&mut self.locals);
        }
        got.outcomes
            .into_iter()
            .next()
            .map(|(_, o)| o)
            .ok_or_else(|| "no outcome".to_owned())
    }
    /// The stored database image: the decoded piece that starts with the SQLite header.
    fn stored_image(&self) -> R<Vec<u8>> {
        for (key, (body, _)) in &self.store.objects {
            if key.starts_with("objects/")
                && let Ok(raw) = zstd::decode_all(body.as_slice())
                && raw.starts_with(b"SQLite format 3\0")
            {
                return Ok(raw);
            }
        }
        Err("no stored database".to_owned())
    }
}

/// A WAL-mode database whose rows live only in the `-wal`; the writer stays open.
fn wal_db(path: &Path, rows: u32) -> R<Connection> {
    let conn = Connection::open(path).map_err(e)?;
    conn.pragma_update(None, "journal_mode", "wal").map_err(e)?;
    conn.pragma_update(None, "wal_autocheckpoint", 0)
        .map_err(e)?;
    conn.execute("create table t (n integer, s text)", [])
        .map_err(e)?;
    // The first checkpoint-free writes: the table definition is checkpointed away by hand so
    // the rows below are the only frames in the `-wal`.
    conn.query_row("pragma wal_checkpoint(truncate)", [], |_| Ok(()))
        .map_err(e)?;
    for n in 0..rows {
        conn.execute("insert into t values (?1, 'row')", [n])
            .map_err(e)?;
    }
    Ok(conn)
}

fn count_in(image: &[u8], at: &Path) -> R<i64> {
    fs::write(at, image).map_err(e)?;
    let conn = Connection::open(at).map_err(e)?;
    let ok: String = conn
        .query_row("pragma integrity_check", [], |r| r.get(0))
        .map_err(e)?;
    assert_eq!(ok, "ok");
    conn.query_row("select count(*) from t", [], |r| r.get(0))
        .map_err(e)
}

#[test]
fn frames_only_in_the_wal_are_captured_and_the_copy_is_removed() -> R {
    let dir = Dir::new()?;
    let writer = wal_db(&dir.db(), 25)?;
    let wal = fs::metadata(dir.0.join("state_5.sqlite-wal")).map_err(e)?;
    assert!(wal.len() > 0, "rows must be in the -wal");
    let mut rig = Rig::new(&dir, RealFs::with_tmp(dir.tmp()))?;
    assert_eq!(rig.cycle()?, SourceOutcome::Planned);
    assert_eq!(dir.tmp_entries(), 0, "temp copy left after success");
    let image = rig.stored_image()?;
    assert_eq!(count_in(&image, &dir.0.join("restored.sqlite"))?, 25);
    drop(writer);
    Ok(())
}

#[test]
fn a_shm_change_alone_is_unchanged_and_a_wal_change_is_a_new_snapshot() -> R {
    let dir = Dir::new()?;
    let writer = wal_db(&dir.db(), 5)?;
    let mut rig = Rig::new(&dir, RealFs::with_tmp(dir.tmp()))?;
    assert_eq!(rig.cycle()?, SourceOutcome::Planned);
    // Reading through another connection touches the `-shm` only.
    let reader = Connection::open(dir.db()).map_err(e)?;
    let _: i64 = reader
        .query_row("select count(*) from t", [], |r| r.get(0))
        .map_err(e)?;
    let shm = dir.0.join("state_5.sqlite-shm");
    let file = fs::OpenOptions::new().write(true).open(&shm).map_err(e)?;
    file.set_modified(std::time::SystemTime::now() + std::time::Duration::from_secs(60))
        .map_err(e)?;
    assert_eq!(rig.cycle()?, SourceOutcome::Unchanged);
    assert_eq!(dir.tmp_entries(), 0);
    // A new row grows the `-wal`: a new snapshot with the row, once the interval has passed.
    rig.later();
    writer
        .execute("insert into t values (99, 'new')", [])
        .map_err(e)?;
    assert_eq!(rig.cycle()?, SourceOutcome::Planned);
    assert_eq!(count_in(&rig.stored_image()?, &dir.0.join("r.sqlite"))?, 6);
    assert_eq!(rig.cycle()?, SourceOutcome::Unchanged);
    assert_eq!(dir.tmp_entries(), 0);
    drop(reader);
    Ok(())
}

#[test]
fn a_vanished_store_is_raced_and_leaves_no_copy() -> R {
    let dir = Dir::new()?;
    let writer = wal_db(&dir.db(), 3)?;
    let mut rig = Rig::new(&dir, RealFs::with_tmp(dir.tmp()))?;
    // Stat, then the store is replaced by another file before the copy.
    rig.fs.stat(rig.source.handle).map_err(e)?;
    drop(writer);
    for name in ["", "-wal", "-shm"] {
        let _ = fs::remove_file(dir.0.join(format!("state_5.sqlite{name}")));
    }
    assert!(rig.fs.begin_snapshot(rig.source.handle).is_err());
    assert_eq!(dir.tmp_entries(), 0);
    assert_eq!(rig.cycle()?, SourceOutcome::Unchanged);
    assert_eq!(dir.tmp_entries(), 0);
    Ok(())
}

#[test]
fn a_file_that_is_not_a_database_is_a_backup_failure_and_leaves_no_copy() -> R {
    let dir = Dir::new()?;
    fs::write(dir.db(), vec![b'x'; 4096]).map_err(e)?;
    let mut rig = Rig::new(&dir, RealFs::with_tmp(dir.tmp()))?;
    assert_eq!(rig.cycle()?, SourceOutcome::BackupFailed);
    assert_eq!(dir.tmp_entries(), 0);
    assert!(rig.store.objects.is_empty());
    Ok(())
}

#[test]
fn without_a_temp_directory_the_backup_fails_closed() -> R {
    let dir = Dir::new()?;
    let _writer = wal_db(&dir.db(), 2)?;
    let mut rig = Rig::new(&dir, RealFs::new())?;
    assert_eq!(rig.cycle()?, SourceOutcome::BackupFailed);
    assert!(rig.store.objects.is_empty());
    Ok(())
}

/// A read of the copy allocates exactly what it returns. A buffer grown by `read_to_end`
/// overshot each piece and left freed blocks behind: 561 MB peak on a 615 MB store
/// (2026-11-21); with exact buffers a 308 MB synthetic store peaked at 26 MB.
#[test]
fn a_read_of_the_copy_allocates_exactly_the_bytes_it_returns() -> R {
    let dir = Dir::new()?;
    let _writer = big_db(&dir.db())?;
    let mut rig = Rig::new(&dir, RealFs::with_tmp(dir.tmp()))?;
    let handle = rig.source.handle;
    rig.fs.stat(handle).map_err(e)?;
    let len = rig
        .fs
        .begin_snapshot(handle)
        .map_err(e)?
        .ok_or("no copy length")?;
    assert!(len > 100_000, "store too small to show growth: {len}");
    for range in [0..len, 0..len / 3, len / 3..len, len - 1..len + 4096] {
        let buf = rig.fs.read(handle, range.clone()).map_err(e)?;
        assert!(!buf.is_empty(), "{range:?}");
        assert_eq!(buf.capacity(), buf.len(), "{range:?}");
    }
    rig.fs.end_snapshot(handle);
    assert_eq!(dir.tmp_entries(), 0);
    Ok(())
}

/// Simulated seam: the live bytes move after the copy is made; reads see only the copy.
#[derive(Default)]
struct SimFs {
    live: Vec<u8>,
    copy: Option<Vec<u8>>,
    wal_mtime: i64,
    fail: Option<Backup>,
    mutate_after_copy: bool,
    begun: u32,
    ended: u32,
}

impl FileSystem for SimFs {
    type Error = ();
    fn stat(&mut self, _: u64) -> Result<Option<Stat>, ()> {
        Ok(Some(Stat {
            id: FileId {
                dev: 1,
                ino: 1,
                birth: Birth::Unavailable,
            },
            size: self.live.len() as u64,
            mtime_ns: 5,
        }))
    }
    fn read(&mut self, _: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, ()> {
        let copy = self.copy.as_ref().ok_or(())?;
        let end = usize::try_from(range.end).map_err(|_| ())?.min(copy.len());
        let start = usize::try_from(range.start).map_err(|_| ())?.min(end);
        Ok(copy.get(start..end).unwrap_or_default().to_vec())
    }
    fn snapshot_fingerprint(&mut self, _: u64) -> Result<Option<SnapshotFingerprint>, ()> {
        let id = FileId {
            dev: 1,
            ino: 1,
            birth: Birth::Unavailable,
        };
        Ok(Some(SnapshotFingerprint::Sqlite {
            db: Fingerprint {
                id: id.clone(),
                size: self.live.len() as u64,
                mtime_ns: 5,
            },
            wal: WalFingerprint::Present(Fingerprint {
                id,
                size: 0,
                mtime_ns: self.wal_mtime,
            }),
        }))
    }
    fn begin_snapshot(&mut self, _: u64) -> Result<Option<u64>, Backup> {
        self.begun += 1;
        if let Some(f) = self.fail {
            return Err(f);
        }
        self.copy = Some(self.live.clone());
        if self.mutate_after_copy {
            self.live.extend_from_slice(b"-later");
        }
        Ok(Some(self.copy.as_ref().map_or(0, |c| c.len() as u64)))
    }
    fn end_snapshot(&mut self, _: u64) {
        self.ended += 1;
        self.copy = None;
    }
}

#[test]
fn simulated_store_reads_the_copy_not_the_live_bytes_and_releases_it() -> R {
    let dir = Dir::new()?;
    let mut rig = Rig::new(&dir, RealFs::new())?;
    let mut sim = SimFs {
        live: b"SQLite format 3\0body".to_vec(),
        mutate_after_copy: true,
        ..SimFs::default()
    };
    let run = |rig: &mut Rig, sim: &mut SimFs| -> R<SourceOutcome> {
        let empty = OwnerState::default();
        let state = rig.base.as_ref().map_or(&empty, |b| &b.state);
        let env = Env {
            header: header()?,
            context: &rig.context,
            now_ns: rig.now_ns,
            piece_len: 8 * 1024 * 1024,
        };
        let got = capture(
            sim,
            std::slice::from_ref(&rig.source),
            &rig.locals,
            state,
            &env,
        );
        if let Some(mut request) = got.commit {
            attach_launches(&mut request, &[]);
            let landed =
                commit(&mut rig.store, rig.base.as_ref(), request, &mut |_| {}).map_err(e)?;
            rig.base = Some(landed.base());
        }
        for update in got.updates {
            update.apply(&mut rig.locals);
        }
        got.outcomes
            .into_iter()
            .next()
            .map(|(_, o)| o)
            .ok_or_else(|| "none".to_owned())
    };
    assert_eq!(run(&mut rig, &mut sim)?, SourceOutcome::Planned);
    assert_eq!((sim.begun, sim.ended), (1, 1));
    assert_eq!(rig.stored_image()?, b"SQLite format 3\0body");
    // The live bytes moved (size changed), so the next round copies again.
    rig.later();
    assert_eq!(run(&mut rig, &mut sim)?, SourceOutcome::Planned);
    // Same fingerprint, no copy is made.
    sim.mutate_after_copy = false;
    rig.later();
    assert_eq!(run(&mut rig, &mut sim)?, SourceOutcome::Planned);
    let begun = sim.begun;
    assert_eq!(run(&mut rig, &mut sim)?, SourceOutcome::Unchanged);
    assert_eq!(sim.begun, begun);
    // A -wal change alone is a new snapshot.
    sim.wal_mtime = 9;
    rig.later();
    assert_eq!(run(&mut rig, &mut sim)?, SourceOutcome::Planned);
    // Failures map to the closed outcomes and the copy is released each time.
    sim.wal_mtime = 10;
    rig.later();
    sim.fail = Some(Backup::Changed);
    assert_eq!(run(&mut rig, &mut sim)?, SourceOutcome::Raced);
    sim.fail = Some(Backup::Failed);
    assert_eq!(run(&mut rig, &mut sim)?, SourceOutcome::BackupFailed);
    assert!(sim.copy.is_none());
    Ok(())
}

fn big_db(path: &Path) -> R<Connection> {
    let conn = Connection::open(path).map_err(e)?;
    conn.pragma_update(None, "journal_mode", "wal").map_err(e)?;
    conn.pragma_update(None, "wal_autocheckpoint", 0)
        .map_err(e)?;
    conn.execute("create table t (n integer primary key, s text)", [])
        .map_err(e)?;
    for n in 0..600_u32 {
        let text = format!(
            "{n:08}-{}",
            format!("{:x}", u64::from(n).wrapping_mul(0x9e37_79b9_7f4a_7c15)).repeat(40)
        );
        conn.execute("insert into t values (?1, ?2)", rusqlite::params![n, text])
            .map_err(e)?;
    }
    Ok(conn)
}

fn objects(rig: &Rig) -> std::collections::BTreeSet<String> {
    rig.store
        .objects
        .keys()
        .filter(|k| k.starts_with("objects/"))
        .cloned()
        .collect()
}

#[test]
fn a_resnapshot_after_one_row_changes_stores_only_the_changed_pieces() -> R {
    let dir = Dir::new()?;
    let writer = big_db(&dir.db())?;
    let mut rig = Rig::new(&dir, RealFs::with_tmp(dir.tmp()))?;
    rig.piece_len = 16 * 1024;
    assert_eq!(rig.cycle()?, SourceOutcome::Planned);
    let first = objects(&rig);
    assert!(first.len() > 8, "need many pieces, got {}", first.len());
    writer
        .execute("update t set s = 'changed' where n = 300", [])
        .map_err(e)?;
    rig.later();
    assert_eq!(rig.cycle()?, SourceOutcome::Planned);
    let new: Vec<_> = objects(&rig).difference(&first).cloned().collect();
    assert!(
        !new.is_empty() && new.len() <= 3,
        "new objects: {}",
        new.len()
    );
    assert_eq!(dir.tmp_entries(), 0);
    Ok(())
}

#[test]
fn a_lost_index_stores_nothing_new_for_the_same_bytes() -> R {
    let dir = Dir::new()?;
    let _writer = big_db(&dir.db())?;
    let mut rig = Rig::new(&dir, RealFs::with_tmp(dir.tmp()))?;
    rig.piece_len = 16 * 1024;
    assert_eq!(rig.cycle()?, SourceOutcome::Planned);
    let first = objects(&rig);
    // The index is lost: every piece is encoded and put again; each put meets its stored twin.
    rig.locals = Locals::new();
    assert_eq!(rig.cycle()?, SourceOutcome::Planned);
    assert_eq!(objects(&rig), first);
    Ok(())
}

#[test]
fn inside_the_interval_a_changed_store_is_deferred_and_after_it_lands() -> R {
    let dir = Dir::new()?;
    let writer = wal_db(&dir.db(), 3)?;
    let mut rig = Rig::new(&dir, RealFs::with_tmp(dir.tmp()))?;
    assert_eq!(rig.cycle()?, SourceOutcome::Planned);
    writer
        .execute("insert into t values (7, 'x')", [])
        .map_err(e)?;
    rig.now_ns += 3600 * 1_000_000_000;
    assert_eq!(rig.cycle()?, SourceOutcome::Deferred);
    assert_eq!(dir.tmp_entries(), 0);
    rig.now_ns += 6 * 3600 * 1_000_000_000;
    assert_eq!(rig.cycle()?, SourceOutcome::Planned);
    assert_eq!(count_in(&rig.stored_image()?, &dir.0.join("i.sqlite"))?, 4);
    Ok(())
}

#[derive(Clone, Debug)]
enum Op {
    /// A reader touches the `-shm` only: not part of the fingerprint.
    Shm,
    /// A write: bytes change and the `-wal` moves.
    Write(usize, u8),
    /// Only the `-wal` moves (a frame with the same bytes in the copy).
    Wal,
    /// The clock moves by an hour or by seven hours.
    Tick(bool),
    Capture,
}

fn op() -> impl Strategy<Value = Op> {
    prop_oneof![
        Just(Op::Shm),
        (0..4000_usize, any::<u8>()).prop_map(|(p, b)| Op::Write(p, b)),
        Just(Op::Wal),
        any::<bool>().prop_map(Op::Tick),
        Just(Op::Capture),
        Just(Op::Capture),
    ]
}

/// The bytes the latest stored epoch reassembles to, each piece checked against its digest.
fn reassembled(rig: &Rig) -> R<Vec<u8>> {
    let base = rig.base.as_ref().ok_or("no base")?;
    let entry = base.state.sources.values().next().ok_or("no source")?;
    let record = entry.epochs.last().ok_or("no epoch")?;
    let mut out = Vec::new();
    for piece in &record.pieces {
        let (body, _) = rig
            .store
            .objects
            .get(piece.object())
            .ok_or("piece missing")?;
        let raw = zstd::decode_all(body.as_slice()).map_err(e)?;
        if manifest_hash(&raw) != piece.sha256() || raw.len() as u64 != piece.len() {
            return Err("descriptor digest differs from the stored bytes".to_owned());
        }
        out.extend_from_slice(&raw);
    }
    Ok(out)
}

fn model(initial: &[u8], ops: &[Op]) -> R {
    let dir = Dir::new()?;
    let mut rig = Rig::new(&dir, RealFs::new())?;
    rig.piece_len = 64;
    let mut sim = SimFs {
        live: initial.to_vec(),
        wal_mtime: 1,
        ..SimFs::default()
    };
    // Model: the fingerprint last landed and when.
    let mut landed: Option<((i64, usize), i64)> = None;
    for op in ops {
        match op {
            Op::Shm => {}
            Op::Write(pos, byte) => {
                let at = pos % sim.live.len();
                if let Some(b) = sim.live.get_mut(at) {
                    *b ^= byte | 1;
                }
                sim.wal_mtime += 1;
            }
            Op::Wal => sim.wal_mtime += 1,
            Op::Tick(long) => rig.now_ns += if *long { 7 } else { 1 } * 3600 * 1_000_000_000,
            Op::Capture => {
                let fp = (sim.wal_mtime, sim.live.len());
                let want = match landed {
                    None => SourceOutcome::Planned,
                    Some((last, _)) if last == fp => SourceOutcome::Unchanged,
                    Some((_, at)) if rig.now_ns - at < 6 * 3600 * 1_000_000_000 => {
                        SourceOutcome::Deferred
                    }
                    Some(_) => SourceOutcome::Planned,
                };
                let got = capture_sim(&mut rig, &mut sim)?;
                if got != want {
                    return Err(format!("expected {want:?}, got {got:?}"));
                }
                if got == SourceOutcome::Planned {
                    landed = Some((fp, rig.now_ns));
                    if reassembled(&rig)? != sim.live {
                        return Err("stored epoch differs from the copy".to_owned());
                    }
                }
            }
        }
    }
    if sim.begun != sim.ended {
        return Err("a copy was not released".to_owned());
    }
    Ok(())
}

fn capture_sim(rig: &mut Rig, sim: &mut SimFs) -> R<SourceOutcome> {
    let empty = OwnerState::default();
    let state = rig.base.as_ref().map_or(&empty, |b| &b.state);
    let env = Env {
        header: header()?,
        context: &rig.context,
        now_ns: rig.now_ns,
        piece_len: rig.piece_len,
    };
    let got = capture(
        sim,
        std::slice::from_ref(&rig.source),
        &rig.locals,
        state,
        &env,
    );
    if let Some(mut request) = got.commit {
        attach_launches(&mut request, &[]);
        let landed = commit(&mut rig.store, rig.base.as_ref(), request, &mut |_| {}).map_err(e)?;
        rig.base = Some(landed.base());
    }
    for update in got.updates {
        update.apply(&mut rig.locals);
    }
    got.outcomes
        .into_iter()
        .next()
        .map(|(_, o)| o)
        .ok_or_else(|| "none".to_owned())
}

proptest! {
    /// A `-shm` touch is Unchanged, a `-wal` move is a new snapshot once the interval allows
    /// it, and every stored epoch (with pieces reused by digest) reassembles to the copy.
    #[test]
    fn proptest_sqlite_fingerprint_interval_and_reuse(
        initial in proptest::collection::vec(any::<u8>(), 200..1200),
        ops in proptest::collection::vec(op(), 1..40),
    ) {
        model(&initial, &ops).map_err(TestCaseError::fail)?;
    }
}
