//! Independently authored privacy regression oracles from the parent review.
use memory_core::privacy::{
    Admission, Authorization, AuthorizedBody, PolicyContext, Profile, ResolvedSourceFacts, admit,
    authorize, rules_digest, sanitize_launch, transform,
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

fn publish(path: &str, bytes: &[u8]) -> Result<AuthorizedBody, &'static str> {
    let policy = context();
    let Admission::Allow(source) = admit(&policy, facts(&[path], Profile::Config)) else {
        return Err("fixture admission failed");
    };
    let redacted = transform(&source, bytes).map_err(|_| "fixture transform failed")?;
    match authorize(source, redacted, &policy) {
        Authorization::Allow(body) => Ok(body),
        Authorization::Deny(_) | Authorization::Unknown(_) => Err("fixture authorization failed"),
    }
}

#[test]
fn malformed_selected_url_does_not_preserve_invalid_port_literal()
-> Result<(), Box<dyn std::error::Error>> {
    let body = publish(
        "synthetic/.mcp.json",
        br#"{"mcpServers":{"demo":{"url":"https://host:FixtureCanary/"}}}"#,
    )?;
    let value: serde_json::Value = serde_json::from_slice(body.bytes())?;
    assert_eq!(
        value.pointer("/mcpServers/demo/url"),
        Some(&serde_json::Value::String("[redacted]".to_owned()))
    );
    Ok(())
}

#[test]
fn malformed_selected_url_does_not_preserve_invalid_escape_literal()
-> Result<(), Box<dyn std::error::Error>> {
    let body = publish(
        "synthetic/.mcp.json",
        br#"{"mcpServers":{"demo":{"url":"https://host/%ZZFixtureCanary"}}}"#,
    )?;
    let value: serde_json::Value = serde_json::from_slice(body.bytes())?;
    assert_eq!(
        value.pointer("/mcpServers/demo/url"),
        Some(&serde_json::Value::String("[redacted]".to_owned()))
    );
    Ok(())
}

#[test]
fn scrubbed_toml_retains_unrelated_datetime_type() -> Result<(), Box<dyn std::error::Error>> {
    let body = publish(
        "synthetic/config.toml",
        b"when = 1979-05-27T07:32:00Z\n[mcp_servers.demo.env]\nAPI_KEY = \"FixtureCanary\"\n",
    )?;
    let output = std::str::from_utf8(body.bytes())?;
    let value: toml::Value = toml::from_str(output)?;
    assert!(value.get("when").is_some_and(toml::Value::is_datetime));
    assert!(!output.contains("FixtureCanary"));
    Ok(())
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
    let Admission::Allow(config) = admit(
        &policy,
        facts(&["synthetic/settings.json"], Profile::Config),
    ) else {
        return Err("fixture admission failed");
    };
    assert!(matches!(
        authorize(config, raw, &policy),
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
fn escaped_duplicate_config_keys_fail_closed_and_launch_prompt_is_dropped() {
    assert!(
        publish(
            "synthetic/settings.json",
            br#"{"env":{"SAFE":"one","SA\u0046E":"FixtureCanary"}}"#,
        )
        .is_err()
    );
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
