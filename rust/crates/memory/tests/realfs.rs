//! realfs acceptance: stat, FileId, exact-range reads, bounded deterministic walk.
use hegel::{TestCase, generators as gs};
use memory::environment::FileSystem;
use memory::realfs::{RealFs, RealFsError, WalkLimits, walk};
use memory_core::domain::{Agent, ArchiveId, MachineId, SourceKind};
use memory_core::locator::{ConfigAsset, Located, Role, RootLayout, RootNamespace, Roots};
use proptest::prelude::*;
use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

type TestResult = Result<(), Box<dyn std::error::Error>>;

macro_rules! ensure {
    ($c:expr, $($a:tt)*) => {
        if !$c {
            return Err(format!($($a)*).into());
        }
    };
}

static NEXT: AtomicU64 = AtomicU64::new(0);

struct Tmp(PathBuf);

impl Tmp {
    fn new() -> Result<Self, std::io::Error> {
        let p = std::env::temp_dir().join(format!(
            "realfs-test-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::SeqCst)
        ));
        fs::create_dir_all(&p)?;
        Ok(Self(p))
    }
    fn path(&self) -> &Path {
        &self.0
    }
    fn write(&self, rel: &str, body: &[u8]) -> Result<PathBuf, std::io::Error> {
        let p = self.0.join(rel);
        if let Some(parent) = p.parent() {
            fs::create_dir_all(parent)?;
        }
        fs::write(&p, body)?;
        Ok(p)
    }
}

