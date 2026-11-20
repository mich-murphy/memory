# 42. Day-to-day commands: `memory status`, `memory unpark`, `memory reconcile` (2026-11-20)

Coordinator brief. The parent session owns this file and acceptance. It follows brief 41
([evidence 33](evidence/33-log-rotation/)). Evidence goes to `evidence/34-operations/`. Text that
states a plan stays as written; later changes are marked.

The production daemon (`launchd` label `memory`, `a47eb19` build) keeps running while this brief is
worked on. Nothing here touches `~/.local/state/memory*`, `~/Library/Logs/memory`, the installed
binary, the agents' settings or R2. Every test uses a temporary directory and the local S3 fake.

## Why

Operator, 2026-11-20: build the day-to-day commands. DESIGN 13 names the subcommands `status`,
`reconcile` and `unpark`; DESIGN 11.3 defines `status` in three tiers and `unpark <source|owner>`;
R1 says withheld and unattributed sources are counted and shown by `memory status`. Today
`main.rs` exits 78 for all three. A parked owner can be cleared only by editing a checksummed
state file, and the only health view is `memory check`, which fetches every piece (about 23
minutes on this Mac).

## Facts checked on `c986828`

| Fact | Where |
|---|---|
| `main.rs` dispatches `hook daemon check install uninstall export owners restore`; anything else exits 78 | `memory/src/main.rs:6-25` |
| `status.json` is `StatusBody { updated_at, rounds, host_paused, parked: Vec<ParkedEntry { owner, reason, source? }>, failures: FailureCounts, audit: AuditCounts }`, schema `memory.status/1`, `deny_unknown_fields` | `memory/src/daemon.rs:128-170, 186-215` |
| The daemon reads `status.json` once at start (`open`), keeps it in memory and rewrites it at most every 60 s (`INDEX_INTERVAL`) and at shutdown. A file edited beside a running daemon is overwritten | `memory/src/daemon.rs:53, 365-380, 1353-1378` |
| `index.json` is `IndexBody { sources: BTreeMap<SourceId, SourceIndex { index: Option<SourceState>, missing, snapshot, deletion_recorded }>, hints }`; `SourceState` holds `observed { id, size, mtime_ns, observed_at_ns }` and `position.captured_to`. Written at most every 60 s | `memory/src/daemon.rs:104-126`; `memory-core/src/domain.rs:471-480, 572-587` |
| `DaemonLock::acquire` takes `flock(daemon.lock, LOCK_EX|LOCK_NB)`; contention is `StateError::LockHeld`, exit 75 (`EXIT_LOCK_HELD`). `StateDir::prepare(&lock)` gives `read`/`write` of a `StateFile` (checksummed envelope, durable write) | `memory/src/state.rs:16-27, 146-188, 657, 861-870` |
| `memory check` is read-only without the lock: `read_durable` of `status.json`, the walk the daemon uses (`WalkSources`, `counts()`), `policy_context` + `admit` for withheld, closed output (`owner_prefix`, kind and reason codes, never a path or source id), exits 0/1/2/3/64 | `memory/src/check.rs:1-60, 206-255, 508-687` |
| `start_from_config(root, config)` takes the lock and builds the production daemon; `reconcile_once()` runs one full round and returns a `RoundReport`; `shutdown()` flushes status and index and releases the lock; `set_stop` installs a stop probe; `crate::log::round` writes the `round` line | `memory/src/assemble.rs:158-197`; `memory/src/daemon.rs:634, 1101, 1372`; `memory/src/log.rs:300` |
| Owners are written and parsed in prefix form: `owner_prefix`, `parse_owner_prefix` | `memory-core/src/limits.rs:26-49, 90` |
| Signal stop wiring: `SignalSource::install` / `forward_to(StopHandle)`; `watch::inbox(n)` gives a `StopHandle` and a `StopFlag` | `memory/src/run.rs:100-145`; `memory/src/watch.rs:256-380` |
| Real-binary test pattern for a read-only command beside the S3 fake: `tests/s3_store/check.rs` (`run`, PUT count unchanged, state listing unchanged, no path / source id / content in stdout or stderr) | `memory/tests/s3_store/check.rs:20-110, 198-230` |

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| `status`: access | Read-only, like `check`: no lock, nothing created or written under the state root, **no network**. Reads `config.toml` (roots), `status.json` and `index.json` with `read_durable`, walks the roots with `WalkSources` and stats each source. No byte of any source is read |
| `status`: tier 1 (local view) | Per walked source, against its `index.json` entry: append sources with the same `FileId` and `size > captured_to` are `behind`; `size == captured_to` is `current`; a different `FileId` or `size < captured_to` is `changed`; no index entry (or no append state) is `unindexed`. Snapshot sources: `current` when the stored `Plain` fingerprint equals the stat, else `changed` (a `Sqlite` fingerprint compares the database stat only). `oldest_behind_s`: the largest `now − observed_at_ns` over `behind` and `changed` sources (time since the change could first have been missed), 0 when none. Also the walk counts (`unknown_kind unknown_owner duplicate deferred_snapshot unreadable`) and `withheld` (by `admit`, as `check` does, no read) |
| `status`: tier 2 | From `status.json`: `updated_at`, its age in seconds, `rounds`, `host_paused`, the failure counts, the audit counts, and one line per parked entry: `parked owner=<prefix> reason=<code> scope=<owner|source>` (as `check` prints it) |
| `status`: tier 3 `--verify` | Not built here: `memory check` is the verified view (it fetches every piece). `status --verify` exits 64 (`usage`). DESIGN 11.3 is amended to say so (D51) |
| `status`: output and exit | First line `status as_of=<updated_at> age_s=<n> rounds=<n> host_paused=<bool> state=<loaded|missing|damaged|newer_schema> index=<same codes>`; second line `sources ...=<n>` with the tier-1 and walk counts; third line `failures ...` and `audit ...`; then the parked lines; never "all captured", never a path, a source id or content. Exit 0; 1 when anything is parked, the host is paused, or `status.json` is not `loaded`; 2 config refusal; 64 usage. The index is up to 60 s old by design, so a source captured in the last minute may show as `behind`: the doc says so |
| `unpark`: why the lock | The daemon is the only writer of `status.json` and reads it only at start, so an edit beside it is overwritten and never seen. `unpark` takes the daemon lock and refuses beside a running daemon (`memory: lock_held`, exit 75), the same rule DESIGN 13 sets for `reconcile`. The operator stops the daemon (`launchctl bootout gui/$(id -u)/memory`), unparks, and starts it again (`launchctl bootstrap …`); the doc gives the three lines |
| `unpark`: arguments | `memory unpark <owner-prefix>` removes every parked entry of that owner (owner-wide and per-source; source ids are never printed anywhere, so they are not an argument). `memory unpark --all` removes every entry. Anything else: `usage`, exit 64; an unparsable prefix: `owner`, exit 64 |
| `unpark`: write | Read `status.json` through `StateDir` under the lock; `Missing`: nothing parked (exit 1, `memory: not_parked`); `Damaged` or `NewerSchema`: refuse, file untouched (`memory: status_damaged` / `memory: status_newer_schema`, exit 2). Otherwise remove the matching entries, keep every other field byte-for-byte equal in value, write with `StateDir::write`. Output `unparked owner=<prefix> entries=<n>` per owner (or one line per owner for `--all`); no match: exit 1 `memory: not_parked` and the file untouched. No network |
| `reconcile` | `memory reconcile` (no arguments): SIGTERM/SIGINT handlers first, then `start_from_config` (the lock: beside a daemon it refuses with exit 75, `memory: lock_held`), the log subscriber to stderr, one `reconcile_once()` with the stop probe, `crate::log::round` with a new `Cause::Manual` rendered `manual`, `shutdown()`. Stdout: one line `reconcile landed=<n> failed=<n> parked=<n> skipped=<n> deferred=<n>`. Exit 0 when `failed == 0 && parked == 0`, 1 otherwise, 2 config, 75 lock held, 130 when stopped by a signal before the round ended |
| Grammar | The `round` line's `cause` gains `manual`; `log.rs` table and `log_lines.rs` learn it. No other new log event |
| Out of scope | `status --verify` (above), deletions (slice 3, the next brief), the Linux install, unparking a single source |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | OPS | `rust/ops-commands` | worker (Sonnet, medium) |

