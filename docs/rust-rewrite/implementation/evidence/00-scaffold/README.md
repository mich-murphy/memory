# Source-bound scaffold evidence (not integrated-core acceptance)

Canonical run: `nix develop .#coverage --max-jobs 2 --cores 2 --command bash -c 'cd rust/artifacts/scaffold-baseline && PROPERTY_CASES=2000 PROPERTY_SEED=730052 bash scripts/gates.sh'`.
See status.tsv and canonical-run.log for exact statuses. Six original scaffold tests
were preserved in initial-six-test-source.tar.gz. Added three meaningful CLI/schema
checks: missing/drift/roundtrip schema output, command dispatch failures, and memory
refusing to simulate capture. The verified snapshot excludes active owner modules
and tests; no active core file was reset or whole-workspace formatted.

Initial baseline discovery executed all six tests successfully but report write
failed (exit1: output directory missing); report-only recovery exit0. Original
coverage3/33 lines=9.0909%, then added diagnostics to address untested CLI and xtask.
Final measurement49/50 lines=98%; this is the initial measured floor, not a lowered
regression threshold. No source file coverage exclusions. uncovered-regions.txt
records remaining zero-count region entries; this is not claimed as100% coverage.
coverage-floor.txt is now98. The previous100 was explicitly provisional, never a
measured baseline and never passed. Current production implementation was NOT used
to establish this baseline; integrated gates remain parent-owned.

Original compiler probe command: `rustc -C instrument-coverage instrumented-hello.rs -o probe; LLVM_PROFILE_FILE=probe.profraw ./probe` under the original locked Nix Rust1.99/LLVM21 shell: compile0, run139(SIGSEGV); llvm-profdata merge1 (raw format11 vs reader10). Synthetic source is included and hashed.
Official pinned toolchain probe: same source/command plus matching bundled LLVM tools: compile0/run0/profile-merge0/report0. Official rustc1.99 uses LLVM23.1.1.

Tool fix: rust-overlay d3fe74269f2a6652437e21e78eb7e83be81f6982; official Rust1.99.0 plus clippy/rustfmt/llvm-tools-preview. Original nixpkgs node unchanged (asserted against HEAD). cargo-llvm-cov0.9.0 built with this compiler and its matching tools. Upstream tests unchanged, RUST_TEST_THREADS=2/CARGO_BUILD_JOBS=2. Corrected derivation fng70671hbjjk45w27xarrxh5myqhjim-cargo-llvm-cov-0.9.0 passed build/check/install; check phase4m48s. Native tool bootstrap exit0. No global installs.

BC first gate: synthetic scratch, original pinned Nix rust1.99, Hegel=0.52.0 static-engine. Exact command and initial full results are in00-scaffold.md: compile38.73s, one property passed0.03s, exit0. Additional2000-case replay with seed730052 passed0.42s, exit0. Host actually Linux x86_64, not ARM.
Final official-toolchain BC recheck is BLOCKED: SSH signing-agent communication/publickey failure exit255 before build/credential access; prior pass is not relabelled a final-toolchain pass. No repeated authentication retries. Remote files/services/config/collectors unchanged by the blocked attempt. No ARM execution claimed.

Ownership now: parent owns core lib.rs AND all Cargo manifests/lock after raw_value+url wiring; privacy worker owns reserved src/privacy/ and tests/privacy*.rs. Scaffold retains toolchain/scripts/report only until parent review. Parent owns new rust.yml; no workflow created by this worker. No hosted run, integrated full-core gates, Go gates, monthly mutants or live acceptance claimed.


## Expanded current integration wrapper

`canonical-integrated-gates.sh` is the current wrapper; its source hash is recorded
separately. It adds explicit workspace doctests and the actual production window
mutation checker, and locks the core-ban graph. These two gates cannot run on the
empty scaffold snapshot, which intentionally lacks a planner and privacy module.
Parent must run the expanded wrapper against stable integrated source; no result
is fabricated. Syntax check/shellcheck passed (exit0), not equivalent to integration.
