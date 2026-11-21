//! Whole-file content scan for `Profile::Scanned` (D54, brief 47). The file is published whole
//! or withheld whole: a clean file passes byte for byte, nothing is redacted. Reasons are fixed
//! codes; no match, offset or content ever leaves this module.

use super::{Reason, names, text};
use regex::Regex;
use sha2::{Digest, Sha256};
use std::sync::OnceLock;

/// Largest file that is read and scanned. A larger file is withheld from its stat alone.
pub const MAX_SCANNED_BYTES: u64 = 1024 * 1024;

/// Version of the table below; bump with any change to a pattern, so the digest moves even if a
/// edit were to cancel out.
const TABLE_VERSION: &str = "scan/1";

/// Fixed secret shapes. Each hit withholds the whole file (false positives are accepted).
const PATTERNS: &[&str] = &[
    // PEM, OpenSSH and PGP private-key headers.
    r"-----BEGIN [A-Z ]*PRIVATE KEY( BLOCK)?-----",
    // AWS access key ids.
    r"(AKIA|ASIA)[A-Z0-9]{16}",
    // GitHub tokens (classic prefixes and fine-grained).
    r"gh[pousr]_[A-Za-z0-9]{20,}",
    r"github_pat_[A-Za-z0-9_]{20,}",
    // Slack tokens.
    r"xox[abprs]-[A-Za-z0-9-]{10,}",
    // Anthropic and OpenAI keys.
    r"sk-ant-[A-Za-z0-9_-]{16,}",
    r"sk-proj-[A-Za-z0-9_-]{16,}",
    r"sk-[A-Za-z0-9_-]{32,}",
    // Google API keys.
    r"AIza[0-9A-Za-z_-]{35}",
    // Stripe live keys.
    r"(sk|rk)_live_[0-9A-Za-z]{16,}",
    // JSON web tokens (header and payload segments).
    r"eyJ[A-Za-z0-9_-]{5,}\.eyJ[A-Za-z0-9_-]{5,}\.",
    // Credentials embedded in a URL: scheme://user:password@host.
    r"://[^\s/:@]+:[^\s/@]+@",
];

/// A name, `=` or `:` (optionally quoted: env, YAML, JSON, TOML, source code), then a value of
/// 8 or more characters. Names are judged by `names::is_secret_name`, values by
/// `names::reference` plus the `<placeholder>` form.
const ASSIGNMENT: &str = r#"([A-Za-z_][A-Za-z0-9_.-]*)["']?\s*[=:]\s*["']?([^\s"',;]{8,})"#;

struct Table {
    patterns: Vec<Regex>,
    assignment: Regex,
}

fn table() -> Option<&'static Table> {
    static TABLE: OnceLock<Option<Table>> = OnceLock::new();
    TABLE
        .get_or_init(|| {
            let patterns = PATTERNS
                .iter()
                .map(|p| Regex::new(p))
                .collect::<Result<Vec<_>, _>>()
                .ok()?;
            Some(Table {
                patterns,
                assignment: Regex::new(ASSIGNMENT).ok()?,
            })
        })
        .as_ref()
}

/// Scanner table and cap, for `rules_digest`.
pub(super) fn digest_rules(hash: &mut Sha256) {
    hash.update(TABLE_VERSION.as_bytes());
    hash.update(MAX_SCANNED_BYTES.to_le_bytes());
    for pattern in PATTERNS.iter().copied().chain([ASSIGNMENT]) {
        hash.update(pattern.as_bytes());
        hash.update(b"\n");
    }
}

fn placeholder(value: &str) -> bool {
    value.starts_with('<') && value.ends_with('>')
}

/// First hit wins: size, binary, secret pattern, internal error.
pub(super) fn check(bytes: &[u8]) -> Result<(), Reason> {
    if bytes.len() as u64 > MAX_SCANNED_BYTES {
        return Err(Reason::SourceLimit);
    }
    if bytes.contains(&0) {
        return Err(Reason::Binary);
    }
    let Ok(source) = std::str::from_utf8(bytes) else {
        return Err(Reason::Binary);
    };
    let Some(table) = table() else {
        return Err(Reason::ScrubFailed);
    };
    if table.patterns.iter().any(|re| re.is_match(source)) {
        return Err(Reason::SecretInText);
    }
    // The shell-assignment guard, with no extension exemption.
    text::check(&[], bytes)?;
    for found in table.assignment.captures_iter(source) {
        let (Some(name), Some(value)) = (found.get(1), found.get(2)) else {
            return Err(Reason::ScrubFailed);
        };
        if names::is_secret_name(name.as_str())
            && !names::reference(value.as_str())
            && !placeholder(value.as_str())
        {
            return Err(Reason::SecretInText);
        }
    }
    Ok(())
}
