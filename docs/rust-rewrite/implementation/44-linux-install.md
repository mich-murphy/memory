# 44. `memory install` on Linux: a systemd user unit (2026-11-20)

Coordinator brief. The parent session owns this file and acceptance. It follows brief 43
([evidence 35](evidence/35-deletions/)). Evidence goes to `evidence/36-linux-install/`. Text that
states a plan stays as written; later changes are marked.

The production daemon (`launchd` label `memory`, `94230d2` build) keeps running while this brief is
worked on. Nothing here touches `~/.local/state/memory*`, `~/Library/Logs/memory`,
`~/Library/LaunchAgents`, the installed binary, the agents' settings or R2. Every test uses a
temporary `HOME`.

## Why

Operator, 2026-11-20: BC-01 is replaced by dev-01, the busier host, and "we need to build it then"
(the Linux install). Today `memory install` writes the launchd plist on macOS and prints
`launchd: skipped (not macOS)` elsewhere, so on Linux nothing supervises the daemon. Deploying on
dev-01 is out of scope here: the dev-01 server still refuses `~/.ssh/dev-01` (checked 10:17Z).

## Facts checked on `765a463`

| Fact | Where |
|---|---|
| `run_steps` writes the plist only under `cfg!(target_os = "macos")`, else prints `launchd: skipped (not macOS)`; then the Claude and Codex hooks and the Pi extension, which are platform-independent; `next:` lines are printed after | `memory/src/install.rs:68-104` |
| `plist`, `plist_is_ours`, `plist_file` (install writes, uninstall removes only our file, a foreign file is `Kept`), `write_file` (backup `<name>.memory-backup-<stamp>`, temp sibling, fsync, rename; new files 0644) | `memory/src/install/files.rs:95-150, 196-270, 325-358` |
| `install` refuses `MEMORY_STATE_DIR`, loads `~/.local/state/memory/config.toml`, requires an absolute exe path named `memory` | `memory/src/install.rs:112-156` |
| Tests: pure golden-text tests of the plist run on every OS; real-binary tests (`Home`) branch on `cfg!(target_os = "macos")`; `backups()` lists `Library/LaunchAgents` | `memory/tests/install.rs:537-600, 675-740, 770-790, 960-980` |
| The daemon logs to stderr; rotation acts only when fd 2 is a regular file, so a journald socket is never rotated (D50) | `memory/src/log.rs`, `memory/src/log/rotate.rs` |
| The daemon stops cleanly on SIGTERM/SIGINT (exit 0) | `memory/src/run.rs:100-145, 199-256` |
| Hosted Rust gates run on `ubuntu-24.04`, `ubuntu-24.04-arm` and `macos-26` | `.github/workflows/rust.yml:17-30` |

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Unit file path | `$XDG_CONFIG_HOME/systemd/user/memory.service` when `XDG_CONFIG_HOME` is set, non-empty and absolute; else `~/.config/systemd/user/memory.service` |
| Unit text | `files::systemd_unit(exe)`, pure and tested on every OS. First line `# Managed by memory install; memory uninstall removes it.` `[Unit]` `Description=memory: session capture`, `After=network-online.target`, `Wants=network-online.target`. `[Service]` `Type=simple`, `ExecStart=<quoted exe> daemon`, `Restart=on-failure`, `RestartSec=60`, `StandardOutput=null`, `StandardError=journal`. `[Install]` `WantedBy=default.target`. No `Environment=` line (no `MEMORY_STATE_DIR`) |
| Quoting of the exe | `ExecStart` takes the path in double quotes with `\` → `\\`, `"` → `\"`, `%` → `%%` and `$` → `$$`. A path containing a control character or a newline is refused (`install_binary_name`, exit 2), as today for a non-absolute path |
| Ownership | `files::unit_is_ours(text)`: our first line and an `ExecStart=` whose first word (unquoted) names a binary called `memory` followed by `daemon`. Uninstall removes only our file; a foreign file at the path is `Kept` |
| `run_steps` | `cfg!(target_os = "linux")`: `files::unit_file(path, install.then_some(exe), dry, now)`, same `Outcome`s and backups as the plist. Other non-macOS: `service: skipped (unsupported)`. The macOS branch is unchanged |
| `next:` lines (Linux) | Install: `systemctl --user daemon-reload`, `systemctl --user enable --now memory.service`, `loginctl enable-linger $USER` (a server reached over SSH stops the user manager at logout otherwise). Uninstall: `systemctl --user disable --now memory.service`, then `systemctl --user daemon-reload`. The binary does not run them |
| Logs | `journalctl --user -u memory`; journald's own limits apply |
| Tests on Linux | Real-binary install, reinstall (`unchanged`), uninstall and foreign-file tests assert the unit and the `next:` lines under `cfg!(target_os = "linux")`. A test runs `systemd-analyze verify` on the generated unit when the target is Linux; on Linux a missing `systemd-analyze` is a failure, not a skip (the hosted Ubuntu runners have it). If `verify` cannot run unprivileged on the runner, stop and report |
| Docs | `docs/install.md` "Operations": the systemd equivalents of the launchd stop / unpark / start sequence and `journalctl`; a "Linux (systemd user unit)" paragraph noting `fs.inotify.max_user_watches` as the first thing to check on a busy host |
| Out of scope | Deploying on dev-01, a system-wide unit, cross-compiled release artifacts |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | SYSTEMD | `rust/linux-install` | worker (Sonnet, medium) |

