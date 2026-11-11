//! Probe copy of the `Store` seam in rust/crates/memory/src/environment.rs (ETag is a String here).
#![allow(dead_code)]

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StoreError { PreconditionFailed, NotFound, Offline, Throttled, Server, Auth, Ambiguous }

pub struct Stored { pub body: Vec<u8>, pub etag: String }

pub trait Store {
    fn put_if_absent(&mut self, key: &str, body: Vec<u8>) -> Result<String, StoreError>;
    fn put_if_match(&mut self, key: &str, etag: &str, body: Vec<u8>) -> Result<String, StoreError>;
    fn get(&mut self, key: &str) -> Result<Stored, StoreError>;
    /// Probe-only: redacted description of the last failure (fake-server runs only are printed).
    fn last_detail(&self) -> String { String::new() }
    /// Probe-only: Retry-After seconds seen on the last reply, if the client exposes it.
    fn last_retry_after(&self) -> Option<u64> { None }
}

#[derive(Clone)]
pub struct Cfg { pub bucket: String, pub endpoint: String, pub key: String, pub secret: String }
impl Cfg {
    pub fn from_env() -> Cfg {
        let g = |k: &str| std::env::var(k).unwrap_or_else(|_| { eprintln!("missing env {k}"); std::process::exit(2) });
        Cfg { bucket: g("R2_BUCKET"), endpoint: g("R2_ENDPOINT"), key: g("R2_ACCESS_KEY_ID"), secret: g("R2_SECRET_ACCESS_KEY") }
    }
    pub fn redact(&self, s: &str) -> String {
        let host = self.endpoint.split("://").last().unwrap_or("").trim_end_matches('/');
        let mut o = s.to_string();
        for v in [&self.endpoint, host, &self.key, &self.secret, &self.bucket] {
            if !v.is_empty() { o = o.replace(&v[..], "<redacted>"); }
        }
        o
    }
}
