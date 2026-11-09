use super::{Reason, names};
use serde::Deserializer;
use serde::de::{self, MapAccess, SeqAccess, Visitor};
use serde_json::value::RawValue;
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::fmt;

// Typed executable rules are also the canonical digest input. There is no second
// textual specification or permissive unknown-mode parser to drift from them.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Family {
    Claude,
    Settings,
    Mcp,
    Models,
    Codex,
}
#[derive(Clone, Copy)]
enum Mode {
    Nothing,
    References,
    Environment,
    Commands,
    Url,
    Args,
}
impl Mode {
    fn code(self) -> u8 {
        match self {
            Self::Nothing => b'N',
            Self::References => b'R',
            Self::Environment => b'E',
            Self::Commands => b'C',
            Self::Url => b'U',
            Self::Args => b'A',
        }
    }
}
const CONFIGS: &[(&[&str], Family)] = &[
    (&["claude.json", ".claude.json"], Family::Claude),
    (
        &[
            "settings.json",
            "settings.local.json",
            "managed-settings.json",
        ],
        Family::Settings,
    ),
    (&[".mcp.json", "mcp.json", "managed-mcp.json"], Family::Mcp),
    (&["models.json"], Family::Models),
    (&["config.toml", "managed_config.toml"], Family::Codex),
];
const CLAUDE: &[(&str, Mode)] = &[
    ("primaryApiKey", Mode::Nothing),
    ("customApiKeyResponses.*", Mode::Nothing),
];
const SETTINGS: &[(&str, Mode)] = &[
    ("env.*", Mode::References),
    ("hooks.*.*.hooks.*.headers.*", Mode::References),
];
const MCP: &[(&str, Mode)] = &[
    ("mcpServers.*.env.*", Mode::References),
    ("mcpServers.*.headers.*", Mode::References),
    ("mcpServers.*.url", Mode::Url),
    ("mcpServers.*.oauth.clientSecret", Mode::Nothing),
    ("mcpServers.*.args", Mode::Args),
];
const MODELS: &[(&str, Mode)] = &[
    ("providers.*.apiKey", Mode::Environment),
    ("providers.*.headers.*", Mode::Commands),
];
const CODEX: &[(&str, Mode)] = &[
    ("mcp_servers.*.env.*", Mode::References),
    ("mcp_servers.*.http_headers.*", Mode::References),
    ("mcp_servers.*.url", Mode::Url),
    ("mcp_servers.*.bearer_token", Mode::Nothing),
    ("mcp_servers.*.args", Mode::Args),
    ("model_providers.*.http_headers.*", Mode::References),
    ("model_providers.*.experimental_bearer_token", Mode::Nothing),
    ("model_providers.*.base_url", Mode::Url),
    ("shell_environment_policy.set.*", Mode::References),
    ("otel.*.*.headers.*", Mode::References),
];
fn kind(name: &str) -> Option<Family> {
    let base = names::basename(name);
    CONFIGS
        .iter()
        .find_map(|(names, family)| names.contains(&base.as_str()).then_some(*family))
}
pub(super) fn recognizes(name: &str) -> bool {
    kind(name).is_some()
}
fn rules(family: Family) -> Vec<(Vec<String>, Mode)> {
    let base = match family {
        Family::Claude => CLAUDE,
        Family::Settings => SETTINGS,
        Family::Mcp => MCP,
        Family::Models => MODELS,
        Family::Codex => CODEX,
    };
    let mut rules: Vec<_> = base
        .iter()
        .map(|(path, mode)| (path.split('.').map(str::to_owned).collect(), *mode))
        .collect();
    if family == Family::Claude {
        for prefix in ["", "projects.*.", "project."] {
            rules.extend(MCP.iter().map(|(path, mode)| {
                (
                    format!("{prefix}{path}")
                        .split('.')
                        .map(str::to_owned)
                        .collect(),
                    *mode,
                )
            }));
        }
    }
    rules
}
pub(super) fn digest_rules(hash: &mut Sha256) {
    for (names, family) in CONFIGS {
        for name in *names {
            hash.update(name.as_bytes());
            hash.update(b"\n");
            for (pattern, mode) in rules(*family) {
                hash.update(pattern.join(".").as_bytes());
                hash.update([0, mode.code(), b'\n']);
            }
        }
    }
}

