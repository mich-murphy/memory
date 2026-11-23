//! D54, brief 47: a scanned snapshot behind the content scan, on the real binary against the
//! loopback fake. Brief 54: Claude file-history is `Ignored` now, so the scanned kind is the
//! Pi run record `sessions/<cwd>/subagent-artifacts/<name>.json` under a Pi root. One Claude
//! session, three run records (clean, secret, binary) and a file-history file that is ignored.
//! Secrets are synthetic and assembled from parts.
use super::daemon_restore::{Setup, setup};
use super::{R, e};
use std::fs;
use std::path::PathBuf;
use std::process::Command;

const UUID: &str = "0f0e0d0c-0b0a-4908-8706-050403020100";
const CWD: &str = "--pi-cwd--";
const CLEAN: &str = "aaaaaaaaaaaaaaaa.json";
const SECRET_FILE: &str = "bbbbbbbbbbbbbbbb.json";
const BINARY: &str = "cccccccccccccccc.json";
const HISTORY: &str = "dddddddddddddddd@v1";
const CLEAN_BODY: &str = "{\"run\":\"clean_marker_fh\"}\n";

fn secret_body() -> String {
    concat!("aws = ", "AKIA", "EXAMPLEEXAMPLE00", "\n").to_owned()
}

struct Ran {
    code: i32,
    out: String,
    err: String,
}

fn command(s: &Setup, args: &[&str]) -> R<Ran> {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.env_clear()
        .env("MEMORY_STATE_DIR", &s.rig.state)
        .args(args);
    if let Some(v) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", v);
    }
    let o = cmd.output().map_err(e)?;
    let ran = Ran {
        code: o.status.code().ok_or("signal")?,
        out: String::from_utf8_lossy(&o.stdout).into_owned(),
        err: String::from_utf8_lossy(&o.stderr).into_owned(),
    };
    // No file name, secret, content byte or path in any output. The owner prefix (which
    // `check` prints by design) carries the session id, so that is not a needle.
    let needles = [
        CLEAN,
        SECRET_FILE,
        BINARY,
        "AKIA",
        "EXAMPLEEXAMPLE",
        "clean_marker_fh",
        "file-history",
        "subagent-artifacts",
    ];
    let dir = s.rig.dir.display().to_string();
    for text in [&ran.out, &ran.err] {
        for (n, needle) in needles.iter().copied().chain([dir.as_str()]).enumerate() {
            assert!(
                !text.contains(needle),
                "output holds needle #{n} (args {args:?})"
            );
        }
    }
    Ok(ran)
}

/// A Pi root (config re-copied into the state dir) and its run-record directory, plus a Claude
/// file-history file that brief 54 ignores.
fn pi_records(s: &Setup) -> R<PathBuf> {
    let pi = s.rig.add_root("pi")?;
    fs::copy(&s.rig.cfg, s.rig.state.join("config.toml")).map_err(e)?;
    let fh = s.rig.root.join("file-history").join(UUID);
    fs::create_dir_all(&fh).map_err(e)?;
    fs::write(fh.join(HISTORY), "fn history() {}\n").map_err(e)?;
    let dir = pi.join("sessions").join(CWD).join("subagent-artifacts");
    fs::create_dir_all(&dir).map_err(e)?;
    Ok(dir)
}

fn shared_prefix(s: &Setup) -> R<String> {
    use memory::config::Config;
    use memory::daemon::Sources;
    use memory::realfs::WalkLimits;
    use memory::resolve::{SharedFs, WalkSources};
    use memory::roots::locator_roots;
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
    Ok(memory_core::limits::owner_prefix(&owner))
}

