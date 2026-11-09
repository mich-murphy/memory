//! Daemon-owned local state (DESIGN.md 9, 12): the state directory, the single-instance lock,
//! the durable versioned envelope and the create-once machine id. Safe std only.
//!
//! State root: `MEMORY_STATE_DIR`, else `$HOME/.local/state/memory`, the same rule as
//! `hook.rs`, so the hook and the daemon agree on `state/hints`.
use memory_core::domain::MachineId;
use sha2::{Digest, Sha256};
use std::ffi::OsString;
use std::fmt::Write as _;
use std::fs::{self, DirBuilder, File, OpenOptions};
use std::io::{ErrorKind, Read, Write};
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

/// Exit code for "another daemon holds the lock" (`EX_TEMPFAIL`), distinct from other failures.
pub const EXIT_LOCK_HELD: u8 = 75;
/// Largest body one envelope carries.
pub const MAX_BODY_BYTES: usize = 64 << 20;
/// Longest header line, newline excluded.
pub const MAX_HEADER_BYTES: usize = 256;
const MAX_FILE_BYTES: usize = MAX_BODY_BYTES + MAX_HEADER_BYTES + 1;
const MAGIC: &str = "memory-state/1";
const MAGIC_PREFIX: &str = "memory-state/";
/// Writer version recorded in every header.
pub const WRITER_VERSION: &str = env!("CARGO_PKG_VERSION");
const LOCK_NAME: &str = "daemon.lock";

/// Closed set of state failures. No variant carries a path or a value.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum StateError {
    /// Another process holds `daemon.lock`.
    #[error("lock_held")]
    LockHeld,
    /// `flock` failed for a reason other than contention.
    #[error("lock_unavailable: {0}")]
    LockUnavailable(ErrorKind),
    /// A filesystem call failed.
    #[error("state_io: {0}")]
    Io(ErrorKind),
    /// A state directory is a symlink.
    #[error("state_symlink")]
    Symlink,
    /// A state directory is not a directory.
    #[error("state_not_directory")]
    NotDirectory,
    /// A state directory is group- or world-writable.
    #[error("state_permissions")]
    Permissions,
    /// A state directory is not owned by the daemon's user.
    #[error("state_not_owned")]
    NotOwned,
    /// A file or schema name is not a plain identifier.
    #[error("state_invalid_name")]
    InvalidName,
    /// A body exceeds [`MAX_BODY_BYTES`].
    #[error("state_too_large")]
    TooLarge,
    /// The stored machine id exists but is not a valid id. It is never overwritten.
    #[error("machine_id_corrupt")]
    MachineIdCorrupt,
}

impl StateError {
    /// Fixed machine-readable code.
    #[must_use]
    pub const fn code(self) -> &'static str {
        match self {
            Self::LockHeld => "lock_held",
            Self::LockUnavailable(_) => "lock_unavailable",
            Self::Io(_) => "state_io",
            Self::Symlink => "state_symlink",
            Self::NotDirectory => "state_not_directory",
            Self::Permissions => "state_permissions",
            Self::NotOwned => "state_not_owned",
            Self::InvalidName => "state_invalid_name",
            Self::TooLarge => "state_too_large",
            Self::MachineIdCorrupt => "machine_id_corrupt",
        }
    }

    /// Process exit code: [`EXIT_LOCK_HELD`] for contention, 1 otherwise.
    #[must_use]
    pub const fn exit_code(self) -> u8 {
        match self {
            Self::LockHeld => EXIT_LOCK_HELD,
            _ => 1,
        }
    }
}

fn io(error: std::io::Error) -> StateError {
    StateError::Io(error.kind())
}

/// State root from explicit inputs (pure form of [`default_root`]).
#[must_use]
pub fn root_from(state_dir: Option<OsString>, home: Option<OsString>) -> Option<PathBuf> {
    if let Some(dir) = state_dir {
        return Some(PathBuf::from(dir));
    }
    Some(PathBuf::from(home?).join(".local/state/memory"))
}

