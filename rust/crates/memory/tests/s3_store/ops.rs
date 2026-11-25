//! `memory status`, `memory unpark` and `memory reconcile` on the real binary beside the local
//! server. Every run clears the environment and sets `MEMORY_STATE_DIR` to a temporary directory.
use super::assemble::{MARKER, SESSION, lines};
use super::daemon_setup::{BOUND, Guard, Setup, setup};
use super::fake::Fault;
use super::{R, e};
use memory::assemble::start_from_config;
use memory::config::Config;
use memory::daemon::{ParkedEntry, ParkedReason, Sources, StatusBody};
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory::state::{DaemonLock, StateDir, StateFile};
use memory_core::domain::{Owner, ParkReason};
use std::fs;
use std::io::Read;
use std::net::TcpListener;
use std::path::Path;
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

struct Ran {
    code: i32,
    out: String,
    err: String,
}

impl Ran {
    fn has(&self, line: &str, key: &str, value: &str) -> bool {
        self.out
            .lines()
            .find(|l| l.starts_with(line))
            .is_some_and(|l| l.split(' ').any(|w| w == format!("{key}={value}")))
    }
    fn lines_with(&self, word: &str) -> usize {
        self.out.lines().filter(|l| l.starts_with(word)).count()
    }
}

fn capture(s: &Setup) -> R {
    let mut daemon = start_from_config(&s.rig.state, &s.rig.cfg).map_err(e)?;
    let report = daemon.reconcile_once().map_err(e)?;
    assert_eq!(report.failed, 0);
    daemon.shutdown().map_err(e)
}

fn state_listing(dir: &Path) -> R<Vec<(String, u64)>> {
    let mut out = Vec::new();
    for entry in fs::read_dir(dir).map_err(e)? {
        let entry = entry.map_err(e)?;
        let meta = entry.metadata().map_err(e)?;
        out.push((entry.file_name().to_string_lossy().into_owned(), meta.len()));
    }
    out.sort();
    Ok(out)
}

fn needles(s: &Setup) -> R<Vec<String>> {
    let mut needles = vec![
        s.rig.dir.display().to_string(),
        s.rig.state.display().to_string(),
        s.rig.token.display().to_string(),
        SESSION.to_owned(),
        MARKER.to_owned(),
        "projects".to_owned(),
    ];
    let config = Config::load(&s.rig.cfg).map_err(e)?;
    let mut walk = WalkSources::new(
        locator_roots(&config).map_err(e)?,
        WalkLimits::default(),
        SharedFs::new(),
    );
    needles.extend(
        walk.enumerate()
            .iter()
            .map(|(_, src)| src.id.as_str().to_owned()),
    );
    Ok(needles)
}

fn command(s: &Setup, args: &[&str]) -> R<Ran> {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear()
        .env("MEMORY_STATE_DIR", &s.rig.state)
        .args(args);
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    let output = cmd.output().map_err(e)?;
    Ok(Ran {
        code: output.status.code().ok_or("signal")?,
        out: String::from_utf8_lossy(&output.stdout).into_owned(),
        err: String::from_utf8_lossy(&output.stderr).into_owned(),
    })
}

fn leak_free(s: &Setup, ran: &Ran) -> R {
    for needle in &needles(s)? {
        assert!(!ran.out.contains(needle), "stdout holds {needle}");
        assert!(!ran.err.contains(needle), "stderr holds {needle}");
    }
    Ok(())
}

/// `memory status`: no request of any method, the state listing unchanged, a closed output.
fn status(s: &Setup, args: &[&str]) -> R<Ran> {
    let requests = s.server.requests().len();
    let before = state_listing(&s.rig.state)?;
    let mut all = vec!["status"];
    all.extend_from_slice(args);
    let ran = command(s, &all)?;
    assert_eq!(s.server.requests().len(), requests, "status sent a request");
    assert_eq!(state_listing(&s.rig.state)?, before, "status wrote state");
    leak_free(s, &ran)?;
    Ok(ran)
}

fn entry(owner: &Owner) -> ParkedEntry {
    ParkedEntry {
        owner: owner.clone(),
        reason: ParkedReason::Source(ParkReason::SourceUnstable),
        source: None,
    }
}

fn park(s: &Setup) -> R {
    write_status(
        s,
        &StatusBody {
            parked: vec![entry(&s.owner)],
            ..StatusBody::default()
        },
    )
}

fn write_status(s: &Setup, body: &StatusBody) -> R {
    let lock = DaemonLock::acquire(&s.rig.state).map_err(e)?;
    let dir = StateDir::prepare(&lock).map_err(e)?;
    dir.write(StateFile::Status, &serde_json::to_vec(&body).map_err(e)?)
        .map_err(e)
}

