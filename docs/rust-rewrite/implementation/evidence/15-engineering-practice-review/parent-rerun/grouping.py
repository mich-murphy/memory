p='rust/crates/memory/src/daemon.rs'; s=open(p).read()
old='''        let mut groups: Vec<(Owner, Vec<Source>)> = Vec::new();
        for (owner, source) in self.sources.enumerate() {
            match groups.iter_mut().find(|(o, _)| *o == owner) {
                Some((_, list)) => list.push(source),
                None => groups.push((owner, vec![source])),
            }
        }
'''
new='''        let mut groups: Vec<(Owner, Vec<Source>)> = Vec::new();
        let mut group_of: HashMap<Owner, usize> = HashMap::new();
        for (owner, source) in self.sources.enumerate() {
            let index = *group_of.entry(owner.clone()).or_insert(groups.len());
            match groups.get_mut(index) {
                Some((_, list)) => list.push(source),
                None => groups.push((owner, vec![source])),
            }
        }
'''
if s.count(new)==1: print('already applied')
else:
    assert s.count(old)==1; open(p,'w').write(s.replace(old,new)); print('applied')
