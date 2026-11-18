//! `memory install` / `memory uninstall`: the ordered JSON type, the merge rules, the written
//! files and the real binary under a temporary `HOME`.
use memory::install::json::{self, Value};
use proptest::prelude::*;
use serde_json::Number;

type R<T = ()> = Result<T, String>;

fn parse(text: &str) -> R<Value> {
    json::parse(text).map_err(|error| error.to_string())
}

fn round_trips(text: &str) -> R {
    let out = json::to_text(&parse(text)?);
    if out == text {
        Ok(())
    } else {
        Err(format!("not identical:\n{text}\n--\n{out}"))
    }
}

#[test]
fn json_round_trip_nested_objects_and_arrays() -> R {
    round_trips(
        "{\n  \"b\": {\n    \"z\": [\n      1,\n      {\n        \"y\": null\n      }\n    ],\n    \"a\": true\n  },\n  \"a\": false\n}\n",
    )
}

#[test]
fn json_round_trip_empty_containers() -> R {
    round_trips("{\n  \"a\": [],\n  \"b\": {},\n  \"c\": [\n    []\n  ]\n}\n")?;
    round_trips("{}\n")
}

#[test]
fn json_round_trip_unicode_and_escapes() -> R {
    round_trips(
        "{\n  \"naïve ☃\": \"héllo 日本 😀\",\n  \"esc\": \"a\\\"b\\\\c\\n\\r\\t\\b\\f\\u0001\\u001f/\"\n}\n",
    )
}

#[test]
fn json_round_trip_numbers() -> R {
    round_trips(
        "{\n  \"i\": 0,\n  \"n\": -17,\n  \"big\": 18446744073709551615,\n  \"f\": 1.5,\n  \"g\": -0.25,\n  \"h\": 3.0\n}\n",
    )
}

#[test]
fn json_blank_is_empty_object() -> R {
    for text in ["", "  \n\t"] {
        if parse(text)? != Value::Object(Vec::new()) {
            return Err("blank is not an empty object".into());
        }
    }
    Ok(())
}

#[test]
fn json_duplicate_keys_refused() {
    for text in [
        "{\"a\":1,\"a\":2}",
        "{\"x\":{\"a\":1,\"b\":2,\"a\":3}}",
        "{\"x\":[{\"k\":1,\"k\":1}]}",
    ] {
        assert_eq!(
            json::parse(text),
            Err(json::JsonError::Unparsable),
            "{text}"
        );
    }
}

#[test]
fn json_non_object_and_garbage_refused() {
    for text in ["[]", "1", "\"s\"", "null", "true"] {
        assert_eq!(json::parse(text), Err(json::JsonError::NotObject), "{text}");
    }
    for text in ["{", "{} x", "{\"a\":}", "nope", "{\"a\":1,}"] {
        assert_eq!(
            json::parse(text),
            Err(json::JsonError::Unparsable),
            "{text}"
        );
    }
}

#[test]
fn json_key_order_kept() -> R {
    let Value::Object(members) = parse("{\"z\":1,\"a\":2,\"m\":3}")? else {
        return Err("not an object".into());
    };
    let keys: Vec<&str> = members.iter().map(|(key, _)| key.as_str()).collect();
    if keys == ["z", "a", "m"] {
        Ok(())
    } else {
        Err(format!("{keys:?}"))
    }
}

fn arb_text() -> impl Strategy<Value = String> {
    proptest::collection::vec(any::<char>(), 0..8).prop_map(String::from_iter)
}

fn arb_value() -> impl Strategy<Value = Value> {
    let leaf = prop_oneof![
        Just(Value::Null),
        any::<bool>().prop_map(Value::Bool),
        any::<i64>().prop_map(|n| Value::Number(n.into())),
        any::<u64>().prop_map(|n| Value::Number(n.into())),
        // Quarter steps are exact in binary; serde_json without `float_roundtrip` may misread
        // 17-digit floats, and the brief forbids a feature change.
        (-4_000_000_i32..4_000_000)
            .prop_map(|n| Number::from_f64(f64::from(n) / 4.0).map_or(Value::Null, Value::Number)),
        arb_text().prop_map(Value::String),
    ];
    leaf.prop_recursive(4, 48, 4, |inner| {
        prop_oneof![
            proptest::collection::vec(inner.clone(), 0..4).prop_map(Value::Array),
            proptest::collection::vec((arb_text(), inner), 0..4).prop_map(|members| {
                let mut seen = std::collections::HashSet::new();
                Value::Object(
                    members
                        .into_iter()
                        .filter(|(key, _)| seen.insert(key.clone()))
                        .collect(),
                )
            }),
        ]
    })
}

