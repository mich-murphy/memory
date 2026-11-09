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
fn safe(value: &str) -> bool {
    names::matches(r"^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}$", value)
        && !names::is_secret_name(value)
}

/// Apply legacy secret-argument scrub then native metadata allowlist before hints.
pub fn sanitize_launch(argv: &[String], environ: &[String]) -> SanitizedLaunch {
    let bounded: Vec<String> = argv.iter().take(32).cloned().collect();
    let argv = document::args_scrub(&bounded);
    let mut out = Vec::new();
    let mut input = argv.iter();
    if let Some(program) = input.next() {
        let base = program.rsplit('/').next().unwrap_or_default();
        out.push(
            if ["claude", "codex", "pi", "node", "nodejs", "bun"].contains(&base) {
                base.to_owned()
            } else {
                "[redacted]".to_owned()
            },
        );
    }
    while let Some(arg) = input.next() {
        let (key, eq) = arg
            .split_once('=')
            .map_or((arg.as_str(), None), |(k, v)| (k, Some(v)));
        if [
            "--resume",
            "--continue",
            "--no-session",
            "--dangerously-skip-permissions",
            "--full-auto",
            "--version",
        ]
        .contains(&key)
        {
            out.push(if eq.is_none() {
                key.to_owned()
            } else {
                "[redacted]".to_owned()
            });
        } else if [
            "--model",
            "-m",
            "--provider",
            "--permission-mode",
            "--session-id",
            "--session",
            "--config",
            "--config-dir",
            "--cwd",
        ]
        .contains(&key)
        {
            let value = eq
                .or_else(|| input.next().map(String::as_str))
                .unwrap_or_default();
            out.push(key.to_owned());
            out.push(if safe(value) {
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
        if env.len() == 8 {
            break;
        }
        let Some((key, value)) = entry.split_once('=') else {
            continue;
        };
        if [
            "ANTHROPIC_MODEL",
            "ANTHROPIC_SMALL_FAST_MODEL",
            "ANTHROPIC_DEFAULT_OPUS_MODEL",
            "ANTHROPIC_DEFAULT_SONNET_MODEL",
            "ANTHROPIC_DEFAULT_HAIKU_MODEL",
            "CLAUDE_CONFIG_DIR",
            "MAX_THINKING_TOKENS",
            "MAX_MCP_OUTPUT_TOKENS",
            "DISABLE_PROMPT_CACHING",
        ]
        .contains(&key)
            && !names::is_secret_name(key)
            && safe(value)
        {
            env.insert(key.to_owned(), value.to_owned());
        }
    }
    SanitizedLaunch { argv: out, env }
}
