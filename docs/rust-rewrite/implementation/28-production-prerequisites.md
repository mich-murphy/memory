# 28. Prerequisites of the first production write: logs, hint retention, the audit (2026-11-12)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 27](27-run-loop.md) (R1 to R4: `memory daemon` and `memory restore` run on the real
binary) and [evidence 18](evidence/18-run-loop/). It is R5 of brief 27, split into increments.
Status: written; no increment started.

## Goal

DESIGN 18 and A13.2 allow no production write before four things exist: zstd pieces (done),
`tracing` to stderr, hint retention, and the audit with a property that covers a rewrite between
the windows. This brief delivers the three that are missing. After it, the daemon is connected
to the watcher and scheduler and gains a stop check inside a round; only then is the
empty-prefix refusal lifted and the acceptance runs of DESIGN 17 started.

## Checkout

Everything sits on `e11db32` (`docs/run-loop`, top of stack #103, PRs #96 to #102, in hosted CI
when this was written). The branches of this brief are local until that stack merges.

| Branch | Worktree | Content |
|---|---|---|
| `docs/r5-brief` | `memory-wt/r5-brief` | This brief |
| `rust/log-stderr` | `memory-wt/sk-zstd` | LOG-1 |
| `rust/hint-retention` | `memory-wt/r5-hints` | HINT-1 |
| `rust/audit` | `memory-wt/rl-run` | AUDIT-1, AUDIT-2 |

## Facts checked on `e11db32` before writing

A read-only planner scoped the work. The parent rechecked what the increments depend on.
Paths are under `rust/crates/`.

| Fact | Where |
|---|---|
| The shell only ever passes `AuditEvidence::NotDue` | `memory/src/capture.rs:323`, `:426`, `:723` |
| The planner already turns `StableMismatch` into `Replay{AuditMismatch}` and `RecheckRequired` into `NeedAuditRecheck`; the shell answers `NeedAuditRecheck` with `Unavailable` | `memory-core/src/planner.rs:180-183`, `:234-237`; `capture.rs:441`, `:732` |
| `audit_eligible(captured, before, after)` exists and has no shell caller | `planner.rs:44` |
| The index already has per-source audit bookkeeping, `AuditState { last_checked_at_ns, consecutive_failures }`, and capture resets it on every write | `memory-core/src/domain.rs:557`; `capture.rs:640-643` |
| A stat refresh the planner asks for (`refresh_stat`) is never stored | `capture.rs:27` (module docs), no other use |
| The capture property moves any rewrite that only an audit could see to the tail window | `memory/tests/capture_property.rs` `detectable` |
| An extension that cannot resume the digest re-reads `[0, to)` and takes the committed digest from that fresh read, while the stored pieces before `from` stay as they were | `capture.rs` `Job::write`, `whole_digest`; read from code, not tested |
| Hints are read with `std::fs::read_dir` and `File::open` in the daemon, and nothing deletes a hint file | `memory/src/daemon.rs:612`, `:623` |
| `StateDir` already owns `hints_dir()` and `vet_hints_dir()`; the `FileSystem` trait is keyed by source handle and cannot list or remove | `memory/src/state.rs:713`, `:763`; `environment.rs:30` |
| A pending launch record is attached only when its owner has a commit for another reason | `daemon.rs` `run_owner` |
| The workspace has no logging crate; `tracing-subscriber` is not in the local cargo cache | `rust/Cargo.toml`, `Cargo.lock` |
| A clean stop is asserted to leave stderr empty | `memory/tests/harness.rs:135`, `:247`; `tests/s3_store/daemon_restore.rs:151` |
| `status.json` carries the interim `events` block of A11.3 | `daemon.rs:160`, `:182` |
| `InboxTx::push` drops the input on a full queue and records an overflow | `memory/src/watch.rs:255` |

## Decisions the parent made where the design is silent

Each is reversible before the first production write, because only `rust-acceptance/` prefixes
are written until then and `index.json` and `status.json` are rebuilt when they do not parse.

