//! Std-only fake S3 server: in-memory objects, R2-like conditionals, scripted faults.
#![allow(dead_code)]
use std::collections::{HashMap, VecDeque};
use std::io::{BufRead, BufReader, Read, Write};
use std::net::{Shutdown, SocketAddr, TcpListener, TcpStream};
use std::sync::{Arc, Mutex};
use std::{thread, time::Duration};

#[derive(Clone, Copy, Debug)]
pub enum Fault {
    /// Apply the write, reply with this status.
    CommitThen(u16),
    /// Apply the write, close the socket without replying.
    CommitThenDrop,
    /// Apply the write, stay silent for this many ms, then close.
    CommitThenHang(u64),
    /// Do not apply; reply with status and optional Retry-After.
    Status(u16, Option<u32>),
}
#[derive(Clone, Debug)]
pub struct Rec { pub method: String, pub path: String, pub status: u16 }
struct State { objs: HashMap<String, (Vec<u8>, String)>, faults: VecDeque<Fault>, log: Vec<Rec>, ctr: u64 }
pub struct Fake { pub addr: SocketAddr, st: Arc<Mutex<State>> }

impl Fake {
    pub fn start() -> Fake {
        let l = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = l.local_addr().unwrap();
        let st = Arc::new(Mutex::new(State { objs: HashMap::new(), faults: VecDeque::new(), log: vec![], ctr: 0 }));
        let s2 = st.clone();
        thread::spawn(move || for c in l.incoming().flatten() { let s = s2.clone(); thread::spawn(move || handle(c, s)); });
        Fake { addr, st }
    }
    pub fn queue(&self, f: Fault) { self.st.lock().unwrap().faults.push_back(f); }
    pub fn clear_log(&self) { self.st.lock().unwrap().log.clear(); }
    pub fn count(&self, method: &str, suffix: &str) -> usize {
        self.st.lock().unwrap().log.iter().filter(|r| r.method == method && r.path.ends_with(suffix)).count()
    }
    pub fn statuses(&self, method: &str, suffix: &str) -> Vec<u16> {
        self.st.lock().unwrap().log.iter().filter(|r| r.method == method && r.path.ends_with(suffix)).map(|r| r.status).collect()
    }
}

fn reply(w: &mut TcpStream, code: u16, extra: &str, body: &[u8]) {
    let _ = write!(w, "HTTP/1.1 {code} X\r\nContent-Length: {}\r\nConnection: close\r\n{extra}\r\n", body.len());
    let _ = w.write_all(body);
    let _ = w.flush();
    let _ = w.shutdown(Shutdown::Both);
}

fn handle(s: TcpStream, st: Arc<Mutex<State>>) {
    let _ = s.set_read_timeout(Some(Duration::from_secs(10)));
    let mut r = BufReader::new(s.try_clone().unwrap());
    let mut w = s;
    let mut line = String::new();
    if r.read_line(&mut line).unwrap_or(0) == 0 { return; }
    let mut p = line.split_whitespace();
    let method = p.next().unwrap_or("").to_string();
    let path = p.next().unwrap_or("").split('?').next().unwrap_or("").to_string();
    let mut h: HashMap<String, String> = HashMap::new();
    loop {
        let mut l = String::new();
        if r.read_line(&mut l).unwrap_or(0) == 0 { return; }
        let l = l.trim_end();
        if l.is_empty() { break; }
        if let Some((k, v)) = l.split_once(':') { h.insert(k.trim().to_ascii_lowercase(), v.trim().to_string()); }
    }
    if h.get("expect").map(|v| v.eq_ignore_ascii_case("100-continue")).unwrap_or(false) {
        let _ = w.write_all(b"HTTP/1.1 100 Continue\r\n\r\n");
    }
    let n: usize = h.get("content-length").and_then(|v| v.parse().ok()).unwrap_or(0);
    let mut body = vec![0u8; n];
    if r.read_exact(&mut body).is_err() { return; }

    let mut g = st.lock().unwrap();
    let fault = if method == "PUT" { g.faults.pop_front() } else { None };
    if let Some(Fault::Status(c, ra)) = fault {
        g.log.push(Rec { method, path, status: c });
        drop(g);
        let extra = ra.map(|v| format!("Retry-After: {v}\r\n")).unwrap_or_default();
        reply(&mut w, c, &extra, b"<Error><Code>Fault</Code></Error>");
        return;
    }
    let (code, etag, rbody): (u16, Option<String>, Vec<u8>) = if method == "GET" {
        match g.objs.get(&path) {
            Some((b, e)) => (200, Some(e.clone()), b.clone()),
            None => (404, None, b"<Error><Code>NoSuchKey</Code></Error>".to_vec()),
        }
    } else {
        let cur = g.objs.get(&path).map(|x| x.1.clone());
        let inm = h.get("if-none-match").cloned();
        let im = h.get("if-match").cloned();
        let fail = (inm.as_deref() == Some("*") && cur.is_some())
            || (im.is_some() && cur.as_ref() != im.as_ref());
        if fail {
            (412, None, b"<Error><Code>PreconditionFailed</Code></Error>".to_vec())
        } else {
            g.ctr += 1;
            let e = format!("\"{:032x}\"", g.ctr);
            g.objs.insert(path.clone(), (body, e.clone()));
            (200, Some(e), vec![])
        }
    };
    g.log.push(Rec { method: method.clone(), path, status: code });
    drop(g);
    if method == "PUT" && code == 200 {
        match fault {
            Some(Fault::CommitThen(c)) => { reply(&mut w, c, "", b"<Error><Code>InternalError</Code></Error>"); return; }
            Some(Fault::CommitThenDrop) => { let _ = w.shutdown(Shutdown::Both); return; }
            Some(Fault::CommitThenHang(ms)) => { thread::sleep(Duration::from_millis(ms)); let _ = w.shutdown(Shutdown::Both); return; }
            _ => {}
        }
    }
    let extra = etag.map(|e| format!("ETag: {e}\r\n")).unwrap_or_default();
    reply(&mut w, code, &extra, &rbody);
}
