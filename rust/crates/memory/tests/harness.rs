//! The real `memory` binary: start, stop, refusals. Nothing listens on the configured endpoint
//! and no root is configured, so no round makes a request.
use std::io::Read;
use std::os::unix::fs::{MetadataExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant};

const BOUND: Duration = Duration::from_secs(15);

/// A temp state root with a token file; removed on drop.
struct Setup {
    base: PathBuf,
    root: PathBuf,
}

impl Setup {
    fn new(name: &str, prefix: &str, with_config: bool) -> std::io::Result<Self> {
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
                    "archive_id = \"a\"\nmachine_id = \"m\"\nendpoint = \"http://127.0.0.1:9\"\nbucket = \"bucket-1\"\nprefix = \"{prefix}\"\ntoken_path = \"{}\"\n",
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

fn clean_stop(done: &Done) {
    assert_eq!(done.code, Some(0));
    assert!(done.stdout.is_empty());
    assert!(done.stderr.is_empty());
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
    clean_stop(&done);
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
    clean_stop(&done);
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
    clean_stop(&first.finish(Duration::from_secs(5))?);
    Ok(())
}

#[test]
fn an_empty_prefix_is_refused_with_exit_2() -> std::io::Result<()> {
    let setup = Setup::new("prefix", "", true)?;
    let done = setup.spawn(&["daemon"], None)?.finish(BOUND)?;
    assert_eq!(done.code, Some(2));
    assert_eq!(done.stderr, "memory: config_prefix_required\n");
    assert!(done.stdout.is_empty());
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
    clean_stop(&first.finish(Duration::from_secs(5))?);
    let before = std::fs::metadata(setup.status())?.ino();
    let mut second = setup.spawn(&["daemon"], FAST)?;
    let start = Instant::now();
    while std::fs::metadata(setup.status())?.ino() == before {
        assert!(start.elapsed() < BOUND, "second daemon never ran a round");
        std::thread::sleep(Duration::from_millis(10));
    }
    second.signal("-TERM")?;
    clean_stop(&second.finish(Duration::from_secs(5))?);
    Ok(())
}

#[test]
fn an_unimplemented_command_still_exits_78() -> std::io::Result<()> {
    let setup = Setup::new("status", "rust-acceptance/t/", true)?;
    let done = setup.spawn(&["status"], None)?.finish(BOUND)?;
    assert_eq!(done.code, Some(78));
    assert!(done.stdout.is_empty());
    assert!(done.stderr.is_empty());
    Ok(())
}
