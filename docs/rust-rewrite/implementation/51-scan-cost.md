# 51. Slice 3, part 7: the cost of the gitleaks scan (2026-11-22)

Coordinator brief. The parent session owns this file and acceptance. It follows D61 (brief 50) and
the finding in evidence 45. Evidence goes to `evidence/46-scan-cost/`. Text that states a plan
stays as written; later changes are marked.

Production daemons run on this Mac (launchd) and on dev-01 (systemd), both on the `6510ff7` build,
while this brief is worked on. Nothing here touches `~/.local/state/memory*`, `~/.codex`,
`~/.claude`, `~/.pi`, `~/Library`, the installed binaries, agent settings, `.env` or R2.

## Why

After brief 50 was installed, the Mac daemon's footprint reached 130 MB in its first timer round,
above the 128 MB backlog peak of DESIGN section 15. Measured on this Mac over the 2,108 scanned
files, one thread (the daemon judges on one thread), with the real `admit` and `transform`:

| Build or variant | Resident added after the walk | CPU |
|---|---|---|
| `30002d9` (old scan) | +2.6 MB | 0.6 s |
| `6510ff7` as merged | +21.9 MB (10.7 MB compiled table, 11.3 MB match caches) | 11.8 s |
| keyword prefilter through `memchr::memmem` | +21.9 MB | 0.6 to 0.9 s |
| plus compile a rule at its first keyword hit (64 of 221 rules ever compile) | +15.7 MB | 0.6 s |
| plus a 256 KiB lazy-DFA cache per gitleaks pattern | +12.8 MB | 1.0 s |

Every variant gives the same 3 withholds. The slow part is `contains`, a byte-window loop run for
each of about 290 keywords over each file. Most memory is rules compiled for keywords that never
occur.

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Keyword prefilter | `memchr::memmem::find` over the lowercased text, as measured. `memchr` 2 becomes a direct dependency of `memory-core`; it is already in `Cargo.lock` (through `regex`), so no new crate enters the graph. Added to `[workspace.dependencies]` |
| Lazy compile | At load every rule pattern and allowlist pattern is compiled once to validate it (a failure is still a table failure, `scrub_failed` for every file), then the rule regex is dropped. A rule's regex is compiled again at the first text whose keywords match and kept in a `OnceLock` in the rule. A rule without keywords compiles on its first use. Allowlist regexes stay compiled from load (few, small) |
| A lazy compile that fails | Cannot happen for a validated pattern, but is handled: the scan returns an error and the file is withheld with `ScrubFailed`, never published. `Table::detect` returns a closed result (found, clean, failed) instead of `bool` |
| Lazy-DFA cache | `dfa_size_limit` 256 KiB per gitleaks rule regex (`RegexBuilder::dfa_size_limit`); own patterns unchanged |
| Verdicts and digest | Unchanged: same findings for every input, so `TABLE_VERSION` stays `scan/2` and `rules_digest` does not move (the golden test is not edited). The dfa limit and laziness are not semantic; nothing is re-judged after install |
| Out of scope | Rule semantics, own patterns, the unescape pass, other profiles, threads |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | SCANCOST | `rust/scan-memory` | worker (Sonnet, medium) |

Owns `memory-core/src/privacy/{gitleaks,scan}.rs`, `memory-core/Cargo.toml`, `rust/Cargo.toml`
(`[workspace.dependencies]` only), `rust/Cargo.lock` (only the dependency edge),
`memory-core/tests/**`. Read-only everywhere else; if another file must change, stop and report.

A scratch diff written by the parent for the measurement is handed to the worker in its prompt
(behaviour reference only; it reads environment variables and is not production quality).

Ordered steps, one commit each; `cargo fmt`, clippy `-Dwarnings` and the focused tests green before
each commit:

1. Prefilter through `memmem`. Tests: a keyword at the start, the end and across case; an empty
   keyword never matches; a rule whose keywords are absent never runs its regex (count it through
   a test-only hook or by the lazy state in step 2).
2. Lazy compile with load-time validation, the closed detect result, `ScrubFailed` on a lazy
   failure, the 256 KiB DFA limit. Tests: after load no rule regex is held; after a text with one
   keyword only the rules with that keyword hold a regex; a pattern that compiles at load but is
   forced to fail lazily (a test-only constructor) withholds `ScrubFailed`; every existing
   `gitleaks::` and `privacy_scan` test passes unchanged; the digest golden is unchanged.
3. Module docs (`gitleaks.rs`: when a rule compiles, the DFA limit, why).

## Rules for the worker

- Cargo only via `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && …'`,
  one cargo job at a time. Combine nextest `-E` conditions into one expression. The consumer gate
  needs `MEMORY_REQUIRE_CONSUMER=1 MEMORY_ANALYTICS_PYTHON=/Users/mm/dev/memory/.analytics-venv/bin/python`.
- No `unsafe`, no crate outside `Cargo.lock` today, no `unwrap`, `expect` or panic in `memory-core`
  src. No `git stash`. Never commit `.proptest-regressions`.
- Never touch `~/.local/state/memory*`, `~/.codex`, `~/.claude`, `~/.pi`, `~/Library`, the
  installed binary, `.env` or R2; do not list or read real agent directories. Use temporary
  directories. A production daemon runs on this Mac: do not inspect or signal it.
- No gate is loosened: no timeout raised, no test deleted or weakened, no coverage floor lowered.
  No existing test expectation changes; if one must, stop and report.
- If a decision looks wrong or cannot be implemented cleanly, stop on that point, report it with the
  reason, and do not decide it yourself. Do not edit the brief, DESIGN.md or the handoff.
- Do not run `scripts/gates.sh`. Do not push.
- Report: commits, deviations with reasons, exact commands with test counts, `git status --short`.

## Acceptance (parent)

Read the whole diff. Teeth: prefilter inverted or dropped; regex compiled eagerly (held after
load); a lazy failure publishing; the DFA limit dropped (measured, not a test); the digest moved.
Then the full gates, the Linux container, the scratch measurement on this Mac against the table
above (target: at most +13 MB and 1.5 s, the same 3 withholds), a replay of verdicts on both hosts
(must equal `6510ff7` file for file), the hosted gates, evidence 46, the handoff; merge; install on
both hosts; the daemon's peak footprint over a full `memory check` on this Mac and `VmHWM` on
dev-01, under 128 MB.

## Ledger

- 2026-11-22 05:00Z: brief written on `6510ff7`.
- 2026-11-22: SCANCOST accepted locally at `1b1d5e8` (3 worker commits). The worker's DFA cap also
  reaches the allowlist regexes (accepted: not semantic). Teeth 10 of 10; gates 14/14, seed
  3088399108, nextest 911, coverage 95.64 %; Linux container passed. Verdicts equal `6510ff7` file
  for file on both hosts; this Mac +22.0 → +12.6 MB and 12 → 1.0 s, dev-01 +29.6 → +19.6 MB and
  43.5 → 6.0 s. [Evidence 46](evidence/46-scan-cost/). Next: hosted gates, merge, install on both
  hosts, the daemon's peak below 128 MB.
- 2026-11-22 06:11Z to 06:51Z: merged as `ecb1d74` (stack #250) and installed on this Mac and
  dev-01. No source judged again; `memory check` exit 0 on both with the same withholds (3, 26).
  Peak: this Mac 82 MB over a full `memory check` and two timer rounds (was 130 MB), dev-01
  `VmHWM` 109 MB. Exit criteria met. Details in evidence 46.