| Topic | Decision | Reason |
|---|---|---|
| Log level | Fixed at info. No filter variable, no debug events | DESIGN 11.5 names info as what is logged. With no debug event there is no path in any log line. A level switch arrives with `clap` |
| Clean stop and stderr | A clean run now writes log lines. The three "stderr is empty" assertions become "every line parses as a known event, in this order, and holds none of the canary strings" | 11.5 requires the lines. The new check is an allowlist, not a weaker check |
| Refusal line | `memory: <closed code>` stays byte for byte, written as now, and is the last line. No duplicate log event | Tests and supervisors match on it |
| Event vocabulary | Every event is emitted by a function in one module that takes typed values. No other module uses a `tracing` macro. No foreign error is ever formatted into an event | Makes "content never appears" a property of one file |
| Owner in a log line | The stored key prefix (`memory_core::limits::owner_prefix`) | 11.5 allows owner ids. It is the text `memory restore` takes |
| `memory-core`, `memory hook`, `memory restore` | No `tracing` in `memory-core`. The hook and restore do not start a subscriber | Core does no I/O (DESIGN 12, 13). The hook budget is 1 ms |
| Hint I/O | Listing, bounded read and removal of hint day files move to `StateDir` | It already owns and vets the directory. A new trait would have one implementation |
| Retention rule | A day file is removed when all hold: its name is a valid day and that day ended at least 7 full days before the current UTC day; it was read whole (not cut at the 64 MiB bound); the stored offset equals the end of its last complete line; no torn tail is pending | DESIGN 9.1. The offset already encodes "every launch record committed" (`advance_hints`) |
| When the sweep runs | In every round, after hint offsets advance. No separate sweep at start | A11.2 says "at start and on tick". The first round is the start, and the rule needs the scan that round does. Before it, offsets come from an index that may be stale |
| A record that can never commit | The file is kept (the design, literally), and the round reports how many files are past 7 days and kept | A cap would delete launch evidence; that is the operator's call |
| What the audit compares | The digest of a fresh read of `[0, size)`, transformed as capture transforms it, against the whole-stream digest of the last epoch in the recovered manifest chain. Only when the index position equals that remote position | 11.4: the check trusts neither the index nor the resumable hash |
| Which sources are audited | Append class only | A snapshot is re-read whole and hashed fresh whenever its fingerprint changes; it has no window or resumable state |
| When an audit is due | The source has never been audited in this epoch; or it was captured or stat-refreshed since its last audit and that audit is at least 24 h old by the wall clock (or the wall clock is behind it); or its mtime changed at an unchanged size | 11.4: once a day per active source and at every epoch's first capture. "Active" is "captured since the last audit", so an idle corpus is not re-read every day. The third trigger is the same-size rewrite, which 7.2's table leaves to the audit |
| Audit state across captures | An extension keeps `last_checked_at_ns`; a new epoch clears it; a match sets it and zeroes the failure count; a stable mismatch increments the count on the new epoch | Without this an active file would be re-hashed in full after every append |
| Stat refresh | Stored when the planner asks for it | 7.2: "a match is `Unchanged` with the stat updated". Without it a same-size rewrite never becomes eligible |
| Threading for the watcher | One thread, one event loop, captures run serially | Satisfies "one capture per owner" and the host-wide caps by construction. Latency under backlog is measured in the acceptance run |
| Lifting the empty-prefix refusal | After the watcher, scheduler and in-round stop are in, not straight after this brief | A13.2 would allow it earlier. The 2-day criterion should measure the loop that will run |
| Acceptance runs with real session data | Not before this brief is done, even under `rust-acceptance/` | Without the audit and logs the run could not be judged |

## Increments

Each is one or more local commits on its branch. "Teeth" are mutants the parent applies after
acceptance; the worker runs them too and reports the failing test.

### LOG-1: `tracing` to stderr, start, round and stop events

