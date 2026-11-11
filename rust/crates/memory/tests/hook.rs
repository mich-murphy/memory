//! `memory hook` (DESIGN.md 9.1, 10.1): golden lines, concurrency, refusal paths, privacy
//! canaries, properties over arbitrary stdin, and the synthetic P4 measurement (ignored).
use hegel::{TestCase, generators as gs};
use memory::hook::{self, HookCall, HookResult};
use memory_core::hint::{self, LaunchHint, MAX_HOOK_INPUT_BYTES, MAX_LINE_BYTES};
use proptest::prelude::*;
use std::fmt::Debug;
use std::fs;
use std::io::{Cursor, Read, Write};
use std::os::unix::fs::{MetadataExt, PermissionsExt, symlink};
use std::path::{Path, PathBuf};
use std::process::{Command, Output, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{Duration, Instant};

type R = Result<(), String>;
const DAY: &str = "2026-11-08";
const BIN: &str = env!("CARGO_BIN_EXE_memory");

/// Coverage runs (cargo llvm-cov) need the profile path in spawned binaries; every other
/// variable is still cleared, so test behaviour is unchanged.
fn profile_env() -> Option<(&'static str, std::ffi::OsString)> {
    std::env::var_os("LLVM_PROFILE_FILE").map(|v| ("LLVM_PROFILE_FILE", v))
}

fn e<E: Debug>(error: E) -> String {
    format!("{error:?}")
}

struct Tmp(PathBuf);
impl Tmp {
    fn new(label: &str) -> Result<Self, String> {
        static N: AtomicU64 = AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!(
            "memory-hook-{}-{}-{label}",
            std::process::id(),
            N.fetch_add(1, Ordering::SeqCst)
        ));
        fs::create_dir_all(&path).map_err(e)?;
        fs::set_permissions(&path, fs::Permissions::from_mode(0o700)).map_err(e)?;
        Ok(Self(path))
    }
    fn p(&self) -> &Path {
        &self.0
    }
}
impl Drop for Tmp {
    fn drop(&mut self) {
        for dir in [self.0.clone(), self.0.join("hints")] {
            let _ = fs::set_permissions(dir, fs::Permissions::from_mode(0o700));
        }
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn payload(event: &str, session: &str) -> String {
    format!(
        r#"{{"hook_event_name":"{event}","session_id":"{session}","cwd":"/w","transcript_path":"/w/t.jsonl","prompt":"CANARYPROMPT","tool_input":{{"command":"CANARYTOOL"}}}}"#
    )
}

fn call<'a>(agent: &'a str, stdin: &'a [u8], dir: &'a Path) -> HookCall<'a> {
    HookCall {
        agent,
        stdin,
        argv: &[],
        env: &[],
        hints_dir: dir,
        today: DAY,
    }
}

fn spawn(
    args: &[&str],
    state: &Path,
    env: &[(&str, &str)],
    stdin: &[u8],
) -> Result<Output, String> {
    let mut child = Command::new(BIN)
        .args(args)
        .env_clear()
        .envs(profile_env())
        .env("MEMORY_STATE_DIR", state)
        .envs(env.iter().copied())
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(e)?;
    if let Some(mut pipe) = child.stdin.take() {
        let _ = pipe.write_all(stdin); // the child may exit before reading everything
    }
    child.wait_with_output().map_err(e)
}

fn quiet_success(out: &Output) -> R {
    if !out.status.success() {
        return Err(format!("status {:?}", out.status));
    }
    if !out.stdout.is_empty() {
        return Err("stdout not empty".into());
    }
    Ok(())
}

fn all_hint_bytes(hints: &Path) -> Vec<u8> {
    let mut names: Vec<PathBuf> = fs::read_dir(hints)
        .map(|rd| rd.filter_map(|x| x.ok().map(|x| x.path())).collect())
        .unwrap_or_default();
    names.sort();
    names
        .iter()
        .flat_map(|p| fs::read(p).unwrap_or_default())
        .collect()
}

fn contains(hay: &[u8], needle: &[u8]) -> bool {
    hay.windows(needle.len()).any(|w| w == needle)
}

fn golden(agent: &str, event: &str, session: &str) -> String {
    format!(
        r#"{{"schema":"memory.hint/1","agent":"{agent}","event":"{event}","session":"{session}","cwd":{{"state":"present","path":"/w"}},"transcript":{{"state":"present","path":"/w/t.jsonl"}},"agent_transcript":{{"state":"absent"}},"launch":{{"state":"none"}},"reduction":"none"}}
"#
    )
}

