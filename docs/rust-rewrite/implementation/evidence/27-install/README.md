# Evidence 27: `memory install` and `memory uninstall` on macOS (brief 36)

Dates are UTC; the work ran on 2026-11-18 from 11:07Z to 11:43Z. Status: increment INSTALL accepted
locally as `f3b3deb` (5 commits over `d007bc4`). Nothing was installed into the real home
directory, sent to R2 or run against the acceptance run's state root, and no `launchctl` command
was run. The acceptance run `20261117-a28bfa4` kept running on its own release binary throughout.

[Brief 36](../../36-install.md) sits on `d007bc4` (on top of `main` `ae26e3e`).

## Parent probe before the brief

Turning on serde_json `preserve_order` for the workspace (the obvious way to keep key order in an
edited `settings.json`) reorders the three exported schemas (`rust/schemas/memory.*.json`, 609
lines each way) and fails `xtask::cli schema_cli_check_dispatches_and_rejects_unknown_commands`.
The probe worktree was removed. The installer therefore has its own ordered JSON type.

## Commits

`git log --reverse d007bc4..f3b3deb`. "Worker" is a delegated Sonnet session (medium), "parent"
the coordinating session.

| Commit | By | Change |
|---|---|---|
| `6a368f0` | worker | `memory/src/install/json.rs`: ordered JSON, duplicate keys and non-objects refused, serde_json-style pretty writer; round-trip tests and a property |
| `0087fd5` | worker | `install/merge.rs`: the exact "ours" rule, Go `replaceOurs`/`mergeHooks` semantics, the Codex top-level check |
| `4f0b635` | worker | `install/files.rs`: Pi extension and plist text, unchanged check, backup, temp-sibling write, uninstall rules |
| `d217979` | worker | `install.rs` entry, `main.rs` words; real-binary and hook round-trip tests |
| `f3b3deb` | parent | numbers keep their exact text; binary path as invoked and refused unless named `memory`; property without failure persistence and the `.proptest-regressions` file dropped |

### Parent changes in `f3b3deb`

- **Numbers.** The worker's type read numbers through `serde_json::Number`; without the
  `float_roundtrip` feature a 17-digit float can read back with a different last digit, so a
  rewrite could change a foreign value, and its property generated only quarter-step floats. The
  parser now validates the whole document as a `RawValue` (feature `raw_value`, already on) and
  splits each value's raw text by its first byte; a number is kept as its validated text. New test
  `json_numbers_keep_their_exact_text` (17+ digits, `1e400`, `-2.50E-3`, a 30-digit integer; bad
  forms refused). The property now generates numbers with up to 30 fraction digits and exponents.
- **Binary path.** The worker canonicalized `current_exe()`. A probe on this Mac showed that
  `current_exe()` on macOS returns the path as invoked through a symlink; canonicalizing would
  write a versioned target path into every hook command, and Codex's trust hash covers the command
  string, so each upgrade would void the Codex approval (the Go installer's note,
  `internal/install/install.go` `Command`). The path is now used as invoked. Install refuses a
  binary whose file name is not `memory` (`install_binary_name`, exit 2): its entries would not
  match the "ours" rule on a later install or uninstall. New test
  `bin_install_refuses_a_binary_not_named_memory`.
- The repository tracks no `.proptest-regressions` files; the worker's was removed and the
  property runs with `failure_persistence: None`, as the other proptest properties do.

### Worker deviations accepted

- A backup name that already exists gets a `-1`, `-2` suffix (two changes within one second).
- `install_io` (exit 1) for an I/O error; a missing or empty `HOME` is `state_root` (exit 2).
- Stricter than Go: `hooks` that is not an object, or one of our events holding a non-array, is
  `install_unparsable` and the file is left alone (Go would drop the value).
- Plist ownership on uninstall is a text match on `Label` and the first `ProgramArguments`
  string, not a plist parse.
- Uninstall of a missing file reports `unchanged`.
- The non-macOS branch (`launchd: skipped (not macOS)`) is untested on this Mac; hosted Linux
  runners run the real-binary tests through it.

## Teeth (parent)