fn arb_document() -> impl Strategy<Value = Value> {
    proptest::collection::vec((arb_text(), arb_value()), 0..5).prop_map(|members| {
        let mut seen = std::collections::HashSet::new();
        Value::Object(
            members
                .into_iter()
                .filter(|(key, _)| seen.insert(key.clone()))
                .collect(),
        )
    })
}

proptest! {
    #[test]
    fn proptest_json_write_parse_write_is_stable(doc in arb_document()) {
        let text = json::to_text(&doc);
        let back = json::parse(&text).map_err(|e| TestCaseError::fail(e.to_string()))?;
        prop_assert_eq!(&back, &doc);
        prop_assert_eq!(json::to_text(&back), text);
    }
}

// ---- merge ----

use memory::install::Failure;
use memory::install::merge::{Agent, hook_command, is_ours, merge_text, quote};

const EXE: &str = "/opt/bin/memory";

fn install(text: &str, agent: Agent) -> Result<String, Failure> {
    merge_text(text, agent, Some(&hook_command(EXE, agent)))
}

fn uninstall(text: &str, agent: Agent) -> Result<String, Failure> {
    merge_text(text, agent, None)
}

fn at<'a>(value: &'a Value, path: &[&str]) -> Option<&'a Value> {
    let mut cur = value;
    for step in path {
        cur = match cur {
            Value::Object(members) => &members.iter().find(|(k, _)| k == step)?.1,
            Value::Array(items) => items.get(step.parse::<usize>().ok()?)?,
            _ => return None,
        };
    }
    Some(cur)
}

fn command_at(text: &str, path: &[&str]) -> Option<String> {
    match at(&json::parse(text).ok()?, path)? {
        Value::String(s) => Some(s.clone()),
        _ => None,
    }
}

const FOREIGN: &str = r#"{
  "theme": "dark",
  "hooks": {
    "Stop": [
      {
        "matcher": "x",
        "hooks": [
          { "type": "command", "command": "foreign-a" },
          { "type": "command", "command": "foreign-b" }
        ]
      },
      { "hooks": [ { "type": "command", "command": "foreign-c" } ] }
    ],
    "Other": [ { "hooks": [ { "type": "command", "command": "foreign-d" } ] } ]
  },
  "zed": [1, 2]
}"#;

fn canon(text: &str) -> Result<String, String> {
    Ok(json::to_text(&parse(text)?))
}

#[test]
fn merge_foreign_handlers_keep_their_positions_and_other_keys() -> R {
    let out = install(FOREIGN, Agent::Claude).map_err(|e| e.to_string())?;
    let g = |p: &[&str]| command_at(&out, p);
    assert_eq!(
        g(&["hooks", "Stop", "0", "hooks", "0", "command"]).as_deref(),
        Some("foreign-a")
    );
    assert_eq!(
        g(&["hooks", "Stop", "0", "hooks", "1", "command"]).as_deref(),
        Some("foreign-b")
    );
    assert_eq!(
        g(&["hooks", "Stop", "1", "hooks", "0", "command"]).as_deref(),
        Some("foreign-c")
    );
    assert_eq!(
        g(&["hooks", "Stop", "2", "hooks", "0", "command"]).as_deref(),
        Some("/opt/bin/memory hook claude")
    );
    assert_eq!(
        g(&["hooks", "Other", "0", "hooks", "0", "command"]).as_deref(),
        Some("foreign-d")
    );
    assert_eq!(g(&["theme"]).as_deref(), Some("dark"));
    let Value::Object(top) = parse(&out)? else {
        return Err("not an object".into());
    };
    let keys: Vec<&str> = top.iter().map(|(k, _)| k.as_str()).collect();
    assert_eq!(keys, ["theme", "hooks", "zed"]);
    let Some(Value::Object(hooks)) = at(&parse(&out)?, &["hooks"]).cloned() else {
        return Err("no hooks".into());
    };
    let names: Vec<&str> = hooks.iter().map(|(k, _)| k.as_str()).collect();
    assert_eq!(
        names.iter().take(2).copied().collect::<Vec<_>>(),
        ["Stop", "Other"]
    );
    assert_eq!(names.len(), 9);
    Ok(())
}

#[test]
fn merge_second_install_is_byte_identical() -> R {
    for agent in [Agent::Claude, Agent::Codex] {
        let doc = if agent == Agent::Codex {
            "{\"hooks\": {}}"
        } else {
            FOREIGN
        };
        let once = install(doc, agent).map_err(|e| e.to_string())?;
        let twice = install(&once, agent).map_err(|e| e.to_string())?;
        assert_eq!(once, twice);
    }
    Ok(())
}

