//! In-memory `FileSystem` keyed by source handle, shared by the integration tests.
//!
//! Every item must be used by every test crate that includes this file (see `store.rs`:
//! `#[allow]` is denied, so an item only some crates use would fail the lint gate). Hence the
//! API is `set` (which every includer calls) plus public fields for the rest; `replace` and
//! `generation` are live because `set` and `stat` use them.
use memory::environment::{FileSystem, Stat};
use memory_core::domain::{Birth, FileId};
use std::collections::BTreeMap;

/// Files by handle. A file that was only inserted into `files` keeps the default mtime, so a
/// change shows through its size alone.
#[derive(Default)]
pub struct MemFs {
    /// Current bytes per handle.
    pub files: BTreeMap<u64, Vec<u8>>,
    /// Modification time per handle, as set by `set` and `replace`.
    pub mtimes: BTreeMap<u64, i64>,
    /// File identity generation per handle: a rename-replace gives the path a new identity.
    pub generations: BTreeMap<u64, u64>,
    tick: i64,
}

impl MemFs {
    /// Replace a source's bytes. A real change moves the mtime, as a real write does; the
    /// identity stays.
    pub fn set(&mut self, source: u64, bytes: &[u8]) {
        if self.files.get(&source).map(Vec::as_slice) != Some(bytes) {
            self.replace(source, bytes, self.generation(source));
        }
    }

    /// Put a new file with identity `generation` at the path (a rename over the old one).
    pub fn replace(&mut self, source: u64, bytes: &[u8], generation: u64) {
        self.generations.insert(source, generation);
        self.tick += 1;
        self.mtimes.insert(source, 1_000 + self.tick);
        self.files.insert(source, bytes.to_vec());
    }

    /// Identity generation of the file at `source` (0 until a `replace` says otherwise).
    pub fn generation(&self, source: u64) -> u64 {
        self.generations.get(&source).copied().unwrap_or(0)
    }
}

impl FileSystem for MemFs {
    type Error = ();
    fn stat(&mut self, source: u64) -> Result<Option<Stat>, ()> {
        let generation = self.generation(source);
        Ok(self.files.get(&source).map(|b| Stat {
            id: FileId {
                dev: 1,
                ino: source | generation << 32,
                birth: Birth::Unavailable,
            },
            size: u64::try_from(b.len()).unwrap_or(0),
            mtime_ns: self.mtimes.get(&source).copied().unwrap_or(1_000),
        }))
    }
    fn read(&mut self, source: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, ()> {
        let bytes = self.files.get(&source).ok_or(())?;
        let start = usize::try_from(range.start).map_err(|_| ())?;
        let end = usize::try_from(range.end).map_err(|_| ())?.min(bytes.len());
        Ok(bytes.get(start..end).unwrap_or_default().to_vec())
    }
}
