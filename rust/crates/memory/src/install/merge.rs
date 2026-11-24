//! Merging our hook handlers into a Claude or Codex settings document, after the
//! former Go installer's `replaceOurs`/`mergeHooks` rules. Ours is replaced in place, foreign
//! handlers keep their group and handler positions (Codex trust keys hold them), emptied groups
//! are dropped and an event with no group is deleted.
use super::Failure;
use super::json::{Object, Value};

/// Which agent a settings file belongs to.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Agent {
    /// `settings.json` of Claude Code.
    Claude,
    /// `hooks.json` of Codex.
    Codex,
}

impl Agent {
    /// The word `memory hook` takes.
    #[must_use]
    pub fn word(self) -> &'static str {
        match self {
            Self::Claude => "claude",
            Self::Codex => "codex",
        }
    }

    /// The events this agent's handlers are written for.
    #[must_use]
    pub fn events(self) -> &'static [HookEvent] {
        match self {
            Self::Claude => CLAUDE_EVENTS,
            Self::Codex => CODEX_EVENTS,
        }
    }
}

/// One event and how its handler runs.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct HookEvent {
    /// Event name as the agent spells it.
    pub name: &'static str,
    /// Written as `"async": true`.
    pub is_async: bool,
    /// Seconds.
    pub timeout: u64,
}

const fn ev(name: &'static str, is_async: bool, timeout: u64) -> HookEvent {
    HookEvent {
        name,
        is_async,
        timeout,
    }
}

/// Claude events (`docs/hooks.md`).
pub const CLAUDE_EVENTS: &[HookEvent] = &[
    ev("SessionStart", true, 30),
    ev("Stop", true, 30),
    ev("StopFailure", true, 30),
    ev("SubagentStop", true, 30),
    ev("PreCompact", true, 30),
    ev("TaskCompleted", true, 30),
    ev("TeammateIdle", true, 30),
    ev("SessionEnd", false, 5),
];

/// Codex events.
pub const CODEX_EVENTS: &[HookEvent] = &[
    ev("SessionStart", true, 30),
    ev("Stop", true, 30),
    ev("SubagentStop", true, 30),
    ev("PreCompact", true, 30),
    ev("Interrupt", true, 3),
    ev("SessionEnd", false, 3),
];

fn safe(text: &str) -> bool {
    !text.is_empty()
        && text
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"@%+=:,./_-".contains(&b))
}

/// POSIX-quote a path: unquoted when it only has `[A-Za-z0-9@%+=:,./_-]`, else single quotes
/// with `'"'"'` for each quote.
#[must_use]
pub fn quote(path: &str) -> String {
    if safe(path) {
        path.to_owned()
    } else {
        format!("'{}'", path.replace('\'', "'\"'\"'"))
    }
}

/// The command we write: `<q> hook <agent>`.
#[must_use]
pub fn hook_command(exe: &str, agent: Agent) -> String {
    format!("{} hook {}", quote(exe), agent.word())
}

/// Undo [`quote`]; `None` unless `text` is exactly what `quote` produces for some path.
fn unquote(text: &str) -> Option<String> {
    let path = if safe(text) {
        text.to_owned()
    } else {
        text.strip_prefix('\'')?
            .strip_suffix('\'')?
            .replace("'\"'\"'", "'")
    };
    (quote(&path) == text).then_some(path)
}

/// Whether `command` is exactly `<q> hook <agent>` with `<q>` an absolute path whose last
/// component is `memory`.
#[must_use]
pub fn is_ours(command: &str, agent: Agent) -> bool {
    let suffix = format!(" hook {}", agent.word());
    let Some(quoted) = command.strip_suffix(&suffix) else {
        return false;
    };
    let Some(path) = unquote(quoted) else {
        return false;
    };
    let path = std::path::Path::new(&path);
    !path.as_os_str().as_encoded_bytes().ends_with(b"/")
        && path.is_absolute()
        && path.file_name().is_some_and(|name| name == "memory")
}

fn get<'a>(object: &'a Object, key: &str) -> Option<&'a Value> {
    object.iter().find(|(k, _)| k == key).map(|(_, v)| v)
}

fn set(object: &mut Object, key: &str, value: Value) {
    if let Some(slot) = object.iter_mut().find(|(k, _)| k == key) {
        slot.1 = value;
    } else {
        object.push((key.to_owned(), value));
    }
}

fn command_of(handler: &Value, agent: Agent) -> bool {
    let Value::Object(members) = handler else {
        return false;
    };
    matches!(get(members, "command"), Some(Value::String(c)) if is_ours(c, agent))
}

