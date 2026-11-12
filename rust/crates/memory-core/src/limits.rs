//! Pure admission limits: `current.json` key length, distinct sources per owner and the
//! size-scaled request budget (DESIGN.md sections 6.2 and 7.3). No I/O, no clock.
use crate::domain::{
    Agent, ArchiveId, MachineId, Owner, SessionId, SessionKey, Sha256, SourceId, WithholdReason,
};
use std::collections::HashSet;
use std::time::Duration;

/// Longest admitted object key, in UTF-8 bytes (DESIGN.md section 6.2).
pub const MAX_KEY_BYTES: usize = 1024;
/// Most distinct sources one owner may hold (DESIGN.md section 6.2).
pub const MAX_SOURCES_PER_OWNER: usize = 1024;
/// Request budget floor.
pub const BUDGET_FLOOR: Duration = Duration::from_secs(60);
/// Fixed request budget before the size term.
pub const BUDGET_BASE: Duration = Duration::from_secs(30);
/// Assumed upload throughput in bytes per second (1 MiB/s).
pub const BUDGET_BYTES_PER_SEC: u64 = 1024 * 1024;

// Object-key layout (DESIGN.md section 6.1): the one place its spelling lives. The shell
// reaches it through these functions and never formats a key itself.
const SESSION_PREFIX: &str = "sessions/";
const SHARED_PREFIX: &str = "shared/";
const CURRENT_SUFFIX: &str = "/current.json";

/// The owner's key prefix: the stored `current.json` key without its suffix. This is also the
/// owner's text form on the command line; [`parse_owner_prefix`] reads it back. No segment can
/// contain `/` (identifiers allow only ASCII alphanumerics and `._:-`), so it is unambiguous.
pub fn owner_prefix(owner: &Owner) -> String {
    match owner {
        Owner::Session(k) => format!(
            "{SESSION_PREFIX}{}/{}/{}/{}",
            k.archive.as_str(),
            k.machine.as_str(),
            k.agent.as_str(),
            k.session.as_str()
        ),
        Owner::Shared {
            archive,
            machine,
            agent,
        } => format!(
            "{SHARED_PREFIX}{}/{}/{}",
            archive.as_str(),
            machine.as_str(),
            agent.as_str()
        ),
    }
}

/// Exact `current.json` key for an owner.
pub fn current_key(owner: &Owner) -> String {
    format!("{}{CURRENT_SUFFIX}", owner_prefix(owner))
}

/// Why owner text was refused. Closed; never carries the input.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum OwnerTextError {
    /// The `current.json` key built from this text would exceed [`MAX_KEY_BYTES`].
    #[error("owner text too long")]
    TooLong,
    /// First segment is neither `sessions` nor `shared`.
    #[error("unknown owner prefix")]
    Prefix,
    /// Wrong number of segments for the kind (a trailing slash or a left-on suffix lands here).
    #[error("wrong segment count")]
    Segments,
    /// A segment is empty.
    #[error("empty segment")]
    EmptySegment,
    /// Archive, machine or session id refused by its constructor.
    #[error("invalid identifier")]
    Identifier,
    /// Agent is not one of the known words.
    #[error("unknown agent")]
    Agent,
}

fn parse_agent(s: &str) -> Result<Agent, OwnerTextError> {
    [Agent::Claude, Agent::Codex, Agent::Pi]
        .into_iter()
        .find(|a| a.as_str() == s)
        .ok_or(OwnerTextError::Agent)
}

/// Parse exactly what [`owner_prefix`] prints.
///
/// # Errors
/// A closed [`OwnerTextError`]; the input is never echoed.
pub fn parse_owner_prefix(text: &str) -> Result<Owner, OwnerTextError> {
    if text.len().saturating_add(CURRENT_SUFFIX.len()) > MAX_KEY_BYTES {
        return Err(OwnerTextError::TooLong);
    }
    let parts: Vec<&str> = text.split('/').collect();
    let want = match parts.first().copied() {
        Some("sessions") => 5,
        Some("shared") => 4,
        _ => return Err(OwnerTextError::Prefix),
    };
    if parts.len() != want {
        return Err(OwnerTextError::Segments);
    }
    if parts.iter().any(|p| p.is_empty()) {
        return Err(OwnerTextError::EmptySegment);
    }
    let id = |s: &str| s.to_owned();
    let bad = |_| OwnerTextError::Identifier;
    match parts.as_slice() {
        [_, archive, machine, agent, session] => Ok(Owner::Session(SessionKey {
            archive: ArchiveId::try_from(id(archive)).map_err(bad)?,
            machine: MachineId::try_from(id(machine)).map_err(bad)?,
            agent: parse_agent(agent)?,
            session: SessionId::try_from(id(session)).map_err(bad)?,
        })),
        [_, archive, machine, agent] => Ok(Owner::Shared {
            archive: ArchiveId::try_from(id(archive)).map_err(bad)?,
            machine: MachineId::try_from(id(machine)).map_err(bad)?,
            agent: parse_agent(agent)?,
        }),
        _ => Err(OwnerTextError::Segments),
    }
}

