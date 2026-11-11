//! Pure reconcile decisions from independently acquired evidence.
use crate::domain::{
    EpochPosition, Observed, Offset, Plan, ReplayReason, RulesDigest, Sha256, SnapshotFingerprint,
    SourceState, StatTrust, WithholdReason,
};

/// Head-window byte limit from DESIGN §7.2, not a configurable safety claim.
pub const HEAD_WINDOW_BYTES: u64 = 4096;
/// Tail-window byte limit from DESIGN §7.2.
pub const TAIL_WINDOW_BYTES: u64 = 65536;
/// Exact byte ranges a shell must read before supplying window evidence.
/// Small-file overlap is intentional; this is bounded evidence, not full integrity.
pub fn window_ranges(captured_to: Offset) -> (std::ops::Range<Offset>, std::ops::Range<Offset>) {
    let at = captured_to.get();
    (
        Offset::new(0)..Offset::new(at.min(HEAD_WINDOW_BYTES)),
        Offset::new(at.saturating_sub(TAIL_WINDOW_BYTES))..captured_to,
    )
}

/// Independent head and tail digests read at the *old committed* byte boundary.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WindowEvidence {
    /// Hash of `[0, min(4096, captured_to))`.
    pub head: Sha256,
    /// Hash of `[captured_to.saturating_sub(65536), captured_to)`.
    pub tail: Sha256,
}
/// Audit result, never inferred from bounded windows or resumable hash state.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AuditEvidence {
    /// No full check requested.
    NotDue,
    /// Identity, size or mtime changed; no audit comparison is valid.
    Ineligible,
    /// Fresh full-prefix digest matched under a stable pinned observation.
    Matched,
    /// Initial mismatch requires a second independent stable read.
    RecheckRequired,
    /// Two fresh full-prefix reads stably disagree with committed digest.
    StableMismatch,
}
/// Whether a full audit compares the same physical input as the capture.
pub fn audit_eligible(captured: &Observed, before: &Observed, after: &Observed) -> bool {
    captured.id == before.id
        && before.id == after.id
        && captured.size == before.size
        && before.size == after.size
        && captured.mtime_ns == before.mtime_ns
        && before.mtime_ns == after.mtime_ns
}
/// Source observation; absence is counted across reconciles by the shell.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Observation {
    /// Local absence has already been committed remotely; repeating it is no work.
    DeletionRecorded,
    /// First missing reconcile: not deletion evidence (DESIGN 7.2).
    MissingOnce,
    /// Missing on two consecutive reconciles: the deletion evidence (DESIGN 7.2).
    MissingConfirmed,
    /// Present source with physical fingerprint when snapshot-class.
    Present(Observed),
}
impl Observation {
    /// Classify a count of consecutive missing reconciles including this one (0 and 1 are
    /// not deletion evidence; 2 or more is).
    pub const fn missing(consecutive: u32) -> Self {
        if consecutive >= 2 {
            Self::MissingConfirmed
        } else {
            Self::MissingOnce
        }
    }
}
/// What an append-class source is known by; only valid combinations are representable.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum AppendBasis {
    /// Rebuildable local index agrees with the remote position.
    Indexed {
        /// Rebuildable local state.
        state: Box<SourceState>,
        /// Fresh old-prefix window hashes, not cached hashes repeated as evidence.
        windows: Option<WindowEvidence>,
    },
    /// No index, but a committed remote epoch exists and must be adopted or replayed.
    Unindexed {
        /// Committed remote epoch.
        remote: EpochPosition,
        /// Full fresh local digest of remote `[0, N)` for adoption.
        adopt_digest: Option<Sha256>,
    },
    /// Neither index nor remote epoch: nothing was ever captured.
    Fresh,
}
/// Source strategy and its independent cache/evidence.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Strategy {
    /// Logical append stream. Physical decompression is a shell concern.
    Append(AppendBasis),
    /// Full snapshot, including both DB and WAL for SQLite.
    Snapshot {
        /// Previously captured physical inputs.
        previous: Option<SnapshotFingerprint>,
        /// Current physical inputs.
        current: SnapshotFingerprint,
        /// Previously applied protected transform rules.
        previous_rules: Option<RulesDigest>,
    },
}
/// Fully explicit pure planning inputs; writer version deliberately absent.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PlanInput {
    /// Admission denial already established by privacy policy.
    pub withheld: Option<WithholdReason>,
    /// Current stat or consecutive absence.
    pub observation: Observation,
    /// Strategy, cache and independent digest evidence.
    pub strategy: Strategy,
    /// Current applicable protected rules; None for unprotected sources.
    pub rules: Option<RulesDigest>,
    /// Full audit evidence with explicit eligibility/recheck state.
    pub audit: AuditEvidence,
}
/// Planning never disguises missing evidence as successful verification.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Planning {
    /// Shell must read the old committed head and tail windows.
    NeedWindows,
    /// Shell must perform a second stable full-prefix audit read.
    NeedAuditRecheck,
    /// Decision plus explicit local-only cache updates.
    Ready {
        /// Capture operation, if any.
        plan: Plan,
        /// Record fresh stat even when matching racy windows produce no capture.
        refresh_stat: bool,
        /// Rebuild index at this verified remote position before append/no-op.
        adopted: Option<EpochPosition>,
    },
}
fn ready(plan: Plan, refresh_stat: bool, adopted: Option<EpochPosition>) -> Planning {
    Planning::Ready {
        plan,
        refresh_stat,
        adopted,
    }
}
fn replay(reason: ReplayReason) -> Planning {
    ready(Plan::Replay { reason }, false, None)
}
/// Decide work without touching source bytes, clocks, remote state or privacy capabilities.
///
/// Windows deliberately do not certify interior bytes. `StableMismatch` is the
/// only full-audit repair trigger; ineligible audits make no liveness claim.
pub fn plan(input: &PlanInput) -> Planning {
    if let Some(reason) = &input.withheld {
        return ready(
            Plan::Withheld {
                reason: reason.clone(),
            },
            false,
            None,
        );
    }
    let observed = match &input.observation {
        Observation::DeletionRecorded => return ready(Plan::Unchanged, false, None),
        Observation::MissingOnce => return ready(Plan::Unchanged, false, None),
        Observation::MissingConfirmed => return ready(Plan::Deleted, false, None),
        Observation::Present(observed) => observed,
    };
    match &input.strategy {
        Strategy::Snapshot {
            previous,
            current,
            previous_rules,
        } => {
            if previous.is_some() && previous_rules != &input.rules {
                return replay(ReplayReason::RulesChanged);
            }
            match input.audit {
                AuditEvidence::StableMismatch => return replay(ReplayReason::AuditMismatch),
                AuditEvidence::RecheckRequired => return Planning::NeedAuditRecheck,
                AuditEvidence::NotDue | AuditEvidence::Ineligible | AuditEvidence::Matched => {}
            }
            ready(
                if previous.as_ref() == Some(current) {
                    Plan::Unchanged
                } else {
                    Plan::Snapshot
                },
                false,
                None,
            )
        }
        Strategy::Append(AppendBasis::Fresh) => replay(ReplayReason::FirstCapture),
        Strategy::Append(AppendBasis::Unindexed {
            remote,
            adopt_digest,
        }) => {
            if observed.size < remote.captured_to.get() {
                return replay(ReplayReason::Truncated);
            }
            if remote.rules != input.rules {
                return replay(ReplayReason::RulesChanged);
            }
            let Some(digest) = adopt_digest else {
                return ready(
                    Plan::Adopt {
                        remote: remote.clone(),
                    },
                    false,
                    None,
                );
            };
            if digest != &remote.whole {
                return replay(ReplayReason::AdoptMismatch);
            }
            ready(
                extension(remote.captured_to, observed.size),
                true,
                Some(remote.clone()),
            )
        }
        Strategy::Append(AppendBasis::Indexed { state, windows }) => {
            if observed.id != state.observed.id {
                return replay(ReplayReason::IdentityChanged);
            }
            if observed.size < state.position.captured_to.get() {
                return replay(ReplayReason::Truncated);
            }
            if input.rules != state.position.rules {
                return replay(ReplayReason::RulesChanged);
            }
            match input.audit {
                AuditEvidence::StableMismatch => return replay(ReplayReason::AuditMismatch),
                AuditEvidence::RecheckRequired => return Planning::NeedAuditRecheck,
                AuditEvidence::NotDue | AuditEvidence::Ineligible | AuditEvidence::Matched => {}
            }
            let equal_size = observed.size == state.position.captured_to.get();
            if equal_size
                && observed.mtime_ns == state.observed.mtime_ns
                && state.observed.trust() == StatTrust::Settled
            {
                return ready(Plan::Unchanged, false, None);
            }
            let Some(windows) = windows else {
                return Planning::NeedWindows;
            };
            if windows.head != state.head || windows.tail != state.tail {
                return replay(ReplayReason::WindowMismatch);
            }
            ready(
                extension(state.position.captured_to, observed.size),
                true,
                None,
            )
        }
    }
}
fn extension(from: Offset, size: u64) -> Plan {
    if from.get() == size {
        Plan::Unchanged
    } else {
        Plan::Append {
            from,
            to: Offset::new(size),
        }
    }
}
