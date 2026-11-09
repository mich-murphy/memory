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
//! schema dispatch, structural validation) rather than raw `serde_json` on untrusted bytes.
use crate::domain::{
    CommitOutcome, EpochPosition, Offset, Owner, OwnerParkReason, RulesDigest, Sha256, SourceClass,
    SourceId, SourceKind,
};
use crate::privacy::SanitizedLaunch;
use schemars::JsonSchema;
use serde::de::{self, DeserializeOwned, MapAccess, SeqAccess, Visitor};
use serde::{Deserialize, Deserializer, Serialize};
use sha2::{Digest, Sha256 as Hasher};
use std::collections::{BTreeMap, BTreeSet, HashSet};

/// Every manifest whose `seq` is a multiple of this is a checkpoint.
pub const CHECKPOINT_INTERVAL: u64 = 64;
const DUPLICATE: &str = "duplicate key";

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

/// SHA-256 of serialized manifest bytes: the manifest's identity and request id.
pub fn manifest_hash(bytes: &[u8]) -> Sha256 {
    Sha256::from_bytes(Hasher::digest(bytes).into())
}

// ---------------------------------------------------------------- duplicate keys

struct NoDup;
struct NoDupVisitor;
impl<'de> Deserialize<'de> for NoDup {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        deserializer.deserialize_any(NoDupVisitor)
    }
}
impl<'de> Visitor<'de> for NoDupVisitor {
    type Value = NoDup;
    fn expecting(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("any JSON value")
    }
    fn visit_bool<E: de::Error>(self, _: bool) -> Result<NoDup, E> {
        Ok(NoDup)
    }
    fn visit_i64<E: de::Error>(self, _: i64) -> Result<NoDup, E> {
        Ok(NoDup)
    }
    fn visit_u64<E: de::Error>(self, _: u64) -> Result<NoDup, E> {
        Ok(NoDup)
    }
    fn visit_f64<E: de::Error>(self, _: f64) -> Result<NoDup, E> {
        Ok(NoDup)
    }
    fn visit_str<E: de::Error>(self, _: &str) -> Result<NoDup, E> {
        Ok(NoDup)
    }
    fn visit_unit<E: de::Error>(self) -> Result<NoDup, E> {
        Ok(NoDup)
    }
    fn visit_seq<A: SeqAccess<'de>>(self, mut seq: A) -> Result<NoDup, A::Error> {
        while seq.next_element::<NoDup>()?.is_some() {}
        Ok(NoDup)
    }
    fn visit_map<A: MapAccess<'de>>(self, mut map: A) -> Result<NoDup, A::Error> {
        let mut seen = BTreeSet::new();
        while let Some(key) = map.next_key::<String>()? {
            if !seen.insert(key) {
                return Err(de::Error::custom(DUPLICATE));
            }
            map.next_value::<NoDup>()?;
        }
        Ok(NoDup)
    }
}

/// Reject any JSON object (at any depth) that repeats a key, and trailing bytes.
///
/// # Errors
/// [`ManifestError::DuplicateKey`] or [`ManifestError::Malformed`].
pub fn check_no_duplicate_keys(bytes: &[u8]) -> Result<(), ManifestError> {
    let mut de = serde_json::Deserializer::from_slice(bytes);
    NoDup::deserialize(&mut de).map_err(|e| {
        if e.to_string().starts_with(DUPLICATE) {
            ManifestError::DuplicateKey
        } else {
            ManifestError::Malformed
        }
    })?;
    de.end().map_err(|_| ManifestError::Malformed)
}

fn decode<T: DeserializeOwned>(bytes: &[u8]) -> Result<T, ManifestError> {
    serde_json::from_slice(bytes).map_err(|_| ManifestError::Malformed)
}

// ---------------------------------------------------------------- schema dispatch

/// How a schema string relates to the version this build understands.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SchemaStatus {
    /// Exactly the supported version.
    Supported,
    /// A later version of the same family.
    Newer,
    /// Anything else.
    Unknown,
}

/// Classify `schema` against `family` (for example `memory.manifest/`), version 1 supported.
pub fn classify_schema(family: &str, schema: &str) -> SchemaStatus {
    let Some(version) = schema.strip_prefix(family) else {
        return SchemaStatus::Unknown;
    };
    if version.is_empty()
        || version.starts_with('0')
        || !version.bytes().all(|b| b.is_ascii_digit())
    {
        SchemaStatus::Unknown
    } else if version == "1" {
        SchemaStatus::Supported
    } else {
        SchemaStatus::Newer
    }
}

#[derive(Deserialize)]
struct Envelope {
    schema: String,
}

fn preflight(bytes: &[u8], family: &str) -> Result<(), ManifestError> {
    check_no_duplicate_keys(bytes)?;
    let envelope: Envelope = decode(bytes)?;
    match classify_schema(family, &envelope.schema) {
        SchemaStatus::Supported => Ok(()),
        SchemaStatus::Newer => Err(ManifestError::NewerSchema),
        SchemaStatus::Unknown => Err(ManifestError::UnknownSchema),
    }
}

// ---------------------------------------------------------------- leaf types

