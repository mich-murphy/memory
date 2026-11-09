//! `memory hook <agent>`: the per-agent hook command (DESIGN.md sections 9.1, 10.1, 14).
//!
//! [`run`] is a pure-ish core: it takes the stdin bytes, launch argv/env, the hints directory
//! and the UTC day as arguments, and returns a closed [`HookResult`]. [`main_entry`] is the
//! thin process wrapper. Every path ends in exit 0, nothing is ever printed, there is no
//! retry and no wait on the daemon.
use memory_core::domain::Agent;
use memory_core::hint::{self, HintEvent, MAX_HOOK_INPUT_BYTES};
use memory_core::privacy::sanitize_launch;
use std::fs::{DirBuilder, OpenOptions};
use std::io::{IsTerminal, Read, Write};
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::process::ExitCode;
use std::sync::mpsc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

/// How long the wrapper waits for stdin before giving up (Go: `stdinTimeout`, 2 s).
pub const STDIN_DEADLINE: Duration = Duration::from_secs(2);

/// Closed set of outcomes of one hook invocation. Carries no input text.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum HookResult {
    /// One whole line was written with a single `write(2)`.
    Appended,
    /// The agent argument is not `claude`, `codex` or `pi`.
    UnknownAgent,
    /// The day string is not `YYYY-MM-DD`-shaped.
    InvalidDate,
    /// Stdin was not a usable hook payload.
    BadInput,
    /// No line fits the size bound.
    BuildFailed,
    /// The hints directory is not a directory, is group/world-writable or is not ours.
    DirRefused,
    /// The hints directory could not be inspected or created.
    DirUnavailable,
    /// The day file could not be opened.
    OpenFailed,
    /// `write(2)` failed (for example `ENOSPC`).
    WriteFailed,
    /// `write(2)` accepted fewer bytes than the line.
    ShortWrite,
    /// No state directory could be determined.
    NoStateDir,
}

impl HookResult {
    /// Fixed code for diagnostics. Never derived from input.
    #[must_use]
    pub const fn code(self) -> &'static str {
        match self {
            Self::Appended => "appended",
            Self::UnknownAgent => "unknown_agent",
            Self::InvalidDate => "invalid_date",
            Self::BadInput => "bad_input",
            Self::BuildFailed => "build_failed",
            Self::DirRefused => "dir_refused",
            Self::DirUnavailable => "dir_unavailable",
            Self::OpenFailed => "open_failed",
            Self::WriteFailed => "write_failed",
            Self::ShortWrite => "short_write",
            Self::NoStateDir => "no_state_dir",
        }
    }
}

/// Inputs of one hook run.
#[derive(Debug, Clone, Copy)]
pub struct HookCall<'a> {
    /// Agent argument as typed on the command line.
    pub agent: &'a str,
    /// Stdin bytes, already bounded by [`read_bounded`].
    pub stdin: &'a [u8],
    /// Launch argv (the agent's, not the hook's) used only for `SessionStart`.
    pub argv: &'a [String],
    /// Environment as `KEY=VALUE` strings, used only for `SessionStart`.
    pub env: &'a [String],
    /// Hints directory (`state/hints`).
    pub hints_dir: &'a Path,
    /// UTC day `YYYY-MM-DD` naming the file.
    pub today: &'a str,
}

fn parse_agent(name: &str) -> Option<Agent> {
    match name {
        "claude" => Some(Agent::Claude),
        "codex" => Some(Agent::Codex),
        "pi" => Some(Agent::Pi),
        _ => None,
    }
}

/// True when `day` has the shape `DDDD-DD-DD` (digits and dashes only, so it cannot escape the
/// hints directory).
#[must_use]
pub fn is_day_name(day: &str) -> bool {
    day.len() == 10
        && day.bytes().enumerate().all(|(at, byte)| {
            if at == 4 || at == 7 {
                byte == b'-'
            } else {
                byte.is_ascii_digit()
            }
        })
}

/// UTC calendar day of a Unix time, `YYYY-MM-DD`. Civil-from-days (Hinnant), no dependency.
#[must_use]
pub fn utc_day(unix_secs: u64) -> String {
    let z = i64::try_from(unix_secs / 86_400).unwrap_or(0) + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let day = doy - (153 * mp + 2) / 5 + 1;
    let month = if mp < 10 { mp + 3 } else { mp - 9 };
    let year = yoe + era * 400 + i64::from(month <= 2);
    format!("{year:04}-{month:02}-{day:02}")
}

/// Incremental end-of-JSON-object detector, so a parent that never closes stdin does not hold
/// the hook (Go decodes one value for the same reason). Not a validator: the real parse is
/// `parse_hook_input`.
#[derive(Default)]
struct ValueEnd {
    started: bool,
    depth: u32,
    in_string: bool,
    escaped: bool,
}

enum Feed {
    More,
    End,
    Bad,
}

impl ValueEnd {
    fn feed(&mut self, byte: u8) -> Feed {
        if !self.started {
            return match byte {
                b' ' | b'\t' | b'\n' | b'\r' => Feed::More,
                b'{' => {
                    self.started = true;
                    self.depth = 1;
                    Feed::More
                }
                _ => Feed::Bad,
            };
        }
        if self.in_string {
            if self.escaped {
                self.escaped = false;
            } else if byte == b'\\' {
                self.escaped = true;
            } else if byte == b'"' {
                self.in_string = false;
            }
            return Feed::More;
        }
        match byte {
            b'"' => self.in_string = true,
            b'{' | b'[' => self.depth = self.depth.saturating_add(1),
            b'}' | b']' => {
                self.depth = self.depth.saturating_sub(1);
                if self.depth == 0 {
                    return Feed::End;
                }
            }
            _ => {}
        }
        Feed::More
    }
}

