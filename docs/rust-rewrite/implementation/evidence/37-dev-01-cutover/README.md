# Evidence 37: cutover on dev-01 (DESIGN 18, second host)

All dates are UTC, 2026-11-20, from about 21:00Z to 21:50Z. Status: dev-01 runs the Rust `memory`
daemon as a systemd user unit, built from `main` `769e689` (brief 44's Linux install). The Go
candidate on dev-01 is uninstalled and its files are retired. Operator, 2026-11-20: BC-01 is
replaced by dev-01; "do this for me" (the SSH access and the install).

No credential, transcript, identifier or endpoint is recorded here. Configs were read with values
masked or compared by hash.

## Access

- The dev-01 server refused `~/.ssh/dev-01` (`Permission denied (publickey)`). The VM is
  `vm-dev-01` in the resource group `rg-dev-01`; its admin user is `dev`.
- The public key derived from `~/.ssh/dev-01` (fingerprint `SHA256:LVA3PVxp…`) was appended to
  `/home/dev/.ssh/authorized_keys` with `az vm run-command invoke … RunShellScript` (the script
  appends only when the key is absent; 3 lines before, 4 after).
- SSH then works with `-o IdentityAgent=none`. A plain `ssh dev-01` routes signing through the
  1Password agent (`Host *` in the Mac's SSH config), which holds the key and waits for an approval
  in the app; this is a client-side matter and the SSH config was not changed.

## Host facts

- Ubuntu, kernel `7.0.0-1017-azure`, x86_64, 4 CPUs, 31 GiB memory, 48 GB free on `/`. Login shell
  fish (commands are sent to `bash -s`). Determinate Nix. `systemctl --user` is `running`;
  `Linger=yes` already; `fs.inotify.max_user_watches` 259,533.
- Sources before the install: Claude `projects/` 2,784 files (846 JSONL, 1.5 GB), Codex `sessions/`
  138 rollouts (450 MB), Pi `sessions/` 2,474 files (2,230 JSONL, 1.3 GB). Claude
  `cleanupPeriodDays` 3650.
- Codex `state_5.sqlite` lists 139 threads; 1 (2026-11) has no rollout file on disk. The large
  loss of handoff item 18 is specific to the Mac. A consistent copy of the five Codex databases
  (SQLite backup API, `integrity_check` ok) is in `~/.local/state/codex-preserved-20261120/`
  (0700, files 0600).
- Claude `file-history`: 30 session directories, 514 files, 9.5 MB.

## Build

- `git archive 769e689` shipped to `~/.cache/memory-build/769e689` on dev-01 and built there with
  `nix develop .#rust` (`cargo build --release --locked -p memory`, 1 min 41 s). Binary sha256
  prefix `b5f2dca35a05b564`, ELF x86-64, dynamically linked against Nix store glibc 2.44 and
  gcc-16.2.0-lib.
- Both store paths are pinned as GC roots in `~/.local/libexec/memory/gcroots/`, so a Nix garbage
  collection cannot remove the interpreter or `libgcc_s` from under the daemon.

## Go candidate removed (DESIGN 18 step 2)

- Backups (0600) in `~/.local/state/memory-cutover/backup/`: Claude `settings.json`, Codex
  `hooks.json` and `config.toml`, the Go Pi extension and its older backup, the Go unit and its
  `restart-backoff.conf` drop-in.
- The Go unit `agent-sessions-capture-experimental.service` was `inactive` and `disabled`; no Go
  process ran.
- `capture-v1 hooks --action uninstall` and `capture-v1 service --action uninstall` first refused
  with `capture paths must be clean absolute non-root paths`: `validateOptions` runs `SafePath` on
  `--executable` even for an uninstall, and the flag was empty. With `--config` and `--executable`
  given, both plans were read, then applied (exit 0). No `agent-sessions` handler is left; the
  other handlers are kept (Claude 11, Codex 5). The Go uninstall left the drop-in directory and two
  `….ts.agent-sessions-backup-<n>` files beside the Pi extensions; all three were moved into the
  backup.
- Not from nix-config or home-manager (no reference in either). The Go files (`~/.config/
  agent-sessions`, `~/.local/bin/agent-sessions`, `~/.local/libexec/agent-sessions-capture`,
  `~/.local/state/agent-sessions{,-capture,-locks,-release}`, 5.8 GB) were moved to
  `~/.local/state/agent-sessions-retired-20261120/`. `config.env` was deleted after checking that
  `token.toml` holds the same key pair (hash comparison).
- Opt-out records (step 3): the Go `exclusions` directory was empty.

## Production config and install

- `~/.local/state/memory/` (0700) with `config.toml` and `token.toml` (0600, created with
  `O_EXCL`). The archive id and machine id come from the Go `capture-runtime.json`, which equals
  the Go `machine-id` file; the archive id equals the Mac's (hash comparison), so both hosts write
  one archive. The endpoint and the key pair equal the Mac's (hash comparison). The three default
  roots, `bucket = "agent-sessions"`, `prefix = ""`. The Go config had no custom or enrichment
  roots.
- `~/.local/libexec/memory/memory` by an atomic rename. `memory install --dry-run` then
  `memory install` (21:27:19Z, exit 0): wrote `~/.config/systemd/user/memory.service`, 8 Claude and
  6 Codex `memory hook` handlers (each file backed up as `*.memory-backup-20261120T212719Z`) and
  `~/.pi/agent/extensions/memory.ts`. The printed `systemctl --user daemon-reload` and
  `enable --now memory.service` were run; linger was already on.

## First capture and verification (step 4 and 5)

- First round (`cause=start`), 21:27:21Z to 21:37:59Z: `landed=445 failed=0 parked=0`, every
  failure class 0, `deleted=0 absence_skipped=0`. Daemon RSS 98 MB just after it.
- `memory status`, exit 0: `sources total=4291 current=4291 behind=0`, `unknown_kind=48042`,
  `unknown_owner=0`, `deferred_snapshot=2177`, all failure classes 0. `index.json` lists 445
  owners.
- `memory check` 21:38:54Z to 21:45:33Z, exit 0: `owners=445 sources=4291 match=4291`; behind,
  unsettled, mismatch, uncaptured, withheld, remote_only, parked, duplicate, unreadable and
  store_error all 0; no stderr line.
- Restart: `systemctl --user restart memory.service` at 21:47:03Z; the old process exited 0
  (`ExecMainStatus=0`). The start round (21:48:09Z) had `landed=0 failed=0` and
  `audit_matched=4291 audit_mismatched=0`, so it resumed from its index.
- systemd reported `2.5G memory peak` for the old process. That is the cgroup figure, which counts
  page cache from reading 3 GB of sources; it is not the daemon's resident memory and is not
  recorded as a memory-bound result.
- No WARN or ERROR line in the journal.
- Watch: `memory-check-loop.service` (a transient user unit from `systemd-run`) runs the same
  `check-loop.sh` as the Mac, `memory check` every 12 h into
  `~/.local/state/memory-cutover/checks.log`. It does not survive a reboot.

## Not done here

- Hook delivery on dev-01: no hint has been observed yet; the six Codex `memory hook` entries need
  the operator's approval in Codex `/hooks` on dev-01.
- The 2-day criterion on dev-01 (slice 2b, A13.1), restore and export/import on dev-01's owners.
- The 48,042 `unknown_kind` files on dev-01 are not classified yet (the `Ignored` verdict, D54).
