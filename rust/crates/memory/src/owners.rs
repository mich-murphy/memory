//! `memory owners`: list every session owner that has a `current.json` under the configured
//! prefix, by ListObjectsV2 over `<prefix>sessions/`.
//!
//! stdout: owner texts, sorted and deduplicated, one per line, in the form `memory export` and
//! `memory restore` take. stderr: `memory: owners=N` or one closed `memory: <code>` line; no
//! endpoint, key, token or prefix. A store failure is `memory: store cause=<code>` (the closed
//! `StoreError` codes, brief 52) and exits 75 when a retry may help, 77 on auth. The paging loop
//! lives here, so `s3.rs` keeps one exchange per call.
use crate::assemble::build_store;
use crate::config::Config;
use crate::daemon::EXIT_CONFIG;
use crate::environment::StoreError;
use crate::restore::{EXIT_FAILED, EXIT_USAGE, store_exit, store_line};
use crate::s3::Page;
use memory_core::domain::Owner;
use memory_core::limits::parse_owner_prefix;
use std::collections::BTreeSet;
use std::ffi::OsString;
use std::io::Write;
use std::process::ExitCode;

/// The key family listed, below the configured prefix.
const FAMILY: &str = "sessions/";
/// The shared-owner key family (`memory owners --shared`, brief 56).
const SHARED_FAMILY: &str = "shared/";
/// The suffix every listed key must end with.
const CURRENT: &str = "/current.json";
/// Most pages followed before the listing is declared a loop.
pub const MAX_PAGES: usize = 10_000;

/// Why a listing failed. Closed; carries no input text.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum Failure {
    /// The store failed.
    #[error("{}", store_line(*.0))]
    Store(Option<StoreError>),
    /// A listed key was not under the requested prefix.
    #[error("listing_outside_prefix")]
    OutsidePrefix,
    /// A key under `sessions/` is not `<owner text>/current.json`.
    #[error("listing_unexpected_key")]
    UnexpectedKey,
    /// A continuation token repeated, or too many pages.
    #[error("listing_loop")]
    Loop,
}

/// Follow the pages `list` yields and return the sorted, deduplicated owner texts.
///
/// # Errors
/// A [`Failure`]; no partial result is returned.
pub fn collect_owners(
    list: impl FnMut(Option<&str>) -> Result<Page, StoreError>,
) -> Result<Vec<String>, Failure> {
    collect_family(false, list)
}

/// As [`collect_owners`] for the `shared/` family: every key must be
/// `shared/<archive>/<machine>/<agent>/current.json`; a session-shaped or any other key is
/// [`Failure::UnexpectedKey`].
///
/// # Errors
/// A [`Failure`]; no partial result is returned.
pub fn collect_shared_owners(
    list: impl FnMut(Option<&str>) -> Result<Page, StoreError>,
) -> Result<Vec<String>, Failure> {
    collect_family(true, list)
}

fn collect_family(
    shared: bool,
    mut list: impl FnMut(Option<&str>) -> Result<Page, StoreError>,
) -> Result<Vec<String>, Failure> {
    let family = if shared { SHARED_FAMILY } else { FAMILY };
    let mut owners = BTreeSet::new();
    let mut seen: BTreeSet<String> = BTreeSet::new();
    let mut token: Option<String> = None;
    let mut pages = 0_usize;
    loop {
        pages += 1;
        if pages > MAX_PAGES {
            return Err(Failure::Loop);
        }
        let page = list(token.as_deref()).map_err(|e| Failure::Store(Some(e)))?;
        if page.outside > 0 {
            return Err(Failure::OutsidePrefix);
        }
        for key in &page.keys {
            let text = key
                .strip_suffix(CURRENT)
                .filter(|_| key.starts_with(family))
                .ok_or(Failure::UnexpectedKey)?;
            match (parse_owner_prefix(text), shared) {
                (Ok(Owner::Session(_)), false) | (Ok(Owner::Shared { .. }), true) => {
                    owners.insert(text.to_owned());
                }
                _ => return Err(Failure::UnexpectedKey),
            }
        }
        match page.next {
            None => return Ok(owners.into_iter().collect()),
            Some(next) => {
                if !seen.insert(next.clone()) {
                    return Err(Failure::Loop);
                }
                token = Some(next);
            }
        }
    }
}

fn fail(code: &str, exit: u8) -> ExitCode {
    let _ = writeln!(std::io::stderr(), "memory: {code}");
    ExitCode::from(exit)
}

/// Entry of `memory owners [--shared]`; `args` follow the word `owners` and are empty or the one
/// word `--shared` (list `<prefix>shared/` instead, brief 56); anything else is `usage`.
///
/// Exit codes: 0 listed (also none), 1 failed, 2 config or token, 64 usage, 75 a transient store
/// failure (retry), 77 the store refused the credential. A store failure prints
/// `memory: store cause=<code>`. Nothing is printed on stdout unless the whole listing succeeded.
#[must_use]
pub fn main_entry(mut args: impl Iterator<Item = OsString>) -> ExitCode {
    let shared = match args.next() {
        None => false,
        Some(a) if a == "--shared" => true,
        Some(_) => return fail("usage", EXIT_USAGE),
    };
    if args.next().is_some() {
        return fail("usage", EXIT_USAGE);
    }
    let Some(root) = crate::state::default_root() else {
        return fail("state_root", EXIT_CONFIG);
    };
    let config = match Config::load(&root.join("config.toml")) {
        Ok(config) => config,
        Err(error) => return fail(error.code(), EXIT_CONFIG),
    };
    let mut store = match build_store(&config) {
        Ok(store) => store,
        Err(error) => return fail(error.code(), error.exit_code()),
    };
    let listed = if shared {
        collect_shared_owners(|token| store.list_page(SHARED_FAMILY, token))
    } else {
        collect_owners(|token| store.list_page(FAMILY, token))
    };
    match listed {
        Ok(owners) => {
            let mut out = std::io::stdout().lock();
            for owner in &owners {
                if writeln!(out, "{owner}").is_err() {
                    return fail("stdout", EXIT_FAILED);
                }
            }
            if out.flush().is_err() {
                return fail("stdout", EXIT_FAILED);
            }
            let _ = writeln!(std::io::stderr(), "memory: owners={}", owners.len());
            ExitCode::SUCCESS
        }
        Err(failure) => {
            let exit = match failure {
                Failure::Store(e) => store_exit(e),
                _ => EXIT_FAILED,
            };
            fail(&failure.to_string(), exit)
        }
    }
}
