//! The daemon's log (DESIGN.md 11.5): the only module that uses `tracing`.
//!
//! Every event is one function taking typed values; no function takes text, a path or an
//! error from outside this file, and no foreign error is ever formatted. Level is fixed at info.
//! There is no environment variable and there are no debug or trace events.
//!
//! # Line grammar
//!
//! One event per line, ASCII only, written to stderr:
//!
//! ```text
//! <timestamp> INFO <event>[ <key>=<value>]*
//! ```
//!
//! `<timestamp>` is UTC, RFC 3339, whole seconds: `YYYY-MM-DDTHH:MM:SSZ`. Keys are in the fixed
//! order below. A value is an unsigned decimal integer, except `version` (the crate version:
//! digits and dots), `file`, `result` and `reason` (closed lower-case identifiers) and `owner`.
//! `owner` is the only open value: the owner's stored key prefix, charset `[A-Za-z0-9._:/-]`,
//! never empty, no space. It is safe to write because identifiers are validated on construction
//! to ASCII alphanumerics and `._:-` and joined with `/`; it holds no path, secret or content.
//! There is no ANSI escape, target, thread id, span, file or line.
//!
//! | event | keys |
//! |---|---|
//! | `started` | `version` |
//! | `round` | `cause` (`start`, `timer`, `rescan`, `wake`, `dir`, `manual`) `landed failed skipped parked deferred offline throttled transient destination_auth local deterministic hints_removed hints_kept_old audit_matched audit_mismatched audit_ineligible deleted absence_skipped withheld` |
//! | `progress` | `done total landed failed elapsed_s` |
//! | `capture` | `owner` `result` (`landed`, `unchanged`, `failed`, `stopped`) |
//! | `stopped` | none |
//! | `log_rotated` | none |
//! | `state_load` | `file` (`index` or `status`) `result` (`damaged` or `newer_schema`) |
//! | `retry` | `owner` `class` (`offline throttled transient destination_auth local deterministic`) `cause` (a closed code: a transport cause such as `dns refused timeout reset`, or `server throttled auth not_found base_unchanged corrupt` and the like) `landing` (`unknown` or `no`) `attempt elapsed_ms after_ms` |
//! | `parked` | `owner` `reason` `scope` (`owner` or `source`; reasons: `foreign_writer`, `newer_remote_schema`, `repeated_audit_mismatch`, `source_unstable`, `repeated_non_json_response`, `repeated_schema_error`) |
//! | `host_paused` | none |
//! | `host_resumed` | none |
//!
//! `progress` is logged inside a full round after an owner's turn when at least 60 s of monotonic
//! time passed since the round started or since the last `progress` line; a shorter round logs
//! none. `done` and `total` count owners, `landed` and `failed` are the round's running counts,
//! `elapsed_s` is whole seconds since the round started. It carries no owner and no path.
//!
//! `state_load` follows `started`, `status` before `index`, only for a damaged or newer-schema
//! startup load. `parked` is logged once, in the round an owner or one of its sources becomes
//! parked; a source id is never logged. `host_paused` and `host_resumed` are logged in the
//! round the pause starts and ends. In a full round all come before that round's `round` line;
//! in a per-owner capture `parked` and `host_paused` are logged inside `capture_owner`, so they
//! come before that capture's `capture` line, which is written after it returns (a capture that
//! parks an owner or source or pauses the host maps to `Failed` unless something landed or
//! stopped, so the reader sees `parked` or
//! `host_paused`, then `capture ... result=failed`). `capture` is logged for the outcomes
//! landed, unchanged, failed and stopped only; `Paused`, `Skipped` and `Deferred` write none.
//!
//! `retry` is logged once per failed attempt that will be retried, by the settle of the turn, so
//! in a full round it comes before the `round` line and in a per-owner capture before its
//! `capture` line. `attempt` counts failed attempts since the last success (1 for the first),
//! `elapsed_ms` is the monotonic time from the turn's start, `after_ms` the retry delay.
//! `landing=unknown` means the `current.json` write may have landed. It carries only the owner
//! prefix, closed codes and integers.
//!
//! # Rotation
//!
//! When fd 2 is a regular file (launchd's `StandardErrorPath`), the daemon rotates it itself: before
//! a line is written, a file of at least 8 MiB is renamed to `<path>.1` (older ones shift to `.2`
//! and `.3`, the oldest is dropped), a new mode 0600 file takes fd 2, and its first line is
//! `log_rotated` (no keys), followed by the line that triggered it. A line is never split. A pipe,
//! socket, tty or `/dev/null` is never touched. If the path no longer names the file behind fd 2,
//! or any step fails, rotation is off for the life of the process and nothing is written about it.
//! Under the `sim` feature `MEMORY_SIM_LOG_CAP=<bytes>` replaces the cap; the release binary never
//! reads it. See [`rotate`].
//!
//! `memory hook` never starts the subscriber.
use crate::daemon::{OwnerOutcome, ParkedReason, RoundReport, StateLoad};
use crate::driver::Cause;
use memory_core::domain::{ErrorClass, Owner, OwnerParkReason, ParkReason};
use memory_core::limits::owner_prefix;
use std::fmt;
use std::io::Write;
use std::sync::{Arc, Mutex};
use tracing::field::{Field, Visit};
use tracing::{Event, Level, Subscriber};
use tracing_subscriber::fmt::format::Writer;
use tracing_subscriber::fmt::{FmtContext, FormatEvent, FormatFields, MakeWriter};
use tracing_subscriber::registry::LookupSpan;

