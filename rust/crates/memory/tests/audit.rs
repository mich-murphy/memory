//! The bounded audit (DESIGN.md 11.4, measure 2) through the real `Daemon`, with a spying
//! fake filesystem (byte counts, corrupted reads, a source that moves between two stats), the
//! fake store and a settable wall clock. Restores go through `memory-reader`.
use memory::audit::{AUDIT_INTERVAL_NS, AuditCounts, Mismatch, due, matched, mismatch};
use memory::capture::{Published, Source};
use memory::daemon::ParkedReason;
use memory::daemon::{Daemon, IndexBody, Parts, RoundReport, StartOptions, StatusBody};
use memory::environment::{FileSystem, Stat, Store, StoreError, Stored};
use memory::state::{Loaded, StateFile, SyncPolicy, read_durable};
use memory_core::domain::{
    Agent, ArchiveId, AuditState, ETag, MachineId, Owner, ParkReason, SessionId, SessionKey,
    SourceClass, SourceId, SourceKind, SourceState,
};
use memory_core::privacy::{PolicyContext, Profile, ResolvedSourceFacts, rules_digest};
use std::collections::VecDeque;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use std::sync::atomic::{AtomicU64, Ordering};

#[path = "common/fs.rs"]
mod mem_fs;
#[path = "common/store.rs"]
mod mem_store;
#[path = "common/clock.rs"]
mod test_clock;
use mem_fs::MemFs;
use mem_store::MemStore;
use test_clock::{TestClock, WALL};

type R<T = ()> = Result<T, String>;
fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}

const SECOND: i128 = 1_000_000_000;
const HOUR: i128 = 3_600 * SECOND;
/// Bigger than the head window (4 KiB) plus the tail window (64 KiB), so a byte between them
/// is seen by neither.
const SIZE: usize = 200 * 1024;
/// Bytes of the head and tail window reads at the old boundary.
const WINDOWS: u64 = 4096 + 65536;

