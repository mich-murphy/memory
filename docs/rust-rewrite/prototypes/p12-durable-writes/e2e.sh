#!/bin/sh
# usage: p10-e2e.sh <copydir> <label>
S=/private/tmp/claude-501/-Users-mm-dev-memory/3f584787-501e-4ec6-ad81-408fa14b5f75/scratchpad
c=$1; l=$2
cd /Users/mm/dev/memory && s=$(date +%s)
nix develop --max-jobs 2 --cores 2 --command sh -c "cd $c && CARGO_TARGET_DIR=$c/target CARGO_BUILD_JOBS=2 HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory --test daemon -- --test-threads=2" > $S/p10-e2e-$l.log 2>&1
st=$?
echo "{\"label\":\"$l\",\"status\":$st,\"seconds\":$(( $(date +%s)-s ))}" > $S/p10-e2e-$l.json
