# Rust rewrite foundation (not yet a collector)

Read [DESIGN.md](../DESIGN.md) for requirements and the
[active handoff](../docs/session-capture-handoff.md#current-session-checkpoint) for
ownership, current results and next actions. Read DESIGN.md with its
[2026-11-09 amendments](../docs/rust-rewrite/design-amendments-20261109.md).
`memory-core` holds the pure slice-1 core: domain types, locator, manifests, hint lines,
planner, coalescer and privacy admission/transform/authorization. `memory` holds slice-2
work against fakes and temp directories: hook, commit protocol, daemon state, capture
wiring, real filesystem adapter, source resolver, notify watcher/scheduler and a daemon
skeleton. There is no R2 client, zstd, daemon loop or reader yet. No slice or release
acceptance is claimed.

All commands use the repository's **locked Nix shell**, Rust/cargo 1.99.0.
No rustup/global installs. From repository root:

```sh
nix develop --max-jobs 2 --cores 2
cd rust
cargo xtask schemas                 # regenerate committed diagnostic schema
PROPERTY_CASES=2000 bash scripts/gates.sh
# CI invocation: PROPERTY_CASES=500 bash scripts/gates.sh
```

The canonical wrapper also runs workspace doctests (nextest does not), gates `05-hegel` and
`06-proptest` (every `hegel_` and every `proptest_` test in the workspace), and two
production-source mutation checkers: `check-window-mutation.py` (planner window comparison)
and `check-mutations.py` (blind `If-Match`, index advanced after a commit that did not land,
skipped credential denylist). Under `gates.sh` both share one temporary target directory,
removed afterwards. Missing tools/scripts or a failed mutation baseline fail the wrapper,
never skip successfully.

`artifacts/status.tsv` preserves each gate exit status; logs and seeds remain in
`artifacts/`. Replay with `PROPERTY_SEED=<recorded> PROPERTY_CASES=2000`.
The repaired coverage tool is available in the default shell and the explicit
`nix develop .#coverage` alias. It uses pinned official Rust 1.99.0 and its matching
LLVM tools; upstream tool tests remain enabled. `coverage-floor.txt` is a fixed 80% line
backstop (amendment A9, 2026-11-09), not a ratchet from the latest measurement. The earlier
98% value was the scaffold's first measurement (49/50 lines, evidence/00-scaffold). A failed
gate must never silently lower the floor; raising it is a recorded decision.

`memory hook <agent>` appends one hint line and always exits 0. `memory daemon` runs its
startup (lock, state directory, recovery) and then exits 78; every other command exits 78,
so the binary cannot silently simulate capture or restore. The reader crate is a
placeholder. Tests must never resolve the real state root: set `MEMORY_STATE_DIR` to a
temp directory and `env_clear()` (only `LLVM_PROFILE_FILE` passes through).

The latest integrated local wrapper (review and fix round, 2026-11-10) passed on
`d8e80a6` at 2,000 cases, seed 730213: all 13 subgates, 391/391 nextest tests (2 ignored
benchmarks; the later test review removed 24 tests and merged 9, not re-counted), 12 compile-fail doctests, 5,595/5,766 lines covered (97.03%, floor 80%), no
source changed during the run. See
[its evidence](../docs/rust-rewrite/implementation/evidence/13-review-fix-round/) and
[report 23](../docs/rust-rewrite/implementation/23-review-fix-round.md). Gate 8
([round 10](../docs/rust-rewrite/implementation/evidence/12-slice2-round10/)) is superseded.

The final pinned-toolchain synthetic Hegel probe passed on BC-01, Linux x86_64,
at 2,000 cases/seed 730052. This is not the whole workspace native suite or ARM
acceptance. Hosted CI (`.github/workflows/rust.yml`, ubuntu x86_64/arm64 and macOS, 500
cases) is green on every branch of draft stack #56, whose top is now `rust/coverage-floor-80`
(PR #60). After gate 8 the stack added A7 (`SyncPolicy`, crash-point property), A6
(`ErrorClass::Transient`, full-jitter backoff, `known_ours`), the P2 client choice
(`rusty-s3` + `ureq`, not yet wired) and the review and fix round (report 23).

What the tests do and do not show:
- Store uploads take `Published`, whose constructors are crate-private and slice an
  `AuthorizedBody` or the writer's own metadata; compile-fail doctests prove raw bytes cannot
  reach `Store`. This is sink privacy by type at the seam, not a live R2 check.
- `environment::Counting` counts stats, bytes read and PUTs through production `Daemon` and
  capture code over fakes: an idle round makes no reads or PUTs, 30,000 unchanged sources
  take at most one stat each, and a 59 KB append to a 200 kB source reads 128,632 bytes
  (bound 59 KB + 68 KiB). Timer wakeups are not counted; the 1 s/30k target is measured
  (1.24 s in a debug build), not asserted.
- Property 3 (zero PUTs after convergence, restart and index deletion) runs in Hegel only;
  a writer-version change has no seam yet.
- The daemon state machine (Hegel and proptest) covers append, rewrite, truncate,
  remove-source, revoke/restore, crash before `current.json`, late write (A6), lost reply,
  offline, restart and index deletion, with a 12-shape coverage guard and per-shape negative
  controls. 1b liveness "after one audit" is untested because audit evidence is always
  `NotDue`.
- Footprint, native shadow and writer→reader→importer→report acceptance remain outstanding.

`cargo deny` checks workspace licenses/advisories/sources; a separate pure-core
ban invocation excludes dev dependencies. Dependency bans cannot prohibit
`std::fs`/`std::net`; review core source boundaries too. Later runtimes must use
safe APIs for flock/F_FULLFSYNC and must never suppress `forbid(unsafe_code)`.

Monthly diagnostic: `cargo mutants -p memory-core --jobs 2` (not run at bootstrap, and nothing
schedules it). First baseline, 2026-11-10, example tests only, `memory-core` not run:
`capture.rs` 62.5%, `commit.rs` 89.5%, `state.rs` 91.6% (design-amendments A10.8).
The parent owns `.github/workflows/rust.yml`; hosted CI execution remains parent-owned.
