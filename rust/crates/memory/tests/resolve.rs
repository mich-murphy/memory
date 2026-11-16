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

// ------------------------------------------------------------------ note / of
use memory::daemon::{Daemon, IndexBody, OwnerOutcome, Parts};
use memory::environment::Clock;
use memory::state::{Loaded, StateFile, read_durable};
use memory_core::domain::SessionId;
use memory_core::hint::{
    HintEvent, HintPath, HintRecord, HintSchema, LaunchHint, PathHint, Reduction,
};
use std::os::unix::fs::PermissionsExt;

fn walker(root: &Path, limits: WalkLimits) -> R<WalkSources> {
    Ok(WalkSources::new(
        vec![(root.to_path_buf(), roots()?)],
        limits,
        SharedFs::new(),
    ))
}
fn key_of(owner: &Owner, s: &memory::capture::Source) -> String {
    format!("{owner:?}|{:?}|{}", s.id, s.handle)
}
fn owner_at(rel: &str) -> R<Owner> {
    match located(rel)? {
        Located::Source { owner, .. } => Ok(owner),
        _ => Err("not a source".into()),
    }
}

#[test]
fn note_maps_a_path_to_its_owner_in_either_spelling() -> R {
    let t = Tmp::new()?;
    let file = t.write(T, b"{}\n")?;
    let link = t.0.join("link");
    symlink(t.root(), &link)?;
    let want = owner_at("projects/-p/abc.jsonl")?;
    // Configured through a link; reported canonically, as FSEvents does.
    let mut w = walker(&link, WalkLimits::default())?;
    assert_eq!(w.note(&fs::canonicalize(&file)?), Some(want.clone()));
    assert_eq!(
        w.note(&link.join("projects/-p/abc.jsonl")),
        Some(want.clone())
    );
    assert_eq!(w.of(&want).len(), 1);
    // Configured canonically; reported through the link is not a spelling of the root.
    let mut w = walker(&fs::canonicalize(t.root())?, WalkLimits::default())?;
    assert_eq!(w.note(&fs::canonicalize(&file)?), Some(want));
    Ok(())
}

#[test]
fn note_ignores_paths_outside_the_roots_and_paths_locate_rejects() -> R {
    let t = Tmp::new()?;
    t.write(T, b"{}\n")?;
    t.write("other/projects/-p/abc.jsonl", b"{}\n")?;
    let mut w = walker(&t.root(), WalkLimits::default())?;
    assert_eq!(w.note(&t.0.join("other/projects/-p/abc.jsonl")), None);
    assert_eq!(w.note(&t.0.join("claude2/projects/-p/abc.jsonl")), None);
    assert_eq!(w.note(&t.root()), None);
    assert_eq!(w.note(Path::new("relative/projects/-p/abc.jsonl")), None);
    assert_eq!(w.note(&t.root().join("projects/-p/notes.txt")), None);
    assert_eq!(w.note(&t.root().join("zzz/unknown.bin")), None);
    Ok(())
}

