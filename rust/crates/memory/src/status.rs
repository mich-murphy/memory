//! `memory status`: the local health view, in two tiers (DESIGN.md 11.3).
//!
//! Read-only and offline: no daemon lock, nothing created or written under the state root and no
//! network request of any method (the store is never built and the token file is not read). It
//! reads `config.toml`, `status.json` and `index.json` with [`crate::check::read_state`], walks
//! the configured roots with the daemon's walk and stats each source. No byte of a source is
//! read.
//!
//! Tier 1 compares each walked source with its `index.json` entry: `current`, `behind`
//! (appended since the last capture), `changed` (another file, or shorter than captured),
//! `unindexed` (no entry). `index.json` is written at most every 60 s, so a source captured in
//! the last minute may still show as `behind`. Tier 2 is `status.json`: age, rounds, host pause,
//! failure and audit counts and the parked entries. The verified view, which fetches every piece
//! from the store, is `memory check`; `status --verify` is a usage error.
//!
//! Output is a closed vocabulary: counts, owner prefixes and reason codes. Never a path, a
//! source id, content or a credential, and never a claim that everything is captured.
use crate::assemble::policy_context;
use crate::capture::Source;
use crate::check::{Refusal, parked_line, read_state};
use crate::clock::SystemClock;
use crate::config::Config;
use crate::daemon::{EXIT_CONFIG, IndexBody, SourceIndex, StatusBody};
use crate::environment::{Clock, FileSystem, Stat};
use crate::realfs::WalkLimits;
use crate::resolve::{SharedFs, WalkSources};
use crate::roots::locator_roots;
use crate::state::{StateFile, default_root};
use memory_core::domain::{SnapshotFingerprint, SourceClass};
use memory_core::privacy::{Admission, admit};
use std::io::Write;
use std::path::Path;
use std::process::ExitCode;

/// Exit code: something is parked, the host is paused or `status.json` is not loaded.
pub const EXIT_ATTENTION: u8 = 1;
/// Exit code: usage error.
pub const EXIT_USAGE: u8 = 64;

/// Tier-1 counts.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Sources {
    /// Walked sources.
    pub total: u64,
    /// Equal to the indexed position.
    pub current: u64,
    /// Longer than the indexed position.
    pub behind: u64,
    /// A different file or shorter than the indexed position.
    pub changed: u64,
    /// No index entry.
    pub unindexed: u64,
    /// Policy withholds them (judged without a read).
    pub withheld: u64,
    /// Seconds since the oldest `behind` or `changed` source was last observed.
    pub oldest_behind_s: u64,
    /// Walk: files no pattern matched.
    pub unknown_kind: u64,
    /// Walk: files with no unambiguous owner.
    pub unknown_owner: u64,
    /// Walk: duplicates of another source id.
    pub duplicate: u64,
    /// Walk: snapshot files other than Codex SQLite stores, withheld.
    pub deferred_snapshot: u64,
    /// Walk entries or stats that failed.
    pub unreadable: u64,
}

/// A finished run.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Report {
    /// Output lines.
    pub lines: Vec<String>,
    /// Exit code.
    pub exit: u8,
}

enum Tier {
    Current,
    Behind(i64),
    Changed(i64),
    Unindexed,
}

fn append_tier(entry: Option<&SourceIndex>, stat: &Stat) -> Tier {
    let Some(state) = entry.and_then(|e| e.index.as_ref()) else {
        return Tier::Unindexed;
    };
    let at = state.observed.observed_at_ns;
    let to = state.position.captured_to.get();
    if state.observed.id != stat.id || stat.size < to {
        Tier::Changed(at)
    } else if stat.size > to {
        Tier::Behind(at)
    } else {
        Tier::Current
    }
}

fn snapshot_tier(entry: Option<&SourceIndex>, stat: &Stat) -> Tier {
    let Some((fingerprint, _)) = entry.and_then(|e| e.snapshot.as_ref()) else {
        return Tier::Unindexed;
    };
    let fp = match fingerprint {
        SnapshotFingerprint::Plain(fp) | SnapshotFingerprint::Sqlite { db: fp, .. } => fp,
    };
    if fp.id == stat.id && fp.size == stat.size && fp.mtime_ns == stat.mtime_ns {
        Tier::Current
    } else {
        Tier::Changed(i64::MAX)
    }
}

