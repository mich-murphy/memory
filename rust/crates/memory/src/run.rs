//! Entry of `memory daemon` (DESIGN.md 10.3, 11.2, 11.5): the event loop of [`crate::driver`]
//! over the real watcher and the real daemon, until a stop is requested.
//!
//! [`SignalSource`] holds the signal handlers from before the lock is taken until the watcher
//! exists; [`main_entry`] wires everything.
use crate::assemble::{Wired, start_wired};
use crate::daemon::EXIT_CONFIG;
use crate::driver::{Timings, drive};
use crate::environment::Clock;
use crate::watch::{FsWatcher, StopHandle};
use crate::wired::{DaemonWork, WatchInputs};
use signal_hook::consts::{SIGINT, SIGTERM};
use signal_hook::iterator::Signals;
use std::io::Write;
use std::process::ExitCode;
use std::time::Duration;

/// Most inputs the loop takes per turn.
const BATCH: usize = 256;
/// Period of the wake tick (DESIGN.md 10.3).
const TICK: Duration = Duration::from_secs(60);

const STARTUP_MIN_MS: u64 = 5_000;
const STARTUP_MAX_MS: u64 = 60_000;

/// The pause between safety reconciles (DESIGN.md 10.3).
pub const RECONCILE_INTERVAL: Duration = Duration::from_secs(15 * 60);

/// The two durations of the loop that a simulation may replace.
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

/// SIGTERM and SIGINT, caught from the moment of installation.
///
/// `signal-hook` writes each signal into a self-pipe and a pending flag from its
/// async-signal-safe handler. Both are kept by the [`Signals`] value, so a signal raised
/// before [`SignalSource::forward_to`] starts iterating is not lost: `forever()` reads the
/// pending state first (verified in signal-hook 0.4.6, `src/iterator/backend.rs`: the pipe is
/// drained and the pending flags read only when iteration begins). A second signal while
/// stopping only sets the flag again: the process is not killed by it.
#[derive(Debug)]
pub struct SignalSource {
    signals: Signals,
}

impl SignalSource {
    /// Install the handlers.
    ///
    /// # Errors
    /// The handlers could not be created.
    pub fn install() -> std::io::Result<Self> {
        Ok(Self {
            signals: Signals::new([SIGTERM, SIGINT])?,
        })
    }

    /// Start the forwarding thread, which requests a stop through `handle` (flag first, then a
    /// wake of the inbox) for each signal, including one raised since [`SignalSource::install`].
    /// The thread owns the handle: if it ends without a signal, the handle drops and the inbox
    /// reports a failed stop source ([`crate::watch::Received::Failed`]).
    ///
    /// # Errors
    /// The thread could not be created.
    pub fn forward_to(self, handle: StopHandle) -> std::io::Result<()> {
        let mut signals = self.signals;
        std::thread::Builder::new()
            .name("stop-signals".to_owned())
            .spawn(move || {
                for _ in signals.forever() {
                    forward_signal(&handle);
                }
            })?;
        Ok(())
    }
}

/// What the signal thread does for each signal: request a stop through the inbox handle.
/// Separate from the operating-system source so a test can call it without raising a signal.
pub fn forward_signal(handle: &StopHandle) {
    handle.stop();
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

/// Entry of `memory daemon`: install the stop signals, start from `config.toml` under the
/// state root, build the watcher, run the event loop, return the exit code.
///
/// Order: signal handlers, the lock and state, the watcher over the configured roots and the
/// hint directory, the forwarding thread, the log. Signals are installed before the lock, so a
/// stop requested at any point after the lock file appears is kept and forwarded as soon as the
/// thread starts. Every refusal and fatal error writes exactly one line, `memory: <closed
/// code>`, to stderr (the last one: after the subscriber starts, [`crate::log`] lines may
/// precede it); stdout stays empty. When stderr is a regular file the log rotates it by size
/// ([`crate::log`]), so that line lands in the newest file.
#[must_use]
pub fn main_entry() -> ExitCode {
    let Ok(signals) = SignalSource::install() else {
        return refuse("signals_unavailable", 1);
    };
    let timing_override = match timing_override() {
        Ok(timing) => timing,
        Err(code) => return refuse(code, EXIT_CONFIG),
    };
    let Some(root) = crate::state::default_root() else {
        return refuse("state_root_unresolved", 1);
    };
    let Wired {
        mut daemon,
        roots,
        window,
        clock,
    } = match start_wired(&root, &root.join("config.toml")) {
        Ok(wired) => wired,
        Err(error) => return refuse(error.code(), error.exit_code()),
    };
    let hints = daemon.root().join("hints");
    let Ok(watcher) = FsWatcher::with_dir(&roots, &hints) else {
        return refuse("watch_unavailable", 1);
    };
    let flag = watcher.stop_flag();
    let probe = flag.clone();
    daemon.set_stop(move || probe.requested());
    if signals.forward_to(watcher.stop_handle()).is_err() {
        return refuse("signals_unavailable", 1);
    }
    // Only now: every startup refusal above stays the sole line on stderr.
    if crate::log::start().is_err() {
        return refuse("log_unavailable", 1);
    }
    crate::log::started();
    crate::log::state_loads(daemon.index_load(), daemon.status_load());
    let draw = draw_from(clock.monotonic_ns(), clock.wall_ns(), std::process::id());
    let timing = timing_override.unwrap_or(Timing {
        startup: startup_delay(draw),
        interval: RECONCILE_INTERVAL,
    });
    let timings = Timings {
        startup: timing.startup,
        tick: TICK,
        interval: timing.interval,
        batch: BATCH,
    };
    let mut inputs = WatchInputs::new(watcher, flag);
    let mut work = DaemonWork::new(daemon, &hints);
    let outcome = drive(timings, window, &clock, &mut inputs, &mut work);
    if let Some(code) = outcome.code() {
        return refuse(code, outcome.exit_code());
    }
    crate::log::stopped();
    ExitCode::SUCCESS
}
