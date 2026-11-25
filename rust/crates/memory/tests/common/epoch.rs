//! Test-only oracle: the bytes an epoch stores, decoded piece by piece through
//! `memory_reader::decode_piece` (which checks each stored and decoded digest) and checked
//! against the epoch's contiguity, `size` and `sha256`.
use memory_core::manifest::{EpochRecord, manifest_hash};

/// The decoded bytes of `record`, fetching each piece object with `fetch`.
pub fn epoch_bytes<E: std::fmt::Debug>(
    record: &EpochRecord,
    mut fetch: impl FnMut(&str) -> Result<Vec<u8>, E>,
) -> Result<Vec<u8>, String> {
    let mut out: Vec<u8> = Vec::new();
    for piece in &record.pieces {
        if u64::try_from(out.len()).ok() != Some(piece.offset().get()) {
            return Err("pieces are not contiguous".to_owned());
        }
        let stored = fetch(piece.object()).map_err(|e| format!("fetch failed: {e:?}"))?;
        out.extend(memory_reader::decode_piece(piece, &stored).map_err(|e| format!("{e:?}"))?);
    }
    if u64::try_from(out.len()).ok() != Some(record.size) {
        return Err("decoded size differs from the epoch size".to_owned());
    }
    if manifest_hash(&out) != record.sha256 {
        return Err("decoded digest differs from the epoch digest".to_owned());
    }
    Ok(out)
}
