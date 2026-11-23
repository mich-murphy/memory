//! Brief 48, reduced by brief 54: deletions on the real binary against the loopback fake. One
//! session (transcript plus a tool-results file) and a Pi run record (the kept snapshot kind;
//! Codex stores and file-history are ignored now). After both are removed, two reconciles
//! record both deleted; `check`, `restore` and `export` follow D52/D59; a recreated run record
//! lands as a new epoch.
use super::daemon_restore::{Setup, setup};
use super::{R, e};
use memory::config::Config;
use memory::daemon::Sources;
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory_core::domain::Owner;
use memory_core::limits::owner_prefix;
use std::fs;
use std::process::Command;

const UUID: &str = "0f0e0d0c-0b0a-4908-8706-050403020100";
const TR: &str = "t.txt";
const RECORD: &str = "sessions/--pi-cwd--/subagent-artifacts/r.json";
const RECORD_BODY: &str = "{\"run\":\"record_marker_k54\"}\n";

fn run(s: &Setup, args: &[&str]) -> R<(i32, String, String)> {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear()
        .env("MEMORY_STATE_DIR", &s.rig.state)
        .args(args);
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    let o = cmd.output().map_err(e)?;
    Ok((
        o.status.code().ok_or("signal")?,
        String::from_utf8_lossy(&o.stdout).into_owned(),
        String::from_utf8_lossy(&o.stderr).into_owned(),
    ))
}

fn round(s: &Setup) -> R<String> {
    let (code, out, err) = run(s, &["reconcile"])?;
    assert_eq!(code, 0, "{out}{err}");
    err.lines()
        .find(|l| l.contains(" INFO round "))
        .map(str::to_owned)
        .ok_or_else(|| "no round line".to_owned())
}

fn states(v: &serde_json::Value, out: &mut Vec<String>) {
    match v {
        serde_json::Value::Object(m) => {
            if let Some(serde_json::Value::String(st)) = m.get("state") {
                out.push(st.clone());
            }
            m.values().for_each(|x| states(x, out));
        }
        serde_json::Value::Array(a) => a.iter().for_each(|x| states(x, out)),
        _ => {}
    }
}

#[test]
fn deleted_snapshot_sources_follow_d52_and_a_recreated_store_lands_again() -> R {
    let s = setup("snapdel")?;
    let pi = s.rig.add_root("pi")?;
    fs::copy(&s.rig.cfg, s.rig.state.join("config.toml")).map_err(e)?;
    let tr = s.rig.root.join(format!("projects/-p/{UUID}/tool-results"));
    fs::create_dir_all(&tr).map_err(e)?;
    fs::write(tr.join(TR), "tool line\n").map_err(e)?;
    let rec = pi.join(RECORD);
    fs::create_dir_all(rec.parent().ok_or("parent")?).map_err(e)?;
    fs::write(&rec, RECORD_BODY).map_err(e)?;

    let config = Config::load(&s.rig.cfg).map_err(e)?;
    let shared: Owner = WalkSources::new(
        locator_roots(&config).map_err(e)?,
        WalkLimits::default(),
        SharedFs::new(),
    )
    .enumerate()
    .into_iter()
    .map(|(o, _)| o)
    .find(|o| *o != s.owner)
    .ok_or("no shared owner")?;

    let first = round(&s)?;
    assert!(first.contains(" deleted=0"), "{first}");
    fs::remove_file(tr.join(TR)).map_err(e)?;
    fs::remove_file(&rec).map_err(e)?;
    let one = round(&s)?;
    assert!(
        one.contains(" deleted=0"),
        "one miss deletes nothing: {one}"
    );
    let two = round(&s)?;
    assert!(two.contains(" deleted=2"), "{two}");

    let (code, out, err) = run(&s, &["check"])?;
    assert_eq!(code, 0, "{out}{err}");
    assert!(out.contains(" remote_only=0 "), "{out}");

    let prefix = owner_prefix(&s.owner);
    let dest = s.rig.dir.join("restored");
    let (code, out, err) = run(
        &s,
        &["restore", &prefix, "--out", &dest.display().to_string()],
    )?;
    assert_eq!(code, 0, "{out}{err}");
    assert!(
        err.contains("restored written=1 skipped_deleted=1 "),
        "{err}"
    );
    assert!(
        !dest
            .join(format!("projects/-p/{UUID}/tool-results"))
            .join(TR)
            .exists()
    );

    let exp = s.rig.dir.join("exported");
    let (code, out, err) = run(
        &s,
        &["export", &prefix, "--out", &exp.display().to_string()],
    )?;
    assert_eq!(code, 0, "{out}{err}");
    let meta: serde_json::Value =
        serde_json::from_slice(&fs::read(exp.join("metadata.json")).map_err(e)?).map_err(e)?;
    let mut seen = Vec::new();
    states(&meta, &mut seen);
    assert!(seen.iter().any(|x| x == "sealed"), "{seen:?}");

    // The run record comes back: a new epoch, restorable again.
    fs::write(&rec, RECORD_BODY).map_err(e)?;
    let back = round(&s)?;
    assert!(
        back.contains(" landed=1") && back.contains(" deleted=0"),
        "{back}"
    );
    let dest = s.rig.dir.join("restored-shared");
    let (code, out, err) = run(
        &s,
        &[
            "restore",
            &owner_prefix(&shared),
            "--out",
            &dest.display().to_string(),
        ],
    )?;
    assert_eq!(code, 0, "{out}{err}");
    assert!(
        err.contains("restored written=1 skipped_deleted=0 "),
        "{err}"
    );
    assert_eq!(
        fs::read(dest.join(RECORD)).map_err(e)?,
        RECORD_BODY.as_bytes()
    );
    Ok(())
}
