//! The token reader: accepted shape, every refusal, and no secret in any output.
use memory::token::{MAX_TOKEN_BYTES, TokenError, read_token};
use std::fmt::Debug;
use std::fs;
use std::os::unix::fs::{PermissionsExt, symlink};
use std::path::{Path, PathBuf};

type R = Result<(), String>;
const KEY: &str = "KEYIDMARKER42";
const SECRET: &str = "SECRETMARKER99";

fn e<E: Debug>(error: E) -> String {
    format!("{error:?}")
}

struct Tmp(PathBuf);
impl Tmp {
    fn new(label: &str) -> Result<Self, String> {
        let path =
            std::env::temp_dir().join(format!("memory-token-{}-{label}", std::process::id()));
        let _ = fs::remove_dir_all(&path);
        fs::create_dir_all(&path).map_err(e)?;
        Ok(Self(path))
    }
    fn file(&self, name: &str, body: &[u8], mode: u32) -> Result<PathBuf, String> {
        let path = self.0.join(name);
        fs::write(&path, body).map_err(e)?;
        fs::set_permissions(&path, fs::Permissions::from_mode(mode)).map_err(e)?;
        Ok(path)
    }
}
impl Drop for Tmp {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn good() -> String {
    format!("access_key_id = \"{KEY}\"\nsecret_access_key = \"{SECRET}\"\n")
}

fn err_of(path: &Path) -> Result<TokenError, String> {
    match read_token(path) {
        Err(error) => Ok(error),
        Ok(_) => Err("accepted".to_owned()),
    }
}

#[test]
fn accepts_the_exact_shape() -> R {
    let tmp = Tmp::new("ok")?;
    let creds = read_token(&tmp.file("t", good().as_bytes(), 0o600)?).map_err(e)?;
    assert_eq!(creds.access_key_id(), KEY);
    assert_eq!(creds.secret_access_key(), SECRET);
    Ok(())
}

#[test]
fn refusals_are_closed_codes_without_text() -> R {
    let tmp = Tmp::new("bad")?;
    let big = format!("{}\n#{}", good(), "x".repeat(MAX_TOKEN_BYTES as usize));
    let cases: Vec<(&str, PathBuf, TokenError)> = vec![
        (
            "group readable",
            tmp.file("g", good().as_bytes(), 0o640)?,
            TokenError::Permissions,
        ),
        (
            "world readable",
            tmp.file("w", good().as_bytes(), 0o604)?,
            TokenError::Permissions,
        ),
        (
            "unknown field",
            tmp.file("u", format!("{}extra = 1\n", good()).as_bytes(), 0o600)?,
            TokenError::Parse,
        ),
        (
            "missing secret",
            tmp.file(
                "m",
                format!("access_key_id = \"{KEY}\"\n").as_bytes(),
                0o600,
            )?,
            TokenError::Parse,
        ),
        (
            "not toml",
            tmp.file("n", format!("{KEY} {SECRET}").as_bytes(), 0o600)?,
            TokenError::Parse,
        ),
        (
            "not utf8",
            tmp.file("b", &[0xff, 0xfe], 0o600)?,
            TokenError::Parse,
        ),
        (
            "empty key",
            tmp.file(
                "ek",
                format!("access_key_id = \"\"\nsecret_access_key = \"{SECRET}\"\n").as_bytes(),
                0o600,
            )?,
            TokenError::Empty,
        ),
        (
            "empty secret",
            tmp.file(
                "es",
                format!("access_key_id = \"{KEY}\"\nsecret_access_key = \"\"\n").as_bytes(),
                0o600,
            )?,
            TokenError::Empty,
        ),
        (
            "too large",
            tmp.file("l", big.as_bytes(), 0o600)?,
            TokenError::TooLarge,
        ),
    ];
    for (label, path, want) in cases {
        let got = err_of(&path)?;
        assert_eq!(got, want, "{label}");
        for text in [format!("{got}"), format!("{got:?}"), got.code().to_owned()] {
            assert!(!text.contains(KEY) && !text.contains(SECRET), "{label}");
        }
    }
    assert!(matches!(
        err_of(&tmp.0.join("absent"))?,
        TokenError::Unavailable(_)
    ));
    assert_eq!(err_of(&tmp.0)?, TokenError::NotFile);
    // A symlink to a good file is followed and its target's mode is what counts.
    let target = tmp.file("target", good().as_bytes(), 0o600)?;
    let link = tmp.0.join("link");
    symlink(&target, &link).map_err(e)?;
    assert!(read_token(&link).is_ok());
    Ok(())
}

#[test]
fn credentials_debug_prints_neither_value() -> R {
    let tmp = Tmp::new("dbg")?;
    let creds = read_token(&tmp.file("t", good().as_bytes(), 0o600)?).map_err(e)?;
    let text = format!("{creds:?}{creds:#?}");
    assert!(!text.contains(KEY) && !text.contains(SECRET));
    Ok(())
}
