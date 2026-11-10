//! Validated pure capture vocabulary; no paths, policy capabilities or I/O.
//! Non-ASCII/path-shaped source names are not identifiers: the shell derives stable,
//! root-qualified content-independent IDs (for example SHA-256 coordinates). It must
//! preserve original path provenance separately, never classify an invalid ID as UnknownKind.
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use std::time::Duration;

/// Boundary validation failure; never includes rejected input.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum ValidationError {
    /// Invalid identifier syntax.
    #[error("invalid identifier")]
    Identifier,
    /// Invalid opaque entity tag, with no rejected header value in diagnostics.
    #[error("invalid entity tag")]
    EntityTag,
    /// Invalid SHA-256 syntax.
    #[error("invalid SHA-256")]
    Hash,
    /// Offset arithmetic overflow.
    #[error("offset overflow")]
    Overflow,
}

/// One definition of the coordinate identifier rule, for the four ID brands that share it.
/// Each brand is a distinct type so coordinates cannot be swapped; the rule, the wire form
/// and the exported JSON schema are identical by construction.
macro_rules! identifier {
    ($name:ident) => {
        #[doc = concat!(
            "Validated ",
            stringify!($name),
            " boundary value. IDs allow ASCII coordinates, not traversal."
        )]
        #[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
        #[serde(try_from = "String", into = "String")]
        pub struct $name(String);
        impl TryFrom<String> for $name {
            type Error = ValidationError;
            fn try_from(value: String) -> Result<Self, Self::Error> {
                if value.is_empty()
                    || value.len() > 1024
                    || value == "."
                    || value == ".."
                    || !value
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || b"._:-".contains(&b))
                {
                    return Err(ValidationError::Identifier);
                }
                Ok(Self(value))
            }
        }
        impl From<$name> for String {
            fn from(value: $name) -> Self {
                value.0
            }
        }
        impl $name {
            /// Return the validated spelling.
            pub fn as_str(&self) -> &str {
                &self.0
            }
        }
        impl JsonSchema for $name {
            fn schema_name() -> std::borrow::Cow<'static, str> {
                stringify!($name).into()
            }
            fn json_schema(_generator: &mut schemars::SchemaGenerator) -> schemars::Schema {
                schemars::json_schema!({"type": "string", "minLength": 1, "maxLength": 1024, "pattern": "^(?!\\.{1,2}$)[A-Za-z0-9._:-]+$"})
            }
        }
    };
}
identifier!(ArchiveId);
identifier!(MachineId);
identifier!(SessionId);
identifier!(SourceId);