// Raw numeric lexemes retain arbitrary precision like Go json.Number. The
// standard parser validates syntax; our recursive map visitor rejects decoded
// duplicate keys and our string decode rejects invalid Unicode. No reserved
// serde_json arbitrary-precision object key can impersonate a number.
#[derive(serde::Serialize)]
#[serde(untagged)]
enum Value {
    Object(BTreeMap<String, Value>),
    Array(Vec<Value>),
    String(String),
    Number(Box<RawValue>),
    Bool(bool),
    Null,
}
impl Value {
    fn is_object(&self) -> bool {
        matches!(self, Self::Object(_))
    }
    fn as_str(&self) -> Option<&str> {
        if let Self::String(s) = self {
            Some(s)
        } else {
            None
        }
    }
    fn as_array(&self) -> Option<&Vec<Self>> {
        if let Self::Array(v) = self {
            Some(v)
        } else {
            None
        }
    }
    fn get(&self, key: &str) -> Option<&Self> {
        if let Self::Object(map) = self {
            map.get(key)
        } else {
            None
        }
    }
}
impl From<String> for Value {
    fn from(s: String) -> Self {
        Self::String(s)
    }
}
fn parse(bytes: &[u8], depth: usize) -> Result<Value, serde_json::Error> {
    if depth > 128 {
        return Err(de::Error::custom("depth limit"));
    }
    let raw: Box<RawValue> = serde_json::from_slice(bytes)?;
    match raw.get().as_bytes().first() {
        Some(b'{') => {
            struct ObjectVisitor(usize);
            impl<'de> Visitor<'de> for ObjectVisitor {
                type Value = Value;
                fn expecting(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                    f.write_str("object")
                }
                fn visit_map<A: MapAccess<'de>>(self, mut map: A) -> Result<Value, A::Error> {
                    let mut values = BTreeMap::new();
                    while let Some((key, raw)) = map.next_entry::<String, Box<RawValue>>()? {
                        let value =
                            parse(raw.get().as_bytes(), self.0 + 1).map_err(de::Error::custom)?;
                        if values.insert(key, value).is_some() {
                            return Err(de::Error::custom("duplicate key"));
                        }
                    }
                    Ok(Value::Object(values))
                }
            }
            serde_json::Deserializer::from_str(raw.get()).deserialize_map(ObjectVisitor(depth))
        }
        Some(b'[') => {
            struct ArrayVisitor(usize);
            impl<'de> Visitor<'de> for ArrayVisitor {
                type Value = Value;
                fn expecting(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                    f.write_str("array")
                }
                fn visit_seq<A: SeqAccess<'de>>(self, mut seq: A) -> Result<Value, A::Error> {
                    let mut values = Vec::new();
                    while let Some(raw) = seq.next_element::<Box<RawValue>>()? {
                        values.push(
                            parse(raw.get().as_bytes(), self.0 + 1).map_err(de::Error::custom)?,
                        );
                    }
                    Ok(Value::Array(values))
                }
            }
            serde_json::Deserializer::from_str(raw.get()).deserialize_seq(ArrayVisitor(depth))
        }
        Some(b'"') => serde_json::from_str(raw.get()).map(Value::String),
        Some(b'n') => Ok(Value::Null),
        Some(b't' | b'f') => serde_json::from_str(raw.get()).map(Value::Bool),
        _ => Ok(Value::Number(raw)),
    }
}
fn from_toml(value: &toml::Value, depth: usize) -> Result<Value, Reason> {
    if depth > 128 {
        return Err(Reason::MalformedProtected);
    }
    Ok(match value {
        toml::Value::String(s) => Value::String(s.clone()),
        toml::Value::Table(map) => Value::Object(
            map.iter()
                .map(|(k, v)| Ok((k.clone(), from_toml(v, depth + 1)?)))
                .collect::<Result<_, Reason>>()?,
        ),
        toml::Value::Array(array) => Value::Array(
            array
                .iter()
                .map(|v| from_toml(v, depth + 1))
                .collect::<Result<_, _>>()?,
        ),
        // Native nonstrings remain in original_toml, never transformed/retyped.
        toml::Value::Datetime(_)
        | toml::Value::Integer(_)
        | toml::Value::Float(_)
        | toml::Value::Boolean(_) => Value::Null,
    })
}

