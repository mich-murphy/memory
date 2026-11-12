#!/usr/bin/env bash
# Per-branch M1/M3/predicates/CommitOutcome users. Run from anywhere.
S=/private/tmp/claude-501/-Users-mm-dev-memory/c32b7bd9-0169-47d4-bf70-da528e7ec2a3/scratchpad/review/proto/p2-scratch
cd /Users/mm/dev/memory-wt/ds-landed
for br in base a b c; do
  git checkout -q proto/ds-landed-$br || exit 1
  echo "######## $br $(git log --oneline -1)"
  nix develop /Users/mm/dev/memory#rust --command sh -c '
    cd rust
    cargo fmt --check && echo FMT-OK
    cargo clippy --workspace --all-targets --locked -- -Dwarnings 2>&1 | tail -1
    cargo nextest run -p memory --locked -E "binary(commit_protocol) | binary(capture_property) | binary(daemon) | binary(mutation_daemon)" 2>&1 | tail -1
    cargo nextest run -p memory-core --locked -E "binary(core_verify) | binary(core_manifest)" 2>&1 | tail -1
    touch crates/memory/src/lib.rs
    CLIPPY_CONF_DIR='$S'/clippyconf cargo clippy -p memory --lib --locked -- -A clippy::all -W clippy::cognitive_complexity 2>&1 | grep -A1 "cognitive complexity of" | grep -E "complexity|-->" | paste - - | grep -E "commit.rs|daemon.rs" | sed -E "s/ +/ /g" | grep -E "commit.rs:(3[0-9][0-9]|[6-9][0-9]|1[0-9][0-9])|daemon.rs:(6[4-9][0-9]|7[0-9][0-9]|8[0-9][0-9])"
  '
  python3 - <<'PY'
a=open('rust/crates/memory/src/daemon.rs').read(); c=open('rust/crates/memory/src/commit.rs').read()
print("PRED advance-on-fail:", a.count("        if let Ok(landed) = &result {\n            // Remote is authoritative"), "| if-match:", c.count("Some(etag) => store.put_if_match(&key, etag, Published::metadata(pointer.clone())),"))
PY
  echo "CommitOutcome non-test users:"; rg -n 'CommitOutcome::(Committed|AlreadyOurs)' rust/crates/*/src | sed 's/^/  /'
done
git checkout -q proto/ds-landed-base
