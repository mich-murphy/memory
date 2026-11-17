//! Proofs of the streamed capture (brief 31, STREAM): the pieces a streamed capture stores
//! equal an oracle computed here from the file bytes; `capture()` (collect every piece, then
//! commit) and `capture_into` with a `StoreSink` (PUT each piece at once, then `commit_staged`)
//! agree; reads are at most one piece long; and a stop, a failed PUT and a 412 end as designed.
//! Synthetic in-memory filesystem and store only.
use memory::audit::AuditCounts;
use memory::capture::{Env, Locals, Source, SourceOutcome, Update, capture, capture_into};
use memory::commit::{
    Base, Failure, Halted, KnownOurs, PieceSink, StoreSink, commit, commit_staged,
};
use memory::environment::{FileSystem, Published, Stat, Store, StoreError, Stored};
use memory_core::domain::{
    Agent, ArchiveId, ETag, MachineId, Owner, SessionId, SessionKey, Sha256, Sha256State,
    SourceClass, SourceId, SourceKind,
};
use memory_core::limits::object_key;
use memory_core::manifest::{
    Change, Header, OwnerState, Timestamp, Writer, manifest_hash, parse_manifest,
};
use memory_core::privacy::{PolicyContext, Profile, ResolvedSourceFacts, rules_digest};
use proptest::prelude::*;
use proptest::test_runner::{Config, TestRunner};
use std::cell::RefCell;
use std::io::Write;
use std::ops::Range;
use std::rc::Rc;

#[path = "common/fs.rs"]
mod mem_fs;
#[path = "common/store.rs"]
mod mem_store;
use mem_fs::MemFs;
use mem_store::MemStore;

type R<T = ()> = Result<T, String>;
fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}
fn need<T>(value: Option<T>, what: &str) -> R<T> {
    value.ok_or_else(|| what.to_owned())
}
fn ensure(condition: bool, what: impl FnOnce() -> String) -> R {
    if condition { Ok(()) } else { Err(what()) }
}
fn sha(bytes: &[u8]) -> Sha256 {
    manifest_hash(bytes)
}
fn len64(n: usize) -> u64 {
    u64::try_from(n).unwrap_or(u64::MAX)
}

