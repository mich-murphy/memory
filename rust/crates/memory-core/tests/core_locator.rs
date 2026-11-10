//! Locator acceptance: table-driven matrix rows (separately authored expectations) and
//! property 7 (total, sibling-stable, every unmapped path countable) on Hegel and proptest.
use hegel::{TestCase, generators as gs};
use memory_core::domain::{
    Agent, ArchiveId, MachineId, Owner, SourceClass, SourceKind, WithholdReason,
};
use memory_core::locator::{
    ClaudeSessionIndex, ConfigAsset, InputError, Located, Role, RootLayout, RootNamespace,
    RootRelativePath, Roots, locate,
};
use proptest::prelude::*;
use sha2::{Digest, Sha256};
use std::collections::HashSet;
use std::fmt::Write;
use std::sync::LazyLock;

type TestResult = Result<(), Box<dyn std::error::Error>>;

macro_rules! ensure {
    ($c:expr, $($a:tt)*) => {
        if !$c {
            return Err(format!($($a)*).into());
        }
    };
}

const U1: &str = "11111111-2222-3333-4444-555555555555";
const U2: &str = "22222222-2222-3333-4444-555555555555";
const U3: &str = "33333333-2222-3333-4444-555555555555";
const UA: &str = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
const UB: &str = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff";

#[derive(Clone, Copy, Debug, PartialEq)]
enum Home {
    Claude,
    Codex,
    Pi,
    ClaudePrimary,
    PiCustom,
    CodexOnCustom,
    ClaudeOnCustom,
}

fn agent_of(home: Home) -> Agent {
    match home {
        Home::Claude | Home::ClaudePrimary | Home::ClaudeOnCustom => Agent::Claude,
        Home::Codex | Home::CodexOnCustom => Agent::Codex,
        Home::Pi | Home::PiCustom => Agent::Pi,
    }
}

/// Shell facts shared by every table row: U1 unique, U2 seen under two slugs.
fn base_facts() -> Vec<(String, String)> {
    vec![
        (U1.to_owned(), "-p".to_owned()),
        (U2.to_owned(), "-p".to_owned()),
        (U2.to_owned(), "-q".to_owned()),
    ]
}

fn make_roots(
    home: Home,
    ns: &str,
    extra: &[(String, String)],
    reverse: bool,
) -> Result<Roots, Box<dyn std::error::Error>> {
    let mut facts = base_facts();
    facts.extend(extra.iter().cloned());
    if reverse {
        facts.reverse();
    }
    let mut index = ClaudeSessionIndex::new();
    for (u, s) in &facts {
        index.record(u, s)?;
    }
    let layout = match home {
        Home::ClaudePrimary => RootLayout::ClaudePrimaryHome,
        Home::PiCustom | Home::CodexOnCustom | Home::ClaudeOnCustom => RootLayout::PiCustomRoot,
        _ => RootLayout::AgentDir,
    };
    Ok(Roots::new(
        ArchiveId::try_from("a".to_owned())?,
        MachineId::try_from("m".to_owned())?,
        agent_of(home),
        RootNamespace::new(ns)?,
        layout,
    )
    .with_claude_sessions(index))
}

#[derive(Debug)]
enum Exp {
    Src {
        kind: SourceKind,
        class: SourceClass,
        role: Role,
        owner: Option<String>,
        id: String,
    },
    Kind,
    Owner,
}

struct Row {
    home: Home,
    path: String,
    exp: Exp,
}

/// Expected strings are given pre-escaped, without the `r1:` namespace prefix.
fn src(kind: SourceKind, class: SourceClass, role: Role, owner: Option<&str>, id: &str) -> Exp {
    Exp::Src {
        kind,
        class,
        role,
        owner: owner.map(|o| format!("r1:{o}")),
        id: format!("r1:{id}"),
    }
}

fn row(home: Home, path: impl Into<String>, exp: Exp) -> Row {
    Row {
        home,
        path: path.into(),
        exp,
    }
}

fn esc(s: &str) -> String {
    s.bytes()
        .map(|b| {
            if b.is_ascii_alphanumeric() || b == b'.' || b == b'-' {
                char::from(b).to_string()
            } else {
                format!("_{b:02x}")
            }
        })
        .collect()
}

fn eid(path: &str) -> String {
    path.split('/').map(esc).collect::<Vec<_>>().join(":")
}

fn matches(got: &Located, exp: &Exp, agent: &Agent) -> bool {
    match (got, exp) {
        (Located::UnknownKind, Exp::Kind) | (Located::UnknownOwner, Exp::Owner) => true,
        (
            Located::Source {
                owner,
                kind,
                class,
                id,
                role,
            },
            Exp::Src {
                kind: k,
                class: c,
                role: r,
                owner: o,
                id: i,
            },
        ) => {
            let owner_ok = match (owner, o) {
                (
                    Owner::Shared {
                        archive,
                        machine,
                        agent: a,
                    },
                    None,
                ) => archive.as_str() == "a" && machine.as_str() == "m" && a == agent,
                (Owner::Session(key), Some(s)) => {
                    key.archive.as_str() == "a"
                        && key.machine.as_str() == "m"
                        && &key.agent == agent
                        && key.session.as_str() == s
                }
                _ => false,
            };
            owner_ok && kind == k && class == c && role == r && id.as_str() == i
        }
        _ => false,
    }
}

