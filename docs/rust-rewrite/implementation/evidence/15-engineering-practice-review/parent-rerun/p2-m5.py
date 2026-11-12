import sys
p='rust/crates/memory/src/daemon.rs'; s=open(p).read(); mode=sys.argv[1]
helper="            if let CommitEnd::Landed(landed) = end {\n                self.apply_landed(landed, got.updates, hints, attached.then_some(owner));\n            }"
if mode=='c-helper':   # hand the adopted value to the helper
    n=s.count(helper); s=s.replace(helper,"            match end {\n                CommitEnd::Landed(l) => self.apply_landed(l, got.updates, hints, attached.then_some(owner)),\n                CommitEnd::Adopted(a) => self.apply_landed(a, got.updates, hints, attached.then_some(owner)),\n            }")
elif mode=='c-inline': # copy the effects into the adopted arm
    n=s.count(helper); s=s.replace(helper,"            match end {\n                CommitEnd::Landed(l) => self.apply_landed(l, got.updates, hints, attached.then_some(owner)),\n                CommitEnd::Adopted(_) => { self.apply(got.updates); if attached { for r in hints.iter_mut().flat_map(|f| f.records.iter_mut()) { r.done |= r.owner == *owner; } } }\n            }")
else:
    old={'base':'!landed.follow_up','a':'!matches!(landed, Landed::AdoptedEarlier { .. })','b':'landed.how != LandedHow::AdoptedEarlier'}[mode]
    n=s.count(old); s=s.replace(old,'true')
print('edits',n); assert n>=1
open(p,'w').write(s)
