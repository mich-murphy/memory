use crate::domain::{CommitOutcome, OwnerParkReason};

/// Closed failure reasons. Never carries input text (parser messages can echo secrets).
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum ManifestError {
    /// Not valid JSON of the expected shape.
    #[error("malformed document")]
    Malformed,
    /// A JSON object repeats a key.
    #[error("duplicate key")]
    DuplicateKey,
    /// Schema string of this family that is not understood and not newer.
    #[error("unknown schema")]
    UnknownSchema,
    /// Schema string of a newer version of this family.
    #[error("newer schema")]
    NewerSchema,
    /// `seq` inconsistent with the manifest kind or the chain.
    #[error("bad seq")]
    BadSeq,
    /// `previous` or `checkpoint` link inconsistent.
    #[error("bad link")]
    BadLink,
    /// Timestamp is not `YYYY-MM-DDTHH:MM:SSZ`.
    #[error("bad timestamp")]
    BadTimestamp,
    /// Token text empty, too long or not printable ASCII.
    #[error("bad text")]
    BadText,
    /// Source path empty, absolute, traversing or too long.
    #[error("bad path")]
    BadPath,
    /// Launch record count, digest or uniqueness violation.
    #[error("bad launch")]
    BadLaunch,
    /// Piece with zero length or zero encoded length, or an empty extend.
    #[error("bad piece")]
    BadPiece,
    /// Object key is not `objects/<piece sha256>.zst`.
    #[error("bad object key")]
    BadObjectKey,
    /// Piece ranges leave a gap.
    #[error("piece gap")]
    Gap,
    /// Piece ranges overlap.
    #[error("piece overlap")]
    Overlap,
    /// Offset arithmetic overflow.
    #[error("offset overflow")]
    Overflow,
    /// Operation does not match the source class.
    #[error("op/class mismatch")]
    OpClassMismatch,
    /// `size` is not the end of the last piece.
    #[error("size mismatch")]
    SizeMismatch,
    /// Duplicate (or, in checkpoints, unsorted) source ids.
    #[error("duplicate source")]
    DuplicateSource,
    /// Epoch numbering or stamp inconsistent.
    #[error("bad epoch")]
    BadEpoch,
    /// Change references a source with no state.
    #[error("missing source")]
    MissingSource,
    /// Change not legal for a deleted (or already deleted) source.
    #[error("source deleted")]
    SourceDeleted,
    /// Kind, path or class of an existing source changed.
    #[error("source identity changed")]
    SourceChanged,
    /// A reader chain longer than 64 manifests.
    #[error("too many manifest steps")]
    TooManySteps,
    /// Manifests of one chain name different owners.
    #[error("owner mismatch")]
    OwnerMismatch,
    /// Chain does not start with a checkpoint followed only by deltas.
    #[error("broken chain")]
    BrokenChain,
    /// Piece object not present in the store model.
    #[error("missing object")]
    MissingObject,
    /// Descriptor differs from the encoding stored under its key (R2).
    #[error("encoding mismatch")]
    EncodingMismatch,
    /// Serialization failed.
    #[error("encode failure")]
    Encode,
}

impl ManifestError {
    /// Owner park reason for an error that must park rather than retry.
    pub fn park_reason(&self) -> Option<OwnerParkReason> {
        matches!(self, Self::NewerSchema).then_some(OwnerParkReason::NewerRemoteSchema)
    }
    /// Commit outcome for a verification GET whose manifest cannot be understood.
    pub fn commit_outcome(&self) -> Option<CommitOutcome> {
        matches!(self, Self::NewerSchema).then_some(CommitOutcome::NewerSchema)
    }
}