/// UTC timestamp `YYYY-MM-DDTHH:MM:SSZ` (second precision, validated shape).
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
#[serde(try_from = "String", into = "String")]
pub struct Timestamp(String);
impl TryFrom<String> for Timestamp {
    type Error = ManifestError;
    fn try_from(value: String) -> Result<Self, ManifestError> {
        let raw =
            <[u8; 20]>::try_from(value.as_bytes()).map_err(|_| ManifestError::BadTimestamp)?;
        let [
            y1,
            y2,
            y3,
            y4,
            b'-',
            m1,
            m2,
            b'-',
            d1,
            d2,
            b'T',
            h1,
            h2,
            b':',
            i1,
            i2,
            b':',
            s1,
            s2,
            b'Z',
        ] = raw
        else {
            return Err(ManifestError::BadTimestamp);
        };
        let two = |a: u8, b: u8| -> Option<u32> {
            Some(char::from(a).to_digit(10)? * 10 + char::from(b).to_digit(10)?)
        };
        let ok = [y1, y2, y3, y4].iter().all(u8::is_ascii_digit)
            && matches!(two(m1, m2), Some(1..=12))
            && matches!(two(d1, d2), Some(1..=31))
            && matches!(two(h1, h2), Some(0..=23))
            && matches!(two(i1, i2), Some(0..=59))
            && matches!(two(s1, s2), Some(0..=59));
        if ok {
            Ok(Self(value))
        } else {
            Err(ManifestError::BadTimestamp)
        }
    }
}
impl From<Timestamp> for String {
    fn from(value: Timestamp) -> Self {
        value.0
    }
}
impl JsonSchema for Timestamp {
    fn schema_name() -> std::borrow::Cow<'static, str> {
        "Timestamp".into()
    }
    fn json_schema(_generator: &mut schemars::SchemaGenerator) -> schemars::Schema {
        schemars::json_schema!({"type": "string", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"})
    }
}

fn token(value: &str, max: usize) -> Result<(), ManifestError> {
    if value.is_empty() || value.len() > max || !value.bytes().all(|b| b.is_ascii_graphic()) {
        Err(ManifestError::BadText)
    } else {
        Ok(())
    }
}

fn check_path(path: &str) -> Result<(), ManifestError> {
    if path.is_empty()
        || path.len() > 1024
        || path.starts_with('/')
        || path.contains('\\')
        || path.chars().any(char::is_control)
        || path
            .split('/')
            .any(|c| c.is_empty() || c == "." || c == "..")
    {
        Err(ManifestError::BadPath)
    } else {
        Ok(())
    }
}

/// Manifest writer stamp.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Writer {
    /// Program name.
    pub name: String,
    /// Program version.
    pub version: String,
    /// Source revision.
    pub commit: String,
}
impl Writer {
    fn validate(&self) -> Result<(), ManifestError> {
        token(&self.name, 64)?;
        token(&self.version, 64)?;
        token(&self.commit, 64)
    }
}

/// Writer stamp of one epoch.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct EpochWriter {
    /// Program version.
    pub version: String,
    /// Source revision.
    pub commit: String,
}

/// Epoch number and the stamp of the writer that started it.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct EpochStamp {
    /// Epoch number.
    pub n: u32,
    /// Writer that started the epoch.
    pub writer: EpochWriter,
    /// Transform rules digest, present for protected kinds.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rules: Option<RulesDigest>,
}
impl EpochStamp {
    fn validate(&self) -> Result<(), ManifestError> {
        token(&self.writer.version, 64)?;
        token(&self.writer.commit, 64)
    }
}

/// Stored encoding of a piece. A zstd upgrade changes encoded bytes, never the object key.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub enum Encoding {
    /// Zstandard level 3.
    #[serde(rename = "zstd-3")]
    Zstd3,
}

/// Encoded size and digest actually stored under an object key (the dedup winner).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct StoredEncoding {
    /// Stored byte length.
    pub encoded_len: u64,
    /// SHA-256 of the stored bytes.
    pub encoded_sha256: Sha256,
}

/// Object key `objects/<sha256 of raw piece bytes>.zst`.
pub fn object_key(raw: &Sha256) -> String {
    format!("objects/{}.zst", String::from(*raw))
}

/// One contiguous byte range of transformed content (not line aligned).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Piece {
    /// Start offset in the transformed stream.
    pub offset: Offset,
    /// Length in bytes.
    pub len: u64,
    /// SHA-256 of the raw (transformed, unencoded) piece bytes.
    pub sha256: Sha256,
    /// Object key, `objects/<sha256>.zst`.
    pub object: String,
    /// Encoding of the stored object.
    pub encoding: Encoding,
    /// Stored length.
    pub encoded_len: u64,
    /// SHA-256 of the stored bytes.
    pub encoded_sha256: Sha256,
}
impl Piece {
    /// Describe a piece with the canonical object key and encoding.
    pub fn new(
        offset: Offset,
        len: u64,
        sha256: Sha256,
        encoded_len: u64,
        encoded_sha256: Sha256,
    ) -> Self {
        Self {
            offset,
            len,
            sha256,
            object: object_key(&sha256),
            encoding: Encoding::Zstd3,
            encoded_len,
            encoded_sha256,
        }
    }
    /// Replace the encoding fields by the encoding that won the `If-None-Match` race (R2).
    #[must_use]
    pub fn adopt_stored(mut self, stored: &StoredEncoding) -> Self {
        self.encoded_len = stored.encoded_len;
        self.encoded_sha256 = stored.encoded_sha256;
        self
    }
    fn validate(&self) -> Result<Offset, ManifestError> {
        if self.len == 0 || self.encoded_len == 0 {
            return Err(ManifestError::BadPiece);
        }
        if self.object != object_key(&self.sha256) {
            return Err(ManifestError::BadObjectKey);
        }
        self.offset
            .checked_add(self.len)
            .map_err(|_| ManifestError::Overflow)
    }
}

