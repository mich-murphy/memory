//! Brief 38: Codex rollouts keep one identity across archive, recompression and resume.
//! Each scenario runs the daemon against the loopback fake, then `memory check` and
//! `memory export` on the real binary.
use super::assemble::{PREFIX, Rig, lines};
use super::fake::Fake;
use super::{R, e, need};
use memory::assemble::{ProductionDaemon, start_from_config};
use memory::config::Config;
use memory::daemon::Sources;
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory_core::limits::owner_prefix;
use serde_json::Value;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::Command;

const NAME: &str = "rollout-2026-11-17T10-00-00-33333333-3333-4333-8333-333333333333.jsonl";

struct Ctx {
    _server: Fake,
    rig: Rig,
    codex: PathBuf,
    daemon: ProductionDaemon,
}

struct Src {
    id: String,
    path: String,
    state: String,
    role: String,
    body: Vec<u8>,
}

struct Done {
    check: String,
    export_line: String,
    sources: Vec<Src>,
}

impl Done {
    fn has(&self, key: &str, value: u64) -> bool {
        self.check.contains(&format!(" {key}={value} "))
    }
}

fn ctx(label: &str) -> R<Ctx> {
    let server = Fake::start()?;
    let rig = Rig::new(label, &server.url(), PREFIX, false)?;
    let codex = rig.add_root("codex")?;
    fs::create_dir_all(&rig.state).map_err(e)?;
    fs::set_permissions(&rig.state, fs::Permissions::from_mode(0o700)).map_err(e)?;
    fs::copy(&rig.cfg, rig.state.join("config.toml")).map_err(e)?;
    let daemon = start_from_config(&rig.state, &rig.cfg).map_err(e)?;
    Ok(Ctx {
        _server: server,
        rig,
        codex,
        daemon,
    })
}

fn zst(bytes: &[u8]) -> R<Vec<u8>> {
    zstd::encode_all(bytes, 3).map_err(e)
}

fn live(name: &str) -> String {
    format!("sessions/2026/11/17/{name}")
}
fn archived(name: &str) -> String {
    format!("archived_sessions/{name}")
}

impl Ctx {
    fn write(&self, rel: &str, body: &[u8]) -> R {
        let p = self.codex.join(rel);
        fs::create_dir_all(p.parent().ok_or("parent")?).map_err(e)?;
        fs::write(p, body).map_err(e)
    }
    fn remove(&self, rel: &str) -> R {
        fs::remove_file(self.codex.join(rel)).map_err(e)
    }
    fn rounds(&mut self, n: usize) -> R {
        for i in 0..n {
            let r = self.daemon.reconcile_once().map_err(e)?;
            assert_eq!(r.failed, 0, "round {i}: failed");
            assert_eq!(r.parked, 0, "round {i}: parked");
        }
        Ok(())
    }
    fn finish(self) -> R<Done> {
        let Self {
            _server,
            rig,
            codex: _,
            daemon,
        } = self;
        daemon.shutdown().map_err(e)?;
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
        let text = owner_prefix(&owner);
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
        let out = run(&["check".as_ref()])?;
        let stdout = String::from_utf8_lossy(&out.stdout).into_owned();
        assert_eq!(out.status.code(), Some(0), "{stdout}");
        let check = format!("{} ", stdout.lines().next().unwrap_or_default());
        assert!(
            check.contains(" mismatch=0 ") && check.contains(" uncaptured=0 "),
            "{check}"
        );
        let dest = rig.dir.join("out");
        let ex = run(&[
            "export".as_ref(),
            text.as_ref(),
            "--out".as_ref(),
            dest.as_os_str(),
        ])?;
        let export_line = String::from_utf8_lossy(&ex.stderr).into_owned();
        assert_eq!(ex.status.code(), Some(0), "{export_line}");
        let meta: Value =
            serde_json::from_slice(&fs::read(dest.join("metadata.json")).map_err(e)?).map_err(e)?;
        let sources = read_sources(&meta, &dest)?;
        Ok(Done {
            check,
            export_line,
            sources,
        })
    }
}

fn get<'a>(v: &'a Value, k: &str) -> R<&'a Value> {
    need(v.get(k), k)
}
fn text(v: &Value, k: &str) -> R<String> {
    Ok(need(get(v, k)?.as_str(), k)?.to_owned())
}

fn read_sources(meta: &Value, dest: &Path) -> R<Vec<Src>> {
    let sources = need(
        get(get(meta, "manifest")?, "sources")?.as_array(),
        "sources",
    )?;
    let bodies = need(get(meta, "bodies")?.as_array(), "bodies")?;
    let mut out = Vec::new();
    for (s, b) in sources.iter().zip(bodies) {
        out.push(Src {
            id: text(s, "source_id")?,
            path: text(s, "path")?,
            state: text(s, "state")?,
            role: text(s, "role")?,
            body: fs::read(dest.join(text(b, "path")?)).map_err(e)?,
        });
    }
    assert_eq!(sources.len(), bodies.len());
    Ok(out)
}

