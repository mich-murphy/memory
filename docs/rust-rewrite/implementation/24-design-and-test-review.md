# 24. Design, test and complexity review (2026-11-10)

Coordinator brief. The parent session owns this file and acceptance. Status: in progress.

## Checkout

- `/Users/mm/dev/memory`, branch `rust/coverage-floor-80`, base HEAD `6ea015e`, clean at start.
- Nothing is committed by this task unless the operator asks.

## Scope and acceptance

- Operator request: critique the design against evidence and authoritative sources; challenge
  every test and remove tautological or low-value ones; measure complexity; audit the Hegel
  property tests; settle open questions by experiment.
- Acceptance: every removal is backed by a mutant or a stated reason the parent has checked;
  no privacy or security assertion is removed; gates are not relaxed; `rust/scripts/gates.sh`
  passes on the result; each experiment records command, raw output and conclusion.
- Exclusions: Go code; live R2 writes; reading transcript contents; host changes; commits.

## Phase 1 (complete): eight read-only Sonnet reviews

Reports are outside the repo, in the session scratchpad `review/01` to `08` (not yet copied
into the repo; the parent decides what is preserved as evidence once phase 2 lands).

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

## Next action

Stack submitted as PRs; operator decisions on the open items.