#[test]
fn status_after_a_capture_is_all_current() -> R {
    let s = setup("status-current")?;
    capture(&s)?;
    let ran = status(&s, &[])?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert!(ran.has("status ", "state", "loaded") && ran.has("status ", "index", "loaded"));
    assert!(ran.has("sources ", "total", "1") && ran.has("sources ", "current", "1"));
    assert!(ran.has("sources ", "behind", "0") && ran.has("sources ", "unindexed", "0"));
    assert_eq!(ran.out.lines().count(), 3);
    Ok(())
}

#[test]
fn status_counts_an_ignored_file_apart_from_unknown_kind() -> R {
    // Brief 46 / D54: `ignored=` follows `unknown_kind=`.
    let s = setup("status-ignored")?;
    capture(&s)?;
    fs::create_dir_all(s.rig.root.join("cache")).map_err(e)?;
    fs::write(s.rig.root.join("cache/c"), "x\n").map_err(e)?;
    let ran = status(&s, &[])?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert!(ran.has("sources ", "ignored", "1") && ran.has("sources ", "unknown_kind", "0"));
    assert!(ran.out.contains(" unknown_kind=0 ignored=1 unknown_owner="));
    Ok(())
}

#[test]
fn status_shows_an_appended_transcript_as_behind() -> R {
    let s = setup("status-behind")?;
    capture(&s)?;
    s.append(12..20)?;
    let ran = status(&s, &[])?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert!(ran.has("sources ", "behind", "1") && ran.has("sources ", "current", "0"));
    assert!(ran.out.contains(" oldest_behind_s="));
    Ok(())
}

#[test]
fn status_shows_a_never_captured_file_as_unindexed() -> R {
    let s = setup("status-new")?;
    capture(&s)?;
    let other = s
        .rig
        .root
        .join("projects/-p/1f0e0d0c-0b0a-4908-8706-050403020199.jsonl");
    fs::write(&other, lines(0..3)).map_err(e)?;
    let ran = status(&s, &[])?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert!(ran.has("sources ", "unindexed", "1") && ran.has("sources ", "current", "1"));
    Ok(())
}

#[test]
fn status_lists_a_parked_entry_and_exits_1() -> R {
    let s = setup("status-parked")?;
    capture(&s)?;
    park(&s)?;
    let ran = status(&s, &[])?;
    assert_eq!(ran.code, 1, "{}{}", ran.out, ran.err);
    assert_eq!(ran.lines_with("parked owner="), 1);
    assert!(ran.out.contains(" reason=source_unstable scope=source"));
    Ok(())
}

#[test]
fn status_without_a_status_file_says_missing_and_exits_1() -> R {
    let s = setup("status-missing")?;
    capture(&s)?;
    fs::remove_file(s.rig.state.join("status.json")).map_err(e)?;
    let ran = status(&s, &[])?;
    assert_eq!(ran.code, 1, "{}{}", ran.out, ran.err);
    assert!(ran.has("status ", "state", "missing"));
    Ok(())
}

#[test]
fn status_runs_beside_a_held_lock() -> R {
    let s = setup("status-locked")?;
    capture(&s)?;
    let lock = DaemonLock::acquire(&s.rig.state).map_err(e)?;
    let ran = status(&s, &[])?;
    drop(lock);
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert!(ran.has("sources ", "current", "1"));
    Ok(())
}

#[test]
fn status_verify_is_a_usage_error() -> R {
    let s = setup("status-verify")?;
    capture(&s)?;
    let ran = status(&s, &["--verify"])?;
    assert_eq!(ran.code, 64);
    assert_eq!(ran.err, "memory: usage\n");
    assert!(ran.out.is_empty());
    Ok(())
}

// ------------------------------------------------------------------ unpark

fn other_owner(s: &Setup) -> R<Owner> {
    match &s.owner {
        Owner::Session(k) => Ok(Owner::Shared {
            archive: k.archive.clone(),
            machine: k.machine.clone(),
            agent: k.agent.clone(),
        }),
        Owner::Shared { .. } => Err("session owner expected".to_owned()),
    }
}

fn status_bytes(s: &Setup) -> R<Vec<u8>> {
    fs::read(s.rig.state.join("status.json")).map_err(e)
}

fn counters() -> StatusBody {
    let mut body = StatusBody {
        updated_at: "2026-11-20T01:02:03Z".to_owned(),
        rounds: 7,
        ..StatusBody::default()
    };
    body.failures.offline = 3;
    body.audit.matched = 2;
    body
}

fn two_parked(s: &Setup) -> R<(Owner, StatusBody)> {
    let other = other_owner(s)?;
    let mut body = counters();
    body.parked = vec![entry(&s.owner), entry(&other)];
    write_status(s, &body)?;
    Ok((other, body))
}