/// Opaque HTTP entity tag used unchanged as a store fence, including quotes.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(try_from = "String", into = "String")]
pub struct ETag(String);
impl TryFrom<String> for ETag {
    type Error = ValidationError;
    fn try_from(value: String) -> Result<Self, Self::Error> {
        if value.is_empty() || value.chars().any(char::is_control) {
            return Err(ValidationError::EntityTag);
        }
        Ok(Self(value))
    }
}
impl From<ETag> for String {
    fn from(value: ETag) -> Self {
        value.0
    }
}
impl ETag {
    /// Return the exact opaque fence, without unquoting or normalization.
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

/// Byte offset, independent of text and record boundaries.
#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize, JsonSchema,
)]
#[serde(transparent)]
pub struct Offset(u64);
impl Offset {
    /// Construct a byte offset. Every u64 is a valid absolute offset.
    pub const fn new(value: u64) -> Self {
        Self(value)
    }
    /// Return the byte position.
    pub const fn get(self) -> u64 {
        self.0
    }
    /// Advance without wrapping.
    ///
    /// # Errors
    /// Returns `Overflow` if the resulting offset cannot be represented.
    pub fn checked_add(self, bytes: u64) -> Result<Self, ValidationError> {
        self.0
            .checked_add(bytes)
            .map(Self)
            .ok_or(ValidationError::Overflow)
    }
}
/// SHA-256 of bytes in the explicitly chosen source or publication domain.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(try_from = "String", into = "String")]
pub struct Sha256([u8; 32]);
impl Sha256 {
    /// Construct from a complete digest, not an unchecked string.
    pub const fn from_bytes(bytes: [u8; 32]) -> Self {
        Self(bytes)
    }
}
impl TryFrom<String> for Sha256 {
    type Error = ValidationError;
    fn try_from(value: String) -> Result<Self, Self::Error> {
        fn nibble(byte: u8) -> Option<u8> {
            match byte {
                b'0'..=b'9' => Some(byte - b'0'),
                b'a'..=b'f' => Some(byte - b'a' + 10),
                _ => None,
            }
        }
        let (pairs, rest) = value.as_bytes().as_chunks::<2>();
        if pairs.len() != 32 || !rest.is_empty() {
            return Err(ValidationError::Hash);
        }
        let mut bytes = [0_u8; 32];
        for (out, [high, low]) in bytes.iter_mut().zip(pairs) {
            let (Some(h), Some(l)) = (nibble(*high), nibble(*low)) else {
                return Err(ValidationError::Hash);
            };
            *out = (h << 4) | l;
        }
        Ok(Self(bytes))
    }
}
impl From<Sha256> for String {
    fn from(value: Sha256) -> Self {
        use std::fmt::Write;
        let mut result = String::with_capacity(64);
        for byte in value.0 {
            let _ = write!(result, "{byte:02x}");
        }
        result
    }
}
/// Resumable SHA-256 of a stream prefix: a cost optimisation for the derived index only.
///
/// It is never proof. A caller must compare [`Sha256State::digest`] with the authoritative
/// committed digest before resuming, and an absent or invalid state means "read the prefix again".
#[derive(Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(try_from = "String", into = "String")]
pub struct Sha256State(Vec<u8>);
impl std::fmt::Debug for Sha256State {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("Sha256State(..)")
    }
}
impl Sha256State {
    fn hasher(&self) -> Result<sha2::Sha256, ValidationError> {
        use sha2::digest::common::hazmat::SerializableState;
        let serialized = sha2::digest::common::hazmat::SerializedState::<sha2::Sha256>::try_from(
            self.0.as_slice(),
        )
        .map_err(|_| ValidationError::Hash)?;
        <sha2::Sha256 as SerializableState>::deserialize(&serialized)
            .map_err(|_| ValidationError::Hash)
    }
    fn of(hasher: &sha2::Sha256) -> Self {
        use sha2::digest::common::hazmat::SerializableState;
        Self(hasher.serialize().to_vec())
    }
    /// State of the empty stream.
    pub fn empty() -> Self {
        Self::of(&sha2::Sha256::default())
    }
    /// State after appending `bytes`.
    ///
    /// # Errors
    /// `Hash` if this state is not a valid serialized hasher.
    pub fn extend(&self, bytes: &[u8]) -> Result<Self, ValidationError> {
        use sha2::Digest;
        let mut hasher = self.hasher()?;
        hasher.update(bytes);
        Ok(Self::of(&hasher))
    }
    /// Digest of everything absorbed so far.
    ///
    /// # Errors
    /// `Hash` if this state is not a valid serialized hasher.
    pub fn digest(&self) -> Result<Sha256, ValidationError> {
        use sha2::Digest;
        Ok(Sha256::from_bytes(self.hasher()?.finalize().into()))
    }
}
impl TryFrom<String> for Sha256State {
    type Error = ValidationError;
    fn try_from(value: String) -> Result<Self, Self::Error> {
        let (pairs, rest) = value.as_bytes().as_chunks::<2>();
        if !rest.is_empty() {
            return Err(ValidationError::Hash);
        }
        let nibble = |b: u8| {
            char::from(b)
                .to_digit(16)
                .and_then(|d| u8::try_from(d).ok())
        };
        let mut bytes = Vec::with_capacity(pairs.len());
        for [high, low] in pairs {
            if high.is_ascii_uppercase() || low.is_ascii_uppercase() {
                return Err(ValidationError::Hash);
            }
            let (Some(h), Some(l)) = (nibble(*high), nibble(*low)) else {
                return Err(ValidationError::Hash);
            };
            bytes.push((h << 4) | l);
        }
        let state = Self(bytes);
        state.hasher()?;
        Ok(state)
    }
}
impl From<Sha256State> for String {
    fn from(value: Sha256State) -> Self {
        use std::fmt::Write;
        let mut result = String::with_capacity(value.0.len() * 2);
        for byte in value.0 {
            let _ = write!(result, "{byte:02x}");
        }
        result
    }
}
impl JsonSchema for Sha256State {
    fn schema_name() -> std::borrow::Cow<'static, str> {
        "Sha256State".into()
    }
    fn json_schema(_generator: &mut schemars::SchemaGenerator) -> schemars::Schema {
        schemars::json_schema!({"type": "string", "pattern": "^[0-9a-f]+$"})
    }
}
/// Digest of transform rules and semantic version, not writer version.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(transparent)]
pub struct RulesDigest(Sha256);
impl RulesDigest {
    /// Brand a digest as the protected-rules digest; it is not interchangeable with content hashes.
    pub const fn new(digest: Sha256) -> Self {
        Self(digest)
    }
    /// The underlying digest.
    pub const fn digest(self) -> Sha256 {
        self.0
    }
}

