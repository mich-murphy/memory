cd /Users/mm/dev/memory-wt/ds-authorize
P=/private/tmp/claude-501/-Users-mm-dev-memory/c32b7bd9-0169-47d4-bf70-da528e7ec2a3/scratchpad/review/proto/p3-scratch
for o in base a ab ab2 b bb c; do
  git checkout -q proto/ds-authorize-$o || exit 1
  touch rust/crates/memory-core/src/privacy/mod.rs rust/crates/memory/src/capture.rs
  echo "## $o"
  nix develop /Users/mm/dev/memory#rust --command sh -c "cd rust && CLIPPY_CONF_DIR=$P/clippyconf cargo clippy -p memory-core -p memory --lib --locked --message-format short -- -W clippy::cognitive_complexity" 2>&1 | grep -E "(privacy/mod.rs|memory/src/capture.rs):[0-9]+.*cognitive" | while IFS=: read f l c rest; do
    n=$(echo "$rest" | grep -oE '\([0-9]+/1\)'); line=$(sed -n ${l}p rust/$f | sed 's/^ *//' | cut -c1-50); echo "$n $line"; done | grep -E "fn (authorize|admit|binding|decide|authorized_body|admitted_clone)\b"
done
git checkout -q proto/ds-authorize-c; git status --short
for o in base a ab ab2 b bb c; do echo "$o: $(git show proto/ds-authorize-$o:rust/crates/memory-core/src/privacy/mod.rs | grep -c 'None =>')"; done