fn rows() -> Vec<Row> {
    use ConfigAsset as C;
    use Role as R;
    use SourceClass::{Append as A, Snapshot as S};
    use SourceKind as K;
    let mut v = Vec::new();
    let cl = Home::Claude;

    // ---- Claude transcripts and session-local artifacts ----
    v.push(row(
        cl,
        "projects/-p/abc.jsonl",
        src(
            K::ClaudeTranscript,
            A,
            R::Main,
            Some("-p:abc"),
            "projects:-p:abc.jsonl",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/abc.orphaned-x.jsonl",
        src(
            K::ClaudeTranscript,
            A,
            R::Superseded,
            Some("-p:abc"),
            "projects:-p:abc.orphaned-x.jsonl",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/abc.jsonl.superseded-1",
        src(
            K::ClaudeTranscript,
            A,
            R::Superseded,
            Some("-p:abc"),
            "projects:-p:abc.jsonl.superseded-1",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/abc/subagents/agent-1.jsonl",
        src(
            K::ClaudeSubagent,
            A,
            R::Child,
            Some("-p:abc"),
            "projects:-p:abc:subagents:agent-1.jsonl",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/abc/subagents/w/x.jsonl",
        src(
            K::ClaudeSubagent,
            A,
            R::Child,
            Some("-p:abc"),
            "projects:-p:abc:subagents:w:x.jsonl",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/abc/subagents/meta.json",
        src(
            K::ClaudeAux,
            S,
            R::Artifact,
            Some("-p:abc"),
            "projects:-p:abc:subagents:meta.json",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/abc/tool-results/t.txt",
        src(
            K::ClaudeToolResults,
            A,
            R::Artifact,
            Some("-p:abc"),
            "projects:-p:abc:tool-results:t.txt",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/abc/tool-results/d/t.txt",
        src(
            K::ClaudeToolResults,
            A,
            R::Artifact,
            Some("-p:abc"),
            "projects:-p:abc:tool-results:d:t.txt",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/abc/tool-results",
        src(
            K::ClaudeAux,
            S,
            R::Artifact,
            Some("-p:abc"),
            "projects:-p:abc:tool-results",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/abc/other/x.txt",
        src(
            K::ClaudeAux,
            S,
            R::Artifact,
            Some("-p:abc"),
            "projects:-p:abc:other:x.txt",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/abc/x.txt",
        src(
            K::ClaudeAux,
            S,
            R::Artifact,
            Some("-p:abc"),
            "projects:-p:abc:x.txt",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/memory/MEMORY.md",
        src(
            K::ClaudeMemory,
            S,
            R::Artifact,
            None,
            "projects:-p:memory:MEMORY.md",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/é.jsonl",
        src(
            K::ClaudeTranscript,
            A,
            R::Main,
            Some("-p:_c3_a9"),
            "projects:-p:_c3_a9.jsonl",
        ),
    ));
    v.push(row(
        cl,
        "projects/-p/a_b.jsonl",
        src(
            K::ClaudeTranscript,
            A,
            R::Main,
            Some("-p:a_5fb"),
            "projects:-p:a_5fb.jsonl",
        ),
    ));

    // ---- Claude external per-session artifacts (owner from shell UUID facts) ----
    for (dir, kind, class) in [
        ("file-history", K::ClaudeFileHistory, S),
        ("session-env", K::ClaudeSessionEnv, S),
        ("todos", K::ClaudeTodos, S),
        ("tasks", K::ClaudeTasks, S),
        ("debug", K::ClaudeDebug, A),
    ] {
        let path = format!("{dir}/{U1}/x");
        let id = eid(&path);
        let owner = format!("-p:{U1}");
        v.push(row(
            cl,
            path,
            src(kind, class, R::Artifact, Some(&owner), &id),
        ));
        for bad in [
            format!("{dir}/{U3}/x"),
            format!("{dir}/{U2}/x"),
            format!("{dir}/not-a-uuid.txt"),
            format!("{dir}/{U1}x.json"),
        ] {
            v.push(row(cl, bad, Exp::Owner));
        }
    }
    v.push(row(
        cl,
        "file-history/".to_owned() + U1 + "/abc@v1",
        src(
            K::ClaudeFileHistory,
            S,
            R::Artifact,
            Some(&format!("-p:{U1}")),
            &format!("file-history:{U1}:abc_40v1"),
        ),
    ));
    v.push(row(
        cl,
        format!("debug/{U1}.txt"),
        src(
            K::ClaudeDebug,
            A,
            R::Artifact,
            Some(&format!("-p:{U1}")),
            &format!("debug:{U1}.txt"),
        ),
    ));
    v.push(row(
        cl,
        format!("todos/{U1}-agent-{U1}.json"),
        src(
            K::ClaudeTodos,
            S,
            R::Artifact,
            Some(&format!("-p:{U1}")),
            &format!("todos:{U1}-agent-{U1}.json"),
        ),
    ));

    // ---- Claude shared artifacts ----
    for (path, kind, class) in [
        ("teams/t/config.json", K::ClaudeTeams, S),
        ("plans/p.md", K::ClaudePlan, S),
        ("paste-cache/x", K::ClaudePaste, S),
        ("usage-data/u.json", K::ClaudeUsage, S),
        ("agent-memory/a/m.md", K::ClaudeMemory, S),
        ("history.jsonl", K::ClaudeHistory, A),
    ] {
        let role = if path == "history.jsonl" {
            R::History
        } else {
            R::Artifact
        };
        v.push(row(cl, path, src(kind, class, role, None, &eid(path))));
    }
    for (name, a) in [
        ("settings.json", C::Settings),
        ("settings.local.json", C::LocalSettings),
        ("CLAUDE.md", C::Instruction),
        ("keybindings.json", C::Keybindings),
        ("launcher-settings.json", C::LauncherSettings),
        ("remote-settings.json", C::RemoteSettings),
        ("policy-limits.json", C::PolicyLimits),
        ("run.sh", C::Script),
        ("x.py", C::Script),
        ("x.js", C::Script),
        ("x.ts", C::Script),
        ("plugins/installed_plugins.json", C::Plugin),
    ] {
        v.push(row(
            cl,
            name,
            src(K::ClaudeConfig, S, R::Asset(a), None, &eid(name)),
        ));
    }
    for (dir, a) in [
        ("rules", C::Rule),
        ("skills", C::Skill),
        ("agents", C::Agent),
        ("commands", C::Command),
        ("output-styles", C::OutputStyle),
        ("hooks", C::HookDir),
    ] {
        let path = format!("{dir}/a/b.md");
        v.push(row(
            cl,
            path.clone(),
            src(K::ClaudeConfig, S, R::Asset(a), None, &eid(&path)),
        ));
    }
    for p in [
        "projects",
        "projects/-p",
        "projects/-p/x.txt",
        "projects/-p/abc.jsonl.bak",
        "projects/-p/.jsonl",
        "projects/-p/a.b.jsonl",
        "projects/-p/abc.orphaned-.jsonl",
        "projects/-p/memory",
        "projects/-p/abc.jsonl/x",
        "random.txt",
        "skills",
        "teams",
        "file-history",
        "plugins/cache/x",
        "plugins/installed_plugins.json.bak",
        "backups/.claude.json",
        ".claude.json",
        ".credentials.json",
        "settings.json/x",
        "history.jsonl/x",
        "sessions/x.json",
        "claude.md",
    ] {
        v.push(row(cl, p, Exp::Kind));
    }

    // ---- Claude primary home: only the exact file ----
    let ph = Home::ClaudePrimary;
    v.push(row(
        ph,
        ".claude.json",
        src(
            K::ClaudeConfig,
            S,
            R::Asset(C::PrimaryConfig),
            None,
            ".claude.json",
        ),
    ));
    for p in [
        "x",
        ".claude.json/x",
        "projects/-p/a.jsonl",
        ".claude.json.bak",
    ] {
        v.push(row(ph, p, Exp::Kind));
    }

    // ---- Codex ----
    let cx = Home::Codex;
    let name = format!("rollout-2026-01-02T03-04-05-{UA}.jsonl");
    let stem = format!("rollout-2026-01-02T03-04-05-{UA}");
    for path in [
        format!("sessions/2026/01/02/{name}"),
        format!("archived_sessions/{name}"),
    ] {
        v.push(row(
            cx,
            path,
            src(
                K::CodexRollout,
                A,
                R::Main,
                Some(&stem),
                &format!("rollouts:{name}"),
            ),
        ));
    }
    v.push(row(
        cx,
        format!("sessions/2026/01/02/{name}.zst"),
        src(
            K::CodexCompressed,
            A,
            R::Main,
            Some(&stem),
            &format!("rollouts:{name}"),
        ),
    ));
    v.push(row(
        cx,
        format!("sessions/rollout-2026-01-02T03-04-05-{UA}_{UB}.jsonl"),
        src(
            K::CodexRollout,
            A,
            R::Main,
            Some(&format!("{stem}_5f{UB}")),
            &format!("rollouts:{stem}_5f{UB}.jsonl"),
        ),
    ));
    v.push(row(
        cx,
        "history.jsonl",
        src(K::CodexHistory, A, R::History, None, "history.jsonl"),
    ));
    v.push(row(
        cx,
        "session_index.jsonl",
        src(
            K::CodexHistory,
            A,
            R::SessionIndex,
            None,
            "session_5findex.jsonl",
        ),
    ));
    for (n, a) in [
        ("config.toml", C::Config),
        ("hooks.json", C::HooksFile),
        ("AGENTS.md", C::Instruction),
        ("AGENTS.override.md", C::Instruction),
        ("requirements.toml", C::Requirements),
        ("managed_config.toml", C::ManagedConfig),
        ("plugins/p/plugin.json", C::Plugin),
    ] {
        v.push(row(
            cx,
            n,
            src(K::CodexConfig, S, R::Asset(a), None, &eid(n)),
        ));
    }
    for (dir, a) in [
        ("rules", C::Rule),
        ("skills", C::Skill),
        ("prompts", C::Prompt),
    ] {
        let path = format!("{dir}/a/b.md");
        v.push(row(
            cx,
            path.clone(),
            src(K::CodexConfig, S, R::Asset(a), None, &eid(&path)),
        ));
    }
    v.push(row(
        cx,
        "memories/m.md",
        src(K::CodexMemory, S, R::Artifact, None, "memories:m.md"),
    ));
    v.push(row(
        cx,
        "log/x.log",
        src(K::CodexLog, A, R::Artifact, None, "log:x.log"),
    ));
    for p in ["state_5.sqlite", "logs.sqlite3", "a.db", "STATE.DB"] {
        v.push(row(
            cx,
            p,
            src(K::CodexStore, S, R::Artifact, None, &eid(p)),
        ));
    }
    for p in [
        "sessions".to_owned(),
        "sessions/x.jsonl".to_owned(),
        format!("sessions/rollout-2026-01-02T03-04-05-{UA}.json"),
        format!("sessions/{name}.zst.zst"),
        format!("sessions/rollout-2026-01-02-{UA}.jsonl"),
        "sessions/rollout-2026-01-02T03-04-05-gggggggg-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl"
            .to_owned(),
        format!("sessions/rollout-2026-01-02T03-04-05-{UA}_x.jsonl"),
        name.clone(),
        "sessions/extra/dir".to_owned(),
        "plugins".to_owned(),
        "plugins/cache/x".to_owned(),
        "state.sqlite-wal".to_owned(),
        "unknown.txt".to_owned(),
        "memories".to_owned(),
        "log".to_owned(),
        "AGENTS.md/x".to_owned(),
        "history.jsonl.bak".to_owned(),
        "skills".to_owned(),
        "settings.json".to_owned(),
        format!("projects/-p/{UA}.jsonl"),
    ] {
        v.push(row(cx, p, Exp::Kind));
    }

    // ---- Pi ----
    let pi = Home::Pi;
    let sr = format!("2026-01-02T03-04-05.123Z_{UA}");
    let se = format!("2026-01-02T03-04-05.123Z_5f{UA}");
    v.push(row(
        pi,
        format!("sessions/--c--/{sr}.jsonl"),
        src(
            K::PiSession,
            A,
            R::Main,
            Some(&format!("--c--:{se}")),
            &format!("sessions:--c--:{se}.jsonl"),
        ),
    ));
    v.push(row(
        pi,
        format!("sessions/{sr}.jsonl"),
        src(
            K::PiSession,
            A,
            R::Main,
            Some(&se),
            &format!("sessions:{se}.jsonl"),
        ),
    ));
    v.push(row(
        pi,
        "sessions/foo.jsonl",
        src(K::PiSession, A, R::Main, Some("foo"), "sessions:foo.jsonl"),
    ));
    v.push(row(
        pi,
        "sessions/--c--/foo.jsonl",
        src(
            K::PiSession,
            A,
            R::Main,
            Some("--c--:foo"),
            "sessions:--c--:foo.jsonl",
        ),
    ));
    for (path, id) in [
        (
            format!("sessions/--c--/{sr}/tasks/t.jsonl"),
            format!("sessions:--c--:{se}:tasks:t.jsonl"),
        ),
        (
            format!("sessions/--c--/{sr}/forks/f.jsonl"),
            format!("sessions:--c--:{se}:forks:f.jsonl"),
        ),
        (
            format!("sessions/--c--/{sr}/{UA}/run-0/session.jsonl"),
            format!("sessions:--c--:{se}:{UA}:run-0:session.jsonl"),
        ),
        (
            format!("sessions/--c--/{sr}/{UA}/run-12/session.jsonl"),
            format!("sessions:--c--:{se}:{UA}:run-12:session.jsonl"),
        ),
    ] {
        v.push(row(
            pi,
            path,
            src(K::PiSession, A, R::Child, Some(&format!("--c--:{se}")), &id),
        ));
    }
    v.push(row(
        pi,
        format!("sessions/{sr}/tasks/t.jsonl"),
        src(
            K::PiSession,
            A,
            R::Child,
            Some(&se),
            &format!("sessions:{se}:tasks:t.jsonl"),
        ),
    ));
    v.push(row(
        pi,
        format!("{sr}.jsonl"),
        src(K::PiSession, A, R::Main, Some(&se), &format!("{se}.jsonl")),
    ));
    v.push(row(
        pi,
        format!("--c--/{sr}.jsonl"),
        src(
            K::PiSession,
            A,
            R::Main,
            Some(&format!("--c--:{se}")),
            &format!("--c--:{se}.jsonl"),
        ),
    ));
    v.push(row(
        pi,
        format!("{sr}/tasks/t.jsonl"),
        src(
            K::PiSession,
            A,
            R::Child,
            Some(&se),
            &format!("{se}:tasks:t.jsonl"),
        ),
    ));
    v.push(row(
        pi,
        "settings.json",
        src(
            K::PiSettings,
            S,
            R::Asset(C::Settings),
            None,
            "settings.json",
        ),
    ));
    v.push(row(
        pi,
        "models.json",
        src(K::PiModels, S, R::Asset(C::Models), None, "models.json"),
    ));
    for (n, a) in [
        ("models-store.json", C::ModelsStore),
        ("mcp.json", C::Mcp),
        ("keybindings.json", C::Keybindings),
        ("AGENTS.md", C::Instruction),
        ("AGENTS.override.md", C::Instruction),
        ("CLAUDE.md", C::Instruction),
        ("SYSTEM.md", C::Instruction),
        ("APPEND_SYSTEM.md", C::Instruction),
    ] {
        v.push(row(pi, n, src(K::PiConfig, S, R::Asset(a), None, &eid(n))));
    }
    for (dir, a) in [
        ("extensions", C::Extension),
        ("prompts", C::Prompt),
        ("skills", C::Skill),
        ("themes", C::Theme),
        ("tools", C::Tool),
        ("experimental", C::Experimental),
    ] {
        let path = format!("{dir}/a/b.md");
        v.push(row(
            pi,
            path.clone(),
            src(K::PiConfig, S, R::Asset(a), None, &eid(&path)),
        ));
    }
    v.push(row(
        pi,
        "pi.log",
        src(K::PiLog, A, R::Artifact, None, "pi.log"),
    ));
    for tail in [
        "other.txt",
        "tasks/t.txt",
        "tasks/a/b.jsonl",
        &format!("{UA}/run-01/session.jsonl"),
        &format!("{UA}/run-x/session.jsonl"),
    ] {
        let path = format!("sessions/--c--/{sr}/{tail}");
        let id = format!("sessions:--c--:{se}:{}", eid(tail));
        v.push(row(
            pi,
            path,
            src(K::PiAux, S, R::Artifact, Some(&format!("--c--:{se}")), &id),
        ));
    }
    v.push(row(
        pi,
        format!("sessions/{sr}/x.txt"),
        src(
            K::PiAux,
            S,
            R::Artifact,
            Some(&se),
            &format!("sessions:{se}:x.txt"),
        ),
    ));
    v.push(row(
        pi,
        format!("{sr}/x.txt"),
        src(K::PiAux, S, R::Artifact, Some(&se), &format!("{se}:x.txt")),
    ));
    // ---- Pi learned custom roots: session layouts only ----
    let pc = Home::PiCustom;
    v.push(row(
        pc,
        format!("{sr}.jsonl"),
        src(K::PiSession, A, R::Main, Some(&se), &format!("{se}.jsonl")),
    ));
    v.push(row(
        pc,
        "foo.jsonl",
        src(K::PiSession, A, R::Main, Some("foo"), "foo.jsonl"),
    ));
    v.push(row(
        pc,
        "--c--/foo.jsonl",
        src(
            K::PiSession,
            A,
            R::Main,
            Some("--c--:foo"),
            "--c--:foo.jsonl",
        ),
    ));
    v.push(row(
        pc,
        format!("--c--/{sr}/tasks/t.jsonl"),
        src(
            K::PiSession,
            A,
            R::Child,
            Some(&format!("--c--:{se}")),
            &format!("--c--:{se}:tasks:t.jsonl"),
        ),
    ));
    v.push(row(
        pc,
        format!("{sr}/x.txt"),
        src(K::PiAux, S, R::Artifact, Some(&se), &format!("{se}:x.txt")),
    ));
    // Go (agentDir=false): every non-transcript file is sessionless aux; no owner exists.
    // Correction 3 (Go Pi.Classify, pi.go:158-168): promotion to agent-dir rules.
    // (b) topRole(piRoles) names and dirs.
    for (p, kind, a) in [
        ("settings.json", K::PiSettings, C::Settings),
        ("models.json", K::PiModels, C::Models),
        ("models-store.json", K::PiConfig, C::ModelsStore),
        ("mcp.json", K::PiConfig, C::Mcp),
        ("keybindings.json", K::PiConfig, C::Keybindings),
        ("AGENTS.md", K::PiConfig, C::Instruction),
        ("AGENTS.override.md", K::PiConfig, C::Instruction),
        ("CLAUDE.md", K::PiConfig, C::Instruction),
        ("SYSTEM.md", K::PiConfig, C::Instruction),
        ("APPEND_SYSTEM.md", K::PiConfig, C::Instruction),
        ("skills/a/b.md", K::PiConfig, C::Skill),
        ("prompts/a.md", K::PiConfig, C::Prompt),
        ("extensions/a/b.ts", K::PiConfig, C::Extension),
        ("themes/a.json", K::PiConfig, C::Theme),
        ("tools/a", K::PiConfig, C::Tool),
        ("experimental/a", K::PiConfig, C::Experimental),
    ] {
        v.push(row(pc, p, src(kind, S, R::Asset(a), None, &eid(p))));
    }
    // (c) single-part *.log.
    v.push(row(
        pc,
        "pi.log",
        src(K::PiLog, A, R::Artifact, None, "pi.log"),
    ));
    v.push(row(
        pc,
        "x.log",
        src(K::PiLog, A, R::Artifact, None, "x.log"),
    ));
    // (a) `sessions/` uses the agent-dir sessions rules.
    v.push(row(
        pc,
        "sessions/foo.jsonl",
        src(K::PiSession, A, R::Main, Some("foo"), "sessions:foo.jsonl"),
    ));
    v.push(row(
        pc,
        format!("sessions/{sr}.jsonl"),
        src(
            K::PiSession,
            A,
            R::Main,
            Some(&se),
            &format!("sessions:{se}.jsonl"),
        ),
    ));
    v.push(row(
        pc,
        format!("sessions/--c--/{sr}/tasks/t.jsonl"),
        src(
            K::PiSession,
            A,
            R::Child,
            Some(&format!("--c--:{se}")),
            &format!("sessions:--c--:{se}:tasks:t.jsonl"),
        ),
    ));
    v.push(row(
        pc,
        "sessions/foo/x",
        src(K::PiAux, S, R::Artifact, None, "sessions:foo:x"),
    ));
    v.push(row(pc, "sessions/x.txt", Exp::Kind));
    v.push(row(pc, "sessions/--c--", Exp::Kind));
    // Not promoted: Go agentDir=false keeps these as sessionless aux.
    for p in [
        "settings.json/x",
        "models.json.bak",
        "skills",
        "sessions",
        "unknown/x.log",
        "x.txt",
        "foo/x.jsonl",
        "--c--",
        "--c--/x.txt",
        "--c--/foo/x.jsonl",
        ".jsonl",
    ] {
        v.push(row(pc, p, src(K::PiAux, S, R::Artifact, None, &eid(p))));
    }
    for (h, p) in [
        (Home::CodexOnCustom, "foo.jsonl"),
        (Home::CodexOnCustom, "history.jsonl"),
        (Home::ClaudeOnCustom, "foo.jsonl"),
        (Home::ClaudeOnCustom, "settings.json"),
    ] {
        v.push(row(h, p, Exp::Kind));
    }
    // Correction 2: Go keeps multi-part paths under `sessions/` outside any session dir as
    // sessionless aux (pi.go:192-195).
    for p in [
        "sessions/foo/tasks/t.jsonl",
        "sessions/foo/x",
        "sessions/--c--/foo/x.txt",
        "sessions/--c--/foo/tasks/t.jsonl",
        "sessions/.jsonl",
        "sessions/2026-01-02T03-04-05.123Z/x.txt",
        "sessions/not_a_uuid/x",
        "sessions/a/b/c/d.jsonl",
    ] {
        v.push(row(pi, p, src(K::PiAux, S, R::Artifact, None, &eid(p))));
    }
    // ... and a sessions path with no components after it (a file named `sessions`).
    v.push(row(
        pi,
        "sessions",
        src(K::PiAux, S, R::Artifact, None, "sessions"),
    ));
    for p in [
        "sessions/--c--".to_owned(),
        "sessions/x.txt".to_owned(),
        "sessions/--c--/x.txt".to_owned(),
        "sessions/settings.json".to_owned(),
        "foo.jsonl".to_owned(),
        "nested/x.jsonl".to_owned(),
        "--c--/foo.jsonl".to_owned(),
        ".log".to_owned(),
        "sub/a.log".to_owned(),
        "settings.json/x".to_owned(),
        "models.json.bak".to_owned(),
        "unknown.txt".to_owned(),
        "migration-backups/x".to_owned(),
        "bin/x".to_owned(),
        "history.jsonl".to_owned(),
        "config.toml".to_owned(),
    ] {
        v.push(row(pi, p, Exp::Kind));
    }
    v
}

static ROWS: LazyLock<Vec<Row>> = LazyLock::new(rows);

fn locate_row(r: &Row, ns: &str) -> Result<Located, Box<dyn std::error::Error>> {
    Ok(locate(
        &RootRelativePath::parse(&r.path)?,
        &make_roots(r.home, ns, &[], false)?,
    ))
}

#[test]
fn matrix_rows_match_independent_expectations() -> TestResult {
    ensure!(ROWS.len() > 150, "table unexpectedly small: {}", ROWS.len());
    for r in ROWS.iter() {
        let got = locate_row(r, "r1")?;
        ensure!(
            matches(&got, &r.exp, &agent_of(r.home)),
            "row {:?} {}: expected {:?}, got {:?}",
            r.home,
            r.path,
            r.exp,
            got
        );
    }
    Ok(())
}

/// Every string constant a JSON schema lists as an allowed value (`const` or `enum`).
fn collect_wire_names(schema: &serde_json::Value, out: &mut Vec<String>) {
    match schema {
        serde_json::Value::Object(map) => {
            for (key, value) in map {
                match (key.as_str(), value) {
                    ("const", serde_json::Value::String(name)) => out.push(name.clone()),
                    ("enum", serde_json::Value::Array(names)) => {
                        out.extend(names.iter().filter_map(|n| n.as_str().map(str::to_owned)));
                    }
                    _ => collect_wire_names(value, out),
                }
            }
        }
        serde_json::Value::Array(items) => {
            for item in items {
                collect_wire_names(item, out);
            }
        }
        _ => {}
    }
}

#[test]
fn every_kind_but_generated_snapshot_and_every_asset_is_reachable() -> TestResult {
    let mut kinds = HashSet::new();
    let mut roles = HashSet::new();
    for r in ROWS.iter() {
        if let Located::Source { kind, role, .. } = locate_row(r, "r1")? {
            kinds.insert(serde_json::to_string(&kind)?);
            roles.insert(role);
        }
    }
    // Derived from the enum's own schema, so a new kind forces a fixture row; the only
    // kind that is never a path kind is the generated snapshot.
    let mut expected = Vec::new();
    collect_wire_names(
        &serde_json::to_value(schemars::schema_for!(SourceKind))?,
        &mut expected,
    );
    expected.retain(|name| name != "pi.snapshot");
    ensure!(expected.len() >= 29, "schema walk found {expected:?}");
    for k in &expected {
        ensure!(
            kinds.contains(&format!("\"{k}\"")),
            "kind never located: {k}"
        );
    }
    let mut shared_aux = false;
    for r in ROWS.iter() {
        shared_aux |= matches!(
            locate_row(r, "r1")?,
            Located::Source {
                kind: SourceKind::PiAux,
                owner: Owner::Shared { .. },
                class: SourceClass::Snapshot,
                role: Role::Artifact,
                ..
            }
        );
    }
    ensure!(shared_aux, "the Shared PiAux arm is never reached");
    ensure!(
        !kinds.contains("\"pi.snapshot\""),
        "pi.snapshot is generated, never a path kind"
    );
    ensure!(kinds.len() == expected.len(), "unexpected extra kind");
    use ConfigAsset as C;
    for a in [
        C::Settings,
        C::LocalSettings,
        C::Instruction,
        C::Keybindings,
        C::LauncherSettings,
        C::RemoteSettings,
        C::PolicyLimits,
        C::Script,
        C::Skill,
        C::Agent,
        C::Command,
        C::Rule,
        C::OutputStyle,
        C::HookDir,
        C::Plugin,
        C::Prompt,
        C::Config,
        C::HooksFile,
        C::Requirements,
        C::ManagedConfig,
        C::Models,
        C::ModelsStore,
        C::Mcp,
        C::Extension,
        C::Theme,
        C::Tool,
        C::Experimental,
        C::PrimaryConfig,
    ] {
        ensure!(
            roles.contains(&Role::Asset(a)),
            "asset never located: {a:?}"
        );
    }
    for role in [
        Role::Main,
        Role::Superseded,
        Role::Child,
        Role::History,
        Role::SessionIndex,
        Role::Artifact,
    ] {
        ensure!(roles.contains(&role), "role never located: {role:?}");
    }
    Ok(())
}

#[test]
fn namespaces_separate_roots_with_equal_names() -> TestResult {
    let p = RootRelativePath::parse("projects/-p/abc.jsonl")?;
    let a = locate(&p, &make_roots(Home::Claude, "r1", &[], false)?);
    let b = locate(&p, &make_roots(Home::Claude, "r2", &[], false)?);
    match (a, b) {
        (
            Located::Source {
                owner: Owner::Session(ka),
                id: ia,
                kind: xa,
                ..
            },
            Located::Source {
                owner: Owner::Session(kb),
                id: ib,
                kind: xb,
                ..
            },
        ) => {
            ensure!(ka.session.as_str() == "r1:-p:abc", "owner a");
            ensure!(kb.session.as_str() == "r2:-p:abc", "owner b");
            ensure!(ia.as_str() == "r1:projects:-p:abc.jsonl", "id a");
            ensure!(ib.as_str() == "r2:projects:-p:abc.jsonl", "id b");
            ensure!(xa == xb, "kind unaffected by namespace");
        }
        other => return Err(format!("unexpected {other:?}").into()),
    }
    Ok(())
}

fn hex(bytes: &[u8]) -> String {
    let mut s = String::new();
    for b in bytes {
        let _ = write!(s, "{b:02x}");
    }
    s
}

fn hash_of(parts: &[&str]) -> String {
    let mut h = Sha256::new();
    for p in parts {
        h.update(p.as_bytes());
        h.update([0_u8]);
    }
    hex(&h.finalize())
}

#[test]
fn oversized_ids_become_stable_valid_hashes() -> TestResult {
    let big = "a".repeat(255);
    let path = format!("projects/-p/abc/tool-results/{big}/{big}/{big}/{big}/{big}");
    let roots = make_roots(Home::Claude, "r1", &[], false)?;
    let got = locate(&RootRelativePath::parse(&path)?, &roots);
    let Located::Source { id, owner, .. } = &got else {
        return Err(format!("not a source: {got:?}").into());
    };
    let want = format!(
        "r1:h:{}",
        hash_of(&[
            "projects",
            "-p",
            "abc",
            "tool-results",
            &big,
            &big,
            &big,
            &big,
            &big
        ])
    );
    ensure!(id.as_str() == want, "hashed id mismatch");
    ensure!(
        matches!(owner, Owner::Session(k) if k.session.as_str() == "r1:-p:abc"),
        "owner stays short"
    );

    // Escaping can triple a component; a long owner is hashed too.
    let slug = "_".repeat(255);
    let sid = "_".repeat(249);
    let path = format!("projects/{slug}/{sid}.jsonl");
    let got = locate(&RootRelativePath::parse(&path)?, &roots);
    let Located::Source { owner, .. } = &got else {
        return Err(format!("not a source: {got:?}").into());
    };
    let want = format!("r1:h:{}", hash_of(&[&slug, &sid]));
    ensure!(
        matches!(owner, Owner::Session(k) if k.session.as_str() == want),
        "hashed owner mismatch"
    );
    Ok(())
}

#[test]
fn path_validation_rejects_without_echo() -> TestResult {
    let long_component = "a".repeat(256);
    let long_path = vec!["a".repeat(255); 17].join("/");
    let cases: Vec<(String, InputError)> = vec![
        (String::new(), InputError::Empty),
        ("/a".into(), InputError::Absolute),
        ("a//b".into(), InputError::EmptyComponent),
        ("a/".into(), InputError::EmptyComponent),
        ("a/./b".into(), InputError::Traversal),
        ("a/../b".into(), InputError::Traversal),
        ("..".into(), InputError::Traversal),
        ("a\0b".into(), InputError::Nul),
        (long_component, InputError::TooLong),
        (long_path, InputError::TooLong),
    ];
    for (raw, want) in cases {
        let got = RootRelativePath::parse(&raw);
        ensure!(got == Err(want), "wrong result for {want:?}: {got:?}");
        ensure!(
            raw.len() < 3 || !want.to_string().contains(&raw),
            "message must not echo"
        );
    }
    let ok = RootRelativePath::parse("a/b.c/é")?;
    ensure!(ok.components() == ["a", "b.c", "é"], "components");
    ensure!(
        RootRelativePath::parse("..a/.b/a..")?.components().len() == 3,
        "dots inside names ok"
    );
    Ok(())
}

#[test]
fn namespace_and_index_validation() -> TestResult {
    for bad in ["", "a_b", "a:b", "a/b", "é"] {
        ensure!(
            RootNamespace::new(bad) == Err(InputError::Namespace),
            "ns {bad}"
        );
    }
    ensure!(
        RootNamespace::new(&"a".repeat(65)) == Err(InputError::Namespace),
        "long ns"
    );
    ensure!(
        RootNamespace::new("ok.1-X")?.as_str() == "ok.1-X",
        "valid ns"
    );

    let mut i = ClaudeSessionIndex::new();
    ensure!(i.record("bad", "-p") == Err(InputError::Uuid), "uuid");
    ensure!(i.record(U1, "..") == Err(InputError::Slug), "slug dots");
    ensure!(i.record(U1, "") == Err(InputError::Slug), "slug empty");
    ensure!(i.record(U1, "a\0") == Err(InputError::Slug), "slug nul");
    ensure!(
        i == ClaudeSessionIndex::new(),
        "rejected records leave no trace"
    );
    Ok(())
}

#[test]
fn ambiguity_is_absorbing_and_order_independent() -> TestResult {
    let mk = |order: &[&str]| -> Result<ClaudeSessionIndex, InputError> {
        let mut i = ClaudeSessionIndex::new();
        for s in order {
            i.record(U1, s)?;
        }
        Ok(i)
    };
    ensure!(
        mk(&["-p", "-q"])? == mk(&["-q", "-p"])?,
        "order independent"
    );
    ensure!(mk(&["-p", "-p"])? == mk(&["-p"])?, "idempotent");
    ensure!(mk(&["-p", "-q", "-p"])? == mk(&["-p", "-q"])?, "absorbing");
    let roots = make_roots(Home::Claude, "r1", &[(U1.into(), "-zz".into())], false)?;
    let got = locate(
        &RootRelativePath::parse(&format!("debug/{U1}.txt"))?,
        &roots,
    );
    ensure!(
        got == Located::UnknownOwner,
        "conflict withholds, got {got:?}"
    );
    // A transcript path is self-owning: conflicting facts never affect it.
    let got = locate(&RootRelativePath::parse("projects/-p/abc.jsonl")?, &roots);
    ensure!(
        matches!(got, Located::Source { .. }),
        "transcripts do not need facts"
    );
    Ok(())
}

#[test]
fn non_claude_roots_ignore_claude_facts_and_foreign_layouts_map_nothing() -> TestResult {
    let mut index = ClaudeSessionIndex::new();
    index.record(U1, "-p")?;
    let roots = Roots::new(
        ArchiveId::try_from("a".to_owned())?,
        MachineId::try_from("m".to_owned())?,
        Agent::Codex,
        RootNamespace::new("r1")?,
        RootLayout::ClaudePrimaryHome,
    )
    .with_claude_sessions(index);
    for p in [".claude.json", "history.jsonl", "a"] {
        let got = locate(&RootRelativePath::parse(p)?, &roots);
        ensure!(got == Located::UnknownKind, "{p}: {got:?}");
    }
    let codex = make_roots(Home::Codex, "r1", &[], false)?;
    let got = locate(
        &RootRelativePath::parse(&format!("debug/{U1}.txt"))?,
        &codex,
    );
    ensure!(
        got == Located::UnknownKind,
        "facts never leak to other agents"
    );
    Ok(())
}

// ---- property 7 ------------------------------------------------------------------------

struct Pick<'a> {
    bytes: &'a [u8],
    at: usize,
}

impl Pick<'_> {
    fn below(&mut self, n: usize) -> usize {
        let len = self.bytes.len().max(1);
        let b = self.bytes.get(self.at % len).copied().unwrap_or(0);
        let round = self.at / len;
        self.at += 1;
        (usize::from(b) + round * 7) % n.max(1)
    }
    fn pick(&mut self, xs: &[String]) -> String {
        let i = self.below(xs.len());
        xs.get(i).cloned().unwrap_or_default()
    }
    fn home(&mut self) -> Home {
        match self.below(5) {
            0 => Home::Claude,
            1 => Home::Codex,
            2 => Home::Pi,
            3 => Home::PiCustom,
            _ => Home::ClaudePrimary,
        }
    }
    /// Facts for UUIDs that begin with `0`, which no fixture path uses.
    fn noise(&mut self) -> Vec<(String, String)> {
        let n = self.below(9);
        (0..n)
            .map(|_| {
                let a = (self.below(256) << 16) | (self.below(256) << 8) | self.below(256);
                let b = self.below(256);
                let slug = ["-p", "-q", "x"].get(self.below(3)).copied().unwrap_or("x");
                (format!("{a:08x}-0000-0000-0000-{b:012x}"), slug.to_owned())
            })
            .collect()
    }
}

fn frags() -> Vec<String> {
    let mut v: Vec<String> = [
        "projects",
        "-p",
        "abc.jsonl",
        "abc",
        "subagents",
        "tool-results",
        "memory",
        "file-history",
        U1,
        U2,
        U3,
        "debug",
        "todos",
        "tasks",
        "session-env",
        "teams",
        "plans",
        "sessions",
        "archived_sessions",
        "settings.json",
        "skills",
        "rules",
        "history.jsonl",
        "config.toml",
        "log",
        "x.log",
        "state.sqlite",
        "--c--",
        "forks",
        "run-1",
        "session.jsonl",
        "t.jsonl",
        "plugins",
        "cache",
        "é",
        "a_b",
        "..",
        ".",
        "",
        "x\0y",
        ".claude.json",
    ]
    .iter()
    .map(|s| (*s).to_owned())
    .collect();
    v.push(format!("rollout-2026-01-02T03-04-05-{UA}.jsonl"));
    v.push(format!("2026-01-02T03-04-05.123Z_{UA}"));
    v.push(format!("2026-01-02T03-04-05.123Z_{UA}.jsonl"));
    v
}

fn prop_total(e: &[u8]) -> TestResult {
    let mut p = Pick { bytes: e, at: 0 };
    let frags = frags();
    let home = p.home();
    let n = 1 + p.below(7);
    let comps: Vec<String> = (0..n).map(|_| p.pick(&frags)).collect();
    let raw = comps.join("/");
    let bad = comps
        .iter()
        .any(|c| c.is_empty() || c == "." || c == ".." || c.contains('\0'));
    let parsed = RootRelativePath::parse(&raw);
    ensure!(
        parsed.is_ok() == !bad,
        "parse validity disagrees with oracle"
    );
    let Ok(path) = parsed else { return Ok(()) };
    let base = make_roots(home, "r1", &[], false)?;
    let noisy = make_roots(home, "r1", &p.noise(), p.below(2) == 0)?;
    let got = locate(&path, &base);
    ensure!(got == locate(&path, &base), "deterministic");
    ensure!(got == locate(&path, &noisy), "stable under unrelated facts");
    match &got {
        Located::Source {
            owner, kind, id, ..
        } => {
            ensure!(got.withhold_reason().is_none(), "source has no reason");
            ensure!(id.as_str().starts_with("r1:"), "id qualified");
            if let Owner::Session(k) = owner {
                ensure!(k.session.as_str().starts_with("r1:"), "session qualified");
            }
            let name = serde_json::to_value(kind)?;
            let want = match agent_of(home) {
                Agent::Claude => "claude.",
                Agent::Codex => "codex.",
                Agent::Pi => "pi.",
            };
            ensure!(
                name.as_str().is_some_and(|s| s.starts_with(want)),
                "kind belongs to the root's agent"
            );
        }
        Located::UnknownKind => ensure!(
            got.withhold_reason() == Some(WithholdReason::UnknownKind),
            "kind counted"
        ),
        Located::UnknownOwner => ensure!(
            got.withhold_reason() == Some(WithholdReason::UnknownOwner),
            "owner counted"
        ),
    }
    Ok(())
}

fn prop_stable_rows(e: &[u8]) -> TestResult {
    let mut p = Pick { bytes: e, at: 0 };
    // Row choice needs more range than one byte: combine two draws.
    let idx = (p.below(256) * 256 + p.below(256)) % ROWS.len();
    let r = ROWS.get(idx).ok_or("row")?;
    let noise = p.noise();
    let reverse = p.below(2) == 0;
    let roots = make_roots(r.home, "r1", &noise, reverse)?;
    let got = locate(&RootRelativePath::parse(&r.path)?, &roots);
    ensure!(
        matches(&got, &r.exp, &agent_of(r.home)),
        "row {} changed under unrelated facts: {got:?}",
        r.path
    );
    // Conflicting facts for the row's own UUID can only withhold, never reassign.
    if let Exp::Src { owner: Some(o), .. } = &r.exp
        && r.home == Home::Claude
        && r.path.contains(U1)
        && o.ends_with(U1)
    {
        let conflict = make_roots(r.home, "r1", &[(U1.to_owned(), "-zz".to_owned())], reverse)?;
        let got = locate(&RootRelativePath::parse(&r.path)?, &conflict);
        ensure!(
            got == Located::UnknownOwner,
            "conflict must withhold: {got:?}"
        );
    }
    Ok(())
}

fn is_shared_pi_aux(got: &Located) -> bool {
    matches!(
        got,
        Located::Source {
            kind: SourceKind::PiAux,
            owner: Owner::Shared { .. },
            class: SourceClass::Snapshot,
            role: Role::Artifact,
            ..
        }
    )
}

fn prop_unmapped(e: &[u8]) -> TestResult {
    let mut p = Pick { bytes: e, at: 0 };
    let frags = frags();
    let home = p.home();
    let mut name = String::from("zz");
    for _ in 0..p.below(8) {
        let c = ["a", "b", "7", "-", "_"]
            .get(p.below(5))
            .copied()
            .unwrap_or("a");
        name.push_str(c);
    }
    let mut comps = vec![name];
    for _ in 0..p.below(5) {
        let c = p.pick(&frags);
        if !(c.is_empty() || c == "." || c == ".." || c.contains('\0')) {
            comps.push(c);
        }
    }
    let roots = make_roots(home, "r1", &p.noise(), false)?;
    let got = locate(&RootRelativePath::parse(&comps.join("/"))?, &roots);
    if home == Home::PiCustom {
        // Go agentDir=false: all non-transcript files are sessionless aux.
        ensure!(is_shared_pi_aux(&got), "custom root aux expected: {got:?}");
    } else {
        ensure!(
            got == Located::UnknownKind,
            "unknown first component must be UnknownKind: {got:?}"
        );
    }

    // Sessionless Pi aux: under `sessions/`, optional cwd dir, then a non-session dir.
    let cwd = if p.below(2) == 0 { "--c--/" } else { "" };
    let mut tail = vec![p.pick(&frags)];
    tail.retain(|c| !(c.is_empty() || c == "." || c == ".." || c.contains('\0')));
    if tail.is_empty() {
        tail.push("x".to_owned());
    }
    let sl = format!("sessions/{cwd}zz{}/{}", p.below(10), tail.join("/"));
    // Custom-root promotion (Go Pi.Classify): these must take the agent-dir rules.
    let promo: [(&str, &str); 6] = [
        ("settings.json", "pi.settings"),
        ("models.json", "pi.models"),
        ("mcp.json", "pi.config"),
        ("skills/zz/x", "pi.config"),
        ("zz.log", "pi.log"),
        ("sessions/zz.jsonl", "pi.session"),
    ];
    let (pp, want) = promo
        .get(p.below(6))
        .copied()
        .unwrap_or(("settings.json", "pi.settings"));
    let custom = make_roots(Home::PiCustom, "r1", &p.noise(), false)?;
    let agent = make_roots(Home::Pi, "r1", &[], false)?;
    for path in [pp, sl.as_str()] {
        let path = RootRelativePath::parse(path)?;
        let got = locate(&path, &custom);
        ensure!(got == locate(&path, &agent), "promotion differs: {got:?}");
    }
    let got = locate(&RootRelativePath::parse(pp)?, &custom);
    let Located::Source { kind, .. } = &got else {
        return Err(format!("{pp}: not promoted: {got:?}").into());
    };
    ensure!(
        serde_json::to_value(kind)?.as_str() == Some(want),
        "{pp}: wrong promoted kind {kind:?}"
    );
    let pi_roots = make_roots(Home::Pi, "r1", &p.noise(), false)?;
    let got = locate(&RootRelativePath::parse(&sl)?, &pi_roots);
    ensure!(is_shared_pi_aux(&got), "{sl}: {got:?}");
    if let Located::Source { id, .. } = &got {
        ensure!(id.as_str().starts_with("r1:sessions:"), "path-qualified id");
    }

    // Claude externals with a UUID absent from the shell facts are always UnknownOwner.
    let first = ["c", "d", "e", "f"].get(p.below(4)).copied().unwrap_or("c");
    let uuid = format!(
        "{first}{:07x}-0000-0000-0000-{:012x}",
        p.below(256),
        p.below(256)
    );
    let dir = ["file-history", "session-env", "debug", "todos", "tasks"]
        .get(p.below(5))
        .copied()
        .unwrap_or("debug");
    let suffix = ["", ".txt", "-agent", "_x"]
        .get(p.below(4))
        .copied()
        .unwrap_or("");
    let path = format!("{dir}/{uuid}{suffix}/x");
    let roots = make_roots(Home::Claude, "r1", &p.noise(), false)?;
    let got = locate(&RootRelativePath::parse(&path)?, &roots);
    ensure!(got == Located::UnknownOwner, "{path}: {got:?}");
    ensure!(
        got.withhold_reason() == Some(WithholdReason::UnknownOwner),
        "countable"
    );
    Ok(())
}

#[hegel::test]
fn hegel_property7_locate_total_and_countable(tc: TestCase) {
    let e: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
    let r = prop_total(&e);
    assert!(r.is_ok(), "{r:?}");
}

#[hegel::test]
fn hegel_property7_result_stable_under_unrelated_facts(tc: TestCase) {
    let e: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
    let r = prop_stable_rows(&e);
    assert!(r.is_ok(), "{r:?}");
}

#[hegel::test]
fn hegel_property7_unmapped_paths_are_withheld(tc: TestCase) {
    let e: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
    let r = prop_unmapped(&e);
    assert!(r.is_ok(), "{r:?}");
}

proptest! {
    #[test]
    fn proptest_property7_locate_total_and_countable(e in prop::collection::vec(any::<u8>(), 0..96)) {
        let r = prop_total(&e);
        prop_assert!(r.is_ok(), "{:?}", r);
    }
    #[test]
    fn proptest_property7_result_stable_under_unrelated_facts(e in prop::collection::vec(any::<u8>(), 0..96)) {
        let r = prop_stable_rows(&e);
        prop_assert!(r.is_ok(), "{:?}", r);
    }
    #[test]
    fn proptest_property7_unmapped_paths_are_withheld(e in prop::collection::vec(any::<u8>(), 0..96)) {
        let r = prop_unmapped(&e);
        prop_assert!(r.is_ok(), "{:?}", r);
    }
}

#[test]
fn root_relative_path_length_limit_is_exactly_4096_bytes() -> TestResult {
    // Mutant: `raw.len() > MAX_PATH` -> `>=`.
    let mut parts = vec!["a".repeat(100); 40];
    parts.push("b".repeat(56));
    let at_limit = parts.join("/");
    ensure!(
        at_limit.len() == 4096,
        "fixture is {} bytes",
        at_limit.len()
    );
    ensure!(
        RootRelativePath::parse(&at_limit).is_ok(),
        "4096 bytes must parse"
    );
    let over = format!("{at_limit}b");
    ensure!(
        RootRelativePath::parse(&over) == Err(InputError::TooLong),
        "4097 bytes must be TooLong"
    );
    Ok(())
}

#[test]
fn malformed_pi_stems_and_cwd_directories_are_not_sessions() -> TestResult {
    // Mutants: each `&&` -> `||` in `pi_stem_ok` (a stem needs a uuid AND a timestamp date
    // AND a non-empty body of timestamp characters) and `len > 4` -> `>= 4` in `pi_cwd`.
    let ok = format!("2026-01-02T03-04-05.123Z_{UA}");
    let stems = [
        format!("2026-01-02X03-04-05.123Z_{UA}"),
        format!("2026-01-02T03-04-05.123Z_{}", "z".repeat(36)),
        format!("2026-01-02T_{UA}"),
        format!("2026-01-02T03 04_{UA}"),
    ];
    let roots = make_roots(Home::Pi, "r1", &[], false)?;
    let session = |path: &str| -> Result<bool, Box<dyn std::error::Error>> {
        Ok(matches!(
            locate(&RootRelativePath::parse(path)?, &roots),
            Located::Source {
                kind: SourceKind::PiSession,
                role: Role::Main,
                ..
            }
        ))
    };
    ensure!(
        session(&format!("{ok}.jsonl"))?,
        "the well-formed stem is a session"
    );
    for stem in &stems {
        ensure!(
            !session(&format!("{stem}.jsonl"))?,
            "{stem} must not be a session"
        );
    }
    ensure!(
        session(&format!("--c--/{ok}.jsonl"))?,
        "a 5-byte cwd directory is accepted"
    );
    ensure!(
        !session(&format!("----/{ok}.jsonl"))?,
        "a 4-byte `----` is not a cwd directory"
    );
    Ok(())
}