/// Agent namespace.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum Agent {
    /// Claude agent.
    Claude,
    /// Codex agent.
    Codex,
    /// Pi agent.
    Pi,
}

impl Agent {
    /// Wire spelling, identical to the serde tag (single source for keys and namespaces).
    pub const fn as_str(&self) -> &'static str {
        match self {
            Self::Claude => "claude",
            Self::Codex => "codex",
            Self::Pi => "pi",
        }
    }
}

/// Stable session coordinates.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct SessionKey {
    /// Archive coordinate.
    pub archive: ArchiveId,
    /// Machine coordinate.
    pub machine: MachineId,
    /// Agent coordinate.
    pub agent: Agent,
    /// Session coordinate, spelled `session_id` in owned wire schemas.
    #[serde(rename = "session_id")]
    pub session: SessionId,
}

/// Manifest-chain owner; shared sources have no fictitious session.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum Owner {
    /// Session-local evidence.
    Session(SessionKey),
    /// Shared agent resources.
    Shared {
        /// Archive namespace.
        archive: ArchiveId,
        /// Host namespace.
        machine: MachineId,
        /// Agent namespace.
        agent: Agent,
    },
}

/// Capture byte-stream strategy.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum SourceClass {
    /// Retain every epoch.
    Append,
    /// Latest epoch selected in checkpoints.
    Snapshot,
}

