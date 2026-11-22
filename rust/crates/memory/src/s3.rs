//! S3 adapter for the [`Store`] seam: `rusty-s3` signs (sans-IO), `ureq` sends (blocking).
//!
//! Every `Store` call is exactly one HTTP exchange. Neither crate retries, redirects are
//! disabled and this module has no loop, so a `current.json` write is never resent behind the
//! caller (prototype P2, reason 1). Errors leaving a `Store` call are [`StoreError`] values only;
//! no text is built from a URL (a presigned URL carries the signature) or from a `ureq` error.
use crate::environment::{Cause, Published, Store, StoreError, Stored};
use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use md5::{Digest, Md5};
use memory_core::domain::ETag;
use rusty_s3::{Bucket, Credentials, S3Action, UrlStyle};
use std::time::Duration;

/// Cap on a downloaded body.
const MAX_GET_BODY: u64 = 64 << 20;
/// The most of a PUT reply body, or of a GET error reply body, that is read to free its
/// connection. An S3 error reply is a short XML document; ureq pools a connection only once its
/// body is read to the end, so a reply larger than this is not read and its connection is
/// dropped.
const MAX_PUT_REPLY_BODY: u64 = 64 << 10;
/// Cap on a ListObjectsV2 reply body; a page of 1,000 keys is about 0.2 MB.
pub const MAX_LIST_BODY: u64 = 4 << 20;
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
            .ok_or(StoreError::Ambiguous(Cause::NoEtag)),
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
///
/// Refused, DNS, connect and TLS failures and the resolve and connect timeouts never reached
/// the server, so they are `Offline` even for a write; every other failure is `Ambiguous` for
/// a write and `Offline` for a read. The cause says which failure it was.
pub fn map_transport(write: bool, error: &ureq::Error) -> StoreError {
    use std::io::ErrorKind as K;
    use ureq::{Error as E, Timeout as T};
    // The flag is true when the request never reached the server.
    let (cause, never_sent) = match error {
        E::HostNotFound => (Cause::Dns, true),
        E::ConnectionFailed => (Cause::Connect, true),
        E::Tls(_) | E::Rustls(_) => (Cause::Tls, true),
        E::Timeout(T::Resolve) => (Cause::DnsTimeout, true),
        E::Timeout(T::Connect) => (Cause::ConnectTimeout, true),
        E::Io(io) if io.kind() == K::ConnectionRefused => (Cause::Refused, true),
        E::BodyExceedsLimit(_) => return StoreError::Server,
        E::Timeout(T::SendRequest | T::SendBody | T::Await100 | T::PerWrite) => {
            (Cause::SendTimeout, false)
        }
        E::Timeout(T::RecvResponse | T::RecvBody | T::PerRead) => (Cause::RecvTimeout, false),
        E::Timeout(T::Global | T::PerCall) => (Cause::Timeout, false),
        E::Io(io) => match io.kind() {
            K::ConnectionReset | K::ConnectionAborted | K::BrokenPipe | K::UnexpectedEof => {
                (Cause::Reset, false)
            }
            _ => (Cause::Io, false),
        },
        E::Protocol(_) => (Cause::Protocol, false),
        _ => (Cause::Other, false),
    };
    if write && !never_sent {
        StoreError::Ambiguous(cause)
    } else {
        StoreError::Offline(cause)
    }
}

/// One ListObjectsV2 page: keys with the store prefix stripped, and the next token.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Page {
    /// Keys under `prefix + under`, with `prefix` removed, in the order the server gave them.
    pub keys: Vec<String>,
    /// Keys the server returned that did not start with `prefix + under`; they are not in
    /// `keys`. A caller treats any as a failure.
    pub outside: usize,
    /// Token for the next page; `None` when the listing is complete.
    pub next: Option<String>,
}

/// A 200 list reply that cannot be used: a protocol fault, retryable like any garbled read.
const PROTOCOL: StoreError = StoreError::Offline(Cause::Protocol);