// ------------------------------------------------------------------ core, golden

#[test]
fn golden_line_per_agent() -> R {
    let cases = [
        ("claude", payload("Stop", "abc"), "turn_end"),
        ("codex", payload("Stop", "abc"), "turn_end"),
        (
            "pi",
            r#"{"event":"agent_settled","session_id":"abc","cwd":"/w","session_file":"/w/t.jsonl"}"#.to_owned(),
            "turn_end",
        ),
    ];
    for (agent, input, event) in cases {
        let tmp = Tmp::new(agent)?;
        let dir = tmp.p().join("hints");
        let got = hook::run(&call(agent, input.as_bytes(), &dir));
        assert_eq!(got, HookResult::Appended, "{agent}");
        let bytes = fs::read(dir.join(format!("{DAY}.jsonl"))).map_err(e)?;
        assert_eq!(
            String::from_utf8(bytes).map_err(e)?,
            golden(agent, event, "abc")
        );
    }
    Ok(())
}

#[test]
fn launch_evidence_only_on_session_start() -> R {
    let argv: Vec<String> = ["claude", "--model", "m2", "CANARYARGV positional"]
        .map(str::to_owned)
        .to_vec();
    let env: Vec<String> = [
        "ANTHROPIC_MODEL=m1",
        "SECRET_TOKEN=CANARYENV",
        "ANTHROPIC_API_KEY=CANARYKEY",
    ]
    .map(str::to_owned)
    .to_vec();
    let tmp = Tmp::new("launch")?;
    let dir = tmp.p().join("hints");
    for (event, session) in [("SessionStart", "start"), ("Stop", "stop")] {
        let input = payload(event, session);
        let c = HookCall {
            argv: &argv,
            env: &env,
            ..call("claude", input.as_bytes(), &dir)
        };
        assert_eq!(hook::run(&c), HookResult::Appended);
    }
    let bytes = all_hint_bytes(&dir);
    assert!(!contains(&bytes, b"CANARY"));
    let scan = hint::scan(&bytes, 0).map_err(e)?;
    assert_eq!(scan.records.len(), 2);
    let launches: Vec<&LaunchHint> = scan.records.iter().map(|r| &r.record.launch).collect();
    match launches.as_slice() {
        [LaunchHint::Full(first), LaunchHint::None] => {
            assert_eq!(
                first.env().get("ANTHROPIC_MODEL").map(String::as_str),
                Some("m1")
            );
            assert_eq!(first.env().len(), 1);
            assert!(first.argv().iter().any(|a| a == "m2"));
        }
        other => return Err(format!("unexpected launches {other:?}")),
    }
    // A SessionStart without any launch evidence records none.
    let tmp = Tmp::new("nolaunch")?;
    let dir = tmp.p().join("hints");
    let input = payload("SessionStart", "x");
    assert_eq!(
        hook::run(&call("claude", input.as_bytes(), &dir)),
        HookResult::Appended
    );
    let scan = hint::scan(&all_hint_bytes(&dir), 0).map_err(e)?;
    assert!(matches!(
        scan.records.first().map(|r| &r.record.launch),
        Some(LaunchHint::None)
    ));
    Ok(())
}

#[test]
fn oversize_launch_is_reduced_within_bound() -> R {
    let mut argv = vec!["claude".to_owned()];
    for _ in 0..15 {
        argv.push("--model".to_owned());
        argv.push("a".repeat(250));
    }
    let tmp = Tmp::new("big")?;
    let dir = tmp.p().join("hints");
    let input = payload("SessionStart", "big");
    let c = HookCall {
        argv: &argv,
        ..call("claude", input.as_bytes(), &dir)
    };
    assert_eq!(hook::run(&c), HookResult::Appended);
    let bytes = all_hint_bytes(&dir);
    assert!(bytes.len() <= MAX_LINE_BYTES);
    assert_eq!(hint::scan(&bytes, 0).map_err(e)?.records.len(), 1);
    Ok(())
}

// ------------------------------------------------------------------ core, refusals

