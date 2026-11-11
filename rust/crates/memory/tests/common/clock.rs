//! Fixed `Clock` for the daemon integration tests (see `store.rs` for why each concern is its
//! own file).
use memory::environment::Clock;

/// The wall-clock instant (nanoseconds since the Unix epoch) every test run starts at.
pub const WALL: i128 = 1_794_000_000_000_000_000;

/// A clock that stands still: tests move time by building a new one.
pub struct TestClock {
    /// Monotonic nanoseconds.
    pub mono: u64,
    /// Wall-clock nanoseconds since the Unix epoch.
    pub wall: i128,
}

impl TestClock {
    /// A clock at monotonic time `mono` and the default wall time.
    pub const fn at(mono: u64) -> Self {
        Self { mono, wall: WALL }
    }
}

impl Clock for TestClock {
    fn monotonic_ns(&self) -> u64 {
        self.mono
    }
    fn wall_ns(&self) -> i128 {
        self.wall
    }
}
