import sys
p='rust/crates/memory/src/daemon.rs'
s=open(p).read(); old,new=sys.argv[1],sys.argv[2]
assert s.count(old)==1, "no unique match"
open(p,'w').write(s.replace(old,new))
