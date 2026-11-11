//! Pure, total path locator (slice 1).
//!
//! `locate` maps a validated root-relative path plus explicit shell-supplied root facts to a
//! source kind, byte-stream class, owner and stable source id. It performs no I/O, reads no
//! clock and never guesses: an unknown layout is `UnknownKind`, a known layout whose owner
//! cannot be derived from the path or an unambiguous shell fact is `UnknownOwner`. Both are
//! withheld and counted by the caller, never uploaded.
//!
//! Identifiers are content-independent and derived only from the root namespace and the
//! path itself, so an unrelated sibling appearing elsewhere never changes a result.
use crate::domain::{
    Agent, ArchiveId, MachineId, Owner, SessionId, SessionKey, SourceClass, SourceId, SourceKind,
    WithholdReason,
};
use sha2::{Digest, Sha256 as Hasher};
use std::collections::BTreeMap;
use std::collections::btree_map::Entry;
use std::fmt::Write;

/// Rejected locator input; never carries the rejected text.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum InputError {
    /// Empty path.
    #[error("empty path")]
    Empty,
    /// Path starts with a separator.
    #[error("absolute path")]
    Absolute,
    /// Empty path component (`a//b`, trailing separator).
    #[error("empty component")]
    EmptyComponent,
    /// `.` or `..` component.
    #[error("traversal component")]
    Traversal,
    /// NUL byte in a component.
    #[error("nul byte")]
    Nul,
    /// Component over 255 bytes or path over 4096 bytes.
    #[error("path too long")]
    TooLong,
    /// Invalid root namespace.
    #[error("invalid namespace")]
    Namespace,
    /// Invalid session UUID.
    #[error("invalid uuid")]
    Uuid,
    /// Invalid project slug.
    #[error("invalid slug")]
    Slug,
}

const MAX_COMPONENT: usize = 255;
const MAX_PATH: usize = 4096;
const MAX_ID: usize = 1024;

fn validate_component(c: &str) -> Result<(), InputError> {
    if c.is_empty() {
        Err(InputError::EmptyComponent)
    } else if c == "." || c == ".." {
        Err(InputError::Traversal)
    } else if c.contains('\0') {
        Err(InputError::Nul)
    } else if c.len() > MAX_COMPONENT {
        Err(InputError::TooLong)
    } else {
        Ok(())
    }
}

/// A relative path below an admitted root: non-empty, no absolute prefix, no empty, `.` or
/// `..` component, no NUL. Separator is `/`.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct RootRelativePath(Vec<String>);

impl RootRelativePath {
    /// Validate and split a root-relative path.
    ///
    /// # Errors
    /// Returns the first violated rule; the input is never echoed.
    pub fn parse(raw: &str) -> Result<Self, InputError> {
        if raw.is_empty() {
            return Err(InputError::Empty);
        }
        if raw.starts_with('/') {
            return Err(InputError::Absolute);
        }
        if raw.len() > MAX_PATH {
            return Err(InputError::TooLong);
        }
        let mut parts = Vec::new();
        for c in raw.split('/') {
            validate_component(c)?;
            parts.push(c.to_owned());
        }
        Ok(Self(parts))
    }

    /// Validated components.
    pub fn components(&self) -> &[String] {
        &self.0
    }
}

/// Root identity used to qualify every identifier, so two roots may reuse names.
/// ASCII alphanumerics, `.` and `-`, 1 to 64 bytes (no `_` or `:`, which delimit ids).
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct RootNamespace(String);

impl RootNamespace {
    /// Validate a namespace.
    ///
    /// # Errors
    /// `Namespace` when empty, over 64 bytes or outside the allowed alphabet.
    pub fn new(value: &str) -> Result<Self, InputError> {
        if value.is_empty()
            || value.len() > 64
            || !value
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b == b'.' || b == b'-')
        {
            return Err(InputError::Namespace);
        }
        Ok(Self(value.to_owned()))
    }

    /// Validated spelling.
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

