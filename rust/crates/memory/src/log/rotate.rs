//! Size-based rotation of the file behind the daemon's stderr (brief 41).
//!
//! [`Rotator`] decides and renames; it takes the target descriptor and its path from the caller,
//! so tests use temporary files. Nothing here writes text about a failure: any error turns
//! rotation off for the life of the [`Rotator`] and the target is left as it was.
use rustix::fs::{FileType, Stat, fstat, stat};
use std::ffi::OsString;
use std::fs::{File, OpenOptions};
use std::os::fd::{AsFd, BorrowedFd};
use std::os::unix::fs::OpenOptionsExt;
use std::path::{Path, PathBuf};

/// Size at which the log file is rotated: 8 MiB.
pub const LOG_CAP_BYTES: u64 = 8 * 1024 * 1024;

/// Rotated generations kept next to the live file (`.1` to `.3`).
pub const LOG_GENERATIONS: u32 = 3;

enum State {
    /// Nothing decided yet: the first line checks the target kind and resolves its path.
    Fresh,
    /// The target is a regular file at this path.
    Armed(PathBuf),
    /// Not a regular file, path unknown, or a rotation step failed: never rotate again.
    Off,
}

/// The rotation decision for one log target.
pub struct Rotator {
    cap: u64,
    state: State,
}

fn numbered(path: &Path, generation: u32) -> PathBuf {
    let mut name = OsString::from(path.as_os_str());
    name.push(format!(".{generation}"));
    PathBuf::from(name)
}

fn same_file(a: &Stat, b: &Stat) -> bool {
    a.st_dev == b.st_dev && a.st_ino == b.st_ino
}

/// Shift the generations up, move the live file to `.1` and open a fresh live file.
fn rotate(path: &Path) -> Option<File> {
    for generation in (1..LOG_GENERATIONS).rev() {
        match std::fs::rename(numbered(path, generation), numbered(path, generation + 1)) {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(_) => return None,
        }
    }
    std::fs::rename(path, numbered(path, 1)).ok()?;
    OpenOptions::new()
        .create_new(true)
        .append(true)
        .mode(0o600)
        .open(path)
        .ok()
}

impl Rotator {
    /// A rotator that acts once the target holds at least `cap` bytes.
    #[must_use]
    pub const fn new(cap: u64) -> Self {
        Self {
            cap,
            state: State::Fresh,
        }
    }

    /// Whether rotation has been turned off.
    #[cfg(test)]
    #[must_use]
    pub const fn is_off(&self) -> bool {
        matches!(self.state, State::Off)
    }

    /// Turn rotation off for the life of this value.
    pub fn disable(&mut self) {
        self.state = State::Off;
    }

