//! `memory check` on the real binary against the local server, over states seeded by a real
//! in-process capture. Every run asserts: no PUT reached the server, the state root is
//! unchanged, and the output holds no path, session name, source id or file bytes.
use super::assemble::{MARKER, SESSION, lines};
use super::daemon_restore::{PREFIX, Setup, setup};
use super::fake::Fake;
use super::{BUCKET, R, e, store_at};
use memory::assemble::start_from_config;
use memory::check::{Compared, compare};
use memory::config::Config;
use memory::daemon::{ParkedEntry, ParkedReason, Sources, StatusBody};
use memory::environment::{Store, StoreError};
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory::s3::TimeoutPolicy;
use memory::state::{DaemonLock, StateDir, StateFile};
use memory_core::domain::ParkReason;
use memory_reader::read_owner;
use std::fs;
use std::io::Write;
use std::net::TcpListener;
use std::path::Path;
use std::process::Command;

const UUID: &str = "0f0e0d0c-0b0a-4908-8706-050403020100";

struct Ran {
    code: i32,
    out: String,
    err: String,
}

impl Ran {
    fn has(&self, key: &str, value: u64) -> bool {
        let first = self.out.lines().next().unwrap_or_default();
        first.contains(&format!(" {key}={value} "))
    }
    fn lines_with(&self, word: &str) -> usize {
        self.out
            .lines()
            .skip(1)
            .filter(|l| l.starts_with(word))
            .count()
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

fn run(s: &Setup) -> R<Ran> {
    let puts = s.server.count("PUT");
    let before = state_listing(&s.rig.state)?;
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear()
        .env("MEMORY_STATE_DIR", &s.rig.state)
        .arg("check");
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    let output = cmd.output().map_err(e)?;
    let ran = Ran {
        code: output.status.code().ok_or("signal")?,
        out: String::from_utf8_lossy(&output.stdout).into_owned(),
        err: String::from_utf8_lossy(&output.stderr).into_owned(),
    };
    assert_eq!(s.server.count("PUT"), puts, "check sent a PUT");
    assert_eq!(state_listing(&s.rig.state)?, before, "check wrote state");
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
    for needle in &needles {
        assert!(!ran.out.contains(needle), "stdout holds {needle}");
        assert!(!ran.err.contains(needle), "stderr holds {needle}");
    }
    Ok(ran)
}

#[test]
fn a_captured_quiet_file_matches() -> R {
    let s = setup("check-match")?;
    capture(&s)?;
    let ran = run(&s)?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert!(ran.has("owners", 1) && ran.has("sources", 1) && ran.has("match", 1));
    assert!(ran.has("mismatch", 0) && ran.has("uncaptured", 0) && ran.has("store_error", 0));
    assert_eq!(ran.out.lines().count(), 1);
    assert!(
        ran.out
            .starts_with("check owners=1 sources=1 match=1 behind=0 unsettled=0 ")
    );
    Ok(())
}

#[test]
fn a_flipped_stored_byte_is_a_mismatch() -> R {
    let s = setup("check-flip")?;
    capture(&s)?;
    let mut store = store_at(&s.server.url(), PREFIX, TimeoutPolicy::PRODUCTION)?;
    let folded = read_owner(&s.owner, |key| match store.get(key) {
        Ok(stored) => Ok(Some(stored.body)),
        Err(StoreError::NotFound) => Ok(None),
        Err(other) => Err(other),
    })
    .map_err(e)?
    .ok_or("absent")?;
    let entry = folded.state.sources.values().next().ok_or("no source")?;
    let piece = entry
        .epochs
        .last()
        .and_then(|r| r.pieces.first())
        .ok_or("no piece")?;
    let path = format!("/{BUCKET}/{PREFIX}{}", piece.object());
    let (mut body, _) = s.server.object(&path).ok_or("piece absent")?;
    *body.first_mut().ok_or("empty")? ^= 0xff;
    s.server.seed(&path, &body);
    let ran = run(&s)?;
    assert_eq!(ran.code, 1, "{}", ran.out);
    assert!(ran.has("mismatch", 1) && ran.has("match", 0));
    assert_eq!(ran.lines_with("mismatch owner="), 1);
    assert!(ran.out.contains(" kind=claude.transcript reason="));
    Ok(())
}

#[test]
fn an_appended_transcript_is_behind_not_a_failure() -> R {
    let s = setup("check-behind")?;
    capture(&s)?;
    s.append(12..20)?;
    let ran = run(&s)?;
    assert_eq!(ran.code, 0, "{}", ran.out);
    assert!(ran.has("behind", 1) && ran.has("match", 0) && ran.has("mismatch", 0));
    Ok(())
}

#[test]
fn a_changed_prefix_of_a_longer_file_is_a_mismatch_not_behind() -> R {
    let s = setup("check-prefix")?;
    capture(&s)?;
    let mut text = fs::read(&s.file).map_err(e)?;
    *text.first_mut().ok_or("empty")? = b'X';
    text.extend_from_slice(lines(12..14).as_bytes());
    fs::write(&s.file, text).map_err(e)?;
    let ran = run(&s)?;
    assert_eq!(ran.code, 1, "{}", ran.out);
    assert!(ran.has("mismatch", 1) && ran.has("behind", 0));
    Ok(())
}

#[test]
fn a_file_never_captured_is_uncaptured() -> R {
    let s = setup("check-new")?;
    capture(&s)?;
    let other = s
        .rig
        .root
        .join("projects/-p/1f0e0d0c-0b0a-4908-8706-050403020199.jsonl");
    fs::write(&other, lines(0..3)).map_err(e)?;
    let ran = run(&s)?;
    assert_eq!(ran.code, 1, "{}", ran.out);
    assert!(ran.has("uncaptured", 1) && ran.has("match", 1) && ran.has("owners", 2));
    assert_eq!(ran.lines_with("uncaptured owner="), 1);
    Ok(())
}

#[test]
fn a_parked_entry_fails_the_check() -> R {
    let s = setup("check-parked")?;
    capture(&s)?;
    {
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
            .map_err(e)?;
    }
    let ran = run(&s)?;
    assert_eq!(ran.code, 1, "{}", ran.out);
    assert!(ran.has("parked", 1) && ran.has("match", 1));
    assert_eq!(ran.lines_with("parked owner="), 1);
    assert!(ran.out.contains(" reason=source_unstable scope=source"));
    assert!(
        ran.out
            .lines()
            .next()
            .unwrap_or_default()
            .ends_with("status=loaded")
    );
    Ok(())
}

#[test]
fn a_deleted_sibling_is_remote_only() -> R {
    let s = setup("check-remote")?;
    let debug = s.rig.root.join(format!("debug/{UUID}.txt"));
    fs::create_dir_all(debug.parent().ok_or("parent")?).map_err(e)?;
    fs::write(&debug, "debug line\n").map_err(e)?;
    capture(&s)?;
    let ran = run(&s)?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert!(ran.has("sources", 2) && ran.has("match", 2) && ran.has("remote_only", 0));
    fs::remove_file(&debug).map_err(e)?;
    let ran = run(&s)?;
    assert_eq!(ran.code, 0, "{}", ran.out);
    assert!(ran.has("sources", 1) && ran.has("match", 1) && ran.has("remote_only", 1));
    Ok(())
}

#[test]
fn a_deferred_snapshot_is_counted_apart() -> R {
    let s = setup("check-deferred")?;
    capture(&s)?;
    fs::write(s.rig.root.join("settings.json"), "{}\n").map_err(e)?;
    let ran = run(&s)?;
    assert_eq!(ran.code, 0, "{}", ran.out);
    assert!(ran.has("deferred_snapshot", 1) && ran.has("uncaptured", 0) && ran.has("sources", 1));
    Ok(())
}

#[test]
fn a_withheld_source_is_withheld_not_uncaptured() -> R {
    let server = Fake::start()?;
    let rig = super::assemble::Rig::new("check-withheld", &server.url(), PREFIX, true)?;
    fs::create_dir_all(&rig.state).map_err(e)?;
    fs::set_permissions(
        &rig.state,
        std::os::unix::fs::PermissionsExt::from_mode(0o700),
    )
    .map_err(e)?;
    fs::copy(&rig.cfg, rig.state.join("config.toml")).map_err(e)?;
    let s = Setup {
        server,
        rig,
        owner: super::daemon_restore::setup("check-unused")?.owner,
        file: std::path::PathBuf::new(),
        current: String::new(),
    };
    let ran = run(&s)?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert!(ran.has("withheld", 1) && ran.has("uncaptured", 0) && ran.has("match", 0));
    assert_eq!(ran.lines_with("withheld owner="), 1);
    assert!(ran.out.contains(" reason=collector_state"), "{}", ran.out);
    Ok(())
}

#[test]
fn an_unreachable_store_makes_the_result_unknown() -> R {
    let s = setup("check-down")?;
    capture(&s)?;
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
    let ran = run(&s)?;
    assert_eq!(ran.code, 3, "{}{}", ran.out, ran.err);
    assert!(ran.has("store_error", 1));
    Ok(())
}

#[test]
fn a_store_fault_on_a_get_makes_the_result_unknown() -> R {
    let s = setup("check-fault")?;
    capture(&s)?;
    s.server
        .queue("GET", "current.json", super::fake::Fault::Status(500, None));
    let ran = run(&s)?;
    assert_eq!(ran.code, 3, "{}{}", ran.out, ran.err);
    Ok(())
}

#[test]
fn a_refused_config_is_exit_two_with_one_closed_line() -> R {
    let s = setup("check-refuse")?;
    fs::remove_file(s.rig.state.join("config.toml")).map_err(e)?;
    let ran = run(&s)?;
    assert_eq!(ran.code, 2);
    assert!(ran.out.is_empty());
    assert_eq!(ran.err.lines().count(), 1);
    assert!(ran.err.starts_with("memory: "));
    Ok(())
}

#[test]
fn compare_decides_each_case() {
    assert_eq!(compare(b"abc", b"abc", true), Compared::Match);
    assert_eq!(compare(b"abc", b"abc", false), Compared::Match);
    assert_eq!(compare(b"abc", b"abcd", true), Compared::Behind);
    assert_eq!(compare(b"abc", b"abcd", false), Compared::Unsettled);
    assert_eq!(compare(b"abc", b"xbcd", true), Compared::Mismatch);
    assert_eq!(compare(b"abcd", b"abc", true), Compared::Mismatch);
    assert_eq!(compare(b"abc", b"abd", true), Compared::Mismatch);
    assert_eq!(compare(b"abc", b"abd", false), Compared::Mismatch);
}

#[test]
fn an_appended_not_prefix_stable_source_is_unsettled() -> R {
    let s = setup("check-unsettled")?;
    let debug = s.rig.root.join(format!("debug/{UUID}.txt"));
    fs::create_dir_all(debug.parent().ok_or("parent")?).map_err(e)?;
    fs::write(&debug, "debug line\n").map_err(e)?;
    capture(&s)?;
    fs::OpenOptions::new()
        .append(true)
        .open(&debug)
        .map_err(e)?
        .write_all(b"more\n")
        .map_err(e)?;
    let ran = run(&s)?;
    assert_eq!(ran.code, 0, "{}", ran.out);
    assert!(ran.has("mismatch", 0));
    Ok(())
}