pub mod rotate;

/// The subscriber could not be installed.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Unavailable;

/// `secs` since the Unix epoch as `YYYY-MM-DDTHH:MM:SSZ` (proleptic Gregorian, UTC).
#[must_use]
pub fn format_utc(secs: u64) -> String {
    let days = secs / 86_400;
    let rest = secs % 86_400;
    // Days since 0000-03-01 (Hinnant's civil_from_days).
    let z = days + 719_468;
    let era = z / 146_097;
    let doe = z % 146_097;
    let yoe = (doe - doe / 1_460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let day = doy - (153 * mp + 2) / 5 + 1;
    let month = if mp < 10 { mp + 3 } else { mp - 9 };
    let year = yoe + era * 400 + u64::from(month <= 2);
    format!(
        "{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}Z",
        rest / 3_600,
        rest % 3_600 / 60,
        rest % 60
    )
}

struct Line<'a>(Writer<'a>);

impl Visit for Line<'_> {
    fn record_u64(&mut self, field: &Field, value: u64) {
        let _ = write!(self.0, " {}={value}", field.name());
    }
    fn record_str(&mut self, field: &Field, value: &str) {
        if field.name() == "event" {
            let _ = write!(self.0, " {value}");
        } else {
            let _ = write!(self.0, " {}={value}", field.name());
        }
    }
    fn record_debug(&mut self, _field: &Field, _value: &dyn fmt::Debug) {}
}

struct Grammar;

impl<S, N> FormatEvent<S, N> for Grammar
where
    S: Subscriber + for<'a> LookupSpan<'a>,
    N: for<'a> FormatFields<'a> + 'static,
{
    fn format_event(
        &self,
        _ctx: &FmtContext<'_, S, N>,
        mut writer: Writer<'_>,
        event: &Event<'_>,
    ) -> fmt::Result {
        // Only this module's events are written: foreign text never reaches a line. Nothing is
        // written here, so the layer writes zero bytes (`write_all` of an empty buffer).
        if event.metadata().target() != module_path!() {
            return Ok(());
        }
        let secs = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map_or(0, |d| d.as_secs());
        write!(writer, "{} {}", format_utc(secs), event.metadata().level())?;
        event.record(&mut Line(writer.by_ref()));
        writeln!(writer)
    }
}

fn subscriber<W>(writer: W) -> impl Subscriber + Send + Sync
where
    W: for<'a> MakeWriter<'a> + Send + Sync + 'static,
{
    tracing_subscriber::fmt()
        .with_max_level(Level::INFO)
        // The builder default prints failures to stderr with `eprintln!`, which panics when
        // stderr itself is unwritable. A failed log write is dropped silently.
        .log_internal_errors(false)
        .event_format(Grammar)
        .with_writer(writer)
        .finish()
}

/// The rotation cap. Under `sim`, `MEMORY_SIM_LOG_CAP=<bytes>` (above zero) replaces it.
#[cfg(feature = "sim")]
fn log_cap() -> u64 {
    std::env::var("MEMORY_SIM_LOG_CAP")
        .ok()
        .and_then(|text| text.parse::<u64>().ok())
        .filter(|cap| *cap > 0)
        .unwrap_or(rotate::LOG_CAP_BYTES)
}

/// Without `sim` the variable is never read.
#[cfg(not(feature = "sim"))]
const fn log_cap() -> u64 {
    rotate::LOG_CAP_BYTES
}

#[cfg(target_os = "macos")]
fn stderr_path(fd: std::os::fd::BorrowedFd<'_>) -> Option<std::path::PathBuf> {
    use std::os::unix::ffi::OsStrExt;
    let path = rustix::fs::getpath(fd).ok()?;
    Some(std::path::PathBuf::from(std::ffi::OsStr::from_bytes(
        path.to_bytes(),
    )))
}

#[cfg(target_os = "linux")]
fn stderr_path(_fd: std::os::fd::BorrowedFd<'_>) -> Option<std::path::PathBuf> {
    std::fs::read_link("/proc/self/fd/2").ok()
}

