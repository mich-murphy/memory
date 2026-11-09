//! Std-only HTTP CONNECT tunnel. It cannot read TLS payloads; it counts client->server TLS
//! application-data records and, when armed, swallows the reply of the first request on a
//! connection (lost reply: the request still reaches the server).
#![allow(dead_code)]
use std::io::{Read, Write};
use std::net::{Shutdown, SocketAddr, TcpListener, TcpStream};
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering::SeqCst};
use std::sync::Arc;
use std::{thread, time::Duration};

pub struct Proxy { pub addr: SocketAddr, pub connects: Arc<AtomicUsize>, pub drop_remaining: Arc<AtomicUsize> }

impl Proxy {
    pub fn start() -> Proxy {
        let l = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = l.local_addr().unwrap();
        let connects = Arc::new(AtomicUsize::new(0));
        let dropr = Arc::new(AtomicUsize::new(0));
        let (c2, d2) = (connects.clone(), dropr.clone());
        thread::spawn(move || for c in l.incoming().flatten() { let (a, b) = (c2.clone(), d2.clone()); thread::spawn(move || serve(c, a, b)); });
        Proxy { addr, connects, drop_remaining: dropr }
    }
    pub fn connects(&self) -> usize { self.connects.load(SeqCst) }
    pub fn arm(&self, n: usize) { self.drop_remaining.store(n, SeqCst); }
}

fn serve(mut c: TcpStream, connects: Arc<AtomicUsize>, dropr: Arc<AtomicUsize>) {
    let mut buf = vec![];
    let mut b = [0u8; 1];
    while !buf.ends_with(b"\r\n\r\n") {
        if c.read(&mut b).unwrap_or(0) == 0 { return; }
        buf.push(b[0]);
        if buf.len() > 8192 { return; }
    }
    let head = String::from_utf8_lossy(&buf).to_string();
    let target = match head.split_whitespace().nth(1) { Some(t) => t.to_string(), None => return };
    connects.fetch_add(1, SeqCst);
    let up = match TcpStream::connect(&target) { Ok(u) => u, Err(_) => { let _ = c.write_all(b"HTTP/1.1 502 Bad Gateway\r\n\r\n"); return; } };
    let _ = c.write_all(b"HTTP/1.1 200 Connection established\r\n\r\n");
    let dropping = Arc::new(AtomicBool::new(false));
    let (mut up_r, mut c_w) = (up.try_clone().unwrap(), c.try_clone().unwrap());
    let dr = dropping.clone();
    thread::spawn(move || {
        let mut buf = vec![0u8; 65536];
        loop {
            let n = match up_r.read(&mut buf) { Ok(0) | Err(_) => break, Ok(n) => n };
            if dr.load(SeqCst) { continue; }
            if c_w.write_all(&buf[..n]).is_err() { break; }
        }
        let _ = c_w.shutdown(Shutdown::Both);
    });
    let mut up_w = up.try_clone().unwrap();
    let (mut hdr, mut skip, mut app) = (Vec::<u8>::new(), 0usize, 0usize);
    let mut buf = vec![0u8; 65536];
    loop {
        let n = match c.read(&mut buf) { Ok(0) | Err(_) => break, Ok(n) => n };
        if up_w.write_all(&buf[..n]).is_err() { break; }
        for &byte in &buf[..n] {
            if skip > 0 { skip -= 1; continue; }
            hdr.push(byte);
            if hdr.len() == 5 {
                skip = u16::from_be_bytes([hdr[3], hdr[4]]) as usize;
                if hdr[0] == 0x17 {
                    app += 1;
                    // record 1 is the TLS 1.3 client Finished; record 2 starts the request
                    if app == 2 && dropr.fetch_update(SeqCst, SeqCst, |v| v.checked_sub(1)).is_ok() {
                        dropping.store(true, SeqCst);
                        let (c2, u2) = (c.try_clone().unwrap(), up.try_clone().unwrap());
                        thread::spawn(move || { thread::sleep(Duration::from_millis(1500)); let _ = c2.shutdown(Shutdown::Both); let _ = u2.shutdown(Shutdown::Both); });
                    }
                }
                hdr.clear();
            }
        }
    }
    let _ = up_w.shutdown(Shutdown::Both);
}