impl Drop for Tmp {
    fn drop(&mut self) {
        // Restore permissions a test may have removed, then delete.
        fn fix(p: &Path) {
            if let Ok(rd) = fs::read_dir(p) {
                for e in rd.flatten() {
                    if e.file_type().is_ok_and(|t| t.is_dir()) {
                        fix(&e.path());
                    }
                }
            } else {
                let _ = fs::set_permissions(p, std::os::unix::fs::PermissionsExt::from_mode(0o755));
                if let Ok(rd) = fs::read_dir(p) {
                    for e in rd.flatten() {
                        fix(&e.path());
                    }
                }
            }
        }
        fix(&self.0);
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn roots(agent: Agent, layout: RootLayout) -> Result<Roots, Box<dyn std::error::Error>> {
    Ok(Roots::new(
        ArchiveId::try_from("a".to_owned())?,
        MachineId::try_from("m".to_owned())?,
        agent,
        RootNamespace::new("r1")?,
        layout,
    ))
}

#[test]
fn stat_absent_present_and_symlink() -> TestResult {
    let t = Tmp::new()?;
    let f = t.write("a.txt", b"hello")?;
    let mut rfs = RealFs::new();
    let absent = rfs.register(t.path().join("nope"));
    let present = rfs.register(&f);
    let link_path = t.path().join("link");
    std::os::unix::fs::symlink(&f, &link_path)?;
    let link = rfs.register(&link_path);
    ensure!(rfs.stat(absent)?.is_none(), "absent must be Ok(None)");
    ensure!(
        matches!(rfs.stat_len(absent), Err(RealFsError::Absent)),
        "stat_len absent"
    );
    let s = rfs.stat(present)?.ok_or("present missing")?;
    ensure!(s.size == 5, "size {}", s.size);
    ensure!(s.mtime_ns > 0, "mtime");
    ensure!(rfs.stat_len(present)? == 5, "stat_len");
    let l = rfs.stat(link)?.ok_or("link missing")?;
    ensure!(
        l.id != s.id,
        "symlink must report its own identity, not the target's"
    );
    ensure!(l.size != 5, "symlink size is the link text length");
    ensure!(
        matches!(rfs.stat(99), Err(RealFsError::UnknownHandle)),
        "unknown handle"
    );
    ensure!(
        matches!(rfs.stat_len(99), Err(RealFsError::UnknownHandle)),
        "unknown handle len"
    );
    // A path through a regular file is not NotFound-by-absence: ENOTDIR is an error.
    let bad = rfs.register(f.join("child"));
    ensure!(
        matches!(rfs.stat(bad), Err(RealFsError::Io(_))),
        "non-NotFound is Err"
    );
    ensure!(rfs.counters().stats == 8, "stats {}", rfs.counters().stats);
    Ok(())
}

#[test]
fn file_id_changes_on_replace_and_stays_on_append() -> TestResult {
    let t = Tmp::new()?;
    let f = t.write("s.jsonl", b"one\n")?;
    let mut rfs = RealFs::new();
    let h = rfs.register(&f);
    let before = rfs.stat(h)?.ok_or("missing")?;
    fs::OpenOptions::new()
        .append(true)
        .open(&f)?
        .write_all(b"two\n")?;
    let appended = rfs.stat(h)?.ok_or("missing")?;
    ensure!(appended.id == before.id, "append must keep FileId");
    ensure!(appended.size == 8, "size after append");
    let tmp = t.write("s.new", b"replacement")?;
    fs::rename(&tmp, &f)?;
    let replaced = rfs.stat(h)?.ok_or("missing")?;
    ensure!(
        replaced.id != before.id,
        "replace-by-rename must change FileId"
    );
    Ok(())
}

#[test]
fn read_exact_short_and_past_eof() -> TestResult {
    let t = Tmp::new()?;
    let f = t.write("r.bin", b"0123456789")?;
    let mut rfs = RealFs::new();
    let h = rfs.register(&f);
    ensure!(rfs.read(h, 2..6)? == b"2345", "exact range");
    ensure!(rfs.read(h, 3..3)?.is_empty(), "empty range");
    ensure!(rfs.read(h, 0..10)? == b"0123456789", "whole");
    ensure!(rfs.read(h, 8..100)? == b"89", "clipped at EOF");
    ensure!(rfs.read(h, 50..60)?.is_empty(), "past EOF is empty");
    ensure!(
        matches!(
            rfs.read(h, std::ops::Range { start: 5, end: 2 }),
            Err(RealFsError::InvalidRange)
        ),
        "reversed"
    );
    ensure!(
        matches!(rfs.read(7, 0..1), Err(RealFsError::UnknownHandle)),
        "handle"
    );
    let gone = rfs.register(t.path().join("gone"));
    ensure!(
        matches!(
            rfs.read(gone, 0..1),
            Err(RealFsError::Io(std::io::ErrorKind::NotFound))
        ),
        "missing file read"
    );
    let dir = rfs.register(t.path());
    let got = rfs.read(dir, 0..1);
    ensure!(
        matches!(got, Err(RealFsError::Io(_))),
        "directory read is an io error: {got:?}"
    );
    fs::OpenOptions::new().write(true).open(&f)?.set_len(4)?;
    ensure!(rfs.read(h, 2..8)? == b"23", "short read after truncation");
    // Failed reads above (missing, directory, handle, range) are not counted.
    let c = rfs.counters();
    ensure!(c.reads == 6 && c.bytes_read == 18, "counters {c:?}");
    // An unreadable file reports its io kind (root bypasses mode bits and reads it).
    {
        use std::io::ErrorKind;
        use std::os::unix::fs::PermissionsExt;
        let p = t.write("secret.txt", b"abc")?;
        fs::set_permissions(&p, fs::Permissions::from_mode(0o000))?;
        let locked = rfs.register(&p);
        let got = rfs.read(locked, 0..3);
        fs::set_permissions(&p, fs::Permissions::from_mode(0o644))?;
        ensure!(
            got == Err(RealFsError::Io(ErrorKind::PermissionDenied)) || got == Ok(b"abc".to_vec()),
            "{got:?}"
        );
    }
    Ok(())
}

#[test]
fn error_display_is_closed() {
    for (e, s) in [
        (RealFsError::UnknownHandle, "unknown handle"),
        (RealFsError::InvalidRange, "invalid range"),
        (RealFsError::Absent, "source absent"),
    ] {
        assert_eq!(e.to_string(), s);
    }
    assert!(
        RealFsError::Io(std::io::ErrorKind::NotFound)
            .to_string()
            .starts_with("io error")
    );
}

fn at(w: &memory::realfs::Walk, i: usize) -> Result<&Located, Box<dyn std::error::Error>> {
    Ok(w.files.get(i).map(|f| &f.2).ok_or("row missing")?)
}

fn is_source(l: &Located, kind: SourceKind, role: Role) -> bool {
    matches!(l, Located::Source { kind: k, role: r, .. } if *k == kind && *r == role)
}

#[test]
fn walk_synthetic_layouts() -> TestResult {
    let t = Tmp::new()?;
    t.write("claude/projects/-p/abc.jsonl", b"{}\n")?;
    t.write("claude/settings.json", b"{}")?;
    t.write("claude/unknown.txt", b"x")?;
    t.write("codex/config.toml", b"a=1")?;
    t.write("pi/models.json", b"{}")?;
    t.write("pi/bin/x", b"x")?;
    let rs = vec![
        (
            t.path().join("claude"),
            roots(Agent::Claude, RootLayout::AgentDir)?,
        ),
        (
            t.path().join("codex"),
            roots(Agent::Codex, RootLayout::AgentDir)?,
        ),
        (t.path().join("pi"), roots(Agent::Pi, RootLayout::AgentDir)?),
    ];
    let w = walk(&rs, &WalkLimits::default());
    let rel: Vec<String> = w.files.iter().map(|f| f.1.components().join("/")).collect();
    ensure!(
        rel == [
            "projects/-p/abc.jsonl",
            "settings.json",
            "unknown.txt",
            "config.toml",
            "bin/x",
            "models.json"
        ],
        "order {rel:?}"
    );
    ensure!(
        is_source(at(&w, 0)?, SourceKind::ClaudeTranscript, Role::Main),
        "claude transcript {:?}",
        at(&w, 0)?
    );
    ensure!(
        is_source(
            at(&w, 1)?,
            SourceKind::ClaudeConfig,
            Role::Asset(ConfigAsset::Settings)
        ),
        "settings {:?}",
        at(&w, 1)?
    );
    ensure!(*at(&w, 2)? == Located::UnknownKind, "unknown.txt");
    ensure!(matches!(at(&w, 3)?, Located::Source { .. }), "codex config");
    ensure!(*at(&w, 4)? == Located::UnknownKind, "bin/x");
    ensure!(matches!(at(&w, 5)?, Located::Source { .. }), "pi models");
    ensure!(
        w.files.first().ok_or("row")?.0 == t.path().join("claude/projects/-p/abc.jsonl"),
        "abs path"
    );
    ensure!(w.entries == 9, "entries {}", w.entries);
    ensure!(
        w.unreadable == 0 && w.symlinks == 0 && !w.truncated,
        "clean walk {w:?}"
    );
    let again = walk(&rs, &WalkLimits::default());
    ensure!(again == w, "deterministic");
    Ok(())
}

#[test]
fn walk_does_not_follow_symlinks() -> TestResult {
    let t = Tmp::new()?;
    t.write("real/dir/settings.json", b"{}")?;
    t.write("root/settings.json", b"{}")?;
    std::os::unix::fs::symlink(t.path().join("real/dir"), t.path().join("root/linked"))?;
    std::os::unix::fs::symlink(
        t.path().join("real/dir/settings.json"),
        t.path().join("root/flink"),
    )?;
    let rs = vec![(
        t.path().join("root"),
        roots(Agent::Claude, RootLayout::AgentDir)?,
    )];
    let w = walk(&rs, &WalkLimits::default());
    ensure!(
        w.files.len() == 1,
        "only the regular file: {}",
        w.files.len()
    );
    ensure!(w.symlinks == 2, "symlinks {}", w.symlinks);
    Ok(())
}

#[test]
fn walk_depth_and_entry_bounds() -> TestResult {
    let t = Tmp::new()?;
    t.write("r/a/b/c/deep.txt", b"x")?;
    t.write("r/a/top.txt", b"x")?;
    let rs = vec![(
        t.path().join("r"),
        roots(Agent::Claude, RootLayout::AgentDir)?,
    )];
    let w = walk(
        &rs,
        &WalkLimits {
            max_depth: 2,
            max_entries: 100,
        },
    );
    ensure!(w.too_deep == 1, "too_deep {}", w.too_deep);
    ensure!(w.files.len() == 1, "files {}", w.files.len());
    let full = walk(
        &rs,
        &WalkLimits {
            max_depth: 3,
            max_entries: 100,
        },
    );
    ensure!(
        full.too_deep == 0 && full.files.len() == 2,
        "depth 3 reaches c"
    );
    let cut = walk(
        &rs,
        &WalkLimits {
            max_depth: 32,
            max_entries: 2,
        },
    );
    ensure!(
        cut.truncated && cut.entries == 2,
        "entries {} {}",
        cut.entries,
        cut.truncated
    );
    let d = WalkLimits::default();
    ensure!(
        d.max_depth == 32 && d.max_entries == 500_000,
        "documented defaults"
    );
    // The entry bound can stop partway through one directory, keeping the first sorted entry.
    let t = Tmp::new()?;
    t.write("r/a.txt", b"x")?;
    t.write("r/b.txt", b"x")?;
    t.write("r/c.txt", b"x")?;
    let rs = vec![(
        t.path().join("r"),
        roots(Agent::Claude, RootLayout::AgentDir)?,
    )];
    let w = walk(
        &rs,
        &WalkLimits {
            max_depth: 32,
            max_entries: 1,
        },
    );
    ensure!(w.truncated && w.entries == 1 && w.files.len() == 1, "{w:?}");
    ensure!(
        w.files
            .first()
            .is_some_and(|f| f.1.components().join("/") == "a.txt"),
        "first sorted entry kept: {w:?}"
    );
    let exact = walk(
        &rs,
        &WalkLimits {
            max_depth: 32,
            max_entries: 3,
        },
    );
    ensure!(!exact.truncated && exact.files.len() == 3, "{exact:?}");
    Ok(())
}

#[test]
fn walk_counts_unreadable_dirs_and_missing_root() -> TestResult {
    use std::os::unix::fs::PermissionsExt;
    let t = Tmp::new()?;
    t.write("r/ok/settings.json", b"{}")?;
    t.write("r/locked/x.txt", b"x")?;
    fs::set_permissions(t.path().join("r/locked"), fs::Permissions::from_mode(0o000))?;
    let rs = vec![
        (
            t.path().join("r"),
            roots(Agent::Claude, RootLayout::AgentDir)?,
        ),
        (
            t.path().join("missing"),
            roots(Agent::Claude, RootLayout::AgentDir)?,
        ),
    ];
    let w = walk(&rs, &WalkLimits::default());
    fs::set_permissions(t.path().join("r/locked"), fs::Permissions::from_mode(0o755))?;
    ensure!(w.files.len() == 1, "readable file still found");
    ensure!(
        w.unreadable == 2,
        "locked dir and missing root counted: {}",
        w.unreadable
    );
    Ok(())
}

#[test]
fn walk_counts_non_utf8_names_and_non_regular_files() -> TestResult {
    use std::os::unix::ffi::OsStrExt;
    let t = Tmp::new()?;
    fs::create_dir_all(t.path().join("r"))?;
    let bad = t
        .path()
        .join("r")
        .join(std::ffi::OsStr::from_bytes(b"bad\xff.txt"));
    // APFS rejects non-UTF-8 names; the branch is then exercised only on Linux.
    let bad_made = fs::write(bad, b"x").is_ok();
    t.write("r/ok.txt", b"x")?;
    let rs = vec![(
        t.path().join("r"),
        roots(Agent::Claude, RootLayout::AgentDir)?,
    )];
    let w = walk(&rs, &WalkLimits::default());
    ensure!(
        w.invalid_names == u64::from(bad_made) && w.files.len() == 1,
        "{w:?}"
    );
    Ok(())
}

fn check_read(bytes: &[u8], a: u64, b: u64) -> TestResult {
    let t = Tmp::new()?;
    let f = t.write("p.bin", bytes)?;
    let mut rfs = RealFs::new();
    let h = rfs.register(&f);
    let (s, e) = (a.min(b), a.max(b));
    let got = rfs.read(h, s..e)?;
    let len = bytes.len() as u64;
    let lo = usize::try_from(s.min(len))?;
    let hi = usize::try_from(e.min(len))?;
    ensure!(
        got == bytes.get(lo..hi).ok_or("slice")?,
        "range {s}..{e} len {len}"
    );
    Ok(())
}

#[test]
fn hegel_read_equals_range_intersection() {
    hegel::Hegel::new(|tc: TestCase| {
        let bytes: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
        let a = u64::from(tc.draw(gs::integers::<u16>()));
        let b = u64::from(tc.draw(gs::integers::<u16>()));
        let r = check_read(&bytes, a, b);
        assert!(r.is_ok(), "{:?}", r.err());
    })
    .run();
}

proptest! {
    #[test]
    fn proptest_read_equals_range_intersection(
        bytes in proptest::collection::vec(any::<u8>(), 0..512),
        a in 0u64..1024,
        b in 0u64..1024,
    ) {
        let r = check_read(&bytes, a, b);
        prop_assert!(r.is_ok(), "{:?}", r.err());
    }
}

// ---- Coverage round

#[test]
fn walk_skips_sockets_and_fifos_without_counting_them_as_files() -> TestResult {
    let t = Tmp::new()?;
    fs::create_dir_all(t.path().join("r"))?;
    t.write("r/ok.txt", b"x")?;
    let sock = t.path().join("r/s.sock");
    let listener = std::os::unix::net::UnixListener::bind(&sock)?;
    let rs = vec![(
        t.path().join("r"),
        roots(Agent::Claude, RootLayout::AgentDir)?,
    )];
    let w = walk(&rs, &WalkLimits::default());
    drop(listener);
    ensure!(
        w.files.len() == 1 && w.entries == 2 && w.symlinks == 0 && w.invalid_names == 0,
        "{w:?}"
    );
    Ok(())
}
