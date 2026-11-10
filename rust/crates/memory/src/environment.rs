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
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, thiserror::Error)]
pub enum StoreError {
    /// HTTP 412: an `If-None-Match: *` or `If-Match` precondition failed.
    #[error("precondition failed")]
    PreconditionFailed,
    /// HTTP 404 (and a conditional write against a missing key).
    #[error("not found")]
    NotFound,
    /// Connect, DNS, TLS or timeout failure.
    #[error("offline")]
    Offline,
    /// HTTP 429 or 503 (DESIGN.md 11.2 `Throttled`), with the reply's `Retry-After` in whole
    /// seconds when it carried one (the adapter parses it).
    #[error("throttled")]
    Throttled(Option<u32>),
    /// Any other 5xx reply. DESIGN.md 11.2 lists only 429/503 as throttled, so this
    /// retries with the `Offline` exponential backoff.
    #[error("server error")]
    Server,
    /// HTTP 401 or 403.
    #[error("authorization failed")]
    Auth,
    /// The outcome of the request is unknown (lost reply).
    #[error("ambiguous reply")]
    Ambiguous,
}

impl StoreError {
    /// `Retry-After` of a throttling reply.
    pub fn retry_after(self) -> Option<std::time::Duration> {
        match self {
            Self::Throttled(Some(secs)) => Some(std::time::Duration::from_secs(u64::from(secs))),
            _ => None,
        }
    }
    /// Error class of the failure for the disposition table (DESIGN.md 11.2).
    ///
    /// `Offline`, `Ambiguous` and a non-throttling 5xx retry as offline; 429/503 are
    /// `Throttled`; auth pauses the host. A 412 or 404 reaching here is unexpected and retries as offline.
    pub const fn class(self) -> memory_core::domain::ErrorClass {
        use memory_core::domain::ErrorClass;
        match self {
            Self::Auth => ErrorClass::DestinationAuth,
            Self::Throttled(_) => ErrorClass::Throttled,
            Self::PreconditionFailed
            | Self::NotFound
            | Self::Offline
            | Self::Server
            | Self::Ambiguous => ErrorClass::Offline,
        }
    }
}

/// An object read back with the ETag that fences later writes. A download is not an upload,
/// so the body is plain bytes and needs no capability.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Stored {
    /// Object body.
    pub body: Vec<u8>,
    /// Opaque entity tag, used unchanged as the `If-Match` fence.
    pub etag: memory_core::domain::ETag,
}

/// The only body a [`Store`] accepts (DESIGN.md 8, R2: no byte leaves the host unless it is
/// authorized or is the writer's own metadata).
///
/// It can be built in exactly two ways, both crate-private: a slice of a
/// `memory_core::privacy::AuthorizedBody` (a piece) and the writer's own serialized
/// manifest or `current.json`. It has no public constructor, `From<Vec<u8>>`, `Default` or
/// `Deserialize`.
///
/// ```compile_fail,E0423
/// let _ = memory::environment::Published(vec![1, 2, 3]);
/// ```
/// ```compile_fail
/// let _ = memory::environment::Published::from(vec![1_u8]);
/// ```
/// ```compile_fail
/// let _ = memory::environment::Published::default();
/// ```
/// ```compile_fail
/// let _: memory::environment::Published = serde_json::from_str("[]").unwrap();
/// ```
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Published(Vec<u8>);

impl Published {
    /// One piece: a byte range of an authorized body.
    pub(crate) fn piece(
        body: &memory_core::privacy::AuthorizedBody,
        start: usize,
        end: usize,
    ) -> Option<Self> {
        body.bytes().get(start..end).map(|b| Self(b.to_vec()))
    }
    /// The writer's own serialized metadata (manifest build output, `current.json`).
    pub(crate) const fn metadata(bytes: Vec<u8>) -> Self {
        Self(bytes)
    }
    /// Simulation and test builds only: arbitrary stored bytes, so tests can model encodings
    /// and foreign writers. Absent from the release binary (cargo feature `sim`).
    #[cfg(feature = "sim")]
    pub const fn sim(bytes: Vec<u8>) -> Self {
        Self(bytes)
    }
}
impl AsRef<[u8]> for Published {
    fn as_ref(&self) -> &[u8] {
        &self.0
    }
}

