//! STREAM: the real `memory daemon` binary keeps its peak memory under 128 MB while it captures
//! one 100 MB incompressible source, then one owner whose sources total more than 256 MB, and
//! then audits them in a second run over the same state (a source never audited is due at once).
//!
//! Only the daemon is measured: on macOS it runs under `/usr/bin/time -l` and the `peak memory
//! footprint` line is parsed; elsewhere `VmHWM` is read from `/proc/<pid>/status` before the
//! stop. The fake server's memory is in the test process, not the daemon's.
use super::assemble::Rig;
use super::daemon_restore::{Guard, PREFIX, Setup, setup};
use super::fake::Fake;
use super::{BUCKET, R, e};
use memory::config::Config;
use memory::daemon::Sources;
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory_core::limits::current_key;
use std::fs;
use std::io::{BufRead, BufReader, Write};
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::process::{Command, Stdio};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant};

const LIMIT: u64 = 128_000_000;
const OWNER_DIR: &str = "projects/-p/0f0e0d0c-0b0a-4908-8706-050403020100";
const MIB: u64 = 1 << 20;

/// `bytes` of file content; `random` is incompressible, otherwise a repeated JSON-ish line.
fn fill(path: &Path, bytes: u64, seed: u64, random: bool) -> R {
    fs::create_dir_all(path.parent().ok_or("parent")?).map_err(e)?;
    let mut out = fs::File::create(path).map_err(e)?;
    let mut x = seed | 1;
    let mut written = 0_u64;
    let mut n = 0_u64;
    while written < bytes {
        let mut chunk = Vec::with_capacity(1 << 20);
        while chunk.len() < 1 << 20 {
            if random {
                x ^= x << 13;
                x ^= x >> 7;
                x ^= x << 17;
                chunk.extend_from_slice(&x.to_le_bytes());
            } else {
                n += 1;
                chunk.extend_from_slice(
                    format!("{{\"role\":\"user\",\"n\":{n},\"text\":\"synthetic line\"}}\n")
                        .as_bytes(),
                );
            }
        }
        let take = usize::try_from((bytes - written).min(chunk.len() as u64)).map_err(e)?;
        out.write_all(chunk.get(..take).ok_or("take")?).map_err(e)?;
        written += take as u64;
    }
    Ok(())
}

fn peak_of(text: &str) -> Option<u64> {
    text.lines()
        .find(|l| l.contains("peak memory footprint"))?
        .split_whitespace()
        .next()?
        .parse()
        .ok()
}

fn kill_term(pid: u32) -> R {
    let status = Command::new("/bin/kill")
        .arg("-TERM")
        .arg(pid.to_string())
        .status()
        .map_err(e)?;
    ensure_ok(status.success(), "kill -TERM")
}
fn ensure_ok(ok: bool, what: &str) -> R {
    if ok { Ok(()) } else { Err(what.to_owned()) }
}

/// The daemon's own pid: the child of `/usr/bin/time` on macOS, the child itself elsewhere.
fn daemon_pid(wrapper: u32) -> R<u32> {
    if !cfg!(target_os = "macos") {
        return Ok(wrapper);
    }
    let start = Instant::now();
    while start.elapsed() < Duration::from_secs(10) {
        let out = Command::new("/usr/bin/pgrep")
            .arg("-P")
            .arg(wrapper.to_string())
            .output()
            .map_err(e)?;
        if let Some(pid) = String::from_utf8_lossy(&out.stdout)
            .split_whitespace()
            .next()
            .and_then(|p| p.parse().ok())
        {
            return Ok(pid);
        }
        thread::sleep(Duration::from_millis(20));
    }
    Err("no daemon child of time".to_owned())
}

