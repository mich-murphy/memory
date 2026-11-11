//! The error for a byte range outside its buffer (a hint scan offset past the end).
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

/// A byte range outside the supplied buffer (including inverted ranges).
#[derive(
    Debug, Clone, Copy, PartialEq, Eq, thiserror::Error, Serialize, Deserialize, JsonSchema,
)]
#[serde(deny_unknown_fields)]
#[error("byte range out of bounds")]
pub struct RangeError;
