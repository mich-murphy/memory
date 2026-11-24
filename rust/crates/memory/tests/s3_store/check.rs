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
    // Brief 54: a kept kind of the same session (Claude tool-results), formerly a debug file.
    let debug = s
        .rig
        .root
        .join(format!("projects/-p/{UUID}/tool-results/t.txt"));
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

// Brief 54: session-env, usage-data and paste-cache files are `Ignored` now (formerly counted
// `deferred_snapshot`): not yielded, not unknown_kind, exit 0.
#[test]
fn session_env_usage_and_paste_files_are_counted_ignored() -> R {
    let s = setup("check-deferred")?;
    capture(&s)?;
    for rel in [
        format!("session-env/{UUID}/e"),
        "usage-data/session-meta/u.json".to_owned(),
        "paste-cache/p.txt".to_owned(),
    ] {
        let f = s.rig.root.join(rel);
        fs::create_dir_all(f.parent().ok_or("parent")?).map_err(e)?;
        fs::write(f, "x\n").map_err(e)?;
    }
    let ran = run(&s)?;
    assert_eq!(ran.code, 0, "{}", ran.out);
    assert!(ran.has("ignored", 3) && ran.has("unknown_kind", 0));
    assert!(ran.has("uncaptured", 0) && ran.has("sources", 1));
    Ok(())
}

#[test]
fn an_ignored_file_is_counted_apart_from_unknown_kind() -> R {
    // Brief 46 / D54: `ignored=` follows `unknown_kind=` and nothing is uncaptured.
    let s = setup("check-ignored")?;
    capture(&s)?;
    fs::create_dir_all(s.rig.root.join("cache")).map_err(e)?;
    fs::write(s.rig.root.join("cache/c"), "x\n").map_err(e)?;
    let ran = run(&s)?;
    assert_eq!(ran.code, 0, "{}", ran.out);
    assert!(ran.has("ignored", 1) && ran.has("unknown_kind", 0) && ran.has("uncaptured", 0));
    assert!(ran.out.contains(" unknown_kind=0 ignored=1 unknown_owner="));
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
    let lines = store_error_lines(&ran.out);
    assert_eq!(lines.len(), 1, "{}", ran.out);
    assert!(
        lines
            .iter()
            .all(|l| l.contains(" kind=chain cause=refused")),
        "{}",
        ran.out
    );
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
    let lines = store_error_lines(&ran.out);
    assert_eq!(lines.len(), 1, "{}", ran.out);
    assert!(
        lines
            .iter()
            .all(|l| l.ends_with(" kind=chain cause=server")),
        "{}",
        ran.out
    );
    Ok(())
}

#[test]
fn a_piece_get_fault_names_the_source_kind_and_cause() -> R {
    let s = setup("check-piece-fault")?;
    capture(&s)?;
    s.server
        .queue("GET", ".zst", super::fake::Fault::Status(500, None));
    let ran = run(&s)?;
    assert_eq!(ran.code, 3, "{}{}", ran.out, ran.err);
    let lines = store_error_lines(&ran.out);
    assert_eq!(lines.len(), 1, "{}", ran.out);
    assert!(
        lines.iter().all(|l| l.contains(" kind=")
            && !l.contains("kind=chain")
            && l.ends_with(" cause=server")),
        "{}",
        ran.out
    );
    Ok(())
}

