//! Store failures of `memory export` and `owners` on the real binary: each injected
//! fault prints one closed line and exits with a code an operator can act on.
use super::assemble::{PREFIX, Rig};
use super::export::capture;
use super::fake::{Fake, Fault};
use super::{BUCKET, R, e};
use memory::environment::{Cause, StoreError};
use memory::exit::store_exit;
use std::net::TcpListener;
use std::process::{Command, Output};

fn run(rig: &Rig, args: &[&str]) -> R<Output> {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear().env("MEMORY_STATE_DIR", &rig.dir).args(args);
    cmd.output().map_err(e)
}

fn line(o: &Output) -> String {
    format!(
        "exit={} stdout_bytes={} {}",
        o.status.code().unwrap_or(-1),
        o.stdout.len(),
        String::from_utf8_lossy(&o.stderr).trim_end()
    )
}

/// One command, `n` = fresh destination index.
fn cmd(rig: &Rig, owner: &str, which: &str, n: usize) -> R<Output> {
    let out = rig.dir.join(format!("o{which}{n}"));
    let out = out.to_string_lossy().into_owned();
    match which {
        "export" => run(rig, &["export", owner, "--out", &out]),
        _ => run(rig, &["owners"]),
    }
}

fn expect(o: &Output, exit: i32, text: &str) {
    assert_eq!(
        line(o),
        format!("exit={exit} stdout_bytes=0 memory: {text}")
    );
}

#[test]
fn store_failures_name_their_cause_and_exit_by_retryability() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("errors-table", &server.url(), PREFIX, false)?;
    let (owner, _) = capture(&rig)?;
    let piece = server
        .requests()
        .into_iter()
        .filter(|r| r.method == "PUT")
        .map(|r| r.path)
        .find(|p| !p.ends_with("current.json") && !p.contains("manifest"))
        .ok_or("no piece")?;
    let tail = piece.rsplit('/').next().unwrap_or("").to_owned();
    let mut n = 0;
    for which in ["export"] {
        let table: [(&str, Fault, i32, &str); 8] = [
            (
                "",
                Fault::Status(503, Some("7")),
                75,
                "store cause=throttled",
            ),
            ("", Fault::Status(403, None), 77, "store cause=auth"),
            ("", Fault::Status(500, None), 75, "store cause=server"),
            ("", Fault::ApplyThenDrop, 75, "store cause=reset"),
            (&tail, Fault::Status(403, None), 77, "store cause=auth"),
            (&tail, Fault::Status(503, None), 75, "store cause=throttled"),
            (&tail, Fault::Status(404, None), 1, "object_missing"),
            ("", Fault::Status(404, None), 66, "owner_absent"),
        ];
        for (suffix, fault, exit, text) in table {
            n += 1;
            server.queue("GET", suffix, fault);
            expect(&cmd(&rig, &owner, which, n)?, exit, text);
        }
        n += 1;
        let good = server.object(&piece).ok_or("piece")?.0;
        let mut bad = good.clone();
        if let Some(b) = bad.first_mut() {
            *b ^= 0xff;
        }
        server.seed(&piece, &bad);
        expect(&cmd(&rig, &owner, which, n)?, 1, "source_corrupt");
        server.seed(&piece, &good);
    }
    let list_cases: [(Fault, i32, &str); 5] = [
        (Fault::Status(403, None), 77, "store cause=auth"),
        (Fault::Status(500, None), 75, "store cause=server"),
        (Fault::Status(503, None), 75, "store cause=throttled"),
        (Fault::Status(404, None), 1, "store cause=not_found"),
        (
            Fault::ListBody("this is not xml"),
            75,
            "store cause=protocol",
        ),
    ];
    for (fault, exit, text) in list_cases {
        server.queue("GET", "", fault);
        expect(&cmd(&rig, &owner, "owners", 0)?, exit, text);
    }
    let _ = BUCKET;
    Ok(())
}

#[test]
fn a_refused_connection_is_transient_for_both_commands() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("errors-live", &server.url(), PREFIX, false)?;
    let (owner, _) = capture(&rig)?;
    let l = TcpListener::bind("127.0.0.1:0").map_err(e)?;
    let closed = format!("http://{}", l.local_addr().map_err(e)?);
    drop(l);
    let dead = Rig::new("errors-dead", &closed, PREFIX, false)?;
    for (n, which) in ["export", "owners"].into_iter().enumerate() {
        expect(&cmd(&dead, &owner, which, n)?, 75, "store cause=refused");
    }
    Ok(())
}

/// Every cause. The witness match has no wildcard arm: a new `Cause` fails to compile here.
fn causes() -> Vec<Cause> {
    let all = vec![
        Cause::Dns,
        Cause::DnsTimeout,
        Cause::Connect,
        Cause::ConnectTimeout,
        Cause::Refused,
        Cause::Tls,
        Cause::SendTimeout,
        Cause::RecvTimeout,
        Cause::Timeout,
        Cause::Reset,
        Cause::Io,
        Cause::Protocol,
        Cause::NoEtag,
        Cause::Other,
    ];
    for c in &all {
        match c {
            Cause::Dns
            | Cause::DnsTimeout
            | Cause::Connect
            | Cause::ConnectTimeout
            | Cause::Refused
            | Cause::Tls
            | Cause::SendTimeout
            | Cause::RecvTimeout
            | Cause::Timeout
            | Cause::Reset
            | Cause::Io
            | Cause::Protocol
            | Cause::NoEtag
            | Cause::Other => {}
        }
    }
    all
}

/// Every store error with its expected exit (brief 52). The witness match has no wildcard arm.
fn store_errors() -> Vec<(StoreError, u8)> {
    let mut all = vec![
        (StoreError::PreconditionFailed, 1),
        (StoreError::NotFound, 1),
        (StoreError::Throttled(None), 75),
        (StoreError::Throttled(Some(7)), 75),
        (StoreError::Server, 75),
        (StoreError::Auth, 77),
    ];
    for c in causes() {
        all.push((StoreError::Offline(c), 75));
        all.push((StoreError::Ambiguous(c), 75));
    }
    for (e, _) in &all {
        match e {
            StoreError::PreconditionFailed
            | StoreError::NotFound
            | StoreError::Offline(_)
            | StoreError::Throttled(_)
            | StoreError::Server
            | StoreError::Auth
            | StoreError::Ambiguous(_) => {}
        }
    }
    all
}

#[test]
fn the_exit_mapping_covers_every_store_error_variant() {
    assert_eq!(store_exit(None), 1);
    for (error, exit) in store_errors() {
        assert_eq!(store_exit(Some(error)), exit, "{error:?}");
        assert_eq!(
            memory::export::Failure::Store(Some(error)).exit_code(),
            exit
        );
    }
}

#[test]
fn every_cause_line_is_closed_text_without_input() {
    for (error, _) in store_errors() {
        let want = format!("store cause={}", error.code());
        let lines = [
            memory::export::Failure::Store(Some(error)).to_string(),
            memory::owners::Failure::Store(Some(error)).to_string(),
        ];
        for text in lines {
            assert_eq!(text, want);
            assert!(
                text.bytes()
                    .all(|b| b.is_ascii_lowercase() || b == b'_' || b == b' ' || b == b'='),
                "{text}"
            );
        }
    }
}