/// Validate contiguity and return the end offset (0 for no pieces).
fn check_run(pieces: &[Piece], from_zero: bool) -> Result<u64, ManifestError> {
    let mut end: Option<u64> = None;
    for piece in pieces {
        let piece_end = piece.validate()?.get();
        match end {
            None if from_zero && piece.offset.get() != 0 => return Err(ManifestError::Gap),
            None => {}
            Some(previous) => match piece.offset.get().cmp(&previous) {
                std::cmp::Ordering::Less => return Err(ManifestError::Overlap),
                std::cmp::Ordering::Greater => return Err(ManifestError::Gap),
                std::cmp::Ordering::Equal => {}
            },
        }
        end = Some(piece_end);
    }
    Ok(end.unwrap_or(0))
}

/// Sanitized launch evidence attached once per distinct digest.
///
/// Constructible only from [`SanitizedLaunch`]; fields are private.
/// ```compile_fail
/// let _ = memory_core::manifest::LaunchRecord {
///     digest: memory_core::domain::Sha256::from_bytes([0; 32]),
///     argv: vec![],
///     env: Default::default(),
/// };
/// ```
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct LaunchRecord {
    digest: Sha256,
    argv: Vec<String>,
    env: BTreeMap<String, String>,
}
impl LaunchRecord {
    /// Record sanitized evidence with its content digest.
    ///
    /// # Errors
    /// [`ManifestError::Encode`] if the digest input cannot be serialized.
    pub fn from_sanitized(launch: &SanitizedLaunch) -> Result<Self, ManifestError> {
        let argv = launch.argv().to_vec();
        let env = launch.env().clone();
        Ok(Self {
            digest: launch_digest(&argv, &env)?,
            argv,
            env,
        })
    }
    /// Content digest.
    pub fn digest(&self) -> Sha256 {
        self.digest
    }
    /// Sanitized argv.
    pub fn argv(&self) -> &[String] {
        &self.argv
    }
    /// Allowlisted environment.
    pub fn env(&self) -> &BTreeMap<String, String> {
        &self.env
    }
    fn validate(&self) -> Result<(), ManifestError> {
        if self.argv.len() > 32
            || self.env.len() > 8
            || launch_digest(&self.argv, &self.env)? != self.digest
        {
            return Err(ManifestError::BadLaunch);
        }
        Ok(())
    }
}
fn launch_digest(argv: &[String], env: &BTreeMap<String, String>) -> Result<Sha256, ManifestError> {
    serde_json::to_vec(&(argv, env))
        .map(|bytes| manifest_hash(&bytes))
        .map_err(|_| ManifestError::Encode)
}
fn check_header(writer: &Writer, launch: &[LaunchRecord]) -> Result<(), ManifestError> {
    writer.validate()?;
    let mut seen = HashSet::new();
    for record in launch {
        record.validate()?;
        if !seen.insert(record.digest) {
            return Err(ManifestError::BadLaunch);
        }
    }
    Ok(())
}

// ---------------------------------------------------------------- changes

/// Full description of one source write (the payload of `extend`, `epoch` and `replace`).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct SourceWrite {
    /// Source id.
    pub id: SourceId,
    /// Source kind.
    pub kind: SourceKind,
    /// Root-relative path.
    pub path: String,
    /// Source class.
    pub class: SourceClass,
    /// Epoch stamp.
    pub epoch: EpochStamp,
    /// Size of the whole transformed stream `[0, size)` after this change.
    pub size: u64,
    /// SHA-256 of the whole transformed stream `[0, size)`.
    pub sha256: Sha256,
    /// New pieces, contiguous, ending at `size`.
    pub pieces: Vec<Piece>,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Op {
    Extend,
    Epoch,
    Replace,
}

impl SourceWrite {
    fn check(&self, op: Op) -> Result<(), ManifestError> {
        check_path(&self.path)?;
        self.epoch.validate()?;
        match (op, &self.class) {
            (Op::Replace, SourceClass::Snapshot)
            | (Op::Extend | Op::Epoch, SourceClass::Append) => {}
            _ => return Err(ManifestError::OpClassMismatch),
        }
        if op == Op::Extend && self.pieces.is_empty() {
            return Err(ManifestError::BadPiece);
        }
        if check_run(&self.pieces, op != Op::Extend)? != self.size {
            return Err(ManifestError::SizeMismatch);
        }
        Ok(())
    }
    fn record(&self) -> EpochRecord {
        EpochRecord {
            epoch: self.epoch.clone(),
            size: self.size,
            sha256: self.sha256,
            pieces: self.pieces.clone(),
        }
    }
    fn entry(&self) -> SourceEntry {
        SourceEntry {
            kind: self.kind.clone(),
            path: self.path.clone(),
            class: self.class.clone(),
            epochs: vec![self.record()],
            deleted_at: None,
        }
    }
}

