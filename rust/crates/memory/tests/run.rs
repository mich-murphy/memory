//! `memory::run`: exit codes, the startup delay, the simulation timing and the signal source.
use memory::run::{Outcome, SignalSource, Timing, draw_from, parse_sim_timing, startup_delay};
use memory::watch::{FsWatcher, Received};
use std::time::{Duration, Instant};

fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}

/// The daemon installs its handlers before it takes the lock and starts the forwarding thread
/// only once the watcher exists. A signal raised in between is kept and forwarded.
#[test]
fn a_signal_raised_before_the_forwarding_thread_starts_is_forwarded() -> Result<(), String> {
    let source = SignalSource::install().map_err(e)?;
    signal_hook::low_level::raise(signal_hook::consts::SIGTERM).map_err(e)?;
    let watcher = FsWatcher::new(&[]).map_err(e)?;
    let flag = watcher.stop_flag();
    assert!(
        !flag.requested(),
        "nothing forwards before the thread starts"
    );
    source.forward_to(watcher.stop_handle()).map_err(e)?;
    let start = Instant::now();
    loop {
        match watcher.recv_timeout(Duration::from_millis(50)) {
            Received::Stop => break,
            Received::Timeout => {}
            other => return Err(format!("{other:?}")),
        }
        if start.elapsed() > Duration::from_secs(20) {
            return Err("the signal was never forwarded".to_owned());
        }
    }
    assert!(flag.requested());
    Ok(())
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
    assert_eq!(Outcome::Stopped.exit_code(), 0);
}

#[test]
fn startup_delay_spans_five_to_sixty_seconds_inclusive() {
    const STARTUP_MIN: Duration = Duration::from_secs(5);
    const STARTUP_MAX: Duration = Duration::from_secs(60);
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
