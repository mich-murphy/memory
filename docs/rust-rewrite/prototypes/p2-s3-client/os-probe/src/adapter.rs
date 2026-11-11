//! Sync `Store` over object_store 0.14.2 on a shared 2-worker tokio runtime.
//! `retries=false` builds the client with max_retries 0 (the only switch: it is per client,
//! not per call; see report). A real adapter holds two clients.
#![allow(dead_code)]
use crate::store::*;
use object_store::{aws::{AmazonS3, AmazonS3Builder, S3ConditionalPut}, path::Path, BackoffConfig, ClientOptions,
    ObjectStore, ObjectStoreExt, PutMode, PutPayload, RetryConfig, UpdateVersion};
use std::sync::Arc;
use std::time::Duration;

#[derive(Clone)]
pub struct OsStore { pub rt: Arc<tokio::runtime::Runtime>, pub s3: Arc<AmazonS3>, detail: String, redact: Option<Cfg> }

pub fn runtime() -> Arc<tokio::runtime::Runtime> {
    Arc::new(tokio::runtime::Builder::new_multi_thread().worker_threads(2).enable_all().build().unwrap())
}

impl OsStore {
    pub fn new(rt: Arc<tokio::runtime::Runtime>, endpoint: &str, bucket: &str, key: &str, secret: &str, retries: bool,
               proxy: Option<&str>, timeout_s: u64, checksum_sha256: bool, redact: Option<Cfg>) -> OsStore {
        let mut co = ClientOptions::new().with_timeout(Duration::from_secs(timeout_s)).with_allow_http(true);
        if let Some(p) = proxy { co = co.with_proxy_url(p); }
        let mut b = AmazonS3Builder::new().with_bucket_name(bucket).with_endpoint(endpoint)
            .with_access_key_id(key).with_secret_access_key(secret).with_region("auto")
            .with_conditional_put(S3ConditionalPut::ETagMatch)
            .with_retry(RetryConfig { max_retries: if retries { 3 } else { 0 }, retry_timeout: Duration::from_secs(60),
                backoff: BackoffConfig { init_backoff: Duration::from_millis(50), max_backoff: Duration::from_millis(200), base: 2.0 } })
            .with_client_options(co);
        if checksum_sha256 { b = b.with_checksum_algorithm(object_store::aws::Checksum::SHA256); }
        OsStore { rt, s3: Arc::new(b.build().unwrap()), detail: String::new(), redact }
    }
    fn map(&mut self, e: object_store::Error) -> StoreError {
        use object_store::Error as E;
        let mut text = format!("{e}");
        let mut kind = None;
        if let E::Generic { source, .. } = &e {
            let mut cur: Option<&(dyn std::error::Error + 'static)> = Some(source.as_ref());
            while let Some(x) = cur {
                if let Some(h) = x.downcast_ref::<object_store::client::HttpError>() { kind = Some(h.kind()); }
                cur = x.source();
            }
        }
        text.truncate(300);
        self.detail = match &self.redact { Some(c) => c.redact(&text), None => text.clone() } + &format!(" [http_kind={kind:?}]");
        match e {
            E::Precondition { .. } | E::AlreadyExists { .. } | E::NotModified { .. } => StoreError::PreconditionFailed,
            E::NotFound { .. } => StoreError::NotFound,
            E::Unauthenticated { .. } | E::PermissionDenied { .. } => StoreError::Auth,
            _ => {
                use object_store::client::HttpErrorKind as K;
                if text.contains("status code: 429") || text.contains("status code: 503") { StoreError::Throttled }
                else if text.contains("status code: 5") { StoreError::Server }
                else if text.contains("status code: 401") || text.contains("status code: 403") { StoreError::Auth }
                else { match kind { Some(K::Connect) => StoreError::Offline, _ => StoreError::Ambiguous } }
            }
        }
    }
}

impl Store for OsStore {
    fn put_if_absent(&mut self, key: &str, body: Vec<u8>) -> Result<String, StoreError> {
        let (s3, p) = (self.s3.clone(), Path::from(key));
        match self.rt.block_on(async move { s3.put_opts(&p, PutPayload::from(body), PutMode::Create.into()).await }) {
            Ok(r) => r.e_tag.ok_or(StoreError::Ambiguous), Err(e) => Err(self.map(e)) }
    }
    fn put_if_match(&mut self, key: &str, etag: &str, body: Vec<u8>) -> Result<String, StoreError> {
        let (s3, p) = (self.s3.clone(), Path::from(key));
        let v = UpdateVersion { e_tag: Some(etag.to_string()), version: None };
        match self.rt.block_on(async move { s3.put_opts(&p, PutPayload::from(body), PutMode::Update(v).into()).await }) {
            Ok(r) => r.e_tag.ok_or(StoreError::Ambiguous), Err(e) => Err(self.map(e)) }
    }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        let (s3, p) = (self.s3.clone(), Path::from(key));
        match self.rt.block_on(async move { let g = s3.get(&p).await?; let et = g.meta.e_tag.clone(); let b = g.bytes().await?; Ok::<_, object_store::Error>((b, et)) }) {
            Ok((b, et)) => Ok(Stored { body: b.to_vec(), etag: et.ok_or(StoreError::Ambiguous)? }), Err(e) => Err(self.map(e)) }
    }
    fn last_detail(&self) -> String { self.detail.clone() }
}
