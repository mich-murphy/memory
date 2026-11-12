//! Production [`Clock`]: monotonic time from [`Instant`], wall time from [`SystemTime`].
//!
//! DESIGN.md 7.1 requires a monotonic clock that excludes suspend. The std documentation says
//! `Instant` is `CLOCK_UPTIME_RAW` on Darwin and `CLOCK_MONOTONIC` on Linux, and both exclude
//! suspend (read 2026-11-12). std also says this may change between versions, so the claim
//! is tied to the toolchain the workspace pins: Rust 1.99.0 (`flake.nix`). Revisit it when
//! the pin moves.
//!
//! The monotonic origin is the moment of construction, so a reading is meaningful only
//! within one process and must never be persisted.
use crate::environment::Clock;
use std::time::{Instant, SystemTime, UNIX_EPOCH};

/// The production clock.
#[derive(Debug, Clone, Copy)]
pub struct SystemClock {
    origin: Instant,
}

impl SystemClock {
    /// A clock whose monotonic origin is now.
    #[must_use]
    pub fn new() -> Self {
        Self {
            origin: Instant::now(),
        }
    }
}

impl Default for SystemClock {
    fn default() -> Self {
        Self::new()
    }
}

/// Nanoseconds of `time` relative to the Unix epoch; negative before it. Saturates rather
/// than panics.
#[must_use]
pub fn wall_ns_of(time: SystemTime) -> i128 {
    match time.duration_since(UNIX_EPOCH) {
        Ok(after) => i128::try_from(after.as_nanos()).unwrap_or(i128::MAX),
        Err(before) => {
            i128::try_from(before.duration().as_nanos()).map_or(i128::MIN, |nanos| -nanos)
        }
    }
}

impl Clock for SystemClock {
    fn monotonic_ns(&self) -> u64 {
        u64::try_from(self.origin.elapsed().as_nanos()).unwrap_or(u64::MAX)
    }

    fn wall_ns(&self) -> i128 {
        wall_ns_of(SystemTime::now())
    }
}