/// One change of a delta manifest.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(tag = "op", rename_all = "snake_case", deny_unknown_fields)]
pub enum Change {
    /// Append pieces to the current epoch (append class).
    Extend(SourceWrite),
    /// Start epoch n+1 from offset 0 (append class).
    Epoch(SourceWrite),
    /// Snapshot epoch replacing the previous one (snapshot class).
    Replace(SourceWrite),
    /// Record a deletion.
    Deleted {
        /// Source id.
        id: SourceId,
        /// Source kind.
        kind: SourceKind,
        /// Deletion time.
        deleted_at: Timestamp,
    },
}
impl Change {
    /// Id of the affected source.
    pub fn id(&self) -> &SourceId {
        match self {
            Self::Extend(w) | Self::Epoch(w) | Self::Replace(w) => &w.id,
            Self::Deleted { id, .. } => id,
        }
    }
    fn validate(&self) -> Result<(), ManifestError> {
        match self {
            Self::Extend(w) => w.check(Op::Extend),
            Self::Epoch(w) => w.check(Op::Epoch),
            Self::Replace(w) => w.check(Op::Replace),
            Self::Deleted { .. } => Ok(()),
        }
    }
}

/// A retained epoch with all of its pieces (checkpoint and state form).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct EpochRecord {
    /// Epoch stamp.
    pub epoch: EpochStamp,
    /// Size of the epoch's whole transformed stream.
    pub size: u64,
    /// SHA-256 of the epoch's whole transformed stream.
    pub sha256: Sha256,
    /// All pieces from offset 0, contiguous, ending at `size`.
    pub pieces: Vec<Piece>,
}
impl EpochRecord {
    fn validate(&self) -> Result<(), ManifestError> {
        self.epoch.validate()?;
        if check_run(&self.pieces, true)? != self.size {
            return Err(ManifestError::SizeMismatch);
        }
        Ok(())
    }
}

/// One source in a checkpoint.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct CheckpointSource {
    /// Source id.
    pub id: SourceId,
    /// Source kind.
    pub kind: SourceKind,
    /// Root-relative path.
    pub path: String,
    /// Source class.
    pub class: SourceClass,
    /// Every append epoch, or exactly the latest snapshot epoch.
    pub epochs: Vec<EpochRecord>,
    /// Deletion time if the source was deleted and not since recreated.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub deleted_at: Option<Timestamp>,
}
impl CheckpointSource {
    fn validate(&self) -> Result<(), ManifestError> {
        check_path(&self.path)?;
        if self.epochs.is_empty() || (self.class == SourceClass::Snapshot && self.epochs.len() != 1)
        {
            return Err(ManifestError::BadEpoch);
        }
        let mut last: Option<u32> = None;
        for record in &self.epochs {
            record.validate()?;
            if let Some(n) = last
                && n.checked_add(1) != Some(record.epoch.n)
            {
                return Err(ManifestError::BadEpoch);
            }
            last = Some(record.epoch.n);
        }
        Ok(())
    }
}

// ---------------------------------------------------------------- manifests

/// Schema tag of `memory.manifest/1`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub enum ManifestSchema {
    /// Version 1.
    #[serde(rename = "memory.manifest/1")]
    V1,
}

/// Delta manifest: only what changed since `previous`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Delta {
    /// Schema tag.
    pub schema: ManifestSchema,
    /// Chain owner.
    pub owner: Owner,
    /// Sequence number, never a multiple of 64.
    pub seq: u64,
    /// Hash of manifest `seq - 1`.
    pub previous: Sha256,
    /// Hash of the latest checkpoint manifest (`seq - seq % 64`).
    pub checkpoint: Sha256,
    /// Capture time.
    pub captured_at: Timestamp,
    /// Writer stamp.
    pub writer: Writer,
    /// Launch evidence.
    pub launch: Vec<LaunchRecord>,
    /// Changes, at most one per source.
    pub changes: Vec<Change>,
}
impl Delta {
    fn validate(&self) -> Result<(), ManifestError> {
        if self.seq.is_multiple_of(CHECKPOINT_INTERVAL) {
            return Err(ManifestError::BadSeq);
        }
        check_header(&self.writer, &self.launch)?;
        let mut seen = HashSet::new();
        for change in &self.changes {
            change.validate()?;
            if !seen.insert(change.id()) {
                return Err(ManifestError::DuplicateSource);
            }
        }
        Ok(())
    }
}