Owns new `memory/src/status.rs`, `memory/src/unpark.rs`, `memory/src/reconcile.rs`, `main.rs`,
`lib.rs`, the `Cause` enum in `driver.rs` and its rendering in `log.rs` (only for `manual`), small
`pub` accessors in `daemon.rs` or `check.rs` if a shared helper is needed (prefer moving
`read_status` / `code_of` to a shared place over copying), `tests/common/log_lines.rs`, a new
`tests/s3_store/ops.rs` plus its `mod` line in `tests/s3_store.rs`, `docs/install.md` (an
"Operations" section). Read-only everywhere else.

Ordered steps, one commit each; `cargo fmt`, clippy `-Dwarnings` and the focused tests green before
each commit:

1. `status`. Real-binary tests beside the fake (pattern of `s3_store/check.rs`): after a capture,
   all `current`, exit 0; an appended transcript is `behind` with `oldest_behind_s` ≥ 0; a file
   never captured is `unindexed`; a parked entry gives a `parked` line and exit 1; a missing
   `status.json` gives `state=missing` and exit 1; `--verify` exits 64. Every test asserts: zero
   requests to the fake (any method), the state directory listing unchanged, and no path, source
   id, session id or content marker in stdout or stderr.
2. `unpark`. Tests: with a `DaemonLock` held by the test, exit 75 and `status.json` bytes
   unchanged; two parked owners, `unpark <one>` leaves the other and keeps `rounds`, `failures` and
   `audit` equal; `--all` empties the list; an owner with no entry exits 1 with the file bytes
   unchanged; a damaged `status.json` (a flipped byte) exits 2 with the bytes unchanged; bad
   arguments exit 64. A daemon started after `unpark` captures the formerly parked owner.