fn owner() -> R<Owner> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
        machine: MachineId::try_from("m".to_owned()).map_err(e)?,
        agent: Agent::Claude,
        session: SessionId::try_from("s1".to_owned()).map_err(e)?,
    }))
}
fn header() -> R<Header> {
    Ok(Header {
        owner: owner()?,
        captured_at: Timestamp::try_from("2026-11-07T10:30:00Z".to_owned()).map_err(e)?,
        writer: Writer {
            name: "memory".to_owned(),
            version: "0.1.0".to_owned(),
            commit: "abc".to_owned(),
        },
        launch: vec![],
    })
}
fn source(handle: u64) -> R<Source> {
    let path = format!("projects/p/s{handle}.jsonl");
    Ok(Source {
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
/// Deterministic bytes that are neither all alike nor a secret.
fn data(seed: u8, n: usize, salt: usize) -> Vec<u8> {
    (0..n)
        .map(|i| {
            let v = i * 31 + i / 7 + usize::from(seed) * 13 + salt;
            u8::try_from(v % 251).unwrap_or(0)
        })
        .collect()
}

// ------------------------------------------------------------------ the spy filesystem

#[derive(Debug, Clone, PartialEq, Eq)]
enum Ev {
    Read(u64, Range<u64>),
    Put(String),
}
type Log = Rc<RefCell<Vec<Ev>>>;

/// Cut the file to `cut` bytes right after serving the first read that starts at or after `from`.
struct Shrink {
    from: u64,
    cut: usize,
}

struct Spy {
    inner: MemFs,
    log: Log,
    stats: Vec<u64>,
    shrink: Option<Shrink>,
}
impl Spy {
    fn new() -> Self {
        Self {
            inner: MemFs::default(),
            log: Log::default(),
            stats: Vec::new(),
            shrink: None,
        }
    }
    fn reads(&self) -> Vec<(u64, Range<u64>)> {
        self.log
            .borrow()
            .iter()
            .filter_map(|ev| match ev {
                Ev::Read(h, r) => Some((*h, r.clone())),
                Ev::Put(_) => None,
            })
            .collect()
    }
}
impl FileSystem for Spy {
    type Error = ();
    fn stat(&mut self, source: u64) -> Result<Option<Stat>, ()> {
        self.stats.push(source);
        self.inner.stat(source)
    }
    fn read(&mut self, source: u64, range: Range<u64>) -> Result<Vec<u8>, ()> {
        let out = self.inner.read(source, range.clone())?;
        self.log.borrow_mut().push(Ev::Read(source, range.clone()));
        if self.shrink.as_ref().is_some_and(|s| range.start >= s.from)
            && let Some(s) = self.shrink.take()
            && let Some(file) = self.inner.files.get_mut(&source)
        {
            file.truncate(s.cut);
        }
        Ok(out)
    }
}

/// Logs each put before handing the body on; keeps nothing.
struct Tee<'a, K: PieceSink> {
    inner: &'a mut K,
    log: Log,
}
impl<K: PieceSink> PieceSink for Tee<'_, K> {
    fn put(&mut self, key: &str, body: Published) -> Result<(), Halted> {
        self.log.borrow_mut().push(Ev::Put(key.to_owned()));
        self.inner.put(key, body)
    }
    fn halted(&self) -> bool {
        self.inner.halted()
    }
}

// ------------------------------------------------------------------ one capture step, both paths

struct Side {
    fs: Spy,
    store: MemStore,
    locals: Locals,
    base: Option<Base>,
}
impl Side {
    fn new() -> Self {
        Self {
            fs: Spy::new(),
            store: MemStore::default(),
            locals: Locals::new(),
            base: None,
        }
    }
}

struct Out {
    outcomes: Vec<SourceOutcome>,
    audit: AuditCounts,
    changes: Vec<Change>,
    updates: Vec<Update>,
    log: Vec<Ev>,
}

/// One capture and commit. `stream` selects `capture_into` + `StoreSink` + `commit_staged`;
/// otherwise `capture` + `commit`.
fn run_step(side: &mut Side, stream: bool, now_ns: i64, pl: usize, sources: &[Source]) -> R<Out> {
    side.fs.log.borrow_mut().clear();
    let ctx = policy();
    let env = Env {
        header: header()?,
        context: &ctx,
        now_ns,
        piece_len: pl,
    };
    let empty = OwnerState::default();
    let state = side.base.as_ref().map_or(&empty, |b| &b.state);
    let log = side.fs.log.clone();
    let mut changes = Vec::new();
    let (got, landed) = if stream {
        let mut stop = || false;
        let mut sink = StoreSink::new(&mut side.store, &mut stop);
        let mut got = {
            let mut tee = Tee {
                inner: &mut sink,
                log,
            };
            capture_into(&mut side.fs, sources, &side.locals, state, &env, &mut tee)
        };
        let staged = sink.finish();
        ensure(staged.failed.is_none(), || {
            format!("sink failed: {:?}", staged.failed)
        })?;
        let landed = match got.commit.take() {
            Some(request) => {
                changes.clone_from(&request.changes);
                Some(
                    commit_staged(
                        &mut side.store,
                        side.base.as_ref(),
                        request,
                        staged.winners,
                        &mut KnownOurs::new(),
                        &mut |_| {},
                        &mut || false,
                    )
                    .map_err(e)?,
                )
            }
            None => None,
        };
        (got, landed)
    } else {
        let mut got = capture(&mut side.fs, sources, &side.locals, state, &env);
        let landed = match got.commit.take() {
            Some(request) => {
                changes.clone_from(&request.changes);
                Some(commit(&mut side.store, side.base.as_ref(), request, &mut |_| {}).map_err(e)?)
            }
            None => None,
        };
        (got, landed)
    };
    if let Some(end) = landed {
        side.base = Some(end.base());
    }
    for update in got.updates.clone() {
        update.apply(&mut side.locals);
    }
    let log = side.fs.log.borrow().clone();
    Ok(Out {
        outcomes: got.outcomes.into_iter().map(|(_, o)| o).collect(),
        audit: got.audit,
        changes,
        updates: got.updates,
        log,
    })
}

// ------------------------------------------------------------------ the oracle

/// `change` must be the one write of `full[from..]` (or of `full[..]` for an epoch).
fn check_write(
    change: &Change,
    full: &[u8],
    from: usize,
    epoch: bool,
    store: &MemStore,
    pl: usize,
) -> R {
    let (w, is_epoch) = match change {
        Change::Extend(w) => (w, false),
        Change::Epoch(w) => (w, true),
        other => return Err(format!("unexpected change {other:?}")),
    };
    ensure(is_epoch == epoch, || format!("kind: epoch={is_epoch}"))?;
    ensure(w.size == len64(full.len()), || format!("size {}", w.size))?;
    ensure(w.sha256 == sha(full), || "whole digest".to_owned())?;
    let chunks: Vec<&[u8]> = need(full.get(from..), "from")?.chunks(pl).collect();
    ensure(w.pieces.len() == chunks.len(), || {
        format!("{} pieces for {} chunks", w.pieces.len(), chunks.len())
    })?;
    for (i, (piece, chunk)) in w.pieces.iter().zip(chunks).enumerate() {
        let v = serde_json::to_value(piece).map_err(e)?;
        let field = |name: &str| v.get(name).cloned().unwrap_or_default();
        let digest = sha(chunk);
        let encoded = zstd::bulk::compress(chunk, 3).map_err(e)?;
        let expect = [
            ("offset", serde_json::json!(len64(from + i * pl))),
            ("len", serde_json::json!(len64(chunk.len()))),
            ("sha256", serde_json::json!(String::from(digest))),
            ("object", serde_json::json!(object_key(&digest))),
            ("encoding", serde_json::json!("zstd-3")),
            ("encoded_len", serde_json::json!(len64(encoded.len()))),
            (
                "encoded_sha256",
                serde_json::json!(String::from(sha(&encoded))),
            ),
        ];
        for (name, want) in expect {
            ensure(field(name) == want, || format!("piece {i} {name}: {v}"))?;
        }
        let (body, _) = need(store.objects.get(&object_key(&digest)), "piece object")?;
        ensure(*body == encoded, || format!("piece {i} body"))?;
    }
    Ok(())
}

/// The last index update must describe the stream `full` exactly.
fn check_index(out: &Out, full: &[u8]) -> R {
    let last = out.updates.iter().rev().find_map(|u| match u {
        Update::Index(_, state) => Some(state),
        _ => None,
    });
    let state = need(last, "an index update")?;
    let n = full.len();
    ensure(state.position.captured_to.get() == len64(n), || {
        "captured_to".to_owned()
    })?;
    ensure(state.position.whole == sha(full), || {
        "index whole".to_owned()
    })?;
    ensure(
        state.head == sha(need(full.get(..n.min(4096)), "head")?),
        || "head window".to_owned(),
    )?;
    ensure(
        state.tail == sha(need(full.get(n.saturating_sub(65_536)..), "tail")?),
        || "tail window".to_owned(),
    )?;
    let whole = Sha256State::empty().extend(full).map_err(e)?;
    ensure(state.whole_state == Some(whole), || {
        "whole_state".to_owned()
    })
}

/// Reads of a prefix-stable source are at most one piece, apart from at most two window reads
/// at the old committed boundary (head at most 4 KiB, tail at most 64 KiB).
fn check_reads(log: &[Ev], pl: usize, boundary: Option<u64>) -> R {
    let mut over = 0;
    for ev in log {
        if let Ev::Read(_, r) = ev {
            let len = r.end - r.start;
            if len > len64(pl) {
                over += 1;
                let window =
                    len <= 65_536 && (Some(r.end) == boundary || (r.start == 0 && len <= 4096));
                ensure(window, || format!("read {r:?} longer than piece_len {pl}"))?;
            }
        }
    }
    ensure(over <= 2, || format!("{over} reads longer than a piece"))
}

/// No two puts are adjacent: a piece is handed over before the next one is read.
fn check_interleave(log: &[Ev], pieces: usize) -> R {
    let puts = log.iter().filter(|ev| matches!(ev, Ev::Put(_))).count();
    ensure(puts == pieces, || {
        format!("{puts} puts for {pieces} pieces")
    })?;
    ensure(
        log.windows(2)
            .all(|w| !matches!(w, [Ev::Put(_), Ev::Put(_)])),
        || format!("two puts in a row: {log:?}"),
    )
}

// ------------------------------------------------------------------ the property

#[derive(Debug, Clone, Copy)]
enum Kind {
    Fresh,
    ExtendResumed,
    ExtendCold,
    Epoch,
    Adopt,
    Audit,
    RacedFresh,
    RacedExtend,
}
#[derive(Debug, Clone)]
struct Case {
    kind: Kind,
    pl: usize,
    a: usize,
    b: usize,
    seed: u8,
}

fn cases() -> impl Strategy<Value = Case> {
    let kind = prop_oneof![
        Just(Kind::Fresh),
        Just(Kind::ExtendResumed),
        Just(Kind::ExtendCold),
        Just(Kind::Epoch),
        Just(Kind::Adopt),
        Just(Kind::Audit),
        Just(Kind::RacedFresh),
        Just(Kind::RacedExtend),
    ];
    (
        kind,
        1usize..=64,
        (0usize..6, 0usize..=64),
        (0usize..6, 0usize..=64),
        any::<u8>(),
        0u8..10,
    )
        .prop_map(|(kind, pl, (ka, oa), (kb, ob), seed, big)| {
            let (mut pl, mut a, mut b) = (pl, ka * pl + oa % (pl + 1), kb * pl + ob % (pl + 1));
            match big {
                0 => {
                    pl = pl.max(16);
                    a = 4090 + oa % 12;
                }
                1 => {
                    pl = pl.max(32);
                    a = 65_530 + oa % 12;
                }
                _ => {}
            }
            match kind {
                Kind::Fresh => {}
                Kind::RacedFresh => a = a.max(pl + 1),
                Kind::RacedExtend => {
                    a = a.max(1);
                    b = b.max(pl + 1);
                }
                _ => a = a.max(1),
            }
            Case {
                kind,
                pl,
                a,
                b,
                seed,
            }
        })
}

struct Step {
    file: Vec<u8>,
    drop_whole: bool,
    clear_locals: bool,
    now_ns: i64,
    shrink: Option<Shrink>,
}
fn step(file: Vec<u8>, now_ns: i64) -> Step {
    Step {
        file,
        drop_whole: false,
        clear_locals: false,
        now_ns,
        shrink: None,
    }
}

fn scenario(c: &Case) -> R {
    let (t0, t1) = (10_000_000_000, 20_000_000_000);
    let a = data(c.seed, c.a, 0);
    let grown = || [a.clone(), data(c.seed, c.b, 99)].concat();
    let mut steps = vec![step(a.clone(), t0)];
    // (file after the last step, from, epoch) of the write the last step must produce.
    let (last_file, expect): (Vec<u8>, Option<(usize, bool)>) = match c.kind {
        Kind::Fresh => (a.clone(), Some((0, true))),
        Kind::RacedFresh => {
            if let Some(first) = steps.first_mut() {
                first.shrink = Some(Shrink { from: 0, cut: c.pl });
            }
            (a.clone(), None)
        }
        Kind::ExtendResumed | Kind::ExtendCold | Kind::Adopt | Kind::RacedExtend => {
            let file = grown();
            let mut next = step(file.clone(), t1);
            next.drop_whole = matches!(c.kind, Kind::ExtendCold);
            next.clear_locals = matches!(c.kind, Kind::Adopt);
            if matches!(c.kind, Kind::RacedExtend) {
                next.shrink = Some(Shrink {
                    from: len64(c.a),
                    cut: c.a + c.pl,
                });
            }
            steps.push(next);
            let expect = (c.b > 0 && !matches!(c.kind, Kind::RacedExtend)).then_some((c.a, false));
            (file, expect)
        }
        Kind::Epoch => {
            let mut file = grown();
            if let Some(first) = file.first_mut() {
                *first ^= 0xff;
            }
            steps.push(step(file.clone(), t1));
            (file, Some((0, true)))
        }
        Kind::Audit => {
            steps.push(step(a.clone(), t1));
            (a.clone(), None)
        }
    };
    let src = [source(1)?];
    let mut sides = [Side::new(), Side::new()];
    let mut outs: [Vec<Out>; 2] = [Vec::new(), Vec::new()];
    for (i, stream) in [false, true].into_iter().enumerate() {
        let side = need(sides.get_mut(i), "side")?;
        for s in &steps {
            side.fs.inner.set(1, &s.file);
            if s.drop_whole {
                for local in side.locals.values_mut() {
                    if let Some(ix) = &mut local.index {
                        ix.whole_state = None;
                    }
                }
            }
            if s.clear_locals {
                side.locals.clear();
            }
            side.fs.shrink = s.shrink.as_ref().map(|s| Shrink {
                from: s.from,
                cut: s.cut,
            });
            let out = run_step(side, stream, s.now_ns, c.pl, &src)?;
            need(outs.get_mut(i), "outs")?.push(out);
        }
    }
    let [collect_side, stream_side] = &sides;
    let [collect_outs, stream_outs] = &outs;
    // Same outcomes, audit counts, changes, index updates and reads on both paths.
    for (n, (x, y)) in collect_outs.iter().zip(stream_outs).enumerate() {
        ensure(x.outcomes == y.outcomes, || {
            format!("step {n} outcomes {:?} / {:?}", x.outcomes, y.outcomes)
        })?;
        ensure(x.audit == y.audit, || format!("step {n} audit"))?;
        ensure(
            format!("{:?}", x.changes) == format!("{:?}", y.changes),
            || format!("step {n} changes"),
        )?;
        ensure(
            format!("{:?}", x.updates) == format!("{:?}", y.updates),
            || format!("step {n} updates"),
        )?;
        let reads = |o: &Out| {
            o.log
                .iter()
                .filter(|ev| matches!(ev, Ev::Read(..)))
                .cloned()
                .collect::<Vec<_>>()
        };
        ensure(reads(x) == reads(y), || format!("step {n} reads"))?;
    }
    let (last_collect, last) = (
        need(collect_outs.last(), "out")?,
        need(stream_outs.last(), "out")?,
    );
    let _ = last_collect;
    let boundary = (steps.len() > 1).then(|| len64(c.a));
    check_reads(&last.log, c.pl, boundary)?;
    if matches!(c.kind, Kind::Audit) {
        ensure(last.audit.matched == 1, || {
            format!("audit {:?}", last.audit)
        })?;
        let mut at = 0;
        for ev in &last.log {
            if let Ev::Read(_, r) = ev {
                ensure(r.start == at, || format!("audit read gap at {at}: {r:?}"))?;
                at = r.end;
            }
        }
        ensure(at == len64(c.a), || {
            format!("audit covered {at} of {}", c.a)
        })?;
    }
    if matches!(c.kind, Kind::RacedFresh | Kind::RacedExtend) {
        ensure(last.outcomes == [SourceOutcome::Raced], || {
            format!("raced outcome {:?}", last.outcomes)
        })?;
        ensure(last.changes.is_empty(), || "raced change".to_owned())?;
        let meta = |m: &MemStore| {
            m.objects
                .iter()
                .filter(|(k, _)| !k.starts_with("objects/"))
                .map(|(k, v)| (k.clone(), v.clone()))
                .collect::<Vec<_>>()
        };
        ensure(
            meta(&stream_side.store) == meta(&collect_side.store),
            || "the raced streamed capture changed a manifest or current.json".to_owned(),
        )?;
        return Ok(());
    }
    ensure(
        collect_side.store.objects == stream_side.store.objects,
        || "the stores differ".to_owned(),
    )?;
    // First step: an epoch of the original file.
    let first = need(stream_outs.first(), "first")?;
    if let Some(change) = first.changes.first() {
        check_write(change, &a, 0, true, &stream_side.store, c.pl)?;
        check_interleave(&first.log, a.len().div_ceil(c.pl))?;
    } else {
        ensure(c.a == 0, || "first capture wrote nothing".to_owned())?;
    }
    match expect {
        Some((from, epoch)) if steps.len() > 1 || c.a > 0 => {
            ensure(last.changes.len() == 1, || {
                format!("{} changes", last.changes.len())
            })?;
            let change = need(last.changes.first(), "change")?;
            check_write(change, &last_file, from, epoch, &stream_side.store, c.pl)?;
            check_interleave(&last.log, (last_file.len() - from).div_ceil(c.pl))?;
            check_index(last, &last_file)?;
        }
        _ => {
            if steps.len() > 1 {
                ensure(last.changes.is_empty(), || "unexpected change".to_owned())?;
                ensure(last.outcomes == [SourceOutcome::Unchanged], || {
                    format!("outcomes {:?}", last.outcomes)
                })?;
            }
        }
    }
    Ok(())
}

#[test]
fn proptest_streamed_capture_equals_the_oracle_and_the_collect_path() {
    let mut runner = TestRunner::new(Config {
        failure_persistence: None,
        ..Config::default()
    });
    let result = runner.run(&cases(), |c| {
        scenario(&c).map_err(|m| TestCaseError::fail(format!("{c:?}: {m}")))
    });
    assert!(result.is_ok(), "{result:?}");
}

// ------------------------------------------------------------------ stop and failure

fn two_files(side: &mut Side) {
    side.fs.inner.set(1, b"0123456789abcdef");
    side.fs.inner.set(2, b"second file");
}
fn env_of(ctx: &PolicyContext) -> R<Env<'_>> {
    Ok(Env {
        header: header()?,
        context: ctx,
        now_ns: 10_000_000_000,
        piece_len: 4,
    })
}