#[test]
fn merge_older_path_replaced_in_place() -> R {
    let doc = r#"{"hooks":{"Stop":[{"hooks":[
        {"type":"command","command":"keep-1"},
        {"type":"command","command":"/old/place/memory hook claude","async":true,"timeout":9},
        {"type":"command","command":"keep-2"}]},
        {"hooks":[{"type":"command","command":"'/old two/memory' hook claude"}]}]}}"#;
    let out = install(doc, Agent::Claude).map_err(|e| e.to_string())?;
    let g = |p: &[&str]| command_at(&out, p);
    assert_eq!(
        g(&["hooks", "Stop", "0", "hooks", "0", "command"]).as_deref(),
        Some("keep-1")
    );
    assert_eq!(
        g(&["hooks", "Stop", "0", "hooks", "1", "command"]).as_deref(),
        Some("/opt/bin/memory hook claude")
    );
    assert_eq!(
        g(&["hooks", "Stop", "0", "hooks", "2", "command"]).as_deref(),
        Some("keep-2")
    );
    // The second old one is dropped with its emptied group.
    assert_eq!(at(&parse(&out)?, &["hooks", "Stop", "1"]), None);
    Ok(())
}

#[test]
fn merge_uninstall_restores_a_foreign_only_document() -> R {
    for agent in [Agent::Claude, Agent::Codex] {
        let doc = if agent == Agent::Codex {
            "{\"description\": \"d\", \"hooks\": {\"Stop\": [{\"hooks\": [{\"type\": \"command\", \"command\": \"f\"}]}]}}"
        } else {
            FOREIGN
        };
        let installed = install(doc, agent).map_err(|e| e.to_string())?;
        assert_ne!(installed, canon(doc)?);
        assert_eq!(
            uninstall(&installed, agent).map_err(|e| e.to_string())?,
            canon(doc)?
        );
    }
    Ok(())
}

#[test]
fn merge_uninstall_of_an_empty_document_removes_hooks_key() -> R {
    let installed = install("", Agent::Claude).map_err(|e| e.to_string())?;
    assert_eq!(
        uninstall(&installed, Agent::Claude).map_err(|e| e.to_string())?,
        "{}\n"
    );
    Ok(())
}

#[test]
fn merge_acceptance_and_go_commands_are_never_touched() -> R {
    let doc = r#"{"hooks":{"Stop":[{"hooks":[
        {"type":"command","command":": memory-acceptance; exec env MEMORY_STATE_DIR=/s /x/memory hook claude"},
        {"type":"command","command":"/usr/local/bin/agent-sessions --x hook claude"},
        {"type":"command","command":"/opt/bin/memory hook codex"},
        {"type":"command","command":"/opt/bin/other hook claude"},
        {"type":"command","command":"memory hook claude"}]}]}}"#;
    let out = uninstall(doc, Agent::Claude).map_err(|e| e.to_string())?;
    assert_eq!(out, canon(doc)?);
    let out = install(doc, Agent::Claude).map_err(|e| e.to_string())?;
    for i in ["0", "1", "2", "3", "4"] {
        assert!(command_at(&out, &["hooks", "Stop", "0", "hooks", i, "command"]).is_some());
    }
    assert_eq!(
        command_at(&out, &["hooks", "Stop", "1", "hooks", "0", "command"]).as_deref(),
        Some("/opt/bin/memory hook claude")
    );
    Ok(())
}

