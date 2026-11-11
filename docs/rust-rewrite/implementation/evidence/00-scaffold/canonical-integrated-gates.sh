#!/usr/bin/env bash
# Invoke from the pinned Nix shell. Run all nine gates and preserve their statuses.
set -uo pipefail
cd "$(dirname "$0")/.." || exit $?
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
  "$@" > "artifacts/$name.log" 2>&1
  local status=$?
  printf '%s\t%s\n' "$name" "$status" | tee -a artifacts/status.tsv
  if [[ $status != 0 ]]; then tail -30 "artifacts/$name.log"; failed=1; fi
}
run 01-fmt cargo fmt --check
run 02-clippy cargo clippy --workspace --all-targets --locked -- -Dwarnings
run 03-deny cargo deny --locked check
run 03-core-bans bash -c 'cd crates/memory-core && cargo deny --locked --exclude-dev check bans'
run 04-nextest cargo nextest run --workspace --locked
# nextest intentionally does not execute rustdoc examples.
run 04-doctests cargo test --workspace --doc --locked -- --test-threads=2
run 05-hegel cargo test -p memory-core --test scaffold hegel_ --locked -- --test-threads=2 --nocapture
run 06-proptest cargo test -p memory-core --test scaffold proptest_ --locked -- --test-threads=2 --nocapture
run 06-window-mutation python3 scripts/check-window-mutation.py
run 07-schema cargo xtask schemas --check
run 08-coverage cargo llvm-cov --workspace --locked --fail-under-lines "$(< coverage-floor.txt)" --json --output-path artifacts/coverage.json -- --test-threads=2
run 09-lints bash scripts/check-lints.sh
exit "$failed"
