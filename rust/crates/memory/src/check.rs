//! `memory check`: measures the acceptance-run exit criterion beside a running daemon.
//!
//! Read-only: no daemon lock, no directory created, nothing written under the state root and
//! nothing written to the store (GET only). It walks the configured roots with the walk the
//! daemon uses, reads each owner's chain from the store, and compares every local source with
//! the latest stored epoch, restored through `memory-reader`, against the bytes capture would
//! publish for the local file ([`crate::capture::publishable`]).
//!
//! Only owners that have at least one local row are fetched: the store has no LIST, so an owner
//! whose every file is gone cannot be found, and its stored sources are not reported as
//! `remote_only`. Cost per run: for each owner one GET of `current.json`, at most 64 manifest
//! GETs, and one GET per piece of each local source's latest epoch.
//!
//! Output is a closed vocabulary: counts, then one line per mismatch, uncaptured, withheld or
//! parked item with the owner prefix, the source kind and a reason code. Never a path, a source
//! id, file content or a credential.
use crate::assemble::{build_store, policy_context};
use crate::capture::{Source, SourceOutcome, publishable};
use crate::config::Config;
use crate::daemon::{EXIT_CONFIG, ParkedReason, Sources, StatusBody};
use crate::environment::{FileSystem, Store, StoreError};
use crate::realfs::WalkLimits;
use crate::resolve::{SharedFs, WalkSources};
use crate::roots::locator_roots;
use crate::s3::S3Store;
use crate::state::{Loaded, MissingReason, StateFile, default_root, read_durable};
use memory_core::domain::{Owner, SourceId};
use memory_core::limits::owner_prefix;
use memory_core::manifest::OwnerState;
use memory_core::privacy::{Admission, PolicyContext, UnknownReason, admit};
use memory_reader::{OwnerError, RestoreError, read_owner, restore_source};
use serde::Serialize;
use std::collections::{BTreeMap, BTreeSet};
use std::io::Write;
use std::path::Path;
use std::process::ExitCode;

/// Exit code: a mismatch, an uncaptured source or a parked owner.
pub const EXIT_FAILING: u8 = 1;
/// Exit code: usage error.
pub const EXIT_USAGE: u8 = 64;
/// Exit code: the result is unknown (store error, damaged status, incomplete walk).
pub const EXIT_UNKNOWN: u8 = 3;

/// How one local source compares with its restored latest epoch.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Compared {
    /// Equal.
    Match,
    /// Local is longer, the source is prefix-stable and the restored bytes are its prefix.
    Behind,
    /// Local is longer and not prefix-stable: the stored epoch cannot be a prefix.
    Unsettled,
    /// Anything else.
    Mismatch,
}

/// Compare `restored` with `local`, the bytes capture would publish now.
#[must_use]
pub fn compare(restored: &[u8], local: &[u8], prefix_stable: bool) -> Compared {
    if restored == local {
        Compared::Match
    } else if local.len() > restored.len() {
        if !prefix_stable {
            Compared::Unsettled
        } else if local.starts_with(restored) {
            Compared::Behind
        } else {
            Compared::Mismatch
        }
    } else {
        Compared::Mismatch
    }
}

/// Counts of one run.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Tally {
    /// Owners with at least one local row.
    pub owners: u64,
    /// Local rows.
    pub sources: u64,
    /// Rows equal to their stored latest epoch.
    pub matched: u64,
    /// Rows whose stored epoch is a prefix of the local bytes.
    pub behind: u64,
    /// Rows that moved during the read or cannot be compared yet.
    pub unsettled: u64,
    /// Rows whose stored state differs or cannot be restored.
    pub mismatch: u64,
    /// Rows with no live stored entry.
    pub uncaptured: u64,
    /// Rows policy withholds.
    pub withheld: u64,
    /// Live stored entries with no local row.
    pub remote_only: u64,
    /// Parked entries in `status.json`.
    pub parked: u64,
    /// Walk: files no pattern matched.
    pub unknown_kind: u64,
    /// Walk: files with no unambiguous owner.
    pub unknown_owner: u64,
    /// Walk: snapshot files deferred until slice 3.
    pub deferred_snapshot: u64,
    /// Walk: entries that could not be read.
    pub unreadable: u64,
    /// Store failures.
    pub store_error: u64,
}

