# DESIGN.md amendments, 2026-11-09

**Status: authoritative where it conflicts with DESIGN.md or AGENTS.md.** Both of those files are
hash-pinned by `implementation/verify-evidence.py`, which forbids admitting them as revisions,
so they are not edited. Read DESIGN.md, then this file. Each amendment names the operator decision
or measured evidence it rests on. The handoff brief records the operations themselves.

## A0. Go writers are stopped (AGENTS.md "Go candidate (frozen, running until cutover)"; contract status note)

Operator instruction, 2026-11-09. The Go capture daemons on the Mac and BC-01 and the Mac
publication-refresh job are stopped and disabled; the dashboard still runs against local data.
The Go hooks remain installed and write only into the local Go spool. They are removed at
cutover (DESIGN 18 step 2). Statements that the Go candidate keeps running until cutover
describe the earlier plan.

## A1. P5 is waived (DESIGN 16 P5; 17 slice-1 prerequisite)

Operator decision. Slice 1 is not held on P5. Correctness never depended on it: a prefix rewrite is
detected by the window check (7.2) and becomes `Replay`. The rewrite frequency is measured later
from the running daemon's own counters (`Replay{WindowMismatch}`, `Replay{AdoptMismatch}`). No
collector reads private sources for it.

## A2. One bucket: the existing `agent-sessions` (DESIGN 0.5, 6.1, 16 "Needs", 17, 18; AGENTS.md "fresh shared bucket")

Operator decision, confirmed twice ("stop the current process, cleanup R2 and use that").

- There is no separate acceptance bucket and no fresh production bucket. Prototypes, acceptance
  (P2, P2b, slices 2, 2b, 3) and production all use the existing `agent-sessions` bucket with the
  credentials in the repository's ignored `.env` (mode 0600). Credentials are loaded into a process
  environment only, never printed, logged or written.
- Acceptance runs write only under `rust-acceptance/<run-id>/` and delete that prefix at the end,
  verifying it empty. They never list, read or delete other keys.
- The old archive is already gone. On 2026-11-09 all Go writers were stopped (Mac capture daemon
  and refresh job, BC-01 capture unit), the bucket was inventoried, and the `capture-v1/` and `v3/`
  prefixes (435,618 objects) were deleted. Two unrelated `delivery-artifacts/` deployment bundles
  remain. DESIGN 18 step 6 (inventory and delete the old archive, then delete the acceptance bucket)
  is therefore replaced by: before rollout, delete every `rust-acceptance/` prefix and verify that
  the bucket holds only `delivery-artifacts/` and data written by the accepted Rust writer.
  DESIGN 18 step 1 (create a production bucket) is dropped.
- One token per host (6.1) is not decided by this amendment; the existing credentials are used.

## A3. The hook records no launch argv (DESIGN 6.3 `launch.argv`, 8.4, 10.1, 16 P4)

Operator decision: drop it. The Rust hook records env facts only. There is no process-tree,
`kern.procargs2` or `/proc` read, and no `--argv` flag. `LaunchRecord.argv` stays in the schema and
is empty from the hook. The argv allowlist code stays as a protection for any argv that reaches it.
This is an accepted regression from the Go hook.

## A4. Symlinked files are skipped (DESIGN 7.2 "every symlink link", 8.2)

Operator decision. `realfs::walk` counts symlinks and never yields them, so no file is captured
through a link. The resolver's link-chain and identity checks stay as defence for paths reached
directly (hint paths) and for ancestor and root identities. A link to a credential is denied
either way.

## A5. State root (DESIGN 9)

Operator decision: `$HOME/.local/state/memory` on both hosts. `MEMORY_STATE_DIR` overrides it, and
tests must always set it to a temp directory with `env_clear()`.

## A6. `BaseUnchanged` exhaustion gets its own class (DESIGN 7.4 step 5, 11.2)

Evidence: [P11](prototypes/P11-commit-retry.md) (real R2 probe, 889 requests; seeded simulation,
100 owners, 12 seeds, 8 approaches plus the parent's hybrid run). DDIA basis: ch. 8 "Handling
errors and aborts" (retry only transient errors, limit retries, exponential backoff, treat overload
differently) and ch. 2 "When an overloaded system won't recover" (randomised backoff against retry
storms).

- In-commit: keep up to 4 rebase retries of step 3, with full-jitter pauses.
- After exhaustion the capture ends `Failed(Transient)`, a new `ErrorClass`, not `Throttled`.
  `Transient` retries with full jitter, `uniform(1 s, min(120 s, 2 × 2^n))`, and is counted
  separately. It carries no `Retry-After`.
