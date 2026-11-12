//! Generated pure privacy oracle, not a PUT/hint-sink or runtime property
//! (DESIGN.md 8.3, 15 property 5 and gate 6: Hegel property plus proptest cross-check).
//!
//! Documents are generated: family and filename, which secret-bearing positions exist,
//! the shape of the values placed there (plain, nested, URL, argv, non-string), noise of
//! generated shape and depth at safe positions, and malformed variants. The oracle is the
//! fail-closed contract, authored here and not selected by any production rule table:
//! no canary placed at a secret position survives, every safe value is preserved exactly,
//! and unparseable input is never published.
use hegel::{TestCase, generators as gs};
use memory_core::privacy::{
    self, Admission, Authorization, PolicyContext, Profile, Reason, ResolvedSourceFacts,
};
use proptest::prelude::*;
use serde_json::{Map, Value, json};

// ---------- fixtures shared by the generated and the named example tests ----------

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

#[derive(Debug, PartialEq, Eq)]
enum Withheld {
    Admission,
    Transform(Reason),
    Authorization,
}

fn publish(facts: ResolvedSourceFacts, bytes: &[u8]) -> Result<privacy::AuthorizedBody, Withheld> {
    let policy = context();
    let Admission::Allow(source) = privacy::admit(&policy, facts) else {
        return Err(Withheld::Admission);
    };
    let transformed = privacy::transform(&source, bytes).map_err(Withheld::Transform)?;
    match privacy::authorize(&source, transformed, &policy) {
        Authorization::Allow(body) => Ok(body),
        Authorization::Deny(_) | Authorization::Unknown(_) => Err(Withheld::Authorization),
    }
}

fn config_facts(name: &str, nonce: u64) -> ResolvedSourceFacts {
    facts(
        vec![
            "/fixture/alias.md".to_owned(),
            format!("/fixture/{name}"),
            "/fixture/final-blob".to_owned(),
        ],
        Profile::TextAsset,
        nonce,
    )
}

fn has(haystack: &[u8], needle: &str) -> bool {
    !needle.is_empty()
        && haystack
            .windows(needle.len())
            .any(|window| window == needle.as_bytes())
}

// ---------- the eleven protected names and their secret-bearing positions ----------
//
// Positions are written as dotted templates: `{k}` is a generated object key, `{i}` the
// index of a one-element array. They restate DESIGN 8.3 and the Go rule table; the
// production table is never consulted.

#[derive(Clone, Copy, PartialEq, Eq)]
enum Family {
    Claude,
    Settings,
    Mcp,
    Models,
    Codex,
}

const NAMES: [(&str, Family); 11] = [
    ("claude.json", Family::Claude),
    (".claude.json", Family::Claude),
    ("settings.json", Family::Settings),
    ("settings.local.json", Family::Settings),
    ("managed-settings.json", Family::Settings),
    (".mcp.json", Family::Mcp),
    ("mcp.json", Family::Mcp),
    ("managed-mcp.json", Family::Mcp),
    ("models.json", Family::Models),
    ("config.toml", Family::Codex),
    ("managed_config.toml", Family::Codex),
];

#[derive(Clone, Copy)]
enum Leaf {
    /// Everything but the empty string is withheld; references are not kept.
    Strict,
    /// Withheld unless it is an environment reference, number or boolean word.
    Plain,
    Url,
    Args,
}

const MCP_SLOTS: [(&str, Leaf); 5] = [
    ("mcpServers.{k}.env.{k}", Leaf::Plain),
    ("mcpServers.{k}.headers.{k}", Leaf::Plain),
    ("mcpServers.{k}.url", Leaf::Url),
    ("mcpServers.{k}.oauth.clientSecret", Leaf::Strict),
    ("mcpServers.{k}.args", Leaf::Args),
];