/// A finished run.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Report {
    /// The counts.
    pub tally: Tally,
    /// `loaded`, `missing`, `damaged` or `newer_schema`.
    pub status: &'static str,
    /// One line per mismatch, uncaptured, withheld and parked item.
    pub details: Vec<String>,
    /// The walk did not cover everything.
    pub incomplete: bool,
}

impl Report {
    /// The result is unknown: a store error, an unreadable status or an incomplete walk.
    #[must_use]
    pub fn unknown(&self) -> bool {
        self.tally.store_error > 0
            || self.incomplete
            || !matches!(self.status, "loaded" | "missing")
    }

    /// Process exit code.
    #[must_use]
    pub fn exit(&self) -> u8 {
        let t = &self.tally;
        if self.unknown() {
            EXIT_UNKNOWN
        } else if t.mismatch > 0 || t.uncaptured > 0 || t.parked > 0 {
            EXIT_FAILING
        } else {
            0
        }
    }

    /// The first output line.
    #[must_use]
    pub fn summary(&self) -> String {
        let t = &self.tally;
        format!(
            "check owners={} sources={} match={} behind={} unsettled={} mismatch={} uncaptured={} withheld={} remote_only={} parked={} unknown_kind={} unknown_owner={} deferred_snapshot={} unreadable={} store_error={} status={}",
            t.owners,
            t.sources,
            t.matched,
            t.behind,
            t.unsettled,
            t.mismatch,
            t.uncaptured,
            t.withheld,
            t.remote_only,
            t.parked,
            t.unknown_kind,
            t.unknown_owner,
            t.deferred_snapshot,
            t.unreadable,
            t.store_error,
            self.status
        )
    }
}

/// A refusal before any measurement: a closed code and the exit code.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Refusal {
    /// Machine-readable code.
    pub code: String,
    /// Exit code.
    pub exit: u8,
}

enum Verdict {
    Match,
    Behind,
    Unsettled,
    Mismatch(&'static str),
    Uncaptured,
    Withheld(String),
    StoreFailed,
}

/// The snake-case code of a closed enum value: its string form, or the tag of a variant that
/// carries data (the data is never printed).
fn code_of<T: Serialize>(value: &T) -> String {
    match serde_json::to_value(value) {
        Ok(serde_json::Value::String(text)) => text,
        Ok(serde_json::Value::Object(map)) => map
            .keys()
            .next()
            .cloned()
            .unwrap_or_else(|| "unknown".to_owned()),
        _ => "unknown".to_owned(),
    }
}

const fn unknown_code(reason: UnknownReason) -> &'static str {
    match reason {
        UnknownReason::ConfigInvalid => "config_invalid",
        UnknownReason::RootUnresolvable => "root_unresolvable",
        UnknownReason::PathUnresolvable => "path_unresolvable",
        UnknownReason::RulesUnavailable => "rules_unavailable",
    }
}

fn outcome_code(outcome: &SourceOutcome) -> String {
    match outcome {
        SourceOutcome::Withheld(reason) => code_of(reason),
        SourceOutcome::Blocked(reason) => unknown_code(*reason).to_owned(),
        SourceOutcome::Unchanged
        | SourceOutcome::Raced
        | SourceOutcome::Unavailable
        | SourceOutcome::Planned
        | SourceOutcome::Parked(_) => "withheld".to_owned(),
    }
}

/// Stat, read `[0, size)`, stat again; `None` when the file moved or could not be read.
fn read_settled<F: FileSystem>(fs: &mut F, handle: u64) -> Option<Vec<u8>> {
    let before = fs.stat(handle).ok()??;
    let raw = fs.read(handle, 0..before.size).ok()?;
    let after = fs.stat(handle).ok()??;
    (before == after && u64::try_from(raw.len()) == Ok(before.size)).then_some(raw)
}

