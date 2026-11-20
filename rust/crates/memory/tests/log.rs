//! `memory::log` event lines against a scoped subscriber writing to a buffer.
use memory::audit::AuditCounts;
use memory::daemon::{FailureCounts, RoundReport};
use memory::daemon::{OwnerOutcome, ParkedReason, StateLoad};
use memory::driver::Cause;
use memory::log::{
    Scope, capture_outcome, format_utc, host_paused, host_resumed, parked, progress, render, round,
    started, state_loads, stopped,
};
use memory_core::domain::{
    Agent, ArchiveId, MachineId, Owner, OwnerParkReason, ParkReason, SessionId, SessionKey, Sha256,
};

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
        stopped: 0,
        failures: FailureCounts {
            offline: 6,
            throttled: 7,
            transient: 8,
            destination_auth: 9,
            local: 10,
            deterministic: 11,
        },
        audit: AuditCounts {
            matched: 14,
            mismatched: 15,
            ineligible: 16,
        },
        hints_removed: 12,
        hints_kept_old: 13,
        deleted: 17,
        absence_skipped: 18,
    };
    let text = render(|| round(Cause::Timer, &report));
    let (_, rest) = split(only_line(&text)).unwrap_or_default();
    assert_eq!(
        rest,
        "INFO round cause=timer landed=1 failed=2 skipped=3 parked=4 deferred=5 offline=6 throttled=7 transient=8 destination_auth=9 local=10 deterministic=11 hints_removed=12 hints_kept_old=13 audit_matched=14 audit_mismatched=15 audit_ineligible=16 deleted=17 absence_skipped=18"
    );
}

#[test]
fn progress_has_exactly_the_closed_numeric_keys() {
    let text = render(|| progress(2, 3, 1, 4, 125));
    let (_, rest) = split(only_line(&text)).unwrap_or_default();
    assert_eq!(
        rest,
        "INFO progress done=2 total=3 landed=1 failed=4 elapsed_s=125"
    );
}

#[test]
fn progress_is_in_the_event_table_of_the_module_doc() {
    let doc = include_str!("../src/log.rs");
    assert!(
        doc.lines()
            .any(|l| l == "//! | `progress` | `done total landed failed elapsed_s` |"),
        "no progress row in the event table"
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

#[test]
fn an_event_from_another_module_writes_nothing() {
    let text = render(|| tracing::info!(note = "foreign text", count = 7_u64, "a message"));
    assert_eq!(text, "");
}

fn lines_of(text: &str) -> Vec<&str> {
    text.lines()
        .map(|l| split(l).unwrap_or_default().1)
        .collect()
}

#[test]
fn state_load_lines_are_status_then_index() {
    let text = render(|| state_loads(StateLoad::NewerSchema, StateLoad::Damaged));
    assert_eq!(
        lines_of(&text),
        [
            "INFO state_load file=status result=damaged",
            "INFO state_load file=index result=newer_schema"
        ]
    );
    let text = render(|| state_loads(StateLoad::Damaged, StateLoad::Loaded));
    assert_eq!(
        lines_of(&text),
        ["INFO state_load file=index result=damaged"]
    );
    let text = render(|| state_loads(StateLoad::Loaded, StateLoad::NewerSchema));
    assert_eq!(
        lines_of(&text),
        ["INFO state_load file=status result=newer_schema"]
    );
}

#[test]
fn state_load_renders_nothing_for_loaded_and_missing() {
    for load in [StateLoad::Loaded, StateLoad::Missing] {
        assert_eq!(render(|| state_loads(load, load)), "");
    }
}

fn owner() -> Result<Owner, String> {
    let bad = |e: memory_core::domain::ValidationError| format!("{e:?}");
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(bad)?,
        machine: MachineId::try_from("m".to_owned()).map_err(bad)?,
        agent: Agent::Claude,
        session: SessionId::try_from("s1".to_owned()).map_err(bad)?,
    }))
}

