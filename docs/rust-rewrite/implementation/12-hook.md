# 12 Hook worker report (slice 2)

Scope: `memory hook <agent>` in `rust/crates/memory/src/{main.rs,hook.rs}`, tests in `rust/crates/memory/tests/hook.rs`. Dated 2026-11-09. Uncommitted. Synthetic data in temp dirs only; no real hook configs, state or transcripts touched.

## Design decisions

- **Argv** (DESIGN 10.1): hand-parsed. `memory hook <agent>`; agent is `claude|codex|pi`. Unknown agent, missing agent or any failure exits 0 with no write. Any other subcommand keeps exit 78. No clap, no tokio.
- **Core**: `hook::run(&HookCall) -> HookResult`. `HookResult` is a closed enum (11 variants, fixed `code()` strings, no input text). The wrapper `main_entry` always returns `ExitCode::SUCCESS` and prints nothing (stdout and stderr both).
- **Day name**: passed in as `today`. The wrapper computes UTC from `SystemTime` with Hinnant civil-from-days (`utc_day`), with no dependency. UTC was chosen so the file name does not depend on the TZ environment or on DST. `is_day_name` accepts only `DDDD-DD-DD`, so the name cannot escape the directory.
- **Stdin** (Go `hook.go`: 1 MiB bound, decode one value, 2 s timeout): `read_bounded` stops at the end of the first top-level JSON object, at EOF or read error, at a first byte that cannot start an object, or at 1 MiB + 1 bytes (the parser then reports `TooLarge`). So a parent that sends a full object and leaves stdin open does not delay the hook. For a parent that sends nothing or an incomplete object, the wrapper reads in a thread and waits with `recv_timeout(2 s)`. On timeout nothing is written, and the blocked reader dies with the process. A terminal stdin is treated as empty.
- **Directory** (DESIGN 9.1): created recursively 0700 if missing. Refused (exit 0, no write) if it is not a directory, is a symlink (`symlink_metadata`), or has any group/world write bit.
- **File**: `OpenOptions` append + create, mode 0600. The line is written with **one `write` call**, not `write_all`. A short write is `ShortWrite` and a failed write is `WriteFailed`. Neither retries. `write_line` is public so tests inject the sink and assert exactly one call.
- **Launch evidence**: attached only for `SessionStart`, because DESIGN 7.6 and 8.4 define launch evidence as the session-start record. It is built with `sanitize_launch` and omitted when the allowlist leaves nothing. The environment is inherited from the agent. The hook's own argv is `memory hook <agent>`, so the **agent's argv is not passed** (`argv: &[]`). An ancestor walk like Go's does not fit the 1 ms budget. This is a limitation, listed below.
- **State dir**: `MEMORY_STATE_DIR` if set, otherwise `$HOME/.local/state/memory`, plus `/hints`. The path is my assumption: DESIGN names `state/hints` but not the root. The parent should confirm it or give me the config rule.

## Disagreements and limitations

1. **Ownership check is indirect.** `unsafe` is forbidden and std has no `geteuid`, so the hook compares the uid of the day file it just opened with the uid of the directory. This catches a directory owned by someone else only if the file ends up owned by someone else, which is not guaranteed. It is not the DESIGN 9.1 "owned by the user" check. The daemon should do the strict check. A real fix needs `rustix` or `libc`, or a dependency-free trick such as comparing against the owner of the config file. Not done because there are no new dependencies.
2. Agent argv is never recorded (see above), so launch records currently carry the environment allowlist only.
3. `ulimit -f` does not bound `O_APPEND` writes on this macOS (I measured it: a 500-byte file grew to 750 under a 512-byte limit). The ENOSPC and short-write paths are therefore tested through the injected sink, not a real full disk.
4. The ValueEnd scanner is not a validator. A malformed object that closes early is passed to `parse_hook_input`, which rejects it.
5. Coverage was not run. Branches are exercised by the tests below, but the 98% floor is unverified.

## Tests (`tests/hook.rs`, 26 run, 2 ignored)

Core: golden line per agent, a second line appended, launch evidence only on SessionStart, SessionStart with no evidence, an oversize launch reduced within 4096 bytes, bad agent, date and input (no directory created), group/world-writable dir refused (4 modes), non-directory, symlink and parent-is-file, unreadable parent, read-only dir and a directory squatting on the day-file name, creation modes 0700/0600, result codes distinct, `utc_day` known values (including leap days), `read_bounded` end-of-value and cap/error cases, and `write_line` short/failed write with exactly one call.

