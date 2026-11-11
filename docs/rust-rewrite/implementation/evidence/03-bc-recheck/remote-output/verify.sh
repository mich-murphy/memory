#!/bin/sh
# Synthetic native check only. No project gate or production source is modified.
set -u
run() {
  label=$1
  shift
  printf '\nCOMMAND [%s]:' "$label"
  printf ' %s' "$@"
  printf '\n'
  "$@"
  result=$?
  printf '%s\t%s\n' "$label" "$result" >> remote-status.tsv
  printf 'STATUS [%s]: %s\n' "$label" "$result"
  if [ "$result" -ne 0 ]; then exit "$result"; fi
}
: > remote-status.tsv
run uname uname -sm
run rustc rustc -Vv
run cargo cargo -V
run llvm-cov "$LLVM_COV" --version
run llvm-profdata "$LLVM_PROFDATA" --version
printf 'HEGEL_TEST_CASES=%s HEGEL_SEED=%s CARGO_BUILD_JOBS=%s RUST_TEST_THREADS=%s\n' "$HEGEL_TEST_CASES" "$HEGEL_SEED" "$CARGO_BUILD_JOBS" "$RUST_TEST_THREADS"
# Preserve frozen research manifest unchanged; pin its resolved version in scratch lock.
run lock-generate cargo generate-lockfile
run lock-pin-hegel cargo update -p hegeltest --precise 0.52.0
run feature-tree cargo tree --locked -e features
run hegel cargo test --locked --test coalesce pieces_concatenate -- --test-threads=2
run post-hashes sha256sum flake.nix flake.lock .golangci-lint-version Cargo.toml tests/coalesce.rs verify.sh Cargo.lock
