# Evidence 20: the event loop (brief 29)

Dates are UTC; all work and runs here are from 2026-11-16. Status: accepted locally on
2026-11-16 as `969d6fc`; not yet pushed, not on hosted CI. No R2 run happened, and the daemon
still refuses an empty key prefix, so there is still no production write.

[Brief 29](../../29-event-loop.md) sits on `ef18965` (the brief, on top of `origin/main`
`94b8abd`, which is PR #114 on top of stack #111). The code stack `ef18965..969d6fc` is 34
commits on six branches. Seven checks and parent mutants were run per increment, then on the
combined tips; the full gates ran once on `969d6fc` (see "Full gates").

## Commits

`git log --reverse ef18965..969d6fc`. Branch tips are those of the final linear stack; several
increments were accepted on tips before a rebase (`backup/` branches hold them), and the hashes
below are the rebased ones. "Worker" is a delegated session, "parent" the coordinating session.

| Branch | Commit | By | Change |
|---|---|---|---|
| `docs/loop-brief` | `ef18965` | parent | brief 29 (written over `94b8abd`) |
| `rust/stop-flag` | `9a709a9` | worker | a stop flag in the watcher inbox that a full queue cannot drop |
| `rust/stop-flag` | `9dea33d` | worker, asked by parent | a read-only stop flag view of the inbox |
| `rust/stop-flag` | `7c31f6e` | parent | watcher test pins that the extra directory is watched non-recursively |
| `rust/owner-capture` | `672777b` | worker | per-owner capture, hint refresh and path-based owner sources |
| `rust/owner-capture` | `7487bff` | worker | tests for per-owner capture, hint refresh and `note`/`of` |
| `rust/owner-capture` | `074f889` | worker | tests: satisfy clippy in the resolve tests |
| `rust/owner-capture` | `4899d02` | second worker | a swept hint file leaves the daemon's scan and seen offsets |
| `rust/owner-capture` | `80fc4bd` | second worker | a per-owner capture refreshes the status stamp |
| `rust/owner-capture` | `ad8c203` | second worker | pin the depth boundary of `of` against the walk |
| `rust/owner-capture` | `d28ca14` | parent | daemon test pins that `next_retry` is the earliest deadline among owners |
| `rust/loop-driver` | `7e54ec1` | worker | `Coalescer::take_due`; `Scheduler` takes the path map per call, keeps no owner list |
| `rust/loop-driver` | `1cbdfda` | worker | scripted event-loop driver over two traits, with tests |
| `rust/loop-driver` | `b9125fb` | worker | ignored measurement of `Scheduler::handle` with 5,000 known owners |
| `rust/loop-driver` | `9ce0f90` | worker | measurement test avoids indexing |
| `rust/loop-driver` | `a6d3d1d` | worker, asked by parent | a retry the work leaves due is not a wake after it was offered |
| `rust/loop-driver` | `07821b3` | worker | name the retry tuple in the driver test |
| `rust/stop-in-round` | `696acac` | worker | split the commit into a stoppable part and a publish part |
| `rust/stop-in-round` | `7a0f6e6` | worker | the daemon asks a stop probe between owners and inside a commit |
| `rust/stop-in-round` | `6745fbb` | worker | pin the stop before `current.json` in the commit protocol tests |
| `rust/stop-in-round` | `44e4718` | worker | pin the stop in the daemon: owner, round and per-owner capture |
| `rust/stop-in-round` | `1bb2b6f` | worker | use `from_ref` in the stop tests |
| `rust/stop-in-round` | `564577b` | parent | a round cut short by a stop does not resume a paused host |
| `rust/hint-owner` | `b0ac599` | worker | a hint record's owner is the owner its transcript path locates |
| `rust/hint-owner` | `a3c631a` | worker | pin hint owners against the real locator and fixed sources |
| `rust/hint-owner` | `1bb1255` | worker | order the subagent hint test so the launch owner is pinned |
| `rust/hint-owner` | `4f104db` | worker | satisfy clippy and rustfmt in the hint owner change |
| `rust/hint-owner` | `d187108` | parent | resolve test: a subagent path alone gives a hint no owner and no signal |
| `rust/loop-wired` | `5b70021` | worker | run the event loop in the daemon binary |
| `rust/loop-wired` | `8bd5b3a` | worker | test the event loop on the real binary |
| `rust/loop-wired` | `fb83d73` | worker | pin the one clock origin against a creation gap |
| `rust/loop-wired` | `aca704a` | follow-up worker | a directory created in the hint directory is a hint change, not a reconcile |
| `rust/loop-wired` | `39f9659` | follow-up worker | pin the loop clock to the daemon's clock by equality |
| `rust/loop-wired` | `e6060d5` | follow-up worker | pin every exit code and closed code in the driver tests; document the capture log order |
| `rust/loop-wired` | `969d6fc` | parent | tests for the retry wake on the real binary and for a signal raised before forwarding starts |

`ef18965..969d6fc`: 27 files, +4698/-357 (`git diff --stat`). The rebased `rust/loop-wired`
commits (`5b70021`, `8bd5b3a`, `fb83d73`) were reported by the worker as `085f546` over `4f104db`
and rebased by the parent onto `d187108`, which touches only `tests/resolve.rs`. The first LOOP-1
worker's session could not be resumed, so a second worker took the parent's three review items;
the first LOOP-2b worker's session could not be resumed either, so a follow-up worker took the
review points.

## What the binary does now, beyond `94b8abd`

`memory daemon` (parent-verified against the code and the real-binary tests):

- Installs SIGTERM and SIGINT handlers before it takes the lock. It starts from `config.toml`
  with every earlier refusal unchanged, including `config_prefix_required`. It builds the
  watcher over the configured roots and, non-recursively, `state/hints`; on failure it writes
  `memory: watch_unavailable`, exits 1 and releases the lock. It then starts the signal thread
  and then the log.
- After the 5 s to 60 s startup delay it runs the start reconcile. No capture runs before it.
- Afterwards a changed file is captured for its owner only, once the coalescer window
  (`window = { quiet, cap }` in `config.toml`) says so. A hint line signals its owner; a
  session-end or interrupted hint flushes at once.
- Lost events, a watcher error, a new directory and a wake gap (more than 120 s between 60 s
  ticks) each ask for one full reconcile, however many arrive together. A full reconcile also
  runs every 15 minutes. A failed capture is retried at its own deadline, without a round.
- A stop is honoured between owners, between pieces, before the manifest and once before the
  publish part; never between the `current.json` write and its verification. A stopped capture
  changes nothing, has no retry deadline and is retried from the same base after a restart.
  Exit codes are unchanged.
- The log gains a leading `cause` key on `round` and a `capture` line (see "Log grammar").

## Acceptance per increment (parent)

The seven checks are fmt, clippy `-Dwarnings`, rustdoc `-Dwarnings`, nextest, doctests,
`cargo xtask schemas --check` and `cargo deny --locked check`. Every one was 0 on every tip
below. Mutants are in [`teeth.txt`](teeth.txt) with the failing tests and assertion lines. The
four mutation predicates each occurred once on the combined tips.

Source `path:line` references below are on `969d6fc`. A test line named for a mutant is the
line on the tip that mutant ran on (the headings of `teeth.txt` name each tip); later commits
moved some of those tests.

| Increment | Tip checked | Seven checks | Tests | Mutants |
|---|---|---|---|---|
| STOP-1a | `9dea33d` | 0 | 594 passed, 4 skipped, 12 doctests | 8; 7 killed, 1 survivor fixed by `7c31f6e` |
| LOOP-1 | `3b969c9` (before rebase) | 0 | 603 passed, 4 skipped, 12 doctests | 13; 11 killed, 2 survivors |
| LOOP-2a | `ef15093` (before rebase) | 0 | 616 passed, 5 skipped, 12 doctests | 10, all killed |
| Wave 1 combined | `07821b3` | 0 | 648 passed, 5 skipped, 12 doctests | predicates once each |
| STOP-1b and hint owner | `4f104db` | 0 | 665 passed, 5 skipped, 12 doctests | 12 (plus one rerun); 11 killed, 1 survivor fixed by `d187108` |
| LOOP-2b | `e6060d5` | 0 | 674 passed, 5 skipped, 12 doctests | 11 on `969d6fc`, all killed |

### STOP-1a

Adds `StopFlag` (`watch.rs:292`, `FsWatcher::stop_flag` at `:508`, `InboxRx::stop_flag` at `:377`)
and `recv_ignoring_stop` (`watch.rs:395`): the stop is a flag the receive reports before an
overflow and before queued inputs, so a full queue cannot drop it. The watcher also watches one
given directory non-recursively. Parent follow-up `9dea33d`: the view is read-only and is not
counted as a stop source, so a live flag does not hide a failed source.

Mutants: `flag-not-set`, `failed-silent`, `flag-counted` (`tests/watch.rs:367`),
`overflow-before-stop`, `dir-unwatched` (`:690`, `:701`), `missing-dir-accepted` (`:704`),
`forward-noop` (`:380`) were killed. `dir-recursive` survived the first run; the parent added a
watcher test in `7c31f6e` that pins the non-recursive watch, and the mutant was then killed at
`tests/watch.rs:712`.

### LOOP-1

Adds `Sources::note` and `Sources::of` (`daemon.rs:259`, `:263` defaults; `resolve.rs:387`,
`:411` for `WalkSources`), `Daemon::note_path` (`daemon.rs:1198`), `next_retry` (`:1203`),
`retries_due` (`:1214`), `refresh_hints` (`:1157`) and `capture_owner` (`:1232`). Review items
sent back after the first worker: a hint day file removed by the sweep stayed in the in-memory
scan and its offset entry was re-inserted by a later per-owner capture (defect reproduced
first; fixed in `4899d02`); `updated_at` was not refreshed by a per-owner capture (`80fc4bd`);
the depth rule of `of` was pinned on one side only (`ad8c203`).

Mutants: 11 killed on the intended assertion (for example `paused-ignored`,
`deferred-ignored` at `tests/daemon.rs:3575`, `sweep-keeps-scan` at `:3434`, `stamp-not-set` at
`:3460`, `symlinked-dir-passes` at `tests/resolve.rs:487`, `canon-ignored` at `:409`). Two
survived the first run: `next-retry-latest` (fixed by the parent's `d28ca14`, the test of
the earliest deadline) and `hint-paths-not-noted` (see the hint-owner defect below). No later
run kills `hint-paths-not-noted`; the paths a hint names are noted when the scan is built, and
`HintSignal.paths` (`daemon.rs:298`) has no production consumer.

Known differences accepted: `of` needs search permission only where the walk needs read;
`max_entries` truncation is not modelled by `of`; an owner whose files are all gone keeps its
`RetryAt`; `take_due` is linear in owners.

### LOOP-2a

Adds `src/driver.rs`: `drive` (`driver.rs:122`) over the `Inputs` and `Work` traits, `Cause`
(`:36`); `Coalescer::take_due` (`memory-core/src/coalescer.rs:189`); `Scheduler::handle` takes
the path map per call. A retry the work leaves due is not offered again as a wake in the same
turn (parent follow-up; otherwise the loop spins while the host is paused). Measured by the
worker (release, median of 7, ignored test): `Scheduler::handle` at 5,000 owners went from
224,346 ns to 29,362 ns per input.

Mutants, 10 of 10 killed: `retry-always-wake` (`tests/driver.rs:822`), `due-order-reversed`
(`memory-core/tests/core_boundaries.rs:257`), `hints-every-batch`, `shutdown-failed-always`,
`cause-last` (`tests/driver.rs:536`), `no-poll`, `quiet-strict`, `hints-not-fed`,
`dir-as-rescan`, `wait-failed-as-stop`.

### STOP-1b

Adds `commit_stoppable(store, base, request, known, pause, stop)` (`commit.rs:437`) with
`Failure::Stopped` (`commit.rs:169`), private `prepare` (stoppable) and `publish` (no stop
argument), `Daemon::set_stop(impl Fn() -> bool + 'static)` (`daemon.rs:622`),
`RoundReport.stopped` and `OwnerOutcome::Stopped`. Parent fix `564577b`: the stopped round
cleared `host_paused` and logged a false `host_resumed`; the test fails at
`tests/daemon.rs:4121` (on `564577b`) with the fix reverted.

Mutants (wave 2): `stop-outcome-failed`, `no-probe-before-publish`, `no-probe-before-manifest`,
`piece-probe-once`, `land-probe-false`, `outcome-not-stopped` (`tests/daemon.rs:4126`),
`cancelled-drops-base` (`:4137`), `round-stop-ignored` were killed on the intended tests.

### Hint owner

Described in the next section. Adds `hint_owner` (`daemon.rs:438`); `owner_of` and the daemon's
`config` field are removed; `PendingHint.owner` is `Option<Owner>`. Mutants `attach-any-owner`,
`done-any-owner` and `agent-unchecked` (`tests/resolve.rs:752`) were killed on the first run.
`sub-without-main` survived; the parent's `d187108` pins that a subagent path alone gives a hint
no owner and no signal, and the mutant was then killed at `tests/resolve.rs:754`. Fixtures
changed (hint lines gain a transcript path, fixed test sources gain `note`); no existing
assertion changed.

### LOOP-2b

Adds `src/wired.rs` (`WatchInputs`, `hint_dirs`, `route`, `DaemonWork`; `wired.rs:13-76`),
`main_entry` rewritten in `run.rs:199`, `SystemClock: PartialEq`, the `capture` log line, and
`tests/wired.rs` and `tests/s3_store/event_loop.rs`. 13 files, +814/-271 on the worker's
report; nothing changed in `daemon`, `driver`, `watch`, `commit`, `capture`, `resolve` or
`memory-core` in the first three commits. Follow-ups the parent sent back: log doc ordering
sentence for capture-driven lines; the origin test by equality; the loop-test map compared
assertion by assertion; and the hint-directory fix below.

Real-binary tests (`event_loop.rs`, local fake server, reconcile interval one hour): an
append is in the store within seconds and `memory restore` returns the file; a new file is
captured by the watcher alone; a session-end hint written by the real `memory hook` flushes
before `quiet`; SIGTERM during a held piece PUT gives one piece PUT, no manifest and no
`current.json` change, exit 0, and a restart uploads everything; an idle stop; the watcher
refusal; a failed capture (one 503) is retried without a round; loop clock equals the daemon's
clock. `tests/run.rs`: a signal raised before the forwarding thread starts is forwarded. The
last two were added by the parent in `969d6fc` (5 of 5 runs each; about 2.6 s and 0.07 s).

Mutants on `969d6fc`, 11 of 11 killed: `inputs-stop-timeout` (`wired.rs` test `:75`),
`inputs-failed-timeout` (`:86`), `next-retry-none` and `retries-due-empty` (retry test, 20 s
bound), `hints-dropped` (hint test, bound), `capture-not-logged` (5 event-loop tests),
`cause-fixed` (`event_loop.rs:55`), `shutdown-skipped` (`tests/harness.rs:186`, `:286`),
`hint-dir-not-dirty` (`tests/driver.rs:420`), `signal-dropped` (run test, bound),
`window-scaled` (4 event-loop tests, bound).

## The hint-owner defect

Found by the surviving LOOP-1 mutant `hint-paths-not-noted`. It was already on `main`
(`owner_of` in `daemon.rs`): a hint record's owner was built from the hook's raw `session_id`,
while the locator's owner for the same Claude transcript is the session
`<root namespace>:<slug>:<stem>` (`locator.rs`). With the real locator the two never match, so
a launch record never attached, its day file was kept for ever, and a hint signalled an owner
that has no sources. No test on `main` combined a hint with `WalkSources`: every hint test used
a fixed source list whose owner is the raw id. A parent test showed it (fails with left session
`new`, right `r1:-p:new`).

Fix (`rust/hint-owner`): a record's owner is `Sources::note` of its transcript path, only an
`Owner::Session` of the record's agent; the raw `session_id` never builds an owner; a subagent
path gives a second signal when it locates to a different session owner. An ownerless record
signals nothing and a launch record on it stays pending (file kept, counted in
`hints_kept_old`), with no fallback.

Open: ownerless launch records widen the existing "kept for ever" item (for example a Codex
session that is not persisted has no transcript path). Nothing here measures how many exist.

## The hint-directory event

Parent stress of `cargo nextest ... --test s3_store event_loop` on `fb83d73`: 30 runs, 3 failed
(`event_loop.rs:55`, `:97`, `:147`), each with a second full round `round cause=dir` right
after the start round. A temporary debug line in the driver (reverted) showed the only
`ReadDir` path was `state/hints`, 20 to 35 ms after start and always after the start round
(which has a zero startup delay in tests): 2 of 200 and 26 of about 240 daemon starts. The
platform's report is for the directory the daemon created just before the stream started; no
root directory ever appeared. This also explains one 0.07 s failure the worker saw. In
production the 5 to 60 s startup delay absorbs it, because the start reconcile clears the flag.

Fix `aca704a`: a `DirCreated` whose path classifies as `Route::Hints` marks the hints dirty and
sets no reconcile flag (`driver.rs:252`; test `tests/driver.rs:420`). The tests keep "exactly one
round".

| Runs | Tip | Result |
|---|---|---|
| 30 runs of `s3_store event_loop` | `fb83d73` | 3 failed of 30 |
| 50 x 4 tests, debug line | `fb83d73` + debug | 1 failed of 50 |
| 60 x 7 tests, debug line | `fb83d73` + debug | 12 failed of 60 (the debug line itself broke the log check) |
| 30 runs of 8 tests | `969d6fc` | 0 failed of 30; no log holds `cause=dir` |

The follow-up worker's own stress after `aca704a`: 60 runs, 0 failures.

## Log grammar and events

Unchanged otherwise. `round` gains a leading `cause=<start|timer|rescan|wake|dir>` (17 keys).
New: `capture owner=<owner prefix> result=<landed|unchanged|failed|stopped>`, one per
event-driven capture. A paused host, a parked owner and a deferred retry write no `capture`
line. No path and no source id appears in any line.

Order rule: in a full round `parked`, `host_paused` and `host_resumed` come before that
round's `round` line. In a per-owner capture they are logged inside `capture_owner`, so they
come before its `capture` line, and a capture that parks or pauses is `result=failed` unless
something landed. That order is by reading the code, not pinned by a test (the follow-up worker
documented it in `log.rs`).

There is no separate `wake_gap` event. `round cause=wake` is the record of a wake gap; this
replaces the `wake_gap` line the brief planned.

## Full gates

`scripts/gates.sh` on `969d6fc`, local, finished 2026-11-16 at 19:21 UTC: 500 cases, seed
1749603793, all 13 sub-gates 0 (`rustdoc -Dwarnings` was run by hand on each increment tip, not
by this run). Files are in [`gates/`](gates/): `status-969d6fc.tsv`, `seeds-969d6fc.txt` and
`summary-969d6fc.txt`, built as in [evidence 19](../19-production-prerequisites/README.md).

| Tip | Date | Seed | nextest | Hegel | proptest | Doctests | Gate 08 under coverage | Coverage |
|---|---|---|---|---|---|---|---|---|
| `969d6fc` | 2026-11-16 | 1749603793 | 616 | 31 | 29 | 12 | 676 passed, 5 skipped, 290 s | 7750/7957 = 97.40 % (floor 80) |

The `if-match`, `advance-on-fail`, `privacy-deny` and window mutants are killed. Not run: 2,000
cases, `cargo mutants`.

## Known limits

- Nothing here is pushed or on hosted CI. The real-binary event-loop tests have run on macOS
  only; Linux (inotify) runs first on hosted CI.
- The daemon still refuses an empty prefix, so there is no production write.
- A deleted file is never recorded as deleted: `WalkSources::enumerate` yields only files the
  walk finds and the absence rule runs only for sources given. Probe on `2edf2f8` (reverted,
  skeleton rig, fake server): first round landed 1; after the removal four more rounds each
  landed 0, failed 0, skipped 0, deferred 0, with no new PUT; the reader showed
  `deleted_at_set=[false]`. The daemon and capture tests pass because they use fixed source
  lists. Deletions are slice 3 (DESIGN 17).
- Commit pauses are zero. A capture holds the whole range in memory.
- A round does not end early when the network is down; the stop check bounds the stop, not the
  round.
- No `unpark`. Ownerless launch records keep their hint day file.
- The wake gap is tested on the driver only. `HintSignal.paths` has no production consumer.
- The order of `parked` or `host_paused` before the `capture` line is not pinned by a test.
- `AssembleError::Window` (`config_window` from `start_wired`) is unreachable after config
  validation. `signals_unavailable`, `state_root_unresolved` and `log_unavailable` are untested.
- `WalkSources::of` differs from the walk in permission and `max_entries` handling; `take_due`
  is linear in owners. A directory path under a root can be remembered by `WalkSources::note`
  when `locate` accepts it; `of` re-checks and yields nothing.
- With a zero startup delay (tests only) the platform's report of the hint directory's own
  creation arrives after the start round, which is why the driver treats it as a hint change.
- The wake stagger of DESIGN 7.1 is not built; DESIGN.md still describes it.
- Tick 60 s and batch 256 are not overridable.

## Not run

2,000 property cases, `cargo mutants`, anything against R2, hosted CI.
