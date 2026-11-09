//! Diagnostic checks of counter/DST seams, not daemon footprint acceptance.
use memory::environment::{Clock, Counters, FileSystem, Stat, Store, StoreError, Stored};
use memory_core::domain::{Birth, ETag, FileId};
use std::collections::BTreeMap;

#[derive(Default)]
struct Fake {
    counters: Counters,
    now: u64,
    objects: BTreeMap<String, Vec<u8>>,
    lose_reply: bool,
}
impl Clock for Fake {
    fn monotonic_ns(&self) -> u64 {
        self.now
    }
    fn wall_ns(&self) -> i128 {
        i128::from(self.now)
    }
}
impl FileSystem for Fake {
    type Error = ();
    fn stat(&mut self, source: u64) -> Result<Option<Stat>, ()> {
        self.counters.stats += 1;
        Ok(Some(Stat {
            id: FileId {
                dev: 0,
                ino: source,
                birth: Birth::Unavailable,
            },
            size: 30_000,
            mtime_ns: 0,
        }))
    }
    fn stat_len(&mut self, _source: u64) -> Result<u64, ()> {
        self.counters.stats += 1;
        Ok(30_000)
    }
    fn read(&mut self, _source: u64, range: std::ops::Range<u64>) -> Result<Vec<u8>, ()> {
        let len = range.end.checked_sub(range.start).ok_or(())?;
        self.counters.bytes_read += len;
        Ok(vec![0; usize::try_from(len).map_err(|_| ())?])
    }
}
impl Store for Fake {
    type Body = Vec<u8>;
    fn document(bytes: Vec<u8>) -> Vec<u8> {
        bytes
    }
    fn put_if_absent(&mut self, key: &str, body: Vec<u8>) -> Result<ETag, StoreError> {
        self.counters.puts += 1;
        let landed = !self.objects.contains_key(key);
        if landed {
            self.objects.insert(key.to_owned(), body);
        }
        if self.lose_reply {
            Err(StoreError::Ambiguous)
        } else if landed {
            ETag::try_from("\"e\"".to_owned()).map_err(|_| StoreError::Server)
        } else {
            Err(StoreError::PreconditionFailed)
        }
    }
    fn put_if_match(&mut self, _: &str, _: &ETag, _: Vec<u8>) -> Result<ETag, StoreError> {
        Err(StoreError::Server)
    }
    fn get(&mut self, key: &str) -> Result<Stored<Vec<u8>>, StoreError> {
        let body = self.objects.get(key).ok_or(StoreError::NotFound)?.clone();
        let etag = ETag::try_from("\"e\"".to_owned()).map_err(|_| StoreError::Server)?;
        Ok(Stored { body, etag })
    }
}

#[test]
fn counters_detect_each_wrapped_operation() -> Result<(), ()> {
    let mut fake = Fake::default();
    for source in 0..30_000 {
        fake.stat_len(source)?;
    }
    assert_eq!(fake.counters.stats, 30_000);
    fake.read(0, 0..59_000)?;
    fake.read(0, 0..(68 * 1024))?;
    assert_eq!(fake.counters.bytes_read, 59_000 + 68 * 1024);
    fake.now += 60_000_000_000;
    fake.counters.wakeups += 1;
    assert_eq!(fake.monotonic_ns(), 60_000_000_000);
    assert_eq!(fake.wall_ns(), 60_000_000_000);
    assert_eq!(fake.counters.wakeups, 1);
    Ok(())
}

#[test]
fn lost_reply_is_visible_and_duplicate_create_preserves_first_body() {
    let mut fake = Fake {
        lose_reply: true,
        ..Fake::default()
    };
    assert_eq!(
        fake.put_if_absent("k", vec![1, 2]),
        Err(StoreError::Ambiguous)
    );
    assert_eq!(fake.objects.get("k"), Some(&vec![1, 2]));
    fake.lose_reply = false;
    assert_eq!(
        fake.put_if_absent("k", vec![9]),
        Err(StoreError::PreconditionFailed)
    );
    assert_eq!(fake.objects.get("k"), Some(&vec![1, 2]));
    assert_eq!(fake.counters.puts, 2);
}

#[test]
fn scaffold_binary_refuses_capture_instead_of_success() -> std::io::Result<()> {
    use std::os::unix::fs::PermissionsExt;
    // Isolated state root: a test must never resolve or create the host's real state dir.
    let base = std::env::temp_dir().join(format!("memory-harness-{}", std::process::id()));
    let root = base.join("state");
    std::fs::create_dir_all(&root)?;
    std::fs::set_permissions(&base, std::fs::Permissions::from_mode(0o700))?;
    std::fs::set_permissions(&root, std::fs::Permissions::from_mode(0o700))?;
    let token = base.join("token");
    std::fs::write(&token, "x")?;
    std::fs::set_permissions(&token, std::fs::Permissions::from_mode(0o600))?;
    std::fs::write(
        root.join("config.toml"),
        format!(
            "archive_id = \"a\"\nmachine_id = \"m\"\nbucket = \"bucket-1\"\ntoken_path = \"{}\"\n",
            token.display()
        ),
    )?;
    let mut cmd = std::process::Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.arg("daemon").env_clear().env("MEMORY_STATE_DIR", &root);
    if let Some(profile) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", profile);
    }
    let output = cmd.output();
    let _ = std::fs::remove_dir_all(&base);
    let output = output?;
    // Startup succeeds, then the binary refuses: no adapters, never a simulated capture.
    assert_eq!(output.status.code(), Some(78));
    assert!(output.stdout.is_empty());
    assert!(output.stderr.is_empty());
    Ok(())
}
