#!/bin/bash
# Parent rerun of the workers' prebuilt release benchmark binaries, interleaved, no builds running.
P=/private/tmp/claude-501/-Users-mm-dev-memory/c32b7bd9-0169-47d4-bf70-da528e7ec2a3/scratchpad/review/proto; Q=/private/tmp/claude-501/-Users-mm-dev-memory/dca4e13d-dbd3-43e3-a935-f0b0b842e363/scratchpad/rerun
uptime > $Q/load-before.txt
# P2 landed: base a b c base(again)
: > $Q/p2.raw
for i in $(seq 11); do for br in base a b c base2; do src=$br; [ $br = base2 ] && src=base
  $P/p2-scratch/bin/$src --ignored --nocapture 2>&1 | awk -v b=$br '/BENCH/{print b,$3,$4,$5} /SIZE/{print b,"size",$2,$3}' >> $Q/p2.raw
done; done
# P3 authorize
: > $Q/p3.txt
for r in $(seq 11); do for x in base a ab ab2 b bb c base2; do src=$x; [ $x = base2 ] && src=base
  echo "run$r $x $($P/p3-scratch/bin/$src --ignored --nocapture 2>&1 | grep -E 'RESULT|size_of' | tr '\n' ' ')" >> $Q/p3.txt; done; done
# P1 owner-state, 1,000 owners
: > $Q/p1-1k.txt
for i in $(seq 11); do for x in base a b d base2; do IDLE_OWNERS=1000 $P/p1-scratch/bins/$x --ignored --nocapture idle_rounds 2>&1 | grep IDLE | sed "s/^/$x /" >> $Q/p1-1k.txt; done; done
uptime > $Q/load-after.txt
echo DONE-FAST
