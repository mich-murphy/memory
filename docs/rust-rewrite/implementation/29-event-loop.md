# 29. The event loop: per-owner capture, watcher and hints, a stop inside a round (2026-11-16)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 28](28-production-prerequisites.md) (logs, hint retention, the audit; merged as `d765f9c`)
and [evidence 19](evidence/19-production-prerequisites/). Status: written; no increment started.

## Goal

`memory daemon` today runs one full reconcile after a 5 s to 60 s startup delay and one every 15
minutes (`run.rs:124`, `run.rs:235`). A change is uploaded up to 15 minutes late, a sleep is not
noticed, and a stop waits for the round. This brief connects what already exists (the watcher,
the scheduler, the hint files) to one loop on one thread, so that:

- a changed session file is captured after the coalescer window (quiet 60 s, cap 300 s, DESIGN
  7.1), for that owner only;
- a hook hint signals its owner, and a session end flushes at once;
- a wake gap, a lost-event report and a new directory lead to a full reconcile (DESIGN 10.2, 10.3);
- a stop is never lost and is honoured between owners and between pieces, never between the
  `current.json` write and its verification.

After it, and not as part of it, the empty-prefix refusal (`assemble.rs:111`) is lifted and the
acceptance runs of DESIGN 17 start.

## Checkout

