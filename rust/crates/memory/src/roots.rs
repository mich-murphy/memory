//! Configured roots become locator [`Roots`] (DESIGN.md 9.1, report 11).
//!
//! The owner namespace of a root is `<agent>-<32 hex>` where the hex is the first 16 bytes of
//! `sha256` over the UTF-8 bytes of the declared root path, as
//! [`memory_core::coordinates::Inventory::resolve`] computes it. No aliases or identity
//! observations are declared yet, and every root uses [`RootLayout::AgentDir`].
//!
//! Overlapping roots (equal, or one beneath another, whatever the agent) are refused: the walker
//! pairs files with roots by path prefix, so a file under two roots would be captured under both.
use crate::config::Config;
use memory_core::coordinates::{Inventory, Root as CoordRoot};
use memory_core::locator::{RootLayout, RootNamespace, Roots};
use std::path::PathBuf;

/// Closed set of root failures (config class). No variant carries a path.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum RootsError {
    /// A root path is not valid UTF-8.
    #[error("config_root_not_utf8")]
    NotUtf8,
    /// A root path is not clean.
    #[error("config_root_invalid")]
    Invalid,
    /// Two roots are equal or one is beneath the other.
    #[error("config_root_overlap")]
    Overlap,
    /// The coordinates inventory refused the declarations (for example too many roots).
    #[error("config_root_inventory")]
    Inventory,
}

impl RootsError {
    /// Fixed machine-readable code.
    #[must_use]
    pub const fn code(self) -> &'static str {
        match self {
            Self::NotUtf8 => "config_root_not_utf8",
            Self::Invalid => "config_root_invalid",
            Self::Overlap => "config_root_overlap",
            Self::Inventory => "config_root_inventory",
        }
    }
}

/// Build the walker's root list from the configuration, in configured order.
///
/// # Errors
/// A [`RootsError`] for a non-UTF-8, unclean or overlapping root, or an inventory refusal.
pub fn locator_roots(config: &Config) -> Result<Vec<(PathBuf, Roots)>, RootsError> {
    let mut declared = Vec::with_capacity(config.roots.len());
    for root in &config.roots {
        let path = root.path.to_str().ok_or(RootsError::NotUtf8)?;
        declared.push(CoordRoot {
            agent: root.agent.clone(),
            path: path.to_owned(),
        });
    }
    let inventory = Inventory::compile(&declared, &[], &[]).map_err(|error| match error {
        memory_core::coordinates::CoordError::InvalidPath => RootsError::Invalid,
        _ => RootsError::Inventory,
    })?;
    for (i, a) in config.roots.iter().enumerate() {
        for b in config.roots.iter().skip(i + 1) {
            if a.path.starts_with(&b.path) || b.path.starts_with(&a.path) {
                return Err(RootsError::Overlap);
            }
        }
    }
    let mut out = Vec::with_capacity(declared.len());
    for (root, decl) in config.roots.iter().zip(&declared) {
        let coordinate = inventory
            .resolve(&decl.agent, &decl.path, &[])
            .map_err(|_| RootsError::Inventory)?;
        let namespace =
            RootNamespace::new(&coordinate.namespace).map_err(|_| RootsError::Inventory)?;
        out.push((
            root.path.clone(),
            Roots::new(
                config.archive_id.clone(),
                config.machine_id.clone(),
                root.agent.clone(),
                namespace,
                RootLayout::AgentDir,
            ),
        ));
    }
    Ok(out)
}
