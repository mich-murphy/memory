//! Client-independent scenarios over the `Store` seam. Output is JSON lines.
#![allow(dead_code)]
use crate::fake::{Fake, Fault};
use crate::proxy::Proxy;
use crate::store::*;
use md5::{Digest, Md5};
use serde_json::{json, Value};

pub fn emit(v: Vec<Value>) { for x in v { println!("{x}"); } }
pub fn sleep(ms: u64) { std::thread::sleep(std::time::Duration::from_millis(ms)); }
fn e<T>(r: &Result<T, StoreError>) -> String { match r { Ok(_) => "Ok".into(), Err(x) => format!("{x:?}") } }
fn md5hex(b: &[u8]) -> String { format!("{:x}", Md5::digest(b)) }

pub fn conformance<S: Store>(s: &mut S, prefix: &str) -> Vec<Value> {
    let mut o = vec![];
    let k = format!("{prefix}/cond.json");
    let miss = format!("{prefix}/missing.json");
    let (a, b, c) = (b"{\"v\":\"A\"}".to_vec(), b"{\"v\":\"B\"}".to_vec(), b"{\"v\":\"C\"}".to_vec());
    let r1 = s.put_if_absent(&k, a.clone());
    o.push(json!({"t":"create_new","r":e(&r1)}));
    let Ok(e1) = r1 else { return o };
    o.push(json!({"t":"etag_is_md5_of_body","v": e1 == format!("\"{}\"", md5hex(&a))}));
    let r2 = s.put_if_absent(&k, b"zzz".to_vec());
    o.push(json!({"t":"create_again","r":e(&r2)}));
    let g = s.get(&k);
    o.push(json!({"t":"get_after_create","r":e(&g),"body_eq":g.as_ref().map(|x|x.body==a).unwrap_or(false),"etag_eq_put_reply":g.as_ref().map(|x|x.etag==e1).unwrap_or(false)}));
    sleep(1200);
    let r3 = s.put_if_match(&k, &e1, b.clone());
    o.push(json!({"t":"match_fresh","r":e(&r3)}));
    let Ok(e2) = r3 else { return o };
    sleep(1200);
    let r4 = s.put_if_match(&k, &e1, c);
    o.push(json!({"t":"match_stale","r":e(&r4)}));
    let g2 = s.get(&k);
    o.push(json!({"t":"get_after_stale","r":e(&g2),"body_is_B":g2.as_ref().map(|x|x.body==b).unwrap_or(false),"etag_eq_put_reply":g2.as_ref().map(|x|x.etag==e2).unwrap_or(false)}));
    let r5 = s.put_if_match(&miss, "\"deadbeefdeadbeefdeadbeefdeadbeef\"", a);
    o.push(json!({"t":"match_missing_key","r":e(&r5)}));
    let r6 = s.get(&miss);
    o.push(json!({"t":"get_missing","r":e(&r6)}));
    o
}

