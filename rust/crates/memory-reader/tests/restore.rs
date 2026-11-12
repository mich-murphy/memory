//! Piece decoding and single-epoch restore, against descriptors built from JSON (the remote
//! manifest path) and an independent SHA-256.
use memory_reader::{DecodeError, RestoreError, decode_piece, restore_epoch};
use sha2::{Digest, Sha256};

type R<T = ()> = Result<T, String>;

fn hex(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
fn enc(raw: &[u8]) -> R<Vec<u8>> {
    zstd::bulk::compress(raw, 3).map_err(|e| e.to_string())
}

/// A descriptor of `raw` at `offset` whose stored bytes are `stored`.
fn piece_json(raw: &[u8], offset: u64, stored: &[u8]) -> serde_json::Value {
    serde_json::json!({
        "offset": offset,
        "len": raw.len(),
        "sha256": hex(raw),
        "object": format!("objects/{}.zst", hex(raw)),
        "encoding": "zstd-3",
        "encoded_len": stored.len(),
        "encoded_sha256": hex(stored),
    })
}
fn piece(raw: &[u8], offset: u64, stored: &[u8]) -> R<memory_core::manifest::Piece> {
    serde_json::from_value(piece_json(raw, offset, stored)).map_err(|e| e.to_string())
}
fn epoch(whole: &[u8], pieces: &[serde_json::Value]) -> R<memory_core::manifest::EpochRecord> {
    serde_json::from_value(serde_json::json!({
        "epoch": {"n": 1, "writer": {"version": "v", "commit": "c"}},
        "size": whole.len(),
        "sha256": hex(whole),
        "pieces": pieces,
    }))
    .map_err(|e| e.to_string())
}
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

#[test]
fn restore_concatenates_pieces_and_verifies_the_epoch() -> R {
    let whole = data();
    let (a, b) = whole.split_at(300);
    let (sa, sb) = (enc(a)?, enc(b)?);
    let rec = epoch(&whole, &[piece_json(a, 0, &sa), piece_json(b, 300, &sb)])?;
    let got = restore_epoch(&rec, |key| -> Result<Vec<u8>, String> {
        if key.contains(&hex(a)) {
            Ok(sa.clone())
        } else if key.contains(&hex(b)) {
            Ok(sb.clone())
        } else {
            Err("unknown key".to_owned())
        }
    });
    assert_eq!(got, Ok(whole));
    Ok(())
}

#[test]
fn non_contiguous_pieces_are_rejected() -> R {
    let whole = data();
    let (a, b) = whole.split_at(300);
    let (sa, sb) = (enc(a)?, enc(b)?);
    let rec = epoch(&whole, &[piece_json(a, 0, &sa), piece_json(b, 301, &sb)])?;
    let got = restore_epoch(&rec, |key| -> Result<Vec<u8>, String> {
        Ok(if key.contains(&hex(a)) {
            sa.clone()
        } else {
            sb.clone()
        })
    });
    assert_eq!(got, Err(RestoreError::Gap));
    // A first piece that does not start at 0 is a gap too.
    let rec = epoch(&whole, &[piece_json(&whole, 1, &enc(&whole)?)])?;
    let stored = enc(&whole)?;
    assert_eq!(
        restore_epoch(&rec, |_| -> Result<Vec<u8>, String> { Ok(stored.clone()) }),
        Err(RestoreError::Gap)
    );
    Ok(())
}

#[test]
fn whole_stream_size_and_digest_are_checked() -> R {
    let whole = data();
    let stored = enc(&whole)?;
    let fetch = |_: &str| -> Result<Vec<u8>, String> { Ok(stored.clone()) };
    let pieces = [piece_json(&whole, 0, &stored)];
    let mut bad = serde_json::to_value(epoch(&whole, &pieces)?).map_err(|e| e.to_string())?;
    set(&mut bad, "sha256", hex(b"x").into())?;
    let rec: memory_core::manifest::EpochRecord =
        serde_json::from_value(bad).map_err(|e| e.to_string())?;
    assert_eq!(restore_epoch(&rec, fetch), Err(RestoreError::Digest));
    let mut bad = serde_json::to_value(epoch(&whole, &pieces)?).map_err(|e| e.to_string())?;
    set(&mut bad, "size", (whole.len() + 1).into())?;
    let rec: memory_core::manifest::EpochRecord =
        serde_json::from_value(bad).map_err(|e| e.to_string())?;
    assert_eq!(restore_epoch(&rec, fetch), Err(RestoreError::Size));
    Ok(())
}

#[test]
fn fetch_errors_pass_through() -> R {
    let whole = data();
    let stored = enc(&whole)?;
    let rec = epoch(&whole, &[piece_json(&whole, 0, &stored)])?;
    let got = restore_epoch(&rec, |_| -> Result<Vec<u8>, &'static str> { Err("boom") });
    assert_eq!(got, Err(RestoreError::Fetch("boom")));
    Ok(())
}
