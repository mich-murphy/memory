//! `memory export <owner> --out DIR [--max-bytes N]`: write one session owner as a
//! `session-reader/v2` directory through `memory-reader`'s export library.
//!
//! Read-only towards the bucket (GET only), no daemon lock, nothing under the state root.
//! Every failure is a closed code on one stderr line; no key, path, endpoint or owner text.
//!
//! Sessions are never deleted from the store, so a deleted source's epochs are exported too, every
//! one `sealed` (none `active`). The success line is `memory: exported sources=<n> deleted=<n>
//! bytes=<n>`, `deleted` counting the deleted sources exported.
use crate::assemble::build_store;
use crate::config::Config;
use crate::daemon::EXIT_CONFIG;
use crate::environment::{Store, StoreError};
use crate::restore::{EXIT_ABSENT, EXIT_FAILED, EXIT_USAGE};
use memory_core::domain::Owner;
use memory_core::limits::parse_owner_prefix;
use memory_reader::export::{ExportError, build_envelope, write_export};
use memory_reader::{OwnerError, read_owner_head};
use std::ffi::OsString;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

/// Exit code: the export exceeds the byte budget.
pub const EXIT_TOO_LARGE: u8 = 4;
/// Default byte budget: 1 GiB.
pub const DEFAULT_MAX_BYTES: u64 = 1 << 30;
/// Largest accepted `--max-bytes`: 1 TiB.
pub const MAX_MAX_BYTES: u64 = 1 << 40;

/// Why an export did not happen. Closed; carries no input text.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum Failure {
    /// The owner has no `current.json`; nothing was created.
    #[error("owner_absent")]
    Absent,
    /// The destination exists.
    #[error("output_exists")]
    OutputExists,
    /// The store failed.
    #[error("store")]
    Store,
    /// The manifest was written by a newer schema.
    #[error("newer_schema")]
    NewerSchema,
    /// The stored chain is corrupt.
    #[error("chain_corrupt")]
    ChainCorrupt,
    /// The owner is a shared owner; the envelope needs a session id.
    #[error("owner_shared")]
    Shared,
    /// The export library refused; its code.
    #[error("{0}")]
    Export(&'static str),
    /// The export exceeds the byte budget.
    #[error("export_too_large")]
    TooLarge,
}

impl Failure {
    /// Process exit code.
    #[must_use]
    pub fn exit_code(self) -> u8 {
        match self {
            Self::Absent => EXIT_ABSENT,
            Self::TooLarge => EXIT_TOO_LARGE,
            Self::Shared => EXIT_USAGE,
            _ => EXIT_FAILED,
        }
    }
}

/// What a successful export wrote.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Report {
    /// Epoch bodies written.
    pub sources: usize,
    /// Deleted sources exported (every epoch `sealed`).
    pub deleted: usize,
    /// Body bytes.
    pub bytes: u64,
}

fn code<E>(error: &ExportError<E>) -> Failure {
    match error {
        ExportError::TooLarge => Failure::TooLarge,
        ExportError::DestinationExists => Failure::OutputExists,
        ExportError::Fetch(_) => Failure::Store,
        ExportError::Shared => Failure::Shared,
        ExportError::Overflow => Failure::Export("export_overflow"),
        ExportError::BadTime => Failure::Export("export_bad_time"),
        ExportError::TooManySources => Failure::Export("export_too_many_sources"),
        ExportError::MetadataTooLarge => Failure::Export("export_metadata_too_large"),
        ExportError::Io => Failure::Export("output_io"),
        ExportError::ObjectMissing => Failure::Export("object_missing"),
        ExportError::Corrupt => Failure::Export("source_corrupt"),
    }
}

/// Export `owner` read through `fetch` to `out`. `out` is checked before the first fetch; every
/// limit is checked before anything is created.
///
/// # Errors
/// A closed [`Failure`].
pub fn export_owner<E>(
    owner: &Owner,
    mut fetch: impl FnMut(&str) -> Result<Option<Vec<u8>>, E>,
    out: &Path,
    max_bytes: u64,
) -> Result<Report, Failure> {
    if matches!(owner, Owner::Shared { .. }) {
        return Err(Failure::Shared);
    }
    match std::fs::symlink_metadata(out) {
        Ok(_) => return Err(Failure::OutputExists),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
        Err(_) => return Err(Failure::Export("output_io")),
    }
    let head = match read_owner_head(owner, &mut fetch) {
        Ok(Some(head)) => head,
        Ok(None) => return Err(Failure::Absent),
        Err(OwnerError::Fetch(_)) => return Err(Failure::Store),
        Err(OwnerError::NewerSchema) => return Err(Failure::NewerSchema),
        Err(OwnerError::Corrupt(_)) => return Err(Failure::ChainCorrupt),
    };
    let envelope = build_envelope::<E>(&head, max_bytes).map_err(|e| code(&e))?;
    write_export(&envelope, &mut fetch, out).map_err(|e| code(&e))?;
    Ok(Report {
        sources: envelope.sources,
        deleted: envelope.deleted,
        bytes: envelope.bytes,
    })
}

fn fail(code: &str, exit: u8) -> ExitCode {
    let _ = writeln!(std::io::stderr(), "memory: {code}");
    ExitCode::from(exit)
}

struct Args {
    owner: String,
    out: PathBuf,
    max_bytes: u64,
}

fn parse_args(args: impl Iterator<Item = OsString>) -> Option<Args> {
    let (mut owner, mut out, mut max): (Option<String>, Option<PathBuf>, Option<u64>) =
        (None, None, None);
    let mut args = args;
    while let Some(arg) = args.next() {
        if arg == "--out" {
            if out.is_some() {
                return None;
            }
            out = Some(PathBuf::from(args.next()?));
        } else if arg == "--max-bytes" {
            if max.is_some() {
                return None;
            }
            let n: u64 = args.next()?.into_string().ok()?.parse().ok()?;
            if !(1..=MAX_MAX_BYTES).contains(&n) {
                return None;
            }
            max = Some(n);
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
        max_bytes: max.unwrap_or(DEFAULT_MAX_BYTES),
    })
}

/// Entry of `memory export`; `args` follow the word `export`.
///
/// Exit codes: 0 exported, 1 failed, 2 config or token, 4 too large, 64 usage, owner text or
/// shared owner, 66 owner absent. One stderr line; nothing on stdout.
#[must_use]
pub fn main_entry(args: impl Iterator<Item = OsString>) -> ExitCode {
    let Some(args) = parse_args(args) else {
        return fail("usage", EXIT_USAGE);
    };
    let Ok(owner) = parse_owner_prefix(&args.owner) else {
        return fail("owner_text", EXIT_USAGE);
    };
    if matches!(owner, Owner::Shared { .. }) {
        return fail("owner_shared", EXIT_USAGE);
    }
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
    match export_owner(&owner, fetch, &args.out, args.max_bytes) {
        Ok(report) => {
            let _ = writeln!(
                std::io::stderr(),
                "memory: exported sources={} deleted={} bytes={}",
                report.sources,
                report.deleted,
                report.bytes
            );
            ExitCode::SUCCESS
        }
        Err(failure) => fail(&failure.to_string(), failure.exit_code()),
    }
}
