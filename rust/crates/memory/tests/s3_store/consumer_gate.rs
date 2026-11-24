//! The consumer gate: real writer output (the daemon against the loopback fake) -> the real
//! `memory export` and `memory restore` binary -> the unchanged Python importer and report
//! (`tools/analytics/test_rust_export_journey.py`), for Claude, Codex and Pi, plus the privacy
//! canary scan of every export.
//!
//! With `MEMORY_REQUIRE_CONSUMER=1` a missing interpreter or duckdb fails the gate; without it
//! the journey prints `consumer gate skipped` and returns.
use super::assemble::{PREFIX, Rig};
use super::fake::Fake;
use super::{BUCKET, R, e};
use memory::assemble::start_from_config;
use memory::config::Config;
use memory::daemon::Sources;
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory_core::limits::{current_key, manifest_key, owner_prefix, parse_owner_prefix};
use memory_core::manifest::{manifest_hash, parse_current};
use serde_json::{Value, json};
use std::fs;
use std::path::{Path, PathBuf};
use std::process::{Command, Output};

const U1: &str = "11111111-1111-4111-8111-111111111111";
const UR: &str = "22222222-2222-4222-8222-222222222222";
const CX: &str = "33333333-3333-4333-8333-333333333333";
const CZ: &str = "55555555-5555-4555-8555-555555555555";
const CODEX_ZST: &str = "sessions/2026/11/17/rollout-2026-11-17T11-00-00-55555555-5555-4555-8555-555555555555.jsonl.zst";
const PI: &str = "44444444-4444-4444-8444-444444444444";
const CODEX_FILE: &str =
    "sessions/2026/11/17/rollout-2026-11-17T10-00-00-33333333-3333-4333-8333-333333333333.jsonl";
const PI_DIR: &str =
    "sessions/--home-me--/2026-11-17T10:00:00.000Z_44444444-4444-4444-8444-444444444444";
const NON_ASCII: &str = "projects/-p-ü/séss-é.jsonl";

fn jl(records: &[&str]) -> String {
    records.iter().map(|r| format!("{r}\n")).collect()
}
fn put(path: &Path, body: &str) -> R {
    fs::create_dir_all(path.parent().ok_or("parent")?).map_err(e)?;
    fs::write(path, body).map_err(e)
}

