//! Round-4 F2 gap closers (09-f2-counterparts.md rows 1a, 1b, 5a, 5b, 7c, 10b, 12).
//! Pure tests with literal tables and test-written models; no filesystem access.
use memory_core::disposition::{Outcome, Streak};
fn transition(
    o: &Outcome,
    cancel: bool,
    at: std::time::Duration,
    prior: &Streak,
) -> (memory_core::domain::Disposition, Streak) {
    memory_core::disposition::transition_paced(
        o,
        cancel,
        at,
        prior,
        memory_core::disposition::Pacing::FLOOR,
    )
}

use memory_core::domain::{
    Agent, ArchiveId, Birth, Disposition, ErrorClass, FileId, Fingerprint, LocalError, MachineId,
    Observed, Offset, Owner, Plan, RulesDigest, SessionId, SessionKey, Sha256, SnapshotFingerprint,
    SourceClass, SourceId, SourceKind, WalFingerprint,
};
use memory_core::locator::{Located, RootLayout, RootNamespace, RootRelativePath, Roots, locate};
use memory_core::manifest::{
    Change, CheckpointSource, EpochRecord, EpochStamp, EpochWriter, Header, Manifest, OwnerState,
    Piece, Position, SourceWrite, Timestamp, Writer, build, fold,
};
use memory_core::planner::{AuditEvidence, Observation, PlanInput, Planning, Strategy, plan};
use memory_core::privacy::{
    self, Admission, Authorization, AuthorizedBody, PolicyContext, Profile, Reason,
    ResolvedSourceFacts, UnknownReason, sanitize_launch,
};
use sha2::{Digest, Sha256 as Hasher};
use std::collections::{BTreeMap, BTreeSet};

type R = Result<(), Box<dyn std::error::Error>>;

// ------------------------------------------------------------ privacy helpers

fn context() -> PolicyContext {
    PolicyContext {
        valid: true,
        revision: "f2-gaps".into(),
        rules: Some(privacy::rules_digest()),
        collector_paths: vec!["/collector/state".into()],
        collector_identities: vec![99],
    }
}
fn facts(paths: &[&str], profile: Profile) -> ResolvedSourceFacts {
    ResolvedSourceFacts {
        identities: paths.iter().map(|s| (*s).to_owned()).collect(),
        file_identity: Some(1),
        ancestor_identities: vec![2],
        path_resolved: true,
        root_resolved: true,
        owner: Some("fixture-owner".into()),
        profile,
    }
}
fn body(
    paths: &[&str],
    profile: Profile,
    input: &[u8],
) -> Result<AuthorizedBody, Box<dyn std::error::Error>> {
    let Admission::Allow(source) = privacy::admit(&context(), facts(paths, profile)) else {
        return Err("admission failed".into());
    };
    let redacted = privacy::transform(&source, input)?;
    match privacy::authorize(&source, redacted, &context()) {
        Authorization::Allow(value) => Ok(value),
        _ => Err("authorization failed".into()),
    }
}
/// `Some(reason)` when transform of an admitted source fails.
fn transform_err(
    paths: &[&str],
    profile: Profile,
    input: &[u8],
) -> Result<Option<Reason>, &'static str> {
    let Admission::Allow(source) = privacy::admit(&context(), facts(paths, profile)) else {
        return Err("admission failed");
    };
    Ok(privacy::transform(&source, input).err())
}

