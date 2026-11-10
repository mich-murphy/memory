//! Hint layer: tolerant hook DTO, strict owned line schema, size bound, resync reader and
//! the canary privacy oracle (DESIGN.md 7.6, 10.1, 15 property 5).
use hegel::{TestCase, generators as gs};
use memory_core::domain::{Agent, SessionId};
use memory_core::hint::*;
use memory_core::privacy::sanitize_launch;
use proptest::prelude::*;
use std::collections::BTreeMap;

type R = Result<(), String>;

fn s(text: &str) -> String {
    text.to_owned()
}
fn sid(text: &str) -> Result<SessionId, String> {
    SessionId::try_from(s(text)).map_err(|e| e.to_string())
}
fn path(text: &str) -> PathHint {
    HintPath::try_from(s(text)).map_or(PathHint::Absent, |path| PathHint::Present { path })
}
fn text(line: &[u8]) -> Result<&str, String> {
    std::str::from_utf8(line).map_err(|e| e.to_string())
}
fn find(haystack: &[u8], needle: &[u8]) -> bool {
    !needle.is_empty() && haystack.windows(needle.len()).any(|w| w == needle)
}
fn agent_of(n: u8) -> Agent {
    match n % 3 {
        0 => Agent::Claude,
        1 => Agent::Codex,
        _ => Agent::Pi,
    }
}
fn event_of(n: u8) -> HintEvent {
    match n % 9 {
        0 => HintEvent::SessionStart,
        1 => HintEvent::TurnEnd,
        2 => HintEvent::TurnFailed,
        3 => HintEvent::SubagentStop,
        4 => HintEvent::PreCompact,
        5 => HintEvent::TaskCompleted,
        6 => HintEvent::TeammateIdle,
        7 => HintEvent::Interrupted,
        _ => HintEvent::SessionEnd,
    }
}
fn input(
    agent: Agent,
    event: HintEvent,
    session: &str,
    cwd: &str,
    tr: &str,
) -> Result<HookInput, String> {
    Ok(HookInput {
        agent,
        event,
        session: sid(session)?,
        cwd: path(cwd),
        transcript: path(tr),
        agent_transcript: path("/t/sub/agent.jsonl"),
    })
}
fn parse(agent: &Agent, json: &str) -> Result<HookInput, HookInputError> {
    parse_hook_input(agent, json.as_bytes())
}

// ---------- goldens ----------

#[test]
fn golden_lines_per_agent() -> R {
    let cases = [
        (
            Agent::Claude,
            r#"{"hook_event_name":"Stop","session_id":"abc-1","cwd":"/w/p","transcript_path":"/t/a.jsonl","prompt":"ignored","extra":{"x":1}}"#,
            r#"{"schema":"memory.hint/1","agent":"claude","event":"turn_end","session":"abc-1","cwd":{"state":"present","path":"/w/p"},"transcript":{"state":"present","path":"/t/a.jsonl"},"agent_transcript":{"state":"absent"},"launch":{"state":"none"},"reduction":"none"}"#,
        ),
        (
            Agent::Codex,
            r#"{"hook_event_name":"SessionEnd","session_id":"c9","cwd":"/w","transcript_path":"/t/c.jsonl"}"#,
            r#"{"schema":"memory.hint/1","agent":"codex","event":"session_end","session":"c9","cwd":{"state":"present","path":"/w"},"transcript":{"state":"present","path":"/t/c.jsonl"},"agent_transcript":{"state":"absent"},"launch":{"state":"none"},"reduction":"none"}"#,
        ),
        (
            Agent::Pi,
            r#"{"event":"session_shutdown","session_id":"p.1","cwd":"/w","session_file":"/t/p.jsonl"}"#,
            r#"{"schema":"memory.hint/1","agent":"pi","event":"session_end","session":"p.1","cwd":{"state":"present","path":"/w"},"transcript":{"state":"present","path":"/t/p.jsonl"},"agent_transcript":{"state":"absent"},"launch":{"state":"none"},"reduction":"none"}"#,
        ),
    ];
    for (agent, stdin, golden) in cases {
        let parsed = parse(&agent, stdin).map_err(|e| e.to_string())?;
        let line = build_line(&parsed, None).map_err(|e| e.to_string())?;
        assert_eq!(text(&line)?, format!("{golden}\n"));
    }
    Ok(())
}

#[test]
fn golden_launch_line_carries_digest_and_allowlisted_values() -> R {
    let argv = [
        s("/usr/bin/claude"),
        s("--model"),
        s("opus"),
        s("hello prompt"),
    ];
    let launch = sanitize_launch(&argv, &[s("ANTHROPIC_MODEL=opus")]);
    let parsed = input(Agent::Claude, HintEvent::SessionStart, "s1", "/w", "/t")?;
    let line = build_line(&parsed, Some(&launch)).map_err(|e| e.to_string())?;
    let out = scan(&line, 0).map_err(|e| e.to_string())?;
    let record = out.records.first().ok_or("record")?;
    let LaunchHint::Full(full) = &record.record.launch else {
        return Err(s("expected full launch"));
    };
    assert_eq!(full.argv(), ["claude", "--model", "opus", "[redacted]"]);
    assert_eq!(
        full.env().get("ANTHROPIC_MODEL").map(String::as_str),
        Some("opus")
    );
    assert!(full.digest_matches());
    assert!(!find(&line, b"hello prompt"));
    assert!(text(&line)?.contains(&String::from(full.digest())));
    Ok(())
}

