//! Builders shared by the reader tests.
use sha2::{Digest, Sha256};

pub type R<T = ()> = Result<T, String>;

pub fn hex(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
pub fn enc(raw: &[u8]) -> R<Vec<u8>> {
    zstd::bulk::compress(raw, 3).map_err(|e| e.to_string())
}

/// A descriptor of `raw` at `offset` whose stored bytes are `stored`.
pub fn piece_json(raw: &[u8], offset: u64, stored: &[u8]) -> serde_json::Value {
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
pub fn piece(raw: &[u8], offset: u64, stored: &[u8]) -> R<memory_core::manifest::Piece> {
    serde_json::from_value(piece_json(raw, offset, stored)).map_err(|e| e.to_string())
}