/// Run the daemon until `until(log)` holds, stop it, and return its peak memory in bytes.
fn run_daemon(s: &Setup, until: &dyn Fn(&str) -> bool) -> R<(u64, String)> {
    let bin = env!("CARGO_BIN_EXE_memory");
    let mut cmd = if cfg!(target_os = "macos") {
        let mut c = Command::new("/usr/bin/time");
        c.arg("-l").arg(bin);
        c
    } else {
        Command::new(bin)
    };
    cmd.arg("daemon")
        .env_clear()
        .env("MEMORY_STATE_DIR", &s.rig.state)
        .env("MEMORY_SIM_TIMING", "0,3600000")
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::piped());
    let mut child = cmd.spawn().map_err(e)?;
    let err = child.stderr.take().ok_or("stderr")?;
    let wrapper = child.id();
    let mut guard = Guard(child);
    let log = Arc::new(Mutex::new(String::new()));
    let sink = Arc::clone(&log);
    let reader = thread::spawn(move || {
        for line in BufReader::new(err).lines().map_while(Result::ok) {
            if let Ok(mut g) = sink.lock() {
                g.push_str(&line);
                g.push('\n');
            }
        }
    });
    let snapshot = || log.lock().map(|g| g.clone()).unwrap_or_default();
    let start = Instant::now();
    loop {
        if until(&snapshot()) {
            break;
        }
        if let Some(status) = guard.0.try_wait().map_err(e)? {
            return Err(format!("daemon exited early: {status}\n{}", snapshot()));
        }
        if start.elapsed() > Duration::from_secs(150) {
            return Err(format!("not done in 150 s\n{}", snapshot()));
        }
        thread::sleep(Duration::from_millis(50));
    }
    let pid = daemon_pid(wrapper)?;
    let mut hwm = None;
    if !cfg!(target_os = "macos") {
        let status = fs::read_to_string(format!("/proc/{pid}/status")).map_err(e)?;
        hwm = status
            .lines()
            .find_map(|l| l.strip_prefix("VmHWM:"))
            .and_then(|v| v.split_whitespace().next()?.parse::<u64>().ok())
            .map(|kb| kb * 1024);
    }
    kill_term(pid)?;
    let begin = Instant::now();
    while guard.0.try_wait().map_err(e)?.is_none() {
        ensure_ok(
            begin.elapsed() < Duration::from_secs(30),
            "no exit after SIGTERM",
        )?;
        thread::sleep(Duration::from_millis(20));
    }
    let _ = reader.join();
    let text = snapshot();
    let peak = hwm
        .or_else(|| peak_of(&text))
        .ok_or("no peak memory reading")?;
    Ok((peak, text))
}

fn audit_matched(log: &str) -> u64 {
    log.lines()
        .filter(|l| l.contains(" INFO round cause=start"))
        .find_map(|l| {
            l.split_whitespace()
                .find_map(|t| t.strip_prefix("audit_matched=")?.parse().ok())
        })
        .unwrap_or(0)
}

fn measure(label: &str, files: &[(&str, u64, bool)], min_objects: usize) -> R<(u64, u64)> {
    let s = setup(label)?;
    let mut seed = 0x9e37_79b9_7f4a_7c15_u64;
    let _ = fs::remove_file(&s.file);
    for (rel, mib, random) in files {
        seed = seed.wrapping_mul(6_364_136_223_846_793_005).wrapping_add(1);
        fill(&s.rig.root.join(rel), mib * MIB, seed, *random)?;
    }
    // Capture.
    let (capture_peak, _) = run_daemon(&s, &|_| s.etag().is_some())?;
    let objects = s
        .server
        .requests()
        .iter()
        .filter(|r| r.method == "PUT" && r.path.contains("/objects/"))
        .count();
    ensure_ok(
        objects >= min_objects,
        &format!("{objects} piece PUTs, wanted {min_objects}"),
    )?;
    // Audit: a second run over the same state; every source is due.
    let want = files.len() as u64;
    let (audit_peak, log) = run_daemon(&s, &|l| l.contains(" INFO round cause=start"))?;
    eprintln!("PEAK {label}: capture={capture_peak} audit={audit_peak} pieces={objects}");
    assert!(
        audit_matched(&log) >= want,
        "audit_matched {} < {want}\n{log}",
        audit_matched(&log)
    );
    Ok((capture_peak, audit_peak))
}

