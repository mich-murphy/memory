//! Brief 39 VERIFY: a Claude transcript atomically replaced (new inode) by its old bytes plus new
//! lines lands one Extend carrying only the new bytes, not a replay.
use super::assemble::{PREFIX, Rig, SESSION, lines};
use super::fake::Fake;
use super::{R, e};
use memory::assemble::start_from_config;
use memory::config::Config;
use memory::daemon::Sources;
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory_core::limits::owner_prefix;
use serde_json::Value;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::process::Command;

fn object_puts(server: &Fake) -> Vec<Vec<u8>> {
    server
        .requests()
        .into_iter()
        .filter(|r| r.method == "PUT" && r.path.contains("/objects/"))
        .map(|r| zstd::decode_all(&r.body[..]).unwrap_or_default())
        .collect()
}

#[test]
fn atomic_replace_with_same_prefix_extends_one_epoch() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("idv", &server.url(), PREFIX, false)?;
    let file = rig.root.join(SESSION);
    let dir = file.parent().ok_or("parent")?.to_owned();
    fs::create_dir_all(&dir).map_err(e)?;
    let old = lines(0..12);
    fs::write(&file, &old).map_err(e)?;
    let mut daemon = start_from_config(&rig.state, &rig.cfg).map_err(e)?;
    let round = |daemon: &mut memory::assemble::ProductionDaemon| -> R {
        let r = daemon.reconcile_once().map_err(e)?;
        assert_eq!((r.failed, r.parked), (0, 0));
        Ok(())
    };
    for _ in 0..2 {
        round(&mut daemon)?;
    }
    let before = object_puts(&server).len();
    let total_before: usize = object_puts(&server).iter().map(Vec::len).sum();
    assert_eq!(total_before, old.len());

    let new = format!("{old}{}", lines(12..20));
    let tmp = dir.join("replace.tmp");
    fs::write(&tmp, &new).map_err(e)?;
    fs::rename(&tmp, &file).map_err(e)?;
    for _ in 0..3 {
        round(&mut daemon)?;
    }
    let puts = object_puts(&server);
    let added: Vec<&Vec<u8>> = puts.iter().skip(before).collect();
    let added_len: usize = added.iter().map(|b| b.len()).sum();
    assert_eq!(added_len, new.len() - old.len(), "only the new bytes");
    let joined: Vec<u8> = added.iter().flat_map(|b| b.iter().copied()).collect();
    assert_eq!(joined, lines(12..20).as_bytes());
    daemon.shutdown().map_err(e)?;
    fs::create_dir_all(&rig.state).map_err(e)?;
    fs::set_permissions(&rig.state, fs::Permissions::from_mode(0o700)).map_err(e)?;
    fs::copy(&rig.cfg, rig.state.join("config.toml")).map_err(e)?;

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
    let run = |args: &[&std::ffi::OsStr]| {
        let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
        cmd.env_clear()
            .env("MEMORY_STATE_DIR", &rig.state)
            .args(args);
        if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
            cmd.env("LLVM_PROFILE_FILE", v);
        }
        cmd.output().map_err(e)
    };
    let text = owner_prefix(&owner);
    let dest = rig.dir.join("out");
    let ex = run(&[
        "export".as_ref(),
        text.as_ref(),
        "--out".as_ref(),
        dest.as_os_str(),
    ])?;
    assert_eq!(
        ex.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&ex.stderr)
    );
    let meta: Value =
        serde_json::from_slice(&fs::read(dest.join("metadata.json")).map_err(e)?).map_err(e)?;
    let sources = meta
        .pointer("/manifest/sources")
        .and_then(Value::as_array)
        .ok_or("sources")?;
    assert_eq!(sources.len(), 1, "one epoch for the source");
    let bodies = meta
        .pointer("/bodies")
        .and_then(Value::as_array)
        .ok_or("bodies")?;
    let rel = bodies
        .first()
        .and_then(|b| b.get("path"))
        .and_then(Value::as_str)
        .ok_or("body path")?;
    assert_eq!(fs::read(dest.join(rel)).map_err(e)?, new.as_bytes());
    Ok(())
}
