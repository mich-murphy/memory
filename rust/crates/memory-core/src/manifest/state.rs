use super::ManifestError;
use super::schema::{Change, Checkpoint, CheckpointSource, EpochRecord, SourceWrite, Timestamp};
use crate::domain::{SourceClass, SourceId, SourceKind};
use std::collections::{BTreeMap, HashSet};

// ---------------------------------------------------------------- owner state

/// Retained state of one source.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SourceEntry {
    /// Source kind.
    pub kind: SourceKind,
    /// Root-relative path.
    pub path: String,
    /// Source class.
    pub class: SourceClass,
    /// Append: every epoch. Snapshot: the latest only.
    pub epochs: Vec<EpochRecord>,
    /// Deletion time, cleared when a new epoch starts.
    pub deleted_at: Option<Timestamp>,
}
impl SourceEntry {
    fn same_identity(&self, w: &SourceWrite) -> Result<(), ManifestError> {
        if self.kind == w.kind && self.path == w.path && self.class == w.class {
            Ok(())
        } else {
            Err(ManifestError::SourceChanged)
        }
    }
}

/// Retained state of an owner: the fold of a checkpoint and the deltas after it.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct OwnerState {
    /// Sources by id.
    pub sources: BTreeMap<SourceId, SourceEntry>,
}
impl OwnerState {
    /// State listed by a checkpoint.
    pub fn from_checkpoint(checkpoint: &Checkpoint) -> Self {
        Self {
            sources: checkpoint
                .sources()
                .iter()
                .map(|s| {
                    (
                        s.id.clone(),
                        SourceEntry {
                            kind: s.kind.clone(),
                            path: s.path.clone(),
                            class: s.class.clone(),
                            epochs: s.epochs.clone(),
                            deleted_at: s.deleted_at.clone(),
                        },
                    )
                })
                .collect(),
        }
    }
    /// Checkpoint listing of this state, sorted by id.
    pub fn to_checkpoint_sources(&self) -> Vec<CheckpointSource> {
        self.sources
            .iter()
            .map(|(id, e)| CheckpointSource {
                id: id.clone(),
                kind: e.kind.clone(),
                path: e.path.clone(),
                class: e.class.clone(),
                epochs: e.epochs.clone(),
                deleted_at: e.deleted_at.clone(),
            })
            .collect()
    }
    /// Apply one manifest's changes atomically (state unchanged on error).
    ///
    /// # Errors
    /// Any structural or state-transition violation.
    pub fn apply_changes(&mut self, changes: &[Change]) -> Result<(), ManifestError> {
        let mut seen = HashSet::new();
        for change in changes {
            change.validate()?;
            if !seen.insert(change.id()) {
                return Err(ManifestError::DuplicateSource);
            }
        }
        let mut next = self.clone();
        for change in changes {
            next.apply_one(change)?;
        }
        *self = next;
        Ok(())
    }
    fn apply_one(&mut self, change: &Change) -> Result<(), ManifestError> {
        match change {
            Change::Extend(w) => {
                let entry = self
                    .sources
                    .get_mut(&w.id)
                    .ok_or(ManifestError::MissingSource)?;
                entry.same_identity(w)?;
                if entry.deleted_at.is_some() {
                    return Err(ManifestError::SourceDeleted);
                }
                let last = entry.epochs.last_mut().ok_or(ManifestError::BadEpoch)?;
                if last.epoch != w.epoch {
                    return Err(ManifestError::BadEpoch);
                }
                let first = w.pieces.first().ok_or(ManifestError::BadPiece)?;
                match first.offset().get().cmp(&last.size) {
                    std::cmp::Ordering::Less => return Err(ManifestError::Overlap),
                    std::cmp::Ordering::Greater => return Err(ManifestError::Gap),
                    std::cmp::Ordering::Equal => {}
                }
                last.size = w.size;
                last.sha256 = w.sha256;
                last.pieces.extend(w.pieces.iter().cloned());
                Ok(())
            }
            Change::Epoch(w) | Change::Replace(w) => {
                let replace = matches!(change, Change::Replace(_));
                match self.sources.get_mut(&w.id) {
                    None => {
                        self.sources.insert(w.id.clone(), w.entry());
                    }
                    Some(entry) => {
                        entry.same_identity(w)?;
                        let next = entry
                            .epochs
                            .last()
                            .and_then(|l| l.epoch.n.checked_add(1))
                            .ok_or(ManifestError::BadEpoch)?;
                        if w.epoch.n != next {
                            return Err(ManifestError::BadEpoch);
                        }
                        if replace {
                            entry.epochs.clear();
                        }
                        entry.epochs.push(w.record());
                        entry.deleted_at = None;
                    }
                }
                Ok(())
            }
            Change::Deleted {
                id,
                kind,
                deleted_at,
            } => {
                let entry = self
                    .sources
                    .get_mut(id)
                    .ok_or(ManifestError::MissingSource)?;
                if entry.kind != *kind {
                    return Err(ManifestError::SourceChanged);
                }
                if entry.deleted_at.is_some() {
                    return Err(ManifestError::SourceDeleted);
                }
                entry.deleted_at = Some(deleted_at.clone());
                Ok(())
            }
        }
    }
}
