//! Real-filesystem adapter (slice 2): [`RealFs`] implements [`FileSystem`] over absolute
//! paths, and [`walk`] is the bounded safety reconcile (DESIGN.md 10.2, 10.3).
//!
//! Stat observation follows DESIGN.md 7.1 and 7.2: `symlink_metadata`, so a symlink is
//! reported as itself (its own inode, size and times) and never followed. `walk` skips
//! symlinks entirely and counts them. No `unsafe`; open flags and errno come from `rustix`.

use crate::environment::{FileSystem, Stat};
use crate::zstd_stream::{Failure, Stream};
use memory_core::domain::{Birth, FileId};
use memory_core::locator::{ClaudeSessionIndex, Located, RootRelativePath, Roots, locate};
use rustix::fs::{Mode, OFlags};
use rustix::io::Errno;
use std::fs;
use std::io::{ErrorKind, Read, Seek, SeekFrom};
use std::os::unix::fs::MetadataExt;
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
    /// The opened source is not the file the last `stat` observed (a symlink, another
    /// inode, or not a regular file). Nothing was read. Capture maps every read error to
    /// `Unavailable`, which is retried later (DESIGN.md 7.2, 8.2).
    #[error("source changed between stat and read")]
    Changed,
    /// The file is registered as zstd and its bytes are not a valid stream of whole frames
    /// within the window bound. Capture withholds the source (never uploads the raw bytes).
    #[error("undecodable zstd stream")]
    Undecodable,
    /// An operating-system error other than absence; only the kind is kept.
    #[error("io error: {0}")]
    Io(ErrorKind),
}

/// Operation counters, for the cost accounting of DESIGN.md 10.3.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Counters {
    /// `stat` calls.
    pub stats: u64,
    /// `read` calls that reached the operating system.
    pub reads: u64,
    /// Bytes returned by `read`.
    pub bytes_read: u64,
}

/// How the bytes of a registered file are encoded on disk. A fact of the physical file, never
/// stored: capture sees only the logical (decoded) bytes.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Encoding {
    /// The file is the logical bytes.
    #[default]
    Plain,
    /// One or more whole zstd frames; `stat` reports the decoded length and `read` decoded
    /// ranges.
    Zstd,
}

/// What a decoded length is valid for: the physical identity, size and modification time.
#[derive(Debug, Clone, PartialEq, Eq)]
struct Key {
    id: FileId,
    size: u64,
    mtime_ns: i64,
}

fn key_of(meta: &fs::Metadata) -> Key {
    Key {
        id: file_id(meta),
        size: meta.size(),
        mtime_ns: meta
            .mtime()
            .saturating_mul(1_000_000_000)
            .saturating_add(meta.mtime_nsec()),
    }
}

/// The one live decoder of a [`RealFs`].
#[derive(Debug)]
struct Live {
    handle: usize,
    key: Key,
    stream: Stream,
}

/// [`FileSystem`] over real paths. Handles are indices from [`RealFs::register`].
///
/// A handle registered with [`Encoding::Zstd`] is decoded on the fly. The decoded length is
/// found by one streaming decode and cached per handle, keyed by the physical key (any change
/// decodes again; undecodable is cached the same way). At most one streaming decoder is alive:
/// a `read` starting at or after its position continues it, an earlier start restarts from 0.
#[derive(Debug, Default)]
pub struct RealFs {
    paths: Vec<PathBuf>,
    encodings: Vec<Encoding>,
    /// Physical observation from the latest `stat` per handle; `read` must open the same file.
    observed: Vec<Option<Key>>,
    /// Decoded length per zstd handle (`None` inside: undecodable), valid for its key.
    lengths: Vec<Option<(Key, Option<u64>)>>,
    live: Option<Live>,
    counters: Counters,
}

impl RealFs {
    /// Empty adapter.
    pub fn new() -> Self {
        Self::default()
    }

    /// Bind an absolute path to a new handle (plain bytes).
    pub fn register(&mut self, path: impl Into<PathBuf>) -> u64 {
        self.register_encoded(path, Encoding::Plain)
    }

    /// Bind an absolute path with the encoding of its bytes to a new handle.
    pub fn register_encoded(&mut self, path: impl Into<PathBuf>, encoding: Encoding) -> u64 {
        self.paths.push(path.into());
        self.encodings.push(encoding);
        self.observed.push(None);
        self.lengths.push(None);
        (self.paths.len() - 1) as u64
    }

    /// Counters so far.
    pub const fn counters(&self) -> Counters {
        self.counters
    }

