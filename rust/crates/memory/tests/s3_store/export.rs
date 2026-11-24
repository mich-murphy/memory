//! `memory export` on the real binary against the loopback fake.
use super::assemble::{PREFIX, Rig, SESSION, lines};
use super::fake::Fake;
use super::{KEY_ID, R, SECRET, e};
use memory::assemble::start_from_config;
use memory::config::Config;
use memory::daemon::Sources;
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory_core::limits::owner_prefix;
use std::fs;
use std::io::Write;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::{Command, Output};

pub(super) fn capture(rig: &Rig) -> R<(String, PathBuf)> {
    let file = rig.root.join(SESSION);
    fs::create_dir_all(file.parent().ok_or("parent")?).map_err(e)?;
    fs::write(&file, lines(0..12)).map_err(e)?;
    let mut daemon = start_from_config(&rig.state, &rig.cfg).map_err(e)?;
    daemon.reconcile_once().map_err(e)?;
    fs::OpenOptions::new()
        .append(true)
        .open(&file)
        .map_err(e)?
        .write_all(lines(12..20).as_bytes())
        .map_err(e)?;
    daemon.reconcile_once().map_err(e)?;
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
    Ok((owner_prefix(&owner), file))
}

fn run(state: &Path, args: &[&std::ffi::OsStr]) -> R<Output> {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear().env("MEMORY_STATE_DIR", state).args(args);
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    cmd.output().map_err(e)
}
fn err(o: &Output) -> String {
    String::from_utf8_lossy(&o.stderr).into_owned()
}
fn names(dir: &Path) -> R<Vec<String>> {
    let mut v: Vec<String> = fs::read_dir(dir)
        .map_err(e)?
        .map(|x| x.map(|x| x.file_name().to_string_lossy().into_owned()))
        .collect::<Result<_, _>>()
        .map_err(e)?;
    v.sort();
    Ok(v)
}

/// Exit code, empty stdout, and exactly one stderr line equal to `line`.
fn check(o: &Output, code: i32, line: &str) {
    assert_eq!(o.status.code(), Some(code), "{}", err(o));
    assert!(o.stdout.is_empty());
    assert_eq!(err(o), format!("{line}\n"));
}

