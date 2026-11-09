//! Hint lines: tolerant hook-input parsing, a strict owned line schema, a bounded builder
//! and a newline-resynchronising reader (DESIGN.md sections 7.6, 10.1 and 9.1).
//!
//! Everything here is pure. The shell reads stdin, supplies [`SanitizedLaunch`], appends the
//! built line with one `write(2)` and reads day files into memory. Two schemas are kept
//! apart on purpose: [`parse_hook_input`] is a tolerant view of untrusted agent JSON that
//! ignores unknown keys, while [`HintRecord`] is our own strict schema that rejects unknown
//! fields and any other `memory.hint/` version.
use crate::domain::{Agent, SessionId, Sha256};
use crate::privacy::SanitizedLaunch;
use crate::slice::{self, RangeError};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256 as Hasher};
use std::collections::BTreeMap;

/// Hard upper bound of one hint line, including its trailing newline.
pub const MAX_LINE_BYTES: usize = 4096;
/// Largest hook stdin payload the tolerant parser will look at.
pub const MAX_HOOK_INPUT_BYTES: usize = 1 << 20;
/// Longest accepted path coordinate, in bytes.
pub const MAX_PATH_BYTES: usize = 1024;
/// Current owned schema string.
pub const SCHEMA: &str = "memory.hint/1";
const SCHEMA_PREFIX: &str = "memory.hint/";
/// Every serialised record starts with this byte string (the schema is the first field).
const MARKER: &[u8] = br#"{"schema":"memory.hint/"#;

/// Why a hook payload produced no hint. Never carries any input text.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum HookInputError {
    /// Payload larger than [`MAX_HOOK_INPUT_BYTES`].
    #[error("hook input too large")]
    TooLarge,
    /// Payload is not JSON.
    #[error("hook input is not JSON")]
    NotJson,
    /// Payload is JSON but not an object.
    #[error("hook input is not an object")]
    NotObject,
    /// No event name key holding a string.
    #[error("hook input names no event")]
    MissingEvent,
    /// Event name unknown for this agent.
    #[error("hook event unknown for agent")]
    UnknownEvent,
    /// No session id key holding a string.
    #[error("hook input names no session")]
    MissingSession,
    /// Session id fails identifier validation.
    #[error("hook session id invalid")]
    InvalidSession,
}

/// Why a hint line could not be built. Never carries any input text.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum BuildError {
    /// Even the most reduced record exceeds the limit.
    #[error("hint line exceeds the size limit")]
    TooLarge,
    /// Serialisation failed.
    #[error("hint line could not be encoded")]
    Encode,
}

/// A path coordinate taken from untrusted hook input. Only a hint: the daemon uses it
/// only if `locate` accepts it. Absolute, at most [`MAX_PATH_BYTES`], no control characters.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(try_from = "String", into = "String")]
pub struct HintPath(String);

/// Path coordinate failed validation.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
#[error("invalid path coordinate")]
pub struct HintPathError;

impl TryFrom<String> for HintPath {
    type Error = HintPathError;
    fn try_from(value: String) -> Result<Self, Self::Error> {
        if value.starts_with('/')
            && value.len() <= MAX_PATH_BYTES
            && !value.chars().any(char::is_control)
        {
            Ok(Self(value))
        } else {
            Err(HintPathError)
        }
    }
}
impl From<HintPath> for String {
    fn from(value: HintPath) -> Self {
        value.0
    }
}
impl HintPath {
    /// Validated spelling.
    pub fn as_str(&self) -> &str {
        &self.0
    }
}
impl JsonSchema for HintPath {
    fn schema_name() -> std::borrow::Cow<'static, str> {
        "HintPath".into()
    }
    fn json_schema(_generator: &mut schemars::SchemaGenerator) -> schemars::Schema {
        schemars::json_schema!({"type": "string", "minLength": 1, "maxLength": 1024, "pattern": "^/"})
    }
}

