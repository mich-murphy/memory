//! Synthetic Go-parity vectors; no filesystem/source-state access.
use memory_core::privacy::{
    self, Admission, Authorization, AuthorizedBody, PolicyContext, Profile, Reason,
    ResolvedSourceFacts, UnknownReason,
};
use sha2::{Digest, Sha256};

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
    match privacy::authorize(source, redacted, &context()) {
        Authorization::Allow(value) => Ok(value),
        _ => Err("fixture authorization failed".into()),
    }
}

#[test]
fn all_eleven_names_all_agents_aliases_and_whole_document_digest()
-> Result<(), Box<dyn std::error::Error>> {
    let cases = [
        (
            "claude.json",
            r#"{"primaryApiKey":"CANARY","copy":"CANARY"}"#,
        ),
        (
            ".claude.json",
            r#"{"primaryApiKey":"CANARY","copy":"CANARY"}"#,
        ),
        (
            "settings.json",
            r#"{"env":{"KEY":"CANARY"},"copy":"CANARY"}"#,
        ),
        (
            "settings.local.json",
            r#"{"env":{"KEY":"CANARY"},"copy":"CANARY"}"#,
        ),
        (
            "managed-settings.json",
            r#"{"env":{"KEY":"CANARY"},"copy":"CANARY"}"#,
        ),
        (
            ".mcp.json",
            r#"{"mcpServers":{"s":{"env":{"KEY":"CANARY"}}},"copy":"CANARY"}"#,
        ),
        (
            "mcp.json",
            r#"{"mcpServers":{"s":{"env":{"KEY":"CANARY"}}},"copy":"CANARY"}"#,
        ),
        (
            "managed-mcp.json",
            r#"{"mcpServers":{"s":{"env":{"KEY":"CANARY"}}},"copy":"CANARY"}"#,
        ),
        (
            "models.json",
            r#"{"providers":{"s":{"apiKey":"CANARY"}},"copy":"CANARY"}"#,
        ),
        (
            "config.toml",
            "copy='CANARY'\n[mcp_servers.s]\nbearer_token='CANARY'\n",
        ),
        (
            "managed_config.toml",
            "copy='CANARY'\n[mcp_servers.s]\nbearer_token='CANARY'\n",
        ),
    ];
    for agent in ["claude", "codex", "pi"] {
        for (name, input) in cases {
            let identity = format!("/{agent}/{name}");
            let output = body(
                &["/safe/alias.md", &identity, "/final/unknown"],
                Profile::TextAsset,
                input.as_bytes(),
            )?;
            assert!(!String::from_utf8_lossy(output.bytes()).contains("CANARY"));
            assert_eq!(
                output.digest(),
                <[u8; 32]>::from(Sha256::digest(output.bytes()))
            );
            assert_ne!(
                output.digest(),
                <[u8; 32]>::from(Sha256::digest(input.as_bytes()))
            );
            assert_eq!(output.owner(), "fixture-owner");
        }
    }
    Ok(())
}

#[test]
fn escaped_copies_nested_arrays_and_keys_are_not_lost() -> Result<(), Box<dyn std::error::Error>> {
    let input = r#"{"providers":{"s":{"apiKey":"synthetic-\u2603\nsecret"}},"copy":["synthetic-☃\nsecret",{"v":"synthetic-\u2603\nsecret"}],"synthetic-\u2603\nsecret":1}"#;
    let output = body(&["models.json"], Profile::Config, input.as_bytes())?;
    let value: serde_json::Value = serde_json::from_slice(output.bytes())?;
    assert_eq!(
        value
            .pointer("/providers/s/apiKey")
            .and_then(serde_json::Value::as_str),
        Some("[redacted]")
    );
    assert_eq!(
        value.pointer("/copy/0").and_then(serde_json::Value::as_str),
        Some("[redacted]")
    );
    assert_eq!(
        value
            .pointer("/copy/1/v")
            .and_then(serde_json::Value::as_str),
        Some("[redacted]")
    );
    assert_eq!(
        value
            .get("synthetic-☃\nsecret")
            .and_then(serde_json::Value::as_u64),
        Some(1)
    );
    Ok(())
}

