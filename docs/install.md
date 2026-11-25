# Installation and removal

## New machine (Rust `memory`, current)

This is how the running hosts (this Mac and dev-01) were set up. Every step is local to the new
host; nothing in the bucket changes until the daemon starts.

1. **Build** from a tagged or merged commit, inside the pinned toolchain:

   ```sh
   git clone git@github.com:mich-murphy/memory.git && cd memory
   nix develop .#rust --command bash -c 'cd rust && cargo build --release --locked -p memory'
   mkdir -p ~/.local/libexec/memory
   cp rust/target/release/memory ~/.local/libexec/memory/.memory.new
   chmod 755 ~/.local/libexec/memory/.memory.new
   mv -f ~/.local/libexec/memory/.memory.new ~/.local/libexec/memory/memory
   ```

   The binary must be named `memory` and be run by an absolute path (`install_binary_name`,
   exit 2, otherwise). On Linux the binary links against Nix store glibc and `libgcc_s`: pin
   them so a garbage collection cannot remove them from under the daemon:

   ```sh
   mkdir -p ~/.local/libexec/memory/gcroots
   for p in $(ldd ~/.local/libexec/memory/memory | grep -o '/nix/store/[^/]*' | sort -u); do
     nix-store --add-root ~/.local/libexec/memory/gcroots/"${p#/nix/store/*-}" --realise "$p"
   done
   ```

2. **Token file**, TOML with exactly two keys, mode 0600 (group or world bits are refused,
   `config_token_permissions`). Its content is never printed or logged:

   ```toml
   access_key_id = "..."
   secret_access_key = "..."
   ```

3. **`~/.local/state/memory/config.toml`** (directory 0700). Unknown keys are refused, and every
   error is a closed code that never echoes a value:

   ```toml
   archive_id = "<the archive id every host shares>"
   machine_id = "<a new id, unique to this host>"
   bucket = "agent-sessions"
   endpoint = "https://<the S3-compatible endpoint of the bucket>"
   region = "auto"            # default "auto"
   prefix = ""                # required; "" is the bucket root, else "a/b/"
   token_path = "/home/<user>/.local/state/memory/token.toml"   # absolute
   window = { quiet = 60, cap = 300 }                            # optional, seconds

   [[roots]]
   agent = "claude"
   path = "/home/<user>/.claude"

   [[roots]]
   agent = "codex"
   path = "/home/<user>/.codex"

   [[roots]]
   agent = "pi"
   path = "/home/<user>/.pi/agent"
   ```

   Use the same `archive_id` as the other hosts so their sessions land in one archive, and a
   `machine_id` no other host uses (ids are `[A-Za-z0-9._:-]`, at most 1,024 bytes). List one
   root per agent installed on the host; roots must be absolute and must not overlap. A root
   that does not exist yet is fine.

4. **Install** the service file and the hooks. `MEMORY_STATE_DIR` must be unset (the hooks run
   without it). Preview first with `--dry-run`:

   ```sh
   ~/.local/libexec/memory/memory install --dry-run
   ~/.local/libexec/memory/memory install
   ```

   It writes the launchd agent (`~/Library/LaunchAgents/memory.plist`) or the systemd user unit
   (see Linux below), the Claude `settings.json` and Codex `hooks.json` hook entries and the Pi
   extension `~/.pi/agent/extensions/memory.ts`, backing up any file it changes. It prints the
   commands it does not run as `next:` lines: on macOS
   `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/memory.plist`; on Linux
   `systemctl --user daemon-reload`, `systemctl --user enable --now memory.service` and
   `loginctl enable-linger $USER`; and, when it wrote Codex hooks, approving them in Codex
   `/hooks`. Run them.

5. **Verify.** The first round captures the backlog; wait for its `round cause=start` line in
   `~/Library/Logs/memory/daemon.log` (macOS) or `journalctl --user -u memory` (Linux), with no
   `WARN` or `ERROR`. Then `memory status` (local, instant) and `memory check` (fetches every
   piece; exit 0 with `mismatch=0` and `remote_only=0`).

**Upgrade:** build the new commit, copy the current binary aside, swap the new one in as in step
1 and restart (`launchctl kickstart -k gui/$(id -u)/memory` or
`systemctl --user restart memory.service`); the hook commands name the stable path, so Codex's
hook approval stays valid. On Linux repeat the GC-root step if `ldd` names new store paths.
**Remove:** `memory uninstall`, then the `next:` lines it prints; the archive is never touched.

**Analytics** read the archive through the binary, never the bucket directly:

```sh
memory owners > owners.txt                 # session owners, one per line
memory owners --shared > shared.txt        # shared owners (Pi subagent run records)
memory export "<owner>" --out exports/<n>  # one directory per owner
py=.analytics-venv/bin/python; db=.analytics/publications.duckdb
$py tools/analytics/publications.py import exports/<n> --database $db
$py tools/analytics/publications.py import-shared exports/<n> --database $db
$py tools/analytics/publications.py report --database $db --summary
```

Imports are idempotent (`unchanged` on a repeat). The tables and views are described in
[tools/analytics/README.md](../tools/analytics/README.md).

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