/// Key of an immutable manifest, `manifests/<sha256 of manifest bytes>.json`.
pub fn manifest_key(hash: &Sha256) -> String {
    format!("manifests/{}.json", String::from(*hash))
}

/// Object key `objects/<sha256 of the transformed piece bytes>.zst`: the digest is taken after
/// the privacy transform and before compression.
pub fn object_key(raw: &Sha256) -> String {
    format!("objects/{}.zst", String::from(*raw))
}

/// UTF-8 byte length of the key for raw segments (`session` is `None` for shared owners).
/// Saturating, so hostile lengths cannot wrap.
pub fn current_key_len(archive: &str, machine: &str, agent: &str, session: Option<&str>) -> usize {
    // The fixed text of `current_key`, plus one "/" between segments.
    let (fixed, session_len) = match session {
        Some(s) => (SESSION_PREFIX.len() + 3 + CURRENT_SUFFIX.len(), s.len()),
        None => (SHARED_PREFIX.len() + 2 + CURRENT_SUFFIX.len(), 0),
    };
    fixed
        .saturating_add(archive.len())
        .saturating_add(machine.len())
        .saturating_add(agent.len())
        .saturating_add(session_len)
}

/// Admit a key byte length: 1,024 passes, 1,025 is `KeyTooLong`.
///
/// # Errors
/// Returns `WithholdReason::KeyTooLong` above [`MAX_KEY_BYTES`].
pub const fn check_key_len(len: usize) -> Result<(), WithholdReason> {
    if len > MAX_KEY_BYTES {
        Err(WithholdReason::KeyTooLong)
    } else {
        Ok(())
    }
}

/// Admit an owner's `current.json` key.
///
/// # Errors
/// Returns `WithholdReason::KeyTooLong` when the key exceeds 1,024 bytes.
pub fn check_owner_key(owner: &Owner) -> Result<(), WithholdReason> {
    check_key_len(current_key(owner).len())
}

/// Distinct sources already admitted for one owner. Counts SourceIds, never epochs.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct SourceSet {
    known: HashSet<SourceId>,
}

impl SourceSet {
    /// Empty set.
    pub fn new() -> Self {
        Self::default()
    }
    /// Number of distinct admitted sources.
    pub fn len(&self) -> usize {
        self.known.len()
    }
    /// True when no source is admitted.
    pub fn is_empty(&self) -> bool {
        self.known.is_empty()
    }
    /// Whether the id is already admitted.
    pub fn contains(&self, id: &SourceId) -> bool {
        self.known.contains(id)
    }
    /// Admit a source. A known source always stays admitted; the 1,025th distinct one is
    /// `SourceLimit` (visible, non-retrying). Nothing is ever evicted.
    ///
    /// # Errors
    /// Returns `WithholdReason::SourceLimit` for a new source beyond the cap.
    pub fn admit(&mut self, id: &SourceId) -> Result<(), WithholdReason> {
        if self.known.contains(id) {
            return Ok(());
        }
        if self.known.len() >= MAX_SOURCES_PER_OWNER {
            return Err(WithholdReason::SourceLimit);
        }
        self.known.insert(id.clone());
        Ok(())
    }
}

/// Request timeout: `max(60 s, 30 s + size / (1 MiB/s))`, exact to the nanosecond.
pub fn request_budget(size_bytes: u64) -> Duration {
    let secs = size_bytes / BUDGET_BYTES_PER_SEC;
    let rem = size_bytes % BUDGET_BYTES_PER_SEC;
    // rem < 2^20, so rem * 1e9 < 2^50 and cannot overflow.
    let nanos = rem.saturating_mul(1_000_000_000) / BUDGET_BYTES_PER_SEC;
    let size_term = Duration::from_secs(secs).saturating_add(Duration::from_nanos(nanos));
    BUDGET_BASE.saturating_add(size_term).max(BUDGET_FLOOR)
}
