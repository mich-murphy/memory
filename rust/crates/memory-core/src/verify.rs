//! Pure classification of the commit verification GET (DESIGN.md 7.4 step 5, amendment A6).
//!
//! The shell reads `current.json` and hands the manifest hash it names to [`classify`], with
//! the commit's own manifest hash, the hash it fenced on and the set of manifests this daemon
//! built since its last landed commit. Everything after the read is a decision over typed
//! inputs and lives here; the shell keeps only I/O.
use crate::domain::{CommitOutcome, ETag, Sha256};

/// What the verification GET proved, with what the shell needs to act on it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Verdict {
    /// `current.json` names our manifest: the write landed.
    Ours {
        /// ETag `current.json` now has.
        etag: ETag,
    },
    /// `current.json` names an earlier manifest of ours that never reported landing
    /// (`known_ours`, A6). Adopt it; this commit's own manifest did not land.
    Adopt {
        /// ETag `current.json` now has.
        etag: ETag,
        /// The earlier manifest.
        manifest: Sha256,
    },
    /// `current.json` still names the manifest we fenced on: nothing landed, rebase.
    Base {
        /// ETag to fence the retry on.
        etag: ETag,
        /// The manifest it names.
        manifest: Sha256,
    },
    /// Another writer advanced the owner.
    Foreign {
        /// The manifest `current.json` names.
        theirs: Sha256,
    },
    /// The manifest `current.json` names has a newer schema.
    NewerSchema,
}

impl Verdict {
    /// The commit outcome this verdict reports.
    pub fn outcome(&self) -> CommitOutcome {
        match self {
            Self::Ours { etag } | Self::Adopt { etag, .. } => {
                CommitOutcome::AlreadyOurs { etag: etag.clone() }
            }
            Self::Base { .. } => CommitOutcome::BaseUnchanged,
            Self::Foreign { theirs } => CommitOutcome::Foreign { theirs: *theirs },
            Self::NewerSchema => CommitOutcome::NewerSchema,
        }
    }
}

/// Classify the manifest `current` that `current.json` names under `etag`.
///
/// Order matters: our own manifest wins, then an earlier attempt of ours (`known_ours`), then
/// the base we fenced on (`previous`). Anything else is foreign, unless the manifest it names
/// has a newer schema. `names_newer_schema` is called at most once and only in that last
/// case, so the shell reads the foreign manifest only when it has to.
pub fn classify(
    current: Sha256,
    etag: ETag,
    ours: Sha256,
    previous: Option<Sha256>,
    known_ours: impl Fn(&Sha256) -> bool,
    names_newer_schema: impl FnOnce() -> bool,
) -> Verdict {
    if current == ours {
        Verdict::Ours { etag }
    } else if known_ours(&current) {
        Verdict::Adopt {
            etag,
            manifest: current,
        }
    } else if Some(current) == previous {
        Verdict::Base {
            etag,
            manifest: current,
        }
    } else if names_newer_schema() {
        Verdict::NewerSchema
    } else {
        Verdict::Foreign { theirs: current }
    }
}