fn prefix(owner: &Owner) -> String {
    memory_core::limits::owner_prefix(owner)
}

#[test]
fn unpark_beside_a_held_lock_refuses_with_75() -> R {
    let s = setup("unpark-lock")?;
    two_parked(&s)?;
    let before = status_bytes(&s)?;
    let lock = DaemonLock::acquire(&s.rig.state).map_err(e)?;
    let ran = command(&s, &["unpark", "--all"])?;
    drop(lock);
    assert_eq!(ran.code, 75, "{}", ran.out);
    assert_eq!(ran.err, "memory: lock_held\n");
    assert_eq!(status_bytes(&s)?, before);
    Ok(())
}

#[test]
fn unpark_one_owner_keeps_the_other_and_the_counters() -> R {
    let s = setup("unpark-one")?;
    let (other, body) = two_parked(&s)?;
    let ran = command(&s, &["unpark", &prefix(&s.owner)])?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert_eq!(
        ran.out,
        format!("unparked owner={} entries=1\n", prefix(&s.owner))
    );
    leak_free(
        &s,
        &Ran {
            out: ran.out.replace(&prefix(&s.owner), ""),
            ..ran
        },
    )?;
    let (after, code) = memory::status::read_status(&s.rig.state);
    assert_eq!(code, "loaded");
    let want = StatusBody {
        parked: vec![entry(&other)],
        ..body
    };
    assert_eq!(after, Some(want));
    Ok(())
}

#[test]
fn unpark_all_empties_the_list() -> R {
    let s = setup("unpark-all")?;
    let (other, body) = two_parked(&s)?;
    let ran = command(&s, &["unpark", "--all"])?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert_eq!(ran.lines_with("unparked owner="), 2);
    assert!(ran.out.contains(&prefix(&other)));
    let (after, _) = memory::status::read_status(&s.rig.state);
    assert_eq!(
        after,
        Some(StatusBody {
            parked: Vec::new(),
            ..body
        })
    );
    Ok(())
}

#[test]
fn unpark_without_a_match_exits_1_and_keeps_the_bytes() -> R {
    let s = setup("unpark-none")?;
    park(&s)?;
    let other = other_owner(&s)?;
    let before = status_bytes(&s)?;
    let ran = command(&s, &["unpark", &prefix(&other)])?;
    assert_eq!(ran.code, 1, "{}", ran.out);
    assert_eq!(ran.err, "memory: not_parked\n");
    assert_eq!(status_bytes(&s)?, before);
    Ok(())
}

#[test]
fn unpark_refuses_a_damaged_status_and_keeps_the_bytes() -> R {
    let s = setup("unpark-damaged")?;
    park(&s)?;
    let mut bytes = status_bytes(&s)?;
    let last = bytes.last_mut().ok_or("empty")?;
    *last ^= 0xff;
    fs::write(s.rig.state.join("status.json"), &bytes).map_err(e)?;
    let ran = command(&s, &["unpark", "--all"])?;
    assert_eq!(ran.code, 2, "{}", ran.out);
    assert_eq!(ran.err, "memory: status_damaged\n");
    assert_eq!(status_bytes(&s)?, bytes);
    Ok(())
}

#[test]
fn unpark_bad_arguments_exit_64() -> R {
    let s = setup("unpark-usage")?;
    park(&s)?;
    let before = status_bytes(&s)?;
    for args in [
        vec!["unpark"],
        vec!["unpark", "--all", "x"],
        vec!["unpark", "--bogus"],
    ] {
        let ran = command(&s, &args)?;
        assert_eq!((ran.code, ran.err.as_str()), (64, "memory: usage\n"));
    }
    let ran = command(&s, &["unpark", "not/an/owner"])?;
    assert_eq!((ran.code, ran.err.as_str()), (64, "memory: owner\n"));
    assert_eq!(status_bytes(&s)?, before);
    Ok(())
}

#[test]
fn a_daemon_started_after_unpark_captures_the_owner() -> R {
    let s = setup("unpark-then-capture")?;
    park(&s)?;
    {
        let mut daemon = start_from_config(&s.rig.state, &s.rig.cfg).map_err(e)?;
        let report = daemon.reconcile_once().map_err(e)?;
        assert_eq!((report.landed, report.skipped), (0, 1));
        daemon.shutdown().map_err(e)?;
    }
    let ran = command(&s, &["unpark", &prefix(&s.owner)])?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    let mut daemon = start_from_config(&s.rig.state, &s.rig.cfg).map_err(e)?;
    let report = daemon.reconcile_once().map_err(e)?;
    assert_eq!((report.landed, report.skipped), (1, 0));
    daemon.shutdown().map_err(e)
}

// ------------------------------------------------------------------ reconcile