// Brief 54: the scanned kind is a Pi run record; Claude file-history is ignored.
#[test]
fn file_history_lands_clean_and_withholds_secret_and_binary() -> R {
    let s = setup("file-history")?;
    let fh = pi_records(&s)?;
    fs::write(fh.join(CLEAN), CLEAN_BODY).map_err(e)?;
    fs::write(fh.join(SECRET_FILE), secret_body()).map_err(e)?;
    fs::write(fh.join(BINARY), [0x89_u8, 0x50, 0, 0x47, 0xff]).map_err(e)?;

    let ran = command(&s, &["reconcile"])?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    let round = ran
        .err
        .lines()
        .find(|l| l.contains(" INFO round "))
        .ok_or("no round line")?;
    assert!(round.contains(" withheld=2"), "{round}");

    let status = command(&s, &["status"])?;
    assert_eq!(status.code, 0, "{}{}", status.out, status.err);
    let line = status
        .out
        .lines()
        .find(|l| l.starts_with("sources "))
        .ok_or("no sources line")?;
    assert!(line.split(' ').any(|w| w == "withheld=2"), "{line}");
    assert!(line.split(' ').any(|w| w == "ignored=1"), "{line}");

    let check = command(&s, &["check"])?;
    assert_eq!(check.code, 0, "{}{}", check.out, check.err);
    let withheld: Vec<&str> = check
        .out
        .lines()
        .filter(|l| l.starts_with("withheld "))
        .collect();
    assert_eq!(withheld.len(), 2, "{}", check.out);
    assert!(
        withheld
            .iter()
            .any(|l| l.ends_with(" reason=secret_in_text"))
    );
    assert!(withheld.iter().any(|l| l.ends_with(" reason=binary")));
    assert!(
        check
            .out
            .lines()
            .next()
            .unwrap_or("")
            .contains(" ignored=1 ")
    );

    // A second round judges nothing again.
    let puts = s.server.count("PUT");
    let again = command(&s, &["reconcile"])?;
    let round = again
        .err
        .lines()
        .find(|l| l.contains(" INFO round "))
        .ok_or("no round line")?;
    assert!(round.contains(" withheld=0"), "{round}");
    assert_eq!(s.server.count("PUT"), puts);

    let out = s.rig.dir.join("restored");
    let out_text = out.display().to_string();
    let prefix = shared_prefix(&s)?;
    let restored = command(&s, &["restore", &prefix, "--out", &out_text])?;
    assert_eq!(restored.code, 0, "{}{}", restored.out, restored.err);
    let rel = out.join("sessions").join(CWD).join("subagent-artifacts");
    assert_eq!(fs::read(rel.join(CLEAN)).map_err(e)?, CLEAN_BODY.as_bytes());
    assert!(!rel.join(SECRET_FILE).exists() && !rel.join(BINARY).exists());
    assert!(!out.join("file-history").exists());

    let export = s.rig.dir.join("exported");
    let export_text = export.display().to_string();
    // Export refuses a shared owner; the session owner holds the transcript only now.
    let own = memory_core::limits::owner_prefix(&s.owner);
    let exported = command(&s, &["export", &own, "--out", &export_text])?;
    assert_eq!(exported.code, 0, "{}{}", exported.out, exported.err);
    assert!(exported.err.contains("sources=1"), "{}", exported.err);
    Ok(())
}

/// Brief 50: the scan judges the value, not the name. Code that merely names a token lands;
/// a high-entropy literal in a generic assignment (a gitleaks rule, no own pattern) is withheld.
#[test]
fn file_history_lands_code_with_token_names_and_withholds_a_literal() -> R {
    // Own label: `Rig::new` keys its directory by pid and label, and `cargo test` runs tests as
    // threads of one process (parent, brief 50).
    // Brief 54: on the Pi run record, the kept scanned kind.
    let s = setup("file-history-literal")?;
    let fh = pi_records(&s)?;
    let code = "token = getToken()\nlet key = lookup(name);\n";
    let literal = concat!(
        "api_key = \"",
        "Zk9Qx2Lm7P",
        "v4Rt8Yb3Nc6W",
        "d1Fe5Gh0Js",
        "\"\n"
    );
    fs::write(fh.join(CLEAN), code).map_err(e)?;
    fs::write(fh.join(SECRET_FILE), literal).map_err(e)?;

    let ran = command(&s, &["reconcile"])?;
    assert_eq!(ran.code, 0, "{}{}", ran.out, ran.err);
    let round = ran
        .err
        .lines()
        .find(|l| l.contains(" INFO round "))
        .ok_or("no round line")?;
    assert!(round.contains(" withheld=1"), "{round}");

    let check = command(&s, &["check"])?;
    assert_eq!(check.code, 0, "{}{}", check.out, check.err);
    let withheld: Vec<&str> = check
        .out
        .lines()
        .filter(|l| l.starts_with("withheld "))
        .collect();
    assert_eq!(withheld.len(), 1, "{}", check.out);
    assert!(
        withheld
            .iter()
            .all(|l| l.ends_with(" reason=secret_in_text")),
        "{}",
        check.out
    );

    let out = s.rig.dir.join("restored");
    let out_text = out.display().to_string();
    let prefix = shared_prefix(&s)?;
    let restored = command(&s, &["restore", &prefix, "--out", &out_text])?;
    assert_eq!(restored.code, 0, "{}{}", restored.out, restored.err);
    let rel = out.join("sessions").join(CWD).join("subagent-artifacts");
    assert_eq!(fs::read(rel.join(CLEAN)).map_err(e)?, code.as_bytes());
    assert!(!rel.join(SECRET_FILE).exists());
    Ok(())
}