/// Checkpoint manifest: every retained epoch of every source.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Checkpoint {
    /// Schema tag.
    pub schema: ManifestSchema,
    /// Chain owner.
    pub owner: Owner,
    /// Sequence number, a multiple of 64.
    pub seq: u64,
    /// Hash of manifest `seq - 1`; absent only at seq 0.
    pub previous: Option<Sha256>,
    /// Capture time.
    pub captured_at: Timestamp,
    /// Writer stamp.
    pub writer: Writer,
    /// Launch evidence.
    pub launch: Vec<LaunchRecord>,
    /// Sources sorted by id, unique.
    pub sources: Vec<CheckpointSource>,
}
impl Checkpoint {
    fn validate(&self) -> Result<(), ManifestError> {
        if !self.seq.is_multiple_of(CHECKPOINT_INTERVAL) {
            return Err(ManifestError::BadSeq);
        }
        if (self.seq == 0) != self.previous.is_none() {
            return Err(ManifestError::BadLink);
        }
        check_header(&self.writer, &self.launch)?;
        let mut last: Option<&SourceId> = None;
        for source in &self.sources {
            if last.is_some_and(|l| *l >= source.id) {
                return Err(ManifestError::DuplicateSource);
            }
            last = Some(&source.id);
            source.validate()?;
        }
        Ok(())
    }
}

/// A manifest, `memory.manifest/1`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum Manifest {
    /// Delta.
    Delta(Delta),
    /// Checkpoint.
    Checkpoint(Checkpoint),
}
impl Manifest {
    /// Sequence number.
    pub fn seq(&self) -> u64 {
        match self {
            Self::Delta(d) => d.seq,
            Self::Checkpoint(c) => c.seq,
        }
    }
    /// Chain owner.
    pub fn owner(&self) -> &Owner {
        match self {
            Self::Delta(d) => &d.owner,
            Self::Checkpoint(c) => &c.owner,
        }
    }
    /// Structural validation (everything checkable without the chain).
    ///
    /// # Errors
    /// The first violated rule.
    pub fn validate(&self) -> Result<(), ManifestError> {
        match self {
            Self::Delta(d) => d.validate(),
            Self::Checkpoint(c) => c.validate(),
        }
    }
    /// Canonical serialization (struct field order, sorted maps, no whitespace).
    ///
    /// # Errors
    /// [`ManifestError::Encode`].
    pub fn to_bytes(&self) -> Result<Vec<u8>, ManifestError> {
        serde_json::to_vec(self).map_err(|_| ManifestError::Encode)
    }
    fn pieces(&self) -> Vec<&Piece> {
        match self {
            Self::Delta(d) => d
                .changes
                .iter()
                .flat_map(|c| match c {
                    Change::Extend(w) | Change::Epoch(w) | Change::Replace(w) => w.pieces.iter(),
                    Change::Deleted { .. } => [].iter(),
                })
                .collect(),
            Self::Checkpoint(c) => c
                .sources
                .iter()
                .flat_map(|s| s.epochs.iter().flat_map(|e| e.pieces.iter()))
                .collect(),
        }
    }
    /// Check every descriptor against the encoding stored under its key (R2).
    ///
    /// # Errors
    /// [`ManifestError::MissingObject`] or [`ManifestError::EncodingMismatch`].
    pub fn verify_encodings(
        &self,
        stored: impl Fn(&str) -> Option<StoredEncoding>,
    ) -> Result<(), ManifestError> {
        for piece in self.pieces() {
            let winner = stored(&piece.object).ok_or(ManifestError::MissingObject)?;
            if winner.encoded_len != piece.encoded_len
                || winner.encoded_sha256 != piece.encoded_sha256
            {
                return Err(ManifestError::EncodingMismatch);
            }
        }
        Ok(())
    }
}

/// Parse and strictly validate a manifest: duplicate keys, schema dispatch, shape, rules.
///
/// # Errors
/// [`ManifestError::NewerSchema`] / [`ManifestError::UnknownSchema`] before anything else
/// about the shape is read; otherwise the first violated rule.
pub fn parse_manifest(bytes: &[u8]) -> Result<Manifest, ManifestError> {
    preflight(bytes, "memory.manifest/")?;
    let manifest: Manifest = decode(bytes)?;
    manifest.validate()?;
    Ok(manifest)
}

// ---------------------------------------------------------------- current

/// Schema tag of `memory.current/1`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub enum CurrentSchema {
    /// Version 1.
    #[serde(rename = "memory.current/1")]
    V1,
}

/// The commit pointer `current.json`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Current {
    /// Schema tag.
    pub schema: CurrentSchema,
    /// Hash of the current manifest.
    pub manifest: Sha256,
    /// Its sequence number.
    pub seq: u64,
    /// Update time.
    pub updated_at: Timestamp,
}
impl Current {
    /// Pointer to `manifest` at `seq`.
    pub fn new(manifest: Sha256, seq: u64, updated_at: Timestamp) -> Self {
        Self {
            schema: CurrentSchema::V1,
            manifest,
            seq,
            updated_at,
        }
    }
    /// Canonical serialization.
    ///
    /// # Errors
    /// [`ManifestError::Encode`].
    pub fn to_bytes(&self) -> Result<Vec<u8>, ManifestError> {
        serde_json::to_vec(self).map_err(|_| ManifestError::Encode)
    }
}
/// Parse `current.json` with duplicate-key and schema dispatch.
///
/// # Errors
/// Typed schema errors first, then [`ManifestError::Malformed`].
pub fn parse_current(bytes: &[u8]) -> Result<Current, ManifestError> {
    preflight(bytes, "memory.current/")?;
    decode(bytes)
}

// ---------------------------------------------------------------- owner state

