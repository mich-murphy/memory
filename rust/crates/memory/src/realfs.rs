//! Real-filesystem adapter (slice 2): [`RealFs`] implements [`FileSystem`] over absolute
//! paths, and [`walk`] is the bounded safety reconcile (DESIGN.md 10.2, 10.3).
//!
//! Stat observation follows DESIGN.md 7.1 and 7.2: `symlink_metadata`, so a symlink is
//! reported as itself (its own inode, size and times) and never followed. `walk` skips
//! symlinks entirely and counts them. No `unsafe`, no new dependencies.

use crate::environment::{FileSystem, Stat};
use memory_core::domain::{Birth, FileId};
use memory_core::locator::{Located, RootRelativePath, Roots, locate};
use std::fs;
use std::io::{ErrorKind, Read, Seek, SeekFrom};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::time::UNIX_EPOCH;

/// Closed error vocabulary of the real filesystem adapter.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum RealFsError {
    /// The handle was never registered.
    #[error("unknown handle")]
    UnknownHandle,
    /// The range ends before it starts.
    #[error("invalid range")]
    InvalidRange,
    /// The source is absent where presence was required (`stat_len`).
    #[error("source absent")]
    Absent,
    /// The opened source is not the file the last `stat` observed (a symlink, another
    /// inode, or not a regular file). Nothing was read. Capture maps every read error to
    /// `Unavailable`, which is retried later (DESIGN.md 7.2, 8.2).
    #[error("source changed between stat and read")]
    Changed,
    /// An operating-system error other than absence; only the kind is kept.
    #[error("io error: {0}")]
    Io(ErrorKind),
}

/// Operation counters, for the cost accounting of DESIGN.md 10.3.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Counters {
    /// `stat` and `stat_len` calls.
    pub stats: u64,
    /// `read` calls that reached the operating system.
    pub reads: u64,
    /// Bytes returned by `read`.
    pub bytes_read: u64,
}

/// [`FileSystem`] over real paths. Handles are indices from [`RealFs::register`].
#[derive(Debug, Default)]
pub struct RealFs {
    paths: Vec<PathBuf>,
    /// Identity from the latest `stat` per handle; `read` must open the same file.
    observed: Vec<Option<FileId>>,
    counters: Counters,
}

/// `O_NOFOLLOW | O_NONBLOCK` for this platform. `libc` is not a dependency, so the values
/// are spelled out (checked against libc 0.2: bsd/apple `O_NOFOLLOW` 0x100, `O_NONBLOCK`
/// 0x4; Linux `O_NONBLOCK` 0x800, `O_NOFOLLOW` 0x20000 on x86_64, riscv64 and s390x and
/// 0x8000 on aarch64 and arm). `O_NONBLOCK` keeps a FIFO swapped in after the stat from
/// blocking the open; it does not change reads of regular files.
#[cfg(target_os = "macos")]
const OPEN_FLAGS: i32 = 0x100 | 0x4;
#[cfg(all(
    target_os = "linux",
    any(target_arch = "x86_64", target_arch = "riscv64", target_arch = "s390x")
))]
const OPEN_FLAGS: i32 = 0x20000 | 0x800;
#[cfg(all(target_os = "linux", any(target_arch = "aarch64", target_arch = "arm")))]
const OPEN_FLAGS: i32 = 0x8000 | 0x800;
#[cfg(not(any(
    target_os = "macos",
    all(
        target_os = "linux",
        any(
            target_arch = "x86_64",
            target_arch = "riscv64",
            target_arch = "s390x",
            target_arch = "aarch64",
            target_arch = "arm"
        )
    )
)))]
compile_error!("RealFs: O_NOFOLLOW value is not known for this target; add it here");

