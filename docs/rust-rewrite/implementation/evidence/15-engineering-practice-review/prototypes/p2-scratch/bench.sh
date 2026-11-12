#!/usr/bin/env bash
# Usage: bench.sh [rounds]  (default 7). Builds each option's landed_bench in release, runs interleaved.
S=/private/tmp/claude-501/-Users-mm-dev-memory/c32b7bd9-0169-47d4-bf70-da528e7ec2a3/scratchpad/review/proto/p2-scratch
R=${1:-7}
cd /Users/mm/dev/memory-wt/ds-landed || exit 1
mkdir -p $S/bin
for br in base a b c; do
  git checkout -q proto/ds-landed-$br || exit 1
  nix develop /Users/mm/dev/memory#rust --command sh -c 'cd rust && cargo test -p memory --locked --release --test landed_bench --no-run 2>&1 | tail -1'
  f=$(ls -t rust/target/release/deps/landed_bench-* | grep -v '\.d$' | head -1)
  cp "$f" $S/bin/$br
done
git checkout -q proto/ds-landed-base
: > $S/bench.raw
for i in $(seq $R); do for br in base a b c base2; do
  src=$br; [ $br = base2 ] && src=base
  $S/bin/$src --ignored --nocapture 2>&1 | awk -v b=$br '/BENCH/{print b,$3,$4,$5} /SIZE/{print b,"size",$2,$3}' >> $S/bench.raw
done; done
python3 - <<'PY'
import statistics as st,collections,re
S="/private/tmp/claude-501/-Users-mm-dev-memory/c32b7bd9-0169-47d4-bf70-da528e7ec2a3/scratchpad/review/proto/p2-scratch"
d=collections.defaultdict(lambda:collections.defaultdict(list));sz={}
for l in open(S+"/bench.raw"):
    p=l.split()
    if p[1]=="size": sz[p[0]]=(p[2],p[3]);continue
    for x in p[1:]:
        k,v=x.split("=");d[p[0]][k].append(int(v.rstrip("ns")))
for b in ["base","a","b","c","base2"]:
    print(b,{k:f"med {int(st.median(v))} [{min(v)}-{max(v)}]" for k,v in d[b].items()},sz.get(b,""))
PY