/// A path coordinate that may be absent (sum type instead of `Option`).
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(tag = "state", rename_all = "snake_case", deny_unknown_fields)]
pub enum PathHint {
    /// The hook did not name a usable path.
    Absent,
    /// A validated path coordinate.
    Present {
        /// The coordinate.
        path: HintPath,
    },
}

/// Closed set of hook events, normalised across agents.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum HintEvent {
    /// Session started (launch evidence).
    SessionStart,
    /// A turn ended (Claude/Codex `Stop`, Pi `agent_settled`); debounced.
    TurnEnd,
    /// A turn ended on an API error (Claude `StopFailure`).
    TurnFailed,
    /// A subagent stopped.
    SubagentStop,
    /// Before compaction rewrites the context.
    PreCompact,
    /// An agent-team task completed.
    TaskCompleted,
    /// An agent-team teammate went idle.
    TeammateIdle,
    /// Session ended (Claude `SessionEnd`, Codex `session_end`, Pi `session_shutdown`).
    SessionEnd,
    /// The user interrupted a turn (Codex `Interrupt`). The session may continue.
    Interrupted,
}
impl HintEvent {
    /// Whether the coalescer flushes at once (section 7.1). `SessionEnd`: no more bytes follow.
    /// `Interrupted`: no `Stop` follows an interrupted turn, and the installer caps its hook
    /// like `SessionEnd`, so waiting out the quiet window gains nothing.
    pub const fn flushes(self) -> bool {
        matches!(self, Self::SessionEnd | Self::Interrupted)
    }
}

/// Validated view of one hook payload; every field is a validated type.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HookInput {
    /// Agent that fired the hook.
    pub agent: Agent,
    /// Normalised event.
    pub event: HintEvent,
    /// Session id.
    pub session: SessionId,
    /// Working directory hint.
    pub cwd: PathHint,
    /// Transcript path hint.
    pub transcript: PathHint,
    /// Subagent transcript hint (`agent_transcript_path`, Claude `SubagentStop`).
    pub agent_transcript: PathHint,
}

fn path_hint(value: Option<&serde_json::Value>) -> PathHint {
    value
        .and_then(serde_json::Value::as_str)
        .and_then(|text| HintPath::try_from(text.to_owned()).ok())
        .map_or(PathHint::Absent, |path| PathHint::Present { path })
}

fn event_kind(agent: &Agent, name: &str) -> Option<HintEvent> {
    if name.len() > 64 {
        return None;
    }
    let key: String = name
        .chars()
        .filter(|c| *c != '_')
        .map(|c| c.to_ascii_lowercase())
        .collect();
    let key = key.as_str();
    match agent {
        Agent::Claude => match key {
            "sessionstart" => Some(HintEvent::SessionStart),
            "stop" => Some(HintEvent::TurnEnd),
            "stopfailure" => Some(HintEvent::TurnFailed),
            "subagentstop" => Some(HintEvent::SubagentStop),
            "precompact" => Some(HintEvent::PreCompact),
            "taskcompleted" => Some(HintEvent::TaskCompleted),
            "teammateidle" => Some(HintEvent::TeammateIdle),
            "sessionend" => Some(HintEvent::SessionEnd),
            _ => None,
        },
        Agent::Codex => match key {
            "sessionstart" => Some(HintEvent::SessionStart),
            "stop" => Some(HintEvent::TurnEnd),
            "subagentstop" => Some(HintEvent::SubagentStop),
            "precompact" => Some(HintEvent::PreCompact),
            "interrupt" => Some(HintEvent::Interrupted),
            "sessionend" => Some(HintEvent::SessionEnd),
            _ => None,
        },
        Agent::Pi => match key {
            "sessionstart" => Some(HintEvent::SessionStart),
            "agentsettled" => Some(HintEvent::TurnEnd),
            "sessionshutdown" => Some(HintEvent::SessionEnd),
            _ => None,
        },
    }
}