#[test]
fn of_yields_exactly_what_enumerate_yields() -> R {
    let t = Tmp::new()?;
    t.write("claude/projects/-p/a.jsonl", b"{}\n")?;
    t.write("claude/projects/-q/b.jsonl", b"{}\n")?;
    t.write("ext/-z/c.jsonl", b"{}\n")?;
    t.write("claude/projects/-p/x/y/deep.jsonl", b"{}\n")?;
    symlink(
        t.root().join("projects/-p/a.jsonl"),
        t.root().join("projects/-p/s.jsonl"),
    )?;
    symlink(t.0.join("ext/-z"), t.root().join("projects/-z"))?;
    let limits = WalkLimits {
        max_depth: 3,
        ..WalkLimits::default()
    };
    let paths: Vec<PathBuf> = [
        "projects/-p/a.jsonl",
        "projects/-q/b.jsonl",
        "projects/-p/s.jsonl",
        "projects/-z/c.jsonl",
        "projects/-p/x/y/deep.jsonl",
    ]
    .iter()
    .map(|p| t.root().join(p))
    .collect();
    let union = |w: &mut WalkSources, owners: &[Owner]| -> Vec<String> {
        let mut got: Vec<String> = owners
            .iter()
            .flat_map(|o| w.of(o).iter().map(|s| key_of(o, s)).collect::<Vec<_>>())
            .collect();
        got.sort();
        got
    };
    // Every path noted first, then the walk.
    let mut w = walker(&t.root(), limits)?;
    let mut owners: Vec<Owner> = Vec::new();
    for p in &paths {
        if let Some(o) = w.note(p)
            && !owners.contains(&o)
        {
            owners.push(o);
        }
    }
    for p in paths.iter().skip(2).take(2) {
        assert!(w.note(p).is_some());
    }
    let before = union(&mut w, &owners);
    let mut walked: Vec<String> = w.enumerate().iter().map(|(o, s)| key_of(o, s)).collect();
    walked.sort();
    assert_eq!(walked.len(), 2, "{walked:?}");
    assert_eq!(before, walked, "of before the walk");
    assert_eq!(union(&mut w, &owners), walked, "of after the walk");
    // The walk first, then every path noted.
    let mut w = walker(&t.root(), limits)?;
    let found = w.enumerate();
    let mut owners: Vec<Owner> = found.iter().map(|f| f.0.clone()).collect();
    for p in &paths {
        if let Some(o) = w.note(p)
            && !owners.contains(&o)
        {
            owners.push(o);
        }
    }
    let mut walked: Vec<String> = found.iter().map(|(o, s)| key_of(o, s)).collect();
    walked.sort();
    assert_eq!(union(&mut w, &owners), walked);
    Ok(())
}

#[test]
fn of_and_the_walk_agree_on_the_depth_boundary() -> R {
    let t = Tmp::new()?;
    let file = t.write(T, b"{}\n")?;
    for (max_depth, want) in [(2, 1), (1, 0)] {
        let limits = WalkLimits {
            max_depth,
            ..WalkLimits::default()
        };
        let mut w = walker(&t.root(), limits)?;
        assert_eq!(w.enumerate().len(), want, "walk at {max_depth}");
        let mut w = walker(&t.root(), limits)?;
        let noted = w.note(&file);
        let of = noted.map_or(0, |o| w.of(&o).len());
        assert_eq!(of, want, "of at {max_depth}");
    }
    Ok(())
}

#[test]
fn of_does_not_yield_a_vanished_file_and_does_not_walk() -> R {
    let t = Tmp::new()?;
    let file = t.write(T, b"{}\n")?;
    t.write("claude/projects/-q/b.jsonl", b"{}\n")?;
    let want = owner_at("projects/-p/abc.jsonl")?;
    let mut w = walker(&t.root(), WalkLimits::default())?;
    assert_eq!(w.enumerate().len(), 2);
    assert_eq!(w.counts().unreadable, 0);
    assert_eq!(w.of(&want).len(), 1);
    // A walk would meet the unreadable directory and count it; `of` must not touch it.
    let sibling = t.root().join("projects/-q");
    fs::set_permissions(&sibling, fs::Permissions::from_mode(0o000))?;
    let yielded = w.of(&want).len();
    let unreadable = w.counts().unreadable;
    fs::set_permissions(&sibling, fs::Permissions::from_mode(0o700))?;
    assert_eq!((yielded, unreadable), (1, 0), "of walked the tree");
    fs::remove_file(&file)?;
    assert!(w.of(&want).is_empty());
    assert_eq!(w.note(&file), Some(want.clone()));
    assert!(w.of(&want).is_empty());
    Ok(())
}

struct Clk;
impl Clock for Clk {
    fn monotonic_ns(&self) -> u64 {
        0
    }
    fn wall_ns(&self) -> i128 {
        1_794_000_000_000_000_000
    }
}

type RealDaemon = Daemon<SharedFs, MemStore, Clk, WalkSources>;

/// A daemon over the real filesystem and `WalkSources` on the root of `t`, state under `t`.
fn real_daemon(t: &Tmp) -> R<RealDaemon> {
    let token = t.0.join("token");
    fs::write(&token, "x")?;
    fs::set_permissions(&token, fs::Permissions::from_mode(0o600))?;
    let cfg = t.0.join("config.toml");
    fs::write(
        &cfg,
        format!(
            "archive_id = \"a\"\nmachine_id = \"m\"\nendpoint = \"http://127.0.0.1:9\"\nbucket = \"bucket-1\"\ntoken_path = \"{}\"\n",
            token.display()
        ),
    )?;
    let shared = SharedFs::new();
    let sources = WalkSources::new(
        vec![(t.root(), roots()?)],
        WalkLimits::default(),
        shared.clone(),
    );
    let state = t.0.join("state");
    let d = Daemon::start(
        &state,
        &cfg,
        Parts {
            fs: shared,
            store: MemStore::default(),
            clock: Clk,
            sources,
            context: context(),
        },
    )
    .map_err(|x| x.code().to_owned())?;
    Ok(d)
}

