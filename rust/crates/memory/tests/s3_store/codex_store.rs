//! Brief 54: Codex SQLite stores are `Ignored` by the locator, so they have no production
//! capture path. This replaces the five brief 45 rig tests (captured_checked_and_restored,
//! failed_backup_counted_as_local, reconcile_reports_local_one, resnapshot_puts_only_changed
//! _pieces, sim_interval_override). Capture-level coverage stays in `tests/sqlite_store.rs`.
use super::assemble::Rig;
use super::fake::Fake;
use super::{R, e};
use rusqlite::Connection;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::process::Command;

fn run(rig: &Rig, args: &[&str]) -> R<(i32, String, String)> {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear()
        .env("MEMORY_STATE_DIR", &rig.state)
        .args(args);
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    let out = cmd.output().map_err(e)?;
    Ok((
        out.status.code().ok_or("signal")?,
        String::from_utf8_lossy(&out.stdout).into_owned(),
        String::from_utf8_lossy(&out.stderr).into_owned(),
    ))
}

fn wal_store(path: &Path) -> R<Connection> {
    let conn = Connection::open(path).map_err(e)?;
    conn.pragma_update(None, "journal_mode", "wal").map_err(e)?;
    conn.pragma_update(None, "wal_autocheckpoint", 0)
        .map_err(e)?;
    conn.execute("create table t (n integer, s text)", [])
        .map_err(e)?;
    for n in 0..40 {
        conn.execute("insert into t values (?1, 'row')", [n])
            .map_err(e)?;
    }
    Ok(conn)
}

#[test]
fn codex_sqlite_stores_are_ignored_and_nothing_lands() -> R {
    let server = Fake::start()?;
    let rig = Rig::new(
        "codex-store",
        &server.url(),
        "rust-acceptance/b45-test/",
        false,
    )?;
    let codex = rig.add_root("codex")?;
    fs::create_dir_all(&rig.root).map_err(e)?; // the walk reads it; no Claude file is needed
    fs::create_dir_all(&rig.state).map_err(e)?;
    fs::set_permissions(&rig.state, fs::Permissions::from_mode(0o700)).map_err(e)?;
    fs::copy(&rig.cfg, rig.state.join("config.toml")).map_err(e)?;
    let _history = wal_store(&codex.join("thread_history_1.sqlite"))?;
    let _logs = wal_store(&codex.join("logs_2.sqlite"))?;

    let (code, out, err) = run(&rig, &["reconcile"])?;
    assert_eq!(code, 0, "{out}{err}");
    let round = err
        .lines()
        .find(|l| l.contains(" INFO round "))
        .ok_or("no round line")?;
    assert!(round.contains(" landed=0"), "{round}");
    assert!(round.contains(" failed=0"), "{round}");
    assert_eq!(server.count("PUT"), 0, "{round}");

    let tmp = rig.state.join("tmp");
    let left = match fs::read_dir(&tmp) {
        Ok(d) => d.count(),
        Err(_) => 0,
    };
    assert_eq!(left, 0, "no file left in state/tmp");

    let (code, out, err) = run(&rig, &["check"])?;
    assert_eq!(code, 0, "{out}{err}");
    let first = out.lines().next().unwrap_or_default();
    // Two databases, each with its -wal and -shm file while the connections are open.
    assert!(first.contains(" ignored=6 "), "{first}");
    assert!(first.contains(" sources=0 ") && first.contains(" unknown_kind=0 "));
    assert!(first.contains(" uncaptured=0 ") && first.contains(" unreadable=0 "));
    Ok(())
}