/// Retained state of one source.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SourceEntry {
    /// Source kind.
    pub kind: SourceKind,
    /// Root-relative path.
    pub path: String,
    /// Source class.
    pub class: SourceClass,
    /// Append: every epoch. Snapshot: the latest only.
    pub epochs: Vec<EpochRecord>,
    /// Deletion time, cleared when a new epoch starts.
    pub deleted_at: Option<Timestamp>,
}
impl SourceEntry {
    fn same_identity(&self, w: &SourceWrite) -> Result<(), ManifestError> {
        if self.kind == w.kind && self.path == w.path && self.class == w.class {
            Ok(())
        } else {
            Err(ManifestError::SourceChanged)
        }
    }
}

/// Retained state of an owner: the fold of a checkpoint and the deltas after it.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct OwnerState {
    /// Sources by id.
    pub sources: BTreeMap<SourceId, SourceEntry>,
}
impl OwnerState {
    /// State listed by a checkpoint.
    pub fn from_checkpoint(checkpoint: &Checkpoint) -> Self {
        Self {
            sources: checkpoint
                .sources
                .iter()
                .map(|s| {
                    (
                        s.id.clone(),
                        SourceEntry {
                            kind: s.kind.clone(),
                            path: s.path.clone(),
                            class: s.class.clone(),
                            epochs: s.epochs.clone(),
                            deleted_at: s.deleted_at.clone(),
                        },
                    )
                })
                .collect(),
        }
    }
    /// Checkpoint listing of this state, sorted by id.
    pub fn to_checkpoint_sources(&self) -> Vec<CheckpointSource> {
        self.sources
            .iter()
            .map(|(id, e)| CheckpointSource {
                id: id.clone(),
                kind: e.kind.clone(),
                path: e.path.clone(),
                class: e.class.clone(),
                epochs: e.epochs.clone(),
                deleted_at: e.deleted_at.clone(),
            })
            .collect()
    }
    /// Apply one manifest's changes atomically (state unchanged on error).
    ///
    /// # Errors
    /// Any structural or state-transition violation.
    pub fn apply_changes(&mut self, changes: &[Change]) -> Result<(), ManifestError> {
        let mut seen = HashSet::new();
        for change in changes {
            change.validate()?;
            if !seen.insert(change.id()) {
                return Err(ManifestError::DuplicateSource);
            }
        }
        let mut next = self.clone();
        for change in changes {
            next.apply_one(change)?;
        }
        *self = next;
        Ok(())
    }
    fn apply_one(&mut self, change: &Change) -> Result<(), ManifestError> {
        match change {
            Change::Extend(w) => {
                let entry = self
                    .sources
                    .get_mut(&w.id)
                    .ok_or(ManifestError::MissingSource)?;
                entry.same_identity(w)?;
                if entry.deleted_at.is_some() {
                    return Err(ManifestError::SourceDeleted);
                }
                let last = entry.epochs.last_mut().ok_or(ManifestError::BadEpoch)?;
                if last.epoch != w.epoch {
                    return Err(ManifestError::BadEpoch);
                }
                let first = w.pieces.first().ok_or(ManifestError::BadPiece)?;
                match first.offset.get().cmp(&last.size) {
                    std::cmp::Ordering::Less => return Err(ManifestError::Overlap),
                    std::cmp::Ordering::Greater => return Err(ManifestError::Gap),
                    std::cmp::Ordering::Equal => {}
                }
                last.size = w.size;
                last.sha256 = w.sha256;
                last.pieces.extend(w.pieces.iter().cloned());
                Ok(())
            }
            Change::Epoch(w) | Change::Replace(w) => {
                let replace = matches!(change, Change::Replace(_));
                match self.sources.get_mut(&w.id) {
                    None => {
                        self.sources.insert(w.id.clone(), w.entry());
                    }
                    Some(entry) => {
                        entry.same_identity(w)?;
                        let next = entry
                            .epochs
                            .last()
                            .and_then(|l| l.epoch.n.checked_add(1))
                            .ok_or(ManifestError::BadEpoch)?;
                        if w.epoch.n != next {
                            return Err(ManifestError::BadEpoch);
                        }
                        if replace {
                            entry.epochs.clear();
                        }
                        entry.epochs.push(w.record());
                        entry.deleted_at = None;
                    }
                }
                Ok(())
            }
            Change::Deleted {
                id,
                kind,
                deleted_at,
            } => {
                let entry = self
                    .sources
                    .get_mut(id)
                    .ok_or(ManifestError::MissingSource)?;
                if entry.kind != *kind {
                    return Err(ManifestError::SourceChanged);
                }
                if entry.deleted_at.is_some() {
                    return Err(ManifestError::SourceDeleted);
                }
                entry.deleted_at = Some(deleted_at.clone());
                Ok(())
            }
        }
    }
}

// ---------------------------------------------------------------- builder

/// Position of the latest committed manifest of a chain.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Position {
    /// Sequence number.
    pub seq: u64,
    /// Hash of this manifest.
    pub manifest: Sha256,
    /// Hash of the latest checkpoint at or before it.
    pub checkpoint: Sha256,
}

/// Fields of a manifest that do not depend on the chain position.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Header {
    /// Chain owner.
    pub owner: Owner,
    /// Capture time.
    pub captured_at: Timestamp,
    /// Writer stamp.
    pub writer: Writer,
    /// Pending launch records.
    pub launch: Vec<LaunchRecord>,
}

