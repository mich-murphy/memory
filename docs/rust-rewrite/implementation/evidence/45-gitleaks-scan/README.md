# Evidence 45: the content scan on the gitleaks rule set (brief 50)

All dates are UTC, 2026-11-22. Status: increment GITLEAKS accepted locally at `cd6910d`: 5 worker
commits and 4 parent commits over the brief `663441d`, which sits on `main` `62f852f`. During the
work nothing was installed, nothing was sent to R2, and the worker read no production state root
or real agent data. Both production daemons kept running on `30002d9`.

[Brief 50](../../50-gitleaks-scan.md) holds the research summary and the measurements on `62f852f`
that led to the decisions.

## Commits

| Commit | By | Change |
|---|---|---|
| `b91d461` | worker | `privacy/gitleaks.rs`: the vendored v8.30.1 file embedded with `include_str!`, parsed with `toml`, compiled with `regex::bytes` (Unicode off, size limit 128 MiB per pattern); Go-brace translation; port of `detectRule` (keyword prefilter, every match, newline trim, secret group or first non-empty group, entropy `<=` skips, global then rule allowlists with `regexTarget`, OR/AND and stopwords; `paths` never satisfied; `gitleaks:allow` not honoured). The 4 path-and-regex rules apply to every file; the path-only `pkcs12-file` is skipped. The file's sha256 is pinned by a test |
| `25f7e9a` | worker | `scan::check`: own patterns, then gitleaks on the bytes, then both again on a JSON-unescaped copy if the bytes hold a backslash. The generic `ASSIGNMENT` rule and the `text::check` shell guard leave the scan (`text.rs` unchanged). `TABLE_VERSION` `scan/2`; the digest covers the vendored file and the escape pass |
| `1677224` | worker | Real binary against the fake S3 store: a file-history file with `token = getToken()` lands, one with a high-entropy generic literal is withheld `secret_in_text` and is not restored |
| `5eeabe5` | worker | Provenance README for the data directory (source, tag, sha256, update steps) |
| `de345f9` | worker | The `rules_digest` golden moved by design (`privacy_port`) |
| `c4e55f1` | parent finding | An allowlist without a check is a table failure (gitleaks `Validate`); without it an empty `condition = "AND"` allowlist allowed every finding. `commits`, like `paths`, is a check that is never satisfied. Unknown top-level, rule and allowlist keys are table failures, so a semantic field added upstream (as `required` was) cannot be ignored silently. The leftover `expect(dead_code)` on `mod gitleaks`, whose reason was no longer true, moved to the one field only tests read |
| `daff8a7` | parent finding | The tooth "a table failure allows" survived: the vendored table always loads. The verdict is now `judge(bytes, tables)`, and a unit test passes `None` and expects `ScrubFailed` |
| `d9cdd73` | parent finding | The Linux container failed both file-history tests (`lock_held`, `state_io`): the new test used the same rig label as the existing one, `Rig::new` keys its directory by pid and label, and `cargo test` runs tests as threads of one process (nextest on the Mac runs one process per test). The new test has its own label |
| `cd6910d` | parent finding | Own provider patterns start at a word boundary (`\b`), as gitleaks' provider rules do. The first replay with the built scanner withheld 2 files here and 17 on dev-01 only through the own `sk-` pattern (and 1 on dev-01 through the own Google pattern); every one of those hits sat inside a longer identifier (`…task-…`), none at a word start. A test pins both sides (embedded prefixes pass, the same shapes at a word start are withheld); the digest golden moved |

