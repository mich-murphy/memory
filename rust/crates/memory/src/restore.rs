//! `memory restore <owner> --out DIR`: read one owner back from the bucket through
//! `memory-reader` and write one file per live source.
//!
//! Read-only towards the bucket (GET only), takes no daemon lock and writes nothing under the
//! state root. Every error is a closed code; none carries a manifest path, a key, the endpoint,
//! the bucket or a credential.
//!
//! The library takes a fetch closure, not a `Store`: the reader already takes one, so a test
//! (or another caller) supplies a closure without implementing the trait's write methods.
//!
//! Layout: `DIR/<source path>` holds the latest epoch's transformed bytes of each live source.
//! Source paths are manifest data and so untrusted: every path is re-validated here
//! (manifest parsing also rejects bad paths, but this seam does not rely on it) and all paths
//! are checked for collisions before the first byte is written.
//!
//! A refused restore leaves nothing behind when the refusal comes before writing starts (output
//! not usable, owner absent, bad or colliding paths). A failure while writing (a store or
//! decode error, an I/O error) removes what this run created: the files and directories inside
//! `DIR`, and `DIR` itself if this run created it.
use crate::assemble::build_store;
use crate::config::Config;
use crate::daemon::EXIT_CONFIG;
use crate::environment::{Store, StoreError};
use memory_core::domain::Owner;
use memory_core::limits::parse_owner_prefix;
use memory_core::locator::RootRelativePath;
use memory_core::manifest::{OwnerState, SourceEntry};
use memory_reader::{OwnerError, RestoreError as PieceError, read_owner, restore_source};
use std::collections::BTreeSet;
use std::ffi::OsString;
use std::fs::{self, DirBuilder, OpenOptions};
use std::io::Write;
use std::os::unix::fs::{DirBuilderExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::process::ExitCode;

/// Exit code: usage error or unparsable owner.
pub const EXIT_USAGE: u8 = 64;
/// Exit code: the owner has no `current.json`.
pub const EXIT_ABSENT: u8 = 66;
/// Exit code: any other failure.
pub const EXIT_FAILED: u8 = 1;

/// Why a restore was refused or failed. Closed; carries no input text.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum RestoreError {
    /// `DIR` exists and is not an empty directory.
    #[error("output_not_empty")]
    OutputNotEmpty,
    /// `DIR` or a file under it could not be inspected, created or written.
    #[error("output_io")]
    OutputIo,
    /// The store failed (transport, auth or throttle).
    #[error("store")]
    Store,
    /// The owner's manifest was written by a newer schema.
    #[error("newer_schema")]
    NewerSchema,
    /// The owner's manifest chain is corrupt.
    #[error("chain_corrupt")]
    ChainCorrupt,
    /// A source path is not a clean root-relative path.
    #[error("bad_source_path")]
    BadSourcePath,
    /// Two live sources would write the same path, or one is a directory of the other.
    #[error("path_collision")]
    PathCollision,
    /// A piece object named by the manifest is absent from the store.
    #[error("object_missing")]
    ObjectMissing,
    /// A source did not decode to the bytes its manifest promises.
    #[error("source_corrupt")]
    SourceCorrupt,
}

/// What a successful restore wrote.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct Report {
    /// Files written.
    pub written: usize,
    /// Sources not written because they are marked deleted.
    pub skipped_deleted: usize,
    /// Bytes written.
    pub bytes: u64,
}

/// Outcome of [`restore_owner`].
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Outcome {
    /// The owner has no `current.json`; nothing was written.
    Absent,
    /// The owner was restored.
    Restored(Report),
}

enum Miss<E> {
    Missing,
    Fetch(E),
}

/// Whether `out` is usable: `Ok(true)` for an existing empty directory, `Ok(false)` when it
/// does not exist.
fn check_output(out: &Path) -> Result<bool, RestoreError> {
    match fs::symlink_metadata(out) {
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(false),
        Err(_) => Err(RestoreError::OutputIo),
        Ok(meta) if meta.is_dir() => {
            let mut entries = fs::read_dir(out).map_err(|_| RestoreError::OutputIo)?;
            if entries.next().is_none() {
                Ok(true)
            } else {
                Err(RestoreError::OutputNotEmpty)
            }
        }
        Ok(_) => Err(RestoreError::OutputNotEmpty),
    }
}