/// What a root directory is.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum RootLayout {
    /// An agent home directory (`~/.claude`, `~/.codex`, `~/.pi/agent`, or a Pi session root).
    AgentDir,
    /// The directory holding Claude's primary `.claude.json`; only that exact file maps.
    ClaudePrimaryHome,
    /// A learned custom Pi session root: only session layouts (flat `*.jsonl`, `--cwd--/`,
    /// `<timestamp>_<uuid>/…`) map. Config, settings and log rows never match.
    PiCustomRoot,
}

#[derive(Debug, Clone, PartialEq, Eq)]
enum Fact {
    Unique(String),
    Ambiguous,
}

/// Shell-supplied ownership facts for Claude per-session artifacts that live outside the
/// project directory (`file-history/<uuid>`, `todos/<uuid>-…`, …): UUID → project slug.
/// A UUID seen under two different slugs is ambiguous and stays `UnknownOwner`; ambiguity is
/// absorbing, so the result does not depend on insertion order.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct ClaudeSessionIndex(BTreeMap<String, Fact>);

impl ClaudeSessionIndex {
    /// Empty index.
    pub fn new() -> Self {
        Self::default()
    }

    /// Record that the transcript for `uuid` was seen under `project_slug`.
    ///
    /// # Errors
    /// `Uuid` or `Slug` when either argument is malformed.
    pub fn record(&mut self, uuid: &str, project_slug: &str) -> Result<(), InputError> {
        if !is_uuid(uuid) {
            return Err(InputError::Uuid);
        }
        validate_component(project_slug).map_err(|_| InputError::Slug)?;
        match self.0.entry(uuid.to_owned()) {
            Entry::Vacant(v) => {
                v.insert(Fact::Unique(project_slug.to_owned()));
            }
            Entry::Occupied(mut o) => {
                if o.get() != &Fact::Unique(project_slug.to_owned()) {
                    o.insert(Fact::Ambiguous);
                }
            }
        }
        Ok(())
    }

    fn lookup(&self, uuid: &str) -> Option<&str> {
        match self.0.get(uuid) {
            Some(Fact::Unique(slug)) => Some(slug.as_str()),
            _ => None,
        }
    }
}

/// Per-root identity and shell facts for one agent root.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Roots {
    archive: ArchiveId,
    machine: MachineId,
    agent: Agent,
    namespace: RootNamespace,
    layout: RootLayout,
    claude_sessions: ClaudeSessionIndex,
}

impl Roots {
    /// Describe one root.
    pub fn new(
        archive: ArchiveId,
        machine: MachineId,
        agent: Agent,
        namespace: RootNamespace,
        layout: RootLayout,
    ) -> Self {
        Self {
            archive,
            machine,
            agent,
            namespace,
            layout,
            claude_sessions: ClaudeSessionIndex::new(),
        }
    }

    /// Attach Claude UUID → slug ownership facts.
    #[must_use]
    pub fn with_claude_sessions(mut self, index: ClaudeSessionIndex) -> Self {
        self.claude_sessions = index;
        self
    }
}

/// Closed config asset subtypes. Which subtypes a kind admits is fixed by the matrix.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum ConfigAsset {
    /// `settings.json`.
    Settings,
    /// Claude `settings.local.json`.
    LocalSettings,
    /// `CLAUDE.md`, `AGENTS.md`, `AGENTS.override.md`, `SYSTEM.md`, `APPEND_SYSTEM.md`.
    Instruction,
    /// `keybindings.json`.
    Keybindings,
    /// Claude `launcher-settings.json`.
    LauncherSettings,
    /// Claude `remote-settings.json`.
    RemoteSettings,
    /// Claude `policy-limits.json`.
    PolicyLimits,
    /// Claude top-level `.sh/.py/.js/.ts` script.
    Script,
    /// `skills/**`.
    Skill,
    /// Claude `agents/**`.
    Agent,
    /// Claude `commands/**`.
    Command,
    /// `rules/**`.
    Rule,
    /// Claude `output-styles/**`.
    OutputStyle,
    /// Claude `hooks/**`.
    HookDir,
    /// Claude `plugins/installed_plugins.json`, Codex `plugins/**` (not `cache`).
    Plugin,
    /// Codex/Pi `prompts/**`.
    Prompt,
    /// Codex `config.toml`.
    Config,
    /// Codex `hooks.json`.
    HooksFile,
    /// Codex `requirements.toml`.
    Requirements,
    /// Codex `managed_config.toml`.
    ManagedConfig,
    /// Pi `models.json`.
    Models,
    /// Pi `models-store.json`.
    ModelsStore,
    /// Pi `mcp.json`.
    Mcp,
    /// Pi `extensions/**`.
    Extension,
    /// Pi `themes/**`.
    Theme,
    /// Pi `tools/**`.
    Tool,
    /// Pi `experimental/**`.
    Experimental,
    /// Home-level Claude `.claude.json`.
    PrimaryConfig,
}