    /// Check `target` before a line is written. When it is a regular file of at least the cap
    /// whose path still names the same file, rotate and return the new live file; the caller
    /// points the target at it. `resolve` yields the target's path on the first call only.
    /// `None`: nothing to do or rotation is off.
    pub fn before_line(
        &mut self,
        target: impl AsFd,
        resolve: impl FnOnce(BorrowedFd<'_>) -> Option<PathBuf>,
    ) -> Option<File> {
        let fd = target.as_fd();
        let Ok(held) = fstat(fd) else {
            self.disable();
            return None;
        };
        if matches!(self.state, State::Fresh) {
            self.state = match (FileType::from_raw_mode(held.st_mode), resolve(fd)) {
                (FileType::RegularFile, Some(path)) => State::Armed(path),
                _ => State::Off,
            };
        }
        let State::Armed(path) = &self.state else {
            return None;
        };
        if u64::try_from(held.st_size).unwrap_or(0) < self.cap {
            return None;
        }
        let rotated = match stat(path) {
            Ok(named) if same_file(&named, &held) => rotate(path),
            _ => None,
        };
        if rotated.is_none() {
            self.disable();
        }
        rotated
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;
    use std::os::unix::fs::PermissionsExt;

    type R = Result<(), String>;

    fn e(error: impl std::fmt::Display) -> String {
        error.to_string()
    }

    struct Dir(PathBuf);
    impl Dir {
        fn new(label: &str) -> Result<Self, String> {
            let dir =
                std::env::temp_dir().join(format!("memory-rotate-{label}-{}", std::process::id()));
            let _ = std::fs::remove_dir_all(&dir);
            std::fs::create_dir_all(&dir).map_err(e)?;
            Ok(Self(dir))
        }
        fn log(&self) -> PathBuf {
            self.0.join("daemon.log")
        }
    }
    impl Drop for Dir {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }

    fn fill(path: &Path, byte: u8, len: usize) -> Result<File, String> {
        let mut file = OpenOptions::new()
            .create(true)
            .append(true)
            .open(path)
            .map_err(e)?;
        file.write_all(&vec![byte; len]).map_err(e)?;
        Ok(file)
    }

    #[test]
    fn below_the_cap_nothing_happens() -> R {
        let dir = Dir::new("below")?;
        let file = fill(&dir.log(), b'a', 99)?;
        let mut rotator = Rotator::new(100);
        assert!(rotator.before_line(&file, |_| Some(dir.log())).is_none());
        assert!(!rotator.is_off());
        assert!(!numbered(&dir.log(), 1).exists());
        Ok(())
    }

    #[test]
    fn at_the_cap_the_file_moves_to_one_and_a_private_file_replaces_it() -> R {
        let dir = Dir::new("at")?;
        let file = fill(&dir.log(), b'a', 100)?;
        let mut rotator = Rotator::new(100);
        let fresh = rotator.before_line(&file, |_| Some(dir.log()));
        assert!(fresh.is_some());
        assert_eq!(
            std::fs::metadata(numbered(&dir.log(), 1)).map_err(e)?.len(),
            100
        );
        let live = std::fs::metadata(dir.log()).map_err(e)?;
        assert_eq!(live.len(), 0);
        assert_eq!(live.permissions().mode() & 0o777, 0o600);
        assert!(!numbered(&dir.log(), 2).exists());
        Ok(())
    }

    #[test]
    fn four_rotations_keep_three_generations_and_drop_the_oldest() -> R {
        let dir = Dir::new("gens")?;
        let mut rotator = Rotator::new(10);
        for round in 0..4u8 {
            let file = fill(&dir.log(), b'0' + round, 10)?;
            assert!(rotator.before_line(&file, |_| Some(dir.log())).is_some());
        }
        let read = |generation| std::fs::read(numbered(&dir.log(), generation)).map_err(e);
        assert_eq!(read(1)?, vec![b'3'; 10]);
        assert_eq!(read(2)?, vec![b'2'; 10]);
        assert_eq!(read(3)?, vec![b'1'; 10]);
        assert!(!numbered(&dir.log(), 4).exists());
        assert_eq!(std::fs::metadata(dir.log()).map_err(e)?.len(), 0);
        Ok(())
    }

    #[test]
    fn a_replaced_path_disables_rotation_and_renames_nothing() -> R {
        let dir = Dir::new("replaced")?;
        let file = fill(&dir.log(), b'a', 100)?;
        std::fs::remove_file(dir.log()).map_err(e)?;
        fill(&dir.log(), b'b', 100)?;
        let mut rotator = Rotator::new(100);
        assert!(rotator.before_line(&file, |_| Some(dir.log())).is_none());
        assert!(rotator.is_off());
        assert!(!numbered(&dir.log(), 1).exists());
        assert_eq!(std::fs::read(dir.log()).map_err(e)?, vec![b'b'; 100]);
        assert!(rotator.before_line(&file, |_| None).is_none());
        Ok(())
    }

    #[test]
    fn a_pipe_is_never_rotated() -> R {
        let (_reader, mut writer) = std::io::pipe().map_err(e)?;
        writer.write_all(&[b'a'; 100]).map_err(e)?;
        let mut rotator = Rotator::new(1);
        assert!(rotator.before_line(&writer, |_| None).is_none());
        assert!(rotator.is_off());
        Ok(())
    }
}