#[test]
fn merge_event_left_without_groups_is_deleted() -> R {
    let doc = r#"{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/opt/bin/memory hook claude"}]}],"Keep":[{"hooks":[{"type":"command","command":"f"}]}]}}"#;
    let out = uninstall(doc, Agent::Claude).map_err(|e| e.to_string())?;
    assert_eq!(
        out,
        canon(r#"{"hooks":{"Keep":[{"hooks":[{"type":"command","command":"f"}]}]}}"#)?
    );
    Ok(())
}

#[test]
fn merge_handler_shape_and_key_order() -> R {
    let out = install("{}", Agent::Claude).map_err(|e| e.to_string())?;
    assert!(out.contains(
        "\"type\": \"command\",\n            \"command\": \"/opt/bin/memory hook claude\",\n            \"async\": true,\n            \"timeout\": 30\n"
    ));
    assert!(
        out.contains("\"command\": \"/opt/bin/memory hook claude\",\n            \"timeout\": 5\n")
    );
    let codex = install("{}", Agent::Codex).map_err(|e| e.to_string())?;
    assert!(codex.contains("\"timeout\": 3\n"));
    assert_eq!(codex.matches("\"async\": true").count(), 5);
    assert_eq!(out.matches("\"async\": true").count(), 7);
    Ok(())
}

#[test]
fn merge_codex_unknown_top_level_key_is_refused() {
    for doc in [
        "{\"hooks\": {}, \"extra\": 1}",
        "{\"hooks\": []}",
        "{\"description\": 3}",
    ] {
        assert_eq!(
            install(doc, Agent::Codex),
            Err(Failure::CodexSchema),
            "{doc}"
        );
    }
    // The same document is fine for Claude.
    assert!(install("{\"extra\": 1}", Agent::Claude).is_ok());
}

#[test]
fn merge_unparsable_and_odd_shapes_are_refused() {
    for doc in ["[]", "{", "{\"a\":1,\"a\":2}"] {
        assert_eq!(
            install(doc, Agent::Claude),
            Err(Failure::Unparsable),
            "{doc}"
        );
    }
    assert_eq!(
        install("{\"hooks\": 3}", Agent::Claude),
        Err(Failure::Unparsable)
    );
    assert_eq!(
        install("{\"hooks\": {\"Stop\": 3}}", Agent::Claude),
        Err(Failure::Unparsable)
    );
    // A foreign event of another shape is left as it is.
    assert!(install("{\"hooks\": {\"Odd\": 3}}", Agent::Claude).is_ok());
}

#[test]
fn merge_ours_rule_is_exact() {
    let yes = [
        "/opt/bin/memory hook claude",
        "'/opt/my bin/memory' hook claude",
        "'/opt/it'\"'\"'s/memory' hook claude",
    ];
    for c in yes {
        assert!(is_ours(c, Agent::Claude), "{c}");
        assert!(!is_ours(c, Agent::Codex), "{c}");
    }
    let no = [
        "memory hook claude",
        "bin/memory hook claude",
        "/opt/bin/memory2 hook claude",
        "/opt/bin/memory hook claude extra",
        "/opt/bin/memory  hook claude",
        "'/opt/bin/memory' hook claude",
        "/opt/bin/memory/ hook claude",
        "env /opt/bin/memory hook claude",
        "'/opt/my bin/memory hook claude",
    ];
    for c in no {
        assert!(!is_ours(c, Agent::Claude), "{c}");
    }
}

#[test]
fn merge_quote_forms() {
    assert_eq!(quote("/a/b-c_d.e/memory"), "/a/b-c_d.e/memory");
    assert_eq!(quote("/a b/memory"), "'/a b/memory'");
    assert_eq!(quote("/a'b/memory"), "'/a'\"'\"'b/memory'");
    for exe in ["/a b/memory", "/it's/memory", "/x/ü/memory", "/a$b/memory"] {
        assert!(
            is_ours(&hook_command(exe, Agent::Claude), Agent::Claude),
            "{exe}"
        );
    }
}

// ---- files ----

use memory::install::files::{self, Outcome};
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;

fn temp(name: &str) -> std::io::Result<PathBuf> {
    let dir = std::env::temp_dir().join(format!("memory-install-{}-{name}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(&dir)?;
    Ok(dir)
}

/// `<T>` stands for a tab.
const GOLDEN_PI: &str = r#"// memory: managed by `memory install`; edits are overwritten.
import { spawn } from "node:child_process";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const BIN = "/opt/my bin/memory";

function kick(event: string, ctx: any): void {
<T>try {
<T><T>const file = ctx.sessionManager.getSessionFile();
<T><T>// No file for --no-session runs, or before the session's first message.
<T><T>if (!file) return;
<T><T>const payload = JSON.stringify({
<T><T><T>event,
<T><T><T>session_id: ctx.sessionManager.getSessionId(),
<T><T><T>session_file: file,
<T><T><T>cwd: ctx.cwd,
<T><T>});
<T><T>const child = spawn(BIN, ["hook", "pi"], {
<T><T><T>detached: true,
<T><T><T>stdio: ["pipe", "ignore", "ignore"],
<T><T>});
<T><T>child.on("error", () => {});
<T><T>child.stdin.on("error", () => {});
<T><T>child.stdin.end(payload);
<T><T>child.unref();
<T>} catch {
<T><T>// Capture must never break the session.
<T>}
}

export default function (pi: ExtensionAPI) {
<T>pi.on("session_start", (_event, ctx) => kick("session_start", ctx));
<T>pi.on("agent_settled", (_event, ctx) => kick("agent_settled", ctx));
<T>pi.on("session_shutdown", (_event, ctx) => kick("session_shutdown", ctx));
}
"#;

#[test]
fn files_pi_extension_matches_golden_text() {
    assert_eq!(
        files::pi_extension("/opt/my bin/memory"),
        GOLDEN_PI.replace("<T>", "\t")
    );
}

const GOLDEN_PLIST: &str = r#"<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>memory</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/a&amp;b/memory</string>
    <string>daemon</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key>
  <dict><key>SuccessfulExit</key><false/></dict>
  <key>ThrottleInterval</key><integer>60</integer>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>/dev/null</string>
  <key>StandardErrorPath</key><string>/h/Library/Logs/memory/daemon.log</string>
</dict>
</plist>
"#;

#[test]
fn files_plist_matches_golden_text_and_is_recognised_as_ours() {
    let text = files::plist("/opt/a&b/memory", "/h/Library/Logs/memory/daemon.log");
    assert_eq!(text, GOLDEN_PLIST);
    assert!(files::plist_is_ours(&text));
    assert!(!files::plist_is_ours(
        &text.replace("<string>memory</string>", "<string>other</string>")
    ));
    assert!(!files::plist_is_ours(
        &text.replace("/opt/a&amp;b/memory", "/opt/a/other")
    ));
}

#[test]
fn files_plist_macos_plutil_lint() -> R {
    if !cfg!(target_os = "macos") {
        return Ok(());
    }
    let dir = temp("plutil").map_err(|e| e.to_string())?;
    let path = dir.join("memory.plist");
    std::fs::write(&path, files::plist("/opt/a&b/<x>/memory", "/h/l.log"))
        .map_err(|e| e.to_string())?;
    let out = std::process::Command::new("plutil")
        .arg("-lint")
        .arg(&path)
        .output()
        .map_err(|e| e.to_string())?;
    let _ = std::fs::remove_dir_all(&dir);
    if out.status.success() {
        Ok(())
    } else {
        Err("plutil -lint failed".into())
    }
}

#[test]
fn files_utc_stamp_is_utc_basic_format() {
    assert_eq!(files::utc_stamp(0), "19700101T000000Z");
    assert_eq!(files::utc_stamp(951_782_400 + 86_399), "20000229T235959Z");
    assert_eq!(files::utc_stamp(1_700_000_000), "20231114T221320Z");
}

fn mode(p: &std::path::Path) -> Result<u32, String> {
    std::fs::metadata(p)
        .map(|m| m.permissions().mode() & 0o7777)
        .map_err(|x| x.to_string())
}

#[test]
fn files_write_unchanged_backup_and_modes() -> R {
    let dir = temp("write").map_err(|e| e.to_string())?;
    let path = dir.join("f.json");
    let e = |x: Failure| x.to_string();
    let out = files::write_file(&path, None, "one\n", false, 0).map_err(e)?;
    assert_eq!(out, Outcome::Wrote { backup: None });
    assert_eq!(mode(&path)?, 0o644);
    let out = files::write_file(&path, Some(b"one\n"), "one\n", false, 0).map_err(e)?;
    assert_eq!(out, Outcome::Unchanged);
    std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600))
        .map_err(|x| x.to_string())?;
    let out = files::write_file(&path, Some(b"one\n"), "two\n", false, 0).map_err(e)?;
    let Outcome::Wrote { backup: Some(name) } = out else {
        return Err("no backup".into());
    };
    assert_eq!(name, "f.json.memory-backup-19700101T000000Z");
    let backup = dir.join(&name);
    assert_eq!(std::fs::read(&backup).map_err(|x| x.to_string())?, b"one\n");
    assert_eq!(mode(&backup)?, 0o600);
    assert_eq!(mode(&path)?, 0o600);
    assert_eq!(
        std::fs::read_to_string(&path).map_err(|x| x.to_string())?,
        "two\n"
    );
    let out = files::write_file(&path, Some(b"two\n"), "three\n", true, 5).map_err(e)?;
    assert!(matches!(out, Outcome::Wrote { backup: Some(_) }));
    assert_eq!(
        std::fs::read_to_string(&path).map_err(|x| x.to_string())?,
        "two\n"
    );
    assert_eq!(
        std::fs::read_dir(&dir).map_err(|x| x.to_string())?.count(),
        2
    );
    let _ = std::fs::remove_dir_all(&dir);
    Ok(())
}

#[test]
fn files_pi_marker_decides_ownership() -> R {
    let dir = temp("pi").map_err(|e| e.to_string())?;
    let path = dir.join("ext").join("memory.ts");
    let e = |x: Failure| x.to_string();
    let out = files::pi_file(&path, Some("/x/memory"), false, 0).map_err(e)?;
    assert_eq!(out, Outcome::Wrote { backup: None });
    let out = files::pi_file(&path, Some("/x/memory"), false, 0).map_err(e)?;
    assert_eq!(out, Outcome::Unchanged);
    assert_eq!(
        files::pi_file(&path, None, false, 0).map_err(e)?,
        Outcome::Removed
    );
    assert!(!path.exists());
    std::fs::write(&path, "// mine\n").map_err(|x| x.to_string())?;
    assert_eq!(
        files::pi_file(&path, Some("/x/memory"), false, 0).map_err(e)?,
        Outcome::Kept
    );
    assert_eq!(
        files::pi_file(&path, None, false, 0).map_err(e)?,
        Outcome::Kept
    );
    assert_eq!(
        std::fs::read_to_string(&path).map_err(|x| x.to_string())?,
        "// mine\n"
    );
    let _ = std::fs::remove_dir_all(&dir);
    Ok(())
}

// ---- real binary ----

use std::process::{Command, Output, Stdio};

const BIN: &str = env!("CARGO_BIN_EXE_memory");

struct Home(PathBuf);

impl Home {
    fn new(name: &str, with_config: bool) -> Result<Self, String> {
        let e = |x: std::io::Error| x.to_string();
        let base = temp(name).map_err(e)?;
        let home = base.join("home");
        let root = home.join(".local/state/memory");
        std::fs::create_dir_all(&root).map_err(e)?;
        std::fs::set_permissions(&root, std::fs::Permissions::from_mode(0o700)).map_err(e)?;
        let token = base.join("token");
        std::fs::write(&token, "access_key_id = \"k\"\nsecret_access_key = \"s\"\n").map_err(e)?;
        std::fs::set_permissions(&token, std::fs::Permissions::from_mode(0o600)).map_err(e)?;
        if with_config {
            std::fs::write(
                root.join("config.toml"),
                format!(
                    "archive_id = \"a\"\nmachine_id = \"m\"\nendpoint = \"http://127.0.0.1:9\"\nbucket = \"bucket-1\"\nprefix = \"p/\"\ntoken_path = \"{}\"\n",
                    token.display()
                ),
            )
            .map_err(e)?;
        }
        Ok(Self(home))
    }
    fn p(&self, rel: &str) -> PathBuf {
        self.0.join(rel)
    }
    fn run(&self, args: &[&str], extra: &[(&str, &str)]) -> Result<Output, String> {
        let mut cmd = Command::new(BIN);
        cmd.args(args).env_clear().env("HOME", &self.0);
        if let Some(profile) = std::env::var_os("LLVM_PROFILE_FILE") {
            cmd.env("LLVM_PROFILE_FILE", profile);
        }
        for (k, v) in extra {
            cmd.env(k, v);
        }
        cmd.stdin(Stdio::null()).output().map_err(|x| x.to_string())
    }
    fn read(&self, rel: &str) -> Result<String, String> {
        std::fs::read_to_string(self.p(rel)).map_err(|x| format!("{rel}: {x}"))
    }
    fn backups(&self) -> Result<usize, String> {
        let mut n = 0;
        for dir in [
            ".claude",
            ".codex",
            ".pi/agent/extensions",
            "Library/LaunchAgents",
        ] {
            if let Ok(rd) = std::fs::read_dir(self.p(dir)) {
                n += rd
                    .filter_map(Result::ok)
                    .filter(|f| f.file_name().to_string_lossy().contains(".memory-backup-"))
                    .count();
            }
        }
        Ok(n)
    }
}

impl Drop for Home {
    fn drop(&mut self) {
        if let Some(base) = self.0.parent() {
            let _ = std::fs::remove_dir_all(base);
        }
    }
}

fn out_text(o: &Output) -> String {
    String::from_utf8_lossy(&o.stdout).into_owned()
}
fn err_text(o: &Output) -> String {
    String::from_utf8_lossy(&o.stderr).into_owned()
}

const FILES: [&str; 3] = [
    ".claude/settings.json",
    ".codex/hooks.json",
    ".pi/agent/extensions/memory.ts",
];

#[test]
fn bin_install_writes_files_and_second_run_is_unchanged() -> R {
    let h = Home::new("bin-install", true)?;
    let o = h.run(&["install"], &[])?;
    assert_eq!(o.status.code(), Some(0), "{}", err_text(&o));
    let text = out_text(&o);
    for f in FILES {
        assert!(
            text.contains(&format!("wrote {}", h.p(f).display())),
            "{text}"
        );
        assert!(h.p(f).exists());
    }
    if cfg!(target_os = "macos") {
        assert!(h.p("Library/LaunchAgents/memory.plist").exists());
        assert!(text.contains("next: launchctl bootstrap gui/$(id -u) "));
        assert!(h.p("Library/Logs/memory").is_dir());
    } else {
        assert!(text.contains("launchd: skipped (not macOS)"));
    }
    assert!(text.contains("next: approve the memory hooks in Codex /hooks"));
    assert_eq!(h.backups()?, 0);
    let before: Vec<String> = FILES.iter().map(|f| h.read(f)).collect::<Result<_, _>>()?;
    let o = h.run(&["install"], &[])?;
    let text = out_text(&o);
    assert_eq!(o.status.code(), Some(0));
    for f in FILES {
        assert!(
            text.contains(&format!("unchanged {}", h.p(f).display())),
            "{text}"
        );
    }
    assert!(!text.contains("approve the memory hooks"));
    assert_eq!(h.backups()?, 0);
    let after: Vec<String> = FILES.iter().map(|f| h.read(f)).collect::<Result<_, _>>()?;
    assert_eq!(before, after);
    Ok(())
}

#[test]
fn bin_install_backs_up_a_changed_file_once_with_old_bytes_and_mode() -> R {
    let h = Home::new("bin-backup", true)?;
    let e = |x: std::io::Error| x.to_string();
    std::fs::create_dir_all(h.p(".claude")).map_err(e)?;
    let old = "{\n  \"theme\": \"dark\"\n}\n";
    let path = h.p(".claude/settings.json");
    std::fs::write(&path, old).map_err(e)?;
    std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600)).map_err(e)?;
    let o = h.run(&["install"], &[])?;
    assert_eq!(o.status.code(), Some(0), "{}", err_text(&o));
    assert!(out_text(&o).contains(&format!(
        "wrote {} (backup settings.json.memory-backup-",
        path.display()
    )));
    let backups: Vec<PathBuf> = std::fs::read_dir(h.p(".claude"))
        .map_err(e)?
        .filter_map(Result::ok)
        .map(|f| f.path())
        .filter(|p| p.to_string_lossy().contains(".memory-backup-"))
        .collect();
    assert_eq!(backups.len(), 1);
    let b = backups.first().ok_or("none")?;
    assert_eq!(std::fs::read_to_string(b).map_err(e)?, old);
    assert_eq!(mode(b)?, 0o600);
    assert_eq!(mode(&path)?, 0o600);
    assert!(
        h.read(".claude/settings.json")?
            .contains("\"theme\": \"dark\"")
    );
    Ok(())
}

