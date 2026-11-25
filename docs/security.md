# Capture security boundaries

These boundaries are safety requirements for the [capture contract](session-capture-contract.md)
and are implemented in `rust/crates/memory-core/src/privacy/`; [DESIGN.md section 8](../DESIGN.md)
governs the pipeline. Session opt-out is retired by operator decision (2026-11-07); the
protections below are not opt-outs and stay.

Capture applies a known-file policy, not arbitrary transcript DLP. An unrecognised filename
is captured byte for byte, even if its contents look like a supported config or contain a
literal credential. Copies renamed to unrelated filenames with no recognised path identity
are outside this policy.

## Before reading

Every logical path, intermediate symlink and final identity is checked against the policy.
Known credential files, `memory`'s own config, token and state are excluded. Filenames are
compared case-insensitively and with known backup suffixes normalised: editor copies (`~`,
numbered tilde backups, `.bak`, `.backup`, `.orig`, `.old`), eight-digit date suffixes,
installer backups and common archive extensions (`gz`, `bz2`, `xz`, `zst`, `zstd`, `zip`,
`7z`, `rar`, `tar`, `tgz`). Credential exclusion does not depend on decoding a compressed
backup. Arbitrary suffixes such as `auth.json.example` or `notes.key.txt` remain outside the
known-filename policy. These checks do not give an atomic filesystem snapshot or defend
against hostile concurrent path replacement.

## After reading

Each recognised filename in a symlink chain applies its supported schema, whichever agent is
collecting: a Claude skill symlink to Pi's `models.json` must redact Pi provider credentials.
Malformed protected JSON or TOML is withheld, not copied unchanged. Selected literal secrets
and equivalent decoded string copies are redacted without rewriting object keys. Environment
references and Pi secret-command references remain references. Parser errors never echo
credential values.

Launch argv and environment recorded at session start go through an allowlist; values whose
names mark a secret (`TOKEN`, `KEY`, `AUTH`, `SECRET`, `PASSWORD`, `CREDENTIAL`, `COOKIE`) are
dropped, and secrets in argv (such as `--api-key` values or inline MCP config headers) are
redacted. Unknown source kinds are withheld, not captured.

## Previously stored data

These checks protect new captures. Updating `memory`, deleting local state or changing a
filename does not remove bytes already stored. If a credential is known to have been captured,
rotate or revoke it and treat remaining remote objects and local backups as sensitive.
Resetting the archive never authorizes deleting local research artifacts, original sources,
identities or secret-policy code. Remote retention and deletion follow DESIGN.md and need their
own policy, not incidental cleanup.
