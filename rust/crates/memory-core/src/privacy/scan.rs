//! Whole-file content scan for `Profile::Scanned` (D54, brief 47). The file is published whole
//! or withheld whole: a clean file passes byte for byte, nothing is redacted. Reasons are fixed
//! codes; no match, offset or content ever leaves this module.
//!
//! Detection order (brief 50): own patterns, then the vendored gitleaks v8.30.1 rule set on the
//! bytes, then, if the bytes hold a backslash, both again on a copy with JSON string escapes
//! undone. Any finding withholds the file. Not ported from gitleaks: base64, hex and percent
//! decoding passes. The generic assignment rule and the shell guard no longer run here.

use super::{Reason, gitleaks};
use regex::Regex;
use sha2::{Digest, Sha256};
use std::sync::OnceLock;

/// Largest file that is read and scanned. A larger file is withheld from its stat alone.
pub const MAX_SCANNED_BYTES: u64 = 1024 * 1024;

/// Version of the table below; bump with any change to a pattern, so the digest moves even if a
/// edit were to cancel out.
const TABLE_VERSION: &str = "scan/2";

/// Fixed secret shapes. Each hit withholds the whole file (false positives are accepted).
/// Provider shapes start at a word boundary, as gitleaks' provider rules do: measured before
/// brief 50's install, every unanchored `sk-` hit (2 files here, 17 on dev-01) sat inside a
/// longer identifier (`…task-…`), never at a word start.
const PATTERNS: &[&str] = &[
    // PEM, OpenSSH and PGP private-key headers.
    r"-----BEGIN [A-Z ]*PRIVATE KEY( BLOCK)?-----",
    // AWS access key ids.
    r"\b(AKIA|ASIA)[A-Z0-9]{16}",
    // GitHub tokens (classic prefixes and fine-grained).
    r"\bgh[pousr]_[A-Za-z0-9]{20,}",
    r"\bgithub_pat_[A-Za-z0-9_]{20,}",
    // Slack tokens.
    r"\bxox[abprs]-[A-Za-z0-9-]{10,}",
    // Anthropic and OpenAI keys.
    r"\bsk-ant-[A-Za-z0-9_-]{16,}",
    r"\bsk-proj-[A-Za-z0-9_-]{16,}",
    r"\bsk-[A-Za-z0-9_-]{32,}",
    // Google API keys.
    r"\bAIza[0-9A-Za-z_-]{35}",
    // Stripe live keys.
    r"\b(sk|rk)_live_[0-9A-Za-z]{16,}",
    // JSON web tokens (header and payload segments).
    r"\beyJ[A-Za-z0-9_-]{5,}\.eyJ[A-Za-z0-9_-]{5,}\.",
    // Credentials embedded in a URL: scheme://user:password@host.
    r"://[^\s/:@]+:[^\s/@]+@",
];

struct Table {
    patterns: Vec<Regex>,
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
            Some(Table { patterns })
        })
        .as_ref()
}

/// Marker for the JSON-unescape pass, so changing it moves the digest.
const ESCAPE_PASS: &str = "json-unescape:\\\" \\\\ \\/ \\n \\t \\r";

/// Scanner table, vendored rule file and cap, for `rules_digest`.
pub(super) fn digest_rules(hash: &mut Sha256) {
    hash.update(TABLE_VERSION.as_bytes());
    hash.update(MAX_SCANNED_BYTES.to_le_bytes());
    gitleaks::digest_rules(hash);
    for pattern in PATTERNS {
        hash.update(pattern.as_bytes());
        hash.update(b"\n");
    }
    hash.update(ESCAPE_PASS.as_bytes());
}

/// Undo JSON string escapes `\"`, `\\`, `\/`, `\n`, `\t`, `\r`; other backslashes stay.
fn unescape(source: &str) -> String {
    let mut out = String::with_capacity(source.len());
    let mut chars = source.chars().peekable();
    while let Some(c) = chars.next() {
        if c != '\\' {
            out.push(c);
            continue;
        }
        match chars.peek().copied() {
            Some(n @ ('"' | '\\' | '/')) => {
                out.push(n);
                chars.next();
            }
            Some('n') => {
                out.push('\n');
                chars.next();
            }
            Some('t') => {
                out.push('\t');
                chars.next();
            }
            Some('r') => {
                out.push('\r');
                chars.next();
            }
            _ => out.push('\\'),
        }
    }
    out
}

fn finds(own: &Table, rules: &gitleaks::Table, text: &str) -> Result<(), Reason> {
    if own.patterns.iter().any(|re| re.is_match(text)) {
        return Err(Reason::SecretInText);
    }
    match rules.detect(text.as_bytes()) {
        gitleaks::Detect::Found => Err(Reason::SecretInText),
        gitleaks::Detect::Clean => Ok(()),
        gitleaks::Detect::Failed => Err(Reason::ScrubFailed),
    }
}

/// First hit wins: size, binary, secret pattern, internal error.
pub(super) fn check(bytes: &[u8]) -> Result<(), Reason> {
    judge(bytes, table().zip(gitleaks::table()))
}

/// The verdict over given tables; `None` (a table that failed to load) withholds.
fn judge(bytes: &[u8], tables: Option<(&Table, &gitleaks::Table)>) -> Result<(), Reason> {
    if bytes.len() as u64 > MAX_SCANNED_BYTES {
        return Err(Reason::SourceLimit);
    }
    if bytes.contains(&0) {
        return Err(Reason::Binary);
    }
    let Ok(source) = std::str::from_utf8(bytes) else {
        return Err(Reason::Binary);
    };
    let Some((own, rules)) = tables else {
        return Err(Reason::ScrubFailed);
    };
    finds(own, rules, source)?;
    if source.contains('\\') {
        finds(own, rules, &unescape(source))?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Parent, brief 50: a table that failed to load withholds a clean file (fail closed).
    #[test]
    fn a_missing_table_withholds_a_clean_file() {
        assert_eq!(judge(b"clean text\n", None), Err(Reason::ScrubFailed));
        assert_eq!(
            judge(b"clean text\n", table().zip(gitleaks::table())),
            Ok(())
        );
    }

    /// Brief 51: a rule regex that fails to compile when reached withholds with `ScrubFailed`.
    #[test]
    fn a_lazy_compile_failure_withholds_scrub_failed() {
        let failing = gitleaks::failing_table();
        assert!(failing.is_some());
        let (Some(own), Some(bad)) = (table(), failing.as_ref()) else {
            return;
        };
        assert_eq!(judge(b"clean text\n", Some((own, bad))), Ok(()));
        assert_eq!(judge(b"aa=1\n", Some((own, bad))), Err(Reason::ScrubFailed));
    }
}
