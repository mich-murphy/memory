use super::state::SourceEntry;
use super::{CHECKPOINT_INTERVAL, ManifestError};
use crate::domain::{Offset, Owner, RulesDigest, Sha256, SourceClass, SourceId, SourceKind};
use crate::limits::object_key;
use crate::privacy::{AuthorizedBody, SanitizedLaunch};
use schemars::JsonSchema;
use serde::de::{self, DeserializeOwned, MapAccess, SeqAccess, Visitor};
use serde::{Deserialize, Deserializer, Serialize};
use sha2::{Digest, Sha256 as Hasher};
use std::collections::{BTreeMap, BTreeSet, HashSet};

const DUPLICATE: &str = "duplicate key";

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

pub(super) fn decode<T: DeserializeOwned>(bytes: &[u8]) -> Result<T, ManifestError> {
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

pub(super) fn preflight(bytes: &[u8], family: &str) -> Result<(), ManifestError> {
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
    pub(super) fn validate(&self) -> Result<(), ManifestError> {
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
    pub(super) fn validate(&self) -> Result<(), ManifestError> {
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

/// One contiguous byte range of transformed content (not line aligned).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Piece {
    /// Start offset in the transformed stream.
    offset: Offset,
    /// Length in bytes.
    len: u64,
    /// SHA-256 of the raw (transformed, unencoded) piece bytes.
    sha256: Sha256,
    /// Object key, `objects/<sha256>.zst`.
    object: String,
    /// Encoding of the stored object.
    encoding: Encoding,
    /// Stored length.
    encoded_len: u64,
    /// SHA-256 of the stored bytes.
    encoded_sha256: Sha256,
}
/// Piece fields are private. Local construction is [`Piece::from_authorized`] only, which hashes
/// the bytes of an authorized body itself; the other path is deserialising a remote manifest.
/// ```compile_fail
/// let _ = memory_core::manifest::Piece::new;
/// ```
impl Piece {
    /// Describe `body[start..end]` of an authorized body whose first byte sits at stream offset
    /// `base`; the piece offset is `base + start`. The digests and length are computed
    /// here from the authorized bytes; the caller supplies no digest. Encoding is the identity
    /// until zstd lands, so the encoded fields equal the raw ones (see [`Piece::adopt_stored`]).
    ///
    /// # Errors
    /// [`ManifestError::BadPiece`] for an empty or out-of-range slice,
    /// [`ManifestError::Overflow`] if an offset does not fit in `u64`.
    pub fn from_authorized(
        body: &AuthorizedBody,
        base: Offset,
        start: usize,
        end: usize,
    ) -> Result<Self, ManifestError> {
        let chunk = body
            .bytes()
            .get(start..end)
            .filter(|c| !c.is_empty())
            .ok_or(ManifestError::BadPiece)?;
        let sha256 = manifest_hash(chunk);
        let len = u64::try_from(chunk.len()).map_err(|_| ManifestError::Overflow)?;
        let local = u64::try_from(start).map_err(|_| ManifestError::Overflow)?;
        let offset = base
            .checked_add(local)
            .map_err(|_| ManifestError::Overflow)?;
        Ok(Self {
            offset,
            len,
            sha256,
            object: object_key(&sha256),
            encoding: Encoding::Zstd3,
            encoded_len: len,
            encoded_sha256: sha256,
        })
    }
    /// Start offset in the transformed stream.
    pub fn offset(&self) -> Offset {
        self.offset
    }
    /// Length in bytes.
    pub fn len(&self) -> u64 {
        self.len
    }
    /// Pieces are never empty once validated.
    pub fn is_empty(&self) -> bool {
        self.len == 0
    }
    /// SHA-256 of the raw piece bytes.
    pub fn sha256(&self) -> Sha256 {
        self.sha256
    }
    /// Object key.
    pub fn object(&self) -> &str {
        &self.object
    }
    /// Encoding of the stored object.
    pub fn encoding(&self) -> Encoding {
        self.encoding
    }
    /// Stored length.
    pub fn encoded_len(&self) -> u64 {
        self.encoded_len
    }
    /// SHA-256 of the stored bytes.
    pub fn encoded_sha256(&self) -> Sha256 {
        self.encoded_sha256
    }
    /// Replace the encoding fields by the encoding that won the `If-None-Match` race (R2).
    #[must_use]
    pub fn adopt_stored(mut self, stored: &StoredEncoding) -> Self {
        self.encoded_len = stored.encoded_len;
        self.encoded_sha256 = stored.encoded_sha256;
        self
    }
    pub(super) fn validate(&self) -> Result<Offset, ManifestError> {
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
    pub(super) fn validate(&self) -> Result<(), ManifestError> {
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
    pub(super) fn record(&self) -> EpochRecord {
        EpochRecord {
            epoch: self.epoch.clone(),
            size: self.size,
            sha256: self.sha256,
            pieces: self.pieces.clone(),
        }
    }
    pub(super) fn entry(&self) -> SourceEntry {
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
    pub(super) fn validate(&self) -> Result<(), ManifestError> {
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
    pub(super) fn validate(&self) -> Result<(), ManifestError> {
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
    pub(super) fn validate(&self) -> Result<(), ManifestError> {
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
    schema: ManifestSchema,
    /// Chain owner.
    owner: Owner,
    /// Sequence number, never a multiple of 64.
    seq: u64,
    /// Hash of manifest `seq - 1`.
    previous: Sha256,
    /// Hash of the latest checkpoint manifest (`seq - seq % 64`).
    checkpoint: Sha256,
    /// Capture time.
    captured_at: Timestamp,
    /// Writer stamp.
    writer: Writer,
    /// Launch evidence.
    launch: Vec<LaunchRecord>,
    /// Changes, at most one per source.
    changes: Vec<Change>,
}
impl Delta {
    /// Assemble a delta; validated by the builder and by parsing, never by the caller.
    #[expect(
        clippy::too_many_arguments,
        reason = "one parameter per wire field, like `Checkpoint::new`; the only caller is `build`, which already destructures `Header`"
    )]
    pub(super) fn new(
        owner: Owner,
        seq: u64,
        previous: Sha256,
        checkpoint: Sha256,
        captured_at: Timestamp,
        writer: Writer,
        launch: Vec<LaunchRecord>,
        changes: Vec<Change>,
    ) -> Self {
        Self {
            schema: ManifestSchema::V1,
            owner,
            seq,
            previous,
            checkpoint,
            captured_at,
            writer,
            launch,
            changes,
        }
    }
    /// Chain owner.
    pub fn owner(&self) -> &Owner {
        &self.owner
    }
    /// Sequence number.
    pub fn seq(&self) -> u64 {
        self.seq
    }
    /// Hash of manifest `seq - 1`.
    pub fn previous(&self) -> Sha256 {
        self.previous
    }
    /// Hash of the latest checkpoint.
    pub fn checkpoint(&self) -> Sha256 {
        self.checkpoint
    }
    /// Capture time.
    pub fn captured_at(&self) -> &Timestamp {
        &self.captured_at
    }
    /// Writer stamp.
    pub fn writer(&self) -> &Writer {
        &self.writer
    }
    /// Launch evidence.
    pub fn launch(&self) -> &[LaunchRecord] {
        &self.launch
    }
    /// Changes.
    pub fn changes(&self) -> &[Change] {
        &self.changes
    }
    pub(super) fn validate(&self) -> Result<(), ManifestError> {
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
    schema: ManifestSchema,
    /// Chain owner.
    owner: Owner,
    /// Sequence number, a multiple of 64.
    seq: u64,
    /// Hash of manifest `seq - 1`; absent only at seq 0.
    previous: Option<Sha256>,
    /// Capture time.
    captured_at: Timestamp,
    /// Writer stamp.
    writer: Writer,
    /// Launch evidence.
    launch: Vec<LaunchRecord>,
    /// Sources sorted by id, unique.
    sources: Vec<CheckpointSource>,
}
impl Checkpoint {
    /// Assemble a checkpoint; validated by the builder and by parsing, never by the caller.
    pub(super) fn new(
        owner: Owner,
        seq: u64,
        previous: Option<Sha256>,
        captured_at: Timestamp,
        writer: Writer,
        launch: Vec<LaunchRecord>,
        sources: Vec<CheckpointSource>,
    ) -> Self {
        Self {
            schema: ManifestSchema::V1,
            owner,
            seq,
            previous,
            captured_at,
            writer,
            launch,
            sources,
        }
    }
    /// Chain owner.
    pub fn owner(&self) -> &Owner {
        &self.owner
    }
    /// Sequence number.
    pub fn seq(&self) -> u64 {
        self.seq
    }
    /// Hash of manifest `seq - 1`; absent only at seq 0.
    pub fn previous(&self) -> Option<Sha256> {
        self.previous
    }
    /// Capture time.
    pub fn captured_at(&self) -> &Timestamp {
        &self.captured_at
    }
    /// Writer stamp.
    pub fn writer(&self) -> &Writer {
        &self.writer
    }
    /// Launch evidence.
    pub fn launch(&self) -> &[LaunchRecord] {
        &self.launch
    }
    /// Sources sorted by id.
    pub fn sources(&self) -> &[CheckpointSource] {
        &self.sources
    }
    pub(super) fn validate(&self) -> Result<(), ManifestError> {
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