struct Tmp(PathBuf);
impl Tmp {
    fn new() -> R<Self> {
        static N: AtomicU64 = AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!(
            "memory-audit-{}-{}",
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
    fn config(&self) -> R<PathBuf> {
        let token = self.0.join("token");
        fs::write(&token, "x").map_err(e)?;
        fs::set_permissions(&token, fs::Permissions::from_mode(0o600)).map_err(e)?;
        let path = self.0.join("config.toml");
        fs::write(
            &path,
            format!(
                "archive_id = \"a\"\nmachine_id = \"m\"\nendpoint = \"http://127.0.0.1:9\"\nbucket = \"bucket-1\"\ntoken_path = \"{}\"\n",
                token.display()
            ),
        )
        .map_err(e)?;
        Ok(path)
    }
}
impl Drop for Tmp {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn pattern(len: usize) -> Vec<u8> {
    (0..len)
        .map(|i| u8::try_from((i * 7 + 3) % 251).unwrap_or(0))
        .collect()
}

/// A filesystem that counts bytes and can fault: `flips` corrupts the next whole-file reads
/// (one byte position each), `touch` moves the mtime right after the next whole-file read.
#[derive(Default)]
struct Spy {
    inner: MemFs,
    bytes_read: u64,
    stats: u64,
    flips: VecDeque<usize>,
    touch: bool,
}
impl FileSystem for Spy {
    type Error = ();
    fn stat(&mut self, source: u64) -> Result<Option<Stat>, ()> {
        self.stats += 1;
        self.inner.stat(source)
    }
    fn read(&mut self, source: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, ()> {
        let len = self
            .inner
            .files
            .get(&source)
            .map_or(0, |b| u64::try_from(b.len()).unwrap_or(0));
        let whole = range.start == 0 && range.end == len;
        let mut bytes = self.inner.read(source, range)?;
        if whole {
            if let Some(at) = self.flips.pop_front()
                && let Some(byte) = bytes.get_mut(at)
            {
                *byte ^= 0xff;
            }
            if std::mem::take(&mut self.touch) {
                let mtime = self.inner.mtimes.get(&source).copied().unwrap_or(1_000);
                self.inner.mtimes.insert(source, mtime + 1);
            }
        }
        self.bytes_read = self
            .bytes_read
            .saturating_add(u64::try_from(bytes.len()).unwrap_or(u64::MAX));
        Ok(bytes)
    }
}

/// A store whose puts can be made to fail.
#[derive(Default)]
struct Flaky {
    inner: MemStore,
    fail_puts: bool,
}
impl Store for Flaky {
    fn put_if_absent(&mut self, key: &str, body: Published) -> Result<ETag, StoreError> {
        if self.fail_puts {
            return Err(StoreError::Server);
        }
        self.inner.put_if_absent(key, body)
    }
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &ETag,
        body: Published,
    ) -> Result<ETag, StoreError> {
        if self.fail_puts {
            return Err(StoreError::Server);
        }
        self.inner.put_if_match(key, etag, body)
    }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        self.inner.get(key)
    }
}

type Srcs = Box<dyn FnMut() -> Vec<(Owner, Source)>>;
type D = Daemon<Spy, Flaky, TestClock, Srcs>;

struct Rig {
    tmp: Tmp,
    cfg: PathBuf,
    list: Vec<(Owner, Source)>,
    d: Option<D>,
    owner: Owner,
    id: SourceId,
    mono: u64,
    last: RoundReport,
    /// Log lines (without the timestamp) and stats of the last round.
    log: Vec<String>,
    stats: u64,
}

fn boot(tmp: &Tmp, cfg: &std::path::Path, fs: Spy, store: Flaky, list: &[(Owner, Source)]) -> R<D> {
    let list = list.to_vec();
    let sources: Srcs = Box::new(move || list.clone());
    let parts = Parts {
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
    };
    Daemon::start_with(
        &tmp.root(),
        cfg,
        parts,
        StartOptions {
            sync: SyncPolicy::None,
        },
    )
    .map_err(|x| x.code().to_owned())
}

impl Rig {
    fn new(bytes: &[u8]) -> R<Self> {
        let tmp = Tmp::new()?;
        let cfg = tmp.config()?;
        let owner = Owner::Session(SessionKey {
            archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
            machine: MachineId::try_from("m".to_owned()).map_err(e)?,
            agent: Agent::Claude,
            session: SessionId::try_from("s1".to_owned()).map_err(e)?,
        });
        let id = SourceId::try_from("src1".to_owned()).map_err(e)?;
        let path = "projects/p/s1.jsonl".to_owned();
        let source = Source {
            handle: 1,
            id: id.clone(),
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
        };
        let list = vec![(owner.clone(), source)];
        let mut spy = Spy::default();
        spy.inner.set(1, bytes);
        let d = boot(&tmp, &cfg, spy, Flaky::default(), &list)?;
        Ok(Self {
            tmp,
            cfg,
            list,
            d: Some(d),
            owner,
            id,
            mono: 0,
            last: RoundReport::default(),
            log: Vec::new(),
            stats: 0,
        })
    }
    fn d(&mut self) -> R<&mut D> {
        self.d.as_mut().ok_or_else(|| "no daemon".to_owned())
    }
    /// Stop the daemon without a shutdown flush and start a new one on the same state, files
    /// and store.
    fn restart(&mut self) -> R {
        let mut old = self.d.take().ok_or("no daemon")?;
        let fs = std::mem::take(old.fs_mut());
        let store = std::mem::take(old.store_mut());
        drop(old);
        self.d = Some(boot(&self.tmp, &self.cfg, fs, store, &self.list)?);
        self.mono = 0;
        Ok(())
    }
    /// The audit counts of the last round.
    fn counts(&self) -> (u64, u64, u64) {
        let a = self.last.audit;
        (a.matched, a.mismatched, a.ineligible)
    }
    /// The cumulative counts in the daemon's status.
    fn status(&mut self) -> R<(u64, u64, u64)> {
        let a = self.d()?.status().audit;
        Ok((a.matched, a.mismatched, a.ineligible))
    }
    /// One round at wall time `WALL + wall`; the monotonic clock moves 61 s, so the index is
    /// written every round. Returns the bytes the round read through the seam.
    fn round(&mut self, wall: i128) -> R<u64> {
        self.mono += 61_000_000_000;
        let (mono, now) = (self.mono, WALL + wall);
        let d = self.d()?;
        d.clock_mut().mono = mono;
        d.clock_mut().wall = now;
        let (before, stats) = (d.fs_mut().bytes_read, d.fs_mut().stats);
        let mut report = None;
        let text = memory::log::render(|| report = Some(d.reconcile_once()));
        let report = report.ok_or("no round")?.map_err(e)?;
        let read = d.fs_mut().bytes_read - before;
        self.stats = d.fs_mut().stats - stats;
        self.log = text
            .lines()
            .map(|l| l.split_once(' ').unwrap_or_default().1.to_owned())
            .collect();
        self.last = report;
        Ok(read)
    }
    fn file(&mut self) -> R<&mut MemFs> {
        Ok(&mut self.d()?.fs_mut().inner)
    }
    fn state(&self) -> R<SourceState> {
        let loaded = read_durable(
            &self.tmp.root().join(StateFile::Index.name()),
            StateFile::Index.schema(),
        );
        let Loaded::Present(stored) = loaded else {
            return Err("index missing".to_owned());
        };
        let body: IndexBody = serde_json::from_slice(&stored.body).map_err(e)?;
        body.sources
            .get(&self.id)
            .and_then(|s| s.index.clone())
            .ok_or_else(|| "no index entry".to_owned())
    }
    fn audit(&self) -> R<AuditState> {
        Ok(self.state()?.audit)
    }
    /// The latest epoch restored through `memory-reader`, and the number of epochs.
    fn restore(&mut self) -> R<(Vec<u8>, usize)> {
        let id = self.id.clone();
        self.restore_of(&id)
    }
    fn restore_of(&mut self, id: &SourceId) -> R<(Vec<u8>, usize)> {
        let store = &mut self.d.as_mut().ok_or("no daemon")?.store_mut().inner;
        let fetch = |key: &str| store.objects.get(key).map(|(body, _)| body.clone());
        let folded = memory_reader::read_owner(&self.owner, |key| Ok::<_, ()>(fetch(key)))
            .map_err(e)?
            .ok_or("no chain")?;
        let entry = folded.state.sources.get(id).ok_or("no entry")?;
        let bytes =
            memory_reader::restore_source(entry, |key| fetch(key).ok_or("missing")).map_err(e)?;
        Ok((bytes, entry.epochs.len()))
    }
}

fn at(offset: i128) -> R<Option<i64>> {
    Ok(Some(i64::try_from(WALL + offset).map_err(e)?))
}

/// Guarantee: the first quiet round audits `[0, size)` once and records it; the next reads
/// no source bytes.
#[test]
fn first_quiet_round_audits_once_then_the_next_reads_nothing() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    assert_eq!(r.round(0)?, SIZE as u64, "capture reads the stream once");
    assert_eq!(r.audit()?.last_checked_at_ns, None);
    assert_eq!(r.round(SECOND)?, SIZE as u64, "the audit reads [0, size)");
    assert_eq!(r.audit()?.last_checked_at_ns, at(SECOND)?);
    assert_eq!(r.round(2 * SECOND)?, 0, "audited and not captured since");
    assert_eq!(r.audit()?.last_checked_at_ns, at(SECOND)?);
    assert_eq!(r.restore()?, (data, 1));
    Ok(())
}

/// Guarantee: an append after an audit reads the windows and the delta only, and an extension
/// keeps the last audit time.
#[test]
fn append_after_an_audit_reads_no_full_stream_and_keeps_the_audit_time() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    r.round(SECOND)?;
    let mut grown = data;
    grown.extend_from_slice(&pattern(1_000));
    r.file()?.set(1, &grown);
    assert_eq!(r.round(2 * SECOND)?, WINDOWS + 1_000);
    let audit = r.audit()?;
    assert_eq!(
        audit.last_checked_at_ns,
        at(SECOND)?,
        "kept by an extension"
    );
    assert_eq!(audit.consecutive_failures, 0);
    assert_eq!(r.round(3 * SECOND)?, 0, "no re-hash after the append");
    assert_eq!(r.restore()?, (grown, 1));
    Ok(())
}

