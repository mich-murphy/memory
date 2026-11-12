//! The production clock: monotonic readings never go back, wall time is signed.
use memory::clock::{SystemClock, wall_ns_of};
use memory::environment::Clock;
use std::time::{Duration, UNIX_EPOCH};

#[test]
fn monotonic_never_decreases_and_advances() {
    let clock = SystemClock::new();
    let first = clock.monotonic_ns();
    std::thread::sleep(Duration::from_millis(5));
    let second = clock.monotonic_ns();
    assert!(second >= first + 4_000_000, "{first} {second}");
}

#[test]
fn wall_is_relative_to_the_epoch_in_both_directions() {
    assert_eq!(wall_ns_of(UNIX_EPOCH), 0);
    assert_eq!(
        wall_ns_of(UNIX_EPOCH + Duration::from_secs(2)),
        2_000_000_000
    );
    assert_eq!(
        wall_ns_of(UNIX_EPOCH - Duration::from_secs(3)),
        -3_000_000_000
    );
    assert!(SystemClock::new().wall_ns() > 1_700_000_000_000_000_000);
}
