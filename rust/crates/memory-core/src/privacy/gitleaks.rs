//! Port of gitleaks v8 `detectRule` over the vendored v8.30.1 rule file (brief 50).
//!
//! Ported: keyword prefilter, every match of the rule regex, the secret group, the entropy
//! floor, the global and rule allowlists (`regexTarget`, OR/AND, stopwords). A `paths` check is
//! never satisfied (a scanned file has no meaningful path), `gitleaks:allow` is never honoured,
//! and the four rules that carry both `path` and `regex` apply to every file. The path-only
//! `pkcs12-file` rule is skipped. Not ported: base64, hex and percent decoding passes and
//! multi-part `required` rules (none in v8.30.1).
//!
//! Cost (brief 51). The keyword prefilter runs `memchr::memmem` over the lowercased text. Every
//! rule pattern is compiled once at load to validate it (a failure is a table failure), then the
//! regex is dropped. A rule's regex is compiled again, and kept in a `OnceLock`, at the first
//! text whose keywords match (a rule without keywords: at its first use). Most rules never
//! compile, which saves memory. A compile that fails then gives `Detect::Failed`, and the caller
//! withholds with `ScrubFailed`. Each rule regex has a 256 KiB lazy-DFA cache cap instead of the
//! default 2 MiB: 221 caches at the default were most of the scan's resident memory. Neither
//! change alters a verdict. Allowlist regexes stay compiled from load.
//!
//! The table loads once; any parse or compile failure yields `None` and the caller withholds.

use regex::bytes::{Regex, RegexBuilder};
use sha2::Sha256;
use sha2::digest::Update;
use std::collections::HashMap;
use std::sync::OnceLock;

/// The vendored rule file, embedded unchanged.
const RULES_TOML: &str = include_str!("../../data/gitleaks-v8.30.1/gitleaks.toml");

/// Compiled-program cap per regex (128 MiB). The largest vendored rule needs far less; the cap
/// only bounds a pathological pattern.
const REGEX_SIZE_LIMIT: usize = 1 << 27;

/// Lazy-DFA cache cap per regex (256 KiB). The default cache (2 MiB) is allocated per regex on
/// first search; with 221 rules that is most of the scan's memory. Not semantic: a smaller
/// cache only makes a search recompute states.
const DFA_SIZE_LIMIT: usize = 256 * 1024;

/// Go RE2 reads a `{` that does not start `{n}`, `{n,}` or `{n,m}` as a literal; Rust rejects it.
fn go_braces(pattern: &str) -> String {
    let chars: Vec<char> = pattern.chars().collect();
    let counted = |from: usize| -> bool {
        let mut i = from;
        let mut digits = 0;
        while chars.get(i).is_some_and(char::is_ascii_digit) {
            i += 1;
            digits += 1;
        }
        if digits == 0 {
            return false;
        }
        if chars.get(i) == Some(&',') {
            i += 1;
            while chars.get(i).is_some_and(char::is_ascii_digit) {
                i += 1;
            }
        }
        chars.get(i) == Some(&'}')
    };
    let mut out = String::with_capacity(pattern.len() + 8);
    let mut i = 0;
    let mut class = false;
    while let Some(&c) = chars.get(i) {
        if c == '\\' {
            out.push(c);
            if let Some(&next) = chars.get(i + 1) {
                out.push(next);
            }
            i += 2;
            continue;
        }
        if class {
            if c == '[' && chars.get(i + 1) == Some(&':') {
                // A POSIX class `[:alpha:]` inside a bracket class: copy through `:]`.
                while let Some(&d) = chars.get(i) {
                    out.push(d);
                    i += 1;
                    if d == ']' && out.ends_with(":]") {
                        break;
                    }
                }
                continue;
            }
            if c == ']' {
                class = false;
            }
            out.push(c);
            i += 1;
            continue;
        }
        if c == '[' {
            class = true;
            out.push(c);
            i += 1;
            // A leading `^` and then `]` are literal members of the class.
            if chars.get(i) == Some(&'^') {
                out.push('^');
                i += 1;
            }
            if chars.get(i) == Some(&']') {
                out.push(']');
                i += 1;
            }
            continue;
        }
        if c == '{' && !counted(i + 1) {
            out.push_str("\\{");
            i += 1;
            continue;
        }
        out.push(c);
        i += 1;
    }
    out
}

