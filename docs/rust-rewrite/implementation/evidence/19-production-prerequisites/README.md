# Evidence 19: production prerequisites (brief 28)

Dates: work up to `0d28378` and the gates on `b37556e` and `7f99613` ran on 2026-11-12. The
parent's AUDIT-3 acceptance, `2edf2f8` and the final gates ran on 2026-11-16. Everything here
is local evidence except the two sections on hosted CI ("Hosted macOS job time on stack #111"
and "Stack #111 merge"), which were added the same day as the hosted runs finished. No R2 run
happened in brief 28, and the daemon still refuses an empty key prefix.

[Brief 28](../../28-production-prerequisites.md) sits on `e11db32`, the top of stack #103
(merged into `main` as `39944d7` on 2026-11-12; `git diff e11db32 origin/main` was empty).
The stack was pushed on 2026-11-16 as stack #111 (PRs #104 to #110; #112 and #113 were added
on top the same day, each with the same check and 0 matches). Pre-push check of
`e11db32..2edf2f8` (27 commits): 0 matches for the secret, the key id, the endpoint, the host
and the account.

| Branch | Tip | PR | Commits | Diff over the branch below | Content |
|---|---|---|---|---|---|
| `docs/r5-brief` | `368bc4a` | #104 | 1 | 1 file, +208 | Brief 28 as first written |
| `rust/coverage-per-process` | `00e91ab` | #105 | 1 | 1 file, +5/-1 | Gate 08 through nextest |
| `rust/hint-retention` | `8fe5bfa` | #106 | 4 | 6 files, +548/-14 | HINT-1 |
| `rust/log-stderr` | `ec5c0cc` | #107 | 3 | 12 files, +529/-15 | LOG-1 |
| `rust/audit` | `c9941ff` | #108 | 7 | 11 files, +1440/-48 | AUDIT-1, AUDIT-2 |
| `rust/log-events` | `7f99613` | #109 | 3 | 7 files, +536/-102 | LOG-2 |
| `rust/audit-park` | `2edf2f8` | #110 | 8 | 14 files, +890/-79 | AUDIT-3 |
| `docs/production-prerequisites` | `00af49e` | #112 | 6 | 19 files, +1073/-34 | Brief 28 result, evidence 19, handoff, gates entry |
| `rust/ci-coverage-job` | `538279e` | #113 | 2 | 5 files, +71/-6 | Hosted CI runs coverage as its own job per OS |

Code over `368bc4a`: 28 files, +3877/-188. The lockfile gains 62 lines (7 packages, LOG-1
only); `rust/Cargo.toml` +4; `memory/Cargo.toml` +2; `memory-core/deny.toml` +1;
`rust/scripts/gates.sh` +5/-1. Nothing else changes outside `rust/crates`.

## Commits

`e11db32..2edf2f8`, 27 commits, subjects from `git log`. HINT-1, LOG-1, AUDIT-1 and LOG-2 were
accepted on tips before a rebase (HINT-1 as `522c28e` and `aac52da`, LOG-1 as `de94dcd` and
`a204f90`, AUDIT-1 as `ab13d1b`, LOG-2 as `a398d14`); the hashes below are the rebased ones.
`85b93e3` contains one parent edit: the two hint keys added to the `round` line.

