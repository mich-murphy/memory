//! The bounded audit's pure rules (DESIGN.md 11.4, measure 2).
//!
//! The shell in [`crate::capture`] does the reading: a fresh read of `[0, size)` through the
//! `FileSystem` seam, transformed as capture transforms a whole stream, hashed with a fresh
//! hasher. This module decides when an audit is due, how two reads and the committed digest
//! become [`AuditEvidence`], and how a capture carries the per-source [`AuditState`].
use memory_core::disposition::{Outcome, Pacing, Streak, transition_paced};
use memory_core::domain::{
    AuditState, Disposition, ErrorClass, Fault, Observed, ParkReason, Sha256, SourceState,
};
use memory_core::planner::AuditEvidence;
use serde::{Deserialize, Serialize};
use std::time::Duration;

/// Audit verdicts of a capture step, a round or (in `status.json`) since the status file was
/// created. A count is of verdicts reached, whether or not the owner's commit then lands (as
/// `FailureCounts` counts attempts).
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AuditCounts {
    /// Audits whose read compared equal to the committed digest (first or second read).
    pub matched: u64,
    /// New epochs started by an audit mismatch: a stable mismatch, or a non-resumed extension
    /// whose re-read prefix does not hash to the committed digest.
    pub mismatched: u64,
    /// Due audits that reached no verdict: stat pre-check failed, the source moved between the
    /// stats, two reads differed, or a read or transform failed.
    pub ineligible: u64,
}

impl AuditCounts {
    /// Add `other` to `self`, saturating.
    pub fn add(&mut self, other: &Self) {
        self.matched = self.matched.saturating_add(other.matched);
        self.mismatched = self.mismatched.saturating_add(other.mismatched);
        self.ineligible = self.ineligible.saturating_add(other.ineligible);
    }

    /// Count one verdict's evidence: a match or an ineligible audit; a mismatch is counted when
    /// the epoch it starts is.
    pub fn verdict(&mut self, evidence: AuditEvidence) {
        match evidence {
            AuditEvidence::Matched => self.matched = self.matched.saturating_add(1),
            AuditEvidence::Ineligible => self.ineligible = self.ineligible.saturating_add(1),
            AuditEvidence::NotDue
            | AuditEvidence::RecheckRequired
            | AuditEvidence::StableMismatch => {}
        }
    }

    /// Count one new epoch started by an audit mismatch.
    pub fn mismatch(&mut self) {
        self.mismatched = self.mismatched.saturating_add(1);
    }
}

/// Time after the last audit at which a source captured since then is audited again.
pub const AUDIT_INTERVAL_NS: i64 = 24 * 3_600 * 1_000_000_000;

/// Bookkeeping of a source that was never audited in its epoch.
pub const fn never() -> AuditState {
    AuditState {
        last_checked_at_ns: None,
        consecutive_failures: 0,
        first_failure_at_ns: None,
    }
}

/// Whether an audit is due. `observed_at_ns` is the wall time of the stored stat (the last
/// capture or stat refresh), `now_ns` the wall time of this step.
///
/// Due when never audited in this epoch; or when the source was captured or refreshed after
/// its last audit and that audit is at least [`AUDIT_INTERVAL_NS`] old or ahead of the wall
/// clock. A source audited and not captured since is not due, however old the audit is.
pub fn due(audit: &AuditState, observed_at_ns: i64, now_ns: i64) -> bool {
    let Some(last) = audit.last_checked_at_ns else {
        return true;
    };
    observed_at_ns > last && (now_ns < last || now_ns.saturating_sub(last) >= AUDIT_INTERVAL_NS)
}

/// Evidence of the first read: `None` is a read whose stats were not stable.
pub fn first(committed: Sha256, read: Option<Sha256>) -> AuditEvidence {
    match read {
        None => AuditEvidence::Ineligible,
        Some(digest) if digest == committed => AuditEvidence::Matched,
        Some(_) => AuditEvidence::RecheckRequired,
    }
}

/// Evidence of the independent second read. A stable mismatch needs the two reads to agree
/// with each other; two different reads are a source that moved, not a verdict.
pub fn second(committed: Sha256, first_read: Sha256, read: Option<Sha256>) -> AuditEvidence {
    match read {
        Some(digest) if digest == committed => AuditEvidence::Matched,
        Some(digest) if digest == first_read => AuditEvidence::StableMismatch,
        Some(_) | None => AuditEvidence::Ineligible,
    }
}

/// The bookkeeping after a match.
pub const fn matched(now_ns: i64) -> AuditState {
    AuditState {
        last_checked_at_ns: Some(now_ns),
        consecutive_failures: 0,
        first_failure_at_ns: None,
    }
}

/// What an audit mismatch leads to.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Mismatch {
    /// Start a new epoch, storing this bookkeeping with it.
    Epoch(AuditState),
    /// The streak is long enough: park the source and start nothing.
    Park(ParkReason),
}

/// The decision at an audit mismatch, by the core's park rule: [`transition_paced`] with a
/// deterministic [`Fault::AuditMismatch`] and a [`Streak`] built from `previous` (its failure
/// count and the wall time of its first failure, which stand for the streak's offsets). A wall
/// clock behind the first failure gives a span of zero, so no park. An entry that counts
/// failures but has no first-failure time starts its span now.
pub fn mismatch(previous: &AuditState, now_ns: i64) -> Mismatch {
    let offset = |ns: i64| Duration::from_nanos(u64::try_from(ns).unwrap_or(0));
    let live = previous.consecutive_failures > 0;
    let first_ns = if live {
        previous.first_failure_at_ns.unwrap_or(now_ns).min(now_ns)
    } else {
        now_ns
    };
    let prior = if live {
        Streak {
            deterministic: previous.consecutive_failures,
            fault: Some(Fault::AuditMismatch),
            first_at: Some(offset(first_ns)),
            retries: 0,
        }
    } else {
        Streak::default()
    };
    let outcome = Outcome::Failed(ErrorClass::Deterministic(Fault::AuditMismatch));
    let (disposition, next) =
        transition_paced(&outcome, false, offset(now_ns), &prior, Pacing::FLOOR);
    if let Disposition::Park { reason } = disposition {
        return Mismatch::Park(reason);
    }
    Mismatch::Epoch(AuditState {
        last_checked_at_ns: None,
        consecutive_failures: next.deterministic,
        first_failure_at_ns: Some(if live {
            previous.first_failure_at_ns.unwrap_or(now_ns)
        } else {
            now_ns
        }),
    })
}

/// The index entry after a round that publishes nothing: the audit bookkeeping, and the fresh
/// stat when the planner asked for a refresh. A refresh that moved the mtime at an unchanged
/// size clears the last audit, so the next round audits; one that only settles a racy stat
/// does not.
pub fn refreshed(
    state: &SourceState,
    observed: &Observed,
    refresh_stat: bool,
    audit: AuditState,
) -> SourceState {
    let mut next = state.clone();
    next.audit = audit;
    if refresh_stat {
        if observed.mtime_ns != state.observed.mtime_ns {
            next.audit.last_checked_at_ns = None;
        }
        next.observed = observed.clone();
    }
    next
}
