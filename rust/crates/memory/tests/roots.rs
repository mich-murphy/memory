//! Configured roots become locator roots with the coordinates namespace.
use memory::config::Config;
use memory::roots::{RootsError, locator_roots};
use memory_core::locator::{Located, RootRelativePath, locate};
use sha2::{Digest, Sha256};
use std::fmt::Debug;

type R = Result<(), String>;

fn e<E: Debug>(error: E) -> String {
    format!("{error:?}")
}

fn config(roots: &[(&str, &str)]) -> Result<Config, String> {
    let mut text = String::from(
        "archive_id = \"a\"\nmachine_id = \"m\"\nendpoint = \"https://s3.example.com\"\nbucket = \"bucket-1\"\ntoken_path = \"/t\"\n",
    );
    for (agent, path) in roots {
        text.push_str(&format!(
            "[[roots]]\nagent = \"{agent}\"\npath = \"{path}\"\n"
        ));
    }
    Config::parse(&text).map_err(e)
}

fn expected(agent: &str, path: &str) -> String {
    let digest = Sha256::digest(path.as_bytes());
    let hex: String = digest.iter().take(16).map(|b| format!("{b:02x}")).collect();
    format!("{agent}-{hex}")
}

const CLAUDE_FILE: &str = "projects/-p/0f0e0d0c-0b0a-4908-8706-050403020100.jsonl";
const CODEX_FILE: &str =
    "sessions/2026/01/01/rollout-2026-01-01T00-00-00-0f0e0d0c-0b0a-4908-8706-050403020100.jsonl";

fn source_id(roots: &memory_core::locator::Roots, rel: &str) -> Result<String, String> {
    let path = RootRelativePath::parse(rel).map_err(e)?;
    match locate(&path, roots) {
        Located::Source { id, .. } => Ok(id.as_str().to_owned()),
        _ => Err("not a source".to_owned()),
    }
}

#[test]
fn namespace_is_agent_and_sha256_of_the_root_path() -> R {
    let cfg = config(&[("claude", "/h/.claude"), ("codex", "/h/.codex")])?;
    let roots = locator_roots(&cfg).map_err(e)?;
    assert_eq!(roots.len(), 2);
    for ((path, roots), (agent, want, file)) in roots.iter().zip([
        ("claude", "/h/.claude", CLAUDE_FILE),
        ("codex", "/h/.codex", CODEX_FILE),
    ]) {
        assert_eq!(path.to_str(), Some(want));
        let namespace = expected(agent, want);
        assert_eq!(namespace.len(), agent.len() + 1 + 32);
        let id = source_id(roots, file)?;
        assert!(id.starts_with(&format!("{namespace}:")), "{id}");
    }
    Ok(())
}

#[test]
fn the_agent_is_part_of_the_namespace() -> R {
    // Separate configurations: overlapping roots in one are refused.
    let a = locator_roots(&config(&[("claude", "/h/x")])?).map_err(e)?;
    let b = locator_roots(&config(&[("codex", "/h/x")])?).map_err(e)?;
    let id_a = source_id(&a.first().ok_or("root")?.1, CLAUDE_FILE)?;
    let id_b = source_id(&b.first().ok_or("root")?.1, CODEX_FILE)?;
    assert!(id_a.starts_with(&expected("claude", "/h/x")));
    assert!(id_b.starts_with(&expected("codex", "/h/x")));
    assert_ne!(expected("claude", "/h/x"), expected("codex", "/h/x"));
    Ok(())
}

#[test]
fn overlapping_roots_are_refused_and_siblings_accepted() -> R {
    for (label, roots) in [
        ("equal", vec![("claude", "/h/x"), ("claude", "/h/x")]),
        (
            "equal two agents",
            vec![("claude", "/h/x"), ("codex", "/h/x")],
        ),
        (
            "parent then child",
            vec![("claude", "/h/x"), ("codex", "/h/x/y")],
        ),
        (
            "child then parent",
            vec![("claude", "/h/x/y"), ("claude", "/h/x")],
        ),
    ] {
        let cfg = config(&roots)?;
        assert_eq!(
            locator_roots(&cfg).map(|_| ()),
            Err(RootsError::Overlap),
            "{label}"
        );
    }
    let cfg = config(&[("claude", "/h/x"), ("claude", "/h/xy")])?;
    assert_eq!(locator_roots(&cfg).map_err(e)?.len(), 2);
    Ok(())
}

#[test]
fn refusals_are_closed() -> R {
    for bad in ["/h/a/", "/h//a", "/h/./a", "/h/../a"] {
        let cfg = config(&[("claude", bad)])?;
        assert_eq!(
            locator_roots(&cfg).map(|_| ()),
            Err(RootsError::Invalid),
            "{bad}"
        );
    }
    let many: Vec<(String, String)> = (0..129)
        .map(|i| ("claude".to_owned(), format!("/h/{i}")))
        .collect();
    let refs: Vec<(&str, &str)> = many.iter().map(|(a, p)| (a.as_str(), p.as_str())).collect();
    let cfg = config(&refs)?;
    assert_eq!(locator_roots(&cfg).map(|_| ()), Err(RootsError::Inventory));
    Ok(())
}

#[test]
fn non_utf8_root_is_refused() -> R {
    use std::os::unix::ffi::OsStrExt;
    let mut cfg = config(&[("claude", "/h/a")])?;
    if let Some(root) = cfg.roots.first_mut() {
        root.path = std::path::PathBuf::from(std::ffi::OsStr::from_bytes(b"/h/\xff"));
    }
    assert_eq!(locator_roots(&cfg).map(|_| ()), Err(RootsError::NotUtf8));
    Ok(())
}