/// Typed object store seam for the commit protocol (DESIGN.md 6.2, 7.4).
///
/// There is deliberately no production implementation. Uploads take [`Published`], which only
/// privacy-authorized piece slices and the writer's own metadata can produce.
pub trait Store {
    /// Immutable conditional creation (`If-None-Match: *`).
    ///
    /// # Errors
    /// [`StoreError::PreconditionFailed`] when the key exists, otherwise a transport or auth
    /// error. A lost reply is [`StoreError::Ambiguous`] and the object may have landed.
    fn put_if_absent(
        &mut self,
        key: &str,
        body: Published,
    ) -> Result<memory_core::domain::ETag, StoreError>;
    /// Replacement guarded by the ETag we hold (`If-Match`). Client retries stay disabled.
    ///
    /// R2 answers 412 to `If-Match` on a missing key (P2 traces; 2026-11-11 probe,
    /// amendment A13.4), not 404, and its ETag is the MD5 of the body. An implementation
    /// therefore returns [`StoreError::PreconditionFailed`] for a missing key as well as a
    /// stale fence, and does not return [`StoreError::NotFound`] from this call.
    ///
    /// # Errors
    /// [`StoreError::PreconditionFailed`] on a stale fence or a missing key, otherwise a
    /// transport or auth error.
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &memory_core::domain::ETag,
        body: Published,
    ) -> Result<memory_core::domain::ETag, StoreError>;
    /// Read an object and its ETag.
    ///
    /// # Errors
    /// [`StoreError::NotFound`] when absent, otherwise a transport or auth error.
    fn get(&mut self, key: &str) -> Result<Stored, StoreError>;
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

/// Wraps a seam implementation and counts calls through it into [`Counters`].
///
/// `FileSystem` calls count stats (`stat`, `stat_len`) and the length of every byte vector
/// `read` returns; `Store` calls count every `put_*` attempt (including failed or lost-reply
/// ones). `Clock` is delegated untouched (its methods take `&self`), so `wakeups` stays 0.
/// Counters describe calls through this wrapper only, never unwrapped system calls.
#[derive(Debug, Default, Clone)]
pub struct Counting<T> {
    /// The wrapped implementation.
    pub inner: T,
    /// Observations so far.
    pub counters: Counters,
}

impl<T> Counting<T> {
    /// Wrap `inner` with zeroed counters.
    pub const fn new(inner: T) -> Self {
        Self {
            inner,
            counters: Counters {
                stats: 0,
                bytes_read: 0,
                puts: 0,
                wakeups: 0,
            },
        }
    }
}

impl<T: FileSystem> FileSystem for Counting<T> {
    type Error = T::Error;
    fn stat(&mut self, source: u64) -> Result<Option<Stat>, Self::Error> {
        self.counters.stats = self.counters.stats.saturating_add(1);
        self.inner.stat(source)
    }
    fn stat_len(&mut self, source: u64) -> Result<u64, Self::Error> {
        self.counters.stats = self.counters.stats.saturating_add(1);
        self.inner.stat_len(source)
    }
    fn read(&mut self, source: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, Self::Error> {
        let bytes = self.inner.read(source, range)?;
        let got = u64::try_from(bytes.len()).unwrap_or(u64::MAX);
        self.counters.bytes_read = self.counters.bytes_read.saturating_add(got);
        Ok(bytes)
    }
}

impl<T: Store> Store for Counting<T> {
    fn put_if_absent(
        &mut self,
        key: &str,
        body: Published,
    ) -> Result<memory_core::domain::ETag, StoreError> {
        self.counters.puts = self.counters.puts.saturating_add(1);
        self.inner.put_if_absent(key, body)
    }
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &memory_core::domain::ETag,
        body: Published,
    ) -> Result<memory_core::domain::ETag, StoreError> {
        self.counters.puts = self.counters.puts.saturating_add(1);
        self.inner.put_if_match(key, etag, body)
    }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        self.inner.get(key)
    }
}

impl<T: Clock> Clock for Counting<T> {
    fn monotonic_ns(&self) -> u64 {
        self.inner.monotonic_ns()
    }
    fn wall_ns(&self) -> i128 {
        self.inner.wall_ns()
    }
}
