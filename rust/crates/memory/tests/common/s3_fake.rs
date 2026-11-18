//! Std-only fake S3 server on `127.0.0.1:0`: in-memory objects, the R2 conditional rules, a
//! content-derived ETag (the quoted hex MD5 of the body, amendment A13.4), `content-md5`
//! enforcement (400 on mismatch) and scripted faults. Every request is recorded. The request
//! path is percent-decoded before it names an object, as an S3 server does: the client encodes
//! `:` (session ids contain it) as `%3A`.
use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use md5::{Digest, Md5};
use std::collections::{HashMap, VecDeque};
use std::io::{BufRead, BufReader, Read, Write};
use std::net::{Shutdown, TcpListener, TcpStream};
use std::sync::{Arc, Mutex, MutexGuard, PoisonError};
use std::thread;
use std::time::Duration;

/// A scripted fault, consumed by the next request matching its method and path suffix.
#[derive(Clone, Copy, Debug)]
pub enum Fault {
    /// Apply the request, then reply with this status.
    ApplyThenStatus(u16),
    /// Apply the request, then close the socket without a reply.
    ApplyThenDrop,
    /// Apply the request, then stay silent for this long and close.
    ApplyThenHang(Duration),
    /// Reply with this status (and optional `Retry-After`) without applying anything.
    Status(u16, Option<&'static str>),
    /// Flip a body byte before the `content-md5` check, as a damaged upload would.
    CorruptInFlight,
    /// Hold the request for this long (counted by [`Fake::held`] while it waits), then apply it
    /// and reply normally.
    HoldThenApply(Duration),
    /// Reply with this status and a body of this many bytes, without applying anything.
    StatusWithBody(u16, usize),
    /// Answer a list request with status 200 and this body, without looking at the objects.
    ListBody(&'static str),
}

/// What the server saw for one request.
#[derive(Clone, Debug)]
pub struct Seen {
    pub method: String,
    pub path: String,
    pub if_match: Option<String>,
    pub if_none_match: Option<String>,
    pub content_md5: Option<String>,
    pub body: Vec<u8>,
    pub status: u16,
}

#[derive(Default)]
struct State {
    objects: HashMap<String, (Vec<u8>, String)>,
    faults: VecDeque<(String, String, Fault)>,
    seen: Vec<Seen>,
    held: usize,
    accepted: usize,
}

impl State {
    fn take_fault(&mut self, method: &str, path: &str) -> Option<Fault> {
        let index = self
            .faults
            .iter()
            .position(|(m, suffix, _)| m == method && path.ends_with(suffix.as_str()))?;
        self.faults.remove(index).map(|(_, _, fault)| fault)
    }
}

fn lock(state: &Mutex<State>) -> MutexGuard<'_, State> {
    state.lock().unwrap_or_else(PoisonError::into_inner)
}

/// Lower-case hex.
pub fn hex(bytes: &[u8]) -> String {
    bytes
        .iter()
        .flat_map(|b| [b >> 4, b & 15])
        .filter_map(|n| char::from_digit(u32::from(n), 16))
        .collect()
}

/// The ETag R2 gives a single-part PUT: the quoted hex MD5 of the body.
pub fn etag_of(body: &[u8]) -> String {
    format!("\"{}\"", hex(&Md5::digest(body)))
}

/// Base64 MD5, the `content-md5` header value.
pub fn content_md5_of(body: &[u8]) -> String {
    STANDARD.encode(Md5::digest(body))
}

/// The running server.
pub struct Fake {
    addr: std::net::SocketAddr,
    state: Arc<Mutex<State>>,
}

impl Fake {
    pub fn start() -> Result<Self, String> {
        Self::start_with(false)
    }

    /// As [`Fake::start`], but a connection stays open for further requests (`Connection:
    /// keep-alive`); [`Fake::accepted`] counts the connections.
    pub fn start_keep_alive() -> Result<Self, String> {
        Self::start_with(true)
    }

    fn start_with(keep_alive: bool) -> Result<Self, String> {
        let listener = TcpListener::bind("127.0.0.1:0").map_err(|e| format!("{e}"))?;
        let addr = listener.local_addr().map_err(|e| format!("{e}"))?;
        let state = Arc::new(Mutex::new(State::default()));
        let shared = Arc::clone(&state);
        thread::spawn(move || {
            for stream in listener.incoming().flatten() {
                let state = Arc::clone(&shared);
                lock(&state).accepted += 1;
                thread::spawn(move || handle(stream, &state, keep_alive));
            }
        });
        Ok(Self { addr, state })
    }

    pub fn url(&self) -> String {
        format!("http://{}", self.addr)
    }

    pub fn queue(&self, method: &str, suffix: &str, fault: Fault) {
        lock(&self.state)
            .faults
            .push_back((method.to_owned(), suffix.to_owned(), fault));
    }

    /// Store an object directly, as another writer would have.
    pub fn seed(&self, path: &str, body: &[u8]) {
        let etag = etag_of(body);
        lock(&self.state)
            .objects
            .insert(path.to_owned(), (body.to_vec(), etag));
    }

