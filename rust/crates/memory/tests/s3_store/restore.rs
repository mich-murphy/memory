//! R3: `memory restore` through the library and the real binary, against the local server.
use super::assemble::{MARKER, PREFIX, Rig, SESSION, lines};
use super::fake::Fake;
use super::{BUCKET, R, e, store_at};
use memory::assemble::start_from_config;
use memory::config::Config;
use memory::daemon::Sources;
use memory::environment::{Store, StoreError};
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::restore::{Outcome, RestoreError, restore_owner, restore_state};
use memory::roots::locator_roots;
use memory::s3::TimeoutPolicy;
use memory_core::domain::{Offset, Owner, SourceClass, SourceId, SourceKind};
use memory_core::limits::owner_prefix;
use memory_core::manifest::{
    EpochRecord, EpochStamp, EpochWriter, OwnerState, Piece, SourceEntry, Timestamp, manifest_hash,
};
use std::collections::HashMap;
use std::fs;
use std::io::Write;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::{Command, Output};

/// Capture the synthetic session in two rounds; returns its owner and source file.
fn capture(server: &Fake, rig: &Rig) -> R<(Owner, PathBuf)> {
    let file = rig.root.join(SESSION);
    fs::create_dir_all(file.parent().ok_or("parent")?).map_err(e)?;
    fs::write(&file, lines(0..12)).map_err(e)?;
    let mut daemon = start_from_config(&rig.state, &rig.cfg).map_err(e)?;
    let round = |d: &mut memory::assemble::ProductionDaemon| -> R {
        let report = d.reconcile_once().map_err(e)?;
        assert_eq!((report.landed, report.failed, report.parked), (1, 0, 0));
        Ok(())
    };
    round(&mut daemon)?;
    fs::OpenOptions::new()
        .append(true)
        .open(&file)
        .map_err(e)?
        .write_all(lines(12..20).as_bytes())
        .map_err(e)?;
    round(&mut daemon)?;
    daemon.shutdown().map_err(e)?;
    let _ = server;
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
    Ok((owner, file))
}

fn run(rig_state: &Path, args: &[&std::ffi::OsStr]) -> R<Output> {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear()
        .env("MEMORY_STATE_DIR", rig_state)
        .args(args);
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    cmd.output().map_err(e)
}

fn stderr_of(out: &Output) -> String {
    String::from_utf8_lossy(&out.stderr).into_owned()
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

#[test]
fn library_restores_the_captured_file_byte_for_byte_with_gets_only() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("restore-lib", &server.url(), PREFIX, false)?;
    let (owner, file) = capture(&server, &rig)?;
    let before = server.requests().len();
    let mut store = store_at(&server.url(), PREFIX, TimeoutPolicy::PRODUCTION)?;
    let out = rig.dir.join("out");
    let fetch = |key: &str| match store.get(key) {
        Ok(s) => Ok(Some(s.body)),
        Err(StoreError::NotFound) => Ok(None),
        Err(other) => Err(other),
    };
    let outcome = restore_owner(&owner, fetch, &out).map_err(e)?;
    let Outcome::Restored(report) = outcome else {
        return Err(format!("{outcome:?}"));
    };
    let want = fs::read(&file).map_err(e)?;
    assert_eq!(fs::read(out.join(SESSION)).map_err(e)?, want);
    assert!(String::from_utf8(want.clone()).map_err(e)?.contains(MARKER));
    assert_eq!(
        (report.written, report.skipped_deleted, report.bytes),
        (1, 0, want.len() as u64)
    );
    let mode = |p: &Path| fs::metadata(p).map(|m| m.permissions().mode() & 0o777);
    assert_eq!(mode(&out).map_err(e)?, 0o700);
    assert_eq!(mode(&out.join(SESSION)).map_err(e)?, 0o600);
    assert_eq!(
        mode(out.join(SESSION).parent().ok_or("p")?).map_err(e)?,
        0o700
    );
    let seen = server.requests();
    let new = seen.get(before..).ok_or("slice")?;
    assert!(!new.is_empty());
    let root = format!("/{BUCKET}/{PREFIX}");
    assert!(
        new.iter()
            .all(|r| r.method == "GET" && r.path.starts_with(&root)),
        "{new:?}"
    );
    Ok(())
}

