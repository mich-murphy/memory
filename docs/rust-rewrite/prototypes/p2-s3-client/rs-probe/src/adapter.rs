//! `Store` over rusty-s3 0.11.0 (sans-IO signing) + ureq 3.5.1 (blocking HTTP). No retries exist
//! in either crate; ureq 3 only follows redirects and the adapter never loops.
#![allow(dead_code)]
use crate::store::*;
use base64::Engine;
use md5::{Digest, Md5};
use rusty_s3::{Bucket, Credentials, S3Action, UrlStyle};
use std::time::Duration;

#[derive(Clone)]
pub struct RsStore {
    agent: ureq::Agent,
    bucket: Bucket,
    creds: Credentials,
    pub md5: bool,
    detail: String,
    ra: Option<u64>,
    redact: Option<Cfg>,
    pub requests: usize,
}

pub struct Raw { pub status: u16, pub etag: Option<String>, pub body: Vec<u8> }

impl RsStore {
    pub fn new(endpoint: &str, bucket: &str, key: &str, secret: &str, proxy: Option<&str>, timeout_s: u64, redact: Option<Cfg>) -> RsStore {
        let mut cb = ureq::Agent::config_builder()
            .http_status_as_error(false)
            .timeout_global(Some(Duration::from_secs(timeout_s)))
            .max_redirects(0);
        cb = cb.proxy(proxy.map(|p| ureq::Proxy::new(p).unwrap()));
        RsStore {
            agent: cb.build().into(),
            bucket: Bucket::new(endpoint.parse().unwrap(), UrlStyle::Path, bucket.to_string(), "auto".to_string()).unwrap(),
            creds: Credentials::new(key, secret),
            md5: false, detail: String::new(), ra: None, redact, requests: 0,
        }
    }
    fn note(&mut self, s: String) { self.detail = match &self.redact { Some(c) => c.redact(&s), None => s }; }

    /// One HTTP exchange, never repeated by this adapter.
    pub fn raw(&mut self, put: bool, key: &str, hdrs: &[(&str, String)], body: &[u8]) -> Result<Raw, ureq::Error> {
        self.requests += 1;
        let url = if put {
            let mut a = self.bucket.put_object(Some(&self.creds), key);
            for (k, v) in hdrs { a.headers_mut().insert(k.to_string(), v.clone()); }
            a.sign(Duration::from_secs(300))
        } else {
            self.bucket.get_object(Some(&self.creds), key).sign(Duration::from_secs(300))
        };
        let resp = if put {
            let mut r = self.agent.put(url.as_str());
            for (k, v) in hdrs { r = r.header(*k, v.as_str()); }
            r.send(body)?
        } else { self.agent.get(url.as_str()).call()? };
        let (parts, mut b) = resp.into_parts();
        let ra = parts.headers.get("retry-after").and_then(|v| v.to_str().ok()).and_then(|v| v.parse().ok());
        self.ra = ra;
        let etag = parts.headers.get("etag").and_then(|v| v.to_str().ok()).map(String::from);
        let bytes = b.with_config().limit(64 << 20).read_to_vec()?;
        Ok(Raw { status: parts.status.as_u16(), etag, body: bytes })
    }

    fn classify(&mut self, e: ureq::Error, write: bool) -> StoreError {
        use ureq::Timeout as T;
        self.note(format!("{e:?}").chars().take(160).collect());
        match e {
            ureq::Error::HostNotFound | ureq::Error::ConnectionFailed | ureq::Error::Tls(_) => StoreError::Offline,
            ureq::Error::Timeout(T::Resolve | T::Connect) => StoreError::Offline,
            ureq::Error::Io(ref io) if io.kind() == std::io::ErrorKind::ConnectionRefused => StoreError::Offline,
            _ if write => StoreError::Ambiguous,
            _ => StoreError::Offline,
        }
    }
    fn status(&mut self, st: u16) -> StoreError {
        self.note(format!("status {st}"));
        match st {
            412 => StoreError::PreconditionFailed, 404 => StoreError::NotFound,
            401 | 403 => StoreError::Auth, 429 | 503 => StoreError::Throttled,
            _ => StoreError::Server,
        }
    }
    fn put(&mut self, key: &str, cond: (&str, String), body: Vec<u8>) -> Result<String, StoreError> {
        let mut h = vec![(cond.0, cond.1)];
        if self.md5 { h.push(("content-md5", base64::engine::general_purpose::STANDARD.encode(Md5::digest(&body)))); }
        match self.raw(true, key, &h, &body) {
            Err(e) => Err(self.classify(e, true)),
            Ok(r) if r.status == 200 => r.etag.ok_or(StoreError::Ambiguous),
            Ok(r) => Err(self.status(r.status)),
        }
    }
}

impl Store for RsStore {
    fn put_if_absent(&mut self, key: &str, body: Vec<u8>) -> Result<String, StoreError> { self.put(key, ("if-none-match", "*".into()), body) }
    fn put_if_match(&mut self, key: &str, etag: &str, body: Vec<u8>) -> Result<String, StoreError> { self.put(key, ("if-match", etag.into()), body) }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        match self.raw(false, key, &[], &[]) {
            Err(e) => Err(self.classify(e, false)),
            Ok(r) if r.status == 200 => Ok(Stored { body: r.body, etag: r.etag.ok_or(StoreError::Ambiguous)? }),
            Ok(r) => Err(self.status(r.status)),
        }
    }
    fn last_detail(&self) -> String { self.detail.clone() }
    fn last_retry_after(&self) -> Option<u64> { self.ra }
}

/// P2b: raw PUTs with deliberately wrong integrity headers. Returns (label, status).
pub fn p2b(s: &mut RsStore, prefix: &str) -> Vec<(String, String)> {
    let body = b"p2b synthetic body".to_vec();
    let good_md5 = base64::engine::general_purpose::STANDARD.encode(Md5::digest(&body));
    let good_sha = base64::engine::general_purpose::STANDARD.encode(sha2::Sha256::digest(&body));
    let bad_md5 = base64::engine::general_purpose::STANDARD.encode(Md5::digest(b"other"));
    let bad_sha = base64::engine::general_purpose::STANDARD.encode(sha2::Sha256::digest(b"other"));
    let cases: Vec<(&str, Vec<(&str, String)>)> = vec![
        ("control_no_checksum", vec![]),
        ("md5_correct", vec![("content-md5", good_md5.clone())]),
        ("md5_wrong", vec![("content-md5", bad_md5)]),
        ("md5_malformed", vec![("content-md5", "not-base64!!".into())]),
        ("sha256_correct", vec![("x-amz-checksum-sha256", good_sha.clone())]),
        ("sha256_wrong", vec![("x-amz-checksum-sha256", bad_sha)]),
        ("both_wrong_md5_right_sha", vec![("content-md5", "AAAAAAAAAAAAAAAAAAAAAA==".into()), ("x-amz-checksum-sha256", good_sha)]),
    ];
    let mut out = vec![];
    for (i, (name, h)) in cases.into_iter().enumerate() {
        let key = format!("{prefix}/p2b-{i}.bin");
        let r = s.raw(true, &key, &h, &body);
        let res = match r { Ok(r) => format!("{} {}", r.status, String::from_utf8_lossy(&r.body).chars().take(120).collect::<String>()), Err(e) => format!("err {e:?}") };
        let g = s.get(&key).map(|x| x.body == body);
        out.push((name.to_string(), format!("{res} | stored={:?}", g.map_err(|e| format!("{e:?}")))));
    }
    out
}
