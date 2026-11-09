//! Capture wiring table tests (slice 2). Synthetic in-memory filesystem and fake store only.
//! Not the real filesystem, not zstd, not R2, not the daemon loop.
use memory::capture::{Env, Locals, Published, Source, SourceOutcome, attach_launches, capture};
use memory::commit::Commit;
use memory::commit::{Base, commit};
use memory::environment::{FileSystem, Stat, Store, StoreError, Stored};
use memory_core::domain::{
    Agent, ArchiveId, Birth, ETag, FileId, MachineId, Owner, SessionId, SessionKey, SourceClass,
    SourceId, SourceKind, WithholdReason,
};
use memory_core::manifest::{Change, Header, OwnerState, Timestamp, Writer};
use memory_core::privacy::{
    PolicyContext, Profile, ResolvedSourceFacts, SanitizedLaunch, UnknownReason, rules_digest,
    sanitize_launch,
};
use std::collections::BTreeMap;

type R<T = ()> = Result<T, String>;
fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}

#[derive(Default)]
struct MemFs {
    files: BTreeMap<u64, Vec<u8>>,
    mtimes: BTreeMap<u64, i64>,
    clock: i64,
}
impl MemFs {
    /// Write a file and advance its mtime, as a real write would.
    fn set(&mut self, source: u64, bytes: &[u8]) {
        self.clock += 1_000;
        self.files.insert(source, bytes.to_vec());
        self.mtimes.insert(source, self.clock);
    }
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
            mtime_ns: self.mtimes.get(&source).copied().unwrap_or(0),
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
}
impl MemStore {
    fn tag(v: u64) -> Result<ETag, StoreError> {
        ETag::try_from(format!("\"v{v}\"")).map_err(|_| StoreError::Server)
    }
    fn put(
        &mut self,
        key: &str,
        body: Published,
        fence: Option<&ETag>,
        create: bool,
    ) -> Result<ETag, StoreError> {
        let existing = self.objects.get(key).map(|(_, v)| *v);
        match (create, existing) {
            (true, Some(_)) => return Err(StoreError::PreconditionFailed),
            (false, None) => return Err(StoreError::NotFound),
            (false, Some(v)) if Some(&Self::tag(v)?) != fence => {
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
        self.put(key, body, None, true)
    }
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &ETag,
        body: Published,
    ) -> Result<ETag, StoreError> {
        self.put(key, body, Some(etag), false)
    }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        let (b, v) = self.objects.get(key).ok_or(StoreError::NotFound)?;
        Ok(Stored {
            body: b.clone(),
            etag: Self::tag(*v)?,
        })
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
fn source(handle: u64, path: &str, class: SourceClass, profile: Profile) -> R<Source> {
    Ok(Source {
        handle,
        id: SourceId::try_from(format!("src{handle}")).map_err(e)?,
        kind: SourceKind::ClaudeTranscript,
        class,
        path: path.to_owned(),
        facts: ResolvedSourceFacts {
            identities: vec![path.to_owned()],
            file_identity: Some(u128::from(handle)),
            ancestor_identities: vec![],
            path_resolved: true,
            root_resolved: true,
            owner: Some("o".to_owned()),
            profile,
        },
    })
}

struct Rig {
    fs: MemFs,
    store: MemStore,
    sources: Vec<Source>,
    locals: Locals,
    base: Option<Base>,
    context: PolicyContext,
    piece_len: usize,
    launches: Vec<SanitizedLaunch>,
}
impl Rig {
    fn new(sources: Vec<Source>) -> Self {
        Self {
            fs: MemFs::default(),
            store: MemStore::default(),
            sources,
            locals: Locals::new(),
            base: None,
            piece_len: 4,
            launches: vec![],
            context: PolicyContext {
                valid: true,
                revision: "r".to_owned(),
                rules: Some(rules_digest()),
                collector_paths: vec![],
                collector_identities: vec![],
            },
        }
    }
    /// One capture + commit. Returns outcomes and whether a commit was issued.
    fn cycle(&mut self) -> R<(Vec<SourceOutcome>, Vec<Change>)> {
        let empty = OwnerState::default();
        let state = self.base.as_ref().map_or(&empty, |b| &b.state);
        let env = Env {
            header: header()?,
            context: &self.context,
            now_ns: 10_000_000_000,
            piece_len: self.piece_len,
        };
        let got = capture(&mut self.fs, &self.sources, &self.locals, state, &env);
        let mut changes = Vec::new();
        if let Some(mut request) = got.commit {
            attach_launches(&mut request, &self.launches).map_err(e)?;
            changes = request.changes.clone();
            let landed =
                commit(&mut self.store, self.base.as_ref(), request, &mut |_| {}).map_err(e)?;
            self.base = Some(landed.base());
        }
        for update in got.updates {
            update.apply(&mut self.locals);
        }
        Ok((got.outcomes.into_iter().map(|(_, o)| o).collect(), changes))
    }
    fn bodies(&self) -> Vec<&[u8]> {
        self.store
            .objects
            .values()
            .map(|(b, _)| b.as_slice())
            .collect()
    }
    fn find(&self, needle: &[u8]) -> bool {
        self.bodies()
            .iter()
            .any(|b| b.windows(needle.len()).any(|w| w == needle))
    }
}

fn transcript(rig: &mut Rig, bytes: &[u8]) {
    rig.fs.set(1, bytes);
}
fn kinds(changes: &[Change]) -> Vec<&'static str> {
    changes
        .iter()
        .map(|c| match c {
            Change::Extend(_) => "extend",
            Change::Epoch(_) => "epoch",
            Change::Replace(_) => "replace",
            Change::Deleted { .. } => "deleted",
        })
        .collect()
}
fn rig() -> R<Rig> {
    Ok(Rig::new(vec![source(
        1,
        "projects/p/s1.jsonl",
        SourceClass::Append,
        Profile::Transcript,
    )?]))
}

#[test]
fn append_extends_then_unchanged_uploads_nothing() -> R {
    let mut rig = rig()?;
    transcript(&mut rig, b"0123456789");
    let (_, first) = rig.cycle()?;
    assert_eq!(kinds(&first), ["epoch"]);
    transcript(&mut rig, b"0123456789abcdef");
    let (_, second) = rig.cycle()?;
    assert_eq!(kinds(&second), ["extend"]);
    let puts = rig.store.version;
    let (outcomes, third) = rig.cycle()?;
    assert!(third.is_empty());
    assert_eq!(outcomes, [SourceOutcome::Unchanged]);
    assert_eq!(rig.store.version, puts, "unchanged must not write");
    Ok(())
}

#[test]
fn rewrite_and_truncate_start_a_new_epoch() -> R {
    let mut rig = rig()?;
    transcript(&mut rig, b"0123456789");
    rig.cycle()?;
    transcript(&mut rig, b"01234");
    let (_, truncated) = rig.cycle()?;
    assert_eq!(kinds(&truncated), ["epoch"]);
    transcript(&mut rig, b"01234567");
    rig.cycle()?;
    transcript(&mut rig, b"X1234567");
    let (_, rewritten) = rig.cycle()?;
    assert_eq!(kinds(&rewritten), ["epoch"]);
    Ok(())
}

#[test]
fn removed_source_is_deleted_only_after_two_observations() -> R {
    let mut rig = rig()?;
    transcript(&mut rig, b"0123");
    rig.cycle()?;
    rig.fs.files.clear();
    let (_, one) = rig.cycle()?;
    assert!(one.is_empty());
    let (_, two) = rig.cycle()?;
    assert_eq!(kinds(&two), ["deleted"]);
    let (_, three) = rig.cycle()?;
    assert!(three.is_empty());
    Ok(())
}

#[test]
fn snapshot_replaces_and_protected_config_never_stores_the_canary() -> R {
    let canary = "CANARYSECRET0451";
    let mut rig = Rig::new(vec![source(
        1,
        "settings.json",
        SourceClass::Snapshot,
        Profile::Config,
    )?]);
    rig.piece_len = 4096;
    let config = format!("{{\"env\":{{\"API_TOKEN\":\"{canary}\"}},\"theme\":\"dark\"}}");
    rig.fs.set(1, config.as_bytes());
    let (outcomes, _) = rig.cycle()?;
    assert!(
        matches!(
            outcomes.as_slice(),
            [SourceOutcome::Planned | SourceOutcome::Withheld(_)]
        ),
        "{outcomes:?}"
    );
    assert!(!rig.find(canary.as_bytes()), "canary reached the store");
    // Positive control: the same search does find a canary in an unprotected transcript.
    let mut control = rig_with_transcript(canary)?;
    control.cycle()?;
    assert!(
        control.find(canary.as_bytes()),
        "search must find a planted canary"
    );
    Ok(())
}

fn rig_with_transcript(text: &str) -> R<Rig> {
    let mut rig = rig()?;
    rig.piece_len = 4096;
    transcript(&mut rig, text.as_bytes());
    Ok(rig)
}

#[test]
fn snapshot_change_is_replaced() -> R {
    let mut rig = Rig::new(vec![source(
        1,
        "notes/plan.md",
        SourceClass::Snapshot,
        Profile::OtherKnown,
    )?]);
    rig.fs.set(1, b"one");
    let (_, first) = rig.cycle()?;
    assert_eq!(kinds(&first), ["replace"]);
    let (_, same) = rig.cycle()?;
    assert!(same.is_empty());
    rig.fs.set(1, b"two!");
    let (_, next) = rig.cycle()?;
    assert_eq!(kinds(&next), ["replace"]);
    Ok(())
}

#[test]
fn withheld_source_is_never_read_or_stored() -> R {
    let mut rig = Rig::new(vec![source(
        1,
        "projects/p/.env",
        SourceClass::Append,
        Profile::Transcript,
    )?]);
    rig.fs.set(1, b"TOKEN=CANARYENV");
    let (outcomes, changes) = rig.cycle()?;
    assert_eq!(
        outcomes,
        [SourceOutcome::Withheld(WithholdReason::Credential)]
    );
    assert!(changes.is_empty());
    assert!(rig.store.objects.is_empty(), "nothing may reach the store");
    Ok(())
}

#[test]
fn captured_pieces_are_stored_under_object_keys() -> R {
    let mut rig = rig()?;
    transcript(&mut rig, b"abcdefgh");
    rig.cycle()?;
    assert!(rig.store.objects.keys().any(|k| k.starts_with("objects/")));
    Ok(())
}

// ---- launch evidence, property 5 at the store sink

fn canary_launch() -> SanitizedLaunch {
    let argv: Vec<String> = [
        "/usr/bin/claude",
        "--api-key",
        "CANARYKEYARGV",
        "--model",
        "sonnet",
        "summarize CANARYPROMPT please",
    ]
    .map(str::to_owned)
    .to_vec();
    let environ: Vec<String> = ["MY_PRIVATE_VAR=CANARYENVVALUE", "HOME=/Users/x"]
        .map(str::to_owned)
        .to_vec();
    sanitize_launch(&argv, &environ)
}

#[test]
fn launch_canaries_never_reach_any_stored_object() -> R {
    let mut rig = rig()?;
    rig.launches = vec![canary_launch(), canary_launch()];
    transcript(&mut rig, b"hello world, nothing secret");
    let (outcomes, _) = rig.cycle()?;
    assert_eq!(outcomes, [SourceOutcome::Planned]);
    assert!(rig.store.objects.keys().any(|k| k.contains("manifest")));
    for canary in [
        "CANARYKEYARGV",
        "CANARYPROMPT",
        "CANARYENVVALUE",
        "MY_PRIVATE_VAR",
    ] {
        for needle in [canary.to_owned(), canary.to_lowercase()] {
            assert!(!rig.find(needle.as_bytes()), "{needle} reached the store");
        }
    }
    // Positive control: the same search finds canaries in an unprotected transcript.
    let mut control = rig_with_transcript("CANARYKEYARGV CANARYPROMPT CANARYENVVALUE")?;
    control.cycle()?;
    for canary in ["CANARYKEYARGV", "CANARYPROMPT", "CANARYENVVALUE"] {
        assert!(control.find(canary.as_bytes()), "control missed {canary}");
    }
    Ok(())
}

#[test]
fn attach_launches_deduplicates_by_digest() -> R {
    let mut commit: Commit = Commit {
        header: header()?,
        changes: vec![],
        uploads: vec![],
    };
    let other = sanitize_launch(&["pi".to_owned()], &[]);
    attach_launches(
        &mut commit,
        &[canary_launch(), other.clone(), canary_launch()],
    )
    .map_err(e)?;
    assert_eq!(commit.header.launch.len(), 2);
    attach_launches(&mut commit, &[other, canary_launch()]).map_err(e)?;
    assert_eq!(commit.header.launch.len(), 2);
    attach_launches(&mut commit, &[]).map_err(e)?;
    assert_eq!(commit.header.launch.len(), 2);
    Ok(())
}

// ---- NeedWindows and failure branches through a recording seam

#[derive(Default)]
struct Spy {
    inner: MemFs,
    reads: Vec<std::ops::Range<u64>>,
    fail_stat: bool,
    fail_read: bool,
    short_read: bool,
}
impl FileSystem for Spy {
    type Error = ();
    fn stat(&mut self, source: u64) -> Result<Option<Stat>, ()> {
        if self.fail_stat {
            return Err(());
        }
        self.inner.stat(source)
    }
    fn stat_len(&mut self, source: u64) -> Result<u64, ()> {
        self.inner.stat_len(source)
    }
    fn read(&mut self, source: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, ()> {
        self.reads.push(range.clone());
        if self.fail_read {
            return Err(());
        }
        let mut bytes = self.inner.read(source, range)?;
        if self.short_read {
            bytes.pop();
        }
        Ok(bytes)
    }
}

fn run_spy(rig: &mut Rig, spy: &mut Spy) -> R<memory::capture::Captured> {
    let empty = OwnerState::default();
    let state = rig.base.as_ref().map_or(&empty, |b| &b.state);
    let env = Env {
        header: header()?,
        context: &rig.context,
        now_ns: 10_000_000_000,
        piece_len: rig.piece_len,
    };
    Ok(capture(spy, &rig.sources, &rig.locals, state, &env))
}

fn kinds_of(got: &memory::capture::Captured) -> Vec<&'static str> {
    got.commit.as_ref().map_or(vec![], |c| kinds(&c.changes))
}

#[test]
fn need_windows_reads_head_and_tail_through_the_seam() -> R {
    // (new content, expected change, expected reads)
    let cases: [(&[u8], &str); 2] = [
        (b"0123456789abcdef", "extend"),
        (b"X123456789abcdef", "epoch"),
    ];
    for (content, expected) in cases {
        let mut rig = rig()?;
        transcript(&mut rig, b"0123456789");
        rig.cycle()?;
        rig.fs.set(1, content);
        let mut spy = Spy {
            inner: std::mem::take(&mut rig.fs),
            ..Spy::default()
        };
        let got = run_spy(&mut rig, &mut spy)?;
        assert_eq!(kinds_of(&got), [expected], "{content:?}");
        assert_eq!(
            got.outcomes.first().map(|o| &o.1),
            Some(&SourceOutcome::Planned)
        );
        // head window, tail window (both 0..10 at the old boundary), then the planned range.
        assert_eq!(spy.reads, [0..10, 0..10, 0..16], "{content:?}");
    }
    Ok(())
}

#[test]
fn failure_branches_have_literal_outcomes_and_store_nothing() -> R {
    #[derive(Clone, Copy)]
    enum Case {
        InvalidContext,
        StatError,
        ReadError,
        ShortRead,
        WindowReadError,
        WindowShortRead,
    }
    let cases = [
        (
            Case::InvalidContext,
            SourceOutcome::Blocked(UnknownReason::ConfigInvalid),
        ),
        (Case::StatError, SourceOutcome::Unavailable),
        (Case::ReadError, SourceOutcome::Unavailable),
        (Case::ShortRead, SourceOutcome::Raced),
        (Case::WindowReadError, SourceOutcome::Unavailable),
        (Case::WindowShortRead, SourceOutcome::Raced),
    ];
    for (case, expected) in cases {
        let mut rig = rig()?;
        transcript(&mut rig, b"0123456789");
        let windows = matches!(case, Case::WindowReadError | Case::WindowShortRead);
        if windows {
            rig.cycle()?;
            rig.fs.set(1, b"0123456789abcdef");
        }
        let objects = rig.store.objects.len();
        let mut spy = Spy {
            inner: std::mem::take(&mut rig.fs),
            fail_stat: matches!(case, Case::StatError),
            fail_read: matches!(case, Case::ReadError | Case::WindowReadError),
            short_read: matches!(case, Case::ShortRead | Case::WindowShortRead),
            ..Spy::default()
        };
        if matches!(case, Case::InvalidContext) {
            rig.context.valid = false;
        }
        let got = run_spy(&mut rig, &mut spy)?;
        assert_eq!(got.outcomes.first().map(|o| &o.1), Some(&expected));
        assert!(got.commit.is_none(), "no commit for {expected:?}");
        assert_eq!(rig.store.objects.len(), objects, "store untouched");
    }
    Ok(())
}

// ---- Coverage round: withhold mapping through admit and transform

fn withheld_for(src: Source, bytes: &[u8], tweak: impl FnOnce(&mut Rig)) -> R<SourceOutcome> {
    let mut rig = Rig::new(vec![src]);
    rig.fs.set(1, bytes);
    tweak(&mut rig);
    let objects = rig.store.objects.len();
    let (outcomes, changes) = rig.cycle()?;
    assert!(changes.is_empty(), "withheld source commits no change");
    assert_eq!(rig.store.objects.len(), objects, "nothing stored");
    outcomes
        .into_iter()
        .next()
        .ok_or_else(|| "no outcome".to_owned())
}

#[test]
fn admission_denials_map_to_literal_withhold_reasons() -> R {
    let collector = withheld_for(
        source(
            1,
            "projects/p/s1.jsonl",
            SourceClass::Append,
            Profile::Transcript,
        )?,
        b"0123",
        |r| r.context.collector_paths = vec!["projects/p/s1.jsonl".to_owned()],
    )?;
    assert_eq!(
        collector,
        SourceOutcome::Withheld(WithholdReason::CollectorState)
    );

    let unknown_kind = withheld_for(
        source(
            1,
            "projects/p/s1.jsonl",
            SourceClass::Append,
            Profile::Unknown,
        )?,
        b"0123",
        |_| {},
    )?;
    assert_eq!(
        unknown_kind,
        SourceOutcome::Withheld(WithholdReason::UnknownKind)
    );

    let mut no_owner = source(
        1,
        "projects/p/s1.jsonl",
        SourceClass::Append,
        Profile::Transcript,
    )?;
    no_owner.facts.owner = None;
    let unknown_owner = withheld_for(no_owner, b"0123", |_| {})?;
    assert_eq!(
        unknown_owner,
        SourceOutcome::Withheld(WithholdReason::UnknownOwner)
    );

    let credential = withheld_for(
        source(
            1,
            ".credentials.json",
            SourceClass::Snapshot,
            Profile::OtherKnown,
        )?,
        b"{}",
        |_| {},
    )?;
    assert_eq!(
        credential,
        SourceOutcome::Withheld(WithholdReason::Credential)
    );
    Ok(())
}

#[test]
fn transform_denials_map_to_literal_withhold_reasons() -> R {
    let malformed = withheld_for(
        source(1, "settings.json", SourceClass::Snapshot, Profile::Config)?,
        b"{not json",
        |_| {},
    )?;
    assert_eq!(
        malformed,
        SourceOutcome::Withheld(WithholdReason::MalformedProtected)
    );

    let secret = withheld_for(
        source(1, "notes.sh", SourceClass::Snapshot, Profile::TextAsset)?,
        b"export API_KEY=literalvalue123\n",
        |_| {},
    )?;
    assert_eq!(
        secret,
        SourceOutcome::Withheld(WithholdReason::SecretInText)
    );

    let mut big = b"{\"a\":\"".to_vec();
    big.resize(8 * 1024 * 1024 + 16, b'x');
    big.extend_from_slice(b"\"}");
    let limit = withheld_for(
        source(1, "settings.json", SourceClass::Snapshot, Profile::Config)?,
        &big,
        |_| {},
    )?;
    assert_eq!(limit, SourceOutcome::Withheld(WithholdReason::SourceLimit));

    // The same transform denial on the append path.
    let append = withheld_for(
        source(1, "settings.json", SourceClass::Append, Profile::Config)?,
        b"{not json",
        |_| {},
    )?;
    assert_eq!(
        append,
        SourceOutcome::Withheld(WithholdReason::MalformedProtected)
    );
    Ok(())
}

#[test]
fn snapshot_read_failures_have_literal_outcomes() -> R {
    for (fail_read, short, expected) in [
        (true, false, SourceOutcome::Unavailable),
        (false, true, SourceOutcome::Raced),
    ] {
        let mut rig = Rig::new(vec![source(
            1,
            "settings.json",
            SourceClass::Snapshot,
            Profile::Config,
        )?]);
        rig.fs.set(1, b"{\"a\":1}");
        let mut spy = Spy {
            inner: std::mem::take(&mut rig.fs),
            fail_read,
            short_read: short,
            ..Spy::default()
        };
        let got = run_spy(&mut rig, &mut spy)?;
        assert_eq!(got.outcomes.first().map(|o| &o.1), Some(&expected));
        assert!(got.commit.is_none());
    }
    Ok(())
}

#[test]
fn snapshot_unchanged_then_changed_after_commit() -> R {
    let mut rig = Rig::new(vec![source(
        1,
        "settings.json",
        SourceClass::Snapshot,
        Profile::Config,
    )?]);
    rig.fs.set(1, b"{\"a\":1}");
    let (o, c) = rig.cycle()?;
    assert_eq!(
        (o, kinds(&c)),
        (vec![SourceOutcome::Planned], vec!["replace"])
    );
    let (o, c) = rig.cycle()?;
    assert_eq!(
        (o, kinds(&c)),
        (vec![SourceOutcome::Unchanged], Vec::<&str>::new())
    );
    rig.fs.set(1, b"{\"a\":2}");
    let (o, c) = rig.cycle()?;
    assert_eq!(
        (o, kinds(&c)),
        (vec![SourceOutcome::Planned], vec!["replace"])
    );
    Ok(())
}

#[test]
fn adopt_prefix_read_failures_have_literal_outcomes() -> R {
    for (fail_read, short, expected) in [
        (true, false, SourceOutcome::Unavailable),
        (false, true, SourceOutcome::Raced),
    ] {
        let mut rig = rig()?;
        transcript(&mut rig, b"0123456789");
        rig.cycle()?;
        // Lose the local index: the remote position must be adopted by rereading the prefix.
        rig.locals = Locals::new();
        let objects = rig.store.objects.len();
        let mut spy = Spy {
            inner: std::mem::take(&mut rig.fs),
            fail_read,
            short_read: short,
            ..Spy::default()
        };
        let got = run_spy(&mut rig, &mut spy)?;
        assert_eq!(got.outcomes.first().map(|o| &o.1), Some(&expected));
        assert!(got.commit.is_none());
        assert_eq!(rig.store.objects.len(), objects);
    }
    Ok(())
}
