//! `memory install` and `memory uninstall` (brief 36): the launchd agent file, the Claude and
//! Codex hook entries and the Pi extension for the running binary.
pub mod files;
pub mod json;
pub mod merge;

/// Why an install or uninstall step refused. Closed; carries no file text.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum Failure {
    /// A file is not a JSON object, has a duplicate key, or has a shape the merge would damage.
    #[error("install_unparsable")]
    Unparsable,
    /// A Codex `hooks.json` with a top-level key other than `description` and `hooks`.
    #[error("install_codex_schema")]
    CodexSchema,
    /// A file could not be read, written or renamed; only the step fails.
    #[error("install_io")]
    Io,
}

impl Failure {
    /// The closed code printed after `memory: `.
    #[must_use]
    pub fn code(&self) -> &'static str {
        match self {
            Self::Unparsable => "install_unparsable",
            Self::CodexSchema => "install_codex_schema",
            Self::Io => "install_io",
        }
    }
}

use crate::config::Config;
use crate::daemon::EXIT_CONFIG;
use crate::restore::{EXIT_FAILED, EXIT_USAGE};
use files::Outcome;
use merge::Agent;
use std::ffi::OsString;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

fn fail(code: &str, exit: u8) -> ExitCode {
    let _ = writeln!(std::io::stderr(), "memory: {code}");
    ExitCode::from(exit)
}

fn dir_from(var: &str, home: &Path, default: &str) -> PathBuf {
    match std::env::var_os(var) {
        Some(dir) if !dir.is_empty() => PathBuf::from(dir),
        _ => home.join(default),
    }
}

fn report(out: &mut impl Write, path: &Path, outcome: &Outcome) -> bool {
    let line = match outcome {
        Outcome::Wrote { backup: Some(name) } => {
            format!("wrote {} (backup {name})", path.display())
        }
        Outcome::Wrote { backup: None } => format!("wrote {}", path.display()),
        Outcome::Unchanged => format!("unchanged {}", path.display()),
        Outcome::Removed => format!("removed {}", path.display()),
        Outcome::Kept => format!("kept {} (not ours)", path.display()),
    };
    writeln!(out, "{line}").is_ok()
}

fn run_steps(install: bool, dry: bool, exe: &str, home: &Path) -> Result<Vec<String>, Failure> {
    let now = files::now_secs();
    let mut out = std::io::stdout().lock();
    let mut next = Vec::new();
    let say = |out: &mut std::io::StdoutLock<'_>, path: &Path, outcome: &Outcome| {
        let _ = report(out, path, outcome);
    };
    if cfg!(target_os = "macos") {
        let plist = home.join("Library/LaunchAgents/memory.plist");
        let log = home.join("Library/Logs/memory/daemon.log");
        let arg = install.then_some((exe, log.as_path()));
        say(&mut out, &plist, &files::plist_file(&plist, arg, dry, now)?);
        next.push(if install {
            format!("launchctl bootstrap gui/$(id -u) {}", plist.display())
        } else {
            "launchctl bootout gui/$(id -u)/memory".to_owned()
        });
    } else {
        let _ = writeln!(out, "launchd: skipped (not macOS)");
    }
    let claude = dir_from("CLAUDE_CONFIG_DIR", home, ".claude").join("settings.json");
    let codex = dir_from("CODEX_HOME", home, ".codex").join("hooks.json");
    for (path, agent) in [(&claude, Agent::Claude), (&codex, Agent::Codex)] {
        let command = install.then(|| merge::hook_command(exe, agent));
        let outcome = files::hooks_file(path, agent, command.as_deref(), dry, now)?;
        if install && agent == Agent::Codex && matches!(outcome, Outcome::Wrote { .. }) {
            next.push("approve the memory hooks in Codex /hooks".to_owned());
        }
        say(&mut out, path, &outcome);
    }
    let pi = dir_from("PI_CODING_AGENT_DIR", home, ".pi/agent").join("extensions/memory.ts");
    say(
        &mut out,
        &pi,
        &files::pi_file(&pi, install.then_some(exe), dry, now)?,
    );
    Ok(next)
}

/// Entry of `memory install` / `memory uninstall`; `args` follow the word and may be only
/// `--dry-run`.
///
/// Exit codes: 0 done, 1 a file was refused or failed, 2 state root or config, 64 usage.
#[must_use]
pub fn main_entry(install: bool, args: impl Iterator<Item = OsString>) -> ExitCode {
    let args: Vec<OsString> = args.collect();
    let dry = match args.as_slice() {
        [] => false,
        [one] if one == "--dry-run" => true,
        _ => return fail("usage", EXIT_USAGE),
    };
    let Some(home) = std::env::var_os("HOME").filter(|h| !h.is_empty()) else {
        return fail("state_root", EXIT_CONFIG);
    };
    let home = PathBuf::from(home);
    if install {
        if std::env::var_os("MEMORY_STATE_DIR").is_some() {
            return fail("install_state_dir_override", EXIT_CONFIG);
        }
        let root = home.join(".local/state/memory");
        if let Err(error) = Config::load(&root.join("config.toml")) {
            return fail(error.code(), EXIT_CONFIG);
        }
    }
    // The path as invoked, not canonicalized: on macOS `current_exe` keeps a symlink, so an
    // upgrade behind a stable `memory` link keeps the hook command, and with it Codex's trust
    // hash (which covers the command string). The name must be `memory`, or a later install
    // or uninstall could not recognise the entries as ours.
    let exe = std::env::current_exe()
        .ok()
        .and_then(|p| p.to_str().map(str::to_owned));
    let Some(exe) = exe else {
        return fail("install_io", EXIT_FAILED);
    };
    let named = Path::new(&exe);
    if install && (!named.is_absolute() || named.file_name().is_none_or(|name| name != "memory")) {
        return fail("install_binary_name", EXIT_CONFIG);
    }
    match run_steps(install, dry, &exe, &home) {
        Ok(next) => {
            let mut out = std::io::stdout().lock();
            for line in next {
                let _ = writeln!(out, "next: {line}");
            }
            ExitCode::SUCCESS
        }
        Err(failure) => fail(failure.code(), EXIT_FAILED),
    }
}
