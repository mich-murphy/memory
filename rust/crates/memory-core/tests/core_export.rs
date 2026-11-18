//! `Timestamp::unix_ns` against independent oracles, and `export_role` against `locate`.
use memory_core::domain::{Agent, ArchiveId, MachineId, Owner};
use memory_core::hint::day_number;
use memory_core::locator::{
    ClaudeSessionIndex, ExportRole, Located, Role, RootLayout, RootNamespace, RootRelativePath,
    Roots, export_role, locate,
};
use memory_core::manifest::Timestamp;
use proptest::prelude::*;

type R = Result<(), Box<dyn std::error::Error>>;

fn ts(text: &str) -> Result<Timestamp, Box<dyn std::error::Error>> {
    Ok(Timestamp::try_from(text.to_owned())?)
}

#[test]
fn unix_ns_known_instants() -> R {
    // Seconds since the epoch computed independently (Python calendar.timegm).
    let table: [(&str, Option<u64>); 11] = [
        ("1970-01-01T00:00:00Z", Some(0)),
        ("1999-12-31T23:59:59Z", Some(946_684_799_000_000_000)),
        ("2000-01-01T00:00:00Z", Some(946_684_800_000_000_000)),
        ("2024-02-29T12:00:00Z", Some(1_709_208_000_000_000_000)),
        ("2026-11-17T22:50:00Z", Some(1_794_955_800_000_000_000)),
        ("2100-03-01T00:00:00Z", Some(4_107_542_400_000_000_000)),
        ("2262-04-11T23:47:16Z", Some(9_223_372_036_000_000_000)),
        ("2262-04-11T23:47:17Z", None),
        ("1969-12-31T23:59:59Z", None),
        ("2023-02-29T00:00:00Z", None),
        ("2026-04-31T00:00:00Z", None),
    ];
    for (text, want) in table {
        assert_eq!(ts(text)?.unix_ns(), want, "{text}");
    }
    Ok(())
}

proptest! {
    #[test]
    fn proptest_unix_ns_matches_the_day_count(
        y in 1960_u32..2300, m in 1_u32..=12, d in 1_u32..=31,
        h in 0_u32..24, i in 0_u32..60, s in 0_u32..60,
    ) {
        let day = format!("{y:04}-{m:02}-{d:02}");
        let text = format!("{day}T{h:02}:{i:02}:{s:02}Z");
        let t = ts(&text).map_err(|e| TestCaseError::fail(e.to_string()))?;
        let want = day_number(&day).and_then(|days| {
            let secs = days.checked_mul(86_400)?
                .checked_add(i64::from(h * 3600 + i * 60 + s))?;
            let ns = u64::try_from(secs.checked_mul(1_000_000_000)?).ok()?;
            Some(ns)
        });
        prop_assert_eq!(t.unix_ns(), want, "{}", text);
    }
}

fn roots(agent: Agent, layout: RootLayout) -> Result<Roots, Box<dyn std::error::Error>> {
    let mut index = ClaudeSessionIndex::new();
    index.record("11111111-2222-3333-4444-555555555555", "-p")?;
    Ok(Roots::new(
        ArchiveId::try_from("a".to_owned())?,
        MachineId::try_from("m".to_owned())?,
        agent,
        RootNamespace::new("r1")?,
        layout,
    )
    .with_claude_sessions(index))
}

const U: &str = "11111111-2222-3333-4444-555555555555";

fn vocab() -> Vec<String> {
    let mut v: Vec<String> = [
        "projects",
        "-p",
        "sessions",
        "archived_sessions",
        "2026",
        "subagents",
        "tool-results",
        "tasks",
        "forks",
        "run-1",
        "run-0",
        "session.jsonl",
        "--cwd--",
        "x",
        "a.jsonl",
        "debug",
        "memory",
        "history.jsonl",
        "notes.txt",
    ]
    .iter()
    .map(|s| (*s).to_owned())
    .collect();
    v.push(U.to_owned());
    v.push(format!("{U}.jsonl"));
    v.push(format!("{U}.orphaned-x.jsonl"));
    v.push(format!("{U}.jsonl.superseded-1"));
    v.push(format!("{U}.zst"));
    v.push(format!("2026-01-02T03-04-05.123Z_{U}"));
    v.push(format!("2026-01-02T03-04-05.123Z_{U}.jsonl"));
    v.push(format!("rollout-2026-01-02T03-04-05-{U}.jsonl"));
    v.push(format!("rollout-2026-01-02T03-04-05-{U}.jsonl.zst"));
    v
}

