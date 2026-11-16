# 28. Prerequisites of the first production write: logs, hint retention, the audit (2026-11-12)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 27](27-run-loop.md) (R1 to R4: `memory daemon` and `memory restore` run on the real
binary) and [evidence 18](evidence/18-run-loop/). It is R5 of brief 27, split into increments.
Status (2026-11-16): all increments done and accepted locally; record in
[evidence 19](evidence/19-production-prerequisites/); merged into `main` on 2026-11-16 as
`d765f9c` (stack #111, PRs #104 to #110, #112 and #113). The text below up to "Result" is the
dated statement of intent (2026-11-12) plus the decisions taken afterwards.

## Goal

DESIGN 18 and A13.2 allow no production write before four things exist: zstd pieces (done),
`tracing` to stderr, hint retention, and the audit with a property that covers a rewrite between
the windows. This brief delivers the three that are missing. After it, the daemon is connected
to the watcher and scheduler and gains a stop check inside a round; only then is the
empty-prefix refusal lifted and the acceptance runs of DESIGN 17 started.

## Checkout

Everything sits on `e11db32` (`docs/run-loop`, top of stack #103, PRs #96 to #102). That stack
was in hosted CI when this was first written; it merged into `main` as `39944d7` on 2026-11-12.
The branches below were put in this order on 2026-11-12 and pushed on 2026-11-16 as stack #111,
without a rebase onto the merge commit, so the recorded hashes are the pushed ones.

| Branch | PR | Tip | Content |
|---|---|---|---|
| `docs/r5-brief` | #104 | `368bc4a` | This brief as first written |
| `rust/coverage-per-process` | #105 | `00e91ab` | Gate 08 through nextest |
| `rust/hint-retention` | #106 | `8fe5bfa` | HINT-1 |
| `rust/log-stderr` | #107 | `ec5c0cc` | LOG-1 |
| `rust/audit` | #108 | `c9941ff` | AUDIT-1, AUDIT-2 |
| `rust/log-events` | #109 | `7f99613` | LOG-2 |
| `rust/audit-park` | #110 | `2edf2f8` | AUDIT-3 |
| `docs/production-prerequisites` | #112 | `00af49e` | Result, evidence 19, handoff, gates entry |
| `rust/ci-coverage-job` | #113 | `538279e` | Hosted CI runs coverage as its own job per OS |

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

Dated note (2026-11-12): the row "capture resets it on every write" was wrong for what the
audit needs. It was replaced by the carry rules in the decisions table below (an extension
keeps `last_checked_at_ns`; a new epoch clears it; a match zeroes the failure count; a stable
mismatch increments it on the new epoch), and AUDIT-3 added `first_failure_at_ns`.

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

### LOG-2: state-load, park and pause events (dispatched after HINT-1 and AUDIT-2)

Decisions taken after the brief was written: `hints_removed` and `hints_kept_old` are keys of
the `round` line. The events `state_load`, `parked`, `host_paused` and `host_resumed` are
added; the `events` block leaves `status.json` (A13.2); no separate audit event exists (audit
verdicts are the three audit keys of the `round` line, and AUDIT-3 reports a source park as a
`parked` line with `scope=source`). A
`status.json` that still has an `events` object loads as damaged, with no shim. Delivered at
`7f99613` on `rust/log-events`.

### AUDIT-3: park one source (dispatched after LOG-2 and the gates on `7f99613`)

Decisions of the parent: (1) the rule is the core's (`transition_paced`), with no second
copy of 3 failures over at least 3 h; (2) `AuditState` gains `first_failure_at_ns` (serde
default); (3) `ParkedEntry` gains an optional source id; (4) a third consecutive mismatch at
least 3 h after the first parks the source instead of starting an epoch; (5) a parked source
is left out of its owner's capture and the rest of the owner flows; (6) both mismatch sites
use the rule; (7) a source that is not prefix-stable never fails at the prefix site; (8) the
park is recorded before the commit result and `status.json` is written in that round; (9) the
`parked` line gains `scope` and a source id is never logged; (10) no `unpark` command yet.
Delivered at `2edf2f8` on `rust/audit-park`.

### Later, not in this stack

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

## Result (2026-11-16)

Commits, checks, mutants, gates and limits are in
[evidence 19](evidence/19-production-prerequisites/). Accepted locally by the parent, then by
hosted CI: stack #111 merged as `d765f9c` after every "Rust rewrite gates" run succeeded and
every "CI" run failed only in the two accepted `govulncheck` steps (three macOS jobs needed one
rerun each; see evidence 19). Nothing was written outside local test prefixes; no R2 run
happened, and the daemon still refuses an empty prefix.

| Increment | Tip | Delivers |
|---|---|---|
| Gate 08 | `00e91ab` | Coverage gate through nextest, one process per test (root cause of the hosted failure) |
| HINT-1 | `8fe5bfa` | Hint I/O on `StateDir`; day files past 7 days removed once consumed |
| LOG-1 | `ec5c0cc` | `tracing` to stderr: `started`, `round`, `stopped` |
| AUDIT-1 | `b37556e` | Audit evidence, audit state, stat refresh; fix of the between-windows digest bug |
| AUDIT-2 | `c9941ff` | Audit counts, byte-count test, property for rewrites between the windows |
| LOG-2 | `7f99613` | `state_load`, `parked`, `host_paused`, `host_resumed`; `events` block removed |
| AUDIT-3 | `2edf2f8` | Per-source park at repeated audit mismatches |

## Known limits

- No `unpark` command and no automatic unpark.
- A hint day file whose launch record never commits is kept for ever, including after index loss.
- A daemon with no sources never rewrites a damaged `index.json`, so it logs `state_load` every start.
- A `status.json` with an `events` block loads as damaged (none exists in production).
- The Hegel generator of the capture property cannot reach a source park; only a scripted test does.
- EPIPE on the real binary's stderr was not observed; `signals_unavailable` and
  `state_root_unresolved` are untested; no gate runs rustdoc.
- Not run: 2,000 cases, `cargo mutants`, anything against R2.

## Open for the operator

None blocks the next brief.

1. No cap on kept hint files; a cap would delete launch evidence. Default: no cap, count reported.
2. The `events` block left `status.json` in LOG-2, as A13.2 says; confirm.
3. The empty-prefix refusal is lifted after the watcher and in-round stop, later than A13.2 requires.
4. From earlier work: removal of the `launch_attach_failed` counter that A11.3 named, and the
   dated 2026-11-12 notes under A11.3 and A13.3 in the amendments file.
5. AUDIT-3 choices: per-source park, the `scope` key, source id never logged, non-prefix-stable
   sources never fail at the prefix site, no unpark yet.

## Not in this brief

`memory status`, `unpark`, `reconcile` and `export`; the daily read-back restore (11.4 measure
3); the coverage cross-check and the heartbeat object; launchd and systemd units; hook
installation. Each is needed before cutover (DESIGN 17, 18), none before the watcher is wired.

## Progress ledger

- 2026-11-12: brief written on `e11db32`.
- 2026-11-12: wave 1 (HINT-1, LOG-1, AUDIT-1) accepted; gate 08 fixed (`00e91ab`) after the
  hosted failure of PR #101 was root-caused; stack #103 merged as `39944d7`; AUDIT-2 accepted
  at `c9941ff`; LOG-2 accepted (`7f99613` after the rebase); AUDIT-3 worker commits to `0d28378`;
  full gates on `b37556e` and `7f99613` (all 13 sub-gates 0).
- 2026-11-16: AUDIT-3 accepted with parent commit `2edf2f8`; full gates on `2edf2f8` (500 cases,
  seed 3153221467, 583 tests, 97.19 %); stack #111 pushed (PRs #104 to #110).
- 2026-11-16: record added as PR #112 (`00af49e`). The macOS job reached its 30-minute limit on
  #109 and #110 with no gate failing (both passed on one rerun), so the hosted workflow now
  runs the coverage gate as its own job per OS (PR #113, `538279e`). Stack #111 merged as
  `d765f9c`; post-merge "Rust rewrite gates" succeeded on all six jobs.
