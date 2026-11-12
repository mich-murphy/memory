//! The run loop of `memory daemon` (DESIGN.md 10.3, 11.2, 11.5): wait out a startup delay, run
//! one safety reconcile, then one every 15 minutes, until a stop is requested.
//!
//! [`run_loop`] is generic over the thing it drives and over its waiting primitive, so a test
//! scripts "elapsed, elapsed, stop" without time or signals. [`StopSignals`] is the production
//! waiting primitive; [`main_entry`] wires it to the real daemon.
use crate::assemble::start_from_config;
use crate::clock::SystemClock;
use crate::daemon::EXIT_CONFIG;
use crate::environment::Clock;
use signal_hook::consts::{SIGINT, SIGTERM};
use signal_hook::iterator::Signals;
use std::io::Write;
use std::process::ExitCode;
use std::sync::mpsc::{self, RecvTimeoutError};
use std::time::Duration;

const STARTUP_MIN_MS: u64 = 5_000;
const STARTUP_MAX_MS: u64 = 60_000;

/// The shortest startup delay: the minimum before a restarted daemon's first commit.
pub const STARTUP_MIN: Duration = Duration::from_millis(STARTUP_MIN_MS);
/// The longest startup delay.
pub const STARTUP_MAX: Duration = Duration::from_millis(STARTUP_MAX_MS);
/// The pause between safety reconciles (DESIGN.md 10.3).
pub const RECONCILE_INTERVAL: Duration = Duration::from_secs(15 * 60);

/// The two durations of the loop.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Timing {
    /// Wait before the first round.
    pub startup: Duration,
    /// Wait between rounds.
    pub interval: Duration,
}

/// The startup delay for one uniform draw: 5 s to 60 s inclusive, in whole milliseconds.
/// The modulo bias over a 64-bit draw is below 2^-48.
#[must_use]
pub const fn startup_delay(draw: u64) -> Duration {
    Duration::from_millis(STARTUP_MIN_MS + draw % (STARTUP_MAX_MS - STARTUP_MIN_MS + 1))
}

/// One uniform draw from clock readings and the process id (splitmix64, as the daemon's
/// backoff draw does). No random-number dependency.
#[must_use]
pub const fn draw_from(monotonic_ns: u64, wall_ns: i128, pid: u32) -> u64 {
    let seed = (pid as u64).wrapping_mul(0x9E37_79B9_7F4A_7C15) ^ monotonic_ns ^ (wall_ns as u64);
    let mut z = seed.wrapping_add(0x9E37_79B9_7F4A_7C15);
    z = (z ^ (z >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
    z ^ (z >> 31)
}

/// What a wait returned.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Waited {
    /// The full time passed.
    Elapsed,
    /// A stop was requested.
    Stop,
    /// The waiting primitive broke; no stop can be seen any more.
    Failed,
}

/// A step failed. Carries no detail by design: the process reports a closed code only.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Failed;

/// How the loop ended.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Outcome {
    /// A stop was requested and the shutdown succeeded. The only outcome with exit code 0.
    Stopped,
    /// A round failed.
    RoundFailed,
    /// A stop was requested but the shutdown failed.
    ShutdownFailed,
    /// The waiting primitive failed.
    WaitFailed,
}

impl Outcome {
    /// Process exit code: 0 only for [`Outcome::Stopped`] (DESIGN.md 11.5).
    #[must_use]
    pub const fn exit_code(self) -> u8 {
        match self {
            Self::Stopped => 0,
            Self::RoundFailed | Self::ShutdownFailed | Self::WaitFailed => 1,
        }
    }

    /// The closed code written to stderr; `None` for a clean stop.
    #[must_use]
    pub const fn code(self) -> Option<&'static str> {
        match self {
            Self::Stopped => None,
            Self::RoundFailed => Some("round_failed"),
            Self::ShutdownFailed => Some("shutdown_failed"),
            Self::WaitFailed => Some("wait_failed"),
        }
    }
}

fn finish<D>(
    daemon: D,
    shutdown: impl FnOnce(D) -> Result<(), Failed>,
    outcome: Outcome,
) -> Outcome {
    match (shutdown(daemon), outcome) {
        (Err(Failed), Outcome::Stopped) => Outcome::ShutdownFailed,
        _ => outcome,
    }
}

/// Wait `timing.startup`, run `round`, then wait `timing.interval` and run it again, forever.
///
/// `wait(up_to)` returns [`Waited::Stop`] as soon as a stop is requested, so a stop ends any
/// wait at once, and the stop is looked at before every round. A stop that arrives during a
/// round is not seen until that round returns; the next wait then returns at once. On a stop
/// `shutdown` runs and the outcome is [`Outcome::Stopped`]. A failed round or wait ends the
/// loop too, after a best-effort `shutdown` whose result is ignored, so the index is still
/// flushed. Only a requested stop can give exit code 0.
pub fn run_loop<D>(
    timing: Timing,
    mut daemon: D,
    mut wait: impl FnMut(Duration) -> Waited,
    mut round: impl FnMut(&mut D) -> Result<(), Failed>,
    shutdown: impl FnOnce(D) -> Result<(), Failed>,
) -> Outcome {
    let mut delay = timing.startup;
    loop {
        match wait(delay) {
            Waited::Elapsed => {}
            Waited::Stop => return finish(daemon, shutdown, Outcome::Stopped),
            Waited::Failed => return finish(daemon, shutdown, Outcome::WaitFailed),
        }
        if round(&mut daemon).is_err() {
            return finish(daemon, shutdown, Outcome::RoundFailed);
        }
        delay = timing.interval;
    }
}

