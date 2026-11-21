//! Brief 49: the remaining snapshot kinds on the real binary against the loopback fake. One
//! Claude session (transcript, a session aux `json`) with a project memory note, plus
//! `settings.json` in the root. One reconcile lands all of them; `check` is clean; `restore` of the
//! session and of the shared owner writes equal bytes; `export` succeeds.
use super::assemble::SESSION;
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
const AUX: &str = "aux.json";
const NOTE: &str = "n.md";
const AUX_BODY: &str = "{\"aux\":\"aux_marker_k49\"}\n";
const NOTE_BODY: &str = "# note\nnote_marker_k49\n";
const SETTINGS_BODY: &str = "{\n  \"theme\": \"dark\"\n}\n";

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

#[test]
fn session_aux_memory_note_and_settings_land_check_clean_and_restore_equal() -> R {
    let s = setup("snapkinds")?;
    let root = &s.rig.root;
    let aux = root.join(format!("projects/-p/{UUID}/{AUX}"));
    let note = root.join(format!("projects/-p/memory/{NOTE}"));
    for (p, body) in [
        (&aux, AUX_BODY),
        (&note, NOTE_BODY),
        (&root.join("settings.json"), SETTINGS_BODY),
    ] {
        fs::create_dir_all(p.parent().ok_or("parent")?).map_err(e)?;
        fs::write(p, body).map_err(e)?;
    }
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

    let (code, out, err) = run(&s, &["reconcile"])?;
    assert_eq!(code, 0, "{out}{err}");
    let round = err
        .lines()
        .find(|l| l.contains(" INFO round "))
        .ok_or("no round line")?;
    assert!(round.contains(" landed=2"), "{round}");
    assert!(round.contains(" withheld=0"), "{round}");

    let (code, out, err) = run(&s, &["check"])?;
    assert_eq!(code, 0, "{out}{err}");
    for k in ["uncaptured=0", "mismatch=0", "deferred_snapshot=0"] {
        assert!(out.lines().next().unwrap_or("").contains(k), "{k}: {out}");
    }

    let dest = s.rig.dir.join("restored");
    let (code, out, err) = run(
        &s,
        &[
            "restore",
            &owner_prefix(&s.owner),
            "--out",
            &dest.display().to_string(),
        ],
    )?;
    assert_eq!(code, 0, "{out}{err}");
    assert_eq!(
        fs::read(dest.join(SESSION)).map_err(e)?,
        fs::read(&s.file).map_err(e)?
    );
    assert_eq!(
        fs::read(dest.join(format!("projects/-p/{UUID}/{AUX}"))).map_err(e)?,
        AUX_BODY.as_bytes()
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
    assert_eq!(
        fs::read(dest.join(format!("projects/-p/memory/{NOTE}"))).map_err(e)?,
        NOTE_BODY.as_bytes()
    );
    assert_eq!(
        fs::read(dest.join("settings.json")).map_err(e)?,
        SETTINGS_BODY.as_bytes()
    );

    let exp = s.rig.dir.join("exported");
    let (code, out, err) = run(
        &s,
        &[
            "export",
            &owner_prefix(&s.owner),
            "--out",
            &exp.display().to_string(),
        ],
    )?;
    assert_eq!(code, 0, "{out}{err}");
    Ok(())
}