pub(super) struct Fixture {
    pub(super) rig: Rig,
    claude: PathBuf,
    codex: PathBuf,
    pi: PathBuf,
    /// (needle, the file that must exist, the file that holds the needle)
    canaries: Vec<(&'static str, PathBuf, PathBuf)>,
}

/// Brief 56: strings in the run record's never-stored fields; none may reach the database.
const RUN_CANARIES: [&str; 12] = [
    "CANARY-RUN-TASK-a1",
    "CANARY-RUN-ERROR-b2",
    "CANARY-RUN-TRANSCRIPT-c3",
    "CANARY-RUN-CRITERION-d4",
    "CANARY-RUN-EVIDENCE-e5",
    "CANARY-RUN-REPORT-CMD-f6",
    "CANARY-RUN-REPORT-FILE-g7",
    "CANARY-RUN-REPORT-NOTE-h8",
    "CANARY-RUN-COMMAND-i9",
    "CANARY-RUN-ARG-j0",
    "CANARY-RUN-EXTPATH-k1",
    "90817263",
];

/// A synthetic run record (brief 56): every field the importer reads, plus the canaries.
fn run_record() -> String {
    let d = |c: &str| format!("sha256:{}", c.repeat(64));
    json!({
        "runId": "run-gate-1", "agent": "worker", "exitCode": 0, "timestamp": 1_700_000_000_000_u64,
        "model": "pi-m", "requestedModel": "pi-m2",
        "task": RUN_CANARIES[0], "error": RUN_CANARIES[1], "transcriptPath": RUN_CANARIES[2],
        "usage": {"input": 5, "output": 6, "cacheRead": 1, "cacheWrite": 2, "cost": 0.25,
            "turns": 3},
        "launchResolvedExtensions": {"configured": [d("a"), d("b")], "effective": [d("a")],
            "runtime": [], "required": [], "omitted": {"configured": 1},
            "disableAmbientExtensions": true},
        "acceptance": {"status": "passed", "evidenceStatus": "verified", "explicit": true,
            "criteria": [{"text": RUN_CANARIES[3], "evidence": RUN_CANARIES[4]}],
            "effectiveAcceptance": {"level": "strict"}, "reviewResult": {"status": "approved"},
            "runtimeChecks": [{"command": RUN_CANARIES[5]}],
            "childReport": {"commands": [RUN_CANARIES[5]], "changedFiles": [RUN_CANARIES[6]],
                "notes": RUN_CANARIES[7]}},
        "runner": {"type": "external-cli", "command": RUN_CANARIES[8], "args": [RUN_CANARIES[9]]},
        "externalProcess": {"durationMs": 1200, "exitCode": 0, "pid": 90_817_263,
            "path": RUN_CANARIES[10]},
    })
    .to_string()
}

pub(super) fn fixture(server: &Fake, label: &str) -> R<Fixture> {
    let rig = Rig::new(label, &server.url(), PREFIX, false)?;
    let codex = rig.add_root("codex")?;
    let pi = rig.add_root("pi")?;
    let claude = rig.root.clone();
    let p = claude.join("projects/-p");
    put(
        &p.join(format!("{U1}.jsonl")),
        &jl(&[
            r#"{"type":"user","uuid":"u1","version":"9.9.9","message":{"role":"user","content":"hello claude"}}"#,
            r#"{"type":"attachment","uuid":"at1","attachment":{"type":"instructions","files":[{"path":"/home/me/proj/CLAUDE.md","type":"Project"}]}}"#,
            r#"{"type":"assistant","uuid":"a1","message":{"role":"assistant","model":"claude-x","usage":{"input_tokens":3,"output_tokens":4},"content":[{"type":"text","text":"hi"}]}}"#,
            r#"{"type":"assistant","uuid":"a2","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"p":1}}]}}"#,
            r#"{"type":"user","uuid":"u2","toolUseResult":{"durationMs":5},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}"#,
        ]),
    )?;
    put(
        &p.join(format!("{U1}/subagents/agent-a1.jsonl")),
        &jl(&[
            r#"{"type":"user","uuid":"s1","message":{"role":"user","content":"task for agent"}}"#,
            r#"{"type":"assistant","uuid":"s2","message":{"role":"assistant","content":"done"}}"#,
        ]),
    )?;
    put(
        &p.join(format!("{U1}/tool-results/t1.txt")),
        "tool output bytes\n",
    )?;
    put(
        &claude.join(NON_ASCII),
        &jl(&[r#"{"type":"user","uuid":"n1","message":{"role":"user","content":"héllo ✓"}}"#]),
    )?;
    put(
        &p.join(format!("{UR}.jsonl")),
        &jl(&[
            r#"{"type":"user","uuid":"r1","message":{"role":"user","content":"first one"}}"#,
            r#"{"type":"assistant","uuid":"r2","message":{"role":"assistant","content":"second"}}"#,
            r#"{"type":"user","uuid":"r3","message":{"role":"user","content":"third one"}}"#,
        ]),
    )?;
    put(
        &codex.join(CODEX_FILE),
        &jl(&[
            r#"{"type":"session_meta","payload":{"id":"cx1","cli_version":"1.2.3"}}"#,
            r#"{"type":"turn_context","payload":{"model":"gpt-x","approval_policy":"never"}}"#,
            r#"{"type":"event_msg","payload":{"type":"user_message","message":"codex prompt"}}"#,
            r#"{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"codex prompt"}]}}"#,
            r#"{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"codex answer"}]}}"#,
            r#"{"type":"response_item","payload":{"type":"function_call","name":"shell","call_id":"c1","arguments":"{\"cmd\":\"ls\"}"}}"#,
            r#"{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"files"}}"#,
            r#"{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":9},"last_token_usage":{"input_tokens":6,"cached_input_tokens":2,"output_tokens":3}}}}"#,
        ]),
    )?;
    let zst = jl(&[
        r#"{"type":"session_meta","payload":{"id":"cz1"}}"#,
        r#"{"type":"event_msg","payload":{"type":"user_message","message":"zst prompt"}}"#,
        r#"{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"zst prompt"}]}}"#,
        r#"{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"zst answer"}]}}"#,
        r#"{"type":"response_item","payload":{"type":"function_call","name":"shell","call_id":"z1","arguments":"{\"cmd\":\"ls\"}"}}"#,
        r#"{"type":"response_item","payload":{"type":"function_call_output","call_id":"z1","output":"files"}}"#,
    ]);
    let packed = zstd::bulk::compress(zst.as_bytes(), 3).map_err(e)?;
    let at = codex.join(CODEX_ZST);
    fs::create_dir_all(at.parent().ok_or("parent")?).map_err(e)?;
    fs::write(&at, packed).map_err(e)?;
    put(
        &pi.join(format!("{PI_DIR}.jsonl")),
        &jl(&[
            r#"{"type":"session","id":"p1"}"#,
            r#"{"type":"message","id":"pm1","message":{"role":"user","content":"pi prompt"}}"#,
            r#"{"type":"message","id":"pm2","message":{"role":"assistant","model":"pi-m","usage":{"input":4,"output":2,"cost":{"total":0.5}},"content":[{"type":"text","text":"pi answer"},{"type":"toolCall","id":"tc1","name":"bash","arguments":{"c":"ls"}}]}}"#,
            r#"{"type":"message","id":"pm3","message":{"role":"toolResult","toolCallId":"tc1","toolName":"bash","isError":false,"durationMs":7,"details":{"runId":"run-gate-1"},"content":[{"type":"text","text":"out"}]}}"#,
        ]),
    )?;
    put(
        &pi.join(format!("{PI_DIR}/tasks/t1.jsonl")),
        &jl(&[
            r#"{"type":"message","id":"c1","message":{"role":"user","content":"child task"}}"#,
            r#"{"type":"message","id":"c2","message":{"role":"assistant","content":"child done"}}"#,
        ]),
    )?;

    // Brief 56: the run record is a shared owner's source, beside the session directory.
    put(
        &pi.join("sessions/--home-me--/subagent-artifacts/run1.json"),
        &run_record(),
    )?;

    // Canaries: only where the writer must withhold them.
    let mut canaries = Vec::new();
    let mut plant = |needle: &'static str, at: PathBuf| -> R {
        put(&at, &format!("{needle}\n"))?;
        canaries.push((needle, at.clone(), at));
        Ok(())
    };
    plant("CANARY-ENV-7f3a9c", p.join(format!("{U1}/.env")))?;
    plant("CANARY-UNKNOWN-2b81d4", claude.join("unknown-kind.bin"))?;
    plant("CANARY-BACKUP-90c5e1", p.join(format!("{U1}.jsonl.bak")))?;
    plant(
        "CANARY-CODEXAUTH-5d62aa",
        codex.join("sessions/2026/11/17/auth.json"),
    )?;
    plant("CANARY-PEM-e417b3", pi.join(format!("{PI_DIR}/key.pem")))?;
    // Append-class folder (`tool-results`, admitted per file): only the credential denylist
    // withholds these, so a skipped denylist reaches the export.
    plant(
        "CANARY-CRED-ENV-6a1d09",
        p.join(format!("{U1}/tool-results/.env")),
    )?;
    plant(
        "CANARY-CRED-RSA-b74e52",
        p.join(format!("{U1}/tool-results/id_rsa")),
    )?;
    let outside = rig.dir.join("outside/secret.txt");
    put(&outside, "CANARY-SYMLINK-c08f26\n")?;
    let link = p.join(format!("{U1}/link.txt"));
    std::os::unix::fs::symlink(&outside, &link).map_err(e)?;
    canaries.push(("CANARY-SYMLINK-c08f26", link, outside));
    Ok(Fixture {
        rig,
        claude,
        codex,
        pi,
        canaries,
    })
}

pub(super) fn owners(rig: &Rig) -> R<Vec<String>> {
    let config = Config::load(&rig.cfg).map_err(e)?;
    let mut v: Vec<String> = WalkSources::new(
        locator_roots(&config).map_err(e)?,
        WalkLimits::default(),
        SharedFs::new(),
    )
    .enumerate()
    .iter()
    .map(|(o, _)| owner_prefix(o))
    .filter(|t| t.starts_with("sessions/"))
    .collect();
    v.sort();
    v.dedup();
    Ok(v)
}

pub(super) fn run(state: &Path, args: &[&std::ffi::OsStr]) -> R<Output> {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear().env("MEMORY_STATE_DIR", state).args(args);
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    cmd.output().map_err(e)
}

pub(super) fn export(fx: &Fixture, text: &str, out: &Path) -> R {
    fs::create_dir_all(out.parent().ok_or("parent")?).map_err(e)?;
    let r = run(
        &fx.rig.dir,
        &[
            "export".as_ref(),
            text.as_ref(),
            "--out".as_ref(),
            out.as_os_str(),
        ],
    )?;
    assert_eq!(
        r.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&r.stderr)
    );
    Ok(())
}

fn root_of<'a>(fx: &'a Fixture, agent: &str) -> &'a Path {
    match agent {
        "codex" => &fx.codex,
        "pi" => &fx.pi,
        _ => &fx.claude,
    }
}