| Increment | Commit | By | Change |
|---|---|---|---|
| Brief | `368bc4a` | parent | brief 28, prerequisites of the first production write (logs, hint retention, audit) |
| Gate | `00e91ab` | parent | the coverage gate runs through nextest, one process per test |
| HINT-1 | `a4bc501` | worker | hint day files are listed, read with a bound and removed through `StateDir` |
| HINT-1 | `8c996fa` | worker | hint day files past 7 days are removed once consumed, with a pure retention predicate |
| HINT-1 | `28cb57f` | worker | retention table covers a torn tail on its own (`memory-core`) |
| HINT-1 | `8fe5bfa` | worker | daemon test keeps an old hint file longer than the read bound |
| LOG-1 | `983bcc3` | worker | add `tracing` and `tracing-subscriber` for the daemon's stderr log |
| LOG-1 | `85b93e3` | worker, one parent edit | log start, round and stop events to stderr through one module |
| LOG-1 | `ec5c0cc` | worker | write only this module's log events and never panic on a failed log write |
| AUDIT-1 | `c64b10e` | worker | capture produces audit evidence, keeps audit state and stores stat refreshes |
| AUDIT-1 | `28949c8` | worker | audit tests through the daemon (due rule, recheck, between-windows rewrite, refresh) |
| AUDIT-1 | `b37556e` | worker | audit test for the cheap stat pre-check |
| AUDIT-2 | `8a9b838` | worker | audit counts in capture, round report, status and the round log line |
| AUDIT-2 | `ef3bda3` | worker | byte-count test for the audit over a 200 KiB source |
| AUDIT-2 | `22af43e` | worker | property for rewrites between the windows, found by the audit's turn |
| AUDIT-2 | `c9941ff` | parent | capture test pins the mismatch count of a non-resumed prefix check |
| LOG-2 | `2acbbd4` | worker | log `state_load`, `parked` and host pause events, drop the status `events` block |
| LOG-2 | `e955438` | worker | check `state_load` lines on the real daemon |
| LOG-2 | `7f99613` | parent | wrap the log grammar doc line |
| AUDIT-3 | `ddca6fa` | worker | park a source at repeated audit mismatches by the core rule |
| AUDIT-3 | `0833d11` | worker | park one source of an owner and log its scope |
| AUDIT-3 | `78bde46` | worker | test the source park rule, the daemon park and its recovery |
| AUDIT-3 | `32b00ca` | worker | test the prefix-site park and the non-prefix-stable restart |
| AUDIT-3 | `bc3e3ac` | worker | test that a parked source costs no reads or stats |
| AUDIT-3 | `b9956b5` | worker | model the source park in the capture property |
| AUDIT-3 | `0d28378` | worker | reach the prefix site with a source that is not prefix-stable |
| AUDIT-3 | `2edf2f8` | parent | copy an owner's source list only when one of its sources is parked; audit count and log docs |

## What the binary does now, beyond `e11db32`

Implemented, with the commits above:

- It logs to stderr through `tracing`: `started`, `state_load`, `round` (16 keys), `parked`,
  `host_paused`, `host_resumed`, `stopped`.
- Hint day files older than 7 days are removed once consumed. The kept-old count is reported
  and logged each round.
- Append sources are audited. A mismatch is rechecked, a stable mismatch starts a new epoch,
  and the third consecutive failure at least 3 h after the first parks the source alone.
- A non-resumed extension verifies its re-read prefix against the committed digest.
- `status.json` gains `audit` counts, loses the `events` block, and its parked entries may
  carry a `source`.

Unchanged: the loop is the safety reconcile only (5 s to 60 s startup delay, then every 15
minutes). The watcher and scheduler are not connected, a stop is seen between rounds, and the
daemon refuses an empty prefix (`config_prefix_required`).

## Gate 08 and the hosted coverage failure

PR #101 (stack #103), run 43193745120, job `macos-26`, gate `08-coverage`:
`hegel_daemon_state_machine` with `HEGEL_SEED=2091885382`, 500 cases, reported "unconfirmed
failure: failed 0 of 10 replays". The same seed passed in `05-hegel` of the same run (nextest,
one process per test). An earlier failure, run 42565602976 (seed 938868371), had the same
symptom and was recorded as not root-caused.

Cause: `second_process_exits_lock_held` in `tests/daemon.rs` spawns a child inside the test
process under `cargo test`. Between fork and exec the child holds a copy of every open
descriptor, so a state-machine restart that drops and retakes `flock(daemon.lock)` sees
`lock_held`. Both tests are on `origin/main` unchanged by stack #103.

| Reproduction | Result |
|---|---|
| Isolated, 200,000 drop and re-acquire iterations, no spawns | 0 `lock_held` |
| Isolated, a thread spawning `/usr/bin/true` (about 2,300 children) | 142 and 144 `lock_held` |
| In the tree, spawn amplified x4000 (reverted), 3 runs | 2 fail with `DeleteIndex: Err("lock_held")` at `tests/daemon.rs:1825`; one printed the hosted message verbatim |
| In the tree, hosted seed unmodified, 12 runs | 12 pass |

