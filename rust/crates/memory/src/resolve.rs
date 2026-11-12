//! Source resolution (slice 2): turns walked paths into privacy facts and capture sources.
//!
//! DESIGN.md 7.2 (reconcile resolves every symlink link and the final identity) and 8.1-8.3
//! (admission facts: identities of every link and the final file, file identity, ancestor
//! identities, `path_resolved`/`root_resolved`, owner, profile; anything unresolved fails
//! closed). Facts are observations; `privacy::admit` decides.
//!
//! * Identity packing: `file_identity = (dev << 64) | ino` as `u128`. Ancestor identities use
//!   the same packing for every directory from the canonical final path's parent up to `/`
//!   (so the root is always included when the file is under it; the root's own identity is
//!   added when the file escapes it).
//! * Hop bound: [`MAX_HOPS`] (40) links on the last component; more, or a loop, is
//!   unresolved. Directory-component links are resolved by `canonicalize`, which also
//!   rejects loops.
//! * No `unsafe`, no new dependencies, no panics; any I/O failure clears a resolved flag.

use crate::capture::Source;
use crate::environment::{FileSystem, Stat};
use crate::realfs::{RealFs, RealFsError, WalkLimits, walk};
use memory_core::domain::{Owner, SourceKind};
use memory_core::locator::{Located, RootRelativePath, Roots};
use memory_core::privacy::{Profile, ResolvedSourceFacts};
use std::cell::RefCell;
use std::collections::BTreeMap;
use std::fs;
use std::io::ErrorKind;
use std::ops::Range;
use std::os::unix::fs::MetadataExt;
use std::path::{Path, PathBuf};
use std::rc::Rc;

/// Most symlinks followed on the last path component before the path is unresolved.
pub const MAX_HOPS: usize = 40;

fn pack(meta: &fs::Metadata) -> u128 {
    (u128::from(meta.dev()) << 64) | u128::from(meta.ino())
}