/// Role of a source within its kind, where the kind alone does not say.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Role {
    /// Main transcript.
    Main,
    /// Orphaned or superseded transcript set aside by the agent.
    Superseded,
    /// Child transcript (Claude subagent, Pi task/fork/run).
    Child,
    /// Prompt history.
    History,
    /// Codex `session_index.jsonl`.
    SessionIndex,
    /// Any other artifact; the kind identifies it.
    Artifact,
    /// Config asset subtype.
    Asset(ConfigAsset),
}

/// Result of locating one path. Never fails: unmapped paths are explicit and countable.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Located {
    /// Known layout with a derivable owner.
    Source {
        /// Manifest-chain owner.
        owner: Owner,
        /// Source family.
        kind: SourceKind,
        /// Byte-stream strategy for the family.
        class: SourceClass,
        /// Stable, root-qualified, content-independent id.
        id: SourceId,
        /// Role within the family.
        role: Role,
    },
    /// No pattern matches.
    UnknownKind,
    /// Known pattern, but no unambiguous owner.
    UnknownOwner,
}

impl Located {
    /// Fixed local reason when the path is withheld, `None` for a capturable source.
    pub fn withhold_reason(&self) -> Option<WithholdReason> {
        match self {
            Self::Source { .. } => None,
            Self::UnknownKind => Some(WithholdReason::UnknownKind),
            Self::UnknownOwner => Some(WithholdReason::UnknownOwner),
        }
    }
}

// ---- matching helpers -------------------------------------------------------------------

/// `d` = ASCII digit, `x` = hex digit, anything else literal.
fn shape(s: &str, template: &str) -> bool {
    s.len() == template.len()
        && s.bytes().zip(template.bytes()).all(|(c, p)| match p {
            b'd' => c.is_ascii_digit(),
            b'x' => c.is_ascii_hexdigit(),
            _ => c == p,
        })
}

fn is_uuid(s: &str) -> bool {
    shape(s, "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx")
}

/// Leading UUID followed by end of name or `.`, `-`, `_`.
fn uuid_prefix(s: &str) -> Option<&str> {
    let (u, rest) = s.split_at_checked(36)?;
    (is_uuid(u) && (rest.is_empty() || rest.starts_with(['.', '-', '_']))).then_some(u)
}

/// Claude session name: no `.`.
fn session_stem_ok(s: &str) -> bool {
    !s.is_empty() && !s.contains('.')
}

fn rollout_stem_ok(stem: &str) -> bool {
    let Some(rest) = stem.strip_prefix("rollout-") else {
        return false;
    };
    let Some((date, ids)) = rest.split_at_checked(20) else {
        return false;
    };
    if !shape(date, "dddd-dd-ddTdd-dd-dd-") {
        return false;
    }
    match ids.split_once('_') {
        Some((a, b)) => is_uuid(a) && is_uuid(b),
        None => is_uuid(ids),
    }
}

/// Pi session directory/stem: `<timestamp>_<uuid>`.
fn pi_stem_ok(s: &str) -> bool {
    let Some(cut) = s.len().checked_sub(37) else {
        return false;
    };
    let Some((head, tail)) = s.split_at_checked(cut) else {
        return false;
    };
    let Some(uuid) = tail.strip_prefix('_') else {
        return false;
    };
    let Some((date, body)) = head.split_at_checked(11) else {
        return false;
    };
    is_uuid(uuid)
        && shape(date, "dddd-dd-ddT")
        && !body.is_empty()
        && body
            .bytes()
            .all(|b| b.is_ascii_digit() || b":.Z+-".contains(&b))
}