#[cfg(not(any(target_os = "macos", target_os = "linux")))]
fn stderr_path(_fd: std::os::fd::BorrowedFd<'_>) -> Option<std::path::PathBuf> {
    None
}

/// The first line of a file the daemon rotated into.
fn rotated_line() -> String {
    let secs = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |d| d.as_secs());
    format!("{} INFO log_rotated\n", format_utc(secs))
}

/// Writes each line to fd 2, rotating the file behind it first when it is due. The lock is held
/// from the check through the write, so a line is never split across files.
#[derive(Clone)]
struct Rotating(Arc<Mutex<rotate::Rotator>>);

impl Write for Rotating {
    fn write(&mut self, data: &[u8]) -> std::io::Result<usize> {
        let mut stderr = std::io::stderr();
        let mut guard = self.0.lock().ok();
        if let Some(rotator) = guard.as_mut()
            && let Some(fresh) = rotator.before_line(&stderr, stderr_path)
        {
            if rustix::stdio::dup2_stderr(&fresh).is_ok() {
                let _ = stderr.write_all(rotated_line().as_bytes());
            } else {
                rotator.disable();
            }
        }
        stderr.write_all(data)?;
        Ok(data.len())
    }
    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

/// Install the stderr subscriber for the process.
///
/// # Errors
/// A global subscriber is already set.
pub fn start() -> Result<(), Unavailable> {
    let writer = Rotating(Arc::new(Mutex::new(rotate::Rotator::new(log_cap()))));
    tracing::subscriber::set_global_default(subscriber(move || writer.clone()))
        .map_err(|_| Unavailable)
}

#[derive(Clone)]
struct Buffer(Arc<Mutex<Vec<u8>>>);

impl Write for Buffer {
    fn write(&mut self, data: &[u8]) -> std::io::Result<usize> {
        if let Ok(mut inner) = self.0.lock() {
            inner.extend_from_slice(data);
        }
        Ok(data.len())
    }
    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

/// Run `emit` under a scoped subscriber that writes to memory; return what it logged.
/// For tests: the global subscriber is not touched.
pub fn render(emit: impl FnOnce()) -> String {
    let buffer = Buffer(Arc::default());
    let sink = buffer.clone();
    tracing::subscriber::with_default(subscriber(move || sink.clone()), emit);
    let bytes = buffer
        .0
        .lock()
        .map(|inner| inner.clone())
        .unwrap_or_default();
    String::from_utf8_lossy(&bytes).into_owned()
}

fn count(value: usize) -> u64 {
    u64::try_from(value).unwrap_or(u64::MAX)
}

/// The daemon is built from its config and about to wait out the startup delay.
pub fn started() {
    tracing::info!(event = "started", version = env!("CARGO_PKG_VERSION"));
}

/// The closed identifier of a reconcile cause.
const fn cause_id(cause: Cause) -> &'static str {
    match cause {
        Cause::Start => "start",
        Cause::Timer => "timer",
        Cause::Rescan => "rescan",
        Cause::Wake => "wake",
        Cause::Dir => "dir",
        Cause::Manual => "manual",
    }
}

/// One reconcile round succeeded, run for `cause`.
pub fn round(cause: Cause, report: &RoundReport) {
    let f = &report.failures;
    tracing::info!(
        event = "round",
        cause = cause_id(cause),
        landed = count(report.landed),
        failed = count(report.failed),
        skipped = count(report.skipped),
        parked = count(report.parked),
        deferred = count(report.deferred),
        offline = f.offline,
        throttled = f.throttled,
        transient = f.transient,
        destination_auth = f.destination_auth,
        local = f.local,
        deterministic = f.deterministic,
        hints_removed = count(report.hints_removed),
        hints_kept_old = count(report.hints_kept_old),
        audit_matched = report.audit.matched,
        audit_mismatched = report.audit.mismatched,
        audit_ineligible = report.audit.ineligible,
        deleted = count(report.deleted),
        absence_skipped = count(report.absence_skipped),
        withheld = count(report.withheld),
    );
}

/// A running full round has handled `done` of `total` owners, `elapsed_s` seconds in.
pub fn progress(done: usize, total: usize, landed: usize, failed: usize, elapsed_s: u64) {
    tracing::info!(
        event = "progress",
        done = count(done),
        total = count(total),
        landed = count(landed),
        failed = count(failed),
        elapsed_s = elapsed_s,
    );
}

/// How a logged per-owner capture ended.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CaptureResult {
    /// A commit landed.
    Landed,
    /// Nothing to commit.
    Unchanged,
    /// The capture failed.
    Failed,
    /// A stop ended it.
    Stopped,
}

