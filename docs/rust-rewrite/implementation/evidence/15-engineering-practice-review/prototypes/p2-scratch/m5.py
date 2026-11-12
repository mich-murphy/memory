import sys
p='rust/crates/memory/src/daemon.rs'
s=open(p).read()
mode=sys.argv[1]
if mode=='c1':
    old="            if let CommitEnd::Landed(landed) = end {\n                self.apply_landed(landed, got.updates, hints, attached.then_some(owner));\n            }"
    new="            match end {\n                CommitEnd::Landed(l) => self.apply_landed(l, got.updates, hints, attached.then_some(owner)),\n                CommitEnd::Adopted(a) => self.apply_landed(a, got.updates, hints, attached.then_some(owner)),\n            }"
elif mode=='c2':
    old="            if let CommitEnd::Landed(landed) = end {\n                self.apply_landed(landed, got.updates, hints, attached.then_some(owner));\n            }"
    new="            match end {\n                CommitEnd::Landed(l) => self.apply_landed(l, got.updates, hints, attached.then_some(owner)),\n                CommitEnd::Adopted(_) => { self.apply(got.updates); if attached { for r in hints.iter_mut().flat_map(|f| f.records.iter_mut()) { r.done |= r.owner == *owner; } } }\n            }"
else:
    s=s.replace("if !matches!(landed, Landed::AdoptedEarlier { .. }) {","if true {").replace("if attached && !matches!(landed, Landed::AdoptedEarlier { .. }) {","if attached {")
    old=new=""
if old:
    assert old in s; s=s.replace(old,new)
open(p,'w').write(s)
