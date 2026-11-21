# 49. Slice 3, part 5: the remaining snapshot kinds behind the fail-closed scan (2026-11-21)

Coordinator brief. The parent session owns this file and acceptance. It follows D54 to D59 (briefs
45 to 48) and handoff item 24. Evidence goes to `evidence/43-deferred-snapshots/`. Text that states
a plan stays as written; later changes are marked.

Production daemons run on this Mac (launchd) and on dev-01 (systemd), both on the `87795f3` build,
while this brief is worked on. Nothing here touches `~/.local/state/memory*`, `~/.codex`,
`~/.claude`, `~/.pi`, `~/Library`, the installed binaries, agent settings, `.env` or R2.

## Why

DESIGN section 18 puts snapshot and config kinds in slice 3. After briefs 45 and 47 only Codex
stores and Claude file-history are captured; every other snapshot kind is counted
`deferred_snapshot` and never leaves the host (1,211 files on this Mac, 1,174 on dev-01). The
uncaptured-files review marked Claude per-session aux, Claude memory notes, Pi session aux and the
config assets "capture". The Claude memory notes are the longest-lived knowledge on either host.

Their current privacy profiles would not be safe to switch on as they are. A probe on `9884ac2`
ran the real `admit` and `transform` in memory over every deferred file on both hosts (counts only):

| Profile today | Kinds | Under that profile | Under `Scanned` |
|---|---|---|---|
| `OtherKnown` (no text check) | `ClaudeAux`, `PiAux` | every file publishes | Mac: 5 of 1,018 withheld (`SecretInText`). dev-01: 41 of 1,057 withheld (corrected at acceptance; first written as 71 of 1,072), 40 of them Pi `subagent-artifacts/*.jsonl` run logs (30 `SecretInText`, 10 over 1 MiB) |
| `TextAsset` (fails open on binary, non-UTF-8, over 1 MiB, code extensions) | `ClaudeMemory`, `ClaudePlan` | every file publishes | Mac: 3 of 127 withheld. dev-01: 4 of 52 |
| `Config` (document scrub, then the fail-open text check) | Claude, Codex and Pi config assets | every file publishes | 4 `png` (`Binary`), 3 `py` (`SecretInText`) in Codex's vendored `skills/.system/`, and 1 Pi extension `ts` per host |

D54 requires a fail-closed judgement for any raw copy whose content no path rule can see. The
`Scanned` profile (D58) is that judgement, with a per-fingerprint withheld record and a closed
reason, and D59 already covers its deletions.

## Facts checked on `9884ac2`

| Fact | Where |
|---|---|
| The walk yields `CodexStore` and `ClaudeFileHistory` and counts every other snapshot kind `deferred_snapshot` | `memory/src/resolve.rs` (`enumerate`, the `!matches!(kind, …)` arm) |
| `profile_of` maps config kinds to `Config`, `ClaudeMemory`/`CodexMemory`/`ClaudePlan` to `TextAsset`, the rest to `OtherKnown`; no captured source today has a `Config` or `TextAsset` profile | `memory/src/resolve.rs` (`profile_of`) |
| `transform` scrubs any recognized config document first, whatever the profile, then runs `scan::check` for `Scanned` | `memory-core/src/privacy/mod.rs` (`transform_owned`) |
| Append kinds with `OtherKnown` (`ClaudeToolResults`, `ClaudeDebug`, `ClaudeHistory`, `CodexHistory`, logs) are captured today and must not change | `memory/src/resolve.rs` |
| A `Scanned` snapshot gets the stat-time 1 MiB cap, the cached withheld record and `withheld=` counts; `check` prints one closed `withheld` line per file | `memory/src/capture.rs` (`step`), `memory/src/check.rs` |
| D59: deletions of any live snapshot entry; a withheld-only record is dropped after two misses | brief 48 |
| `apply_ignored` can turn a `CodexConfig` hit into `Ignored` | `memory-core/src/locator.rs` (`apply_ignored`, `codex_ignored`) |
| Codex `skills/.system/` is a pack Codex installs itself (Mac and dev-01: 49 files, 4 `png`, 8 `py`); one user skill file exists on this Mac | path shapes, counts only |
| No `ClaudeSessionEnv`, `ClaudeUsage`, `ClaudeTodos`, `ClaudeTasks`, `ClaudeTeams`, `CodexMemory` or `PiSnapshot` file was found by the walk on either host | census on `9884ac2` (Mac) and `89b155e` (dev-01) |

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Yielded | Every snapshot kind except `ClaudeSessionEnv`, `ClaudeUsage` and `ClaudePaste`. Those three stay `deferred_snapshot`: session-env holds environment values (it needs its own decision), usage-data is telemetry, and paste-cache is `Ignored` by D55 in agent dirs (a custom root keeps it deferred) |
| Profile | Every newly yielded snapshot kind gets `Profile::Scanned`: the config assets, `ClaudeMemory`, `CodexMemory`, `ClaudePlan`, `ClaudeAux`, `PiAux`, `ClaudeTodos`, `ClaudeTasks`, `ClaudeTeams`, `PiSnapshot`. Recognized config documents are still scrubbed first, then scanned. `CodexStore` keeps `OtherKnown`; no append kind changes profile. `Profile::Config` and `Profile::TextAsset` stay in `memory-core` unchanged (no code there changes) |
| Codex vendored skills | `skills/.system/**` in a Codex agent dir is `Ignored`, for the reason D54 gave for Claude `skills`. Other files under Codex `skills/` remain `CodexConfig` assets and are captured |
| Scan rules | Unchanged (`rules_digest` unchanged, `TRANSFORM_VERSION` unchanged). The open operator question on code-shaped false positives (evidence 41) applies here too and is not decided here |
| Interval | No snapshot interval: like file-history, a changed file is captured at the next full round or capture that walks it |
| Log, status, check | Unchanged vocabulary. `withheld=` and the `withheld` lines now include these kinds; `deferred_snapshot` drops to the three kinds above |
| Out of scope | Watcher routing of snapshot sources, recording withheld versions in R2, purging, any change to the scanner |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | SNAPKINDS | `rust/deferred-snapshots` | worker (Sonnet, medium) |

