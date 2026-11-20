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
use crate::daemon::Probe;
use crate::environment::{FileSystem, Stat};
use crate::realfs::{
    Encoding, RealFs, RealFsError, WalkLimits, index_claude_sessions, walk, walk_under,
};
use memory_core::domain::{Owner, SourceClass, SourceId, SourceKind};
use memory_core::locator::{Located, RootRelativePath, Roots, locate};
use memory_core::privacy::{Profile, ResolvedSourceFacts};
use std::cell::RefCell;
use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::fs;
use std::io::ErrorKind;
use std::ops::Range;
use std::os::unix::fs::MetadataExt;
use std::path::{Path, PathBuf};
use std::rc::Rc;

/// Most symlinks followed on the last path component before the path is unresolved.
pub const MAX_HOPS: usize = 40;

/// Opaque identity of a file or directory: `(dev << 64) | ino`.
pub fn pack(meta: &fs::Metadata) -> u128 {
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
        | SourceKind::CodexCompressed
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

/// Precedence among files with one (root, owner, id): plain before `.zst`, then the
/// lexicographically first root-relative path. The smaller rank wins.
fn rank(located: &Located, rel: &RootRelativePath) -> (bool, String) {
    let zst = matches!(
        located,
        Located::Source {
            kind: SourceKind::CodexCompressed,
            ..
        }
    );
    (zst, rel.components().join("/"))
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
    // A `.zst` rollout is captured as the logical rollout: plain kind and path, decoded bytes.
    let zst = *kind == SourceKind::CodexCompressed;
    let handle = *handles.entry(target.clone()).or_insert_with(|| {
        let encoding = if zst { Encoding::Zstd } else { Encoding::Plain };
        fs.register_encoded(target, encoding)
    });
    let mut path_string = rel.components().join("/");
    if zst && let Some(plain) = path_string.strip_suffix(".zst") {
        path_string = plain.to_owned();
    }
    Some((
        owner.clone(),
        Source {
            handle,
            id: id.clone(),
            kind: if zst {
                SourceKind::CodexRollout
            } else {
                kind.clone()
            },
            class: class.clone(),
            path: path_string,
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
    fn is_undecodable(error: &RealFsError) -> bool {
        *error == RealFsError::Undecodable
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
    /// Files whose (owner, source id) another file with a lexicographically smaller
    /// root-relative path already holds; not yielded.
    pub duplicate: u64,
    /// Snapshot-class files located but withheld until slice 3 (DESIGN.md 17); neither
    /// yielded nor counted as unknown.
    pub deferred_snapshot: u64,
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
    /// Canonical spelling of each root (FSEvents reports canonical paths); `None` until the
    /// root exists.
    canon: Vec<Option<PathBuf>>,
    /// Per owner, the files of the last walk and the paths noted since, as (root index, path
    /// spelled under the configured root).
    known: HashMap<Owner, BTreeSet<(usize, PathBuf)>>,
}

/// Root-relative path of `rest`, or `None` for a non-UTF-8 or non-plain component.
fn rel_of(rest: &Path) -> Option<RootRelativePath> {
    let mut parts = Vec::new();
    for c in rest.components() {
        match c {
            std::path::Component::Normal(n) => parts.push(n.to_str()?),
            _ => return None,
        }
    }
    RootRelativePath::parse(&parts.join("/")).ok()
}

/// What the walk would yield: every directory on the way is a real directory (the root itself
/// may be a link) and the last component is a regular file, never a link. One `lstat` per
/// component.
fn plain_file(root: &Path, rel: &RootRelativePath) -> bool {
    let mut current = root.to_path_buf();
    let last = rel.components().len().saturating_sub(1);
    for (k, part) in rel.components().iter().enumerate() {
        current.push(part);
        let Ok(meta) = fs::symlink_metadata(&current) else {
            return false;
        };
        if k == last {
            return meta.file_type().is_file();
        }
        if !meta.file_type().is_dir() {
            return false;
        }
    }
    false
}

/// Plain components of a directory path under a root, or `None` for a non-UTF-8 or non-plain
/// component. Empty for the root itself.
fn dir_parts(rest: &Path) -> Option<Vec<String>> {
    rest.components()
        .map(|c| match c {
            std::path::Component::Normal(n) => n.to_str().map(str::to_owned),
            _ => None,
        })
        .collect()
}

/// `root/rel` is a real directory: every component on the way is one, never a link (the root
/// itself may be a link). One `lstat` per component.
fn plain_dir(root: &Path, rel: &[String]) -> bool {
    let is_dir = |p: &Path| fs::symlink_metadata(p).is_ok_and(|m| m.file_type().is_dir());
    let mut current = root.to_path_buf();
    if rel.is_empty() {
        return fs::metadata(&current).is_ok_and(|m| m.is_dir());
    }
    rel.iter().all(|part| {
        current.push(part);
        is_dir(&current)
    })
}

impl WalkSources {
    /// Enumerate `roots`, registering paths in `fs`.
    pub fn new(roots: Vec<(PathBuf, Roots)>, limits: WalkLimits, fs: SharedFs) -> Self {
        let canon = roots
            .iter()
            .map(|(d, _)| fs::canonicalize(d).ok())
            .collect();
        Self {
            roots,
            limits,
            fs,
            handles: BTreeMap::new(),
            counts: Counts::default(),
            canon,
            known: HashMap::new(),
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
        for ((dir, _), canon) in self.roots.iter().zip(self.canon.iter_mut()) {
            if canon.is_none() {
                *canon = fs::canonicalize(dir).ok();
            }
        }
        let Ok(mut fs) = self.fs.0.try_borrow_mut() else {
            counts.busy = true;
            self.counts = counts;
            return Vec::new();
        };
        // The Claude session index is derived: rebuilt from `projects/` before every locate,
        // and kept in `roots` so `note` and `of` resolve as the last walk did.
        self.roots = self
            .roots
            .iter()
            .map(|(d, r)| (d.clone(), index_claude_sessions(d, r)))
            .collect();
        let found = walk(&self.roots, &self.limits);
        counts.symlinks = found.symlinks;
        counts.unreadable = found.unreadable;
        counts.truncated = found.truncated;
        let mut out = Vec::new();
        self.known.clear();
        // Per (root, owner, id), the lexicographically first root-relative path is kept.
        let mut first: HashMap<(usize, Owner, SourceId), (bool, String)> = HashMap::new();
        for (index, (dir, _)) in self.roots.iter().enumerate() {
            for (_, rel, located) in found.files.iter().filter(|f| f.0.starts_with(dir)) {
                if let Located::Source {
                    owner,
                    id,
                    class: SourceClass::Append,
                    ..
                } = located
                {
                    let key = rank(located, rel);
                    first
                        .entry((index, owner.clone(), id.clone()))
                        .and_modify(|k| {
                            if key < *k {
                                k.clone_from(&key);
                            }
                        })
                        .or_insert(key);
                }
            }
        }
        for (index, (dir, _)) in self.roots.iter().enumerate() {
            for (path, rel, located) in found.files.iter().filter(|f| f.0.starts_with(dir)) {
                if let Located::Source {
                    owner,
                    id,
                    class: SourceClass::Append,
                    ..
                } = located
                    && first
                        .get(&(index, owner.clone(), id.clone()))
                        .is_some_and(|k| *k != rank(located, rel))
                {
                    counts.duplicate += 1;
                    continue;
                }
                match located {
                    Located::UnknownKind => counts.unknown_kind += 1,
                    Located::UnknownOwner => counts.unknown_owner += 1,
                    Located::Source {
                        class: SourceClass::Snapshot,
                        ..
                    } => {
                        counts.deferred_snapshot += 1;
                        continue;
                    }
                    Located::Source { .. } => {}
                }
                if let Some((owner, source)) =
                    to_source(&mut fs, &mut self.handles, path, dir, rel, located)
                {
                    counts.yielded += 1;
                    counts.unresolved += u64::from(!source.facts.path_resolved);
                    self.known
                        .entry(owner.clone())
                        .or_default()
                        .insert((index, path.clone()));
                    out.push((owner, source));
                }
            }
        }
        self.counts = counts;
        out
    }

    fn walk_complete(&self) -> bool {
        !self.counts.truncated && !self.counts.busy
    }

    fn locate_absent(&mut self, id: &SourceId, path: &str) -> Probe {
        let namespace = id.as_str().split(':').next().unwrap_or_default();
        let Some((dir, _)) = self.roots.iter().find(|(_, r)| r.namespace() == namespace) else {
            return Probe::Skip;
        };
        // An unmounted or moved root never deletes.
        if !fs::metadata(dir).is_ok_and(|m| m.is_dir()) {
            return Probe::Skip;
        }
        let Ok(rel) = RootRelativePath::parse(path) else {
            return Probe::Skip;
        };
        let plain = dir.join(rel.components().join("/"));
        // A `.zst` rollout is stored under its logical (plain) path.
        let mut packed = plain.clone().into_os_string();
        packed.push(".zst");
        let packed = PathBuf::from(packed);
        let (target, encoding) =
            if fs::symlink_metadata(&plain).is_err() && fs::symlink_metadata(&packed).is_ok() {
                (packed, Encoding::Zstd)
            } else {
                (plain, Encoding::Plain)
            };
        let Ok(mut shared) = self.fs.0.try_borrow_mut() else {
            return Probe::Skip;
        };
        let handle = *self
            .handles
            .entry(target.clone())
            .or_insert_with(|| shared.register_encoded(target, encoding));
        Probe::Handle(handle)
    }

    fn note(&mut self, path: &Path) -> Option<Owner> {
        for (index, (dir, roots)) in self.roots.iter().enumerate() {
            let canon = self.canon.get(index).and_then(Option::as_ref);
            let Some(rel) = [Some(dir), canon]
                .into_iter()
                .flatten()
                .find_map(|prefix| path.strip_prefix(prefix).ok())
                .and_then(rel_of)
            else {
                continue;
            };
            let Located::Source {
                owner,
                class: SourceClass::Append,
                ..
            } = locate(&rel, roots)
            else {
                continue;
            };
            let spelled = dir.join(rel.components().join("/"));
            self.known
                .entry(owner.clone())
                .or_default()
                .insert((index, spelled));
            return Some(owner);
        }
        None
    }

    fn read_dir(&mut self, path: &Path) -> (Vec<Owner>, bool) {
        let Some((index, rel)) = self.roots.iter().enumerate().find_map(|(index, (dir, _))| {
            let canon = self.canon.get(index).and_then(Option::as_ref);
            [Some(dir), canon]
                .into_iter()
                .flatten()
                .find_map(|prefix| path.strip_prefix(prefix).ok())
                .and_then(dir_parts)
                .map(|rel| (index, rel))
        }) else {
            return (Vec::new(), false);
        };
        let Some((dir, roots)) = self.roots.get(index) else {
            return (Vec::new(), false);
        };
        if !plain_dir(dir, &rel) {
            return (Vec::new(), false);
        }
        let found = walk_under(dir, &rel, roots, &self.limits);
        let mut owners = Vec::new();
        let append: Vec<PathBuf> = found
            .files
            .iter()
            .filter(|(_, _, located)| {
                matches!(
                    located,
                    Located::Source {
                        class: SourceClass::Append,
                        ..
                    }
                )
            })
            .map(|(file, _, _)| file.clone())
            .collect();
        for file in append {
            if let Some(owner) = self.note(&file)
                && !owners.contains(&owner)
            {
                owners.push(owner);
            }
        }
        (owners, found.truncated)
    }

    fn of(&mut self, owner: &Owner) -> Vec<Source> {
        let Some(paths) = self.known.get(owner) else {
            return Vec::new();
        };
        let Ok(mut fs) = self.fs.0.try_borrow_mut() else {
            return Vec::new();
        };
        let mut live = Vec::new();
        for (index, path) in paths {
            let Some((dir, roots)) = self.roots.get(*index) else {
                continue;
            };
            let Some(rel) = path.strip_prefix(dir).ok().and_then(rel_of) else {
                continue;
            };
            if rel.components().len() > self.limits.max_depth.saturating_add(1)
                || !plain_file(dir, &rel)
            {
                continue;
            }
            // No snapshot row reaches `known`: `enumerate` and `note` both leave them out, and a
            // path's class depends on its pattern alone, not on the session index.
            let located = locate(&rel, roots);
            live.push((*index, path, dir, rel, located));
        }
        // One source per (root, id): the lexicographically first root-relative path.
        let mut first: HashMap<(usize, SourceId), (bool, String)> = HashMap::new();
        for (index, _, _, rel, located) in &live {
            if let Located::Source { id, .. } = located {
                let key = rank(located, rel);
                first
                    .entry((*index, id.clone()))
                    .and_modify(|k| {
                        if key < *k {
                            k.clone_from(&key);
                        }
                    })
                    .or_insert(key);
            }
        }
        let mut out = Vec::new();
        for (index, path, dir, rel, located) in &live {
            if let Located::Source { id, .. } = located
                && first
                    .get(&(*index, id.clone()))
                    .is_some_and(|k| *k != rank(located, rel))
            {
                continue;
            }
            if let Some((found, source)) =
                to_source(&mut fs, &mut self.handles, path, dir, rel, located)
                && found == *owner
            {
                out.push(source);
            }
        }
        out
    }
}
