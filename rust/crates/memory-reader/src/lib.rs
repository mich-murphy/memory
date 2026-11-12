//! Authoritative restore library: decodes stored pieces and restores one source's bytes.
//!
//! [`read_owner`] walks `current.json` and the manifest chain; [`restore_source`] then restores
//! one source from the folded state. Callers pass a fetch function over object keys.
use memory_core::domain::{CommitOutcome, Owner};
use memory_core::limits::{current_key, manifest_key};
use memory_core::manifest::{
    CHECKPOINT_INTERVAL, EpochRecord, Folded, ManifestError, Piece, ProjectedKind, SourceEntry,
    fold, manifest_hash, parse_current, recover_projection,
};
use std::io::Read;

/// Why a stored piece did not decode to the bytes its descriptor promises.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum DecodeError {
    /// The stored length differs from `encoded_len`.
    #[error("stored length differs from the descriptor")]
    StoredLength,
    /// The stored digest differs from `encoded_sha256`.
    #[error("stored digest differs from the descriptor")]
    StoredDigest,
    /// The stored bytes are not a valid zstd frame.
    #[error("stored bytes are not a valid zstd frame")]
    Zstd,
    /// The frame decodes to more than the descriptor's `len`.
    #[error("decoded bytes exceed the descriptor length")]
    TooLong,
    /// The decoded length differs from `len`.
    #[error("decoded length differs from the descriptor")]
    RawLength,
    /// The decoded digest differs from `sha256`.
    #[error("decoded digest differs from the descriptor")]
    RawDigest,
}

/// Why one source could not be restored.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum RestoreError<E> {
    /// The source has no epoch to restore.
    #[error("the source has no epoch")]
    NoEpoch,
    /// Fetching an object failed; the fetch error is passed through.
    #[error("fetching an object failed")]
    Fetch(E),
    /// A piece did not decode.
    #[error("a piece did not decode")]
    Decode(DecodeError),
    /// Pieces are not contiguous from offset 0.
    #[error("pieces are not contiguous from offset 0")]
    Gap,
    /// The restored length differs from the epoch's `size`.
    #[error("restored length differs from the epoch size")]
    Size,
    /// The restored digest differs from the epoch's `sha256`.
    #[error("restored digest differs from the epoch digest")]
    Digest,
}

/// Decode one stored piece, verifying it against its descriptor.
///
/// The output is bounded by the descriptor's `len`, so a decompression bomb fails instead of
/// allocating.
///
/// # Errors
/// The first failed check, as a [`DecodeError`].
pub fn decode_piece(piece: &Piece, stored: &[u8]) -> Result<Vec<u8>, DecodeError> {
    if u64::try_from(stored.len()).ok() != Some(piece.encoded_len()) {
        return Err(DecodeError::StoredLength);
    }
    if manifest_hash(stored) != piece.encoded_sha256() {
        return Err(DecodeError::StoredDigest);
    }
    let bound = piece.len();
    let mut decoder = zstd::stream::read::Decoder::new(stored).map_err(|_| DecodeError::Zstd)?;
    let mut out = Vec::new();
    decoder
        .by_ref()
        .take(bound.saturating_add(1))
        .read_to_end(&mut out)
        .map_err(|_| DecodeError::Zstd)?;
    let got = u64::try_from(out.len()).map_err(|_| DecodeError::TooLong)?;
    if got > bound {
        return Err(DecodeError::TooLong);
    }
    if got != bound {
        return Err(DecodeError::RawLength);
    }
    if manifest_hash(&out) != piece.sha256() {
        return Err(DecodeError::RawDigest);
    }
    Ok(out)
}

/// Restore the transformed bytes of one epoch: fetch and decode every piece, require them
/// contiguous from offset 0, and check the epoch's `size` and `sha256`.
///
/// # Errors
/// [`RestoreError`]; a fetch error is returned unchanged in [`RestoreError::Fetch`].
pub fn restore_epoch<E>(
    record: &EpochRecord,
    mut fetch: impl FnMut(&str) -> Result<Vec<u8>, E>,
) -> Result<Vec<u8>, RestoreError<E>> {
    let mut out: Vec<u8> = Vec::new();
    for piece in &record.pieces {
        if u64::try_from(out.len()).ok() != Some(piece.offset().get()) {
            return Err(RestoreError::Gap);
        }
        let stored = fetch(piece.object()).map_err(RestoreError::Fetch)?;
        out.extend(decode_piece(piece, &stored).map_err(RestoreError::Decode)?);
    }
    if u64::try_from(out.len()).ok() != Some(record.size) {
        return Err(RestoreError::Size);
    }
    if manifest_hash(&out) != record.sha256 {
        return Err(RestoreError::Digest);
    }
    Ok(out)
}

