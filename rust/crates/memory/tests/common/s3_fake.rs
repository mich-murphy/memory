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
        let listener = TcpListener::bind("127.0.0.1:0").map_err(|e| format!("{e}"))?;
        let addr = listener.local_addr().map_err(|e| format!("{e}"))?;
        let state = Arc::new(Mutex::new(State::default()));
        let shared = Arc::clone(&state);
        thread::spawn(move || {
            for stream in listener.incoming().flatten() {
                let state = Arc::clone(&shared);
                thread::spawn(move || handle(stream, &state));
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

    pub fn count(&self, method: &str) -> usize {
        self.requests()
            .iter()
            .filter(|r| r.method == method)
            .count()
    }
}

fn reply(w: &mut TcpStream, code: u16, extra: &str, body: &[u8]) {
    let head = format!(
        "HTTP/1.1 {code} X\r\nContent-Length: {}\r\nConnection: close\r\n{extra}\r\n",
        body.len()
    );
    let _ = w.write_all(head.as_bytes());
    let _ = w.write_all(body);
    let _ = w.flush();
    let _ = w.shutdown(Shutdown::Both);
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

fn handle(stream: TcpStream, state: &Mutex<State>) -> Option<()> {
    stream
        .set_read_timeout(Some(Duration::from_secs(10)))
        .ok()?;
    let mut reader = BufReader::new(stream.try_clone().ok()?);
    let mut w = stream;
    let mut line = String::new();
    reader.read_line(&mut line).ok().filter(|n| *n > 0)?;
    let mut parts = line.split_whitespace();
    let method = parts.next()?.to_owned();
    let path = percent_decode(parts.next()?.split('?').next()?)?;
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
        reply(&mut w, code, &extra, b"<Error><Code>Fault</Code></Error>");
        return Some(());
    }
    if matches!(fault, Some(Fault::CorruptInFlight))
        && let Some(b) = body.first_mut()
    {
        *b ^= 0xff;
    }
    let (code, etag, payload): (u16, Option<String>, Vec<u8>) = match method.as_str() {
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
        Some(Fault::ApplyThenStatus(c)) => reply(&mut w, c, "", b"<Error/>"),
        Some(Fault::ApplyThenDrop) => {
            let _ = w.shutdown(Shutdown::Both);
        }
        Some(Fault::ApplyThenHang(d)) => {
            thread::sleep(d);
            let _ = w.shutdown(Shutdown::Both);
        }
        _ => {
            let extra = etag.map(|e| format!("ETag: {e}\r\n")).unwrap_or_default();
            reply(&mut w, code, &extra, &payload);
        }
    }
    Some(())
}
