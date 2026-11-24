//! Synthetic Go-parity vectors; no filesystem/source-state access.
use memory_core::privacy::{
    self, Admission, Authorization, AuthorizedBody, PolicyContext, Profile, Reason,
    ResolvedSourceFacts, UnknownReason,
};

fn context() -> PolicyContext {
    PolicyContext {
        valid: true,
        revision: "fixture-v1".into(),
        rules: Some(privacy::rules_digest()),
        collector_paths: vec![
            "/collector/config.toml".into(),
            "/collector/state".into(),
            "/collector/token".into(),
        ],
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
fn admitted(paths: &[&str], profile: Profile) -> Result<privacy::AdmittedSource, &'static str> {
    match privacy::admit(&context(), facts(paths, profile)) {
        Admission::Allow(value) => Ok(value),
        _ => Err("fixture admission failed"),
    }
}
fn body(
    paths: &[&str],
    profile: Profile,
    input: &[u8],
) -> Result<AuthorizedBody, Box<dyn std::error::Error>> {
    let source = admitted(paths, profile)?;
    let redacted = privacy::transform(&source, input)?;
    match privacy::authorize(&source, redacted, &context()) {
        Authorization::Allow(value) => Ok(value),
        _ => Err("fixture authorization failed".into()),
    }
}

#[test]
fn denied_identities_backups_collector_aliases_and_unknowns() {
    for name in [
        "auth.json",
        "oauth.json",
        "mcp-auth.json",
        ".credentials.json",
        "config.env",
        ".env",
        ".env.local",
        "x.key",
        "x.pem",
        ".git-credentials",
        ".netrc",
        ".npmrc",
        ".pypirc",
        "id_rsa",
        "id_dsa",
        "id_ecdsa",
        "id_ed25519",
        "id_ecdsa_sk",
        "id_ed25519_sk",
        ".claude.json",
    ] {
        for suffix in [
            ".bak.old~",
            "-20260412-235959.gz",
            ".before-update.bak",
            ".agent-sessions-backup-20261014-101010",
            ".~123~.tar.gz",
        ] {
            let path = format!("/safe/{}{}", name.to_uppercase(), suffix);
            assert!(matches!(
                privacy::admit(
                    &context(),
                    facts(&["alias.md", &path, "final.txt"], Profile::Scanned)
                ),
                Admission::Deny(Reason::Credential)
            ));
        }
    }
    for dir in [
        ".ssh",
        ".gnupg",
        ".aws",
        ".azure",
        ".kube",
        ".config/gcloud",
    ] {
        assert!(privacy::is_credential(&format!("/home/u/{dir}/safe.md")));
    }
    for path in [
        "auth.json.example",
        "notes.key.txt",
        "token-count.json",
        "/.ssh-not/file",
        "models.json.bak2",
    ] {
        assert!(!privacy::is_credential(path));
    }
    for path in [
        "/collector/config.toml.bak",
        "/collector/state/x",
        "/collector/token",
    ] {
        assert!(matches!(
            privacy::admit(&context(), facts(&[path], Profile::Scanned)),
            Admission::Deny(Reason::CollectorState)
        ));
    }
    let mut aliases = facts(&["renamed.md"], Profile::Scanned);
    aliases.ancestor_identities.push(99);
    assert!(matches!(
        privacy::admit(&context(), aliases),
        Admission::Deny(Reason::CollectorState)
    ));
    assert!(matches!(
        privacy::admit(&context(), facts(&["x"], Profile::Unknown)),
        Admission::Deny(Reason::UnknownKind)
    ));
    let mut unresolved = facts(&["x"], Profile::Transcript);
    unresolved.path_resolved = false;
    assert!(matches!(
        privacy::admit(&context(), unresolved),
        Admission::Unknown(UnknownReason::PathUnresolvable)
    ));
    let mut ctx = context();
    ctx.rules = None;
    assert!(matches!(
        privacy::admit(&ctx, facts(&["run.json"], Profile::Scanned)),
        Admission::Unknown(UnknownReason::RulesUnavailable)
    ));
    assert!(matches!(
        privacy::admit(&ctx, facts(&["x"], Profile::Transcript)),
        Admission::Allow(_)
    ));
}

#[test]
fn capability_binding_rejects_cross_source_cross_profile_and_changed_policy()
-> Result<(), Box<dyn std::error::Error>> {
    for target in [
        admitted(&["run.json"], Profile::Scanned)?,
        admitted(&["transcript.jsonl"], Profile::Scanned)?,
    ] {
        let original = admitted(&["transcript.jsonl"], Profile::Transcript)?;
        let raw = privacy::transform(&original, b"unsafe config-shaped transcript")?;
        assert!(matches!(
            privacy::authorize(&target, raw, &context()),
            Authorization::Deny(Reason::ContextChanged)
        ));
    }
    let source = admitted(&["x"], Profile::Transcript)?;
    let redacted = privacy::transform(&source, b"raw")?;
    let mut ctx = context();
    ctx.revision = "changed".into();
    assert!(matches!(
        privacy::authorize(&source, redacted, &ctx),
        Authorization::Deny(Reason::ContextChanged)
    ));
    let raw = b"not json\xff\nAPI_KEY=CANARY\n";
    assert_eq!(
        body(&["transcript.jsonl"], Profile::Transcript, raw)?.bytes(),
        raw
    );
    Ok(())
}

#[test]
fn native_launch_drops_prompt_secret_flags_and_nonexact_env() {
    let args: Vec<String> = [
        "/bin/claude",
        "--model",
        "sonnet-4",
        "--prompt",
        "CANARY_PROMPT",
        "positional",
        "--system-prompt=CANARY",
        "--token",
        "CANARY",
        "--cwd",
        "/safe/project",
        "--resume",
    ]
    .into_iter()
    .map(str::to_owned)
    .collect();
    let env: Vec<String> = [
        "ANTHROPIC_MODEL=sonnet-4",
        "CLAUDE_CODE_PROMPT=CANARY",
        "PI_API_KEY=CANARY",
        "UNKNOWN=CANARY",
        "CLAUDE_CONFIG_DIR=/safe/root",
    ]
    .into_iter()
    .map(str::to_owned)
    .collect();
    let launch = privacy::sanitize_launch(&args, &env);
    assert!(!format!("{launch:?}").contains("CANARY"));
    assert!(launch.argv().iter().any(|s| s == "sonnet-4"));
    assert_eq!(
        launch.env().get("ANTHROPIC_MODEL").map(String::as_str),
        Some("sonnet-4")
    );
    let args: Vec<String> = std::iter::once("pi".into())
        .chain(std::iter::repeat_n("--model=ok".into(), 100))
        .collect();
    assert!(privacy::sanitize_launch(&args, &[]).argv().len() <= 32);
}

#[test]
fn rule_digest_golden_requires_explicit_semantic_review() {
    assert_eq!(
        privacy::rules_digest(),
        [
            // D54, brief 47: the scanner table and cap joined the digest (was 165, 111, 102, ...).
            // Brief 50: scan/2 on the vendored gitleaks v8.30.1 rules moved the digest (was 223, 26, 99, ...).
            // Brief 50 (parent): own provider patterns start at a word boundary (was 136, 128, 227, ...).
            // Brief 57: the config rule table left the digest (was 0, 202, 21, ...).
            216, 242, 14, 138, 15, 84, 228, 78, 126, 175, 3, 19, 219, 140, 127, 215, 1, 18, 135, 23,
            17, 58, 188, 238, 67, 237, 170, 244, 80, 77, 54, 146
        ]
    );
}

proptest::proptest! {
    /// The by-value transform gives the same bytes and authorization as `transform`.
    #[test]
    fn proptest_transform_owned_equals_transform(
        input in proptest::collection::vec(proptest::prelude::any::<u8>(), 0..512),
        config in proptest::prelude::any::<bool>(),
    ) {
        let profile = if config { Profile::Scanned } else { Profile::Transcript };
        let path = if config { "notes.txt" } else { "s.jsonl" };
        let source = admitted(&[path], profile).map_err(proptest::test_runner::TestCaseError::fail)?;
        let a = privacy::transform(&source, &input);
        let b = privacy::transform_owned(&source, input.clone());
        match (a, b) {
            (Ok(x), Ok(y)) => {
                let (x, y) = (privacy::authorize(&source, x, &context()), privacy::authorize(&source, y, &context()));
                match (x, y) {
                    (Authorization::Allow(x), Authorization::Allow(y)) => {
                        proptest::prop_assert_eq!(x.bytes(), y.bytes());
                        proptest::prop_assert!(config || x.bytes() == input.as_slice());
                    }
                    (Authorization::Deny(x), Authorization::Deny(y)) => proptest::prop_assert_eq!(x, y),
                    (Authorization::Unknown(x), Authorization::Unknown(y)) => proptest::prop_assert_eq!(x, y),
                    _ => proptest::prop_assert!(false, "authorization differs"),
                }
            }
            (Err(x), Err(y)) => proptest::prop_assert_eq!(x, y),
            _ => proptest::prop_assert!(false, "transform differs"),
        }
    }
}