fn pi_cwd(s: &str) -> bool {
    s.len() > 4 && s.starts_with("--") && s.ends_with("--")
}

fn pi_run_ok(s: &str) -> bool {
    s.strip_prefix("run-").is_some_and(|n| {
        n == "0" || (!n.is_empty() && !n.starts_with('0') && n.bytes().all(|b| b.is_ascii_digit()))
    })
}

fn dir_asset(dir: &str, table: &[(&str, ConfigAsset)]) -> Option<ConfigAsset> {
    table.iter().find(|(d, _)| *d == dir).map(|(_, a)| *a)
}

// ---- identifiers ------------------------------------------------------------------------

/// Byte-wise reversible escape: ASCII alphanumerics, `.` and `-` stay, every other byte
/// (including `_` and `:`) becomes `_xx`.
fn esc(part: &str, out: &mut String) {
    for b in part.bytes() {
        if b.is_ascii_alphanumeric() || b == b'.' || b == b'-' {
            out.push(char::from(b));
        } else {
            let _ = write!(out, "_{b:02x}");
        }
    }
}

/// `ns:part:part…`; over 1024 bytes it becomes `ns:h:<sha256>` of the NUL-delimited parts.
fn ident(ns: &RootNamespace, parts: &[&str]) -> String {
    let mut out = ns.as_str().to_owned();
    for p in parts {
        out.push(':');
        esc(p, &mut out);
    }
    if out.len() <= MAX_ID {
        return out;
    }
    let mut h = Hasher::new();
    for p in parts {
        h.update(p.as_bytes());
        h.update([0_u8]);
    }
    let digest: [u8; 32] = h.finalize().into();
    let mut hashed = format!("{}:h:", ns.as_str());
    for b in digest {
        let _ = write!(hashed, "{b:02x}");
    }
    hashed
}

// ---- matrix -----------------------------------------------------------------------------

struct Hit<'a> {
    kind: SourceKind,
    class: SourceClass,
    role: Role,
    owner: OwnerSpec<'a>,
    id: Vec<&'a str>,
}

enum OwnerSpec<'a> {
    Shared,
    Session(Vec<&'a str>),
}

enum Miss {
    Kind,
    Owner,
}

type Res<'a> = Result<Hit<'a>, Miss>;

fn shared<'a>(kind: SourceKind, class: SourceClass, role: Role, parts: &[&'a str]) -> Res<'a> {
    Ok(Hit {
        kind,
        class,
        role,
        owner: OwnerSpec::Shared,
        id: parts.to_vec(),
    })
}

fn asset<'a>(kind: SourceKind, a: ConfigAsset, parts: &[&'a str]) -> Res<'a> {
    shared(kind, SourceClass::Snapshot, Role::Asset(a), parts)
}

fn session<'a>(
    kind: SourceKind,
    class: SourceClass,
    role: Role,
    owner: Vec<&'a str>,
    parts: &[&'a str],
) -> Res<'a> {
    Ok(Hit {
        kind,
        class,
        role,
        owner: OwnerSpec::Session(owner),
        id: parts.to_vec(),
    })
}