fn handler(event: &HookEvent, command: &str) -> Value {
    let mut members: Object = vec![
        ("type".to_owned(), Value::String("command".to_owned())),
        ("command".to_owned(), Value::String(command.to_owned())),
    ];
    if event.is_async {
        members.push(("async".to_owned(), Value::Bool(true)));
    }
    members.push((
        "timeout".to_owned(),
        Value::Number(event.timeout.to_string()),
    ));
    Value::Object(members)
}

fn handlers_of(group: &Value) -> Vec<Value> {
    match group {
        Value::Object(members) => match get(members, "hooks") {
            Some(Value::Array(items)) => items.clone(),
            _ => Vec::new(),
        },
        _ => Vec::new(),
    }
}

/// Put `ours` in place of the first of our handlers and drop the rest of ours.
fn replace_ours(groups: Vec<Value>, ours: Option<&Value>, agent: Agent) -> (Vec<Value>, bool) {
    let mut kept = Vec::new();
    let mut replaced = false;
    for group in groups {
        let handlers = handlers_of(&group);
        let mut others = Vec::new();
        for h in &handlers {
            if !command_of(h, agent) {
                others.push(h.clone());
            } else if let (Some(ours), false) = (ours, replaced) {
                others.push(ours.clone());
                replaced = true;
            }
        }
        if handlers.is_empty() {
            kept.push(group);
        } else if !others.is_empty() {
            let mut group = group;
            if let Value::Object(members) = &mut group {
                set(members, "hooks", Value::Array(others));
            }
            kept.push(group);
        }
    }
    (kept, replaced)
}

/// Merge our handlers (`command` given) or remove them (`None`) in `doc["hooks"]`.
///
/// # Errors
/// [`Failure::Unparsable`] when `hooks`, or the value of one of our events, is not the shape the
/// merge can keep intact; Go would drop such a value, and we leave the file alone instead.
pub fn merge_hooks(doc: &mut Object, agent: Agent, command: Option<&str>) -> Result<(), Failure> {
    let events = agent.events();
    let old: Object = match get(doc, "hooks") {
        None => Vec::new(),
        Some(Value::Object(members)) => members.clone(),
        Some(_) => return Err(Failure::Unparsable),
    };
    let mut ours: Vec<(&'static str, Value)> = command
        .map(|c| events.iter().map(|e| (e.name, handler(e, c))).collect())
        .unwrap_or_default();
    let mut hooks: Object = Vec::new();
    for (event, value) in old {
        match value {
            Value::Array(groups) => {
                let mine = ours.iter().position(|(n, _)| *n == event);
                let (kept, replaced) = replace_ours(
                    groups,
                    mine.and_then(|i| ours.get(i)).map(|(_, h)| h),
                    agent,
                );
                if let (true, Some(i)) = (replaced, mine) {
                    ours.remove(i);
                }
                if !kept.is_empty() {
                    hooks.push((event, Value::Array(kept)));
                }
            }
            other => {
                if events.iter().any(|e| e.name == event) {
                    return Err(Failure::Unparsable);
                }
                hooks.push((event, other));
            }
        }
    }
    for e in events {
        let Some(i) = ours.iter().position(|(n, _)| *n == e.name) else {
            continue;
        };
        let (_, h) = ours.remove(i);
        let group = Value::Object(vec![("hooks".to_owned(), Value::Array(vec![h]))]);
        match hooks.iter_mut().find(|(k, _)| k == e.name) {
            Some((_, Value::Array(groups))) => groups.push(group),
            _ => hooks.push((e.name.to_owned(), Value::Array(vec![group]))),
        }
    }
    if hooks.is_empty() {
        doc.retain(|(k, _)| k != "hooks");
    } else {
        set(doc, "hooks", Value::Object(hooks));
    }
    Ok(())
}

/// Codex's whole-document admission: only `description` (string or null) and `hooks` (an
/// object) at the top level.
///
/// # Errors
/// [`Failure::CodexSchema`].
pub fn install_codex_schema(doc: &Object) -> Result<(), Failure> {
    for (key, value) in doc {
        match (key.as_str(), value) {
            ("description", Value::String(_) | Value::Null) | ("hooks", Value::Object(_)) => {}
            _ => return Err(Failure::CodexSchema),
        }
    }
    Ok(())
}

/// Parse `text`, check it for `agent`, merge and write it back.
///
/// # Errors
/// [`Failure`] from the parse, the Codex check or the merge.
pub fn merge_text(text: &str, agent: Agent, command: Option<&str>) -> Result<String, Failure> {
    let Value::Object(mut doc) = super::json::parse(text).map_err(|_| Failure::Unparsable)? else {
        return Err(Failure::Unparsable);
    };
    if agent == Agent::Codex {
        install_codex_schema(&doc)?;
    }
    merge_hooks(&mut doc, agent, command)?;
    Ok(super::json::to_text(&Value::Object(doc)))
}