pub(super) fn scrub(name: &str, bytes: &[u8]) -> Result<Vec<u8>, Reason> {
    let Some(k) = kind(name) else {
        return Err(Reason::ScrubFailed);
    };
    let mut original_toml = None;
    let mut doc = if k == Family::Codex {
        let source = std::str::from_utf8(bytes).map_err(|_| Reason::MalformedProtected)?;
        let parsed: toml::Value = toml::from_str(source).map_err(|_| Reason::MalformedProtected)?;
        let value = from_toml(&parsed, 0)?;
        original_toml = Some(parsed);
        value
    } else {
        parse(bytes, 0).map_err(|_| Reason::MalformedProtected)?
    };
    if !doc.is_object() {
        return Err(Reason::MalformedProtected);
    }
    let mut replacements = BTreeMap::new();
    walk(&mut doc, &mut Vec::new(), &rules(k), &mut replacements);
    if replacements.is_empty() {
        return Ok(bytes.to_vec());
    }
    copies(&mut doc, &replacements);
    if expansion_bound(&doc, 0).is_none_or(|n| n > super::MAX_PROTECTED_BYTES) {
        return Err(Reason::SourceLimit);
    }
    let result = if k == Family::Codex {
        let mut original = original_toml.ok_or(Reason::ScrubFailed)?;
        restore_toml_strings(&mut original, &doc);
        toml::to_string(&original)
            .map(String::into_bytes)
            .map_err(|_| Reason::ScrubFailed)
    } else {
        serde_json::to_vec(&doc).map_err(|_| Reason::ScrubFailed)
    }?;
    if result.len() > super::MAX_PROTECTED_BYTES {
        return Err(Reason::SourceLimit);
    }
    Ok(result)
}

fn expansion_bound(value: &Value, ancestors: usize) -> Option<usize> {
    let mut total = 32usize.checked_add(ancestors)?;
    match value {
        Value::String(s) => total = total.checked_add(s.len().checked_mul(6)?)?,
        Value::Object(map) => {
            for (key, value) in map {
                let path = ancestors
                    .checked_add(key.len().checked_mul(6)?)?
                    .checked_add(8)?;
                total = total
                    .checked_add(path)?
                    .checked_add(expansion_bound(value, path)?)?;
            }
        }
        Value::Array(values) => {
            for value in values {
                total = total.checked_add(expansion_bound(value, ancestors)?)?;
            }
        }
        Value::Number(n) => total = total.checked_add(n.get().len())?,
        Value::Null | Value::Bool(_) => {}
    }
    Some(total)
}

// Only replace string leaves in the original TOML tree: dates/times, integers,
// floats and table/array structure retain their original native types.
fn restore_toml_strings(original: &mut toml::Value, scrubbed: &Value) {
    match original {
        toml::Value::String(value) => {
            if let Some(s) = scrubbed.as_str() {
                *value = s.to_owned();
            }
        }
        toml::Value::Table(map) => {
            for (key, value) in map {
                if let Some(s) = scrubbed.get(key) {
                    restore_toml_strings(value, s);
                }
            }
        }
        toml::Value::Array(values) => {
            if let Some(scrubbed) = scrubbed.as_array() {
                for (value, s) in values.iter_mut().zip(scrubbed) {
                    restore_toml_strings(value, s);
                }
            }
        }
        toml::Value::Datetime(_)
        | toml::Value::Integer(_)
        | toml::Value::Float(_)
        | toml::Value::Boolean(_) => {}
    }
}

