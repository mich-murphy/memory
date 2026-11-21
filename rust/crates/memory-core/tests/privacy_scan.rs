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
        ("shell", format!("export API_{}=literalvalue123", "KEY")),
        ("generic-env", format!("DB_{}=literalvalue123", "PASSWORD")),
        ("generic-yaml", format!("api_{}: literalvalue123", "token")),
        (
            "generic-json",
            format!("{{\"client_{}\": \"literalvalue123\"}}", "secret"),
        ),
        (
            "generic-toml",
            format!("auth_{} = 'literalvalue123'", "key"),
        ),
        (
            "generic-code",
            format!(
                "let x = {{ apiKey: \"literalvalue123\" }}; // {}",
                "apikey: literalvalue123"
            ),
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