- Add `tracing` and `tracing-subscriber`, exact pins, minimal features, no `ansi`, no
  `env-filter`. Report the lockfile delta, licences and `cargo deny`.
- New `memory/src/log.rs`: subscriber start and the closed events `started`, `round` (the
  counts of `RoundReport`), `stopped`. `memory daemon` starts the subscriber; hook and restore
  do not.
- The three empty-stderr assertions become the allowlist check above, shared by the `harness`
  and `s3_store` test binaries.
- Owns: `rust/Cargo.toml`, `memory/Cargo.toml`, `Cargo.lock`, `deny.toml` if a licence needs
  it, `memory/src/log.rs`, `run.rs`, `lib.rs`, `tests/harness.rs`,
  `tests/s3_store/daemon_restore.rs`, a shared test helper. Not `daemon.rs`, `capture.rs`,
  `state.rs`.
- Teeth: a path or the canary added to an event; the subscriber started in the hook; the
  `stopped` event dropped; the refusal line changed.

### HINT-1: hint I/O on `StateDir`, retention sweep

- `StateDir` lists day files, reads one up to a bound and says whether the bound cut it, and
  removes one. `daemon.rs` no longer names `std::fs` or `File`.
- A pure retention predicate, tested at the 7 and 8 day boundary.
- The sweep per the decisions above; a removed file's offset entry leaves the index; a failed
  removal is retried next round and never fails the round. `RoundReport` gains the counts
  (removed, kept past 7 days).
- After `index.json` is deleted, a retained old file is rescanned from 0 (DESIGN 9.3 step 4).
  The worker checks whether a record whose launch the owner's manifests already carry is
  marked done without a new commit. If it is not, the file can never be removed again; closing
  that is in scope if it fits in the owned files, otherwise it is reported.
- Owns: `memory/src/state.rs`, `daemon.rs` (hint scan, the sweep, its call, `RoundReport`),
  `memory-core/src/hint.rs` only for the pure predicate, `tests/daemon.rs`, `tests/state.rs`.
  Not `capture.rs`, `run.rs`, `Cargo.toml`.
- Teeth: 7 days treated as expired; the offset condition dropped; the torn-tail condition
  dropped; a cut read treated as whole; today's file removed; the index entry left behind.

### AUDIT-1: the audit produces evidence, and a mismatch starts an epoch

- Capture computes `AuditEvidence` for an indexed append source when due: cheap stat
  pre-check, read, stat again, `audit_eligible`, transform, fresh hash, compare. A mismatch is
  `RecheckRequired`; the second independent read gives `Matched`, `StableMismatch`, or
  `Ineligible` when the two reads differ from each other.
- Audit state and the stat refresh are stored per the decisions above.
- The hypothesis in the facts table (an extension that re-reads the whole stream commits a
  digest its stored pieces do not reassemble to, after a rewrite between the windows) is first
  reproduced as a failing test through `memory-reader`. If it reproduces, the re-read verifies
  the committed prefix before extending and a mismatch starts an epoch. If it does not, the
  worker reports why.
- Owns: `memory/src/capture.rs`, a new `memory/src/audit.rs` if it helps, `lib.rs` (one line),
  tests. `planner.rs` and `domain.rs` only if a test proves a change is needed, reported first.
- Teeth: eligibility check removed; recheck removed (first mismatch replays); mismatch ignored;
  `last_checked_at_ns` cleared on an extension; due always true; due never true after the first.

### AUDIT-2: counts, cost, and the property (A12.2)

- `Captured`, `RoundReport` and `status.json` carry audit counts (matched, mismatched,
  ineligible); 11.3 names the mismatch count.
- A byte-count test in the style of `tests/counters.rs`: first capture; the next round reads
  `[0, size)` once more; a third round within a day reads no audit bytes; an append then reads
  only the delta and the windows; after 24 h of simulated wall time the next quiet round audits
  once; a restart with an intact index does not re-audit; a deleted index audits once.