/// Guarantee: the cheap pre-check. A due audit of a source whose stat already differs from the
/// stored one is ineligible without another stat or any read: the round's own stat is the only
/// one, and the append proceeds through the window check.
#[test]
fn a_changed_stat_is_ineligible_before_any_audit_stat_or_read() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    let mut grown = data;
    grown.extend_from_slice(&pattern(1_000));
    r.file()?.set(1, &grown);
    let stats = r.d()?.fs_mut().stats;
    assert_eq!(r.round(SECOND)?, WINDOWS + 1_000, "windows and delta only");
    assert_eq!(r.d()?.fs_mut().stats - stats, 1, "no audit stat");
    assert_eq!(r.audit()?.last_checked_at_ns, None);
    assert_eq!(r.restore()?, (grown, 1));
    Ok(())
}

/// Guarantee: a byte rewritten strictly between the windows with growth passes the window
/// check (the design's accepted window); the next audit finds it and a new epoch restores to
/// the file. The match after it zeroes the failure count.
#[test]
fn rewrite_between_the_windows_with_growth_is_found_by_the_audit() -> R {
    let old = pattern(SIZE);
    let mut r = Rig::new(&old)?;
    r.round(0)?;
    let mut new = old.clone();
    *new.get_mut(50_000).ok_or("byte")? ^= 0xff;
    new.extend_from_slice(&pattern(1_000));
    r.file()?.set(1, &new);
    r.round(SECOND)?;
    let mut accepted = old;
    accepted.extend_from_slice(&pattern(1_000));
    assert_eq!(
        r.restore()?,
        (accepted, 1),
        "the extension the windows accept holds the old prefix"
    );
    assert_eq!(r.audit()?.last_checked_at_ns, None);
    r.round(2 * SECOND)?;
    assert_eq!(r.restore()?, (new, 2), "the audit started a new epoch");
    let audit = r.audit()?;
    assert_eq!(
        audit.last_checked_at_ns, None,
        "a new epoch clears the audit"
    );
    assert_eq!(audit.consecutive_failures, 1, "counted on the new epoch");
    assert_eq!(
        r.round(3 * SECOND)?,
        SIZE as u64 + 1_000,
        "audit of the new epoch"
    );
    let audit = r.audit()?;
    assert_eq!(audit.last_checked_at_ns, at(3 * SECOND)?);
    assert_eq!(audit.consecutive_failures, 0);
    Ok(())
}