fn spelling(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

/// Spellings of every link on the last component; the flag is false on loop, overflow or I/O.
fn chain(path: &Path) -> (Vec<String>, bool) {
    let mut spellings = vec![spelling(path)];
    let mut current = path.to_path_buf();
    for _ in 0..=MAX_HOPS {
        let Ok(meta) = fs::symlink_metadata(&current) else {
            return (spellings, false);
        };
        if !meta.file_type().is_symlink() {
            return (spellings, true);
        }
        let Ok(target) = fs::read_link(&current) else {
            return (spellings, false);
        };
        current = current
            .parent()
            .map_or_else(|| target.clone(), |p| p.join(&target));
        spellings.push(spelling(&current));
    }
    (spellings, false)
}

fn owner_string(owner: &Owner) -> String {
    match owner {
        Owner::Session(k) => format!(
            "{}/{}/{:?}/{}",
            k.archive.as_str(),
            k.machine.as_str(),
            k.agent,
            k.session.as_str()
        ),
        Owner::Shared {
            archive,
            machine,
            agent,
        } => format!("{}/{}/{agent:?}", archive.as_str(), machine.as_str()),
    }
}

/// Profile of a located kind: transcripts, structured config, small text assets, the rest.
fn profile_of(kind: &SourceKind) -> Profile {
    match kind {
        SourceKind::ClaudeTranscript
        | SourceKind::ClaudeSubagent
        | SourceKind::CodexRollout
        | SourceKind::PiSession => Profile::Transcript,
        SourceKind::ClaudeConfig
        | SourceKind::CodexConfig
        | SourceKind::PiConfig
        | SourceKind::PiSettings
        | SourceKind::PiModels => Profile::Config,
        SourceKind::ClaudeMemory | SourceKind::CodexMemory | SourceKind::ClaudePlan => {
            Profile::TextAsset
        }
        SourceKind::ClaudeToolResults
        | SourceKind::ClaudeTodos
        | SourceKind::ClaudeTasks
        | SourceKind::ClaudeTeams
        | SourceKind::ClaudeFileHistory
        | SourceKind::ClaudeDebug
        | SourceKind::ClaudeHistory
        | SourceKind::CodexCompressed
        | SourceKind::CodexStore
        | SourceKind::CodexHistory
        | SourceKind::PiSnapshot
        | SourceKind::ClaudeSessionEnv
        | SourceKind::ClaudePaste
        | SourceKind::ClaudeUsage
        | SourceKind::ClaudeAux
        | SourceKind::CodexLog
        | SourceKind::PiLog
        | SourceKind::PiAux => Profile::OtherKnown,
    }
}

/// Facts plus the path capture should read (canonical final when resolved).
fn resolve(path: &Path, root: &Path, located: &Located) -> (ResolvedSourceFacts, Option<PathBuf>) {
    let (owner, profile) = match located {
        Located::Source { owner, kind, .. } => (Some(owner_string(owner)), profile_of(kind)),
        _ => (None, Profile::Unknown),
    };
    let root_canon = fs::canonicalize(root).ok();
    let root_meta = root_canon.as_deref().and_then(|r| fs::metadata(r).ok());
    let root_resolved = root_meta.as_ref().is_some_and(fs::Metadata::is_dir);
    let (mut identities, chain_ok) = chain(path);
    let mut ancestors: Vec<u128> = Vec::new();
    let mut file_identity = None;
    let mut path_resolved = chain_ok;
    let mut final_path = None;
    if chain_ok {
        match fs::canonicalize(path) {
            Ok(canon) => {
                identities.push(spelling(&canon));
                match fs::symlink_metadata(&canon) {
                    Ok(m) if m.is_file() => file_identity = Some(pack(&m)),
                    _ => path_resolved = false,
                }
                for dir in canon.ancestors().skip(1) {
                    match fs::metadata(dir) {
                        Ok(m) => ancestors.push(pack(&m)),
                        Err(_) => path_resolved = false,
                    }
                }
                final_path = Some(canon);
            }
            Err(_) => path_resolved = false,
        }
    }
    if let Some(m) = root_meta.as_ref().filter(|_| root_resolved) {
        ancestors.push(pack(m));
    }
    ancestors.sort_unstable();
    ancestors.dedup();
    if !path_resolved {
        file_identity = None;
        final_path = None;
    }
    let facts = ResolvedSourceFacts {
        identities,
        file_identity,
        ancestor_identities: ancestors,
        path_resolved,
        root_resolved,
        owner,
        profile,
    };
    (facts, final_path)
}

/// Resolve one path for privacy admission. Never panics; unresolved is a cleared flag.
pub fn resolve_facts(path: &Path, root: &Path, located: &Located) -> ResolvedSourceFacts {
    resolve(path, root, located).0
}

/// Build a capture source for a located file. `None` for withheld (unlocated) rows, which
/// have no id, kind or owner to capture under. The registered path is the canonical final
/// path (the filesystem seam never follows links), or the logical path when unresolved so the
/// source stays observable and capture blocks it on the facts.
pub fn to_source(
    fs: &mut RealFs,
    handles: &mut BTreeMap<PathBuf, u64>,
    path: &Path,
    root: &Path,
    rel: &RootRelativePath,
    located: &Located,
) -> Option<(Owner, Source)> {
    let Located::Source {
        owner,
        kind,
        class,
        id,
        ..
    } = located
    else {
        return None;
    };
    let (facts, final_path) = resolve(path, root, located);
    let target = final_path.unwrap_or_else(|| path.to_path_buf());
    let handle = *handles
        .entry(target.clone())
        .or_insert_with(|| fs.register(target));
    Some((
        owner.clone(),
        Source {
            handle,
            id: id.clone(),
            kind: kind.clone(),
            class: class.clone(),
            path: rel.components().join("/"),
            facts,
        },
    ))
}

/// [`RealFs`] shared between the daemon's filesystem seam and [`WalkSources`].
#[derive(Clone, Debug, Default)]
pub struct SharedFs(Rc<RefCell<RealFs>>);

impl SharedFs {
    /// Fresh adapter.
    pub fn new() -> Self {
        Self::default()
    }
    /// The shared cell.
    pub fn cell(&self) -> Rc<RefCell<RealFs>> {
        Rc::clone(&self.0)
    }
}

impl FileSystem for SharedFs {
    type Error = RealFsError;
    fn stat(&mut self, source: u64) -> Result<Option<Stat>, RealFsError> {
        self.0
            .try_borrow_mut()
            .map_err(|_| RealFsError::Io(ErrorKind::WouldBlock))?
            .stat(source)
    }
    fn read(&mut self, source: u64, range: Range<u64>) -> Result<Vec<u8>, RealFsError> {
        self.0
            .try_borrow_mut()
            .map_err(|_| RealFsError::Io(ErrorKind::WouldBlock))?
            .read(source, range)
    }
}

/// Counts of the last enumeration. Withheld rows are counted, never yielded.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Counts {
    /// Sources yielded.
    pub yielded: u64,
    /// Files no locator pattern matched.
    pub unknown_kind: u64,
    /// Files with a known pattern but no unambiguous owner.
    pub unknown_owner: u64,
    /// Yielded sources whose facts are unresolved (capture will block them).
    pub unresolved: u64,
    /// Symlinks the walk skipped.
    pub symlinks: u64,
    /// Entries the walk could not read.
    pub unreadable: u64,
    /// The walk hit its entry bound.
    pub truncated: bool,
    /// The shared filesystem was busy; nothing was enumerated.
    pub busy: bool,
}