/// Parse a hook's stdin by key, ignoring unknown keys (the payload is untrusted and
/// agent-owned). Reads only the event name (`hook_event_name` or `event`), `session_id`,
/// `cwd`, `transcript_path` (or Pi's `session_file`) and `agent_transcript_path`. Prompt text, tool input and every
/// other key are never read into the result. An unusable path degrades to
/// [`PathHint::Absent`] rather than losing the hint.
///
/// # Errors
/// A typed [`HookInputError`] that never echoes the input.
pub fn parse_hook_input(agent: &Agent, stdin: &[u8]) -> Result<HookInput, HookInputError> {
    if stdin.len() > MAX_HOOK_INPUT_BYTES {
        return Err(HookInputError::TooLarge);
    }
    let value: serde_json::Value =
        serde_json::from_slice(stdin).map_err(|_| HookInputError::NotJson)?;
    let object = value.as_object().ok_or(HookInputError::NotObject)?;
    let name = object
        .get("hook_event_name")
        .or_else(|| object.get("event"))
        .and_then(serde_json::Value::as_str)
        .ok_or(HookInputError::MissingEvent)?;
    let event = event_kind(agent, name).ok_or(HookInputError::UnknownEvent)?;
    let session = object
        .get("session_id")
        .and_then(serde_json::Value::as_str)
        .ok_or(HookInputError::MissingSession)?;
    let session =
        SessionId::try_from(session.to_owned()).map_err(|_| HookInputError::InvalidSession)?;
    Ok(HookInput {
        agent: agent.clone(),
        event,
        session,
        cwd: path_hint(object.get("cwd")),
        transcript: path_hint(
            object
                .get("transcript_path")
                .or_else(|| object.get("session_file")),
        ),
        agent_transcript: path_hint(object.get("agent_transcript_path")),
    })
}

/// Sanitized launch evidence as stored in a hint. Constructible only from
/// [`SanitizedLaunch`]; deserialisation of our own files is digest-checked by the reader.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct LaunchRecord {
    digest: Sha256,
    argv: Vec<String>,
    env: BTreeMap<String, String>,
}

fn put(hasher: &mut Hasher, bytes: &[u8]) {
    hasher.update(u64::try_from(bytes.len()).unwrap_or(u64::MAX).to_be_bytes());
    hasher.update(bytes);
}

fn launch_digest(argv: &[String], env: &BTreeMap<String, String>) -> Sha256 {
    let mut hasher = Hasher::new();
    hasher.update(b"memory.launch/1\0argv");
    for arg in argv {
        put(&mut hasher, arg.as_bytes());
    }
    hasher.update(b"env");
    for (key, value) in env {
        put(&mut hasher, key.as_bytes());
        put(&mut hasher, value.as_bytes());
    }
    Sha256::from_bytes(hasher.finalize().into())
}

impl LaunchRecord {
    /// Build from sanitized evidence only; the digest covers argv and env.
    pub fn from_sanitized(launch: &SanitizedLaunch) -> Self {
        let argv = launch.argv().to_vec();
        let env = launch.env().clone();
        Self {
            digest: launch_digest(&argv, &env),
            argv,
            env,
        }
    }
    /// Digest used to deduplicate launch records on the owner's manifest.
    pub fn digest(&self) -> Sha256 {
        self.digest
    }
    /// Allowlisted argv.
    pub fn argv(&self) -> &[String] {
        &self.argv
    }
    /// Allowlisted environment.
    pub fn env(&self) -> &BTreeMap<String, String> {
        &self.env
    }
    /// Whether the stored digest matches the stored content.
    pub fn digest_matches(&self) -> bool {
        launch_digest(&self.argv, &self.env) == self.digest
    }
}

/// Launch evidence carried by a hint line.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(tag = "state", rename_all = "snake_case", deny_unknown_fields)]
pub enum LaunchHint {
    /// The hook carried no launch evidence.
    None,
    /// Content dropped to fit the size bound; the digest still names it.
    DigestOnly {
        /// Digest of the dropped launch record.
        digest: Sha256,
    },
    /// Full sanitized launch evidence.
    Full(LaunchRecord),
}