    fn index(handle: u64) -> Result<usize, RealFsError> {
        usize::try_from(handle).map_err(|_| RealFsError::UnknownHandle)
    }

    fn path(&self, handle: u64) -> Result<&Path, RealFsError> {
        usize::try_from(handle)
            .ok()
            .and_then(|i| self.paths.get(i))
            .map(PathBuf::as_path)
            .ok_or(RealFsError::UnknownHandle)
    }

    fn encoding(&self, i: usize) -> Encoding {
        self.encodings.get(i).copied().unwrap_or_default()
    }

    /// Open without following a final symlink, then check the handle itself: a regular
    /// file, and (when given) the file the last stat saw. Intermediate path components are
    /// still resolved by the kernel: a directory component swapped for a symlink after
    /// `resolve` is a residual race that DESIGN.md 8.2 does not address. `NONBLOCK` keeps a
    /// FIFO swapped in after the stat from blocking the open; it does not change reads of
    /// regular files.
    fn open_checked(
        &self,
        source: u64,
        expected: Option<&FileId>,
    ) -> Result<(fs::File, fs::Metadata), RealFsError> {
        let io = |e: std::io::Error| RealFsError::Io(e.kind());
        let file = rustix::fs::open(
            self.path(source)?,
            OFlags::RDONLY | OFlags::NOFOLLOW | OFlags::NONBLOCK | OFlags::CLOEXEC,
            Mode::empty(),
        )
        .map(fs::File::from)
        .map_err(|e| {
            if e == Errno::LOOP {
                RealFsError::Changed
            } else {
                io(e.into())
            }
        })?;
        let meta = file.metadata().map_err(io)?;
        if !meta.is_file() || expected.is_some_and(|id| *id != file_id(&meta)) {
            return Err(RealFsError::Changed);
        }
        Ok((file, meta))
    }

    /// Decoded length of a zstd handle for the physical `key`, from the cache or one decode.
    fn decoded_len(&mut self, source: u64, key: &Key) -> Result<u64, RealFsError> {
        let i = Self::index(source)?;
        if let Some(Some((k, len))) = self.lengths.get(i)
            && k == key
        {
            return len.ok_or(RealFsError::Undecodable);
        }
        // The transient decode takes the place of the live decoder.
        self.live = None;
        let (file, _) = self.open_checked(source, Some(&key.id))?;
        let len = match Stream::new(file).and_then(|mut s| s.drain_len()) {
            Ok(n) => Some(n),
            Err(Failure::Undecodable) => None,
            Err(Failure::Io(k)) => return Err(RealFsError::Io(k)),
        };
        if let Some(slot) = self.lengths.get_mut(i) {
            *slot = Some((key.clone(), len));
        }
        len.ok_or(RealFsError::Undecodable)
    }

    fn read_decoded(
        &mut self,
        source: u64,
        file: fs::File,
        key: Key,
        range: std::ops::Range<u64>,
        len: u64,
    ) -> Result<Vec<u8>, RealFsError> {
        let i = Self::index(source)?;
        let reuse = self
            .live
            .as_ref()
            .is_some_and(|l| l.handle == i && l.key == key && l.stream.pos() <= range.start);
        if !reuse {
            self.live = None;
            let stream = Stream::new(file).map_err(|f| match f {
                Failure::Undecodable => RealFsError::Undecodable,
                Failure::Io(k) => RealFsError::Io(k),
            })?;
            self.live = Some(Live {
                handle: i,
                key: key.clone(),
                stream,
            });
        }
        let known = self
            .lengths
            .get(i)
            .and_then(Option::as_ref)
            .filter(|(k, _)| *k == key)
            .and_then(|(_, n)| *n);
        let want = known.map_or(len.min(1 << 20), |n| n.saturating_sub(range.start).min(len));
        let cap = usize::try_from(want).map_err(|_| RealFsError::InvalidRange)?;
        let live = self.live.as_mut().ok_or(RealFsError::Changed)?;
        match live.stream.take(range.start, len, cap) {
            Ok(buf) => Ok(buf),
            Err(Failure::Undecodable) => {
                self.live = None;
                Err(RealFsError::Undecodable)
            }
            Err(Failure::Io(k)) => {
                self.live = None;
                Err(RealFsError::Io(k))
            }
        }
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
        let key = result.as_ref().ok().map(key_of);
        let regular = result.as_ref().is_ok_and(fs::Metadata::is_file);
        if let Some(slot) = usize::try_from(source)
            .ok()
            .and_then(|i| self.observed.get_mut(i))
        {
            slot.clone_from(&key);
        }
        match (result, key) {
            (Ok(_), Some(key)) => {
                let size = if regular && self.encoding(Self::index(source)?) == Encoding::Zstd {
                    self.decoded_len(source, &key)?
                } else {
                    key.size
                };
                Ok(Some(Stat {
                    id: key.id,
                    size,
                    mtime_ns: key.mtime_ns,
                }))
            }
            (Err(e), _) if e.kind() == ErrorKind::NotFound => Ok(None),
            (Err(e), _) => Err(RealFsError::Io(e.kind())),
            (Ok(_), None) => Ok(None),
        }
    }

