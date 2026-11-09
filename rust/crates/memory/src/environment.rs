//! Trait seams for seed-replayable simulation, independent of runtime selection.
//!
//! These are minimal scaffold interfaces, not a storage or retry framework.
//! Runtime adapters must remain safe Rust: do not weaken `forbid(unsafe_code)` to
//! add flock/F_FULLFSYNC. Select safe dependency APIs and review their contracts.

/// Monotonic and wall time are separate and controlled by the simulator.
pub trait Clock {
    /// Monotonic elapsed nanoseconds, excluding sleep on the target hosts.
    fn monotonic_ns(&self) -> u64;
    /// Wall nanoseconds; never an ordering authority.
    fn wall_ns(&self) -> i128;
}

/// One stat observation of a source: identity, size and modification time.
///
/// The shell adds the observation time (it comes from the [`Clock`] seam), so a seam
/// implementation never decides how trustworthy its own timestamp is.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Stat {
    /// Device, inode and birth observation.
    pub id: memory_core::domain::FileId,
    /// Physical size in bytes.
    pub size: u64,
    /// Native modification time in nanoseconds.
    pub mtime_ns: i64,
}

/// Minimal filesystem seam; production metadata types arrive in a later slice.
pub trait FileSystem {
    /// Adapter's closed error type.
    type Error;
    /// Stat a source. `Ok(None)` means the source does not exist (an absence observation,
    /// not an error); an `Err` is a transient failure that must not count as absence.
    ///
    /// # Errors
    /// Returns the adapter's error when the source cannot be examined.
    fn stat(&mut self, source: u64) -> Result<Option<Stat>, Self::Error>;
    /// Observe a synthetic/source handle's length without opening content.
    ///
    /// # Errors
    /// Returns the adapter's error on an unavailable source.
    fn stat_len(&mut self, source: u64) -> Result<u64, Self::Error>;
    /// Read a bounded range through the counter seam. A range that extends past the end of
    /// the source is clipped, so a result shorter than requested is how a racing shrink
    /// shows itself to the caller.
    ///
    /// # Errors
    /// Returns the adapter's error for unavailable or invalid ranges.
    fn read(&mut self, source: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, Self::Error>;
}

/// Closed failure vocabulary of the object store (DESIGN.md 7.4, 11.2).
///
/// `Ambiguous` is a reply that never arrived or could not be interpreted: the request may or
/// may not have landed, so the caller must verify (DESIGN.md 7.4 step 5). `Offline` and
/// `Server` are transient too, but the simulator may also have landed the write behind them.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum StoreError {
    /// HTTP 412: an `If-None-Match: *` or `If-Match` precondition failed.
    PreconditionFailed,
    /// HTTP 404 (and a conditional write against a missing key).
    NotFound,
    /// Connect, DNS, TLS or timeout failure.
    Offline,
    /// HTTP 429 or 503 (DESIGN.md 11.2 `Throttled`).
    Throttled,
    /// Any other 5xx reply. DESIGN.md 11.2 lists only 429/503 as throttled, so this
    /// retries with the `Offline` exponential backoff.
    Server,
    /// HTTP 401 or 403.
    Auth,
    /// The outcome of the request is unknown (lost reply).
    Ambiguous,
}

impl StoreError {
    /// Error class of the failure for the disposition table (DESIGN.md 11.2).
    ///
    /// `Offline`, `Ambiguous` and a non-throttling 5xx retry as offline; 429/503 are
    /// `Throttled`; auth pauses the host. A 412 or 404 reaching here is unexpected and retries as offline.
    pub const fn class(self) -> memory_core::domain::ErrorClass {
        use memory_core::domain::ErrorClass;
        match self {
            Self::Auth => ErrorClass::DestinationAuth,
            Self::Throttled => ErrorClass::Throttled,
            Self::PreconditionFailed
            | Self::NotFound
            | Self::Offline
            | Self::Server
            | Self::Ambiguous => ErrorClass::Offline,
        }
    }
}

impl std::fmt::Display for StoreError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(match self {
            Self::PreconditionFailed => "precondition failed",
            Self::NotFound => "not found",
            Self::Offline => "offline",
            Self::Throttled => "throttled",
            Self::Server => "server error",
            Self::Auth => "authorization failed",
            Self::Ambiguous => "ambiguous reply",
        })
    }
}
impl std::error::Error for StoreError {}

/// An object read back with the ETag that fences later writes.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Stored<B> {
    /// Object body.
    pub body: B,
    /// Opaque entity tag, used unchanged as the `If-Match` fence.
    pub etag: memory_core::domain::ETag,
}

/// Typed object store seam for the commit protocol (DESIGN.md 6.2, 7.4).
///
/// `Body` is the authorized body capability. There is deliberately no production
/// implementation and no public raw-bytes constructor: the later adapter must bind `Body` to
/// `Authorized<Redacted<Bytes>>`, and piece bodies reach the driver only as values the caller
/// obtained from policy. The only bytes the driver itself wraps are the writer's own
/// metadata documents (manifests and `current.json`), through [`Store::document`]; they
/// carry no source content. Tests bind `Body` to a test-only type.
pub trait Store {
    /// The authorized body capability.
    type Body: AsRef<[u8]>;
    /// Wrap one of the writer's own metadata documents (manifest, `current.json`).
    fn document(bytes: Vec<u8>) -> Self::Body;
    /// Immutable conditional creation (`If-None-Match: *`).
    ///
    /// # Errors
    /// [`StoreError::PreconditionFailed`] when the key exists, otherwise a transport or auth
    /// error. A lost reply is [`StoreError::Ambiguous`] and the object may have landed.
    fn put_if_absent(
        &mut self,
        key: &str,
        body: Self::Body,
    ) -> Result<memory_core::domain::ETag, StoreError>;
    /// Replacement guarded by the ETag we hold (`If-Match`). Client retries stay disabled.
    ///
    /// # Errors
    /// [`StoreError::PreconditionFailed`] on a stale fence, [`StoreError::NotFound`] when the
    /// key is missing, otherwise a transport or auth error.
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &memory_core::domain::ETag,
        body: Self::Body,
    ) -> Result<memory_core::domain::ETag, StoreError>;
    /// Read an object and its ETag.
    ///
    /// # Errors
    /// [`StoreError::NotFound`] when absent, otherwise a transport or auth error.
    fn get(&mut self, key: &str) -> Result<Stored<Self::Body>, StoreError>;
}

/// Per-wrapper observations, never a claim about unwrapped system calls.
#[derive(Debug, Default, Clone, Copy, PartialEq, Eq)]
pub struct Counters {
    /// Stat calls through the filesystem seam.
    pub stats: u64,
    /// Bytes returned through the filesystem seam.
    pub bytes_read: u64,
    /// Store calls, including failed/lost-reply attempts.
    pub puts: u64,
    /// Explicit simulator timer wakeups.
    pub wakeups: u64,
}
