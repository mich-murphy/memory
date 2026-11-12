//! S3 adapter for the [`Store`] seam: `rusty-s3` signs (sans-IO), `ureq` sends (blocking).
//!
//! Every `Store` call is exactly one HTTP exchange. Neither crate retries, redirects are
//! disabled and this module has no loop, so a `current.json` write is never resent behind the
//! caller (prototype P2, reason 1). Errors leaving a `Store` call are [`StoreError`] values only;
//! no text is built from a URL (a presigned URL carries the signature) or from a `ureq` error.
use crate::environment::{Published, Store, StoreError, Stored};
use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use md5::{Digest, Md5};
use memory_core::domain::ETag;
use rusty_s3::{Bucket, Credentials, S3Action, UrlStyle};
use std::time::Duration;

/// Cap on a downloaded body.
const MAX_GET_BODY: u64 = 64 << 20;
/// Validity of one presigned request; it is used immediately.
const SIGN_VALIDITY: Duration = Duration::from_secs(300);

/// Why an [`S3Store`] could not be built. Carries no input text, so it never leaks a secret.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum S3ConfigError {
    /// The endpoint is not an `http`/`https` URL with a host.
    #[error("invalid endpoint")]
    Endpoint,
    /// The bucket name is empty.
    #[error("invalid bucket")]
    Bucket,
    /// The region is empty.
    #[error("invalid region")]
    Region,
    /// The access key id or the secret is empty.
    #[error("invalid credentials")]
    Credentials,
    /// The key prefix is not empty and does not end with `/`, or starts with `/`.
    #[error("invalid key prefix")]
    Prefix,
}

/// Request timeout: `base` plus the body size at `bytes_per_sec`, never below `floor`
/// (DESIGN.md 7.3: 30 s plus size at 1 MiB/s, floor 60 s).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct TimeoutPolicy {
    base: Duration,
    bytes_per_sec: u64,
    floor: Duration,
}

impl TimeoutPolicy {
    /// The production policy of DESIGN.md 7.3.
    pub const PRODUCTION: Self =
        Self::new(Duration::from_secs(30), 1 << 20, Duration::from_secs(60));

    /// A custom policy (tests use a short one).
    pub const fn new(base: Duration, bytes_per_sec: u64, floor: Duration) -> Self {
        Self {
            base,
            bytes_per_sec,
            floor,
        }
    }

    /// Timeout for a request carrying `len` body bytes.
    pub fn for_body(&self, len: usize) -> Duration {
        let len = u64::try_from(len).unwrap_or(u64::MAX);
        let transfer = Duration::from_secs(len.checked_div(self.bytes_per_sec).unwrap_or(0));
        self.base.saturating_add(transfer).max(self.floor)
    }
}

impl Default for TimeoutPolicy {
    fn default() -> Self {
        Self::PRODUCTION
    }
}

/// Which `Store` call a reply answers; the 404 meaning depends on it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Operation {
    /// `put_if_absent`.
    PutIfAbsent,
    /// `put_if_match`.
    PutIfMatch,
    /// `get`.
    Get,
}

/// Map one HTTP reply to the call's result (the P2 table). Pure.
///
/// `etag` and `retry_after` are the raw header values. A 200 yields the ETag exactly as
/// given, quotes included.
///
/// # Errors
/// The [`StoreError`] for every non-200 status, and `Ambiguous` for a 200 without a usable ETag.
pub fn map_reply(
    op: Operation,
    status: u16,
    etag: Option<&str>,
    retry_after: Option<&str>,
) -> Result<ETag, StoreError> {
    match status {
        200 => etag
            .and_then(|value| ETag::try_from(value.to_owned()).ok())
            .ok_or(StoreError::Ambiguous),
        412 => Err(StoreError::PreconditionFailed),
        404 if op == Operation::PutIfMatch => Err(StoreError::PreconditionFailed),
        404 => Err(StoreError::NotFound),
        401 | 403 => Err(StoreError::Auth),
        429 | 503 => Err(StoreError::Throttled(
            retry_after.and_then(|v| v.trim().parse::<u32>().ok()),
        )),
        _ => Err(StoreError::Server),
    }
}

/// Map a transport failure. Pure. `write` is true for a PUT, where a lost reply means the
/// request may have landed.
pub fn map_transport(write: bool, error: &ureq::Error) -> StoreError {
    use ureq::{Error as E, Timeout as T};
    match error {
        E::HostNotFound | E::ConnectionFailed | E::Tls(_) | E::Rustls(_) => StoreError::Offline,
        E::Timeout(T::Resolve | T::Connect) => StoreError::Offline,
        E::Io(io) if io.kind() == std::io::ErrorKind::ConnectionRefused => StoreError::Offline,
        E::BodyExceedsLimit(_) => StoreError::Server,
        _ if write => StoreError::Ambiguous,
        _ => StoreError::Offline,
    }
}