#[test]
fn parked_names_the_owner_prefix_and_a_closed_reason() -> Result<(), String> {
    let owner = owner()?;
    let prefix = memory_core::limits::owner_prefix(&owner);
    let digest = Sha256::from_bytes([0xab; 32]);
    let cases = [
        (
            ParkedReason::Owner(OwnerParkReason::ForeignWriter {
                theirs: digest,
                ours: digest,
            }),
            "foreign_writer",
        ),
        (
            ParkedReason::Owner(OwnerParkReason::NewerRemoteSchema),
            "newer_remote_schema",
        ),
        (
            ParkedReason::Source(ParkReason::RepeatedAuditMismatch),
            "repeated_audit_mismatch",
        ),
        (
            ParkedReason::Source(ParkReason::RepeatedReadbackMismatch),
            "repeated_readback_mismatch",
        ),
        (
            ParkedReason::Source(ParkReason::SourceUnstable),
            "source_unstable",
        ),
        (
            ParkedReason::Source(ParkReason::RepeatedNonJsonResponse),
            "repeated_non_json_response",
        ),
        (
            ParkedReason::Source(ParkReason::RepeatedSchemaError),
            "repeated_schema_error",
        ),
    ];
    for (reason, id) in cases {
        let text = render(|| parked(&owner, &reason, Scope::Owner));
        let (_, rest) = split(only_line(&text)).unwrap_or_default();
        assert_eq!(
            rest,
            format!("INFO parked owner={prefix} reason={id} scope=owner")
        );
        assert!(!text.contains("abab"), "no digest in the line");
        let text = render(|| parked(&owner, &reason, Scope::Source));
        let (_, rest) = split(only_line(&text)).unwrap_or_default();
        assert_eq!(
            rest,
            format!("INFO parked owner={prefix} reason={id} scope=source")
        );
    }
    Ok(())
}

#[test]
fn host_pause_events_have_no_keys() {
    let text = render(host_paused);
    assert_eq!(lines_of(&text), ["INFO host_paused"]);
    let text = render(host_resumed);
    assert_eq!(lines_of(&text), ["INFO host_resumed"]);
}

#[test]
fn round_names_every_cause() {
    for (cause, id) in [
        (Cause::Start, "start"),
        (Cause::Timer, "timer"),
        (Cause::Rescan, "rescan"),
        (Cause::Wake, "wake"),
        (Cause::Dir, "dir"),
        (Cause::Manual, "manual"),
    ] {
        let text = render(|| round(cause, &RoundReport::default()));
        let (_, rest) = split(only_line(&text)).unwrap_or_default();
        assert!(
            rest.starts_with(&format!("INFO round cause={id} landed=0 ")),
            "{rest}"
        );
    }
}

#[test]
fn capture_logs_four_outcomes_with_the_owner_prefix_and_nothing_else() -> Result<(), String> {
    let owner = owner()?;
    let prefix = memory_core::limits::owner_prefix(&owner);
    for (outcome, id) in [
        (OwnerOutcome::Landed, "landed"),
        (OwnerOutcome::Unchanged, "unchanged"),
        (OwnerOutcome::Failed, "failed"),
        (OwnerOutcome::Stopped, "stopped"),
    ] {
        let text = render(|| capture_outcome(&owner, outcome));
        let (_, rest) = split(only_line(&text)).unwrap_or_default();
        assert_eq!(rest, format!("INFO capture owner={prefix} result={id}"));
    }
    for outcome in [
        OwnerOutcome::Paused,
        OwnerOutcome::Skipped,
        OwnerOutcome::Deferred,
    ] {
        assert_eq!(
            render(|| capture_outcome(&owner, outcome)),
            "",
            "{outcome:?}"
        );
    }
    Ok(())
}

#[test]
fn retry_renders_the_exact_line() -> Result<(), String> {
    use memory_core::domain::ErrorClass;
    use std::time::Duration;
    let owner = owner()?;
    let prefix = memory_core::limits::owner_prefix(&owner);
    let text = render(|| {
        memory::log::retry(
            &owner,
            &ErrorClass::Offline,
            "dns",
            false,
            1,
            Duration::from_millis(9_250),
            Duration::from_millis(1_500),
        );
    });
    let (_, rest) = split(only_line(&text)).unwrap_or_default();
    assert_eq!(
        rest,
        format!(
            "INFO retry owner={prefix} class=offline cause=dns landing=no attempt=1 elapsed_ms=9250 after_ms=1500"
        )
    );
    let text = render(|| {
        memory::log::retry(
            &owner,
            &ErrorClass::Throttled,
            "server",
            true,
            3,
            Duration::ZERO,
            Duration::from_secs(7),
        );
    });
    let (_, rest) = split(only_line(&text)).unwrap_or_default();
    assert!(
        rest.contains(
            " class=throttled cause=server landing=unknown attempt=3 elapsed_ms=0 after_ms=7000"
        ),
        "{rest}"
    );
    Ok(())
}