/// One per-owner capture ended with `result`.
pub fn capture(owner: &Owner, result: CaptureResult) {
    let prefix = owner_prefix(owner);
    let result = match result {
        CaptureResult::Landed => "landed",
        CaptureResult::Unchanged => "unchanged",
        CaptureResult::Failed => "failed",
        CaptureResult::Stopped => "stopped",
    };
    tracing::info!(event = "capture", owner = prefix.as_str(), result = result);
}

/// Log the outcome of [`crate::daemon::Daemon::capture_owner`]: landed, unchanged, failed and
/// stopped only. A paused host, a parked owner and a deferred retry write nothing: each was
/// logged when it happened, and a retry that stays due while the host is paused is offered
/// every turn.
pub fn capture_outcome(owner: &Owner, outcome: OwnerOutcome) {
    let result = match outcome {
        OwnerOutcome::Landed => CaptureResult::Landed,
        OwnerOutcome::Unchanged => CaptureResult::Unchanged,
        OwnerOutcome::Failed => CaptureResult::Failed,
        OwnerOutcome::Stopped => CaptureResult::Stopped,
        OwnerOutcome::Deferred | OwnerOutcome::Skipped | OwnerOutcome::Paused => return,
    };
    capture(owner, result);
}

/// The closed identifier of an error class.
const fn class_id(class: &ErrorClass) -> &'static str {
    match class {
        ErrorClass::Offline => "offline",
        ErrorClass::Throttled => "throttled",
        ErrorClass::Transient => "transient",
        ErrorClass::DestinationAuth => "destination_auth",
        ErrorClass::Local(_) => "local",
        ErrorClass::Deterministic(_) => "deterministic",
    }
}

fn millis(d: std::time::Duration) -> u64 {
    u64::try_from(d.as_millis()).unwrap_or(u64::MAX)
}

/// A failed attempt of `owner` will be retried: its error `class`, the closed `cause` code,
/// whether the landing is unknown, the failed-attempt count, the time the turn took and the
/// retry delay.
pub fn retry(
    owner: &Owner,
    class: &ErrorClass,
    cause: &'static str,
    landing_unknown: bool,
    attempt: u32,
    elapsed: std::time::Duration,
    after: std::time::Duration,
) {
    let prefix = owner_prefix(owner);
    tracing::info!(
        event = "retry",
        owner = prefix.as_str(),
        class = class_id(class),
        cause = cause,
        landing = if landing_unknown { "unknown" } else { "no" },
        attempt = u64::from(attempt),
        elapsed_ms = millis(elapsed),
        after_ms = millis(after),
    );
}

/// A requested stop and a successful shutdown, just before exit 0.
pub fn stopped() {
    tracing::info!(event = "stopped");
}

/// The startup load of each state file; logs `status` then `index`, and only for a
/// [`StateLoad::Damaged`] or [`StateLoad::NewerSchema`] result.
pub fn state_loads(index: StateLoad, status: StateLoad) {
    for (file, load) in [("status", status), ("index", index)] {
        let result = match load {
            StateLoad::Damaged => "damaged",
            StateLoad::NewerSchema => "newer_schema",
            StateLoad::Loaded | StateLoad::Missing => continue,
        };
        tracing::info!(event = "state_load", file = file, result = result);
    }
}

/// The closed identifier of a parking reason; no digest is included.
const fn reason_id(reason: &ParkedReason) -> &'static str {
    match reason {
        ParkedReason::Owner(OwnerParkReason::ForeignWriter { .. }) => "foreign_writer",
        ParkedReason::Owner(OwnerParkReason::NewerRemoteSchema) => "newer_remote_schema",
        ParkedReason::Source(ParkReason::RepeatedAuditMismatch) => "repeated_audit_mismatch",
        ParkedReason::Source(ParkReason::SourceUnstable) => "source_unstable",
        ParkedReason::Source(ParkReason::RepeatedNonJsonResponse) => "repeated_non_json_response",
        ParkedReason::Source(ParkReason::RepeatedSchemaError) => "repeated_schema_error",
    }
}

/// What a `parked` event parks.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Scope {
    /// The whole owner.
    Owner,
    /// One source of the owner; its id is never logged.
    Source,
}

/// An owner or one of its sources became parked in this round.
pub fn parked(owner: &Owner, reason: &ParkedReason, scope: Scope) {
    let prefix = owner_prefix(owner);
    let scope = match scope {
        Scope::Owner => "owner",
        Scope::Source => "source",
    };
    tracing::info!(
        event = "parked",
        owner = prefix.as_str(),
        reason = reason_id(reason),
        scope = scope
    );
}

/// Uploads were paused by a host-scoped failure in this round.
pub fn host_paused() {
    tracing::info!(event = "host_paused");
}

/// Uploads resumed in this round.
pub fn host_resumed() {
    tracing::info!(event = "host_resumed");
}
