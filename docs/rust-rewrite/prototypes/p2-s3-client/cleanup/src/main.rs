//! Deletes every object under one `rust-acceptance/p2-*` prefix (refuses anything else), then relists.
use rusty_s3::{actions::ListObjectsV2, Bucket, Credentials, S3Action, UrlStyle};
use std::time::Duration;
fn env(k: &str) -> String { std::env::var(k).unwrap_or_else(|_| { eprintln!("missing env"); std::process::exit(2) }) }
fn list(ag: &ureq::Agent, b: &Bucket, c: &Credentials, prefix: &str, n: &mut usize) -> Vec<String> {
    let (mut keys, mut tok) = (vec![], None::<String>);
    loop {
        let mut a = b.list_objects_v2(Some(c));
        a.with_prefix(prefix);
        if let Some(t) = &tok { a.with_continuation_token(t.clone()); }
        let url = a.sign(Duration::from_secs(120));
        *n += 1;
        let mut r = ag.get(url.as_str()).call().expect("list");
        let text = r.body_mut().read_to_string().expect("body");
        let p = ListObjectsV2::parse_response(&text).expect("parse");
        keys.extend(p.contents.into_iter().map(|o| o.key));
        match p.next_continuation_token { Some(t) => tok = Some(t), None => return keys }
    }
}
fn main() {
    let prefix = std::env::args().nth(1).unwrap_or_default();
    if !prefix.starts_with("rust-acceptance/p2-") || !prefix.ends_with('/') { eprintln!("refusing prefix"); std::process::exit(2) }
    let b = Bucket::new(env("R2_ENDPOINT").parse().unwrap(), UrlStyle::Path, env("R2_BUCKET"), "auto".to_string()).unwrap();
    let c = Credentials::new(env("R2_ACCESS_KEY_ID"), env("R2_SECRET_ACCESS_KEY"));
    let ag: ureq::Agent = ureq::Agent::config_builder().http_status_as_error(false).build().into();
    let (mut lists, mut dels) = (0usize, 0usize);
    let keys = list(&ag, &b, &c, &prefix, &mut lists);
    for k in &keys {
        assert!(k.starts_with(&prefix));
        let u = b.delete_object(Some(&c), k).sign(Duration::from_secs(120));
        let r = ag.delete(u.as_str()).call().expect("del"); dels += 1;
        assert!(r.status().as_u16() / 100 == 2);
    }
    let left = list(&ag, &b, &c, &prefix, &mut lists);
    println!("{{\"listed\":{},\"deleted\":{},\"list_requests\":{},\"delete_requests\":{},\"remaining_after\":{}}}", keys.len(), dels, lists, dels, left.len());
}