/// Guarantee: a same-size rewrite between the windows is stored as a stat refresh (which
/// clears the audit time), and the next round audits and starts a new epoch.
#[test]
fn same_size_rewrite_is_refreshed_then_audited() -> R {
    let old = pattern(SIZE);
    let mut r = Rig::new(&old)?;
    r.round(0)?;
    r.round(SECOND)?;
    assert_eq!(r.audit()?.last_checked_at_ns, at(SECOND)?);
    let mut new = old;
    *new.get_mut(50_000).ok_or("byte")? ^= 0xff;
    r.file()?.set(1, &new);
    let mtime = r.file()?.mtimes.get(&1).copied().ok_or("mtime")?;
    assert_eq!(r.round(2 * SECOND)?, WINDOWS, "windows only, not due yet");
    let state = r.state()?;
    assert_eq!(state.observed.mtime_ns, mtime, "the refresh is stored");
    assert_eq!(state.audit.last_checked_at_ns, None, "and clears the audit");
    assert_eq!(r.restore()?.1, 1, "no epoch yet");
    r.round(3 * SECOND)?;
    assert_eq!(r.restore()?, (new, 2));
    Ok(())
}

/// Guarantee: a refresh that only settles a racy stat (same mtime) does not clear the audit
/// time, so an append is not followed by a full re-hash.
#[test]
fn settling_a_racy_stat_keeps_the_audit_time() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    let racy = i64::try_from(WALL).map_err(e)?;
    r.file()?.mtimes.insert(1, racy);
    r.round(0)?;
    assert_eq!(
        r.round(SECOND)?,
        SIZE as u64 + WINDOWS,
        "audit, then the racy windows"
    );
    assert_eq!(r.audit()?.last_checked_at_ns, at(SECOND)?);
    assert_eq!(r.round(5 * SECOND)?, WINDOWS, "still racy: windows again");
    assert_eq!(r.audit()?.last_checked_at_ns, at(SECOND)?, "not cleared");
    assert_eq!(r.round(6 * SECOND)?, 0, "settled");
    Ok(())
}

/// Guarantee: a first mismatch that the independent second read clears is a match.
#[test]
fn a_mismatch_the_second_read_clears_is_a_match() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    r.d()?.fs_mut().flips.push_back(10);
    assert_eq!(r.round(SECOND)?, 2 * SIZE as u64, "two audit reads");
    let audit = r.audit()?;
    assert_eq!(audit.last_checked_at_ns, at(SECOND)?);
    assert_eq!(audit.consecutive_failures, 0);
    assert_eq!(r.restore()?, (data, 1), "no new epoch");
    Ok(())
}

/// Guarantee: two reads that differ from each other are no verdict.
#[test]
fn two_reads_that_differ_are_ineligible() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    r.d()?.fs_mut().flips.extend([10, 20]);
    assert_eq!(r.round(SECOND)?, 2 * SIZE as u64);
    assert_eq!(r.audit()?.last_checked_at_ns, None);
    assert_eq!(r.restore()?, (data, 1), "no new epoch");
    Ok(())
}

/// Guarantee: two reads that agree on a different digest are a stable mismatch.
#[test]
fn two_equal_corrupt_reads_are_a_stable_mismatch() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    r.d()?.fs_mut().flips.extend([10, 10]);
    r.round(SECOND)?;
    let (restored, epochs) = r.restore()?;
    assert_eq!(
        (restored, epochs),
        (data, 2),
        "a new epoch, read from the clean disk"
    );
    assert_eq!(r.audit()?.consecutive_failures, 1);
    Ok(())
}

/// Guarantee: a source that changes between the two stats of the read is ineligible: no
/// epoch, no audit time; the next stable round audits.
#[test]
fn a_source_that_moves_between_the_stats_is_ineligible() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    r.d()?.fs_mut().touch = true;
    r.round(SECOND)?;
    assert_eq!(r.audit()?.last_checked_at_ns, None, "no verdict");
    assert_eq!(r.restore()?.1, 1, "no epoch");
    r.round(2 * SECOND)?;
    assert_eq!(r.audit()?.last_checked_at_ns, None, "stat refresh round");
    r.round(3 * SECOND)?;
    assert_eq!(
        r.audit()?.last_checked_at_ns,
        at(3 * SECOND)?,
        "stable: audited"
    );
    assert_eq!(r.restore()?, (data, 1));
    Ok(())
}