/// The digest `current.json` names for `text`, checked against the stored manifest bytes.
fn head_digest(server: &Fake, text: &str) -> R<String> {
    let owner = parse_owner_prefix(text).map_err(e)?;
    let key = |k: String| format!("/{BUCKET}/{PREFIX}{k}");
    let (pointer, _) = server
        .object(&key(current_key(&owner)))
        .ok_or("current.json")?;
    let current = parse_current(&pointer).map_err(e)?;
    let (stored, _) = server
        .object(&key(manifest_key(&current.manifest)))
        .ok_or("head manifest")?;
    let hex = String::from(current.manifest);
    assert_eq!(
        String::from(manifest_hash(&stored)),
        hex,
        "stored head bytes"
    );
    Ok(hex)
}

fn metadata(dir: &Path) -> R<Value> {
    serde_json::from_slice(&fs::read(dir.join("metadata.json")).map_err(e)?).map_err(e)
}

/// Every active epoch body equals the file on disk; returns (source entries, roles).
fn check_bodies(fx: &Fixture, agent: &str, dir: &Path, sources: usize) -> R {
    let meta = metadata(dir)?;
    let list = meta
        .pointer("/manifest/sources")
        .and_then(Value::as_array)
        .ok_or("sources")?;
    assert_eq!(list.len(), sources, "{dir:?}");
    let bodies = meta
        .pointer("/bodies")
        .and_then(Value::as_array)
        .ok_or("bodies")?;
    assert_eq!(bodies.len(), sources);
    for s in list.iter().filter(|s| s["state"] == "active") {
        let body = bodies
            .iter()
            .find(|b| b["source_id"] == s["source_id"] && b["epoch_id"] == s["epoch_id"])
            .ok_or("body for source")?;
        let got = fs::read(dir.join(body["path"].as_str().ok_or("path")?)).map_err(e)?;
        let path = s["path"].as_str().ok_or("p")?;
        // The manifest path is logical: a `.zst` rollout is found by its physical spelling.
        let plain = root_of(fx, agent).join(path);
        let disk = if plain.exists() {
            fs::read(&plain).map_err(e)?
        } else {
            let packed = fs::read(root_of(fx, agent).join(format!("{path}.zst"))).map_err(e)?;
            zstd::stream::decode_all(packed.as_slice()).map_err(e)?
        };
        assert_eq!(got, disk, "export body equals disk for {}", s["path"]);
    }
    Ok(())
}