pub fn fault_suite<S: Store>(mk: &mut dyn FnMut(&str, bool) -> S, variants: &[bool]) -> Vec<Value> {
    let mut o = vec![];
    for &retries in variants {
        let fake = Fake::start();
        let ep = format!("http://{}", fake.addr);
        let mut s = mk(&ep, retries);
        let key = "b/current.json";
        let _ = s.put_if_absent(key, b"v0".to_vec());
        let cases: Vec<(&str, Fault)> = vec![
            ("commit_then_503", Fault::CommitThen(503)),
            ("commit_then_500", Fault::CommitThen(500)),
            ("commit_then_drop", Fault::CommitThenDrop),
            ("commit_then_hang_timeout", Fault::CommitThenHang(4000)),
            ("429_retry_after_7_no_commit", Fault::Status(429, Some(7))),
            ("503_no_commit", Fault::Status(503, None)),
            ("500_no_commit", Fault::Status(500, None)),
            ("403", Fault::Status(403, None)),
            ("401", Fault::Status(401, None)),
        ];
        for (name, f) in cases {
            let cur = s.get(key).unwrap();
            let body = format!("v-{name}").into_bytes();
            fake.queue(f);
            fake.clear_log();
            let r = s.put_if_match(key, &cur.etag, body.clone());
            let detail = s.last_detail();
            let ra = s.last_retry_after();
            let puts = fake.statuses("PUT", key);
            let landed = s.get(key).map(|x| x.body == body).unwrap_or(false);
            o.push(json!({"suite":"fault","retries":retries,"op":"put_if_match","t":name,"r":e(&r),"server_put_statuses":puts,"server_puts":puts.len(),"landed":landed,"retry_after_s":ra,"detail":detail}));
        }
        // put_if_absent with committed-then-503 (objects/ and manifests/ use this call)
        for (name, f) in [("commit_then_503", Fault::CommitThen(503)), ("503_no_commit", Fault::Status(503, None))] {
            let k2 = format!("b/obj-{name}");
            fake.queue(f);
            fake.clear_log();
            let r = s.put_if_absent(&k2, b"obj".to_vec());
            let puts = fake.statuses("PUT", &k2);
            o.push(json!({"suite":"fault","retries":retries,"op":"put_if_absent","t":name,"r":e(&r),"server_put_statuses":puts,"server_puts":puts.len(),"detail":s.last_detail()}));
        }
        let mut dead = mk("http://127.0.0.1:1", retries);
        let r = dead.get("x");
        o.push(json!({"suite":"fault","retries":retries,"op":"get","t":"connection_refused","r":e(&r),"detail":dead.last_detail()}));
    }
    o
}

pub fn lost_suite<S: Store>(direct: &mut S, mk: &mut dyn FnMut(bool) -> S, px: &Proxy, prefix: &str) -> Vec<Value> {
    let mut o = vec![];
    let key = format!("{prefix}/current.json");
    let e0 = direct.put_if_absent(&key, b"v0".to_vec()).unwrap();
    for (phase, retries, from, body) in [("retries_disabled", false, e0.clone(), b"v1".to_vec()), ("client_default_retries", true, String::new(), b"v2".to_vec())] {
        sleep(1300);
        let cur = direct.get(&key).unwrap();
        let _ = from;
        let mut p = mk(retries);
        px.arm(1);
        let c0 = px.connects();
        let t0 = std::time::Instant::now();
        let r = p.put_if_match(&key, &cur.etag, body.clone());
        let ms = t0.elapsed().as_millis() as u64;
        let c1 = px.connects();
        px.arm(0);
        let after = direct.get(&key).unwrap();
        o.push(json!({"suite":"lost_reply_real_r2","phase":phase,"r":e(&r),"tunnel_connections":c1-c0,"elapsed_ms":ms,
            "landed_body_is_ours":after.body==body,"etag_changed":after.etag!=cur.etag,
            "verify_get_verdict": if after.body==body {"AlreadyOurs"} else {"BaseUnchanged"}}));
    }
    o
}

fn gen(n: usize, mut x: u64) -> Vec<u8> {
    let mut v = Vec::with_capacity(n);
    while v.len() < n { x ^= x << 13; x ^= x >> 7; x ^= x << 17; v.extend_from_slice(&x.to_le_bytes()); }
    v.truncate(n);
    v
}

pub fn rss<S: Store + Send + 'static>(mk: &dyn Fn() -> S, prefix: &str, rounds: usize, threads: usize) -> Vec<Value> {
    let mut o = vec![];
    for r in 0..rounds {
        let t0 = std::time::Instant::now();
        let hs: Vec<_> = (0..threads).map(|t| {
            let mut s = mk();
            let key = format!("{prefix}/rss-{r}-{t}.bin");
            std::thread::spawn(move || { let body = gen(8 << 20, 0x9E37 + (r * 100 + t) as u64); let t1 = std::time::Instant::now(); let x = s.put_if_absent(&key, body); (e(&x), t1.elapsed().as_millis() as u64) })
        }).collect();
        let res: Vec<_> = hs.into_iter().map(|h| h.join().unwrap()).collect();
        o.push(json!({"suite":"rss","round":r,"threads":threads,"results":res.iter().map(|x|x.0.clone()).collect::<Vec<_>>(),"per_put_ms":res.iter().map(|x|x.1).collect::<Vec<_>>(),"wall_ms":t0.elapsed().as_millis() as u64}));
    }
    o
}
