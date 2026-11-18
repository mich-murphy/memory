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
