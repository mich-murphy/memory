p='rust/crates/memory/src/daemon.rs'
s=open(p).read()
def r(a,b):
    global s
    assert s.count(a)==1,a
    s=s.replace(a,b)
r("        attached: bool,\n    ) -> Result<Landed, Failure> {","        attached_for: Option<&Owner>,\n    ) -> Result<Landed, Failure> {")
r("""            if attached && !landed.follow_up {
                for record in hints.iter_mut().flat_map(|f| f.records.iter_mut()) {
                    record.done |= record.owner == *owner;
                }
            }""","""            if let Some(owner) = attached_for
                && !landed.follow_up
            {
                for record in hints.iter_mut().flat_map(|f| f.records.iter_mut()) {
                    record.done |= record.owner == *owner;
                }
            }""")
r("self.land(owner, rt, base.as_ref(), request, got, hints, attached)","self.land(rt, base.as_ref(), request, got, hints, attached.then_some(owner))")
r("    fn land(\n        &mut self,\n        owner: &Owner,\n","    fn land(\n        &mut self,\n")
open(p,'w').write(s)