#[test]
fn an_absent_owner_is_its_own_outcome_and_creates_nothing() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("restore-absent", &server.url(), PREFIX, false)?;
    let mut store = store_at(&server.url(), PREFIX, TimeoutPolicy::PRODUCTION)?;
    let owner = memory_core::limits::parse_owner_prefix("shared/a/m/claude").map_err(e)?;
    let out = rig.dir.join("out");
    let fetch = |key: &str| match store.get(key) {
        Ok(s) => Ok(Some(s.body)),
        Err(StoreError::NotFound) => Ok(None),
        Err(other) => Err(other),
    };
    assert_eq!(restore_owner(&owner, fetch, &out), Ok(Outcome::Absent));
    assert!(!out.exists());
    Ok(())
}

#[test]
fn a_non_empty_output_is_refused_before_any_fetch() -> R {
    let rig = Rig::new("restore-nonempty", "http://127.0.0.1:9", PREFIX, false)?;
    let owner = memory_core::limits::parse_owner_prefix("shared/a/m/claude").map_err(e)?;
    let out = rig.dir.join("out");
    fs::create_dir_all(&out).map_err(e)?;
    fs::write(out.join("keep"), b"x").map_err(e)?;
    let mut calls = 0;
    let fetch = |_: &str| -> Result<Option<Vec<u8>>, ()> {
        calls += 1;
        Ok(None)
    };
    assert_eq!(
        restore_owner(&owner, fetch, &out),
        Err(RestoreError::OutputNotEmpty)
    );
    assert_eq!(calls, 0);
    assert_eq!(names(&out)?, ["keep"]);
    // A regular file at DIR is refused too.
    let file = rig.dir.join("afile");
    fs::write(&file, b"x").map_err(e)?;
    assert_eq!(
        restore_owner(&owner, |_: &str| Ok::<_, ()>(None), &file),
        Err(RestoreError::OutputNotEmpty)
    );
    Ok(())
}

// ---------------------------------------------------------------- hostile and odd manifests

fn entry(path: &str, epochs: Vec<EpochRecord>, deleted: bool) -> R<SourceEntry> {
    Ok(SourceEntry {
        kind: SourceKind::ClaudeTranscript,
        path: path.to_owned(),
        class: SourceClass::Append,
        epochs,
        deleted_at: if deleted {
            Some(Timestamp::try_from("2026-11-07T10:30:00Z".to_owned()).map_err(e)?)
        } else {
            None
        },
    })
}

fn state_of(entries: Vec<SourceEntry>) -> R<OwnerState> {
    let mut sources = std::collections::BTreeMap::new();
    for (i, en) in entries.into_iter().enumerate() {
        sources.insert(SourceId::try_from(format!("s{i}")).map_err(e)?, en);
    }
    Ok(OwnerState { sources })
}

/// One epoch whose single piece is `content`, plus the object it needs.
fn epoch(n: u32, content: &[u8], objects: &mut HashMap<String, Vec<u8>>) -> R<EpochRecord> {
    let raw = manifest_hash(content);
    let stored = zstd::encode_all(content, 3).map_err(e)?;
    let object = format!("objects/{}.zst", String::from(raw));
    let piece: Piece = serde_json::from_value(serde_json::json!({
        "offset": Offset::new(0).get(), "len": content.len(), "sha256": String::from(raw),
        "object": object, "encoding": "zstd-3",
        "encoded_len": stored.len(), "encoded_sha256": String::from(manifest_hash(&stored)),
    }))
    .map_err(e)?;
    objects.insert(object, stored);
    Ok(EpochRecord {
        epoch: EpochStamp {
            n,
            writer: EpochWriter {
                version: "0.1.0".to_owned(),
                commit: "abc".to_owned(),
            },
            rules: None,
        },
        size: content.len() as u64,
        sha256: raw,
        pieces: vec![piece],
    })
}