/// SIGTERM and SIGINT, delivered to a channel the loop blocks on with a timeout.
///
/// `signal-hook` writes each signal into a pipe from its async-signal-safe handler; a
/// forwarding thread reads the pipe and sends on the channel. A signal that arrives before the
/// loop first waits is queued, not lost. A second signal while stopping only queues another
/// message that nobody reads: the process is not killed by it.
#[derive(Debug)]
pub struct StopSignals {
    receiver: mpsc::Receiver<()>,
}

impl StopSignals {
    /// Install the handlers and start the forwarding thread.
    ///
    /// # Errors
    /// The handlers or the thread could not be created.
    pub fn register() -> std::io::Result<Self> {
        let mut signals = Signals::new([SIGTERM, SIGINT])?;
        let (sender, receiver) = mpsc::channel();
        std::thread::Builder::new()
            .name("stop-signals".to_owned())
            .spawn(move || {
                for _ in signals.forever() {
                    if sender.send(()).is_err() {
                        break;
                    }
                }
            })?;
        Ok(Self { receiver })
    }

    /// Wait up to `up_to` for a stop.
    #[must_use]
    pub fn wait(&self, up_to: Duration) -> Waited {
        match self.receiver.recv_timeout(up_to) {
            Ok(()) => Waited::Stop,
            Err(RecvTimeoutError::Timeout) => Waited::Elapsed,
            Err(RecvTimeoutError::Disconnected) => Waited::Failed,
        }
    }
}

/// Environment variable that replaces the two durations. Exists only with the `sim` feature.
#[cfg(feature = "sim")]
pub const SIM_TIMING_VAR: &str = "MEMORY_SIM_TIMING";

/// Parse `<startup ms>,<interval ms>`; the interval must be above zero.
#[cfg(feature = "sim")]
#[must_use]
pub fn parse_sim_timing(text: &str) -> Option<Timing> {
    let (startup, interval) = text.split_once(',')?;
    let startup = startup.parse::<u64>().ok()?;
    let interval = interval.parse::<u64>().ok().filter(|ms| *ms > 0)?;
    Some(Timing {
        startup: Duration::from_millis(startup),
        interval: Duration::from_millis(interval),
    })
}

#[cfg(feature = "sim")]
fn timing_override() -> Result<Option<Timing>, &'static str> {
    match std::env::var_os(SIM_TIMING_VAR) {
        None => Ok(None),
        Some(value) => value
            .to_str()
            .and_then(parse_sim_timing)
            .map(Some)
            .ok_or("config_sim_timing"),
    }
}

/// Without `sim` the variable is never read and the production durations cannot change.
#[cfg(not(feature = "sim"))]
fn timing_override() -> Result<Option<Timing>, &'static str> {
    Ok(None)
}

fn refuse(code: &str, exit: u8) -> ExitCode {
    let _ = writeln!(std::io::stderr(), "memory: {code}");
    ExitCode::from(exit)
}

/// Entry of `memory daemon`: register the stop signals, start from `config.toml` under the
/// state root, run the loop, return the exit code.
///
/// Signals are registered first, before the lock is taken, so a stop requested at any point
/// after the lock file appears is queued and seen by the first wait. Every refusal and fatal
/// error writes exactly one line, `memory: <closed code>`, to stderr; stdout stays empty.
#[must_use]
pub fn main_entry() -> ExitCode {
    let Ok(signals) = StopSignals::register() else {
        return refuse("signals_unavailable", 1);
    };
    let timing_override = match timing_override() {
        Ok(timing) => timing,
        Err(code) => return refuse(code, EXIT_CONFIG),
    };
    let Some(root) = crate::state::default_root() else {
        return refuse("state_root_unresolved", 1);
    };
    let daemon = match start_from_config(&root, &root.join("config.toml")) {
        Ok(daemon) => daemon,
        Err(error) => return refuse(error.code(), error.exit_code()),
    };
    let clock = SystemClock::new();
    let draw = draw_from(clock.monotonic_ns(), clock.wall_ns(), std::process::id());
    let timing = timing_override.unwrap_or(Timing {
        startup: startup_delay(draw),
        interval: RECONCILE_INTERVAL,
    });
    let outcome = run_loop(
        timing,
        daemon,
        |up_to| signals.wait(up_to),
        |daemon| daemon.reconcile_once().map(|_| ()).map_err(|_| Failed),
        |daemon| daemon.shutdown().map_err(|_| Failed),
    );
    if let Some(code) = outcome.code() {
        return refuse(code, outcome.exit_code());
    }
    ExitCode::SUCCESS
}