    fn read(&mut self, source: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, RealFsError> {
        let len = range
            .end
            .checked_sub(range.start)
            .ok_or(RealFsError::InvalidRange)?;
        let io = |e: std::io::Error| RealFsError::Io(e.kind());
        let i = Self::index(source)?;
        let observed = self.observed.get(i).and_then(Option::as_ref).cloned();
        let (mut file, meta) = self.open_checked(source, observed.as_ref().map(|k| &k.id))?;
        if self.encoding(i) == Encoding::Zstd {
            let key = key_of(&meta);
            if observed.is_some_and(|k| k != key) {
                return Err(RealFsError::Changed);
            }
            let buf = self.read_decoded(source, file, key, range, len)?;
            self.counters.reads += 1;
            self.counters.bytes_read += buf.len() as u64;
            return Ok(buf);
        }
        file.seek(SeekFrom::Start(range.start)).map_err(io)?;
        let file_len = meta.len().saturating_sub(range.start);
        // Exact allocation: a growing buffer would leave several large freed blocks behind.
        let size = file_len.min(len);
        let mut buf =
            Vec::with_capacity(usize::try_from(size).map_err(|_| RealFsError::InvalidRange)?);
        file.take(len).read_to_end(&mut buf).map_err(io)?;
        self.counters.reads += 1;
        self.counters.bytes_read += buf.len() as u64;
        Ok(buf)
    }

    fn is_undecodable(error: &RealFsError) -> bool {
        *error == RealFsError::Undecodable
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

/// Attach to `roots` the UUID -> project slug facts of `root`: one `read_dir` of `projects/`
/// and one per slug directory, no file content. Only a regular `<uuid>.jsonl` directly under a
/// real slug directory counts (the locator's `record` validates both names); a UUID under two
/// slugs stays ambiguous. Derived on every call, never stored.
pub fn index_claude_sessions(root: &Path, roots: &Roots) -> Roots {
    let mut index = ClaudeSessionIndex::new();
    let projects = root.join("projects");
    let plain_dir = |m: Result<fs::Metadata, std::io::Error>| m.is_ok_and(|m| m.is_dir());
    if plain_dir(fs::symlink_metadata(&projects))
        && let Ok(slugs) = fs::read_dir(&projects)
    {
        for slug in slugs.flatten() {
            let Some(slug_name) = slug.file_name().to_str().map(str::to_owned) else {
                continue;
            };
            if !slug.file_type().is_ok_and(|t| t.is_dir()) {
                continue;
            }
            let Ok(files) = fs::read_dir(slug.path()) else {
                continue;
            };
            for file in files.flatten() {
                if !file.file_type().is_ok_and(|t| t.is_file()) {
                    continue;
                }
                if let Some(stem) = file
                    .file_name()
                    .to_str()
                    .and_then(|n| n.strip_suffix(".jsonl"))
                {
                    index.record(stem, &slug_name).ok();
                }
            }
        }
    }
    roots.clone().with_claude_sessions(index)
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

/// Walk only `dir`, a directory under `root` whose root-relative components are `rel` (empty for
/// the root itself). Depth, entry bound, symlink and locate rules are those of [`walk`]; files
/// are returned sorted by relative path.
pub fn walk_under(root: &Path, rel: &[String], roots: &Roots, limits: &WalkLimits) -> Walk {
    let mut out = Walk::default();
    let mut dir = root.to_path_buf();
    dir.extend(rel);
    walk_dir(&dir, rel.to_vec(), roots, limits, &mut out);
    out.files
        .sort_by(|a, b| a.1.components().cmp(b.1.components()));
    out
}

fn walk_root(root: &Path, roots: &Roots, limits: &WalkLimits, out: &mut Walk) {
    walk_dir(root, Vec::new(), roots, limits, out);
}

fn walk_dir(
    start: &Path,
    start_rel: Vec<String>,
    roots: &Roots,
    limits: &WalkLimits,
    out: &mut Walk,
) {
    let mut stack = vec![(start.to_path_buf(), start_rel)];
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
