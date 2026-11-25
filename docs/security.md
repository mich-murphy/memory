# Capture security boundaries

These boundaries are safety requirements for the [capture contract](session-capture-contract.md)
and are implemented in `rust/crates/memory-core/src/privacy/`; [DESIGN.md section 8](../DESIGN.md)
governs the pipeline. Session opt-out is retired by operator decision (2026-11-07); the
protections below are not opt-outs and stay.

Capture applies a known-file policy, not arbitrary transcript DLP. A source of a known kind is
captured byte for byte, even if it contains a literal credential, unless its kind is scanned.
Copies renamed to unrelated filenames with no recognised path identity are outside this
policy.

## Before reading

Every logical path, intermediate symlink and final identity is checked against the policy.
Known credential files, `memory`'s own config, token and state are excluded. Filenames are
compared case-insensitively and with known backup suffixes normalised: editor copies (`~`,
numbered tilde backups, `.bak`, `.backup`, `.orig`, `.old`), eight-digit date suffixes,
`memory`'s own installer backups (`.memory-backup-<YYYYMMDDTHHMMSSZ>`, optionally `-<n>`) and
common archive extensions (`gz`, `bz2`, `xz`, `zst`, `zstd`, `zip`,
`7z`, `rar`, `tar`, `tgz`). Credential exclusion does not depend on decoding a compressed
backup. Arbitrary suffixes such as `auth.json.example` or `notes.key.txt` remain outside the
known-filename policy. These checks do not give an atomic filesystem snapshot or defend
against hostile concurrent path replacement.

## After reading

Captured files are not rewritten. A file of a known kind is published byte for byte or withheld
whole; nothing is redacted inside it.

- **Content scan.** Pi run records (`Profile::Scanned` snapshots) are scanned with the vendored
  gitleaks rule set. A finding withholds the whole source, as does a binary file, an oversized
  one or a scanner failure. Reasons are fixed and carry no content.
- **Launch argv and environment.** What is recorded at session start goes through an allowlist
  (`privacy/launch.rs`). Every argument or value that is not allowlisted becomes `[redacted]`.
- **Unknown kinds.** A source of no admitted kind is withheld, not captured. Kinds marked
  `Ignored` are never captured.

Transcripts are published as written: the scan does not cover them.

## Previously stored data

These checks protect new captures. Updating `memory`, deleting local state or changing a
filename does not remove bytes already stored. If a credential is known to have been captured,
rotate or revoke it and treat remaining remote objects and local backups as sensitive.
Resetting the archive never authorizes deleting local research artifacts, original sources,
identities or secret-policy code. Remote retention and deletion follow DESIGN.md and need their
own policy, not incidental cleanup.