#[test]
fn rejects_malformed_duplicates_unicode_trailing_and_nonobjects()
-> Result<(), Box<dyn std::error::Error>> {
    for input in [
        br#"{"env":{"KEY":"CANARY","\u004bEY":"$REF"}}"#.as_slice(),
        br#"{"x":[{"env":{},"env":{}}]}"#,
        br#"{"env":{"KEY":"\ud800"}}"#,
        b"{\"env\":{\"KEY\":\"bad\xff\"}}",
        b"{} {}",
        b"[]",
        b"null",
        b"\"CANARY\"",
        b"{",
    ] {
        let source = admitted(&["settings.json"], Profile::Config)?;
        assert!(matches!(
            privacy::transform(&source, input),
            Err(Reason::MalformedProtected)
        ));
    }
    for input in [
        "[mcp_servers.s]\nbearer_token=CANARY",
        "x='a'\nx='CANARY'",
        "[mcp_servers.s]\nbearer_token=\"CANARY\\z\"",
    ] {
        let source = admitted(&["config.toml"], Profile::Config)?;
        let error = privacy::transform(&source, input.as_bytes())
            .err()
            .ok_or("expected parse error")?;
        assert_eq!(error.to_string(), "malformed_protected");
    }
    Ok(())
}

#[test]
fn rules_union_only_encountered_identities_and_supported_reference_syntax()
-> Result<(), Box<dyn std::error::Error>> {
    let input = br#"{"env":{"KEY":"canary-env"},"providers":{"s":{"apiKey":"canary-api"}},"mcpServers":{"s":{"headers":{"H":"canary-header"}}}}"#;
    let all = body(
        &["settings.json", "models.json.bak.old~", "mcp.json"],
        Profile::Config,
        input,
    )?;
    assert!(!String::from_utf8_lossy(all.bytes()).contains("canary-"));
    let one = body(&["models.json"], Profile::Config, input)?;
    let text = String::from_utf8_lossy(one.bytes());
    assert!(
        text.contains("canary-env")
            && text.contains("canary-header")
            && !text.contains("canary-api")
    );
    let references = br#"{"providers":{"ref":{"apiKey":"OPENAI_API_KEY","headers":{"H":"!read-key"}},"cmd":{"apiKey":"!read-key"},"literal":{"apiKey":"CANARY"}},"other":"safe"}"#;
    let out = body(&["models.json"], Profile::Config, references)?;
    let text = String::from_utf8_lossy(out.bytes());
    assert!(
        text.contains("OPENAI_API_KEY") && text.contains("!read-key") && !text.contains("canary-")
    );
    for value in [
        "$TOKEN",
        "${TOKEN}",
        "Bearer ${TOKEN}",
        "1234",
        "true",
        "[redacted]",
    ] {
        let doc = serde_json::json!({"env":{"KEY":value}});
        let encoded = serde_json::to_vec(&doc)?;
        assert_eq!(
            body(&["settings.json"], Profile::Config, &encoded)?.bytes(),
            encoded
        );
    }
    for value in [
        "${TOKEN:-CANARY}",
        "Bearer ${TOKEN} CANARY",
        "!read-key",
        "OPENAI_API_KEY",
    ] {
        let encoded = serde_json::to_vec(&serde_json::json!({"env":{"KEY":value}}))?;
        let out = body(&["settings.json"], Profile::Config, &encoded)?;
        let value: serde_json::Value = serde_json::from_slice(out.bytes())?;
        assert_eq!(
            value
                .pointer("/env/KEY")
                .and_then(serde_json::Value::as_str),
            Some("[redacted]")
        );
    }
    Ok(())
}

