# 50. Slice 3, part 6: the content scan on the gitleaks rule set (2026-11-22)

Coordinator brief. The parent session owns this file and acceptance. It follows D54, D58 and D60
(briefs 47 and 49) and handoff item 27. Evidence goes to `evidence/45-gitleaks-scan/`. Text that
states a plan stays as written; later changes are marked.

Production daemons run on this Mac (launchd) and on dev-01 (systemd), both on the `30002d9` build,
while this brief is worked on. Nothing here touches `~/.local/state/memory*`, `~/.codex`,
`~/.claude`, `~/.pi`, `~/Library`, the installed binaries, agent settings, `.env` or R2.

## Why

Operator, 2026-11-22: "take the gitleaks approach". The home-grown `Profile::Scanned` check
(`privacy/scan.rs`) judges a `name = value` assignment by its name alone, plus the shell guard of
`text.rs`. Every withhold measured so far came from those two rules, and most are code
(`token = getToken()`). DESIGN R8 says to prefer well-known work over our own mechanisms.

Research (fresh subagent, verified by the parent against crates.io, the gitleaks source and a
rerun): Rust has no built-in scanner. Kingfisher needs a C++ build (`kingfisher-vectorscan`) and
its rules are CC-BY-SA-4.0, outside `deny.toml`. Nosey Parker is archived. `secrets_scanner` 0.2.3
caught 6 of 10 synthetic secrets. Gitleaks (MIT, v8.30.1, about 31k stars) is the industry rule
set: 222 rules, provider formats plus a generic rule gated by keywords, Shannon entropy over 3.5,
a value allowlist and stopwords.

## Measured on `62f852f` (scratch probe, counts and public rule ids only)