fn walk(
    value: &mut Value,
    path: &mut Vec<String>,
    rules: &[(Vec<String>, Mode)],
    replacements: &mut BTreeMap<String, String>,
) {
    if let Some((_, mode)) = rules.iter().find(|(pattern, _)| {
        pattern.len() == path.len()
            && pattern
                .iter()
                .zip(path.iter())
                .all(|(a, b)| a == "*" || a == b)
    }) {
        redact_all(value, *mode, replacements);
        return;
    }
    match value {
        Value::Object(map) => {
            for (key, value) in map {
                path.push(key.clone());
                walk(value, path, rules, replacements);
                path.pop();
            }
        }
        Value::Array(values) => {
            for (index, value) in values.iter_mut().enumerate() {
                path.push(index.to_string());
                walk(value, path, rules, replacements);
                path.pop();
            }
        }
        Value::Null | Value::Bool(_) | Value::Number(_) | Value::String(_) => {}
    }
}

fn redact_all(value: &mut Value, mode: Mode, replacements: &mut BTreeMap<String, String>) {
    if matches!(mode, Mode::Args)
        && let Value::Array(values) = value
    {
        let args: Vec<String> = values
            .iter()
            .map(|v| v.as_str().unwrap_or_default().to_owned())
            .collect();
        for (value, replacement) in values.iter_mut().zip(args_scrub(&args)) {
            if let Some(old) = value.as_str()
                && old != replacement
            {
                replacements.insert(old.to_owned(), replacement.clone());
                *value = replacement.into();
            }
        }
        return;
    }
    match value {
        Value::String(old) => {
            let replacement = redact(old, mode);
            if *old != replacement {
                replacements.insert(old.clone(), replacement.clone());
                *old = replacement;
            }
        }
        Value::Object(map) => {
            for value in map.values_mut() {
                redact_all(value, mode, replacements);
            }
        }
        Value::Array(values) => {
            for value in values {
                redact_all(value, mode, replacements);
            }
        }
        Value::Null | Value::Bool(_) | Value::Number(_) => {}
    }
}
fn copies(value: &mut Value, replacements: &BTreeMap<String, String>) {
    match value {
        Value::String(s) => {
            if let Some(replacement) = replacements.get(s) {
                *s = replacement.clone();
            }
        }
        Value::Object(map) => {
            for value in map.values_mut() {
                copies(value, replacements);
            }
        }
        Value::Array(values) => {
            for value in values {
                copies(value, replacements);
            }
        }
        Value::Null | Value::Bool(_) | Value::Number(_) => {}
    }
}
fn redact(value: &str, mode: Mode) -> String {
    let keep = match mode {
        Mode::Nothing => value.is_empty(),
        Mode::References => names::reference(value),
        Mode::Environment => {
            names::reference(value)
                || value.starts_with('!')
                || names::matches(r"^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$", value)
        }
        Mode::Commands => names::reference(value) || value.starts_with('!'),
        Mode::Url => return url(value),
        // Go keepArgs applies the same flag/inline-document scrub to scalar
        // leaves; positional strings are not a general-purpose DLP target.
        Mode::Args => return args_scrub(&[value.to_owned()]).pop().unwrap_or_default(),
    };
    if keep {
        value.to_owned()
    } else {
        "[redacted]".to_owned()
    }
}

