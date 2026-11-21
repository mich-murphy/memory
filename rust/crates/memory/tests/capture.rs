//! Capture wiring table tests (slice 2). Synthetic in-memory filesystem and fake store only.
//! Not the real filesystem, not zstd, not R2, not the daemon loop.
use memory::audit::AuditCounts;
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
    /// Audit verdicts of the last cycle's capture step.
    audit: AuditCounts,
    /// Wall time of the next capture step.
    now_ns: i64,
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
            audit: AuditCounts::default(),
            now_ns: 10_000_000_000,
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
            now_ns: self.now_ns,
            piece_len: self.piece_len,
        };
        let got = capture(&mut self.fs, &self.sources, &self.locals, state, &env);
        self.audit = got.audit;
        let mut changes = Vec::new();
        if let Some(mut request) = got.commit {
            attach_launches(&mut request, &self.launches);
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
    /// Every stored body, plus the decoded form of each piece object (pieces are zstd frames,
    /// so a plaintext search must look inside them as well as at the stored bytes).
    fn bodies(&self) -> Vec<Vec<u8>> {
        let mut out = Vec::new();
        for (key, (body, _)) in &self.store.objects {
            out.push(body.clone());
            if key.starts_with("objects/")
                && let Ok(raw) = zstd::decode_all(body.as_slice())
            {
                out.push(raw);
            }
        }
        out
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
    );
    assert_eq!(commit.header.launch.len(), 2);
    attach_launches(&mut commit, &[other, canary_launch()]);
    assert_eq!(commit.header.launch.len(), 2);
    attach_launches(&mut commit, &[]);
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
        // only the appended delta for an extend, the whole stream for an epoch. The planned
        // range is streamed in contiguous reads of at most piece_len (4) bytes.
        assert_eq!(spy.reads.get(..2), Some(&[0..10, 0..10][..]), "{content:?}");
        let rest = spy.reads.get(2..).unwrap_or_default();
        assert_eq!(
            rest.first().map(|r| r.start),
            Some(planned.start),
            "{content:?}"
        );
        assert_eq!(rest.last().map(|r| r.end), Some(planned.end), "{content:?}");
        assert!(
            rest.iter()
                .zip(rest.iter().skip(1))
                .all(|(a, b)| a.end == b.start),
            "{content:?}: {rest:?}"
        );
        assert!(
            rest.iter().all(|r| r.end - r.start <= 4),
            "{content:?}: {rest:?}"
        );
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
        uploaded.extend(memory_reader::decode_piece(piece, body).map_err(e)?);
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
    // The delta is streamed in piece_len (4) chunks, not read in one call.
    assert_eq!(spy.reads, [0..0, 0..0, 0..4, 4..8, 8..10]);
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

#[test]
fn stored_pieces_are_zstd_frames_that_match_their_descriptors() -> R {
    let mut rig = rig()?;
    rig.piece_len = 400;
    let data = b"compressible line of transcript text\n".repeat(40);
    transcript(&mut rig, &data);
    let (outcomes, changes) = rig.cycle()?;
    assert_eq!(outcomes, [SourceOutcome::Planned]);
    let [Change::Epoch(write)] = changes.as_slice() else {
        return Err(format!("expected one epoch, got {:?}", kinds(&changes)));
    };
    assert!(write.pieces.len() > 1);
    let mut restored = Vec::new();
    for piece in &write.pieces {
        let (body, _) = rig
            .store
            .objects
            .get(piece.object())
            .ok_or("piece object")?;
        assert_eq!(
            body.get(..4),
            Some(&[0x28, 0xb5, 0x2f, 0xfd][..]),
            "zstd magic"
        );
        let start = usize::try_from(piece.offset().get()).map_err(e)?;
        let len = usize::try_from(piece.len()).map_err(e)?;
        let raw = span(&data, start..start + len)?;
        assert_ne!(body.as_slice(), raw, "stored bytes differ from raw");
        assert!(body.len() < raw.len(), "compressible input shrinks");
        assert_eq!(piece.encoded_len(), u64::try_from(body.len()).map_err(e)?);
        assert_eq!(piece.encoded_sha256(), manifest_hash(body));
        let decoded = memory_reader::decode_piece(piece, body).map_err(e)?;
        assert_eq!(manifest_hash(&decoded), piece.sha256());
        restored.extend(decoded);
    }
    assert_eq!(restored, data);
    Ok(())
}

/// Restore the latest epoch of the first source from the rig's store through `memory_reader`.
fn restore_latest(rig: &Rig) -> R<Vec<u8>> {
    let id = &rig.sources.first().ok_or("no source")?.id;
    let base = rig.base.as_ref().ok_or("no base")?;
    let entry = base.state.sources.get(id).ok_or("no entry")?;
    memory_reader::restore_source(entry, |key| {
        rig.store
            .objects
            .get(key)
            .map(|(body, _)| body.clone())
            .ok_or_else(|| "piece object missing".to_owned())
    })
    .map_err(|err| format!("restore failed: {err:?}"))
}

/// Facts-table row 7: an extension that cannot resume the digest re-reads `[0, to)`. A byte
/// rewritten strictly between the windows, with growth, passes the window check; the committed
/// digest is then of the new bytes while the stored pieces before `from` are the old ones. The
/// committed epoch must still restore to its own digest and to the file.
#[test]
fn non_resumed_extension_after_a_between_windows_rewrite_restores_to_the_file() -> R {
    let mut rig = rig()?;
    rig.piece_len = 1 << 20;
    let old = pattern(200 * 1024);
    transcript(&mut rig, &old);
    assert_eq!(kinds(&rig.cycle()?.1), ["epoch"]);
    // No resumable state: the next extension reads [0, to) again.
    let id = rig.sources.first().ok_or("no source")?.id.clone();
    let local = rig.locals.get_mut(&id).ok_or("no local")?;
    local.index.as_mut().ok_or("no index")?.whole_state = None;
    let mut new = old.clone();
    let at = 50_000;
    *new.get_mut(at).ok_or("byte")? ^= 0xff;
    new.extend_from_slice(&pattern(1_000));
    transcript(&mut rig, &new);
    let (_, changes) = rig.cycle()?;
    let restored = restore_latest(&rig);
    assert_eq!(
        restored.as_ref().map(Vec::len),
        Ok(new.len()),
        "kinds {:?}: the latest epoch restores",
        kinds(&changes)
    );
    assert_eq!(restored, Ok(new), "the latest epoch equals the file");
    // The grown file fails the due audit's stat pre-check (one ineligible); the prefix that no
    // longer hashes to the committed digest starts the epoch and is counted as a mismatch.
    assert_eq!(
        rig.audit,
        AuditCounts {
            matched: 0,
            mismatched: 1,
            ineligible: 1,
        }
    );
    Ok(())
}

const HOUR_NS: i64 = 3_600 * 1_000_000_000;

fn audit_of(rig: &Rig) -> R<memory_core::domain::AuditState> {
    Ok(index_of(rig)?.audit)
}

/// Grow the file and flip one byte under the committed boundary, without resumable state, so the
/// next extension re-reads the prefix and finds it changed.
fn rewrite_and_grow(rig: &mut Rig, bytes: &mut Vec<u8>, at: usize) -> R {
    let id = rig.sources.first().ok_or("no source")?.id.clone();
    let local = rig.locals.get_mut(&id).ok_or("no local")?;
    local.index.as_mut().ok_or("no index")?.whole_state = None;
    *bytes.get_mut(at).ok_or("byte")? ^= 0xff;
    bytes.extend_from_slice(&pattern(1_000));
    transcript(rig, bytes);
    Ok(())
}

/// Guarantee: the prefix site parks a prefix-stable source at the third failure spanning 3 h,
/// starting no epoch and leaving the index entry as it was.
#[test]
fn a_prefix_mismatch_parks_at_three_failures_over_three_hours() -> R {
    let mut rig = rig()?;
    rig.piece_len = 1 << 20;
    let mut bytes = pattern(200 * 1024);
    transcript(&mut rig, &bytes);
    rig.cycle()?;
    for (n, at) in [(1_u32, 50_000_usize), (2, 51_000), (3, 52_000)] {
        rig.now_ns = 10_000_000_000 + i64::from(n) * 1_000_000_000;
        rewrite_and_grow(&mut rig, &mut bytes, at)?;
        let (outcomes, changes) = rig.cycle()?;
        assert_eq!(outcomes, [SourceOutcome::Planned], "failure {n}");
        assert_eq!(kinds(&changes), ["epoch"]);
        assert_eq!(rig.audit.mismatched, 1);
        assert_eq!(audit_of(&rig)?.consecutive_failures, n);
    }
    let before = index_of(&rig)?;
    let puts = rig.store.version;
    rig.now_ns = 11_000_000_000 + HOUR_NS * 3;
    rewrite_and_grow(&mut rig, &mut bytes, 53_000)?;
    let before = memory_core::domain::SourceState {
        whole_state: None,
        ..before
    };
    let (outcomes, changes) = rig.cycle()?;
    assert_eq!(
        outcomes,
        [SourceOutcome::Parked(
            memory_core::domain::ParkReason::RepeatedAuditMismatch
        )]
    );
    assert!(changes.is_empty());
    assert_eq!(rig.audit.mismatched, 0, "a park is not counted");
    assert_eq!(rig.store.version, puts);
    assert_eq!(index_of(&rig)?, before, "the index entry is unchanged");
    Ok(())
}

/// Guarantee: a source that is not prefix-stable restarts on every extension (so the restore
/// stays the file) without counting a failure, however many and however far apart.
#[test]
fn a_source_that_is_not_prefix_stable_never_parks_at_the_prefix_site() -> R {
    let mut rig = Rig::new(vec![source(
        1,
        "projects/p/settings.json",
        SourceClass::Append,
        Profile::Transcript,
    )?]);
    rig.piece_len = 1 << 20;
    let doc = |n: i64| {
        let keys: Vec<String> = (0..=n).map(|k| format!("\"k{k}\":{k}")).collect();
        format!("{{{}}}", keys.join(",")).into_bytes()
    };
    transcript(&mut rig, &doc(0));
    rig.cycle()?;
    let mut epochs = 1;
    for n in 1..7_i64 {
        rig.now_ns = 10_000_000_000 + n * 50 * HOUR_NS;
        let text = doc(n);
        transcript(&mut rig, &text);
        let (outcomes, changes) = rig.cycle()?;
        assert_eq!(outcomes, [SourceOutcome::Planned], "step {n}");
        assert_eq!(rig.audit.mismatched, 0, "step {n}");
        assert_eq!(audit_of(&rig)?.consecutive_failures, 0, "step {n}");
        epochs += changes
            .iter()
            .filter(|c| matches!(c, Change::Epoch(_)))
            .count();
        let restored: serde_json::Value =
            serde_json::from_slice(&restore_latest(&rig)?).map_err(e)?;
        let want: serde_json::Value = serde_json::from_slice(&text).map_err(e)?;
        assert_eq!(restored, want, "step {n}");
    }
    assert!(epochs >= 2, "extensions restarted the epoch: {epochs}");
    Ok(())
}

/// Guarantee: the prefix site is reached by a source that is not prefix-stable (the windows
/// accept the extension, the transformed prefix differs) and restarts the epoch without counting
/// a failure or a mismatch.
#[test]
fn a_non_prefix_stable_prefix_difference_restarts_without_a_failure() -> R {
    let mut rig = Rig::new(vec![source(
        1,
        "projects/p/settings.json",
        SourceClass::Append,
        Profile::Transcript,
    )?]);
    rig.piece_len = 1 << 20;
    let doc = |value: u32| {
        let keys: Vec<String> = (0..8000_u32)
            .map(|k| format!("\"k{k:05}\":{}", if k == 3000 { value } else { k }))
            .collect();
        format!("{{{}}}", keys.join(",")).into_bytes()
    };
    let old = doc(3000);
    assert!(old.len() > 110_000);
    transcript(&mut rig, &old);
    rig.cycle()?;
    for n in 1..=4_i64 {
        let mut new = doc(u32::try_from(3000 + n).map_err(e)?);
        new.extend_from_slice(&b" ".repeat(usize::try_from(3 * n).map_err(e)?));
        rig.now_ns = 10_000_000_000 + n * 50 * HOUR_NS;
        transcript(&mut rig, &new);
        let (outcomes, changes) = rig.cycle()?;
        assert_eq!(outcomes, [SourceOutcome::Planned], "step {n}");
        assert_eq!(
            kinds(&changes),
            ["epoch"],
            "step {n}: the prefix site restarts"
        );
        assert_eq!(rig.audit.mismatched, 0, "step {n}");
        assert_eq!(audit_of(&rig)?.consecutive_failures, 0, "step {n}");
        let restored: serde_json::Value =
            serde_json::from_slice(&restore_latest(&rig)?).map_err(e)?;
        let want: serde_json::Value =
            serde_json::from_slice(&doc(u32::try_from(3000 + n).map_err(e)?)).map_err(e)?;
        assert_eq!(restored, want, "step {n}");
    }
    Ok(())
}

// ---- identity change is verified by content (brief 39)

/// A captured source replaced by a new FileId with the same bytes is Unchanged, the new id is
/// recorded, and the next round reads nothing.
#[test]
fn identity_change_same_bytes_is_unchanged_and_records_the_new_id() -> R {
    let mut rig = rig()?;
    transcript(&mut rig, b"0123456789");
    rig.cycle()?;
    let old = index_of(&rig)?.observed.id;
    rig.fs.replace(1, b"0123456789", 1);
    let (outcomes, changes) = rig.cycle()?;
    assert!(changes.is_empty());
    assert_eq!(outcomes, [SourceOutcome::Unchanged]);
    let new = index_of(&rig)?.observed.id;
    assert_ne!(new, old, "the stat refresh records the new id");
    // The adopted entry has never been audited, so the next round audits once (existing rule)
    // and finds the epoch unchanged; the round after that reads nothing.
    let (outcomes, changes) = rig.cycle()?;
    assert!(changes.is_empty());
    assert_eq!(outcomes, [SourceOutcome::Unchanged]);
    assert_eq!(index_of(&rig)?.observed.id, new);
    let mut spy = Spy {
        inner: std::mem::take(&mut rig.fs),
        ..Spy::default()
    };
    let got = run_spy(&mut rig, &mut spy)?;
    assert!(kinds_of(&got).is_empty());
    assert!(spy.reads.is_empty(), "nothing read again: {:?}", spy.reads);
    assert_eq!(
        got.outcomes.first().map(|o| &o.1),
        Some(&SourceOutcome::Unchanged)
    );
    Ok(())
}

/// Same prefix, more bytes, new id: one Extend that reads only from captured_to.
#[test]
fn identity_change_same_prefix_more_bytes_is_one_extend_from_captured_to() -> R {
    let mut rig = rig()?;
    transcript(&mut rig, b"0123456789");
    rig.cycle()?;
    rig.fs.replace(1, b"0123456789abcdef", 1);
    let mut spy = Spy {
        inner: std::mem::take(&mut rig.fs),
        ..Spy::default()
    };
    let got = run_spy(&mut rig, &mut spy)?;
    assert_eq!(kinds_of(&got), ["extend"]);
    let tail = spy.reads.iter().skip_while(|r| r.start < 10 && r.end <= 10);
    assert_eq!(tail.map(|r| r.start).min(), Some(10));
    assert_eq!(spy.reads.last().map(|r| r.end), Some(16));
    Ok(())
}

/// Same size, different prefix, new id: a new epoch.
#[test]
fn identity_change_different_prefix_is_an_epoch() -> R {
    let mut rig = rig()?;
    transcript(&mut rig, b"0123456789");
    rig.cycle()?;
    rig.fs.replace(1, b"X123456789abcdef", 1);
    let (_, changes) = rig.cycle()?;
    assert_eq!(kinds(&changes), ["epoch"]);
    Ok(())
}

/// A shorter file with a new id is an Epoch (Truncated), not an adopt.
#[test]
fn identity_change_shorter_file_is_an_epoch() -> R {
    let mut rig = rig()?;
    transcript(&mut rig, b"0123456789");
    rig.cycle()?;
    rig.fs.replace(1, b"01234", 1);
    let (_, changes) = rig.cycle()?;
    assert_eq!(kinds(&changes), ["epoch"]);
    Ok(())
}

// ---- Undecodable sources are withheld, never silently retried

struct Undecodable(MemFs, bool);
impl FileSystem for Undecodable {
    type Error = bool;
    fn stat(&mut self, source: u64) -> Result<Option<Stat>, bool> {
        if self.1 {
            return Err(true);
        }
        self.0.stat(source).map_err(|_| false)
    }
    fn read(&mut self, source: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, bool> {
        let _ = (source, range);
        Err(true)
    }
    fn is_undecodable(error: &bool) -> bool {
        *error
    }
}

#[test]
fn undecodable_stat_or_read_is_withheld_with_no_upload() -> R {
    for on_stat in [true, false] {
        let mut rig = rig()?;
        transcript(&mut rig, b"0123456789");
        let objects = rig.store.objects.len();
        let mut fs = Undecodable(std::mem::take(&mut rig.fs), on_stat);
        let empty = OwnerState::default();
        let env = Env {
            header: header()?,
            context: &rig.context,
            now_ns: 10_000_000_000,
            piece_len: rig.piece_len,
        };
        let got = capture(&mut fs, &rig.sources, &rig.locals, &empty, &env);
        assert_eq!(
            got.outcomes.first().map(|o| &o.1),
            Some(&SourceOutcome::Withheld(WithholdReason::Undecodable)),
            "on_stat={on_stat}"
        );
        assert!(got.commit.is_none());
        assert_eq!(rig.store.objects.len(), objects);
    }
    Ok(())
}

// ---- D54, brief 47: scanned snapshot (Claude file-history)

fn scanned() -> R<Source> {
    let mut s = source(
        1,
        "file-history/s1/0123456789abcdef@v1",
        SourceClass::Snapshot,
        Profile::Scanned,
    )?;
    s.kind = SourceKind::ClaudeFileHistory;
    Ok(s)
}

#[test]
fn a_clean_scanned_file_lands_and_a_secret_or_binary_one_puts_nothing() -> R {
    let mut rig = Rig::new(vec![scanned()?]);
    rig.fs.set(1, b"fn main() {}\n");
    let (outcomes, changes) = rig.cycle()?;
    assert_eq!(outcomes, [SourceOutcome::Planned]);
    assert_eq!(kinds(&changes), ["replace"]);

    let secret: &[u8] = concat!("key = ", "AKIA", "EXAMPLEEXAMPLE00", "\n").as_bytes();
    for (bytes, reason) in [
        (secret, WithholdReason::SecretInText),
        (&b"ab\0cd"[..], WithholdReason::Binary),
        (&[0x66_u8, 0xff][..], WithholdReason::Binary),
    ] {
        let got = withheld_for(scanned()?, bytes, |_| {})?;
        assert_eq!(got, SourceOutcome::Withheld(reason));
    }
    Ok(())
}

#[test]
fn an_oversize_scanned_file_is_withheld_before_any_read() -> R {
    let mut rig = Rig::new(vec![scanned()?]);
    let mut spy = Spy::default();
    spy.inner.set(1, &vec![b'a'; 1024 * 1024 + 1]);
    let got = run_spy(&mut rig, &mut spy)?;
    assert_eq!(
        got.outcomes.first().map(|(_, o)| o),
        Some(&SourceOutcome::Withheld(WithholdReason::SourceLimit))
    );
    assert!(got.commit.is_none());
    assert!(spy.reads.is_empty(), "no read: {:?}", spy.reads);
    Ok(())
}

fn round_spy(rig: &mut Rig, spy: &mut Spy) -> R<(Vec<SourceOutcome>, Vec<Change>)> {
    let got = run_spy(rig, spy)?;
    let changes = got.commit.as_ref().map_or(vec![], |c| c.changes.clone());
    if let Some(request) = got.commit {
        let landed = commit(&mut rig.store, rig.base.as_ref(), request, &mut |_| {}).map_err(e)?;
        rig.base = Some(landed.base());
    }
    for update in got.updates {
        update.apply(&mut rig.locals);
    }
    Ok((got.outcomes.into_iter().map(|(_, o)| o).collect(), changes))
}

#[test]
fn a_withheld_scanned_file_is_read_once_over_three_rounds() -> R {
    let mut rig = Rig::new(vec![scanned()?]);
    let mut spy = Spy::default();
    spy.inner.set(1, b"ab\0cd");
    let (first, _) = round_spy(&mut rig, &mut spy)?;
    assert_eq!(first, [SourceOutcome::Withheld(WithholdReason::Binary)]);
    let reads = spy.reads.len();
    assert!(reads > 0);
    for _ in 0..2 {
        let (again, changes) = round_spy(&mut rig, &mut spy)?;
        assert_eq!(again, [SourceOutcome::Unchanged]);
        assert!(changes.is_empty());
    }
    assert_eq!(spy.reads.len(), reads, "a cached withhold costs no read");

    // A changed fingerprint is judged again; a clean result lands and clears the record.
    spy.inner.set(1, b"now clean text\n");
    let (third, changes) = round_spy(&mut rig, &mut spy)?;
    assert_eq!(third, [SourceOutcome::Planned]);
    assert_eq!(kinds(&changes), ["replace"]);
    assert!(rig.locals.values().all(|l| l.withheld.is_none()));

    // Changed again, withheld for a different reason: judged again, new record.
    spy.inner.set(1, &[0x66, 0xff]);
    let (fourth, _) = round_spy(&mut rig, &mut spy)?;
    assert_eq!(fourth, [SourceOutcome::Withheld(WithholdReason::Binary)]);
    assert!(rig.locals.values().all(|l| l.withheld.is_some()));
    Ok(())
}

/// A landed scanned file that turns secret, misses one round and comes back with the same
/// fingerprint hits the cached withhold. Its return must still reset the absence count, or the
/// next single miss would count as the second and delete the live entry (D52, brief 47).
#[test]
fn a_cached_withhold_still_resets_the_absence_count() -> R {
    let mut rig = Rig::new(vec![scanned()?]);
    rig.fs.set(1, b"fn main() {}\n");
    let (landed, _) = rig.cycle()?;
    assert_eq!(landed, [SourceOutcome::Planned]);
    rig.fs.set(1, b"ab\0cd");
    let (withheld, _) = rig.cycle()?;
    assert_eq!(withheld, [SourceOutcome::Withheld(WithholdReason::Binary)]);
    let (files, mtimes) = (rig.fs.files.clone(), rig.fs.mtimes.clone());
    rig.fs.files.clear();
    let (_, one) = rig.cycle()?;
    assert!(one.is_empty());
    // Back with the same identity, size and mtime: the cached withhold answers.
    rig.fs.files = files;
    rig.fs.mtimes = mtimes;
    let (back, changes) = rig.cycle()?;
    assert_eq!(back, [SourceOutcome::Unchanged]);
    assert!(changes.is_empty());
    rig.fs.files.clear();
    let (_, again) = rig.cycle()?;
    assert!(
        again.is_empty(),
        "a single miss deleted: {:?}",
        kinds(&again)
    );
    Ok(())
}

#[test]
fn new_rules_judge_a_withheld_file_again() -> R {
    let mut rig = Rig::new(vec![scanned()?]);
    let mut spy = Spy::default();
    spy.inner.set(1, b"ab\0cd");
    round_spy(&mut rig, &mut spy)?;
    let reads = spy.reads.len();
    rig.context.rules = Some([9; 32]);
    let (out, _) = round_spy(&mut rig, &mut spy)?;
    // Unknown rules fail closed and read nothing; the record is not trusted for other rules.
    assert!(matches!(out.as_slice(), [SourceOutcome::Blocked(_)]));
    assert_eq!(spy.reads.len(), reads);
    Ok(())
}