fn judge(
    fs: &mut SharedFs,
    store: &mut S3Store,
    context: &PolicyContext,
    state: Option<&OwnerState>,
    source: &Source,
) -> Verdict {
    let admitted = match admit(context, source.facts.clone()) {
        Admission::Allow(admitted) => admitted,
        Admission::Deny(reason) => return Verdict::Withheld(reason.to_string()),
        Admission::Unknown(reason) => return Verdict::Withheld(unknown_code(reason).to_owned()),
    };
    let Some(raw) = read_settled(fs, source.handle) else {
        return Verdict::Unsettled;
    };
    let body = match publishable(&admitted, &raw, context) {
        Ok(body) => body,
        Err(outcome) => return Verdict::Withheld(outcome_code(&outcome)),
    };
    let Some(entry) = state
        .and_then(|state| state.sources.get(&source.id))
        .filter(|entry| entry.deleted_at.is_none())
    else {
        return Verdict::Uncaptured;
    };
    match restore_source(entry, |key| store.get(key).map(|stored| stored.body)) {
        Ok(restored) => match compare(&restored, body.bytes(), admitted.prefix_stable()) {
            Compared::Match => Verdict::Match,
            Compared::Behind => Verdict::Behind,
            Compared::Unsettled => Verdict::Unsettled,
            Compared::Mismatch => Verdict::Mismatch("bytes"),
        },
        Err(RestoreError::Fetch(StoreError::NotFound)) => Verdict::Mismatch("restore"),
        Err(RestoreError::Fetch(_)) => Verdict::StoreFailed,
        Err(_) => Verdict::Mismatch("restore"),
    }
}

fn read_status(root: &Path) -> (Option<StatusBody>, &'static str) {
    let file = StateFile::Status;
    match read_durable(&root.join(file.name()), file.schema()) {
        Loaded::Present(stored) => match serde_json::from_slice::<StatusBody>(&stored.body) {
            Ok(body) => (Some(body), "loaded"),
            Err(_) => (None, "damaged"),
        },
        Loaded::Missing(MissingReason::NotFound) => (None, "missing"),
        Loaded::Missing(MissingReason::NewerSchema) => (None, "newer_schema"),
        Loaded::Missing(_) => (None, "damaged"),
    }
}