Changed existing test expectations, each with a comment naming brief 50:
`privacy_scan.rs::every_pattern_is_withheld` and `inserted_secret_is_withheld` (the generic
fixtures carry a high-entropy literal built from parts instead of `literalvalue123`),
`an_assignment_nested_in_a_value_is_withheld` (same value),
`the_shell_guard_catches_short_and_fish_assignments` (replaced by
`code_shaped_lines_pass_byte_for_byte`: 12 code lines, including `export DB_PASSWORD=abc12` and
fish's `set -gx API_TOKEN literalvalue123`, now pass byte for byte), and
`privacy_port.rs::rule_digest_golden_requires_explicit_semantic_review` (golden digest). New:
`a_secret_inside_json_escapes_is_withheld`, `provider_prefixes_inside_an_identifier_pass` (parent).
No `memory/tests` expectation changed.

## Parent acceptance

- Whole diff read (two findings fixed in `c4e55f1`, see above).
- Teeth, 29 of 29 caught (`gitleaks_teeth.py`, `memory-core` filter
  `test(/gitleaks::|scan::/) or binary(privacy_scan) or binary(privacy_port)`, baseline 45):
  entropy check dropped; `<=` turned into `<`; keyword prefilter inverted, dropped, made
  case-sensitive; global allowlist ignored; rule allowlists ignored; `regexTarget` ignored;
  stopwords ignored; secret group ignored; first non-empty group ignored; AND read as OR; the
  never-satisfied `paths`/`commits` check dropped; empty allowlist accepted; unknown keys accepted;
  path-constrained rules skipped; newline trim dropped; Go-brace translation dropped; vendored
  table `None`; unescape pass dropped; `\"` not unescaped; own patterns dropped; a table failure
  allowing (survived at first, caught after `daff8a7`); the vendored file left out of the digest;
  the escape pass left out of the digest; the vendored file edited; the word boundary dropped from
  the own `sk-`, Google and GitHub patterns (3).
- Full gates on `daff8a7`: 14 of 14, seed 3696762941, nextest 905. Again on `cd6910d`: 14 of 14,
  seed 3449721935, nextest 906 passed, Hegel 33, proptest 36, consumer 3, line coverage 95.64 %.
- Linux container (Ubuntu 24.04): on `daff8a7` both file-history tests failed (fixed in
  `d9cdd73`, above); on `cd6910d` `memory-core` lib 17, `privacy_scan` 13, `privacy_port` 16,
  `memory` `s3_store` 134 (1 ignored), all passed.
- `cargo deny` (licences, bans, advisories, sources) passes; no new crate. The vendored file is
  MIT data.

## Replay with the built scanner (counts and public rule ids only)

A scratch crate linked against the worktree crates ran the real `admit` and `transform` (now
`scan/2`) in memory over every `Profile::Scanned` file on each host, and compared each verdict
with the parent's independent probe port (gitleaks plus own patterns). Nothing was written;
no name, path or content was printed.

| | This Mac | dev-01 |
|---|---|---|
| Scanned files judged | 2,108 | 1,639 |
| Withheld today (`30002d9`) | 150 | 141 (10 of them `source_limit`) |
| Withheld by the built scanner (`cd6910d`) | 3: file_history 2, claude.memory 1 (`secret_in_text`) | 26: file_history 4, pi.aux 12 (`secret_in_text`); pi.aux 10 (`source_limit`) |
| Differences from the probe | 0 | 5 pi.aux, all explained: the probe's replay mode does not unescape; its unescape mode finds the same 5 (`curl-auth-user` 1, `generic-api-key` 4) |
| Findings by rule id | `generic-api-key` 2, own URL credential 1 | `generic-api-key` 1, own JWT 1, own URL credential 9, plus the 5 above |

Before `cd6910d` the built scanner withheld 5 here and 38 on dev-01; the difference is the
unanchored own `sk-` and Google hits inside identifiers (see the commit table).

The values released on both hosts were classed before the brief (evidence in brief 50): code
expressions, file paths, URLs without credentials and identifier text without digits; none is a
literal secret by shape, entropy or character class.

## Merge, install on both hosts

- Stack #246 (PRs #243 to #245) merged as `6510ff7`. Rust gates passed on all three PRs; CI failed
  only in the two accepted `govulncheck` steps.
- This Mac: installed in place at 03:52:38Z (sha256 prefix `13dce21b40dde2cb`, `runs = 14`), with
  the `30002d9` binary kept as `backup/memory-30002d9`. Start round at 04:04:00Z: `landed=198
  failed=0 local=0 deleted=0 withheld=3`, `tmp/` empty, no WARN or ERROR line. `memory status`:
  total 8,179, current 8,174, `withheld` 150 → 3, `ignored` 20,220, `deferred_snapshot` 0.
  `memory check` 04:17Z to 04:42Z: exit 0, 1,170 owners, 8,179 sources, 8,176 match, 0 mismatch,
  0 uncaptured, `remote_only=0`, `store_error=0`; withheld file_history 2, claude.memory 1 (all
  `secret_in_text`), as the replay predicted.
- dev-01: `6510ff7` built on the host (sha256 prefix `4cb5e71ac89aa88d`); both Nix store
  dependencies are the pinned GC roots. Installed at 04:07:24Z with `backup/memory-30002d9`. Start
  round: `landed=57 failed=0 local=0 deleted=0 withheld=26`, `VmHWM` 109,264 kB, `NRestarts=0`,
  `tmp/` empty, no WARN or ERROR line. `memory check` 04:12Z to 04:17Z: exit 0, 454 owners, 5,970
  sources, 5,944 match, all other counts 0; withheld file_history 4, pi.aux 12 (`secret_in_text`),
  pi.aux 10 (`source_limit`), as the replay predicted.

### Finding: memory and CPU of the new scan

The Mac daemon's footprint was 99 MB in the start round but reached 130 MB at 04:19:13Z, in the
first timer round, above the 128 MB backlog peak of section 15 (earlier installs: 88 to 105 MB).
A scratch crate judged the same 2,108 scanned files on this Mac with the real `admit` and
`transform` (one thread, as the daemon judges; counts only):

| Build | Resident added after the walk | CPU |
|---|---|---|
| `30002d9` (old scan) | +2.6 MB | 0.6 s |
| `6510ff7` (this brief) | +21.9 MB: 10.7 MB compiled table, 11.3 MB match caches | 11.8 s |

Variants measured in a scratch build of `6510ff7`: the keyword prefilter through
`memchr::memmem` takes the CPU to 0.6 to 0.9 s with no change in memory; compiling a rule only
when one of its keywords occurs (64 of 221 rules ever compile here) takes the added memory to
15.7 MB; a 256 KiB lazy-DFA cache per gitleaks pattern takes it to 12.8 MB (1.0 s). Every variant
gives the same 3 withholds. Fixed in brief 51.

## Limits

- gitleaks' base64, hex and percent decoding passes are not ported. A secret that is only visible
  after such a decoding is not found (JSON string escapes are undone).
- A literal password with characters outside gitleaks' generic value class (for example `&`) is
  not found by the generic rule; own provider patterns, PEM headers and URL credentials still
  apply.
- AWS's documented example key is allowlisted by gitleaks on purpose and publishes.
- `gitleaks:allow` is never honoured, and path and commit allowlists never apply, so the scan can
  only be stricter than gitleaks on these points.
- The digest change re-judges every scanned file once after install.