#[test]
fn a_stop_between_pieces_ends_with_stopped_and_no_manifest_or_current() -> R {
    let mut side = Side::new();
    two_files(&mut side);
    let (ctx, empty) = (policy(), OwnerState::default());
    let env = env_of(&ctx)?;
    let mut calls = 0;
    let mut stop = || {
        calls += 1;
        calls >= 3
    };
    let mut sink = StoreSink::new(&mut side.store, &mut stop);
    let sources = [source(1)?, source(2)?];
    let got = capture_into(
        &mut side.fs,
        &sources,
        &side.locals,
        &empty,
        &env,
        &mut sink,
    );
    let staged = sink.finish();
    assert!(
        matches!(staged.failed, Some(Failure::Stopped)),
        "{:?}",
        staged.failed
    );
    assert!(got.commit.is_none());
    assert_eq!(
        side.store.objects.len(),
        2,
        "two pieces stored before the stop"
    );
    assert!(side.store.objects.keys().all(|k| k.starts_with("objects/")));
    assert!(
        !side
            .store
            .objects
            .keys()
            .any(|k| k.ends_with("current.json"))
    );
    assert_eq!(
        got.outcomes
            .iter()
            .map(|(_, o)| o.clone())
            .collect::<Vec<_>>(),
        [SourceOutcome::Unavailable, SourceOutcome::Unavailable]
    );
    assert!(
        side.fs.reads().iter().all(|(h, _)| *h == 1),
        "source 2 was read"
    );
    Ok(())
}