/// Seconds since the Unix epoch of `YYYY-MM-DDTHH:MM:SSZ`.
fn epoch_of(text: &str) -> Option<i64> {
    let num = |from: usize, to: usize| text.get(from..to)?.parse::<i64>().ok();
    let (y, m, d) = (num(0, 4)?, num(5, 7)?, num(8, 10)?);
    let y = y - i64::from(m <= 2);
    let era = y.div_euclid(400);
    let yoe = y.rem_euclid(400);
    let doy = (153 * (if m > 2 { m - 3 } else { m + 9 }) + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    let days = era * 146_097 + doe - 719_468;
    Some(days * 86_400 + num(11, 13)? * 3600 + num(14, 16)? * 60 + num(17, 19)?)
}

/// Run the status against the state root `root`.
///
/// # Errors
/// A [`Refusal`] for a config or roots refusal.
pub fn status(root: &Path) -> Result<Report, Refusal> {
    let refuse = |code: &str| Refusal {
        code: code.to_owned(),
        exit: EXIT_CONFIG,
    };
    let config_path = root.join("config.toml");
    let config = Config::load(&config_path).map_err(|e| refuse(e.code()))?;
    let roots = locator_roots(&config).map_err(|e| refuse(e.code()))?;
    let context = policy_context(root, &config_path, &config);
    let (status, status_code) = read_state::<StatusBody>(root, StateFile::Status);
    let (index, index_code) = read_state::<IndexBody>(root, StateFile::Index);
    let index = index.unwrap_or_default();
    let now_ns = i64::try_from(SystemClock::new().wall_ns()).unwrap_or(i64::MAX);

    let mut fs = SharedFs::new();
    let mut walk = WalkSources::new(roots, WalkLimits::default(), fs.clone());
    let rows = crate::daemon::Sources::enumerate(&mut walk);
    let counts = walk.counts();
    let mut s = Sources {
        total: u64::try_from(rows.len()).unwrap_or(u64::MAX),
        unknown_kind: counts.unknown_kind,
        unknown_owner: counts.unknown_owner,
        duplicate: counts.duplicate,
        deferred_snapshot: counts.deferred_snapshot,
        unreadable: counts.unreadable,
        ..Sources::default()
    };
    let mut oldest_ns: i64 = 0;
    for (_, source) in &rows {
        classify(
            &mut fs,
            &context,
            &index,
            source,
            now_ns,
            &mut s,
            &mut oldest_ns,
        );
    }
    s.oldest_behind_s = u64::try_from(oldest_ns / 1_000_000_000).unwrap_or(0);

    let now_s = now_ns / 1_000_000_000;
    let (as_of, age) = status
        .as_ref()
        .map(|b| {
            let age = epoch_of(&b.updated_at).map_or(0, |at| now_s.saturating_sub(at).max(0));
            (b.updated_at.clone(), age)
        })
        .unwrap_or_else(|| ("none".to_owned(), 0));
    let body = status.unwrap_or_default();
    let mut lines = vec![
        format!(
            "status as_of={as_of} age_s={age} rounds={} host_paused={} state={status_code} index={index_code}",
            body.rounds, body.host_paused
        ),
        format!(
            "sources total={} current={} behind={} changed={} unindexed={} withheld={} oldest_behind_s={} unknown_kind={} unknown_owner={} duplicate={} deferred_snapshot={} unreadable={}",
            s.total,
            s.current,
            s.behind,
            s.changed,
            s.unindexed,
            s.withheld,
            s.oldest_behind_s,
            s.unknown_kind,
            s.unknown_owner,
            s.duplicate,
            s.deferred_snapshot,
            s.unreadable
        ),
        format!(
            "failures offline={} throttled={} transient={} destination_auth={} local={} deterministic={} audit_matched={} audit_mismatched={} audit_ineligible={}",
            body.failures.offline,
            body.failures.throttled,
            body.failures.transient,
            body.failures.destination_auth,
            body.failures.local,
            body.failures.deterministic,
            body.audit.matched,
            body.audit.mismatched,
            body.audit.ineligible
        ),
    ];
    lines.extend(body.parked.iter().map(parked_line));
    let attention = !body.parked.is_empty() || body.host_paused || status_code != "loaded";
    Ok(Report {
        lines,
        exit: u8::from(attention),
    })
}

fn classify(
    fs: &mut SharedFs,
    context: &memory_core::privacy::PolicyContext,
    index: &IndexBody,
    source: &Source,
    now_ns: i64,
    s: &mut Sources,
    oldest_ns: &mut i64,
) {
    if !matches!(admit(context, source.facts.clone()), Admission::Allow(_)) {
        s.withheld += 1;
        return;
    }
    let Ok(Some(stat)) = fs.stat(source.handle) else {
        s.unreadable += 1;
        return;
    };
    let entry = index.sources.get(&source.id);
    let tier = match source.class {
        SourceClass::Append => append_tier(entry, &stat),
        SourceClass::Snapshot => snapshot_tier(entry, &stat),
    };
    match tier {
        Tier::Current => s.current += 1,
        Tier::Unindexed => s.unindexed += 1,
        Tier::Behind(at) | Tier::Changed(at) => {
            if matches!(tier, Tier::Behind(_)) {
                s.behind += 1;
            } else {
                s.changed += 1;
            }
            if at != i64::MAX {
                *oldest_ns = (*oldest_ns).max(now_ns.saturating_sub(at));
            }
        }
    }
}

fn fail(code: &str, exit: u8) -> ExitCode {
    let _ = writeln!(std::io::stderr(), "memory: {code}");
    ExitCode::from(exit)
}

/// Entry of `memory status`; `args` follow the word `status` and must be empty (`--verify` is
/// not built: `memory check` is the verified view).
///
/// Exit codes: 0 healthy, 1 something is parked, the host is paused or `status.json` is not
/// loaded, 2 config refusal, 64 usage.
#[must_use]
pub fn main_entry(mut args: impl Iterator<Item = std::ffi::OsString>) -> ExitCode {
    if args.next().is_some() {
        return fail("usage", EXIT_USAGE);
    }
    let Some(root) = default_root() else {
        return fail("state_root", EXIT_CONFIG);
    };
    match status(&root) {
        Err(refusal) => fail(&refusal.code, refusal.exit),
        Ok(report) => {
            let mut out = std::io::stdout().lock();
            for line in &report.lines {
                let _ = writeln!(out, "{line}");
            }
            ExitCode::from(report.exit)
        }
    }
}
