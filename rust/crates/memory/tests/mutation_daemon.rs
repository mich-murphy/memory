//! Independent-oracle kill tests for `scripts/check-mutations.py` (if-match and
//! advance-after-land). The oracle is the fake store's own record of what it accepted and
//! rejected, never the production line that the mutant changes.
use memory::capture::{Published, Source};
use memory::daemon::{Daemon, Parts, StartOptions};
use memory::environment::{Clock, FileSystem, Stat, Store, StoreError, Stored};
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
        // Unique per test: `cargo test` runs both tests in one process concurrently.
        static N: AtomicU64 = AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!(
            "memory-mutation-{}-{}",
            std::process::id(),
            N.fetch_add(1, Ordering::SeqCst)
        ));
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

#[derive(Default)]
struct MemStore {
    objects: BTreeMap<String, (Vec<u8>, u64)>,
    version: u64,
    /// `current.json` writes refused with 412 (a stale `If-Match`).
    rejected: u32,
    /// Every write of `current.json` fails before landing.
    fail_current: bool,
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
        let current = key.ends_with("current.json");
        if current && self.fail_current {
            return Err(StoreError::Offline);
        }
        let existing = self.objects.get(key).map(|(_, v)| *v);
        match (fence, existing) {
            (None, Some(_)) => return Err(StoreError::PreconditionFailed),
            (Some(_), None) => return Err(StoreError::NotFound),
            (Some(f), Some(v)) if &Self::tag(v)? != f => {
                self.rejected += u32::from(current);
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

fn source() -> R<(Owner, Source)> {
    let path = "projects/p/s0-1.jsonl".to_owned();
    Ok((
        Owner::Session(SessionKey {
            archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
            machine: MachineId::try_from("m".to_owned()).map_err(e)?,
            agent: Agent::Claude,
            session: SessionId::try_from("s0".to_owned()).map_err(e)?,
        }),
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

type Srcs = Box<dyn FnMut() -> Vec<(Owner, Source)>>;
type D = Daemon<MemFs, MemStore, Clk, Srcs>;

fn boot(tmp: &Tmp, initial: &[u8]) -> R<D> {
    let cfg = tmp.config()?;
    let list = vec![source()?];
    let sources: Srcs = Box::new(move || list.clone());
    let mut files = MemFs::default();
    files.files.insert(1, initial.to_vec());
    let parts = Parts {
        fs: files,
        store: MemStore::default(),
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
        &tmp.0.join("state"),
        &cfg,
        parts,
        StartOptions {
            sync: SyncPolicy::None,
        },
    )
    .map_err(|x| x.code().to_owned())
}

/// `n` rounds, each an hour apart so no backoff can defer a retry.
fn rounds(d: &mut D, mono: &mut u64, n: usize) {
    for _ in 0..n {
        *mono += 3_600_000_000_000;
        d.clock_mut().0 = *mono;
        let _ = d.reconcile_once();
    }
}

fn current_key(d: &mut D) -> R<String> {
    need(
        d.store_mut()
            .objects
            .keys()
            .find(|k| k.ends_with("current.json"))
            .cloned(),
        "no current.json",
    )
}

/// A stale ETag (same bytes, new version: a churned object) must be refused by the store
/// with 412 and only then rebased. A blind or latest-ETag advance is never refused.
#[test]
fn stale_if_match_is_refused_by_the_store_before_the_rebase() -> R {
    let tmp = Tmp::new()?;
    let mut d = boot(&tmp, b"first line\n")?;
    let mut mono = 0;
    rounds(&mut d, &mut mono, 3);
    let key = current_key(&mut d)?;
    let before = need(d.store_mut().objects.get(&key).cloned(), "current")?;
    assert_eq!(d.store_mut().rejected, 0);
    {
        let st = d.store_mut();
        st.version += 1;
        let v = st.version;
        if let Some(o) = st.objects.get_mut(&key) {
            o.1 = v;
        }
    }
    need(d.fs_mut().files.get_mut(&1), "file")?.extend_from_slice(b"second line\n");
    rounds(&mut d, &mut mono, 3);
    let after = need(d.store_mut().objects.get(&key).cloned(), "current")?;
    assert_ne!(before.0, after.0, "the appended bytes never landed");
    assert!(
        d.store_mut().rejected >= 1,
        "stale If-Match was not enforced"
    );
    Ok(())
}

/// A commit that did not land must not advance local progress: once the store heals the
/// same bytes are captured again and `current.json` appears.
#[test]
fn failed_commit_does_not_advance_the_local_index() -> R {
    let tmp = Tmp::new()?;
    let mut d = boot(&tmp, b"first line\nsecond line\n")?;
    let mut mono = 0;
    d.store_mut().fail_current = true;
    rounds(&mut d, &mut mono, 2);
    assert!(
        d.store_mut()
            .objects
            .keys()
            .all(|k| !k.ends_with("current.json"))
    );
    // Independent oracle: the persisted local index must not name the digest of bytes
    // whose commit never landed.
    let digest: String = {
        use sha2::{Digest, Sha256};
        Sha256::digest(b"first line\nsecond line\n")
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect()
    };
    let index = fs::read_to_string(tmp.0.join("state").join("index.json")).unwrap_or_default();
    assert!(
        !index.contains(&digest),
        "local index advanced past a commit that never landed"
    );
    d.store_mut().fail_current = false;
    rounds(&mut d, &mut mono, 6);
    assert!(
        d.store_mut()
            .objects
            .keys()
            .any(|k| k.ends_with("current.json")),
        "the failed commit's bytes were never captured again"
    );
    Ok(())
}