/// The due rule through the daemon: an idle audited source is never re-read; a source captured
/// since its audit is audited at exactly 24 h, not before, and at once when the wall clock is
/// behind the audit.
#[test]
fn due_rule_through_the_daemon() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    r.round(SECOND)?;
    assert_eq!(
        r.round(26 * HOUR)?,
        0,
        "audited and idle: 25 h later, not due"
    );
    let mut grown = data;
    grown.extend_from_slice(&pattern(1_000));
    r.file()?.set(1, &grown);
    assert_eq!(r.round(27 * HOUR)?, WINDOWS + 1_000);
    let size = grown.len() as u64;
    let last = SECOND;
    assert_eq!(
        r.round(last + 24 * HOUR - 1)?,
        0,
        "23 h 59 m 59.999999999 s"
    );
    assert_eq!(r.round(last + 24 * HOUR)?, size, "24 h");
    assert_eq!(r.audit()?.last_checked_at_ns, at(last + 24 * HOUR)?);
    let mut more = grown;
    more.extend_from_slice(&pattern(10));
    r.file()?.set(1, &more);
    r.round(last + 25 * HOUR)?;
    assert_eq!(
        r.round(last + 23 * HOUR)?,
        more.len() as u64,
        "wall clock behind the last audit"
    );
    Ok(())
}

/// The due rule as a table.
#[test]
fn due_rule_table() {
    let audited = |last| AuditState {
        last_checked_at_ns: Some(last),
        consecutive_failures: 0,
        first_failure_at_ns: None,
    };
    let day = AUDIT_INTERVAL_NS;
    let never = AuditState {
        last_checked_at_ns: None,
        consecutive_failures: 0,
        first_failure_at_ns: None,
    };
    assert!(due(&never, 0, 0), "never audited");
    assert!(due(&never, 5, 1), "never audited, whatever the clock");
    assert!(
        !due(&audited(100), 100, 100 + 25 * 3_600_000_000_000),
        "not captured since"
    );
    assert!(
        !due(&audited(100), 50, 100 + 25 * 3_600_000_000_000),
        "stat older than audit"
    );
    assert!(
        !due(&audited(100), 101, 100 + day - 1),
        "23 h 59 m 59.999999999 s"
    );
    assert!(due(&audited(100), 101, 100 + day), "24 h");
    assert!(due(&audited(100), 101, 99), "wall clock behind the audit");
    assert!(
        !due(&audited(100), 100, 99),
        "behind, but not captured since"
    );
}

/// Guarantee: a match is counted on the round that reaches it and on no round around it; the
/// status sums rounds and survives a restart with an intact `status.json`.
#[test]
fn a_match_is_counted_once_in_the_round_and_in_the_status() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    assert_eq!((r.counts(), r.status()?), ((0, 0, 0), (0, 0, 0)));
    r.round(SECOND)?;
    assert_eq!((r.counts(), r.status()?), ((1, 0, 0), (1, 0, 0)));
    r.round(2 * SECOND)?;
    assert_eq!((r.counts(), r.status()?), ((0, 0, 0), (1, 0, 0)));
    r.restart()?;
    assert_eq!(r.status()?, (1, 0, 0), "intact status.json");
    r.round(3 * SECOND)?;
    assert_eq!((r.counts(), r.status()?), ((0, 0, 0), (1, 0, 0)));
    Ok(())
}

/// Guarantee: a first mismatch the second read clears counts one match, not a mismatch.
#[test]
fn a_cleared_first_mismatch_counts_one_match() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    r.d()?.fs_mut().flips.push_back(10);
    r.round(SECOND)?;
    assert_eq!((r.counts(), r.status()?), ((1, 0, 0), (1, 0, 0)));
    Ok(())
}

/// Guarantee: a stable mismatch counts one mismatch; the audit of the new epoch then matches.
#[test]
fn a_stable_mismatch_is_counted_then_the_new_epoch_matches() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    r.d()?.fs_mut().flips.extend([10, 10]);
    r.round(SECOND)?;
    assert_eq!((r.counts(), r.status()?), ((0, 1, 0), (0, 1, 0)));
    r.round(2 * SECOND)?;
    assert_eq!((r.counts(), r.status()?), ((1, 0, 0), (1, 1, 0)));
    r.round(3 * SECOND)?;
    assert_eq!((r.counts(), r.status()?), ((0, 0, 0), (1, 1, 0)));
    Ok(())
}

