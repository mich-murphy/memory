//! The real `memory` binary: start, stop, refusals. Nothing listens on the configured endpoint
//! and no root is configured, so no round makes a request.
use std::io::Read;
use std::os::unix::fs::{MetadataExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant};

#[path = "common/log_lines.rs"]
mod log_lines;

const BOUND: Duration = Duration::from_secs(15);

/// A temp state root with a token file; removed on drop.
struct Setup {
    base: PathBuf,
    root: PathBuf,
}

impl Setup {
    fn new(name: &str, prefix: &str, with_config: bool) -> std::io::Result<Self> {
        Self::build(name, Some(prefix), with_config)
    }

    /// A config with no `prefix` line.
    fn without_prefix(name: &str) -> std::io::Result<Self> {
        Self::build(name, None, true)
    }

    fn build(name: &str, prefix: Option<&str>, with_config: bool) -> std::io::Result<Self> {
        let prefix_line = prefix.map_or_else(String::new, |p| format!("prefix = \"{p}\"\n"));
        let base =
            std::env::temp_dir().join(format!("memory-harness-{}-{name}", std::process::id()));
        let _ = std::fs::remove_dir_all(&base);
        let root = base.join("state");
        std::fs::create_dir_all(&root)?;
        std::fs::set_permissions(&base, std::fs::Permissions::from_mode(0o700))?;
        std::fs::set_permissions(&root, std::fs::Permissions::from_mode(0o700))?;
        let token = base.join("token");
        std::fs::write(&token, "access_key_id = \"k\"\nsecret_access_key = \"s\"\n")?;
        std::fs::set_permissions(&token, std::fs::Permissions::from_mode(0o600))?;
        if with_config {
            std::fs::write(
                root.join("config.toml"),
                format!(
                    "archive_id = \"a\"\nmachine_id = \"m\"\nendpoint = \"http://127.0.0.1:9\"\nbucket = \"bucket-1\"\n{prefix_line}token_path = \"{}\"\n",
                    token.display()
                ),
            )?;
        }
        Ok(Self { base, root })
    }

    fn status(&self) -> PathBuf {
        self.root.join("status.json")
    }

    fn spawn(&self, args: &[&str], timing: Option<&str>) -> std::io::Result<Guard> {
        let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
        cmd.args(args)
            .env_clear()
            .env("MEMORY_STATE_DIR", &self.root)
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        if let Some(timing) = timing {
            cmd.env("MEMORY_SIM_TIMING", timing);
        }
        if let Some(profile) = std::env::var_os("LLVM_PROFILE_FILE") {
            cmd.env("LLVM_PROFILE_FILE", profile);
        }
        Ok(Guard(cmd.spawn()?))
    }

    /// Like `spawn`, but the read end of the child's stderr pipe is closed at once.
    fn spawn_stderr_closed(&self, args: &[&str], timing: Option<&str>) -> std::io::Result<Guard> {
        let mut guard = self.spawn(args, timing)?;
        drop(guard.0.stderr.take());
        Ok(guard)
    }
}

impl Drop for Setup {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.base);
    }
}

/// Kills the child if a test leaves it running.
struct Guard(Child);

impl Drop for Guard {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

struct Done {
    code: Option<i32>,
    stdout: String,
    stderr: String,
}

impl Guard {
    fn signal(&self, name: &str) -> std::io::Result<()> {
        let status = Command::new("/bin/kill")
            .arg(name)
            .arg(self.0.id().to_string())
            .status()?;
        assert!(status.success());
        Ok(())
    }

    fn running(&mut self) -> std::io::Result<bool> {
        Ok(self.0.try_wait()?.is_none())
    }