Binary (spawned with `CARGO_BIN_EXE_memory`): all three agents with modes asserted, 24 concurrent processes appending whole lines that `hint::scan` recovers (0 dropped, 0 pending), writable dir refused with no file, a table of failure paths (exit 0, empty stdout, short stderr), missing state env, other subcommands exit 78, stdin left open and incomplete (exit 0 within 4.5 s, nothing written), a complete object with stdin open (under 1.5 s), and canaries in argv, env and prompt (independent byte search of the file, and of stderr).

Properties over arbitrary stdin bytes (and shaped payloads, launch args): one Hegel and one proptest. Each asserts no panic, at most one line, at most 4096 bytes, newline-terminated, `scan`-parseable, and no bytes written on a non-`Appended` outcome.

Ignored measurements: `p4_core_latency`, `p4_spawn_latency`.

## P4 (synthetic local measurement, not slice-2 acceptance)

Apple M2, macOS 27.0.1 (Darwin 27.0.0), release build, temp dir on the local disk.

```
cd /Users/mm/dev/memory && nix develop --max-jobs 2 --cores 2 --command sh -c 'cd rust && export CARGO_TARGET_DIR=target/w-hook CARGO_BUILD_JOBS=2 && cargo test --release --locked -p memory --test hook -- --ignored --nocapture --test-threads=1 p4_'
```

- In-process `read_bounded` + `run` (everything but process start and the stdin thread), n=2000: **p50 30.5 µs, p99 75.7 µs, max 1.03 ms**. The 1 ms p99 target is met. Max is a single outlier.
- Spawn latency, 300 runs each, informational: hook p50 2.60 ms / p99 4.15 ms; `/usr/bin/true` p50 1.19 ms / p99 1.33 ms. Process start dominates. The main-entry-to-exit figure above excludes the stdin thread spawn and the process environment scan, so the full main-to-exit time is slightly higher and has not been measured separately.
- Max RSS (`/usr/bin/time -l` on the release binary, 3 runs): 2,654,208 bytes (about 2.5 MiB) each.

## Commands and exit statuses

- `rustfmt --edition 2024 --check` on the three Rust files: 0.
- `cargo clippy -p memory --all-targets --locked -- -Dwarnings`: 0.
- `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory --test hook -- --test-threads=2`: 0 (cargo's own status, captured from a redirected run), `26 passed; 0 failed; 2 ignored`. An earlier run failed in a test that depended on `ulimit -f`. It was replaced by the injected-sink test (limitation 3), and no assertion was weakened.
- Not run: `gates.sh`, coverage, `cargo deny`, live ingestion, the workspace-wide tests.

## sha256

```
1273cfa6a7f8c80c30645ca52ae9105268e068824fc60f571c7c9db9b5df274d  rust/crates/memory/src/main.rs
30525ec1196a56cbac5e86cd9211fd9a8acebd557cbe4b7a5c8534431a6b549b  rust/crates/memory/src/hook.rs
80d2bf7a2b1bf83873b35c8bcdb9d0dbf4f3d12c21084cc9bc6f46103bdb4dd8  rust/crates/memory/tests/hook.rs
```

## Release

I release `rust/crates/memory/src/main.rs`, `src/hook.rs`, `tests/hook.rs` and this file.

## Parent review and correction 1 (2026-11-09)

- Integrated gate 5, attempt 1 (seed 730205), failed only `08-coverage`, at 96.9923%. The
  spawned-binary tests used `env_clear()`, which removed `LLVM_PROFILE_FILE`, so the
  subprocess coverage was lost. The parent added `profile_env()` to `tests/hook.rs`, which
  passes only `LLVM_PROFILE_FILE` into the 5 spawns. Every other variable is still cleared
  and no assertion changed. The `tests/hook.rs` hash above is superseded by
  `d21e5266d61f30febd66fd2f23d3bb4f597d7f7d76024af988c131a69b1a4d79`.
- Ownership check: DESIGN 9.1 gives the strict "not owned by its user" refusal to the
  **daemon**. The hook's comparison of directory owner and file owner is accepted as
  defense in depth. The daemon check is still open (slice 2 daemon).
- Launch argv: the hook records no agent argv. Go walks the process tree
  (`kern.procargs2` on macOS, `/proc/<pid>/cmdline` on Linux), and the Pi extension
  passes `--argv` itself (docs/hooks.md:47,119). DESIGN does not say how argv is obtained.
  This is open and is a regression from Go until it is decided.
- The state root `MEMORY_STATE_DIR` or `$HOME/.local/state/memory` is a worker assumption.
  DESIGN names only `state/`, so this is open until the install layout is fixed.

## Resolved 2026-11-09

Launch argv: dropped by operator decision ([amendment A3](../design-amendments-20261109.md)); the hook records env facts only. State root: `$HOME/.local/state/memory` on both hosts, overridden by `MEMORY_STATE_DIR` (A5). The daemon owner check is implemented in `StateDir::vet_hints_dir`.