fn slots(family: Family) -> Vec<(String, Leaf)> {
    let own: Vec<(&str, Leaf)> = match family {
        Family::Claude => vec![
            ("primaryApiKey", Leaf::Strict),
            ("customApiKeyResponses.{k}", Leaf::Strict),
        ],
        Family::Settings => vec![
            ("env.{k}", Leaf::Plain),
            ("hooks.{k}.{i}.hooks.{i}.headers.{k}", Leaf::Plain),
        ],
        Family::Mcp => MCP_SLOTS.to_vec(),
        Family::Models => vec![
            ("providers.{k}.apiKey", Leaf::Plain),
            ("providers.{k}.headers.{k}", Leaf::Plain),
        ],
        Family::Codex => vec![
            ("mcp_servers.{k}.env.{k}", Leaf::Plain),
            ("mcp_servers.{k}.http_headers.{k}", Leaf::Plain),
            ("mcp_servers.{k}.url", Leaf::Url),
            ("mcp_servers.{k}.bearer_token", Leaf::Strict),
            ("mcp_servers.{k}.args", Leaf::Args),
            ("model_providers.{k}.http_headers.{k}", Leaf::Plain),
            (
                "model_providers.{k}.experimental_bearer_token",
                Leaf::Strict,
            ),
            ("model_providers.{k}.base_url", Leaf::Url),
            ("shell_environment_policy.set.{k}", Leaf::Plain),
            ("otel.{k}.{k}.headers.{k}", Leaf::Plain),
        ],
    };
    let mut all: Vec<(String, Leaf)> = own
        .into_iter()
        .map(|(path, leaf)| (path.to_owned(), leaf))
        .collect();
    if family == Family::Claude {
        for prefix in ["", "projects.{k}.", "project."] {
            all.extend(
                MCP_SLOTS
                    .iter()
                    .map(|(path, leaf)| (format!("{prefix}{path}"), *leaf)),
            );
        }
    }
    all
}

const KEYS: [&str; 6] = ["alpha", "beta", "gamma", "a.b", "\u{fc}n\u{ef}", "svc-1"];
const HOSTS: [&str; 3] = ["api.example.test", "mcp.internal.example", "h1.example.org"];
const REDACTED: &str = "[redacted]";

// ---------- generation helpers ----------

struct Cursor<'a> {
    bytes: &'a [u8],
    pos: usize,
}
impl Cursor<'_> {
    fn next(&mut self) -> u8 {
        let byte = self.bytes.get(self.pos % self.bytes.len().max(1)).copied();
        self.pos += 1;
        byte.unwrap_or(0)
    }
}

enum Want {
    Exact(Value),
    Contains(String),
}

struct Built {
    doc: Value,
    /// Secrets placed at secret-bearing positions: none may survive anywhere.
    canaries: Vec<String>,
    /// Safe or deterministic outcomes, by JSON pointer.
    wants: Vec<(String, Want)>,
    secret_positions: usize,
}

enum Seg {
    Key(String),
    Index,
}

fn segments(template: &str, cursor: &mut Cursor<'_>) -> Vec<Seg> {
    template
        .split('.')
        .map(|part| match part {
            "{k}" => Seg::Key(
                KEYS.get(usize::from(cursor.next()) % KEYS.len())
                    .map_or_else(String::new, |key| (*key).to_owned()),
            ),
            "{i}" => Seg::Index,
            literal => Seg::Key(literal.to_owned()),
        })
        .collect()
}

fn pointer(segs: &[Seg]) -> String {
    segs.iter()
        .map(|seg| match seg {
            Seg::Key(key) => format!("/{}", key.replace('~', "~0").replace('/', "~1")),
            Seg::Index => "/0".to_owned(),
        })
        .collect()
}

/// Place `leaf` at `segs`, creating containers. `None` on a shape conflict (not placed).
fn place(current: &mut Value, segs: &[Seg], leaf: Value) -> Option<()> {
    let (first, rest) = segs.split_first()?;
    let slot = match first {
        Seg::Key(key) => current
            .as_object_mut()?
            .entry(key.clone())
            .or_insert(Value::Null),
        Seg::Index => current.as_array_mut()?.get_mut(0)?,
    };
    match rest.first() {
        None if slot.is_null() => {
            *slot = leaf;
            Some(())
        }
        None => None,
        Some(next) => {
            if slot.is_null() {
                *slot = match next {
                    Seg::Key(_) => Value::Object(Map::new()),
                    Seg::Index => Value::Array(vec![Value::Null]),
                };
            }
            place(slot, rest, leaf)
        }
    }
}

