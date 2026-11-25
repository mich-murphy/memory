//! Piece decoding, the check `memory export` runs on every stored piece, against descriptors
//! built from JSON (the remote manifest path) and an independent SHA-256.
use memory_reader::{DecodeError, decode_piece};
mod common;
use common::{R, enc, hex, piece, piece_json};

fn set(json: &mut serde_json::Value, key: &str, value: serde_json::Value) -> R {
    json.as_object_mut()
        .ok_or("not an object")?
        .insert(key.to_owned(), value);
    Ok(())
}
fn data() -> Vec<u8> {
    b"hello hello hello hello hello world".repeat(20)
}

#[test]
fn round_trip_one_piece() -> R {
    let raw = data();
    let stored = enc(&raw)?;
    assert_ne!(stored, raw);
    let p = piece(&raw, 0, &stored)?;
    assert_eq!(decode_piece(&p, &stored), Ok(raw));
    Ok(())
}

#[test]
fn wrong_encoded_digest_is_rejected() -> R {
    let raw = data();
    let stored = enc(&raw)?;
    let mut json = piece_json(&raw, 0, &stored);
    set(&mut json, "encoded_sha256", hex(b"other").into())?;
    let p: memory_core::manifest::Piece =
        serde_json::from_value(json).map_err(|e| e.to_string())?;
    assert_eq!(decode_piece(&p, &stored), Err(DecodeError::StoredDigest));
    Ok(())
}

#[test]
fn wrong_raw_digest_is_rejected() -> R {
    let raw = data();
    let stored = enc(&raw)?;
    let mut json = piece_json(&raw, 0, &stored);
    set(&mut json, "sha256", hex(b"other").into())?;
    set(
        &mut json,
        "object",
        format!("objects/{}.zst", hex(b"other")).into(),
    )?;
    let p: memory_core::manifest::Piece =
        serde_json::from_value(json).map_err(|e| e.to_string())?;
    assert_eq!(decode_piece(&p, &stored), Err(DecodeError::RawDigest));
    Ok(())
}

#[test]
fn truncated_stored_bytes_are_rejected() -> R {
    let raw = data();
    let stored = enc(&raw)?;
    let p = piece(&raw, 0, &stored)?;
    let cut = stored.get(..stored.len() - 1).ok_or("cut")?;
    assert_eq!(decode_piece(&p, cut), Err(DecodeError::StoredLength));
    // A consistent descriptor over a truncated frame fails in the decoder, not the length check.
    let q = piece(&raw, 0, cut)?;
    assert!(matches!(
        decode_piece(&q, cut),
        Err(DecodeError::Zstd | DecodeError::RawLength)
    ));
    Ok(())
}

#[test]
fn a_frame_that_decodes_past_len_is_rejected_without_allocating_it() -> R {
    let big = vec![0_u8; 64 << 20];
    let stored = enc(&big)?;
    assert!(stored.len() < 64 << 10, "the bomb is tiny when stored");
    // The descriptor claims 10 bytes; the stored frame is consistent with its own digest.
    let small = big.get(..10).ok_or("small")?;
    let p: memory_core::manifest::Piece = serde_json::from_value({
        let mut j = piece_json(small, 0, &stored);
        set(&mut j, "encoded_len", stored.len().into())?;
        set(&mut j, "encoded_sha256", hex(&stored).into())?;
        j
    })
    .map_err(|e| e.to_string())?;
    assert_eq!(decode_piece(&p, &stored), Err(DecodeError::TooLong));
    Ok(())
}

#[test]
fn a_frame_shorter_than_len_is_rejected() -> R {
    let raw = data();
    let stored = enc(&raw)?;
    let mut json = piece_json(&raw, 0, &stored);
    set(&mut json, "len", (raw.len() + 5).into())?;
    let p: memory_core::manifest::Piece =
        serde_json::from_value(json).map_err(|e| e.to_string())?;
    assert_eq!(decode_piece(&p, &stored), Err(DecodeError::RawLength));
    Ok(())
}