fn store_error_lines(out: &str) -> Vec<&str> {
    out.lines()
        .filter(|l| l.starts_with("store_error owner="))
        .collect()
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

/// A captured file that can no longer be read cannot be compared: `unsettled`, never `match`
/// and never a failure of the criterion.
#[test]
fn an_unreadable_captured_file_is_unsettled_not_match() -> R {
    use std::os::unix::fs::PermissionsExt;
    let s = setup("check-unreadable")?;
    capture(&s)?;
    let config = Config::load(&s.rig.cfg).map_err(e)?;
    let mut walk = WalkSources::new(
        locator_roots(&config).map_err(e)?,
        WalkLimits::default(),
        SharedFs::new(),
    );
    let rows = walk.enumerate();
    assert_eq!(rows.len(), 1);
    let file = s.rig.root.join(&rows.first().ok_or("row")?.1.path);
    fs::set_permissions(&file, fs::Permissions::from_mode(0o000)).map_err(e)?;
    let ran = run(&s);
    fs::set_permissions(&file, fs::Permissions::from_mode(0o600)).map_err(e)?;
    let ran = ran?;
    assert_eq!(ran.code, 0, "{}", ran.out);
    assert!(ran.has("unsettled", 1) && ran.has("match", 0) && ran.has("mismatch", 0));
    Ok(())
}

/// A file whose size or identity changes between the two stats is not read as settled.
#[test]
fn read_settled_refuses_a_file_that_moved_during_the_read() {
    use memory::check::read_settled;
    use memory::environment::{FileSystem, Stat};
    use memory_core::domain::{Birth, FileId};
    struct Moving {
        stats: u64,
        grow: bool,
    }
    impl FileSystem for Moving {
        type Error = ();
        fn stat(&mut self, _: u64) -> Result<Option<Stat>, ()> {
            self.stats += 1;
            let size = if self.grow && self.stats > 1 { 6 } else { 5 };
            Ok(Some(Stat {
                id: FileId {
                    dev: 1,
                    ino: 1,
                    birth: Birth::Unavailable,
                },
                size,
                mtime_ns: 1,
            }))
        }
        fn read(&mut self, _: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, ()> {
            let end = usize::try_from(range.end).unwrap_or(5).min(5);
            Ok(b"hello".get(..end).unwrap_or_default().to_vec())
        }
    }
    let mut still = Moving {
        stats: 0,
        grow: false,
    };
    assert_eq!(read_settled(&mut still, 1), Some(b"hello".to_vec()));
    let mut moving = Moving {
        stats: 0,
        grow: true,
    };
    assert_eq!(read_settled(&mut moving, 1), None);
}

/// A Claude debug log is not a protected document, so it is prefix-stable: appended bytes put
/// it `behind`, not `unsettled` and not `mismatch`.
#[test]
fn an_appended_debug_log_is_behind() -> R {
    let s = setup("check-unsettled")?;
    // Brief 54: a kept kind of the same session (Claude tool-results), formerly a debug file.
    let debug = s
        .rig
        .root
        .join(format!("projects/-p/{UUID}/tool-results/t.txt"));
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
    assert!(ran.has("mismatch", 0) && ran.has("behind", 1) && ran.has("unsettled", 0));
    Ok(())
}

/// `bytes` of incompressible content, written one MiB at a time.
fn fill_random(path: &Path, bytes: u64) -> R {
    let mut out = fs::File::create(path).map_err(e)?;
    let mut x = 0x9e37_79b9_7f4a_7c15_u64;
    let mut written = 0_u64;
    while written < bytes {
        let mut chunk = Vec::with_capacity(1 << 20);
        while chunk.len() < 1 << 20 {
            x ^= x << 13;
            x ^= x >> 7;
            x ^= x << 17;
            chunk.extend_from_slice(&x.to_le_bytes());
        }
        let take = usize::try_from((bytes - written).min(chunk.len() as u64)).map_err(e)?;
        out.write_all(chunk.get(..take).ok_or("take")?).map_err(e)?;
        written += take as u64;
    }
    Ok(())
}

/// Run `memory check` to completion and return its output and peak memory in bytes: macOS under
/// `/usr/bin/time -l` (`peak memory footprint`), elsewhere the last `VmHWM` read before exit.
fn check_peak(s: &Setup) -> R<(Ran, u64)> {
    use std::process::Stdio;
    let bin = env!("CARGO_BIN_EXE_memory");
    let mut cmd = if cfg!(target_os = "macos") {
        let mut c = Command::new("/usr/bin/time");
        c.arg("-l").arg(bin);
        c
    } else {
        Command::new(bin)
    };
    cmd.arg("check")
        .env_clear()
        .env("MEMORY_STATE_DIR", &s.rig.state)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    let mut child = cmd.spawn().map_err(e)?;
    let pid = child.id();
    let mut out = child.stdout.take().ok_or("stdout")?;
    let mut err = child.stderr.take().ok_or("stderr")?;
    let t_out = std::thread::spawn(move || {
        let mut v = Vec::new();
        let _ = std::io::Read::read_to_end(&mut out, &mut v);
        v
    });
    let t_err = std::thread::spawn(move || {
        let mut v = Vec::new();
        let _ = std::io::Read::read_to_end(&mut err, &mut v);
        v
    });
    let mut hwm = None;
    let status = loop {
        if let Some(status) = child.try_wait().map_err(e)? {
            break status;
        }
        if !cfg!(target_os = "macos")
            && let Ok(text) = fs::read_to_string(format!("/proc/{pid}/status"))
            && let Some(kb) = text
                .lines()
                .find_map(|l| l.strip_prefix("VmHWM:"))
                .and_then(|v| v.split_whitespace().next()?.parse::<u64>().ok())
        {
            hwm = Some(kb * 1024);
        }
        std::thread::sleep(std::time::Duration::from_millis(5));
    };
    let out = String::from_utf8_lossy(&t_out.join().map_err(|_| "join")?).into_owned();
    let err = String::from_utf8_lossy(&t_err.join().map_err(|_| "join")?).into_owned();
    let peak = hwm
        .or_else(|| {
            err.lines()
                .find(|l| l.contains("peak memory footprint"))?
                .split_whitespace()
                .next()?
                .parse()
                .ok()
        })
        .ok_or("no peak memory reading")?;
    let ran = Ran {
        code: status.code().ok_or("signal")?,
        out,
        err,
    };
    Ok((ran, peak))
}

#[test]
fn checking_one_100_mb_incompressible_source_stays_under_128_mb() -> R {
    const LIMIT: u64 = 128_000_000;
    let s = setup("check-peak")?;
    fill_random(&s.file, 100 << 20)?;
    capture(&s)?;
    let (ran, peak) = check_peak(&s)?;
    eprintln!("PEAK check={peak}");
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert!(ran.has("match", 1) && ran.has("mismatch", 0), "{}", ran.out);
    assert!(peak < LIMIT, "check peak {peak}");
    Ok(())
}

/// One `memory reconcile` on the real binary: exit code, stdout and the stderr log lines.
fn reconcile(s: &Setup) -> R<(i32, String, String)> {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear()
        .env("MEMORY_STATE_DIR", &s.rig.state)
        .arg("reconcile");
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    let output = cmd.output().map_err(e)?;
    Ok((
        output.status.code().ok_or("signal")?,
        String::from_utf8_lossy(&output.stdout).into_owned(),
        String::from_utf8_lossy(&output.stderr).into_owned(),
    ))
}

fn round_line(err: &str) -> R<String> {
    for line in err.lines() {
        super::log_lines::parse(line)?;
    }
    err.lines()
        .find(|l| l.contains(" INFO round "))
        .map(str::to_owned)
        .ok_or_else(|| format!("no round line: {err}"))
}

/// A configured root that is gone (unmounted or moved) never deletes: the real resolver skips
/// its stored sources (`absence_skipped`) over three rounds, and the entry stays live.
#[test]
fn a_missing_root_deletes_nothing_on_the_real_binary() -> R {
    let s = setup("check-noroot")?;
    capture(&s)?;
    let moved = s.rig.dir.join("claude-moved");
    fs::rename(&s.rig.root, &moved).map_err(e)?;
    for _ in 0..3 {
        let (code, _, err) = reconcile(&s)?;
        assert_eq!(code, 0, "{err}");
        let line = round_line(&err)?;
        assert!(line.contains(" deleted=0 "), "{line}");
        // Brief 47: the round line ends with `withheld=<n>` now.
        assert!(line.ends_with(" absence_skipped=1 withheld=0"), "{line}");
    }
    fs::rename(&moved, &s.rig.root).map_err(e)?;
    let mut store = store_at(&s.server.url(), PREFIX, TimeoutPolicy::PRODUCTION)?;
    let folded = read_owner(&s.owner, |key| match store.get(key) {
        Ok(stored) => Ok(Some(stored.body)),
        Err(StoreError::NotFound) => Ok(None),
        Err(other) => Err(other),
    })
    .map_err(e)?
    .ok_or("absent")?;
    let entry = folded.state.sources.values().next().ok_or("no source")?;
    assert!(
        entry.deleted_at.is_none(),
        "a missing root recorded a deletion"
    );
    Ok(())
}

#[test]
fn a_deleted_file_is_recorded_on_the_second_reconcile_and_restore_skips_it() -> R {
    let s = setup("check-deleted")?;
    capture(&s)?;
    fs::remove_file(&s.file).map_err(e)?;
    let (code, _, err) = reconcile(&s)?;
    assert_eq!(code, 0, "{err}");
    let line = round_line(&err)?;
    assert!(line.contains(" deleted=0 "), "{line}");
    let (code, _, err) = reconcile(&s)?;
    assert_eq!(code, 0, "{err}");
    let line = round_line(&err)?;
    assert!(line.contains(" deleted=1 "), "{line}");
    let mut store = store_at(&s.server.url(), PREFIX, TimeoutPolicy::PRODUCTION)?;
    let folded = read_owner(&s.owner, |key| match store.get(key) {
        Ok(stored) => Ok(Some(stored.body)),
        Err(StoreError::NotFound) => Ok(None),
        Err(other) => Err(other),
    })
    .map_err(e)?
    .ok_or("absent")?;
    let entry = folded.state.sources.values().next().ok_or("no source")?;
    assert!(entry.deleted_at.is_some(), "no deleted_at");
    let ran = run(&s)?;
    assert!(ran.has("remote_only", 0), "{}", ran.out);
    // A third round records nothing more.
    let (_, _, err) = reconcile(&s)?;
    assert!(round_line(&err)?.contains(" deleted=0 "));
    let out = s.rig.dir.join("out-deleted");
    let result = Command::new(env!("CARGO_BIN_EXE_memory"))
        .env_clear()
        .env("MEMORY_STATE_DIR", &s.rig.state)
        .arg("restore")
        .arg(memory_core::limits::owner_prefix(&s.owner))
        .arg("--out")
        .arg(&out)
        .output()
        .map_err(e)?;
    let err = String::from_utf8_lossy(&result.stderr).into_owned();
    assert_eq!(result.status.code(), Some(0), "{err}");
    assert!(err.contains("skipped_deleted=1"), "{err}");
    assert!(!out.join(SESSION).exists());
    Ok(())
}

// ---- Brief 52, CHECKCONFIRM: a concurrent writer must not cause a false alarm nor hide a
// defect ----

fn debug_file(s: &Setup) -> R<std::path::PathBuf> {
    // Brief 54: a kept kind of the same session (Claude tool-results), formerly a debug file.
    let debug = s
        .rig
        .root
        .join(format!("projects/-p/{UUID}/tool-results/t.txt"));
    fs::create_dir_all(debug.parent().ok_or("parent")?).map_err(e)?;
    Ok(debug)
}

/// Run the in-process check; `act` is the concurrent writer, called once at `at`.
fn raced(s: &Setup, at: memory::check::Phase, mut act: impl FnMut()) -> R<memory::check::Report> {
    let mut done = false;
    let report = memory::check::check_with(&s.rig.state, &mut |p| {
        if p == at && !done {
            done = true;
            act();
        }
    })
    .map_err(|r| r.code)?;
    assert!(done, "hook never ran");
    Ok(report)
}

#[test]
fn a_commit_between_walk_and_fetch_is_not_remote_only() -> R {
    use memory::check::Phase;
    let s = setup("race-remote")?;
    capture(&s)?;
    let debug = debug_file(&s)?;
    let r = raced(&s, Phase::Walked, || {
        let _ = fs::write(&debug, "debug line\n");
        let _ = capture(&s);
    })?;
    assert_eq!(r.tally.remote_only, 0, "{:?}", r.details);
    assert_eq!(r.tally.mismatch + r.tally.uncaptured, 0);
    assert_eq!(r.exit(), 0);
    Ok(())
}

#[test]
fn a_commit_between_fetch_and_judge_is_not_uncaptured() -> R {
    use memory::check::Phase;
    let s = setup("race-uncap")?;
    capture(&s)?;
    fs::write(debug_file(&s)?, "debug line\n").map_err(e)?; // seen by the walk, not yet captured
    let r = raced(&s, Phase::Fetched, || {
        let _ = capture(&s);
    })?;
    assert_eq!(r.tally.uncaptured, 0, "{:?}", r.details);
    assert_eq!(r.tally.matched, 2);
    assert_eq!(r.exit(), 0);
    Ok(())
}

#[test]
fn a_file_deleted_after_the_walk_is_never_a_failure() -> R {
    use memory::check::Phase;
    let s = setup("race-del")?;
    let debug = debug_file(&s)?;
    fs::write(&debug, "debug line\n").map_err(e)?;
    capture(&s)?;
    let r = raced(&s, Phase::Fetched, || {
        let _ = fs::remove_file(&debug);
    })?;
    assert_eq!(r.tally.mismatch + r.tally.uncaptured, 0, "{:?}", r.details);
    assert_eq!(r.exit(), 0);
    Ok(())
}

#[test]
fn a_file_really_gone_locally_is_still_remote_only() -> R {
    use memory::check::Phase;
    let s = setup("race-real-gone")?;
    let debug = debug_file(&s)?;
    fs::write(&debug, "debug line\n").map_err(e)?;
    capture(&s)?;
    fs::remove_file(&debug).map_err(e)?;
    let r = raced(&s, Phase::Walked, || {})?;
    assert_eq!(r.tally.remote_only, 1);
    Ok(())
}

#[test]
fn a_never_captured_file_is_still_uncaptured_and_exits_one() -> R {
    use memory::check::Phase;
    let s = setup("race-real-uncap")?;
    capture(&s)?;
    fs::write(debug_file(&s)?, "debug line\n").map_err(e)?;
    let r = raced(&s, Phase::Fetched, || {})?;
    assert_eq!((r.tally.uncaptured, r.exit()), (1, 1));
    // The real binary agrees.
    let ran = run(&s)?;
    assert_eq!(ran.code, 1, "{}", ran.out);
    assert!(ran.has("uncaptured", 1));
    Ok(())
}

#[test]
fn a_corrupted_piece_is_still_a_mismatch_even_when_the_owner_moved() -> R {
    use memory::check::Phase;
    let s = setup("race-real-piece")?;
    capture(&s)?;
    let mut store = store_at(&s.server.url(), PREFIX, TimeoutPolicy::PRODUCTION)?;
    let folded = read_owner(&s.owner, |k| match store.get(k) {
        Ok(x) => Ok(Some(x.body)),
        Err(StoreError::NotFound) => Ok(None),
        Err(o) => Err(o),
    })
    .map_err(e)?
    .ok_or("absent")?;
    let piece = folded
        .state
        .sources
        .values()
        .next()
        .and_then(|x| x.epochs.last())
        .and_then(|r| r.pieces.first())
        .ok_or("piece")?;
    let path = format!("/{BUCKET}/{PREFIX}{}", piece.object());
    let (mut body, _) = s.server.object(&path).ok_or("absent")?;
    *body.first_mut().ok_or("empty")? ^= 0xff;
    s.server.seed(&path, &body);
    // Quiet: one mismatch, and the real binary exits 1.
    let ran = run(&s)?;
    assert_eq!(ran.code, 1, "{}", ran.out);
    assert!(ran.has("mismatch", 1));
    // The owner moves between fetch and judge (an append is captured): still a mismatch.
    let r = raced(&s, Phase::Fetched, || {
        let _ = s.append(12..14);
        let _ = capture(&s);
    })?;
    assert_eq!(r.tally.mismatch, 1, "{:?}", r.details);
    Ok(())
}

fn gets(s: &Setup) -> usize {
    s.server.count("GET")
}

#[test]
fn a_quiet_owner_costs_no_extra_request() -> R {
    let s = setup("race-cost")?;
    capture(&s)?;
    let before = gets(&s);
    let ran = run(&s)?;
    assert_eq!(ran.code, 0, "{}", ran.out);
    // current.json, one manifest, one piece: nothing more.
    assert_eq!(gets(&s) - before, 3);
    // An anomaly with an unchanged head costs exactly one more current.json GET.
    fs::write(debug_file(&s)?, "debug line\n").map_err(e)?;
    let before = gets(&s);
    let ran = run(&s)?;
    assert_eq!(ran.code, 1, "{}", ran.out);
    // 3 as above for the match, 1 for the uncaptured judge (none), +1 confirmation.
    let extra = gets(&s) - before;
    eprintln!("anomalous owner GETs={extra} (quiet=3)");
    assert_eq!(extra, 4);
    Ok(())
}

/// Parent (brief 52 teeth): a file rewritten shorter and captured between fetch and judge is
/// judged against the first (older) epoch as a `mismatch`; the confirmation sees the moved head
/// and the fresh epoch matches, so it is neither a mismatch nor a failure.
#[test]
fn a_rewrite_captured_between_fetch_and_judge_is_not_a_mismatch() -> R {
    use memory::check::Phase;
    let s = setup("race-rewrite")?;
    let debug = debug_file(&s)?;
    fs::write(&debug, "a longer first debug line\n").map_err(e)?;
    capture(&s)?;
    fs::write(&debug, "short\n").map_err(e)?;
    let r = raced(&s, Phase::Fetched, || {
        let _ = capture(&s);
    })?;
    assert_eq!(r.tally.mismatch + r.tally.uncaptured, 0, "{:?}", r.details);
    assert_eq!(r.exit(), 0);
    Ok(())
}

/// Brief 54: `Sources` that adds one hand-built source (a file-history snapshot, which the
/// locator ignores now) to the real walk, to seed a stored entry as written before the change.
struct WithExtra {
    inner: WalkSources,
    extra: (memory_core::domain::Owner, memory::capture::Source),
}

impl Sources for WithExtra {
    fn enumerate(&mut self) -> Vec<(memory_core::domain::Owner, memory::capture::Source)> {
        let mut all = self.inner.enumerate();
        all.push(self.extra.clone());
        all
    }
}

// Brief 54: a stored live entry whose path now locates to `Ignored` is `retired=1`, not
// `remote_only`, and the check exits 0 (real binary).
#[test]
fn a_stored_retired_entry_is_counted_retired_not_remote_only() -> R {
    use memory::assemble::policy_context;
    use memory::capture::Source;
    use memory::daemon::{Daemon, Parts};
    use memory::realfs::Encoding;
    use memory::resolve::pack;
    use memory_core::domain::{SourceClass, SourceId, SourceKind};
    use memory_core::privacy::{Profile, ResolvedSourceFacts};
    let s = setup("check-retired")?;
    let config = Config::load(&s.rig.cfg).map_err(e)?;
    let roots = locator_roots(&config).map_err(e)?;
    let namespace = roots.first().ok_or("no root")?.1.namespace().to_owned();
    let rel = format!("file-history/{UUID}/aaaaaaaaaaaaaaaa@v1");
    let file = s.rig.root.join(&rel);
    fs::create_dir_all(file.parent().ok_or("parent")?).map_err(e)?;
    fs::write(&file, "fn history() {}\n").map_err(e)?;
    let fs_seam = SharedFs::new();
    let handle = fs_seam
        .cell()
        .borrow_mut()
        .register_encoded(file.clone(), Encoding::Plain);
    let extra = (
        s.owner.clone(),
        Source {
            handle,
            id: SourceId::try_from(format!("{namespace}:retired-fh")).map_err(e)?,
            kind: SourceKind::ClaudeFileHistory,
            class: SourceClass::Snapshot,
            path: rel.clone(),
            facts: ResolvedSourceFacts {
                identities: vec![rel],
                file_identity: Some(pack(&fs::metadata(&file).map_err(e)?)),
                ancestor_identities: vec![],
                path_resolved: true,
                root_resolved: true,
                owner: Some(UUID.to_owned()),
                profile: Profile::Scanned,
            },
        },
    );
    let parts = Parts {
        fs: fs_seam.clone(),
        store: store_at(&s.server.url(), PREFIX, TimeoutPolicy::PRODUCTION)?,
        clock: memory::clock::SystemClock::new(),
        sources: WithExtra {
            inner: WalkSources::new(roots, WalkLimits::default(), fs_seam),
            extra,
        },
        context: policy_context(&s.rig.state, &s.rig.cfg, &config),
    };
    let mut daemon = Daemon::start(&s.rig.state, &s.rig.cfg, parts).map_err(e)?;
    let report = daemon.reconcile_once().map_err(e)?;
    assert_eq!((report.failed, report.landed), (0, 1));
    daemon.shutdown().map_err(e)?;

    let ran = run(&s)?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    assert!(
        ran.has("retired", 1) && ran.has("remote_only", 0),
        "{}",
        ran.out
    );
    assert!(
        ran.out.contains(" remote_only=0 retired=1 parked="),
        "{}",
        ran.out
    );
    assert!(
        ran.has("ignored", 1) && ran.has("uncaptured", 0),
        "{}",
        ran.out
    );
    Ok(())
}
