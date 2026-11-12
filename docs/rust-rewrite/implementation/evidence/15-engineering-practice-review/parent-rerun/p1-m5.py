import sys
p='rust/crates/memory/src/daemon.rs'; s=open(p).read(); mode=sys.argv[1]
if mode=='b':
    old="        if let ControlFlow::Break(pause) = self.recover(owner, rt, at, report) {\n            return pause;\n        }\n"; new=""
elif mode=='d-skip':
    old="        let base = match self.recover(owner, rt, at, report) {\n            ControlFlow::Break(pause) => return pause,\n            ControlFlow::Continue(base) => base,\n        };\n"; new=""
elif mode=='d-fabricate':
    old="        let base = match self.recover(owner, rt, at, report) {\n            ControlFlow::Break(pause) => return pause,\n            ControlFlow::Continue(base) => base,\n        };\n"; new="        let base: Option<Base> = None;\n"
n=s.count(old); print('edits',n); assert n==1
open(p,'w').write(s.replace(old,new))
