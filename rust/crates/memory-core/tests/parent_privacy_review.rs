//! Independently authored privacy regression oracles from the parent review.
use memory_core::privacy::{
    Admission, Authorization, PolicyContext, Profile, ResolvedSourceFacts, admit, authorize,
    rules_digest, sanitize_launch, transform,
};

fn context() -> PolicyContext {
    PolicyContext {
        valid: true,
        revision: "review-1".to_owned(),
        rules: Some(rules_digest()),
        collector_paths: vec![],
        collector_identities: vec![],
    }
}

fn facts(paths: &[&str], profile: Profile) -> ResolvedSourceFacts {
    ResolvedSourceFacts {
        identities: paths.iter().map(|path| (*path).to_owned()).collect(),
        file_identity: Some(42),
        ancestor_identities: vec![],
        path_resolved: true,
        root_resolved: true,
        owner: Some("synthetic-review-owner".to_owned()),
        profile,
    }
}

#[test]
fn transformed_capability_cannot_be_rebound_to_a_protected_source() -> Result<(), &'static str> {
    let policy = context();
    let Admission::Allow(transcript) = admit(
        &policy,
        facts(&["synthetic/session.jsonl"], Profile::Transcript),
    ) else {
        return Err("fixture admission failed");
    };
    let raw = transform(&transcript, br#"{"env":{"API_KEY":"FixtureCanary"}}"#)
        .map_err(|_| "fixture transform failed")?;
    let Admission::Allow(config) = admit(&policy, facts(&["synthetic/run.json"], Profile::Scanned))
    else {
        return Err("fixture admission failed");
    };
    assert!(matches!(
        authorize(&config, raw, &policy),
        Authorization::Deny(_)
    ));
    Ok(())
}

#[test]
fn intermediate_credential_and_unknown_root_are_not_admitted() {
    let policy = context();
    assert!(matches!(
        admit(
            &policy,
            facts(
                &[
                    "synthetic/session.jsonl",
                    "synthetic/auth.json",
                    "synthetic/final"
                ],
                Profile::Transcript
            )
        ),
        Admission::Deny(_)
    ));
    let mut unresolved = facts(&["synthetic/session.jsonl"], Profile::Transcript);
    unresolved.root_resolved = false;
    assert!(matches!(admit(&policy, unresolved), Admission::Unknown(_)));
}

#[test]
fn launch_prompt_is_dropped() {
    let launch = sanitize_launch(
        &["/bin/claude".to_owned(), "FixtureCanary".to_owned()],
        &[
            "ANTHROPIC_API_KEY=FixtureCanary".to_owned(),
            "ANTHROPIC_MODEL=sonnet".to_owned(),
        ],
    );
    assert!(
        !launch
            .argv()
            .iter()
            .any(|arg| arg.contains("FixtureCanary"))
    );
    assert!(
        !launch
            .env()
            .values()
            .any(|value| value.contains("FixtureCanary"))
    );
    assert_eq!(
        launch.env().get("ANTHROPIC_MODEL").map(String::as_str),
        Some("sonnet")
    );
}
