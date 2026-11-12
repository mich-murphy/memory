//! The allowlist check on a daemon's stderr after a clean stop, shared by the `harness` and
//! `s3_store` test binaries. Grammar: the module docs of `memory::log`.

const ROUND_KEYS: [&str; 16] = [
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
fn parse(line: &str) -> Result<&str, String> {
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
        "stopped" => &[],
        other => return Err(format!("unknown event {other:?}")),
    };
    if keys != want {
        return Err(format!("keys {keys:?}, want {want:?}: {line:?}"));
    }
    for (key, value) in pairs {
        let ok = if key == "version" {
            !value.is_empty() && value.bytes().all(|b| b.is_ascii_digit() || b == b'.')
        } else {
            !value.is_empty() && value.bytes().all(|b| b.is_ascii_digit())
        };
        if !ok {
            return Err(format!("bad value for {key}: {line:?}"));
        }
    }
    Ok(event)
}

/// Every line of `stderr` is a known event with exactly its keys; the sequence is `started`,
/// at least `min_rounds` of `round`, then `stopped` last; no line holds a canary.
pub fn assert_clean_log(stderr: &str, min_rounds: usize, canaries: &[&str]) {
    let mut events = Vec::new();
    for line in stderr.lines() {
        let parsed = parse(line);
        assert!(parsed.is_ok(), "{parsed:?}");
        events.extend(parsed.ok());
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
    assert!(middle.iter().all(|e| *e == "round"), "{stderr}");
    assert!(middle.len() >= min_rounds, "{stderr}");
}