fn canary(nonce: u64, index: usize) -> String {
    // Lowercase literal cannot masquerade as the intentionally retained environment
    // variable-name syntax of models.json.
    format!("hegel-private-value-{nonce:016x}-{index:04x}")
}

/// The value placed at a secret-bearing position, with the outcome it must have.
fn secret_leaf(
    leaf: Leaf,
    selector: u8,
    nonce: u64,
    serial: &mut usize,
    base: &str,
    built: &mut Built,
) -> Value {
    let mut fresh = |built: &mut Built| {
        *serial += 1;
        let c = canary(nonce, *serial);
        built.canaries.push(c.clone());
        c
    };
    let redacted = |built: &mut Built, at: &str| {
        built
            .wants
            .push((at.to_owned(), Want::Exact(json!(REDACTED))));
    };
    match (leaf, selector % 6) {
        (Leaf::Url, variant) => {
            let host = HOSTS
                .get(usize::from(selector) % HOSTS.len())
                .copied()
                .unwrap_or_default();
            match variant % 5 {
                0 => {
                    let c = fresh(built);
                    built
                        .wants
                        .push((base.to_owned(), Want::Contains(host.to_owned())));
                    json!(format!("https://svc:{c}@{host}/v1/mcp"))
                }
                1 => {
                    let c = fresh(built);
                    built
                        .wants
                        .push((base.to_owned(), Want::Contains(host.to_owned())));
                    json!(format!("https://{host}/v1?token={c}&mode=fast"))
                }
                2 => {
                    // Not a URL at all: withheld whole.
                    let c = fresh(built);
                    redacted(built, base);
                    json!(c)
                }
                3 => {
                    let url = format!("https://{host}/v1/mcp");
                    built.wants.push((base.to_owned(), Want::Exact(json!(url))));
                    json!(url)
                }
                _ => {
                    built
                        .wants
                        .push((base.to_owned(), Want::Exact(json!("${MCP_URL}"))));
                    json!("${MCP_URL}")
                }
            }
        }
        (Leaf::Args, variant) => {
            let c1 = fresh(built);
            let c2 = fresh(built);
            let (input, output): (Vec<String>, Vec<String>) = match variant {
                0 => (
                    vec!["--port".into(), "8080".into(), "--api-key".into(), c1],
                    vec![
                        "--port".into(),
                        "8080".into(),
                        "--api-key".into(),
                        REDACTED.into(),
                    ],
                ),
                1 => (
                    vec![
                        format!("--password={c1}"),
                        "-y".into(),
                        format!("--client-secret={c2}"),
                    ],
                    vec![
                        format!("--password={REDACTED}"),
                        "-y".into(),
                        format!("--client-secret={REDACTED}"),
                    ],
                ),
                2 => (
                    vec!["--name".into(), "n_alpha".into(), "--token".into(), c1],
                    vec![
                        "--name".into(),
                        "n_alpha".into(),
                        "--token".into(),
                        REDACTED.into(),
                    ],
                ),
                3 => (
                    vec!["@scope/pkg".into(), format!("--auth-token={c1}")],
                    vec!["@scope/pkg".into(), format!("--auth-token={REDACTED}")],
                ),
                5 => {
                    // A scalar where an argv array is expected is still scrubbed.
                    let scalar = format!("--password={c1}");
                    built.wants.push((
                        base.to_owned(),
                        Want::Exact(json!(format!("--password={REDACTED}"))),
                    ));
                    return json!(scalar);
                }
                _ => (
                    vec!["--secret".into(), c1, "--port".into(), "1".into()],
                    vec![
                        "--secret".into(),
                        REDACTED.into(),
                        "--port".into(),
                        "1".into(),
                    ],
                ),
            };
            for (index, expected) in output.iter().enumerate() {
                built
                    .wants
                    .push((format!("{base}/{index}"), Want::Exact(json!(expected))));
            }
            json!(input)
        }
        (Leaf::Strict | Leaf::Plain, 0) => {
            let c = fresh(built);
            redacted(built, base);
            json!(c)
        }
        (Leaf::Strict | Leaf::Plain, 1) => {
            let (c1, c2) = (fresh(built), fresh(built));
            redacted(built, &format!("{base}/n_a"));
            redacted(built, &format!("{base}/n_b/0"));
            redacted(built, &format!("{base}/n_b/1"));
            json!({"n_a": c1, "n_b": [c2, "plain-nested-text"]})
        }
        (Leaf::Strict | Leaf::Plain, 2) => {
            // Native non-strings are never retyped.
            built
                .wants
                .push((base.to_owned(), Want::Exact(json!(1234))));
            json!(1234)
        }
        (Leaf::Strict | Leaf::Plain, 3) => {
            let (c1, c2) = (fresh(built), fresh(built));
            redacted(built, &format!("{base}/0"));
            redacted(built, &format!("{base}/1"));
            json!([c1, c2])
        }
        (Leaf::Strict, _) => {
            let c = fresh(built);
            redacted(built, base);
            json!(c)
        }
        (Leaf::Plain, _) => {
            // An environment reference is not a secret literal.
            built
                .wants
                .push((base.to_owned(), Want::Exact(json!("${SOME_VAR}"))));
            json!("${SOME_VAR}")
        }
    }
}

