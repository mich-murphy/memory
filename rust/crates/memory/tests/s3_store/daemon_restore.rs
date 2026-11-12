//! R4: `memory daemon` captures a synthetic session, is stopped with SIGTERM, and `memory
//! restore` returns the bytes, all on the real binary against the local server.
//!
//! The daemon's state root (holding `config.toml`) is `<dir>/state`, a sibling of the session
//! root `<dir>/claude`: a root under the state root would be withheld as collector state. The
//! child-process helpers are a small local copy: `harness.rs` is its own test binary, and a
//! shared file would need every item used by both binaries without lint suppression.
use super::assemble::{MARKER, Rig, SESSION, lines};
use super::fake::{Fake, Seen};
use super::{BUCKET, KEY_ID, R, SECRET, e};
use memory::config::Config;
use memory::daemon::Sources;
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory_core::domain::Owner;
use memory_core::limits::{current_key, owner_prefix};
use std::fs;
use std::io::{Read, Write};
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Output, Stdio};
use std::time::{Duration, Instant};

const PREFIX: &str = "rust-acceptance/r4-test/";
const BOUND: Duration = Duration::from_secs(20);

/// Kills the child if a test leaves it running.
struct Guard(Child);
impl Drop for Guard {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

struct Setup {
    server: Fake,
    rig: Rig,
    owner: Owner,
    file: PathBuf,
    current: String,
}

fn setup(label: &str) -> R<Setup> {
    let server = Fake::start()?;
    let rig = Rig::new(label, &server.url(), PREFIX, false)?;
    fs::create_dir_all(&rig.state).map_err(e)?;
    fs::set_permissions(&rig.state, fs::Permissions::from_mode(0o700)).map_err(e)?;
    fs::copy(&rig.cfg, rig.state.join("config.toml")).map_err(e)?;
    let file = rig.root.join(SESSION);
    fs::create_dir_all(file.parent().ok_or("parent")?).map_err(e)?;
    fs::write(&file, lines(0..12)).map_err(e)?;
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
    Ok(Setup {
        server,
        rig,
        owner,
        file,
        current,
    })
}

impl Setup {
    fn etag(&self) -> Option<String> {
        self.server.object(&self.current).map(|(_, tag)| tag)
    }
    fn append(&self, range: std::ops::Range<u32>) -> R {
        fs::OpenOptions::new()
            .append(true)
            .open(&self.file)
            .map_err(e)?
            .write_all(lines(range).as_bytes())
            .map_err(e)
    }
    fn spawn(&self) -> R<Guard> {
        let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
        cmd.arg("daemon")
            .env_clear()
            .env("MEMORY_STATE_DIR", &self.rig.state)
            .env("MEMORY_SIM_TIMING", "0,200")
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
            cmd.env("LLVM_PROFILE_FILE", v);
        }
        Ok(Guard(cmd.spawn().map_err(e)?))
    }
    /// Wait (bounded) until `current.json` exists and differs from `before`.
    fn wait_commit(&self, child: &mut Guard, before: Option<&str>) -> R<String> {
        let start = Instant::now();
        loop {
            if let Some(tag) = self.etag()
                && Some(tag.as_str()) != before
            {
                return Ok(tag);
            }
            if let Some(status) = child.0.try_wait().map_err(e)? {
                return Err(format!("daemon exited early: {status}"));
            }
            if start.elapsed() > BOUND {
                return Err(format!(
                    "no committed round within {BOUND:?}; requests so far: {}",
                    self.server.requests().len()
                ));
            }
            std::thread::sleep(Duration::from_millis(10));
        }
    }
}