#[test]
fn core_rejects_bad_agent_date_and_input() -> R {
    let tmp = Tmp::new("bad")?;
    let dir = tmp.p().join("hints");
    let ok = payload("Stop", "abc");
    assert_eq!(
        hook::run(&call("gemini", ok.as_bytes(), &dir)),
        HookResult::UnknownAgent
    );
    for today in ["../../etc/x", "2026-1-08", "2026/11/08", "", "2026-11-0a"] {
        let c = HookCall {
            today,
            ..call("claude", ok.as_bytes(), &dir)
        };
        assert_eq!(hook::run(&c), HookResult::InvalidDate, "{today}");
    }
    for input in [
        &b""[..],
        b"garbage",
        b"[1]",
        b"{}",
        br#"{"hook_event_name":"Nope","session_id":"a"}"#,
    ] {
        assert_eq!(
            hook::run(&call("claude", input, &dir)),
            HookResult::BadInput
        );
    }
    assert!(!dir.exists(), "refusals must not create the directory");
    Ok(())
}

#[test]
fn core_refuses_group_or_world_writable_dir() -> R {
    for mode in [0o770, 0o707, 0o777, 0o720] {
        let tmp = Tmp::new("wr")?;
        let dir = tmp.p().join("hints");
        fs::create_dir(&dir).map_err(e)?;
        fs::set_permissions(&dir, fs::Permissions::from_mode(mode)).map_err(e)?;
        let ok = payload("Stop", "abc");
        assert_eq!(
            hook::run(&call("claude", ok.as_bytes(), &dir)),
            HookResult::DirRefused,
            "{mode:o}"
        );
        assert!(fs::read_dir(&dir).map_err(e)?.next().is_none(), "{mode:o}");
    }
    Ok(())
}

#[test]
fn core_refuses_non_directory_and_symlink() -> R {
    let tmp = Tmp::new("nd")?;
    let ok = payload("Stop", "abc");
    let file = tmp.p().join("file");
    fs::write(&file, b"x").map_err(e)?;
    assert_eq!(
        hook::run(&call("claude", ok.as_bytes(), &file)),
        HookResult::DirRefused
    );
    let real = tmp.p().join("real");
    fs::create_dir(&real).map_err(e)?;
    let link = tmp.p().join("link");
    symlink(&real, &link).map_err(e)?;
    assert_eq!(
        hook::run(&call("claude", ok.as_bytes(), &link)),
        HookResult::DirRefused
    );
    assert!(fs::read_dir(&real).map_err(e)?.next().is_none());
    // Parent is a regular file: the directory cannot be created.
    let under = file.join("hints");
    assert_eq!(
        hook::run(&call("claude", ok.as_bytes(), &under)),
        HookResult::DirUnavailable
    );
    Ok(())
}

#[test]
fn core_unreadable_parent_and_readonly_dir() -> R {
    let ok = payload("Stop", "abc");
    let tmp = Tmp::new("eacces")?;
    let parent = tmp.p().join("locked");
    fs::create_dir(&parent).map_err(e)?;
    fs::set_permissions(&parent, fs::Permissions::from_mode(0o000)).map_err(e)?;
    let got = hook::run(&call("claude", ok.as_bytes(), &parent.join("hints")));
    fs::set_permissions(&parent, fs::Permissions::from_mode(0o700)).map_err(e)?;
    assert_eq!(got, HookResult::DirUnavailable);

    let dir = tmp.p().join("ro");
    fs::create_dir(&dir).map_err(e)?;
    fs::set_permissions(&dir, fs::Permissions::from_mode(0o500)).map_err(e)?;
    let got = hook::run(&call("claude", ok.as_bytes(), &dir));
    fs::set_permissions(&dir, fs::Permissions::from_mode(0o700)).map_err(e)?;
    assert_eq!(got, HookResult::OpenFailed);
    assert!(fs::read_dir(&dir).map_err(e)?.next().is_none());
    // A directory squatting on the day file name cannot be opened for append.
    fs::create_dir(dir.join(format!("{DAY}.jsonl"))).map_err(e)?;
    assert_eq!(
        hook::run(&call("claude", ok.as_bytes(), &dir)),
        HookResult::OpenFailed
    );
    Ok(())
}