/// Result of [`build`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Built {
    /// Canonical bytes to PUT as `manifests/<hash>.json`.
    pub bytes: Vec<u8>,
    /// SHA-256 of `bytes`.
    pub hash: Sha256,
    /// The manifest.
    pub manifest: Manifest,
    /// Owner state after this manifest.
    pub state: OwnerState,
    /// Position after this manifest.
    pub position: Position,
}

/// Build the next manifest after `previous` (or the first, a checkpoint at seq 0).
///
/// `seq` is the previous seq plus one; a checkpoint when `seq % 64 == 0`, listing all
/// retained epochs of the state after the changes.
///
/// # Errors
/// Invalid changes, an invalid transition, or seq overflow.
pub fn build(
    previous: Option<(&Position, &OwnerState)>,
    header: Header,
    changes: Vec<Change>,
) -> Result<Built, ManifestError> {
    let (seq, mut state, link) = match previous {
        None => (0, OwnerState::default(), None),
        Some((p, s)) => (
            p.seq.checked_add(1).ok_or(ManifestError::Overflow)?,
            s.clone(),
            Some((p.manifest, p.checkpoint)),
        ),
    };
    state.apply_changes(&changes)?;
    let Header {
        owner,
        captured_at,
        writer,
        launch,
    } = header;
    let is_checkpoint = seq.is_multiple_of(CHECKPOINT_INTERVAL);
    let manifest = if is_checkpoint {
        Manifest::Checkpoint(Checkpoint {
            schema: ManifestSchema::V1,
            owner,
            seq,
            previous: link.map(|(m, _)| m),
            captured_at,
            writer,
            launch,
            sources: state.to_checkpoint_sources(),
        })
    } else {
        let (previous, checkpoint) = link.ok_or(ManifestError::BadLink)?;
        Manifest::Delta(Delta {
            schema: ManifestSchema::V1,
            owner,
            seq,
            previous,
            checkpoint,
            captured_at,
            writer,
            launch,
            changes,
        })
    };
    manifest.validate()?;
    let bytes = manifest.to_bytes()?;
    let hash = manifest_hash(&bytes);
    let checkpoint = match (is_checkpoint, link) {
        (false, Some((_, cp))) => cp,
        _ => hash,
    };
    Ok(Built {
        bytes,
        hash,
        manifest,
        state,
        position: Position {
            seq,
            manifest: hash,
            checkpoint,
        },
    })
}

// ---------------------------------------------------------------- fold

/// Result of folding a reader chain.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Folded {
    /// Chain owner.
    pub owner: Owner,
    /// Folded state.
    pub state: OwnerState,
    /// Position of the last manifest folded.
    pub position: Position,
}

/// Fold a checkpoint followed by consecutive deltas (oldest first, at most 64 manifests).
///
/// # Errors
/// Parse errors, link/seq/owner inconsistencies, [`ManifestError::TooManySteps`], or an
/// invalid state transition.
pub fn fold(chain: &[&[u8]]) -> Result<Folded, ManifestError> {
    if chain.len() as u64 > CHECKPOINT_INTERVAL {
        return Err(ManifestError::TooManySteps);
    }
    let mut raw = chain.iter();
    let first = raw.next().ok_or(ManifestError::BrokenChain)?;
    let Manifest::Checkpoint(checkpoint) = parse_manifest(first)? else {
        return Err(ManifestError::BrokenChain);
    };
    let checkpoint_hash = manifest_hash(first);
    let mut state = OwnerState::from_checkpoint(&checkpoint);
    let mut position = Position {
        seq: checkpoint.seq,
        manifest: checkpoint_hash,
        checkpoint: checkpoint_hash,
    };
    for bytes in raw {
        let Manifest::Delta(delta) = parse_manifest(bytes)? else {
            return Err(ManifestError::BrokenChain);
        };
        if delta.owner != checkpoint.owner {
            return Err(ManifestError::OwnerMismatch);
        }
        if position.seq.checked_add(1) != Some(delta.seq) {
            return Err(ManifestError::BadSeq);
        }
        if delta.previous != position.manifest || delta.checkpoint != checkpoint_hash {
            return Err(ManifestError::BadLink);
        }
        state.apply_changes(&delta.changes)?;
        position.seq = delta.seq;
        position.manifest = manifest_hash(bytes);
    }
    Ok(Folded {
        owner: checkpoint.owner,
        state,
        position,
    })
}

// ---------------------------------------------------------------- recovery projection

