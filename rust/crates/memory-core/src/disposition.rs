//! Pure attempt-outcome to disposition transition (DESIGN.md sections 5 and 11.2-11.4).
//! The shell supplies a monotonic offset `at`; nothing here reads a clock.
use crate::domain::{Disposition, ErrorClass, Fault, OwnerParkReason, ParkReason};
use std::time::Duration;

/// Consecutive deterministic failures of one source before it parks (DESIGN.md 11.2).
pub const PARK_THRESHOLD: u32 = 3;
/// Minimum span from the first to the latest failure of the streak (DESIGN.md 11.2).
pub const PARK_MIN_SPAN: Duration = Duration::from_secs(3 * 60 * 60);
/// First retry delay.
pub const BACKOFF_MIN: Duration = Duration::from_secs(1);
/// Largest retry delay.
pub const BACKOFF_MAX: Duration = Duration::from_secs(15 * 60);

/// What one attempt ended with. A cancel/failure race is reported as the failure.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Outcome {
    /// Attempt completed.
    Succeeded,
    /// Attempt stopped only because shutdown was requested.
    Aborted,
    /// The operation deadline passed; this is not shutdown.
    TimedOut,
    /// Classified source or host failure.
    Failed(ErrorClass),
    /// Owner-scoped integrity failure (foreign writer, newer schema).
    OwnerFault(OwnerParkReason),
}

/// Per-source failure bookkeeping, persisted by the shell.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Streak {
    /// Consecutive deterministic failures of `fault`.
    pub deterministic: u32,
    /// Fault the streak counts.
    pub fault: Option<Fault>,
    /// Monotonic offset of the streak's first failure.
    pub first_at: Option<Duration>,
    /// Failed attempts since the last success, driving backoff.
    pub retries: u32,
}

/// Source park reason for a fault. Every deterministic fault parks (DESIGN 11.2).
pub const fn park_reason(fault: &Fault) -> ParkReason {
    match fault {
        Fault::AuditMismatch => ParkReason::RepeatedAuditMismatch,
        Fault::ReadbackMismatch => ParkReason::RepeatedReadbackMismatch,
        Fault::SourceUnstable => ParkReason::SourceUnstable,
        Fault::NonJsonResponse => ParkReason::RepeatedNonJsonResponse,
        Fault::Schema => ParkReason::RepeatedSchemaError,
    }
}

/// Exponential backoff for the nth failed attempt (0-based), 1 s to 15 min, no jitter.
pub fn backoff(retries: u32) -> Duration {
    let secs = 1_u64 << retries.min(20);
    Duration::from_secs(secs).clamp(BACKOFF_MIN, BACKOFF_MAX)
}

fn retry(class: ErrorClass, prior: &Streak) -> Disposition {
    Disposition::Retry {
        after: backoff(prior.retries),
        class,
    }
}

/// Map an attempt and the prior streak to a disposition and the next streak.
///
/// `cancel_requested` is true when shutdown was requested during the attempt. It swallows
/// only retryable failures; success, owner faults, deterministic faults and host auth
/// failures keep their own disposition.
pub fn transition(
    outcome: &Outcome,
    cancel_requested: bool,
    at: Duration,
    prior: &Streak,
) -> (Disposition, Streak) {
    let bumped = Streak {
        retries: prior.retries.saturating_add(1),
        ..prior.clone()
    };
    let cancelled = (Disposition::Cancelled, prior.clone());
    match outcome {
        Outcome::Succeeded => (Disposition::Done, Streak::default()),
        Outcome::Aborted => cancelled,
        Outcome::TimedOut if cancel_requested => cancelled,
        Outcome::TimedOut => (retry(ErrorClass::Offline, prior), bumped),
        Outcome::OwnerFault(reason) => (
            Disposition::ParkOwner {
                reason: reason.clone(),
            },
            prior.clone(),
        ),
        Outcome::Failed(class) => match class {
            ErrorClass::DestinationAuth => (
                Disposition::PauseHost {
                    class: class.clone(),
                },
                prior.clone(),
            ),
            ErrorClass::Offline | ErrorClass::Throttled | ErrorClass::Local(_) => {
                if cancel_requested {
                    cancelled
                } else {
                    (retry(class.clone(), prior), bumped)
                }
            }
            ErrorClass::Deterministic(fault) => {
                let (count, first) = match (&prior.fault, prior.first_at) {
                    (Some(f), Some(t)) if f == fault => (prior.deterministic.saturating_add(1), t),
                    _ => (1, at),
                };
                let next = Streak {
                    deterministic: count,
                    fault: Some(fault.clone()),
                    first_at: Some(first),
                    retries: bumped.retries,
                };
                let span_ok = at.saturating_sub(first) >= PARK_MIN_SPAN;
                let disposition = if count >= PARK_THRESHOLD && span_ok {
                    Disposition::Park {
                        reason: park_reason(fault),
                    }
                } else {
                    retry(class.clone(), prior)
                };
                (disposition, next)
            }
        },
    }
}