#[test]
fn core_creates_missing_dirs_0700_and_file_0600() -> R {
    let tmp = Tmp::new("modes")?;
    let dir = tmp.p().join("a/b/hints");
    let ok = payload("Stop", "abc");
    assert_eq!(
        hook::run(&call("claude", ok.as_bytes(), &dir)),
        HookResult::Appended
    );
    let mode = |p: &Path| fs::metadata(p).map(|m| m.mode() & 0o777).map_err(e);
    assert_eq!(mode(&dir)?, 0o700);
    assert_eq!(mode(&tmp.p().join("a/b"))?, 0o700);
    assert_eq!(mode(&dir.join(format!("{DAY}.jsonl")))?, 0o600);
    Ok(())
}

// ------------------------------------------------------------------ helpers

#[test]
fn utc_day_known_values() {
    for (secs, day) in [
        (0, "1970-01-01"),
        (86_399, "1970-01-01"),
        (86_400, "1970-01-02"),
        (951_782_400, "2000-02-29"),
        (1_700_000_000, "2023-11-14"),
        (1_794_182_400, "2026-11-09"),
        (4_102_444_800, "2100-01-01"),
        (4_107_456_000, "2100-02-28"),
        (4_107_542_400, "2100-03-01"),
    ] {
        assert_eq!(hook::utc_day(secs), day, "{secs}");
        assert!(hook::is_day_name(day));
    }
    assert!(!hook::is_day_name("2026-11-0"));
    assert!(!hook::is_day_name("2026_11_08"));
}

#[test]
fn read_bounded_stops_at_end_of_value() {
    let tail = b"{\"a\":1} trailing junk";
    assert_eq!(hook::read_bounded(Cursor::new(&tail[..])), b"{\"a\":1}");
    let nested = br#"  {"a":{"b":[1,{"c":"}\"]"}]},"d":"\\"} xx"#;
    assert_eq!(
        hook::read_bounded(Cursor::new(&nested[..])),
        nested.get(..nested.len() - 3).unwrap_or_default()
    );
    assert_eq!(hook::read_bounded(Cursor::new(&b"[1]"[..])), b"[");
    assert_eq!(hook::read_bounded(Cursor::new(&b"{\"a\":"[..])), b"{\"a\":");
    assert!(hook::read_bounded(Cursor::new(&b""[..])).is_empty());
}

#[test]
fn read_bounded_caps_at_limit_plus_one_and_survives_errors() {
    let mut big = b"{\"a\":\"".to_vec();
    big.resize(MAX_HOOK_INPUT_BYTES * 2, b'x');
    let got = hook::read_bounded(Cursor::new(big));
    assert_eq!(got.len(), MAX_HOOK_INPUT_BYTES + 1);
    struct Broken;
    impl Read for Broken {
        fn read(&mut self, _: &mut [u8]) -> std::io::Result<usize> {
            Err(std::io::Error::other("x"))
        }
    }
    assert!(hook::read_bounded(Broken).is_empty());
}

// ------------------------------------------------------------------ binary

#[test]
fn binary_appends_for_each_agent_with_modes() -> R {
    let tmp = Tmp::new("bin")?;
    for agent in ["claude", "codex", "pi"] {
        let input = if agent == "pi" {
            r#"{"event":"session_shutdown","session_id":"p1"}"#.to_owned()
        } else {
            payload("SessionEnd", agent)
        };
        let out = spawn(&["hook", agent], tmp.p(), &[], input.as_bytes())?;
        quiet_success(&out)?;
        assert!(out.stderr.is_empty());
    }
    let hints = tmp.p().join("hints");
    assert_eq!(fs::metadata(&hints).map_err(e)?.mode() & 0o777, 0o700);
    let files: Vec<_> = fs::read_dir(&hints)
        .map_err(e)?
        .filter_map(Result::ok)
        .collect();
    assert_eq!(files.len(), 1);
    for f in &files {
        assert_eq!(f.metadata().map_err(e)?.mode() & 0o777, 0o600);
    }
    let scan = hint::scan(&all_hint_bytes(&hints), 0).map_err(e)?;
    assert_eq!((scan.records.len(), scan.dropped), (3, 0));
    Ok(())
}