fn mapped(role: Role) -> ExportRole {
    match role {
        Role::Main => ExportRole::Main,
        Role::Superseded => ExportRole::Transcript,
        Role::Child => ExportRole::Subagent,
        _ => ExportRole::Artifact,
    }
}

/// Returns the (checked, main, transcript, subagent) counts for coverage.
fn check_path(parts: &[&str], all: &[Roots]) -> Result<[usize; 4], String> {
    let mut n = [0; 4];
    let Ok(path) = RootRelativePath::parse(&parts.join("/")) else {
        return Ok(n);
    };
    for r in all {
        if let Located::Source {
            owner: Owner::Session(_),
            kind,
            role,
            ..
        } = locate(&path, r)
        {
            let got = export_role(&kind, &parts.join("/"));
            let want = mapped(role);
            if got != want {
                return Err(format!("{parts:?} {kind:?} {role:?} -> {got:?}"));
            }
            n[0] += 1;
            match got {
                ExportRole::Main => n[1] += 1,
                ExportRole::Transcript => n[2] += 1,
                ExportRole::Subagent => n[3] += 1,
                ExportRole::Artifact => {}
            }
        }
    }
    Ok(n)
}

fn all_roots() -> Result<Vec<Roots>, Box<dyn std::error::Error>> {
    Ok(vec![
        roots(Agent::Claude, RootLayout::AgentDir)?,
        roots(Agent::Codex, RootLayout::AgentDir)?,
        roots(Agent::Pi, RootLayout::AgentDir)?,
        roots(Agent::Pi, RootLayout::PiCustomRoot)?,
    ])
}

#[test]
fn export_role_equals_the_locator_role_for_every_short_path() -> R {
    let all = all_roots()?;
    let v = vocab();
    let mut totals = [0_usize; 4];
    let mut add = |n: [usize; 4]| totals.iter_mut().zip(n).for_each(|(t, x)| *t += x);
    for a in &v {
        add(check_path(&[a], &all)?);
        for b in &v {
            add(check_path(&[a, b], &all)?);
            for c in &v {
                add(check_path(&[a, b, c], &all)?);
                for d in &v {
                    add(check_path(&[a, b, c, d], &all)?);
                }
            }
        }
    }
    let [checked, main, transcript, subagent] = totals;
    assert!(
        checked > 0 && main > 0 && transcript > 0 && subagent > 0,
        "{totals:?}"
    );
    Ok(())
}

#[test]
fn export_role_named_rows() {
    use memory_core::domain::SourceKind as K;
    let rows = [
        (
            K::ClaudeTranscript,
            format!("projects/-p/{U}.jsonl"),
            ExportRole::Main,
        ),
        (
            K::ClaudeTranscript,
            format!("projects/-p/{U}.orphaned-x.jsonl"),
            ExportRole::Transcript,
        ),
        (
            K::ClaudeSubagent,
            format!("projects/-p/{U}/subagents/a.jsonl"),
            ExportRole::Subagent,
        ),
        (
            K::ClaudeToolResults,
            format!("projects/-p/{U}/tool-results/a"),
            ExportRole::Artifact,
        ),
        (
            K::PiSession,
            "--c--/2026-01-02T03-04-05.123Z_".to_owned() + U + "/tasks/a.jsonl",
            ExportRole::Subagent,
        ),
        (
            K::CodexRollout,
            "sessions/x.jsonl".to_owned(),
            ExportRole::Main,
        ),
    ];
    for (kind, path, want) in rows {
        assert_eq!(export_role(&kind, &path), want, "{path}");
    }
}

proptest! {
    #[test]
    fn proptest_export_role_matches_locate(picks in prop::collection::vec(0_usize..64, 1..7)) {
        let all = all_roots().map_err(|e| TestCaseError::fail(e.to_string()))?;
        let v = vocab();
        let parts: Vec<&str> = picks.iter().filter_map(|p| v.get(p % v.len())).map(String::as_str).collect();
        prop_assert!(check_path(&parts, &all).is_ok(), "{:?}", check_path(&parts, &all));
    }
}
