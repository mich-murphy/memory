#!/bin/bash
# Rerun: bash <this file> [rounds]  (uses prebuilt $P/bin/<option> release test binaries; label results "under contention" unless quiet)
P=/private/tmp/claude-501/-Users-mm-dev-memory/c32b7bd9-0169-47d4-bf70-da528e7ec2a3/scratchpad/review/proto/p3-scratch
R=${1:-8}; : > $P/bench.txt
for r in $(seq $R); do for x in base a ab ab2 b bb c base; do echo "run$r $x $($P/bin/$x --ignored --nocapture 2>&1 | grep -E 'RESULT|size_of' | tr '\n' ' ')" >> $P/bench.txt; done; done
python3 - $P/bench.txt <<'PY'
import re,sys,statistics as st,collections
d=collections.defaultdict(list)
for l in open(sys.argv[1]):
    m=l.split()[1]
    for lab,a,t,z,tot in re.findall(r'RESULT (\S+) admission_ns=(\d+) transform_ns=(\d+) authorize_ns=(\d+) total_ns=(\d+)',l):
        d[(m,lab)].append(tuple(map(int,(a,t,z,tot))))
for (m,lab),v in sorted(d.items(),key=lambda k:(k[0][1],k[0][0])):
    f=lambda i:f"{int(st.median(x[i] for x in v))}[{min(x[i] for x in v)}-{max(x[i] for x in v)}]"
    ad=st.median(x[0] for x in v); tr=st.median(x[1] for x in v); tot=st.median(x[3] for x in v)
    print(f"{lab} {m:5} n={len(v)} admit/clone={f(0)} transform={f(1)} authorize={f(2)} total={f(3)} admit_share={100*ad/tot:.1f}% transform_share={100*tr/tot:.1f}%")
PY