const JSON_ENV: &str = r#"{"env":{"KEY":"CANARY"},"copy":"CANARY"}"#;
const JSON_MCP: &str = r#"{"mcpServers":{"s":{"env":{"KEY":"CANARY"}}},"copy":"CANARY"}"#;
const TOML_CODEX: &str = "copy='CANARY'\n[mcp_servers.s]\nbearer_token='CANARY'\n";
/// The 11 protected basenames with a canary-bearing input and whether the syntax is TOML.
const ELEVEN: [(&str, &str, bool); 11] = [
    (
        "claude.json",
        r#"{"primaryApiKey":"CANARY","copy":"CANARY"}"#,
        false,
    ),
    (
        ".claude.json",
        r#"{"primaryApiKey":"CANARY","copy":"CANARY"}"#,
        false,
    ),
    ("settings.json", JSON_ENV, false),
    ("settings.local.json", JSON_ENV, false),
    ("managed-settings.json", JSON_ENV, false),
    (".mcp.json", JSON_MCP, false),
    ("mcp.json", JSON_MCP, false),
    ("managed-mcp.json", JSON_MCP, false),
    (
        "models.json",
        r#"{"providers":{"s":{"apiKey":"CANARY"}},"copy":"CANARY"}"#,
        false,
    ),
    ("config.toml", TOML_CODEX, true),
    ("managed_config.toml", TOML_CODEX, true),
];
/// Backup spellings per the suffix normalization (names.rs / Go policy.go:75-123).
const SUFFIXES: [&str; 19] = [
    "",
    ".bak",
    ".backup",
    ".orig",
    ".old",
    "~",
    ".~1~",
    "~2~",
    "-20260102",
    "-20260102-030405",
    ".before-upgrade",
    ".gz",
    ".zst",
    ".tar",
    ".agent-sessions-backup-2026-01-02",
    ".bak.old~",
    ".orig.gz",
    ".bak.bak",
    "-20260102.bak",
];

#[test]
fn row1a_backup_aliased_spellings_of_all_eleven_names_select_structural_rules() -> R {
    let mut denied = 0_u32;
    let mut scrubbed = 0_u32;
    for agent in ["claude", "codex", "pi"] {
        for (name, input, toml) in ELEVEN {
            for suffix in SUFFIXES {
                for spelled in [
                    format!("{name}{suffix}"),
                    format!("{name}{suffix}").to_uppercase(),
                ] {
                    let identity = format!("/home/u/.{agent}/{spelled}");
                    // Independent expectation: any `.claude.json` spelling other than the exact
                    // primary file is a credential backup; every other alias is scrubbed.
                    let credential = spelled != ".claude.json" && name == ".claude.json";
                    for profile in [Profile::Config, Profile::OtherKnown] {
                        let paths = ["/safe/alias.md", identity.as_str(), "/final/unknown"];
                        let admission = privacy::admit(&context(), facts(&paths, profile));
                        if credential {
                            assert!(
                                matches!(admission, Admission::Deny(Reason::Credential)),
                                "{identity} must be denied as credential"
                            );
                            denied += 1;
                            continue;
                        }
                        assert!(matches!(admission, Admission::Allow(_)), "{identity} admit");
                        let out = body(&paths, profile, input.as_bytes())?;
                        assert!(
                            !String::from_utf8_lossy(out.bytes()).contains("CANARY"),
                            "{identity} leaked"
                        );
                        assert_ne!(out.bytes(), input.as_bytes(), "{identity} unchanged");
                        let malformed: &[u8] = if toml { b"[" } else { b"{" };
                        assert_eq!(
                            transform_err(&paths, profile, malformed)?,
                            Some(Reason::MalformedProtected),
                            "{identity} malformed must fail closed"
                        );
                        scrubbed += 1;
                    }
                }
            }
        }
    }
    // 3 agents x 19 suffixes x 2 case spellings x 2 profiles x (10 scrubbed + 1 credential name),
    // except the exact primary spelling of `.claude.json` (1 per agent per profile) is scrubbed.
    assert_eq!(denied, 3 * (19 * 2 - 1) * 2);
    assert_eq!(scrubbed, 3 * 2 * (10 * 19 * 2 + 1));
    Ok(())
}

#[test]
fn row1a_non_protected_alias_is_untouched_control() -> R {
    let input = br#"{"copy":"CANARY"}"#;
    for name in [
        "notes.json.bak",
        "claude.jsonx.bak",
        "settings.json.d",
        "xmodels.json~",
    ] {
        let identity = format!("/home/u/.claude/{name}");
        let out = body(&[identity.as_str()], Profile::OtherKnown, input)?;
        assert_eq!(out.bytes(), input.as_slice(), "{name}");
    }
    Ok(())
}

// ------------------------------------------------------------------ row 1b

