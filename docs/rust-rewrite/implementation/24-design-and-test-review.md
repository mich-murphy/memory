# 24. Design, test and complexity review (2026-11-10)

Coordinator brief. The parent session owns this file and acceptance. Status: complete and
merged to `main` (`c239d1b`, 2026-11-11). Open items are listed under "Close-out".
Raw reports and results: [evidence 14](evidence/14-design-test-review/).

## Checkout

- `/Users/mm/dev/memory`, branch `rust/coverage-floor-80`, base HEAD `6ea015e`, clean at start.
- At the start nothing was to be committed; the operator later asked for commits and PRs
  (stack #56).

## Scope and acceptance

- Operator request: critique the design against evidence and authoritative sources; challenge
  every test and remove tautological or low-value ones; measure complexity; audit the Hegel
  property tests; settle open questions by experiment.
- Acceptance: every removal is backed by a mutant or a stated reason the parent has checked;
  no privacy or security assertion is removed; gates are not relaxed; `rust/scripts/gates.sh`
  passes on the result; each experiment records command, raw output and conclusion.
- Exclusions: Go code; live R2 writes; reading transcript contents; host changes; commits.

## Phase 1 (complete): eight read-only Sonnet reviews

Reports: [evidence 14](evidence/14-design-test-review/) `reports/01` to `08`.

Parent spot-checks that confirmed a worker claim against the code:

| Claim | Where |
|---|---|
| No logging calls in the `memory` crate; "logged" in DESIGN 9.2 cannot happen | `crates/memory/src` |
| Damaged or newer `status.json` silently becomes empty | `daemon.rs:259` |
| Source read opens by path, no `O_NOFOLLOW`, no identity recheck | `realfs.rs:126` |
| DESIGN 7.3 says raw-byte hash and "412 is success" | `DESIGN.md:371`, `:602` |
| `known_ours` is memory-only | `daemon.rs:307` |
| 8 MiB cap cites P4; P4 shows 135 MB peak against a 128 MB target | `DESIGN.md:559`, P4 |
| Only hand-written mutants run; no `cargo-mutants` in gates | `rust/scripts` |
| Binary refuses to run the daemon; `Store`/`Clock` have only test implementors | `main.rs`, `daemon.rs:276` |
| Schema-drift gate is neutralised: the xtask cli test writes schemas before `--check` | `xtask/tests/cli.rs`, `gates.sh:25,37` |
| Retry test uses the constant as its own oracle | `commit_protocol.rs` ~1697 |
| Store fake ETag is a version counter, not content-derived | `commit_protocol.rs:174` |
| Hegel state machines set no `database_key` | three `Hegel::new` sites |
| Gates 05-hegel and 06-proptest run only the scaffold std-wrapper property | `gates.sh:28-29` (parent finding) |

Test verdicts (worker counts, 384 tests): 36 remove, 22 merge, 29 strengthen, rest keep.

## Phase 2 ownership

- Parent: this brief, acceptance.
- W1 implementer: `rust/crates/*/tests/**`, `rust/scripts/gates.sh`. Applies removals and
  merges, fixes the schema gate ordering bug, replaces constant-as-oracle assertions.
  Held back by the parent (not to be removed without operator decision): tests that pin
  closed error text (`error_text_is_closed`, `error_display_is_closed`,
  `result_codes_are_fixed_and_distinct`), because closed error text is a privacy control.
- W4 OS experiments, W5 evidence reconciliation: read-only on the repo.
- W2 property flake rates, W3 `cargo mutants` baseline: start after W1 lands (CPU contention).

## Open decisions for the operator

- Rollback after amendments A0/A2 (Go stopped, bucket reused) – pending W5's reading.
- Live R2 probes (If-Match on a missing key, late-landing PUT) need explicit approval.
- Prefix-rewrite frequency and snapshot bytes per day need metadata from real session files.

## Phase 2 results

- W1 (complete, parent-accepted, uncommitted working tree, 16 files, +97/-557): 24 tests
  removed, 9 merged; tests 392 -> 360 (2 ignored). Schema gate fixed (cli test no longer
  writes schemas; proven with a stale schema in a scratch copy). Gates 05/06 now run all
  `hegel_` (61) and `proptest_` (58) tests. Retry test asserts literal 5 PUTs / 4 pauses.
  Worker evidence: `PROPERTY_CASES=500 bash scripts/gates.sh`, seed 3352165921, all 13
  subgates 0; line coverage 97.03% before and after. Parent checks: diff of `gates.sh` and
  `cli.rs` read; moved privacy URL and TOML datetime assertions confirmed present in
  `privacy_coverage.rs`; audit mutant evidence read for the removed `directed_*` tests.
  Not done: all STRENGTHEN verdicts; `GET_ATTEMPTS` literal (DESIGN states no number);
  docs still naming the scaffold gate (`docs/quality-gates.md` ~35, 41; `rust/README.md:38`).
- W4 (complete): `sync_all` is `F_FULLFSYNC` for files and directories (std source plus dyld
  interposer). Symlink swap between stat and read is followed and would upload
  (`realfs.rs:126`, only a length check at `capture.rs:253`). FSEvents burst loss up to 3.1%,
  always with a rescan flag; a directory moved in yields no events for its children. Walk
  timing inconclusive (machine loaded, no cache purge).
- W5 (complete): table 3.1 was produced at quiet 30 s (`policy_sim.py:24`), heading says 60 s;
  code hashes scrubbed bytes, DESIGN 7.3 says raw; manifests label identity bytes `zstd-3`;
  real R2 returns 412 for If-Match on a missing key, the fake returns NotFound; old archive
  deleted 2026-11-09 (A2), so slice-2b Go cross-check cannot run as written and DESIGN 18 has
  no numeric exit criteria.

- W3 (complete, partial scope): `cargo mutants` on `capture.rs`, `commit.rs`, `state.rs`
  against the `memory` example tests only (properties filtered out, so a floor): 145 caught,
  32 missed, 81.9% (capture 62.5%, commit 89.5%, state 91.6%). 17 misses are in
  `stream_span`, including body replaced by `Ok(vec![])`; `fsync_dir` -> `Ok(())` survives.
  Removal acceptance: 10 missed capture/commit mutants survive both the pre-removal suite and
  the current tree with properties included, so none is attributable to W1. Not run:
  `memory-core`, rest of `memory`; one mutant lost to a disk-full error.
- W2 (complete): 55 properties, about 9,000 runs, every failure a coverage-guard miss, no
  real counterexample. At 500 cases only `proptest_daemon_state_machine` failed (5/59, guard
  `rewrite-mid-file`), so one gate run fails by chance about 8.5% of the time (95% interval
  3.7-18%). At library defaults a plain run fails about 62% of the time. Seed 98595426
  reproduces the miss.

## Round 3 (complete, integrated as a stack on `rust/coverage-floor-80`, not pushed)

| Branch | Content |
|---|---|
| `rust/test-review-cut` | W1 test cut, schema gate fix, gates 05/06 |
| `rust/design-amendments-a10` | Amendment A10 (DESIGN.md untouched; first draft edited it in place, a coordinator briefing error, corrected) |
| `rust/realfs-nofollow-watch` | Source read opens `O_NOFOLLOW`, rechecks identity on the handle; watcher maps a moved-in directory to a directory read; bounded inbox |
| `rust/state-durability-seam` | Durable-write step recorder under `sim` and ordering tests; size-bound tests; one hints-dir vetting rule; injectable hook deadline |
| `rust/commit-daemon-tests` | Deterministic guard shapes; tests for the surviving commit/daemon mutants; fakes return 412 for If-Match on a missing key and run under two ETag models; state-load events counted; `run_owner` split |
| `rust/hook-dead-code` | `HookResult::code` and duplicate state-root lookup removed; amendment A11 |
| `rust/capture-tests-simplify` | `stream_span`, resume-guard and piece-cap tests; Hegel capture machine reaches both window sizes, rename-replace step, failure database; `Job::write`/`append`/`step` split |
| `rust/core-privacy-properties` | Generated launch and document privacy properties with proptest twins; `identifier!` macro; one key layout; pure-core `clippy.toml`; `commit.rs` re-exports `manifest_key` |

Parent verification on the integrated stack (HEAD `51fdd22`):
`PROPERTY_CASES=500 bash scripts/gates.sh`, seed 1442705893, all 13 sub-gates 0; nextest 415
passed, 2 skipped, 368 s; line coverage 5766/5902 = 97.70%; `verify-evidence.py` PASS.
`hegel_property4_capture_state_machine` now takes 254 s at 500 cases on a quiet machine
(about 30 s before).

Worker-reported, not rerun by the parent: `capture.rs` mutation score 61.7% -> 93.5%
(example tests only); `memory-core` baselines privacy 87%, planner 94%, locator 97%, hint 82%
before the new tests; coordinates guard 0 failures in 120 runs; daemon/commit guards 0 misses
in 49 runs (30 seeds per machine asked, not reached).

Parent review still owed: line-by-line read of the capture, daemon and memory-core diffs, in
particular the removal of the `live` guard and the fixed four-round loop in `capture.rs`
(argued unreachable, untestable); the A10/A11 text.

Open items: mid-file rewrite between the windows has no detection path until the audit is
implemented (the capture property now avoids generating it); hint retention (A11.2); logging
decision (A11.3); secret-shaped values such as `sk-ant-...` pass DESIGN 8.4; which 8 of 9
allowlisted env names survive is unspecified; `recover_base` accepts 65 manifests where 64 is
the valid maximum; watcher aborts on a missing root (DESIGN silent); hand-copied `O_NOFOLLOW`
constants versus a `rustix`/`libc` dependency; test fakes still copied across test files;
`cargo mutants` not run on the whole workspace; live R2 probes; A10.3 and A10.7.

## Round 4 (complete, integrated)

| Branch | Content |
|---|---|
| `rust/realfs-rustix` | Named `rustix` open flags and errno replace hand-copied per-platform values (A12.4) |
| `rust/launch-value-rule` | Value-shape rule for launch argv and env values (A12.1) |
| `rust/chain-cap-oracle-memo` | `recover_base` reads at most 64 manifests (A12.3); capture property oracle memoised; amendment A12 |

Parent verification on the final stack (HEAD `9b7b48d`): `PROPERTY_CASES=500 bash
scripts/gates.sh`, seed 778961796, all 13 sub-gates 0; nextest 419 passed, 2 skipped, 234 s;
`hegel_property4_capture_state_machine` 30 s; line coverage 5826/5962 = 97.72%;
`verify-evidence.py` PASS. Not checked: Linux target build (target not in the Nix shell),
hosted CI, whole-workspace `cargo mutants`.

Still open (operator): live R2 probes; `GET_ATTEMPTS` literal; `realfs` error-text test;
`zstd-3` label (A10.3); slice 2b cross-check and section 18 criteria (A10.7); env cap 8 of 9;
watcher on a missing root; logging (A11.3); canonical-path type to close the parent-directory
race (A12.4); hint retention (A11.2); shared test fakes; 30-seed guard confirmation.

## Round 5 (complete, integrated above `rust/gates-properties-once`)

Operator decisions applied (amendment A13): shadow-run exit is 2 consecutive days with zero
restore mismatches, parked owners and uncaptured local sessions; the Go cross-check is dropped;
zstd, the audit, hint retention and `tracing` are prerequisites of the first production write;
the 8-entry env cap is removed (it came from Go `internal/launch/native.go:58`, no design
reason); verification GET is 4 attempts; the watcher skips and counts a missing root.

| Branch | Content |
|---|---|
| `rust/gates-properties-once` | Gates 04 to 06 run each test once. The round 3 change made 05/06 repeat gate 04's properties and two hosted runners hit the 30 minute limit (coordinator error) |
| `rust/review-decisions-a13` | Amendment A13.1, A13.2 |
| `rust/guards-read-random-only` | `core_coordinates` guard reads only random cases; switch shape ASCII-only |
| `rust/env-allowlist-no-cap` | Env cap removed; A13.3, A13.4 |
| `rust/shared-test-fakes` | One `MemStore`, `MemFs`, `TestClock` under `tests/common/`; the store defaults to the measured content ETag; `tests/fake_store.rs` pins the fake |
| `rust/watcher-roots-error-text` | Missing root skipped and counted; a symlink is never a directory; `RealFsError` text test; A13.5, A13.6 |
| `rust/daemon-commit-guards-random` | Daemon and commit guards read only random cases; state-aware generators; GET attempt literals; two test-model fixes (below) |

Findings of this round:

- Independent read-only review of rounds 3 and 4 (evidence 14, `reports/R5-B-stack-review.md`)
  found the round 3 coverage guards could not fail: a scripted prologue bumped the counters
  the guard read. Fixed in all three files. Parent check: with the `late-write` or
  `lost-reply` step disabled in the generator, the guard fails and names the shape.
- Live R2 probes (operator-approved, evidence 14 `reports/R5-E-r2-probes.md`, A13.4): the ETag is the MD5
  of the body; a PUT from a killed process landed in 15 of 20 trials, up to 0.5 s later.
  39 keys created, 39 deleted, prefixes verified empty. The probe used several thousand list
  requests against about 200 planned.
- With the guards real and the generators state-aware, the daemon state machines failed for
  the first time. Both failures were faults in the test model, not in production code:
  1. Settle ran four rounds of 61 s, less than the 15 minute maximum retry delay (DESIGN
     11.2), so a run of failures left the owner rightly deferred. Settle now advances past the
     delay first. Regression test `settle_converges_after_a_long_offline_run_once_the_retry_delay_passes`.
  2. The oracle required every refused `current.json` write to fall inside the step that
     injected a late write. A truncate that regrows the same bytes makes the round report
     `Unchanged`, so the held write lands steps later. The fake now counts refusals with no
     late write ahead of them and the oracle requires that count to be zero. Regression test
     `a_held_write_that_lands_steps_later_is_adopted_and_the_store_converges`. A stale-base
     mutant in `settle` is still caught by both state machines and two directed tests.

Parent verification on the final stack (HEAD `8532325`):

- `PROPERTY_CASES=500 bash scripts/gates.sh`, seed 3481505894, all 13 sub-gates 0; 373 + 30 +
  28 tests passed; line coverage 5840/5976 = 97.72%; `verify-evidence.py` PASS. The machine
  was shared with a mutation run, so wall times (05-hegel 714 s, 06-proptest 672 s) are high.
- 40 random-seed runs at library-default case counts of each of the four daemon and commit
  state machines, `HEGEL_DATABASE=disabled`: 0 failures in 160. The same campaign before fix
  2 failed 3 of 30 on `proptest_daemon_state_machine`.

`cargo mutants`, example tests only, tree `da01300`, interrupted and not triaged: `commit.rs`
100%, `hook.rs` 98.0%, `privacy/launch.rs` 97.8%, `coordinates.rs` 95.7%, `config.rs` 93.8%,
`capture.rs` 93.5%, `disposition.rs` 92.6%, `resolve.rs` 92.5%, `realfs.rs` 88.1%,
`privacy/document.rs` 86.8%, `daemon.rs` 78.1% (15 of 21 misses in the `next_draw` jitter),
`environment.rs` 61.1%. `state.rs`, `watch.rs` and the rest of `memory-core` incomplete.

Not done: triage of the missed mutants; a canonical-path type for the parent-directory race
(A12.4); removal of `FileSystem::stat_len` if it has no production caller; a parent rerun of
the `GET_ATTEMPTS` mutants (3 and 5), which the worker reported caught; Linux target build
locally (hosted CI covers it).

Open (operator): minimum startup delay before a restarted daemon's first commit (A13.4;
5 s suggested); whether the 2-day criterion also replaces the 7-day shadow runs of DESIGN 17
slices 2b and 3 (A13.1 currently says it does).

