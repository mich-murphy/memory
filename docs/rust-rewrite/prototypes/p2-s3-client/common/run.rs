//! Mode dispatch shared by both probes. Modes: fake | r2 <prefix> | rss-fake.
#![allow(dead_code)]
use crate::scen::*;
use crate::store::*;
use crate::proxy::Proxy;

pub fn run<S: Store + Send + 'static>(
    mode: &str, prefix: &str,
    mut fake_mk: impl FnMut(&str, bool) -> S,
    mut r2_mk: impl FnMut(Option<&str>, bool) -> S,
    r2_rss_mk: impl Fn() -> S,
) {
    match mode {
        "fake" => emit(fault_suite(&mut fake_mk, &[false, true])),
        "r2" => {
            let mut d = r2_mk(None, false);
            emit(conformance(&mut d, prefix));
            let px = Proxy::start();
            let url = format!("http://{}", px.addr);
            let mut mk = |retries: bool| r2_mk(Some(&url), retries);
            emit(lost_suite(&mut d, &mut mk, &px, prefix));
        }
        "rss" => emit(rss(&r2_rss_mk, prefix, 3, 8)),
        _ => { eprintln!("modes: fake|r2|rss"); std::process::exit(2) }
    }
}
