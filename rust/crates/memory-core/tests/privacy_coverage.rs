//! Independent adversarial known-file boundary fixtures; all data is synthetic.
use memory_core::privacy::{
    self, Admission, Authorization, PolicyContext, Profile, Reason, ResolvedSourceFacts,
    UnknownReason,
};

type TestResult = Result<(), Box<dyn std::error::Error>>;

fn policy() -> PolicyContext {
    PolicyContext {
        valid: true,
        revision: "independent-negative-fixtures".into(),
        rules: Some(privacy::rules_digest()),
        collector_paths: vec![],
        collector_identities: vec![],
    }
}
fn facts(path: &str, profile: Profile) -> ResolvedSourceFacts {
    ResolvedSourceFacts {
        identities: vec![path.into()],
        file_identity: Some(123),
        ancestor_identities: vec![456],
        path_resolved: true,
        root_resolved: true,
        owner: Some("synthetic-owner".into()),
        profile,
    }
}
fn transformed(path: &str, profile: Profile, bytes: &[u8]) -> Result<Vec<u8>, Reason> {
    let context = policy();
    let Admission::Allow(source) = privacy::admit(&context, facts(path, profile)) else {
        return Err(Reason::UnknownKind);
    };
    let redacted = privacy::transform(&source, bytes)?;
    match privacy::authorize(&source, redacted, &context) {
        Authorization::Allow(body) => Ok(body.bytes().to_vec()),
        Authorization::Deny(reason) => Err(reason),
        Authorization::Unknown(_) => Err(Reason::ContextChanged),
    }
}
#[test]
fn missing_observations_have_distinct_closed_outcomes() {
    let base = facts("synthetic/session.jsonl", Profile::Transcript);
    let mut context = policy();
    context.valid = false;
    assert!(matches!(
        privacy::admit(&context, base.clone()),
        Admission::Unknown(UnknownReason::ConfigInvalid)
    ));
    for variant in 0..5 {
        let mut source = base.clone();
        match variant {
            0 => source.root_resolved = false,
            1 => source.path_resolved = false,
            2 => source.identities.clear(),
            3 => source.file_identity = None,
            _ => source.profile = Profile::Unknown,
        }
        let result = privacy::admit(&policy(), source);
        match variant {
            0 => assert!(matches!(
                result,
                Admission::Unknown(UnknownReason::RootUnresolvable)
            )),
            1..=3 => assert!(matches!(
                result,
                Admission::Unknown(UnknownReason::PathUnresolvable)
            )),
            _ => assert!(matches!(result, Admission::Deny(Reason::UnknownKind))),
        }
    }
    for owner in [None, Some(String::new())] {
        let mut source = base.clone();
        source.owner = owner;
        assert!(matches!(
            privacy::admit(&policy(), source),
            Admission::Deny(Reason::UnknownOwner)
        ));
    }
}

#[test]
fn collector_ancestor_alias_and_other_known_profile() -> TestResult {
    let mut context = policy();
    context.collector_identities.push(456);
    assert!(matches!(
        privacy::admit(&context, facts("synthetic/ordinary", Profile::OtherKnown)),
        Admission::Deny(Reason::CollectorState)
    ));
    context.collector_identities.clear();
    context.collector_paths.push("synthetic/state/".into());
    assert!(matches!(
        privacy::admit(
            &context,
            facts("synthetic/state/child", Profile::OtherKnown)
        ),
        Admission::Deny(Reason::CollectorState)
    ));
    assert_eq!(
        transformed(
            "synthetic/ordinary",
            Profile::OtherKnown,
            b"API_KEY=fixture-literal"
        )?,
        b"API_KEY=fixture-literal"
    );
    Ok(())
}

#[test]
fn absent_rules_only_withhold_the_scanned_profile() {
    for rules in [None, Some([0; 32])] {
        let mut context = policy();
        context.rules = rules;
        assert!(matches!(
            privacy::admit(&context, facts("synthetic/run.json", Profile::Scanned)),
            Admission::Unknown(UnknownReason::RulesUnavailable)
        ));
        assert!(matches!(
            privacy::admit(
                &context,
                facts("synthetic/session.jsonl", Profile::Transcript)
            ),
            Admission::Allow(_)
        ));
    }
}

#[test]
fn launch_empty_unknown_missing_and_duplicate_metadata() {
    assert!(privacy::sanitize_launch(&[], &[]).argv().is_empty());
    let argv = [
        "/synthetic/unknown",
        "--version=fixture",
        "--unknown=fixture",
        "--model",
    ]
    .map(str::to_owned);
    let env = [
        "MALFORMED",
        "anthropic_model=fixture",
        "ANTHROPIC_MODEL=first",
        "ANTHROPIC_MODEL=second",
        "ANTHROPIC_MODEL=unsafe value",
        "MAX_THINKING_TOKENS=123",
    ]
    .map(str::to_owned);
    let launch = privacy::sanitize_launch(&argv, &env);
    assert_eq!(
        launch.argv(),
        &[
            "[redacted]",
            "[redacted]",
            "[redacted]",
            "--model",
            "[redacted]"
        ]
    );
    assert_eq!(launch.env().len(), 2);
    assert_eq!(
        launch.env().get("ANTHROPIC_MODEL").map(String::as_str),
        Some("second")
    );
    assert_eq!(
        launch.env().get("MAX_THINKING_TOKENS").map(String::as_str),
        Some("123")
    );
}