Owns `memory/src/resolve.rs`, doc comments in `memory/src/{check,status}.rs`, the `codex_ignored`
rule and its table in `memory-core/src/locator.rs`, `memory/tests/**`, `memory-core/tests/**`.
Read-only everywhere else; if another file must change, stop and report.

Ordered steps, one commit each; `cargo fmt`, clippy `-Dwarnings` and the focused tests green before
each commit:

1. Codex `skills/.system/**` is `Ignored` in an agent dir; a user skill beside it stays a
   `CodexConfig` asset; a custom root is not affected. Locator tests in `memory-core/tests/`.
2. `profile_of` and the walk: the kinds above are yielded with `Profile::Scanned`; session-env,
   usage-data and paste-cache stay `deferred_snapshot`. Update the existing tests that pin
   `deferred_snapshot` counts (`tests/resolve.rs`, `tests/s3_store/check.rs`
   `a_deferred_snapshot_is_counted_apart` moves to a session-env file) with a comment naming this
   brief. New walk tests: each yielded kind gets `Scanned`; every append kind keeps its profile.
3. Capture tests (`tests/capture.rs` or `tests/daemon.rs`): a Claude memory note, a session aux
   `json` and a `settings.json` land; a memory note holding a secret assignment is withheld with
   `secret_in_text` and not read again while unchanged; a `png` config asset is withheld `binary`;
   a 2 MiB Pi aux file is withheld `source_limit` without a read; a `settings.json` with a token
   field lands scrubbed (the token value is not in the stored bytes).
4. Real binary against the fake S3 store (`tests/s3_store/`): one Claude session with a transcript,
   a session aux `json` and a memory note in the project, plus `settings.json` in the Claude root.
   One reconcile lands all of them; `memory check` exits 0 with `uncaptured=0`, `mismatch=0` and
   `deferred_snapshot=0`; `memory restore` of the session and of the shared Claude owner writes
   each file with equal bytes (scrubbed `settings.json` equal to the transformed bytes); `memory
   export` of the session exits 0 and the consumer gate test still passes.
5. Module docs (`resolve.rs`, locator ignore table comment).

## Rules for the worker

- Cargo only via `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && …'`,
  one cargo job at a time. Combine nextest `-E` conditions into one expression. The consumer gate
  needs `MEMORY_REQUIRE_CONSUMER=1 MEMORY_ANALYTICS_PYTHON=/Users/mm/dev/memory/.analytics-venv/bin/python`.
- No `unsafe`, no new crate. No `git stash`. Never commit `.proptest-regressions`.
- Never touch `~/.local/state/memory*`, `~/.codex`, `~/.claude`, `~/.pi`, `~/Library`, the
  installed binary, `.env` or R2; do not list or read real agent directories. Use temporary
  directories. A production daemon runs on this Mac: do not inspect or signal it.
- Test secrets are built by concatenation (as in brief 47's tests) so no literal secret is
  committed.
- No gate is loosened: no timeout raised, no test deleted or weakened, no coverage floor lowered.
  An existing test whose expectation changes because of a decision above is updated with a comment
  naming this brief; list each one in the report.
- If a decision looks wrong or cannot be implemented cleanly, stop on that point, report it with the
  reason, and do not decide it yourself. Do not edit the brief, DESIGN.md or the handoff.
- Do not run `scripts/gates.sh`. Do not push.
- Report: commits, deviations with reasons, changed existing test expectations, exact commands with
  test counts, `git status --short`.

## Acceptance (parent)

Read the whole diff. Teeth: a newly yielded kind left deferred; a newly yielded kind given its old
profile (`Config`, `TextAsset`, `OtherKnown`); an append kind moved to `Scanned`; session-env
yielded; Codex `.system` skills captured; a user Codex skill ignored; the scrub skipped for a
`Scanned` config document. Then the full gates once, the Linux container run, the hosted gates,
evidence 43, DESIGN (8, 9, 18, D60), the handoff; merge; install on this Mac and dev-01; before and
after `memory status` counts, the first round's `landed=`/`withheld=`, the peak memory, `memory
check`, and a restore of the shared Claude owner compared by SHA-256 with `transform(local)`.

## Ledger

- 2026-11-21 13:30Z: brief written on `9884ac2`.
- 2026-11-21: SNAPKINDS accepted locally at `b9ef539` (5 worker commits, 1 parent commit). The
  parent found that no test pinned Pi aux at `Scanned` and added a Pi walk to the profile test
  (`b9ef539`). Teeth 12 of 12; gates 14/14, seed 1051600503, nextest 887, coverage 95.58 %; Linux
  container core_locator, capture, daemon and resolve passed.
  [Evidence 43](evidence/43-deferred-snapshots/). Next: hosted gates, merge, install on both
  hosts, before and after counts, `memory check` and a restore of the shared Claude owner.