/// State root from the environment, the same rule as the hook.
#[must_use]
pub fn default_root() -> Option<PathBuf> {
    root_from(
        std::env::var_os("MEMORY_STATE_DIR"),
        std::env::var_os("HOME"),
    )
}

/// Create (0700) if missing, then refuse a symlink, a non-directory or a group/world-writable
/// directory. Only the final component is examined; a symlinked parent is allowed.
fn vet(path: &Path, create: bool) -> Result<fs::Metadata, StateError> {
    let meta = match fs::symlink_metadata(path) {
        Ok(meta) => meta,
        Err(error) if create && error.kind() == ErrorKind::NotFound => {
            DirBuilder::new()
                .recursive(true)
                .mode(0o700)
                .create(path)
                .map_err(io)?;
            fs::symlink_metadata(path).map_err(io)?
        }
        Err(error) => return Err(io(error)),
    };
    if meta.file_type().is_symlink() {
        return Err(StateError::Symlink);
    }
    if !meta.is_dir() {
        return Err(StateError::NotDirectory);
    }
    if meta.mode() & 0o022 != 0 {
        return Err(StateError::Permissions);
    }
    Ok(meta)
}

/// The single-instance lock. Dropping it closes the file and releases the lock; the kernel
/// does the same if the process dies. `flock` belongs to the open file description, so a
/// second [`DaemonLock::acquire`] fails even inside one process.
#[derive(Debug)]
pub struct DaemonLock {
    _file: File,
    root: PathBuf,
}

impl DaemonLock {
    /// Create the state root if needed and take `flock(daemon.lock, LOCK_EX | LOCK_NB)`.
    /// Nothing else is touched first (DESIGN.md 12).
    ///
    /// # Errors
    /// [`StateError::LockHeld`] on contention; directory refusals and I/O failures otherwise.
    pub fn acquire(root: &Path) -> Result<Self, StateError> {
        vet(root, true)?;
        let file = OpenOptions::new()
            .write(true)
            .create(true)
            .truncate(false)
            .mode(0o600)
            .open(root.join(LOCK_NAME))
            .map_err(io)?;
        match file.try_lock() {
            Ok(()) => Ok(Self {
                _file: file,
                root: root.to_path_buf(),
            }),
            Err(std::fs::TryLockError::WouldBlock) => Err(StateError::LockHeld),
            Err(std::fs::TryLockError::Error(error)) => {
                Err(StateError::LockUnavailable(error.kind()))
            }
        }
    }

    /// The locked state root.
    #[must_use]
    pub fn root(&self) -> &Path {
        &self.root
    }
}

/// A versioned schema name, rendered `name/version` in the header.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Schema {
    /// Plain identifier, for example `memory.index`.
    pub name: &'static str,
    /// The version this build reads and writes.
    pub version: u32,
}

/// The daemon's state files with a fixed schema each.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StateFile {
    /// `roots.json`, learned custom roots.
    Roots,
    /// `index.json`, a derived cache.
    Index,
    /// `status.json`, parked list and counters.
    Status,
    /// `machine_id`, created once.
    MachineId,
}

impl StateFile {
    /// File name inside the state root.
    #[must_use]
    pub const fn name(self) -> &'static str {
        match self {
            Self::Roots => "roots.json",
            Self::Index => "index.json",
            Self::Status => "status.json",
            Self::MachineId => "machine_id",
        }
    }

    /// Envelope schema of this file.
    #[must_use]
    pub const fn schema(self) -> Schema {
        let name = match self {
            Self::Roots => "memory.roots",
            Self::Index => "memory.index",
            Self::Status => "memory.status",
            Self::MachineId => "memory.machine-id",
        };
        Schema { name, version: 1 }
    }
}

/// Why a file is treated as missing (DESIGN.md 9.2). Closed; no content.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MissingReason {
    /// The file does not exist.
    NotFound,
    /// The file could not be opened or read.
    Unreadable,
    /// The file exceeds the size bound.
    TooLarge,
    /// The header is absent, too long, not UTF-8 or malformed.
    BadHeader,
    /// The envelope format version is not this build's.
    UnknownVersion,
    /// The header names a different schema.
    SchemaMismatch,
    /// The schema version is newer than this build understands.
    NewerSchema,
    /// Fewer body bytes than the header declares.
    ShortBody,
    /// More body bytes than the header declares.
    ExtraBytes,
    /// The body's SHA-256 differs from the header's.
    ChecksumMismatch,
}

