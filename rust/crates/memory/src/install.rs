//! `memory install` and `memory uninstall` (brief 36): the launchd agent file, the Claude and
//! Codex hook entries and the Pi extension for the running binary.
pub mod files;
pub mod json;
pub mod merge;

/// Why an install or uninstall step refused. Closed; carries no file text.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum Failure {
    /// A file is not a JSON object, has a duplicate key, or has a shape the merge would damage.
    #[error("install_unparsable")]
    Unparsable,
    /// A Codex `hooks.json` with a top-level key other than `description` and `hooks`.
    #[error("install_codex_schema")]
    CodexSchema,
    /// A file could not be read, written or renamed; only the step fails.
    #[error("install_io")]
    Io,
}

impl Failure {
    /// The closed code printed after `memory: `.
    #[must_use]
    pub fn code(&self) -> &'static str {
        match self {
            Self::Unparsable => "install_unparsable",
            Self::CodexSchema => "install_codex_schema",
            Self::Io => "install_io",
        }
    }
}
