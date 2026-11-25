# Quality gates

> **Scope:** Passing the gates is not end-to-end acceptance. The agreed
> [session-capture contract](session-capture-contract.md) also requires actual writer output
> through the shared reader, importer, event index and report, and live acceptance on both
> hosts. The rule behind the gates: **a check is a
> hard gate only if fixing what it flags always makes the code better.** Never relax a gate
> to make a run pass.

## Rust gates

The Rust workspace is under `rust/`; [DESIGN.md](../DESIGN.md) sections 14–17
specify its additional gates. Current evidence and ownership are in the
[active handoff](session-capture-handoff.md#rust-implementation-delivery--active-coordinator-brief)
and [`rust-rewrite/implementation/`](rust-rewrite/implementation/).

Run the Rust gate wrapper in the pinned Nix shell, with bounded native parallelism:

```sh
nix develop --max-jobs 2 --cores 2 --command sh -c \
  'cd rust && PROPERTY_CASES=2000 bash scripts/gates.sh'
```

Hosted CI runs the same command in the Rust-only `.#rust` shell (`flake.nix`), which carries the same pinned Rust tools and environment as the default shell, so the runner downloads less. Hosted CI runs it as two jobs per OS, selected by `GATES_PART` (2026-11-16): `gates` is every gate but `08-coverage` and `coverage` is `08-coverage` alone, each under the unchanged 30-minute job limit, so one OS may now use up to two 30-minute jobs. Without `GATES_PART` the wrapper runs all 14 in one pass, which is the local evidence run.

The wrapper runs format, strict clippy (twice: all targets, then libraries and binaries only, which is the only pass that compiles code without the test-only `sim` feature, as the release build does; added 2026-11-22 after `main` `25fae57` passed every gate but did not build in release), dependency/license/security and pure-core
dependency bans, nextest, compile-fail doctests, Hegel (every `hegel_` test in the workspace), proptest (every `proptest_` test in the workspace), the actual production
window-comparison mutation, the three-invariant mutation check (`scripts/check-mutations.py`: a blind/latest-ETag `If-Match`, a local index advanced after a commit that did not land, and a skipped credential denylist, each applied once to production source in one private copy and required to fail an independent-oracle test; see `crates/memory/tests/mutation_daemon.rs`), schema drift (the xtask cli test no longer writes schemas, so `cargo xtask schemas --check` can fail), coverage, the deliberate-lint checker and `10-consumer` (brief 32, added 2026-11-18): actual Rust writer output through `memory export` into the unchanged `tools/analytics/publications.py` importer, event index and report for Claude, Codex and Pi, plus a privacy canary scan of every export. It needs the pinned analytics dependencies (`tools/analytics/requirements.txt`) through `MEMORY_ANALYTICS_PYTHON`, default `.analytics-venv/bin/python` at the repository root; under `gates.sh` a missing interpreter or duckdb fails the gate instead of skipping it. Hosted CI installs the venv in the `gates` part only. It records seeds and each command's exact status under `rust/artifacts/`;
a failed or unavailable gate makes the aggregate fail. CI uses at least 500 cases,
local slice acceptance requires 2,000. A scaffold/property pass is not proof of
the daemon, real R2 semantics, privacy at the actual store sink, export/import,
footprint, or the two native shadow runs (seven days each until A14.2 of 2026-11-12 put the
2-day criterion of A13.1 in their place). The line-coverage floor is a fixed
**80%** backstop ([amendment A9](rust-rewrite/design-amendments-20261109.md), 2026-11-09),
not a ratchet of the latest measurement; evidence for invariants comes from the
independent-oracle properties, their coverage guards and the production mutation checks.
It is never lowered to make a run pass. The earlier 98% floor was the scaffold's first
measurement. The latest source-bound integrated run (review and fix round, 2026-11-10,
`d8e80a6`) passed all 13 subgates at **97.03%** (5,595/5,766 lines), 391/391 nextest (before the 2026-11-10 test review removed 24 tests and merged 9; not re-counted), 12
compile-fail doctests, 2,000 cases, seed 730213; see
[its evidence](rust-rewrite/implementation/evidence/13-review-fix-round/). Gate 8
(98.3173%, seed 730210, [round 10](rust-rewrite/implementation/evidence/12-slice2-round10/))
is superseded. After the design and test review (2026-11-11, `main` at `c239d1b`,
[report 24](rust-rewrite/implementation/24-design-and-test-review.md)) the gates passed at 500 cases, seed 3481505894, 431 tests, **97.72%** (5,840/5,976 lines); that run was not repeated at 2,000 cases. On the data-structure stack (2026-11-12, `0f3cb7d`,
[report 25](rust-rewrite/implementation/25-engineering-practice-review.md)) they passed at 500 cases, seed 374449330, 431 tests, **97.72%** (5,869/6,006 lines). On the walking-skeleton stack (2026-11-12, `8f10261`, [report 26](rust-rewrite/implementation/26-walking-skeleton.md)) they passed at 500 cases, seed 335062796, 471 tests, **97.62%** (6,140/6,290 lines). After the structural fixes on the same stack (2026-11-12, `6fd536a`, [evidence 17](rust-rewrite/implementation/evidence/17-structural-fixes/)) they passed at 500 cases, seed 3194196348, 472 tests, **97.75%** (6,031/6,170 lines). On the run-loop branches above that stack (2026-11-12, `2c10eb0`, local run, [evidence 18](rust-rewrite/implementation/evidence/18-run-loop/)) they passed at 500 cases, seed 2202692434, 519 tests, **97.36%** (6,596/6,775 lines). On the production-prerequisites stack (2026-11-16, `2edf2f8`, local run, [brief 28](rust-rewrite/implementation/28-production-prerequisites.md)) they passed at 500 cases, seed 3153221467, 583 tests, **97.19%** (7,044/7,248 lines). Under `gates.sh` the two mutation scripts share one temporary target The event-loop stack (`969d6fc`, [brief 29](rust-rewrite/implementation/29-event-loop.md), [evidence 20](rust-rewrite/implementation/evidence/20-event-loop/)) was accepted locally the same way; its gate results (all 13 sub-gates 0, 97.40 %) are in evidence 20. It merged into `main` as `f15f651` on 2026-11-16.
directory (`MEMORY_MUTATION_TARGET`), so the second does not rebuild dependencies; this kept
the macOS job inside its unchanged 30-minute limit.
Gate 08 runs through `cargo llvm-cov nextest` (one process per test, since `00e91ab`): under
plain `cargo test` the daemon tests share one process, and a child spawned by
`second_process_exits_lock_held` could hold the daemon lock a state-machine restart retakes.
At 2,000 cases the two daemon state-machine properties took about 6 minutes because every
simulated round did real `F_FULLFSYNC` writes. Amendment A7 is implemented on
`rust/sync-policy`: only those two properties select `SyncPolicy::None` and now take about
14–20 s; every other test and the binary keep `Full`. Hosted CI (`.github/workflows/rust.yml`,
500 cases, random seed) runs `scripts/gates.sh` on every branch of stack #56. Ad-hoc coverage runs at low case counts fail the property coverage guards by
design; use 2,000 cases or `--ignore-run-fail` and say so.
The [final-toolchain BC probe](rust-rewrite/implementation/evidence/03-bc-recheck/README.md)
also passed, on native x86_64. Neither result proves hosted CI or full release
acceptance; changed source requires relevant revalidation.

Focused examples (also run inside the Nix shell):

```sh
cd rust
cargo test --locked -p memory-core --test parent_privacy_review -- --test-threads=2
cargo test --locked -p memory-core --doc
cargo xtask schemas --check
```

The official Rust toolchain is separately pinned through `rust-overlay`; the
nixpkgs pin is preserved. This avoids the independently
reproduced profiling-runtime failure of the original locked nixpkgs Rust build;
it is not permission to skip coverage or use an unpinned host compiler.

## Other CI checks

- `.github/workflows/rust.yml` runs the Rust gates above, and in the `gates` part the
  analytics unit suite: `python -m unittest discover -s tools/analytics`, in the pinned venv.
- `.github/workflows/lint.yml` runs zizmor (pedantic persona, `.github/zizmor.yml`) and
  actionlint over the workflows. Every action is pinned to a full commit SHA.
- Dependabot updates the GitHub Actions weekly, grouped, after a 7-day cooldown.