fn noise_leaf(byte: u8, toml: bool) -> Value {
    match byte % 5 {
        0 => json!(format!("safe-text-{byte}")),
        1 => json!(i64::from(byte)),
        2 => json!(byte.is_multiple_of(2)),
        3 if toml => json!("toml-has-no-null"),
        3 => Value::Null,
        _ => json!(1.5),
    }
}

fn noise(cursor: &mut Cursor<'_>, depth: u8, toml: bool) -> Value {
    let byte = cursor.next();
    if depth == 0 {
        return noise_leaf(byte, toml);
    }
    match byte % 6 {
        2 => Value::Object(
            (0..=usize::from(byte / 6 % 3))
                .map(|index| (format!("n_{index}"), noise(cursor, depth - 1, toml)))
                .collect(),
        ),
        3 => Value::Array(
            (0..usize::from(byte / 6 % 3))
                .map(|_| json!(format!("array-text-{}", cursor.next())))
                .collect(),
        ),
        _ => noise_leaf(byte, toml),
    }
}

struct Params<'a> {
    nonce: u64,
    mask: u32,
    entropy: &'a [u8],
    depth: u8,
}

fn build(p: &Params<'_>, toml: bool, family: Family) -> Built {
    let mut cursor = Cursor {
        bytes: p.entropy,
        pos: 0,
    };
    let mut built = Built {
        doc: json!({"n_root": "root-marker"}),
        canaries: Vec::new(),
        wants: Vec::new(),
        secret_positions: 0,
    };
    let mut serial = 0usize;
    let mut plain_secrets = Vec::new();
    for (bit, (template, leaf)) in slots(family).into_iter().enumerate() {
        if p.mask >> (bit % 32) & 1 == 0 {
            continue;
        }
        let segs = segments(&template, &mut cursor);
        let base = pointer(&segs);
        let before = built.canaries.len();
        let selector = cursor.next();
        let value = secret_leaf(leaf, selector, p.nonce, &mut serial, &base, &mut built);
        let mut doc = std::mem::take(&mut built.doc);
        if place(&mut doc, &segs, value).is_some() {
            built.secret_positions += 1;
            if matches!(leaf, Leaf::Strict | Leaf::Plain) && selector.is_multiple_of(6) {
                plain_secrets.extend(built.canaries.get(before).cloned());
            }
        } else {
            // Conflicting generated keys: drop this position and its expectations.
            built.canaries.truncate(before);
            built.wants.retain(|(at, _)| !at.starts_with(&base));
        }
        built.doc = doc;
    }
    // Safe positions: generated noise of generated depth.
    let mut wrapped = noise(&mut cursor, p.depth % 6, toml);
    for _ in 0..p.depth % 4 {
        wrapped = json!({"n_chain": wrapped});
    }
    built
        .wants
        .push(("/n_noise".to_owned(), Want::Exact(wrapped.clone())));
    if let Some(object) = built.doc.as_object_mut() {
        object.insert("n_noise".to_owned(), wrapped);
        // A look-alike of a canary that was NOT placed at a secret position is safe text.
        let lookalike = canary(!p.nonce, 0xfff0);
        built
            .wants
            .push(("/n_lookalike".to_owned(), Want::Exact(json!(lookalike))));
        object.insert("n_lookalike".to_owned(), json!(lookalike));
        // An exact copy of a redacted plain secret is redacted wherever it appears.
        if let Some(copy) = plain_secrets.first() {
            object.insert("n_copy".to_owned(), json!(copy));
            built
                .wants
                .push(("/n_copy".to_owned(), Want::Exact(json!(REDACTED))));
        }
    }
    built
}

