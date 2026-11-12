//! Walking skeleton (brief 26): one real file goes through the daemon and the S3 adapter and
//! comes back through `memory-reader`. Runs against the local server in the normal suite and,
//! with `--ignored`, once against a real endpoint.
use super::clock::TestClock;
use super::fake::Fake;
use super::{BUCKET, R, e, store_at};
use memory::daemon::{Daemon, Parts, Sources};
use memory::environment::{Published, Store, StoreError, Stored};
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::s3::{S3Store, TimeoutPolicy};
use memory_core::domain::{Agent, ArchiveId, ETag, MachineId, Owner};
use memory_core::limits::current_key;
use memory_core::locator::{RootLayout, RootNamespace, Roots};
use memory_core::manifest::{Folded, manifest_hash};
use memory_core::privacy::{PolicyContext, rules_digest};
use memory_reader::{decode_piece, read_owner, restore_source};
use std::cell::RefCell;
use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::io::Write;
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use std::rc::Rc;

const MARKER: &str = "SKELETON-MARKER-7f3a";
const SESSION_FILE: &str = "claude/projects/-p/0f0e0d0c-0b0a-4908-8706-050403020100.jsonl";

struct Tmp(PathBuf);
impl Tmp {
    fn new() -> R<Self> {
        let path = std::env::temp_dir().join(
            format!(
                "skeleton-{}-{:?}",
                std::process::id(),
                std::thread::current().id()
            )
            .replace(['(', ')'], ""),
        );
        let _ = fs::remove_dir_all(&path);
        fs::create_dir_all(&path).map_err(e)?;
        fs::set_permissions(&path, fs::Permissions::from_mode(0o700)).map_err(e)?;
        Ok(Self(path))
    }
    fn config(&self) -> R<PathBuf> {
        let token = self.0.join("token");
        fs::write(&token, "x").map_err(e)?;
        fs::set_permissions(&token, fs::Permissions::from_mode(0o600)).map_err(e)?;
        let path = self.0.join("config.toml");
        let text = format!(
            "archive_id = \"a\"\nmachine_id = \"m\"\nbucket = \"bucket-1\"\ntoken_path = \"{}\"\n",
            token.display()
        );
        fs::write(&path, text).map_err(e)?;
        Ok(path)
    }
}
impl Drop for Tmp {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

/// What went through the store seam: requests per operation and the keys written.
#[derive(Debug, Default, Clone)]
struct Tally {
    requests: BTreeMap<&'static str, u64>,
    written: BTreeSet<String>,
}

struct Tallied {
    inner: S3Store,
    tally: Rc<RefCell<Tally>>,
}
impl Tallied {
    fn note(&self, operation: &'static str, written: Option<&str>) {
        let mut tally = self.tally.borrow_mut();
        *tally.requests.entry(operation).or_insert(0) += 1;
        tally.written.extend(written.map(str::to_owned));
    }
}
impl Store for Tallied {
    fn put_if_absent(&mut self, key: &str, body: Published) -> Result<ETag, StoreError> {
        let result = self.inner.put_if_absent(key, body);
        self.note("put_if_absent", result.is_ok().then_some(key));
        result
    }
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &ETag,
        body: Published,
    ) -> Result<ETag, StoreError> {
        let result = self.inner.put_if_match(key, etag, body);
        self.note("put_if_match", result.is_ok().then_some(key));
        result
    }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        self.note("get", None);
        self.inner.get(key)
    }
}

fn roots() -> R<Roots> {
    Ok(Roots::new(
        ArchiveId::try_from("a".to_owned()).map_err(e)?,
        MachineId::try_from("m".to_owned()).map_err(e)?,
        Agent::Claude,
        RootNamespace::new("r1").map_err(e)?,
        RootLayout::AgentDir,
    ))
}
fn lines(range: std::ops::Range<u32>) -> String {
    range
        .map(|i| {
            format!(
                "{{\"role\":\"user\",\"n\":{i},\"text\":\"{MARKER} line {i} {}\"}}\n",
                "filler ".repeat(40)
            )
        })
        .collect()
}

type D = Daemon<SharedFs, Tallied, TestClock, WalkSources>;

/// One round that lands `landed` owners and fails none.
fn round(daemon: &mut D, landed: usize) -> R {
    let report = daemon.reconcile_once().map_err(e)?;
    assert_eq!(
        (report.landed, report.failed, report.parked),
        (landed, 0, 0)
    );
    Ok(())
}

struct Outcome {
    /// The owner's state as the reader folded it at the end.
    folded: Folded,
    /// The source as the reader restored it at the end.
    restored: Vec<u8>,
    tally: Tally,
}

