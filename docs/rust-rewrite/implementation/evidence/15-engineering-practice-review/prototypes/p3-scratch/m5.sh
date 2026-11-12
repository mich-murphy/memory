cd /Users/mm/dev/memory-wt/ds-authorize
P=/private/tmp/claude-501/-Users-mm-dev-memory/c32b7bd9-0169-47d4-bf70-da528e7ec2a3/scratchpad/review/proto/p3-scratch
cg() { nix develop /Users/mm/dev/memory#rust --command sh -c "cd rust && $*"; }
T=rust/crates/memory-core/tests
M=rust/crates/memory-core/src/privacy/mod.rs
for o in base a ab b bb c; do
  git checkout -q proto/ds-authorize-$o || exit 1
  echo "===== $o $(git rev-parse --short HEAD)"
  if [ $o = base ]; then S=s; Y=y; else S='\&s'; Y='\&y'; fi
  # (ii)
  sed "s/@Y@/$Y/" $P/p3_ii.rs.tpl > $T/p3_ii.rs
  echo "--- (ii)"; cg "cargo test -p memory-core --locked --test p3_ii -- --nocapture 2>&1 | grep -E 'RESULT|^error|test result'"
  # (i)
  sed "s/@S@/$S/" $P/p3_i.rs.tpl > $T/p3_i.rs
  python3 - $M <<'PY'
import sys
p=sys.argv[1]; s=open(p).read()
s=s.replace("    pub collector_identities: Vec<u128>,\n","    pub collector_identities: Vec<u128>,\n    /// scratch\n    pub deny_all: bool,\n",1)
d="Decision" if "fn decide" in s else "Admission"
s=s.replace("    if !facts.root_resolved {","    if context.deny_all { return %s::Deny(Reason::Credential); }\n    if !facts.root_resolved {"%d,1)
open(p,"w").write(s)
PY
  echo "--- (i)"; cg "cargo test -p memory-core --locked --test p3_i -- --nocapture 2>&1 | grep -E 'RESULT|^error|-->|help|test result' | head -12"
  if [ $o = c ]; then
    python3 - $M <<'PY'
import sys
p=sys.argv[1]; s=open(p).read()
s=s.replace("        collector_identities,\n    } = context;","        collector_identities,\n        deny_all: _,\n    } = context;",1)
open(p,"w").write(s)
PY
    echo "--- (i) C lazy"; cg "cargo test -p memory-core --locked --test p3_i -- --nocapture 2>&1 | grep -E 'RESULT|^error|test result' | head"
  fi
  git checkout -q -- . ; rm -f $T/p3_i.rs $T/p3_ii.rs
  # (iii)
  cp $P/p3_iii.rs rust/crates/memory/tests/p3_iii.rs
  echo "--- (iii)"; cg "cargo test -p memory --locked --test p3_iii --no-run 2>&1 | grep -E '^error' -A4 | head -14"
  rm -f rust/crates/memory/tests/p3_iii.rs
  echo "status: [$(git status --short)]"
done