const CLAUDE_FILES: [(&str, ConfigAsset); 7] = [
    ("settings.json", ConfigAsset::Settings),
    ("settings.local.json", ConfigAsset::LocalSettings),
    ("CLAUDE.md", ConfigAsset::Instruction),
    ("keybindings.json", ConfigAsset::Keybindings),
    ("launcher-settings.json", ConfigAsset::LauncherSettings),
    ("remote-settings.json", ConfigAsset::RemoteSettings),
    ("policy-limits.json", ConfigAsset::PolicyLimits),
];
const CLAUDE_DIRS: [(&str, ConfigAsset); 6] = [
    ("rules", ConfigAsset::Rule),
    ("skills", ConfigAsset::Skill),
    ("agents", ConfigAsset::Agent),
    ("commands", ConfigAsset::Command),
    ("output-styles", ConfigAsset::OutputStyle),
    ("hooks", ConfigAsset::HookDir),
];
const CODEX_FILES: [(&str, ConfigAsset); 6] = [
    ("config.toml", ConfigAsset::Config),
    ("hooks.json", ConfigAsset::HooksFile),
    ("AGENTS.md", ConfigAsset::Instruction),
    ("AGENTS.override.md", ConfigAsset::Instruction),
    ("requirements.toml", ConfigAsset::Requirements),
    ("managed_config.toml", ConfigAsset::ManagedConfig),
];
const CODEX_DIRS: [(&str, ConfigAsset); 3] = [
    ("rules", ConfigAsset::Rule),
    ("skills", ConfigAsset::Skill),
    ("prompts", ConfigAsset::Prompt),
];
const PI_FILES: [(&str, ConfigAsset); 8] = [
    ("models-store.json", ConfigAsset::ModelsStore),
    ("mcp.json", ConfigAsset::Mcp),
    ("keybindings.json", ConfigAsset::Keybindings),
    ("AGENTS.md", ConfigAsset::Instruction),
    ("AGENTS.override.md", ConfigAsset::Instruction),
    ("CLAUDE.md", ConfigAsset::Instruction),
    ("SYSTEM.md", ConfigAsset::Instruction),
    ("APPEND_SYSTEM.md", ConfigAsset::Instruction),
];
const PI_DIRS: [(&str, ConfigAsset); 6] = [
    ("extensions", ConfigAsset::Extension),
    ("prompts", ConfigAsset::Prompt),
    ("skills", ConfigAsset::Skill),
    ("themes", ConfigAsset::Theme),
    ("tools", ConfigAsset::Tool),
    ("experimental", ConfigAsset::Experimental),
];

fn claude<'a>(parts: &[&'a str], roots: &'a Roots) -> Res<'a> {
    use SourceClass::{Append, Snapshot};
    if let [dir, _, ..] = parts
        && let Some(a) = dir_asset(dir, &CLAUDE_DIRS)
    {
        return asset(SourceKind::ClaudeConfig, a, parts);
    }
    let ext = |kind, class, sub: &'a str| claude_external(kind, class, sub, parts, roots);
    match parts {
        [name] => claude_top(name, parts),
        ["projects", slug, rest @ ..] => claude_project(slug, rest, parts),
        ["file-history", sub, ..] => ext(SourceKind::ClaudeFileHistory, Snapshot, sub),
        ["session-env", sub, ..] => ext(SourceKind::ClaudeSessionEnv, Snapshot, sub),
        ["debug", sub, ..] => ext(SourceKind::ClaudeDebug, Append, sub),
        ["todos", sub, ..] => ext(SourceKind::ClaudeTodos, Snapshot, sub),
        ["tasks", sub, ..] => ext(SourceKind::ClaudeTasks, Snapshot, sub),
        ["teams", _, ..] => shared(SourceKind::ClaudeTeams, Snapshot, Role::Artifact, parts),
        ["plans", _, ..] => shared(SourceKind::ClaudePlan, Snapshot, Role::Artifact, parts),
        ["paste-cache", _, ..] => shared(SourceKind::ClaudePaste, Snapshot, Role::Artifact, parts),
        ["usage-data", _, ..] => shared(SourceKind::ClaudeUsage, Snapshot, Role::Artifact, parts),
        ["agent-memory", _, ..] => {
            shared(SourceKind::ClaudeMemory, Snapshot, Role::Artifact, parts)
        }
        ["plugins", "installed_plugins.json"] => {
            asset(SourceKind::ClaudeConfig, ConfigAsset::Plugin, parts)
        }
        _ => Err(Miss::Kind),
    }
}

