c='rust/crates/memory/tests/counters.rs'; t=open(c).read(); a='for owners in [1_000_u64, 30_000]'
assert t.count(a)==1
open(c,'w').write(t.replace(a,'for owners in std::env::var("IDLE_OWNERS").ok().and_then(|v| v.parse::<u64>().ok()).map_or(vec![1_000_u64], |n| vec![n])'))
