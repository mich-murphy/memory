//! The allowlist check on a daemon's stderr after a clean stop, shared by the `harness` and
//! `s3_store` test binaries. Grammar: the module docs of `memory::log`.

const ROUND_KEYS: [&str; 20] = [
    "cause",
    "landed",
    "failed",
    "skipped",
    "parked",
    "deferred",
    "offline",
    "throttled",
    "transient",
    "destination_auth",
    "local",
    "deterministic",
    "hints_removed",
    "hints_kept_old",
    "audit_matched",
    "audit_mismatched",
    "audit_ineligible",
    "deleted",
    "absence_skipped",
    // D54, brief 47: scanned files judged and withheld in the round.
    "withheld",
];

fn digits(text: &str, len: usize) -> bool {
    text.len() == len && text.bytes().all(|b| b.is_ascii_digit())
}

fn timestamp_ok(text: &str) -> bool {
    let b = text.as_bytes();
    let at = |i: usize, c: u8| b.get(i) == Some(&c);
    let num = |from: usize, to: usize| text.get(from..to).and_then(|t| t.parse::<u32>().ok());
    text.len() == 20
        && text.get(0..4).is_some_and(|t| digits(t, 4))
        && at(4, b'-')
        && at(7, b'-')
        && at(10, b'T')
        && at(13, b':')
        && at(16, b':')
        && at(19, b'Z')
        && text.get(5..7).is_some_and(|t| digits(t, 2))
        && text.get(8..10).is_some_and(|t| digits(t, 2))
        && text.get(11..13).is_some_and(|t| digits(t, 2))
        && text.get(14..16).is_some_and(|t| digits(t, 2))
        && text.get(17..19).is_some_and(|t| digits(t, 2))
        && num(5, 7).is_some_and(|m| (1..=12).contains(&m))
        && num(8, 10).is_some_and(|d| (1..=31).contains(&d))
        && num(11, 13).is_some_and(|h| h < 24)
        && num(14, 16).is_some_and(|m| m < 60)
        && num(17, 19).is_some_and(|s| s < 61)
}

/// The event name of a valid line, or what is wrong with it.
pub fn parse(line: &str) -> Result<&str, String> {
    if !line.is_ascii() || line.bytes().any(|b| b.is_ascii_control()) {
        return Err(format!("not plain ASCII: {line:?}"));
    }
    let mut words = line.split(' ');
    if !words.next().is_some_and(timestamp_ok) {
        return Err(format!("bad timestamp: {line:?}"));
    }
    if words.next() != Some("INFO") {
        return Err(format!("bad level: {line:?}"));
    }
    let event = words.next().ok_or("no event")?;
    let pairs: Vec<(&str, &str)> = words
        .map(|w| w.split_once('=').ok_or(format!("no '=': {line:?}")))
        .collect::<Result<_, _>>()?;
    let keys: Vec<&str> = pairs.iter().map(|(k, _)| *k).collect();
    let want: &[&str] = match event {
        "started" => &["version"],
        "round" => &ROUND_KEYS,
        "progress" => &["done", "total", "landed", "failed", "elapsed_s"],
        "capture" => &["owner", "result"],
        "stopped" | "host_paused" | "host_resumed" | "log_rotated" => &[],
        "state_load" => &["file", "result"],
        "parked" => &["owner", "reason", "scope"],
        "retry" => &[
            "owner",
            "class",
            "cause",
            "landing",
            "attempt",
            "elapsed_ms",
            "after_ms",
        ],
        other => return Err(format!("unknown event {other:?}")),
    };
    if keys != want {
        return Err(format!("keys {keys:?}, want {want:?}: {line:?}"));
    }
    for (key, value) in pairs {
        let ok = match key {
            "version" => {
                !value.is_empty() && value.bytes().all(|b| b.is_ascii_digit() || b == b'.')
            }
            "file" => ["index", "status"].contains(&value),
            "result" => [
                "damaged",
                "newer_schema",
                "landed",
                "unchanged",
                "failed",
                "stopped",
            ]
            .contains(&value),
            "cause" if event == "retry" => {
                !value.is_empty() && value.bytes().all(|b| b.is_ascii_lowercase() || b == b'_')
            }
            "cause" => ["start", "timer", "rescan", "wake", "dir", "manual"].contains(&value),
            "class" => [
                "offline",
                "throttled",
                "transient",
                "destination_auth",
                "local",
                "deterministic",
            ]
            .contains(&value),
            "landing" => ["unknown", "no"].contains(&value),
            "reason" => [
                "foreign_writer",
                "newer_remote_schema",
                "repeated_audit_mismatch",
                "source_unstable",
                "repeated_non_json_response",
                "repeated_schema_error",
            ]
            .contains(&value),
            "scope" => ["owner", "source"].contains(&value),
            "owner" => {
                !value.is_empty()
                    && value
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || b"._:/-".contains(&b))
            }
            _ => !value.is_empty() && value.bytes().all(|b| b.is_ascii_digit()),
        };
        if !ok {
            return Err(format!("bad value for {key}: {line:?}"));
        }
    }
    Ok(event)
}

/// Every line of `stderr` is a known event with exactly its keys; the sequence is `started`,
/// zero or more `state_load`, then any mix of `round`, `capture`, `retry`, `parked`, `host_paused` and
/// `host_resumed` with at least `min_rounds` of `round`, then `stopped` last; no line holds a
/// canary.
pub fn assert_clean_log(stderr: &str, min_rounds: usize, canaries: &[&str]) {
    let mut events = Vec::new();
    for line in stderr.lines() {
        let parsed = parse(line);
        assert!(parsed.is_ok(), "{parsed:?}");
        // `log_rotated` opens a rotated-into file; it is allowed anywhere after `started`.
        events.extend(parsed.ok().filter(|event| *event != "log_rotated"));
        for canary in canaries {
            assert!(
                canary.is_empty() || !line.contains(canary),
                "a log line holds a canary: {line:?}"
            );
        }
    }
    assert!(stderr.is_empty() || stderr.ends_with('\n'), "{stderr:?}");
    assert_eq!(events.first(), Some(&"started"), "{stderr}");
    assert_eq!(events.last(), Some(&"stopped"), "{stderr}");
    let middle = events.get(1..events.len() - 1).unwrap_or_default();
    let loads = middle.iter().take_while(|e| **e == "state_load").count();
    let rest = middle.get(loads..).unwrap_or_default();
    assert!(
        rest.iter().all(|e| [
            "round",
            "capture",
            "retry",
            "parked",
            "host_paused",
            "host_resumed"
        ]
        .contains(e)),
        "{stderr}"
    );
    assert!(
        rest.iter().filter(|e| **e == "round").count() >= min_rounds,
        "{stderr}"
    );
}
