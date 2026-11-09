//! Pure admission limits: `current.json` key length, distinct sources per owner and the
//! size-scaled request budget (DESIGN.md sections 6.2 and 7.3). No I/O, no clock.
use crate::domain::{Owner, SourceId, WithholdReason};
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

/// Exact `current.json` key for an owner.
pub fn current_key(owner: &Owner) -> String {
    match owner {
        Owner::Session(k) => format!(
            "sessions/{}/{}/{}/{}/current.json",
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
            "shared/{}/{}/{}/current.json",
            archive.as_str(),
            machine.as_str(),
            agent.as_str()
        ),
    }
}

/// UTF-8 byte length of the key for raw segments (`session` is `None` for shared owners).
/// Saturating, so hostile lengths cannot wrap.
pub fn current_key_len(archive: &str, machine: &str, agent: &str, session: Option<&str>) -> usize {
    // "sessions/" + "/" x3 + "/current.json" or "shared/" + "/" x2 + "/current.json".
    let (fixed, session_len) = match session {
        Some(s) => ("sessions/".len() + 3 + "/current.json".len(), s.len()),
        None => ("shared/".len() + 2 + "/current.json".len(), 0),
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