const ALLOWED_ENV: [&str; 9] = [
    "ANTHROPIC_MODEL",
    "ANTHROPIC_SMALL_FAST_MODEL",
    "ANTHROPIC_DEFAULT_OPUS_MODEL",
    "ANTHROPIC_DEFAULT_SONNET_MODEL",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL",
    "CLAUDE_CONFIG_DIR",
    "MAX_THINKING_TOKENS",
    "MAX_MCP_OUTPUT_TOKENS",
    "DISABLE_PROMPT_CACHING",
];
fn env_of(entries: &[String]) -> BTreeMap<String, String> {
    sanitize_launch(&[], entries).env().clone()
}

#[test]
fn row1b_env_publishes_every_allowlisted_name_in_any_order() {
    // The allowlist of nine names is the only bound (amendment A13.3): no name is dropped,
    // whatever order the environment lists them in.
    assert_eq!(ALLOWED_ENV.len(), 9);
    for rot in 0..9 {
        let mut names: Vec<&str> = ALLOWED_ENV.to_vec();
        names.rotate_left(rot);
        let entries: Vec<String> = names.iter().map(|k| format!("{k}=1")).collect();
        let env = env_of(&entries);
        let expect: BTreeSet<&str> = ALLOWED_ENV.iter().copied().collect();
        let got: BTreeSet<&str> = env.keys().map(String::as_str).collect();
        assert_eq!(got, expect, "rotation {rot}");
    }
    // Rejected entries, malformed entries and unknown names are never published.
    let mut entries: Vec<String> = vec![
        "PATH=/bin".into(),
        "NOEQUALS".into(),
        "ANTHROPIC_MODEL=-bad".into(),
        "ANTHROPIC_MODEL_X=a".into(),
        "anthropic_model=a".into(),
        "ANTHROPIC_API_KEY=a".into(),
    ];
    entries.extend(ALLOWED_ENV.iter().skip(1).map(|k| format!("{k}=1")));
    let env = env_of(&entries);
    assert_eq!(env.len(), 8);
    assert!(!env.contains_key("ANTHROPIC_MODEL"));
    // A repeated key does not add an entry; the later value wins.
    let env = env_of(&["ANTHROPIC_MODEL=a".into(), "ANTHROPIC_MODEL=b".into()]);
    assert_eq!(env.len(), 1);
    assert_eq!(env.get("ANTHROPIC_MODEL").map(String::as_str), Some("b"));
}

#[test]
fn row1b_env_value_grammar_accept_and_reject_vectors() {
    let long_ok = "a".repeat(256);
    let long_bad = "a".repeat(257);
    let accept = [
        "a",
        "0",
        "8192",
        "claude-opus-4-1",
        "gpt-5.1",
        "org/model:v1@2+x",
        "1.2.3",
        "A_b",
        "model_v1",
        "Users/x/claude",
        long_ok.as_str(),
    ];
    let reject = [
        "",
        "-a",
        ".a",
        "_a",
        "/a",
        "a b",
        "a\n",
        "a=b",
        "a;b",
        "é",
        "a\u{0}",
        "a$b",
        "Bearer abc",
        "my-token",
        "api_key",
        "x-auth",
        "mysecret",
        "Password1",
        "passphrase",
        "credential-x",
        "cookie",
        "apikey",
        long_bad.as_str(),
    ];
    for v in accept {
        let env = env_of(&[format!("ANTHROPIC_MODEL={v}")]);
        assert_eq!(
            env.get("ANTHROPIC_MODEL").map(String::as_str),
            Some(v),
            "accept {v:?}"
        );
    }
    for v in reject {
        let env = env_of(&[format!("ANTHROPIC_MODEL={v}")]);
        assert!(env.is_empty(), "reject {v:?}");
    }
}

#[test]
fn row1b_argv_bound_is_thirty_two() {
    let argv: Vec<String> = (0..40).map(|i| format!("x{i}")).collect();
    assert_eq!(sanitize_launch(&argv, &[]).argv().len(), 32);
    let argv: Vec<String> = (0..32).map(|i| format!("x{i}")).collect();
    assert_eq!(sanitize_launch(&argv, &[]).argv().len(), 32);
    let argv: Vec<String> = (0..31).map(|i| format!("x{i}")).collect();
    assert_eq!(sanitize_launch(&argv, &[]).argv().len(), 31);
}

