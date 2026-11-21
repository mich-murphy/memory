//! D54, brief 47: Claude file-history behind the content scan, on the real binary against the
//! loopback fake. One session, a transcript and three file-history files (clean, secret,
//! binary). Secrets are synthetic and assembled from parts.
use super::assemble::SESSION;
use super::daemon_restore::{Setup, setup};
use super::{R, e};
use std::fs;
use std::process::Command;

const UUID: &str = "0f0e0d0c-0b0a-4908-8706-050403020100";
const CLEAN: &str = "aaaaaaaaaaaaaaaa@v1";
const SECRET_FILE: &str = "bbbbbbbbbbbbbbbb@v1";
const BINARY: &str = "cccccccccccccccc@v1";
const CLEAN_BODY: &str = "fn clean_marker_fh() {}\n";

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

#[test]
fn file_history_lands_clean_and_withholds_secret_and_binary() -> R {
    let s = setup("file-history")?;
    let fh = s.rig.root.join("file-history").join(UUID);
    fs::create_dir_all(&fh).map_err(e)?;
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
    let prefix = memory_core::limits::owner_prefix(&s.owner);
    let restored = command(&s, &["restore", &prefix, "--out", &out_text])?;
    assert_eq!(restored.code, 0, "{}{}", restored.out, restored.err);
    assert_eq!(
        fs::read(out.join(SESSION)).map_err(e)?,
        fs::read(&s.file).map_err(e)?
    );
    let rel = out.join("file-history").join(UUID);
    assert_eq!(fs::read(rel.join(CLEAN)).map_err(e)?, CLEAN_BODY.as_bytes());
    assert!(!rel.join(SECRET_FILE).exists() && !rel.join(BINARY).exists());

    let export = s.rig.dir.join("exported");
    let export_text = export.display().to_string();
    let exported = command(&s, &["export", &prefix, "--out", &export_text])?;
    assert_eq!(exported.code, 0, "{}{}", exported.out, exported.err);
    assert!(exported.err.contains("sources=2"), "{}", exported.err);
    Ok(())
}