    pub fn object(&self, path: &str) -> Option<(Vec<u8>, String)> {
        lock(&self.state).objects.get(path).cloned()
    }

    pub fn requests(&self) -> Vec<Seen> {
        lock(&self.state).seen.clone()
    }

    /// How many requests have started a hold.
    pub fn held(&self) -> usize {
        lock(&self.state).held
    }

    /// How many connections the server has accepted.
    pub fn accepted(&self) -> usize {
        lock(&self.state).accepted
    }

    pub fn count(&self, method: &str) -> usize {
        self.requests()
            .iter()
            .filter(|r| r.method == method)
            .count()
    }
}

fn reply(w: &mut TcpStream, code: u16, extra: &str, body: &[u8], keep_alive: bool) {
    let connection = if keep_alive { "keep-alive" } else { "close" };
    let head = format!(
        "HTTP/1.1 {code} X\r\nContent-Length: {}\r\nConnection: {connection}\r\n{extra}\r\n",
        body.len()
    );
    let _ = w.write_all(head.as_bytes());
    let _ = w.write_all(body);
    let _ = w.flush();
    if !keep_alive {
        let _ = w.shutdown(Shutdown::Both);
    }
}

fn percent_decode(raw: &str) -> Option<String> {
    let mut out = Vec::with_capacity(raw.len());
    let mut bytes = raw.bytes();
    while let Some(byte) = bytes.next() {
        if byte == b'%' {
            let high = char::from(bytes.next()?).to_digit(16)?;
            let low = char::from(bytes.next()?).to_digit(16)?;
            out.push(u8::try_from(high * 16 + low).ok()?);
        } else {
            out.push(byte);
        }
    }
    String::from_utf8(out).ok()
}

/// Percent-encode as S3 does for `encoding-type=url`: everything but unreserved and `/`.
fn url_encode(raw: &str) -> String {
    let mut out = String::new();
    for byte in raw.bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.' | b'~' | b'/') {
            out.push(char::from(byte));
        } else {
            out.push_str(&format!("%{byte:02X}"));
        }
    }
    out
}

/// A ListObjectsV2 reply for the bucket at `bucket_path`: `prefix`, `continuation-token` (here
/// `t<index>` into the sorted matching keys) and `max-keys` (default 1,000) are honoured.
fn list_reply(
    objects: &HashMap<String, (Vec<u8>, String)>,
    bucket_path: &str,
    query: &str,
) -> Vec<u8> {
    let mut prefix = String::new();
    let mut start = 0_usize;
    let mut max_keys = 1000_usize;
    for pair in query.split('&') {
        let (name, value) = pair.split_once('=').unwrap_or((pair, ""));
        let value = percent_decode(value).unwrap_or_default();
        match name {
            "prefix" => prefix = value,
            "continuation-token" => {
                start = value
                    .strip_prefix('t')
                    .and_then(|n| n.parse().ok())
                    .unwrap_or(0);
            }
            "max-keys" => max_keys = value.parse().unwrap_or(1000),
            _ => {}
        }
    }
    let base = format!("{}/", bucket_path.trim_end_matches('/'));
    let mut keys: Vec<&str> = objects
        .keys()
        .filter_map(|p| p.strip_prefix(base.as_str()))
        .filter(|k| k.starts_with(prefix.as_str()))
        .collect();
    keys.sort_unstable();
    let page: Vec<&str> = keys.iter().copied().skip(start).take(max_keys).collect();
    let end = start + page.len();
    let truncated = end < keys.len();
    let mut xml = String::from(
        "<?xml version=\"1.0\" encoding=\"UTF-8\"?><ListBucketResult xmlns=\"http://s3.amazonaws.com/doc/2006-03-01/\"><EncodingType>url</EncodingType>",
    );
    xml.push_str(&format!("<MaxKeys>{}</MaxKeys>", max_keys.min(1000)));
    for key in &page {
        xml.push_str(&format!(
            "<Contents><Key>{}</Key></Contents>",
            url_encode(key)
        ));
    }
    if truncated {
        xml.push_str(&format!(
            "<IsTruncated>true</IsTruncated><NextContinuationToken>t{end}</NextContinuationToken>"
        ));
    } else {
        xml.push_str("<IsTruncated>false</IsTruncated>");
    }
    xml.push_str("</ListBucketResult>");
    xml.into_bytes()
}

fn handle(stream: TcpStream, state: &Mutex<State>, keep_alive: bool) -> Option<()> {
    stream
        .set_read_timeout(Some(Duration::from_secs(10)))
        .ok()?;
    let mut reader = BufReader::new(stream.try_clone().ok()?);
    let mut w = stream;
    while exchange(&mut reader, &mut w, state, keep_alive).is_some() && keep_alive {}
    Some(())
}