/// Guarantee: a rewrite between the windows with growth is counted as a mismatch by the audit.
#[test]
fn a_between_windows_rewrite_is_counted_as_a_mismatch() -> R {
    let old = pattern(SIZE);
    let mut r = Rig::new(&old)?;
    r.round(0)?;
    let mut new = old;
    *new.get_mut(50_000).ok_or("byte")? ^= 0xff;
    new.extend_from_slice(&pattern(1_000));
    r.file()?.set(1, &new);
    r.round(SECOND)?;
    assert_eq!(
        r.counts(),
        (0, 0, 1),
        "the windows accept the extension; the due audit sees a changed stat"
    );
    r.round(2 * SECOND)?;
    assert_eq!(
        (r.counts(), r.status()?),
        ((0, 1, 0), (0, 1, 1)),
        "the status also holds the earlier pre-check round"
    );
    Ok(())
}

/// Guarantee: each way a due audit reaches no verdict counts one ineligible audit.
#[test]
fn ineligible_audits_are_counted() -> R {
    let data = pattern(SIZE);
    // Two reads that differ.
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    r.d()?.fs_mut().flips.extend([10, 20]);
    r.round(SECOND)?;
    assert_eq!((r.counts(), r.status()?), ((0, 0, 1), (0, 0, 1)));
    // The source moves between the stats.
    r.d()?.fs_mut().touch = true;
    r.round(2 * SECOND)?;
    assert_eq!(r.counts(), (0, 0, 1));
    r.round(3 * SECOND)?;
    assert_eq!(
        r.counts(),
        (0, 0, 1),
        "stale stat: pre-check, then refreshed"
    );
    r.round(4 * SECOND)?;
    assert_eq!((r.counts(), r.status()?), ((1, 0, 0), (1, 0, 3)));
    // The stat pre-check.
    let mut grown = data;
    grown.extend_from_slice(&pattern(1_000));
    let mut p = Rig::new(&pattern(SIZE))?;
    p.round(0)?;
    p.file()?.set(1, &grown);
    p.round(SECOND)?;
    assert_eq!((p.counts(), p.status()?), ((0, 0, 1), (0, 0, 1)));
    Ok(())
}

/// An old `status.json` without the `audit` key still loads, with zero counts.
#[test]
fn an_old_status_without_audit_loads() -> R {
    let body = r#"{"updated_at":"t","rounds":3,"host_paused":false,"parked":[]}"#;
    let status: StatusBody = serde_json::from_str(body).map_err(e)?;
    assert_eq!(status.audit, AuditCounts::default());
    assert_eq!(status.rounds, 3);
    Ok(())
}

// ---- Parking a source at repeated audit mismatches (DESIGN.md 11.2, 11.4) ----

const H3: i128 = 3 * HOUR;

impl Rig {
    /// Corrupt both reads of the next audit (a stable mismatch), then run a round.
    fn mismatch_round(&mut self, wall: i128) -> R<u64> {
        self.d()?.fs_mut().flips.extend([10, 10]);
        self.round(wall)
    }
    fn disk_status(&self) -> R<StatusBody> {
        let loaded = read_durable(
            &self.tmp.root().join(StateFile::Status.name()),
            StateFile::Status.schema(),
        );
        let Loaded::Present(stored) = loaded else {
            return Err("status missing".to_owned());
        };
        serde_json::from_slice(&stored.body).map_err(e)
    }
    fn prefix(&self) -> String {
        memory_core::limits::owner_prefix(&self.owner)
    }
    fn version(&mut self) -> R<u64> {
        Ok(self.d()?.store_mut().inner.version)
    }
    /// A second source under the same owner, present from the first round.
    fn add_second(&mut self, bytes: &[u8]) -> R<SourceId> {
        let id = SourceId::try_from("src2".to_owned()).map_err(e)?;
        let path = "projects/p/s1b.jsonl".to_owned();
        let (owner, mut source) = self.list.first().cloned().ok_or("no source")?;
        source.handle = 2;
        source.id = id.clone();
        source.path = path.clone();
        source.facts.identities = vec![path];
        source.facts.file_identity = Some(2);
        self.list.push((owner, source));
        self.file()?.set(2, bytes);
        self.restart()?;
        Ok(id)
    }
    /// The park line this rig's owner logs for a source park.
    fn park_line(&self) -> String {
        format!(
            "INFO parked owner={} reason=repeated_audit_mismatch scope=source",
            self.prefix()
        )
    }
    /// Three stable mismatches one second apart, then the one exactly 3 h after the first.
    fn park(&mut self) -> R {
        self.round(0)?;
        for n in 1..=3 {
            self.mismatch_round(n * SECOND)?;
        }
        self.mismatch_round(SECOND + H3)?;
        Ok(())
    }
}

fn state_of(count: u32, first: Option<i64>) -> AuditState {
    AuditState {
        last_checked_at_ns: None,
        consecutive_failures: count,
        first_failure_at_ns: first,
    }
}