#[test]
fn codex_and_claude_all_rule_families_including_args_and_urls()
-> Result<(), Box<dyn std::error::Error>> {
    let input = br#"{"primaryApiKey":"CANARY","customApiKeyResponses":{"approved":["CANARY"]},"projects":{"p":{"mcpServers":{"s":{"env":{"A":"CANARY"},"headers":{"H":"CANARY"},"oauth":{"clientSecret":"CANARY"},"args":["--api-key","CANARY","-H","X-Key: CANARY","{\"env\":{\"A\":\"CANARY\"}}"],"url":"https://u:CANARY@host/p?token=CANARY&ref=$TOKEN"}}}}}"#;
    let out = body(&["claude.json"], Profile::Config, input)?;
    let text = String::from_utf8_lossy(out.bytes());
    assert!(!text.contains("CANARY"));
    assert!(text.contains("https://u@host/p?token=[redacted]&ref=$TOKEN"));
    let input = "[mcp_servers.s]\nargs=['--token','CANARY']\nurl='https://u:CANARY@host/?x=CANARY'\nbearer_token='CANARY'\n[mcp_servers.s.env]\nKEY='CANARY'\n[mcp_servers.s.http_headers]\nH='CANARY'\n[model_providers.s]\nexperimental_bearer_token='CANARY'\nbase_url='https://host/?x=CANARY'\n[model_providers.s.http_headers]\nH='CANARY'\n[shell_environment_policy.set]\nKEY='CANARY'\n[otel.exporter.otlp-http.headers]\nH='CANARY'\n";
    assert!(
        !String::from_utf8_lossy(
            body(&["config.toml"], Profile::Config, input.as_bytes())?.bytes()
        )
        .contains("CANARY")
    );
    Ok(())
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
                    facts(&["alias.md", &path, "final.txt"], Profile::TextAsset)
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
            privacy::admit(&context(), facts(&[path], Profile::Config)),
            Admission::Deny(Reason::CollectorState)
        ));
    }
    let mut aliases = facts(&["renamed.md"], Profile::Config);
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
        privacy::admit(&ctx, facts(&["settings.json"], Profile::Config)),
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
        admitted(&["settings.json"], Profile::Config)?,
        admitted(&["transcript.jsonl"], Profile::Config)?,
    ] {
        let original = admitted(&["transcript.jsonl"], Profile::Transcript)?;
        let raw = privacy::transform(&original, b"unsafe config-shaped transcript")?;
        assert!(matches!(
            privacy::authorize(target, raw, &context()),
            Authorization::Deny(Reason::ContextChanged)
        ));
    }
    let source = admitted(&["x"], Profile::Transcript)?;
    let redacted = privacy::transform(&source, b"raw")?;
    let mut ctx = context();
    ctx.revision = "changed".into();
    assert!(matches!(
        privacy::authorize(source, redacted, &ctx),
        Authorization::Deny(Reason::ContextChanged)
    ));
    let raw = b"not json\xff\nAPI_KEY=CANARY\n";
    assert_eq!(
        body(&["transcript.jsonl"], Profile::Transcript, raw)?.bytes(),
        raw
    );
    // Kind cannot override a recognized protected identity.
    let source = admitted(&["settings.json"], Profile::Transcript)?;
    assert!(matches!(
        privacy::transform(&source, b"not json"),
        Err(Reason::MalformedProtected)
    ));
    assert_eq!(privacy::rules_digest(), privacy::rules_digest());
    assert!(!privacy::TRANSFORM_VERSION.is_empty());
    Ok(())
}