#[test]
fn the_binary_exports_a_session_and_its_exit_table() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("export-bin", &server.url(), PREFIX, false)?;
    let (text, file) = capture(&rig)?;
    let want = fs::read(&file).map_err(e)?;
    let out = rig.dir.join("out");
    let o = out.as_os_str();
    let r = run(
        &rig.dir,
        &["export".as_ref(), text.as_ref(), "--out".as_ref(), o],
    )?;
    assert_eq!(r.status.code(), Some(0), "{}", err(&r));
    assert!(r.stdout.is_empty());
    let line = err(&r);
    assert_eq!(line.lines().count(), 1);
    assert_eq!(
        line,
        format!(
            "memory: exported sources=1 deleted=0 bytes={}\n",
            want.len()
        )
    );
    assert_eq!(fs::read(out.join("bodies/0000.bin")).map_err(e)?, want);
    assert_eq!(names(&out)?, ["bodies", "metadata.json"]);
    let mode = |p: &Path| fs::metadata(p).map(|m| m.permissions().mode() & 0o777);
    assert_eq!(mode(&out).map_err(e)?, 0o700);
    assert_eq!(mode(&out.join("metadata.json")).map_err(e)?, 0o600);
    for text_out in [err(&r)] {
        for secret in [
            text.as_str(),
            SECRET,
            KEY_ID,
            &server.url(),
            "objects/",
            PREFIX,
        ] {
            assert!(!text_out.contains(secret));
        }
    }
    // The destination exists now: refused and unchanged.
    let before = fs::read(out.join("metadata.json")).map_err(e)?;
    let r = run(
        &rig.dir,
        &["export".as_ref(), text.as_ref(), "--out".as_ref(), o],
    )?;
    check(&r, 1, "memory: output_exists");
    assert_eq!(fs::read(out.join("metadata.json")).map_err(e)?, before);
    assert_eq!(names(&out)?, ["bodies", "metadata.json"]);

    // Usage refusals (64).
    let fresh = rig.dir.join("fresh");
    let f = fresh.as_os_str();
    let t = text.as_str();
    let cases: Vec<(Vec<&std::ffi::OsStr>, &str)> = vec![
        (vec!["export".as_ref(), t.as_ref()], "memory: usage"),
        (
            vec!["export".as_ref(), "--out".as_ref(), f],
            "memory: usage",
        ),
        (
            vec![
                "export".as_ref(),
                t.as_ref(),
                t.as_ref(),
                "--out".as_ref(),
                f,
            ],
            "memory: usage",
        ),
        (
            vec![
                "export".as_ref(),
                t.as_ref(),
                "--out".as_ref(),
                f,
                "--max-bytes".as_ref(),
                "abc".as_ref(),
            ],
            "memory: usage",
        ),
        (
            vec![
                "export".as_ref(),
                t.as_ref(),
                "--out".as_ref(),
                f,
                "--max-bytes".as_ref(),
                "0".as_ref(),
            ],
            "memory: usage",
        ),
        (
            vec![
                "export".as_ref(),
                t.as_ref(),
                "--out".as_ref(),
                f,
                "--max-bytes".as_ref(),
                "1099511627777".as_ref(),
            ],
            "memory: usage",
        ),
        (
            vec!["export".as_ref(), "garbage".as_ref(), "--out".as_ref(), f],
            "memory: owner_text",
        ),
    ];
    for (args, line) in cases {
        check(&run(&rig.dir, &args)?, 64, line);
    }
    // Brief 56: a shared owner is no longer a usage error; one with no `current.json` is
    // absent (66), as for a session owner.
    check(
        &run(
            &rig.dir,
            &[
                "export".as_ref(),
                "shared/a/m/claude".as_ref(),
                "--out".as_ref(),
                f,
            ],
        )?,
        66,
        "memory: owner_absent",
    );
    assert!(!fresh.exists());
    // Too large: exit 4, nothing created.
    let r = run(
        &rig.dir,
        &[
            "export".as_ref(),
            t.as_ref(),
            "--out".as_ref(),
            f,
            "--max-bytes".as_ref(),
            "1".as_ref(),
        ],
    )?;
    check(&r, 4, "memory: export_too_large");
    assert!(!fresh.exists(), "limits refuse before creating anything");
    // 1 TiB itself is accepted by the parser.
    let r = run(
        &rig.dir,
        &[
            "export".as_ref(),
            t.as_ref(),
            "--out".as_ref(),
            f,
            "--max-bytes".as_ref(),
            "1099511627776".as_ref(),
        ],
    )?;
    assert_eq!(r.status.code(), Some(0), "{}", err(&r));
    fs::remove_dir_all(&fresh).map_err(e)?;
    // Absent owner: 66, nothing created.
    let r = run(
        &rig.dir,
        &[
            "export".as_ref(),
            "sessions/a/m/claude/nope".as_ref(),
            "--out".as_ref(),
            f,
        ],
    )?;
    check(&r, 66, "memory: owner_absent");
    assert!(!fresh.exists());
    // No config: 2.
    let empty = rig.dir.join("empty-state");
    fs::create_dir_all(&empty).map_err(e)?;
    let r = run(
        &empty,
        &["export".as_ref(), t.as_ref(), "--out".as_ref(), f],
    )?;
    check(&r, 2, "memory: config_read");
    assert!(!fresh.exists());
    Ok(())
}