#[test]
fn bin_install_dry_run_writes_nothing() -> R {
    let h = Home::new("bin-dry", true)?;
    let o = h.run(&["install", "--dry-run"], &[])?;
    assert_eq!(o.status.code(), Some(0), "{}", err_text(&o));
    assert!(out_text(&o).contains("wrote "));
    for f in FILES {
        assert!(!h.p(f).exists(), "{f}");
    }
    assert!(!h.p("Library").exists());
    Ok(())
}

#[test]
fn bin_install_unparsable_settings_refused_and_stops_later_files() -> R {
    let h = Home::new("bin-unparsable", true)?;
    let e = |x: std::io::Error| x.to_string();
    std::fs::create_dir_all(h.p(".claude")).map_err(e)?;
    std::fs::write(h.p(".claude/settings.json"), "{ nope").map_err(e)?;
    let o = h.run(&["install"], &[])?;
    assert_eq!(o.status.code(), Some(1));
    assert_eq!(err_text(&o), "memory: install_unparsable\n");
    assert_eq!(h.read(".claude/settings.json")?, "{ nope");
    assert!(!h.p(".codex/hooks.json").exists());
    assert!(!h.p(".pi").exists());
    assert_eq!(h.backups()?, 0);
    Ok(())
}

#[test]
fn bin_install_codex_unknown_key_refused() -> R {
    let h = Home::new("bin-codex-schema", true)?;
    let e = |x: std::io::Error| x.to_string();
    std::fs::create_dir_all(h.p(".codex")).map_err(e)?;
    std::fs::write(h.p(".codex/hooks.json"), "{\"zzz\": 1}").map_err(e)?;
    let o = h.run(&["install"], &[])?;
    assert_eq!(o.status.code(), Some(1));
    assert_eq!(err_text(&o), "memory: install_codex_schema\n");
    assert_eq!(h.read(".codex/hooks.json")?, "{\"zzz\": 1}");
    assert!(!h.p(".pi").exists());
    Ok(())
}

