use super::{document, names};
use std::collections::BTreeMap;

/// Sanitized launch evidence. No raw payload constructor or deserializer.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SanitizedLaunch {
    argv: Vec<String>,
    env: BTreeMap<String, String>,
}
impl SanitizedLaunch {
    /// Bounded native metadata arguments, never positional prompt text.
    pub fn argv(&self) -> &[String] {
        &self.argv
    }
    /// Exact allowlisted environment metadata.
    pub fn env(&self) -> &BTreeMap<String, String> {
        &self.env
    }
}
pub(super) const SAFE_VALUE: &str = r"^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}$";
const PROGRAMS: [&str; 6] = ["claude", "codex", "pi", "node", "nodejs", "bun"];
const FLAGS: [&str; 6] = [
    "--resume",
    "--continue",
    "--no-session",
    "--dangerously-skip-permissions",
    "--full-auto",
    "--version",
];
const VALUE_FLAGS: [&str; 9] = [
    "--model",
    "-m",
    "--provider",
    "--permission-mode",
    "--session-id",
    "--session",
    "--config",
    "--config-dir",
    "--cwd",
];
const ENV_ALLOWLIST: [&str; 9] = [
    "ANTHROPIC_MODEL",
    "ANTHROPIC_SMALL_FAST_MODEL",
    "ANTHROPIC_DEFAULT_OPUS_MODEL",
    "ANTHROPIC_DEFAULT_SONNET_MODEL",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL",
    "CLAUDE_CONFIG_DIR",
    "MAX_THINKING_TOKENS",
    "MAX_MCP_OUTPUT_TOKENS",
    "DISABLE_PROMPT_CACHING",
];

/// What a legitimate value of an allowlisted flag or environment name looks like.
#[derive(Clone, Copy)]
enum Slot {
    /// Model name, optionally with a bracket suffix such as `[1m]`.
    Model,
    /// Provider id: no slash, at most 64 bytes.
    Provider,
    /// Permission mode: letters and hyphens, at most 32 bytes.
    Mode,
    /// Token budget: one to nine ASCII digits.
    Count,
    /// Boolean switch: `0`, `1`, `true` or `false`.
    Switch,
    /// Session id or relative path: `SAFE_VALUE`.
    Plain,
}

const MODEL_VALUE: &str = r"^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}(\[[0-9a-z]{1,8}\])?$";
const PROVIDER_VALUE: &str = r"^[A-Za-z][A-Za-z0-9._-]{0,63}$";
const MODE_VALUE: &str = r"^[A-Za-z][A-Za-z-]{0,31}$";
const COUNT_VALUE: &str = r"^[0-9]{1,9}$";
const SWITCH_VALUE: &str = r"^(?i-u:0|1|true|false)$";
/// A token that starts with one of these (compared case-folded) is a provider key.
const KEY_PREFIXES: [&str; 17] = [
    "sk-",
    "sk_",
    "ghp_",
    "gho_",
    "ghu_",
    "ghs_",
    "ghr_",
    "github_pat_",
    "glpat-",
    "xoxb-",
    "xoxp-",
    "xoxa-",
    "xoxs-",
    "xapp-",
    "hf_",
    "npm_",
    "pypi-",
];
/// Shortest opaque run treated as a token.
const OPAQUE_RUN: usize = 32;

fn flag_slot(flag: &str) -> Slot {
    match flag {
        "--model" | "-m" => Slot::Model,
        "--provider" => Slot::Provider,
        "--permission-mode" => Slot::Mode,
        _ => Slot::Plain,
    }
}

fn env_slot(name: &str) -> Slot {
    match name {
        "MAX_THINKING_TOKENS" | "MAX_MCP_OUTPUT_TOKENS" => Slot::Count,
        "DISABLE_PROMPT_CACHING" => Slot::Switch,
        "CLAUDE_CONFIG_DIR" => Slot::Plain,
        _ => Slot::Model,
    }
}

/// Provider key shapes and long opaque runs, independent of the slot.
fn secret_shaped(value: &str) -> bool {
    let folded = value.to_ascii_lowercase();
    let prefixed = value
        .split(['/', ':', '@', '+', '.'])
        .zip(folded.split(['/', ':', '@', '+', '.']))
        .any(|(raw, low)| {
            KEY_PREFIXES.iter().any(|p| low.starts_with(p))
                || low.starts_with("eyj")
                || raw.starts_with("AIza") && raw.len() >= 20
                || (raw.starts_with("AKIA") || raw.starts_with("ASIA"))
                    && raw.len() >= 20
                    && raw
                        .bytes()
                        .all(|b| b.is_ascii_uppercase() || b.is_ascii_digit())
        });
    prefixed
        || value
            .split(['/', ':', '@', '+', '.'])
            .any(|segment| segment.len() >= OPAQUE_RUN && opaque(segment))
}

/// A hex or base62 blob, or a mixed-case token with digits (base64url body).
fn opaque(segment: &str) -> bool {
    let has = |f: fn(&u8) -> bool| segment.bytes().any(|b| f(&b));
    let (upper, lower, digit) = (
        has(u8::is_ascii_uppercase),
        has(u8::is_ascii_lowercase),
        has(u8::is_ascii_digit),
    );
    let plain = segment.bytes().all(|b| b.is_ascii_alphanumeric());
    (plain && digit && (upper || lower)) || (upper && lower && digit)
}

fn safe(slot: Slot, value: &str) -> bool {
    let shape = match slot {
        Slot::Model => MODEL_VALUE,
        Slot::Provider => PROVIDER_VALUE,
        Slot::Mode => MODE_VALUE,
        Slot::Count => COUNT_VALUE,
        Slot::Switch => SWITCH_VALUE,
        Slot::Plain => SAFE_VALUE,
    };
    names::matches(shape, value) && !names::is_secret_name(value) && !secret_shaped(value)
}

/// Apply legacy secret-argument scrub then native metadata allowlist before hints.
pub fn sanitize_launch(argv: &[String], environ: &[String]) -> SanitizedLaunch {
    let bounded: Vec<String> = argv.iter().take(32).cloned().collect();
    let argv = document::args_scrub(&bounded);
    let mut out = Vec::new();
    let mut input = argv.iter();
    if let Some(program) = input.next() {
        let base = program.rsplit('/').next().unwrap_or_default();
        out.push(if PROGRAMS.contains(&base) {
            base.to_owned()
        } else {
            "[redacted]".to_owned()
        });
    }
    while let Some(arg) = input.next() {
        let (key, eq) = arg
            .split_once('=')
            .map_or((arg.as_str(), None), |(k, v)| (k, Some(v)));
        if FLAGS.contains(&key) {
            out.push(if eq.is_none() {
                key.to_owned()
            } else {
                "[redacted]".to_owned()
            });
        } else if VALUE_FLAGS.contains(&key) {
            let value = eq
                .or_else(|| input.next().map(String::as_str))
                .unwrap_or_default();
            out.push(key.to_owned());
            out.push(if safe(flag_slot(key), value) {
                value.to_owned()
            } else {
                "[redacted]".to_owned()
            });
        } else {
            out.push("[redacted]".to_owned());
        }
    }
    out.truncate(32);
    let mut env = BTreeMap::new();
    for entry in environ {
        let Some((key, value)) = entry.split_once('=') else {
            continue;
        };
        if ENV_ALLOWLIST.contains(&key) && !names::is_secret_name(key) && safe(env_slot(key), value)
        {
            env.insert(key.to_owned(), value.to_owned());
        }
    }
    SanitizedLaunch { argv: out, env }
}
