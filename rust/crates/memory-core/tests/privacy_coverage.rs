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
    match privacy::authorize(source, redacted, &context) {
        Authorization::Allow(body) => Ok(body.bytes().to_vec()),
        Authorization::Deny(reason) => Err(reason),
        Authorization::Unknown(_) => Err(Reason::ContextChanged),
    }
}
fn json(
    path: &str,
    value: &serde_json::Value,
) -> Result<serde_json::Value, Box<dyn std::error::Error>> {
    let output = transformed(path, Profile::Config, &serde_json::to_vec(value)?)?;
    Ok(serde_json::from_slice(&output)?)
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
fn absent_rules_only_withhold_protected_profiles() {
    for rules in [None, Some([0; 32])] {
        let mut context = policy();
        context.rules = rules;
        for (path, profile) in [
            ("synthetic/asset", Profile::TextAsset),
            ("synthetic/config", Profile::Config),
            ("synthetic/settings.json", Profile::Transcript),
        ] {
            assert!(matches!(
                privacy::admit(&context, facts(path, profile)),
                Admission::Unknown(UnknownReason::RulesUnavailable)
            ));
        }
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

#[test]
fn fish_nonassignments_preserve_exact_bytes_as_in_go_text_fixtures() -> TestResult {
    // internal/scrub/text_test.go: TestTextDoesNotTreatUnrelatedSetCommandsAsAssignments.
    for line in [
        "set '-gx API_KEY fixture-private-value",
        "set -q API_KEY fixture-private-value",
        "set -gx --erase API_KEY fixture-private-value",
        "set -g API_KEY fixture-private-value",
        "set -gx 'PASSWORD NAME' fixture-private-value",
        "set -gx 'API_KEY fixture-private-value",
        "set --global --export",
        "set -- API_KEY fixture-private-value",
        "set -gx PASSWORD",
    ] {
        assert_eq!(
            transformed("synthetic/skill.md", Profile::TextAsset, line.as_bytes())?,
            line.as_bytes()
        );
    }
    Ok(())
}

#[test]
fn fish_unfinished_values_and_non_utf8_text_match_go_boundary() -> TestResult {
    for line in [
        "set -gx API_KEY 'fixture-private-value",
        "set --global --export API_KEY \\",
        "true;or set -gx -- API_KEY fixture-private-value",
    ] {
        assert!(matches!(
            transformed("synthetic/skill.md", Profile::TextAsset, line.as_bytes()),
            Err(Reason::SecretInText)
        ));
    }
    let bytes = b"API_KEY=fixture\xff";
    assert_eq!(
        transformed("synthetic/skill.md", Profile::TextAsset, bytes)?,
        bytes
    );
    Ok(())
}

#[test]
fn malformed_url_forms_never_escape_as_literals() -> TestResult {
    for url in [
        " https://host/fixture",
        "https://host/fixture\n",
        "fixture-relative",
        "mailto:fixture@example.test",
        "https:example.test/fixture",
        "https:///fixture",
        "https://host/%",
        "https://host/#%ZZ",
        "https://[broken/fixture",
        "https://host:99999/fixture",
    ] {
        let output = json(
            "synthetic/mcp.json",
            &serde_json::json!({"mcpServers":{"s":{"url":url}}, "copy":url}),
        )?;
        assert_eq!(
            output.pointer("/mcpServers/s/url"),
            Some(&serde_json::json!("[redacted]"))
        );
        assert_eq!(output.get("copy"), Some(&serde_json::json!("[redacted]")));
    }
    Ok(())
}

#[test]
fn url_scrubbing_preserves_safe_spelling_and_reference_query_components() -> TestResult {
    for (input, expected) in [
        ("${MCP_URL}", "${MCP_URL}"),
        (
            "HTTPS://User:fixture@EXAMPLE.test:443/a/../b%2f?ref=%24TOKEN&bad=%FF&flag#Frag",
            "HTTPS://User@EXAMPLE.test:443/a/../b%2f?ref=%24TOKEN&bad=[redacted]&flag#Frag",
        ),
        (
            "//User@EXAMPLE.test:80/a?ref=Bearer+%24TOKEN&n=2&empty=",
            "//User@EXAMPLE.test:80/a?ref=Bearer+%24TOKEN&n=2&empty=",
        ),
        (
            "https://host/a?x=%Q0&y=%&z=%2",
            "https://host/a?x=[redacted]&y=[redacted]&z=[redacted]",
        ),
    ] {
        let output = json(
            "synthetic/mcp.json",
            &serde_json::json!({"mcpServers":{"s":{"url":input}}}),
        )?;
        assert_eq!(
            output.pointer("/mcpServers/s/url"),
            Some(&serde_json::json!(expected))
        );
    }
    Ok(())
}

#[test]
fn selected_nested_shapes_redact_strings_not_json_primitives() -> TestResult {
    let input = serde_json::json!({"primaryApiKey":{"nested":["fixture-literal",null,true,false,12.5,{"leaf":"other-literal"}]},"safe":[null,true,false,42],"copy":"fixture-literal"});
    let output = json("synthetic/claude.json", &input)?;
    assert_eq!(
        output,
        serde_json::json!({"primaryApiKey":{"nested":["[redacted]",null,true,false,12.5,{"leaf":"[redacted]"}]},"safe":[null,true,false,42],"copy":"[redacted]"})
    );
    let nested_env =
        serde_json::json!({"env":{"X":["fixture-env", {"leaf":"fixture-leaf"}, false, null]}});
    assert_eq!(
        json("synthetic/settings.json", &nested_env)?,
        serde_json::json!({"env":{"X":["[redacted]", {"leaf":"[redacted]"}, false, null]}})
    );
    Ok(())
}

#[test]
fn args_odd_shapes_and_header_references_keep_go_known_file_semantics() -> TestResult {
    // Header reference/literal cases derive from internal/scrub/scrub_test.go.
    let input = serde_json::json!({"mcpServers":{
        "array":{"args":[null,true,7,{"safe":"ordinary"},"--header","X-Ref: $TOKEN","-H","X-Key: fixture-literal","--header=NoColon","--api-key=fixture-literal","--token","--other"]},
        "object":{"args":{"nested":["fixture-object",false]}},
        "scalar":{"args":"--token=fixture-scalar"},
        "safeScalar":{"args":"ordinary-positional"},
        "objectLeaves":{"args":{"safe":"ordinary-leaf", "secret":"--api-key=fixture-leaf", "inline":"{\"env\":{\"API_KEY\":\"fixture-object-inline\"}}"}},
        "inline":{"args":["{malformed", "{\"env\":{\"API_KEY\":\"fixture-inline\"}}"]}
    }});
    let output = json("synthetic/mcp.json", &input)?;
    assert_eq!(
        output.pointer("/mcpServers/array/args"),
        Some(
            &serde_json::json!([null,true,7,{"safe":"ordinary"},"--header","X-Ref: $TOKEN","-H","X-Key: [redacted]","--header=NoColon","--api-key=[redacted]","--token","--other"])
        )
    );
    assert_eq!(
        output.pointer("/mcpServers/object/args"),
        Some(&serde_json::json!({"nested":["fixture-object",false]}))
    );
    assert_eq!(
        output.pointer("/mcpServers/inline/args"),
        Some(&serde_json::json!([
            "[redacted]",
            "{\"env\":{\"API_KEY\":\"[redacted]\"}}"
        ]))
    );
    assert_eq!(
        output.pointer("/mcpServers/safeScalar/args"),
        Some(&serde_json::json!("ordinary-positional"))
    );
    assert_eq!(
        output.pointer("/mcpServers/objectLeaves/args"),
        Some(
            &serde_json::json!({"safe":"ordinary-leaf", "secret":"--api-key=[redacted]", "inline":"{\"env\":{\"API_KEY\":\"[redacted]\"}}"})
        )
    );
    assert_eq!(
        output.pointer("/mcpServers/scalar/args"),
        Some(&serde_json::json!("--token=[redacted]"))
    );
    Ok(())
}

#[test]
fn protected_depth_and_conservative_expansion_fail_closed() -> TestResult {
    let nested = format!("{{\"safe\":{}0{}}}", "[".repeat(130), "]".repeat(130));
    assert!(matches!(
        transformed(
            "synthetic/settings.json",
            Profile::Config,
            nested.as_bytes()
        ),
        Err(Reason::MalformedProtected)
    ));
    let input =
        serde_json::json!({"primaryApiKey":"fixture-literal", "safe":"a".repeat(1_500_000)});
    let bytes = serde_json::to_vec(&input)?;
    assert!(bytes.len() < privacy::MAX_PROTECTED_BYTES);
    assert!(matches!(
        transformed("synthetic/claude.json", Profile::Config, &bytes),
        Err(Reason::SourceLimit)
    ));
    Ok(())
}

#[test]
fn toml_nested_arrays_preserve_native_values_when_strings_change() -> TestResult {
    let input = b"safe = [[1, 2], [3, 4]]\nwhen = 1979-05-27\n[mcp_servers.demo]\nbearer_token = 'fixture-literal'\nargs = ['--token', 'fixture-arg']\n";
    let output = transformed("synthetic/config.toml", Profile::Config, input)?;
    let value: toml::Value = toml::from_str(std::str::from_utf8(&output)?)?;
    let original: toml::Value = toml::from_str(std::str::from_utf8(input)?)?;
    assert_eq!(value.get("safe"), original.get("safe"));
    assert_eq!(value.get("when"), original.get("when"));
    assert!(!std::str::from_utf8(&output)?.contains("fixture-"));
    Ok(())
}
