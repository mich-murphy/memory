use super::schema::{
    Change, Checkpoint, Delta, Manifest, Timestamp, Writer, manifest_hash, parse_manifest,
};
use super::state::OwnerState;
use super::{CHECKPOINT_INTERVAL, ManifestError};
use crate::domain::{Owner, Sha256};
use crate::launch::LaunchRecord;

// ---------------------------------------------------------------- builder

/// Position of the latest committed manifest of a chain.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Position {
    /// Sequence number.
    pub seq: u64,
    /// Hash of this manifest.
    pub manifest: Sha256,
    /// Hash of the latest checkpoint at or before it.
    pub checkpoint: Sha256,
}

/// Fields of a manifest that do not depend on the chain position.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Header {
    /// Chain owner.
    pub owner: Owner,
    /// Capture time.
    pub captured_at: Timestamp,
    /// Writer stamp.
    pub writer: Writer,
    /// Pending launch records.
    pub launch: Vec<LaunchRecord>,
}

/// Result of [`build`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Built {
    /// Canonical bytes to PUT as `manifests/<hash>.json`.
    pub bytes: Vec<u8>,
    /// SHA-256 of `bytes`.
    pub hash: Sha256,
    /// The manifest.
    pub manifest: Manifest,
    /// Owner state after this manifest.
    pub state: OwnerState,
    /// Position after this manifest.
    pub position: Position,
}

/// Build the next manifest after `previous` (or the first, a checkpoint at seq 0).
///
/// `seq` is the previous seq plus one; a checkpoint when `seq % 64 == 0`, listing all
/// retained epochs of the state after the changes.
///
/// # Errors
/// Invalid changes, an invalid transition, or seq overflow.
pub fn build(
    previous: Option<(&Position, &OwnerState)>,
    header: Header,
    changes: Vec<Change>,
) -> Result<Built, ManifestError> {
    let (seq, mut state, link) = match previous {
        None => (0, OwnerState::default(), None),
        Some((p, s)) => (
            p.seq.checked_add(1).ok_or(ManifestError::Overflow)?,
            s.clone(),
            Some((p.manifest, p.checkpoint)),
        ),
    };
    state.apply_changes(&changes)?;
    let Header {
        owner,
        captured_at,
        writer,
        launch,
    } = header;
    let is_checkpoint = seq.is_multiple_of(CHECKPOINT_INTERVAL);
    let manifest = if is_checkpoint {
        Manifest::Checkpoint(Checkpoint::new(
            owner,
            seq,
            link.map(|(m, _)| m),
            captured_at,
            writer,
            launch,
            state.to_checkpoint_sources(),
        ))
    } else {
        let (previous, checkpoint) = link.ok_or(ManifestError::BadLink)?;
        Manifest::Delta(Delta::new(
            owner,
            seq,
            previous,
            checkpoint,
            captured_at,
            writer,
            launch,
            changes,
        ))
    };
    manifest.validate()?;
    let bytes = manifest.to_bytes()?;
    let hash = manifest_hash(&bytes);
    let checkpoint = match (is_checkpoint, link) {
        (false, Some((_, cp))) => cp,
        _ => hash,
    };
    Ok(Built {
        bytes,
        hash,
        manifest,
        state,
        position: Position {
            seq,
            manifest: hash,
            checkpoint,
        },
    })
}

// ---------------------------------------------------------------- fold

/// Result of folding a reader chain.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Folded {
    /// Chain owner.
    pub owner: Owner,
    /// Folded state.
    pub state: OwnerState,
    /// Position of the last manifest folded.
    pub position: Position,
}

/// Fold a checkpoint followed by consecutive deltas (oldest first, at most 64 manifests).
///
/// # Errors
/// Parse errors, link/seq/owner inconsistencies, [`ManifestError::TooManySteps`], or an
/// invalid state transition.
pub fn fold(chain: &[&[u8]]) -> Result<Folded, ManifestError> {
    if chain.len() as u64 > CHECKPOINT_INTERVAL {
        return Err(ManifestError::TooManySteps);
    }
    let mut raw = chain.iter();
    let first = raw.next().ok_or(ManifestError::BrokenChain)?;
    let Manifest::Checkpoint(checkpoint) = parse_manifest(first)? else {
        return Err(ManifestError::BrokenChain);
    };
    let checkpoint_hash = manifest_hash(first);
    let mut state = OwnerState::from_checkpoint(&checkpoint);
    let mut position = Position {
        seq: checkpoint.seq(),
        manifest: checkpoint_hash,
        checkpoint: checkpoint_hash,
    };
    for bytes in raw {
        let Manifest::Delta(delta) = parse_manifest(bytes)? else {
            return Err(ManifestError::BrokenChain);
        };
        if delta.owner() != checkpoint.owner() {
            return Err(ManifestError::OwnerMismatch);
        }
        if position.seq.checked_add(1) != Some(delta.seq()) {
            return Err(ManifestError::BadSeq);
        }
        if delta.previous() != position.manifest || delta.checkpoint() != checkpoint_hash {
            return Err(ManifestError::BadLink);
        }
        state.apply_changes(delta.changes())?;
        position.seq = delta.seq();
        position.manifest = manifest_hash(bytes);
    }
    Ok(Folded {
        owner: checkpoint.owner().clone(),
        state,
        position,
    })
}