/// Guarantee: the park rule is the core's: three failures spanning at least 3 h.
#[test]
fn the_park_rule_table() {
    let first = 1_000_000_000_000_i64;
    let h3 = i64::try_from(H3).unwrap_or(0);
    let hours = |h: i64| first + h * 3_600 * 1_000_000_000;
    let park = Mismatch::Park(ParkReason::RepeatedAuditMismatch);
    let table = [
        // third failure one nanosecond short of 3 h: an epoch, count 3
        (
            state_of(2, Some(first)),
            first + h3 - 1,
            Mismatch::Epoch(state_of(3, Some(first))),
        ),
        // exactly 3 h parks
        (state_of(2, Some(first)), first + h3, park.clone()),
        (state_of(3, Some(first)), first + h3, park),
        // two failures 50 h apart never park
        (
            state_of(1, Some(first)),
            hours(50),
            Mismatch::Epoch(state_of(2, Some(first))),
        ),
        // the first failure time is set once, and kept
        (
            state_of(0, None),
            first,
            Mismatch::Epoch(state_of(1, Some(first))),
        ),
        (
            state_of(1, Some(first)),
            first + 7,
            Mismatch::Epoch(state_of(2, Some(first))),
        ),
        // a wall clock behind the first failure gives a span of zero
        (
            state_of(2, Some(first)),
            first - 1,
            Mismatch::Epoch(state_of(3, Some(first))),
        ),
        (
            state_of(2, Some(first)),
            first - h3,
            Mismatch::Epoch(state_of(3, Some(first))),
        ),
    ];
    for (previous, now, want) in table {
        assert_eq!(mismatch(&previous, now), want, "{previous:?} at {now}");
    }
    assert_eq!(matched(5).first_failure_at_ns, None);
    assert_eq!(matched(5).consecutive_failures, 0);
}

/// Guarantee: three mismatches with the third before 3 h start three epochs; the next one at
/// 3 h parks the source alone: no epoch, no PUT, a status entry and one log line.
#[test]
fn a_source_parks_at_the_next_mismatch_after_3_h() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    for n in 1..=3 {
        r.mismatch_round(n * SECOND)?;
        assert_eq!((r.counts(), r.last.parked), ((0, 1, 0), 0));
    }
    let a = r.audit()?;
    assert_eq!(
        (a.consecutive_failures, a.first_failure_at_ns),
        (3, at(SECOND)?)
    );
    assert_eq!(r.restore()?.1, 4);
    let version = r.version()?;
    r.mismatch_round(SECOND + H3)?;
    assert_eq!(r.last.parked, 1);
    assert_eq!(
        r.counts(),
        (0, 0, 0),
        "a park is not a new epoch started by a mismatch"
    );
    assert_eq!(r.restore()?, (data.clone(), 4), "no new epoch");
    assert_eq!(r.version()?, version, "no PUT");
    assert_eq!(r.audit()?, a, "the index entry is unchanged");
    assert_eq!(r.log, [r.park_line()]);
    let status = r.disk_status()?;
    assert_eq!(status.parked.len(), 1);
    let entry = status.parked.first().ok_or("entry")?;
    assert_eq!(entry.source.as_ref(), Some(&r.id));
    assert_eq!(entry.owner, r.owner);
    assert_eq!(
        entry.reason,
        ParkedReason::Source(ParkReason::RepeatedAuditMismatch)
    );
    // Later rounds, even with a changed file: nothing logged, read or stat-ed.
    let mut grown = data;
    grown.extend_from_slice(&pattern(500));
    r.file()?.set(1, &grown);
    for n in 2..4 {
        assert_eq!(r.round(SECOND + H3 + n * SECOND)?, 0);
        assert_eq!((r.stats, r.log.len()), (0, 0));
        assert_eq!(r.last.parked, 0);
    }
    assert_eq!(r.restore()?.1, 4);
    Ok(())
}

/// Guarantee: the owner's other source keeps flowing after the first is parked.
#[test]
fn the_other_source_of_the_owner_keeps_flowing() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    let other = r.add_second(&pattern(1000))?;
    r.park()?;
    assert_eq!(r.last.parked, 1);
    let mut grown = pattern(1000);
    grown.extend_from_slice(b"more\n");
    r.file()?.set(2, &grown);
    r.round(SECOND + H3 + SECOND)?;
    assert_eq!(r.last.landed, 1);
    assert_eq!(r.restore_of(&other)?.0, grown);
    assert_eq!(r.restore()?, (data, 4), "the parked source is frozen");
    Ok(())
}