- `Offline` gets the full jitter DESIGN 11.2 already requires; `disposition::backoff` has none
  today. `Throttled` honours `Retry-After`, falling back to the `Offline` schedule.
- `verify` accepts every manifest hash this daemon built for the owner since its last landed
  commit (`known_ours`, in memory). A late `current.json` write from an earlier failed capture
  then becomes `AlreadyOurs` (adopt its ETag and position, queue a follow-up) instead of a false
  `Foreign` park. The owner key contains the machine id, so outside a cloned machine id this
  cannot hide a real foreign writer. A Rust test must drive that interleaving.
- Rejected, measured: fail fast with no in-commit retry (p99 325 s against 23 s at 20 % errors);
  the hybrid (fail fast once a streak exists: p99 40.5 s against 23.2 s at 20 %, no better under
  bursts). Unproven, not adopted: a shared retry token bucket (never bound in the simulation).

How data arrival and late commits are coordinated, as implemented and measured:

- Bytes arriving during a capture: a replay or append reads to the stat-time size; `captured_to`
  advances only when the commit lands, so a failed capture's bytes are re-read by the retry, and
  bytes after the stat ride in the next capture. The coalescer keeps one capture in flight per
  owner and queues exactly one follow-up. Unlanded bytes at the end of every simulated run: 0.
- Lost reply: the verification GET compares the content-addressed manifest hash (the request id)
  and returns `AlreadyOurs`.
- Delayed write from an old attempt: fenced by `If-Match` on the ETag. Real R2: 28 of 28 stale
  ETags got 412; 60 of 60 two-writer races had exactly one winner, and the stored body was always
  the winner's.
- Another host: one writer per owner, enforced by the owner key's machine id and the daemon lock;
  a real foreign write parks the owner and never merges.

## A7. Durable state writes (DESIGN 9.2, 15)

Evidence: [P12](prototypes/P12-durable-state-writes.md) (APFS microbenchmark; end-to-end daemon
properties at 2,000 cases on patched copies). DDIA basis: ch. 8 "Durability" (fsync is what makes a
write durable, and it is easy to get wrong).

- Production is unchanged: temp file, `F_FULLFSYNC` (`sync_all`), rename, directory sync; status
  and index written on authoritative change or at most every 60 s, and at shutdown. That is about
  two writes a minute, roughly 12 ms of flush time. `F_BARRIERFSYNC` only orders writes (SDK:
  "fsync + issue barrier to drive") and is not adopted.
- Tests: `StateDir` gets a `SyncPolicy { Full, None }`, `Full` by default. `None` keeps temp, write,
  rename and directory open and skips only the flush calls. Only the daemon simulation properties
  select `None`; `state.rs`'s own tests and the daemon example tests keep `Full`; the binary always
  uses `Full`. Measured: the two properties go from 421 s to 14 s, all 30 tests passing. A flush
  cannot change what a live process reads back, so no current assertion loses evidence.
- Add a deterministic crash-point property: drop the daemon between `stage` and `publish`, reload,
  and assert the previous or the new body is read, never a mix. Neither the old nor the new setup
  modelled this.
- Rejected, measured: skipping unchanged bodies (no faster, because status carries counters and
  `updated_at`, and it changed what `shutdown_table` checks).
- Not measured: Linux (ext4) costs; BC-01 SSH was unavailable from the session.

## A8. Prototype ids

DESIGN 16 already assigns P9 (SQLite unchanged test) and P10 (Linux racy mtime); both stay slice-3
work. The 2026-11-09 prototypes are P11 (commit retry) and P12 (durable state writes).

## A9. Line-coverage floor is 80 %, not a ratchet (DESIGN 15 gate 8; quality-gates.md)

Operator decision, 2026-11-09. `rust/coverage-floor.txt` is 80. DESIGN 15 gate 8 ("the current
percentage as a floor") is replaced: the floor is a fixed backstop, not the latest measurement.

- Why: the 98 % floor was the scaffold's first measurement (49/50 lines,
  `implementation/00-scaffold.md`), carried forward to about 5,400 lines. It left 9 uncovered
  lines of headroom and rewarded removing error branches or adding coverage-driven tests.
  `challenge/06-strategy-engineering.md` F8.1 and `reports/04-tests.md` finding 7 recommended
  against a "current percentage" ratchet. The Go gate is 75 % total.
- What carries the evidence instead: independent-oracle properties with their coverage guards
  (DESIGN 15, unchanged), production mutation checks, and the consumer gate. Tautological tests
  (oracle re-derived from the code under test, fake asserted against itself, derive-only) are
  removed rather than kept for line count.
- The floor is still a hard gate and is not lowered to make a run pass. Raising it is allowed only
  as a recorded decision, never automatically from a measurement.