type Ignored = serde::de::IgnoredAny;
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "snake_case")]
enum RawOp {
    Extend,
    Epoch,
    Replace,
    Deleted,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RawEpoch {
    n: u32,
    #[serde(default)]
    rules: Option<RulesDigest>,
    #[expect(dead_code, reason = "allowed envelope field, not projected")]
    writer: Ignored,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[expect(dead_code, reason = "allowed envelope fields, not projected")]
struct RawChange {
    id: SourceId,
    op: RawOp,
    kind: Option<Ignored>,
    path: Option<Ignored>,
    class: Option<Ignored>,
    pieces: Option<Ignored>,
    deleted_at: Option<Ignored>,
    epoch: Option<RawEpoch>,
    size: Option<u64>,
    sha256: Option<Sha256>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[expect(dead_code, reason = "allowed envelope field, not projected")]
struct RawRecord {
    epoch: RawEpoch,
    size: u64,
    sha256: Sha256,
    pieces: Ignored,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[expect(dead_code, reason = "allowed envelope fields, not projected")]
struct RawSource {
    id: SourceId,
    kind: Ignored,
    path: Ignored,
    class: Ignored,
    epochs: Vec<RawRecord>,
    deleted_at: Option<String>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[expect(dead_code, reason = "allowed envelope fields, not projected")]
struct RawDelta {
    schema: Ignored,
    owner: Ignored,
    seq: u64,
    previous: Option<Sha256>,
    checkpoint: Option<Sha256>,
    captured_at: Ignored,
    writer: Ignored,
    launch: Ignored,
    changes: Vec<RawChange>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[expect(dead_code, reason = "allowed envelope fields, not projected")]
struct RawCheckpoint {
    schema: Ignored,
    owner: Ignored,
    seq: u64,
    previous: Option<Sha256>,
    captured_at: Ignored,
    writer: Ignored,
    launch: Ignored,
    sources: Vec<RawSource>,
}
/// Strict per-kind envelope: a delta may carry `changes` and never `sources`; a checkpoint
/// the reverse. Presence of the wrong field is an error whatever its length.
#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
enum RawProjection {
    Delta(RawDelta),
    Checkpoint(RawCheckpoint),
}

/// Kind of a projected manifest.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ProjectedKind {
    /// Delta.
    Delta,
    /// Checkpoint.
    Checkpoint,
}
/// Latest epoch position of one source, or `None` if the source was deleted.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ProjectedSource {
    /// Source id.
    pub id: SourceId,
    /// Latest epoch position.
    pub latest: Option<EpochPosition>,
}
/// Position fields of a remote manifest, as the writer's recovery path may read them.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Projection {
    /// Delta or checkpoint.
    pub kind: ProjectedKind,
    /// Sequence number.
    pub seq: u64,
    /// Previous manifest hash.
    pub previous: Option<Sha256>,
    /// Latest checkpoint hash (deltas only).
    pub checkpoint: Option<Sha256>,
    /// Changed sources (delta) or all sources (checkpoint).
    pub sources: Vec<ProjectedSource>,
}

fn position(epoch: &RawEpoch, size: u64, whole: Sha256) -> EpochPosition {
    EpochPosition {
        epoch: epoch.n,
        captured_to: Offset::new(size),
        whole,
        rules: epoch.rules,
    }
}

/// Recovery projection (R5): strict envelope (duplicate keys, schema dispatch, kind/seq
/// rule) and only the position fields; every other field, at any depth, is ignored. Strict
/// validation of everything else belongs to [`parse_manifest`].
///
/// # Errors
/// [`ManifestError::NewerSchema`] / [`ManifestError::UnknownSchema`] before the body is
/// read; otherwise [`ManifestError::Malformed`], [`ManifestError::BadSeq`] or
/// [`ManifestError::BadLink`].
pub fn recover_projection(bytes: &[u8]) -> Result<Projection, ManifestError> {
    preflight(bytes, "memory.manifest/")?;
    match decode::<RawProjection>(bytes)? {
        RawProjection::Checkpoint(c) => {
            if !c.seq.is_multiple_of(CHECKPOINT_INTERVAL) {
                return Err(ManifestError::BadSeq);
            }
            if (c.seq == 0) != c.previous.is_none() {
                return Err(ManifestError::BadLink);
            }
            let sources = c
                .sources
                .iter()
                .map(|s| {
                    let latest = match (&s.deleted_at, s.epochs.last()) {
                        (None, Some(r)) => Some(position(&r.epoch, r.size, r.sha256)),
                        (Some(_), Some(_)) => None,
                        (_, None) => return Err(ManifestError::Malformed),
                    };
                    Ok(ProjectedSource {
                        id: s.id.clone(),
                        latest,
                    })
                })
                .collect::<Result<Vec<_>, _>>()?;
            Ok(Projection {
                kind: ProjectedKind::Checkpoint,
                seq: c.seq,
                previous: c.previous,
                checkpoint: None,
                sources,
            })
        }
        RawProjection::Delta(d) => {
            if d.seq.is_multiple_of(CHECKPOINT_INTERVAL) {
                return Err(ManifestError::BadSeq);
            }
            if d.previous.is_none() || d.checkpoint.is_none() {
                return Err(ManifestError::BadLink);
            }
            let sources = d
                .changes
                .iter()
                .map(|c| {
                    let latest = match (c.op, &c.epoch, c.size, c.sha256) {
                        (RawOp::Deleted, _, _, _) => None,
                        (_, Some(e), Some(size), Some(whole)) => Some(position(e, size, whole)),
                        _ => return Err(ManifestError::Malformed),
                    };
                    Ok(ProjectedSource {
                        id: c.id.clone(),
                        latest,
                    })
                })
                .collect::<Result<Vec<_>, _>>()?;
            Ok(Projection {
                kind: ProjectedKind::Delta,
                seq: d.seq,
                previous: d.previous,
                checkpoint: d.checkpoint,
                sources,
            })
        }
    }
}
