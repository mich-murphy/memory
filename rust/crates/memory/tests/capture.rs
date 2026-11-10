//! Capture wiring table tests (slice 2). Synthetic in-memory filesystem and fake store only.
//! Not the real filesystem, not zstd, not R2, not the daemon loop.
use memory::capture::{Env, Locals, Source, SourceOutcome, attach_launches, capture};
use memory::commit::Commit;
use memory::commit::{Base, commit};
use memory::environment::{FileSystem, Stat};
use memory_core::domain::{
    Agent, ArchiveId, MachineId, Owner, SessionId, SessionKey, SourceClass, SourceId, SourceKind,
    WithholdReason,
};
use memory_core::manifest::{Change, Header, OwnerState, Timestamp, Writer, manifest_hash};
use memory_core::privacy::{
    PolicyContext, Profile, ResolvedSourceFacts, SanitizedLaunch, UnknownReason, rules_digest,
    sanitize_launch,
};

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
    let cases: [(&[u8], &str, std::ops::Range<u64>); 2] = [
        (b"0123456789abcdef", "extend", 10..16),
        (b"X123456789abcdef", "epoch", 0..16),
    ];
    for (content, expected, planned) in cases {
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
        // head window, tail window (both 0..10 at the old boundary), then the planned range:
        // only the appended delta for an extend, the whole stream for an epoch.
        assert_eq!(spy.reads, [0..10, 0..10, planned], "{content:?}");
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

// ---- Round 3: exact bytes, windows and manifest fields (cargo-mutants gaps in capture.rs)

/// Deterministic content: byte `i` is a pure function of `i`, so a longer file always
/// starts with every shorter one.
fn pattern(len: usize) -> Vec<u8> {
    (0..len)
        .map(|i| u8::try_from((i * 7 + 3) % 251).unwrap_or(0))
        .collect()
}
fn span(bytes: &[u8], range: std::ops::Range<usize>) -> R<&[u8]> {
    bytes
        .get(range)
        .ok_or_else(|| "range outside bytes".to_owned())
}
fn index_of(rig: &Rig) -> R<memory_core::domain::SourceState> {
    let id = &rig.sources.first().ok_or("no source")?.id;
    rig.locals
        .get(id)
        .and_then(|l| l.index.clone())
        .ok_or_else(|| "no index".to_owned())
}

/// First capture of `old` bytes, then growth to `new`: the second capture must publish
/// exactly the delta, describe it with literal piece offsets and lengths, record the exact
/// head and tail window digests of the new stream, and the next growth must still extend
/// (an index holding wrong windows would force a spurious epoch).
fn extend_records_exact_windows(
    (old, new): (usize, usize),
    piece_len: usize,
    pieces: &[(u64, u64)],
    head: std::ops::Range<usize>,
    tail: std::ops::Range<usize>,
) -> R {
    let data = pattern(new + 10);
    let mut rig = rig()?;
    rig.piece_len = piece_len;
    transcript(&mut rig, span(&data, 0..old)?);
    assert_eq!(kinds(&rig.cycle()?.1), ["epoch"]);
    transcript(&mut rig, span(&data, 0..new)?);
    let (outcomes, changes) = rig.cycle()?;
    assert_eq!(outcomes, [SourceOutcome::Planned]);
    let [Change::Extend(write)] = changes.as_slice() else {
        return Err(format!("expected one extend, got {:?}", kinds(&changes)));
    };
    assert_eq!(write.size, u64::try_from(new).map_err(e)?);
    assert_eq!(write.sha256, manifest_hash(span(&data, 0..new)?));
    let described: Vec<(u64, u64)> = write
        .pieces
        .iter()
        .map(|p| (p.offset().get(), p.len()))
        .collect();
    assert_eq!(described, pieces);
    // The uploaded objects are exactly the appended bytes, in order.
    let mut uploaded = Vec::new();
    for piece in &write.pieces {
        let (body, _) = rig
            .store
            .objects
            .get(piece.object())
            .ok_or("piece object")?;
        uploaded.extend_from_slice(body);
    }
    assert_eq!(uploaded, span(&data, old..new)?);
    let state = index_of(&rig)?;
    assert_eq!(
        state.position.captured_to.get(),
        u64::try_from(new).map_err(e)?
    );
    assert_eq!(state.head, manifest_hash(span(&data, head)?));
    assert_eq!(state.tail, manifest_hash(span(&data, tail)?));
    transcript(&mut rig, span(&data, 0..new + 10)?);
    let (_, next) = rig.cycle()?;
    assert_eq!(kinds(&next), ["extend"]);
    Ok(())
}

#[test]
fn extend_across_the_head_window_publishes_exact_delta_and_window_digests() -> R {
    // Head window [0, 4096) is shorter than the file; the tail window is the whole file.
    extend_records_exact_windows(
        (5000, 8000),
        1000,
        &[(5000, 1000), (6000, 1000), (7000, 1000)],
        0..4096,
        0..8000,
    )
}

#[test]
fn extend_across_the_tail_window_publishes_exact_delta_and_window_digests() -> R {
    // 72,000 > 65,536: the tail window starts at 6,464 and no longer overlaps the head.
    extend_records_exact_windows(
        (70_000, 72_000),
        8192,
        &[(70_000, 2000)],
        0..4096,
        6464..72_000,
    )
}

#[test]
fn piece_size_is_capped_at_eight_mebibytes_whatever_the_request() -> R {
    let mut rig = rig()?;
    rig.piece_len = 64 * 1024 * 1024;
    let data = pattern(8 * 1024 * 1024 + 5);
    transcript(&mut rig, &data);
    let (_, changes) = rig.cycle()?;
    let [Change::Epoch(write)] = changes.as_slice() else {
        return Err(format!("expected one epoch, got {:?}", kinds(&changes)));
    };
    let described: Vec<(u64, u64)> = write
        .pieces
        .iter()
        .map(|p| (p.offset().get(), p.len()))
        .collect();
    assert_eq!(described, [(0, 8_388_608), (8_388_608, 5)]);
    Ok(())
}

#[test]
fn extend_from_an_empty_capture_reads_nothing_beyond_the_delta() -> R {
    let mut rig = rig()?;
    transcript(&mut rig, b"");
    assert_eq!(kinds(&rig.cycle()?.1), ["epoch"]);
    rig.fs.set(1, b"0123456789");
    let mut spy = Spy {
        inner: std::mem::take(&mut rig.fs),
        ..Spy::default()
    };
    let got = run_spy(&mut rig, &mut spy)?;
    assert_eq!(kinds_of(&got), ["extend"]);
    // Two empty window reads at the old boundary, then the delta; deriving the new windows
    // from the verified prefix and the delta needs no further read, not even an empty one.
    assert_eq!(spy.reads, [0..0, 0..0, 0..10]);
    Ok(())
}

#[test]
fn non_prefix_stable_source_with_an_index_is_read_whole_not_as_a_delta() -> R {
    let mut rig = rig()?;
    transcript(&mut rig, b"{\"a\":1}\n");
    assert_eq!(kinds(&rig.cycle()?.1), ["epoch"]);
    assert!(index_of(&rig)?.whole_state.is_some());
    // The source is now recognized as a protected JSON document: transforming a delta alone
    // is invalid, so the whole stream must be read and transformed again.
    rig.sources.first_mut().ok_or("source")?.facts.identities = vec!["settings.json".to_owned()];
    // Still one valid document (trailing whitespace), so the whole stream transforms fine
    // while the lone delta "\n" is not a document at all.
    rig.fs.set(1, b"{\"a\":1}\n\n");
    let mut spy = Spy {
        inner: std::mem::take(&mut rig.fs),
        ..Spy::default()
    };
    let got = run_spy(&mut rig, &mut spy)?;
    assert_eq!(
        got.outcomes.first().map(|o| &o.1),
        Some(&SourceOutcome::Planned)
    );
    assert_eq!(kinds_of(&got), ["extend"]);
    assert_eq!(spy.reads.last(), Some(&(0..9)), "whole stream, not 8..9");
    Ok(())
}
