//! Pure `memory.manifest/1` and `memory.current/1` (DESIGN.md sections 5, 6.3-6.5, 7.4).
//!
//! No I/O. Published digests and offsets are over the TRANSFORMED bytes (R1). A piece
//! descriptor must describe the encoding actually stored under its object key (R2, see
//! [`Piece::adopt_stored`] and [`Manifest::verify_encodings`]). Checkpoints are not bounded
//! in size by 64 pieces (R3); only the number of manifest GETs is bounded. Envelope dispatch
//! comes first (R5): a newer or unknown schema is a typed error, never a generic parse
//! failure, and [`recover_projection`] reads position fields only.
//!
//! Types deserialize with `deny_unknown_fields`; call [`parse_manifest`] (duplicate keys,

mod build;
mod error;
mod recovery;
mod schema;
mod state;

/// Every manifest whose `seq` is a multiple of this is a checkpoint.
pub const CHECKPOINT_INTERVAL: u64 = 64;

pub use build::{Built, Folded, Header, Position, build, fold};
pub use error::ManifestError;
pub use recovery::{ProjectedKind, ProjectedSource, Projection, recover_projection};
pub use schema::{
    Change, Checkpoint, CheckpointSource, Current, CurrentSchema, Delta, Encoding, EpochRecord,
    EpochStamp, EpochWriter, LaunchRecord, Manifest, ManifestSchema, Piece, SchemaStatus,
    SourceWrite, StoredEncoding, Timestamp, Writer, check_no_duplicate_keys, classify_schema,
    manifest_hash, object_key, parse_current, parse_manifest,
};
pub use state::{OwnerState, SourceEntry};