/// Restore the latest epoch of a source (the only one for a snapshot source).
///
/// # Errors
/// [`RestoreError::NoEpoch`] when the entry has none, otherwise as [`restore_epoch`].
pub fn restore_source<E>(
    entry: &SourceEntry,
    fetch: impl FnMut(&str) -> Result<Vec<u8>, E>,
) -> Result<Vec<u8>, RestoreError<E>> {
    let record = entry.epochs.last().ok_or(RestoreError::NoEpoch)?;
    restore_epoch(record, fetch)
}

/// What is wrong with an owner's stored chain.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum ChainFault {
    /// `current.json` or a delta names a manifest that is not stored.
    #[error("a named manifest is not stored")]
    MissingManifest,
    /// A manifest's bytes do not hash to the key it was fetched under.
    #[error("a manifest does not hash to its key")]
    HashMismatch,
    /// No checkpoint within the most manifests a valid chain holds.
    #[error("no checkpoint within the chain bound")]
    TooLong,
    /// A document did not parse, or the chain does not fold.
    #[error("a document is invalid or the chain does not fold")]
    Invalid,
    /// The chain belongs to a different owner.
    #[error("the chain belongs to a different owner")]
    WrongOwner,
    /// The folded head is not the manifest `current.json` names.
    #[error("the folded head is not the manifest current.json names")]
    WrongHead,
}

/// Why an owner's state could not be read.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum OwnerError<E> {
    /// Fetching an object failed; the fetch error is passed through.
    #[error("fetching an object failed")]
    Fetch(E),
    /// A stored document has a newer schema than this reader understands.
    #[error("a stored document has a newer schema")]
    NewerSchema,
    /// The stored chain is corrupt or inconsistent.
    #[error("the stored chain is corrupt")]
    Corrupt(ChainFault),
}

fn newer_or_invalid<E>(e: &ManifestError) -> OwnerError<E> {
    if e.commit_outcome() == Some(CommitOutcome::NewerSchema) {
        OwnerError::NewerSchema
    } else {
        OwnerError::Corrupt(ChainFault::Invalid)
    }
}

/// Read an owner's folded state: GET `current.json`, walk `previous` back to the checkpoint
/// (at most [`CHECKPOINT_INTERVAL`] manifest fetches, each verified against its key) and fold.
///
/// `fetch` returns `Ok(None)` for an absent key. An absent `current.json` is `Ok(None)`; an
/// absent manifest that `current.json` or a delta names is corruption.
///
/// # Errors
/// [`OwnerError`]; a fetch error is returned unchanged in [`OwnerError::Fetch`].
pub fn read_owner<E>(
    owner: &Owner,
    mut fetch: impl FnMut(&str) -> Result<Option<Vec<u8>>, E>,
) -> Result<Option<Folded>, OwnerError<E>> {
    let Some(pointer) = fetch(&current_key(owner)).map_err(OwnerError::Fetch)? else {
        return Ok(None);
    };
    let current = parse_current(&pointer).map_err(|e| newer_or_invalid(&e))?;
    let mut hash = current.manifest;
    let mut chain: Vec<Vec<u8>> = Vec::new();
    loop {
        // A valid chain is a checkpoint plus at most 63 deltas; a 65th fetch cannot be valid.
        if chain.len() as u64 >= CHECKPOINT_INTERVAL {
            return Err(OwnerError::Corrupt(ChainFault::TooLong));
        }
        let bytes = fetch(&manifest_key(&hash))
            .map_err(OwnerError::Fetch)?
            .ok_or(OwnerError::Corrupt(ChainFault::MissingManifest))?;
        if manifest_hash(&bytes) != hash {
            return Err(OwnerError::Corrupt(ChainFault::HashMismatch));
        }
        let projection = recover_projection(&bytes).map_err(|e| newer_or_invalid(&e))?;
        chain.push(bytes);
        match projection.kind {
            ProjectedKind::Checkpoint => break,
            ProjectedKind::Delta => {
                hash = projection
                    .previous
                    .ok_or(OwnerError::Corrupt(ChainFault::Invalid))?;
            }
        }
    }
    chain.reverse();
    let refs: Vec<&[u8]> = chain.iter().map(Vec::as_slice).collect();
    let folded = fold(&refs).map_err(|e| newer_or_invalid(&e))?;
    if folded.owner != *owner {
        return Err(OwnerError::Corrupt(ChainFault::WrongOwner));
    }
    if folded.position.manifest != current.manifest {
        return Err(OwnerError::Corrupt(ChainFault::WrongHead));
    }
    Ok(Some(folded))
}