A scratch port of gitleaks' `detectRule` ran the v8.30.1 `config/gitleaks.toml` with the `regex`
crate (`regex::bytes`, Unicode off, as Go RE2's ASCII `\w`):

| Check | Result |
|---|---|
| Rules loaded | 217 of 222. Skipped: 4 rules constrained to a path, 1 path-only rule (`pkcs12-file`). No pattern uses lookaround or a backreference. One global allowlist pattern (`^\$(?:\d+\|{\d+})$`) needs Go's reading of a stray `{` as a literal |
| Synthetic set (11 secrets, 12 code lines) | 9 of 11 caught, 0 of 12 false positives. Misses: AWS's documented example key (allowlisted by gitleaks on purpose) and a password containing `&` |
| This Mac, every `Scanned` file | 150 withheld by the current scan; gitleaks plus our PEM, URL-credential and provider patterns withhold 5 and release 145 |
| dev-01, gitleaks only | 141 withheld today (10 of them `source_limit`); 121 released, 1 newly withheld; JSON-unescaping the text first adds 5 withholds (secrets inside escaped `*.jsonl` strings) |
| Released values (both hosts) | Code expressions, file paths, URLs without credentials, identifier text without digits. None is a literal secret by shape, entropy or character class |
| Cost | Mac replay of about 2,100 files: 8.8 s CPU, 60 MB peak resident (probe, including the walk) |

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Rule data | Vendor gitleaks v8.30.1 `config/gitleaks.toml` and its `LICENSE` (MIT) unchanged under `rust/crates/memory-core/data/gitleaks-v8.30.1/` (already copied into this worktree; sha256 `e163e53b…a329caf` and `e3884b25…ebc6`). Embedded with `include_str!`, parsed with `toml`, compiled with `regex::bytes` (`unicode(false)`). No new crate. A test pins the file's sha256 |
| Detection | A port of gitleaks `detectRule`: keyword prefilter (case-insensitive substring), every match of the rule regex, the match trimmed of `\n`, the secret is `secretGroup` or else the first non-empty capture group, skipped if `entropy` is set and the secret's Shannon entropy (over chars) is `<=` it, then the global allowlist and the rule's allowlists (`regexTarget` secret, `match` or `line`; condition OR or AND; `stopwords` matched as lowercase substrings of the secret; a `paths` check is never satisfied, since a scanned file has no meaningful path). Any finding withholds the whole file (`SecretInText`). `gitleaks:allow` is never honoured |
| Go regex syntax | A `{` that does not start `{n}`, `{n,}` or `{n,m}` outside a class is escaped before compiling (Go RE2 reads it as a literal). Any other pattern that fails to compile is a table failure |
| Path-constrained rules | The 4 rules with both `path` and `regex` apply to every file (their path cannot be known; fail closed). The path-only `pkcs12-file` is skipped (binary content is withheld anyway) |
| Own patterns kept | Our PEM private-key header, URL-credential and provider patterns (`PATTERNS` today) stay as an extra pass: they catch shapes gitleaks matches only at exact lengths. Measured cost: 2 more withholds on this Mac |
| Removed | The generic `ASSIGNMENT` rule and the `text::check` shell guard leave `scan::check`. `text.rs` itself is unchanged (still used by the `Config` and `TextAsset` profiles) |
| JSON escapes | The scan runs on the bytes and, if they contain a backslash, also on a copy with JSON string escapes undone (`\"`, `\\`, `\/`, `\n`, `\t`, `\r`). Either finding withholds |
| Not ported | gitleaks' base64, hex and percent decoding passes (`max-decode-depth`), and multi-part `required` rules (none in v8.30.1). Recorded as limits |
| Fail closed | Unchanged: over 1 MiB (from the stat), a NUL byte or invalid UTF-8 withhold first. The table loads once (`OnceLock`); a parse or compile failure withholds every scanned file with `ScrubFailed` |
| Rules digest | `TABLE_VERSION` becomes `scan/2`; the digest covers the version, the cap, the vendored file's bytes, the own patterns and the escape pass. Every scanned file is judged again once after install; unchanged bytes reuse pieces by digest |
| Out of scope | `text.rs`, the `Config`/`TextAsset` profiles, which kinds are captured, recording withheld versions in R2 |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | GITLEAKS | `rust/gitleaks-scan` | worker (Sonnet, medium) |

Owns `memory-core/src/privacy/scan.rs`, a new `memory-core/src/privacy/gitleaks.rs` (and its
`mod` line in `privacy/mod.rs`), `memory-core/data/**`, `memory-core/tests/**`, `memory/tests/**`.
Read-only everywhere else; if another file must change, stop and report.

A reference port written by the parent for the measurement is handed to the worker in its prompt
(a scratchpad file, read-only; probe code, not production quality: no `unwrap`, `expect` or
panics are allowed in `memory-core`).

Ordered steps, one commit each; `cargo fmt`, clippy `-Dwarnings` and the focused tests green before
each commit:

1. `gitleaks.rs`: load, translate, compile and detect as decided. Unit or integration tests: the
   pinned sha256; 217 rules load and the 5 skipped ids are exactly the ones above; the brace
   translation; entropy at the boundary (`<=` skips); `secretGroup` and first-group selection;
   each allowlist target and the AND/OR conditions; stopwords; keyword prefilter; a table failure
   gives `None` (the caller maps it to `ScrubFailed`).
2. `scan::check` on the new table: own patterns, then gitleaks on the bytes, then on the
   unescaped copy; `ASSIGNMENT` and the shell guard removed; digest as decided. Update
   `tests/privacy_scan.rs`: the generic fixtures use high-entropy literal values (built by
   concatenation); add the 12 code-shaped lines of the synthetic set as values that pass byte for
   byte; a secret only visible after JSON unescaping is withheld; the proptests keep their meaning.
   Every changed expectation gets a comment naming this brief.
3. Capture and real-binary tests in `memory/tests` that relied on the removed rules: update with a
   comment naming this brief. Add one capture test: a file-history file with `token = getToken()`
   lands, one with a gitleaks-shaped literal is withheld `secret_in_text`.
4. Module docs (`scan.rs`, `gitleaks.rs`, the data directory's provenance in a short README:
   source URL, tag, sha256, how to update).

## Rules for the worker

- Cargo only via `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && …'`,
  one cargo job at a time. Combine nextest `-E` conditions into one expression. The consumer gate
  needs `MEMORY_REQUIRE_CONSUMER=1 MEMORY_ANALYTICS_PYTHON=/Users/mm/dev/memory/.analytics-venv/bin/python`.
- No `unsafe`, no new crate. No `git stash`. Never commit `.proptest-regressions`.
- Never touch `~/.local/state/memory*`, `~/.codex`, `~/.claude`, `~/.pi`, `~/Library`, the
  installed binary, `.env` or R2; do not list or read real agent directories. Use temporary
  directories. A production daemon runs on this Mac: do not inspect or signal it.
- Test secrets are built by concatenation so no literal secret is committed (the vendored gitleaks
  file is exempt: it is upstream data).
- No gate is loosened: no timeout raised, no test deleted or weakened, no coverage floor lowered.
- If a decision looks wrong or cannot be implemented cleanly, stop on that point, report it with the
  reason, and do not decide it yourself. Do not edit the brief, DESIGN.md or the handoff.
- Do not run `scripts/gates.sh`. Do not push.
- Report: commits, deviations with reasons, changed existing test expectations, exact commands with
  test counts, `git status --short`.

## Acceptance (parent)

Read the whole diff. Teeth: entropy check dropped; `<=` turned into `<`; keyword prefilter
inverted; global allowlist ignored; rule allowlists ignored; `regexTarget` ignored; stopwords
ignored; secret group ignored (whole match judged); the unescape pass dropped; own patterns
dropped; a table failure allowing instead of `ScrubFailed`; path-constrained rules skipped; the
sha256 pin removed. Then the full gates, the Linux container, a replay on both hosts with the
built scanner (counts only; compare with the measurements above), the hosted gates, evidence 45,
DESIGN (8, D61), the handoff; merge; install on both hosts; the first round's `landed=` and
`withheld=`, peak memory, `memory status`, `memory check`.

## Ledger

- 2026-11-22 12:10Z: brief written on `62f852f`.