#[test]
fn archived_then_appended_lands_under_the_archived_path() -> R {
    let mut c = ctx("ri-archived")?;
    c.write(&live(NAME), lines(0..12).as_bytes())?;
    c.rounds(2)?;
    fs::create_dir_all(c.codex.join("archived_sessions")).map_err(e)?;
    fs::rename(c.codex.join(live(NAME)), c.codex.join(archived(NAME))).map_err(e)?;
    c.rounds(2)?;
    let mut f = fs::OpenOptions::new()
        .append(true)
        .open(c.codex.join(archived(NAME)))
        .map_err(e)?;
    std::io::Write::write_all(&mut f, lines(12..20).as_bytes()).map_err(e)?;
    drop(f);
    let want = fs::read(c.codex.join(archived(NAME))).map_err(e)?;
    c.rounds(3)?;
    let d = c.finish()?;
    assert_eq!(d.sources.len(), 1);
    let s = d.sources.first().ok_or("source")?;
    assert_eq!(s.path, archived(NAME));
    assert_eq!((s.state.as_str(), s.role.as_str()), ("active", "main"));
    assert_eq!(s.body, want);
    assert!(d.export_line.contains("skipped_deleted=0"));
    Ok(())
}

#[test]
fn recompressed_rollout_lands_the_zst_beside_the_plain_source() -> R {
    // Deletion of the vanished plain source is not detected by the walk (reported on brief 38).
    let mut c = ctx("ri-recompressed")?;
    let plain = lines(0..12);
    c.write(&live(NAME), plain.as_bytes())?;
    c.rounds(2)?;
    c.remove(&live(NAME))?;
    c.write(&live(&format!("{NAME}.zst")), &zst(plain.as_bytes())?)?;
    c.rounds(5)?;
    let d = c.finish()?;
    assert_eq!(d.sources.len(), 2, "{}", d.export_line);
    assert!(d.sources.iter().any(|s| s.id.ends_with(".jsonl.zst")));
    for s in &d.sources {
        assert_eq!(s.role, "main");
        assert_eq!(s.body, plain.as_bytes());
    }
    Ok(())
}

#[test]
fn resumed_rollout_lands_the_plain_source_beside_the_zst() -> R {
    // Deletion of the vanished .zst source is not detected by the walk (reported on brief 38).
    let mut c = ctx("ri-resumed")?;
    let old = lines(0..12);
    c.write(&live(&format!("{NAME}.zst")), &zst(old.as_bytes())?)?;
    c.rounds(2)?;
    c.remove(&live(&format!("{NAME}.zst")))?;
    let new = lines(0..20);
    c.write(&live(NAME), new.as_bytes())?;
    c.rounds(5)?;
    let d = c.finish()?;
    assert_eq!(d.sources.len(), 2, "{}", d.export_line);
    let s = d
        .sources
        .iter()
        .find(|s| !s.id.ends_with(".zst"))
        .ok_or("plain")?;
    assert_eq!((s.state.as_str(), s.role.as_str()), ("active", "main"));
    assert_eq!(s.body, new.as_bytes());
    Ok(())
}

#[test]
fn resumed_after_recompression_gives_the_plain_source_a_new_epoch() -> R {
    let mut c = ctx("ri-resumed-twice")?;
    let old = lines(0..12);
    c.write(&live(NAME), old.as_bytes())?;
    c.rounds(2)?;
    c.remove(&live(NAME))?;
    c.write(&live(&format!("{NAME}.zst")), &zst(old.as_bytes())?)?;
    c.rounds(5)?;
    c.remove(&live(&format!("{NAME}.zst")))?;
    let new = lines(0..20);
    c.write(&live(NAME), new.as_bytes())?;
    c.rounds(5)?;
    let d = c.finish()?;
    let plain: Vec<&Src> = d
        .sources
        .iter()
        .filter(|s| !s.id.ends_with(".zst"))
        .collect();
    // Brief 39: the resumed plain file has a new inode but the same prefix, so it is verified
    // by content and continues the one epoch (no sealed epoch beside it).
    assert_eq!(plain.len(), 1, "{}", d.export_line);
    assert_eq!(plain.first().map(|s| s.state.as_str()), Some("active"));
    assert_eq!(
        plain.first().map(|s| s.body.as_slice()),
        Some(new.as_bytes())
    );
    Ok(())
}

#[test]
fn plain_and_zst_at_once_both_land() -> R {
    let mut c = ctx("ri-both")?;
    let body = lines(0..12);
    c.write(&live(NAME), body.as_bytes())?;
    c.write(&live(&format!("{NAME}.zst")), &zst(body.as_bytes())?)?;
    c.rounds(3)?;
    let d = c.finish()?;
    assert!(d.has("duplicate", 0), "{}", d.check);
    assert_eq!(d.sources.len(), 2);
    assert_ne!(
        d.sources.first().map(|s| &s.id),
        d.sources.last().map(|s| &s.id)
    );
    for s in &d.sources {
        assert_eq!((s.state.as_str(), s.role.as_str()), ("active", "main"));
        assert_eq!(s.body, body.as_bytes());
    }
    Ok(())
}

#[test]
fn sessions_and_archived_copies_at_once_land_once() -> R {
    let mut c = ctx("ri-copies")?;
    let body = lines(0..12);
    c.write(&live(NAME), body.as_bytes())?;
    c.write(&archived(NAME), body.as_bytes())?;
    c.rounds(3)?;
    let d = c.finish()?;
    assert!(d.has("duplicate", 1), "{}", d.check);
    assert_eq!(d.sources.len(), 1);
    let s = d.sources.first().ok_or("source")?;
    assert_eq!(s.path, archived(NAME));
    assert_eq!(s.body, body.as_bytes());
    Ok(())
}