fn compile(pattern: &str) -> Result<Regex, String> {
    RegexBuilder::new(&go_braces(pattern))
        .unicode(false)
        .size_limit(REGEX_SIZE_LIMIT)
        .dfa_size_limit(DFA_SIZE_LIMIT)
        .build()
        .map_err(|e| format!("pattern {pattern:?}: {e}"))
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Target {
    Secret,
    Matched,
    Line,
}

struct Allow {
    target: Target,
    regexes: Vec<Regex>,
    stopwords: Vec<String>,
    and: bool,
    /// `paths` or `commits` present: checks that are never satisfied (no path, no commit).
    unmatchable: bool,
}

impl Allow {
    fn allowed(&self, secret: &[u8], matched: &[u8], line: &[u8]) -> bool {
        let target = match self.target {
            Target::Secret => secret,
            Target::Matched => matched,
            Target::Line => line,
        };
        let regex = !target.is_empty() && self.regexes.iter().any(|r| r.is_match(target));
        let stop = !secret.is_empty() && {
            let lower = String::from_utf8_lossy(secret).to_lowercase();
            self.stopwords.iter().any(|w| lower.contains(w.as_str()))
        };
        if !self.and {
            return regex || stop;
        }
        let mut checks = Vec::new();
        if self.unmatchable {
            checks.push(false);
        }
        if !self.regexes.is_empty() {
            checks.push(regex);
        }
        if !self.stopwords.is_empty() {
            checks.push(stop);
        }
        checks.iter().all(|c| *c)
    }
}

struct Rule {
    /// Validated at load, compiled again at the first text whose keywords match (or the first
    /// text at all if the rule has no keywords). `None` inside records a failed compile.
    pattern: String,
    regex: OnceLock<Option<Regex>>,
    keywords: Vec<String>,
    entropy: f64,
    group: usize,
    allows: Vec<Allow>,
}

/// The compiled rule set.
pub(super) struct Table {
    rules: Vec<Rule>,
    global: Vec<Allow>,
    #[cfg_attr(not(test), expect(dead_code, reason = "read only by the load tests"))]
    skipped: Vec<String>,
}

/// Keys this port understands. Any other key is a table failure, so a semantic field added
/// upstream (as `required` was) cannot be ignored silently.
const RULE_KEYS: &[&str] = &[
    "id",
    "description",
    "regex",
    "path",
    "keywords",
    "entropy",
    "secretGroup",
    "allowlists",
    "allowlist",
    "tags",
];
const ALLOW_KEYS: &[&str] = &[
    "description",
    "condition",
    "regexTarget",
    "regexes",
    "stopwords",
    "paths",
    "commits",
];
const TOP_KEYS: &[&str] = &["title", "minVersion", "allowlist", "allowlists", "rules"];

fn known(table: &toml::Table, keys: &[&str], what: &str) -> Result<(), String> {
    match table.keys().find(|k| !keys.contains(&k.as_str())) {
        None => Ok(()),
        Some(k) => Err(format!("unknown {what} key {k:?}")),
    }
}

fn strings(table: &toml::Table, key: &str) -> Vec<String> {
    table
        .get(key)
        .and_then(toml::Value::as_array)
        .map(|a| {
            a.iter()
                .filter_map(|v| v.as_str().map(str::to_owned))
                .collect()
        })
        .unwrap_or_default()
}

fn allow(table: &toml::Table) -> Result<Allow, String> {
    known(table, ALLOW_KEYS, "allowlist")?;
    let target = match table.get("regexTarget").and_then(toml::Value::as_str) {
        None => Target::Secret,
        Some("match") => Target::Matched,
        Some("line") => Target::Line,
        Some(other) => return Err(format!("unknown regexTarget {other:?}")),
    };
    let and = match table.get("condition").and_then(toml::Value::as_str) {
        None => false,
        Some(s) if s.eq_ignore_ascii_case("or") => false,
        Some(s) if s.eq_ignore_ascii_case("and") => true,
        Some(other) => return Err(format!("unknown condition {other:?}")),
    };
    let regexes: Vec<Regex> = strings(table, "regexes")
        .iter()
        .map(|p| compile(p))
        .collect::<Result<_, _>>()?;
    let stopwords: Vec<String> = strings(table, "stopwords")
        .into_iter()
        .map(|s| s.to_lowercase())
        .collect();
    let unmatchable = !strings(table, "paths").is_empty() || !strings(table, "commits").is_empty();
    // gitleaks `Validate`: an allowlist needs at least one check. Without this an empty AND
    // allowlist would allow every finding.
    if regexes.is_empty() && stopwords.is_empty() && !unmatchable {
        return Err("allowlist without a check".to_owned());
    }
    Ok(Allow {
        target,
        regexes,
        stopwords,
        and,
        unmatchable,
    })
}

fn allowlists(table: &toml::Table, plural: &str, singular: &str) -> Result<Vec<Allow>, String> {
    let mut out = Vec::new();
    for item in table
        .get(plural)
        .and_then(toml::Value::as_array)
        .into_iter()
        .flatten()
    {
        out.push(allow(item.as_table().ok_or("allowlist is not a table")?)?);
    }
    if let Some(item) = table.get(singular) {
        out.push(allow(item.as_table().ok_or("allowlist is not a table")?)?);
    }
    Ok(out)
}

/// Parse and compile a gitleaks rule file.
fn load(source: &str) -> Result<Table, String> {
    let doc: toml::Table = toml::from_str(source).map_err(|e| e.to_string())?;
    known(&doc, TOP_KEYS, "top-level")?;
    let global = allowlists(&doc, "allowlists", "allowlist")?;
    let mut rules = Vec::new();
    let mut skipped = Vec::new();
    for item in doc
        .get("rules")
        .and_then(toml::Value::as_array)
        .into_iter()
        .flatten()
    {
        let rule = item.as_table().ok_or("rule is not a table")?;
        known(rule, RULE_KEYS, "rule")?;
        let id = rule
            .get("id")
            .and_then(toml::Value::as_str)
            .ok_or("rule without id")?;
        let Some(pattern) = rule.get("regex").and_then(toml::Value::as_str) else {
            if rule.contains_key("path") {
                // Path-only rule: a scanned file has no path to match.
                skipped.push(id.to_owned());
                continue;
            }
            return Err(format!("rule {id} has neither regex nor path"));
        };
        let entropy = match rule.get("entropy") {
            None => 0.0,
            Some(v) => v
                .as_float()
                .or_else(|| v.as_integer().map(|i| i as f64))
                .ok_or("entropy is not a number")?,
        };
        let group = match rule.get("secretGroup") {
            None => 0,
            Some(v) => v
                .as_integer()
                .and_then(|i| usize::try_from(i).ok())
                .ok_or("secretGroup is not a count")?,
        };
        // Compile once to validate, then drop: the regex is rebuilt only if the rule is reached.
        drop(compile(pattern).map_err(|e| format!("rule {id}: {e}"))?);
        rules.push(Rule {
            pattern: pattern.to_owned(),
            regex: OnceLock::new(),
            keywords: strings(rule, "keywords")
                .into_iter()
                .map(|k| k.to_lowercase())
                .collect(),
            entropy,
            group,
            allows: allowlists(rule, "allowlists", "allowlist")?,
        });
    }
    Ok(Table {
        rules,
        global,
        skipped,
    })
}

/// The table over the vendored file, built once. `None` on any failure (the caller withholds).
pub(super) fn table() -> Option<&'static Table> {
    static TABLE: OnceLock<Option<Table>> = OnceLock::new();
    TABLE.get_or_init(|| load(RULES_TOML).ok()).as_ref()
}