#[test]
fn bin_install_keeps_a_foreign_pi_extension() -> R {
    let h = Home::new("bin-foreign-pi", true)?;
    let e = |x: std::io::Error| x.to_string();
    std::fs::create_dir_all(h.p(".pi/agent/extensions")).map_err(e)?;
    std::fs::write(h.p(".pi/agent/extensions/memory.ts"), "// mine\n").map_err(e)?;
    let o = h.run(&["install"], &[])?;
    assert_eq!(o.status.code(), Some(0));
    assert!(out_text(&o).contains(&format!("kept {} (not ours)", h.p(FILES[2]).display())));
    assert_eq!(h.read(FILES[2])?, "// mine\n");
    Ok(())
}

#[test]
fn bin_install_refuses_state_dir_override_and_missing_config() -> R {
    let h = Home::new("bin-refuse", true)?;
    let o = h.run(&["install"], &[("MEMORY_STATE_DIR", "/nonexistent-x")])?;
    assert_eq!(o.status.code(), Some(2));
    assert_eq!(err_text(&o), "memory: install_state_dir_override\n");
    assert!(!h.p(".claude").exists());
    let n = Home::new("bin-noconfig", false)?;
    let o = n.run(&["install"], &[])?;
    assert_eq!(o.status.code(), Some(2));
    assert!(
        err_text(&o).starts_with("memory: config_"),
        "{}",
        err_text(&o)
    );
    assert!(!n.p(".claude").exists());
    Ok(())
}