struct Planned<'a> {
    path: RootRelativePath,
    entry: &'a SourceEntry,
}

/// Validate every live source's path and refuse collisions, before anything is written.
fn plan(state: &OwnerState) -> Result<(Vec<Planned<'_>>, usize), RestoreError> {
    let mut planned = Vec::new();
    let mut skipped = 0;
    let mut seen: BTreeSet<Vec<String>> = BTreeSet::new();
    for entry in state.sources.values() {
        if entry.deleted_at.is_some() {
            skipped += 1;
            continue;
        }
        let path = RootRelativePath::parse(&entry.path).map_err(|_| RestoreError::BadSourcePath)?;
        if !seen.insert(path.components().to_vec()) {
            return Err(RestoreError::PathCollision);
        }
        planned.push(Planned { path, entry });
    }
    for item in &planned {
        let parts = item.path.components();
        for k in 1..parts.len() {
            if parts.get(..k).is_some_and(|prefix| seen.contains(prefix)) {
                return Err(RestoreError::PathCollision);
            }
        }
    }
    Ok((planned, skipped))
}

fn write_file(root: &Path, path: &RootRelativePath, bytes: &[u8]) -> Result<(), RestoreError> {
    let mut target = root.to_path_buf();
    target.extend(path.components());
    if let Some(parent) = target.parent() {
        DirBuilder::new()
            .recursive(true)
            .mode(0o700)
            .create(parent)
            .map_err(|_| RestoreError::OutputIo)?;
    }
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&target)
        .map_err(|_| RestoreError::OutputIo)?;
    file.write_all(bytes)
        .and_then(|()| file.sync_all())
        .map_err(|_| RestoreError::OutputIo)
}

fn cleanup(out: &Path, created: bool) {
    if let Ok(entries) = fs::read_dir(out) {
        for entry in entries.flatten() {
            let path = entry.path();
            let is_dir = entry.file_type().is_ok_and(|t| t.is_dir());
            let _ = if is_dir {
                fs::remove_dir_all(&path)
            } else {
                fs::remove_file(&path)
            };
        }
    }
    if created {
        let _ = fs::remove_dir(out);
    }
}

/// Write every live source of `state` under `out`. `fetch` returns `Ok(None)` for an absent key.
///
/// `out` must not exist or must be an empty directory (checked here, before any fetch). Paths
/// are validated and checked for collisions before the first write.
///
/// # Errors
/// A closed [`RestoreError`]; a failure after writing started removes what this run created.
pub fn restore_state<E>(
    state: &OwnerState,
    mut fetch: impl FnMut(&str) -> Result<Option<Vec<u8>>, E>,
    out: &Path,
) -> Result<Report, RestoreError> {
    let existed = check_output(out)?;
    let (planned, skipped_deleted) = plan(state)?;
    if !existed {
        DirBuilder::new()
            .recursive(true)
            .mode(0o700)
            .create(out)
            .map_err(|_| RestoreError::OutputIo)?;
    }
    let mut report = Report {
        skipped_deleted,
        ..Report::default()
    };
    for item in &planned {
        let step = restore_source(item.entry, |key| match fetch(key) {
            Ok(Some(body)) => Ok(body),
            Ok(None) => Err(Miss::Missing),
            Err(e) => Err(Miss::Fetch(e)),
        })
        .map_err(|e| match e {
            PieceError::Fetch(Miss::Missing) => RestoreError::ObjectMissing,
            PieceError::Fetch(Miss::Fetch(_)) => RestoreError::Store,
            PieceError::NoEpoch
            | PieceError::Decode(_)
            | PieceError::Gap
            | PieceError::Size
            | PieceError::Digest => RestoreError::SourceCorrupt,
        })
        .and_then(|bytes| {
            write_file(out, &item.path, &bytes)?;
            Ok(bytes.len())
        });
        match step {
            Ok(len) => {
                report.written += 1;
                report.bytes = report.bytes.saturating_add(len as u64);
            }
            Err(error) => {
                cleanup(out, !existed);
                return Err(error);
            }
        }
    }
    Ok(report)
}