    fn finish(&mut self, within: Duration) -> std::io::Result<Done> {
        let start = Instant::now();
        let status = loop {
            if let Some(status) = self.0.try_wait()? {
                break status;
            }
            assert!(start.elapsed() < within, "child did not exit in time");
            std::thread::sleep(Duration::from_millis(10));
        };
        let mut stdout = String::new();
        let mut stderr = String::new();
        if let Some(pipe) = self.0.stdout.as_mut() {
            pipe.read_to_string(&mut stdout)?;
        }
        if let Some(pipe) = self.0.stderr.as_mut() {
            pipe.read_to_string(&mut stderr)?;
        }
        Ok(Done {
            code: status.code(),
            stdout,
            stderr,
        })
    }
}

fn wait_for(path: &Path) {
    let start = Instant::now();
    while !path.exists() {
        assert!(start.elapsed() < BOUND, "file never appeared");
        std::thread::sleep(Duration::from_millis(10));
    }
}

fn clean_stop(done: &Done, setup: &Setup, min_rounds: usize) {
    assert_eq!(done.code, Some(0));
    assert!(done.stdout.is_empty());
    let base = setup.base.display().to_string();
    let root = setup.root.display().to_string();
    log_lines::assert_clean_log(
        &done.stderr,
        min_rounds,
        &[
            &base,
            &root,
            "bucket-1",
            "127.0.0.1",
            "rust-acceptance",
            "token",
        ],
    );
}

const FAST: Option<&str> = Some("0,200");

#[test]
fn sigterm_interrupts_the_production_startup_delay() -> std::io::Result<()> {
    let setup = Setup::new("prod", "rust-acceptance/t/", true)?;
    let mut daemon = setup.spawn(&["daemon"], None)?;
    wait_for(&setup.root.join("daemon.lock"));
    let sent = Instant::now();
    daemon.signal("-TERM")?;
    let done = daemon.finish(Duration::from_secs(2))?;
    assert!(sent.elapsed() < Duration::from_secs(2));
    clean_stop(&done, &setup, 0);
    assert!(!setup.status().exists(), "no round ran during the delay");
    Ok(())
}

fn round_then_stop(name: &str, signal: &str) -> std::io::Result<()> {
    let setup = Setup::new(name, "rust-acceptance/t/", true)?;
    let mut daemon = setup.spawn(&["daemon"], FAST)?;
    wait_for(&setup.status());
    let before = std::fs::metadata(setup.status())?.ino();
    daemon.signal(signal)?;
    let done = daemon.finish(Duration::from_secs(5))?;
    clean_stop(&done, &setup, 1);
    // Shutdown force-flushes the status: a new file replaces the one the round wrote.
    assert_ne!(std::fs::metadata(setup.status())?.ino(), before);
    Ok(())
}

#[test]
fn sigterm_after_a_round_exits_zero_and_flushes_status() -> std::io::Result<()> {
    round_then_stop("term", "-TERM")
}

#[test]
fn sigint_after_a_round_exits_zero_and_flushes_status() -> std::io::Result<()> {
    round_then_stop("int", "-INT")
}

#[test]
fn a_second_daemon_on_the_same_root_exits_75_and_the_first_is_unaffected() -> std::io::Result<()> {
    let setup = Setup::new("lock", "rust-acceptance/t/", true)?;
    let mut first = setup.spawn(&["daemon"], FAST)?;
    wait_for(&setup.status());
    let mut second = setup.spawn(&["daemon"], FAST)?;
    let done = second.finish(Duration::from_secs(5))?;
    assert_eq!(done.code, Some(75));
    assert_eq!(done.stderr, "memory: lock_held\n");
    assert!(done.stdout.is_empty());
    assert!(first.running()?);
    first.signal("-TERM")?;
    clean_stop(&first.finish(Duration::from_secs(5))?, &setup, 1);
    Ok(())
}

#[test]
fn a_missing_prefix_is_refused_with_exit_2_by_every_command() -> std::io::Result<()> {
    let setup = Setup::without_prefix("prefix")?;
    let out = setup.base.join("out");
    let out = out.to_string_lossy().into_owned();
    let owner = "sessions/a/m/claude/s1";
    let commands: [&[&str]; 3] = [&["daemon"], &["export", owner, "--out", &out], &["owners"]];
    for args in commands {
        let done = setup.spawn(args, None)?.finish(BOUND)?;
        assert_eq!(done.code, Some(2), "{args:?}");
        assert_eq!(done.stderr, "memory: config_prefix_missing\n", "{args:?}");
        assert!(done.stdout.is_empty(), "{args:?}");
    }
    Ok(())
}

#[test]
fn a_missing_config_is_refused_with_exit_2() -> std::io::Result<()> {
    let setup = Setup::new("noconfig", "", false)?;
    let done = setup.spawn(&["daemon"], None)?.finish(BOUND)?;
    assert_eq!(done.code, Some(2));
    assert_eq!(done.stderr, "memory: config_read\n");
    assert!(done.stdout.is_empty());
    Ok(())
}

#[test]
fn a_malformed_sim_timing_is_a_config_refusal() -> std::io::Result<()> {
    let setup = Setup::new("badtiming", "rust-acceptance/t/", true)?;
    let done = setup.spawn(&["daemon"], Some("soon"))?.finish(BOUND)?;
    assert_eq!(done.code, Some(2));
    assert_eq!(done.stderr, "memory: config_sim_timing\n");
    Ok(())
}

#[test]
fn a_new_daemon_starts_after_a_clean_stop() -> std::io::Result<()> {
    let setup = Setup::new("restart", "rust-acceptance/t/", true)?;
    let mut first = setup.spawn(&["daemon"], FAST)?;
    wait_for(&setup.status());
    first.signal("-TERM")?;
    clean_stop(&first.finish(Duration::from_secs(5))?, &setup, 1);
    let before = std::fs::metadata(setup.status())?.ino();
    let mut second = setup.spawn(&["daemon"], FAST)?;
    let start = Instant::now();
    while std::fs::metadata(setup.status())?.ino() == before {
        assert!(start.elapsed() < BOUND, "second daemon never ran a round");
        std::thread::sleep(Duration::from_millis(10));
    }
    second.signal("-TERM")?;
    clean_stop(&second.finish(Duration::from_secs(5))?, &setup, 1);
    Ok(())
}

#[test]
fn an_unimplemented_command_still_exits_78() -> std::io::Result<()> {
    let setup = Setup::new("unknown", "rust-acceptance/t/", true)?;
    let done = setup.spawn(&["frobnicate"], None)?.finish(BOUND)?;
    assert_eq!(done.code, Some(78));
    assert!(done.stdout.is_empty());
    assert!(done.stderr.is_empty());
    Ok(())
}

#[test]
fn a_closed_stderr_does_not_stop_the_daemon() -> std::io::Result<()> {
    let setup = Setup::new("closed", "rust-acceptance/t/", true)?;
    // The startup delay outlasts the parent closing the pipe, so the round line is written to a
    // pipe with no reader.
    let mut daemon = setup.spawn_stderr_closed(&["daemon"], Some("300,200"))?;
    wait_for(&setup.status());
    let before = std::fs::metadata(setup.status())?.ino();
    daemon.signal("-TERM")?;
    let done = daemon.finish(Duration::from_secs(5))?;
    assert_eq!(done.code, Some(0));
    assert!(done.stdout.is_empty());
    assert_ne!(std::fs::metadata(setup.status())?.ino(), before);
    Ok(())
}

#[test]
fn a_refusal_with_a_closed_stderr_still_exits_2() -> std::io::Result<()> {
    let setup = Setup::without_prefix("prefixclosed")?;
    let done = setup
        .spawn_stderr_closed(&["daemon"], None)?
        .finish(BOUND)?;
    assert_eq!(done.code, Some(2));
    Ok(())
}

/// The ino of `path`, or `None` when it does not exist.
fn ino_of(path: &Path) -> Option<u64> {
    std::fs::metadata(path).ok().map(|m| m.ino())
}

/// Start a daemon, wait until a round replaced the status file, stop it; the log lines without
/// their timestamps.
fn run_once(setup: &Setup) -> std::io::Result<Vec<String>> {
    let before = ino_of(&setup.status());
    let mut daemon = setup.spawn(&["daemon"], FAST)?;
    let start = Instant::now();
    while ino_of(&setup.status()) == before {
        assert!(start.elapsed() < BOUND, "no round ran");
        std::thread::sleep(Duration::from_millis(10));
    }
    daemon.signal("-TERM")?;
    let done = daemon.finish(Duration::from_secs(5))?;
    clean_stop(&done, setup, 1);
    Ok(done
        .stderr
        .lines()
        .map(|l| {
            l.split_once(' ')
                .map(|(_, r)| r.to_owned())
                .unwrap_or_default()
        })
        .collect())
}

fn state_loads(lines: &[String]) -> Vec<&str> {
    lines
        .iter()
        .filter(|l| l.starts_with("INFO state_load"))
        .map(String::as_str)
        .collect()
}

fn assert_started_then(lines: &[String], loads: &[&str]) {
    assert!(
        lines
            .first()
            .is_some_and(|l| l.starts_with("INFO started ")),
        "{lines:?}"
    );
    let got: Vec<&str> = lines
        .iter()
        .skip(1)
        .take(loads.len())
        .map(String::as_str)
        .collect();
    assert_eq!(got, loads, "{lines:?}");
    assert_eq!(state_loads(lines), loads, "{lines:?}");
}

/// Start over `damage(setup)`, check the loads, then check the second start's loads.
fn damaged_start(
    name: &str,
    damage: impl FnOnce(&Setup) -> std::io::Result<()>,
    loads: &[&str],
    second: &[&str],
) -> std::io::Result<()> {
    let setup = Setup::new(name, "rust-acceptance/t/", true)?;
    damage(&setup)?;
    assert_started_then(&run_once(&setup)?, loads);
    assert_started_then(&run_once(&setup)?, second);
    Ok(())
}

#[test]
fn a_damaged_status_is_logged_once() -> std::io::Result<()> {
    damaged_start(
        "dmgstatus",
        |s| std::fs::write(s.status(), b"garbage"),
        &["INFO state_load file=status result=damaged"],
        &[],
    )
}

#[test]
fn a_damaged_index_is_logged_once() -> std::io::Result<()> {
    damaged_start(
        "dmgindex",
        |s| std::fs::write(s.root.join("index.json"), b"garbage"),
        &["INFO state_load file=index result=damaged"],
        // A daemon with no sources never dirties the index, so it is not rewritten and the
        // next start finds it damaged again. The replacement status is always written.
        &["INFO state_load file=index result=damaged"],
    )
}

#[test]
fn both_damaged_log_status_before_index() -> std::io::Result<()> {
    damaged_start(
        "dmgboth",
        |s| {
            std::fs::write(s.status(), b"garbage")?;
            std::fs::write(s.root.join("index.json"), b"garbage")
        },
        &[
            "INFO state_load file=status result=damaged",
            "INFO state_load file=index result=damaged",
        ],
        &["INFO state_load file=index result=damaged"],
    )
}

#[test]
fn a_clean_first_start_logs_no_state_load() -> std::io::Result<()> {
    let setup = Setup::new("firststart", "rust-acceptance/t/", true)?;
    assert_started_then(&run_once(&setup)?, &[]);
    Ok(())
}

/// Replace the schema name in the first line of a state file written by a first run.
fn newer_schema(setup: &Setup, file: &str, from: &str, to: &str) -> std::io::Result<()> {
    let path = setup.root.join(file);
    let bytes = std::fs::read(&path)?;
    let text = String::from_utf8_lossy(&bytes).into_owned();
    assert!(text.contains(from), "header lacks {from}");
    std::fs::write(&path, text.replacen(from, to, 1))
}

#[test]
fn a_newer_schema_is_logged_once_per_file() -> std::io::Result<()> {
    let setup = Setup::new("newer", "rust-acceptance/t/", true)?;
    assert_started_then(&run_once(&setup)?, &[]);
    // A daemon with no sources never writes the index, so write an empty one with the state API.
    {
        let lock = memory::state::DaemonLock::acquire(&setup.root)
            .map_err(|e| std::io::Error::other(format!("{e:?}")))?;
        let state = memory::state::StateDir::prepare(&lock)
            .map_err(|e| std::io::Error::other(format!("{e:?}")))?;
        state
            .write(
                memory::state::StateFile::Index,
                br#"{"sources":{},"hints":{}}"#,
            )
            .map_err(|e| std::io::Error::other(format!("{e:?}")))?;
    }
    newer_schema(&setup, "status.json", "memory.status/1", "memory.status/2")?;
    newer_schema(&setup, "index.json", "memory.index/1", "memory.index/2")?;
    assert_started_then(
        &run_once(&setup)?,
        &[
            "INFO state_load file=status result=newer_schema",
            "INFO state_load file=index result=newer_schema",
        ],
    );
    // The status is replaced by the round; the index is not rewritten without sources.
    assert_started_then(
        &run_once(&setup)?,
        &["INFO state_load file=index result=newer_schema"],
    );
    Ok(())
}
