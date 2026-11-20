//! `memory status`, `memory unpark` and `memory reconcile` on the real binary beside the local
//! server. Every run clears the environment and sets `MEMORY_STATE_DIR` to a temporary directory.
use super::assemble::{MARKER, SESSION, lines};
use super::daemon_restore::{Setup, setup};
use super::{R, e};
use memory::assemble::start_from_config;
use memory::config::Config;
use memory::daemon::{ParkedEntry, ParkedReason, Sources, StatusBody};
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory::state::{DaemonLock, StateDir, StateFile};
use memory_core::domain::ParkReason;
use std::fs;
use std::path::Path;
use std::process::Command;

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

fn park(s: &Setup) -> R {
    let lock = DaemonLock::acquire(&s.rig.state).map_err(e)?;
    let dir = StateDir::prepare(&lock).map_err(e)?;
    let body = StatusBody {
        parked: vec![ParkedEntry {
            owner: s.owner.clone(),
            reason: ParkedReason::Source(ParkReason::SourceUnstable),
            source: None,
        }],
        ..StatusBody::default()
    };
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
fn status_verify_is_a_usage_error() -> R {
    let s = setup("status-verify")?;
    capture(&s)?;
    let ran = status(&s, &["--verify"])?;
    assert_eq!(ran.code, 64);
    assert_eq!(ran.err, "memory: usage\n");
    assert!(ran.out.is_empty());
    Ok(())
}