Fix: `00e91ab`, gate 08 runs `cargo llvm-cov nextest`, one process per test. The rerun of run
43193745120 succeeded on attempt 2 on all three jobs. The first run of gate 08 through nextest
took 262 s; `hegel_daemon_state_machine` passed in 31.4 s.

## Hosted macOS job time on stack #111

Added on 2026-11-16 after the record above.

On stack #111 the `Rust (macos-26)` job of "Rust rewrite gates" reached its 30-minute limit
twice. No gate failed in either run.

| PR | Head | Run | macOS job | Result |
|---|---|---|---|---|
| #104 | `368bc4a` | 43865483154 | 23.8 min | success |
| #105 | `00e91ab` | 43865501341 | 23.9 min | success |
| #106 | `8fe5bfa` | 43865523893 | 22.6 min | success |
| #107 | `ec5c0cc` | 43865542471 | 26.0 min | success |
| #108 | `c9941ff` | 43865560855 | 26.2 min | success |
| #109 | `7f99613` | 43865576080, attempt 1 | 30.4 min | cancelled at the limit inside `08-coverage`; the 11 gates before it were 0 |
| #109 | `7f99613` | 43865576080, attempt 2 | 26.1 min | success |
| #110 | `2edf2f8` | 43865589662, attempt 1 | 30.0 min | cancelled at the limit after the gates step succeeded; all 13 gates 0 in the uploaded `status.tsv`, seed 1749040497 |
| #110 | `2edf2f8` | 43865589662, attempt 2 | 28.6 min | success |
| #112 | `00af49e` | 43867816396 | 26.1 min | success |