/// Finite known source families; locator still must prove pattern and ownership.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum SourceKind {
    /// ClaudeTranscript family; not arbitrary-path admission.
    #[serde(rename = "claude.transcript")]
    ClaudeTranscript,
    /// ClaudeSubagent family; not arbitrary-path admission.
    #[serde(rename = "claude.subagent")]
    ClaudeSubagent,
    /// ClaudeToolResults family; not arbitrary-path admission.
    #[serde(rename = "claude.tool_results")]
    ClaudeToolResults,
    /// ClaudeTodos family; not arbitrary-path admission.
    #[serde(rename = "claude.todos")]
    ClaudeTodos,
    /// ClaudeTasks family; not arbitrary-path admission.
    #[serde(rename = "claude.tasks")]
    ClaudeTasks,
    /// ClaudeTeams family; not arbitrary-path admission.
    #[serde(rename = "claude.teams")]
    ClaudeTeams,
    /// ClaudeFileHistory family; not arbitrary-path admission.
    #[serde(rename = "claude.file_history")]
    ClaudeFileHistory,
    /// ClaudeDebug family; not arbitrary-path admission.
    #[serde(rename = "claude.debug")]
    ClaudeDebug,
    /// ClaudeHistory family; not arbitrary-path admission.
    #[serde(rename = "claude.history")]
    ClaudeHistory,
    /// ClaudeConfig family; not arbitrary-path admission.
    #[serde(rename = "claude.config")]
    ClaudeConfig,
    /// CodexRollout family; not arbitrary-path admission.
    #[serde(rename = "codex.rollout")]
    CodexRollout,
    /// CodexCompressed family; not arbitrary-path admission.
    #[serde(rename = "codex.compressed")]
    CodexCompressed,
    /// CodexStore family; not arbitrary-path admission.
    #[serde(rename = "codex.store")]
    CodexStore,
    /// CodexHistory family; not arbitrary-path admission.
    #[serde(rename = "codex.history")]
    CodexHistory,
    /// CodexConfig family; not arbitrary-path admission.
    #[serde(rename = "codex.config")]
    CodexConfig,
    /// PiSession family; not arbitrary-path admission.
    #[serde(rename = "pi.session")]
    PiSession,
    /// PiSettings family; not arbitrary-path admission.
    #[serde(rename = "pi.settings")]
    PiSettings,
    /// PiModels family; not arbitrary-path admission.
    #[serde(rename = "pi.models")]
    PiModels,
    /// PiSnapshot family; not arbitrary-path admission.
    #[serde(rename = "pi.snapshot")]
    PiSnapshot,
    /// ClaudeSessionEnv family; not arbitrary-path admission.
    #[serde(rename = "claude.session_env")]
    ClaudeSessionEnv,
    /// ClaudePlan family; not arbitrary-path admission.
    #[serde(rename = "claude.plan")]
    ClaudePlan,
    /// ClaudePaste family; not arbitrary-path admission.
    #[serde(rename = "claude.paste")]
    ClaudePaste,
    /// ClaudeUsage family; not arbitrary-path admission.
    #[serde(rename = "claude.usage")]
    ClaudeUsage,
    /// ClaudeMemory family; not arbitrary-path admission.
    #[serde(rename = "claude.memory")]
    ClaudeMemory,
    /// ClaudeAux family; not arbitrary-path admission.
    #[serde(rename = "claude.aux")]
    ClaudeAux,
    /// CodexMemory family; not arbitrary-path admission.
    #[serde(rename = "codex.memory")]
    CodexMemory,
    /// CodexLog family; not arbitrary-path admission.
    #[serde(rename = "codex.log")]
    CodexLog,
    /// PiConfig family; not arbitrary-path admission.
    #[serde(rename = "pi.config")]
    PiConfig,
    /// PiLog family; not arbitrary-path admission.
    #[serde(rename = "pi.log")]
    PiLog,
    /// PiAux family: non-transcript files inside a validated Pi session directory.
    #[serde(rename = "pi.aux")]
    PiAux,
}

/// Birth time when supported by the filesystem.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum Birth {
    /// Native birth timestamp in nanoseconds.
    Known(i64),
    /// Filesystem does not expose birth time.
    Unavailable,
}

/// Filesystem identity including birth time to reduce inode-reuse ambiguity.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct FileId {
    /// Device number.
    pub dev: u64,
    /// Inode number.
    pub ino: u64,
    /// Birth observation.
    pub birth: Birth,
}

/// Immutable shell stat observation.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Observed {
    /// File identity.
    pub id: FileId,
    /// Physical source size in bytes.
    pub size: u64,
    /// Native modification time.
    pub mtime_ns: i64,
    /// Wall observation time; not a scheduling clock.
    pub observed_at_ns: i64,
}

/// Trust of the previously recorded stat.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum StatTrust {
    /// Outside the racy timestamp interval.
    Settled,
    /// Within two seconds of observation, including future mtimes.
    Racy,
}

impl Observed {
    /// Classify timestamp races without overflow, treating future timestamps as racy.
    pub fn trust(&self) -> StatTrust {
        if i128::from(self.observed_at_ns) - i128::from(self.mtime_ns) > 2_000_000_000 {
            StatTrust::Settled
        } else {
            StatTrust::Racy
        }
    }
}

/// Physical source fingerprint; never a digest of transformed publication.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Fingerprint {
    /// Identity.
    pub id: FileId,
    /// Physical size.
    pub size: u64,
    /// Modification time.
    pub mtime_ns: i64,
}

