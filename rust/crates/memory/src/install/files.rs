//! The files `memory install` writes: launchd plist, Pi extension, and the Claude/Codex hook
//! documents. Text builders are pure; [`write_file`] does the unchanged check, the backup and
//! the temp-sibling rename.
use super::Failure;
use super::json;
use super::merge::{self, Agent};
use std::fs::{self, OpenOptions};
use std::io::Write;
use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};
use std::path::Path;

/// First line of the Pi extension we manage.
pub const PI_MARKER: &str = "// memory: managed by `memory install`; edits are overwritten.";

/// What happened to one file.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Outcome {
    /// Written; `backup` is the backup's file name when an older file existed.
    Wrote {
        /// Backup file name.
        backup: Option<String>,
    },
    /// Nothing to change.
    Unchanged,
    /// Deleted.
    Removed,
    /// Present but not ours; left alone.
    Kept,
}

/// The Pi extension text for `exe`.
#[must_use]
pub fn pi_extension(exe: &str) -> String {
    let bin = serde_json::to_string(exe).unwrap_or_default();
    format!(
        r#"{PI_MARKER}
import {{ spawn }} from "node:child_process";
import type {{ ExtensionAPI }} from "@earendil-works/pi-coding-agent";

const BIN = {bin};

function kick(event: string, ctx: any): void {{
	try {{
		const file = ctx.sessionManager.getSessionFile();
		// No file for --no-session runs, or before the session's first message.
		if (!file) return;
		const payload = JSON.stringify({{
			event,
			session_id: ctx.sessionManager.getSessionId(),
			session_file: file,
			cwd: ctx.cwd,
		}});
		const child = spawn(BIN, ["hook", "pi"], {{
			detached: true,
			stdio: ["pipe", "ignore", "ignore"],
		}});
		child.on("error", () => {{}});
		child.stdin.on("error", () => {{}});
		child.stdin.end(payload);
		child.unref();
	}} catch {{
		// Capture must never break the session.
	}}
}}

export default function (pi: ExtensionAPI) {{
	pi.on("session_start", (_event, ctx) => kick("session_start", ctx));
	pi.on("agent_settled", (_event, ctx) => kick("agent_settled", ctx));
	pi.on("session_shutdown", (_event, ctx) => kick("session_shutdown", ctx));
}}
"#
    )
}

/// XML text escaping.
#[must_use]
pub fn xml_escape(text: &str) -> String {
    text.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

fn xml_unescape(text: &str) -> String {
    text.replace("&lt;", "<")
        .replace("&gt;", ">")
        .replace("&quot;", "\"")
        .replace("&apos;", "'")
        .replace("&amp;", "&")
}

/// The launchd agent file for `exe`, logging stderr to `log`.
#[must_use]
pub fn plist(exe: &str, log: &str) -> String {
    let exe = xml_escape(exe);
    let log = xml_escape(log);
    format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>memory</string>
  <key>ProgramArguments</key>
  <array>
    <string>{exe}</string>
    <string>daemon</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key>
  <dict><key>SuccessfulExit</key><false/></dict>
  <key>ThrottleInterval</key><integer>60</integer>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>/dev/null</string>
  <key>StandardErrorPath</key><string>{log}</string>
</dict>
</plist>
"#
    )
}

/// Whether a plist is ours: label `memory` and a first argument naming a `memory` binary.
#[must_use]
pub fn plist_is_ours(text: &str) -> bool {
    let Some((_, rest)) = text.split_once("<key>Label</key>") else {
        return false;
    };
    if !rest.trim_start().starts_with("<string>memory</string>") {
        return false;
    }
    let Some((_, rest)) = text.split_once("<key>ProgramArguments</key>") else {
        return false;
    };
    let Some((_, rest)) = rest.split_once("<array>") else {
        return false;
    };
    let rest = rest.trim_start();
    let Some(rest) = rest.strip_prefix("<string>") else {
        return false;
    };
    let Some((first, _)) = rest.split_once("</string>") else {
        return false;
    };
    let first = xml_unescape(first);
    let path = Path::new(&first);
    path.is_absolute()
        && !first.ends_with('/')
        && path.file_name().is_some_and(|name| name == "memory")
}

/// `YYYYMMDDTHHMMSSZ` for a Unix time.
#[must_use]
pub fn utc_stamp(secs: u64) -> String {
    let days = i64::try_from(secs / 86_400).unwrap_or(0);
    let rem = secs % 86_400;
    // Civil date from days since 1970-01-01 (Hinnant).
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let day = doy - (153 * mp + 2) / 5 + 1;
    let month = if mp < 10 { mp + 3 } else { mp - 9 };
    let year = yoe + era * 400 + i64::from(month <= 2);
    format!(
        "{year:04}{month:02}{day:02}T{:02}{:02}{:02}Z",
        rem / 3600,
        rem % 3600 / 60,
        rem % 60
    )
}

/// Current Unix time in seconds.
#[must_use]
pub fn now_secs() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |d| d.as_secs())
}

/// Read a file; `None` when absent.
///
/// # Errors
/// [`Failure::Io`] for any other error.
pub fn read_existing(path: &Path) -> Result<Option<Vec<u8>>, Failure> {
    match fs::read(path) {
        Ok(bytes) => Ok(Some(bytes)),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(_) => Err(Failure::Io),
    }
}