Seconds per gate on the macOS runner, from the timestamps of the status lines in the job logs
(a gate's time is the time since the previous gate finished; gates under 10 s are left out):

| Run | 02 clippy | 04 nextest | 05 hegel | 06 proptest | 06 window | 06 mutations | 08 coverage | All gates |
|---|---|---|---|---|---|---|---|---|
| `main` `39944d7`, 43217491627 | 76 | 187 | 197 | 149 | 96 | 64 | 512 | 1,303 |
| #104 | 87 | 171 | 194 | 139 | 108 | 72 | 501 | 1,295 |
| #105 | 77 | 161 | 208 | 159 | 90 | 72 | 501 | 1,289 |
| #108 | 55 | 172 | 214 | 173 | 109 | 88 | 645 | 1,483 |
| #109, attempt 1 | 92 | 229 | 368 | 177 | 142 | 81 | not finished | 1,111 before `08` |
| #110, attempt 1 | 90 | 220 | 297 | 180 | 132 | 89 | 626 | 1,661 |

The job also spends about 1.5 to 2 minutes before the gates step (checkout, Nix, the cached
`cargo-llvm-cov`). The same commit varies by runner: `05-hegel` took 204 s to 369 s on the two
Linux runners for #109 and #110. From #107 up the macOS job took 26.0 to 30.4 minutes, 87 % or
more of its limit (the top of the stack has 583 tests against 519 on `main`), so a rerun alone
does not make it reliable.

Fix: `e599100` on `rust/ci-coverage-job`. `rust/scripts/gates.sh` takes `GATES_PART` (`all` by
default, `gates`, `coverage`); the hosted workflow runs `gates` and `coverage` as separate
jobs per OS. No gate is dropped and the 30-minute job limit is unchanged, but one OS may now
use two 30-minute jobs where it had one, which is more total time than before. Checked
locally: `bash -n`, `shellcheck` and `actionlint` exit 0; with every command stubbed the
parts select 13, 12 and 1 gates; an unknown value exits 2. Not checked locally: a real run of
either part.

First hosted runs of the split, in minutes per job (PR #113 is run 43878896700; the post-merge
run on `main` `d765f9c` is 43887779155):

| Job | PR #113 | Post-merge |
|---|---|---|
| Rust gates (ubuntu-24.04) | 9.8 | 9.4 |
| Rust gates (ubuntu-24.04-arm) | 14.0 | 14.0 |
| Rust gates (macos-26) | 17.4 | 16.6 |
| Rust coverage (ubuntu-24.04) | 7.7 | 7.2 |
| Rust coverage (ubuntu-24.04-arm) | 10.0 | 10.5 |
| Rust coverage (macos-26) | 12.1 | 15.0 |

The longest job is now 17.4 minutes against the 30-minute limit. On PR #113 the
`Rust coverage (macos-26)` job of attempt 1 never started: it was cancelled after 15.0 minutes
in the queue with no steps and the annotation "The job was not acquired by Runner of type
hosted even after multiple attempts". The parent reran that job once; the 12.1 minutes are
attempt 2. The split does not prevent that kind of failure, and it can now happen to any of
six jobs per run where there were three.

The parent reran the cancelled macOS job of #109 and #110 once (`gh run rerun <id> --failed`).
Attempt 2 succeeded for both, at 26.1 and 28.6 minutes, so both still ran as one job close to
the limit.

## Acceptance per increment (parent)

The seven checks are fmt, clippy `-Dwarnings`, rustdoc `-Dwarnings`, nextest, doctests,
`cargo xtask schemas --check` and `cargo deny --locked check`. Mutants are in
[`teeth.txt`](teeth.txt). Every mutation predicate occurs exactly once on every tip checked.

| Increment | Tip checked | Seven checks | Tests | Mutants |
|---|---|---|---|---|
| HINT-1 | `522c28e` (mutants on `aac52da`) | 0 | 529 passed, 4 skipped | 8, all killed |
| LOG-1 | `a204f90` | 0 (`cargo deny` ok) | 526 passed, 4 skipped | 5, all killed |
| AUDIT-1 | `ab13d1b` | 0 | 532 passed, 4 skipped | 8, all killed |
| AUDIT-2 | `22af43e` | 0 | 559 passed, 4 skipped | 12; 11 killed on the first run, 1 survivor fixed by `c9941ff` |
| LOG-2 | `a398d14` | 0 | 551 passed, 4 skipped | 8, all killed |
| AUDIT-3 | `2edf2f8` | 0 | 583 passed, 4 skipped | 15, all killed |

### HINT-1

Mutants and the tests that fail: `boundary` (>= 7) `day_boundary_table`, `facts_table`;
`today` 6 tests; `offset` (<=) `facts_table` and 2 daemon tests; `torn` `facts_table`;
`len` (<=) `facts_table`; `cut` (`whole: true`) daemon
`old_hint_file_longer_than_the_read_bound_is_kept`; `failed-removal`
`failed_hint_removal_keeps_the_entry_and_is_retried`; `stale-entry`
`stale_hint_offset_entry_is_dropped`.

The read cut: `state.rs` truncates to the bound and `daemon.rs` takes the length from the bytes
read, so only the `whole` flag keeps a file whose first 64 MiB end on a line boundary (the
tests take about 1.5 s).

### LOG-1

Pins `tracing` `=0.1.44` (std) and `tracing-subscriber` `=0.3.23` (fmt, std). The lockfile gains
7 packages: `tracing`, `tracing-core`, `tracing-subscriber`, `sharded-slab` (MIT) and
`lazy_static`, `pin-project-lite`, `thread_local` (MIT OR Apache-2.0). The stripped release
binary grows from 6,318,544 to 6,438,384 bytes (+119,840). `MEMORY_SIM_TIMING` is absent from
the release binary.

Hardening in `a204f90`: events from another target write nothing, and
`log_internal_errors(false)` is set (the builder default is true, which prints with `eprintln!`
and panics on an unwritable stderr, `tracing-subscriber` `fmt/mod.rs:473`,
`fmt_layer.rs:1049-1063`).

Mutants: `foreign` log `an_event_from_another_module_writes_nothing`; `internal-errors`
harness `a_closed_stderr_does_not_stop_the_daemon`; `no-stopped` 7 tests; `path-in-started` 8
tests; `early-start` 4 refusal tests.

### AUDIT-1 and the between-windows bug

Hypothesis from the brief, reproduced before the fix: a non-resumed extension after a rewrite
between the windows committed a digest its stored pieces could not reassemble. The test
`capture non_resumed_extension_after_a_between_windows_rewrite_restores_to_the_file` failed with
`Err("restore failed: Digest")`. The fix verifies the re-read prefix against the committed
digest before extending, and a mismatch starts an epoch.

Append sources are prefix-stable unless a symlink spelling carries a protected-document
basename (`profile_of`: append kinds are Transcript or OtherKnown; `document.rs` `CONFIGS`).

Mutants: `post-stat` `a_source_that_moves_between_the_stats_is_ineligible`; `no-recheck` 3
tests; `mismatch-matched` 5; `extend-clears` 2; `no-prefix-check` the hypothesis test;
`due-always` 5; `due-ignores-capture` `due_rule_table`; `refresh-keeps-audit`
`same_size_rewrite_is_refreshed_then_audited`.

### AUDIT-2

Commits `8a9b838`, `ef3bda3`, `22af43e`, `c9941ff`. `AuditCounts { matched, mismatched,
ineligible }` is carried by `Captured`, `RoundReport` and `StatusBody.audit` (serde default).
The round after a grown or moved file counts one ineligible, because the stale stored stat
fails the pre-check.

Bytes read over a 200 KiB source (`tests/counters.rs`
`audit_reads_the_stream_once_per_turn_and_nothing_otherwise`):

| Step | Bytes read |
|---|---|
| First capture | 204,800 |
| Next quiet round (audit) | 204,800 |
| Round after that | 0 |
| Append of 1,000 | 69,632 + 1,000 |
| At 24 h, one audit, then the next round | 205,800, then 0 |
| Restart with an intact index | 0 |
| Restart without `index.json`: adoption, audit, next round | 205,800, 205,800, then 0 |

The property (`tests/capture_property.rs`) adds `Step::MidRewrite` (a flip strictly between
the windows, same size or growing) to the Hegel and proptest generators. The wall clock ticks
1 s per capture and `Settle` is the audit's turn (+24 h). The model keeps an `awaiting` map:
before the turn the latest epoch is the old file plus delta and hashes to its digest; after it
the latest epoch is the file and exactly one epoch was added. The shapes
`mid-rewrite-grow-audited` and `mid-rewrite-same-audited` are required by the Hegel guard and a
scripted test; the proptest twin keeps its five guards. No existing assertion, guard or weight
was removed (the parent read the diff). It passed on unmodified production code, 500 cases on
seeds 1 to 5 by the worker (83 to 130 s per seed).

Survivor: `prefix-mismatch-uncounted` (60 passed). Parent commit `c9941ff` pins the count; the
rerun fails `capture
non_resumed_extension_after_a_between_windows_rewrite_restores_to_the_file` at
`capture.rs:936` (left `mismatched` 0, right 1).

### LOG-2

Events: `state_load file=(index|status) result=(damaged|newer_schema)`, after `started`,
status before index; `parked owner=<owner_prefix> reason=<closed id>`, once in the round an
owner becomes parked; `host_paused` and `host_resumed` on the change only. `owner` uses the
charset `[A-Za-z0-9._:/-]`. There are seven closed reason ids, matched exhaustively.

`EventCounts`, `StatusBody.events` and `count_load` are removed. A `status.json` with an
`events` object loads as Damaged (pinned by daemon
`a_status_with_an_events_object_loads_as_damaged`; no shim; no production `status.json`
exists). Finding kept as is: a daemon with no sources never rewrites `index.json`, so a damaged
or newer-schema index logs `state_load` on every start until a source dirties the index.

Mutants: `parked-every-round` 2 daemon tests; `pause-every-round` and `no-resume` daemon
`host_pause_and_resume_are_logged_on_the_change_only`; `state-missing` 4 harness tests plus log
`state_load_renders_nothing_for_loaded_and_missing`; `index-first` 2 harness tests plus log
`state_load_lines_are_status_then_index`; `before-started` 4 harness tests; `events-accepted`
daemon `a_status_with_an_events_object_loads_as_damaged` (left Loaded, right Damaged);
`reason-swapped` daemon `a_source_park_is_logged_with_its_reason` plus log
`parked_names_the_owner_prefix_and_a_closed_reason`.

### AUDIT-3 and the park

Rule: `memory/src/audit.rs` `mismatch(previous, now_ns) -> Mismatch::{Epoch(AuditState),
Park(ParkReason)}` calls `memory_core::disposition::transition_paced` (`Pacing::FLOOR`) with
`Outcome::Failed(Deterministic(AuditMismatch))` and a `Streak` built from `AuditState`. There
is no second copy of the 3-failures / 3 h rule. `transition` itself is not public, hence
`transition_paced`. `audit::new_epoch` is removed.

What a park does:

- A third consecutive audit mismatch at least 3 h (wall clock) after the first parks the
  source instead of starting an epoch. The constants are `PARK_THRESHOLD` and `PARK_MIN_SPAN`
  in `disposition.rs`.
- Both mismatch sites use the rule: the stable audit mismatch and the non-resumed prefix check.
- A source that is not prefix-stable restarts at the prefix site with `audit::never()`: no
  failure, no `mismatched` count, no park.
- A parked source is filtered out of its owner's capture (no stat, no read, no absence count)
  and the rest of the owner flows. The park is recorded before the commit result, and
  `status.json` is written in that round.
- Parks load at start and are sorted by owner, then source.
- `AuditState` gains `first_failure_at_ns: Option<i64>` and `ParkedEntry` gains
  `source: Option<SourceId>` (both serde default; `source` skipped when `None`). The `parked`
  line gains `scope=<owner|source>`; a source id is never logged.

Existing assertions changed, all as the brief required: two daemon log strings gain
`scope=owner`; one `ParkedEntry` literal gains `source: None`; `tests/log.rs` `parked` takes a
`Scope`; `log_lines.rs` keys become `owner reason scope`; `AuditState` literals gain the field;
the Hegel guard leaves out only the new source-park shape (no existing guard was lowered).

Property: the `Sut` mirrors the daemon (parked sources left out, latest epoch frozen); a parked
source is exempt from follows and liveness for that source only; a park is accepted only after
at least `PARK_THRESHOLD` between-windows rewrites. The Hegel generator cannot reach a park
(`MidRewrite` through exec settles first and the new epoch's audit matches, resetting the
streak), so a source park is reached only by the scripted test
`repeated_prefix_mismatches_park_the_source_in_a_scripted_run`. The 500-case count of that shape
was not measured. `hegel_daemon_state_machine` never moves the wall clock (only monotonic time,
`tests/daemon.rs:1633`), so it cannot reach a source park either.

Mutants (15): `park-at-two` audit `the_park_rule_table` plus 3; `ignore-span`
`the_park_rule_table` plus 4; `first-overwritten` 8 tests; `match-keeps-streak` audit
`a_match_resets_the_streak_and_an_extension_keeps_it` plus 2; `park-starts-epoch` 5
(`audit.rs:1029`, left (0,0,1), right (1,0,1)); `park-counted`
`a_source_parks_at_the_next_mismatch_after_3_h`; `parked-still-captured` 3 (`audit.rs:980`,
read 204800 vs 0; counters `a_parked_source_costs_zero_reads_and_zero_stats_per_round`);
`owner-skipped` `a_park_is_recorded_when_the_owners_commit_fails` and
`the_other_source_of_the_owner_keeps_flowing`; `status-not-written` 3; `lost-on-restart`
`a_park_survives_a_restart_and_a_lost_status_heals`; `park-needs-no-commit`
`a_park_is_recorded_when_the_owners_commit_fails`; `prefix-nonstable-counts` capture
`a_non_prefix_stable_prefix_difference_restarts_without_a_failure` (`capture.rs:1081`, left 1,
right 0); `prefix-never-parks` capture `a_prefix_mismatch_parks_at_three_failures_over_three_hours`
and the scripted property test; `scope-always-owner` 4 including log
`parked_names_the_owner_prefix_and_a_closed_reason`; `logged-every-round` 2.

`capture_property` and the daemon tests at 500 cases: worker seeds 1 to 5 (87 to 123 s each,
before `0d28378`); parent seeds 11, 12 and 13 on `2edf2f8`, 59 passed each (102, 89 and 101 s).

## Log grammar and events

Line grammar: `<UTC RFC 3339> INFO <event>[ key=value]*`. The only open value is `owner` (the
stored key prefix). No path, no source id and no foreign error text is ever formatted into an
event.

| Event | Keys | When |
|---|---|---|
| `started` | `version` | once, after `start_from_config` has succeeded; nothing is logged before that |
| `state_load` | `file` (`index`, `status`), `result` (`damaged`, `newer_schema`) | at start, after `started`, status before index; nothing for a loaded or missing file |
| `round` | `landed failed skipped parked deferred offline throttled transient destination_auth local deterministic hints_removed hints_kept_old audit_matched audit_mismatched audit_ineligible` | every round |
| `parked` | `owner`, `reason`, `scope` | once, in the round an owner or source becomes parked |
| `host_paused`, `host_resumed` | none | on the change only |
| `stopped` | none | on a clean stop |

The refusal line `memory: <closed code>` is unchanged and is the last line on a refusal.

## Full gates

`scripts/gates.sh`, local, 500 cases, all 13 sub-gates 0 on each tip, `rustdoc -Dwarnings` 0
(run by hand; no gate runs rustdoc). Gate 08 runs through nextest. Files are in
[`gates/`](gates/), built from each run's `status.tsv`, `seeds.txt`, the `Summary` and `test
result` lines of the logs, the `PASS` lines of the mutation gates and the totals of
`coverage.json`.

| Tip | Date | Seed | nextest | Hegel | proptest | Doctests | Gate 08 under coverage | Coverage |
|---|---|---|---|---|---|---|---|---|
| `b37556e` | 2026-11-12 | 307748004 | 491 | 31 | 29 | 12 | 551 passed, 4 skipped, 262 s | 6942/7133 = 97.32 % |
| `7f99613` | 2026-11-12 | 154133121 | 512 | 31 | 29 | 12 | 572 passed, 4 skipped, 286 s | 6994/7185 = 97.34 % |
| `2edf2f8` | 2026-11-16 | 3153221467 | 523 | 31 | 29 | 12 | 583 passed, 4 skipped, 288 s | 7044/7248 = 97.19 % |

The `if-match`, `advance-on-fail`, `privacy-deny` and window mutants are killed on each tip. On
`7f99613` and `2edf2f8`, `hegel_daemon_state_machine` (34.1 s and 30.0 s) and
`second_process_exits_lock_held` passed under coverage. 0 secret-shape matches in the saved
artifacts of the three runs.

## Stack #103 merge

PRs #96 to #102 merged on 2026-11-12 (about 12:23 UTC) as `39944d7`. Both Go "CI" failures
outside the accepted steps were rerun once with authorisation: run 43193672667 (PR #96,
`internal/syncer` 10 minute test timeout in `make crap`) and run 43193732212 (PR #100,
`TestRuntimeRunValidatesArchiveAndJoinsCancellation`, "runtime did not start", 5 s wait). On
attempt 2 both failed only in the two accepted `govulncheck` steps (GO-2026-6859). The stack
changes no Go, Makefile, workflow or flake file; the causes of the two Go failures were not
established. The ready rule was met, but `gh stack merge --yes --merge` refused with `branch
"main" belongs to multiple stacks` (the local metadata held three older stacks on trunk
`main` and no entry for #103). The parent rechecked all seven Rust runs (success), all seven CI
runs, heads, PR states and `origin/main` (`cf99a3d`), then ran `gh stack merge 103 --yes
--merge`: "Merged #96..#102 into main (39944d7)". Post-merge "Rust rewrite gates" run
43217491627 succeeded (3 jobs); post-merge "CI" run 43217492534 failed only in the two accepted
steps.

## Stack #111 merge

PRs #104 to #110, #112 and #113 merged on 2026-11-16 (about 14:38 UTC) as `d765f9c` with
`gh stack merge 111 --yes --merge`; `git diff 538279e origin/main` is empty. Before the merge
every "Rust rewrite gates" run was `success` on its latest attempt and every "CI" run failed
only in the two accepted `govulncheck` steps, checked job by job for all nine. Three "Rust
rewrite gates" runs needed one rerun of one macOS job each, none after a failed step:

| PR | Run | Attempt 1 | Attempt 2 |
|---|---|---|---|
| #109 | 43865576080 | macOS job cancelled at the 30-minute limit inside `08-coverage` | success, 26.1 min |
| #110 | 43865589662 | macOS job cancelled at the limit after all 13 gates were 0 | success, 28.6 min |
| #113 | 43878896700 | `Rust coverage (macos-26)` never acquired a runner (no steps) | success, 12.1 min |

No Go "CI" run was rerun. Post-merge on `d765f9c`: "Rust rewrite gates" run 43887779155
succeeded on all six jobs at attempt 1; "CI" run 43887779219 failed only in the two accepted
steps. The `govulncheck` output was not read again; acceptance of the "CI" runs rests on the
job and step names.

## Known limits

- No `unpark` command and no automatic unpark. A parked source stays parked when its file
  changes. Clearing it today means deleting `status.json` with the daemon stopped, which also
  loses every other park, the host pause and the counters. The file carries a length and
  SHA-256 header, so an edited one loads as damaged, with the same effect. The index streak
  then re-parks the source at the next due audit if it still mismatches.
- A hint day file whose launch record never commits is kept for ever. This includes the
  index-loss case: after `index.json` is lost, a launch record that the owner's manifests
  already carry never becomes done when the owner has nothing else to commit (the recovery
  projection ignores `launch`), so its day file is kept and counted in `hints_kept_old`.
- A daemon with no sources never rewrites a damaged `index.json`, so it logs `state_load` on
  every start.
- A `status.json` written before LOG-2 (with `events`) loads as Damaged and loses its parked
  list. None exists in production.
- The Hegel generator of `capture_property` cannot reach a source park; only the scripted test
  does.
- EPIPE on the real binary's stderr was not observed directly (a closed read end is tested).
- The `signals_unavailable` and `state_root_unresolved` refusals are untested (from brief 27).
- No gate runs rustdoc; it was run by hand.
- The watcher and scheduler are not connected, there is no wake-gap detection, and a stop is
  seen only between rounds.

## Not run

2,000 cases, `cargo mutants`, anything against R2.

## PR #114 and the runs on `94b8abd`

Added 2026-11-16 (UTC). PR #114 (`docs/stack-111-merged`, `2da4750`, the close-out record) ran
"Rust rewrite gates" 43891615139: success at attempt 1 on all six jobs (macOS gates 17.2
minutes, macOS coverage 11.0 minutes). "CI" 43891615119 attempt 1 had `Tests (macos-26)`
cancelled with no steps ("not acquired by Runner"); one rerun failed only in the two accepted
`govulncheck` steps. `gh stack merge --yes --merge` refused ("this stack has not been submitted
to GitHub yet": a one-branch stack gets no stack number) and `gh stack merge 114` refused ("not
a stack number or a stacked pull request"), so the PR was merged with `gh pr merge 114 --merge
--match-head-commit <sha>` at 2026-11-16T15:39:36Z. `origin/main` is `94b8abd`, tree equal to
`2da4750`.

Post-merge on `94b8abd`: "CI" 43898746499 failed only in the two accepted steps. "Rust rewrite
gates" 43898746418 needed three attempts: attempt 1, both macOS jobs never acquired a runner
(zero steps, 15.0 minutes queued); attempt 2, macOS gates success and macOS coverage again never
acquired; attempt 3, macOS coverage success (all six jobs success at 16:36 UTC). No step failed
in any attempt; the parent reran only jobs that never started.