/// Read `owner` through `fetch` and restore it under `out`.
///
/// `out` is checked before the first fetch. An owner with no `current.json` is
/// [`Outcome::Absent`] and nothing is created.
///
/// # Errors
/// A closed [`RestoreError`].
pub fn restore_owner<E>(
    owner: &Owner,
    mut fetch: impl FnMut(&str) -> Result<Option<Vec<u8>>, E>,
    out: &Path,
) -> Result<Outcome, RestoreError> {
    check_output(out)?;
    let folded = match read_owner(owner, &mut fetch) {
        Ok(Some(folded)) => folded,
        Ok(None) => return Ok(Outcome::Absent),
        Err(OwnerError::Fetch(_)) => return Err(RestoreError::Store),
        Err(OwnerError::NewerSchema) => return Err(RestoreError::NewerSchema),
        Err(OwnerError::Corrupt(_)) => return Err(RestoreError::ChainCorrupt),
    };
    restore_state(&folded.state, fetch, out).map(Outcome::Restored)
}

fn fail(code: &str, exit: u8) -> ExitCode {
    let _ = writeln!(std::io::stderr(), "memory: {code}");
    ExitCode::from(exit)
}

struct Args {
    owner: String,
    out: PathBuf,
}

fn parse_args(args: impl Iterator<Item = OsString>) -> Option<Args> {
    let mut owner: Option<String> = None;
    let mut out: Option<PathBuf> = None;
    let mut args = args;
    while let Some(arg) = args.next() {
        if arg == "--out" {
            if out.is_some() {
                return None;
            }
            out = Some(PathBuf::from(args.next()?));
        } else {
            if owner.is_some() {
                return None;
            }
            owner = Some(arg.into_string().ok()?);
        }
    }
    Some(Args {
        owner: owner?,
        out: out?,
    })
}

/// Entry of `memory restore`; `args` are the arguments after the word `restore`.
///
/// Exit codes: 0 restored, 2 config or token refusal, 64 usage or unparsable owner, 66 owner
/// absent, 1 anything else. Failure writes one line `memory: <code>` to stderr, success one line
/// of counts. Nothing goes to stdout.
#[must_use]
pub fn main_entry(args: impl Iterator<Item = OsString>) -> ExitCode {
    let Some(args) = parse_args(args) else {
        return fail("usage", EXIT_USAGE);
    };
    let Ok(owner) = parse_owner_prefix(&args.owner) else {
        return fail("owner_text", EXIT_USAGE);
    };
    let Some(root) = crate::state::default_root() else {
        return fail("state_root", EXIT_CONFIG);
    };
    let config = match Config::load(&root.join("config.toml")) {
        Ok(config) => config,
        Err(error) => return fail(error.code(), EXIT_CONFIG),
    };
    let mut store = match build_store(&config) {
        Ok(store) => store,
        Err(error) => return fail(error.code(), error.exit_code()),
    };
    let fetch = |key: &str| match store.get(key) {
        Ok(stored) => Ok(Some(stored.body)),
        Err(StoreError::NotFound) => Ok(None),
        Err(other) => Err(other),
    };
    match restore_owner(&owner, fetch, &args.out) {
        Ok(Outcome::Absent) => fail("owner_absent", EXIT_ABSENT),
        Ok(Outcome::Restored(report)) => {
            let _ = writeln!(
                std::io::stderr(),
                "memory: restored written={} skipped_deleted={} bytes={}",
                report.written,
                report.skipped_deleted,
                report.bytes
            );
            ExitCode::SUCCESS
        }
        Err(error) => fail(&error.to_string(), EXIT_FAILED),
    }
}
