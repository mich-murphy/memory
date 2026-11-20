# Installation and removal

> **Scope:** These are binary installation/removal and legacy managed-hook mechanics,
> not candidate service activation or controlled cutover instructions. Preserve machine
> IDs, exclusions, learned roots, original sources and pending state. See the
> [contract](session-capture-contract.md) for acceptance and the
> [current handoff](session-capture-handoff.md#current-session-checkpoint) for deployment
> and next action.

The Nix and release routes below install a selected binary; `agent-sessions install`
configures the legacy managed hooks/extension. The opt-in
[candidate lifecycle](session-capture-phase3.md) separately provides service/hook
planning and apply commands. Neither installing a binary nor applying candidate hooks
is final default cutover or evidence of native/R2/two-machine acceptance.

## Nix (built from source)

The flake's `packages.<system>.default` builds the binary from source with `CGO_ENABLED=0`
and the commit as its version. Add the repo as a flake input pinned to a tag, and install
`inputs.agent-sessions.packages.${system}.default` with nix-darwin's
`environment.systemPackages` or home-manager's `home.packages`:

```nix
inputs.agent-sessions.url = "git+ssh://git@github.com/mich-murphy/memory?ref=refs/tags/v0.3.0";
```

While the repo is private, Nix fetches over SSH with a key GitHub accepts (or set a GitHub
token in `access-tokens` and use `github:mich-murphy/memory/v0.3.0`). Your `flake.lock`
records the exact commit and its content hash, and the Go dependencies are checked against
`vendorHash`. Upgrade by changing the tag and running `nix flake update agent-sessions`,
then rebuild: the lock diff shows what changed. After changing `go.mod` or `go.sum`, update
`vendorHash` in `flake.nix` (set `lib.fakeHash`, build, copy the reported hash).

The hooks run the profile path on `PATH` (for example `/etc/profiles/per-user/$USER/bin`
on nix-darwin, `~/.nix-profile/bin` with standalone home-manager), not the store path, so
an upgrade keeps Codex's hook trust valid. Run `agent-sessions install` once after the
first rebuild.

## Release binary (`install.sh`)

Releases are immutable: once published, GitHub locks the tag and every asset, and signs a
release attestation of the tag, commit and asset digests. Download the script from the
release you chose, verify it, read it, then run it:

```bash
gh release download v0.3.0 -R mich-murphy/memory -p install.sh
gh release verify-asset v0.3.0 install.sh -R mich-murphy/memory
less install.sh
VERSION=v0.3.0 sh install.sh
```

The script requires `VERSION` (there is no implicit latest) and a logged-in `gh`. It
refuses to install unless `gh release verify` confirms the release is immutable and
attested, `gh release verify-asset` matches the downloaded binary to that attestation,
and the binary matches `checksums.txt`. The checksum alone only shows the two downloads
agree; the attestation is what catches a binary and checksum replaced together. All of its
logic runs from a `main` function called on the last line, so a truncated download runs
nothing. `BIN_DIR` selects another directory than `~/.local/bin`, including one with spaces.

This proves the files are exactly what was published under that tag. Releases are built
locally (see [quality-gates.md](quality-gates.md#ci-and-releases)), so it does not prove
the binary was built from the tagged commit; the Nix route does.

Each `install.sh` installation exclusively creates its own temporary sibling binary, copies
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

## Operations (Rust `memory` binary)

Three day-to-day commands. Output is closed: counts, owner prefixes and reason codes, never a
path, a source id, content or a credential.

**`memory status`** is the local health view. It takes no lock, writes nothing under the state
root and makes no network request. It reads `config.toml`, `status.json` and `index.json`,
walks the configured roots and stats each source (no source byte is read). Line 1 gives
`as_of`, `age_s`, `rounds`, `host_paused` and how `status.json` and `index.json` loaded; line 2
counts sources as `current`, `behind`, `changed` or `unindexed`, plus `withheld`,
`oldest_behind_s` and the walk counts; line 3 gives failure and audit counts; then one `parked`
line per parked entry. Exit 0, or 1 when anything is parked, the host is paused or
`status.json` is not `loaded`; 2 config; 64 usage. `index.json` is written at most every 60 s,
so a source captured in the last minute may show as `behind`. It never claims everything is
captured: `memory check` is the verified view (it fetches every piece, about 23 minutes on a
large archive), and `status --verify` exits 64.

**`memory unpark <owner-prefix>|--all`** clears parked entries from `status.json`. The daemon is
the only writer of that file and reads it once at start, so `unpark` takes the daemon lock and
refuses beside a running daemon (`memory: lock_held`, exit 75). With launchd:

```sh
launchctl bootout gui/$(id -u)/memory
memory unpark <owner-prefix>        # or: memory unpark --all
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/memory.plist
```

On Linux (systemd user unit) the same sequence is:

```sh
systemctl --user stop memory.service
memory unpark <owner-prefix>        # or: memory unpark --all
systemctl --user start memory.service
```

It removes every entry of that owner (owner-wide and per-source) and keeps every other field.
Exit 1 (`not_parked`) when nothing matches, 2 for a damaged or newer-schema `status.json` (the
file is left untouched), 64 for bad arguments. No network.

**`memory reconcile`** runs one full round now and exits. It takes the daemon lock (exit 75
beside a running daemon), logs a `round` line with `cause=manual` to stderr and prints
`reconcile landed=<n> failed=<n> parked=<n> skipped=<n> deferred=<n>`. Exit 0 when nothing
failed and nothing was parked, 1 otherwise, 2 config, 130 when stopped by SIGINT or SIGTERM
before the round ended.

### Linux (systemd user unit)

On Linux `memory install` writes `$XDG_CONFIG_HOME/systemd/user/memory.service` (when
`XDG_CONFIG_HOME` is set, non-empty and absolute; otherwise `~/.config/systemd/user/memory.service`)
and prints the commands it does not run: `systemctl --user daemon-reload`,
`systemctl --user enable --now memory.service` and `loginctl enable-linger $USER` (without
linger a server reached over SSH stops the user manager at logout). `memory uninstall` removes
only a unit that carries our first line and runs `memory daemon`; it then prints
`systemctl --user disable --now memory.service` and `systemctl --user daemon-reload`. A path with
a control character is refused (`install_binary_name`, exit 2).

Logs go to the journal: `journalctl --user -u memory`; journald's own limits apply, and the
daemon's log rotation does not act on it. On a busy host check `fs.inotify.max_user_watches`
first if watching fails or sources lag.