/// `ELOOP`, which `open(2)` with `O_NOFOLLOW` returns for a final symlink. `ErrorKind` has
/// no stable variant for it, so the errno is compared (62 on macOS, 40 on Linux).
#[cfg(target_os = "macos")]
const ELOOP: i32 = 62;
#[cfg(not(target_os = "macos"))]
const ELOOP: i32 = 40;

impl RealFs {
    /// Empty adapter.
    pub fn new() -> Self {
        Self::default()
    }

    /// Bind an absolute path to a new handle.
    pub fn register(&mut self, path: impl Into<PathBuf>) -> u64 {
        self.paths.push(path.into());
        self.observed.push(None);
        (self.paths.len() - 1) as u64
    }

    /// Counters so far.
    pub const fn counters(&self) -> Counters {
        self.counters
    }

    fn path(&self, handle: u64) -> Result<&Path, RealFsError> {
        usize::try_from(handle)
            .ok()
            .and_then(|i| self.paths.get(i))
            .map(PathBuf::as_path)
            .ok_or(RealFsError::UnknownHandle)
    }
}

/// Identity from dev, ino and birth time. Birth time is `created()` in nanoseconds since
/// the epoch; when the platform or filesystem does not provide it (or it predates the
/// epoch or overflows `i64`) the id carries `Birth::Unavailable`.
fn file_id(meta: &fs::Metadata) -> FileId {
    let birth = meta
        .created()
        .ok()
        .and_then(|t| t.duration_since(UNIX_EPOCH).ok())
        .and_then(|d| i64::try_from(d.as_nanos()).ok())
        .map_or(Birth::Unavailable, Birth::Known);
    FileId {
        dev: meta.dev(),
        ino: meta.ino(),
        birth,
    }
}

impl FileSystem for RealFs {
    type Error = RealFsError;

    fn stat(&mut self, source: u64) -> Result<Option<Stat>, RealFsError> {
        self.counters.stats += 1;
        let result = fs::symlink_metadata(self.path(source)?);
        let seen = result.as_ref().ok().map(file_id);
        if let Some(slot) = usize::try_from(source)
            .ok()
            .and_then(|i| self.observed.get_mut(i))
        {
            *slot = seen;
        }
        match result {
            Ok(m) => Ok(Some(Stat {
                id: file_id(&m),
                size: m.size(),
                mtime_ns: m
                    .mtime()
                    .saturating_mul(1_000_000_000)
                    .saturating_add(m.mtime_nsec()),
            })),
            Err(e) if e.kind() == ErrorKind::NotFound => Ok(None),
            Err(e) => Err(RealFsError::Io(e.kind())),
        }
    }

    fn stat_len(&mut self, source: u64) -> Result<u64, RealFsError> {
        self.stat(source)?
            .map(|s| s.size)
            .ok_or(RealFsError::Absent)
    }

    fn read(&mut self, source: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, RealFsError> {
        let len = range
            .end
            .checked_sub(range.start)
            .ok_or(RealFsError::InvalidRange)?;
        let io = |e: std::io::Error| RealFsError::Io(e.kind());
        // Open without following a final symlink, then check the handle itself: a regular
        // file, and the file the last stat saw. Intermediate path components are still
        // resolved by the kernel: a directory component swapped for a symlink after
        // `resolve` is a residual race that DESIGN.md 8.2 does not address.
        let mut file = fs::OpenOptions::new()
            .read(true)
            .custom_flags(OPEN_FLAGS)
            .open(self.path(source)?)
            .map_err(|e| {
                if e.raw_os_error() == Some(ELOOP) {
                    RealFsError::Changed
                } else {
                    io(e)
                }
            })?;
        let meta = file.metadata().map_err(io)?;
        let expected = usize::try_from(source)
            .ok()
            .and_then(|i| self.observed.get(i))
            .and_then(Option::as_ref);
        if !meta.is_file() || expected.is_some_and(|id| *id != file_id(&meta)) {
            return Err(RealFsError::Changed);
        }
        file.seek(SeekFrom::Start(range.start)).map_err(io)?;
        let mut buf = Vec::new();
        file.take(len).read_to_end(&mut buf).map_err(io)?;
        self.counters.reads += 1;
        self.counters.bytes_read += buf.len() as u64;
        Ok(buf)
    }
}

/// Walk bounds. DESIGN.md 3.4 only measures one host (24,127 files, 14,947 directories);
/// it states no limit, so these are safety bounds well above that measurement.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct WalkLimits {
    /// Deepest directory level entered (a root's children are depth 1).
    pub max_depth: usize,
    /// Directory entries examined across all roots before the walk stops.
    pub max_entries: u64,
}