/// Test-only: a table whose one rule validated at load but fails to compile when reached.
#[cfg(test)]
pub(super) fn failing_table() -> Option<Table> {
    let mut t = load("[[rules]]\nid='a'\nregex='aa=(\\w+)'\nkeywords=['aa']\n").ok()?;
    for rule in &mut t.rules {
        rule.pattern = "(".to_owned();
    }
    Some(t)
}

/// The vendored file's bytes, for `rules_digest`.
pub(super) fn digest_rules(hash: &mut Sha256) {
    Update::update(hash, RULES_TOML.as_bytes());
}

/// Shannon entropy over chars, normalised by the byte length as gitleaks does.
fn entropy(secret: &[u8]) -> f64 {
    let text = String::from_utf8_lossy(secret);
    if text.is_empty() {
        return 0.0;
    }
    let mut counts: HashMap<char, usize> = HashMap::new();
    for c in text.chars() {
        *counts.entry(c).or_default() += 1;
    }
    let inv = 1.0 / text.len() as f64;
    counts
        .values()
        .map(|&n| {
            let f = n as f64 * inv;
            -f * f.log2()
        })
        .sum()
}

fn contains(haystack: &[u8], needle: &[u8]) -> bool {
    !needle.is_empty() && memchr::memmem::find(haystack, needle).is_some()
}

