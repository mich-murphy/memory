# Slice 0 scaffold — historical implementation and verification ledger

> **Current navigation:** all worker paths are released. The original blockers and
> ownership below are chronological snapshots, not open assignments. Isolated scaffold
> gates, corrected integrated local gates and the final official-toolchain BC probe
> subsequently passed. See [parent review](02-parent-review.md) and the
> [active handoff](../../session-capture-handoff.md#current-session-checkpoint) for
> current acceptance limits; this ledger alone is not slice/release acceptance.

## Ownership and source

Owns only `rust/`, `flake.nix`, `flake.lock`, optionally one new Rust-only
workflow, and this report. No commit/push or live capture. Initial HEAD:
`57d8400f632ab47a81e34d060f86d3687123153e` (dirty research/docs preserved).
Parent observed actual `openai/gpt-6.1-sol`, requested thinking medium.
Parent retains acceptance. Branch is now `rust/scaffold` in the parent-owned stack;
HEAD is unchanged.

## Initial actions

Read DESIGN.md in full, AGENTS.md, quality gates, active handoff brief and contract.
First gate task is the BC-01 synthetic Hegel static-engine build, before workspace
gates. Initial access probe:

```
ssh -o BatchMode=yes -o ConnectTimeout=10 bc-01 'uname -sm; command -v nix; command -v cargo'
```

Exit 127: SSH succeeds; host reports Linux x86_64 (not the design's assumed
aarch64), Nix is available, cargo is not on the default PATH. This is not a
Hegel build failure; use the pinned Nix shell. No services/config/collector or
credentials accessed.

Local `nix eval` confirms existing locked nixpkgs provides Rust/cargo 1.99.0.
Use that exact locked source without refreshing the Go pins. All build invocations
use CARGO_BUILD_JOBS=2, tests use two threads. The scaffold provides genuine harness
tests only: capture/privacy/restore claims await later slices, not dummy properties.

## Plan

1. Build Hegel =0.52.0 static engine on BC-01 in synthetic scratch using this lock.
2. Workspace and lint enforcement including deliberate violations; pure-core bans.
3. Schema generation/drift, counter harness and deterministic environment traits.
4. Run or precisely block each DESIGN section 15 gate; record commands/statuses.
5. Release paths to parent with unresolved criteria, never a release acceptance claim.

## BC-01 first gate: passed, x86_64 Linux only

Synthetic scratch `/tmp/memory-slice0-hegel.OkSGar`; no services, installed
collectors, config or credentials touched. Its minimal flake uses the unchanged
repository flake.lock and Rust/cargo 1.99.0 plus clang/pkg-config from that lock.
Copied the frozen `experiments/hegel-probe/Cargo.toml` and `tests/coalesce.rs`.
Exact build command (after scratch creation and synthetic input transfer):

```sh
ssh bc-01 "sh -c 'cd /tmp/memory-slice0-hegel.OkSGar && CARGO_BUILD_JOBS=2 nix develop --max-jobs 2 --cores 2 --command cargo test --test coalesce pieces_concatenate -- --test-threads=2'"
```

Exit **0**: `hegeltest 0.52.0`, `hegeltest-c 0.50.0`, static-engine;
38.73 s compile, one property passed in 0.03 s, one deliberately wrong research
property filtered out. The latter is not a production property or ignored failure.
BC reports `Linux x86_64`, not ARM; ARM is not verified by this run.
An initial scratch command failed (127) because the remote shell is fish; subsequent
commands explicitly use `sh`. This did not affect the build outcome.

## Bootstrap failure: coverage blocked, other tools unblocked

`nix develop --max-jobs 2 --cores 2 --command sh -c 'rustc --version; cargo --version; cargo nextest --version; cargo deny --version; cargo llvm-cov --version'`
exited **1** when the unchanged locked nixpkgs `cargo-llvm-cov-0.9.0` derivation
ran its own tests: 6 passed, 14 failed, 3 ignored, 2 filtered out. Exact upstream
cause observed in its log: instrumented fixture test executables terminate with
SIGSEGV (signal 11), including issue361, no_test, real1 and workspace_with_bin.
This is a toolchain/instrumentation bootstrap failure, not a project test result.

```sh
nix log /nix/store/xif6d7nvqjj5qy7w27y0nbns2b23yy3v-cargo-llvm-cov-0.9.0.drv
```

Log inspection exits 0; local diagnostic copy `/tmp/memory-cov-build.log`.
No upstream tests were disabled. No source pin/Go lock was refreshed. Coverage tool
is isolated in `nix develop .#coverage`; the default shell provides all other tools.
Re-attempts of that coverage shell will fail until this upstream problem is fixed.
Missing `cargo llvm-cov` in the default shell exits **101**, not green.

## Implemented scope (diagnostics, not production capture)

- Four crates under `rust/crates/`; synchronous core exposes only checked byte slices.
- Workspace lint inheritance, forbid unsafe, stale-expect checks; 12 independently
  matched deliberate lint diagnostics plus a compiling safe control. Tests retain
  stricter expect/unwrap bans rather than global test suppressions.
- notify pinned `=8.2.0` after reading the published crate Cargo.toml (source download
  `https://crates.io/api/v1/crates/notify/8.2.0/download`, version/default FSEvents read).
- Hegel `=0.52.0` static-engine, proptest and proptest-state-machine locked. Independent
  iterator oracle cross-checks the actual checked-slice helper; no capture property
  is claimed. State-machine production cross-check awaits slice 2.
- Generated diagnostic RangeError schema with byte-comparison drift check. No fake
  manifest/current schemas; these belong to slice 1.
- Clock/filesystem/store seams and counter snapshots in `memory::environment`;
  synthetic tests count every call/read and exercise landed writes with lost replies.
  No daemon idle/reconcile/performance acceptance is inferred from a fake.
- Binary exits 78: no fake-green capture/restore commands.
- `scripts/gates.sh` attempts all nine gates, preserves statuses/logs and records
  Hegel/proptest seeds. Default 500 cases (CI), configurable 2000 local, minimum 500.
- No new workflow: parent owns CI/integration and full Go gates. Inherited main Go
  FuzzEditJSON failure was parent-reported, not changed here.

## Initial project gates and correction

`nix develop --max-jobs 2 --cores 2 --command sh -c 'cd rust && cargo fmt && cargo xtask schemas && PROPERTY_CASES=2000 PROPERTY_SEED=730052 bash scripts/gates.sh'`
exited **1**: fmt/clippy/nextest/Hegel/proptest/schema/lint-check all 0;
workspace deny 2 (local workspace dependency version required), core bans 2
(config argument order), coverage 101 (blocked tool). Fixed the deny invocation and
added the exact local workspace dependency version; unchanged gates rerun below.

## Final diagnostic run and ownership boundary

Exact unchanged gate command after fixing configuration discovery and adding the
privacy worker's requested dependencies:

```sh
nix develop --max-jobs 2 --cores 2 --command sh -c 'cd rust && cargo generate-lockfile && cargo fmt && cargo xtask schemas && PROPERTY_CASES=2000 PROPERTY_SEED=730052 bash scripts/gates.sh'
```

All tests use two threads, CARGO_BUILD_JOBS=2; Nix max-jobs/cores 2.
Seeds: Hegel and proptest 730052, 2000 cases each. Per-gate exit statuses:

```
01-fmt	0
02-clippy	0
03-deny	0
03-core-bans	2
04-nextest	0
05-hegel	0
06-proptest	0
07-schema	0
08-coverage	101
09-lints	0
```

The command returns nonzero while coverage is blocked. No acceptance claim.
Core bans now discover their dedicated `crates/memory-core/deny.toml` from that
crate directory; prior --config attempts were command errors, not ban passes.
No measured coverage floor exists yet. The 100% argument is a provisional strict
target, NOT a claimed current baseline; parent must establish and commit the
actual baseline from working instrumentation before accepting slice 0.

BC-01 additional replay at 2000 cases passed, exit 0 (0.42 s):

```sh
ssh bc-01 "sh -c 'cd /tmp/memory-slice0-hegel.OkSGar && HEGEL_TEST_CASES=2000 HEGEL_SEED=730052 CARGO_BUILD_JOBS=2 nix develop --max-jobs 2 --cores 2 --command cargo test --test coalesce pieces_concatenate -- --test-threads=2'"
```

Ownership release/reservation:
- `rust/crates/memory-core/src/privacy/` and
  `rust/crates/memory-core/tests/privacy*.rs` are untouched and reserved/released
  for the separate privacy worker to stage pure implementation and fixtures.
- Dependencies serde_json, toml, sha2 (=0.11.1) and regex are wired by scaffold;
  no privacy module or production behavior was introduced.
- `memory-core/src/lib.rs` currently declares only `slice`; parent owns subsequent
  module/Cargo integration. Core path is `rust/crates/memory-core`.
- Scaffold interfaces are stable for parent review; parent may transfer the rest
  of core ownership, but green slice-0 acceptance is still blocked on coverage.

Not run: full/native/live Go gates (parent coordinates), hosted CI, Linux ARM,
monthly mutants, production capture/privacy properties, R2, natural-sleep and
shadow acceptance. No credentials/transcripts were read/printed, no services or
installed collectors changed, no global installs, commits or pushes. Nix fetched
only sandboxed development dependencies. Existing Go pins/flake.lock unchanged.
Inherited Go fuzz failure remains outside this scope. No custom runtime framework.

Source identity at report update (sha256; dirty source, not a commit):

```
30a0ad68de1c9cbbeb9e0808b6e397958546c17eadd18b285ead1a1b9b8455df  DESIGN.md
f88e5cdcd01cab16c65592ddc33be2178c26d3ebe07c3e288167399880f0dc0c  flake.nix
d9d280ce72a107459bd16d648491ab3f1262ddf64e806d1b36e5e9754c9faa3b  flake.lock
bb26c743a5bbf7e4314ef0a2fc54c885c5496044ec983a20d1dac71072532f14  rust/Cargo.toml
b2366af4511c6ccab96af7bd7ec983838770a45aeea938df9f9e35c89336dcc9  rust/Cargo.lock
```

### Final core-ban correction

Adding the requested sha2 exposed a real ban failure (exit 2): sha2 -> cpufeatures
-> libc. Inspected cpufeatures 0.3.1 source: platform CPU detection uses getauxval
and sysctlbyname. Scoped the libc ban's only permitted wrapper to `cpufeatures`;
direct core libc use and all other wrappers remain banned. No runtime/I/O crate
ban was removed. The final unchanged 2000-case gate rerun returns 1 solely for
the unavailable coverage command. Exact final statuses supersede earlier runs:

```
01-fmt	0
02-clippy	0
03-deny	0
03-core-bans	0
04-nextest	0
05-hegel	0
06-proptest	0
07-schema	0
08-coverage	101
09-lints	0
```

Six diagnostic tests pass in nextest; no tests skipped. All 12 lint violations
produce their named diagnostic and the safe control compiles. Source interfaces
and manifests are released to parent for integration review now, with privacy
reserved paths untouched. Coverage baseline/tool repair remains the unresolved
slice-0 criterion; this report is delivery evidence, not acceptance.

## Resumed final correction — authoritative current status

Earlier broad release/default-shell/coverage-blocker statements above are dated
progress, superseded here. The official pinned Nix compiler and matching LLVM
tools FIX the independent instrumentation crash. Upstream cargo-llvm-cov tests
remain enabled and passed; both default and coverage shells contain the tool.
Original nixpkgs/Go pin preserved; only rust-overlay added to lock.

Sanitized canonical logs, exact status.tsv, initial/final source bundles,
SHA256 manifests, measured coverage JSON and compiler probe source/results are
under [evidence/00-scaffold](evidence/00-scaffold/README.md).
The initial measured scaffold floor is49/50 lines=98%, after adding CLI/schema
diagnostics; the original six-test measurement was3/33. No file exclusions.

Only the isolated scaffold snapshot was gated; active core modules were NOT reset
or formatted. Parent owns current implementation gates/acceptance and rust.yml.
Final-toolchain BC recheck blocked by SSH-agent/publickey failure255; original
pinned-Nix BC Hegel build and2000-case replay remain actual passing evidence.

Ownership: explicit release of core src/lib.rs to parent; after url2.5.8 and
serde_json raw_value additions/lock refresh(exit0), all Cargo manifests/lock
also explicitly released to parent. Reserved privacy src/tests remain the privacy
worker's. No further Cargo edits by scaffold.

Unresolved: final official-toolchain Hegel BC recheck, integrated active-core gates,
parent review of measured floor/evidence, native ARM and later slice acceptance.
No commit/push. This is a path release, not slice acceptance.

### Final canonical wrapper integration (not executed on active core)

Inspected mechanics-owned `scripts/check-window-mutation.py`: uses locked Cargo,
500 minimum/2000 default property cases, seed/source hashes, isolated copy, actual
production predicate mutation, passing baseline required, and runtime independent
oracle assertion failure required (compile errors do not count as kills).
Canonical `scripts/gates.sh` now invokes it as `06-window-mutation` after the
property gates. It also runs `cargo test --workspace --doc --locked --
--test-threads=2` as `04-doctests`, because nextest does not run privacy's compile-fail
doctests. Core bans now use `--locked` too. No conditional skips or silent-success
fallbacks. Not run against active core here: parent coordinates stable integrated
gates. The isolated scaffold snapshot deliberately predates these two integration
gates: no planner/privacy functionality existed in that snapshot. Source-snapshot
all-green evidence must not be represented as passing the expanded integrated gate
wrapper. Current wrapper is separately hashed in evidence for this reason.

`bash -n rust/scripts/gates.sh` exit0; canonical pinned Nix shellcheck on both shell
scripts exit0 (log persisted). No whole-workspace fmt/source test run on active
owner files. Parent owns rust.yml, Cargo manifests/lock, core lib.rs and active-core
integration. Explicit release reaffirmed: no more Cargo edits by scaffold.

Shellcheck correction: the first current-wrapper shellcheck actually exited1 on
SC2164 (gate runner intentionally does not set-e). Added explicit `cd ... || exit
$?` so a failed source-root change cannot continue into unrelated directories.
Reran identical pinned shellcheck: exit0; `bash -n` exit0. Final evidence log/hash
updated. Expanded active-core gates remain intentionally unrun by this worker.
The one uncovered line in the98% snapshot is the synthetic schema test's optional
cleanup of a previously existing scratch directory (xtask line58); no source files
were excluded to establish the floor. Other zero-count regions in the list are
fallible serde/filesystem error paths, not additional uncovered lines.


## Final pinned-toolchain BC recheck — passed 2026-11-07

This bounded recheck supersedes **only** the final-toolchain SSH blocker above.
The original nixpkgs compiler pass remains historical; it is not relabelled as
an official-toolchain result. Full source-bound logs, exact commands/statuses,
transferred inputs, returned scratch lock, and before/after hashes are in
[evidence/03-bc-recheck](evidence/03-bc-recheck/README.md).

The explicitly requested SSH retry authenticated successfully. Its exit **127**
means cargo is absent outside Nix, not failed authentication. BC-01 reports
**Linux x86_64**, not ARM. Fresh synthetic scratch:
`/tmp/memory-slice0-official-bc.RcafTr`. Transferred the byte-exact **current**
`flake.nix`, `flake.lock`, required `.golangci-lint-version`, and unchanged frozen
Hegel probe manifest/test. Local originals, transferred copies, and returned
post-run copies match SHA-256; current flake/lock were not edited.

```sh
ssh -o BatchMode=yes -o ConnectTimeout=10 bc-01 "sh -c 'cd /tmp/memory-slice0-official-bc.RcafTr && HEGEL_TEST_CASES=2000 HEGEL_SEED=730052 CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2 nix develop --no-update-lock-file --max-jobs 2 --cores 2 --command sh ./verify.sh'"
```

The recorded verification script probes `rustc -Vv`, cargo and matching LLVM
tools, generates/pins only the scratch Cargo.lock, records the feature tree, then
runs `cargo test --locked --test coalesce pieces_concatenate -- --test-threads=2`.
Frozen research Cargo.toml retains its original `"0.52"` version range;
`cargo update -p hegeltest --precise 0.52.0` pins the resulting scratch lock.
The newly resolved transitive graph is retained, not claimed identical to the
original synthetic gate's graph.

**Exit 0**, Hegel **0.52.0**, static-engine / hegeltest-c **0.50.0**.
Official `rustc 1.99.0 (b940084d7 2026-09-28)`, host
`x86_64-unknown-linux-gnu`, LLVM **23.1.1**; bundled llvm-cov and llvm-profdata both
report **23.1.1-rust-1.99.0-stable**. Compile **19.53 s**; property **0.39 s**.
Result: 1 passed, 0 failed, 0 ignored, 1 filtered out (the deliberately wrong
research property, same selection as the prior positive gate). Cases **2000**,
seed **730052**, Nix jobs/cores **2**, Cargo jobs **2**, test threads **2**.
Transfer, remote hash probe, native shell/gate, evidence collection, tool probes,
lock resolution, feature tree and post-run hashes all exit **0**. No gate weakened.

No local full suite or integrated-core native suite was rerun; parent owns the
separate integrated evidence and acceptance. No Go/live/R2/ARM/hosted/shadow or
end-to-end release acceptance inferred. No production Rust, Cargo, flake, scripts,
other reports, installed services/hooks, credentials, private files, transcripts,
or source data modified/read as an incidental check. Only this report/new evidence
and unique synthetic remote scratch were written, with authorized sandboxed Nix
development builds. No commit/push or remote cleanup.

**Ownership release:** `implementation/00-scaffold.md` and the complete new
`implementation/evidence/03-bc-recheck/` tree are explicitly released to parent;
no paths remain reserved by this verifier. The former final-toolchain BC blocker
is resolved; parent review and later slice criteria remain parent-owned.