/// Decoded bytes of a logical path: the plain file, or its `.zst` spelling.
fn disk_logical(root: &Path, rel: &Path) -> R<Vec<u8>> {
    let plain = root.join(rel);
    if plain.exists() {
        return fs::read(&plain).map_err(e);
    }
    let mut z = plain.into_os_string();
    z.push(".zst");
    zstd::stream::decode_all(fs::read(z).map_err(e)?.as_slice()).map_err(e)
}

fn walk(dir: &Path, base: &Path, out: &mut Vec<PathBuf>) -> R {
    for entry in fs::read_dir(dir).map_err(e)? {
        let p = entry.map_err(e)?.path();
        if p.is_dir() {
            walk(&p, base, out)?;
        } else {
            out.push(p.strip_prefix(base).map_err(e)?.to_path_buf());
        }
    }
    Ok(())
}

fn has(hay: &[u8], needle: &[u8]) -> bool {
    hay.windows(needle.len()).any(|w| w == needle)
}

/// Scan every export: `metadata.json`, each body, and all bodies concatenated.
fn scan(fx: &Fixture, dirs: &[PathBuf]) -> R {
    for (needle, exists, holder) in &fx.canaries {
        assert!(
            fs::symlink_metadata(exists).is_ok(),
            "canary file {exists:?} missing"
        );
        assert!(
            has(&fs::read(holder).map_err(e)?, needle.as_bytes()),
            "{needle} not on disk"
        );
    }
    let mut all = Vec::new();
    let mut scanned = 0;
    for dir in dirs {
        let meta = fs::read(dir.join("metadata.json")).map_err(e)?;
        let mut parts = vec![meta];
        for entry in fs::read_dir(dir.join("bodies")).map_err(e)? {
            parts.push(fs::read(entry.map_err(e)?.path()).map_err(e)?);
        }
        for part in &parts {
            all.extend_from_slice(part);
            scanned += 1;
            for (needle, _, _) in &fx.canaries {
                assert!(
                    !has(part, needle.as_bytes()),
                    "{needle} leaked into {dir:?}"
                );
            }
        }
    }
    assert!(scanned >= dirs.len() * 2);
    for (needle, _, _) in &fx.canaries {
        assert!(
            !has(&all, needle.as_bytes()),
            "{needle} leaked across bodies"
        );
    }
    // Positive control: the scan really reads transcript bytes.
    assert!(has(&all, b"hello claude") && has(&all, b"pi prompt"));
    Ok(())
}