Script in the parent's scratchpad (`install_teeth.py`); each tooth replaced exact anchors, ran
`tests/install.rs` and restored the files.

| Tooth | Caught by |
|---|---|
| ours matched by substring (`memory hook`) | `merge_ours_rule_is_exact`, `merge_acceptance_and_go_commands_are_never_touched` |
| ours appended instead of replaced in place | `merge_older_path_replaced_in_place` |
| foreign object keys sorted | `json_key_order_kept`, `merge_foreign_handlers_keep_their_positions_and_other_keys`, the round-trip tests, the property |
| backup skipped | `files_write_unchanged_backup_and_modes`, `bin_install_backs_up_a_changed_file_once_with_old_bytes_and_mode` |
| write without the unchanged check | `files_write_unchanged_backup_and_modes`, `files_pi_marker_decides_ownership`, `bin_install_writes_files_and_second_run_is_unchanged` |
| unparsable file treated as empty | `merge_unparsable_and_odd_shapes_are_refused`, `bin_install_unparsable_settings_refused_and_stops_later_files` |
| Pi marker check removed | `files_pi_marker_decides_ownership`, `bin_install_keeps_a_foreign_pi_extension` |
| `MEMORY_STATE_DIR` check removed | `bin_install_refuses_state_dir_override_and_missing_config` |
| binary name check removed (parent) | `bin_install_refuses_a_binary_not_named_memory` |
| numbers converted through a float (parent) | `json_numbers_keep_their_exact_text`, `json_round_trip_numbers`, the property |

10 of 10 caught.

## Copies of this Mac's agent settings (parent, 11:27Z)

The built binary, copied into a temp directory as `memory`, ran with `env -i HOME=<temp>` against
copies of `~/.claude/settings.json` and `~/.codex/hooks.json` and a config with `prefix = ""` and
a dummy token. Only structure and counts were printed.

- Before: `settings.json` 27 top-level keys, handlers acceptance 8, Go 8, other 11; `hooks.json`
  acceptance 6, Go 6, other 5. Neither file has a float.
- `--dry-run`: the four `wrote` lines and both `next:` lines; both files byte-identical after.
- Install: ours 8 and 6 added; top-level key order and every non-hook key unchanged; every foreign
  handler at the same (event, group, index).
- Second install: `unchanged` for all four files, no backup.
- Uninstall: both files byte-identical to the originals; plist and Pi extension removed.

## Checks on `f3b3deb`

`RUSTDOCFLAGS=-Dwarnings cargo doc --workspace --no-deps`: clean. `nix develop .#rust --command
sh -c 'cd rust && MEMORY_ANALYTICS_PYTHON=<repo>/.analytics-venv/bin/python PROPERTY_CASES=500 bash
scripts/gates.sh'`: all 14 sub-gates 0, seed 4123598146; 04-nextest 755 passed, 05-hegel 32,
06-proptest 34, 10-consumer 2; coverage 96.55 % (9,725 of 10,073 lines).

## Hosted CI (stack #169)

- `docs/brief-36-record`, Rust gates (macos-26), attempt 1: `02-clippy` exited 101 because the
  runner could not resolve `index.crates.io`; every other sub-gate 0. Rerun of the failed job:
  success. The same gates passed on `rust/install`.
- Post-merge CI on `8b095ff` (run 44307310632): attempts 1 and 2 failed `make crap` on macOS,
  `internal/syncer` hitting Go's 10-minute package limit (running test 9 s old, nothing hung).
  The merge changed no Go file; the package took 439 to 555 s on the five earlier `main` runs and
  263 s locally with the same flags. Attempt 3 got no runner (0 steps); attempt 4 passed it in
  446.6 s, leaving only the two accepted `govulncheck` failures. Recorded as an existing Go
  finding; the timeout is not raised.

## Limits

- macOS only (DESIGN 17 slice 3 owns Linux and systemd).
- Removing the Go hooks, the Go Pi extensions and the Go launchd agents stays with the Go
  binary's own uninstall commands at cutover; `memory install` leaves them in place.
- Codex trust still needs a person to approve the hooks in Codex `/hooks`.
- Not run: `cargo mutants` (operator decision), any install into the real home directory.