/// Write `new` to `path` unless `old` already equals it. An older file is first copied to
/// `<path>.memory-backup-<stamp>` with its mode; the new file is a temp sibling, synced and
/// renamed. New files get 0644, existing ones keep their mode. `dry` reports without writing.
///
/// # Errors
/// [`Failure::Io`].
pub fn write_file(
    path: &Path,
    old: Option<&[u8]>,
    new: &str,
    dry: bool,
    now: u64,
) -> Result<Outcome, Failure> {
    if old == Some(new.as_bytes()) {
        return Ok(Outcome::Unchanged);
    }
    let name = path
        .file_name()
        .and_then(|n| n.to_str())
        .ok_or(Failure::Io)?;
    // A second change within the same second gets a numeric suffix rather than failing.
    let backup = old.map(|_| {
        let base = format!("{name}.memory-backup-{}", utc_stamp(now));
        let mut candidate = base.clone();
        let mut n = 1_u32;
        while path.with_file_name(&candidate).symlink_metadata().is_ok() {
            candidate = format!("{base}-{n}");
            n += 1;
        }
        candidate
    });
    if dry {
        return Ok(Outcome::Wrote { backup });
    }
    let mode = match old {
        Some(_) => fs::metadata(path).map_err(|_| Failure::Io)?.mode() & 0o7777,
        None => 0o644,
    };
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|_| Failure::Io)?;
    }
    if let (Some(old), Some(backup)) = (old, &backup) {
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(mode)
            .open(path.with_file_name(backup))
            .map_err(|_| Failure::Io)?;
        file.write_all(old).map_err(|_| Failure::Io)?;
        file.sync_all().map_err(|_| Failure::Io)?;
        fs::set_permissions(
            path.with_file_name(backup),
            fs::Permissions::from_mode(mode),
        )
        .map_err(|_| Failure::Io)?;
    }
    let tmp = path.with_file_name(format!("{name}.memory-tmp-{}", std::process::id()));
    let result = (|| {
        let mut file = OpenOptions::new()
            .write(true)
            .create(true)
            .truncate(true)
            .mode(mode)
            .open(&tmp)?;
        file.write_all(new.as_bytes())?;
        file.sync_all()?;
        fs::set_permissions(&tmp, fs::Permissions::from_mode(mode))?;
        fs::rename(&tmp, path)
    })();
    if result.is_err() {
        let _ = fs::remove_file(&tmp);
        return Err(Failure::Io);
    }
    Ok(Outcome::Wrote { backup })
}

fn remove_file(path: &Path, dry: bool) -> Result<Outcome, Failure> {
    if !dry {
        fs::remove_file(path).map_err(|_| Failure::Io)?;
    }
    Ok(Outcome::Removed)
}

/// Install (`command` given) or uninstall our handlers in a Claude or Codex document.
///
/// # Errors
/// [`Failure`] for an unparsable or refused document, or an I/O error.
pub fn hooks_file(
    path: &Path,
    agent: Agent,
    command: Option<&str>,
    dry: bool,
    now: u64,
) -> Result<Outcome, Failure> {
    let old = read_existing(path)?;
    if old.is_none() && command.is_none() {
        return Ok(Outcome::Unchanged);
    }
    let text = match &old {
        Some(bytes) => std::str::from_utf8(bytes).map_err(|_| Failure::Unparsable)?,
        None => "",
    };
    let new = merge::merge_text(text, agent, command)?;
    if old.is_some() && json::parse(text).ok() == json::parse(&new).ok() {
        return Ok(Outcome::Unchanged);
    }
    write_file(path, old.as_deref(), &new, dry, now)
}

/// Install or remove the Pi extension. A file without our first line is foreign.
///
/// # Errors
/// [`Failure::Io`].
pub fn pi_file(path: &Path, exe: Option<&str>, dry: bool, now: u64) -> Result<Outcome, Failure> {
    let old = read_existing(path)?;
    if let Some(bytes) = &old {
        let ours = bytes
            .split(|b| *b == b'\n')
            .next()
            .is_some_and(|line| line == PI_MARKER.as_bytes());
        if !ours {
            return Ok(Outcome::Kept);
        }
    }
    match (exe, old) {
        (Some(exe), old) => write_file(path, old.as_deref(), &pi_extension(exe), dry, now),
        (None, Some(_)) => remove_file(path, dry),
        (None, None) => Ok(Outcome::Unchanged),
    }
}

/// Install or remove the launchd plist.
///
/// # Errors
/// [`Failure::Io`].
pub fn plist_file(
    path: &Path,
    install: Option<(&str, &Path)>,
    dry: bool,
    now: u64,
) -> Result<Outcome, Failure> {
    let old = read_existing(path)?;
    match (install, old) {
        (Some((exe, log)), old) => {
            if !dry && let Some(dir) = log.parent() {
                use std::os::unix::fs::DirBuilderExt;
                fs::DirBuilder::new()
                    .recursive(true)
                    .mode(0o700)
                    .create(dir)
                    .map_err(|_| Failure::Io)?;
            }
            let text = plist(exe, log.to_str().ok_or(Failure::Io)?);
            write_file(path, old.as_deref(), &text, dry, now)
        }
        (None, None) => Ok(Outcome::Unchanged),
        (None, Some(bytes)) => {
            if std::str::from_utf8(&bytes).is_ok_and(plist_is_ours) {
                remove_file(path, dry)
            } else {
                Ok(Outcome::Kept)
            }
        }
    }
}