// ------------------------------------------------------------------ row 5a

fn fp() -> Fingerprint {
    Fingerprint {
        id: FileId {
            dev: 1,
            ino: 2,
            birth: Birth::Unavailable,
        },
        size: 100,
        mtime_ns: 1,
    }
}
/// One variant per physical field.
fn variants() -> Vec<Fingerprint> {
    let b = fp();
    vec![
        Fingerprint {
            id: FileId {
                dev: 9,
                ..b.id.clone()
            },
            ..b.clone()
        },
        Fingerprint {
            id: FileId {
                ino: 9,
                ..b.id.clone()
            },
            ..b.clone()
        },
        Fingerprint {
            id: FileId {
                birth: Birth::Known(5),
                ..b.id.clone()
            },
            ..b.clone()
        },
        Fingerprint {
            size: 101,
            ..b.clone()
        },
        Fingerprint { mtime_ns: 2, ..b },
    ]
}
fn snapshot_plan(previous: SnapshotFingerprint, current: SnapshotFingerprint) -> Option<Plan> {
    let observed = Observed {
        id: FileId {
            dev: 1,
            ino: 2,
            birth: Birth::Unavailable,
        },
        size: 100,
        mtime_ns: 1,
        observed_at_ns: 9_000_000_000,
    };
    let input = PlanInput {
        withheld: None,
        observation: Observation::Present(observed),
        strategy: Strategy::Snapshot {
            previous: Some(previous),
            current,
            previous_rules: None,
        },
        rules: None,
        audit: AuditEvidence::NotDue,
    };
    match plan(&input) {
        Planning::Ready { plan, .. } => Some(plan),
        _ => None,
    }
}
fn sqlite(db: Fingerprint, wal: WalFingerprint) -> SnapshotFingerprint {
    SnapshotFingerprint::Sqlite { db, wal }
}

#[test]
fn row5a_db_only_wal_only_and_unchanged_fingerprints() {
    let present = WalFingerprint::Present(fp());
    for wal in [WalFingerprint::Absent, present.clone()] {
        assert_eq!(
            snapshot_plan(sqlite(fp(), wal.clone()), sqlite(fp(), wal.clone())),
            Some(Plan::Unchanged)
        );
        for changed in variants() {
            // db-only change, WAL unchanged.
            assert_eq!(
                snapshot_plan(sqlite(fp(), wal.clone()), sqlite(changed, wal.clone())),
                Some(Plan::Snapshot)
            );
        }
    }
    for changed in variants() {
        // WAL-only change, db unchanged.
        assert_eq!(
            snapshot_plan(
                sqlite(fp(), present.clone()),
                sqlite(fp(), WalFingerprint::Present(changed.clone()))
            ),
            Some(Plan::Snapshot)
        );
        // Both changed.
        assert_eq!(
            snapshot_plan(
                sqlite(fp(), present.clone()),
                sqlite(changed.clone(), WalFingerprint::Present(changed))
            ),
            Some(Plan::Snapshot)
        );
    }
    assert_eq!(
        snapshot_plan(
            sqlite(fp(), present.clone()),
            sqlite(fp(), WalFingerprint::Absent)
        ),
        Some(Plan::Snapshot)
    );
    assert_eq!(
        snapshot_plan(sqlite(fp(), WalFingerprint::Absent), sqlite(fp(), present)),
        Some(Plan::Snapshot)
    );
    // A plain fingerprint never equals a SQLite one.
    assert_eq!(
        snapshot_plan(
            SnapshotFingerprint::Plain(fp()),
            sqlite(fp(), WalFingerprint::Absent)
        ),
        Some(Plan::Snapshot)
    );
}