#[test]
fn bin_install_extra_argument_is_usage() -> R {
    let h = Home::new("bin-usage", true)?;
    for args in [
        &["install", "extra"][..],
        &["uninstall", "--x"],
        &["install", "--dry-run", "x"],
    ] {
        let o = h.run(args, &[])?;
        assert_eq!(o.status.code(), Some(64), "{args:?}");
        assert_eq!(err_text(&o), "memory: usage\n");
    }
    Ok(())
}

#[test]
fn bin_uninstall_removes_ours_and_keeps_foreign() -> R {
    let h = Home::new("bin-uninstall", true)?;
    let e = |x: std::io::Error| x.to_string();
    std::fs::create_dir_all(h.p(".claude")).map_err(e)?;
    let foreign = "{\n  \"hooks\": {\n    \"Stop\": [\n      {\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"foreign\"\n          }\n        ]\n      }\n    ]\n  }\n}\n";
    std::fs::write(h.p(".claude/settings.json"), foreign).map_err(e)?;
    assert_eq!(h.run(&["install"], &[])?.status.code(), Some(0));
    let o = h.run(&["uninstall"], &[])?;
    assert_eq!(o.status.code(), Some(0), "{}", err_text(&o));
    let text = out_text(&o);
    assert_eq!(h.read(".claude/settings.json")?, foreign);
    assert!(
        text.contains(&format!("removed {}", h.p(FILES[2]).display())),
        "{text}"
    );
    assert!(!h.p(FILES[2]).exists());
    if cfg!(target_os = "macos") {
        assert!(!h.p("Library/LaunchAgents/memory.plist").exists());
        assert!(text.contains("next: launchctl bootout gui/$(id -u)/memory"));
    }
    // Uninstall needs no config and a second run changes nothing.
    let o = h.run(&["uninstall"], &[])?;
    assert_eq!(o.status.code(), Some(0));
    assert!(!out_text(&o).contains("removed"));
    Ok(())
}