#[test]
fn concurrent_hooks_append_whole_lines() -> R {
    let tmp = Tmp::new("conc")?;
    let n = 24;
    std::thread::scope(|scope| -> R {
        let handles: Vec<_> = (0..n)
            .map(|i| {
                let state = tmp.p();
                scope.spawn(move || {
                    let input = payload("Stop", &format!("s{i}"));
                    spawn(&["hook", "claude"], state, &[], input.as_bytes())
                        .and_then(|o| quiet_success(&o))
                })
            })
            .collect();
        for handle in handles {
            handle.join().map_err(|_| "thread panicked".to_owned())??;
        }
        Ok(())
    })?;
    let bytes = all_hint_bytes(&tmp.p().join("hints"));
    let scan = hint::scan(&bytes, 0).map_err(e)?;
    assert_eq!(
        (scan.records.len(), scan.dropped, scan.pending_bytes),
        (n, 0, 0)
    );
    for i in 0..n {
        assert!(
            contains(&bytes, format!(r#""session":"s{i}""#).as_bytes()),
            "s{i}"
        );
    }
    Ok(())
}

#[test]
fn binary_refuses_writable_dir_without_file() -> R {
    for mode in [0o770, 0o777, 0o707] {
        let tmp = Tmp::new("refuse")?;
        let hints = tmp.p().join("hints");
        fs::create_dir(&hints).map_err(e)?;
        fs::set_permissions(&hints, fs::Permissions::from_mode(mode)).map_err(e)?;
        let out = spawn(
            &["hook", "claude"],
            tmp.p(),
            &[],
            payload("Stop", "a").as_bytes(),
        )?;
        quiet_success(&out)?;
        assert!(
            fs::read_dir(&hints).map_err(e)?.next().is_none(),
            "{mode:o}"
        );
    }
    Ok(())
}

#[test]
fn binary_failure_paths_exit_zero_silently() -> R {
    let tmp = Tmp::new("fail")?;
    let ok = payload("Stop", "a");
    let big = {
        let mut v = b"{\"a\":\"".to_vec();
        v.resize(MAX_HOOK_INPUT_BYTES + 4096, b'x');
        v
    };
    let blocker = tmp.p().join("blocker");
    fs::write(&blocker, b"x").map_err(e)?;
    let ro = tmp.p().join("ro");
    fs::create_dir_all(ro.join("hints")).map_err(e)?;
    fs::set_permissions(ro.join("hints"), fs::Permissions::from_mode(0o500)).map_err(e)?;
    let cases: Vec<(Vec<&str>, &Path, Vec<u8>)> = vec![
        (vec!["hook", "gemini"], tmp.p(), ok.clone().into_bytes()),
        (vec!["hook"], tmp.p(), ok.clone().into_bytes()),
        (
            vec!["hook", "claude", "extra"],
            &blocker,
            ok.clone().into_bytes(),
        ),
        (vec!["hook", "claude"], &blocker, ok.clone().into_bytes()),
        (vec!["hook", "claude"], &ro, ok.clone().into_bytes()),
        (vec!["hook", "claude"], tmp.p(), b"garbage\xff\x00".to_vec()),
        (vec!["hook", "claude"], tmp.p(), big),
        (vec!["hook", "claude"], tmp.p(), Vec::new()),
    ];
    let before = all_hint_bytes(&tmp.p().join("hints"));
    for (args, state, input) in &cases {
        let out = spawn(args, state, &[], input)?;
        quiet_success(&out).map_err(|m| format!("{args:?}: {m}"))?;
        assert!(out.stderr.len() < 64, "{args:?}");
    }
    assert_eq!(all_hint_bytes(&tmp.p().join("hints")), before);
    assert!(fs::read_dir(ro.join("hints")).map_err(e)?.next().is_none());
    Ok(())
}

#[test]
fn missing_state_dir_environment_exits_zero() -> R {
    let out = Command::new(BIN)
        .args(["hook", "claude"])
        .env_clear()
        .envs(profile_env())
        .stdin(Stdio::null())
        .output()
        .map_err(e)?;
    quiet_success(&out)
}

#[test]
fn other_subcommands_keep_explicit_failure() -> R {
    let tmp = Tmp::new("other")?;
    for args in [&[][..], &["sync"], &["restore", "x"], &["hooks"]] {
        let out = spawn(args, tmp.p(), &[], b"")?;
        assert_eq!(out.status.code(), Some(78), "{args:?}");
        assert!(out.stdout.is_empty());
    }
    assert!(!tmp.p().join("hints").exists());
    Ok(())
}

/// A reader that yields `first` once, then blocks until its sender is dropped.
struct Stalls {
    first: Cursor<Vec<u8>>,
    release: std::sync::mpsc::Receiver<()>,
}

impl std::io::Read for Stalls {
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        let n = self.first.read(buf)?;
        if n == 0 {
            let _ = self.release.recv();
        }
        Ok(n)
    }
}

/// No wall-clock bound: the injected deadline is 1 ms, and the assertion is that the call
/// returns at all, empty, while the reader is still blocked. Kills: removing the timeout
/// (`recv` instead of `recv_timeout`, which would hang) and returning the partial bytes.
#[test]
fn silent_or_partial_reader_is_abandoned_at_the_deadline() {
    for first in [Vec::new(), b"{\"hook_event_name\":\"Stop\",".to_vec()] {
        let (hold, release) = std::sync::mpsc::channel();
        let got = hook::read_with_deadline(
            Stalls {
                first: Cursor::new(first),
                release,
            },
            Duration::from_millis(1),
        );
        assert!(got.is_empty());
        drop(hold);
    }
}

/// A complete object returns without waiting for EOF or the deadline: the injected deadline is
/// one hour, so a version that waited would hang. Kills: reading to EOF before returning.
#[test]
fn complete_object_returns_before_a_long_deadline() {
    let object = payload("Stop", "inproc").into_bytes();
    let (hold, release) = std::sync::mpsc::channel();
    let got = hook::read_with_deadline(
        Stalls {
            first: Cursor::new(object.clone()),
            release,
        },
        Duration::from_secs(3600),
    );
    assert_eq!(got, object);
    drop(hold);
}

/// Process-level wiring check of `STDIN_DEADLINE`; the deadline logic itself is pinned without
/// wall-clock bounds by the two tests above. A parent that writes nothing useful and never
/// closes stdin must not hold the hook past the 2 s deadline (plus scheduling slack); bound asserted: 4.5 s.
#[test]
fn stdin_left_open_exits_within_bound() -> R {
    let tmp = Tmp::new("open")?;
    let mut child = Command::new(BIN)
        .args(["hook", "claude"])
        .env_clear()
        .envs(profile_env())
        .env("MEMORY_STATE_DIR", tmp.p())
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(e)?;
    let mut pipe = child.stdin.take().ok_or("no stdin")?;
    pipe.write_all(b"{\"hook_event_name\":\"Stop\",")
        .map_err(e)?; // never completed, never closed
    let start = Instant::now();
    let out = child.wait_with_output().map_err(e)?;
    let took = start.elapsed();
    drop(pipe);
    quiet_success(&out)?;
    assert!(took < Duration::from_millis(4500), "{took:?}");
    assert!(all_hint_bytes(&tmp.p().join("hints")).is_empty());
    Ok(())
}

/// A complete object with stdin still open is handled at once (Go decodes one value).
#[test]
fn complete_object_with_stdin_open_does_not_wait() -> R {
    let tmp = Tmp::new("open2")?;
    let mut child = Command::new(BIN)
        .args(["hook", "claude"])
        .env_clear()
        .envs(profile_env())
        .env("MEMORY_STATE_DIR", tmp.p())
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .map_err(e)?;
    let mut pipe = child.stdin.take().ok_or("no stdin")?;
    pipe.write_all(payload("Stop", "open").as_bytes())
        .map_err(e)?;
    let start = Instant::now();
    let out = child.wait_with_output().map_err(e)?;
    let took = start.elapsed();
    drop(pipe);
    quiet_success(&out)?;
    assert!(took < Duration::from_millis(1500), "{took:?}");
    assert_eq!(
        hint::scan(&all_hint_bytes(&tmp.p().join("hints")), 0)
            .map_err(e)?
            .records
            .len(),
        1
    );
    Ok(())
}

#[test]
fn canaries_never_reach_the_file() -> R {
    let tmp = Tmp::new("canary")?;
    let env = [
        ("ANTHROPIC_API_KEY", "CANARYKEY"),
        ("SECRET_TOKEN", "CANARYENV"),
        ("ANTHROPIC_MODEL", "CANARYMODELSECRET sk-x"),
        ("CLAUDE_CONFIG_DIR", "/CANARYDIR"),
    ];
    for event in ["SessionStart", "Stop", "SessionEnd"] {
        let out = spawn(
            &["hook", "claude", "CANARYARGV", "--token=CANARYARGV2"],
            tmp.p(),
            &env,
            payload(event, "c1").as_bytes(),
        )?;
        quiet_success(&out)?;
        assert!(!contains(&out.stderr, b"CANARY"));
    }
    let bytes = all_hint_bytes(&tmp.p().join("hints"));
    assert!(!bytes.is_empty());
    assert!(!contains(&bytes, b"CANARY"));
    assert!(!contains(&bytes, b"sk-x"));
    assert_eq!(hint::scan(&bytes, 0).map_err(e)?.records.len(), 3);
    Ok(())
}

/// `ulimit -f` does not limit O_APPEND writes on macOS (checked), so the short-write and
/// write-error paths are driven through an injected sink instead.
#[test]
fn write_line_short_and_failed_writes_are_failures_without_retry() {
    struct Sink(Option<usize>, usize);
    impl Write for Sink {
        fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
            self.1 += 1;
            self.0
                .map(|n| n.min(buf.len()))
                .ok_or_else(|| std::io::Error::other("full"))
        }
        fn flush(&mut self) -> std::io::Result<()> {
            Ok(())
        }
    }
    let line = b"0123456789\n";
    for (limit, want) in [
        (Some(line.len()), HookResult::Appended),
        (Some(3), HookResult::ShortWrite),
        (Some(0), HookResult::ShortWrite),
        (None, HookResult::WriteFailed),
    ] {
        let mut sink = Sink(limit, 0);
        assert_eq!(hook::write_line(&mut sink, line), want);
        assert_eq!(sink.1, 1, "exactly one write call, no retry");
    }
}

