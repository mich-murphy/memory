//! Counter tests (DESIGN 15) and the idempotence property (property 3), through the real
//! `Daemon`/`capture` code with a counting wrapper over a fake filesystem and store.
//! The 1 s target for a 30k-source reconcile stays a footprint measurement: wall time is
//! printed, never asserted (it would be a flaky gate under coverage instrumentation).
use hegel::{TestCase, generators as gs};
use memory::capture::{Published, Source};
use memory::daemon::{Daemon, Parts, StartOptions};
use memory::environment::{Clock, Counters, Counting, FileSystem, Stat, Store, StoreError, Stored};
use memory::state::SyncPolicy;
use memory_core::domain::{
    Agent, ArchiveId, Birth, ETag, FileId, MachineId, Owner, SessionId, SessionKey, SourceClass,
    SourceId, SourceKind,
};
use memory_core::privacy::{PolicyContext, Profile, ResolvedSourceFacts, rules_digest};
use std::collections::BTreeMap;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use std::sync::atomic::{AtomicU64, Ordering};

type R<T = ()> = Result<T, String>;
fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}
fn need<T>(v: Option<T>, what: &str) -> R<T> {
    v.ok_or_else(|| what.to_owned())
}

struct Tmp(PathBuf);
impl Tmp {
    fn new() -> R<Self> {
        static N: AtomicU64 = AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!(
            "memory-counters-{}-{}",
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
                "archive_id = \"a\"\nmachine_id = \"m\"\nbucket = \"bucket-1\"\ntoken_path = \"{}\"\n",
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
            mtime_ns: i64::try_from(b.len()).unwrap_or(0),
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

/// How the fake derives an ETag. R2 reports the MD5 of the body (P2 `etag_is_md5_of_body`), so
/// the content model is measured for distinct bodies; what R2 answers for an identical rewrite
/// is unmeasured, so the properties run under both.
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
fn model_from(tc: &TestCase) -> EtagModel {
    if tc.draw(gs::booleans()) {
        EtagModel::Content
    } else {
        EtagModel::PerWrite
    }
}

#[derive(Default)]
struct MemStore {
    /// Body and ETag token.
    objects: BTreeMap<String, (Vec<u8>, u64)>,
    /// Count of writes.
    version: u64,
    model: EtagModel,
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
        let existing = self.objects.get(key).map(|(_, v)| *v);
        match (fence, existing) {
            (None, Some(_)) => return Err(StoreError::PreconditionFailed),
            // Measured on R2 (P2 `r2-os.jsonl`): If-Match on a missing key is 412, not 404.
            (Some(_), None) => return Err(StoreError::PreconditionFailed),
            (Some(f), Some(v)) if &Self::tag(v)? != f => {
                return Err(StoreError::PreconditionFailed);
            }
            _ => {}
        }
        self.version += 1;
        let token = match self.model {
            EtagModel::PerWrite => self.version,
            EtagModel::Content => content_token(body.as_ref()),
        };
        self.objects
            .insert(key.to_owned(), (body.as_ref().to_vec(), token));
        Self::tag(token)
    }
}
impl Store for MemStore {
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
        let (b, v) = self.objects.get(key).ok_or(StoreError::NotFound)?;
        Ok(Stored {
            body: b.clone(),
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

fn src_with(owner_n: u64, handle: u64, profile: Profile) -> R<(Owner, Source)> {
    let path = format!("projects/p/s{owner_n}-{handle}.jsonl");
    Ok((
        Owner::Session(SessionKey {
            archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
            machine: MachineId::try_from("m".to_owned()).map_err(e)?,
            agent: Agent::Claude,
            session: SessionId::try_from(format!("s{owner_n}")).map_err(e)?,
        }),
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
                profile,
            },
        },
    ))
}

type Fs = Counting<MemFs>;
type St = Counting<MemStore>;
type Srcs = Box<dyn FnMut() -> Vec<(Owner, Source)>>;
type D = Daemon<Fs, St, Clk, Srcs>;

struct Rig {
    tmp: Tmp,
    cfg: PathBuf,
    list: Vec<(Owner, Source)>,
    d: Option<D>,
    mono: u64,
}

fn boot(tmp: &Tmp, cfg: &std::path::Path, fs: Fs, store: St, list: &[(Owner, Source)]) -> R<D> {
    let list = list.to_vec();
    let sources: Srcs = Box::new(move || list.clone());
    let parts = Parts {
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

/// `owners` owners with `per` sources each; handles are 1-based and every file holds `bytes`.
fn rig(owners: u64, per: u64, bytes: &[u8]) -> R<Rig> {
    rig_with(owners, per, bytes, Profile::Transcript)
}
fn rig_with(owners: u64, per: u64, bytes: &[u8], profile: Profile) -> R<Rig> {
    rig_model(owners, per, bytes, profile, EtagModel::PerWrite)
}
fn rig_model(owners: u64, per: u64, bytes: &[u8], profile: Profile, model: EtagModel) -> R<Rig> {
    let tmp = Tmp::new()?;
    let cfg = tmp.config()?;
    let mut list = Vec::new();
    let mut fs = Fs::default();
    for o in 0..owners {
        for k in 0..per {
            let handle = o * per + k + 1;
            list.push(src_with(o, handle, profile)?);
            fs.inner.files.insert(handle, bytes.to_vec());
        }
    }
    let store = St::new(MemStore {
        model,
        ..MemStore::default()
    });
    let d = boot(&tmp, &cfg, fs, store, &list)?;
    Ok(Rig {
        tmp,
        cfg,
        list,
        d: Some(d),
        mono: 0,
    })
}

impl Rig {
    fn d(&mut self) -> R<&mut D> {
        self.d.as_mut().ok_or_else(|| "no daemon".to_owned())
    }
    fn round(&mut self) -> R {
        self.mono += 61_000_000_000;
        let mono = self.mono;
        let d = self.d()?;
        d.clock_mut().0 = mono;
        d.reconcile_once().map_err(e)?;
        Ok(())
    }
    fn counts(&mut self) -> R<(Counters, Counters)> {
        let d = self.d()?;
        Ok((d.fs_mut().counters, d.store_mut().counters))
    }
    /// Rounds until one makes no put (at most 8).
    fn converge(&mut self) -> R {
        for _ in 0..8 {
            let before = self.counts()?.1.puts;
            self.round()?;
            if self.counts()?.1.puts == before {
                return Ok(());
            }
        }
        Err("did not converge in 8 rounds".to_owned())
    }
    fn restart(&mut self) -> R {
        let mut d = need(self.d.take(), "no daemon")?;
        let fs = std::mem::take(d.fs_mut());
        let store = std::mem::take(d.store_mut());
        drop(d);
        self.d = Some(boot(&self.tmp, &self.cfg, fs, store, &self.list)?);
        self.mono = 0;
        Ok(())
    }
    /// One round; returns (stats, bytes_read, puts) it made.
    fn measure(&mut self) -> R<(u64, u64, u64)> {
        let (f0, s0) = self.counts()?;
        self.round()?;
        let (f1, s1) = self.counts()?;
        Ok((
            f1.stats - f0.stats,
            f1.bytes_read - f0.bytes_read,
            s1.puts - s0.puts,
        ))
    }
}

fn lines(n: usize) -> Vec<u8> {
    let mut out = Vec::new();
    while out.len() < n {
        out.extend_from_slice(&[b'a'; 99]);
        out.push(b'\n');
    }
    out.truncate(n.saturating_sub(1));
    out.push(b'\n');
    out
}

#[test]
fn idle_round_makes_no_puts_and_reads_nothing() -> R {
    let mut r = rig(2, 2, b"hello\n")?;
    r.converge()?;
    let (stats, read, puts) = r.measure()?;
    assert_eq!((read, puts), (0, 0));
    assert!(stats <= 4, "stats {stats} for 4 sources");
    Ok(())
}

#[test]
fn reconcile_at_30k_unchanged_sources_stats_each_once() -> R {
    let mut r = rig(30, 1000, b"hello\n")?;
    r.converge()?;
    let started = std::time::Instant::now();
    let (stats, read, puts) = r.measure()?;
    eprintln!(
        "30k-source unchanged reconcile: {:?} (1 s target is a footprint measurement, not asserted)",
        started.elapsed()
    );
    // DESIGN.md 15: at most one stat per source. The wall-time target is not asserted; this
    // call-count bound is its deterministic proxy (work per source is constant, so the time is
    // linear in the sources), and the lower bound keeps it from passing by not looking at all.
    assert!(
        (1..=30_000).contains(&stats),
        "stats {stats} for 30,000 sources"
    );
    assert_eq!((read, puts), (0, 0));
    Ok(())
}

#[test]
fn append_of_59kb_reads_at_most_59kb_plus_68kib() -> R {
    let mut r = rig(1, 1, &lines(1_000))?;
    r.converge()?;
    let mut grown = lines(1_000);
    grown.extend(lines(59_000));
    r.d()?.fs_mut().inner.files.insert(1, grown);
    let (_, read, puts) = r.measure()?;
    assert!(puts > 0, "the append was captured");
    assert!(read <= 59_000 + 68 * 1024, "read {read} bytes");
    // The same append to a 200 kB source must read no more: reads scale with the append.
    let mut big = rig(1, 1, &lines(200_000))?;
    big.converge()?;
    let mut more = lines(200_000);
    more.extend(lines(59_000));
    big.d()?.fs_mut().inner.files.insert(1, more);
    let (_, big_read, _) = big.measure()?;
    eprintln!("59 KB append to a 200 kB source read {big_read} bytes");
    assert!(big_read <= 59_000 + 68 * 1024, "read {big_read} bytes");
    Ok(())
}

fn idempotence_case(tc: &TestCase) -> R {
    let owners = u64::from(tc.draw(gs::integers::<u8>().min_value(1).max_value(3)));
    let per = u64::from(tc.draw(gs::integers::<u8>().min_value(1).max_value(3)));
    let mut r = rig_model(owners, per, b"", Profile::Transcript, model_from(tc))?;
    for h in 1..=owners * per {
        r.d()?.fs_mut().inner.files.remove(&h);
    }
    let appends = tc.draw(gs::integers::<u8>().min_value(1).max_value(6));
    for _ in 0..appends {
        let h =
            u64::from(tc.draw(gs::integers::<u8>().min_value(0).max_value(8))) % (owners * per) + 1;
        let n = usize::from(tc.draw(gs::integers::<u8>().min_value(1).max_value(200)));
        let mut file = r
            .d()?
            .fs_mut()
            .inner
            .files
            .get(&h)
            .cloned()
            .unwrap_or_default();
        file.extend(lines(n.max(2)));
        r.d()?.fs_mut().inner.files.insert(h, file);
        if tc.draw(gs::integers::<u8>().min_value(0).max_value(1)) == 1 {
            r.round()?;
        }
    }
    r.converge()?;
    let (_, _, puts) = r.measure()?;
    if puts != 0 {
        return Err(format!("a second round made {puts} puts"));
    }
    r.restart()?;
    let (_, _, puts) = r.measure()?;
    if puts != 0 {
        return Err(format!("round after restart made {puts} puts"));
    }
    let _ = fs::remove_file(r.tmp.root().join("index.json"));
    r.restart()?;
    let (_, _, puts) = r.measure()?;
    if puts != 0 {
        return Err(format!("round after delete-index made {puts} puts"));
    }
    Ok(())
}

/// Property 3 (safety): after convergence, a further round, a restart then a round, and a
/// delete-index restart then a round all make zero store PUTs (counted at the store).
/// A writer-version change is not tested: `WRITER_VERSION` is a compile-time constant with no
/// seam, so it cannot be varied without a production change.
#[test]
fn hegel_converged_reconcile_makes_zero_puts() {
    hegel::Hegel::new(|tc: TestCase| {
        let result = idempotence_case(&tc);
        assert!(result.is_ok(), "{result:?}");
    })
    .run();
}

#[test]
fn non_prefix_stable_source_still_reads_in_full() -> R {
    let mut r = rig_with(1, 1, &lines(200_000), Profile::Config)?;
    r.converge()?;
    let mut more = lines(200_000);
    more.extend(lines(59_000));
    let size = more.len() as u64;
    r.d()?.fs_mut().inner.files.insert(1, more);
    let (_, read, puts) = r.measure()?;
    assert!(puts > 0, "the append was captured");
    assert!(read >= size, "read {read} < {size}");
    Ok(())
}

fn hex_sha(bytes: &[u8]) -> String {
    use sha2::{Digest, Sha256};
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}

/// The delta path (and the fallback after a lost or corrupted index) keeps the resumable
/// whole-stream digest equal to the independent digest of the final file.
#[test]
fn hegel_delta_capture_matches_independent_whole_digest() {
    hegel::Hegel::new(|tc: TestCase| {
        let result = (|| -> R {
            let mut r = rig_model(1, 1, b"", Profile::Transcript, model_from(&tc))?;
            r.d()?.fs_mut().inner.files.remove(&1);
            let mut file: Vec<u8> = Vec::new();
            for _ in 0..tc.draw(gs::integers::<u8>().min_value(1).max_value(6)) {
                let n = usize::from(tc.draw(gs::integers::<u16>().min_value(2).max_value(3000)));
                file.extend(lines(n));
                r.d()?.fs_mut().inner.files.insert(1, file.clone());
                r.round()?;
                match tc.draw(gs::integers::<u8>().min_value(0).max_value(3)) {
                    1 => {
                        let _ = fs::remove_file(r.tmp.root().join("index.json"));
                        r.restart()?;
                    }
                    2 => {
                        fs::write(r.tmp.root().join("index.json"), b"{not json").map_err(e)?;
                        r.restart()?;
                    }
                    _ => {}
                }
            }
            r.converge()?;
            let index = fs::read_to_string(r.tmp.root().join("index.json")).map_err(e)?;
            if !index.contains(&hex_sha(&file)) {
                return Err("index whole digest differs from the independent digest".to_owned());
            }
            Ok(())
        })();
        assert!(result.is_ok(), "{result:?}");
    })
    .run();
}
