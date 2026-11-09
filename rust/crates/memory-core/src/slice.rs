//! Checked byte-range access shared by future offset code.
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

/// A byte range outside the supplied buffer (including inverted ranges).
#[derive(
    Debug, Clone, Copy, PartialEq, Eq, thiserror::Error, Serialize, Deserialize, JsonSchema,
)]
#[serde(deny_unknown_fields)]
#[error("byte range out of bounds")]
pub struct RangeError;

/// Return a checked byte range without panicking.
///
/// # Errors
/// Returns [`RangeError`] for an inverted or out-of-bounds range.
pub fn bytes(buffer: &[u8], range: std::ops::Range<usize>) -> Result<&[u8], RangeError> {
    buffer.get(range).ok_or(RangeError)
}
