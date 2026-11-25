//! Object-key layout and the owner text form (DESIGN.md sections 6.1 and 6.2), with the
//! longest admitted key. No I/O, no clock.
use crate::domain::{Agent, ArchiveId, MachineId, Owner, SessionId, SessionKey, Sha256};

/// Longest admitted object key, in UTF-8 bytes (DESIGN.md section 6.2).
pub const MAX_KEY_BYTES: usize = 1024;

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