- `capture_property.rs` generates rewrites strictly between the windows, at a growing size and
  at the same size. The property: once the audit has had its turn (the model advances the wall
  clock and runs quiet rounds), the latest epoch restores to the file's bytes. Every existing
  assertion stays for every case; the worker reports exactly what the oracle now says before
  the audit's turn.
- Owns: as AUDIT-1 plus `daemon.rs` (the counts only), `tests/capture_property.rs`,
  `tests/counters.rs`.
- Teeth: the window-mutation gate still passes; with the audit disabled the new property fails
  on a between-windows case.

### Later, not dispatched from this text

- **AUDIT-3, parking.** 11.2 parks a source after 3 consecutive failures spanning at least 3 h.
  The code parks whole owners and has no first-failure time for a source. The parent settles
  the mechanics after AUDIT-2. Required before the refusal is lifted: it bounds a replay loop.
- **LOG-2.** State-load, park, pause and audit events from `daemon.rs`; the `events` block
  leaves `status.json` (A13.2: logs replace the interim counters). After HINT-1 and AUDIT-2,
  because all three change `daemon.rs`.
- **LOOP-1, LOOP-2, STOP-1.** A per-owner capture entry on `Daemon`; the event loop over the
  watcher inbox, hint directory, coalescer deadlines, the 60 s wake-gap tick, retry deadlines
  and the 15 min reconcile; a stop that is never dropped by a full inbox and is checked between
  owners and between pieces, never between the `current.json` write and its verification. A
  separate brief.

## Rules for workers

- Work only in the assigned worktree and branch. Local commits only, repository style
  (`memory: ...`, `memory-core: ...`, `rust: ...`), no trailers. No push, no PR, no `gh stack`.
- Cargo only as `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd rust && <cargo ...>'`.
- Nothing is weakened: no test, assertion, doctest, lint or gate removed or loosened, no
  `#[allow]` or `#[expect]`, no drive-by refactor. A changed assertion is named in the report
  with the reason.
- No reads of `~/.claude`, `~/.codex`, `~/.pi`, `.env`, credential files or `~/.local/state`.
  Test data is synthetic. No host change, no installed service, nothing sent to R2.
- Do not run `scripts/gates.sh`, coverage or `cargo mutants`. Run fmt, clippy with
  `-Dwarnings`, rustdoc with `-Dwarnings`, the focused tests, then the workspace nextest once.
- The strings `check-mutations.py` and `check-window-mutation.py` patch must each still occur
  exactly once.
- Scratch stays outside the repository. Commit a fix before mutating its file; move any
  `*.proptest-regressions` file out afterwards.
- A file outside "Owns" is changed only when a test proves it is needed, and the report says so.
- A decision this brief does not make, or one that would change what is stored, logged or
  deleted, is reported as a question, not decided.

## Acceptance (parent)

Per increment: read the whole diff; rerun fmt, clippy, rustdoc, nextest, doctests, schemas and
`cargo deny` on the tip; confirm the mutation predicates; rerun at least one teeth check per
guarantee and confirm it fails on the intended assertion; then `scripts/gates.sh` on the
combined tip. Evidence goes to `evidence/19-production-prerequisites/`.

## Open for the operator

None blocks this brief.

1. A hint day file with a launch record that never commits is kept for ever. A cap (say 30
   days) would delete launch evidence. Default: no cap; the kept count is reported.
2. The `events` block leaves `status.json` when the events are logged (LOG-2), as A13.2 says.
3. The refusal is lifted after the watcher and the in-round stop, later than A13.2 requires.

## Not in this brief

`memory status`, `unpark`, `reconcile` and `export`; the daily read-back restore (11.4 measure
3); the coverage cross-check and the heartbeat object; launchd and systemd units; hook
installation. Each is needed before cutover (DESIGN 17, 18), none before the watcher is wired.

## Progress ledger

- 2026-11-12: brief written on `e11db32`.