/// Daemon source enumeration over configured roots (DESIGN.md 7.2 safety reconcile).
///
/// Unlocated rows have no `SourceId`, kind or owner, so capture cannot account for them
/// (D20 withhold accounting is per located source); they are only counted.
pub struct WalkSources {
    roots: Vec<(PathBuf, Roots)>,
    limits: WalkLimits,
    fs: SharedFs,
    handles: BTreeMap<PathBuf, u64>,
    counts: Counts,
}

impl WalkSources {
    /// Enumerate `roots`, registering paths in `fs`.
    pub fn new(roots: Vec<(PathBuf, Roots)>, limits: WalkLimits, fs: SharedFs) -> Self {
        Self {
            roots,
            limits,
            fs,
            handles: BTreeMap::new(),
            counts: Counts::default(),
        }
    }
    /// Counts of the last enumeration.
    pub const fn counts(&self) -> Counts {
        self.counts
    }
}

impl crate::daemon::Sources for WalkSources {
    fn enumerate(&mut self) -> Vec<(Owner, Source)> {
        let mut counts = Counts::default();
        let Ok(mut fs) = self.fs.0.try_borrow_mut() else {
            counts.busy = true;
            self.counts = counts;
            return Vec::new();
        };
        let found = walk(&self.roots, &self.limits);
        counts.symlinks = found.symlinks;
        counts.unreadable = found.unreadable;
        counts.truncated = found.truncated;
        let mut out = Vec::new();
        for (dir, _) in &self.roots {
            for (path, rel, located) in found.files.iter().filter(|f| f.0.starts_with(dir)) {
                match located {
                    Located::UnknownKind => counts.unknown_kind += 1,
                    Located::UnknownOwner => counts.unknown_owner += 1,
                    Located::Source { .. } => {}
                }
                if let Some((owner, source)) =
                    to_source(&mut fs, &mut self.handles, path, dir, rel, located)
                {
                    counts.yielded += 1;
                    counts.unresolved += u64::from(!source.facts.path_resolved);
                    out.push((owner, source));
                }
            }
        }
        self.counts = counts;
        out
    }
}
