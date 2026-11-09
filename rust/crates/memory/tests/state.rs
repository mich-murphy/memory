//! Daemon state (DESIGN.md 9.1, 9.2, 12): config table, lock, durable envelope, corruption,
//! crash simulation, hint-dir vetting, machine id and properties. Temp dirs only.
use hegel::{TestCase, generators as gs};
use memory::config::{Config, ConfigError};
use memory::state::{
    DaemonLock, Loaded, MAX_BODY_BYTES, MAX_HEADER_BYTES, MissingReason, Schema, StateDir,
    StateError, StateFile, decode, default_root, encode, read_durable, root_from, stage,
    write_durable,
};
use memory_core::domain::MachineId;
use proptest::prelude::*;
use std::fmt::Debug;
use std::fs;
use std::os::unix::fs::{MetadataExt, PermissionsExt, symlink};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

type R = Result<(), String>;
const SCHEMA: Schema = Schema {
    name: "memory.test",
    version: 1,
};

fn e<E: Debug>(error: E) -> String {
    format!("{error:?}")
}

struct Tmp(PathBuf);
impl Tmp {
    fn new(label: &str) -> Result<Self, String> {
        static N: AtomicU64 = AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!(
            "memory-state-{}-{}-{label}",
            std::process::id(),
            N.fetch_add(1, Ordering::SeqCst)
        ));
        fs::create_dir_all(&path).map_err(e)?;
        fs::set_permissions(&path, fs::Permissions::from_mode(0o700)).map_err(e)?;
        Ok(Self(path))
    }
    fn p(&self) -> &Path {
        &self.0
    }
}
impl Drop for Tmp {
    fn drop(&mut self) {
        if let Ok(rd) = fs::read_dir(&self.0) {
            for entry in rd.flatten() {
                let _ = fs::set_permissions(entry.path(), fs::Permissions::from_mode(0o700));
            }
        }
        let _ = fs::set_permissions(&self.0, fs::Permissions::from_mode(0o700));
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn mode(path: &Path) -> Result<u32, String> {
    Ok(fs::metadata(path).map_err(e)?.mode() & 0o777)
}

fn chmod(path: &Path, m: u32) -> R {
    fs::set_permissions(path, fs::Permissions::from_mode(m)).map_err(e)
}

/// A locked, prepared state dir under `tmp/state`.
fn open(tmp: &Tmp) -> Result<(DaemonLock, StateDir), String> {
    let lock = DaemonLock::acquire(&tmp.p().join("state")).map_err(e)?;
    let dir = StateDir::prepare(&lock).map_err(e)?;
    Ok((lock, dir))
}

fn missing(loaded: &Loaded) -> Option<MissingReason> {
    match loaded {
        Loaded::Missing(reason) => Some(*reason),
        Loaded::Present(_) => None,
    }
}

// ------------------------------------------------------------------ round trip and modes

#[test]
fn round_trip_and_modes() -> R {
    let tmp = Tmp::new("rt")?;
    let (_lock, dir) = open(&tmp)?;
    dir.write(StateFile::Index, b"{\"a\":1}").map_err(e)?;
    let Loaded::Present(stored) = dir.read(StateFile::Index) else {
        return Err("missing".into());
    };
    assert_eq!(stored.body, b"{\"a\":1}");
    assert_eq!(stored.schema_version, 1);
    assert_eq!(stored.writer_version, env!("CARGO_PKG_VERSION"));
    // Overwrite replaces.
    dir.write(StateFile::Index, b"").map_err(e)?;
    let Loaded::Present(stored) = dir.read(StateFile::Index) else {
        return Err("missing".into());
    };
    assert!(stored.body.is_empty());
    assert_eq!(mode(&dir.path(StateFile::Index))?, 0o600);
    assert_eq!(mode(dir.root())?, 0o700);
    assert_eq!(mode(&dir.tmp_dir())?, 0o700);
    assert_eq!(mode(&dir.root().join("daemon.lock"))?, 0o600);
    let hints = dir.vet_hints_dir().map_err(e)?;
    assert_eq!(mode(&hints)?, 0o700);
    assert_eq!(hints, dir.hints_dir());
    assert_eq!(dir.uid(), fs::metadata(dir.root()).map_err(e)?.uid());
    // No temp files remain after a clean write.
    let leftovers = fs::read_dir(dir.root())
        .map_err(e)?
        .flatten()
        .filter(|x| x.file_name().to_string_lossy().contains(".tmp-"))
        .count();
    assert_eq!(leftovers, 0);
    for file in [StateFile::Roots, StateFile::Status, StateFile::MachineId] {
        assert_eq!(
            missing(&dir.read(file)),
            Some(MissingReason::NotFound),
            "{file:?}"
        );
    }
    Ok(())
}

#[test]
fn older_schema_version_is_readable() -> R {
    let old = Schema {
        name: "memory.test",
        version: 0,
    };
    let bytes = encode(old, b"x").map_err(e)?;
    let Loaded::Present(stored) = decode(&bytes, SCHEMA) else {
        return Err("missing".into());
    };
    assert_eq!(stored.schema_version, 0);
    Ok(())
}

// ------------------------------------------------------------------ corruption

fn good() -> Result<Vec<u8>, String> {
    encode(SCHEMA, b"hello body").map_err(e)
}

#[test]
fn every_corruption_reads_as_missing_with_its_reason() -> R {
    let bytes = good()?;
    let newline = bytes.iter().position(|b| *b == b'\n').ok_or("no newline")?;
    let header = String::from_utf8(bytes.get(..newline).ok_or("x")?.to_vec()).map_err(e)?;
    let body = bytes.get(newline + 1..).ok_or("x")?.to_vec();
    let with = |h: &str, b: &[u8]| {
        let mut v = h.as_bytes().to_vec();
        v.push(b'\n');
        v.extend_from_slice(b);
        v
    };
    let fields: Vec<&str> = header.split(' ').collect();
    let f = |i: usize| fields.get(i).copied().unwrap_or("");
    let join = |m: &str, n: &str, w: &str, l: &str, s: &str| format!("{m} {n} {w} {l} {s}");

    let mut flipped = bytes.clone();
    *flipped.last_mut().ok_or("empty")? ^= 1;
    let mut truncated = bytes.clone();
    truncated.pop();
    let mut extra = bytes.clone();
    extra.push(b'!');
    let mut non_utf8 = vec![0xff, 0xfe, b'\n'];
    non_utf8.extend_from_slice(&body);
    let mut long_header = vec![b'a'; MAX_HEADER_BYTES + 10];
    long_header.push(b'\n');

    let cases: Vec<(&str, Vec<u8>, MissingReason)> = vec![
        ("flipped", flipped, MissingReason::ChecksumMismatch),
        ("truncated", truncated, MissingReason::ShortBody),
        ("extra", extra, MissingReason::ExtraBytes),
        (
            "len too long",
            with(&join(f(0), f(1), f(2), "999", f(4)), &body),
            MissingReason::ShortBody,
        ),
        (
            "len too short",
            with(&join(f(0), f(1), f(2), "2", f(4)), &body),
            MissingReason::ExtraBytes,
        ),
        (
            "unknown envelope version",
            with(&join("memory-state/2", f(1), f(2), f(3), f(4)), &body),
            MissingReason::UnknownVersion,
        ),
        (
            "other schema",
            with(&join(f(0), "memory.other/1", f(2), f(3), f(4)), &body),
            MissingReason::SchemaMismatch,
        ),
        (
            "newer schema",
            with(&join(f(0), "memory.test/2", f(2), f(3), f(4)), &body),
            MissingReason::NewerSchema,
        ),
        (
            "bad sha",
            with(&join(f(0), f(1), f(2), f(3), &"0".repeat(64)), &body),
            MissingReason::ChecksumMismatch,
        ),
        (
            "garbage",
            b"not an envelope".to_vec(),
            MissingReason::BadHeader,
        ),
        (
            "garbage with newline",
            b"garbage\nbody".to_vec(),
            MissingReason::BadHeader,
        ),
        ("empty", Vec::new(), MissingReason::BadHeader),
        ("non utf8", non_utf8, MissingReason::BadHeader),
        ("long header", long_header, MissingReason::BadHeader),
        (
            "missing field",
            with("memory-state/1 memory.test/1 0.1.0 10", &body),
            MissingReason::BadHeader,
        ),
        (
            "extra field",
            with(&format!("{header} extra"), &body),
            MissingReason::BadHeader,
        ),
        (
            "bad len",
            with(&join(f(0), f(1), f(2), "ten", f(4)), &body),
            MissingReason::BadHeader,
        ),
        (
            "bad schema syntax",
            with(&join(f(0), "noversion", f(2), f(3), f(4)), &body),
            MissingReason::BadHeader,
        ),
        (
            "bad schema version",
            with(&join(f(0), "memory.test/x", f(2), f(3), f(4)), &body),
            MissingReason::BadHeader,
        ),
    ];
    let tmp = Tmp::new("corrupt")?;
    for (label, data, reason) in cases {
        assert_eq!(missing(&decode(&data, SCHEMA)), Some(reason), "{label}");
        let path = tmp.p().join("f");
        fs::write(&path, &data).map_err(e)?;
        assert_eq!(
            missing(&read_durable(&path, SCHEMA)),
            Some(reason),
            "{label} file"
        );
    }
    // The control decodes.
    assert!(matches!(decode(&bytes, SCHEMA), Loaded::Present(_)));
    Ok(())
}

#[test]
fn read_failures_are_missing() -> R {
    let tmp = Tmp::new("readfail")?;
    // A directory opens but cannot be read.
    assert_eq!(
        missing(&read_durable(tmp.p(), SCHEMA)),
        Some(MissingReason::Unreadable)
    );
    // Oversized file.
    let big = tmp.p().join("big");
    let f = fs::File::create(&big).map_err(e)?;
    f.set_len(u64::try_from(MAX_BODY_BYTES + MAX_HEADER_BYTES + 1).map_err(e)?)
        .map_err(e)?;
    assert_eq!(
        missing(&read_durable(&big, SCHEMA)),
        Some(MissingReason::TooLarge)
    );
    // Unopenable file (skipped when mode bits do not bind, for example as root).
    let locked = tmp.p().join("locked");
    fs::write(&locked, b"x").map_err(e)?;
    chmod(&locked, 0)?;
    if fs::File::open(&locked).is_err() {
        assert_eq!(
            missing(&read_durable(&locked, SCHEMA)),
            Some(MissingReason::Unreadable)
        );
    }
    Ok(())
}

#[test]
fn invalid_names_and_sizes_are_refused() -> R {
    let tmp = Tmp::new("names")?;
    for bad in ["", ".hidden", "a/b", "../x", "sp ace"] {
        assert_eq!(
            write_durable(tmp.p(), bad, SCHEMA, b"x"),
            Err(StateError::InvalidName),
            "{bad:?}"
        );
        assert_eq!(
            encode(
                Schema {
                    name: "x",
                    version: 1
                },
                b""
            )
            .map(|_| ()),
            Ok(())
        );
        let schema = Schema {
            name: if bad.is_empty() { "" } else { bad },
            version: 1,
        };
        assert_eq!(encode(schema, b"x"), Err(StateError::InvalidName));
    }
    let huge = vec![0_u8; MAX_BODY_BYTES + 1];
    assert_eq!(encode(SCHEMA, &huge), Err(StateError::TooLarge));
    assert_eq!(
        write_durable(&tmp.p().join("absent"), "f", SCHEMA, b"x").map_err(|x| x.code()),
        Err("state_io")
    );
    Ok(())
}

// ------------------------------------------------------------------ crash simulation

#[test]
fn crash_before_rename_leaves_old_file_readable_and_start_sweeps() -> R {
    let tmp = Tmp::new("crash")?;
    let root = tmp.p().join("state");
    {
        let (_lock, dir) = open(&tmp)?;
        dir.write(StateFile::Status, b"v1").map_err(e)?;
        // Staged but never published: the process "died" after fsync, before rename.
        let staged =
            stage(dir.root(), "status.json", StateFile::Status.schema(), b"v2").map_err(e)?;
        drop(staged);
        // A torn temp file: half an envelope.
        fs::write(root.join(".status.json.tmp-1-1"), b"memory-state/1 par").map_err(e)?;
        fs::write(dir.tmp_dir().join("backup.sqlite"), b"junk").map_err(e)?;
        fs::create_dir(dir.tmp_dir().join("sub")).map_err(e)?;
        fs::write(dir.tmp_dir().join("sub/inner"), b"junk").map_err(e)?;
        let Loaded::Present(stored) = dir.read(StateFile::Status) else {
            return Err("old file unreadable".into());
        };
        assert_eq!(stored.body, b"v1");
        let temps = fs::read_dir(&root)
            .map_err(e)?
            .flatten()
            .filter(|x| x.file_name().to_string_lossy().contains(".tmp-"))
            .count();
        assert!(temps >= 2);
    }
    let (_lock, dir) = open(&tmp)?;
    assert_eq!(fs::read_dir(dir.tmp_dir()).map_err(e)?.count(), 0);
    let temps = fs::read_dir(&root)
        .map_err(e)?
        .flatten()
        .filter(|x| x.file_name().to_string_lossy().contains(".tmp-"))
        .count();
    assert_eq!(temps, 0);
    let Loaded::Present(stored) = dir.read(StateFile::Status) else {
        return Err("old file lost".into());
    };
    assert_eq!(stored.body, b"v1");
    Ok(())
}

#[test]
fn publish_failures_are_closed_errors_and_clean_up() -> R {
    let tmp = Tmp::new("publish")?;
    let (_lock, dir) = open(&tmp)?;
    // Rename onto a non-empty directory fails; the temp file is removed.
    let dest = dir.root().join("status.json");
    fs::create_dir(&dest).map_err(e)?;
    fs::write(dest.join("x"), b"x").map_err(e)?;
    assert_eq!(
        dir.write(StateFile::Status, b"v").map_err(|x| x.code()),
        Err("state_io")
    );
    let temps = fs::read_dir(dir.root())
        .map_err(e)?
        .flatten()
        .filter(|x| x.file_name().to_string_lossy().contains(".tmp-"))
        .count();
    assert_eq!(temps, 0);
    // publish_new with the temp file vanished is an Io error, not "exists".
    let staged = stage(dir.root(), "fresh", SCHEMA, b"x").map_err(e)?;
    for entry in fs::read_dir(dir.root()).map_err(e)?.flatten() {
        if entry.file_name().to_string_lossy().contains(".fresh.tmp-") {
            fs::remove_file(entry.path()).map_err(e)?;
        }
    }
    assert_eq!(
        staged.publish_new(),
        Err(StateError::Io(std::io::ErrorKind::NotFound))
    );
    Ok(())
}

// ------------------------------------------------------------------ lock

#[test]
fn second_lock_is_held_in_process_and_on_a_second_description() -> R {
    let tmp = Tmp::new("lock")?;
    let root = tmp.p().join("state");
    let first = DaemonLock::acquire(&root).map_err(e)?;
    assert_eq!(first.root(), root);
    let Err(err) = DaemonLock::acquire(&root).map(|_| ()) else {
        return Err("second lock acquired".into());
    };
    assert_eq!(err, StateError::LockHeld);
    assert_eq!(err.exit_code(), memory::state::EXIT_LOCK_HELD);
    assert_eq!(err.code(), "lock_held");
    // flock belongs to the open file description: an independent open conflicts too, which is
    // what a second process would see.
    let other = fs::File::open(root.join("daemon.lock")).map_err(e)?;
    assert!(matches!(
        other.try_lock(),
        Err(std::fs::TryLockError::WouldBlock)
    ));
    drop(first);
    // Released on drop, as the kernel does on death.
    other.try_lock().map_err(e)?;
    drop(other);
    let again = DaemonLock::acquire(&root).map_err(e)?;
    drop(again);
    Ok(())
}

#[test]
fn lock_refusals() -> R {
    let tmp = Tmp::new("lockref")?;
    let real = tmp.p().join("real");
    fs::create_dir(&real).map_err(e)?;
    chmod(&real, 0o700)?;
    let link = tmp.p().join("link");
    symlink(&real, &link).map_err(e)?;
    assert_eq!(
        DaemonLock::acquire(&link).map(|_| ()),
        Err(StateError::Symlink)
    );
    let file = tmp.p().join("file");
    fs::write(&file, b"").map_err(e)?;
    assert_eq!(
        DaemonLock::acquire(&file).map(|_| ()),
        Err(StateError::NotDirectory)
    );
    let open_dir = tmp.p().join("open");
    fs::create_dir(&open_dir).map_err(e)?;
    chmod(&open_dir, 0o775)?;
    assert_eq!(
        DaemonLock::acquire(&open_dir).map(|_| ()),
        Err(StateError::Permissions)
    );
    // daemon.lock that cannot be opened for writing.
    let odd = tmp.p().join("odd");
    fs::create_dir_all(odd.join("daemon.lock")).map_err(e)?;
    chmod(&odd, 0o700)?;
    assert!(matches!(
        DaemonLock::acquire(&odd).map(|_| ()),
        Err(StateError::Io(_))
    ));
    // A parent that cannot be created.
    assert!(matches!(
        DaemonLock::acquire(&file.join("child")).map(|_| ()),
        Err(StateError::Io(_))
    ));
    Ok(())
}

// ------------------------------------------------------------------ hint dir and tmp

#[test]
fn hint_dir_refusals() -> R {
    let tmp = Tmp::new("hints")?;
    let (_lock, dir) = open(&tmp)?;
    // Missing: vet_dir creates nothing.
    assert_eq!(
        dir.vet_dir(&dir.hints_dir()),
        Err(StateError::Io(std::io::ErrorKind::NotFound))
    );
    let hints = dir.vet_hints_dir().map_err(e)?;
    assert_eq!(dir.vet_dir(&hints), Ok(()));
    // Group-writable, world-writable.
    for bad in [0o770, 0o707, 0o777, 0o720] {
        chmod(&hints, bad)?;
        assert_eq!(dir.vet_hints_dir(), Err(StateError::Permissions), "{bad:o}");
    }
    // Group-readable is not refused (only writable bits are, as in hook.rs).
    chmod(&hints, 0o750)?;
    assert_eq!(dir.vet_hints_dir(), Ok(hints.clone()));
    chmod(&hints, 0o700)?;
    // Symlink.
    fs::remove_dir(&hints).map_err(e)?;
    let elsewhere = tmp.p().join("elsewhere");
    fs::create_dir(&elsewhere).map_err(e)?;
    chmod(&elsewhere, 0o700)?;
    symlink(&elsewhere, &hints).map_err(e)?;
    assert_eq!(dir.vet_hints_dir(), Err(StateError::Symlink));
    fs::remove_file(&hints).map_err(e)?;
    // Not a directory.
    fs::write(&hints, b"").map_err(e)?;
    assert_eq!(dir.vet_hints_dir(), Err(StateError::NotDirectory));
    fs::remove_file(&hints).map_err(e)?;
    // Not owned by us: the filesystem root belongs to root, never to the test user.
    if dir.uid() != 0 {
        assert_eq!(dir.vet_dir(Path::new("/")), Err(StateError::NotOwned));
    }
    Ok(())
}

#[test]
fn prepare_refuses_bad_tmp() -> R {
    let tmp = Tmp::new("badtmp")?;
    let root = tmp.p().join("state");
    let lock = DaemonLock::acquire(&root).map_err(e)?;
    let t = root.join("tmp");
    fs::create_dir(&t).map_err(e)?;
    chmod(&t, 0o777)?;
    assert_eq!(
        StateDir::prepare(&lock).map(|_| ()),
        Err(StateError::Permissions)
    );
    fs::remove_dir(&t).map_err(e)?;
    let elsewhere = tmp.p().join("elsewhere");
    fs::create_dir(&elsewhere).map_err(e)?;
    symlink(&elsewhere, &t).map_err(e)?;
    assert_eq!(
        StateDir::prepare(&lock).map(|_| ()),
        Err(StateError::Symlink)
    );
    Ok(())
}

#[test]
fn state_root_rule_matches_the_hook() {
    use std::ffi::OsString;
    let some = |s: &str| Some(OsString::from(s));
    assert_eq!(root_from(some("/s"), some("/h")), Some(PathBuf::from("/s")));
    assert_eq!(
        root_from(None, some("/h")),
        Some(PathBuf::from("/h/.local/state/memory"))
    );
    assert_eq!(root_from(None, None), None);
    // Pure read of the environment; no directory is created.
    let _ = default_root();
}

#[test]
fn error_text_is_closed() {
    let all = [
        StateError::LockHeld,
        StateError::LockUnavailable(std::io::ErrorKind::Other),
        StateError::Io(std::io::ErrorKind::NotFound),
        StateError::Symlink,
        StateError::NotDirectory,
        StateError::Permissions,
        StateError::NotOwned,
        StateError::InvalidName,
        StateError::TooLarge,
        StateError::MachineIdCorrupt,
    ];
    let mut codes = std::collections::BTreeSet::new();
    for error in all {
        assert!(codes.insert(error.code()));
        assert!(error.to_string().starts_with(error.code()));
        assert!(!error.to_string().contains('/'));
        assert_eq!(
            error.exit_code() == memory::state::EXIT_LOCK_HELD,
            error == StateError::LockHeld
        );
    }
}

// ------------------------------------------------------------------ machine id

#[test]
fn machine_id_is_created_once_and_never_overwritten() -> R {
    let tmp = Tmp::new("mid")?;
    let (_lock, dir) = open(&tmp)?;
    let a = MachineId::try_from("bc-01".to_owned()).map_err(e)?;
    let b = MachineId::try_from("other".to_owned()).map_err(e)?;
    assert_eq!(dir.create_machine_id(&a), Ok(a.clone()));
    assert_eq!(dir.create_machine_id(&b), Ok(a.clone()));
    assert_eq!(dir.create_machine_id(&a), Ok(a.clone()));
    assert_eq!(mode(&dir.path(StateFile::MachineId))?, 0o600);
    // Damaged file: reported, never replaced.
    let path = dir.path(StateFile::MachineId);
    fs::write(&path, b"garbage").map_err(e)?;
    assert_eq!(dir.create_machine_id(&b), Err(StateError::MachineIdCorrupt));
    assert_eq!(fs::read(&path).map_err(e)?, b"garbage");
    // Valid envelope holding an invalid id.
    fs::write(
        &path,
        encode(StateFile::MachineId.schema(), b"a/b").map_err(e)?,
    )
    .map_err(e)?;
    assert_eq!(dir.create_machine_id(&b), Err(StateError::MachineIdCorrupt));
    fs::write(
        &path,
        encode(StateFile::MachineId.schema(), &[0xff]).map_err(e)?,
    )
    .map_err(e)?;
    assert_eq!(dir.create_machine_id(&b), Err(StateError::MachineIdCorrupt));
    Ok(())
}

#[test]
fn machine_id_race_has_one_winner() -> R {
    let tmp = Tmp::new("midrace")?;
    let (_lock, dir) = open(&tmp)?;
    let results: Vec<MachineId> = std::thread::scope(|s| {
        let handles: Vec<_> = (0..8)
            .map(|i| {
                let dir = dir.clone();
                s.spawn(move || {
                    let id = MachineId::try_from(format!("m{i}")).map_err(e)?;
                    dir.create_machine_id(&id).map_err(e)
                })
            })
            .collect();
        handles
            .into_iter()
            .filter_map(|h| h.join().ok().and_then(Result::ok))
            .collect()
    });
    assert_eq!(results.len(), 8);
    assert!(results.windows(2).all(|w| w.first() == w.get(1)));
    Ok(())
}

// ------------------------------------------------------------------ config

const VALID: &str = r#"
archive_id = "arch-1"
machine_id = "bc-01"
bucket = "memory-acceptance"
token_path = "/tokens/r2"
[[roots]]
agent = "claude"
path = "/home/u/.claude/projects"
[window]
quiet = 30
cap = 120
"#;

fn with_line(extra: &str) -> String {
    format!("{VALID}\n{extra}\n")
}

#[test]
fn config_accept_reject_table() -> R {
    let ok = Config::parse(VALID).map_err(e)?;
    assert_eq!(ok.archive_id.as_str(), "arch-1");
    assert_eq!(ok.machine_id.as_str(), "bc-01");
    assert_eq!(ok.roots.len(), 1);
    assert_eq!(ok.window.quiet.as_secs(), 30);
    assert_eq!(ok.window.cap.as_secs(), 120);
    // Minimal with defaults.
    let min =
        Config::parse("archive_id=\"a\"\nmachine_id=\"m\"\nbucket=\"abc\"\ntoken_path=\"/t\"\n")
            .map_err(e)?;
    assert_eq!(
        (min.window.quiet.as_secs(), min.window.cap.as_secs()),
        (60, 300)
    );
    assert!(min.roots.is_empty());
    let partial = Config::parse(&VALID.replace("quiet = 30\n", "")).map_err(e)?;
    assert_eq!(partial.window.quiet.as_secs(), 60);

    let rep = |from: &str, to: &str| VALID.replace(from, to);
    let table: Vec<(&str, String, ConfigError)> = vec![
        ("unknown field", with_line("extra = 1"), ConfigError::Parse),
        (
            "unknown root field",
            rep("path = \"/home", "color = 1\npath = \"/home"),
            ConfigError::Parse,
        ),
        (
            "unknown window field",
            rep("cap = 120", "cap = 120\nslow = 1"),
            ConfigError::Parse,
        ),
        (
            "missing bucket",
            rep("bucket = \"memory-acceptance\"", ""),
            ConfigError::Parse,
        ),
        (
            "wrong type",
            rep("quiet = 30", "quiet = \"x\""),
            ConfigError::Parse,
        ),
        ("unknown agent", rep("claude", "gemini"), ConfigError::Parse),
        ("not toml", "][".to_owned(), ConfigError::Parse),
        (
            "bad archive id",
            rep("arch-1", "a/b"),
            ConfigError::ArchiveId,
        ),
        (
            "empty archive id",
            rep("arch-1", ""),
            ConfigError::ArchiveId,
        ),
        ("bad machine id", rep("bc-01", ".."), ConfigError::MachineId),
        (
            "upper bucket",
            rep("memory-acceptance", "Memory"),
            ConfigError::Bucket,
        ),
        (
            "short bucket",
            rep("memory-acceptance", "ab"),
            ConfigError::Bucket,
        ),
        (
            "dash bucket",
            rep("memory-acceptance", "-abc"),
            ConfigError::Bucket,
        ),
        (
            "trailing dash bucket",
            rep("memory-acceptance", "abc-"),
            ConfigError::Bucket,
        ),
        (
            "underscore bucket",
            rep("memory-acceptance", "ab_c"),
            ConfigError::Bucket,
        ),
        (
            "long bucket",
            rep("memory-acceptance", &"a".repeat(64)),
            ConfigError::Bucket,
        ),
        (
            "zero quiet",
            rep("quiet = 30", "quiet = 0"),
            ConfigError::Window,
        ),
        (
            "inverted window",
            rep("cap = 120", "cap = 10"),
            ConfigError::Window,
        ),
        (
            "huge cap",
            rep("cap = 120", "cap = 86401"),
            ConfigError::Window,
        ),
        (
            "relative root",
            rep("/home/u/.claude/projects", "rel/dir"),
            ConfigError::RootNotAbsolute,
        ),
        (
            "relative token",
            rep("/tokens/r2", "tokens/r2"),
            ConfigError::TokenPathNotAbsolute,
        ),
    ];
    for (label, text, want) in table {
        assert_eq!(Config::parse(&text), Err(want), "{label}");
    }
    Ok(())
}

#[test]
fn config_errors_never_echo_values() {
    let secret = "SECRETVALUE";
    let texts = [
        VALID.replace("arch-1", &format!("{secret}/x")),
        VALID.replace("bc-01", &format!("{secret}/x")),
        VALID.replace("memory-acceptance", &format!("{secret}_X")),
        VALID.replace("/tokens/r2", secret),
        VALID.replace("/home/u/.claude/projects", secret),
        with_line(&format!("{secret} = 1")),
        with_line(&format!("archive_id = \"{secret}\"")),
    ];
    for text in texts {
        let parsed = Config::parse(&text);
        assert!(parsed.is_err(), "accepted");
        if let Err(error) = parsed {
            assert!(!error.to_string().contains(secret));
            assert!(!format!("{error:?}").contains(secret));
        }
    }
}

#[test]
fn config_load_and_token_checks() -> R {
    let tmp = Tmp::new("cfg")?;
    let token = tmp.p().join("token");
    fs::write(&token, b"s3cret-token").map_err(e)?;
    let text = VALID.replace("/tokens/r2", &token.to_string_lossy());
    let cfg = tmp.p().join("config.toml");
    fs::write(&cfg, &text).map_err(e)?;

    for (m, ok) in [
        (0o600, true),
        (0o400, true),
        (0o640, false),
        (0o604, false),
        (0o660, false),
        (0o644, false),
    ] {
        chmod(&token, m)?;
        let got = Config::load(&cfg);
        if ok {
            assert!(got.is_ok(), "{m:o}");
        } else {
            assert_eq!(got.map(|_| ()), Err(ConfigError::TokenPermissions), "{m:o}");
        }
    }
    // Contents are never read: an unreadable (mode 0) file is group/world-clean, so check_token
    // passes on metadata alone.
    chmod(&token, 0)?;
    assert!(Config::load(&cfg).is_ok());
    chmod(&token, 0o600)?;

    fs::remove_file(&token).map_err(e)?;
    assert!(matches!(
        Config::load(&cfg),
        Err(ConfigError::TokenUnavailable(std::io::ErrorKind::NotFound))
    ));
    fs::create_dir(&token).map_err(e)?;
    assert_eq!(
        Config::load(&cfg).map(|_| ()),
        Err(ConfigError::TokenNotFile)
    );

    assert!(matches!(
        Config::load(&tmp.p().join("absent.toml")),
        Err(ConfigError::Read(std::io::ErrorKind::NotFound))
    ));
    fs::write(&cfg, [0xff, 0xfe]).map_err(e)?;
    assert!(matches!(Config::load(&cfg), Err(ConfigError::Read(_))));
    fs::write(&cfg, "bogus = 1").map_err(e)?;
    assert_eq!(Config::load(&cfg).map(|_| ()), Err(ConfigError::Parse));

    let all = [
        ConfigError::Read(std::io::ErrorKind::NotFound),
        ConfigError::Parse,
        ConfigError::ArchiveId,
        ConfigError::MachineId,
        ConfigError::Bucket,
        ConfigError::Window,
        ConfigError::RootNotAbsolute,
        ConfigError::TokenPathNotAbsolute,
        ConfigError::TokenUnavailable(std::io::ErrorKind::NotFound),
        ConfigError::TokenNotFile,
        ConfigError::TokenPermissions,
    ];
    let codes: std::collections::BTreeSet<_> = all.iter().map(|x| x.code()).collect();
    assert_eq!(codes.len(), all.len());
    for error in all {
        assert!(error.to_string().starts_with(error.code()));
    }
    Ok(())
}

// ------------------------------------------------------------------ properties

fn flip(bytes: &[u8], index: usize, delta: u8) -> Vec<u8> {
    let mut out = bytes.to_vec();
    let at = index % out.len().max(1);
    if let Some(b) = out.get_mut(at) {
        *b ^= (delta % 255) + 1;
    }
    out
}

fn identity(body: &[u8]) -> R {
    let tmp = Tmp::new("prop")?;
    write_durable(tmp.p(), "f.json", SCHEMA, body).map_err(e)?;
    let Loaded::Present(stored) = read_durable(&tmp.p().join("f.json"), SCHEMA) else {
        return Err("missing".into());
    };
    if stored.body != body {
        return Err("body differs".into());
    }
    Ok(())
}

fn mutation(body: &[u8], index: usize, delta: u8) -> R {
    let stored = encode(SCHEMA, body).map_err(e)?;
    let mutated = flip(&stored, index, delta);
    if mutated == stored {
        return Err("mutation was a no-op".into());
    }
    match decode(&mutated, SCHEMA) {
        Loaded::Missing(_) => Ok(()),
        Loaded::Present(got) if got.body == body => Ok(()),
        Loaded::Present(_) => Err("a mutation yielded a different Ok body".into()),
    }
}

#[hegel::test]
fn hegel_write_read_is_identity(tc: TestCase) {
    let body: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
    let r = identity(&body);
    assert!(r.is_ok(), "{r:?}");
}

#[hegel::test]
fn hegel_single_byte_mutation_never_yields_a_different_body(tc: TestCase) {
    let body: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
    let index = usize::from(tc.draw(gs::integers::<u16>()));
    let delta = tc.draw(gs::integers::<u8>());
    let r = mutation(&body, index, delta);
    assert!(r.is_ok(), "{r:?}");
}

proptest! {
    #[test]
    fn proptest_write_read_is_identity(body in proptest::collection::vec(any::<u8>(), 0..4096)) {
        let r = identity(&body);
        prop_assert!(r.is_ok(), "{r:?}");
    }

    #[test]
    fn proptest_single_byte_mutation_never_yields_a_different_body(
        body in proptest::collection::vec(any::<u8>(), 0..4096),
        index in any::<usize>(),
        delta in any::<u8>(),
    ) {
        let r = mutation(&body, index, delta);
        prop_assert!(r.is_ok(), "{r:?}");
    }
}
