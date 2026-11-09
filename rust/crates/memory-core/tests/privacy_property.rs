//! Generated pure privacy oracle, not a PUT/hint-sink or runtime property.
//! Fixtures are independently authored: no production rule table selects expected fields.
use hegel::{TestCase, generators as gs};
use memory_core::privacy::{
    self, Admission, Authorization, PolicyContext, Profile, Reason, ResolvedSourceFacts,
};

// Eleven protected names plus credential alias, unknown kind, launch, transcript
// and safe-known-file controls. One selected case per Hegel example bounds cost.
const CASE_COUNT: usize = 16;
const CONFIGS: &[(&str, &str)] = &[
    (
        "claude.json",
        r#"{"primaryApiKey":"CANARY","copy":"CANARY","safe_marker":"SAFE"}"#,
    ),
    (
        ".claude.json",
        r#"{"primaryApiKey":"CANARY","copy":"CANARY","safe_marker":"SAFE"}"#,
    ),
    (
        "settings.json",
        r#"{"env":{"API_KEY":"CANARY"},"copy":"CANARY","safe_marker":"SAFE"}"#,
    ),
    (
        "settings.local.json",
        r#"{"env":{"API_KEY":"CANARY"},"copy":"CANARY","safe_marker":"SAFE"}"#,
    ),
    (
        "managed-settings.json",
        r#"{"env":{"API_KEY":"CANARY"},"copy":"CANARY","safe_marker":"SAFE"}"#,
    ),
    (
        ".mcp.json",
        r#"{"mcpServers":{"fixture":{"headers":{"Authorization":"CANARY"}}},"copy":"CANARY","safe_marker":"SAFE"}"#,
    ),
    (
        "mcp.json",
        r#"{"mcpServers":{"fixture":{"env":{"API_KEY":"CANARY"}}},"copy":"CANARY","safe_marker":"SAFE"}"#,
    ),
    (
        "managed-mcp.json",
        r#"{"mcpServers":{"fixture":{"oauth":{"clientSecret":"CANARY"}}},"copy":"CANARY","safe_marker":"SAFE"}"#,
    ),
    (
        "models.json",
        r#"{"providers":{"fixture":{"apiKey":"CANARY"}},"copy":"CANARY","safe_marker":"SAFE"}"#,
    ),
    (
        "config.toml",
        "copy='CANARY'\nsafe_marker='SAFE'\n[mcp_servers.fixture]\nbearer_token='CANARY'\n",
    ),
    (
        "managed_config.toml",
        "copy='CANARY'\nsafe_marker='SAFE'\n[mcp_servers.fixture.env]\nAPI_KEY='CANARY'\n",
    ),
];

fn context() -> PolicyContext {
    PolicyContext {
        valid: true,
        revision: "hegel-fixture-v1".to_owned(),
        rules: Some(privacy::rules_digest()),
        collector_paths: vec!["/fixture/collector".to_owned()],
        collector_identities: vec![99],
    }
}

fn facts(paths: Vec<String>, profile: Profile, nonce: u64) -> ResolvedSourceFacts {
    ResolvedSourceFacts {
        identities: paths,
        file_identity: Some(u128::from(nonce) + 100),
        ancestor_identities: vec![1, 2],
        path_resolved: true,
        root_resolved: true,
        owner: Some(format!("fixture-owner-{nonce:016x}")),
        profile,
    }
}

fn publish(
    facts: ResolvedSourceFacts,
    bytes: &[u8],
) -> Result<privacy::AuthorizedBody, &'static str> {
    let policy = context();
    let Admission::Allow(source) = privacy::admit(&policy, facts) else {
        return Err("expected fixture admission");
    };
    let transformed = privacy::transform(&source, bytes).map_err(|_| "fixture transform failed")?;
    match privacy::authorize(source, transformed, &policy) {
        Authorization::Allow(body) => Ok(body),
        Authorization::Deny(_) | Authorization::Unknown(_) => Err("fixture authorization failed"),
    }
}