/// SIGTERM, then exit 0 within a bound with empty stdout and stderr.
fn stop(mut child: Guard) -> R {
    let status = Command::new("/bin/kill")
        .arg("-TERM")
        .arg(child.0.id().to_string())
        .status()
        .map_err(e)?;
    assert!(status.success());
    let start = Instant::now();
    let status = loop {
        if let Some(status) = child.0.try_wait().map_err(e)? {
            break status;
        }
        if start.elapsed() > Duration::from_secs(10) {
            return Err("daemon did not exit within 10s of SIGTERM".to_owned());
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
    assert_eq!(status.code(), Some(0), "stderr: {err}");
    assert!(out.is_empty(), "stdout: {out}");
    assert!(err.is_empty(), "stderr: {err}");
    Ok(())
}

fn files_under(dir: &Path, out: &mut Vec<PathBuf>) -> R {
    for entry in fs::read_dir(dir).map_err(e)? {
        let path = entry.map_err(e)?.path();
        if path.is_dir() {
            files_under(&path, out)?;
        } else {
            out.push(path);
        }
    }
    Ok(())
}

/// Run `memory restore` into a fresh directory and check the output; returns it.
fn restore(s: &Setup, label: &str) -> R<PathBuf> {
    let out = s.rig.dir.join(format!("out-{label}"));
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear()
        .env("MEMORY_STATE_DIR", &s.rig.state)
        .arg("restore")
        .arg(owner_prefix(&s.owner))
        .arg("--out")
        .arg(&out);
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    let result: Output = cmd.output().map_err(e)?;
    let err = String::from_utf8_lossy(&result.stderr).into_owned();
    assert_eq!(result.status.code(), Some(0), "{err}");
    assert!(result.stdout.is_empty());
    assert_eq!(err.lines().count(), 1, "{err}");
    assert!(
        err.starts_with("memory: restored written=1 skipped_deleted=0 bytes="),
        "{err}"
    );
    let mut found = Vec::new();
    files_under(&out, &mut found)?;
    assert_eq!(found, [out.join(SESSION)]);
    let want = fs::read(&s.file).map_err(e)?;
    assert!(
        fs::read(out.join(SESSION)).map_err(e)? == want,
        "bytes differ"
    );
    assert!(String::from_utf8(want).map_err(e)?.contains(MARKER));
    Ok(out)
}

fn has(haystack: &[u8], needle: &[u8]) -> bool {
    !needle.is_empty() && haystack.windows(needle.len()).any(|w| w == needle)
}

/// Nothing stored carries the session text raw, a credential, the token path or the config.
fn assert_hygiene(s: &Setup, seen: &[Seen]) {
    let first_line = lines(0..1);
    let token = s.rig.token.display().to_string();
    for r in seen.iter().filter(|r| r.method == "PUT") {
        let decoded = zstd::decode_all(&r.body[..]).unwrap_or_default();
        for hay in [r.path.as_bytes(), &r.body[..], &decoded[..]] {
            for needle in [KEY_ID, SECRET, "token_path", "archive_id", token.as_str()] {
                assert!(!has(hay, needle.as_bytes()), "{needle} reached the server");
            }
        }
        assert!(
            !has(&r.body, first_line.as_bytes()),
            "raw session text stored"
        );
        let count = |h: &[u8]| {
            h.windows(MARKER.len())
                .filter(|w| *w == MARKER.as_bytes())
                .count()
        };
        if count(&decoded) > 1 {
            assert!(
                count(&r.body) < count(&decoded),
                "stored bytes look unencoded"
            );
        }
    }
}

#[test]
fn daemon_captures_then_restore_returns_the_bytes() -> R {
    let s = setup("d2r")?;
    // The owner agrees with the key the fake stored current.json under.
    let mut child = s.spawn()?;
    let first = s.wait_commit(&mut child, None)?;
    let after_capture = s.server.requests().len();
    s.append(12..20)?;
    // A later round commits the append: current.json's ETag changes (it only changes by a PUT).
    s.wait_commit(&mut child, Some(&first))?;
    stop(child)?;
    let seen = s.server.requests();
    let after_append = seen.len();
    assert!(after_capture > 0 && after_append > after_capture);
    let owner_text = owner_prefix(&s.owner);
    assert!(
        seen.iter().any(|r| r.method == "PUT"
            && r.path == s.current
            && r.path.contains(&format!("{owner_text}/"))),
        "current.json not stored under the owner's key"
    );

    restore(&s, "a")?;
    let all = s.server.requests();
    let new = all.get(after_append..).ok_or("slice")?;
    assert!(!new.is_empty());
    assert!(new.iter().all(|r| r.method == "GET"), "{new:?}");
    assert_hygiene(&s, &all);
    Ok(())
}

#[test]
fn a_restarted_daemon_continues_and_restore_returns_everything() -> R {
    let s = setup("restart")?;
    let mut one = s.spawn()?;
    let first = s.wait_commit(&mut one, None)?;
    stop(one)?;
    let before = s.server.requests().len();
    s.append(12..20)?;
    let mut two = s.spawn()?;
    s.wait_commit(&mut two, Some(&first))?;
    stop(two)?;
    // Nothing stored by the first daemon was uploaded again: every non-pointer PUT is unique.
    let puts: Vec<String> = s
        .server
        .requests()
        .into_iter()
        .filter(|r| r.method == "PUT" && !r.path.ends_with("current.json"))
        .map(|r| r.path)
        .collect();
    let mut unique = puts.clone();
    unique.sort();
    unique.dedup();
    assert_eq!(
        unique.len(),
        puts.len(),
        "an object was uploaded twice: {puts:?}"
    );
    assert!(s.server.requests().len() > before);
    // Nor were its bytes sent again inside a differently cut object: every object upload,
    // decoded, adds up to the file's length exactly once.
    let uploaded = s
        .server
        .requests()
        .iter()
        .filter(|r| r.method == "PUT" && r.path.contains("/objects/"))
        .map(|r| {
            zstd::decode_all(&r.body[..])
                .map(|raw| raw.len())
                .map_err(e)
        })
        .sum::<R<usize>>()?;
    assert_eq!(
        uploaded,
        fs::read(&s.file).map_err(e)?.len(),
        "session bytes were uploaded more or less than once"
    );

    restore(&s, "b")?;
    assert_hygiene(&s, &s.server.requests());
    Ok(())
}