3. `reconcile`. Tests: with a `DaemonLock` held, exit 75 and no request to the fake; a new file is
   captured (`landed=1`, exit 0) and `restore` returns its bytes; a second run against the
   unchanged disk lands nothing and makes zero PUTs (DESIGN 14 idempotence); stderr lines parse
   with `log_lines::parse` and the `round` line has `cause=manual`; an unreachable store gives
   `failed=1`, exit 1.
4. Docs: module docs, `main.rs` doc, `docs/install.md` "Operations" (status, the stop / unpark /
   start sequence for launchd, reconcile).

## Rules for the worker

- Cargo only via `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && …'`,
  one cargo job at a time. Combine nextest `-E` conditions into one expression.
- No `unsafe`. No new crate. Never touch `~/.local/state/memory*`, `~/Library/Logs`,
  `~/Library/LaunchAgents`, agent settings, the installed binary, `.env` or R2. A production daemon
  runs on this Mac: do not inspect, signal or run any command against its state root. Every test
  sets `MEMORY_STATE_DIR` to a temporary directory and clears the environment. No `git stash`.
  Never commit `.proptest-regressions`.
- No gate is loosened: no timeout raised, no test deleted or weakened.
- Report: commits, deviations with reasons, exact commands with test counts, `git status --short`.

## Acceptance (parent)

Read the whole diff. Teeth: `status` takes the lock or writes a file; `status` sends a request;
`status` prints a source id; `behind` and `current` swapped; `unpark` without the lock; `unpark`
writes on no match; `unpark` overwrites a damaged file; `unpark` drops the counters; `reconcile`
without the lock check (a second instance runs); `reconcile` exit 0 on a failure. Then the full
gates once, evidence 34, DESIGN 11.3, 13 and D51, the handoff, push, hosted gates, merge, install in
place and run `memory status` against production read-only.

## Ledger

- 2026-11-20 06:20Z: brief written on `c986828`.
- 2026-11-20 07:10Z: OPS accepted locally at `4d5e1d6` (worker 5 commits, parent 1: tests for
  `status` beside a held lock and `reconcile` exit 130 on SIGTERM, which the worker had left
  unproven). Teeth 13 of 13. Full gates on `4d5e1d6`: 14 sub-gates 0, seed 4231285103, coverage
  96.00 %. Record: [evidence 34](evidence/34-operations/). Next: push, hosted gates, merge,
  install in place, `memory status` against production.
- 2026-11-20 07:35Z: stack #201 merged as `1c5618b`; installed on this Mac at 07:25:20Z
  (`runs = 5`); `memory status` against production exit 0, 6,051 of 6,051 sources current,
  nothing parked. Brief closed.
