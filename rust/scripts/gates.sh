#!/usr/bin/env bash
# Invoke from the pinned Nix shell. Run all ten gates and preserve their statuses.
# GATES_PART selects what runs: all (the default, and the local evidence run), gates (everything
# but 08-coverage) or coverage (08-coverage alone). Hosted CI runs the two parts as separate jobs:
# as one job the macOS runner reached its 30 minute limit (hosted runs 43865576080, 43865589662).
set -uo pipefail
cd "$(dirname "$0")/.." || exit $?
part="${GATES_PART:-all}"
case "$part" in
  all | gates | coverage) ;;
  *) echo 'GATES_PART must be all, gates or coverage' >&2; exit 2 ;;
esac
mkdir -p artifacts
export CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2
export HEGEL_TEST_CASES="${PROPERTY_CASES:-500}" PROPTEST_CASES="${PROPERTY_CASES:-500}"
export HEGEL_SEED="${PROPERTY_SEED:-$(python3 -c 'import secrets; print(secrets.randbits(32))')}"
export PROPTEST_RNG_SEED="$HEGEL_SEED" HEGEL_PRINT_BLOB=true
if [[ "$HEGEL_TEST_CASES" -lt 500 ]]; then echo 'minimum 500 property cases' >&2; exit 2; fi
printf 'HEGEL_SEED=%s PROPTEST_RNG_SEED=%s CASES=%s\n' "$HEGEL_SEED" "$PROPTEST_RNG_SEED" "$HEGEL_TEST_CASES" | tee artifacts/seeds.txt
: > artifacts/status.tsv
failed=0
run() {
  local name="$1"; shift
  case "$part:$name" in
    gates:08-coverage) return 0 ;;
    coverage:08-coverage) ;;
    coverage:*) return 0 ;;
  esac
  "$@" > "artifacts/$name.log" 2>&1
  local status=$?
  printf '%s\t%s\n' "$name" "$status" | tee -a artifacts/status.tsv
  if [[ $status != 0 ]]; then tail -30 "artifacts/$name.log"; failed=1; fi
}
run 01-fmt cargo fmt --check
run 02-clippy cargo clippy --workspace --all-targets --locked -- -Dwarnings
run 03-deny cargo deny --locked check
run 03-core-bans bash -c 'cd crates/memory-core && cargo deny --locked --exclude-dev check bans'
# Each test runs once across 04, 05 and 06: 04 runs everything that is not a named property,
# 05 the Hegel properties, 06 the proptest twins. Running the properties in 04 as well put
# the hosted run over its 30 minute limit.
# 04 also leaves out the consumer gate, which runs once as 10-consumer.
run 04-nextest cargo nextest run --workspace --locked -E 'not (test(/hegel_/) | test(/proptest_/) | test(/consumer_gate_/))'
# nextest intentionally does not execute rustdoc examples.
run 04-doctests cargo test --workspace --doc --locked -- --test-threads=2
run 05-hegel cargo nextest run --workspace --locked -E 'test(/hegel_/)'
run 06-proptest cargo nextest run --workspace --locked -E 'test(/proptest_/)'
# One temp target for both sequential mutation scripts (never the source target), removed after.
MEMORY_MUTATION_TARGET="$(mktemp -d "${TMPDIR:-/tmp}/memory-mutation-target.XXXXXX")"
export MEMORY_MUTATION_TARGET
run 06-window-mutation python3 scripts/check-window-mutation.py
run 06-mutations python3 scripts/check-mutations.py
rm -rf "$MEMORY_MUTATION_TARGET"
unset MEMORY_MUTATION_TARGET
run 07-schema cargo xtask schemas --check
# Through nextest, like 04 to 06: one process per test. Under `cargo test` every test of a binary
# shares one process, and a child spawned by one test (second_process_exits_lock_held) holds a
# copy of every open descriptor between fork and exec, so another test that releases and
# re-takes its daemon lock in that window gets lock_held (hosted runs 42565602976, 43193745120).
run 08-coverage cargo llvm-cov nextest --workspace --locked --fail-under-lines "$(< coverage-floor.txt)" --json --output-path artifacts/coverage.json
run 09-lints bash scripts/check-lints.sh
# Actual writer -> memory export -> the unchanged Python importer, event index and report
# (brief 32). The pinned analytics venv comes from MEMORY_ANALYTICS_PYTHON (default
# ../.analytics-venv/bin/python); a missing interpreter or duckdb fails this gate, never skips it.
run 10-consumer env MEMORY_REQUIRE_CONSUMER=1 cargo nextest run -p memory --test s3_store --locked -E 'test(/consumer_gate_/)'
exit "$failed"
