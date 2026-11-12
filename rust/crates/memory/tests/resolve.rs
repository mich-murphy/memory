//! Resolver tests (slice 2): synthetic temp trees only; no real agent directories.
use memory::capture::{Env, Locals, capture};
use memory::commit::{Base, commit};
use memory::daemon::Sources;
use memory::realfs::{RealFs, WalkLimits, walk};
use memory::resolve::{MAX_HOPS, SharedFs, WalkSources, resolve_facts, to_source};
use memory_core::domain::{Agent, ArchiveId, MachineId, Owner};
use memory_core::locator::{Located, RootLayout, RootNamespace, RootRelativePath, Roots, locate};
use memory_core::manifest::{Header, OwnerState, Timestamp, Writer};
use memory_core::privacy::{Admission, PolicyContext, Reason, UnknownReason, admit, rules_digest};
use std::collections::BTreeMap;
use std::fs;
use std::os::unix::fs::{MetadataExt, symlink};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

#[path = "common/store.rs"]
mod mem_store;
use mem_store::MemStore;

type R<T = ()> = Result<T, Box<dyn std::error::Error>>;
static NEXT: AtomicU64 = AtomicU64::new(0);

struct Tmp(PathBuf);
impl Tmp {
    fn new() -> R<Self> {
        let p = std::env::temp_dir().join(format!(
            "resolve-test-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::SeqCst)
        ));
        fs::create_dir_all(&p)?;
        Ok(Self(p))
    }
    fn write(&self, rel: &str, body: &[u8]) -> R<PathBuf> {
        let p = self.0.join(rel);
        if let Some(d) = p.parent() {
            fs::create_dir_all(d)?;
        }
        fs::write(&p, body)?;
        Ok(p)
    }
    fn root(&self) -> PathBuf {
        self.0.join("claude")
    }
}
impl Drop for Tmp {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn roots() -> R<Roots> {
    Ok(Roots::new(
        ArchiveId::try_from("a".to_owned())?,
        MachineId::try_from("m".to_owned())?,
        Agent::Claude,
        RootNamespace::new("r1")?,
        RootLayout::AgentDir,
    ))
}
fn located(rel: &str) -> R<Located> {
    Ok(locate(&RootRelativePath::parse(rel)?, &roots()?))
}
fn context() -> PolicyContext {
    PolicyContext {
        valid: true,
        revision: "r".into(),
        rules: Some(rules_digest()),
        collector_paths: vec![],
        collector_identities: vec![],
    }
}
fn pack(p: &Path) -> R<u128> {
    let m = fs::metadata(p)?;
    Ok((u128::from(m.dev()) << 64) | u128::from(m.ino()))
}
const T: &str = "claude/projects/-p/abc.jsonl";

#[test]
fn plain_file_resolves_and_is_admitted() -> R {
    let t = Tmp::new()?;
    let f = t.write(T, b"{}\n")?;
    let facts = resolve_facts(&f, &t.root(), &located("projects/-p/abc.jsonl")?);
    assert!(facts.path_resolved && facts.root_resolved);
    assert_eq!(facts.file_identity, Some(pack(&f)?));
    assert!(facts.owner.is_some());
    assert!(matches!(admit(&context(), facts), Admission::Allow(_)));
    Ok(())
}

#[test]
fn ancestors_include_root_and_parents() -> R {
    let t = Tmp::new()?;
    let f = t.write(T, b"x")?;
    let facts = resolve_facts(&f, &t.root(), &located("projects/-p/abc.jsonl")?);
    assert!(facts.ancestor_identities.contains(&pack(&t.root())?));
    assert!(
        facts
            .ancestor_identities
            .contains(&pack(&t.root().join("projects/-p"))?)
    );
    Ok(())
}

#[test]
fn symlink_inside_root_and_chain_record_every_spelling() -> R {
    let t = Tmp::new()?;
    let f = t.write(T, b"x")?;
    let d = t.root().join("projects/-p");
    symlink("abc.jsonl", d.join("b.jsonl"))?;
    symlink("b.jsonl", d.join("c.jsonl"))?;
    let facts = resolve_facts(
        &d.join("c.jsonl"),
        &t.root(),
        &located("projects/-p/c.jsonl")?,
    );
    assert!(facts.path_resolved);
    assert_eq!(facts.file_identity, Some(pack(&f)?));
    for name in ["c.jsonl", "b.jsonl", "abc.jsonl"] {
        assert!(
            facts.identities.iter().any(|i| i.ends_with(name)),
            "{name} in {:?}",
            facts.identities
        );
    }
    Ok(())
}

#[test]
fn symlink_to_credential_is_denied_through_identity_union() -> R {
    let t = Tmp::new()?;
    t.write("claude/.credentials.json", b"secret")?;
    let d = t.root().join("projects/-p");
    fs::create_dir_all(&d)?;
    symlink("../../.credentials.json", d.join("abc.jsonl"))?;
    let facts = resolve_facts(
        &d.join("abc.jsonl"),
        &t.root(),
        &located("projects/-p/abc.jsonl")?,
    );
    assert!(facts.path_resolved);
    match admit(&context(), facts) {
        Admission::Deny(r) => assert_eq!(r, Reason::Credential),
        _ => return Err("not denied".into()),
    }
    Ok(())
}

fn assert_unresolved(facts: memory_core::privacy::ResolvedSourceFacts) {
    assert!(!facts.path_resolved);
    assert_eq!(facts.file_identity, None);
    assert!(matches!(
        admit(&context(), facts),
        Admission::Unknown(UnknownReason::PathUnresolvable)
    ));
}

#[test]
fn loop_dangling_and_hop_overflow_fail_closed() -> R {
    let t = Tmp::new()?;
    t.write(T, b"x")?;
    let d = t.root().join("projects/-p");
    let l = located("projects/-p/a.jsonl")?;
    symlink("b.jsonl", d.join("a.jsonl"))?;
    symlink("a.jsonl", d.join("b.jsonl"))?;
    assert_unresolved(resolve_facts(&d.join("a.jsonl"), &t.root(), &l));
    symlink("nowhere", d.join("dangling.jsonl"))?;
    assert_unresolved(resolve_facts(&d.join("dangling.jsonl"), &t.root(), &l));
    symlink("abc.jsonl", d.join("h0"))?;
    for i in 1..=MAX_HOPS + 2 {
        symlink(format!("h{}", i - 1), d.join(format!("h{i}")))?;
    }
    assert_unresolved(resolve_facts(
        &d.join(format!("h{}", MAX_HOPS + 2)),
        &t.root(),
        &l,
    ));
    // Within the bound it resolves.
    assert!(resolve_facts(&d.join("h5"), &t.root(), &l).path_resolved);
    Ok(())
}

#[test]
fn missing_root_and_directory_target_fail_closed() -> R {
    let t = Tmp::new()?;
    let f = t.write(T, b"x")?;
    let facts = resolve_facts(&f, &t.0.join("absent"), &located("projects/-p/abc.jsonl")?);
    assert!(!facts.root_resolved);
    assert!(matches!(
        admit(&context(), facts),
        Admission::Unknown(UnknownReason::RootUnresolvable)
    ));
    // A directory is not a file identity.
    let facts = resolve_facts(
        &t.root().join("projects"),
        &t.root(),
        &located("projects/-p/abc.jsonl")?,
    );
    assert!(!facts.path_resolved);
    // Unlocated rows carry no owner and the unknown profile.
    let facts = resolve_facts(&f, &t.root(), &located("nothing/here.bin")?);
    assert!(facts.owner.is_none());
    assert!(matches!(
        admit(&context(), facts),
        Admission::Deny(Reason::UnknownKind)
    ));
    Ok(())
}

#[test]
fn file_removed_between_walk_and_resolve_fails_closed() -> R {
    let t = Tmp::new()?;
    let f = t.write(T, b"x")?;
    let w = walk(&[(t.root(), roots()?)], &WalkLimits::default());
    let (path, rel, loc) = w.files.first().ok_or("row")?;
    fs::remove_file(&f)?;
    let mut rfs = RealFs::new();
    let mut handles = BTreeMap::new();
    let (_, src) = to_source(&mut rfs, &mut handles, path, &t.root(), rel, loc).ok_or("source")?;
    assert!(!src.facts.path_resolved);
    assert!(
        to_source(
            &mut rfs,
            &mut handles,
            path,
            &t.root(),
            rel,
            &Located::UnknownKind
        )
        .is_none()
    );
    Ok(())
}

#[test]
fn handles_are_stable_and_withheld_rows_are_counted() -> R {
    let t = Tmp::new()?;
    t.write(T, b"x")?;
    t.write("claude/unknown.txt", b"x")?;
    symlink("abc.jsonl", t.root().join("projects/-p/link.jsonl"))?;
    let fs_ = SharedFs::new();
    let mut s = WalkSources::new(
        vec![(t.root(), roots()?)],
        WalkLimits::default(),
        fs_.clone(),
    );
    let a = s.enumerate();
    let c = s.counts();
    let b = s.enumerate();
    assert_eq!(a.len(), 1);
    assert_eq!(a.first().map(|x| x.1.handle), b.first().map(|x| x.1.handle));
    assert_eq!(
        (c.yielded, c.unknown_kind, c.symlinks, c.unresolved),
        (1, 1, 1, 0)
    );
    // A busy filesystem yields nothing and says so.
    let cell = fs_.cell();
    let _held = cell.borrow_mut();
    assert!(s.enumerate().is_empty());
    assert!(s.counts().busy);
    Ok(())
}

#[test]
fn shared_fs_reports_busy_instead_of_panicking() -> R {
    use memory::environment::FileSystem;
    let mut fs_ = SharedFs::new();
    let cell = fs_.cell();
    let h = cell.borrow_mut().register("/nonexistent-resolve-test");
    assert!(fs_.stat(h)?.is_none());
    assert!(fs_.read(h, 0..1).is_err());
    let _held = cell.borrow_mut();
    assert!(fs_.stat(h).is_err());
    assert!(fs_.read(h, 0..1).is_err());
    Ok(())
}

fn contains(store: &MemStore, needle: &[u8]) -> bool {
    store
        .objects
        .values()
        .any(|(b, _)| b.windows(needle.len()).any(|w| w == needle))
}

#[test]
fn end_to_end_commits_transcript_and_never_a_symlinked_credential() -> R {
    let t = Tmp::new()?;
    t.write(T, b"{\"role\":\"user\",\"text\":\"TRANSCRIPT-MARKER\"}\n")?;
    t.write("claude/.credentials.json", b"CREDENTIAL-MARKER")?;
    let d = t.root().join("projects/-p");
    symlink("../../.credentials.json", d.join("evil.jsonl"))?;
    let fs_ = SharedFs::new();
    let mut s = WalkSources::new(
        vec![(t.root(), roots()?)],
        WalkLimits::default(),
        fs_.clone(),
    );
    let found = s.enumerate();
    assert_eq!(found.len(), 1);
    let owner: Owner = found.first().ok_or("owner")?.0.clone();
    let sources: Vec<_> = found.into_iter().map(|(_, s)| s).collect();
    let ctx = context();
    let env = Env {
        header: Header {
            owner,
            captured_at: Timestamp::try_from("2026-11-07T10:30:00Z".to_owned())?,
            writer: Writer {
                name: "memory".into(),
                version: "0.1.0".into(),
                commit: "abc".into(),
            },
            launch: vec![],
        },
        context: &ctx,
        now_ns: 10_000_000_000,
        piece_len: 4096,
    };
    let mut cap_fs = fs_;
    let got = capture(
        &mut cap_fs,
        &sources,
        &Locals::new(),
        &OwnerState::default(),
        &env,
    );
    let request = got.commit.ok_or("nothing planned")?;
    let mut store = MemStore::default();
    let landed =
        commit(&mut store, None::<&Base>, request, &mut |_| {}).map_err(|e| format!("{e:?}"))?;
    let _ = landed;
    assert!(contains(&store, b"TRANSCRIPT-MARKER"), "positive control");
    assert!(!contains(&store, b"CREDENTIAL-MARKER"));
    assert!(!contains(&store, b"secret"));
    Ok(())
}

#[test]
fn shared_owner_and_profiles_are_literal() -> R {
    use memory_core::privacy::Profile;
    let t = Tmp::new()?;
    // A plan is a shared (sessionless) artifact with the text-asset profile.
    let plan = t.write("claude/plans/p.md", b"plan")?;
    let facts = resolve_facts(&plan, &t.root(), &located("plans/p.md")?);
    assert_eq!(facts.owner.as_deref(), Some("a/m/Claude"));
    assert_eq!(facts.profile, Profile::TextAsset);
    // settings.json is structured config.
    let cfg = t.write("claude/settings.json", b"{}")?;
    let facts = resolve_facts(&cfg, &t.root(), &located("settings.json")?);
    assert_eq!(facts.profile, Profile::Config);
    assert_eq!(facts.owner.as_deref(), Some("a/m/Claude"));
    // An unlocated row has no owner and the unknown profile.
    let facts = resolve_facts(&cfg, &t.root(), &Located::UnknownKind);
    assert_eq!((facts.owner, facts.profile), (None, Profile::Unknown));
    Ok(())
}

#[test]
fn unknown_owner_rows_are_counted_not_yielded() -> R {
    let t = Tmp::new()?;
    t.write(T, b"x")?;
    // A Claude external whose UUID no shell fact names has no derivable owner.
    let rel = "todos/0f0e0d0c-0b0a-4908-8706-050403020100-agent.json";
    assert_eq!(located(rel)?, Located::UnknownOwner);
    t.write(&format!("claude/{rel}"), b"[]")?;
    let mut s = WalkSources::new(
        vec![(t.root(), roots()?)],
        WalkLimits::default(),
        SharedFs::new(),
    );
    assert_eq!(s.enumerate().len(), 1);
    let c = s.counts();
    assert_eq!((c.yielded, c.unknown_owner, c.unknown_kind), (1, 1, 0));
    Ok(())
}