/// WAL absence is explicit, not a fabricated zero-length file.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum WalFingerprint {
    /// No WAL observed.
    Absent,
    /// Observed WAL.
    Present(Fingerprint),
}

/// Complete physical snapshot inputs.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum SnapshotFingerprint {
    /// Plain file.
    Plain(Fingerprint),
    /// Database and WAL together.
    Sqlite {
        /// Database fingerprint.
        db: Fingerprint,
        /// WAL fingerprint, including absence.
        wal: WalFingerprint,
    },
}

/// Authoritative remote committed position, independent of local cache.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct EpochPosition {
    /// Epoch number.
    pub epoch: u32,
    /// Committed logical byte boundary.
    pub captured_to: Offset,
    /// Fresh digest of the committed prefix.
    pub whole: Sha256,
    /// Applicable protected transform rules only.
    pub rules: Option<RulesDigest>,
}

/// Local audit bookkeeping; no resumable state is treated as proof.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct AuditState {
    /// Last eligible full audit.
    pub last_checked_at_ns: Option<i64>,
    /// Failures on successive epochs.
    pub consecutive_failures: u32,
}

/// Derived index position. Resumable hasher bytes belong to shell cache, not proof.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct SourceState {
    /// Stat pinned at last capture; its trust is derived by [`Observed::trust`], never stored.
    pub observed: Observed,
    /// Committed byte position.
    pub position: EpochPosition,
    /// Digest of prefix head window.
    pub head: Sha256,
    /// Digest of prefix tail window.
    pub tail: Sha256,
    /// Audit bookkeeping.
    pub audit: AuditState,
    /// Resumable hasher over exactly `[0, position.captured_to)` of the transformed stream.
    /// Present only for prefix-stable sources; absent or mismatching means a full re-read.
    #[serde(default)]
    pub whole_state: Option<Sha256State>,
}

/// Closed reasons to begin a fresh epoch.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum ReplayReason {
    /// FirstCapture requires byte-zero replay.
    FirstCapture,
    /// IdentityChanged requires byte-zero replay.
    IdentityChanged,
    /// Truncated requires byte-zero replay.
    Truncated,
    /// WindowMismatch requires byte-zero replay.
    WindowMismatch,
    /// AdoptMismatch requires byte-zero replay.
    AdoptMismatch,
    /// AuditMismatch requires byte-zero replay.
    AuditMismatch,
    /// RulesChanged requires byte-zero replay.
    RulesChanged,
}

/// Fixed local reasons; no path or parser text.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum WithholdReason {
    /// Credential withheld and counted.
    Credential,
    /// CollectorState withheld and counted.
    CollectorState,
    /// UnknownKind withheld and counted.
    UnknownKind,
    /// UnknownOwner withheld and counted.
    UnknownOwner,
    /// MalformedProtected withheld and counted.
    MalformedProtected,
    /// ScrubFailed withheld and counted.
    ScrubFailed,
    /// SecretInText withheld and counted.
    SecretInText,
    /// KeyTooLong withheld and counted.
    KeyTooLong,
    /// SourceLimit withheld and counted.
    SourceLimit,
}

/// Pure requested work; no I/O is performed.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum Plan {
    /// No capture work.
    Unchanged,
    /// Extend current epoch after independent window verification.
    Append {
        /// Inclusive start.
        from: Offset,
        /// Exclusive end.
        to: Offset,
    },
    /// Request a full independent local prefix digest before cache adoption.
    Adopt {
        /// Position to verify and rebuild.
        remote: EpochPosition,
    },
    /// Capture from byte zero.
    Replay {
        /// Fixed replay reason.
        reason: ReplayReason,
    },
    /// Capture a changed physical snapshot.
    Snapshot,
    /// Two consecutive missing observations.
    Deleted,
    /// No upload, count locally.
    Withheld {
        /// Local fixed reason.
        reason: WithholdReason,
    },
}

/// Local failures remain distinct from integrity faults.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum LocalError {
    /// DiskFull local failure.
    DiskFull,
    /// Permission local failure.
    Permission,
    /// BackupFailed local failure.
    BackupFailed,
    /// SourceChanged local failure.
    SourceChanged,
    /// Io local failure.
    Io,
}

