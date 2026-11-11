#[path = "../../common/store.rs"] mod store;
#[path = "../../common/fake.rs"] mod fake;
#[path = "../../common/proxy.rs"] mod proxy;
#[path = "../../common/scen.rs"] mod scen;
#[path = "../../common/run.rs"] mod run;
mod adapter;
use store::*;
use adapter::RsStore;
fn main() {
    let a: Vec<String> = std::env::args().collect();
    let mode = a.get(1).map(String::as_str).unwrap_or("fake");
    let prefix = a.get(2).map(String::as_str).unwrap_or("");
    
    if mode == "p2b" {
        let c = Cfg::from_env();
        let mut s = RsStore::new(&c.endpoint, &c.bucket, &c.key, &c.secret, None, 30, Some(c.clone()));
        for (n, r) in adapter::p2b(&mut s, prefix) { println!("{{\"suite\":\"p2b\",\"case\":\"{n}\",\"result\":{:?}}}", c.redact(&r)); }
        return;
    }
    run::run(mode, prefix, |ep, _r| RsStore::new(ep, "b", "AK", "SK", None, 2, None), |px, _r| { let c = Cfg::from_env(); RsStore::new(&c.endpoint, &c.bucket, &c.key, &c.secret, px, 3, Some(c.clone())) }, || { let c = Cfg::from_env(); let mut s = RsStore::new(&c.endpoint, &c.bucket, &c.key, &c.secret, None, 60, Some(c.clone())); s.md5 = true; s });
}