fn render(doc: &Value, toml: bool) -> Result<Vec<u8>, String> {
    if toml {
        let value = toml::Value::try_from(doc).map_err(|e| e.to_string())?;
        toml::to_string(&value)
            .map(String::into_bytes)
            .map_err(|e| e.to_string())
    } else {
        serde_json::to_vec(doc).map_err(|e| e.to_string())
    }
}

fn parse_output(bytes: &[u8], toml: bool) -> Result<Value, String> {
    if toml {
        let text = std::str::from_utf8(bytes).map_err(|e| e.to_string())?;
        let value: toml::Value = toml::from_str(text).map_err(|e| e.to_string())?;
        serde_json::to_value(value).map_err(|e| e.to_string())
    } else {
        serde_json::from_slice(bytes).map_err(|e| e.to_string())
    }
}

/// Unparseable variants. Every one must be withheld: never published, never raw.
fn malform(bytes: &[u8], toml: bool, kind: u8, cut: usize) -> Vec<u8> {
    let mut out = bytes.to_vec();
    if toml {
        match kind % 4 {
            0 => out.extend_from_slice(b"\n= 1\n"),
            // Top-level duplicate: simple values are emitted before any table header.
            1 => out = [b"n_root = \"again\"\n".as_slice(), &out].concat(),
            2 => out.push(0xff),
            _ => out.extend_from_slice(b"\nbad = \"unterminated\n"),
        }
        return out;
    }
    match kind % 8 {
        // Strictly shorter than a complete object is never a complete object.
        0 => out.truncate(cut % out.len().max(1)),
        1 => out.extend_from_slice(b" {}"),
        2 => {
            let tail = out.split_off(1.min(out.len()));
            out = b"{\"n_dup\":1,\"n_dup\":2,".to_vec();
            out.extend(tail);
        }
        3 => out = [b"[".as_slice(), &out, b"]"].concat(),
        4 => {
            let tail = out.split_off(1.min(out.len()));
            out = b"{\"n_bad\":\"\\ud800\",".to_vec();
            out.extend(tail);
        }
        5 => out.push(0xff),
        6 => {
            let deep = format!("{}1{}", "[".repeat(200), "]".repeat(200));
            let tail = out.split_off(1.min(out.len()));
            out = format!("{{\"n_deep\":{deep},").into_bytes();
            out.extend(tail);
        }
        _ => out = b"not json".to_vec(),
    }
    out
}

fn check_document(
    nonce: u64,
    name: u8,
    mask: u32,
    entropy: &[u8],
    depth: u8,
    malformed: Option<(u8, u16)>,
) -> Result<(), String> {
    let (file, family) = NAMES
        .get(usize::from(name) % NAMES.len())
        .copied()
        .ok_or("no name")?;
    let toml = family == Family::Codex;
    let params = Params {
        nonce,
        mask,
        entropy,
        depth,
    };
    let built = build(&params, toml, family);
    let input = render(&built.doc, toml)?;
    if let Some((kind, cut)) = malformed {
        let broken = malform(&input, toml, kind, usize::from(cut));
        return match publish(config_facts(file, nonce), &broken) {
            Err(Withheld::Transform(Reason::MalformedProtected)) => Ok(()),
            other => Err(format!(
                "{file}: malformed variant {kind} gave {:?}",
                other.map(|b| b.bytes().len())
            )),
        };
    }
    let body = publish(config_facts(file, nonce), &input)
        .map_err(|why| format!("{file}: well-formed document withheld: {why:?}"))?;
    let output = body.bytes();
    for secret in &built.canaries {
        if has(output, secret) {
            return Err(format!("{file}: canary {secret} survived"));
        }
    }
    if built.secret_positions == 0 && output != input {
        return Err(format!("{file}: document without secrets was rewritten"));
    }
    let parsed = parse_output(output, toml)?;
    for (at, want) in &built.wants {
        let found = parsed
            .pointer(at)
            .ok_or_else(|| format!("{file}: {at} missing from output"))?;
        match want {
            Want::Exact(expected) if found != expected => {
                return Err(format!("{file}: {at} is {found}, expected {expected}"));
            }
            Want::Contains(part) if !found.as_str().is_some_and(|s| s.contains(part.as_str())) => {
                return Err(format!("{file}: {at} lost safe part {part}"));
            }
            _ => {}
        }
    }
    Ok(())
}