fn python() -> R<Option<PathBuf>> {
    let py = std::env::var_os("MEMORY_ANALYTICS_PYTHON").map_or_else(
        || Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../.analytics-venv/bin/python"),
        PathBuf::from,
    );
    let ok = py.exists()
        && Command::new(&py)
            .args(["-I", "-c", "import duckdb"])
            .output()
            .is_ok_and(|o| o.status.success());
    if ok {
        Ok(Some(py))
    } else if std::env::var("MEMORY_REQUIRE_CONSUMER").as_deref() == Ok("1") {
        Err("MEMORY_REQUIRE_CONSUMER=1 but the analytics python or duckdb is unavailable".into())
    } else {
        eprintln!("consumer gate skipped");
        Ok(None)
    }
}

struct Want {
    name: &'static str,
    agent: &'static str,
    needle: &'static str,
    sources: usize,
    events: usize,
    issues: Value,
    roles: Value,
}

/// Brief 56: the one shared owner of the fixture.
pub(super) fn shared_owner(rig: &Rig) -> R<String> {
    let config = Config::load(&rig.cfg).map_err(e)?;
    let v: Vec<String> = WalkSources::new(
        locator_roots(&config).map_err(e)?,
        WalkLimits::default(),
        SharedFs::new(),
    )
    .enumerate()
    .iter()
    .map(|(o, _)| owner_prefix(o))
    .filter(|t| t.starts_with("shared/"))
    .collect();
    assert_eq!(v.len(), 1, "{v:?}");
    v.into_iter().next().ok_or_else(|| "shared".to_owned())
}

fn find<'a>(owners: &'a [String], w: &Want) -> R<&'a String> {
    let hits: Vec<_> = owners
        .iter()
        .filter(|t| t.contains(w.needle) && t.split('/').nth(3) == Some(w.agent))
        .collect();
    assert_eq!(hits.len(), 1, "{}: {owners:?}", w.name);
    hits.first().copied().ok_or_else(|| "owner".to_owned())
}

