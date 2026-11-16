//! The run loop with a scripted waiting primitive: no time, no signals, no threads.
use memory::run::{
    Outcome, STARTUP_MAX, STARTUP_MIN, Timing, draw_from, parse_sim_timing, startup_delay,
};
use std::time::Duration;

#[test]
fn only_a_stop_gives_exit_zero() {
    for outcome in [
        Outcome::RoundFailed,
        Outcome::ShutdownFailed,
        Outcome::WaitFailed,
    ] {
        assert_eq!(outcome.exit_code(), 1);
        assert!(outcome.code().is_some());
    }
    assert_eq!(Outcome::Stopped.code(), None);
    assert_eq!(Outcome::Stopped.exit_code(), 0);
}

#[test]
fn startup_delay_spans_five_to_sixty_seconds_inclusive() {
    assert_eq!(STARTUP_MIN, Duration::from_secs(5));
    assert_eq!(STARTUP_MAX, Duration::from_secs(60));
    assert_eq!(startup_delay(0), STARTUP_MIN);
    assert_eq!(startup_delay(55_000), STARTUP_MAX);
    assert_eq!(startup_delay(55_001), STARTUP_MIN);
    let mut draw = 1_u64;
    for _ in 0..10_000 {
        draw = draw.wrapping_mul(6_364_136_223_846_793_005).wrapping_add(1);
        let delay = startup_delay(draw);
        assert!((STARTUP_MIN..=STARTUP_MAX).contains(&delay));
    }
    assert!((STARTUP_MIN..=STARTUP_MAX).contains(&startup_delay(u64::MAX)));
}

#[test]
fn draw_depends_on_clocks_and_pid() {
    let base = draw_from(1, 2, 3);
    assert_ne!(base, draw_from(2, 2, 3));
    assert_ne!(base, draw_from(1, 3, 3));
    assert_ne!(base, draw_from(1, 2, 4));
    assert_eq!(base, draw_from(1, 2, 3));
}

#[test]
fn sim_timing_parses_two_millisecond_values_and_refuses_the_rest() {
    assert_eq!(
        parse_sim_timing("10,200"),
        Some(Timing {
            startup: Duration::from_millis(10),
            interval: Duration::from_millis(200)
        })
    );
    for bad in [
        "", "10", "10,", ",5", "a,b", "10,0", "10,-1", "1,2,3", " 1,2",
    ] {
        assert_eq!(parse_sim_timing(bad), None, "{bad:?}");
    }
}