#[test]
fn row5a_wal_identity_is_covered_by_admission_when_supplied_as_facts() {
    let kind = |f: ResolvedSourceFacts| privacy::admit(&context(), f);
    // Control: db plus plain WAL spelling.
    let ok = facts(
        &["/p/state.sqlite", "/p/state.sqlite-wal"],
        Profile::OtherKnown,
    );
    assert!(matches!(kind(ok), Admission::Allow(_)));
    // WAL aliased to a credential spelling (including a backup alias).
    for alias in [
        "/home/u/.codex/auth.json",
        "/home/u/.codex/auth.json.bak",
        "/home/u/.ssh/id_rsa",
    ] {
        let f = facts(&["/p/state.sqlite-wal", alias], Profile::OtherKnown);
        assert!(
            matches!(kind(f), Admission::Deny(Reason::Credential)),
            "{alias}"
        );
    }
    // WAL under or identical to collector state, by spelling or by final/ancestor identity.
    let f = facts(&["/collector/state/x-wal"], Profile::OtherKnown);
    assert!(matches!(kind(f), Admission::Deny(Reason::CollectorState)));
    let mut f = facts(&["/p/state.sqlite-wal"], Profile::OtherKnown);
    f.file_identity = Some(99);
    assert!(matches!(kind(f), Admission::Deny(Reason::CollectorState)));
    let mut f = facts(&["/p/state.sqlite-wal"], Profile::OtherKnown);
    f.ancestor_identities = vec![99];
    assert!(matches!(kind(f), Admission::Deny(Reason::CollectorState)));
    // Unresolved WAL path is unknown, never an allow.
    let mut f = facts(&["/p/state.sqlite-wal"], Profile::OtherKnown);
    f.path_resolved = false;
    assert!(matches!(
        kind(f),
        Admission::Unknown(UnknownReason::PathUnresolvable)
    ));
}

// ------------------------------------------------------------------ row 5b

#[test]
fn row5b_changed_and_failed_are_closed_local_retries_never_done() -> R {
    for (err, wire) in [
        (LocalError::BackupFailed, "\"backup_failed\""),
        (LocalError::SourceChanged, "\"source_changed\""),
    ] {
        assert_eq!(serde_json::to_string(&err)?, wire);
        assert_eq!(serde_json::from_str::<LocalError>(wire)?, err);
        for (retries, secs) in [
            (0_u32, 1_u64),
            (1, 2),
            (3, 8),
            (9, 512),
            (10, 900),
            (25, 900),
        ] {
            let prior = Streak {
                retries,
                ..Streak::default()
            };
            let outcome = Outcome::Failed(ErrorClass::Local(err.clone()));
            let (disposition, next) =
                transition(&outcome, false, std::time::Duration::from_secs(5), &prior);
            assert_eq!(
                disposition,
                Disposition::Retry {
                    after: std::time::Duration::from_secs(secs),
                    class: ErrorClass::Local(err.clone()),
                }
            );
            assert_eq!(
                next,
                Streak {
                    retries: retries + 1,
                    ..Streak::default()
                }
            );
            // Shutdown swallows the retry into Cancelled and leaves the streak untouched.
            let (cancelled, same) =
                transition(&outcome, true, std::time::Duration::from_secs(5), &prior);
            assert_eq!(cancelled, Disposition::Cancelled);
            assert_eq!(same, prior);
        }
    }
    assert!(serde_json::from_str::<LocalError>("\"other\"").is_err());
    Ok(())
}

// ------------------------------------------------------------------ row 7c

fn codex_located(path: &str) -> Result<Located, Box<dyn std::error::Error>> {
    let roots = Roots::new(
        ArchiveId::try_from("a".to_owned())?,
        MachineId::try_from("m".to_owned())?,
        Agent::Codex,
        RootNamespace::new("r1")?,
        RootLayout::AgentDir,
    );
    Ok(locate(&RootRelativePath::parse(path)?, &roots))
}

