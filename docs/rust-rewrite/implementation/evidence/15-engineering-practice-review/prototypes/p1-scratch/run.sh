#!/bin/bash
# Usage: bash run.sh   (from anywhere). Does M1, M3, M4, M5, size_of, builds and runs the M6 benchmark.
S=/private/tmp/claude-501/-Users-mm-dev-memory/c32b7bd9-0169-47d4-bf70-da528e7ec2a3/scratchpad/review/proto/p1-scratch
W=/Users/mm/dev/memory-wt/ds-owner-state; cd $W
D=rust/crates/memory/src/daemon.rs; C=rust/crates/memory/tests/counters.rs
nx() { nix develop /Users/mm/dev/memory#rust --command sh -c "cd rust && $1"; }
mkdir -p $S/clip $S/bins; printf 'cognitive-complexity-threshold = 1\n' > $S/clip/clippy.toml
BR="base a b d"
m5() { git checkout -q proto/ds-owner-state-$1; python3 $S/m5.py "$3" "$4" || { echo "$1 $2 EDIT FAILED"; git checkout -q -- .; return; }
 out=$(nx 'cargo check -p memory --locked' 2>&1)
 if echo "$out" | grep -q '^error'; then echo "$1 $2 ERROR: $(echo "$out" | grep -A5 '^error' | head -7)"; else echo "$1 $2 compiles"; fi; git checkout -q -- .; }
REC=$'        if let ControlFlow::Break(pause) = self.recover(owner, rt, at, report) {\n            return pause;\n        }\n'
echo "##### M5"
m5 base i "$(printf '            rt.base = None;\n            rt.needs_recover = true;\n')" $'            rt.base = None;\n'
m5 base ii $'                rt.parked = Some(ParkedReason::Source(reason));\n' $'                rt.parked = Some(ParkedReason::Source(reason));\n                rt.not_before = Some(at);\n'
m5 base iii "$REC" ""
for x in a b d; do m5 $x i $'            rt.base = RemoteBase::Unknown;\n' $'            rt.base = RemoteBase::Unknown;\n            rt.needs_recover = true;\n'; done
m5 a ii $'                rt.parked = Some(ParkedReason::Source(reason));\n' $'                rt.parked = Some(ParkedReason::Source(reason));\n                rt.not_before = Some(at);\n'
for x in b d; do m5 $x ii 'rt.turn = Turn::Parked(ParkedReason::Source(reason));' 'rt.turn = Turn::Parked(ParkedReason::Source(reason)); rt.not_before = Some(at);'; done
m5 a iii "$REC" ""; m5 b iii "$REC" ""
m5 d iii 'self.capture_owner(owner, sources, base.as_ref(), when)' 'self.capture_owner(owner, sources, Some(&rt.base), when)'
git status --short
for x in $BR; do git checkout -q proto/ds-owner-state-$x; echo "##### $x $(git rev-parse --short HEAD)"
 echo "M1 focused:"; nx 'cargo nextest run -p memory --locked -E "binary(daemon) | binary(mutation_daemon) | binary(counters) | binary(state)" 2>&1 | tail -1'
 echo "fmt:"; nx 'cargo fmt --check && echo fmt-ok'
 echo "clippy:"; nx 'cargo clippy --workspace --all-targets --locked -- -Dwarnings 2>&1 | tail -1'
 echo "M4:"; git diff --numstat proto/ds-owner-state-base | cat
 python3 - <<PY
import re
s=open('$D').read()
for n in ['settle','due','recover','capture_owner','land','run_owner']:
    m=re.search(r'\n    (?:pub )?fn '+n+r'\(.*?\n    }\n',s,re.S)
    b=m.group(0); print('decision points',n,len(re.findall(r'\bif\b|\bmatch\b|matches!|is_some|is_none|map_or|\bwhile\b',b)),'wildcard',len(re.findall(r'\b_ =>',b)))
print('predicate count',s.count('        if let Ok(landed) = &result {\n            // Remote is authoritative'))
PY
 touch $D; echo "cognitive:"
 nx "CLIPPY_CONF_DIR=$S/clip cargo clippy -p memory --lib --locked -- -W clippy::cognitive_complexity 2>&1 | grep -A1 'cognitive complexity' | grep -E 'complexity|daemon.rs' | paste - - | grep daemon.rs | sed -E 's/.*complexity of \(([0-9]+)\/1\).*--> (crates\/memory\/src\/daemon.rs):([0-9]+).*/\3 \1/'" | sort -n | while read l c; do echo "  $c  $(sed -n "${l}p" $D | sed 's/^ *//' | cut -c1-50)"; done
 printf '\n#[cfg(test)]\nmod sz {\n    #[test]\n    fn owner_rt_size() {\n        eprintln!("SIZE_OF_OWNERRT {}", std::mem::size_of::<super::OwnerRt>());\n    }\n}\n' >> $D
 nx 'cargo test -p memory --locked --lib sz:: -- --nocapture 2>&1 | grep SIZE_OF'; git checkout -q -- $D
 python3 - <<PY
p='$C'; s=open(p).read(); a='for owners in [1_000_u64, 30_000]'
assert s.count(a)==1
open(p,'w').write(s.replace(a,'for owners in std::env::var("IDLE_OWNERS").ok().and_then(|v| v.parse::<u64>().ok()).map_or(vec![1_000_u64], |n| vec![n])'))
PY
 nx 'cargo test -p memory --locked --release --no-run --test counters 2>&1 | tail -1'
 cp $(ls -t rust/target/release/deps/counters-* | grep -v '\.d$' | head -1) $S/bins/$x; git checkout -q -- $C
done
git status --short
echo "##### M6 (under contention): base a b d base2, 8 rounds, 1,000 owners"
cp $S/bins/base $S/bins/base2
for i in 1 2 3 4 5 6 7 8; do for x in base a b d base2; do IDLE_OWNERS=1000 $S/bins/$x --ignored --nocapture idle_rounds 2>&1 | grep IDLE | sed "s/^/$x /"; done; done > $S/bench1k.txt
for i in 1 2 3; do for x in base d; do IDLE_OWNERS=30000 $S/bins/$x --ignored --nocapture idle_rounds 2>&1 | grep IDLE | sed "s/^/$x /"; done; done > $S/bench30k.txt
python3 - <<PY
import re,statistics as st,collections
for f in ['$S/bench1k.txt','$S/bench30k.txt']:
    d=collections.defaultdict(list)
    for l in open(f):
        m=re.match(r'(\w+) IDLE owners=(\d+) phase=(\w+) median_us=(\d+)',l); d[(m[1],m[2],m[3])].append(int(m[4]))
    print(f)
    for k,v in sorted(d.items(),key=lambda kv:(kv[0][1],kv[0][2],kv[0][0])): print(k,'median',st.median(v),'min-max',min(v),max(v),'n',len(v))
PY
git checkout -q proto/ds-owner-state-d
echo "##### whole nextest on d"; nx 'cargo nextest run -p memory --locked 2>&1 | tail -2'
git status --short; echo DONE
