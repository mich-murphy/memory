//! LOOP-2b: `memory daemon` runs the event loop on the real binary against the local server.
//! The reconcile interval is one hour in every test here, so only the watcher, a hint or a
//! stop can make anything happen after the start round. Every wait is bounded.
use super::daemon_restore::{BOUND, Guard, PREFIX, Setup, restore, setup, stop};
use super::fake::Fault;
use super::{BUCKET, R, e};
use memory::assemble::start_wired;
use memory::config::Config;
use memory::daemon::Sources;
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory_core::limits::current_key;
use std::fs;
use std::io::{Read, Write};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

const HOUR: &str = "0,3600000";
const SECOND_SESSION: &str = "projects/-p/2f0e0d0c-0b0a-4908-8706-050403020102.jsonl";

fn with_window(s: &Setup, quiet: u32, cap: u32) -> R {
    let path = s.rig.state.join("config.toml");
    let text = fs::read_to_string(&path).map_err(e)?;
    fs::write(
        &path,
        format!("window = {{ quiet = {quiet}, cap = {cap} }}\n{text}"),
    )
    .map_err(e)
}

fn lines_with<'a>(log: &'a str, event: &str) -> Vec<&'a str> {
    let needle = format!(" INFO {event}");
    log.lines().filter(|l| l.contains(&needle)).collect()
}

fn landed_captures(log: &str) -> usize {
    lines_with(log, "capture owner=")
        .iter()
        .filter(|l| l.ends_with(" result=landed"))
        .count()
}

#[test]
fn an_append_is_in_the_store_within_seconds_not_an_hour() -> R {
    let s = setup("loop-append")?;
    with_window(&s, 1, 2)?;
    let mut child = s.spawn_with(HOUR)?;
    let first = s.wait_commit(&mut child, None)?;
    s.append(12..20)?;
    s.wait_commit(&mut child, Some(&first))?;
    let log = stop(&s, child)?;
    let rounds = lines_with(&log, "round");
    assert_eq!(rounds.len(), 1, "{log}");
    assert!(
        rounds
            .iter()
            .all(|l| l.contains(" INFO round cause=start "))
    );
    assert!(landed_captures(&log) >= 1, "{log}");
    restore(&s, "a")?;
    Ok(())
}