/// A store whose n-th `put_if_absent` fails.
struct FailPut {
    inner: MemStore,
    fail_at: usize,
    puts: usize,
}
impl Store for FailPut {
    fn put_if_absent(&mut self, key: &str, body: Published) -> Result<ETag, StoreError> {
        self.puts += 1;
        if self.puts == self.fail_at {
            return Err(StoreError::Offline);
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
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        self.inner.get(key)
    }
}

#[test]
fn a_put_failure_on_piece_k_stops_reading_and_builds_no_manifest() -> R {
    let mut side = Side::new();
    two_files(&mut side);
    let (ctx, empty) = (policy(), OwnerState::default());
    let env = env_of(&ctx)?;
    let mut store = FailPut {
        inner: MemStore::default(),
        fail_at: 2,
        puts: 0,
    };
    let mut stop = || false;
    let mut sink = StoreSink::new(&mut store, &mut stop);
    let sources = [source(1)?, source(2)?];
    let got = capture_into(
        &mut side.fs,
        &sources,
        &side.locals,
        &empty,
        &env,
        &mut sink,
    );
    let staged = sink.finish();
    assert!(
        matches!(staged.failed, Some(Failure::NotLanded(_))),
        "{:?}",
        staged.failed
    );
    assert!(got.commit.is_none());
    assert_eq!(
        got.outcomes
            .iter()
            .map(|(_, o)| o.clone())
            .collect::<Vec<_>>(),
        [SourceOutcome::Unavailable, SourceOutcome::Unavailable]
    );
    assert_eq!(store.puts, 2, "no put after the failed one");
    assert_eq!(store.inner.objects.len(), 1);
    assert!(
        side.fs.reads().iter().all(|(h, _)| *h == 1),
        "source 2 was read"
    );
    assert!(!side.fs.stats.contains(&2), "source 2 was examined");
    Ok(())
}

/// Every JSON object in `v` that has an `encoded_sha256`, in document order.
fn pieces_in(v: &serde_json::Value, out: &mut Vec<serde_json::Value>) {
    match v {
        serde_json::Value::Object(map) => {
            if map.contains_key("encoded_sha256") {
                out.push(v.clone());
            }
            map.values().for_each(|x| pieces_in(x, out));
        }
        serde_json::Value::Array(items) => items.iter().for_each(|x| pieces_in(x, out)),
        _ => {}
    }
}

#[test]
fn a_412_on_a_streamed_piece_adopts_the_stored_encoding_into_the_manifest() -> R {
    let content = b"0123456789abcdef".to_vec();
    let chunk = need(content.get(..4), "chunk")?;
    let key = object_key(&sha(chunk));
    // The same bytes under another zstd frame (with a checksum): another stored encoding.
    let mut enc = zstd::stream::write::Encoder::new(Vec::new(), 3).map_err(e)?;
    enc.include_checksum(true).map_err(e)?;
    enc.write_all(chunk).map_err(e)?;
    let winner = enc.finish().map_err(e)?;
    ensure(winner != zstd::bulk::compress(chunk, 3).map_err(e)?, || {
        "the foreign frame equals ours".to_owned()
    })?;
    let mut stores = Vec::new();
    for stream in [false, true] {
        let mut side = Side::new();
        side.fs.inner.set(1, &content);
        side.store.write(&key, winner.clone());
        run_step(&mut side, stream, 10_000_000_000, 4, &[source(1)?])?;
        let (body, _) = need(side.store.objects.get(&key), "winner")?;
        assert_eq!(*body, winner, "the winner is not overwritten");
        let mut found = Vec::new();
        for (k, (body, _)) in &side.store.objects {
            if !k.starts_with("objects/")
                && let Ok(m) = parse_manifest(body)
            {
                pieces_in(&serde_json::to_value(&m).map_err(e)?, &mut found);
            }
        }
        let first = need(found.first(), "a piece in the manifest")?;
        assert_eq!(first.get("object"), Some(&serde_json::json!(key)));
        assert_eq!(first.get("len"), Some(&serde_json::json!(4)));
        assert_eq!(
            first.get("encoded_len"),
            Some(&serde_json::json!(winner.len()))
        );
        assert_eq!(
            first.get("encoded_sha256"),
            Some(&serde_json::json!(String::from(sha(&winner))))
        );
        stores.push(side.store.objects);
    }
    assert!(
        stores.first() == stores.last(),
        "both paths store the same objects"
    );
    Ok(())
}

// ------------------------------------------------------------------ one piece alive, read lengths

/// Takes each body and drops it at once, so at most the body being handed over is alive.
#[derive(Default)]
struct DropSink {
    lens: Vec<usize>,
}
impl PieceSink for DropSink {
    fn put(&mut self, _key: &str, body: Published) -> Result<(), Halted> {
        self.lens.push(body.as_ref().len());
        drop(body);
        Ok(())
    }
    fn halted(&self) -> bool {
        false
    }
}

#[test]
fn each_piece_is_handed_over_before_the_next_is_read() -> R {
    let mut side = Side::new();
    side.fs.inner.set(1, &data(3, 100, 0));
    let (ctx, empty) = (policy(), OwnerState::default());
    let env = Env {
        header: header()?,
        context: &ctx,
        now_ns: 10_000_000_000,
        piece_len: 8,
    };
    let mut drop_sink = DropSink::default();
    let mut tee = Tee {
        inner: &mut drop_sink,
        log: side.fs.log.clone(),
    };
    capture_into(
        &mut side.fs,
        &[source(1)?],
        &side.locals,
        &empty,
        &env,
        &mut tee,
    );
    assert_eq!(drop_sink.lens.len(), 13);
    assert!(
        drop_sink.lens.iter().all(|n| *n <= 64),
        "an encoded piece is bounded"
    );
    let log = side.fs.log.borrow().clone();
    // Strict alternation: read k, put k, read k+1, put k+1 ...
    assert_eq!(log.len(), 26, "{log:?}");
    for pair in log.chunks(2) {
        assert!(
            matches!(pair, [Ev::Read(_, r), Ev::Put(_)] if r.end - r.start <= 8),
            "{pair:?}"
        );
    }
    Ok(())
}

fn max_read(out: &Out) -> u64 {
    out.log
        .iter()
        .filter_map(|ev| match ev {
            Ev::Read(_, r) => Some(r.end - r.start),
            Ev::Put(_) => None,
        })
        .max()
        .unwrap_or(0)
}

#[test]
fn the_audit_read_and_the_adopt_read_are_at_most_one_piece_long() -> R {
    let file = data(5, 1000, 0);
    let src = [source(1)?];
    // Audit: a second capture of an unchanged file whose audit is due.
    let mut side = Side::new();
    side.fs.inner.set(1, &file);
    run_step(&mut side, true, 10_000_000_000, 8, &src)?;
    let audited = run_step(&mut side, true, 20_000_000_000, 8, &src)?;
    assert_eq!(audited.audit.matched, 1, "{:?}", audited.audit);
    assert_eq!(max_read(&audited), 8);
    assert!(
        audited
            .log
            .iter()
            .filter(|ev| matches!(ev, Ev::Read(..)))
            .count()
            >= 125
    );
    // Adopt: the local index is lost and the file has grown.
    let mut side = Side::new();
    side.fs.inner.set(1, &file);
    run_step(&mut side, true, 10_000_000_000, 8, &src)?;
    side.locals.clear();
    side.fs
        .inner
        .set(1, &[file.clone(), data(5, 40, 9)].concat());
    let adopted = run_step(&mut side, true, 20_000_000_000, 8, &src)?;
    assert_eq!(adopted.outcomes, [SourceOutcome::Planned]);
    assert_eq!(max_read(&adopted), 8);
    assert!(
        adopted
            .log
            .iter()
            .filter(|ev| matches!(ev, Ev::Put(_)))
            .count()
            == 5
    );
    Ok(())
}