fn claude_top<'a>(name: &str, parts: &[&'a str]) -> Res<'a> {
    if name == "history.jsonl" {
        return shared(
            SourceKind::ClaudeHistory,
            SourceClass::Append,
            Role::History,
            parts,
        );
    }
    if let Some(a) = dir_asset(name, &CLAUDE_FILES) {
        return asset(SourceKind::ClaudeConfig, a, parts);
    }
    if [".sh", ".py", ".js", ".ts"]
        .iter()
        .any(|s| name.ends_with(s))
    {
        return asset(SourceKind::ClaudeConfig, ConfigAsset::Script, parts);
    }
    Err(Miss::Kind)
}

fn claude_external<'a>(
    kind: SourceKind,
    class: SourceClass,
    sub: &'a str,
    parts: &[&'a str],
    roots: &'a Roots,
) -> Res<'a> {
    let uuid = uuid_prefix(sub).ok_or(Miss::Owner)?;
    let slug = roots.claude_sessions.lookup(uuid).ok_or(Miss::Owner)?;
    session(kind, class, Role::Artifact, vec![slug, uuid], parts)
}

fn claude_project<'a>(slug: &'a str, rest: &[&'a str], parts: &[&'a str]) -> Res<'a> {
    use SourceClass::{Append, Snapshot};
    match rest {
        [name] => claude_transcript(slug, name, parts),
        ["memory", _, ..] => shared(SourceKind::ClaudeMemory, Snapshot, Role::Artifact, parts),
        [sid, tail @ ..] if session_stem_ok(sid) && !tail.is_empty() => {
            let owner = vec![slug, *sid];
            match tail {
                ["subagents", .., last] if last.ends_with(".jsonl") => session(
                    SourceKind::ClaudeSubagent,
                    Append,
                    Role::Child,
                    owner,
                    parts,
                ),
                ["tool-results", _, ..] => session(
                    SourceKind::ClaudeToolResults,
                    Append,
                    Role::Artifact,
                    owner,
                    parts,
                ),
                _ => session(
                    SourceKind::ClaudeAux,
                    Snapshot,
                    Role::Artifact,
                    owner,
                    parts,
                ),
            }
        }
        _ => Err(Miss::Kind),
    }
}

fn claude_transcript<'a>(slug: &'a str, name: &'a str, parts: &[&'a str]) -> Res<'a> {
    let transcript = |stem, role| {
        session(
            SourceKind::ClaudeTranscript,
            SourceClass::Append,
            role,
            vec![slug, stem],
            parts,
        )
    };
    if let Some(stem) = name.strip_suffix(".jsonl")
        && session_stem_ok(stem)
    {
        return transcript(stem, Role::Main);
    }
    let Some((stem, tail)) = name.split_once('.') else {
        return Err(Miss::Kind);
    };
    let set_aside = tail
        .strip_prefix("orphaned-")
        .and_then(|t| t.strip_suffix(".jsonl"))
        .or_else(|| tail.strip_prefix("jsonl.superseded-"));
    if !stem.is_empty() && set_aside.is_some_and(|t| !t.is_empty()) {
        return transcript(stem, Role::Superseded);
    }
    Err(Miss::Kind)
}

fn codex<'a>(parts: &[&'a str]) -> Res<'a> {
    use SourceClass::{Append, Snapshot};
    if let [dir, _, ..] = parts
        && let Some(a) = dir_asset(dir, &CODEX_DIRS)
    {
        return asset(SourceKind::CodexConfig, a, parts);
    }
    match parts {
        [name] => codex_top(name, parts),
        ["sessions" | "archived_sessions", .., name] => codex_rollout(name),
        ["plugins", sub, ..] if *sub != "cache" => {
            asset(SourceKind::CodexConfig, ConfigAsset::Plugin, parts)
        }
        ["memories", _, ..] => shared(SourceKind::CodexMemory, Snapshot, Role::Artifact, parts),
        ["log", _, ..] => shared(SourceKind::CodexLog, Append, Role::Artifact, parts),
        _ => Err(Miss::Kind),
    }
}