fn exchange(
    reader: &mut BufReader<TcpStream>,
    w: &mut TcpStream,
    state: &Mutex<State>,
    keep_alive: bool,
) -> Option<()> {
    let mut line = String::new();
    reader.read_line(&mut line).ok().filter(|n| *n > 0)?;
    let mut parts = line.split_whitespace();
    let method = parts.next()?.to_owned();
    let target = parts.next()?;
    let (raw_path, query) = target.split_once('?').unwrap_or((target, ""));
    let path = percent_decode(raw_path)?;
    let list_query = query
        .split('&')
        .any(|pair| pair == "list-type=2")
        .then(|| query.to_owned());
    let mut headers: HashMap<String, String> = HashMap::new();
    loop {
        let mut l = String::new();
        reader.read_line(&mut l).ok().filter(|n| *n > 0)?;
        let l = l.trim_end();
        if l.is_empty() {
            break;
        }
        if let Some((k, v)) = l.split_once(':') {
            headers.insert(k.trim().to_ascii_lowercase(), v.trim().to_owned());
        }
    }
    if headers
        .get("expect")
        .is_some_and(|v| v.eq_ignore_ascii_case("100-continue"))
    {
        let _ = w.write_all(b"HTTP/1.1 100 Continue\r\n\r\n");
    }
    let n: usize = headers
        .get("content-length")
        .and_then(|v| v.parse().ok())
        .unwrap_or(0);
    let mut body = vec![0_u8; n];
    reader.read_exact(&mut body).ok()?;

    let mut g = lock(state);
    let fault = g.take_fault(&method, &path);
    if let Some(Fault::HoldThenApply(hold)) = fault {
        g.held += 1;
        drop(g);
        thread::sleep(hold);
        g = lock(state);
    }
    let seen = |status: u16, body: Vec<u8>| Seen {
        method: method.clone(),
        path: path.clone(),
        if_match: headers.get("if-match").cloned(),
        if_none_match: headers.get("if-none-match").cloned(),
        content_md5: headers.get("content-md5").cloned(),
        body,
        status,
    };
    if let Some(Fault::Status(code, retry_after)) = fault {
        g.seen.push(seen(code, body));
        drop(g);
        let extra = retry_after
            .map(|v| format!("Retry-After: {v}\r\n"))
            .unwrap_or_default();
        reply(
            w,
            code,
            &extra,
            b"<Error><Code>Fault</Code></Error>",
            keep_alive,
        );
        return Some(());
    }
    if let Some(Fault::StatusWithBody(code, size)) = fault {
        g.seen.push(seen(code, body));
        drop(g);
        reply(w, code, "", &vec![b'x'; size], keep_alive);
        return Some(());
    }
    if let (Some(Fault::ListBody(text)), Some(_)) = (fault, &list_query) {
        g.seen.push(seen(200, body));
        drop(g);
        reply(w, 200, "", text.as_bytes(), keep_alive);
        return Some(());
    }
    if matches!(fault, Some(Fault::CorruptInFlight))
        && let Some(b) = body.first_mut()
    {
        *b ^= 0xff;
    }
    let (code, etag, payload): (u16, Option<String>, Vec<u8>) = match method.as_str() {
        "GET" if list_query.is_some() => (
            200,
            None,
            list_reply(&g.objects, &path, list_query.as_deref().unwrap_or("")),
        ),
        "GET" => match g.objects.get(&path) {
            Some((b, e)) => (200, Some(e.clone()), b.clone()),
            None => (404, None, b"<Error><Code>NoSuchKey</Code></Error>".to_vec()),
        },
        "PUT" => {
            let current = g.objects.get(&path).map(|(_, e)| e.clone());
            let if_none_match = headers.get("if-none-match").map(String::as_str);
            let if_match = headers.get("if-match").cloned();
            if headers
                .get("content-md5")
                .is_some_and(|v| *v != content_md5_of(&body))
            {
                (400, None, b"<Error><Code>BadDigest</Code></Error>".to_vec())
            } else if (if_none_match == Some("*") && current.is_some())
                || (if_match.is_some() && current != if_match)
            {
                (
                    412,
                    None,
                    b"<Error><Code>PreconditionFailed</Code></Error>".to_vec(),
                )
            } else {
                let etag = etag_of(&body);
                g.objects.insert(path.clone(), (body.clone(), etag.clone()));
                (200, Some(etag), Vec::new())
            }
        }
        _ => (405, None, Vec::new()),
    };
    g.seen.push(seen(code, body));
    drop(g);
    match fault {
        Some(Fault::ApplyThenStatus(c)) => reply(w, c, "", b"<Error/>", keep_alive),
        Some(Fault::ApplyThenDrop) => {
            let _ = w.shutdown(Shutdown::Both);
        }
        Some(Fault::ApplyThenHang(d)) => {
            thread::sleep(d);
            let _ = w.shutdown(Shutdown::Both);
        }
        _ => {
            let extra = etag.map(|e| format!("ETag: {e}\r\n")).unwrap_or_default();
            reply(w, code, &extra, &payload, keep_alive);
        }
    }
    Some(())
}
