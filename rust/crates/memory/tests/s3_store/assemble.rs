//! R1: a daemon started from `config.toml` and a token file alone, against the local server.
use super::fake::Fake;
use super::{BUCKET, KEY_ID, R, SECRET, e, store_at};
use memory::assemble::start_from_config;
use memory::config::Config;
use memory::daemon::Sources;
use memory::environment::{Store, StoreError};
use memory::realfs::WalkLimits;
use memory::resolve::{SharedFs, WalkSources};
use memory::roots::locator_roots;
use memory::s3::TimeoutPolicy;
use memory_reader::{read_owner, restore_source};
use std::fs;
use std::io::Write;
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;

const PREFIX: &str = "rust-acceptance/r1-test/";
const MARKER: &str = "R1-MARKER-51c2";
const SESSION: &str = "projects/-p/0f0e0d0c-0b0a-4908-8706-050403020100.jsonl";
const TOKEN_IN_ROOT: &str = "projects/-p/1f0e0d0c-0b0a-4908-8706-050403020101.jsonl";

struct Rig {
    dir: PathBuf,
    root: PathBuf,
    state: PathBuf,
    cfg: PathBuf,
    token: PathBuf,
}

impl Rig {
    fn new(label: &str, url: &str, prefix: &str, token_in_root: bool) -> R<Self> {
        let dir = std::env::temp_dir().join(format!("r1-assemble-{}-{label}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).map_err(e)?;
        fs::set_permissions(&dir, fs::Permissions::from_mode(0o700)).map_err(e)?;
        let root = dir.join("claude");
        let token = if token_in_root {
            root.join(TOKEN_IN_ROOT)
        } else {
            dir.join("token")
        };
        fs::create_dir_all(token.parent().ok_or("parent")?).map_err(e)?;
        let rig = Self {
            state: dir.join("state"),
            cfg: dir.join("config.toml"),
            dir,
            root,
            token,
        };
        rig.write_token(&good_token(), 0o600)?;
        rig.write_config(url, prefix)?;
        Ok(rig)
    }
    fn write_token(&self, body: &str, mode: u32) -> R {
        fs::write(&self.token, body).map_err(e)?;
        fs::set_permissions(&self.token, fs::Permissions::from_mode(mode)).map_err(e)
    }
    fn write_config(&self, url: &str, prefix: &str) -> R {
        let text = format!(
            "archive_id = \"a\"\nmachine_id = \"m\"\nendpoint = \"{url}\"\nbucket = \"{BUCKET}\"\nprefix = \"{prefix}\"\ntoken_path = \"{}\"\n[[roots]]\nagent = \"claude\"\npath = \"{}\"\n",
            self.token.display(),
            self.root.display()
        );
        fs::write(&self.cfg, text).map_err(e)
    }
}
impl Drop for Rig {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.dir);
    }
}

fn good_token() -> String {
    format!("access_key_id = \"{KEY_ID}\"\nsecret_access_key = \"{SECRET}\"\n")
}

fn lines(range: std::ops::Range<u32>) -> String {
    range
        .map(|i| {
            format!(
                "{{\"role\":\"user\",\"n\":{i},\"text\":\"{MARKER} line {i} {}\"}}\n",
                "filler ".repeat(40)
            )
        })
        .collect()
}

fn code_of<T>(result: Result<T, memory::assemble::AssembleError>) -> R<(String, u8)> {
    match result {
        Err(error) => Ok((error.code().to_owned(), error.exit_code())),
        Ok(_) => Err("start unexpectedly succeeded".to_owned()),
    }
}

#[test]
fn daemon_from_config_alone_round_trips_through_the_server() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("e2e", &server.url(), PREFIX, false)?;
    let file = rig.root.join(SESSION);
    fs::create_dir_all(file.parent().ok_or("parent")?).map_err(e)?;
    fs::write(&file, lines(0..12)).map_err(e)?;

    let mut daemon = start_from_config(&rig.state, &rig.cfg).map_err(e)?;
    let landed = |daemon: &mut memory::assemble::ProductionDaemon| -> R {
        let report = daemon.reconcile_once().map_err(e)?;
        assert_eq!((report.landed, report.failed, report.parked), (1, 0, 0));
        Ok(())
    };
    landed(&mut daemon)?;
    fs::OpenOptions::new()
        .append(true)
        .open(&file)
        .map_err(e)?
        .write_all(lines(12..20).as_bytes())
        .map_err(e)?;
    landed(&mut daemon)?;
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
    let mut store = store_at(&server.url(), PREFIX, TimeoutPolicy::PRODUCTION)?;
    let folded = read_owner(&owner, |key| match store.get(key) {
        Ok(stored) => Ok(Some(stored.body)),
        Err(StoreError::NotFound) => Ok(None),
        Err(other) => Err(other),
    })
    .map_err(e)?
    .ok_or("owner absent")?;
    let entry = folded.state.sources.values().next().ok_or("no source")?;
    let restored = restore_source(entry, |key| store.get(key).map(|s| s.body)).map_err(e)?;
    assert_eq!(restored, fs::read(&file).map_err(e)?);
    assert!(String::from_utf8(restored).map_err(e)?.contains(MARKER));

    let seen = server.requests();
    assert!(seen.iter().any(|r| r.method == "PUT"));
    let root = format!("/{BUCKET}/{PREFIX}");
    assert!(seen.iter().all(|r| r.path.starts_with(&root)), "{seen:?}");
    Ok(())
}

#[test]
fn a_token_file_under_a_root_is_withheld_and_never_uploaded() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("collector", &server.url(), PREFIX, true)?;
    let mut daemon = start_from_config(&rig.state, &rig.cfg).map_err(e)?;
    let report = daemon.reconcile_once().map_err(e)?;
    // The file is yielded as a transcript and refused by admission: nothing is captured, so
    // nothing lands and nothing is written.
    assert_eq!((report.landed, report.failed, report.parked), (0, 0, 0));
    daemon.shutdown().map_err(e)?;
    assert_eq!(server.count("PUT"), 0);
    for seen in server.requests() {
        let decoded = zstd::decode_all(&seen.body[..]).unwrap_or_default();
        for haystack in [seen.path.as_bytes(), &seen.body[..], &decoded[..]] {
            for needle in [KEY_ID, SECRET] {
                assert!(
                    !haystack
                        .windows(needle.len())
                        .any(|w| w == needle.as_bytes()),
                    "secret reached the server"
                );
            }
        }
    }
    Ok(())
}

#[test]
fn refusals_are_closed_codes_and_leave_no_lock() -> R {
    let server = Fake::start()?;
    let url = server.url();
    let rig = Rig::new("refuse", &url, "", false)?;
    let again = |rig: &Rig| -> R { start_from_config(&rig.state, &rig.cfg).map(drop).map_err(e) };

    assert_eq!(
        code_of(start_from_config(&rig.state, &rig.cfg))?,
        ("config_prefix_required".to_owned(), 2)
    );
    rig.write_config(&url, PREFIX)?;

    rig.write_token(&good_token(), 0o640)?;
    assert_eq!(
        code_of(start_from_config(&rig.state, &rig.cfg))?,
        ("config_token_permissions".to_owned(), 2)
    );
    rig.write_token(&format!("{}extra = 1\n", good_token()), 0o600)?;
    assert_eq!(
        code_of(start_from_config(&rig.state, &rig.cfg))?,
        ("token_parse".to_owned(), 2)
    );
    rig.write_token(&good_token(), 0o600)?;
    again(&rig)?;
    assert!(server.requests().is_empty(), "no network call");
    Ok(())
}