// ------------------------------------------------------------------ properties

const SAFE: &[u8] = b"abcdef/._-";

/// Oracle independent of the implementation: whatever the input, at most one line, at most
/// 4096 bytes, newline-terminated and recovered whole by the reader.
fn property(stdin: &[u8], pick: usize, launch_args: &[(bool, u16)]) -> R {
    const EVENTS: [&str; 4] = ["SessionStart", "Stop", "SessionEnd", "Bogus"];
    let tmp = Tmp::new("prop")?;
    let dir = tmp.p().join("hints");
    let mut argv = vec!["claude".to_owned()];
    for (flag, len) in launch_args {
        argv.push(if *flag {
            "--model".to_owned()
        } else {
            "--x".to_owned()
        });
        argv.push("a".repeat(usize::from(*len % 400)));
    }
    let env = vec!["ANTHROPIC_MODEL=m".to_owned()];
    let shaped: Vec<u8>;
    let stdin = if pick.is_multiple_of(3) {
        stdin
    } else {
        let path: String = stdin
            .iter()
            .map(|b| {
                char::from(
                    SAFE.get(usize::from(*b) % SAFE.len())
                        .copied()
                        .unwrap_or(b'a'),
                )
            })
            .collect();
        shaped = format!(
            r#"{{"hook_event_name":"{}","session_id":"s","cwd":"/{path}","transcript_path":"/{path}"}}"#,
            EVENTS.get(pick % EVENTS.len()).copied().unwrap_or("Stop")
        )
        .into_bytes();
        &shaped
    };
    let c = HookCall {
        argv: &argv,
        env: &env,
        ..call("claude", stdin, &dir)
    };
    let got = hook::run(&c);
    let bytes = fs::read(dir.join(format!("{DAY}.jsonl"))).unwrap_or_default();
    match got {
        HookResult::Appended => {
            if bytes.len() > MAX_LINE_BYTES || bytes.last() != Some(&b'\n') {
                return Err(format!("bad line length {}", bytes.len()));
            }
            if bytes.iter().filter(|b| **b == b'\n').count() != 1 {
                return Err("not exactly one line".into());
            }
            let scan = hint::scan(&bytes, 0).map_err(e)?;
            if (scan.records.len(), scan.dropped, scan.pending_bytes) != (1, 0, 0) {
                return Err("not scan-parseable".into());
            }
        }
        _ => {
            if !bytes.is_empty() {
                return Err(format!("{got:?} but wrote {} bytes", bytes.len()));
            }
        }
    }
    Ok(())
}