fn entry(w: &Want, text: &str, dir: &Path) -> Value {
    let mut parts = text.splitn(5, '/');
    let (_, archive, machine, agent, session) = (
        parts.next(),
        parts.next(),
        parts.next(),
        parts.next(),
        parts.next(),
    );
    json!({"name": w.name, "agent": agent, "archive": archive, "machine": machine,
        "session": session, "dir": dir, "sources": w.sources, "events": w.events,
        "issues": w.issues, "roles": w.roles})
}

pub(super) fn captured(fx: &Fixture) -> R<memory::assemble::ProductionDaemon> {
    let mut daemon = start_from_config(&fx.rig.state, &fx.rig.cfg).map_err(e)?;
    let report = daemon.reconcile_once().map_err(e)?;
    assert_eq!((report.failed, report.parked), (0, 0));
    // Brief 56: was 6; the fixture gained a shared owner (the Pi run record).
    assert_eq!(
        report.landed, 7,
        "six session owners and one shared owner land"
    );
    Ok(daemon)
}

fn phase1() -> Vec<Want> {
    vec![
        Want {
            name: "s1",
            agent: "claude",
            needle: U1,
            sources: 3,
            // Brief 53: was 6; the fixture gained an `instructions` attachment (a system event).
            events: 7,
            issues: json!({"artifact_not_normalized": 1}),
            roles: json!({"main": 1, "subagent": 1, "artifact": 1}),
        },
        Want {
            name: "nonascii",
            agent: "claude",
            needle: "s_c3_a9ss",
            sources: 1,
            events: 1,
            issues: json!({}),
            roles: json!({"main": 1}),
        },
        Want {
            name: "rewrite",
            agent: "claude",
            needle: UR,
            sources: 1,
            events: 3,
            issues: json!({}),
            roles: json!({"main": 1}),
        },
        Want {
            name: "codex",
            agent: "codex",
            needle: CX,
            sources: 1,
            // Brief 53: was 6; the fixture gained a `turn_context` record (a system event).
            // Brief 55: was 7; the fixture gained a `token_count` record (a system event).
            events: 8,
            issues: json!({}),
            roles: json!({"main": 1}),
        },
        Want {
            name: "codex-zst",
            agent: "codex",
            needle: CZ,
            sources: 1,
            events: 6,
            issues: json!({}),
            roles: json!({"main": 1}),
        },
        Want {
            name: "pi",
            agent: "pi",
            needle: PI,
            sources: 2,
            events: 7,
            issues: json!({}),
            roles: json!({"main": 1, "subagent": 1}),
        },
    ]
}

