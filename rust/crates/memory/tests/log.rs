//! `memory::log` event lines against a scoped subscriber writing to a buffer.
use memory::daemon::{FailureCounts, RoundReport};
use memory::log::{format_utc, render, round, started, stopped};

/// Split a line into its timestamp and the rest.
fn split(line: &str) -> Option<(&str, &str)> {
    line.split_once(' ')
}

fn only_line(text: &str) -> &str {
    assert_eq!(text.lines().count(), 1, "{text:?}");
    assert!(text.ends_with('\n'));
    text.trim_end()
}

#[test]
fn started_has_the_version_only() {
    let text = render(started);
    let (stamp, rest) = split(only_line(&text)).unwrap_or_default();
    assert_eq!(stamp.len(), 20);
    assert_eq!(
        rest,
        format!("INFO started version={}", env!("CARGO_PKG_VERSION"))
    );
}

#[test]
fn round_has_every_count_in_order() {
    let report = RoundReport {
        landed: 1,
        failed: 2,
        skipped: 3,
        parked: 4,
        deferred: 5,
        failures: FailureCounts {
            offline: 6,
            throttled: 7,
            transient: 8,
            destination_auth: 9,
            local: 10,
            deterministic: 11,
        },
        hints_removed: 12,
        hints_kept_old: 13,
    };
    let text = render(|| round(&report));
    let (_, rest) = split(only_line(&text)).unwrap_or_default();
    assert_eq!(
        rest,
        "INFO round landed=1 failed=2 skipped=3 parked=4 deferred=5 offline=6 throttled=7 transient=8 destination_auth=9 local=10 deterministic=11 hints_removed=12 hints_kept_old=13"
    );
}

#[test]
fn stopped_has_no_keys() {
    let text = render(stopped);
    let (_, rest) = split(only_line(&text)).unwrap_or_default();
    assert_eq!(rest, "INFO stopped");
}

#[test]
fn the_timestamp_is_rfc3339_utc() {
    assert_eq!(format_utc(0), "1970-01-01T00:00:00Z");
    assert_eq!(format_utc(951_782_400), "2000-02-29T00:00:00Z");
    assert_eq!(format_utc(1_794_483_045), "2026-11-12T11:30:45Z");
}