## Close-out (2026-11-11): merge, CI and what is left

Merged: stack #56, PRs #54 to #81 (26 PRs), one atomic merge into `main` as `c239d1b`. The
stack branches were deleted after checking each tip is contained in `main`.

Two PRs were added on top during the merge:

- #80 `rust/review-round5-brief`: this report's round 5 section and the A12.1 note.
- #81 `rust/ci-rust-shell`: hosted Rust gates run in a Rust-only Nix shell (`.#rust`, same
  pinned tools; the default shell's derivation is unchanged) and restore the built
  `cargo-llvm-cov` from the Actions cache, keyed on `flake.nix` and `flake.lock` with no
  fallback key. Cause, measured from job logs: the flake builds `cargo-llvm-cov` with the
  pinned toolchain (report 00), so it is never in the public cache and took 8m49s to build on
  macos-26 in every run. With that, macOS ran 22 to 30 minutes against the 30 minute limit
  (#79 cancelled during `08-coverage` with every earlier gate passed; #80 passed in 29m43s).
  With a cache hit the macOS setup is 1m30s instead of 11m00s. The Rust-only shell alone
  saved 17 s; the coordinator's first diagnosis (download size) was wrong. The limit was not
  raised.

Hosted results:

- `main` at `c239d1b`, "Rust rewrite gates" run 42990315805 (cache miss, first run): all
  three runners success; macos-26 23m47s, ubuntu-24.04 23m21s, ubuntu-24.04-arm 22m46s.
- Gate time itself varies by several minutes between identical runs on the same runner
  (macos-26 gates step 12m48s to 19m42s), so the headroom is real but not large.
- `main` "CI" run 42990315981 fails in two jobs, "Format, lint and static checks" and
  "Tests (macos-26)", on `make vuln` only: GO-2026-6859 (net/http, found in go1.27.3, fixed
  in go1.27.4) is the one advisory in both logs. Operator decision 2026-11-11: no Go toolchain
  bump; the advisory goes away when the Go code is replaced. nixpkgs did not ship Go 1.27.4 on
  that day in any case. Consequences: every PR shows those two jobs red until cutover, and the
  `check` targets after `vuln` (`crap`, `bench-smoke`, `actionlint`, `capture-acceptance`) do
  not run in hosted CI; run them locally for changes that touch Go or workflows. Production
  Go code runs no HTTP server and does not call `MaxBytesReader` (the advisory is a server
  crash); this was checked by search, not by analysis of the advisory.

Red checks that were accepted on merged PRs, each fixed by a PR higher in the same merge:
"Rust rewrite gates" cancelled at 30 minutes on #62 to #72 (properties ran twice, fixed by
#73) and on #79 macos-26 (fixed by #81); the `rewrite-mid-file` guard miss on #63 (seed
1781712999; the flaky guard W2 measured, fixed by #66 and #79); GO-2026-6859 on #74 to #81.
The Go "Fuzz" job on #68 was cancelled once and passed on its one rerun.

`cargo mutants` finished for `memory-core` after round 5 was written (evidence 14,
`mutants/`): privacy 268 caught and 22 missed, hint/planner/limits/manifest 271 and 26,
locator 147 and 1. Not triaged.

Not done, in priority order:

1. Triage of the missed mutants (lists in evidence 14); tests only for real gaps.
2. Canonical-path type to close the parent-directory race in the source read (A12.4).
3. Complete `cargo mutants` for `state.rs` and `watch.rs`; rerun the `GET_ATTEMPTS` mutants
   (3 and 5), which a worker reported caught and the parent did not rerun.
4. Remove `FileSystem::stat_len` if it has no production caller.
5. Prerequisites of the first production write (A13.2): zstd encoding, the audit, hint
   retention, `tracing`.

Open operator decisions: the minimum startup delay before a restarted daemon's first commit
(A13.4; 5 s suggested; a dead process's write was seen to land up to 0.5 s later); whether
the 2-day criterion also replaces the 7-day shadow runs of DESIGN 17 slices 2b and 3 (A13.1
says it does).

## Next action

Slice 2 as listed in the handoff (license decision, S3 adapter, zstd, run loop), with the
A13.2 prerequisites before any production write. Items 1 to 4 above are independent of it.