/// The outcome of a scan. `Failed` (a rule regex that could not be compiled) is closed: the
/// caller withholds.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub(super) enum Detect {
    Found,
    Clean,
    Failed,
}

impl Table {
    /// Whether any rule finds a secret in `text`.
    pub(super) fn detect(&self, text: &[u8]) -> Detect {
        let lower = text.to_ascii_lowercase();
        for rule in &self.rules {
            match rule.finds(self, text, &lower) {
                Detect::Clean => {}
                other => return other,
            }
        }
        Detect::Clean
    }

    /// Rules currently holding a compiled regex.
    #[cfg(test)]
    fn compiled(&self) -> usize {
        self.rules
            .iter()
            .filter(|r| r.regex.get().is_some())
            .count()
    }
}

impl Rule {
    fn finds(&self, table: &Table, text: &[u8], lower: &[u8]) -> Detect {
        if !self.keywords.is_empty() && !self.keywords.iter().any(|k| contains(lower, k.as_bytes()))
        {
            return Detect::Clean;
        }
        let Some(regex) = self
            .regex
            .get_or_init(|| compile(&self.pattern).ok())
            .as_ref()
        else {
            return Detect::Failed;
        };
        for found in regex.find_iter(text) {
            let bytes = found.as_bytes();
            let lead = bytes.iter().take_while(|c| **c == b'\n').count();
            let tail = bytes.iter().rev().take_while(|c| **c == b'\n').count();
            let matched: &[u8] = if lead + tail >= bytes.len() {
                &[]
            } else {
                bytes.get(lead..bytes.len() - tail).unwrap_or_default()
            };
            let start = found.start() + lead;
            let mut secret = matched;
            if let Some(caps) = regex.captures(matched)
                && caps.len() >= 2
            {
                if self.group > 0 {
                    if caps.len() <= self.group {
                        continue;
                    }
                    secret = caps.get(self.group).map_or(&[][..], |g| g.as_bytes());
                } else if let Some(g) = (1..caps.len())
                    .filter_map(|i| caps.get(i))
                    .find(|g| !g.as_bytes().is_empty())
                {
                    secret = g.as_bytes();
                }
            }
            if self.entropy > 0.0 && entropy(secret) <= self.entropy {
                continue;
            }
            let end = start + matched.len();
            let line_start = text
                .get(..start)
                .and_then(|t| t.iter().rposition(|c| *c == b'\n'))
                .map_or(0, |i| i + 1);
            let line_end = text
                .get(end..)
                .and_then(|t| t.iter().position(|c| *c == b'\n'))
                .map_or(text.len(), |i| end + i);
            let line = text
                .get(line_start..line_end.max(line_start))
                .unwrap_or_default();
            if table
                .global
                .iter()
                .chain(&self.allows)
                .any(|a| a.allowed(secret, matched, line))
            {
                continue;
            }
            return Detect::Found;
        }
        Detect::Clean
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use sha2::Digest;

    fn hit(rule: &str, text: &str) -> bool {
        load(rule).is_ok_and(|t| t.detect(text.as_bytes()) == Detect::Found)
    }

    #[test]
    fn vendored_file_is_pinned() {
        let digest = Sha256::digest(RULES_TOML.as_bytes());
        let hex: String = digest.iter().map(|b| format!("{b:02x}")).collect();
        assert_eq!(
            hex,
            "e163e53b9e7e8a8511e77271e2b323ed057759542a6d988258afe3a1fa329caf"
        );
    }

    #[test]
    fn vendored_rules_load_and_only_the_path_rule_is_skipped() {
        assert!(table().is_some(), "vendored table failed to load");
        let Some(table) = table() else {
            return;
        };
        assert_eq!(table.rules.len(), 221);
        assert_eq!(table.skipped, vec!["pkcs12-file".to_owned()]);
    }

    #[test]
    fn digest_covers_the_rule_file() {
        let mut a = Sha256::new();
        digest_rules(&mut a);
        assert_eq!(a.finalize(), Sha256::digest(RULES_TOML.as_bytes()));
    }

    #[test]
    fn vendored_table_finds_a_provider_token_and_passes_code() {
        assert!(table().is_some(), "vendored table failed to load");
        let Some(table) = table() else {
            return;
        };
        let token = format!("{}{}", "ghp_", "aZ3kQ9xLm2Pv7Rt1Yb6Nc8Wd4Fe5Gh0JsKuA");
        assert_eq!(
            table.detect(format!("export X={token}\n").as_bytes()),
            Detect::Found
        );
        assert_eq!(
            table.detect(b"token = getToken()\nlet key = lookup(name);\n"),
            Detect::Clean
        );
    }

    #[test]
    fn braces_follow_go() {
        assert_eq!(go_braces(r"a{2}b{2,}c{2,3}"), r"a{2}b{2,}c{2,3}");
        assert_eq!(go_braces(r"a{b}"), r"a\{b}");
        assert_eq!(go_braces(r"a{,3}"), r"a\{,3}");
        assert_eq!(go_braces(r"^\$(?:\d+|{\d+})$"), r"^\$(?:\d+|\{\d+})$");
        assert_eq!(go_braces(r"[{]\{{"), r"[{]\{\{");
        assert_eq!(go_braces(r"[^]{]{"), r"[^]{]\{");
        assert_eq!(go_braces(r"[[:alpha:]{]x{"), r"[[:alpha:]{]x\{");
    }

    #[test]
    fn a_table_failure_is_reported() {
        assert!(load("rules = [").is_err());
        assert!(load("[[rules]]\nid = 'x'\nregex = '(' \n").is_err());
        assert!(load("[[rules]]\nid = 'x'\n").is_err());
        assert!(load("[[rules]]\nid = 'x'\nregex = 'a'\nsecretGroup = -1\n").is_err());
        // Parent, brief 50: an allowlist without a check, and unknown keys, are table failures.
        assert!(
            load("[[rules]]\nid='x'\nregex='a'\n[[rules.allowlists]]\ncondition='AND'\n").is_err()
        );
        assert!(
            load("[[rules]]\nid='x'\nregex='a'\n[[rules.allowlists]]\ndescription='d'\n").is_err()
        );
        assert!(load("[[rules]]\nid='x'\nregex='a'\nrequired=[]\n").is_err());
        assert!(
            load("[[rules]]\nid='x'\nregex='a'\n[[rules.allowlists]]\nregexes=['b']\nnew=1\n")
                .is_err()
        );
        assert!(load("future = 1\n[[rules]]\nid='x'\nregex='a'\n").is_err());
    }

    #[test]
    fn a_commits_only_and_allowlist_never_allows() {
        let doc = "[[rules]]\nid='x'\nregex='tok=(\\w+)'\n[[rules.allowlists]]\ncondition='AND'\ncommits=['abc']\n";
        assert!(hit(doc, "tok=abcd"));
        let or = "[[rules]]\nid='x'\nregex='tok=(\\w+)'\n[[rules.allowlists]]\ncommits=['abc']\n";
        assert!(hit(or, "tok=abcd"));
    }

    #[test]
    fn entropy_floor_is_exclusive() {
        // "ab" has exactly 1.0 bit of entropy: equal to the floor skips, below the floor finds.
        let rule = |e: &str| {
            format!("[[rules]]\nid='x'\nregex='''zz([ab]+)'''\nentropy = {e}\nkeywords=['zz']\n")
        };
        assert!(!hit(&rule("1.0"), "zzab"));
        assert!(hit(&rule("0.99"), "zzab"));
        assert!(!hit(&rule("2"), "zzab"));
        assert!(hit(&rule("0.0"), "zzaaaa"));
    }

    #[test]
    fn secret_group_and_first_non_empty_group() {
        // The stopword judges the secret only: which group is the secret shows through it.
        let first = |stop: &str| {
            format!(
                "[[rules]]\nid='x'\nregex='''id(a*)(b+)'''\n[[rules.allowlists]]\nstopwords=['{stop}']\n"
            )
        };
        assert!(!hit(&first("bbb"), "idbbb"));
        assert!(!hit(&first("aa"), "idaab"));
        assert!(hit(&first("bb"), "idaab"));
        let grouped = |stop: &str| {
            format!(
                "[[rules]]\nid='x'\nregex='''(x+)(y+)'''\nsecretGroup=2\n[[rules.allowlists]]\nstopwords=['{stop}']\n"
            )
        };
        assert!(!hit(&grouped("yy"), "xxyy"));
        assert!(hit(&grouped("xx"), "xxyy"));
    }

    #[test]
    fn allowlist_targets() {
        let rule = |target: &str, re: &str| {
            format!(
                "[[rules]]\nid='x'\nregex='''tok=(\\w+)'''\n[[rules.allowlists]]\n{target}regexes=['''{re}''']\n"
            )
        };
        let text = "line: tok=abc\n";
        assert!(!hit(&rule("regexTarget='match'\n", "^tok="), text));
        assert!(hit(&rule("", "^tok="), text));
        assert!(!hit(&rule("", "^abc$"), text));
        assert!(!hit(&rule("regexTarget='line'\n", "^line: tok"), text));
        assert!(hit(&rule("regexTarget='line'\n", "^tok"), text));
        assert!(hit(&rule("regexTarget='match'\n", "^line"), text));
    }

    #[test]
    fn allowlist_conditions() {
        let rule = |cond: &str, extra: &str| {
            format!(
                "[[rules]]\nid='x'\nregex='''tok=(\\w+)'''\n[[rules.allowlists]]\ncondition='{cond}'\nregexes=['''^abc''']\nstopwords=['bcd']\n{extra}"
            )
        };
        // OR: either check allows. AND: both are needed.
        assert!(!hit(&rule("OR", ""), "tok=abcx"));
        assert!(!hit(&rule("OR", ""), "tok=zbcdz"));
        assert!(hit(&rule("OR", ""), "tok=zzz"));
        assert!(hit(&rule("AND", ""), "tok=abcx"));
        assert!(hit(&rule("AND", ""), "tok=zbcdz"));
        assert!(!hit(&rule("AND", ""), "tok=abcd"));
        // A paths check is never satisfied.
        assert!(hit(&rule("AND", "paths=['''.*''']\n"), "tok=abcd"));
        assert!(hit(
            "[[rules]]\nid='x'\nregex='tok=(\\w+)'\n[[rules.allowlists]]\npaths=['''.*''']\n",
            "tok=abcd"
        ));
    }

    #[test]
    fn global_allowlist_applies_to_every_rule() {
        let doc = "[allowlist]\nregexes=['''^abc''']\n[[rules]]\nid='x'\nregex='tok=(\\w+)'\n";
        assert!(!hit(doc, "tok=abcd"));
        assert!(hit(doc, "tok=zbcd"));
        let plural = "[[allowlists]]\nstopwords=['bcd']\n[[rules]]\nid='x'\nregex='tok=(\\w+)'\n";
        assert!(!hit(plural, "tok=zbcdz"));
    }

    #[test]
    fn keyword_prefilter_is_case_insensitive() {
        let doc = "[[rules]]\nid='x'\nregex='tok=(\\w+)'\nkeywords=['Zzz']\n";
        assert!(!hit(doc, "tok=abcd"));
        assert!(hit(doc, "tok=abcd ZZZ"));
        assert!(hit(doc, "tok=abcd zzz"));
    }

    #[test]
    fn contains_finds_start_end_and_never_empty() {
        assert!(contains(b"abcdef", b"abc"));
        assert!(contains(b"abcdef", b"def"));
        assert!(contains(b"abcdef", b"cd"));
        assert!(!contains(b"abcdef", b"xyz"));
        assert!(!contains(b"abcdef", b""));
        assert!(!contains(b"", b""));
        assert!(!contains(b"ab", b"abc"));
    }

    #[test]
    fn keyword_at_start_end_and_mixed_case() {
        let doc = "[[rules]]\nid='x'\nregex='tok=(\\w+)'\nkeywords=['Key']\n";
        assert!(hit(doc, "KEY tok=abcd"));
        assert!(hit(doc, "tok=abcd kEy"));
        assert!(!hit(doc, "tok=abcd ke"));
        let empty = "[[rules]]\nid='x'\nregex='tok=(\\w+)'\nkeywords=['']\n";
        assert!(!hit(empty, "tok=abcd"));
    }

    #[test]
    fn rules_compile_only_when_reached() {
        let doc = "[[rules]]\nid='a'\nregex='aa=(\\w+)'\nkeywords=['aa']\n\
                   [[rules]]\nid='b'\nregex='bb=(\\w+)'\nkeywords=['bb']\n\
                   [[rules]]\nid='c'\nregex='cc=(\\w+)'\n";
        assert!(load(doc).is_ok(), "load failed");
        let Ok(t) = load(doc) else {
            return;
        };
        assert_eq!(t.compiled(), 0);
        // Only rule a's keyword occurs; rule c has no keywords and runs on every text.
        assert_eq!(t.detect(b"XX aa=1"), Detect::Found);
        assert_eq!(t.compiled(), 1);
        assert_eq!(t.detect(b"nothing here"), Detect::Clean);
        assert_eq!(t.compiled(), 2);
        assert!(
            t.rules
                .iter()
                .any(|r| r.pattern.starts_with("cc") && r.regex.get().is_some())
        );
        assert!(
            t.rules
                .iter()
                .any(|r| r.pattern.starts_with("bb") && r.regex.get().is_none())
        );
    }

    #[test]
    fn a_lazy_compile_failure_is_failed_not_clean() {
        let doc = "[[rules]]\nid='a'\nregex='aa=(\\w+)'\nkeywords=['aa']\n";
        assert!(load(doc).is_ok(), "load failed");
        let Ok(mut t) = load(doc) else {
            return;
        };
        for rule in &mut t.rules {
            rule.pattern = "(".to_owned();
        }
        assert_eq!(t.detect(b"nothing"), Detect::Clean);
        assert_eq!(t.detect(b"aa=1"), Detect::Failed);
        assert_eq!(t.detect(b"aa=1"), Detect::Failed);
    }

    #[test]
    fn path_rules_and_allow_marker() {
        let both = "[[rules]]\nid='x'\npath='''\\.php$'''\nregex='tok=(\\w+)'\n";
        assert!(hit(both, "tok=abcd"));
        let path_only = "[[rules]]\nid='p'\npath='''\\.p12$'''\n";
        assert!(load(path_only).is_ok_and(|t| t.skipped == vec!["p".to_owned()]));
        assert!(!hit(path_only, "anything"));
        // `gitleaks:allow` on the line is not honoured.
        assert!(hit(both, "tok=abcd # gitleaks:allow"));
    }

    #[test]
    fn match_trims_newlines_and_line_is_the_matched_line() {
        let doc = "[[rules]]\nid='x'\nregex='''\\ntok=(\\w+)\\n'''\n[[rules.allowlists]]\nregexTarget='line'\nregexes=['''^a=1$''']\n";
        assert!(hit(doc, "a=1\ntok=abcd\nb=2"));
        let doc = doc.replace("^a=1$", "^tok=abcd$");
        assert!(!hit(&doc, "a=1\ntok=abcd\nb=2"));
    }
}
