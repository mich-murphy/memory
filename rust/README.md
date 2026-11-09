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

The canonical wrapper also runs workspace doctests (nextest does not) and the
actual production-source window-mutation checker. Missing tools/scripts or a failed
mutation baseline fail the wrapper, never skip successfully.

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

The latest integrated local wrapper (gate 8) passed at 2,000 cases, seed 730210: all 12
subgates, 369/369 nextest tests, 5,200/5,289 lines covered (98.3173%, floor unchanged at
98%). See [its evidence](../docs/rust-rewrite/implementation/evidence/12-slice2-round10/).

The final pinned-toolchain synthetic Hegel probe passed on BC-01, Linux x86_64,
at 2,000 cases/seed 730052. This is not the whole workspace native suite or ARM
acceptance. Hosted CI (`.github/workflows/rust.yml`, ubuntu x86_64/arm64 and macOS, 500
cases) is green on every branch of draft stack #56. After gate 8 the stack added A7
(`SyncPolicy`, crash-point property), A6 (`ErrorClass::Transient`, full-jitter backoff,
`known_ours`) and the P2 client choice (`rusty-s3` + `ureq`, not yet wired).

Tests cover pure mechanics and known-file privacy boundaries; the actual-source
mutation proves sensitivity to a disabled window comparison. The filesystem
counters and lost-reply fixture validate test seams, not a real commit protocol.
The structural epoch guard is not retained-history reconstruction or zero-PUT
recovery, and authorized-body tests are not actual PUT/hint sink privacy proof.
`proptest-state-machine` is available for property 4 in slice 2; its existence is
not a state-machine correctness claim. Full DESIGN properties, footprint and
writer→reader→importer→report acceptance remain outstanding.

`cargo deny` checks workspace licenses/advisories/sources; a separate pure-core
ban invocation excludes dev dependencies. Dependency bans cannot prohibit
`std::fs`/`std::net`; review core source boundaries too. Later runtimes must use
safe APIs for flock/F_FULLFSYNC and must never suppress `forbid(unsafe_code)`.

Monthly diagnostic: `cargo mutants -p memory-core --jobs 2` (not run at bootstrap).
The parent owns `.github/workflows/rust.yml`; hosted CI execution remains parent-owned.