#[test]
fn row7c_codex_sessions_archive_and_zst_share_one_owner_session_and_id() -> R {
    let uuid = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
    let stem = format!("rollout-2026-01-02T03-04-05-{uuid}");
    let name = format!("{stem}.jsonl");
    let paths = [
        (
            format!("sessions/2026/01/02/{name}"),
            SourceKind::CodexRollout,
        ),
        (
            format!("archived_sessions/{name}"),
            SourceKind::CodexRollout,
        ),
        (
            format!("sessions/2026/01/02/{name}.zst"),
            SourceKind::CodexCompressed,
        ),
        (
            format!("archived_sessions/{name}.zst"),
            SourceKind::CodexCompressed,
        ),
    ];
    let mut seen: Vec<(Owner, SourceId)> = Vec::new();
    for (path, want_kind) in &paths {
        let Located::Source {
            owner, kind, id, ..
        } = codex_located(path)?
        else {
            return Err(format!("{path} not located").into());
        };
        assert_eq!(&kind, want_kind, "{path}");
        let Owner::Session(SessionKey { session, agent, .. }) = &owner else {
            return Err("owner must be session-local".into());
        };
        assert_eq!(session.as_str(), format!("r1:{stem}"));
        assert_eq!(agent, &Agent::Codex);
        assert_eq!(id.as_str(), format!("r1:rollouts:{name}"));
        seen.push((owner, id));
    }
    let first = seen.first().ok_or("empty")?;
    assert!(
        seen.iter().all(|s| s == first),
        "one owner and id for every spelling"
    );
    // A different rollout UUID is a different owner (control).
    let other = "rollout-2026-01-02T03-04-05-bbbbbbbb-cccc-dddd-eeee-ffffffffffff.jsonl";
    let Located::Source { owner, .. } = codex_located(&format!("sessions/2026/01/02/{other}"))?
    else {
        return Err("other not located".into());
    };
    assert_ne!(owner, first.0);
    Ok(())
}

// ----------------------------------------------------------------- row 10b

#[test]
fn row10b_transcript_bytes_with_invalid_utf8_and_lone_surrogates_are_byte_exact() -> R {
    let inputs: [&[u8]; 7] = [
        b"\xff\x80\n",
        b"{\"t\":\"\\ud800\"}\n",
        b"{\"t\":\"\\udfff\\ud800x\"}\n{\"t\":\"\xc3\"}\n",
        b"\xe2\x82",
        b"\xed\xa0\x80 raw encoded surrogate\n",
        b"a\x00b\r\n\xf0\x9f\n",
        b"",
    ];
    for profile in [Profile::Transcript, Profile::OtherKnown] {
        for input in inputs {
            let out = body(&["/home/u/.claude/projects/-p/abc.jsonl"], profile, input)?;
            assert_eq!(out.bytes(), input);
            assert_eq!(out.digest(), <[u8; 32]>::from(Hasher::digest(input)));
        }
    }
    Ok(())
}

#[test]
fn row10b_protected_config_with_lone_surrogates_or_invalid_utf8_is_malformed() -> R {
    for (name, _, toml) in ELEVEN {
        let identity = format!("/home/u/.claude/{name}");
        let bad: [&[u8]; 2] = if toml {
            [b"copy='\xff'\n", b"copy=\"\\ud800\"\n"]
        } else {
            [b"{\"copy\":\"\xff\"}", b"{\"copy\":\"\\ud800\"}"]
        };
        for input in bad {
            assert_eq!(
                transform_err(&[identity.as_str()], Profile::Config, input)?,
                Some(Reason::MalformedProtected),
                "{name}"
            );
        }
    }
    Ok(())
}

// ------------------------------------------------------------------ row 12

fn h(n: u64) -> Sha256 {
    Sha256::from_bytes(Hasher::digest(n.to_le_bytes()).into())
}
fn stamp(n: u32, rules: Option<u64>) -> EpochStamp {
    EpochStamp {
        n,
        writer: EpochWriter {
            version: "0.1.0".to_owned(),
            commit: "abc123".to_owned(),
        },
        rules: rules.map(|r| RulesDigest::new(h(r))),
    }
}
fn piece(c: u64, off: u64, len: u64) -> Piece {
    raw_piece(Offset::new(off), len, h(c), 1 + c % 50, h(c + 1_000_000))
}
fn write(
    name: &str,
    append: bool,
    epoch: EpochStamp,
    size: u64,
    whole: Sha256,
    pieces: Vec<Piece>,
) -> Result<SourceWrite, Box<dyn std::error::Error>> {
    Ok(SourceWrite {
        id: SourceId::try_from(name.to_owned())?,
        kind: if append {
            SourceKind::ClaudeTranscript
        } else {
            SourceKind::ClaudeTodos
        },
        path: format!("p/{name}"),
        class: if append {
            SourceClass::Append
        } else {
            SourceClass::Snapshot
        },
        epoch,
        size,
        sha256: whole,
        pieces,
    })
}
fn owner() -> Result<Owner, Box<dyn std::error::Error>> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned())?,
        machine: MachineId::try_from("m".to_owned())?,
        agent: Agent::Claude,
        session: SessionId::try_from("s1".to_owned())?,
    }))
}
fn header() -> Result<Header, Box<dyn std::error::Error>> {
    Ok(Header {
        owner: owner()?,
        captured_at: Timestamp::try_from("2026-11-07T10:30:00Z".to_owned())?,
        writer: Writer {
            name: "memory".to_owned(),
            version: "0.1.0".to_owned(),
            commit: "abc123".to_owned(),
        },
        launch: vec![],
    })
}