/// A verified envelope.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Stored {
    /// Schema version found (never newer than requested).
    pub schema_version: u32,
    /// Version of the writer that produced it.
    pub writer_version: String,
    /// The body, verified by length and SHA-256.
    pub body: Vec<u8>,
}

/// Result of reading an envelope: a verified body or "treat as missing".
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Loaded {
    /// Verified.
    Present(Stored),
    /// Treat as missing, for this reason.
    Missing(MissingReason),
}

fn hex(bytes: &[u8]) -> String {
    let mut out = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        let _ = write!(out, "{byte:02x}");
    }
    out
}

fn sha_hex(body: &[u8]) -> String {
    let digest: [u8; 32] = Sha256::digest(body).into();
    hex(&digest)
}

fn plain(name: &str) -> bool {
    !name.is_empty()
        && !name.starts_with('.')
        && name
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"._-".contains(&b))
}

/// Encode header line plus body (DESIGN.md 9.2 step 1).
///
/// # Errors
/// [`StateError::InvalidName`] for a schema name that is not plain; [`StateError::TooLarge`].
pub fn encode(schema: Schema, body: &[u8]) -> Result<Vec<u8>, StateError> {
    if !plain(schema.name) {
        return Err(StateError::InvalidName);
    }
    if body.len() > MAX_BODY_BYTES {
        return Err(StateError::TooLarge);
    }
    let header = format!(
        "{MAGIC} {}/{} {WRITER_VERSION} {} {}\n",
        schema.name,
        schema.version,
        body.len(),
        sha_hex(body)
    );
    let mut out = Vec::with_capacity(header.len() + body.len());
    out.extend_from_slice(header.as_bytes());
    out.extend_from_slice(body);
    Ok(out)
}

/// Verify stored bytes. Any failure is a [`MissingReason`]; a body is returned only when the
/// header parses, the schema is known and not newer, and length and SHA-256 both match.
#[must_use]
pub fn decode(bytes: &[u8], schema: Schema) -> Loaded {
    use MissingReason::{
        BadHeader, ChecksumMismatch, ExtraBytes, NewerSchema, SchemaMismatch, ShortBody,
        UnknownVersion,
    };
    let Some(newline) = bytes
        .iter()
        .take(MAX_HEADER_BYTES + 1)
        .position(|b| *b == b'\n')
    else {
        return Loaded::Missing(BadHeader);
    };
    let (head, rest) = bytes.split_at(newline);
    let body = rest.get(1..).unwrap_or(&[]);
    let Ok(text) = std::str::from_utf8(head) else {
        return Loaded::Missing(BadHeader);
    };
    let mut fields = text.split(' ');
    match fields.next() {
        Some(MAGIC) => {}
        Some(other) if other.starts_with(MAGIC_PREFIX) => {
            return Loaded::Missing(UnknownVersion);
        }
        _ => return Loaded::Missing(BadHeader),
    }
    let (Some(named), Some(writer), Some(len), Some(sha), None) = (
        fields.next(),
        fields.next(),
        fields.next(),
        fields.next(),
        fields.next(),
    ) else {
        return Loaded::Missing(BadHeader);
    };
    let (Some((name, version)), Ok(len)) = (named.split_once('/'), len.parse::<usize>()) else {
        return Loaded::Missing(BadHeader);
    };
    let Ok(version) = version.parse::<u32>() else {
        return Loaded::Missing(BadHeader);
    };
    if name != schema.name {
        return Loaded::Missing(SchemaMismatch);
    }
    if version > schema.version {
        return Loaded::Missing(NewerSchema);
    }
    if body.len() < len {
        return Loaded::Missing(ShortBody);
    }
    if body.len() > len {
        return Loaded::Missing(ExtraBytes);
    }
    if sha != sha_hex(body) {
        return Loaded::Missing(ChecksumMismatch);
    }
    Loaded::Present(Stored {
        schema_version: version,
        writer_version: writer.to_owned(),
        body: body.to_vec(),
    })
}