#[test]
fn a_corrupt_object_exits_1_and_leaves_no_directory() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("export-corrupt", &server.url(), PREFIX, false)?;
    let (text, _) = capture(&rig)?;
    let obj = server
        .requests()
        .into_iter()
        .find(|r| r.method == "PUT" && r.path.contains("/objects/"))
        .ok_or("no object put")?;
    server.seed(&obj.path, b"corrupted bytes");
    let out = rig.dir.join("out");
    let r = run(
        &rig.dir,
        &[
            "export".as_ref(),
            text.as_ref(),
            "--out".as_ref(),
            out.as_os_str(),
        ],
    )?;
    check(&r, 1, "memory: source_corrupt");
    assert!(!out.exists());
    Ok(())
}

#[test]
fn an_existing_destination_is_refused_before_any_fetch() -> R {
    let owner = memory_core::limits::parse_owner_prefix("sessions/a/m/claude/s1").map_err(e)?;
    let dir = std::env::temp_dir().join(format!("b32-exists-{}", std::process::id()));
    let _ = fs::remove_dir_all(&dir);
    fs::create_dir_all(&dir).map_err(e)?;
    let mut calls = 0;
    let r = memory::export::export_owner(
        &owner,
        |_: &str| -> Result<Option<Vec<u8>>, ()> {
            calls += 1;
            Ok(None)
        },
        &dir,
        1 << 30,
    );
    assert_eq!(r, Err(memory::export::Failure::OutputExists));
    assert_eq!(calls, 0);
    // Brief 56: `Failure::Shared` is removed; a shared owner is exported like any owner, so an
    // absent one is `Absent`.
    let shared = memory_core::limits::parse_owner_prefix("shared/a/m/claude").map_err(e)?;
    let fresh = dir.join("fresh");
    let r = memory::export::export_owner(
        &shared,
        |_: &str| Ok::<Option<Vec<u8>>, ()>(None),
        &fresh,
        1,
    );
    assert_eq!(r, Err(memory::export::Failure::Absent));
    let _ = fs::remove_dir_all(&dir);
    Ok(())
}

/// Brief 56: a shared Pi owner with one run record and one non-run-record `PiAux` file exports
/// the record only; the line carries `skipped`.
#[test]
fn the_binary_exports_a_shared_owner_run_records() -> R {
    use super::daemon_restore::setup;
    let s = setup("export-shared")?;
    let pi = s.rig.add_root("pi")?;
    fs::copy(&s.rig.cfg, s.rig.state.join("config.toml")).map_err(e)?;
    let dir = pi.join("sessions/--pi-cwd--/subagent-artifacts");
    fs::create_dir_all(&dir).map_err(e)?;
    let body = "{\"runId\":\"r1\",\"agent\":\"worker\",\"exitCode\":0}\n";
    fs::write(dir.join("run1.json"), body).map_err(e)?;
    let r = run(&s.rig.state, &["reconcile".as_ref()])?;
    assert_eq!(r.status.code(), Some(0), "{}", err(&r));
    let prefix = shared_prefix(&s)?;
    let out = s.rig.dir.join("shared-out");
    let r = run(
        &s.rig.state,
        &[
            "export".as_ref(),
            prefix.as_ref(),
            "--out".as_ref(),
            out.as_os_str(),
        ],
    )?;
    check(
        &r,
        0,
        &format!(
            "memory: exported sources=1 deleted=0 bytes={} skipped=0",
            body.len()
        ),
    );
    assert_eq!(
        fs::read(out.join("bodies/0000.bin")).map_err(e)?,
        body.as_bytes()
    );
    let meta = fs::read_to_string(out.join("metadata.json")).map_err(e)?;
    assert!(meta.contains("\"shared-reader/v1\"") && meta.contains("\"pi_run_record\""));
    Ok(())
}

fn shared_prefix(s: &super::daemon_restore::Setup) -> R<String> {
    let config = Config::load(&s.rig.cfg).map_err(e)?;
    let owner = WalkSources::new(
        locator_roots(&config).map_err(e)?,
        WalkLimits::default(),
        SharedFs::new(),
    )
    .enumerate()
    .into_iter()
    .map(|(o, _)| o)
    .find(|o| *o != s.owner)
    .ok_or("no shared owner")?;
    Ok(owner_prefix(&owner))
}
