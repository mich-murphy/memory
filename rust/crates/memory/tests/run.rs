//! The run loop with a scripted waiting primitive: no time, no signals, no threads.
use memory::run::{
    Failed, Outcome, STARTUP_MAX, STARTUP_MIN, Timing, Waited, draw_from, parse_sim_timing,
    run_loop, startup_delay,
};
use std::cell::RefCell;
use std::time::Duration;

const TIMING: Timing = Timing {
    startup: Duration::from_secs(7),
    interval: Duration::from_secs(900),
};

#[derive(Debug, PartialEq, Eq)]
enum Event {
    Wait(Duration),
    Round(u32),
    Shutdown,
}

/// Run the loop with scripted waits and round results; return the outcome and the event log.
fn script(
    waits: &[Waited],
    round_fails_at: Option<u32>,
    shutdown_ok: bool,
) -> (Outcome, Vec<Event>) {
    let log = RefCell::new(Vec::new());
    let mut waits = waits.iter().copied();
    let outcome = run_loop(
        TIMING,
        0_u32,
        |up_to| {
            log.borrow_mut().push(Event::Wait(up_to));
            waits.next().unwrap_or(Waited::Failed)
        },
        |count| {
            *count += 1;
            log.borrow_mut().push(Event::Round(*count));
            if Some(*count) == round_fails_at {
                Err(Failed)
            } else {
                Ok(())
            }
        },
        |_| {
            log.borrow_mut().push(Event::Shutdown);
            if shutdown_ok { Ok(()) } else { Err(Failed) }
        },
    );
    (outcome, log.into_inner())
}

#[test]
fn elapsed_elapsed_stop_runs_two_rounds_then_shuts_down() {
    let (outcome, log) = script(
        &[Waited::Elapsed, Waited::Elapsed, Waited::Stop],
        None,
        true,
    );
    assert_eq!(outcome, Outcome::Stopped);
    assert_eq!(outcome.exit_code(), 0);
    assert_eq!(
        log,
        vec![
            Event::Wait(TIMING.startup),
            Event::Round(1),
            Event::Wait(TIMING.interval),
            Event::Round(2),
            Event::Wait(TIMING.interval),
            Event::Shutdown,
        ]
    );
}

#[test]
fn stop_during_startup_wait_runs_no_round() {
    let (outcome, log) = script(&[Waited::Stop], None, true);
    assert_eq!(outcome, Outcome::Stopped);
    assert_eq!(log, vec![Event::Wait(TIMING.startup), Event::Shutdown]);
}

#[test]
fn round_error_exits_one_after_shutdown() {
    let (outcome, log) = script(&[Waited::Elapsed, Waited::Elapsed], Some(1), true);
    assert_eq!(outcome, Outcome::RoundFailed);
    assert_eq!(outcome.exit_code(), 1);
    assert_eq!(outcome.code(), Some("round_failed"));
    assert_eq!(
        log,
        vec![
            Event::Wait(TIMING.startup),
            Event::Round(1),
            Event::Shutdown
        ]
    );
}

#[test]
fn failed_shutdown_after_stop_exits_one() {
    let (outcome, _) = script(&[Waited::Stop], None, false);
    assert_eq!(outcome, Outcome::ShutdownFailed);
    assert_eq!(outcome.exit_code(), 1);
}

#[test]
fn failed_wait_exits_one_and_never_zero() {
    let (outcome, log) = script(&[Waited::Elapsed, Waited::Failed], None, true);
    assert_eq!(outcome, Outcome::WaitFailed);
    assert_eq!(outcome.exit_code(), 1);
    assert_eq!(log.last(), Some(&Event::Shutdown));
}

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