`main` is `94b8abd` (PR #114 on top of stack #111). The crates are unchanged since `2edf2f8`, so
line numbers below hold for both. Branches go on top of `docs/loop-brief` in the order of the
increment table; the parent creates each worktree.

## Facts checked on `94b8abd` before writing

A read-only planner scoped the work; the parent rechecked what the increments depend on. Paths
are under `rust/crates/`. "Probe" means a throwaway test the parent ran and reverted.

| Fact | Where |
|---|---|
| `watch.rs` and the coalescer have no production caller. `run.rs` and `assemble.rs` build no watcher and no scheduler | `rg` over `memory/src`: only two comments in `config.rs` |
| `Scheduler::handle` and `complete` exist and are property-tested; `Scheduler` owns its path-to-owner closure and has no "next deadline" accessor, though `Coalescer::next_due` exists | `watch.rs:70`, `:103`, `:137`; `memory-core/src/coalescer.rs:177` |
| `Scheduler` keeps every owner it has seen in a `Vec`, checks membership linearly and polls all of them on every input; the list is never pruned | `watch.rs:149-167` |
| A wake gap gives only `ReconcileAll`; nothing staggers queued captures | `watch.rs:117-126`; `coalescer.rs:211-225` |
| Nothing executes `Action::ReadDir`, and nothing produces `Input::Hint`. `HintEvent::flushes()` exists; a mapping from hint events to `Signal` does not | `watch.rs:54-66`; `memory-core/src/hint.rs:146` |
| The inbox drops an input when full and reports one `Rescan`; capacity 65,536 is a private constant | `watch.rs:223`, `:255`, `:264` |
| Configured roots are not canonicalised; the watcher tests canonicalise their temp root because FSEvents reports canonical paths | `roots.rs:49-75`; `tests/watch.rs:447` |
| The daemon has one entry, `reconcile_once`: walk every root, group by owner, run every owner. It resets `host_paused` at the start of every round | `daemon.rs:952-1029`, `:966` |
| `capture` looks only at the sources it is given, so a capture over part of an owner's sources counts nothing else as missing | `capture.rs:195-209`, `:326-348` |
| `scan_hints` reads every day file whole on every call; offsets advance only past records whose owner committed | `daemon.rs:597-646`, `:866-881` |
| A stop is seen only between rounds: the signal thread feeds a channel the loop reads in `wait` | `run.rs:124-143`, `:152-186` |
| In `commit_known` the pieces are put at `:423`, the manifest at `:428`, `current.json` at `:438-441`, and the verification runs at `:457` inside the retry loop `:436-497` | `commit.rs` |
| The shell never produces `Outcome::Aborted`; `settle` passes `cancel_requested = false`. The core's rule for it exists and is tested | `daemon.rs:654`; `memory-core/src/disposition.rs:131-134`; `core_limits.rs:235` |
| `Clock` has `monotonic_ns` and `wall_ns` and no sleep | `environment.rs:8-13` |
| A full walk is 0.4 s of stats at 24k files [measured, DESIGN 9.3]; the idle budget is "at most the listed timers (60 s tick, index flush when dirty)" (DESIGN 15 table) | `DESIGN.md:969`, `:1325` |

## Decisions the parent made where the design is silent

Each is reversible before the first production write. A worker that finds one of them wrong
reports it as a question and does not decide.

| Topic | Decision | Reason |
|---|---|---|
| Which sources an event capture covers | The owner's known paths, resolved again at capture time. `Sources` gains `note(&Path) -> Option<Owner>` (default `None`) and `of(&Owner) -> Vec<Source>` (default: `enumerate` filtered). `WalkSources` remembers, per owner, the paths of the last walk and the paths noted since, and resolves them with the same `to_source` the walk uses. `of` never yields what the walk would not yield at that moment: only a regular file, not a symlink, not under a symlinked directory, within the depth limit. A vanished file yields nothing | Facts are never older than the capture; the daemon tests' fixed lists keep working through the defaults; a retry needs the owner's sources without a walk |
| Paths the watcher reports | Matched against each configured root and its canonical spelling, computed once at start | FSEvents reports canonical paths |
| What a per-owner capture does besides capture | Skips when the host is paused or the owner is parked or not yet due; uses the pending launch records the daemon holds; advances hint offsets; logs park changes; writes status at once on an authoritative change and the index under the 60 s rule. It does not sweep hints, does not reset `host_paused` and does not count a round | The full round stays the re-probe (DESIGN 11.1) and the only sweeper |
| Hint records in memory | The daemon keeps its last hint scan and refreshes it when the hint directory changes and at the start of each full round. A per-file "seen" offset, in memory only, makes each record signal once | One reader; no day file is read once per capture; DESIGN 7.6 "the daemon keeps pending launch records by owner" |
| How the daemon notices a hint | The watcher also watches `state/hints` (not recursive). No poll | The idle budget names only the 60 s tick |
| Hint event to signal | `flushes()` gives `Signal::SessionEnd`, every other event `Signal::Stop`. The paths a hint names are noted if `locate` accepts them | DESIGN 7.1; `hint.rs:65` |
| `ReadDir` | Sets the reconcile flag. No per-directory walk yet | 0.4 s per walk; the per-directory read is needed for inotify (slice 3) |
| Reconcile flag | Level, not count: any number of causes, one reconcile, served when no capture is due | DESIGN 10.3 |
| Wake stagger | None. A wake gap sets the reconcile flag only | Captures are serial (brief 28), so there is no burst to spread. Deviation from DESIGN 7.1, to be folded |
| Scheduler and its closure | `handle` takes the path-to-owner function per call. The scheduler asks the coalescer for due owners and for the next deadline instead of keeping its own list | The function must borrow the daemon for one call only; the list grows for ever |
| Loop seam | A pure driver over two traits: one yields the next input or a timeout or a stop (real: the inbox; tests: a script that advances a fake clock), one does the work (real: the daemon; tests: a recorder). All deadlines are absolute monotonic instants; an input never moves the tick, the retry or the 15 minute deadline | Scripted tests without sleeping (DESIGN 12, simulation) |
| Order within one turn | Stop; drain inputs (bounded batch); captures that are due; retries that are due; the reconcile flag or timer; then wait for the earliest deadline | A stop is looked at between every two units of work |
| How a stop travels | An atomic flag set by the signal thread, plus a best-effort wake through the inbox. The receive reports stop before overflow before queued inputs | A flag cannot be dropped by a full queue |
| Where a commit can stop | `commit_known` is split into a stoppable part (pieces, manifest) and a part that takes no stop argument (`current.json` and its verification). `commit` and `commit_known` keep their signatures | "Never between the write and its verification" holds by type; 23 call sites stay as they are |
| A stopped capture | `Failure::Stopped`, `Outcome::Aborted`, `Disposition::Cancelled`: no retry deadline, streak unchanged, not counted as a failure, base unchanged | The core rule exists; uploaded pieces are unreferenced output (DESIGN 7.4) |
| Log | `capture owner=<prefix> result=<landed\|unchanged\|failed\|deferred\|skipped\|paused>` per event-driven capture; `round` gains a leading `cause=<start\|timer\|rescan\|wake\|dir>`; `wake_gap` once per detected gap. No path in any line | DESIGN 11.5: info logs owner ids |
| Test timing | `MEMORY_SIM_TIMING` is unchanged. The real-binary test sets `window = { quiet = 1, cap = 2 }` in its config. The wake gap is tested on the driver only | Seconds, not minutes; no test sleeps through a tick |

## Increments

| # | Increment | Branch | Depends on | Wave |
|---|---|---|---|---|
| 1 | STOP-1a: a stop that a full inbox cannot drop | `rust/stop-flag` | none | 1 |
| 2 | LOOP-1: per-owner capture on the daemon | `rust/owner-capture` | none | 1 |
| 3 | LOOP-2a: the driver, scripted | `rust/loop-driver` | none (defines its two traits) | 1 |
| 4 | STOP-1b: stop between owners and pieces | `rust/stop-in-round` | 2 | 2 |
| 5 | LOOP-2b: the binary runs the loop | `rust/loop-wired` | 1 to 4 | 3 |

Wave 1 runs in parallel. `watch.rs` is shared by 1 (inbox, `FsWatcher`, lines 219 on) and 3
(`Scheduler`, lines 68 to 167): each edits only its region and its own tests.

### STOP-1a

Owns `watch.rs` (inbox, `FsWatcher`), `run.rs` (`StopSignals`), `tests/watch.rs` (inbox and
watcher tests), `tests/run.rs`. Must not touch `daemon.rs`, `commit.rs`, `Scheduler`.

- The inbox carries a stop flag and reports it first; `FsWatcher` exposes a handle the signal
  thread uses; the watcher also watches a given non-recursive directory (the hint directory).
- The signal thread sets the flag and wakes the inbox. If the thread ends, the loop sees a
  failed wait (`wait_failed`, exit 1), as today.
- Proof: with a full one-slot inbox, a stop is the next thing received; a stop set before the
  first receive is not lost; an overflow still gives one `Rescan` afterwards.
- Teeth: stop sent only as a queued input; flag checked after the queue; overflow lost when a
  stop is pending.

### LOOP-1

Owns `daemon.rs`, `resolve.rs`, `tests/daemon.rs`, `tests/resolve.rs`, `tests/counters.rs`.
Must not touch `watch.rs`, `commit.rs`, `run.rs`.

- `Sources::note` and `Sources::of`; `WalkSources` implements both without a walk.
- `Daemon::note_path`, `Daemon::capture_owner(&Owner)`, `Daemon::next_retry`, the owners due
  for a retry, the hint refresh that returns new records as (owner, signal, paths).
  `reconcile_once` keeps its behaviour and its 72 test call sites.
- Proof: one owner's capture reads and stats nothing of another owner (counters); it commits
  what a full round would have committed for that owner; parked owner, parked source, retry not
  yet due and host pause are each skipped without a request; a host pause set by a full round
  is not cleared by a per-owner capture; a launch record is attached and its offset advances; a
  park is written to `status.json` at once; a path under a canonical spelling of a root maps to
  its owner; a path outside every root maps to nothing; on a tree with symlinks and deep
  directories `of` over every owner yields exactly what `enumerate` yields; after a file
  vanishes `of` does not yield it and no absence is counted.
- Teeth: paused check removed; parked owner captured; `host_paused` reset; `rounds` counted;
  hint offset not advanced; `of` walks; `of` yields a symlink; canonical spelling ignored.

### LOOP-2a

Owns a new `src/driver.rs` (one line in `lib.rs`), `tests/driver.rs`, `Scheduler` in
`watch.rs`, the scheduler tests in `tests/watch.rs`, `memory-core/src/coalescer.rs` (due owners
only) and its tests. Must not touch `daemon.rs`, `run.rs`, `commit.rs`.

- The driver as decided above, with the startup delay as its first deadline.
- Every guarantee of the nine tests in `tests/run.rs` that concerns the loop is restated
  against the driver before LOOP-2b removes `run_loop`; the report lists old test to new test.
- Proof (all scripted): a path change gives one capture of its owner after `quiet`, and after
  `cap` under continuous changes; a session-end hint captures at once; a tick with wall minus
  monotonic of 121 s reconciles and 120 s does not; `Rescan`, a watcher error and a new
  directory each reconcile once however many arrive in one batch; an input does not move the
  tick, retry or reconcile deadline; a retry runs at its deadline and not before; a stop ends
  the loop before the next unit of work and runs shutdown; only a stop gives exit 0. The
  existing scheduler property still passes with the per-call function.
- Measure (ignored test, numbers in the report): `Scheduler::handle` per input at 5,000 known
  owners, before and after the due-owner change.
- Teeth: tick moved by an input; gap boundary at 120; reconcile counted per cause; stop looked
  at only after the batch; retry run early; capture not marked complete.

### STOP-1b

Owns `commit.rs`, `daemon.rs` (stop argument and `land` only), `tests/commit_protocol.rs`,
`tests/daemon.rs`. Must not touch `watch.rs`, `run.rs`, the driver.

- The split of `commit_known`; `Failure::Stopped`; the daemon checks the stop between owners
  in a full round and before each piece and the manifest.
- Proof: a stop raised after piece 1 of 3 leaves one piece and no manifest, no `current.json`
  write, an unchanged index, and the next start captures the whole range; a stop raised by the
  store during the `current.json` write or during any verification GET changes nothing: the
  commit completes and the index advances; a stopped owner has no retry deadline and no
  failure count; a full round stopped after owner 2 of 5 has run 2.
- Teeth: stop checked between the `current.json` write and the verification; stop not checked
  between pieces; a stopped capture given a retry deadline; index advanced on a stopped
  capture; the round continuing after a stop.

### LOOP-2b

Owns `run.rs`, `assemble.rs` (building the watcher only; the prefix refusal stays), `log.rs`,
`tests/harness.rs`, `tests/s3_store/daemon_restore.rs`, `tests/common/log_lines.rs`,
`tests/log.rs`.

- `main_entry` builds the watcher over the configured roots and the hint directory and runs
  the driver with the real daemon. `run_loop` goes once its guarantees are covered.
- Proof on the real binary against the local server, every wait bounded at 20 s: with the
  reconcile interval set to one hour, bytes appended after the first round are in the store
  and `memory restore` returns the file; a session-end hint line appended to today's hint file
  gets its owner captured without waiting for `quiet`; SIGTERM during a capture whose piece PUT
  the server holds exits 0 after that request, with no `current.json` change, and a restart
  uploads the range; a stop with an idle loop still exits 0 with `stopped` last; the log keeps
  its grammar and order rule with the new events.
- Teeth: watcher not started (the append never arrives); hints directory not watched; stop
  flag not passed to the daemon; `capture` line carrying a path.

## Rules for workers

The rules of [brief 28](28-production-prerequisites.md#rules-for-workers) apply unchanged. In
addition: an existing test is removed only when the report names the test that now carries the
same assertion; no new dependency; no thread besides the signal thread and the one `notify`
starts; no change to what `status.json` or `index.json` store.

## Acceptance (parent)

As in brief 28: whole diff read, the seven checks, the mutation predicates, teeth per guarantee
on the intended assertion, `scripts/gates.sh` on the combined tip. Evidence goes to
`evidence/20-event-loop/`.

## Found while scoping, not fixed here

1. **A deleted file is never recorded as deleted.** `WalkSources::enumerate` yields only files
   the walk finds (`resolve.rs:300-329`), and the two-absence rule runs only for sources capture
   is given (`capture.rs:326-348`). Probe on unmodified code, real files through the S3 adapter:
   round 1 landed 1; the file was removed; four more rounds each landed 0, failed 0, sent no
   PUT; the reader still shows the source live. The capture and daemon tests pass because they
   keep a fixed source list and remove the file under it. DESIGN 17 puts deletions in slice 3,
   so this is unbuilt, not regressed, but no document said that the rule is unreachable today.
   A vanished file also fails admission (`privacy/mod.rs:291`), which slice 3 must settle.
2. **Commit pauses are zero.** `land` passes an empty pause (`daemon.rs:788`), so the 4
   verification GETs and the 4 rebase retries run back to back. DESIGN 7.4 says "retry the GET
   with backoff" and "full-jitter pauses" and gives no lengths. Nothing here depends on the
   lengths; they need a decision before the refusal is lifted.
3. **A capture holds the whole range in memory.** A replay reads `[0, size)` in one read
   (`capture.rs:521-524`), the transform copies it, and every piece of every source of the
   owner is built before the first PUT (`capture.rs:657`, `commit.rs:423`). The target is a
   peak RSS under 128 MB (DESIGN 7.3). Source file sizes on the two hosts were not measured
   for this brief; the only recorded sizes are of objects the Go path published, the largest
   615 MB raw and 215 MB encoded (`reports/01-requirements.md:321`), so files of that order
   may exist and the first reconcile of the existing files would read each in one piece. A10.4
   names the settling measurement; it has not been run.
4. **A round does not end early when the network is down.** Only a host pause ends a round
   (`daemon.rs:985-988`); every owner is tried, each up to its request timeout. The stop check
   of STOP-1b bounds the stop, not the round. Measure in the acceptance run before choosing a
   rule.

## Open for the operator

None blocks wave 1.

1. Items 2 and 3 above: pause lengths, and whether a streaming capture is required before the
   first production write or after the peak-RSS measurement.
2. The wake stagger is dropped (serial captures); confirm, and it is folded into DESIGN 7.1.
3. One `capture` log line per event-driven capture at info.
4. Still open from brief 28: no cap on kept hint files; the `events` block removal; the
   `launch_attach_failed` removal and the dated notes under A11.3 and A13.3; the AUDIT-3
   choices; no `unpark` yet.

## Not in this brief

Lifting the empty-prefix refusal; `memory status`, `unpark`, `reconcile`, `export`; the age of
the oldest uncaptured change (DESIGN 11.3); the shadow-run comparison and the coverage
cross-check that the 2-day criterion needs (DESIGN 17 slice 2b); the daily read-back; service
units and hook installation; deletions, snapshots and inotify (slice 3).

## Progress ledger

- 2026-11-16: brief written on `94b8abd`.
