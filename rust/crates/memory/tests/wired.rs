//! The two adapters of the event loop: routing of changed paths and the watcher as `Inputs`.
use memory::driver::{Inputs, Next, Route};
use memory::watch::FsWatcher;
use memory::wired::{WatchInputs, hint_dirs, route};
use memory_core::domain::{Agent, ArchiveId, MachineId, Owner, SessionId, SessionKey};
use std::cell::Cell;
use std::path::PathBuf;
use std::time::{Duration, Instant};

type R<T = ()> = Result<T, String>;

fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}

fn owner() -> R<Owner> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
        machine: MachineId::try_from("m".to_owned()).map_err(e)?,
        agent: Agent::Claude,
        session: SessionId::try_from("s1".to_owned()).map_err(e)?,
    }))
}

struct Tmp(PathBuf);
impl Drop for Tmp {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

#[test]
fn classify_routes_hint_paths_in_both_spellings_sources_and_the_rest() -> R {
    let base = std::env::temp_dir().join(format!("memory-wired-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&base);
    let tmp = Tmp(base);
    std::fs::create_dir_all(tmp.0.join("real/hints")).map_err(e)?;
    std::os::unix::fs::symlink(tmp.0.join("real"), tmp.0.join("link")).map_err(e)?;
    let configured = tmp.0.join("link/hints");
    let canonical = std::fs::canonicalize(&configured).map_err(e)?;
    assert_ne!(configured, canonical);
    let dirs = hint_dirs(&configured);
    assert_eq!(dirs, [configured.clone(), canonical.clone()]);
    let owner = owner()?;
    let called = Cell::new(false);
    for dir in [&configured, &canonical] {
        let got = route(&dirs, &dir.join("2026-11-17.jsonl"), |_| {
            called.set(true);
            Some(owner.clone())
        });
        assert_eq!(got, Route::Hints, "{dir:?}");
    }
    assert!(!called.get(), "a hint path must not be offered as a source");
    let source = tmp.0.join("claude/projects/x.jsonl");
    assert_eq!(
        route(&dirs, &source, |_| Some(owner.clone())),
        Route::Owner(owner.clone())
    );
    assert_eq!(route(&dirs, &source, |_| None), Route::Nothing);
    let sibling = tmp.0.join("real/hints-extra/x");
    assert_eq!(route(&dirs, &sibling, |_| None), Route::Nothing);
    Ok(())
}

#[test]
fn inputs_report_timeout_then_the_stop_flag_and_stop() -> R {
    let watcher = FsWatcher::new(&[]).map_err(e)?;
    let handle = watcher.stop_handle();
    let flag = watcher.stop_flag();
    let mut inputs = WatchInputs::new(watcher, flag);
    assert!(!inputs.stop_requested());
    assert_eq!(inputs.next(Duration::from_millis(10)), Next::Timeout);
    handle.stop();
    assert!(inputs.stop_requested());
    assert_eq!(inputs.next(Duration::from_millis(10)), Next::Stop);
    Ok(())
}

#[test]
fn inputs_report_a_dead_stop_source_as_failed() -> R {
    let watcher = FsWatcher::new(&[]).map_err(e)?;
    drop(watcher.stop_handle());
    let flag = watcher.stop_flag();
    let mut inputs = WatchInputs::new(watcher, flag);
    assert!(!inputs.stop_requested());
    assert_eq!(inputs.next(Duration::from_millis(10)), Next::Failed);
    Ok(())
}

#[test]
fn inputs_deliver_a_watched_change() -> R {
    let base = std::env::temp_dir().join(format!("memory-wired-in-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&base);
    let tmp = Tmp(base);
    std::fs::create_dir_all(&tmp.0).map_err(e)?;
    let dir = std::fs::canonicalize(&tmp.0).map_err(e)?;
    let watcher = FsWatcher::new(std::slice::from_ref(&dir)).map_err(e)?;
    let _keep = watcher.stop_handle();
    let flag = watcher.stop_flag();
    let mut inputs = WatchInputs::new(watcher, flag);
    std::fs::write(dir.join("f"), b"x").map_err(e)?;
    let start = Instant::now();
    loop {
        match inputs.next(Duration::from_millis(100)) {
            Next::Input(_) => return Ok(()),
            Next::Timeout => {}
            other => return Err(format!("{other:?}")),
        }
        if start.elapsed() > Duration::from_secs(20) {
            return Err("no input".to_owned());
        }
    }
}