#[derive(Debug, Clone, PartialEq)]
struct Epoch {
    n: u32,
    size: u64,
    whole: Sha256,
    pieces: Vec<(u64, u64, Sha256)>,
}
fn view(records: &[EpochRecord]) -> Vec<Epoch> {
    records
        .iter()
        .map(|e| Epoch {
            n: e.epoch.n,
            size: e.size,
            whole: e.sha256,
            pieces: e
                .pieces
                .iter()
                .map(|p| (p.offset().get(), p.len(), p.sha256()))
                .collect(),
        })
        .collect()
}
fn of<'a>(sources: &'a [CheckpointSource], id: &str) -> Result<&'a CheckpointSource, String> {
    sources
        .iter()
        .find(|s| s.id.as_str() == id)
        .ok_or_else(|| format!("missing {id}"))
}

/// 300 manifests; append source `t` starts a new epoch at every seq except `seq % 3 == 2`
/// (which extends); snapshot source `c` is replaced at every seq.
#[test]
fn row12_three_hundred_epoch_rewrite_history_retains_appends_and_latest_snapshot() -> R {
    const LEN: u64 = 300;
    let mut model_t: Vec<Epoch> = Vec::new();
    let mut chain: Vec<Vec<u8>> = Vec::new();
    let mut prev: Option<(Position, OwnerState)> = None;
    let mut checkpoint_bytes: Vec<(u64, usize)> = Vec::new();
    let mut delta_bytes: Vec<(u64, usize)> = Vec::new();
    for seq in 0..LEN {
        let ctr = seq * 10 + 1;
        let (a, b) = (5 + seq % 7, 3 + seq % 5);
        let mut changes = Vec::new();
        if seq % 3 == 2 {
            let last = model_t.last_mut().ok_or("model")?;
            let off = last.size;
            let whole = h(ctr + 500_000);
            last.size = off + a;
            last.whole = whole;
            last.pieces.push((off, a, h(ctr)));
            changes.push(Change::Extend(write(
                "t",
                true,
                stamp(last.n, None),
                last.size,
                whole,
                vec![piece(ctr, off, a)],
            )?));
        } else {
            let n = u32::try_from(model_t.len() + 1)?;
            let whole = h(ctr + 500_000);
            model_t.push(Epoch {
                n,
                size: a + b,
                whole,
                pieces: vec![(0, a, h(ctr)), (a, b, h(ctr + 1))],
            });
            changes.push(Change::Epoch(write(
                "t",
                true,
                stamp(n, None),
                a + b,
                whole,
                vec![piece(ctr, 0, a), piece(ctr + 1, a, b)],
            )?));
        }
        let cn = u32::try_from(seq + 1)?;
        let cwhole = h(ctr + 3);
        let model_c = Epoch {
            n: cn,
            size: 4,
            whole: cwhole,
            pieces: vec![(0, 4, h(ctr + 2))],
        };
        changes.push(Change::Replace(write(
            "c",
            false,
            stamp(cn, Some(9)),
            4,
            cwhole,
            vec![piece(ctr + 2, 0, 4)],
        )?));

        let built = build(
            prev.as_ref().map(|(p, s)| (p, s)),
            header()?,
            changes.clone(),
        )?;
        let sources = built.state.to_checkpoint_sources();
        assert_eq!(
            view(&of(&sources, "t")?.epochs),
            model_t,
            "state t at seq {seq}"
        );
        assert_eq!(view(&of(&sources, "c")?.epochs), vec![model_c.clone()]);
        match &built.manifest {
            Manifest::Checkpoint(c) => {
                assert_eq!(seq % 64, 0);
                assert_eq!(
                    view(&of(c.sources(), "t")?.epochs),
                    model_t,
                    "checkpoint t at seq {seq}"
                );
                assert_eq!(of(c.sources(), "c")?.epochs.len(), 1);
                assert_eq!(view(&of(c.sources(), "c")?.epochs), vec![model_c.clone()]);
                checkpoint_bytes.push((seq, built.bytes.len()));
                if seq > 0 {
                    let s = usize::try_from(seq)?;
                    let slice = chain.get(s - 64..s).ok_or("range")?;
                    let refs: Vec<&[u8]> = slice.iter().map(Vec::as_slice).collect();
                    let mut folded = fold(&refs)?;
                    assert_eq!(folded.position.seq, seq - 1);
                    folded.state.apply_changes(&changes)?;
                    assert_eq!(folded.state, built.state);
                    let fs = folded.state.to_checkpoint_sources();
                    assert_eq!(view(&of(&fs, "t")?.epochs), model_t);
                }
            }
            Manifest::Delta(_) => delta_bytes.push((seq, built.bytes.len())),
        }
        chain.push(built.bytes.clone());
        prev = Some((built.position, built.state));
    }
    // Literal counts: new epochs at every seq with seq % 3 != 2; 200 of 300, 172 of 0..=256.
    assert_eq!(model_t.len(), 200);
    assert_eq!(
        checkpoint_bytes.iter().map(|(s, _)| *s).collect::<Vec<_>>(),
        [0, 64, 128, 192, 256]
    );
    // Reader rule at the end: checkpoint 256 plus 43 deltas equals the model.
    let slice = chain.get(256..).ok_or("range")?;
    let refs: Vec<&[u8]> = slice.iter().map(Vec::as_slice).collect();
    let folded = fold(&refs)?;
    assert_eq!(folded.position.seq, LEN - 1);
    let fs = folded.state.to_checkpoint_sources();
    assert_eq!(view(&of(&fs, "t")?.epochs), model_t);
    assert_eq!(of(&fs, "c")?.epochs.len(), 1);
    // Byte accounting: checkpoints grow with retained append epochs; deltas do not.
    let sizes: Vec<usize> = checkpoint_bytes.iter().map(|(_, b)| *b).collect();
    assert!(
        sizes.windows(2).all(|w| matches!(w, [x, y] if y > x)),
        "{sizes:?}"
    );
    let cp = |s: u64| {
        checkpoint_bytes
            .iter()
            .find(|(q, _)| *q == s)
            .map(|(_, b)| *b)
    };
    assert!(cp(256) > cp(64).map(|b| b * 2), "{sizes:?}");
    let max = |lo: u64, hi: u64| {
        delta_bytes
            .iter()
            .filter(|(s, _)| (lo..hi).contains(s))
            .map(|(_, b)| *b)
            .max()
    };
    assert!(
        max(200, 300) <= max(1, 60).map(|b| b + 64),
        "delta size must not grow with history"
    );
    Ok(())
}

#[expect(
    clippy::panic,
    reason = "test fixture: a literal piece that fails to parse is a broken test, not a runtime path"
)]
fn raw_piece(
    offset: Offset,
    len: u64,
    sha: Sha256,
    encoded_len: u64,
    encoded_sha: Sha256,
) -> Piece {
    let h = String::from(sha);
    let v = serde_json::json!({"offset": offset.get(), "len": len, "sha256": h,
        "object": format!("objects/{h}.zst"), "encoding": "zstd-3",
        "encoded_len": encoded_len, "encoded_sha256": String::from(encoded_sha)});
    match serde_json::from_value(v) {
        Ok(p) => p,
        Err(_) => panic!("piece"),
    }
}
