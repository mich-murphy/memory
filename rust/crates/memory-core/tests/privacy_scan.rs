//! `Profile::Scanned` whole-file scan (D54, brief 47). Secrets are synthetic and assembled
//! from parts so the repository's own secret scanning does not flag this file.
use memory_core::privacy::{
    self, Admission, Authorization, MAX_SCANNED_BYTES, PolicyContext, Profile, Reason,
    ResolvedSourceFacts, UnknownReason,
};
use proptest::prelude::*;

fn context() -> PolicyContext {
    PolicyContext {
        valid: true,
        revision: "fixture-v1".into(),
        rules: Some(privacy::rules_digest()),
        collector_paths: vec![],
        collector_identities: vec![],
    }
}

fn facts(profile: Profile) -> ResolvedSourceFacts {
    ResolvedSourceFacts {
        identities: vec!["/fh/0123456789abcdef@v1".into()],
        file_identity: Some(1),
        ancestor_identities: vec![2],
        path_resolved: true,
        root_resolved: true,
        owner: Some("fixture-owner".into()),
        profile,
    }
}

fn scan(input: &[u8]) -> Result<Vec<u8>, Reason> {
    let Admission::Allow(source) = privacy::admit(&context(), facts(Profile::Scanned)) else {
        return Err(Reason::UnknownKind);
    };
    let redacted = privacy::transform(&source, input)?;
    match privacy::authorize(&source, redacted, &context()) {
        Authorization::Allow(body) => Ok(body.bytes().to_vec()),
        _ => Err(Reason::ContextChanged),
    }
}

/// High-entropy literal for the generic fixtures (brief 50: gitleaks judges the value, not the
/// name alone). Built from parts so no literal secret is committed.
fn value() -> String {
    format!("{}{}{}", "Zk9Qx2Lm7P", "v4Rt8Yb3Nc6W", "d1Fe5Gh0Js")
}

fn hits() -> Vec<(&'static str, String)> {
    let up = "ABCDEFGHIJKLMNOP";
    let low = "abcdefghijklmnopqrstuvwxyz0123456789";
    vec![
        ("pem", format!("-----BEGIN {} PRIVATE KEY-----", "RSA")),
        (
            "openssh",
            format!("-----BEGIN {} PRIVATE KEY-----", "OPENSSH"),
        ),
        ("pgp", "-----BEGIN PGP PRIVATE KEY BLOCK-----".to_string()),
        ("aws", format!("{}{}", "AKIA", up)),
        ("aws-session", format!("{}{}", "ASIA", up)),
        ("gh-ghp", format!("{}{}", "ghp_", low)),
        ("gh-gho", format!("{}{}", "gho_", low)),
        ("gh-ghu", format!("{}{}", "ghu_", low)),
        ("gh-ghs", format!("{}{}", "ghs_", low)),
        ("gh-ghr", format!("{}{}", "ghr_", low)),
        ("gh-pat", format!("{}{}", "github_pat_", low)),
        ("slack", format!("{}{}", "xoxb-", "1234567890-abcdef")),
        (
            "anthropic",
            format!("{}{}", "sk-ant-", "abcdefghijklmnop12"),
        ),
        ("proj", format!("{}{}", "sk-proj-", "abcdefghijklmnop12")),
        ("openai", format!("{}{}", "sk-", low)),
        ("google", format!("{}{}", "AIza", "A".repeat(35))),
        ("stripe", format!("{}{}", "sk_live_", "abcdefghijklmnop12")),
        (
            "stripe-rk",
            format!("{}{}", "rk_live_", "abcdefghijklmnop12"),
        ),
        ("jwt", format!("{}.{}.sig", "eyJhbGciOiJ", "eyJzdWIiOiJ")),
        ("url", format!("postgres{}user:hunter22@host/db", "://")),
        // Brief 50: the generic fixtures carry a high-entropy value (gitleaks generic rule).
        ("shell", format!("export API_{}={}", "KEY", value())),
        ("generic-env", format!("DB_{}={}", "PASSWORD", value())),
        ("generic-yaml", format!("api_{}: {}", "token", value())),
        (
            "generic-json",
            format!("{{\"client_{}\": \"{}\"}}", "secret", value()),
        ),
        ("generic-toml", format!("auth_{} = '{}'", "key", value())),
        (
            "generic-code",
            format!("let x = {{ apiKey: \"{}\" }};", value()),
        ),
    ]
}

#[test]
fn every_pattern_is_withheld() {
    for (name, text) in hits() {
        let input = format!("before\n{text}\nafter\n");
        assert_eq!(
            scan(input.as_bytes()).err(),
            Some(Reason::SecretInText),
            "pattern {name} not withheld"
        );
    }
}

#[test]
fn near_misses_pass_byte_for_byte() {
    let misses = [
        "API_KEY=${API_KEY}",
        "token: <redacted>",
        "api_key: <your key here>",
        "password: short12",
        "password: 1234567",
        "sk-tooshort",
        "AKIA123",
        "ghp_short",
        "plain prose about a key and a token",
        "url: https://example.com/path",
        "-----BEGIN CERTIFICATE-----",
        "eyJhbGciOiJ only one segment",
    ];
    for text in misses {
        let input = format!("{text}\n");
        assert_eq!(
            scan(input.as_bytes()),
            Ok(input.clone().into_bytes()),
            "near miss {text:?} withheld"
        );
    }
}

