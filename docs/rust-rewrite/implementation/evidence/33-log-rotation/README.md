# Evidence 33: the daemon rotates its own log when stderr is a file (brief 41)

All dates are UTC, 2026-11-20, from 02:43Z to 03:13Z. Status: increment ROTATE accepted locally at
`9f85a2b` (3 worker commits over `55d96a8`). Nothing was installed, sent to R2 or run against the
production state root or `~/Library/Logs/memory`; the production daemon (`1b4f981` build) kept
running throughout.

[Brief 41](../../41-log-rotation.md) sits on `55d96a8` (on top of `main` `2518aaa`). Operator
request, 2026-11-20: "how do we solve this? automated rotating? do it". launchd never rotates
`StandardErrorPath` and macOS `newsyslog` rules need root; the acceptance run's log grew about
330 KB a day.

## Commits

| Commit | By | Change |
|---|---|---|
| `054a6f3` | worker | `log/rotate.rs`: `Rotator` (first line decides: regular file with a resolvable path, else off for good), size check against the cap, device and inode check of the path against the descriptor, rename chain `.2`→`.3`, `.1`→`.2`, live→`.1`, new live file `create_new` + append, mode 0600; any failure turns rotation off. 5 unit tests |
| `91e879b` | worker | `log.rs`: a `Rotating` writer replaces `std::io::stderr` in `start()`; under one lock it checks, rotates, `rustix::stdio::dup2_stderr`s the new file, writes `log_rotated`, then the line. Path of fd 2: `rustix::fs::getpath` on macOS, `/proc/self/fd/2` on Linux, none elsewhere. `MEMORY_SIM_LOG_CAP` under `sim` only. `rustix` gains the `stdio` feature (no new crate, `Cargo.lock` unchanged). Two real-binary tests |
| `9f85a2b` | worker | `log.rs` module doc (rotation rule, the event), `run.rs` doc comment |

Cap 8 MiB, 3 generations: at most 32 MiB on disk. Plist, install and log grammar otherwise
unchanged; `log_rotated` has no keys.

### Worker deviations accepted

- `std::fs::rename` and `OpenOptions` (`create_new`, `append`, mode 0600; std adds `O_CLOEXEC`)
  instead of rustix `open`/`rename`; the flags are those of the brief.
- `log_lines::parse` made `pub`; `assert_clean_log` ignores `log_rotated` when checking event order.
- The real-binary test stops at the third rotation (`.3` appears) and asserts exactly three
  `log_rotated` lines and no `.4`; dropping the oldest generation on a fourth rotation is proven by
  the unit test `four_rotations_keep_three_generations_and_drop_the_oldest`.
- Rotated files are asserted to hold between the cap and cap + 512 bytes (log lines are shorter).

## Tests

- Unit (`log::rotate::tests`): `below_the_cap_nothing_happens`,
  `at_the_cap_the_file_moves_to_one_and_a_private_file_replaces_it`,
  `four_rotations_keep_three_generations_and_drop_the_oldest`,
  `a_replaced_path_disables_rotation_and_renames_nothing`, `a_pipe_is_never_rotated`.
- Real binary (`s3_store::event_loop`): `a_daemon_logging_to_a_file_rotates_it_into_three_generations`
  (stderr a file, cap 2,000 bytes; `.1` to `.3` exist, no `.4`; every line parses; each rotated-into
  file starts with `log_rotated`; the files in age order pass `assert_clean_log` from `started` to
  `stopped`), `a_daemon_logging_to_a_pipe_never_rotates`.

## Teeth (parent)

Script `rotate_teeth.py` in the parent's scratchpad; each tooth replaced an exact anchor, ran the
rotation tests and restored.

| Tooth | Caught by |
|---|---|
| rotation never triggers | three unit tests, the real-binary rotation test |
| generations not capped (`.4` kept) | `four_rotations_keep_three_generations_and_drop_the_oldest` |
| inode check removed | `a_replaced_path_disables_rotation_and_renames_nothing` |
| `dup2_stderr` skipped (old file keeps growing) | the real-binary rotation test |
| `log_rotated` not written | the real-binary rotation test |
| new file mode 0644 | `at_the_cap_the_file_moves_to_one_and_a_private_file_replaces_it` |

6 of 6 caught.

## Checks

- Worker: fmt, clippy `-Dwarnings`, `cargo doc`, `cargo deny check` ok; workspace nextest with the
  consumer gate: 858 passed.
- Parent, `9f85a2b`: `nix develop .#rust --command sh -c 'cd rust &&
  MEMORY_ANALYTICS_PYTHON=<repo>/.analytics-venv/bin/python PROPERTY_CASES=500 bash
  scripts/gates.sh'`: all 14 sub-gates 0, seed 181026750; 04-nextest 790 passed, 05-hegel 32,
  06-proptest 34, 10-consumer 2; coverage 96.27 % (10,280 of 10,678 lines).

## Hosted gates, a model finding, merge

- Stack #195 (PRs #192 to #194). First push: the code branch's Rust gates passed, but Hegel
  property 4 failed on the docs-only bottom branch (CI run 44666459304, ubuntu-24.04, seed
  1848172391): `1a violated: committed bytes were never held by the source`. Reproduced locally
  with that seed on `main`'s code and shrunk to a script. Cause, in the test model, not in
  capture: `RemoveSource` kept the in-memory file's `FileId`, so a file re-created with the same
  leading bytes looked like the old file edited between its windows (which only the audit
  certifies, DESIGN 11.4), and the store's spliced bytes were not in the model's history. On
  APFS and ext4 with statx a re-created file has a new identity. Fixed in PR #196 (`5d103aa`,
  merged as `4110ee1`): the model bumps the generation on removal; regression test
  `a_source_created_again_after_removal_is_verified_as_a_new_file` fails without the change.
  `binary(capture_property)` at 500 cases passed for seeds 1848172391 and 2026112001. One macOS
  coverage job of #196 failed fetching from cache.nixos.org (no test ran) and passed on rerun.
- The stack was rebased on `4110ee1` and pushed again: Rust gates green on all three branches;
  CI failed only in the two `govulncheck` steps. Merged as `a47eb19`.

## Installed on this Mac

- 2026-11-20 05:30:33Z: release build of `a47eb19` (sha256 prefix `7c754c88442be51f`) put in
  place at `~/.local/libexec/memory/memory` by an atomic rename; the `1b4f981` binary is kept as
  `~/.local/state/memory-cutover/backup/memory-1b4f981`. `launchctl kickstart -k`: `runs = 4`.
- Start round (05:37:01Z): landed 1, failed 1 (`offline`), parked 0. The one `retry` line was
  `class=offline cause=io landing=no attempt=1 elapsed_ms=45493 after_ms=1000`; the owner came
  back `unchanged` on its next capture. No WARN or ERROR line.
- The live log is 41 KB, far below the cap, so no production rotation has happened yet; the
  rotation itself is proven by the real-binary test above.

## Limits

- No compression of rotated files and no time-based rotation (out of scope).
- Under systemd stderr goes to journald, which is never rotated by the daemon; journald's own
  limits apply (the Linux install brief).
- Not run: 2,000 property cases, `cargo mutants` (operator decision).