/// Capture a file in two rounds, read it back, restart on an empty state root (one idle round,
/// then one more append) and read it back again. `mark` is called at the end of each phase.
fn scenario(make: &dyn Fn() -> R<S3Store>, mark: &mut dyn FnMut(&str)) -> R<Outcome> {
    let tmp = Tmp::new()?;
    let cfg = tmp.config()?;
    let file = tmp.0.join(SESSION_FILE);
    fs::create_dir_all(file.parent().ok_or("parent")?).map_err(e)?;
    fs::write(&file, lines(0..12)).map_err(e)?;
    let append = |range| -> R {
        let mut f = fs::OpenOptions::new().append(true).open(&file).map_err(e)?;
        f.write_all(lines(range).as_bytes()).map_err(e)
    };
    let sources = |fs: SharedFs| -> R<WalkSources> {
        Ok(WalkSources::new(
            vec![(tmp.0.join("claude"), roots()?)],
            WalkLimits::default(),
            fs,
        ))
    };
    let owner: Owner = sources(SharedFs::new())?
        .enumerate()
        .first()
        .ok_or("no source")?
        .0
        .clone();
    let tally: Rc<RefCell<Tally>> = Rc::default();
    let store = || -> R<Tallied> {
        Ok(Tallied {
            inner: make()?,
            tally: Rc::clone(&tally),
        })
    };
    let start = |state: &str| -> R<D> {
        let fs = SharedFs::new();
        let parts = Parts {
            fs: fs.clone(),
            store: store()?,
            clock: TestClock::at(0),
            sources: sources(fs)?,
            context: PolicyContext {
                valid: true,
                revision: "r".to_owned(),
                rules: Some(rules_digest()),
                collector_paths: vec![],
                collector_identities: vec![],
            },
        };
        Daemon::start(&tmp.0.join(state), &cfg, parts).map_err(|x| x.code().to_owned())
    };
    // An independent reader: its own store, and nothing from the daemon but the owner.
    let read_back = || -> R<(Folded, Vec<u8>)> {
        let mut store = store()?;
        let folded = read_owner(&owner, |key| match store.get(key) {
            Ok(stored) => Ok(Some(stored.body)),
            Err(StoreError::NotFound) => Ok(None),
            Err(other) => Err(other),
        })
        .map_err(e)?
        .ok_or("owner absent")?;
        let entry = folded.state.sources.values().next().ok_or("no source")?;
        let restored = restore_source(entry, |key| store.get(key).map(|s| s.body)).map_err(e)?;
        assert_eq!(restored, fs::read(&file).map_err(e)?);
        Ok((folded, restored))
    };

    let mut daemon = start("state1")?;
    round(&mut daemon, 1)?;
    mark("round1");
    append(12..20)?;
    round(&mut daemon, 1)?;
    mark("round2");
    daemon.shutdown().map_err(e)?;
    read_back()?;
    mark("readback1");

    let mut daemon = start("state2")?;
    round(&mut daemon, 0)?;
    mark("restart_idle");
    append(20..26)?;
    round(&mut daemon, 1)?;
    mark("round4");
    daemon.shutdown().map_err(e)?;
    let (folded, restored) = read_back()?;
    mark("readback2");
    Ok(Outcome {
        folded,
        restored,
        tally: tally.borrow().clone(),
    })
}

/// What the scenario costs through the store seam, the same on any conforming server.
fn assert_seam_totals(tally: &Tally) {
    // GET: round 1 finds no current.json (1); the read-backs take current.json, the manifests
    // and the pieces (1 + 2 + 2, then 1 + 3 + 3); the restart recovers its base (1 + 2).
    // If-None-Match: three pieces, three manifests and the first current.json.
    // If-Match: current.json in the two later landing rounds.
    let want = BTreeMap::from([("get", 16), ("put_if_absent", 7), ("put_if_match", 2)]);
    assert_eq!(tally.requests, want);
    assert_eq!(tally.written.len(), 7);
}