fn within(label: &str, peaks: (u64, u64)) {
    assert!(peaks.0 < LIMIT, "{label}: capture peak {}", peaks.0);
    assert!(peaks.1 < LIMIT, "{label}: audit peak {}", peaks.1);
}

#[test]
fn one_100_mb_incompressible_source_stays_under_128_mb() -> R {
    let rel = format!("{OWNER_DIR}.jsonl");
    within(
        "peak-a",
        measure("peak-a", &[(rel.as_str(), 100, true)], 12)?,
    );
    Ok(())
}

#[test]
fn an_owner_of_more_than_256_mb_stays_under_128_mb() -> R {
    let main = format!("{OWNER_DIR}.jsonl");
    let subs: Vec<String> = (0..4)
        .map(|i| format!("{OWNER_DIR}/subagents/agent-{i}.jsonl"))
        .collect();
    let mut files: Vec<(&str, u64, bool)> = vec![(main.as_str(), 60, false)];
    files.extend(subs.iter().map(|p| (p.as_str(), 60, false)));
    within("peak-b", measure("peak-b", &files, 30)?);
    Ok(())
}

/// A 100 MB JSONL written as `.jsonl.zst` (streamed through the encoder) is decoded at capture
/// and audit with bounded buffers.
#[test]
fn one_100_mb_zst_rollout_stays_under_128_mb() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("peak-zst", &server.url(), PREFIX, false)?;
    let codex = rig.add_root("codex")?;
    fs::create_dir_all(&rig.state).map_err(e)?;
    fs::set_permissions(&rig.state, fs::Permissions::from_mode(0o700)).map_err(e)?;
    fs::copy(&rig.cfg, rig.state.join("config.toml")).map_err(e)?;
    let name = "rollout-2026-11-17T10-00-00-33333333-3333-4333-8333-333333333333.jsonl.zst";
    let path = codex.join("sessions/2026/11/17").join(name);
    fs::create_dir_all(path.parent().ok_or("parent")?).map_err(e)?;
    let mut enc =
        zstd::stream::write::Encoder::new(fs::File::create(&path).map_err(e)?, 3).map_err(e)?;
    let mut n = 0_u64;
    let mut written = 0_u64;
    while written < 100 * MIB {
        let mut chunk = Vec::with_capacity(1 << 20);
        while chunk.len() < 1 << 20 {
            n += 1;
            chunk.extend_from_slice(
                format!("{{\"role\":\"user\",\"n\":{n},\"text\":\"synthetic line\"}}\n").as_bytes(),
            );
        }
        enc.write_all(&chunk).map_err(e)?;
        written += chunk.len() as u64;
    }
    enc.finish().map_err(e)?;
    let config = Config::load(&rig.cfg).map_err(e)?;
    let owner = WalkSources::new(
        locator_roots(&config).map_err(e)?,
        WalkLimits::default(),
        SharedFs::new(),
    )
    .enumerate()
    .first()
    .ok_or("no source")?
    .0
    .clone();
    let current = format!("/{BUCKET}/{PREFIX}{}", current_key(&owner));
    let s = Setup {
        server,
        rig,
        owner,
        file: path,
        current,
    };
    let (capture_peak, _) = run_daemon(&s, &|_| s.etag().is_some())?;
    let (audit_peak, log) = run_daemon(&s, &|l| l.contains(" INFO round cause=start"))?;
    eprintln!("PEAK peak-zst: capture={capture_peak} audit={audit_peak}");
    assert!(audit_matched(&log) >= 1, "{log}");
    within("peak-zst", (capture_peak, audit_peak));
    Ok(())
}