#[test]
fn a_hint_names_a_file_no_walk_has_seen_and_its_owner_is_captured() -> R {
    let t = Tmp::new()?;
    fs::set_permissions(&t.0, fs::Permissions::from_mode(0o700))?;
    t.write(T, b"{\"a\":1}\n")?;
    let mut d = real_daemon(&t)?;
    assert_eq!(d.reconcile_once().map_err(|x| format!("{x:?}"))?.landed, 1);
    // Created after the walk and never reported by the watcher: only the hint names it.
    let new = t.write("claude/projects/-p/new.jsonl", b"{\"b\":1}\n")?;
    let owner = owner_at("projects/-p/new.jsonl")?;
    assert_eq!(
        d.capture_owner(&owner).map_err(|x| format!("{x:?}"))?,
        OwnerOutcome::Unchanged,
        "unknown until something names it"
    );
    let path =
        HintPath::try_from(new.to_str().ok_or("path")?.to_owned()).map_err(|x| format!("{x:?}"))?;
    let record = HintRecord {
        schema: HintSchema::V1,
        agent: Agent::Claude,
        event: HintEvent::SessionEnd,
        session: SessionId::try_from("new".to_owned())?,
        cwd: PathHint::Absent,
        transcript: PathHint::Present { path },
        agent_transcript: PathHint::Absent,
        launch: LaunchHint::None,
        reduction: Reduction::None,
    };
    let mut line = serde_json::to_vec(&record)?;
    line.push(b'\n');
    fs::write(t.0.join("state/hints/2026-11-07.jsonl"), line)?;
    let signals = d.refresh_hints();
    assert_eq!(signals.len(), 1);
    let signal = signals.first().ok_or("no signal")?;
    assert_eq!(
        signal.owner, owner,
        "the hint and the locator name one owner"
    );
    assert_eq!(signal.paths, vec![new]);
    assert_eq!(
        d.capture_owner(&owner).map_err(|x| format!("{x:?}"))?,
        OwnerOutcome::Landed
    );
    Ok(())
}

#[test]
fn a_vanished_file_is_never_an_absence_for_an_owner_capture() -> R {
    let t = Tmp::new()?;
    fs::set_permissions(&t.0, fs::Permissions::from_mode(0o700))?;
    let file = t.write(T, b"{\"a\":1}\n")?;
    let state = t.0.join("state");
    let mut d = real_daemon(&t)?;
    assert_eq!(d.reconcile_once().map_err(|x| format!("{x:?}"))?.landed, 1);
    let owner = d.note_path(&file).ok_or("not noted")?;
    fs::write(&file, b"{\"a\":1}\n{\"a\":2}\n")?;
    assert_eq!(
        d.capture_owner(&owner).map_err(|x| format!("{x:?}"))?,
        OwnerOutcome::Landed
    );
    let objects = d.store_mut().objects.len();
    fs::remove_file(&file)?;
    for _ in 0..4 {
        assert_eq!(
            d.capture_owner(&owner).map_err(|x| format!("{x:?}"))?,
            OwnerOutcome::Unchanged
        );
    }
    assert_eq!(
        d.store_mut().objects.len(),
        objects,
        "no deletion was written"
    );
    d.shutdown().map_err(|x| format!("{x:?}"))?;
    let Loaded::Present(stored) = read_durable(
        &state.join(StateFile::Index.name()),
        StateFile::Index.schema(),
    ) else {
        return Err("no index".into());
    };
    let index: IndexBody = serde_json::from_slice(&stored.body)?;
    assert!(!index.sources.is_empty());
    assert!(
        index
            .sources
            .values()
            .all(|s| s.missing == 0 && !s.deletion_recorded)
    );
    Ok(())
}
