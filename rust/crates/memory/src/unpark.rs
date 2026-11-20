//! `memory unpark <owner-prefix>|--all`: clear parked entries from `status.json`.
//!
//! The daemon is the only writer of `status.json` and reads it once at start, so an edit beside
//! a running daemon would be overwritten and never seen. This command takes the daemon lock and
//! refuses (`memory: lock_held`, exit 75) beside a running daemon: stop it, unpark, start it.
//!
//! No network. The file is written only when at least one entry matches; on no match, a damaged
//! file or a newer schema it is left untouched. Every other field of the body is kept equal.
//! Output is closed: one line `unparked owner=<prefix> entries=<n>` per owner. Source ids are
//! never printed, so the argument is an owner prefix, never a source.
use crate::daemon::{EXIT_CONFIG, StatusBody};
use crate::state::{DaemonLock, Loaded, MissingReason, StateDir, StateFile, default_root};
use memory_core::domain::Owner;
use memory_core::limits::{owner_prefix, parse_owner_prefix};
use std::collections::BTreeMap;
use std::ffi::OsString;
use std::io::Write;
use std::path::Path;
use std::process::ExitCode;

/// Exit code: nothing matched, or nothing is parked.
pub const EXIT_NOT_PARKED: u8 = 1;
/// Exit code: usage error or an unparsable owner prefix.
pub const EXIT_USAGE: u8 = 64;

/// What to clear.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Target {
    /// Every entry.
    All,
    /// Every entry of one owner.
    Owner(Owner),
}

/// A refusal: closed code and exit code.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Refusal {
    /// Machine-readable code.
    pub code: &'static str,
    /// Exit code.
    pub exit: u8,
}

const fn refusal(code: &'static str, exit: u8) -> Refusal {
    Refusal { code, exit }
}

/// Parse the arguments after the word `unpark`.
///
/// # Errors
/// `usage` or `owner`, both exit 64.
pub fn parse_args(mut args: impl Iterator<Item = OsString>) -> Result<Target, Refusal> {
    let first = args.next().ok_or(refusal("usage", EXIT_USAGE))?;
    if args.next().is_some() {
        return Err(refusal("usage", EXIT_USAGE));
    }
    let text = first
        .into_string()
        .map_err(|_| refusal("usage", EXIT_USAGE))?;
    if text == "--all" {
        return Ok(Target::All);
    }
    if text.starts_with('-') {
        return Err(refusal("usage", EXIT_USAGE));
    }
    parse_owner_prefix(&text)
        .map(Target::Owner)
        .map_err(|_| refusal("owner", EXIT_USAGE))
}

/// Clear `target` under the held lock on `root`; returns `(owner prefix, entries removed)` per
/// owner.
///
/// # Errors
/// A [`Refusal`]; the file is untouched on every one except a failed write.
pub fn unpark(root: &Path, target: &Target) -> Result<Vec<(String, usize)>, Refusal> {
    let lock = DaemonLock::acquire(root).map_err(|e| refusal(e.code(), e.exit_code()))?;
    let dir = StateDir::prepare(&lock).map_err(|e| refusal(e.code(), e.exit_code()))?;
    let mut body: StatusBody = match dir.read(StateFile::Status) {
        Loaded::Present(stored) => serde_json::from_slice(&stored.body)
            .map_err(|_| refusal("status_damaged", EXIT_CONFIG))?,
        Loaded::Missing(MissingReason::NotFound) => {
            return Err(refusal("not_parked", EXIT_NOT_PARKED));
        }
        Loaded::Missing(MissingReason::NewerSchema) => {
            return Err(refusal("status_newer_schema", EXIT_CONFIG));
        }
        Loaded::Missing(_) => return Err(refusal("status_damaged", EXIT_CONFIG)),
    };
    let mut removed: BTreeMap<String, usize> = BTreeMap::new();
    body.parked.retain(|entry| {
        let hit = match target {
            Target::All => true,
            Target::Owner(owner) => entry.owner == *owner,
        };
        if hit {
            *removed.entry(owner_prefix(&entry.owner)).or_default() += 1;
        }
        !hit
    });
    if removed.is_empty() {
        return Err(refusal("not_parked", EXIT_NOT_PARKED));
    }
    let bytes = serde_json::to_vec(&body).map_err(|_| refusal("state_io", 1))?;
    dir.write(StateFile::Status, &bytes)
        .map_err(|e| refusal(e.code(), e.exit_code()))?;
    Ok(removed.into_iter().collect())
}

fn fail(code: &str, exit: u8) -> ExitCode {
    let _ = writeln!(std::io::stderr(), "memory: {code}");
    ExitCode::from(exit)
}

/// Entry of `memory unpark`; `args` follow the word `unpark`.
///
/// Exit codes: 0 unparked, 1 nothing matched (`not_parked`), 2 damaged or newer-schema
/// `status.json` or a config-class refusal, 64 usage or owner prefix, 75 the daemon lock is held.
#[must_use]
pub fn main_entry(args: impl Iterator<Item = OsString>) -> ExitCode {
    let target = match parse_args(args) {
        Ok(target) => target,
        Err(r) => return fail(r.code, r.exit),
    };
    let Some(root) = default_root() else {
        return fail("state_root", EXIT_CONFIG);
    };
    match unpark(&root, &target) {
        Err(r) => fail(r.code, r.exit),
        Ok(done) => {
            let mut out = std::io::stdout().lock();
            for (prefix, entries) in done {
                let _ = writeln!(out, "unparked owner={prefix} entries={entries}");
            }
            ExitCode::SUCCESS
        }
    }
}
