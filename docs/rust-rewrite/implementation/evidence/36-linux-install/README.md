# Evidence 36: `memory install` on Linux writes a systemd user unit (brief 44)

All dates are UTC, 2026-11-20, from 10:25Z to about 11:30Z. Status: increment SYSTEMD accepted
locally at `d278d23` (3 worker commits and 1 parent commit over `0cbd701`). Nothing was installed,
sent to R2 or run against the production state root; the production daemon (`94230d2` build) kept
running throughout. Not deployed on dev-01: the dev-01 server still refuses `~/.ssh/dev-01`
(checked 10:17Z, `Permission denied (publickey)`).

[Brief 44](../../44-linux-install.md) sits on `0cbd701` (on top of `main` `765a463`). Operator
request, 2026-11-20: BC-01 is replaced by dev-01; build the Linux install.

## Commits

| Commit | By | Change |
|---|---|---|
| `f00ed33` | worker | `files::systemd_unit`, `systemd_quote` (`\`, `"`, `%`, `$` escaped inside double quotes), `unit_is_ours` (our first line and an `ExecStart=` naming an absolute `memory` followed by exactly `daemon`), `unit_file` (same outcomes and backups as the plist); pure tests on every OS |
| `2034c99` | worker | Linux branch of `run_steps`: unit under `$XDG_CONFIG_HOME/systemd/user/` (absolute only) or `~/.config/systemd/user/`; `next:` lines; a control character in the exe path is refused on Linux (`install_binary_name`, exit 2); other non-macOS prints `service: skipped (unsupported)`; real-binary tests under `cfg!(target_os = "linux")` and a `systemd-analyze --user verify` test |
| `55d6219` | worker | `docs/install.md`: the systemd stop / unpark / start sequence, a "Linux (systemd user unit)" paragraph, `journalctl --user -u memory`, `fs.inotify.max_user_watches` |
| `d278d23` | parent | The verify test gives `systemd-analyze --user` a private 0700 `XDG_RUNTIME_DIR`; without it verify fails with `Failed to lookup RuntimeDirectory path` |

### Worker deviations accepted

- No separate newline check: `char::is_control` already covers newline.
- Two extra Linux-only tests: an absolute and a relative `XDG_CONFIG_HOME`, and a foreign unit kept
  on uninstall.

## Linux proof on this Mac

The worker could only run macOS. The parent built a local container image (`ubuntu:24.04`, systemd
255.4-1ubuntu8.19, rustup Rust 1.99.0, an unprivileged user `dev`) on the Docker engine that runs on
this Mac (aarch64 Linux), and ran `cargo test --locked -p memory --test install` with the worktree
mounted read-only.

- On `55d6219`: 46 passed, 1 failed (`systemd_analyze_verify_accepts_the_generated_unit`:
  `Failed to lookup RuntimeDirectory path: No such device or address`). By hand in the container:
  `--user verify` with a private `XDG_RUNTIME_DIR` exits 0 and prints only
  `Failed to connect to system bus`, which names no unit; a `Type=bogus` unit also exits 0 but prints
  `memory.service:8: Failed to parse service type, ignoring`, so the test's check that the output
  names no unit is what catches it; a missing exe exits 1.
- On `d278d23`: 47 of 47 passed.

## Teeth

Each mutation was applied by a script, the install tests run, the file restored (`restored True`
both times).

On macOS (`cargo nextest run -p memory --test install`):

| Tooth | Result |
|---|---|
| `ExecStart` without `daemon` | caught (4 tests) |
| `%` not doubled | caught (`files_systemd_quote_escapes_and_round_trips`) |
| uninstall removes a foreign unit | caught (`files_unit_file_install_reinstall_change_uninstall_and_dry`) |
| reinstall rewrites an unchanged unit | caught (same test) |
| `next:` lacks `enable-linger` | survives on macOS (Linux-only branch) |
| Linux branch skipped | survives on macOS (Linux-only branch) |

In the Linux container (`cargo test --locked -p memory --test install`):

| Tooth | Result |
|---|---|
| `next:` lacks `enable-linger` | caught (`bin_install_writes_files_and_second_run_is_unchanged`) |
| Linux branch skipped | caught (4 tests) |
| `Type=bogus` in the unit | caught (golden test and `systemd_analyze_verify_accepts_the_generated_unit`) |
| control-character refusal removed | caught (`bin_install_refuses_a_control_character_in_the_path_on_linux`) |
| `XDG_CONFIG_HOME` ignored | caught (`bin_install_honours_absolute_xdg_config_home_on_linux`) |

10 of 10 teeth caught, 6 of them on Linux only.

## Gates

`MEMORY_ANALYTICS_PYTHON=… PROPERTY_CASES=500 bash scripts/gates.sh` on `d278d23`, macOS: all 14
sub-gates 0, seed 2642077723. nextest 830 passed (74 skipped by the gate filter), consumer 3,
coverage gate 899 passed, lines 95.62 % (floor 80). The hosted Ubuntu gates (x86_64 and arm64) are
the second Linux proof; see the PR.

## Limits

- Not deployed: dev-01 refuses the key. `systemctl --user enable --now`, linger and a real round
  under systemd have not run anywhere.
- Logs go to journald; the daemon's rotation (D50) does not act on a socket, and journald's limits
  apply.
- No system-wide unit and no cross-compiled release artifact (out of scope).