#[test]
fn a_new_session_file_is_captured_by_the_watcher_alone() -> R {
    let s = setup("loop-new")?;
    with_window(&s, 1, 2)?;
    let mut child = s.spawn_with(HOUR)?;
    s.wait_commit(&mut child, None)?;
    let second = s.rig.root.join(SECOND_SESSION);
    fs::write(&second, super::assemble::lines(0..5)).map_err(e)?;
    let config = Config::load(&s.rig.cfg).map_err(e)?;
    let owner = WalkSources::new(
        locator_roots(&config).map_err(e)?,
        WalkLimits::default(),
        SharedFs::new(),
    )
    .enumerate()
    .into_iter()
    .map(|(owner, _)| owner)
    .find(|owner| *owner != s.owner)
    .ok_or("no second owner")?;
    let path = format!("/{BUCKET}/{PREFIX}{}", current_key(&owner));
    let start = Instant::now();
    while s.server.object(&path).is_none() {
        if let Some(status) = child.0.try_wait().map_err(e)? {
            return Err(format!("daemon exited early: {status}"));
        }
        if start.elapsed() > BOUND {
            return Err("the new session file was never captured".to_owned());
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    let log = stop(&s, child)?;
    assert_eq!(lines_with(&log, "round").len(), 1, "{log}");
    assert!(landed_captures(&log) >= 1, "{log}");
    Ok(())
}

/// The results of the log's `capture` lines, in order.
fn capture_results(log: &str) -> Vec<&str> {
    lines_with(log, "capture owner=")
        .iter()
        .filter_map(|l| l.rsplit_once(" result=").map(|(_, result)| result))
        .collect()
}

#[test]
fn a_failed_capture_is_retried_at_its_deadline_without_a_round() -> R {
    let s = setup("loop-retry")?;
    with_window(&s, 1, 2)?;
    let mut child = s.spawn_with(HOUR)?;
    let first = s.wait_commit(&mut child, None)?;
    // One refused piece PUT: the capture fails and its owner gets a retry deadline one second
    // later. Nothing changes the file again and the next round is an hour away, so only the
    // loop's retry wake can upload the bytes.
    s.server.queue("PUT", ".zst", Fault::Status(503, None));
    s.append(12..20)?;
    s.wait_commit(&mut child, Some(&first))?;
    let log = stop(&s, child)?;
    let results = capture_results(&log);
    assert_eq!(results.first(), Some(&"failed"), "{log}");
    assert_eq!(
        results.iter().filter(|r| **r == "failed").count(),
        1,
        "{log}"
    );
    assert!(results.contains(&"landed"), "{log}");
    assert_eq!(lines_with(&log, "round").len(), 1, "{log}");
    restore(&s, "a")?;
    Ok(())
}

/// The `retry` lines of the log.
fn retry_lines(log: &str) -> Vec<&str> {
    lines_with(log, "retry owner=")
}

fn position(log: &str, needle: &str) -> Option<usize> {
    log.find(needle)
}

#[test]
fn a_dropped_piece_put_logs_a_retry_with_its_cause_before_the_failed_capture() -> R {
    let s = setup("loop-retry-cause")?;
    with_window(&s, 1, 2)?;
    let mut child = s.spawn_with(HOUR)?;
    let first = s.wait_commit(&mut child, None)?;
    s.server.queue("PUT", ".zst", Fault::ApplyThenDrop);
    s.append(12..20)?;
    s.wait_commit(&mut child, Some(&first))?;
    let log = stop(&s, child)?;
    let retries = retry_lines(&log);
    assert_eq!(retries.len(), 1, "{log}");
    let line = retries.first().copied().unwrap_or_default();
    assert!(
        line.contains(" class=offline cause=reset landing=no attempt=1 elapsed_ms="),
        "{log}"
    );
    assert!(line.contains(" after_ms="), "{log}");
    let at = position(&log, line).ok_or("no retry")?;
    let failed = position(&log, " result=failed").ok_or("no failed capture")?;
    let landed = log.rfind(" result=landed").ok_or("no landed capture")?;
    assert!(at < failed && failed < landed, "{log}");
    super::log_lines::assert_clean_log(&log, 1, &[]);
    Ok(())
}

#[test]
fn a_lost_current_reply_that_cannot_be_verified_logs_landing_unknown() -> R {
    let s = setup("loop-retry-unknown")?;
    with_window(&s, 1, 2)?;
    let mut child = s.spawn_with(HOUR)?;
    let first = s.wait_commit(&mut child, None)?;
    s.server.queue("PUT", "current.json", Fault::ApplyThenDrop);
    for _ in 0..memory::commit::GET_ATTEMPTS {
        s.server
            .queue("GET", "current.json", Fault::Status(500, None));
    }
    s.append(12..20)?;
    s.wait_commit(&mut child, Some(&first))?;
    let log = stop(&s, child)?;
    let retries = retry_lines(&log);
    assert!(
        retries
            .iter()
            .any(|l| l.contains(" cause=server landing=unknown attempt=1 ")),
        "{log}"
    );
    super::log_lines::assert_clean_log(&log, 1, &[]);
    Ok(())
}

/// Append one session-end hint for the setup's session file, as the hook does.
fn hint_session_end(s: &Setup) -> R {
    let body = format!(
        "{{\"hook_event_name\":\"SessionEnd\",\"session_id\":\"0f0e0d0c-0b0a-4908-8706-050403020100\",\"transcript_path\":\"{}\"}}",
        s.file.display()
    );
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.args(["hook", "claude"])
        .env_clear()
        .env("MEMORY_STATE_DIR", &s.rig.state)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    let mut child = cmd.spawn().map_err(e)?;
    child
        .stdin
        .take()
        .ok_or("no stdin")?
        .write_all(body.as_bytes())
        .map_err(e)?;
    let out = child.wait_with_output().map_err(e)?;
    assert!(out.status.success());
    let written = fs::read_dir(s.rig.state.join("hints"))
        .map_err(e)?
        .filter_map(Result::ok)
        .filter_map(|f| f.metadata().ok())
        .filter(|m| m.len() > 0)
        .count();
    assert_eq!(written, 1, "the hook wrote no hint line");
    Ok(())
}

#[test]
fn a_session_end_hint_flushes_before_the_quiet_window() -> R {
    let s = setup("loop-hint")?;
    with_window(&s, 600, 900)?;
    let mut child = s.spawn_with(HOUR)?;
    let first = s.wait_commit(&mut child, None)?;
    s.append(12..20)?;
    hint_session_end(&s)?;
    s.wait_commit(&mut child, Some(&first))?;
    let log = stop(&s, child)?;
    assert!(landed_captures(&log) >= 1, "{log}");
    restore(&s, "a")?;
    Ok(())
}

#[test]
fn a_stop_during_a_held_piece_put_ends_the_capture_before_the_manifest() -> R {
    let s = setup("loop-stop")?;
    with_window(&s, 1, 2)?;
    let mut child = s.spawn_with(HOUR)?;
    let first = s.wait_commit(&mut child, None)?;
    let baseline = s.server.requests().len();
    s.server
        .queue("PUT", ".zst", Fault::HoldThenApply(Duration::from_secs(2)));
    // About 13 MB of compressible text: two pieces of at most 8 MiB.
    s.append(12..40_000)?;
    let start = Instant::now();
    while s.server.held() == 0 {
        if let Some(status) = child.0.try_wait().map_err(e)? {
            return Err(format!("daemon exited early: {status}"));
        }
        if start.elapsed() > BOUND {
            return Err("no piece PUT reached the server".to_owned());
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    let log = stop(&s, child)?;
    let all = s.server.requests();
    let after: Vec<_> = all
        .get(baseline..)
        .ok_or("slice")?
        .iter()
        .filter(|r| r.method == "PUT")
        .collect();
    let pieces = after
        .iter()
        .filter(|r| r.path.contains("/objects/"))
        .count();
    let manifests = after
        .iter()
        .filter(|r| r.path.contains("/manifests/"))
        .count();
    let pointers = after
        .iter()
        .filter(|r| r.path.ends_with("current.json"))
        .count();
    assert_eq!((pieces, manifests, pointers), (1, 0, 0), "{after:?}");
    assert_eq!(s.etag().as_deref(), Some(first.as_str()));
    assert!(
        lines_with(&log, "capture owner=")
            .iter()
            .any(|l| l.ends_with(" result=stopped")),
        "{log}"
    );
    assert_eq!(
        log.lines().last().map(|l| l.ends_with(" INFO stopped")),
        Some(true)
    );
    // A restart uploads everything.
    let mut two = s.spawn_with(HOUR)?;
    s.wait_commit(&mut two, Some(&first))?;
    stop(&s, two)?;
    restore(&s, "b")?;
    Ok(())
}

#[test]
fn an_idle_loop_stops_at_once_with_stopped_last() -> R {
    let s = setup("loop-idle")?;
    let mut child = s.spawn_with(HOUR)?;
    s.wait_commit(&mut child, None)?;
    let sent = Instant::now();
    let log = stop(&s, child)?;
    assert!(sent.elapsed() < Duration::from_secs(5));
    assert_eq!(lines_with(&log, "round").len(), 1, "{log}");
    Ok(())
}

#[test]
fn a_root_that_is_a_regular_file_refuses_with_watch_unavailable() -> R {
    let s = setup("loop-refuse")?;
    fs::remove_dir_all(&s.rig.root).map_err(e)?;
    fs::write(&s.rig.root, b"not a directory").map_err(e)?;
    let mut child = s.spawn_with("0,200")?;
    let start = Instant::now();
    let status = loop {
        if let Some(status) = child.0.try_wait().map_err(e)? {
            break status;
        }
        if start.elapsed() > BOUND {
            return Err("daemon did not refuse in time".to_owned());
        }
        std::thread::sleep(Duration::from_millis(10));
    };
    let (mut out, mut err) = (String::new(), String::new());
    if let Some(p) = child.0.stdout.as_mut() {
        p.read_to_string(&mut out).map_err(e)?;
    }
    if let Some(p) = child.0.stderr.as_mut() {
        p.read_to_string(&mut err).map_err(e)?;
    }
    assert_eq!(status.code(), Some(1));
    assert_eq!(err, "memory: watch_unavailable\n");
    assert!(out.is_empty());
    // The lock was released.
    memory::state::DaemonLock::acquire(&s.rig.state)
        .map(drop)
        .map_err(e)?;
    Ok(())
}

#[test]
fn the_loop_clock_has_the_daemons_monotonic_origin() -> R {
    let s = setup("loop-origin")?;
    let wired = start_wired(&s.rig.state, &s.rig.state.join("config.toml")).map_err(e)?;
    let (mut daemon, clock) = (wired.daemon, wired.clock);
    // One origin means equal clocks; a clock created even a microsecond earlier or later differs.
    assert_eq!(clock, *daemon.clock_mut());
    Ok(())
}

#[test]
fn creating_and_removing_lock_directories_logs_no_round_cause_dir() -> R {
    let s = setup("loop-lockdir")?;
    with_window(&s, 1, 2)?;
    let mut child = s.spawn_with(HOUR)?;
    let first = s.wait_commit(&mut child, None)?;
    for k in 0..20 {
        let lock = s.rig.root.join(format!("plugins/store/x{k}.json.lock"));
        fs::create_dir_all(&lock).map_err(e)?;
        fs::remove_dir(&lock).map_err(e)?;
    }
    // A later append proves the loop has worked through the events before it.
    s.append(12..20)?;
    s.wait_commit(&mut child, Some(&first))?;
    let log = stop(&s, child)?;
    assert_eq!(lines_with(&log, "round").len(), 1, "{log}");
    assert!(!log.contains("round cause=dir"), "{log}");
    Ok(())
}

#[test]
fn a_directory_moved_into_a_root_with_a_transcript_is_captured() -> R {
    let s = setup("loop-moved")?;
    with_window(&s, 1, 2)?;
    let mut child = s.spawn_with(HOUR)?;
    s.wait_commit(&mut child, None)?;
    let stage = s
        .rig
        .root
        .parent()
        .ok_or("parent")?
        .join("moved-in-stage")
        .join("-m");
    fs::create_dir_all(&stage).map_err(e)?;
    let name = SECOND_SESSION.rsplit('/').next().ok_or("name")?;
    fs::write(stage.join(name), super::assemble::lines(0..5)).map_err(e)?;
    fs::rename(&stage, s.rig.root.join("projects/-m")).map_err(e)?;
    let config = Config::load(&s.rig.cfg).map_err(e)?;
    let owner = WalkSources::new(
        locator_roots(&config).map_err(e)?,
        WalkLimits::default(),
        SharedFs::new(),
    )
    .enumerate()
    .into_iter()
    .map(|(owner, _)| owner)
    .find(|owner| *owner != s.owner)
    .ok_or("no second owner")?;
    let path = format!("/{BUCKET}/{PREFIX}{}", current_key(&owner));
    let start = Instant::now();
    while s.server.object(&path).is_none() {
        if let Some(status) = child.0.try_wait().map_err(e)? {
            return Err(format!("daemon exited early: {status}"));
        }
        if start.elapsed() > BOUND {
            return Err("the moved-in transcript was never captured".to_owned());
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    let log = stop(&s, child)?;
    assert_eq!(lines_with(&log, "round").len(), 1, "{log}");
    Ok(())
}

/// Spawn the daemon with the given stderr, a rotation cap and a 200 ms round interval.
fn spawn_logging(s: &Setup, stderr: Stdio, cap: &str) -> R<Guard> {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.arg("daemon")
        .env_clear()
        .env("MEMORY_STATE_DIR", &s.rig.state)
        .env("MEMORY_SIM_TIMING", "0,200")
        .env("MEMORY_SIM_LOG_CAP", cap)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(stderr);
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    Ok(Guard(cmd.spawn().map_err(e)?))
}

fn sigterm_and_wait(child: &mut Guard) -> R {
    let status = Command::new("/bin/kill")
        .arg("-TERM")
        .arg(child.0.id().to_string())
        .status()
        .map_err(e)?;
    assert!(status.success());
    let start = Instant::now();
    loop {
        if let Some(status) = child.0.try_wait().map_err(e)? {
            assert_eq!(status.code(), Some(0));
            return Ok(());
        }
        if start.elapsed() > Duration::from_secs(10) {
            return Err("daemon did not exit within 10s of SIGTERM".to_owned());
        }
        std::thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn a_daemon_logging_to_a_file_rotates_it_into_three_generations() -> R {
    const CAP: u64 = 2_000;
    let s = setup("loop-rotate")?;
    let dir = s.rig.dir.join("logs");
    fs::create_dir_all(&dir).map_err(e)?;
    let live = dir.join("daemon.log");
    let file = fs::File::create(&live).map_err(e)?;
    let mut child = spawn_logging(&s, Stdio::from(file), &CAP.to_string())?;
    let third = dir.join("daemon.log.3");
    let start = Instant::now();
    while !third.exists() {
        if let Some(status) = child.0.try_wait().map_err(e)? {
            return Err(format!("daemon exited early: {status}"));
        }
        if start.elapsed() > BOUND {
            return Err("three rotations did not happen in time".to_owned());
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    sigterm_and_wait(&mut child)?;
    assert!(!dir.join("daemon.log.4").exists());
    let names = ["daemon.log.3", "daemon.log.2", "daemon.log.1", "daemon.log"];
    let mut all = String::new();
    for (i, name) in names.iter().enumerate() {
        let text = fs::read_to_string(dir.join(name)).map_err(e)?;
        let newest = i == names.len() - 1;
        if !newest {
            let len = u64::try_from(text.len()).map_err(e)?;
            assert!((CAP..=CAP + 512).contains(&len), "{name}: {len} bytes");
        }
        for line in text.lines() {
            let parsed = super::log_lines::parse(line);
            assert!(parsed.is_ok(), "{name}: {parsed:?}");
        }
        if i > 0 {
            let first = text.lines().next().unwrap_or_default();
            assert!(first.contains(" INFO log_rotated"), "{name}: {first:?}");
            assert!(!first.contains("log_rotated "), "{name}: {first:?}");
        }
        all.push_str(&text);
    }
    super::log_lines::assert_clean_log(&all, 1, &[]);
    assert_eq!(lines_with(&all, "log_rotated").len(), 3, "{all}");
    Ok(())
}

#[test]
fn a_daemon_logging_to_a_pipe_never_rotates() -> R {
    let s = setup("loop-norotate")?;
    let mut child = spawn_logging(&s, Stdio::piped(), "1")?;
    s.wait_commit(&mut child, None)?;
    let log = stop(&s, child)?;
    assert!(lines_with(&log, "log_rotated").is_empty(), "{log}");
    Ok(())
}