/// Read a hook payload: stop at the end of the first JSON object, at EOF, at a first byte that
/// cannot start an object, or one byte past [`MAX_HOOK_INPUT_BYTES`] (which the parser then
/// refuses as too large).
pub fn read_bounded<R: Read>(mut reader: R) -> Vec<u8> {
    let mut out = Vec::new();
    let mut chunk = [0u8; 8192];
    let mut scan = ValueEnd::default();
    loop {
        let n = match reader.read(&mut chunk) {
            Ok(0) | Err(_) => break,
            Ok(n) => n,
        };
        for &byte in chunk.get(..n).unwrap_or_default() {
            out.push(byte);
            if matches!(scan.feed(byte), Feed::End | Feed::Bad) || out.len() > MAX_HOOK_INPUT_BYTES
            {
                return out;
            }
        }
    }
    out
}

fn vet_dir(dir: &Path) -> Result<u32, HookResult> {
    let meta = match std::fs::symlink_metadata(dir) {
        Ok(meta) => meta,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            DirBuilder::new()
                .recursive(true)
                .mode(0o700)
                .create(dir)
                .map_err(|_| HookResult::DirUnavailable)?;
            std::fs::symlink_metadata(dir).map_err(|_| HookResult::DirUnavailable)?
        }
        Err(_) => return Err(HookResult::DirUnavailable),
    };
    if !meta.is_dir() || meta.mode() & 0o022 != 0 {
        return Err(HookResult::DirRefused);
    }
    Ok(meta.uid())
}

fn append(dir: &Path, today: &str, line: &[u8]) -> HookResult {
    let dir_uid = match vet_dir(dir) {
        Ok(uid) => uid,
        Err(result) => return result,
    };
    let path = dir.join(format!("{today}.jsonl"));
    let Ok(mut file) = OpenOptions::new()
        .append(true)
        .create(true)
        .mode(0o600)
        .open(path)
    else {
        return HookResult::OpenFailed;
    };
    // Safe Rust has no geteuid. A file we just opened with O_CREAT is ours unless it already
    // existed, so "directory owner == file owner" stands in for "directory is ours".
    match file.metadata() {
        Ok(meta) if meta.uid() == dir_uid => {}
        _ => return HookResult::DirRefused,
    }
    write_line(&mut file, line)
}

/// One `write` call, not `write_all`: a short write is a failure, never a second write.
pub fn write_line<W: Write>(sink: &mut W, line: &[u8]) -> HookResult {
    match sink.write(line) {
        Ok(n) if n == line.len() => HookResult::Appended,
        Ok(_) => HookResult::ShortWrite,
        Err(_) => HookResult::WriteFailed,
    }
}

/// Run one hook: parse by key, build one bounded line, append it. Launch evidence is attached
/// only on `SessionStart` and only if the allowlist leaves something.
#[must_use]
pub fn run(call: &HookCall<'_>) -> HookResult {
    let Some(agent) = parse_agent(call.agent) else {
        return HookResult::UnknownAgent;
    };
    if !is_day_name(call.today) {
        return HookResult::InvalidDate;
    }
    let Ok(input) = hint::parse_hook_input(&agent, call.stdin) else {
        return HookResult::BadInput;
    };
    let launch = if input.event == HintEvent::SessionStart {
        let launch = sanitize_launch(call.argv, call.env);
        if launch.argv().is_empty() && launch.env().is_empty() {
            None
        } else {
            Some(launch)
        }
    } else {
        None
    };
    let Ok(line) = hint::build_line(&input, launch.as_ref()) else {
        return HookResult::BuildFailed;
    };
    append(call.hints_dir, call.today, &line)
}

fn state_dir() -> Option<PathBuf> {
    if let Some(dir) = std::env::var_os("MEMORY_STATE_DIR") {
        return Some(PathBuf::from(dir));
    }
    let home = std::env::var_os("HOME")?;
    Some(PathBuf::from(home).join(".local/state/memory"))
}

fn read_stdin_with_deadline() -> Vec<u8> {
    let stdin = std::io::stdin();
    if stdin.is_terminal() {
        return Vec::new();
    }
    let (tx, rx) = mpsc::channel();
    let spawned = std::thread::Builder::new().spawn(move || {
        let _ = tx.send(read_bounded(stdin.lock()));
    });
    if spawned.is_err() {
        return Vec::new();
    }
    // On timeout the reader stays blocked and ends with the process.
    rx.recv_timeout(STDIN_DEADLINE).unwrap_or_default()
}

fn process(agent: &str) -> HookResult {
    if parse_agent(agent).is_none() {
        return HookResult::UnknownAgent;
    }
    let Some(state) = state_dir() else {
        return HookResult::NoStateDir;
    };
    let stdin = read_stdin_with_deadline();
    let env: Vec<String> = std::env::vars_os()
        .filter_map(|(key, value)| Some(format!("{}={}", key.to_str()?, value.to_str()?)))
        .collect();
    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |elapsed| elapsed.as_secs());
    let today = utc_day(secs);
    let hints = state.join("hints");
    run(&HookCall {
        agent,
        stdin: &stdin,
        // The hook's own argv is `memory hook <agent>`; the agent's argv would need an
        // ancestor walk, which does not fit the 1 ms budget. Env is inherited from the agent.
        argv: &[],
        env: &env,
        hints_dir: &hints,
        today: &today,
    })
}

/// Process wrapper: always exits 0, prints nothing.
#[must_use]
pub fn main_entry(agent: &str) -> ExitCode {
    let _ = process(agent);
    ExitCode::SUCCESS
}