Owns `memory/src/install.rs`, `memory/src/install/files.rs`, `memory/tests/install.rs`,
`docs/install.md`. Read-only everywhere else.

Ordered steps, one commit each; `cargo fmt`, clippy `-Dwarnings` and the focused tests green before
each commit:

1. `systemd_unit`, the quoting, `unit_is_ours`, `unit_file`. Pure tests on every OS: a golden unit
   text; quoting of a path with a space, `"`, `\`, `%` and `$`; `unit_is_ours` true for ours, false
   for another binary name, another argument or a missing first line; `unit_file` install, reinstall
   `Unchanged`, a changed exe writes with a backup, uninstall removes ours and keeps a foreign file,
   `dry` writes nothing (all in a temp dir).
2. The Linux branch of `run_steps`, the refusal of a control character, the `next:` lines, the
   `backups()` list, the real-binary tests under `cfg!(target_os = "linux")`, the
   `systemd-analyze verify` test.
3. `docs/install.md`.

## Rules for the worker

- Cargo only via `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && …'`,
  one cargo job at a time. Combine nextest `-E` conditions into one expression.
- This Mac cannot run the Linux branch; keep the unit generation pure so it is tested here, and say
  which tests only the hosted Ubuntu gates run.
- No `unsafe`. No new crate. Never touch `~/.local/state/memory*`, `~/Library/Logs`,
  `~/Library/LaunchAgents`, `~/.config/systemd`, agent settings, the installed binary, `.env` or R2.
  A production daemon runs on this Mac: do not inspect, signal or run `memory install` against the
  real `HOME`. No `git stash`. Never commit `.proptest-regressions`.
- No gate is loosened: no timeout raised, no test deleted or weakened.
- Report: commits, deviations with reasons, exact commands with test counts, `git status --short`.

## Acceptance (parent)

Read the whole diff. Teeth: the unit lacks `daemon`; `%` not doubled; uninstall removes a foreign
unit; reinstall rewrites an unchanged unit; `next:` lacks `enable-linger`; the Linux branch skipped
(the hosted Ubuntu gates must catch it). Then the full gates once, the hosted gates (the Ubuntu jobs
are the Linux proof), evidence 36, DESIGN 13 and D53, the handoff, push, merge, install in place on
this Mac (no plist change expected: `memory install` reports `unchanged`).

## Ledger

- 2026-11-20 10:25Z: brief written on `765a463`.
- 2026-11-20 11:30Z: SYSTEMD accepted locally at `d278d23` (worker `f00ed33`, `2034c99`,
  `55d6219`; parent `d278d23` gives `systemd-analyze --user verify` a private runtime directory,
  found by running the Linux tests in a local Ubuntu 24.04 container). Teeth 10 of 10 (6 on Linux
  only, run in the container); gates 14/14, seed 2642077723, nextest 830, coverage 95.62 %.
  [Evidence 36](evidence/36-linux-install/). Next: hosted gates (Ubuntu jobs), merge, install in
  place on this Mac. Deployment on dev-01 waits for the SSH key.
- 2026-11-20 11:45Z: stack #211 merged as `ecc0489` (hosted Ubuntu x86_64 and arm64 gates green);
  installed on this Mac at 11:31:26Z (`runs = 7`); start round clean, `memory status` exit 0,
  6,053 of 6,053 current; `memory install --dry-run` reports every file `unchanged`. Closed except
  for the dev-01 deployment.