fn codex_top<'a>(name: &str, parts: &[&'a str]) -> Res<'a> {
    let history = |role| shared(SourceKind::CodexHistory, SourceClass::Append, role, parts);
    match name {
        "history.jsonl" => return history(Role::History),
        "session_index.jsonl" => return history(Role::SessionIndex),
        _ => {}
    }
    if let Some(a) = dir_asset(name, &CODEX_FILES) {
        return asset(SourceKind::CodexConfig, a, parts);
    }
    let lower = name.to_ascii_lowercase();
    if [".sqlite", ".sqlite3", ".db"]
        .iter()
        .any(|s| lower.ends_with(s))
    {
        return shared(
            SourceKind::CodexStore,
            SourceClass::Snapshot,
            Role::Artifact,
            parts,
        );
    }
    Err(Miss::Kind)
}

/// The logical rollout identity drops date folders, archive location and `.zst`.
fn codex_rollout<'a>(name: &'a str) -> Res<'a> {
    let (plain, zst) = match name.strip_suffix(".zst") {
        Some(p) => (p, true),
        None => (name, false),
    };
    let stem = plain.strip_suffix(".jsonl").ok_or(Miss::Kind)?;
    if !rollout_stem_ok(stem) {
        return Err(Miss::Kind);
    }
    let kind = if zst {
        SourceKind::CodexCompressed
    } else {
        SourceKind::CodexRollout
    };
    Ok(Hit {
        kind,
        class: SourceClass::Append,
        role: Role::Main,
        owner: OwnerSpec::Session(vec![stem]),
        id: vec!["rollouts", plain],
    })
}

fn pi<'a>(parts: &[&'a str]) -> Res<'a> {
    if let Some(hit) = pi_asset(parts) {
        return hit;
    }
    match parts {
        ["sessions", rest @ ..] => pi_session(rest, PiMode::Sessions, parts),
        _ => pi_session(parts, PiMode::Strict, parts),
    }
}

fn pi_asset<'a>(parts: &[&'a str]) -> Option<Res<'a>> {
    use SourceKind::{PiConfig, PiLog, PiModels, PiSettings};
    match parts {
        ["settings.json"] => Some(asset(PiSettings, ConfigAsset::Settings, parts)),
        ["models.json"] => Some(asset(PiModels, ConfigAsset::Models, parts)),
        [name] => {
            if let Some(a) = dir_asset(name, &PI_FILES) {
                return Some(asset(PiConfig, a, parts));
            }
            name.strip_suffix(".log")
                .filter(|s| !s.is_empty())
                .map(|_| shared(PiLog, SourceClass::Append, Role::Artifact, parts))
        }
        [dir, _, ..] => dir_asset(dir, &PI_DIRS).map(|a| asset(PiConfig, a, parts)),
        [] => None,
    }
}

/// Where a Pi path is interpreted (Go `classifyLayout`).
#[derive(Clone, Copy, PartialEq, Eq)]
enum PiMode {
    /// Agent dir, outside `sessions/`: only Pi's own `<timestamp>_<uuid>` names qualify.
    Strict,
    /// Agent dir, under `sessions/`: a single non-`.jsonl` file is Go `RoleUnknown`; a
    /// multi-part path outside any session dir is sessionless aux.
    Sessions,
    /// Custom root (Go `agentDir=false`): every non-transcript file is sessionless aux.
    Custom,
}

