#!/bin/bash
# Run from repository root. Logs/statuses are evidence, not a production gate.
set -u
out=docs/rust-rewrite/implementation/evidence/03-bc-recheck
scratch=$(<"$out/scratch-path.txt")
case "$scratch" in /tmp/memory-slice0-official-bc.*) ;; *) exit 2 ;; esac
record() {
  label=$1
  shift
  "$@"
  result=$?
  printf '%s\t%s\n' "$label" "$result" >> "$out/status.tsv"
  return "$result"
}
record transfer scp -q -o BatchMode=yes -o ConnectTimeout=10 -r "$out/input/." "bc-01:$scratch/" > "$out/transfer.log" 2>&1 || exit "$?"
record remote-input-hashes ssh -o BatchMode=yes -o ConnectTimeout=10 bc-01 "sh -c 'cd $scratch && uname -sm && nix --version && sha256sum flake.nix flake.lock .golangci-lint-version Cargo.toml tests/coalesce.rs verify.sh'" > "$out/remote-before.log" 2>&1 || exit "$?"
record native-nix-hegel ssh -o BatchMode=yes -o ConnectTimeout=10 bc-01 "sh -c 'cd $scratch && HEGEL_TEST_CASES=2000 HEGEL_SEED=730052 CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2 nix develop --no-update-lock-file --max-jobs 2 --cores 2 --command sh ./verify.sh'" > "$out/native-nix-hegel.log" 2>&1
native_result=$?
# Collect diagnostic outputs even on failure; never mask the gate's exact status.
record collect ssh -o BatchMode=yes -o ConnectTimeout=10 bc-01 "sh -c 'cd $scratch && tar -cf - remote-status.tsv Cargo.lock flake.nix flake.lock .golangci-lint-version Cargo.toml tests/coalesce.rs verify.sh'" > "$out/remote-output.tar" 2> "$out/collect.log"
exit "$native_result"