/// Read and verify one envelope file. Never fails: every problem is a [`MissingReason`].
#[must_use]
pub fn read_durable(path: &Path, schema: Schema) -> Loaded {
    let mut file = match File::open(path) {
        Ok(file) => file,
        Err(error) if error.kind() == ErrorKind::NotFound => {
            return Loaded::Missing(MissingReason::NotFound);
        }
        Err(_) => return Loaded::Missing(MissingReason::Unreadable),
    };
    let mut buf = Vec::new();
    let limit = u64::try_from(MAX_FILE_BYTES).unwrap_or(u64::MAX);
    if Read::take(&mut file, limit).read_to_end(&mut buf).is_err() {
        return Loaded::Missing(MissingReason::Unreadable);
    }
    if buf.len() >= MAX_FILE_BYTES {
        return Loaded::Missing(MissingReason::TooLarge);
    }
    decode(&buf, schema)
}

static TEMP_COUNTER: AtomicU64 = AtomicU64::new(0);

/// Whether durable writes flush (DESIGN.md 9.2, amendment A7).
///
/// `Full` is production: `sync_all` on the temp file (`F_FULLFSYNC` on Apple targets) and on
/// the directory. `None` keeps temp file, write, rename and directory open and skips only
/// those flush calls; a flush cannot change what a live process reads back, so it is for
/// simulation tests only. The daemon binary always uses `Full`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum SyncPolicy {
    /// Flush the file and the directory.
    #[default]
    Full,
    /// Skip the flush calls only. Simulation builds only (cargo feature `sim`, amendment A7).
    #[cfg(feature = "sim")]
    None,
}

fn fsync_dir(dir: &Path, sync: SyncPolicy) -> Result<(), StateError> {
    let dir = File::open(dir).map_err(io)?;
    match sync {
        SyncPolicy::Full => dir.sync_all().map_err(io),
        #[cfg(feature = "sim")]
        SyncPolicy::None => Ok(()),
    }
}

fn write_synced(path: &Path, bytes: &[u8], sync: SyncPolicy) -> std::io::Result<()> {
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(path)?;
    file.write_all(bytes)?;
    match sync {
        SyncPolicy::Full => file.sync_all(),
        #[cfg(feature = "sim")]
        SyncPolicy::None => Ok(()),
    }
}

/// A temporary file written and fsynced but not yet published (steps 1 and 2). Dropping it
/// without publishing leaves the temp file behind, exactly like a crash; the next start
/// sweeps it.
#[derive(Debug)]
pub struct Staged {
    tmp: PathBuf,
    dest: PathBuf,
    dir: PathBuf,
    sync: SyncPolicy,
}

/// Steps 1 and 2 of DESIGN.md 9.2: write `dir/.<name>.tmp-<pid>-<n>` (0600, `create_new`) and
/// fsync it. `File::sync_all` is `F_FULLFSYNC` on Apple targets. Always [`SyncPolicy::Full`].
///
/// # Errors
/// [`StateError::InvalidName`], [`StateError::TooLarge`] or [`StateError::Io`].
pub fn stage(dir: &Path, name: &str, schema: Schema, body: &[u8]) -> Result<Staged, StateError> {
    stage_with(dir, name, schema, body, SyncPolicy::Full)
}

fn stage_with(
    dir: &Path,
    name: &str,
    schema: Schema,
    body: &[u8],
    sync: SyncPolicy,
) -> Result<Staged, StateError> {
    if !plain(name) {
        return Err(StateError::InvalidName);
    }
    let bytes = encode(schema, body)?;
    let tmp = dir.join(format!(
        ".{name}.tmp-{}-{}",
        std::process::id(),
        TEMP_COUNTER.fetch_add(1, Ordering::SeqCst)
    ));
    if let Err(error) = write_synced(&tmp, &bytes, sync) {
        let _ = fs::remove_file(&tmp);
        return Err(io(error));
    }
    Ok(Staged {
        tmp,
        dest: dir.join(name),
        dir: dir.to_path_buf(),
        sync,
    })
}