/// Run the check against the state root `root` (holding `config.toml` and, once the daemon has
/// run, `status.json`).
///
/// # Errors
/// A [`Refusal`] for a config, roots or token refusal.
pub fn check(root: &Path) -> Result<Report, Refusal> {
    let refuse = |code: &str, exit: u8| Refusal {
        code: code.to_owned(),
        exit,
    };
    let config_path = root.join("config.toml");
    let config = Config::load(&config_path).map_err(|e| refuse(e.code(), EXIT_CONFIG))?;
    let roots = locator_roots(&config).map_err(|e| refuse(e.code(), EXIT_CONFIG))?;
    let mut store = build_store(&config).map_err(|e| refuse(e.code(), e.exit_code()))?;
    let context = policy_context(root, &config_path, &config);

    let mut tally = Tally::default();
    let mut details = Vec::new();
    let (status_body, status) = read_status(root);
    if let Some(body) = &status_body {
        for entry in &body.parked {
            let (scope, reason) = match &entry.reason {
                ParkedReason::Owner(reason) => ("owner", code_of(reason)),
                ParkedReason::Source(reason) => ("source", code_of(reason)),
            };
            tally.parked += 1;
            details.push(format!(
                "parked owner={} reason={reason} scope={scope}",
                owner_prefix(&entry.owner)
            ));
        }
    }

    let mut fs = SharedFs::new();
    let mut walk = WalkSources::new(roots, WalkLimits::default(), fs.clone());
    let rows = walk.enumerate();
    let counts = walk.counts();
    tally.unknown_kind = counts.unknown_kind;
    tally.unknown_owner = counts.unknown_owner;
    tally.deferred_snapshot = counts.deferred_snapshot;
    tally.unreadable = counts.unreadable;
    tally.sources = u64::try_from(rows.len()).unwrap_or(u64::MAX);
    let mut groups: BTreeMap<String, (Owner, Vec<Source>)> = BTreeMap::new();
    for (owner, source) in rows {
        groups
            .entry(owner_prefix(&owner))
            .or_insert_with(|| (owner, Vec::new()))
            .1
            .push(source);
    }
    tally.owners = u64::try_from(groups.len()).unwrap_or(u64::MAX);

    for (prefix, (owner, sources)) in &groups {
        let folded = read_owner(owner, |key| match store.get(key) {
            Ok(stored) => Ok(Some(stored.body)),
            Err(StoreError::NotFound) => Ok(None),
            Err(other) => Err(other),
        });
        let state = match folded {
            Ok(folded) => folded.map(|folded| folded.state),
            Err(OwnerError::Fetch(_)) => {
                tally.store_error += 1;
                continue;
            }
            Err(OwnerError::NewerSchema | OwnerError::Corrupt(_)) => {
                for source in sources {
                    tally.mismatch += 1;
                    details.push(format!(
                        "mismatch owner={prefix} kind={} reason=chain",
                        code_of(&source.kind)
                    ));
                }
                continue;
            }
        };
        for source in sources {
            let kind = code_of(&source.kind);
            match judge(&mut fs, &mut store, &context, state.as_ref(), source) {
                Verdict::Match => tally.matched += 1,
                Verdict::Behind => tally.behind += 1,
                Verdict::Unsettled => tally.unsettled += 1,
                Verdict::Mismatch(reason) => {
                    tally.mismatch += 1;
                    details.push(format!(
                        "mismatch owner={prefix} kind={kind} reason={reason}"
                    ));
                }
                Verdict::Uncaptured => {
                    tally.uncaptured += 1;
                    details.push(format!(
                        "uncaptured owner={prefix} kind={kind} reason=no_live_entry"
                    ));
                }
                Verdict::Withheld(reason) => {
                    tally.withheld += 1;
                    details.push(format!(
                        "withheld owner={prefix} kind={kind} reason={reason}"
                    ));
                }
                Verdict::StoreFailed => tally.store_error += 1,
            }
        }
        if let Some(state) = &state {
            let local: BTreeSet<&SourceId> = sources.iter().map(|s| &s.id).collect();
            let extra = state
                .sources
                .iter()
                .filter(|(id, entry)| entry.deleted_at.is_none() && !local.contains(id))
                .count();
            tally.remote_only += u64::try_from(extra).unwrap_or(u64::MAX);
        }
    }
    Ok(Report {
        tally,
        status,
        details,
        incomplete: counts.truncated || counts.busy,
    })
}

fn fail(code: &str, exit: u8) -> ExitCode {
    let _ = writeln!(std::io::stderr(), "memory: {code}");
    ExitCode::from(exit)
}

/// Entry of `memory check`; `args` are the arguments after the word `check` (none are taken).
///
/// Exit codes: 0 clean, 1 a mismatch, an uncaptured source or a parked owner, 2 config or token
/// refusal, 3 the result is unknown (the unknown cases win over 1), 64 usage. A refusal writes
/// one line `memory: <code>` to stderr; a result goes to stdout.
#[must_use]
pub fn main_entry(mut args: impl Iterator<Item = std::ffi::OsString>) -> ExitCode {
    if args.next().is_some() {
        return fail("usage", EXIT_USAGE);
    }
    let Some(root) = default_root() else {
        return fail("state_root", EXIT_CONFIG);
    };
    match check(&root) {
        Err(refusal) => fail(&refusal.code, refusal.exit),
        Ok(report) => {
            let mut out = std::io::stdout().lock();
            let _ = writeln!(out, "{}", report.summary());
            for line in &report.details {
                let _ = writeln!(out, "{line}");
            }
            ExitCode::from(report.exit())
        }
    }
}