fn check_case(case: usize, nonce: u64, salt: u64) -> Result<(), Box<dyn std::error::Error>> {
    // Lowercase literal cannot masquerade as the intentionally retained Pi env
    // variable-name reference syntax. Safe metadata is independently generated.
    let canary = format!("hegel-private-value-{nonce:016x}-{salt:016x}");
    let safe = format!("safe-model-{salt:016x}");
    if let Some((name, template)) = CONFIGS.get(case) {
        let input = template.replace("CANARY", &canary).replace("SAFE", &safe);
        let path = format!("/fixture/{name}");
        let body = publish(
            facts(
                vec![
                    "/fixture/alias.md".to_owned(),
                    path,
                    "/fixture/final-blob".to_owned(),
                ],
                Profile::TextAsset,
                nonce,
            ),
            input.as_bytes(),
        )?;
        assert!(
            !body
                .bytes()
                .windows(canary.len())
                .any(|w| w == canary.as_bytes())
        );
        if name.ends_with(".toml") {
            let doc: toml::Value = toml::from_str(std::str::from_utf8(body.bytes())?)?;
            assert_eq!(
                doc.get("safe_marker").and_then(toml::Value::as_str),
                Some(safe.as_str())
            );
            assert_eq!(
                doc.get("copy").and_then(toml::Value::as_str),
                Some("[redacted]")
            );
        } else {
            let doc: serde_json::Value = serde_json::from_slice(body.bytes())?;
            assert_eq!(
                doc.get("safe_marker").and_then(serde_json::Value::as_str),
                Some(safe.as_str())
            );
            assert_eq!(
                doc.get("copy").and_then(serde_json::Value::as_str),
                Some("[redacted]")
            );
        }
        return Ok(());
    }
    match case {
        11 => {
            let credential = match salt % 5 {
                0 => "auth.json.bak.old~",
                1 => "config.env-20260412-235959.gz",
                2 => ".env.local.before-update.bak",
                3 => "id_ed25519.agent-sessions-backup-20261014-101010",
                _ => ".ssh/aliased-file",
            };
            let observed = facts(
                vec![
                    "/fixture/alias.md".to_owned(),
                    format!("/fixture/{credential}"),
                    "/fixture/final-blob".to_owned(),
                ],
                Profile::TextAsset,
                nonce,
            );
            assert!(matches!(
                privacy::admit(&context(), observed),
                Admission::Deny(Reason::Credential)
            ));
        }
        12 => {
            let observed = facts(
                vec![format!("/fixture/unknown-{salt:016x}")],
                Profile::Unknown,
                nonce,
            );
            assert!(matches!(
                privacy::admit(&context(), observed),
                Admission::Deny(Reason::UnknownKind)
            ));
        }
        13 => {
            let argv = vec![
                "/bin/claude".to_owned(),
                "--model".to_owned(),
                safe.clone(),
                "--prompt".to_owned(),
                canary.clone(),
                "--api-key".to_owned(),
                canary.clone(),
                canary.clone(),
            ];
            let env = vec![
                format!("ANTHROPIC_API_KEY={canary}"),
                format!("UNKNOWN={canary}"),
                format!("ANTHROPIC_MODEL={safe}"),
            ];
            let launch = privacy::sanitize_launch(&argv, &env);
            assert!(launch.argv().iter().all(|arg| !arg.contains(&canary)));
            assert!(launch.env().values().all(|value| !value.contains(&canary)));
            assert!(launch.argv().iter().any(|arg| arg == &safe));
            assert_eq!(launch.env().get("ANTHROPIC_MODEL"), Some(&safe));
        }
        14 => {
            let mut input = format!("transcript API_KEY={canary}\npartial {{").into_bytes();
            input.push(255);
            let body = publish(
                facts(
                    vec!["/fixture/session.jsonl".to_owned()],
                    Profile::Transcript,
                    nonce,
                ),
                &input,
            )?;
            assert_eq!(body.bytes(), input);
        }
        15 => {
            let input = format!("known safe raw asset {safe}; ordinary literal {canary}");
            let body = publish(
                facts(
                    vec!["/fixture/known.data".to_owned()],
                    Profile::OtherKnown,
                    nonce,
                ),
                input.as_bytes(),
            )?;
            assert_eq!(body.bytes(), input.as_bytes());
        }
        _ => return Err("fixture case outside independent inventory".into()),
    }
    Ok(())
}

#[hegel::test]
fn hegel_generated_canaries_do_not_escape_pure_privacy_outputs(tc: TestCase) {
    let selector: u8 = tc.draw(gs::integers::<u8>());
    let nonce: u64 = tc.draw(gs::integers::<u64>());
    let salt: u64 = tc.draw(gs::integers::<u64>());
    assert!(check_case(usize::from(selector) % CASE_COUNT, nonce, salt).is_ok());
}

#[test]
fn independent_inventory_exercises_every_family_and_credential_variant()
-> Result<(), Box<dyn std::error::Error>> {
    assert_eq!(CONFIGS.len(), 11);
    for case in 0..CASE_COUNT {
        for salt in 0..5 {
            check_case(case, 123, salt)?;
        }
    }
    Ok(())
}