impl Default for WalkLimits {
    fn default() -> Self {
        Self {
            max_depth: 32,
            max_entries: 500_000,
        }
    }
}

/// One walked file: absolute path, root-relative path and its locator verdict.
pub type WalkedFile = (PathBuf, RootRelativePath, Located);

/// Result of a walk. Every skipped class is a closed count, never a panic.
#[derive(Debug, Default, PartialEq, Eq)]
pub struct Walk {
    /// Regular files, per root in the given order, sorted by relative path components.
    pub files: Vec<WalkedFile>,
    /// Directories or entries that could not be read.
    pub unreadable: u64,
    /// Symlinks seen (never followed, never returned).
    pub symlinks: u64,
    /// Directories not entered because of `max_depth`.
    pub too_deep: u64,
    /// Names that are not UTF-8 or not valid root-relative components.
    pub invalid_names: u64,
    /// Entries examined.
    pub entries: u64,
    /// The entry bound stopped the walk early.
    pub truncated: bool,
}

/// Walk each `(directory, roots)` pair with `read_dir`, calling `locate` per regular file.
/// Order is deterministic: names sorted per directory, results sorted per root.
pub fn walk(roots: &[(PathBuf, Roots)], limits: &WalkLimits) -> Walk {
    let mut out = Walk::default();
    for (dir, r) in roots {
        let start = out.files.len();
        walk_root(dir, r, limits, &mut out);
        out.files
            .get_mut(start..)
            .unwrap_or_default()
            .sort_by(|a, b| a.1.components().cmp(b.1.components()));
    }
    out
}

fn walk_root(root: &Path, roots: &Roots, limits: &WalkLimits, out: &mut Walk) {
    let mut stack = vec![(root.to_path_buf(), Vec::<String>::new())];
    while let Some((dir, rel)) = stack.pop() {
        let Ok(rd) = fs::read_dir(&dir) else {
            out.unreadable += 1;
            continue;
        };
        let mut entries = Vec::new();
        for e in rd {
            match e {
                Ok(e) => entries.push(e),
                Err(_) => out.unreadable += 1,
            }
        }
        entries.sort_by_key(fs::DirEntry::file_name);
        // Pushed in reverse so the stack pops directories in sorted order.
        let mut subdirs = Vec::new();
        for e in entries {
            if out.entries >= limits.max_entries {
                out.truncated = true;
                return;
            }
            out.entries += 1;
            let Some(name) = e.file_name().to_str().map(str::to_owned) else {
                out.invalid_names += 1;
                continue;
            };
            let Ok(ft) = e.file_type() else {
                out.unreadable += 1;
                continue;
            };
            let mut child = rel.clone();
            child.push(name);
            if ft.is_symlink() {
                out.symlinks += 1;
            } else if ft.is_dir() {
                if child.len() > limits.max_depth {
                    out.too_deep += 1;
                } else {
                    subdirs.push((e.path(), child));
                }
            } else if ft.is_file() {
                match RootRelativePath::parse(&child.join("/")) {
                    Ok(p) => {
                        let located = locate(&p, roots);
                        out.files.push((e.path(), p, located));
                    }
                    Err(_) => out.invalid_names += 1,
                }
            }
        }
        stack.extend(subdirs.into_iter().rev());
    }
}
