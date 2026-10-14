# Installation and removal

Run `sh install.sh` (or `sh install.sh install`) to install/upgrade a checksum-verified
release into `~/.local/bin`. `BIN_DIR` selects another directory, including one with
spaces. `VERSION=v0.1.0` selects a release; the default `latest` resolves to a single
release tag before downloading either the binary or its checksum. Authenticated
`gh` is preferred; otherwise the installer uses `curl`. Checksum verification is
integrity checking against the same release, not a separate signature guarantee.

Each installation exclusively creates its own temporary sibling binary, copies
and chmods it before atomic rename, and removes temporary files on failures and
HUP/INT/TERM. Concurrent installs do not share staging paths. SIGKILL cannot run
cleanup; abandoned uniquely named temporary files can be removed manually.

`sh install.sh uninstall` uses **only the installed binary** to run
`agent-sessions uninstall` first, removing managed Claude/Codex hook entries and
the managed Pi extension. Only on success does it remove the binary. It does not
use the network, detect the platform or download tools. Missing installations are
an idempotent no-op. Non-executable files, directories and symlinks are refused;
failed hook cleanup preserves the binary for retry. Config/credentials,
machine-id, collected local state, backups and **all remote objects** are preserved.
If the binary is already absent, this script cannot remove remaining hooks: restore
the binary first. `--help` describes these commands; other arguments are rejected.

## Doctor and Codex trust

Doctor first checks whole-file Codex schema admission, including foreign handlers:
unknown top-level fields and malformed known fields can prevent every hook from
loading. Unknown nested fields and unknown events are allowed where Codex allows
them. Unsupported MCP input representations fail closed as not verified. The
installer refuses to rewrite a Codex document that fails this check.

Doctor then counts distinct required hook events, checks the handler's type, agent,
executable and unconditional matcher, and checks disable switches. It resolves
executables without executing hook commands, including foreign commands.

Codex trust verification follows the Unix command-hook algorithm at the commit
cited by [hooks.md](hooks.md), `openai/codex@806d973`:

- [discovery.rs](https://github.com/openai/codex/blob/806d9732c974bc8a51b8317c1bd8985544fe627c/codex-rs/hooks/src/engine/discovery.rs):
  normalized timeout (1–3 seconds for Interrupt/SessionEnd, otherwise minimum 1,
  default 600), original async flag, status message, context-limit normalization,
  one normalized handler per group, and exact hash comparison. Disabled trust
  records (`enabled = false`) do not count as usable trust.
- [hook_config.rs](https://github.com/openai/codex/blob/806d9732c974bc8a51b8317c1bd8985544fe627c/codex-rs/config/src/hook_config.rs):
  serialized handler and matcher field names. Absent optional fields disappear
  through TOML conversion.
- [common.rs](https://github.com/openai/codex/blob/806d9732c974bc8a51b8317c1bd8985544fe627c/codex-rs/hooks/src/events/common.rs):
  Stop/Interrupt ignore matchers.
- [fingerprint.rs](https://github.com/openai/codex/blob/806d9732c974bc8a51b8317c1bd8985544fe627c/codex-rs/config/src/fingerprint.rs):
  recursively key-sorted compact JSON of the TOML value, SHA-256 prefixed `sha256:`.

Verification supports the six installed Codex events and command handlers on
Linux/macOS. Unsupported/malformed identities and Unicode U+2028/U+2029 separators
fail closed as not verified; they are never claimed trusted based on a nonempty
stored hash. A changed command invalidates existing approval. Re-review in Codex
`/hooks` after changes. Future Codex algorithm changes may require collector updates.
