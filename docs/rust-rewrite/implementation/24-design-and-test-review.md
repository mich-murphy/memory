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

## Next action

All workers are complete. Waiting on the operator: commit the W1 diff or not; whether
production fixes and the missing tests are in scope; the open decisions above.
