//! Pure root coordinates (slice 1): declared logical roots and their observed physical
//! spellings resolve to one root identity and owner namespace.
//!
//! The shell supplies every filesystem fact as data. An alias is accepted only when it is
//! *proven*: either an explicitly declared `(root, physical path)` pair, or a directory
//! observation whose opaque identity equals the identity observed at a declared root. Nothing
//! is ever inferred from spelling, so case is never folded and `/var` is never assumed to be
//! `/private/var`. Every spelling is kept in the result so deny checks can run over all of them.
//!
//! Mirrors `internal/capturecoordinates` (root routes, `validRoutes`, projection, ownership
//! selected after projection). Differences are listed in
//! `docs/rust-rewrite/implementation/11-coordinates.md`.
use crate::domain::Agent;
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, BTreeSet};

/// Most declared roots, aliases and observations together (Go `MaxRoutes`).
pub const MAX_ROUTES: usize = 128;
/// Most nested alias routes over one spelling (Go `MaxDepth`).
pub const MAX_DEPTH: usize = 8;

/// Rejected coordinate input; never carries the rejected text.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum CoordError {
    /// More declarations than [`MAX_ROUTES`].
    #[error("too many declarations")]
    TooMany,
    /// Not absolute, not clean, or contains NUL, CR or LF.
    #[error("invalid path")]
    InvalidPath,
    /// An alias names a root that is not declared.
    #[error("alias of undeclared root")]
    UnknownRoot,
    /// Declarations or observations disagree or overlap ambiguously.
    #[error("ambiguous declarations")]
    Ambiguous,
    /// Alias routes nest deeper than [`MAX_DEPTH`].
    #[error("alias nesting too deep")]
    TooDeep,
    /// The path is under no declared root or proven alias.
    #[error("path not under a declared root")]
    Unowned,
}

/// A declared logical root.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Root {
    /// Owning agent.
    pub agent: Agent,
    /// Clean absolute logical path.
    pub path: String,
}

/// An explicitly declared alias pair: `path` is a physical spelling of `root`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RootAlias {
    /// Owning agent.
    pub agent: Agent,
    /// A declared root.
    pub root: String,
    /// Physical spelling.
    pub path: String,
}

/// A shell observation: the opaque identity (device/inode-like) of the directory at `path`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct IdentityObservation {
    /// Agent the observation was made for; identities never alias across agents.
    pub agent: Agent,
    /// Clean absolute spelling that was inspected.
    pub path: String,
    /// Opaque directory identity.
    pub identity: u128,
}

/// Compiled, immutable alias inventory.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Inventory {
    roots: BTreeSet<(u8, String)>,
    routes: BTreeSet<(u8, String, String)>,
}

/// One resolved path with every identity spelling retained.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Coordinate {
    /// Owning agent.
    pub agent: Agent,
    /// Logical root identity.
    pub root: String,
    /// Path projected onto the logical root.
    pub logical_path: String,
    /// Path as resolved.
    pub path: String,
    /// Owner namespace derived from agent and logical root (valid `RootNamespace`).
    pub namespace: String,
    /// Declared spellings of the same file, including the logical path.
    pub equivalent_paths: BTreeSet<String>,
    /// Physical and logical root spellings for legacy namespace policy checks only.
    pub legacy_roots: BTreeSet<String>,
    /// Every spelling for deny checks: shell chain, resolved path and equivalents.
    pub identities: BTreeSet<String>,
}

fn agent_rank(agent: &Agent) -> u8 {
    match agent {
        Agent::Claude => 0,
        Agent::Codex => 1,
        Agent::Pi => 2,
    }
}

fn valid_path(p: &str) -> bool {
    p.starts_with('/')
        && !p.contains(['\0', '\r', '\n'])
        && (p == "/"
            || p.split('/')
                .skip(1)
                .all(|c| !c.is_empty() && c != "." && c != ".."))
}

fn beneath(root: &str, path: &str) -> bool {
    path == root || path.starts_with(&format!("{}/", root.trim_end_matches('/')))
}

fn project(root: &str, target: &str, path: &str) -> String {
    let rel = path
        .strip_prefix(root.trim_end_matches('/'))
        .unwrap_or_default();
    let joined = format!("{}{rel}", target.trim_end_matches('/'));
    if joined.is_empty() {
        "/".to_owned()
    } else {
        joined
    }
}

impl Inventory {
    /// Compile declarations and observations. Order of any input slice never matters.
    ///
    /// # Errors
    /// The first violated rule; coordinates are never echoed.
    pub fn compile(
        roots: &[Root],
        aliases: &[RootAlias],
        observations: &[IdentityObservation],
    ) -> Result<Self, CoordError> {
        if roots.len() + aliases.len() + observations.len() > MAX_ROUTES {
            return Err(CoordError::TooMany);
        }
        let mut inv = Self {
            roots: BTreeSet::new(),
            routes: BTreeSet::new(),
        };
        for r in roots {
            if !valid_path(&r.path) {
                return Err(CoordError::InvalidPath);
            }
            let k = agent_rank(&r.agent);
            inv.roots.insert((k, r.path.clone()));
            inv.routes.insert((k, r.path.clone(), r.path.clone()));
        }
        for a in aliases {
            if !valid_path(&a.root) || !valid_path(&a.path) {
                return Err(CoordError::InvalidPath);
            }
            let k = agent_rank(&a.agent);
            if !inv.roots.contains(&(k, a.root.clone())) {
                return Err(CoordError::UnknownRoot);
            }
            inv.routes.insert((k, a.path.clone(), a.root.clone()));
        }
        inv.add_proven(observations)?;
        inv.validate_routes()?;
        Ok(inv)
    }