fn pi_session<'a>(rest: &[&'a str], mode: PiMode, parts: &[&'a str]) -> Res<'a> {
    let strict = mode == PiMode::Strict;
    // Go retains these as sessionless aux: no session owner is derivable from the path.
    let sessionless = || {
        shared(
            SourceKind::PiAux,
            SourceClass::Snapshot,
            Role::Artifact,
            parts,
        )
    };
    let (cwd, rest) = match rest {
        [c, tail @ ..] if pi_cwd(c) && !tail.is_empty() => (Some(*c), tail),
        _ => (None, rest),
    };
    let owner = |name: &'a str| cwd.into_iter().chain([name]).collect::<Vec<_>>();
    let child = |name: &'a str| {
        session(
            SourceKind::PiSession,
            SourceClass::Append,
            Role::Child,
            owner(name),
            parts,
        )
    };
    match rest {
        [name] => {
            let stem = name.strip_suffix(".jsonl").filter(|s| !s.is_empty());
            match stem {
                Some(stem) if !strict || pi_stem_ok(stem) => session(
                    SourceKind::PiSession,
                    SourceClass::Append,
                    Role::Main,
                    owner(stem),
                    parts,
                ),
                // Go: custom roots keep every non-transcript file as sessionless aux; a bare
                // `.jsonl` (Go RoleMain with no session key) is kept the same way.
                None if mode == PiMode::Custom
                    || (mode == PiMode::Sessions && *name == ".jsonl") =>
                {
                    sessionless()
                }
                _ => Err(Miss::Kind),
            }
        }
        [dir, tail @ ..] if pi_stem_ok(dir) => match tail {
            ["tasks" | "forks", f] if f.strip_suffix(".jsonl").is_some_and(|s| !s.is_empty()) => {
                child(dir)
            }
            [u, run, "session.jsonl"] if is_uuid(u) && pi_run_ok(run) => child(dir),
            // Go keeps every other file of a validated session directory as session aux.
            _ => session(
                SourceKind::PiAux,
                SourceClass::Snapshot,
                Role::Artifact,
                owner(dir),
                parts,
            ),
        },
        // Go demotes multi-part paths whose first part is not a session directory to
        // sessionless aux (Shared): no owner is derivable, so this is not UnknownOwner.
        _ if mode != PiMode::Strict => sessionless(),
        _ => Err(Miss::Kind),
    }
}

/// Map one root-relative path to its source, or to a countable withhold. Pure and total.
pub fn locate(path: &RootRelativePath, roots: &Roots) -> Located {
    let parts: Vec<&str> = path.0.iter().map(String::as_str).collect();
    let res = match (roots.layout, &roots.agent) {
        (RootLayout::ClaudePrimaryHome, Agent::Claude) => match parts.as_slice() {
            [".claude.json"] => asset(SourceKind::ClaudeConfig, ConfigAsset::PrimaryConfig, &parts),
            _ => Err(Miss::Kind),
        },
        (RootLayout::ClaudePrimaryHome, _) => Err(Miss::Kind),
        (RootLayout::PiCustomRoot, Agent::Pi) => {
            // Go `Pi.Classify`: agent-dir rules when under `sessions/`, a recognised config
            // name/dir (`topRole`), or a single-part `*.log`; otherwise sessionless aux.
            let promoted =
                matches!(parts.as_slice(), ["sessions", _, ..]) || pi_asset(&parts).is_some();
            if promoted {
                pi(&parts)
            } else {
                pi_session(&parts, PiMode::Custom, &parts)
            }
        }
        (RootLayout::PiCustomRoot, _) => Err(Miss::Kind),
        (RootLayout::AgentDir, Agent::Claude) => claude(&parts, roots),
        (RootLayout::AgentDir, Agent::Codex) => codex(&parts),
        (RootLayout::AgentDir, Agent::Pi) => pi(&parts),
    };
    match res {
        Err(Miss::Kind) => Located::UnknownKind,
        Err(Miss::Owner) => Located::UnknownOwner,
        Ok(hit) => finish(hit, roots),
    }
}

fn finish(hit: Hit<'_>, roots: &Roots) -> Located {
    let ns = &roots.namespace;
    let Ok(id) = SourceId::try_from(ident(ns, &hit.id)) else {
        return Located::UnknownKind;
    };
    let owner = match hit.owner {
        OwnerSpec::Shared => Owner::Shared {
            archive: roots.archive.clone(),
            machine: roots.machine.clone(),
            agent: roots.agent.clone(),
        },
        OwnerSpec::Session(parts) => {
            let Ok(session) = SessionId::try_from(ident(ns, &parts)) else {
                return Located::UnknownKind;
            };
            Owner::Session(SessionKey {
                archive: roots.archive.clone(),
                machine: roots.machine.clone(),
                agent: roots.agent.clone(),
                session,
            })
        }
    };
    Located::Source {
        owner,
        kind: hit.kind,
        class: hit.class,
        id,
        role: hit.role,
    }
}