#[test]
fn consumer_gate_claude_codex_pi_export_imports_and_reports() -> R {
    let Some(py) = python()? else { return Ok(()) };
    let server = Fake::start()?;
    let fx = fixture(&server, "gate")?;
    let mut daemon = captured(&fx)?;
    let all = owners(&fx.rig)?;
    assert_eq!(all.len(), 6, "{all:?}");
    let out = fx.rig.dir.join("exports");
    let mut dirs = Vec::new();
    let mut first = Vec::new();
    for (i, w) in phase1().iter().enumerate() {
        let text = find(&all, w)?;
        let dir = out.join(format!("p1-{i}"));
        export(&fx, text, &dir)?;
        check_bodies(&fx, w.agent, &dir, w.sources)?;
        first.push(entry(w, text, &dir));
        dirs.push(dir);
    }
    // Brief 56: the built binary exports the shared Pi owner (one run record).
    let shared_dir = out.join("shared");
    fs::create_dir_all(&out).map_err(e)?;
    let r = run(
        &fx.rig.dir,
        &[
            "export".as_ref(),
            shared_owner(&fx.rig)?.as_ref(),
            "--out".as_ref(),
            shared_dir.as_os_str(),
        ],
    )?;
    let said = format!(
        "{}{}",
        String::from_utf8_lossy(&r.stdout),
        String::from_utf8_lossy(&r.stderr)
    );
    assert_eq!(r.status.code(), Some(0), "{said}");
    assert!(
        said.contains("exported sources=1 deleted=0") && said.contains("skipped="),
        "{said}"
    );

    // `manifest_sha256` and `publication_id` are the stored head's digest, and a second export
    // of the same head is byte-identical.
    for (i, w) in phase1().iter().enumerate() {
        let text = find(&all, w)?;
        let head = head_digest(&server, text)?;
        let dir = out.join(format!("p1-{i}"));
        let meta = metadata(&dir)?;
        for ptr in ["/manifest_sha256", "/manifest/publication_id"] {
            assert_eq!(
                meta.pointer(ptr).and_then(Value::as_str),
                Some(head.as_str()),
                "{} {ptr}",
                w.name
            );
        }
        let again = out.join(format!("again-{i}"));
        export(&fx, text, &again)?;
        assert_eq!(
            fs::read(again.join("metadata.json")).map_err(e)?,
            fs::read(dir.join("metadata.json")).map_err(e)?,
            "{}: a re-export of the same head differs",
            w.name
        );
        dirs.push(again);
    }

    // Phase 2: an append to s1, and a truncate-and-rewrite of "rewrite".
    let p = fx.claude.join("projects/-p");
    let mut main = fs::read(p.join(format!("{U1}.jsonl"))).map_err(e)?;
    main.extend_from_slice(
        br#"{"type":"user","uuid":"u3","message":{"role":"user","content":"more"}}"#,
    );
    main.push(b'\n');
    fs::write(p.join(format!("{U1}.jsonl")), main).map_err(e)?;
    fs::write(
        p.join(format!("{UR}.jsonl")),
        jl(&[
            r#"{"type":"user","uuid":"x1","message":{"role":"user","content":"rewritten"}}"#,
            r#"{"type":"assistant","uuid":"x2","message":{"role":"assistant","content":"ok"}}"#,
        ]),
    )
    .map_err(e)?;
    let report = daemon.reconcile_once().map_err(e)?;
    assert_eq!((report.failed, report.parked), (0, 0));
    daemon.shutdown().map_err(e)?;
    let mut second = Vec::new();
    let all_p1 = phase1();
    // Brief 53: s1 was 7; the fixture gained an `instructions` attachment.
    for (name, sources, events, roles) in [
        ("s1", 3, 8, json!({"main": 1, "subagent": 1, "artifact": 1})),
        ("rewrite", 2, 5, json!({"main": 2})),
    ] {
        let w = all_p1.iter().find(|w| w.name == name).ok_or("want")?;
        let text = find(&all, w)?;
        let dir = out.join(format!("p2-{name}"));
        export(&fx, text, &dir)?;
        check_bodies(&fx, w.agent, &dir, sources)?;
        let mut v = entry(w, text, &dir);
        if let Some(o) = v.as_object_mut() {
            o.insert("sources".into(), json!(sources));
            o.insert("events".into(), json!(events));
            o.insert("roles".into(), roles);
        }
        second.push(v);
        dirs.push(dir);
    }

    // `memory restore` agrees with the disk byte for byte, per owner.
    for (i, w) in phase1().iter().enumerate() {
        let text = find(&all, w)?;
        let dir = out.join(format!("restore-{i}"));
        fs::create_dir_all(&out).map_err(e)?;
        let r = run(
            &fx.rig.dir,
            &[
                "restore".as_ref(),
                text.as_ref(),
                "--out".as_ref(),
                dir.as_os_str(),
            ],
        )?;
        assert_eq!(
            r.status.code(),
            Some(0),
            "{}",
            String::from_utf8_lossy(&r.stderr)
        );
        let mut files = Vec::new();
        walk(&dir, &dir, &mut files)?;
        assert_eq!(
            files.len(),
            w.sources.max(if w.name == "rewrite" { 1 } else { 0 })
        );
        for f in files {
            assert_eq!(
                fs::read(dir.join(&f)).map_err(e)?,
                disk_logical(root_of(&fx, w.agent), &f)?,
                "restore equals disk for {f:?}"
            );
        }
    }
    scan(&fx, &dirs)?;

    let expect = fx.rig.dir.join("expectations.json");
    let canaries: Vec<&str> = fx.canaries.iter().map(|c| c.0).collect();
    fs::write(
        &expect,
        serde_json::to_vec(
            &json!({"phase1": first, "phase2": second, "canaries": canaries,
            "shared": shared_dir, "run_canaries": RUN_CANARIES}),
        )
        .map_err(e)?,
    )
    .map_err(e)?;
    let tools = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../tools/analytics");
    let o = Command::new(&py)
        .current_dir(&tools)
        .env_clear()
        .env("RUST_EXPORT_JOURNEY", &expect)
        .env("PYTHONDONTWRITEBYTECODE", "1")
        .args(["-I", "test_rust_export_journey.py", "-v"])
        .output()
        .map_err(e)?;
    let text = String::from_utf8_lossy(&o.stderr);
    assert_eq!(
        o.status.code(),
        Some(0),
        "{text}{}",
        String::from_utf8_lossy(&o.stdout)
    );
    assert!(text.contains("\nOK"), "{text}");
    assert!(
        !text.contains("skipped"),
        "the Python half must run, not skip: {text}"
    );
    assert!(text.contains("Ran "), "{text}");
    Ok(())
}