    /// Turn observations that prove identity with a declared root into routes.
    fn add_proven(&mut self, observations: &[IdentityObservation]) -> Result<(), CoordError> {
        let mut seen: BTreeMap<(u8, String), u128> = BTreeMap::new();
        for o in observations {
            if !valid_path(&o.path) {
                return Err(CoordError::InvalidPath);
            }
            let key = (agent_rank(&o.agent), o.path.clone());
            if seen
                .insert(key, o.identity)
                .is_some_and(|p| p != o.identity)
            {
                return Err(CoordError::Ambiguous);
            }
        }
        let mut owners: BTreeMap<(u8, u128), String> = BTreeMap::new();
        for (k, root) in &self.roots {
            let Some(id) = seen.get(&(*k, root.clone())) else {
                continue;
            };
            if owners.insert((*k, *id), root.clone()).is_some() {
                return Err(CoordError::Ambiguous);
            }
        }
        for ((k, path), id) in &seen {
            if let Some(root) = owners.get(&(*k, *id)) {
                self.routes.insert((*k, path.clone(), root.clone()));
            }
        }
        Ok(())
    }

    /// Go `validRoutes`: every route nested under another must project consistently.
    fn validate_routes(&self) -> Result<(), CoordError> {
        for a in &self.routes {
            let mut depth = 0;
            for b in &self.routes {
                if a.0 != b.0 || !beneath(&b.1, &a.1) {
                    continue;
                }
                if b.1 != b.2 {
                    depth += 1;
                }
                if project(&b.1, &b.2, &a.1) != a.2 {
                    return Err(CoordError::Ambiguous);
                }
            }
            if depth > MAX_DEPTH {
                return Err(CoordError::TooDeep);
            }
        }
        Ok(())
    }

    /// Project through the first route, then select the owner of the projection.
    fn locate(&self, rank: u8, path: &str) -> Option<(String, String)> {
        let logical = self
            .routes
            .iter()
            .find(|(k, p, _)| *k == rank && beneath(p, path))
            .map(|(_, p, r)| project(p, r, path))?;
        let root = self
            .roots
            .iter()
            .filter(|(k, r)| *k == rank && beneath(r, &logical))
            .max_by_key(|(_, r)| r.len())
            .map(|(_, r)| r.clone())?;
        Some((logical, root))
    }

    /// Resolve `path` (the shell's final path) with the shell's full link `chain`.
    ///
    /// Chain entries are kept verbatim in `identities`, valid or not, so no intermediate is
    /// ever dropped from deny checks.
    ///
    /// # Errors
    /// `InvalidPath` for a malformed `path`; `Unowned` when no declared root or proven alias
    /// covers it (an unproven physical alias is never authorized).
    pub fn resolve(
        &self,
        agent: &Agent,
        path: &str,
        chain: &[String],
    ) -> Result<Coordinate, CoordError> {
        if !valid_path(path) {
            return Err(CoordError::InvalidPath);
        }
        let rank = agent_rank(agent);
        let (logical_path, root) = self.locate(rank, path).ok_or(CoordError::Unowned)?;
        let mut equivalent_paths = BTreeSet::from([logical_path.clone()]);
        for (k, rpath, rroot) in &self.routes {
            if *k == rank && beneath(rroot, &logical_path) {
                let p = project(rroot, rpath, &logical_path);
                if self
                    .locate(rank, &p)
                    .is_some_and(|(l, o)| l == logical_path && o == root)
                {
                    equivalent_paths.insert(p);
                }
            }
        }
        let mut legacy_roots = BTreeSet::new();
        for p in &equivalent_paths {
            for (k, rpath, rroot) in &self.routes {
                if *k == rank && beneath(rpath, p) {
                    legacy_roots.insert(rpath.clone());
                    if beneath(rroot, &root) {
                        legacy_roots.insert(project(rroot, rpath, &root));
                    }
                }
            }
        }
        let mut identities: BTreeSet<String> = chain.iter().cloned().collect();
        identities.insert(path.to_owned());
        identities.extend(equivalent_paths.iter().cloned());
        let digest = Sha256::digest(root.as_bytes());
        let hex: String = digest.iter().take(16).map(|b| format!("{b:02x}")).collect();
        Ok(Coordinate {
            agent: agent.clone(),
            namespace: format!("{}-{hex}", agent.as_str()),
            root,
            logical_path,
            path: path.to_owned(),
            equivalent_paths,
            legacy_roots,
            identities,
        })
    }

    /// True when `c` is exactly what this inventory resolves for its agent and path; a request
    /// supplied root or logical path is never trusted.
    pub fn verified(&self, c: &Coordinate) -> bool {
        self.resolve(&c.agent, &c.path, &[]).is_ok_and(|g| {
            g.root == c.root && g.logical_path == c.logical_path && g.namespace == c.namespace
        })
    }
}
