//! Independent-oracle kill tests for `scripts/check-mutations.py` (if-match and
//! advance-after-land). The oracle is the fake store's own record of what it accepted and
//! rejected, never the production line that the mutant changes.
use memory::capture::{Published, Source};
use memory::daemon::{Daemon, Parts, StartOptions};
use memory::environment::{Store, StoreError, Stored};
use memory::state::SyncPolicy;
use memory_core::domain::{
    Agent, ArchiveId, ETag, MachineId, Owner, SessionId, SessionKey, SourceClass, SourceId,
    SourceKind,
};
use memory_core::privacy::{PolicyContext, Profile, ResolvedSourceFacts, rules_digest};
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
use mem_store::{EtagModel, MemStore};
use test_clock::TestClock;

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

/// The shared store plus the two things this file's oracle needs: a count of the stale
/// `If-Match` writes of `current.json` the store refused, and a switch that makes every write
/// of `current.json` fail before landing.
#[derive(Default)]
struct Judged {
    inner: MemStore,
    /// `current.json` writes refused with 412 (a stale `If-Match`).
    rejected: u32,
    fail_current: bool,
}
impl Store for Judged {
    fn put_if_absent(&mut self, key: &str, body: Published) -> Result<ETag, StoreError> {
        if self.fail_current && key.ends_with("current.json") {
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
        let current = key.ends_with("current.json");
        if self.fail_current && current {
            return Err(StoreError::Offline);
        }
        let result = self.inner.put_if_match(key, etag, body);
        if current && result == Err(StoreError::PreconditionFailed) {
            self.rejected += 1;
        }
        result
    }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        self.inner.get(key)
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
type D = Daemon<MemFs, Judged, TestClock, Srcs>;

fn boot(tmp: &Tmp, initial: &[u8], model: EtagModel) -> R<D> {
    let cfg = tmp.config()?;
    let list = vec![source()?];
    let sources: Srcs = Box::new(move || list.clone());
    let mut files = MemFs::default();
    files.set(1, initial);
    let parts = Parts {
        fs: files,
        store: Judged {
            inner: MemStore {
                model,
                ..MemStore::default()
            },
            ..Judged::default()
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
        d.clock_mut().mono = *mono;
        let _ = d.reconcile_once();
    }
}

fn current_key(d: &mut D) -> R<String> {
    need(
        d.store_mut()
            .inner
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
    let mut d = boot(&tmp, b"first line\n", EtagModel::PerWrite)?;
    let mut mono = 0;
    rounds(&mut d, &mut mono, 3);
    let key = current_key(&mut d)?;
    let before = need(d.store_mut().inner.objects.get(&key).cloned(), "current")?;
    assert_eq!(d.store_mut().rejected, 0);
    {
        let st = &mut d.store_mut().inner;
        st.version += 1;
        let v = st.version;
        if let Some(o) = st.objects.get_mut(&key) {
            o.1 = v;
        }
    }
    let mut grown = need(d.fs_mut().files.get(&1).cloned(), "file")?;
    grown.extend_from_slice(b"second line\n");
    d.fs_mut().set(1, &grown);
    rounds(&mut d, &mut mono, 3);
    let after = need(d.store_mut().inner.objects.get(&key).cloned(), "current")?;
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
    for model in [EtagModel::PerWrite, EtagModel::Content] {
        failed_commit_case(model)?;
    }
    Ok(())
}

fn failed_commit_case(model: EtagModel) -> R {
    let tmp = Tmp::new()?;
    let mut d = boot(&tmp, b"first line\nsecond line\n", model)?;
    let mut mono = 0;
    d.store_mut().fail_current = true;
    rounds(&mut d, &mut mono, 2);
    assert!(
        d.store_mut()
            .inner
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
            .inner
            .objects
            .keys()
            .any(|k| k.ends_with("current.json")),
        "the failed commit's bytes were never captured again"
    );
    Ok(())
}
