//! Authoritative reader: decodes stored pieces, walks an owner's manifest chain and exports it.
//!
//! [`read_owner_head`] walks `current.json` and the manifest chain and folds it;
//! [`decode_piece`] checks one stored piece against its descriptor; [`export`] writes an owner
//! for analytics. Callers pass a fetch function over object keys.
use memory_core::domain::{CommitOutcome, Owner};
use memory_core::limits::{current_key, manifest_key};
use memory_core::manifest::{
    CHECKPOINT_INTERVAL, Folded, Manifest, ManifestError, Piece, ProjectedKind, Timestamp, fold,
    manifest_hash, parse_current, parse_manifest, recover_projection,
};
pub mod export;

use std::io::Read;

/// Most bytes [`decode_piece`] reserves up front: one capture piece (8 MiB).
const PREALLOC_PIECE_BYTES: u64 = 8 * 1024 * 1024;

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
    // Exact allocation from the descriptor, capped at one capture piece: a buffer grown by
    // doubling leaves large freed blocks behind, which kept a streamed read of one
    // 100 MB source at 249 MB (brief 34). `len` is only checked non-zero, hence the cap.
    let mut out =
        Vec::with_capacity(usize::try_from(bound.min(PREALLOC_PIECE_BYTES)).unwrap_or_default());
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

/// An owner's folded state with the two times the export envelope needs.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OwnerHead {
    /// The folded state and position.
    pub folded: Folded,
    /// `captured_at` of the head manifest.
    pub captured_at: Timestamp,
    /// `updated_at` of `current.json`.
    pub updated_at: Timestamp,
}

/// Read an owner's folded state and head times: GET `current.json`, walk `previous` back to
/// the checkpoint (at most [`CHECKPOINT_INTERVAL`] manifest fetches, each verified against its
/// key) and fold. The head carries the head manifest's `captured_at` and `current.json`'s
/// `updated_at`.
///
/// `fetch` returns `Ok(None)` for an absent key. An absent `current.json` is `Ok(None)`; an
/// absent manifest that `current.json` or a delta names is corruption.
///
/// # Errors
/// [`OwnerError`]; a fetch error is returned unchanged in [`OwnerError::Fetch`].
pub fn read_owner_head<E>(
    owner: &Owner,
    mut fetch: impl FnMut(&str) -> Result<Option<Vec<u8>>, E>,
) -> Result<Option<OwnerHead>, OwnerError<E>> {
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
    let head = chain
        .last()
        .ok_or(OwnerError::Corrupt(ChainFault::Invalid))?;
    let captured_at = match parse_manifest(head).map_err(|e| newer_or_invalid(&e))? {
        Manifest::Delta(d) => d.captured_at().clone(),
        Manifest::Checkpoint(c) => c.captured_at().clone(),
    };
    Ok(Some(OwnerHead {
        folded,
        captured_at,
        updated_at: current.updated_at,
    }))
}