impl Staged {
    /// Steps 3 and 4: rename over the destination, then fsync the directory.
    ///
    /// # Errors
    /// [`StateError::Io`]; the temp file is removed on a failed rename.
    pub fn publish(self) -> Result<(), StateError> {
        if let Err(error) = fs::rename(&self.tmp, &self.dest) {
            let _ = fs::remove_file(&self.tmp);
            return Err(io(error));
        }
        fsync_dir(&self.dir, self.sync)
    }

    /// Publish only if the destination does not exist (`link(2)` fails with `EEXIST`, the
    /// `O_EXCL` guarantee, yet readers never see a partial file). Returns whether this call
    /// created the destination.
    ///
    /// # Errors
    /// [`StateError::Io`] other than "already exists".
    pub fn publish_new(self) -> Result<bool, StateError> {
        let linked = fs::hard_link(&self.tmp, &self.dest);
        let _ = fs::remove_file(&self.tmp);
        match linked {
            Ok(()) => fsync_dir(&self.dir, self.sync).map(|()| true),
            Err(error) if error.kind() == ErrorKind::AlreadyExists => Ok(false),
            Err(error) => Err(io(error)),
        }
    }
}

/// Write one file durably: stage, then publish. Always [`SyncPolicy::Full`].
///
/// # Errors
/// As [`stage`] and [`Staged::publish`].
pub fn write_durable(
    dir: &Path,
    name: &str,
    schema: Schema,
    body: &[u8],
) -> Result<(), StateError> {
    stage(dir, name, schema, body)?.publish()
}

/// The prepared state directory. Constructing it needs the [`DaemonLock`], so a second
/// daemon cannot sweep `tmp/` under a running one.
#[derive(Debug, Clone)]
pub struct StateDir {
    root: PathBuf,
    uid: u32,
    sync: SyncPolicy,
}

impl StateDir {
    /// With the lock held: vet and create `tmp/`, empty it, remove stale temp files from the
    /// root, and learn the daemon's uid.
    ///
    /// Safe Rust has no `geteuid`, so the uid is read from a probe file created here with
    /// `create_new`: a file this process just created is owned by its effective uid. That is
    /// sound on local filesystems; it would be wrong only where the filesystem remaps owners
    /// (for example a root-squashed network mount), which one-user-per-host does not use.
    ///
    /// # Errors
    /// Directory refusals, [`StateError::NotOwned`] or [`StateError::Io`].
    pub fn prepare(lock: &DaemonLock) -> Result<Self, StateError> {
        Self::prepare_inner(lock, SyncPolicy::Full)
    }

    /// As [`StateDir::prepare`] with an explicit [`SyncPolicy`] (feature `sim`, tests only).
    ///
    /// # Errors
    /// As [`StateDir::prepare`].
    #[cfg(feature = "sim")]
    pub fn prepare_with(lock: &DaemonLock, sync: SyncPolicy) -> Result<Self, StateError> {
        Self::prepare_inner(lock, sync)
    }

    pub(crate) fn prepare_inner(lock: &DaemonLock, sync: SyncPolicy) -> Result<Self, StateError> {
        let root = lock.root().to_path_buf();
        let tmp = root.join("tmp");
        vet(&tmp, true)?;
        for entry in fs::read_dir(&tmp).map_err(io)? {
            let path = entry.map_err(io)?.path();
            let meta = fs::symlink_metadata(&path).map_err(io)?;
            if meta.is_dir() {
                fs::remove_dir_all(&path).map_err(io)?;
            } else {
                fs::remove_file(&path).map_err(io)?;
            }
        }
        for entry in fs::read_dir(&root).map_err(io)? {
            let entry = entry.map_err(io)?;
            let name = entry.file_name();
            let stale = name
                .to_str()
                .is_some_and(|n| n.starts_with('.') && n.contains(".tmp-"));
            if stale {
                fs::remove_file(entry.path()).map_err(io)?;
            }
        }
        let probe = tmp.join(".uid-probe");
        write_synced(&probe, b"", sync).map_err(io)?;
        let uid = fs::metadata(&probe).map_err(io)?.uid();
        fs::remove_file(&probe).map_err(io)?;
        let state = Self { root, uid, sync };
        state.vet_dir(&state.root)?;
        state.vet_dir(&tmp)?;
        Ok(state)
    }