/// Deterministic source failures without diagnostic payloads.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum Fault {
    /// NonJsonResponse deterministic failure.
    NonJsonResponse,
    /// Schema deterministic failure.
    Schema,
    /// AuditMismatch deterministic failure.
    AuditMismatch,
    /// ReadbackMismatch deterministic failure.
    ReadbackMismatch,
    /// SourceUnstable deterministic failure.
    SourceUnstable,
}

/// Closed failure classes and scope.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum ErrorClass {
    /// Connection or timeout failure.
    Offline,
    /// Remote throttling.
    Throttled,
    /// `BaseUnchanged` persisted through every in-commit rebase retry (DESIGN 7.4 step 5,
    /// amendment A6). Retried with a short full-jitter backoff, counted apart from
    /// `Throttled`, and never carries a `Retry-After`.
    Transient,
    /// Pause host uploads, continue observations.
    DestinationAuth,
    /// Local resource failure.
    Local(LocalError),
    /// Count consecutive deterministic failures.
    Deterministic(Fault),
}

/// Source-level parking reason.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum ParkReason {
    /// RepeatedAuditMismatch source park.
    RepeatedAuditMismatch,
    /// RepeatedReadbackMismatch source park.
    RepeatedReadbackMismatch,
    /// SourceUnstable source park.
    SourceUnstable,
    /// Repeated non-JSON 200 responses (captive portal), DESIGN 11.2.
    RepeatedNonJsonResponse,
    /// Repeated schema errors, DESIGN 11.2.
    RepeatedSchemaError,
}

/// Owner-wide parking, not source parking.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum OwnerParkReason {
    /// Concurrent writer violated ownership.
    ForeignWriter {
        /// Foreign manifest digest.
        theirs: Sha256,
        /// Our manifest digest.
        ours: Sha256,
    },
    /// Unsupported future schema.
    NewerRemoteSchema,
}

/// Outcome of one attempt; cancellation is not a failure.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum Disposition {
    /// Completed.
    Done,
    /// Retry later.
    Retry {
        /// Monotonic delay.
        after: Duration,
        /// Fixed failure class.
        class: ErrorClass,
    },
    /// Stop uploads but continue watching.
    ///
    /// Only a destination authorization failure pauses the host; the shell counts it as
    /// `ErrorClass::DestinationAuth`.
    PauseHost,
    /// Park one source.
    Park {
        /// Source-scoped reason.
        reason: ParkReason,
    },
    /// Park one owner.
    ParkOwner {
        /// Owner-scoped reason.
        reason: OwnerParkReason,
    },
    /// Requested shutdown, not timeout.
    Cancelled,
}

/// Closed guarded commit outcomes; no transport implementation.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "snake_case")]
pub enum CommitOutcome {
    /// Write succeeded.
    Committed {
        /// Returned fence.
        etag: ETag,
    },
    /// Verification proves our write landed.
    AlreadyOurs {
        /// Verified fence.
        etag: ETag,
    },
    /// Retry against refreshed base.
    BaseUnchanged,
    /// Do not merge concurrent writers.
    Foreign {
        /// Other manifest.
        theirs: Sha256,
    },
    /// Park instead of parse retry loop.
    NewerSchema,
}

impl JsonSchema for ETag {
    fn schema_name() -> std::borrow::Cow<'static, str> {
        "ETag".into()
    }
    fn json_schema(_generator: &mut schemars::SchemaGenerator) -> schemars::Schema {
        schemars::json_schema!({"type": "string", "minLength": 1, "pattern": "^[^\\u0000-\\u001f\\u007f-\\u009f]+$"})
    }
}

impl JsonSchema for Sha256 {
    fn schema_name() -> std::borrow::Cow<'static, str> {
        "Sha256".into()
    }
    fn json_schema(_generator: &mut schemars::SchemaGenerator) -> schemars::Schema {
        schemars::json_schema!({"type": "string", "pattern": "^[0-9a-f]{64}$"})
    }
}
