#[path = "../../common/store.rs"] mod store;
#[path = "../../common/fake.rs"] mod fake;
#[path = "../../common/proxy.rs"] mod proxy;
#[path = "../../common/scen.rs"] mod scen;
#[path = "../../common/run.rs"] mod run;
mod adapter;
use store::*;
use adapter::OsStore; use std::sync::Arc;
fn main() {
    let a: Vec<String> = std::env::args().collect();
    let mode = a.get(1).map(String::as_str).unwrap_or("fake");
    let prefix = a.get(2).map(String::as_str).unwrap_or("");
    let rt = adapter::runtime();
    run::run(mode, prefix, |ep, r| OsStore::new(rt.clone(), ep, "b", "AK", "SK", r, None, 2, false, None), |px, r| { let c = Cfg::from_env(); OsStore::new(rt.clone(), &c.endpoint, &c.bucket, &c.key, &c.secret, r, px, 3, false, Some(c.clone())) }, || { let c = Cfg::from_env(); OsStore::new(rt.clone(), &c.endpoint, &c.bucket, &c.key, &c.secret, false, None, 60, false, Some(c.clone())) });
}