fn percent_decode(value: &str) -> Option<String> {
    let mut bytes = Vec::new();
    let mut input = value.bytes();
    while let Some(byte) = input.next() {
        if byte == b'%' {
            let high = char::from(input.next()?).to_digit(16)?;
            let low = char::from(input.next()?).to_digit(16)?;
            bytes.push(u8::try_from(high * 16 + low).ok()?);
        } else {
            bytes.push(if byte == b'+' { b' ' } else { byte });
        }
    }
    String::from_utf8(bytes).ok()
}
fn valid_escapes(value: &str) -> bool {
    let mut chars = value.chars();
    while let Some(c) = chars.next() {
        if c == '%'
            && !(chars.next().is_some_and(|c| c.is_ascii_hexdigit())
                && chars.next().is_some_and(|c| c.is_ascii_hexdigit()))
        {
            return false;
        }
    }
    true
}
fn url(value: &str) -> String {
    if names::reference(value) {
        return value.to_owned();
    }
    if value.trim() != value || value.chars().any(char::is_control) {
        return "[redacted]".to_owned();
    }
    // Use the reviewed parser for authority/port/IPv6 validation. Go accepts
    // network-path references too; the temporary scheme is validation-only.
    let validation = if value.starts_with("//") {
        format!("https:{value}")
    } else {
        value.to_owned()
    };
    let Ok(parsed) = url::Url::parse(&validation) else {
        return "[redacted]".to_owned();
    };
    if parsed.host_str().is_none() {
        return "[redacted]".to_owned();
    }
    // Preserve the original spelling of safe URL components instead of letting
    // WHATWG serialization normalize paths, host case, escapes or explicit ports.
    // These spans perform edits only after parser validation, not URL validation.
    let (prefix, rest) = if let Some(rest) = value.strip_prefix("//") {
        ("//".to_owned(), rest)
    } else if let Some((scheme, rest)) = value.split_once("://") {
        (format!("{scheme}://"), rest)
    } else {
        return "[redacted]".to_owned();
    };
    let end = rest.find(['/', '?', '#']).unwrap_or(rest.len());
    let Some(authority) = rest.get(..end).filter(|s| !s.is_empty()) else {
        return "[redacted]".to_owned();
    };
    let Some(tail) = rest.get(end..) else {
        return "[redacted]".to_owned();
    };
    let (path_query, fragment) = tail
        .split_once('#')
        .map_or((tail, String::new()), |(p, f)| (p, format!("#{f}")));
    let (path, query) = path_query
        .split_once('?')
        .map_or((path_query, None), |(p, q)| (p, Some(q)));
    if !valid_escapes(authority) || !valid_escapes(path) || !valid_escapes(&fragment) {
        return "[redacted]".to_owned();
    }
    let authority = if let Some((user, host)) = authority.rsplit_once('@') {
        if let Some((username, _)) = user.split_once(':') {
            format!("{username}@{host}")
        } else {
            authority.to_owned()
        }
    } else {
        authority.to_owned()
    };
    let query = query
        .map(|query| {
            format!(
                "?{}",
                query
                    .split('&')
                    .map(|part| {
                        if let Some((key, val)) = part.split_once('=')
                            && !percent_decode(val).is_some_and(|v| names::reference(&v))
                        {
                            return format!("{key}=[redacted]");
                        }
                        part.to_owned()
                    })
                    .collect::<Vec<_>>()
                    .join("&")
            )
        })
        .unwrap_or_default();
    format!("{prefix}{authority}{path}{query}{fragment}")
}

pub(super) fn args_scrub(argv: &[String]) -> Vec<String> {
    let mut out = argv.to_vec();
    for (i, arg) in argv.iter().enumerate() {
        if arg.trim_start().starts_with('{') {
            let settings =
                scrub("settings.json", arg.as_bytes()).and_then(|b| scrub("mcp.json", &b));
            if let Some(slot) = out.get_mut(i) {
                *slot = settings
                    .ok()
                    .and_then(|b| String::from_utf8(b).ok())
                    .unwrap_or_else(|| "[redacted]".to_owned());
            }
        } else if let Some((name, value)) = arg.split_once('=') {
            if names::matches(r"^-{0,2}[A-Za-z0-9][\w.-]*$", name)
                && let Some(slot) = out.get_mut(i)
            {
                *slot = format!("{name}={}", flag(name, value));
            }
        } else if arg.starts_with('-')
            && names::matches(r"^-{0,2}[A-Za-z0-9][\w.-]*$", arg)
            && let Some(value) = argv.get(i + 1).filter(|s| !s.starts_with('-'))
            && let Some(slot) = out.get_mut(i + 1)
        {
            *slot = flag(arg, value);
        }
    }
    out
}
fn flag(name: &str, value: &str) -> String {
    if name == "-H" || name == "--header" {
        if let Some((key, val)) = value.split_once(':')
            && !names::reference(val.trim())
        {
            return format!("{key}: [redacted]");
        }
    } else if names::is_secret_name(name) {
        return redact(value, Mode::References);
    }
    value.to_owned()
}
