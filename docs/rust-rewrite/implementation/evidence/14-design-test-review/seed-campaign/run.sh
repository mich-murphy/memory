#!/bin/bash
# 30 random-seed runs at library-default case counts for each of the four state machines.
O=/private/tmp/claude-501/-Users-mm-dev-memory/83693829-eddb-4e4e-a499-380d68c821f5/scratchpad/guards6
cd /Users/mm/dev/memory/rust || exit 1
unset HEGEL_TEST_CASES PROPTEST_CASES HEGEL_SEED PROPTEST_RNG_SEED
export HEGEL_DATABASE=disabled CARGO_BUILD_JOBS=2
one() { # bin test i
  log=$O/$2.$3.log
  cargo test -p memory --locked --test "$1" "$2" -- --exact > "$log" 2>&1
  printf '%s\t%s\t%s\n' "$2" "$3" "$?" >> $O/results.tsv
}
cargo test -p memory --locked --test daemon --test commit_protocol --no-run > $O/build.log 2>&1 || exit 1
for i in $(seq 1 40); do
  one daemon hegel_daemon_state_machine $i &
  one commit_protocol hegel_property4_commit_state_machine $i &
  wait
  one daemon proptest_daemon_state_machine $i &
  one commit_protocol proptest_property4_commit_state_machine $i &
  wait
done
echo finished >> $O/results.tsv