#[test]
fn bin_hook_round_trip_appends_one_hint_per_agent() -> R {
    let h = Home::new("bin-roundtrip", true)?;
    assert_eq!(h.run(&["install"], &[])?.status.code(), Some(0));
    let count = || -> usize {
        let mut n = 0;
        if let Ok(rd) = std::fs::read_dir(h.p(".local/state/memory/hints")) {
            for f in rd.filter_map(Result::ok) {
                n += std::fs::read_to_string(f.path()).map_or(0, |t| t.lines().count());
            }
        }
        n
    };
    let run_cmd = |command: &str, payload: &str| -> R {
        let mut child = Command::new("sh")
            .arg("-c")
            .arg(command)
            .env_clear()
            .env("HOME", &h.0)
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .map_err(|x| x.to_string())?;
        {
            use std::io::Write;
            let mut stdin = child.stdin.take().ok_or("stdin")?;
            stdin
                .write_all(payload.as_bytes())
                .map_err(|x| x.to_string())?;
        }
        child.wait().map_err(|x| x.to_string())?;
        Ok(())
    };
    let mut expected = 0;
    for (file, payload) in [
        (
            ".claude/settings.json",
            r#"{"hook_event_name":"Stop","session_id":"s1","cwd":"/w","transcript_path":"/w/t.jsonl"}"#,
        ),
        (
            ".codex/hooks.json",
            r#"{"hook_event_name":"Stop","session_id":"s2","cwd":"/w","transcript_path":"/w/t.jsonl"}"#,
        ),
    ] {
        let doc = parse(&h.read(file)?)?;
        let Some(Value::String(command)) =
            at(&doc, &["hooks", "Stop", "0", "hooks", "0", "command"])
        else {
            return Err(format!("no Stop command in {file}"));
        };
        run_cmd(command, payload)?;
        expected += 1;
        assert_eq!(count(), expected, "{file}");
    }
    let ts = h.read(FILES[2])?;
    let bin_line = ts
        .lines()
        .find_map(|l| l.strip_prefix("const BIN = "))
        .ok_or("no BIN")?;
    let bin: String =
        serde_json::from_str(bin_line.trim_end_matches(';')).map_err(|x| x.to_string())?;
    run_cmd(
        &format!("{} hook pi", memory::install::merge::quote(&bin)),
        r#"{"event":"agent_settled","session_id":"s3","session_file":"/w/p.jsonl","cwd":"/w"}"#,
    )?;
    assert_eq!(count(), expected + 1);
    Ok(())
}