#[hegel::test]
fn hegel_core_never_panics_and_writes_at_most_one_whole_line(tc: TestCase) {
    let stdin: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
    let pick = usize::from(tc.draw(gs::integers::<u8>()));
    let args: Vec<(bool, u16)> = tc
        .draw(gs::vecs(gs::integers::<u16>()))
        .into_iter()
        .map(|n| (n % 2 == 0, n))
        .collect();
    let r = property(&stdin, pick, &args);
    assert!(r.is_ok(), "{r:?}");
}

proptest! {
    #[test]
    fn proptest_core_never_panics_and_writes_at_most_one_whole_line(
        stdin in proptest::collection::vec(any::<u8>(), 0..2048),
        pick in 0usize..300,
        args in proptest::collection::vec((any::<bool>(), any::<u16>()), 0..24),
    ) {
        let r = property(&stdin, pick, &args);
        prop_assert!(r.is_ok(), "{r:?}");
    }
}

// ------------------------------------------------------------------ P4 (synthetic, informational)

fn percentile(sorted: &[u128], pct: usize) -> u128 {
    let idx = (sorted.len() * pct).div_ceil(100).saturating_sub(1);
    sorted.get(idx).copied().unwrap_or(0)
}

/// Synthetic P4: in-process time of read_bounded + run (the whole hook minus process start),
/// 2,000 runs, asserting the DESIGN.md 14 footprint target (p99 at most 1 ms). Timing-sensitive
/// by nature, so it belongs in no loaded CI job: run it in a quiet release job
/// (`--release -- --ignored p4_core_latency`) as slice-2 footprint acceptance (prototype P4).
#[test]
#[ignore = "measurement: run with --release -- --ignored --nocapture"]
fn p4_core_latency() -> R {
    let tmp = Tmp::new("p4")?;
    let dir = tmp.p().join("hints");
    let input = payload("Stop", "p4").into_bytes();
    let env: Vec<String> = vec!["ANTHROPIC_MODEL=m".to_owned()];
    let mut samples = Vec::new();
    for _ in 0..2_000 {
        let start = Instant::now();
        let stdin = hook::read_bounded(Cursor::new(&input));
        let got = hook::run(&HookCall {
            env: &env,
            ..call("claude", &stdin, &dir)
        });
        samples.push(start.elapsed().as_nanos());
        assert_eq!(got, HookResult::Appended);
    }
    samples.sort_unstable();
    let (p50, p99, max) = (
        percentile(&samples, 50),
        percentile(&samples, 99),
        samples.last().copied().unwrap_or(0),
    );
    eprintln!(
        "P4 synthetic n={} p50={p50}ns p99={p99}ns max={max}ns target_p99<=1ms met={}",
        samples.len(),
        p99 <= 1_000_000
    );
    assert!(
        p99 <= 1_000_000,
        "hook in-process p99 {p99} ns exceeds the 1 ms target"
    );
    Ok(())
}