/// Map one ListObjectsV2 reply to a [`Page`]. Pure. `full` is `prefix + under`, `prefix` the
/// store prefix to strip.
///
/// # Errors
/// The [`StoreError`] of a non-200 status; `Offline(Cause::Protocol)` (brief 52) for a 200 body
/// over [`MAX_LIST_BODY`], not UTF-8 or not a ListObjectsV2 document.
pub fn map_list_reply(
    prefix: &str,
    full: &str,
    status: u16,
    body: &[u8],
    retry_after: Option<&str>,
) -> Result<Page, StoreError> {
    if status != 200 {
        return match map_reply(Operation::Get, status, None, retry_after) {
            Err(error) => Err(error),
            Ok(_) => Err(StoreError::Server),
        };
    }
    if u64::try_from(body.len()).map_or(true, |n| n > MAX_LIST_BODY) {
        return Err(PROTOCOL);
    }
    let text = std::str::from_utf8(body).map_err(|_| PROTOCOL)?;
    let parsed = rusty_s3::actions::ListObjectsV2::parse_response(text).map_err(|_| PROTOCOL)?;
    let mut page = Page {
        next: parsed.next_continuation_token,
        ..Page::default()
    };
    for content in parsed.contents {
        match content
            .key
            .strip_prefix(full)
            .and_then(|_| content.key.strip_prefix(prefix))
        {
            Some(stripped) => page.keys.push(stripped.to_owned()),
            None => page.outside += 1,
        }
    }
    Ok(page)
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

    /// One ListObjectsV2 exchange for `prefix + under`, continuing from `token`. No loop: the
    /// caller follows [`Page::next`].
    ///
    /// # Errors
    /// A [`StoreError`] as for `get`; `Server` for an oversized or unparsable body.
    pub fn list_page(&mut self, under: &str, token: Option<&str>) -> Result<Page, StoreError> {
        let full = format!("{}{under}", self.prefix);
        let mut action = self.bucket.list_objects_v2(Some(&self.creds));
        action.with_prefix(full.clone());
        if let Some(token) = token {
            action.with_continuation_token(token.to_owned());
        }
        let url = action.sign(SIGN_VALIDITY);
        let response = self
            .agent
            .get(url.as_str())
            .config()
            .timeout_global(Some(self.timeouts.for_body(0)))
            .build()
            .call()
            .map_err(|error| map_transport(false, &error))?;
        let (parts, mut body) = response.into_parts();
        let status = parts.status.as_u16();
        let limit = if status == 200 {
            MAX_LIST_BODY
        } else {
            MAX_PUT_REPLY_BODY
        };
        let bytes = body.with_config().limit(limit).read_to_vec();
        if status != 200 {
            let _ = bytes;
            return map_list_reply(
                &self.prefix,
                &full,
                status,
                &[],
                header(&parts, "retry-after"),
            );
        }
        let bytes = bytes.map_err(|error| map_transport(false, &error))?;
        map_list_reply(&self.prefix, &full, status, &bytes, None)
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
        let (parts, mut reply_body) = response.into_parts();
        // A body left unread keeps its connection out of ureq's pool (run.rs `ended`).
        let _drained = reply_body
            .with_config()
            .limit(MAX_PUT_REPLY_BODY)
            .read_to_vec();
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
        let etag = match map_reply(
            Operation::Get,
            parts.status.as_u16(),
            header(&parts, "etag"),
            header(&parts, "retry-after"),
        ) {
            Ok(etag) => etag,
            Err(error) => {
                // An error reply (a 404 for an absent `current.json` above all) is read like a
                // PUT reply, so its connection returns to the pool.
                let _drained = body.with_config().limit(MAX_PUT_REPLY_BODY).read_to_vec();
                return Err(error);
            }
        };
        // Exact allocation from `Content-Length` (capped at the body limit): `read_to_vec` grows
        // its buffer by doubling, and the freed blocks kept a streamed `memory check` of one
        // 100 MB source at 249 MB (brief 34). An over-limit body is still `BodyExceedsLimit`,
        // which `ureq::Error::from` recovers from the reader's `io::Error`.
        let reserve = body.content_length().unwrap_or(0).min(MAX_GET_BODY);
        let mut bytes = Vec::with_capacity(usize::try_from(reserve).unwrap_or_default());
        std::io::Read::read_to_end(
            &mut body.with_config().limit(MAX_GET_BODY).reader(),
            &mut bytes,
        )
        .map_err(|error| map_transport(false, &ureq::Error::from(error)))?;
        Ok(Stored { body: bytes, etag })
    }
}

#[cfg(test)]
mod list_tests {
    use super::*;

    const P: &str = "run/";
    const FULL: &str = "run/sessions/";

    fn doc(keys: &[&str], token: Option<&str>) -> String {
        let mut x = String::from(
            "<?xml version=\"1.0\" encoding=\"UTF-8\"?><ListBucketResult xmlns=\"http://s3.amazonaws.com/doc/2006-03-01/\"><EncodingType>url</EncodingType>",
        );
        for k in keys {
            x.push_str(&format!("<Contents><Key>{k}</Key></Contents>"));
        }
        match token {
            Some(t) => x.push_str(&format!(
                "<IsTruncated>true</IsTruncated><NextContinuationToken>{t}</NextContinuationToken>"
            )),
            None => x.push_str("<IsTruncated>false</IsTruncated>"),
        }
        x.push_str("</ListBucketResult>");
        x
    }

    #[test]
    fn ok_parses_strips_prefix_and_decodes() {
        let body = doc(&["run/sessions/a%3Ab/current.json"], Some("t1"));
        let page = map_list_reply(P, FULL, 200, body.as_bytes(), None);
        assert_eq!(
            page,
            Ok(Page {
                keys: vec!["sessions/a:b/current.json".to_owned()],
                outside: 0,
                next: Some("t1".to_owned())
            })
        );
    }

    #[test]
    fn key_outside_prefix_is_counted_not_returned() {
        let body = doc(&["other/sessions/x/current.json", "run/shared/x"], None);
        let page = map_list_reply(P, FULL, 200, body.as_bytes(), None);
        assert_eq!(
            page,
            Ok(Page {
                keys: vec![],
                outside: 2,
                next: None
            })
        );
    }

    #[test]
    fn oversized_body_is_protocol() {
        let body = vec![b' '; usize::try_from(MAX_LIST_BODY).unwrap_or(0) + 1];
        assert_eq!(
            map_list_reply(P, FULL, 200, &body, None),
            Err(StoreError::Offline(Cause::Protocol))
        );
    }

    #[test]
    fn unparsable_body_is_protocol() {
        assert_eq!(
            map_list_reply(P, FULL, 200, b"not xml", None),
            Err(StoreError::Offline(Cause::Protocol))
        );
        assert_eq!(
            map_list_reply(P, FULL, 200, &[0xff, 0xfe], None),
            Err(StoreError::Offline(Cause::Protocol))
        );
    }

    #[test]
    fn statuses_map_as_get() {
        assert_eq!(
            map_list_reply(P, FULL, 403, b"", None),
            Err(StoreError::Auth)
        );
        assert_eq!(
            map_list_reply(P, FULL, 500, b"", None),
            Err(StoreError::Server)
        );
        assert_eq!(
            map_list_reply(P, FULL, 503, b"", Some("7")),
            Err(StoreError::Throttled(Some(7)))
        );
    }
}
