//! Configured roots become locator roots with the coordinates namespace.
use memory::config::Config;
use memory::roots::{RootsError, locator_roots};
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

#[test]
fn namespace_is_agent_and_sha256_of_the_root_path() -> R {
    let cfg = config(&[("claude", "/h/.claude"), ("pi", "/h/.pi/agent")])?;
    let roots = locator_roots(&cfg).map_err(e)?;
    assert_eq!(roots.len(), 2);
    for ((path, roots), (agent, want)) in roots
        .iter()
        .zip([("claude", "/h/.claude"), ("pi", "/h/.pi/agent")])
    {
        assert_eq!(path.to_str(), Some(want));
        let namespace = expected(agent, want);
        assert_eq!(namespace.len(), agent.len() + 1 + 32);
        assert!(
            format!("{roots:?}").contains(&format!("\"{namespace}\"")),
            "{roots:?}"
        );
    }
    Ok(())
}

#[test]
fn same_path_under_two_agents_differs() -> R {
    let cfg = config(&[("claude", "/h/x"), ("codex", "/h/x")])?;
    let roots = locator_roots(&cfg).map_err(e)?;
    let texts: Vec<String> = roots.iter().map(|(_, r)| format!("{r:?}")).collect();
    assert!(
        texts
            .first()
            .is_some_and(|t| t.contains(&expected("claude", "/h/x")))
    );
    assert!(
        texts
            .get(1)
            .is_some_and(|t| t.contains(&expected("codex", "/h/x")))
    );
    assert_ne!(expected("claude", "/h/x"), expected("codex", "/h/x"));
    Ok(())
}

#[test]
fn refusals_are_closed() -> R {
    let dup = config(&[("claude", "/h/a"), ("claude", "/h/a")])?;
    assert_eq!(locator_roots(&dup).map(|_| ()), Err(RootsError::Invalid));
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