#[test]
fn only_session_end_and_interrupted_flush() {
    use HintEvent as E;
    let table = [
        (E::SessionStart, false),
        (E::TurnEnd, false),
        (E::TurnFailed, false),
        (E::SubagentStop, false),
        (E::PreCompact, false),
        (E::TaskCompleted, false),
        (E::TeammateIdle, false),
        (E::SessionEnd, true),
        (E::Interrupted, true),
    ];
    for (event, flushes) in table {
        assert_eq!(event.flushes(), flushes, "{event:?}");
    }
}

// ---------- hook input ----------

#[test]
fn event_tables() -> R {
    use HintEvent as E;
    let key = |agent: &Agent| {
        if *agent == Agent::Pi {
            "event"
        } else {
            "hook_event_name"
        }
    };
    let claude: [(&str, E); 8] = [
        ("SessionStart", E::SessionStart),
        ("Stop", E::TurnEnd),
        ("StopFailure", E::TurnFailed),
        ("SubagentStop", E::SubagentStop),
        ("PreCompact", E::PreCompact),
        ("TaskCompleted", E::TaskCompleted),
        ("TeammateIdle", E::TeammateIdle),
        ("SessionEnd", E::SessionEnd),
    ];
    let codex: [(&str, E); 6] = [
        ("SessionStart", E::SessionStart),
        ("Stop", E::TurnEnd),
        ("SubagentStop", E::SubagentStop),
        ("PreCompact", E::PreCompact),
        ("Interrupt", E::Interrupted),
        ("SessionEnd", E::SessionEnd),
    ];
    let pi: [(&str, E); 3] = [
        ("session_start", E::SessionStart),
        ("agent_settled", E::TurnEnd),
        ("session_shutdown", E::SessionEnd),
    ];
    let all = [
        (Agent::Claude, claude.to_vec()),
        (Agent::Codex, codex.to_vec()),
        (Agent::Pi, pi.to_vec()),
    ];
    let names: Vec<&str> = all
        .iter()
        .flat_map(|(_, t)| t.iter().map(|(n, _)| *n))
        .collect();
    for (agent, table) in &all {
        for (name, want) in table {
            let json = format!(r#"{{"{}":"{name}","session_id":"x"}}"#, key(agent));
            assert_eq!(parse(agent, &json).map_err(|e| e.to_string())?.event, *want);
        }
        // Every other known name (and the removed Pi guesses) is rejected for this agent.
        let extra = [
            "turn_end",
            "agent_end",
            "session_before_compact",
            "TurnEnd",
            "Interrupt2",
        ];
        for name in names.iter().copied().chain(extra) {
            if table.iter().any(|(n, _)| n.eq_ignore_ascii_case(name)) {
                continue;
            }
            let json = format!(r#"{{"{}":"{name}","session_id":"x"}}"#, key(agent));
            if let Ok(got) = parse(agent, &json) {
                // Normalisation folds case and underscores only; allow only those spellings.
                let norm = |s: &str| s.replace('_', "").to_ascii_lowercase();
                assert!(
                    table
                        .iter()
                        .any(|(n, e)| norm(n) == norm(name) && *e == got.event),
                    "{name}"
                );
            }
        }
    }
    for guess in ["turn_end", "agent_end", "session_before_compact"] {
        let json = format!(r#"{{"event":"{guess}","session_id":"x"}}"#);
        assert_eq!(parse(&Agent::Pi, &json), Err(HookInputError::UnknownEvent));
    }
    for name in ["StopFailure", "TaskCompleted", "TeammateIdle"] {
        let json = format!(r#"{{"hook_event_name":"{name}","session_id":"x"}}"#);
        assert_eq!(
            parse(&Agent::Codex, &json),
            Err(HookInputError::UnknownEvent)
        );
    }
    assert_eq!(
        parse(
            &Agent::Claude,
            r#"{"hook_event_name":"Interrupt","session_id":"x"}"#
        ),
        Err(HookInputError::UnknownEvent)
    );
    Ok(())
}

#[test]
fn subagent_transcript_is_a_separate_hint() -> R {
    let json = r#"{"hook_event_name":"SubagentStop","session_id":"x","transcript_path":"/t/main.jsonl","agent_transcript_path":"/t/sub.jsonl"}"#;
    let got = parse(&Agent::Claude, json).map_err(|e| e.to_string())?;
    assert_eq!(got.transcript, path("/t/main.jsonl"));
    assert_eq!(got.agent_transcript, path("/t/sub.jsonl"));
    let line = build_line(&got, None).map_err(|e| e.to_string())?;
    let rec = scan(&line, 0)
        .map_err(|e| e.to_string())?
        .records
        .remove(0)
        .record;
    assert_eq!(rec.agent_transcript, path("/t/sub.jsonl"));
    assert_eq!(rec.transcript, path("/t/main.jsonl"));
    let reduced = build_line_with_limit(&got, None, line.len() - 1).map_err(|e| e.to_string())?;
    let rec = scan(&reduced, 0)
        .map_err(|e| e.to_string())?
        .records
        .remove(0)
        .record;
    assert_eq!(rec.reduction, Reduction::PathsDropped);
    assert_eq!(rec.agent_transcript, PathHint::Absent);
    let bad =
        r#"{"hook_event_name":"SubagentStop","session_id":"x","agent_transcript_path":"rel"}"#;
    assert_eq!(
        parse(&Agent::Claude, bad)
            .map_err(|e| e.to_string())?
            .agent_transcript,
        PathHint::Absent
    );
    Ok(())
}

#[test]
fn hook_input_negative_fixtures() {
    let c = Agent::Claude;
    assert_eq!(parse(&c, "not json"), Err(HookInputError::NotJson));
    assert_eq!(parse(&c, ""), Err(HookInputError::NotJson));
    assert_eq!(parse(&c, "[1]"), Err(HookInputError::NotObject));
    assert_eq!(
        parse(&c, r#"{"session_id":"x"}"#),
        Err(HookInputError::MissingEvent)
    );
    assert_eq!(
        parse(&c, r#"{"hook_event_name":5,"session_id":"x"}"#),
        Err(HookInputError::MissingEvent)
    );
    assert_eq!(
        parse(&c, r#"{"hook_event_name":"Nope","session_id":"x"}"#),
        Err(HookInputError::UnknownEvent)
    );
    assert_eq!(
        parse(
            &Agent::Codex,
            r#"{"hook_event_name":"StopFailure","session_id":"x"}"#
        ),
        Err(HookInputError::UnknownEvent)
    );
    let long = "S".repeat(100);
    assert_eq!(
        parse(
            &c,
            &format!(r#"{{"hook_event_name":"{long}","session_id":"x"}}"#)
        ),
        Err(HookInputError::UnknownEvent)
    );
    assert_eq!(
        parse(&c, r#"{"hook_event_name":"Stop"}"#),
        Err(HookInputError::MissingSession)
    );
    assert_eq!(
        parse(&c, r#"{"hook_event_name":"Stop","session_id":7}"#),
        Err(HookInputError::MissingSession)
    );
    for bad in ["", "..", "a/b", "a b", "../x"] {
        assert_eq!(
            parse(
                &c,
                &format!(r#"{{"hook_event_name":"Stop","session_id":"{bad}"}}"#)
            ),
            Err(HookInputError::InvalidSession)
        );
    }
    let big = vec![b' '; MAX_HOOK_INPUT_BYTES + 1];
    assert_eq!(parse_hook_input(&c, &big), Err(HookInputError::TooLarge));
    assert_eq!(
        parse_hook_input(&c, &[0xff, 0xfe]),
        Err(HookInputError::NotJson)
    );
}

#[test]
fn bad_paths_degrade_to_absent_and_unknown_keys_are_tolerated() -> R {
    let json = r#"{"hook_event_name":"Stop","session_id":"x","cwd":"relative","transcript_path":"/a\nb","future_key":[1,2,{"k":null}]}"#;
    let got = parse(&Agent::Claude, json).map_err(|e| e.to_string())?;
    assert_eq!(got.cwd, PathHint::Absent);
    assert_eq!(got.transcript, PathHint::Absent);
    let long = format!("/{}", "a".repeat(MAX_PATH_BYTES));
    assert_eq!(path(&long), PathHint::Absent);
    Ok(())
}

#[test]
fn errors_never_echo_input() {
    let canary = "CANARYQ9Z";
    let all = [
        parse(&Agent::Claude, canary),
        parse(&Agent::Claude, &format!(r#"["{canary}"]"#)),
        parse(
            &Agent::Claude,
            &format!(r#"{{"hook_event_name":"{canary}","session_id":"x"}}"#),
        ),
        parse(
            &Agent::Claude,
            &format!(r#"{{"hook_event_name":"Stop","session_id":"{canary}/"}}"#),
        ),
    ];
    for result in all {
        let shown = match result {
            Err(e) => format!("{e} {e:?}"),
            Ok(_) => String::new(),
        };
        assert!(!shown.is_empty() && !shown.contains(canary));
    }
    assert_eq!(HintPathError.to_string(), "invalid path coordinate");
    assert_eq!(
        BuildError::TooLarge.to_string(),
        "hint line exceeds the size limit"
    );
    assert_eq!(
        BuildError::Encode.to_string(),
        "hint line could not be encoded"
    );
}

// ---------- schema ----------

fn good_line() -> Result<Vec<u8>, String> {
    let parsed = input(Agent::Codex, HintEvent::TurnEnd, "k", "/w", "/t")?;
    build_line(&parsed, None).map_err(|e| e.to_string())
}

#[test]
fn reader_rejects_and_counts_bad_own_lines() -> R {
    let good = good_line()?;
    let g = text(&good)?.trim_end().to_owned();
    let v2 = g.replace("memory.hint/1", "memory.hint/2");
    let foreign = g.replace("memory.hint/1", "other/1");
    let extra = g.replacen("\"agent\"", "\"unknown\":1,\"agent\"", 1);
    let bad_digest = {
        let launch = sanitize_launch(&[s("claude")], &[]);
        let parsed = input(Agent::Claude, HintEvent::SessionStart, "k", "/w", "/t")?;
        let line = build_line(&parsed, Some(&launch)).map_err(|e| e.to_string())?;
        let mut t = text(&line)?.trim_end().to_owned();
        t = t.replace("\"claude\"]", "\"codex\"]");
        t
    };
    let file = format!("{v2}\n{foreign}\n{extra}\n{bad_digest}\n\n{g}\n");
    let out = scan(file.as_bytes(), 0).map_err(|e| e.to_string())?;
    assert_eq!(out.rejected_version, 1);
    assert_eq!(out.dropped, 3);
    assert_eq!(out.records.len(), 1);
    assert_eq!(out.pending_bytes, 0);
    assert_eq!(out.next_offset, file.len() as u64);
    Ok(())
}

#[test]
fn reader_pending_tail_resume_and_midfile_fragment() -> R {
    let good = good_line()?;
    let (head, _) = good.split_at(good.len() - 5);
    let mut file = Vec::new();
    file.extend_from_slice(&good);
    file.extend_from_slice(head); // torn mid-file fragment...
    file.extend_from_slice(&good); // ...glued to the next record
    file.extend_from_slice(head); // torn last line waits
    let out = scan(&file, 0).map_err(|e| e.to_string())?;
    assert_eq!(out.records.len(), 2);
    assert_eq!(out.dropped, 1);
    assert_eq!(out.pending_bytes, head.len());
    let resumed = scan(&file, out.next_offset).map_err(|e| e.to_string())?;
    assert!(resumed.records.is_empty() && resumed.dropped == 0);
    assert_eq!(resumed.pending_bytes, head.len());
    // The pending line completes later.
    let (_, rest) = good.split_at(good.len() - 5);
    file.extend_from_slice(rest);
    let done = scan(&file, out.next_offset).map_err(|e| e.to_string())?;
    assert_eq!(done.records.len(), 1);
    assert_eq!(done.dropped, 0);
    assert_eq!(done.pending_bytes, 0);
    assert!(scan(&file, file.len() as u64 + 1).is_err());
    assert!(
        scan(&file, file.len() as u64)
            .map_err(|e| e.to_string())?
            .records
            .is_empty()
    );
    Ok(())
}

#[test]
fn reader_handles_overlong_fragments_and_lines() -> R {
    let good = good_line()?;
    let mut file = vec![b'x'; 6000];
    file.extend_from_slice(&good);
    file.extend(vec![b'y'; 5000]);
    file.push(b'\n');
    let out = scan(&file, 0).map_err(|e| e.to_string())?;
    assert_eq!(out.records.len(), 1);
    assert_eq!(out.dropped, 2);
    // A marker-less overlong line is garbage.
    let mut junk = vec![b'z'; 9000];
    junk.push(b'\n');
    assert_eq!(scan(&junk, 0).map_err(|e| e.to_string())?.dropped, 1);
    Ok(())
}

// ---------- size bound / roundtrip ----------

fn long_safe(n: u8) -> String {
    "a".repeat(200 + usize::from(n) % 56)
}
fn argv_from(ops: &[u8]) -> Vec<String> {
    let mut argv = vec![s("claude")];
    for op in ops.iter().take(60) {
        argv.push(match op % 6 {
            0 => s("--model"),
            1 => s("--cwd"),
            2 => s("--resume"),
            3 => long_safe(*op),
            4 => s("--session-id"),
            _ => format!("junk {op}"),
        });
    }
    argv
}
fn env_from(ops: &[u8]) -> Vec<String> {
    const NAMES: [&str; 4] = [
        "ANTHROPIC_MODEL",
        "CLAUDE_CONFIG_DIR",
        "MAX_THINKING_TOKENS",
        "OTHER",
    ];
    ops.iter()
        .take(12)
        .map(|op| {
            format!(
                "{}=x{}",
                NAMES.get(usize::from(op % 4)).copied().unwrap_or("OTHER"),
                long_safe(*op)
            )
        })
        .collect()
}
fn long_path(n: u8) -> String {
    format!(
        "/{}",
        "p".repeat((usize::from(n) * 4).min(MAX_PATH_BYTES - 1))
    )
}

fn check_size_and_roundtrip(
    a: u8,
    e: u8,
    argv_ops: &[u8],
    env_ops: &[u8],
    p1: u8,
    p2: u8,
    with_launch: bool,
) -> R {
    let parsed = input(
        agent_of(a),
        event_of(e),
        &format!("sess-{a}-{e}"),
        &long_path(p1),
        &long_path(p2),
    )?;
    let launch = sanitize_launch(&argv_from(argv_ops), &env_from(env_ops));
    let line = build_line(&parsed, with_launch.then_some(&launch)).map_err(|e| e.to_string())?;
    if line.len() > MAX_LINE_BYTES {
        return Err(s("line too long"));
    }
    if line.iter().filter(|b| **b == b'\n').count() != 1 || line.last() != Some(&b'\n') {
        return Err(s("newline discipline"));
    }
    let out = scan(&line, 0).map_err(|e| e.to_string())?;
    let rec = out.records.first().ok_or("no record")?;
    if out.records.len() != 1
        || out.dropped != 0
        || out.pending_bytes != 0
        || rec.end != line.len() as u64
    {
        return Err(s("scan accounting"));
    }
    let r = &rec.record;
    if r.agent != parsed.agent || r.event != parsed.event || r.session != parsed.session {
        return Err(s("identity roundtrip"));
    }
    match (&r.reduction, &r.launch) {
        (Reduction::None, LaunchHint::None) => {
            if with_launch || r.cwd != parsed.cwd || r.transcript != parsed.transcript {
                return Err(s("unreduced mismatch"));
            }
        }
        (Reduction::None, LaunchHint::Full(full)) => {
            if !with_launch
                || full.argv() != launch.argv()
                || full.env() != launch.env()
                || r.cwd != parsed.cwd
                || r.transcript != parsed.transcript
            {
                return Err(s("full launch mismatch"));
            }
        }
        (Reduction::LaunchDropped, LaunchHint::DigestOnly { digest }) => {
            if !with_launch
                || *digest != LaunchRecord::from_sanitized(&launch).digest()
                || r.cwd != parsed.cwd
            {
                return Err(s("launch-dropped mismatch"));
            }
        }
        (Reduction::PathsDropped, _) => {
            if r.cwd != PathHint::Absent
                || r.transcript != PathHint::Absent
                || r.agent_transcript != PathHint::Absent
            {
                return Err(s("paths-dropped mismatch"));
            }
        }
        _ => return Err(s("inconsistent reduction")),
    }
    Ok(())
}

#[test]
fn reductions_are_deterministic_and_ordered() -> R {
    let big = "p".repeat(1000);
    let parsed = input(
        Agent::Claude,
        HintEvent::SessionStart,
        "s",
        &format!("/{big}"),
        &format!("/{big}"),
    )?;
    let launch = sanitize_launch(&argv_from(&[3; 40]), &env_from(&[0; 8]));
    let full = build_line(&parsed, Some(&launch)).map_err(|e| e.to_string())?;
    let dropped =
        build_line_with_limit(&parsed, Some(&launch), full.len() - 1).map_err(|e| e.to_string())?;
    assert!(dropped.len() < full.len());
    assert!(
        scan(&dropped, 0)
            .map_err(|e| e.to_string())?
            .records
            .first()
            .ok_or("r")?
            .record
            .reduction
            == Reduction::LaunchDropped
    );
    let tight = build_line_with_limit(&parsed, Some(&launch), 1000).map_err(|e| e.to_string())?;
    assert!(
        scan(&tight, 0)
            .map_err(|e| e.to_string())?
            .records
            .first()
            .ok_or("r")?
            .record
            .reduction
            == Reduction::PathsDropped
    );
    let tight_none = build_line_with_limit(&parsed, None, 1000).map_err(|e| e.to_string())?;
    assert!(tight_none.len() <= 1000);
    assert_eq!(
        build_line_with_limit(&parsed, Some(&launch), 50),
        Err(BuildError::TooLarge)
    );
    assert_eq!(
        build_line_with_limit(&parsed, None, 50),
        Err(BuildError::TooLarge)
    );
    assert_eq!(
        build_line(&parsed, Some(&launch)),
        build_line(&parsed, Some(&launch))
    );
    Ok(())
}

#[hegel::test]
fn hegel_size_bound_and_roundtrip(tc: TestCase) {
    let a = tc.draw(gs::integers::<u8>());
    let e = tc.draw(gs::integers::<u8>());
    let argv_ops: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
    let env_ops: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
    let p1 = tc.draw(gs::integers::<u8>());
    let p2 = tc.draw(gs::integers::<u8>());
    let with_launch = tc.draw(gs::booleans());
    assert_eq!(
        check_size_and_roundtrip(a, e, &argv_ops, &env_ops, p1, p2, with_launch),
        Ok(())
    );
}

proptest! {
    #[test]
    fn proptest_size_bound_and_roundtrip(
        a in any::<u8>(), e in any::<u8>(),
        argv_ops in prop::collection::vec(any::<u8>(), 0..64),
        env_ops in prop::collection::vec(any::<u8>(), 0..16),
        p1 in any::<u8>(), p2 in any::<u8>(), with_launch in any::<bool>()
    ) {
        prop_assert_eq!(check_size_and_roundtrip(a, e, &argv_ops, &env_ops, p1, p2, with_launch), Ok(()));
    }
}

// ---------- resync reader properties ----------

struct Unit {
    bytes: Vec<u8>,
    record: Option<HintRecord>,
    dropped: usize,
}

fn valid_unit(op: u8, i: usize, launch: bool) -> Result<(Vec<u8>, HintRecord), String> {
    let parsed = input(
        agent_of(op),
        event_of(op / 3),
        &format!("u{i}-{op}"),
        "/w/x",
        "/t/y.jsonl",
    )?;
    let l = sanitize_launch(&[s("codex"), s("--model"), format!("m{op}")], &[]);
    let line = build_line(&parsed, launch.then_some(&l)).map_err(|e| e.to_string())?;
    let rec = scan(&line, 0)
        .map_err(|e| e.to_string())?
        .records
        .into_iter()
        .next()
        .ok_or("rec")?;
    Ok((line, rec.record))
}

fn units(ops: &[u8], noise: &[u8]) -> Result<Vec<Unit>, String> {
    let mut out = Vec::new();
    let mut cursor = 0_usize;
    for (i, op) in ops.iter().take(24).enumerate() {
        match op % 4 {
            1 => {
                let take = 1 + usize::from(op / 4) % 20;
                let mut bytes: Vec<u8> = noise
                    .iter()
                    .copied()
                    .skip(cursor)
                    .take(take)
                    .map(|b| if b == b'\n' { b'x' } else { b })
                    .collect();
                if bytes.is_empty() {
                    bytes.push(b'g');
                }
                cursor += take;
                bytes.push(b'\n');
                out.push(Unit {
                    bytes,
                    record: None,
                    dropped: 1,
                });
            }
            2 => {
                let (line, record) = valid_unit(*op, i, false)?;
                let cut = 1 + usize::from(*op) % line.len().saturating_sub(2).max(1);
                let mut bytes: Vec<u8> = line.iter().copied().take(cut).collect();
                bytes.extend_from_slice(&line);
                out.push(Unit {
                    bytes,
                    record: Some(record),
                    dropped: 1,
                });
            }
            k => {
                let (bytes, record) = valid_unit(*op, i, k == 3)?;
                out.push(Unit {
                    bytes,
                    record: Some(record),
                    dropped: 0,
                });
            }
        }
    }
    Ok(out)
}

fn check_interleave(ops: &[u8], noise: &[u8], cut: u16) -> R {
    let units = units(ops, noise)?;
    let file: Vec<u8> = units.iter().flat_map(|u| u.bytes.iter().copied()).collect();
    let at = usize::from(cut) % (file.len() + 1);
    let prefix: Vec<u8> = file.iter().copied().take(at).collect();
    let (mut end, mut want, mut dropped) = (0_usize, Vec::new(), 0_usize);
    for unit in &units {
        if end + unit.bytes.len() > at {
            break;
        }
        end += unit.bytes.len();
        dropped += unit.dropped;
        if let Some(r) = &unit.record {
            want.push((end as u64, r.clone()));
        }
    }
    let got = scan(&prefix, 0).map_err(|e| e.to_string())?;
    let have: Vec<_> = got
        .records
        .iter()
        .map(|r| (r.end, r.record.clone()))
        .collect();
    if have != want {
        return Err(s("records differ"));
    }
    if got.dropped != dropped || got.rejected_version != 0 {
        return Err(format!("dropped {} want {dropped}", got.dropped));
    }
    if got.next_offset != end as u64 || got.pending_bytes != at - end {
        return Err(s("offsets"));
    }
    // Resume from the saved offset: nothing new, same pending tail; and any start is safe.
    let again = scan(&prefix, got.next_offset).map_err(|e| e.to_string())?;
    if !again.records.is_empty() || again.pending_bytes != got.pending_bytes {
        return Err(s("resume"));
    }
    let mid = scan(&prefix, (at / 2) as u64).map_err(|e| e.to_string())?;
    if mid.next_offset + mid.pending_bytes as u64 != at as u64 {
        return Err(s("mid-start accounting"));
    }
    Ok(())
}

fn check_garbage(data: &[u8], start: u8) -> R {
    let from = usize::from(start) % (data.len() + 1);
    let out = scan(data, from as u64).map_err(|e| e.to_string())?;
    if out.next_offset + out.pending_bytes as u64 != data.len() as u64 {
        return Err(s("accounting"));
    }
    let mut last = from as u64;
    for r in &out.records {
        if r.end <= last || r.end > out.next_offset {
            return Err(s("ends not monotonic"));
        }
        last = r.end;
    }
    Ok(())
}

#[hegel::test]
fn hegel_interleavings_and_truncations(tc: TestCase) {
    let ops: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
    let noise: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
    let cut = tc.draw(gs::integers::<u16>());
    assert_eq!(check_interleave(&ops, &noise, cut), Ok(()));
}

#[hegel::test]
fn hegel_reader_never_panics_on_arbitrary_bytes(tc: TestCase) {
    let data: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
    let start = tc.draw(gs::integers::<u8>());
    assert_eq!(check_garbage(&data, start), Ok(()));
}

proptest! {
    #[test]
    fn proptest_interleavings_and_truncations(
        ops in prop::collection::vec(any::<u8>(), 0..24),
        noise in prop::collection::vec(any::<u8>(), 0..200),
        cut in any::<u16>()
    ) {
        prop_assert_eq!(check_interleave(&ops, &noise, cut), Ok(()));
    }

    #[test]
    fn proptest_reader_never_panics_on_arbitrary_bytes(
        data in prop::collection::vec(any::<u8>(), 0..6000), start in any::<u8>()
    ) {
        prop_assert_eq!(check_garbage(&data, start), Ok(()));
    }
}

// ---------- privacy canaries ----------

fn canary(n: u64, k: u8) -> String {
    format!("CNRY{n:016x}K{k}")
}

fn check_canaries(n: u64, session: u8) -> R {
    let c: [String; 8] = std::array::from_fn(|k| canary(n, u8::try_from(k).unwrap_or(0)));
    let [c0, c1, c2, c3, c4, c5, c6, c7] = &c;
    let argv = [
        s("/opt/bin/claude"),
        s("--api-key"),
        c0.clone(),
        format!("--api-key={}", c1),
        format!("--system-prompt={}", c2),
        s("--mcp-config"),
        format!(r#"{{"headers":{{"Authorization":"Bearer {}"}}}}"#, c3),
        c4.clone(),
    ];
    let environ = [
        format!("OPENAI_API_KEY={}", c5),
        format!("PROMPT_TEXT={}", c6),
        format!("ANTHROPIC_AUTH_TOKEN={}", c7),
        s("ANTHROPIC_MODEL=opus"),
    ];
    let launch = sanitize_launch(&argv, &environ);
    let hook = format!(
        r#"{{"hook_event_name":"SessionStart","session_id":"sess{session}","cwd":"/w","prompt":"{0}","tool_input":{{"cmd":"{1}"}},"last_assistant_message":"{2}","unknown":"{3}"}}"#,
        c4, c5, c6, c7
    );
    let parsed = parse_hook_input(&Agent::Claude, hook.as_bytes()).map_err(|e| e.to_string())?;
    let line = build_line(&parsed, Some(&launch)).map_err(|e| e.to_string())?;
    for needle in c.iter() {
        if find(&line, needle.as_bytes()) {
            return Err(format!("canary {needle} leaked"));
        }
        if find(&line, needle.to_lowercase().as_bytes()) {
            return Err(s("lowercased canary leaked"));
        }
    }
    // Positive control: the oracle does see an allowlisted metadata value.
    let control = canary(n, 9);
    let kept = sanitize_launch(&[s("claude"), s("--model"), control.clone()], &[]);
    let kept_line = build_line(&parsed, Some(&kept)).map_err(|e| e.to_string())?;
    if !find(&kept_line, control.as_bytes()) {
        return Err(s("oracle is vacuous"));
    }
    Ok(())
}

// ---------- generated launch records (DESIGN.md 8.4 oracle, independent of `safe()`) ----------
//
// Each generated piece carries its own expectation, authored from the 8.4 text: a value
// flag followed by a value that is unsafe BY CONSTRUCTION (whitespace, leading dash, shell
// metacharacters, a secret-name segment, over-long or non-ASCII) must publish
// `[redacted]`; a value from the safe vocabulary must publish verbatim. The expected argv is
// compared exactly, so neither a leak nor an over-redaction passes.

const DESIGN_VALUE_FLAGS: [&str; 9] = [
    "--model",
    "-m",
    "--provider",
    "--permission-mode",
    "--session-id",
    "--session",
    "--config",
    "--config-dir",
    "--cwd",
];
const DESIGN_ENV_NAMES: [&str; 9] = [
    "ANTHROPIC_MODEL",
    "ANTHROPIC_SMALL_FAST_MODEL",
    "ANTHROPIC_DEFAULT_OPUS_MODEL",
    "ANTHROPIC_DEFAULT_SONNET_MODEL",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL",
    "CLAUDE_CONFIG_DIR",
    "MAX_THINKING_TOKENS",
    "MAX_MCP_OUTPUT_TOKENS",
    "DISABLE_PROMPT_CACHING",
];
const NOT_ALLOWLISTED_ENV: [&str; 7] = [
    "UNKNOWN",
    "PROMPT_TEXT",
    "OPENAI_API_KEY",
    "ANTHROPIC_API_KEY",
    "ANTHROPIC_AUTH_TOKEN",
    "anthropic_model",
    "ANTHROPIC_MODEL_X",
];
const SAFE_VOCABULARY: [&str; 6] = [
    "opus",
    "claude-sonnet-4.5",
    "a/b:c@d+e",
    "gpt5",
    "opt/cfg/dir",
    "bypassPermissions",
];
const KEPT_FLAGS: [&str; 4] = ["--resume", "--continue", "--no-session", "--full-auto"];
const UNKNOWN_FLAGS: [&str; 5] = ["--prompt", "--api-key", "--system-prompt", "--token", "-p"];
const REDACTED: &str = "[redacted]";

fn unsafe_value(c: &str, d: u8) -> String {
    match d % 12 {
        0 => format!("{c} tail"),
        1 => format!("-{c}"),
        2 => format!(".{c}"),
        3 => format!("{c}$HOME"),
        4 => format!("{c}-token"),
        5 => format!("{c}_api_key"),
        6 => format!("{c}.secret"),
        7 => format!("my_passwd_{c}"),
        8 => format!("{c}{}", "a".repeat(256)),
        9 => format!("{c}\u{e9}"),
        10 => format!("{c}\n"),
        _ => format!("{c};rm"),
    }
}
fn nth(items: &[&str], d: u8) -> String {
    items
        .get(usize::from(d) % items.len())
        .map_or_else(String::new, |item| (*item).to_owned())
}

struct Expected {
    argv_in: Vec<String>,
    argv_out: Vec<String>,
    env_in: Vec<String>,
    env_out: BTreeMap<String, String>,
    env_offered: BTreeMap<String, Vec<String>>,
    canaries: Vec<String>,
}

/// One argv piece: its input entries and the entries DESIGN 8.4 says are published.
fn argv_piece(kind: u8, d: u8, c: &str) -> (Vec<String>, Vec<String>) {
    let flag = nth(&DESIGN_VALUE_FLAGS, d);
    let safe = nth(&SAFE_VOCABULARY, d);
    match kind % 9 {
        0 => (
            vec![flag.clone(), unsafe_value(c, d)],
            vec![flag, s(REDACTED)],
        ),
        1 => (
            vec![format!("{flag}={}", unsafe_value(c, d))],
            vec![flag, s(REDACTED)],
        ),
        2 => (vec![flag.clone(), safe.clone()], vec![flag, safe]),
        3 => (vec![format!("{flag}={safe}")], vec![flag, safe]),
        4 => (
            vec![nth(&UNKNOWN_FLAGS, d), s(c)],
            vec![s(REDACTED), s(REDACTED)],
        ),
        5 => (vec![s(c)], vec![s(REDACTED)]),
        6 => (
            vec![format!("{}={c}", nth(&KEPT_FLAGS, d))],
            vec![s(REDACTED)],
        ),
        7 => (vec![nth(&KEPT_FLAGS, d)], vec![nth(&KEPT_FLAGS, d)]),
        _ => (
            vec![format!(
                r#"{{"headers":{{"Authorization":"Bearer {c}"}},"env":{{"API_KEY":"{c}"}}}}"#
            )],
            vec![s(REDACTED)],
        ),
    }
}

fn expected_launch(
    n: u64,
    argv_ops: &[u16],
    env_ops: &[u16],
    program: u8,
    dangling: bool,
) -> Expected {
    let mut canaries = Vec::new();
    let mut fresh = |k: usize| {
        let c = canary(n, u8::try_from(k % 200).unwrap_or(0));
        canaries.push(c.clone());
        c
    };
    let (prog_in, prog_out) = match program % 5 {
        0 => (s("/opt/bin/claude"), s("claude")),
        1 => (format!("/x/{}/node", fresh(0)), s("node")),
        2 => (format!("{}/pi", fresh(0)), s("pi")),
        3 => (fresh(0), s(REDACTED)),
        _ => (format!("/usr/bin/{}", fresh(0)), s(REDACTED)),
    };
    let mut argv_in = vec![prog_in];
    let mut argv_out = vec![prog_out];
    for (i, op) in argv_ops.iter().enumerate() {
        let [kind, d] = op.to_le_bytes();
        let (entries_in, entries_out) = argv_piece(kind, d, &fresh(i + 1));
        if argv_in.len() + entries_in.len() > 32 {
            // Past the 32-entry bound: everything further must be dropped, canaries too.
            argv_in.extend(entries_in);
            continue;
        }
        argv_in.extend(entries_in);
        argv_out.extend(entries_out);
    }
    if dangling && argv_in.len() < 32 {
        argv_in.push(s("--model"));
        argv_out.extend([s("--model"), s(REDACTED)]);
    }
    argv_out.truncate(32);
    let mut env_in = Vec::new();
    let mut env_out: BTreeMap<String, String> = BTreeMap::new();
    let mut env_offered: BTreeMap<String, Vec<String>> = BTreeMap::new();
    for (i, op) in env_ops.iter().take(12).enumerate() {
        let [kind, d] = op.to_le_bytes();
        let name = nth(&DESIGN_ENV_NAMES, d);
        let c = fresh(100 + i);
        match kind % 6 {
            0 => env_in.push(format!("{name}={}", unsafe_value(&c, d))),
            1 => env_in.push(format!("{}={c}", nth(&NOT_ALLOWLISTED_ENV, d))),
            2 | 3 => {
                env_in.push(format!("{name}={}", nth(&SAFE_VOCABULARY, d)));
                env_offered
                    .entry(name.clone())
                    .or_default()
                    .push(nth(&SAFE_VOCABULARY, d));
                env_out.insert(name, nth(&SAFE_VOCABULARY, d));
            }
            4 => env_in.push(c),
            _ => env_in.push(name),
        }
    }
    Expected {
        argv_in,
        argv_out,
        env_in,
        env_out,
        env_offered,
        canaries,
    }
}

fn check_generated_launch(
    n: u64,
    argv_ops: &[u16],
    env_ops: &[u16],
    program: u8,
    dangling: bool,
) -> R {
    let ex = expected_launch(n, argv_ops, env_ops, program, dangling);
    let launch = sanitize_launch(&ex.argv_in, &ex.env_in);
    if launch.argv() != ex.argv_out.as_slice() {
        return Err(format!(
            "argv {:?} != expected {:?}",
            launch.argv(),
            ex.argv_out
        ));
    }
    if ex.env_out.len() <= 8 {
        if launch.env() != &ex.env_out {
            return Err(format!(
                "env {:?} != expected {:?}",
                launch.env(),
                ex.env_out
            ));
        }
    } else {
        // DESIGN 8.4: at most 8 names; which 8 is not specified, so only the bound and
        // "every published pair was a safe offer" are asserted.
        let offered_only = launch.env().iter().all(|(name, value)| {
            ex.env_offered
                .get(name)
                .is_some_and(|values| values.contains(value))
        });
        if launch.env().len() != 8 || !offered_only {
            return Err(format!("env bound violated: {:?}", launch.env()));
        }
    }
    let parsed = input(
        Agent::Claude,
        HintEvent::SessionStart,
        "sess",
        "/w",
        "/t.jsonl",
    )?;
    let line = build_line(&parsed, Some(&launch)).map_err(|e| e.to_string())?;
    for needle in &ex.canaries {
        if find(&line, needle.as_bytes()) || find(&line, needle.to_lowercase().as_bytes()) {
            return Err(format!("canary {needle} reached the hint line"));
        }
    }
    Ok(())
}

#[test]
fn generated_launch_fixed_shapes() {
    // Every argv kind x decoration and every env kind, so a plain run fails without
    // generation (mutant: `safe()` always true).
    let argv: Vec<u16> = (0..9u16)
        .flat_map(|k| (0..12u16).map(move |d| (d << 8) | k))
        .collect();
    for chunk in argv.chunks(12) {
        assert_eq!(check_generated_launch(7, chunk, &[], 0, false), Ok(()));
    }
    let env: Vec<u16> = (0..6u16)
        .flat_map(|k| (0..12u16).map(move |d| (d << 8) | k))
        .collect();
    for chunk in env.chunks(8) {
        assert_eq!(check_generated_launch(7, &[], chunk, 1, true), Ok(()));
    }
    // Nine distinct allowlisted names with safe values: the ninth is dropped (cap of 8).
    let nine: Vec<u16> = (0..9u16).map(|d| (d << 8) | 2).collect();
    assert_eq!(check_generated_launch(7, &[], &nine, 0, false), Ok(()));
    // 40 single-entry pieces: the 32-entry bound drops the tail, canaries included.
    let long = vec![5u16; 40];
    assert_eq!(check_generated_launch(7, &long, &[], 0, true), Ok(()));
}

#[hegel::test]
fn hegel_generated_launch_records_publish_no_canary(tc: TestCase) {
    let n = tc.draw(gs::integers::<u64>());
    let argv_ops: Vec<u16> = tc.draw(gs::vecs(gs::integers::<u16>()).max_size(40));
    let env_ops: Vec<u16> = tc.draw(gs::vecs(gs::integers::<u16>()).max_size(12));
    let program = tc.draw(gs::integers::<u8>());
    let dangling = tc.draw(gs::booleans());
    assert_eq!(
        check_generated_launch(n, &argv_ops, &env_ops, program, dangling),
        Ok(())
    );
}

proptest! {
    #[test]
    fn proptest_generated_launch_records_publish_no_canary(
        n in any::<u64>(),
        argv_ops in prop::collection::vec(any::<u16>(), 0..40),
        env_ops in prop::collection::vec(any::<u16>(), 0..12),
        program in any::<u8>(),
        dangling in any::<bool>(),
    ) {
        prop_assert_eq!(check_generated_launch(n, &argv_ops, &env_ops, program, dangling), Ok(()));
    }
}

#[test]
fn canaries_fixed_seed() {
    assert_eq!(check_canaries(0xdead_beef, 3), Ok(()));
}

#[hegel::test]
fn hegel_no_canary_in_any_line(tc: TestCase) {
    let n = tc.draw(gs::integers::<u64>());
    let session = tc.draw(gs::integers::<u8>());
    assert_eq!(check_canaries(n, session), Ok(()));
}

proptest! {
    #[test]
    fn proptest_no_canary_in_any_line(n in any::<u64>(), session in any::<u8>()) {
        prop_assert_eq!(check_canaries(n, session), Ok(()));
    }
}