// ---------- the property, in both engines ----------

#[hegel::test]
fn hegel_generated_documents_fail_closed(tc: TestCase) {
    let nonce: u64 = tc.draw(gs::integers::<u64>());
    let name: u8 = tc.draw(gs::integers::<u8>());
    let mask: u32 = tc.draw(gs::integers::<u32>());
    let entropy: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()).max_size(96));
    let depth: u8 = tc.draw(gs::integers::<u8>());
    let broken: bool = tc.draw(gs::booleans());
    let kind: u8 = tc.draw(gs::integers::<u8>());
    let cut: u16 = tc.draw(gs::integers::<u16>());
    let malformed = broken.then_some((kind, cut));
    assert_eq!(
        check_document(nonce, name, mask, &entropy, depth, malformed),
        Ok(())
    );
}

proptest! {
    #[test]
    fn proptest_generated_documents_fail_closed(
        nonce in any::<u64>(),
        name in any::<u8>(),
        mask in any::<u32>(),
        entropy in prop::collection::vec(any::<u8>(), 0..96),
        depth in any::<u8>(),
        malformed in prop::option::of((any::<u8>(), any::<u16>())),
    ) {
        prop_assert_eq!(check_document(nonce, name, mask, &entropy, depth, malformed), Ok(()));
    }
}

#[test]
fn every_protected_name_with_every_position_and_every_malformation() {
    assert_eq!(NAMES.len(), 11);
    for name in 0..11u8 {
        // A constant entropy byte picks one key, one host and one leaf variant for every
        // position, so twelve rounds cover every variant of every position kind.
        for selector in 0..12u8 {
            let entropy = vec![selector; 64];
            assert_eq!(
                check_document(
                    u64::from(name) << 8 | u64::from(selector),
                    name,
                    u32::MAX,
                    &entropy,
                    selector + 3,
                    None
                ),
                Ok(())
            );
        }
        let entropy: Vec<u8> = (0..=255).collect();
        for kind in 0..8u8 {
            assert_eq!(
                check_document(9, name, u32::MAX, &entropy, 2, Some((kind, 40))),
                Ok(())
            );
        }
        // No secret position at all: published byte-identical.
        assert_eq!(check_document(5, name, 0, &entropy, 4, None), Ok(()));
    }
}

// ---------- named examples: policy controls outside the structural scrub ----------

#[test]
fn credential_identities_through_aliases_and_backup_suffixes_are_denied() {
    for credential in [
        "auth.json.bak.old~",
        "config.env-20260412-235959.gz",
        ".env.local.before-update.bak",
        "id_ed25519.agent-sessions-backup-20261014-101010",
        ".ssh/aliased-file",
    ] {
        let observed = facts(
            vec![
                "/fixture/alias.md".to_owned(),
                format!("/fixture/{credential}"),
                "/fixture/final-blob".to_owned(),
            ],
            Profile::TextAsset,
            1,
        );
        assert!(
            matches!(
                privacy::admit(&context(), observed),
                Admission::Deny(Reason::Credential)
            ),
            "{credential}"
        );
    }
}

#[test]
fn unknown_kind_has_no_raw_route() {
    let observed = facts(
        vec!["/fixture/unknown-kind".to_owned()],
        Profile::Unknown,
        2,
    );
    assert!(matches!(
        privacy::admit(&context(), observed),
        Admission::Deny(Reason::UnknownKind)
    ));
}