#[test]
fn consumer_gate_withheld_canaries_never_reach_an_export() -> R {
    let server = Fake::start()?;
    let fx = fixture(&server, "canary")?;
    let daemon = captured(&fx)?;
    daemon.shutdown().map_err(e)?;
    let mut dirs = Vec::new();
    for (i, text) in owners(&fx.rig)?.iter().enumerate() {
        let dir = fx.rig.dir.join(format!("exports/{i}"));
        export(&fx, text, &dir)?;
        dirs.push(dir);
    }
    scan(&fx, &dirs)
}

/// A deleted source is still exported (every epoch `sealed`, none `active`) and the unchanged
/// importer accepts it.
#[test]
fn consumer_gate_deleted_source_exports_sealed_and_imports() -> R {
    let Some(py) = python()? else { return Ok(()) };
    let server = Fake::start()?;
    let fx = fixture(&server, "gate-deleted")?;
    let mut daemon = captured(&fx)?;
    let all = owners(&fx.rig)?;
    let want = phase1();
    let w = want.iter().find(|w| w.name == "rewrite").ok_or("want")?;
    let text = find(&all, w)?.clone();
    fs::remove_file(fx.claude.join(format!("projects/-p/{UR}.jsonl"))).map_err(e)?;
    let first = daemon.reconcile_once().map_err(e)?;
    assert_eq!(first.deleted, 0);
    let second = daemon.reconcile_once().map_err(e)?;
    assert_eq!((second.failed, second.parked, second.deleted), (0, 0, 1));
    daemon.shutdown().map_err(e)?;
    let dir = fx.rig.dir.join("exports/deleted");
    export(&fx, &text, &dir)?;
    let meta = metadata(&dir)?;
    let states: Vec<&str> = meta
        .pointer("/manifest/sources")
        .and_then(Value::as_array)
        .ok_or("sources")?
        .iter()
        .filter_map(|s| s.get("state").and_then(Value::as_str))
        .collect();
    assert_eq!(states, ["sealed"], "a deleted source is never active");
    let tools = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../tools/analytics");
    let db = fx.rig.dir.join("deleted.duckdb");
    let script = "import sys, duckdb\nsys.path.insert(0, sys.argv[1])\nimport publications\n\
r = publications.import_export(sys.argv[2], sys.argv[3])\n\
assert r['status'] == 'imported', r\n\
c = duckdb.connect(sys.argv[3], read_only=True)\n\
states = [x[0] for x in c.execute('SELECT state FROM sources').fetchall()]\n\
assert states and set(states) == {'sealed'}, states\n";
    let o = Command::new(&py)
        .current_dir(&tools)
        .env_clear()
        .env("PYTHONDONTWRITEBYTECODE", "1")
        .arg("-I")
        .arg("-c")
        .arg(script)
        .arg(&tools)
        .arg(&dir)
        .arg(&db)
        .output()
        .map_err(e)?;
    assert_eq!(
        o.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&o.stderr)
    );
    Ok(())
}
