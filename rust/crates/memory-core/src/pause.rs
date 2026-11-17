//! Commit pauses with full jitter (decision D44): each pause is `uniform(0, ceiling)`.
//!
//! Pure: the caller supplies the random draw and does the sleeping.
use std::time::Duration;

/// Which wait of the commit protocol is about to happen, with its retry number.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Wait {
    /// Between verification GETs after attempt `n` (0-based, so 0, 1, 2).
    Verify(u32),
    /// Before the `n`th rebase retry (1-based, so 1 to 4).
    Rebase(u32),
}

const CAP_NS: u64 = 4_000_000_000;

/// The longest the wait may be: 1 s, 2 s, 4 s for the first three of either kind, then 4 s.
/// Any out-of-range index clamps to 4 s.
pub const fn ceiling(wait: Wait) -> Duration {
    let ns = match wait {
        Wait::Verify(0) | Wait::Rebase(1) => 1_000_000_000,
        Wait::Verify(1) | Wait::Rebase(2) => 2_000_000_000,
        _ => CAP_NS,
    };
    Duration::from_nanos(ns)
}

/// A point of `[0, ceiling]` chosen by `draw`: 0 gives zero, `u64::MAX` gives the ceiling,
/// and the result never decreases as `draw` grows. Exact integer arithmetic.
pub fn pause_for(wait: Wait, draw: u64) -> Duration {
    let ceiling_ns = ceiling(wait).as_nanos();
    let ns = ceiling_ns * u128::from(draw) / u128::from(u64::MAX);
    // `ns <= ceiling_ns <= 4e9`, so it fits.
    Duration::from_nanos(u64::try_from(ns).unwrap_or(CAP_NS))
}