#[test]
fn skeleton_against_the_local_server() -> R {
    const PREFIX: &str = "rust-acceptance/skeleton-test/";
    let server = Fake::start()?;
    let url = server.url();
    let make = || store_at(&url, PREFIX, TimeoutPolicy::PRODUCTION);
    let mut marks: Vec<(String, usize, usize)> = Vec::new();
    let out = scenario(&make, &mut |name| {
        marks.push((name.to_owned(), server.count("GET"), server.count("PUT")));
    })?;
    assert_seam_totals(&out.tally);

    // Requests the server saw per phase, as (GET, PUT). A landing round is piece, manifest,
    // current.json. The restart recovers its base and writes nothing.
    let want = [
        ("round1", (1, 3)),
        ("round2", (0, 3)),
        ("readback1", (1 + 2 + 2, 0)),
        ("restart_idle", (1 + 2, 0)),
        ("round4", (0, 3)),
        ("readback2", (1 + 3 + 3, 0)),
    ];
    assert_eq!(marks.len(), want.len());
    let (mut gets_before, mut puts_before) = (0, 0);
    for ((name, gets, puts), (want_name, want_counts)) in marks.iter().zip(want) {
        assert_eq!(name, want_name);
        assert_eq!(
            (gets - gets_before, puts - puts_before),
            want_counts,
            "{name}"
        );
        (gets_before, puts_before) = (*gets, *puts);
    }

    // What the server holds: exactly the keys the daemon believes it wrote, under the prefix.
    let root = format!("/{BUCKET}/{PREFIX}");
    let held: BTreeSet<String> = server
        .requests()
        .iter()
        .filter(|r| r.method == "PUT" && r.status == 200)
        .map(|r| r.path.clone())
        .collect();
    let believed: BTreeSet<String> = out
        .tally
        .written
        .iter()
        .map(|key| format!("{root}{key}"))
        .collect();
    assert_eq!(held, believed);
    let under = |dir: &str| {
        let dir = format!("{root}{dir}");
        held.iter().filter(|key| key.starts_with(&dir)).count()
    };
    assert_eq!((under("objects/"), under("manifests/")), (3, 3));
    assert!(held.contains(&format!("{root}{}", current_key(&out.folded.owner))));

    let entry = out
        .folded
        .state
        .sources
        .values()
        .next()
        .ok_or("no source")?;
    let pieces = &entry.epochs.last().ok_or("no epoch")?.pieces;
    assert_eq!(pieces.len(), 3);
    for piece in pieces {
        let (bytes, _) = server
            .object(&format!("{root}{}", piece.object()))
            .ok_or("piece not on the server")?;
        assert_eq!(bytes.get(..4), Some(&[0x28, 0xB5, 0x2F, 0xFD][..]));
        assert_eq!(u64::try_from(bytes.len()).map_err(e)?, piece.encoded_len());
        assert_eq!(manifest_hash(&bytes), piece.encoded_sha256());
        let raw = decode_piece(piece, &bytes).map_err(e)?;
        assert_ne!(raw, bytes);
        assert_eq!(manifest_hash(&raw), piece.sha256());
        // Repeats become back-references, so the stored frame holds fewer copies than the raw
        // bytes (the first copy can survive as a literal).
        let count = |haystack: &[u8]| {
            haystack
                .windows(MARKER.len())
                .filter(|w| *w == MARKER.as_bytes())
                .count()
        };
        assert!(count(&bytes) < count(&raw), "stored bytes look unencoded");
    }
    assert!(String::from_utf8(out.restored).map_err(e)?.contains(MARKER));
    Ok(())
}

fn var(name: &str) -> R<String> {
    std::env::var(name).map_err(|_| format!("missing environment variable {name}"))
}

/// Writes under `rust-acceptance/skeleton-<run id>/` and deletes nothing: the `Store` seam has
/// no delete, so the prefix is removed with a separate tool. Prints the prefix and counts only.
#[test]
#[ignore = "contacts a real S3 endpoint: needs R2_ENDPOINT, R2_BUCKET, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY and SKELETON_RUN_ID"]
fn skeleton_on_r2() -> R {
    let (endpoint, bucket) = (var("R2_ENDPOINT")?, var("R2_BUCKET")?);
    let (key_id, secret) = (var("R2_ACCESS_KEY_ID")?, var("R2_SECRET_ACCESS_KEY")?);
    let run = var("SKELETON_RUN_ID")?;
    if run.is_empty() || !run.chars().all(|c| c.is_ascii_alphanumeric() || c == '-') {
        return Err("SKELETON_RUN_ID must be non-empty and match [A-Za-z0-9-]".to_owned());
    }
    let prefix = format!("rust-acceptance/skeleton-{run}/");
    let make = || {
        S3Store::new(
            &endpoint,
            &bucket,
            "auto",
            &key_id,
            &secret,
            &prefix,
            TimeoutPolicy::PRODUCTION,
        )
        .map_err(|_| "S3 configuration rejected".to_owned())
    };
    eprintln!("prefix: {prefix}");
    let out = scenario(&make, &mut |_| {})?;
    eprintln!("requests: {:?}", out.tally.requests);
    eprintln!("keys written: {}", out.tally.written.len());
    assert_seam_totals(&out.tally);
    Ok(())
}
