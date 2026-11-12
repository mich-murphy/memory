cd /Users/mm/dev/memory-wt/ds-authorize
P=/private/tmp/claude-501/-Users-mm-dev-memory/c32b7bd9-0169-47d4-bf70-da528e7ec2a3/scratchpad/review/proto/p3-scratch
cg() { nix develop /Users/mm/dev/memory#rust --command sh -c "cd rust && $*" 2>&1; }
git checkout -q -B proto/ds-authorize-ab2 proto/ds-authorize-ab || exit 1
python3 $P/ab2.py && cg "cargo fmt" >/dev/null && git commit -qam "memory: AdmittedSource stores the validated owner; authorize has no None arm" && echo "AB2 $(git rev-parse --short HEAD)"
OPTS="base a ab ab2 b bb c"
M=rust/crates/memory-core/src/privacy/mod.rs
for o in $OPTS; do
  git checkout -q proto/ds-authorize-$o || exit 1
  echo "##### M1 $o $(git rev-parse --short HEAD)"
  cg "cargo fmt --check" >/dev/null && echo "fmt ok" || echo "fmt FAIL"
  cg "cargo clippy --workspace --all-targets --locked -- -Dwarnings" | tail -1
  cg "cargo nextest run -p memory-core --locked" | grep -E "Summary|FAIL"
  cg "cargo test -p memory-core --doc --locked -- --test-threads=2" | grep "test result"
  cg "cargo nextest run -p memory --locked -E 'binary(capture) | binary(capture_property) | binary(resolve) | binary(mutation_daemon)'" > $P/nx_$o.txt
  grep -E "Summary|FAIL" $P/nx_$o.txt; echo "binaries: $(grep -oE 'PASS \[[^]]*\] \([0-9/]*\) [a-z_:]+' $P/nx_$o.txt | awk '{print $NF}' | sort | uniq -c | tr '\n' ' ')"
  echo "--- M4 $o"; git diff --numstat proto/ds-authorize-base | grep -v authorize_bench | awk '/\/src\//{s+=$1+$2} /\/tests\//{t+=$1+$2} END{print "src="s+0" tests="t+0}'
  echo "--- predicate count $o"; python3 - "$o" <<'PY'
import sys
s=open("rust/crates/memory-core/src/privacy/mod.rs").read()
print(s.count("        if is_credential(path) {\n            return Admission::Deny(Reason::Credential);"), s.count("        if is_credential(path) {\n            return Decision::Deny(Reason::Credential);"))
PY
  echo "--- cognitive $o"
  touch $M rust/crates/memory/src/capture.rs
  nix develop /Users/mm/dev/memory#rust --command sh -c "cd rust && CLIPPY_CONF_DIR=$P/clippyconf cargo clippy -p memory-core -p memory --lib --locked --message-format short -- -W clippy::cognitive_complexity" 2>&1 | grep -E "(privacy/mod.rs|memory/src/capture.rs):[0-9]+.*cognitive" | while IFS=: read f l c rest; do echo "$(echo $rest | grep -oE '\([0-9]+/1\)') $(sed -n ${l}p $f | sed 's/^ *//' | cut -c1-60)"; done | grep -E "fn (authorize|admit|binding|decide|authorized_body|admitted_clone)\b"
  echo "--- decision points $o"; python3 - <<'PY'
import re
s=open("rust/crates/memory-core/src/privacy/mod.rs").read()
c=open("rust/crates/memory/src/capture.rs").read()
def body(t,n):
    m=re.search(r"\n *(pub )?fn "+n+r"\b.*?\n(    )?\}\n",t,re.S); return m.group(0) if m else None
for t,n in [(s,"authorize"),(s,"admit"),(s,"binding"),(s,"decide"),(c,"authorized_body"),(c,"admitted_clone")]:
    b=body(t,n)
    if b: print(n,"if/else-if:",len(re.findall(r"\bif\b",b)),"match arms(=>):",b.count("=>"),"let-else:",len(re.findall(r"let Some.*else",b)),"matches!/is_*:",len(re.findall(r"matches!|is_none|is_some|is_empty",b)))
PY
done
echo "##### tests catching credential deny"; grep -ln "Reason::Credential" rust/crates/memory-core/tests/*.rs rust/crates/memory/tests/*.rs
echo "##### BENCH"
git checkout -q proto/ds-authorize-base
for o in $OPTS; do git checkout -q proto/ds-authorize-$o; cg "cargo test -p memory --release --locked --test authorize_bench --no-run" >/dev/null; cp $(ls -t rust/target/release/deps/authorize_bench-* | grep -v '\.d$' | head -1) $P/bin/$o; done
bash $P/bench.sh
git checkout -q proto/ds-authorize-c; git status --short