/// Informational spawn latency of the hook binary versus /usr/bin/true, 300 runs each.
/// DESIGN.md 14 lists spawn latency as informational, so only the exit status is asserted.
#[test]
#[ignore = "measurement: run with --release -- --ignored --nocapture"]
fn p4_spawn_latency() -> R {
    let tmp = Tmp::new("p4s")?;
    let input = payload("Stop", "p4").into_bytes();
    let time = |hook: bool| -> Result<Vec<u128>, String> {
        let mut v = Vec::new();
        for _ in 0..300 {
            let start = Instant::now();
            let mut cmd = if hook {
                Command::new(BIN)
            } else {
                Command::new("/usr/bin/true")
            };
            if hook {
                cmd.args(["hook", "claude"])
                    .env_clear()
                    .envs(profile_env())
                    .env("MEMORY_STATE_DIR", tmp.p());
            }
            let mut child = cmd
                .stdin(Stdio::piped())
                .stdout(Stdio::null())
                .spawn()
                .map_err(e)?;
            if let Some(mut pipe) = child.stdin.take() {
                let _ = pipe.write_all(&input);
            }
            let status = child.wait().map_err(e)?;
            assert!(status.success(), "the hook exits 0 on every path");
            v.push(start.elapsed().as_nanos());
        }
        v.sort_unstable();
        Ok(v)
    };
    let (h, t) = (time(true)?, time(false)?);
    eprintln!(
        "spawn hook p50={}ns p99={}ns | true p50={}ns p99={}ns",
        percentile(&h, 50),
        percentile(&h, 99),
        percentile(&t, 50),
        percentile(&t, 99)
    );
    Ok(())
}
