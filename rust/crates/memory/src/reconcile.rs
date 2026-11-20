//! `memory reconcile`: one full round now, then exit.
//!
//! Takes the daemon lock, so beside a running daemon it refuses (`memory: lock_held`, exit 75).
//! Order: stop signals, the lock and state (`start_from_config`), the log to stderr, one
//! [`crate::daemon::Daemon::reconcile_once`] with the signal stop probe, the `round` log line
//! with cause `manual`, shutdown (status and index flushed, lock released).
//!
//! Stdout is one line `reconcile landed=<n> failed=<n> parked=<n> skipped=<n> deferred=<n>`.
//! Exit codes: 0 when nothing failed and nothing was parked, 1 otherwise, 2 config, 75 lock
//! held, 130 stopped by a signal before the round ended.
use crate::assemble::start_from_config;
use crate::daemon::EXIT_CONFIG;
use crate::driver::Cause;
use crate::run::SignalSource;
use std::io::Write;
use std::process::ExitCode;

/// Exit code: a failure or a parked owner in the round.
pub const EXIT_FAILING: u8 = 1;
/// Exit code: stopped by a signal before the round ended.
pub const EXIT_SIGNAL: u8 = 130;

fn fail(code: &str, exit: u8) -> ExitCode {
    let _ = writeln!(std::io::stderr(), "memory: {code}");
    ExitCode::from(exit)
}

/// Entry of `memory reconcile`; `args` follow the word `reconcile` and must be empty.
#[must_use]
pub fn main_entry(mut args: impl Iterator<Item = std::ffi::OsString>) -> ExitCode {
    if args.next().is_some() {
        return fail("usage", 64);
    }
    let Ok(signals) = SignalSource::install() else {
        return fail("signals_unavailable", 1);
    };
    let Some(root) = crate::state::default_root() else {
        return fail("state_root", EXIT_CONFIG);
    };
    let mut daemon = match start_from_config(&root, &root.join("config.toml")) {
        Ok(daemon) => daemon,
        Err(error) => return fail(error.code(), error.exit_code()),
    };
    let (_tx, rx) = crate::watch::inbox(1);
    let flag = rx.stop_flag();
    let probe = flag.clone();
    daemon.set_stop(move || probe.requested());
    if signals.forward_to(rx.stop_handle()).is_err() {
        return fail("signals_unavailable", 1);
    }
    if crate::log::start().is_err() {
        return fail("log_unavailable", 1);
    }
    let report = match daemon.reconcile_once() {
        Ok(report) => report,
        Err(_) => {
            let _ = daemon.shutdown();
            return fail("round_failed", 1);
        }
    };
    crate::log::round(Cause::Manual, &report);
    if daemon.shutdown().is_err() {
        return fail("shutdown_failed", 1);
    }
    if flag.requested() {
        return ExitCode::from(EXIT_SIGNAL);
    }
    let _ = writeln!(
        std::io::stdout(),
        "reconcile landed={} failed={} parked={} skipped={} deferred={}",
        report.landed,
        report.failed,
        report.parked,
        report.skipped,
        report.deferred
    );
    ExitCode::from(u8::from(report.failed != 0 || report.parked != 0) * EXIT_FAILING)
}
