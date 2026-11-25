//! Brief 49, reduced by brief 54: a snapshot kind on the real binary against the loopback fake.
//! One Claude session plus a Pi run record `sessions/<cwd>/subagent-artifacts/<name>.json`
//! (the kept snapshot kind); a project memory note, `settings.json` and a session aux `json`
//! are ignored. One reconcile lands the session and the record; `status` counts the ignored
//! files; `export` of the session and of the shared owner writes bodies equal to the disk.
use super::daemon_setup::{Setup, setup};
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
const RECORD: &str = "sessions/--pi-cwd--/subagent-artifacts/r.json";
const RECORD_BODY: &str = "{\"run\":\"record_marker_k54\"}\n";
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

// Brief 54: the snapshot is a Pi run record; the note, settings and aux file are ignored.
#[test]
fn session_aux_memory_note_and_settings_land_and_export_equal() -> R {
    let s = setup("snapkinds")?;
    let pi = s.rig.add_root("pi")?;
    fs::copy(&s.rig.cfg, s.rig.state.join("config.toml")).map_err(e)?;
    let root = &s.rig.root;
    let aux = root.join(format!("projects/-p/{UUID}/{AUX}"));
    let note = root.join(format!("projects/-p/memory/{NOTE}"));
    for (p, body) in [
        (&aux, AUX_BODY),
        (&note, NOTE_BODY),
        (&root.join("settings.json"), SETTINGS_BODY),
        (&pi.join(RECORD), RECORD_BODY),
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

    let (code, out, err) = run(&s, &["status"])?;
    assert_eq!(code, 0, "{out}{err}");
    let line = out
        .lines()
        .find(|l| l.starts_with("sources "))
        .unwrap_or("");
    assert!(line.split(' ').any(|w| w == "ignored=3"), "{out}");

    let dest = s.rig.dir.join("exported-session");
    let (code, out, err) = run(
        &s,
        &[
            "export",
            &owner_prefix(&s.owner),
            "--out",
            &dest.display().to_string(),
        ],
    )?;
    assert_eq!(code, 0, "{out}{err}");
    assert!(err.contains("exported sources=1 "), "{err}");
    assert_eq!(
        fs::read(dest.join("bodies/0000.bin")).map_err(e)?,
        fs::read(&s.file).map_err(e)?
    );

    let dest = s.rig.dir.join("exported-shared");
    let (code, out, err) = run(
        &s,
        &[
            "export",
            &owner_prefix(&shared),
            "--out",
            &dest.display().to_string(),
        ],
    )?;
    assert_eq!(code, 0, "{out}{err}");
    assert!(err.contains("exported sources=1 "), "{err}");
    assert_eq!(
        fs::read(dest.join("bodies/0000.bin")).map_err(e)?,
        RECORD_BODY.as_bytes()
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
