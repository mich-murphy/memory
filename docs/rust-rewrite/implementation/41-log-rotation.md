# 41. The daemon rotates its own log when stderr is a file (2026-11-20)

Coordinator brief. The parent session owns this file and acceptance. It follows brief 40
([evidence 32](evidence/32-store-causes/)). Evidence goes to `evidence/33-log-rotation/`. Text that
states a plan stays as written; later changes are marked.

The production daemon (`launchd` label `memory`, `1b4f981` build) keeps running while this brief is
worked on. Nothing here touches `~/.local/state/memory*`, `~/Library/Logs/memory`, the installed
binary, the agents' settings or R2. Every test uses a temporary directory.

## Why

Operator, 2026-11-20, on "`~/Library/Logs/memory/daemon.log` is never rotated": "how do we solve
this? automated rotating? do it".

DESIGN 11.5 says "the supervisor owns retention". That holds for systemd (stderr goes to journald,
which has its own limits) but not for launchd: `StandardErrorPath` is opened once by launchd and
never rotated, and macOS `newsyslog` rules need root (`/etc/newsyslog.d`). The acceptance run's log
grew 707,551 bytes in 52 hours (about 330 KB a day, about 120 MB a year), and dev-01 will be busier.

## Facts checked on `2518aaa`

| Fact | Where |
|---|---|
| The subscriber writes to `std::io::stderr` through `tracing_subscriber::fmt().with_writer(...)`; `start()` installs it once, after every startup refusal | `memory/src/log.rs:143-163`; `memory/src/run.rs:229-233` |
| Every event is one `writeln!` from `Grammar::format_event`; a failed write is dropped silently (`log_internal_errors(false)`) | `memory/src/log.rs:116-156` |
| `refuse()` writes `memory: <code>` to `std::io::stderr` (fd 2), also after the subscriber starts | `memory/src/run.rs:184-187` |
| `memory install` writes the plist with `StandardErrorPath` `~/Library/Logs/memory/daemon.log` and `StandardOutPath` `/dev/null`; launchd created the file 0644 | `memory/src/install.rs:77`; `install/files.rs:95-118` |
| `rustix` 1.1.6 is a direct dependency with features `std`, `fs`. `rustix::fs::getpath` (`fcntl(F_GETPATH)`) exists on Apple targets with `alloc` (enabled by `std`). `rustix::stdio::dup2_stderr` is a safe wrapper behind the `stdio` feature (no new crate) | `rust/Cargo.toml:30`; `rustix-1.1.6/src/fs/{mod.rs:31-32,getpath.rs}`, `src/stdio.rs:512-520`, `src/lib.rs:289-292` |
| `unsafe_code = "forbid"` workspace-wide | `rust/Cargo.toml:48-49` |
| The `sim` feature already gates a test-only environment override (`MEMORY_SIM_TIMING`); the release binary never reads it | `memory/src/run.rs:151-182`; `memory/Cargo.toml:32-35` |
| The log grammar and its test parser: closed events and keys | `memory/src/log.rs:1-49`; `memory/tests/common/log_lines.rs` |

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Where | In the daemon: a new `MakeWriter` in `log.rs` (or a submodule `log/rotate.rs`) replaces `std::io::stderr` in `start()`. `render()` (tests) is unchanged |
| When it acts | Only when fd 2 is a regular file (`fstat`). A pipe, socket (journald), tty or `/dev/null` is never touched; the check result for "not a regular file" is cached for the life of the process |
| Trigger | Before writing a line, `fstat(fd 2)`; when its size is at least the cap, rotate, then write the line to the new file. Cap 8 MiB (`LOG_CAP_BYTES`); keep 3 rotated generations (`LOG_GENERATIONS`), so at most 32 MiB. A line is never split across files |
| Path | macOS: `rustix::fs::getpath(fd 2)`. Linux: `std::fs::read_link("/proc/self/fd/2")`. Other targets: no rotation. Before renaming, `stat(path)` must have the same device and inode as `fstat(fd 2)`; otherwise (the file was moved or replaced by someone else) rotation is disabled for the life of the process and writing continues on fd 2 |
| Rotation | Rename `path.2`→`path.3` (replacing), `path.1`→`path.2`, `path`→`path.1`; open `path` with `O_WRONLY|O_CREAT|O_EXCL|O_APPEND|O_CLOEXEC`, mode 0600; `rustix::stdio::dup2_stderr(new)`; drop the new fd. Then the first line in the new file is the event `log_rotated` (no keys), followed by the line that triggered it. `refuse()` and anything else writing fd 2 lands in the new file too. A launchd restart reopens `path`, which is the newest file |
| Failure | Any error during rotation leaves fd 2 as it was, disables rotation for the life of the process, and writes nothing about it (no foreign error text). Logging never fails because of rotation |
| Mutual exclusion | The check and rotation happen under one `Mutex` held while the line is written; the daemon logs from one thread, but the writer must not assume it |
| Tests override | Under `sim` only, `MEMORY_SIM_LOG_CAP=<bytes>` sets the cap (as `MEMORY_SIM_TIMING` does). The release binary never reads it |
| Dependency | `rustix` gains the `stdio` feature in `rust/Cargo.toml`; no new crate, no version change; `cargo deny` must stay clean |
| Grammar | New event `log_rotated` with no keys; `log.rs` module table, `log_lines.rs` `parse` and `assert_clean_log` (allowed anywhere after `started`) learn it |
| Plist, install | Unchanged |
| Out of scope | Compression of rotated files, time-based rotation, the systemd unit (brief for Linux install) |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | ROTATE | `rust/log-rotation` | worker (Sonnet, medium) |