fn tmp(label: &str) -> R<PathBuf> {
    let dir = std::env::temp_dir().join(format!("r3-restore-{}-{label}", std::process::id()));
    let _ = fs::remove_dir_all(&dir);
    fs::create_dir_all(&dir).map_err(e)?;
    Ok(dir)
}

fn no_fetch(_: &str) -> Result<Option<Vec<u8>>, ()> {
    Ok(None)
}

#[test]
fn untrusted_paths_are_refused_before_anything_is_written() -> R {
    let base = tmp("paths")?;
    for bad in ["../x", "a/../b", "/abs", "", "a//b", "./a", "a/\0b"] {
        let out = base.join("out");
        let state = state_of(vec![
            entry("ok.txt", vec![], false)?,
            entry(bad, vec![], false)?,
        ])?;
        assert_eq!(
            restore_state(&state, no_fetch, &out),
            Err(RestoreError::BadSourcePath),
            "{bad:?}"
        );
        assert!(!out.exists(), "{bad:?} left output");
    }
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

#[test]
fn colliding_paths_are_refused_and_leave_nothing() -> R {
    let base = tmp("collide")?;
    let out = base.join("out");
    for pair in [("a/b", "a/b"), ("a", "a/b"), ("a/b/c", "a/b")] {
        let state = state_of(vec![
            entry(pair.0, vec![], false)?,
            entry(pair.1, vec![], false)?,
        ])?;
        assert_eq!(
            restore_state(&state, no_fetch, &out),
            Err(RestoreError::PathCollision),
            "{pair:?}"
        );
        assert!(!out.exists());
    }
    // An empty pre-existing DIR stays empty after a refusal.
    fs::create_dir_all(&out).map_err(e)?;
    let state = state_of(vec![
        entry("a", vec![], false)?,
        entry("a/b", vec![], false)?,
    ])?;
    assert!(restore_state(&state, no_fetch, &out).is_err());
    assert!(names(&out)?.is_empty());
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

#[test]
fn a_failure_while_writing_removes_what_the_run_created() -> R {
    let base = tmp("partial")?;
    let out = base.join("out");
    let mut objects = HashMap::new();
    let good = epoch(0, b"first\n", &mut objects)?;
    let missing = epoch(0, b"second\n", &mut HashMap::new())?;
    let state = state_of(vec![
        entry("d/one.txt", vec![good], false)?,
        entry("d/two.txt", vec![missing], false)?,
    ])?;
    let fetch = |k: &str| Ok::<_, ()>(objects.get(k).cloned());
    assert_eq!(
        restore_state(&state, fetch, &out),
        Err(RestoreError::ObjectMissing)
    );
    assert!(!out.exists());
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

#[test]
fn deleted_sources_are_skipped_and_the_latest_epoch_is_written() -> R {
    let base = tmp("latest")?;
    let out = base.join("out");
    let mut objects = HashMap::new();
    let e0 = epoch(0, b"old epoch\n", &mut objects)?;
    let e1 = epoch(1, b"the newer epoch\n", &mut objects)?;
    let state = state_of(vec![
        entry("live.txt", vec![e0.clone(), e1], false)?,
        entry("gone.txt", vec![e0], true)?,
        entry("gone-empty.txt", vec![], true)?,
    ])?;
    let fetch = |k: &str| Ok::<_, ()>(objects.get(k).cloned());
    let report = restore_state(&state, fetch, &out).map_err(e)?;
    assert_eq!(
        (report.written, report.skipped_deleted, report.bytes),
        (1, 2, 16)
    );
    assert_eq!(
        fs::read(out.join("live.txt")).map_err(e)?,
        b"the newer epoch\n"
    );
    assert_eq!(names(&out)?, ["live.txt"]);
    let _ = fs::remove_dir_all(&base);
    Ok(())
}

#[test]
fn errors_and_codes_carry_no_input_text() {
    for error in [
        RestoreError::OutputNotEmpty,
        RestoreError::OutputIo,
        RestoreError::Store,
        RestoreError::NewerSchema,
        RestoreError::ChainCorrupt,
        RestoreError::BadSourcePath,
        RestoreError::PathCollision,
        RestoreError::ObjectMissing,
        RestoreError::SourceCorrupt,
    ] {
        let text = format!("{error:?} {error}");
        assert!(!text.contains('/') && !text.contains("http"), "{text}");
    }
}

// ---------------------------------------------------------------- the real binary

#[test]
fn the_binary_restores_with_out_before_or_after_the_owner() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("restore-bin", &server.url(), PREFIX, false)?;
    let (owner, file) = capture(&server, &rig)?;
    let text = owner_prefix(&owner);
    let want = fs::read(&file).map_err(e)?;
    let state_before = names(&rig.state)?;
    for (label, out_first) in [("a", false), ("b", true)] {
        let out = rig.dir.join(format!("out-{label}"));
        let args: Vec<&std::ffi::OsStr> = if out_first {
            vec![
                "restore".as_ref(),
                "--out".as_ref(),
                out.as_os_str(),
                text.as_ref(),
            ]
        } else {
            vec![
                "restore".as_ref(),
                text.as_ref(),
                "--out".as_ref(),
                out.as_os_str(),
            ]
        };
        let result = run(&rig.dir, &args)?;
        assert_eq!(result.status.code(), Some(0), "{}", stderr_of(&result));
        assert!(result.stdout.is_empty());
        let err = stderr_of(&result);
        assert_eq!(err.lines().count(), 1, "{err}");
        assert!(err.starts_with("memory: restored written=1 skipped_deleted=0 bytes="));
        assert_eq!(fs::read(out.join(SESSION)).map_err(e)?, want);
    }
    assert_eq!(names(&rig.state)?, state_before);
    Ok(())
}

#[test]
fn the_binary_refusals_have_their_codes() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("restore-refuse", &server.url(), PREFIX, false)?;
    let (owner, _) = capture(&server, &rig)?;
    let text = owner_prefix(&owner);
    let out = rig.dir.join("out");
    let o = out.as_os_str();
    let check = |result: Output, code: i32, line: &str| -> R {
        assert_eq!(result.status.code(), Some(code), "{}", stderr_of(&result));
        assert!(result.stdout.is_empty());
        assert_eq!(stderr_of(&result), format!("{line}\n"));
        Ok(())
    };
    check(
        run(
            &rig.dir,
            &[
                "restore".as_ref(),
                "sessions/a/m/claude/nope".as_ref(),
                "--out".as_ref(),
                o,
            ],
        )?,
        66,
        "memory: owner_absent",
    )?;
    check(
        run(
            &rig.dir,
            &["restore".as_ref(), "garbage".as_ref(), "--out".as_ref(), o],
        )?,
        64,
        "memory: owner_text",
    )?;
    check(
        run(&rig.dir, &["restore".as_ref(), text.as_ref()])?,
        64,
        "memory: usage",
    )?;
    check(
        run(
            &rig.dir,
            &[
                "restore".as_ref(),
                text.as_ref(),
                "--out".as_ref(),
                o,
                "x".as_ref(),
            ],
        )?,
        64,
        "memory: usage",
    )?;
    assert!(!out.exists());
    fs::create_dir_all(&out).map_err(e)?;
    fs::write(out.join("keep"), b"x").map_err(e)?;
    check(
        run(
            &rig.dir,
            &["restore".as_ref(), text.as_ref(), "--out".as_ref(), o],
        )?,
        1,
        "memory: output_not_empty",
    )?;
    assert_eq!(names(&out)?, ["keep"]);
    let empty = tmp("noconfig")?;
    let r = run(
        &empty,
        &[
            "restore".as_ref(),
            text.as_ref(),
            "--out".as_ref(),
            rig.dir.join("o2").as_os_str(),
        ],
    )?;
    assert_eq!(r.status.code(), Some(2));
    assert!(r.stdout.is_empty());
    assert_eq!(stderr_of(&r), "memory: config_read\n");
    let _ = fs::remove_dir_all(&empty);
    Ok(())
}