    /// The state root.
    #[must_use]
    pub fn root(&self) -> &Path {
        &self.root
    }

    /// `state/tmp`.
    #[must_use]
    pub fn tmp_dir(&self) -> PathBuf {
        self.root.join("tmp")
    }

    /// `state/hints`.
    #[must_use]
    pub fn hints_dir(&self) -> PathBuf {
        self.root.join("hints")
    }

    /// The sync policy of this directory's writes.
    #[must_use]
    pub const fn sync_policy(&self) -> SyncPolicy {
        self.sync
    }

    /// Stage a state file (steps 1 and 2) under this directory's policy, so a caller can
    /// stop before [`Staged::publish`], as a crash would.
    ///
    /// # Errors
    /// As [`stage`].
    pub fn stage(&self, file: StateFile, body: &[u8]) -> Result<Staged, StateError> {
        stage_with(&self.root, file.name(), file.schema(), body, self.sync)
    }

    /// The daemon's effective uid as learned by [`StateDir::prepare`].
    #[must_use]
    pub const fn uid(&self) -> u32 {
        self.uid
    }

    /// Path of a state file.
    #[must_use]
    pub fn path(&self, file: StateFile) -> PathBuf {
        self.root.join(file.name())
    }

    /// Refuse an existing directory that is a symlink, not a directory, group/world-writable
    /// or not owned by the daemon's uid (DESIGN.md 9.1). Nothing is created.
    ///
    /// # Errors
    /// [`StateError::Symlink`], [`StateError::NotDirectory`], [`StateError::Permissions`],
    /// [`StateError::NotOwned`] or [`StateError::Io`] (including "not found").
    pub fn vet_dir(&self, path: &Path) -> Result<(), StateError> {
        let meta = vet(path, false)?;
        if meta.uid() == self.uid {
            Ok(())
        } else {
            Err(StateError::NotOwned)
        }
    }

    /// Create `state/hints` (0700) if missing, then [`StateDir::vet_dir`] it.
    ///
    /// # Errors
    /// As [`StateDir::vet_dir`].
    pub fn vet_hints_dir(&self) -> Result<PathBuf, StateError> {
        let dir = self.hints_dir();
        vet(&dir, true)?;
        self.vet_dir(&dir)?;
        Ok(dir)
    }

    /// Durably write a state file.
    ///
    /// # Errors
    /// As [`stage`] and [`Staged::publish`].
    pub fn write(&self, file: StateFile, body: &[u8]) -> Result<(), StateError> {
        self.stage(file, body)?.publish()
    }

    /// Read a state file; every problem is "missing" with a reason.
    #[must_use]
    pub fn read(&self, file: StateFile) -> Loaded {
        read_durable(&self.path(file), file.schema())
    }

    /// Create the machine id once. If one exists, return it and never overwrite, even if the
    /// stored file is damaged ([`StateError::MachineIdCorrupt`]).
    ///
    /// # Errors
    /// [`StateError::MachineIdCorrupt`] or [`StateError::Io`].
    pub fn create_machine_id(&self, candidate: &MachineId) -> Result<MachineId, StateError> {
        let file = StateFile::MachineId;
        let staged = self.stage(file, candidate.as_str().as_bytes())?;
        if staged.publish_new()? {
            return Ok(candidate.clone());
        }
        match self.read(file) {
            Loaded::Present(stored) => String::from_utf8(stored.body)
                .ok()
                .and_then(|text| MachineId::try_from(text).ok())
                .ok_or(StateError::MachineIdCorrupt),
            Loaded::Missing(_) => Err(StateError::MachineIdCorrupt),
        }
    }
}