#[test]
fn text_guard_shell_fish_quotes_separators_and_negative_controls()
-> Result<(), Box<dyn std::error::Error>> {
    for line in [
        "API_KEY=CANARY",
        "OTHER=ok;API_KEY=CANARY",
        "OTHER=ok&&API_KEY=CANARY",
        "OTHER=ok|API_KEY=CANARY",
        "OTHER=ok API_KEY=CANARY CMD='",
        "API_KEY='${TOKEN}'",
        "API_KEY=\\$TOKEN",
        "API_KEY=\"\\$TOKEN\"",
        "API_KEY=\"$TOKEN\"'${TOKEN}'",
        "API_KEY=\\",
        "set -gx API_KEY CANARY",
        "true;and set -xg 'API_KEY' CANARY",
        "set --export --global API_KEY CANARY",
        "s'et' '-gx' A'PI'_KEY CANARY",
        "ex'port' API_KEY=CANARY",
        "```sh\nAPI_KEY=CANARY\n```",
        "API_KEY=${TOKEN:-CANARY}",
    ] {
        let source = admitted(&["SKILL.md"], Profile::TextAsset)?;
        assert!(
            matches!(
                privacy::transform(&source, line.as_bytes()),
                Err(Reason::SecretInText)
            ),
            "{line}"
        );
    }
    for line in [
        "API_KEY=$TOKEN",
        "export API_KEY=\"Bearer ${TOKEN}\"",
        "set -gx API_KEY \"${TOKEN}\"",
        "API_KEY='[redacted]'",
        "AUTH=false",
        "TOKEN=1234",
        "echo API_KEY=CANARY",
        "# API_KEY=CANARY",
        "For example, export API_KEY=CANARY",
        "'API_KEY=CANARY'",
        "'and' export API_KEY=CANARY",
        "set -g API_KEY CANARY",
        "set -gx --erase API_KEY CANARY",
        "set -gx PASSWORD",
        "OTHER='ok;API_KEY=CANARY'",
        "API_KEY=$TOKEN OTHER='",
    ] {
        assert_eq!(
            body(&["SKILL.md"], Profile::TextAsset, line.as_bytes())?.bytes(),
            line.as_bytes(),
            "{line}"
        );
    }
    let code = b"rows=sorted(\n    key=lambda r: r[0],\n)\n";
    assert_eq!(
        body(&["tool.py.bak"], Profile::TextAsset, code)?.bytes(),
        code
    );
    let source = admitted(&["tool.py", "tool.sh"], Profile::TextAsset)?;
    assert!(matches!(
        privacy::transform(&source, code),
        Err(Reason::SecretInText)
    ));
    assert_eq!(
        body(&["SKILL.md"], Profile::TextAsset, b"API_KEY=CANARY\0")?.bytes(),
        b"API_KEY=CANARY\0"
    );
    let large = vec![b'x'; privacy::MAX_PROTECTED_BYTES + 1];
    let source = admitted(&["settings.json"], Profile::Config)?;
    assert!(matches!(
        privacy::transform(&source, &large),
        Err(Reason::SourceLimit)
    ));
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
fn toml_datetime_types_and_url_parser_parity() -> Result<(), Box<dyn std::error::Error>> {
    let input = "date=1979-05-27\ntime=07:32:00\nstamp=1979-05-27T07:32:00Z\n[mcp_servers.s]\nbearer_token='CANARY'\n";
    let out = body(&["config.toml"], Profile::Config, input.as_bytes())?;
    let before: toml::Value = toml::from_str(input)?;
    let after: toml::Value = toml::from_str(std::str::from_utf8(out.bytes())?)?;
    for key in ["date", "time", "stamp"] {
        assert_eq!(before.get(key), after.get(key));
    }
    for value in [
        "https://host:CANARY/",
        "https://host/%xx",
        "https://host/%",
        "https://host/\tCANARY",
        "https://[bad]/",
        "https://u:%xx@host/",
    ] {
        let input = serde_json::to_vec(&serde_json::json!({"mcpServers":{"s":{"url":value}}}))?;
        let out = body(&["mcp.json"], Profile::Config, &input)?;
        let doc: serde_json::Value = serde_json::from_slice(out.bytes())?;
        assert_eq!(
            doc.pointer("/mcpServers/s/url")
                .and_then(serde_json::Value::as_str),
            Some("[redacted]"),
            "{value}"
        );
    }
    for value in [
        "https://[::1]:123/x?ref=$TOKEN",
        "https://host/x?ref=%24%7BTOKEN%7D",
    ] {
        let input = serde_json::to_vec(&serde_json::json!({"mcpServers":{"s":{"url":value}}}))?;
        assert_eq!(body(&["mcp.json"], Profile::Config, &input)?.bytes(), input);
    }
    let input = br#"{"mcpServers":{"s":{"url":"https://u:CANARY@[::1]:443/?x=%xx&safe=$TOKEN"}}}"#;
    let out = body(&["mcp.json"], Profile::Config, input)?;
    assert!(!String::from_utf8_lossy(out.bytes()).contains("CANARY"));
    assert!(String::from_utf8_lossy(out.bytes()).contains("safe=$TOKEN"));
    Ok(())
}

#[test]
fn unrelated_json_numeric_lexemes_survive_rewrite() -> Result<(), Box<dyn std::error::Error>> {
    let input = br#"{"env":{"KEY":"CANARY"},"big":18446744073709551615123456789,"precise":1.23456789012345678901234567890,"exponent":1e999,"$serde_json::private::Number":"still an object string"}"#;
    let out = body(&["settings.json"], Profile::Config, input)?;
    let text = std::str::from_utf8(out.bytes())?;
    for retained in [
        "18446744073709551615123456789",
        "1.23456789012345678901234567890",
        "1e999",
        "still an object string",
    ] {
        assert!(text.contains(retained));
    }
    assert!(!text.contains("CANARY"));
    Ok(())
}

#[test]
fn rule_digest_golden_requires_explicit_semantic_review() {
    assert_eq!(
        privacy::rules_digest(),
        [
            165, 111, 102, 164, 34, 45, 153, 245, 246, 77, 32, 158, 156, 245, 175, 12, 249, 62,
            116, 64, 95, 48, 53, 187, 0, 199, 188, 223, 239, 246, 188, 50
        ]
    );
}

#[test]
fn url_rewrite_preserves_safe_spelling_not_whatwg_normalization()
-> Result<(), Box<dyn std::error::Error>> {
    for (value, expected) in [
        (
            "https://u:CANARY@EXAMPLE.com:443/a/../b%2f?ref=%24TOKEN&key=CANARY#frag",
            "https://u@EXAMPLE.com:443/a/../b%2f?ref=%24TOKEN&key=[redacted]#frag",
        ),
        ("//u:CANARY@EXAMPLE.com:443/p", "//u@EXAMPLE.com:443/p"),
    ] {
        let input = serde_json::to_vec(&serde_json::json!({"mcpServers":{"s":{"url":value}}}))?;
        let out = body(&["mcp.json"], Profile::Config, &input)?;
        let doc: serde_json::Value = serde_json::from_slice(out.bytes())?;
        assert_eq!(
            doc.pointer("/mcpServers/s/url")
                .and_then(serde_json::Value::as_str),
            Some(expected)
        );
    }
    Ok(())
}

proptest::proptest! {
    #[test]
    fn generated_equivalent_copies_and_safe_negative_values(
        payload in "[a-zA-Z0-9_.$:{}-]{0,128}"
    ) {
        let secret = format!("fixture secret {payload}");
        let safe = format!("safe {payload}");
        let input = serde_json::json!({"providers":{"s":{"apiKey":secret}},"copies":[secret, {"copy":secret}],"unrelated":safe});
        let input = serde_json::to_vec(&input).map_err(|e| proptest::test_runner::TestCaseError::fail(e.to_string()))?;
        let out = body(&["models.json"], Profile::Config, &input).map_err(|e| proptest::test_runner::TestCaseError::fail(e.to_string()))?;
        let doc: serde_json::Value = serde_json::from_slice(out.bytes()).map_err(|e| proptest::test_runner::TestCaseError::fail(e.to_string()))?;
        proptest::prop_assert_eq!(doc.pointer("/providers/s/apiKey").and_then(serde_json::Value::as_str), Some("[redacted]"));
        proptest::prop_assert_eq!(doc.pointer("/copies/0").and_then(serde_json::Value::as_str), Some("[redacted]"));
        proptest::prop_assert_eq!(doc.pointer("/copies/1/copy").and_then(serde_json::Value::as_str), Some("[redacted]"));
        proptest::prop_assert_eq!(doc.get("unrelated").and_then(serde_json::Value::as_str), Some(safe.as_str()));
    }
}
