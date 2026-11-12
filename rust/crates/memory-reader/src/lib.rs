//! Authoritative restore library: decodes stored pieces and restores one source's bytes.
//!
//! Walking `current.json` and the manifest chain is not implemented here yet; callers pass the
//! retained epoch (from `memory-core`) and a fetch function for object keys.
use memory_core::manifest::{EpochRecord, Piece, SourceEntry, manifest_hash};
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