/// What the builder dropped to respect the size bound.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum Reduction {
    /// Nothing dropped.
    None,
    /// Launch argv/env dropped, digest kept.
    LaunchDropped,
    /// Launch argv/env and all path hints dropped.
    PathsDropped,
}

/// The only schema tag we write; any other value is not this schema.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
pub enum HintSchema {
    /// `memory.hint/1`.
    #[serde(rename = "memory.hint/1")]
    V1,
}

/// One owned hint line. Strict: unknown fields and other versions are rejected.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct HintRecord {
    /// Schema tag; keep this the first field so [`scan`] can resynchronise.
    pub schema: HintSchema,
    /// Agent.
    pub agent: Agent,
    /// Event.
    pub event: HintEvent,
    /// Session.
    pub session: SessionId,
    /// Working directory hint.
    pub cwd: PathHint,
    /// Transcript path hint.
    pub transcript: PathHint,
    /// Subagent transcript path hint.
    pub agent_transcript: PathHint,
    /// Launch evidence.
    pub launch: LaunchHint,
    /// What was dropped to fit the size bound.
    pub reduction: Reduction,
}

fn encode(record: &HintRecord) -> Result<Vec<u8>, BuildError> {
    let mut line = serde_json::to_vec(record).map_err(|_| BuildError::Encode)?;
    line.push(b'\n');
    Ok(line)
}

/// Build one hint line of at most [`MAX_LINE_BYTES`], newline-terminated.
///
/// # Errors
/// See [`build_line_with_limit`].
pub fn build_line(
    input: &HookInput,
    launch: Option<&SanitizedLaunch>,
) -> Result<Vec<u8>, BuildError> {
    build_line_with_limit(input, launch, MAX_LINE_BYTES)
}

/// Build one line within `limit` bytes. Deterministic reduction order: full record; launch
/// argv/env dropped with its digest kept ([`Reduction::LaunchDropped`], only when there is
/// a launch); path hints dropped as well ([`Reduction::PathsDropped`]). No raw value is
/// ever emitted: launch content comes from [`SanitizedLaunch`] and nothing else is read.
///
/// # Errors
/// [`BuildError::TooLarge`] if even the most reduced record exceeds `limit`.
pub fn build_line_with_limit(
    input: &HookInput,
    launch: Option<&SanitizedLaunch>,
    limit: usize,
) -> Result<Vec<u8>, BuildError> {
    let record = launch.map(LaunchRecord::from_sanitized);
    let make = |reduction, paths: bool, launch| HintRecord {
        schema: HintSchema::V1,
        agent: input.agent.clone(),
        event: input.event,
        session: input.session.clone(),
        cwd: if paths {
            input.cwd.clone()
        } else {
            PathHint::Absent
        },
        transcript: if paths {
            input.transcript.clone()
        } else {
            PathHint::Absent
        },
        agent_transcript: if paths {
            input.agent_transcript.clone()
        } else {
            PathHint::Absent
        },
        launch,
        reduction,
    };
    let mut attempts = Vec::with_capacity(3);
    match record {
        Some(full) => {
            let digest = full.digest();
            attempts.push(make(Reduction::None, true, LaunchHint::Full(full)));
            attempts.push(make(
                Reduction::LaunchDropped,
                true,
                LaunchHint::DigestOnly { digest },
            ));
            attempts.push(make(
                Reduction::PathsDropped,
                false,
                LaunchHint::DigestOnly { digest },
            ));
        }
        None => {
            attempts.push(make(Reduction::None, true, LaunchHint::None));
            attempts.push(make(Reduction::PathsDropped, false, LaunchHint::None));
        }
    }
    for attempt in &attempts {
        let line = encode(attempt)?;
        if line.len() <= limit {
            return Ok(line);
        }
    }
    Err(BuildError::TooLarge)
}