fn reconcile(s: &Setup) -> R<Ran> {
    let ran = command(s, &["reconcile"])?;
    leak_free(
        s,
        &Ran {
            out: ran.out.clone(),
            err: ran.err.clone(),
            code: ran.code,
        },
    )?;
    Ok(ran)
}

#[test]
fn reconcile_beside_a_held_lock_refuses_with_75_and_no_request() -> R {
    let s = setup("reconcile-lock")?;
    let lock = DaemonLock::acquire(&s.rig.state).map_err(e)?;
    let requests = s.server.requests().len();
    let ran = command(&s, &["reconcile"])?;
    drop(lock);
    assert_eq!(ran.code, 75, "{}", ran.out);
    assert_eq!(ran.err, "memory: lock_held\n");
    assert!(ran.out.is_empty());
    assert_eq!(s.server.requests().len(), requests);
    Ok(())
}

#[test]
fn reconcile_captures_a_new_file_then_lands_nothing_with_zero_puts() -> R {
    let s = setup("reconcile-capture")?;
    let ran = reconcile(&s)?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert_eq!(
        ran.out,
        "reconcile landed=1 failed=0 parked=0 skipped=0 deferred=0\n"
    );
    let round = ran
        .err
        .lines()
        .find(|l| l.contains(" INFO round "))
        .ok_or("no round line")?;
    assert!(round.contains(" cause=manual "), "{round}");
    for line in ran.err.lines() {
        assert!(!super::log_lines::parse(line).map_err(e)?.is_empty());
    }
    let out = s.rig.dir.join("exported");
    let out_text = out.display().to_string();
    let restored = command(&s, &["export", &prefix(&s.owner), "--out", &out_text])?;
    assert_eq!(restored.code, 0, "{}{}", restored.out, restored.err);
    assert_eq!(
        fs::read(out.join("bodies/0000.bin")).map_err(e)?,
        fs::read(&s.file).map_err(e)?
    );
    let puts = s.server.count("PUT");
    let again = reconcile(&s)?;
    assert_eq!(again.code, 0, "{}{}", again.out, again.err);
    assert!(
        again.out.starts_with("reconcile landed=0 failed=0 "),
        "{}",
        again.out
    );
    assert_eq!(s.server.count("PUT"), puts, "an unchanged disk made a PUT");
    Ok(())
}

#[test]
fn reconcile_stopped_by_sigterm_during_a_held_put_exits_130() -> R {
    let s = setup("reconcile-signal")?;
    s.server
        .queue("PUT", ".zst", Fault::HoldThenApply(Duration::from_secs(2)));
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear()
        .env("MEMORY_STATE_DIR", &s.rig.state)
        .arg("reconcile")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    let mut child = Guard(cmd.spawn().map_err(e)?);
    let start = Instant::now();
    while s.server.held() == 0 {
        if let Some(status) = child.0.try_wait().map_err(e)? {
            return Err(format!("reconcile exited early: {status}"));
        }
        if start.elapsed() > BOUND {
            return Err("no piece PUT reached the server".to_owned());
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    let killed = Command::new("/bin/kill")
        .arg("-TERM")
        .arg(child.0.id().to_string())
        .status()
        .map_err(e)?;
    assert!(killed.success());
    let start = Instant::now();
    let code = loop {
        if let Some(status) = child.0.try_wait().map_err(e)? {
            break status.code();
        }
        if start.elapsed() > BOUND {
            return Err("reconcile did not exit after SIGTERM".to_owned());
        }
        std::thread::sleep(Duration::from_millis(10));
    };
    assert_eq!(code, Some(130));
    let mut out = String::new();
    if let Some(mut pipe) = child.0.stdout.take() {
        pipe.read_to_string(&mut out).map_err(e)?;
    }
    assert!(out.is_empty(), "{out}");
    let pointers = s
        .server
        .requests()
        .iter()
        .filter(|r| r.method == "PUT" && r.path.ends_with("current.json"))
        .count();
    assert_eq!(pointers, 0, "a stopped reconcile moved current.json");
    Ok(())
}

#[test]
fn reconcile_with_an_unreachable_store_fails_with_exit_1() -> R {
    let s = setup("reconcile-down")?;
    let port = {
        let listener = TcpListener::bind("127.0.0.1:0").map_err(e)?;
        listener.local_addr().map_err(e)?.port()
    };
    let path = s.rig.state.join("config.toml");
    let text = fs::read_to_string(&path).map_err(e)?;
    fs::write(
        &path,
        text.replace(&s.server.url(), &format!("http://127.0.0.1:{port}")),
    )
    .map_err(e)?;
    let ran = reconcile(&s)?;
    assert_eq!(ran.code, 1, "{}{}", ran.out, ran.err);
    assert!(
        ran.out.starts_with("reconcile landed=0 failed=1 "),
        "{}",
        ran.out
    );
    Ok(())
}