/// The S3 (Cloudflare R2) object store.
pub struct S3Store {
    agent: ureq::Agent,
    bucket: Bucket,
    bucket_name: String,
    creds: Credentials,
    prefix: String,
    timeouts: TimeoutPolicy,
}

impl std::fmt::Debug for S3Store {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("S3Store")
            .field("bucket", &self.bucket_name)
            .field("prefix", &self.prefix)
            .field("timeouts", &self.timeouts)
            .finish_non_exhaustive()
    }
}

impl S3Store {
    /// Build a path-style client. `prefix` is empty in production, otherwise it ends with `/`
    /// (for example `rust-acceptance/<run-id>/`); every key sent is `prefix + key`.
    ///
    /// # Errors
    /// An [`S3ConfigError`] naming the invalid part, never its text.
    pub fn new(
        endpoint: &str,
        bucket: &str,
        region: &str,
        access_key_id: &str,
        secret: &str,
        prefix: &str,
        timeouts: TimeoutPolicy,
    ) -> Result<Self, S3ConfigError> {
        if bucket.is_empty() {
            return Err(S3ConfigError::Bucket);
        }
        if region.is_empty() {
            return Err(S3ConfigError::Region);
        }
        if access_key_id.is_empty() || secret.is_empty() {
            return Err(S3ConfigError::Credentials);
        }
        if !(prefix.is_empty() || (prefix.ends_with('/') && !prefix.starts_with('/'))) {
            return Err(S3ConfigError::Prefix);
        }
        let url = endpoint
            .parse::<url::Url>()
            .map_err(|_| S3ConfigError::Endpoint)?;
        let handle = Bucket::new(url, UrlStyle::Path, bucket.to_owned(), region.to_owned())
            .map_err(|_| S3ConfigError::Endpoint)?;
        let agent: ureq::Agent = ureq::Agent::config_builder()
            .http_status_as_error(false)
            .max_redirects(0)
            .proxy(None)
            .build()
            .into();
        Ok(Self {
            agent,
            bucket: handle,
            bucket_name: bucket.to_owned(),
            creds: Credentials::new(access_key_id, secret),
            prefix: prefix.to_owned(),
            timeouts,
        })
    }

    fn put(
        &self,
        op: Operation,
        key: &str,
        condition: (&'static str, &str),
        body: &Published,
    ) -> Result<ETag, StoreError> {
        let full = format!("{}{key}", self.prefix);
        let bytes = body.as_ref();
        let digest = STANDARD.encode(Md5::digest(bytes));
        let mut action = self.bucket.put_object(Some(&self.creds), &full);
        action
            .headers_mut()
            .insert(condition.0, condition.1.to_owned());
        action.headers_mut().insert("content-md5", digest.clone());
        let url = action.sign(SIGN_VALIDITY);
        let response = self
            .agent
            .put(url.as_str())
            .config()
            .timeout_global(Some(self.timeouts.for_body(bytes.len())))
            .build()
            .header(condition.0, condition.1)
            .header("content-md5", digest.as_str())
            .send(bytes)
            .map_err(|error| map_transport(true, &error))?;
        let (parts, _body) = response.into_parts();
        map_reply(
            op,
            parts.status.as_u16(),
            header(&parts, "etag"),
            header(&parts, "retry-after"),
        )
    }
}

fn header<'a>(parts: &'a ureq::http::response::Parts, name: &str) -> Option<&'a str> {
    parts.headers.get(name).and_then(|v| v.to_str().ok())
}

impl Store for S3Store {
    fn put_if_absent(&mut self, key: &str, body: Published) -> Result<ETag, StoreError> {
        self.put(Operation::PutIfAbsent, key, ("if-none-match", "*"), &body)
    }

    fn put_if_match(
        &mut self,
        key: &str,
        etag: &ETag,
        body: Published,
    ) -> Result<ETag, StoreError> {
        self.put(
            Operation::PutIfMatch,
            key,
            ("if-match", etag.as_str()),
            &body,
        )
    }

    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        let full = format!("{}{key}", self.prefix);
        let url = self
            .bucket
            .get_object(Some(&self.creds), &full)
            .sign(SIGN_VALIDITY);
        let response = self
            .agent
            .get(url.as_str())
            .config()
            .timeout_global(Some(self.timeouts.for_body(0)))
            .build()
            .call()
            .map_err(|error| map_transport(false, &error))?;
        let (parts, mut body) = response.into_parts();
        let etag = map_reply(
            Operation::Get,
            parts.status.as_u16(),
            header(&parts, "etag"),
            header(&parts, "retry-after"),
        )?;
        let bytes = body
            .with_config()
            .limit(MAX_GET_BODY)
            .read_to_vec()
            .map_err(|error| map_transport(false, &error))?;
        Ok(Stored { body: bytes, etag })
    }
}