/// A recovered record and the absolute offset just past its newline.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ScannedRecord {
    /// Offset after the record's terminating newline.
    pub end: u64,
    /// The record.
    pub record: HintRecord,
}

/// Result of scanning a day file from an offset.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HintScan {
    /// Valid records, in file order.
    pub records: Vec<ScannedRecord>,
    /// Complete lines (or torn prefixes inside them) that were dropped as garbage.
    pub dropped: usize,
    /// Own-prefix lines with another schema version, rejected.
    pub rejected_version: usize,
    /// Offset after the last complete line; persist this and resume from it.
    pub next_offset: u64,
    /// Bytes after `next_offset` forming an incomplete trailing line that must wait for its
    /// newline. Not dropped, not parsed.
    pub pending_bytes: usize,
}

enum Reject {
    Version,
    Malformed,
}

#[derive(Deserialize)]
struct SchemaPeek {
    schema: String,
}

fn classify(bytes: &[u8]) -> Result<HintRecord, Reject> {
    match serde_json::from_slice::<HintRecord>(bytes) {
        Ok(record) => {
            let sound = match &record.launch {
                LaunchHint::Full(launch) => launch.digest_matches(),
                LaunchHint::None | LaunchHint::DigestOnly { .. } => true,
            };
            if sound {
                Ok(record)
            } else {
                Err(Reject::Malformed)
            }
        }
        Err(_) => match serde_json::from_slice::<SchemaPeek>(bytes) {
            Ok(peek) if peek.schema.starts_with(SCHEMA_PREFIX) && peek.schema != SCHEMA => {
                Err(Reject::Version)
            }
            _ => Err(Reject::Malformed),
        },
    }
}

fn scan_line(line: &[u8], end: u64, out: &mut HintScan) {
    if line.is_empty() {
        return;
    }
    // A record is at most MAX_LINE_BYTES - 1 bytes before its newline, so it can only start
    // in the final window of the line. A torn fragment glued in front of it is dropped.
    let floor = line.len().saturating_sub(MAX_LINE_BYTES - 1);
    let mut starts = Vec::new();
    if line.len() < MAX_LINE_BYTES {
        starts.push(0);
    }
    starts.extend(
        line.windows(MARKER.len())
            .enumerate()
            .filter(|(at, window)| *at > 0 && *at >= floor && *window == MARKER)
            .map(|(at, _)| at),
    );
    for start in starts {
        let Ok(candidate) = slice::bytes(line, start..line.len()) else {
            continue;
        };
        match classify(candidate) {
            Ok(record) => {
                out.dropped += usize::from(start > 0);
                out.records.push(ScannedRecord { end, record });
                return;
            }
            Err(Reject::Version) => {
                out.dropped += usize::from(start > 0);
                out.rejected_version += 1;
                return;
            }
            Err(Reject::Malformed) => {}
        }
    }
    out.dropped += 1;
}

/// Scan a day file's bytes from `start`. Resynchronises on newline; never panics.
///
/// # Errors
/// [`RangeError`] if `start` lies beyond the buffer.
pub fn scan(buffer: &[u8], start: u64) -> Result<HintScan, RangeError> {
    let from = usize::try_from(start).map_err(|_| RangeError)?;
    let rest = slice::bytes(buffer, from..buffer.len())?;
    let mut out = HintScan {
        records: Vec::new(),
        dropped: 0,
        rejected_version: 0,
        next_offset: start,
        pending_bytes: 0,
    };
    for segment in rest.split_inclusive(|byte| *byte == b'\n') {
        match segment.strip_suffix(b"\n") {
            Some(line) => {
                out.next_offset = out
                    .next_offset
                    .saturating_add(u64::try_from(segment.len()).unwrap_or(u64::MAX));
                scan_line(line, out.next_offset, &mut out);
            }
            None => out.pending_bytes = segment.len(),
        }
    }
    Ok(out)
}
