p='rust/crates/memory/src/daemon.rs'
s=open(p).read()
def r(a,b):
    global s
    assert s.count(a)==1,a
    s=s.replace(a,b)
r("""impl RemoteBase {
    /// The fence for `commit_known`; `None` for the first write.
    const fn fence(&self) -> Option<&Base> {
        match self {
            Self::Known(base) => Some(base),
            Self::Unknown | Self::Absent => None,
        }
    }
}

""","")
r(""") -> ControlFlow<bool> {
        if !matches!(rt.base, RemoteBase::Unknown) {
            return ControlFlow::Continue(());
        }
        match recover_base(&mut self.store, owner) {
            Ok(base) => {
                rt.base = base.map_or(RemoteBase::Absent, RemoteBase::Known);
                ControlFlow::Continue(())
            }""",""") -> ControlFlow<bool, Option<Base>> {
        let recovered = match &rt.base {
            RemoteBase::Known(base) => return ControlFlow::Continue(Some(base.clone())),
            RemoteBase::Absent => return ControlFlow::Continue(None),
            RemoteBase::Unknown => recover_base(&mut self.store, owner),
        };
        match recovered {
            Ok(base) => {
                rt.base = base.clone().map_or(RemoteBase::Absent, RemoteBase::Known);
                ControlFlow::Continue(base)
            }""")
r("        rt: &OwnerRt,\n        when: &Timestamp,","        base: Option<&Base>,\n        when: &Timestamp,")
r("""        let remote = match &rt.base {
            RemoteBase::Known(base) => base.state.clone(),
            // `recover` runs first, so `Unknown` does not reach a capture.
            RemoteBase::Absent | RemoteBase::Unknown => OwnerState::default(),
        };
        capture(&mut self.fs, sources, &self.locals, &remote, &env)""","""        let empty = OwnerState::default();
        let remote = base.map_or(&empty, |b| &b.state);
        capture(&mut self.fs, sources, &self.locals, remote, &env)""")
r("        rt: &mut OwnerRt,\n        request: Commit,","        rt: &mut OwnerRt,\n        base: Option<&Base>,\n        request: Commit,")
r("            rt.base.fence(),\n","            base,\n")
r("""        if let ControlFlow::Break(pause) = self.recover(owner, rt, at, report) {
            return pause;
        }
        let mut got = self.capture_owner(owner, sources, rt, when);""","""        let base = match self.recover(owner, rt, at, report) {
            ControlFlow::Break(pause) => return pause,
            ControlFlow::Continue(base) => base,
        };
        let mut got = self.capture_owner(owner, sources, base.as_ref(), when);""")
r("self.land(owner, rt, request, got, hints, attached)","self.land(owner, rt, base.as_ref(), request, got, hints, attached)")
open(p,'w').write(s)