Owns `memory/src/log.rs` (and a new `memory/src/log/rotate.rs` if wanted), the `rustix` feature line
in `rust/Cargo.toml` (and `Cargo.lock` if it changes), the `sim` override in `log.rs` or `run.rs`,
`tests/common/log_lines.rs`, and new tests. Read-only everywhere else.

Ordered steps, one commit each; `cargo fmt`, clippy `-Dwarnings` and the focused tests green before
each commit:

1. The rotation core as a function that takes the target fd and its path explicitly (so tests can
   pass a temp file's fd, not fd 2): size check, inode check, the rename chain, the new file.
   Unit tests in a temp dir: below the cap nothing happens; at the cap the chain is
   `path`, `path.1`; three more rotations keep exactly `path`, `path.1`..`path.3` with the oldest
   dropped; a replaced `path` (different inode) disables rotation and renames nothing; a
   non-regular target (a pipe) is never rotated; the new file is mode 0600.
2. The `MakeWriter` and `start()` wiring, `dup2_stderr`, the `log_rotated` event, the `sim` cap.
   Real-binary test (`s3_store/event_loop.rs` or a new file): spawn the daemon with stderr
   redirected to a file in a temp dir (`Stdio::from(File)`), `MEMORY_SIM_LOG_CAP` small (for
   example 2,000 bytes) and short timings, drive enough rounds to rotate at least four times, stop
   it. Assert: `daemon.log`, `.1`, `.2`, `.3` exist and no `.4`; each file except the newest is at
   least the cap and at most cap plus one line; every line of every file parses with
   `log_lines::parse`; each file after the first rotation starts with `log_rotated`; the
   concatenation in age order (`.3`, `.2`, `.1`, current) holds `started` first and `stopped` last,
   and `assert_clean_log` passes on it with `log_rotated` lines allowed. A second test with stderr
   piped (as the existing tests do) logs no `log_rotated` line.
3. Docs in code: `log.rs` module doc (rotation rule, the event), `run.rs` doc comment where it
   mentions stderr.

## Rules for the worker

- Cargo only via `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && …'`,
  one cargo job at a time. Combine nextest `-E` conditions into one expression.
- No `unsafe`. No new crate. Never touch `~/.local/state/memory*`, `~/Library/Logs`,
  `~/Library/LaunchAgents`, agent settings, the installed binary, `.env` or R2. A production daemon
  runs on this Mac: do not inspect or signal it. No `git stash`. Never commit `.proptest-regressions`.
- No gate is loosened: no timeout raised, no test deleted or weakened.
- Report: commits, deviations with reasons, exact commands with test counts, `git status --short`.

## Acceptance (parent)

Read the whole diff. Teeth: rotation never triggers; generations not capped (a `.4` appears); inode
check removed; `dup2_stderr` skipped (the old file keeps growing); `log_rotated` not written; mode
0644. Then the full gates once, evidence 33, DESIGN 11.5 and D50, the handoff, push, hosted gates,
merge, then install the build on this Mac in place.

## Ledger

- 2026-11-20 03:00Z: brief written on `2518aaa`.