/// Guarantee: a match between failures resets the streak; an extension keeps it; an epoch not
/// started by a mismatch clears it.
#[test]
fn a_match_resets_the_streak_and_an_extension_keeps_it() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    r.round(0)?;
    r.mismatch_round(SECOND)?;
    r.mismatch_round(2 * SECOND)?;
    let kept = state_of(2, at(SECOND)?);
    assert_eq!(r.audit()?, kept);
    let mut grown = data;
    grown.extend_from_slice(&pattern(1000));
    r.file()?.set(1, &grown);
    r.round(3 * SECOND)?;
    assert_eq!(
        r.audit()?.consecutive_failures,
        2,
        "an extension keeps the streak"
    );
    assert_eq!(r.audit()?.first_failure_at_ns, at(SECOND)?);
    r.round(4 * SECOND)?;
    assert_eq!(
        (r.counts(), r.audit()?.consecutive_failures),
        ((1, 0, 0), 0)
    );
    assert_eq!(r.audit()?.first_failure_at_ns, None);
    grown.extend_from_slice(&pattern(1000));
    r.file()?.set(1, &grown);
    r.round(5 * SECOND)?;
    r.mismatch_round(5 * SECOND + 25 * HOUR)?;
    let a = r.audit()?;
    assert_eq!(
        (a.consecutive_failures, a.first_failure_at_ns),
        (1, at(5 * SECOND + 25 * HOUR)?)
    );
    assert_eq!(r.last.parked, 0);
    // A truncation starts an epoch that is not a mismatch: the streak is cleared.
    r.file()?.set(1, &pattern(SIZE / 2));
    r.round(5 * SECOND + 26 * HOUR)?;
    assert_eq!(r.audit()?, state_of(0, None));
    assert_eq!(r.restore()?.0, pattern(SIZE / 2));
    Ok(())
}

/// Guarantee: a park survives a restart with `status.json`; without it the index entry's streak
/// parks the source again at the next due audit, and a match clears the streak.
#[test]
fn a_park_survives_a_restart_and_a_lost_status_heals() -> R {
    let later = SECOND + H3 + 5 * SECOND;
    // status.json intact: still parked, nothing read.
    let mut r = Rig::new(&pattern(SIZE))?;
    r.park()?;
    r.restart()?;
    assert_eq!(r.round(later)?, 0);
    assert_eq!((r.stats, r.log.len(), r.last.parked), (0, 0, 0));
    assert_eq!(r.disk_status()?.parked.len(), 1);
    // status.json lost, corruption present: parked again by the next due audit.
    let mut r = Rig::new(&pattern(SIZE))?;
    r.park()?;
    fs::remove_file(r.tmp.root().join(StateFile::Status.name())).map_err(e)?;
    r.restart()?;
    r.mismatch_round(later)?;
    assert_eq!((r.last.parked, r.log.clone()), (1, vec![r.park_line()]));
    assert_eq!(r.restore()?.1, 4);
    assert_eq!(r.disk_status()?.parked.len(), 1);
    // status.json lost, no corruption: the audit matches and the streak is cleared.
    let mut r = Rig::new(&pattern(SIZE))?;
    r.park()?;
    fs::remove_file(r.tmp.root().join(StateFile::Status.name())).map_err(e)?;
    r.restart()?;
    r.round(later)?;
    assert_eq!((r.counts(), r.last.parked, r.log.len()), ((1, 0, 0), 0, 0));
    assert_eq!(r.audit()?, state_of(0, None).with_checked(at(later)?));
    assert!(r.disk_status()?.parked.is_empty());
    Ok(())
}

trait WithChecked {
    fn with_checked(self, at: Option<i64>) -> Self;
}
impl WithChecked for AuditState {
    fn with_checked(mut self, at: Option<i64>) -> Self {
        self.last_checked_at_ns = at;
        self
    }
}

/// Guarantee: a source park is recorded in the round whose owner commit fails.
#[test]
fn a_park_is_recorded_when_the_owners_commit_fails() -> R {
    let data = pattern(SIZE);
    let mut r = Rig::new(&data)?;
    let other = r.add_second(&pattern(1000))?;
    r.round(0)?;
    for n in 1..=3 {
        r.mismatch_round(n * SECOND)?;
    }
    let mut grown = pattern(1000);
    grown.extend_from_slice(b"more\n");
    r.file()?.set(2, &grown);
    r.d()?.store_mut().fail_puts = true;
    r.mismatch_round(SECOND + H3)?;
    assert_eq!((r.last.parked, r.last.landed, r.last.failed), (1, 0, 1));
    assert_eq!(r.disk_status()?.parked.len(), 1);
    assert_eq!(r.log, [r.park_line()]);
    r.d()?.store_mut().fail_puts = false;
    r.round(SECOND + H3 + 120 * SECOND)?;
    assert_eq!(r.last.landed, 1);
    assert_eq!(r.restore_of(&other)?.0, grown);
    assert_eq!(r.restore()?, (data, 4));
    Ok(())
}