#[test]
fn transcripts_and_other_known_sources_are_not_scrubbed() {
    // Go does not scrub transcripts either (DESIGN 8.3): invalid UTF-8 and secret-looking
    // text pass byte-identical, so a rewrite here would be a behaviour change.
    let mut transcript = b"transcript API_KEY=ordinary-literal\npartial {".to_vec();
    transcript.push(255);
    let body = publish(
        facts(
            vec!["/fixture/session.jsonl".to_owned()],
            Profile::Transcript,
            3,
        ),
        &transcript,
    );
    assert_eq!(body.map(|b| b.bytes().to_vec()).ok(), Some(transcript));
    let known = b"known safe raw asset; ordinary literal text";
    let body = publish(
        facts(
            vec!["/fixture/known.data".to_owned()],
            Profile::OtherKnown,
            4,
        ),
        known,
    );
    assert_eq!(body.map(|b| b.bytes().to_vec()).ok(), Some(known.to_vec()));
}

// ---------- named regressions found by the mutation baseline ----------

fn publish_config(name: &str, doc: &str) -> Result<Vec<u8>, Withheld> {
    publish(config_facts(name, 1), doc.as_bytes()).map(|body| body.bytes().to_vec())
}

fn admitted(path: &str, profile: Profile) -> Result<privacy::AdmittedSource, Withheld> {
    match privacy::admit(&context(), facts(vec![path.to_owned()], profile, 1)) {
        Admission::Allow(source) => Ok(source),
        _ => Err(Withheld::Admission),
    }
}

#[test]
fn only_unrecognized_non_text_sources_are_prefix_stable() -> Result<(), Withheld> {
    // Mutants: `prefix_stable` constant true/false, `&&` -> `||`, either `!` deleted. A shell
    // that transforms a delta alone for a protected document would leak what whole-document
    // scrubbing removes.
    assert!(admitted("/f/session.jsonl", Profile::Transcript)?.prefix_stable());
    assert!(admitted("/f/known.data", Profile::OtherKnown)?.prefix_stable());
    assert!(!admitted("/f/settings.json", Profile::OtherKnown)?.prefix_stable());
    assert!(!admitted("/f/session.jsonl", Profile::TextAsset)?.prefix_stable());
    assert!(!admitted("/f/session.jsonl", Profile::Config)?.prefix_stable());
    assert!(!admitted("/f/config.toml", Profile::Config)?.prefix_stable());
    Ok(())
}

#[test]
fn protected_document_limit_is_exactly_eight_mib() {
    // Mutant: `bytes.len() > MAX_PROTECTED_BYTES` -> `>=`. The limit is on the raw input and
    // never falls back to publishing raw bytes.
    let max = privacy::MAX_PROTECTED_BYTES;
    let frame = r#"{"n_pad":""#.len() + r#""}"#.len();
    let doc = |len: usize| format!(r#"{{"n_pad":"{}"}}"#, "x".repeat(len - frame));
    let at_limit = doc(max);
    assert_eq!(at_limit.len(), max);
    assert_eq!(
        publish_config("settings.json", &at_limit).map(|b| b.len()),
        Ok(max)
    );
    assert_eq!(
        publish_config("settings.json", &doc(max + 1)),
        Err(Withheld::Transform(Reason::SourceLimit))
    );
}

#[test]
fn url_with_a_malformed_percent_escape_is_withheld_whole() {
    // Mutant: `valid_escapes` accepts an escape when only one of its two digits is hex.
    let url = |value: &str| {
        let doc = format!(r#"{{"mcpServers":{{"a":{{"url":"{value}"}}}}}}"#);
        let out = publish_config("mcp.json", &doc).map_err(|why| format!("{why:?}"))?;
        let parsed: Value = serde_json::from_slice(&out).map_err(|e| e.to_string())?;
        parsed
            .pointer("/mcpServers/a/url")
            .and_then(Value::as_str)
            .map(str::to_owned)
            .ok_or_else(|| "no url".to_owned())
    };
    assert_eq!(
        url("https://h.example/a%41b"),
        Ok("https://h.example/a%41b".to_owned())
    );
    for bad in [
        "https://h.example/a%4zb",
        "https://h.example/a%z4b",
        "https://h.example/a%zzb",
        "https://h.example/a%4",
    ] {
        assert_eq!(url(bad), Ok("[redacted]".to_owned()), "{bad}");
    }
}
