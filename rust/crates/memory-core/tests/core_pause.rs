//! Commit pauses (decision D44): full jitter, `uniform(0, ceiling)`.
use memory_core::pause::{Wait, ceiling, pause_for};
use proptest::prelude::*;
use std::time::Duration;

const S: fn(u64) -> Duration = Duration::from_secs;

const WAITS: [Wait; 7] = [
    Wait::Verify(0),
    Wait::Verify(1),
    Wait::Verify(2),
    Wait::Rebase(1),
    Wait::Rebase(2),
    Wait::Rebase(3),
    Wait::Rebase(4),
];

#[test]
fn the_ceiling_table_is_exact() {
    assert_eq!(ceiling(Wait::Verify(0)), S(1));
    assert_eq!(ceiling(Wait::Verify(1)), S(2));
    assert_eq!(ceiling(Wait::Verify(2)), S(4));
    assert_eq!(ceiling(Wait::Rebase(1)), S(1));
    assert_eq!(ceiling(Wait::Rebase(2)), S(2));
    assert_eq!(ceiling(Wait::Rebase(3)), S(4));
    assert_eq!(ceiling(Wait::Rebase(4)), S(4));
}

#[test]
fn out_of_range_indexes_clamp_to_four_seconds() {
    for wait in [
        Wait::Verify(3),
        Wait::Verify(u32::MAX),
        Wait::Rebase(5),
        Wait::Rebase(u32::MAX),
        Wait::Rebase(0),
    ] {
        assert_eq!(ceiling(wait), S(4), "{wait:?}");
    }
}

#[test]
fn the_extreme_draws_give_zero_and_exactly_the_ceiling() {
    for wait in WAITS {
        assert_eq!(pause_for(wait, 0), Duration::ZERO, "{wait:?}");
        assert_eq!(pause_for(wait, u64::MAX), ceiling(wait), "{wait:?}");
    }
}

#[test]
fn the_variants_are_not_interchangeable_in_the_table() {
    assert_ne!(Wait::Verify(1), Wait::Rebase(1));
    // Verify(0) is 1 s but Rebase(0) clamps to 4 s: a swapped variant shows here.
    assert_ne!(ceiling(Wait::Verify(0)), ceiling(Wait::Rebase(0)));
    assert_ne!(
        ceiling(Wait::Rebase(3)),
        ceiling(Wait::Verify(3)).saturating_sub(S(1))
    );
}

proptest! {
    #[test]
    fn never_above_the_ceiling_and_monotone(a in any::<u64>(), b in any::<u64>(), n in 0u32..8) {
        let (lo, hi) = if a <= b { (a, b) } else { (b, a) };
        for wait in [Wait::Verify(n), Wait::Rebase(n)] {
            let (x, y) = (pause_for(wait, lo), pause_for(wait, hi));
            prop_assert!(x <= y);
            prop_assert!(y <= ceiling(wait));
        }
    }
}