/// Brief 50: the generic rule matches an assignment wherever it sits in a line, including
/// inside another assignment's value.
#[test]
fn an_assignment_nested_in_a_value_is_withheld() {
    for text in [
        format!("args: --{}={}", "token", value()),
        format!("url=https://h/p?api_{}={}", "key", value()),
        format!("opts = \"x client_{}: {}\"", "secret", value()),
    ] {
        let input = format!("{text}\n");
        assert_eq!(
            scan(input.as_bytes()).err(),
            Some(Reason::SecretInText),
            "nested assignment not withheld"
        );
    }
    let clean = "args: --verbose=enabled12\nurl=https://h/p?page=123456789\n";
    assert_eq!(scan(clean.as_bytes()), Ok(clean.as_bytes().to_vec()));
}

/// Brief 50: the shell guard and the name-only assignment rule no longer run in the scan, so
/// low-entropy and code-shaped values pass byte for byte (the 12 code lines of the synthetic set).
#[test]
fn code_shaped_lines_pass_byte_for_byte() {
    let lines = [
        "token = getToken()",
        "let key = lookup(name);",
        "const apiKey = process.env.API_KEY;",
        "password = os.environ[\"DB_PASSWORD\"]",
        "auth_token: Option<String>,",
        "secret = config.secret_name",
        "self.token = token_from_header(req)",
        "export DB_PASSWORD=abc12",
        "set -gx API_TOKEN literalvalue123",
        "headers[\"Authorization\"] = format!(\"Bearer {}\", tok)",
        "if token == expected_token { return true }",
        "client_secret = load_secret(path)",
    ];
    for text in lines {
        let input = format!("{text}\n");
        assert_eq!(
            scan(input.as_bytes()),
            Ok(input.clone().into_bytes()),
            "code line {text:?} withheld"
        );
    }
}

/// Brief 50: a secret that is only visible once JSON string escapes are undone.
#[test]
fn a_secret_inside_json_escapes_is_withheld() {
    let input = format!(
        "{{\"text\":\"line\\nDB_{}=\\\"{}\\\"\\n\"}}\n",
        "PASSWORD",
        value()
    );
    assert!(input.contains('\\'));
    assert_eq!(scan(input.as_bytes()).err(), Some(Reason::SecretInText));
    let clean = "{\"text\":\"a\\nb\\\"c\\\"\"}\n";
    assert_eq!(scan(clean.as_bytes()), Ok(clean.as_bytes().to_vec()));
}

#[test]
fn size_cap_is_exact() {
    let cap = usize::try_from(MAX_SCANNED_BYTES).unwrap_or(usize::MAX);
    assert_eq!(scan(&vec![b'a'; cap]).map(|b| b.len()), Ok(cap));
    assert_eq!(scan(&vec![b'a'; cap + 1]).err(), Some(Reason::SourceLimit));
}

#[test]
fn binary_and_invalid_utf8() {
    assert_eq!(scan(b"abc\0def").err(), Some(Reason::Binary));
    assert_eq!(scan(&[0x66, 0xff, 0xfe]).err(), Some(Reason::Binary));
    // A lone surrogate (U+D800) encoded as UTF-8 is not valid UTF-8.
    assert_eq!(scan(&[0xed, 0xa0, 0x80]).err(), Some(Reason::Binary));
    // Order: size beats binary.
    let big = vec![0_u8; usize::try_from(MAX_SCANNED_BYTES).unwrap_or(0) + 1];
    assert_eq!(scan(&big).err(), Some(Reason::SourceLimit));
}

#[test]
fn clean_file_passes_byte_for_byte() {
    let input = "fn main() {\n    println!(\"hello é\");\n}\r\n\ttrailing  \n".as_bytes();
    assert_eq!(scan(input), Ok(input.to_vec()));
    assert_eq!(scan(b""), Ok(Vec::new()));
}

#[test]
fn scanned_is_never_prefix_stable_and_needs_rules() {
    let admitted = privacy::admit(&context(), facts(Profile::Scanned));
    assert!(matches!(&admitted, Admission::Allow(s) if !s.prefix_stable()));
    let mut stale = context();
    stale.rules = None;
    assert!(matches!(
        privacy::admit(&stale, facts(Profile::Scanned)),
        Admission::Unknown(UnknownReason::RulesUnavailable)
    ));
    stale.rules = Some([7; 32]);
    assert!(matches!(
        privacy::admit(&stale, facts(Profile::Scanned)),
        Admission::Unknown(UnknownReason::RulesUnavailable)
    ));
}

#[test]
fn reason_spelling_is_closed() {
    assert_eq!(Reason::Binary.to_string(), "binary");
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(64))]

    #[test]
    fn inserted_secret_is_withheld(
        text in "[a-z \n]{0,400}",
        at in 0_usize..=400,
        which in 0_usize..26,
    ) {
        let all = hits();
        let Some((_, secret)) = all.get(which % all.len()) else {
            return Err(TestCaseError::fail("empty table"));
        };
        let mut at = at.min(text.len());
        while !text.is_char_boundary(at) {
            at -= 1;
        }
        let input = format!("{}\n{secret}\n{}", &text[..at], &text[at..]);
        prop_assert_eq!(scan(input.as_bytes()).err(), Some(Reason::SecretInText));
    }

    #[test]
    fn unpatterned_alphabet_passes_unchanged(text in "[a-z \n.,()]{0,600}") {
        prop_assert_eq!(scan(text.as_bytes()), Ok(text.clone().into_bytes()));
    }
}
