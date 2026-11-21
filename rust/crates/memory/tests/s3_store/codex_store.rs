//! A Codex SQLite store on the real binary against the local server (brief 45, step 4):
//! reconcile, `check` (match), a change, `check` (behind), `restore` of the shared owner, then
//! `integrity_check` and row counts of the restored database.
use super::assemble::Rig;
use super::fake::Fake;
use super::{R, e};
use memory::assemble::start_from_config;
use memory::config::Config;
use memory::daemon::Sources;
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory_core::limits::owner_prefix;
use rusqlite::Connection;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::process::Command;

fn run(rig: &Rig, args: &[&str]) -> R<(i32, String, String)> {
    run_env(rig, args, &[])
}

fn run_env(rig: &Rig, args: &[&str], envs: &[(&str, &str)]) -> R<(i32, String, String)> {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear()
        .env("MEMORY_STATE_DIR", &rig.state)
        .args(args);
    cmd.envs(envs.iter().copied());
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

fn rows(path: &Path) -> R<(String, i64)> {
    let conn = Connection::open(path).map_err(e)?;
    let ok: String = conn
        .query_row("pragma integrity_check", [], |r| r.get(0))
        .map_err(e)?;
    let n: i64 = conn
        .query_row("select count(*) from t", [], |r| r.get(0))
        .map_err(e)?;
    Ok((ok, n))
}

#[test]
fn a_codex_store_is_captured_checked_and_restored() -> R {
    let server = Fake::start()?;
    let rig = Rig::new(
        "codex-store",
        &server.url(),
        "rust-acceptance/b45-test/",
        false,
    )?;
    let codex = rig.add_root("codex")?;
    fs::create_dir_all(&rig.state).map_err(e)?;
    fs::set_permissions(&rig.state, fs::Permissions::from_mode(0o700)).map_err(e)?;
    fs::copy(&rig.cfg, rig.state.join("config.toml")).map_err(e)?;
    let db = codex.join("thread_history_1.sqlite");
    let writer = Connection::open(&db).map_err(e)?;
    writer
        .pragma_update(None, "journal_mode", "wal")
        .map_err(e)?;
    writer
        .pragma_update(None, "wal_autocheckpoint", 0)
        .map_err(e)?;
    writer
        .execute("create table t (n integer, s text)", [])
        .map_err(e)?;
    for n in 0..40 {
        writer
            .execute("insert into t values (?1, 'row')", [n])
            .map_err(e)?;
    }
    // A log database beside it is never captured.
    fs::write(codex.join("logs_2.sqlite"), b"not captured").map_err(e)?;

    let mut daemon = start_from_config(&rig.state, &rig.cfg).map_err(e)?;
    let report = daemon.reconcile_once().map_err(e)?;
    assert_eq!((report.landed, report.failed), (1, 0));
    daemon.shutdown().map_err(e)?;
    assert_eq!(
        fs::read_dir(rig.state.join("tmp")).map_err(e)?.count(),
        0,
        "temp copy left behind"
    );

    let (code, out, err) = run(&rig, &["check"])?;
    assert_eq!(code, 0, "{out}{err}");
    assert!(
        out.contains(" match=1 ") && out.contains(" behind=0 "),
        "{out}"
    );

    writer
        .execute("insert into t values (99, 'new')", [])
        .map_err(e)?;
    let (code, out, err) = run(&rig, &["check"])?;
    assert_eq!(code, 0, "behind keeps its exit code: {out}{err}");
    assert!(
        out.contains(" behind=1 ") && out.contains(" match=0 "),
        "{out}{err}"
    );
    assert!(out.contains(" mismatch=0 "), "{out}");

    let config = Config::load(&rig.cfg).map_err(e)?;
    let owner = WalkSources::new(
        locator_roots(&config).map_err(e)?,
        WalkLimits::default(),
        SharedFs::new(),
    )
    .enumerate()
    .first()
    .ok_or("no source")?
    .0
    .clone();
    let dest = rig.dir.join("restored");
    let prefix = owner_prefix(&owner);
    let (code, out, err) = run(
        &rig,
        &["restore", &prefix, "--out", &dest.display().to_string()],
    )?;
    assert_eq!(code, 0, "{out}{err}");
    // The restored database is the state at the capture: 40 rows, not the later 41.
    assert_eq!(
        rows(&dest.join("thread_history_1.sqlite"))?,
        ("ok".to_owned(), 40)
    );
    assert!(!dest.join("logs_2.sqlite").exists());
    Ok(())
}

/// A Codex root with a file named like a store that is not a database.
fn broken_store(label: &str) -> R<(Fake, Rig)> {
    let server = Fake::start()?;
    let rig = Rig::new(label, &server.url(), "rust-acceptance/b45-test/", false)?;
    let codex = rig.add_root("codex")?;
    fs::create_dir_all(&rig.state).map_err(e)?;
    fs::set_permissions(&rig.state, fs::Permissions::from_mode(0o700)).map_err(e)?;
    fs::copy(&rig.cfg, rig.state.join("config.toml")).map_err(e)?;
    fs::write(codex.join("state_5.sqlite"), vec![b'x'; 4096]).map_err(e)?;
    Ok((server, rig))
}

#[test]
fn a_failed_backup_is_counted_as_local_and_nothing_lands() -> R {
    let (server, rig) = broken_store("codex-backup-fail")?;
    let mut daemon = start_from_config(&rig.state, &rig.cfg).map_err(e)?;
    let report = daemon.reconcile_once().map_err(e)?;
    assert_eq!((report.landed, report.parked), (0, 0));
    assert_eq!(report.failures.local, 1);
    // The next round tries again and counts again.
    let again = daemon.reconcile_once().map_err(e)?;
    assert_eq!(again.failures.local, 1);
    daemon.shutdown().map_err(e)?;
    assert_eq!(server.count("PUT"), 0);
    assert_eq!(fs::read_dir(rig.state.join("tmp")).map_err(e)?.count(), 0);
    Ok(())
}

#[test]
fn reconcile_reports_local_one_for_a_failed_backup() -> R {
    let (server, rig) = broken_store("codex-backup-reconcile")?;
    let (code, out, err) = run(&rig, &["reconcile"])?;
    assert!(
        err.contains(" round cause=manual ") && err.contains(" local=1 "),
        "{err}"
    );
    assert!(out.contains("landed=0"), "{out}");
    // Contract: exit 1 only when an owner failed or parked; a local backup failure is counted
    // (`local=1`) without an owner retry, so the exit is 0 and `failed=0`.
    assert!(out.contains("failed=0"), "{out}");
    assert_eq!(code, 0, "{out}{err}");
    assert_eq!(server.count("PUT"), 0);
    assert_eq!(fs::read_dir(rig.state.join("tmp")).map_err(e)?.count(), 0);
    Ok(())
}

/// A store of about 20 MiB (three 8 MiB pieces) of incompressible rows, in WAL mode.
fn big_store(label: &str) -> R<(Fake, Rig, Connection)> {
    let server = Fake::start()?;
    let rig = Rig::new(label, &server.url(), "rust-acceptance/b45-test/", false)?;
    let codex = rig.add_root("codex")?;
    fs::create_dir_all(&rig.state).map_err(e)?;
    fs::set_permissions(&rig.state, fs::Permissions::from_mode(0o700)).map_err(e)?;
    fs::copy(&rig.cfg, rig.state.join("config.toml")).map_err(e)?;
    let writer = Connection::open(codex.join("thread_history_1.sqlite")).map_err(e)?;
    writer
        .pragma_update(None, "journal_mode", "wal")
        .map_err(e)?;
    writer
        .pragma_update(None, "wal_autocheckpoint", 0)
        .map_err(e)?;
    writer
        .execute_batch(
            "create table t (n integer primary key, s blob);
             with recursive c(n) as (select 1 union all select n + 1 from c where n < 20000)
             insert into t select n, randomblob(1000) from c;",
        )
        .map_err(e)?;
    Ok((server, rig, writer))
}

fn puts_and_gets(server: &Fake) -> (Vec<String>, Vec<String>) {
    let seen = server.requests();
    let by = |m: &str| {
        seen.iter()
            .filter(|r| r.method == m)
            .map(|r| r.path.clone())
            .collect::<Vec<_>>()
    };
    (by("PUT"), by("GET"))
}

#[test]
fn a_resnapshot_puts_only_the_changed_pieces_and_gets_no_reused_one() -> R {
    let started = std::time::Instant::now();
    let (server, rig, writer) = big_store("codex-put-count")?;
    let sim = [("MEMORY_SIM_SNAPSHOT_INTERVAL", "0")];
    let (code, out, err) = run_env(&rig, &["reconcile"], &sim)?;
    assert_eq!(code, 0, "{out}{err}");
    let (first_puts, first_gets) = puts_and_gets(&server);
    let first_objects = first_puts
        .iter()
        .filter(|p| p.contains("/objects/"))
        .count();
    assert!(
        first_objects >= 3,
        "need several pieces, got {first_objects}"
    );
    writer
        .execute("update t set s = randomblob(1000) where n = 15000", [])
        .map_err(e)?;
    let (code, out, err) = run_env(&rig, &["reconcile"], &sim)?;
    assert_eq!(code, 0, "{out}{err}");
    let (all_puts, all_gets) = puts_and_gets(&server);
    let second: Vec<_> = all_puts.iter().skip(first_puts.len()).cloned().collect();
    let objects = second.iter().filter(|p| p.contains("/objects/")).count();
    let others = second.len() - objects;
    assert!(
        (1..first_objects).contains(&objects),
        "changed pieces only: {objects} of {first_objects}"
    );
    assert_eq!(others, 2, "manifest and current.json only: {second:?}");
    let new_object_gets = all_gets
        .iter()
        .skip(first_gets.len())
        .filter(|p| p.contains("/objects/"))
        .count();
    assert_eq!(new_object_gets, 0, "reused or new pieces were read back");
    eprintln!("put-count test took {:?}", started.elapsed());
    Ok(())
}

#[test]
fn the_sim_interval_override_lands_the_second_snapshot_and_its_absence_defers_it() -> R {
    let (server, rig, writer) = big_store("codex-interval")?;
    let (code, out, err) = run(&rig, &["reconcile"])?;
    assert_eq!(code, 0, "{out}{err}");
    assert!(out.contains("landed=1"), "{out}");
    let puts = server.count("PUT");
    writer
        .execute("update t set s = randomblob(1000) where n = 5", [])
        .map_err(e)?;
    // The test binary is built with `sim` (self dev-dependency), so the variable is read;
    // without it the 6 h default defers the changed store and nothing is put.
    let (code, out, err) = run(&rig, &["reconcile"])?;
    assert_eq!(code, 0, "{out}{err}");
    assert!(out.contains("landed=0"), "deferred: {out}");
    assert_eq!(server.count("PUT"), puts);
    let (code, out, err) = run_env(
        &rig,
        &["reconcile"],
        &[("MEMORY_SIM_SNAPSHOT_INTERVAL", "0")],
    )?;
    assert_eq!(code, 0, "{out}{err}");
    assert!(out.contains("landed=1"), "{out}");
    assert!(server.count("PUT") > puts);
    Ok(())
}
