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
//! order below. A value is an unsigned decimal integer, except `version`, which is the crate
//! version (digits and dots). There is no ANSI escape, target, thread id, span, file or line.
//!
//! | event | keys |
//! |---|---|
//! | `started` | `version` |
//! | `round` | `landed failed skipped parked deferred offline throttled transient destination_auth local deterministic hints_removed hints_kept_old` |
//! | `stopped` | none |
//!
//! `memory hook` and `memory restore` never start the subscriber.
use crate::daemon::RoundReport;
use std::fmt;
use std::io::Write;
use std::sync::{Arc, Mutex};
use tracing::field::{Field, Visit};
use tracing::{Event, Level, Subscriber};
use tracing_subscriber::fmt::format::Writer;
use tracing_subscriber::fmt::{FmtContext, FormatEvent, FormatFields, MakeWriter};
use tracing_subscriber::registry::LookupSpan;

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
        .event_format(Grammar)
        .with_writer(writer)
        .finish()
}

/// Install the stderr subscriber for the process.
///
/// # Errors
/// A global subscriber is already set.
pub fn start() -> Result<(), Unavailable> {
    tracing::subscriber::set_global_default(subscriber(std::io::stderr)).map_err(|_| Unavailable)
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

/// One reconcile round succeeded.
pub fn round(report: &RoundReport) {
    let f = &report.failures;
    tracing::info!(
        event = "round",
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
    );
}

/// A requested stop and a successful shutdown, just before exit 0.
pub fn stopped() {
    tracing::info!(event = "stopped");
}
