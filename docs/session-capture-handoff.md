# Implementation handoff: dependable session capture

This is the single compact delivery brief. The [contract](session-capture-contract.md)
is the authoritative design; this file records current state, decisions, ownership and
the next action only. The previous 6.6k-line ledger (V24–V32 private-candidate log,
lease/admission bookkeeping, dated checks) is preserved verbatim in the
[2026-10-27 archive](session-capture-handoff-archive-20261027.md); older history is in
the [progress history](session-capture-progress-history.md). Neither is current guidance.

## Current session checkpoint

**Updated 2026-10-27 (AEDT).** Operator-approved simplification and delivery plan in
progress (decisions below). The replacement is **not release-accepted, production-ready
or cut over**; defaults remain legacy.

### Checkout and preservation

- Repository `/Users/mm/dev/memory`, branch `main`, base `b23712bc`.
- The entire pre-simplification dirty worktree (77 paths, incl. dependency-resume
  bitmap, Python refresh/qualification scaffolding, handoff ledger) is preserved on
  branch `preserve/pre-simplification-20261027`. Recover anything from there; do not
  rebuild it from memory.
- Private `/private/tmp/memory-controlled-acceptance-20261025-v1` ("B") and other
  `/private/tmp/reader-*`/`outer-caller-*` trees are left untouched as historical
  evidence. **No further work builds on them**; fixes land in this repository as commits.
- Frozen research (`docs/research/agent-navigation-*`, `docs/research/README.md`) stays
  untracked and unmodified. `CLAUDE.md` stays absent. `cover.out`/`bin` are ignored.

### Scorecard

| Area | State |
| --- | --- |
| Design | Sound and fit for purpose; matches contract. No direction change. |
| Candidate code | Opt-in `capture-v1`; publication/reader/report v2; one Go reader used by Python via `capture-v1 export`. |
| Autonomous reliability | **Run-fatal step errors fixed `1706c00` (W1); Open-path SQLITE_BUSY fixed `ef727a9`/`56bd2f9` (W6); optional genuine-expiry withholding `c3fd555` (W9); repair-claim race `66ee491` (W11). Installed end-to-end confirmation pending (Step 4).** Original cause: `runtimeWorker` (`internal/sessioncapture/runtime.go`) treats any error returned from a work step as Run-fatal; ~10 workers share one SQLite connection (`SetMaxOpenConns(1)`), so a >60s wait for it returns `context deadline exceeded` and kills Run. Reproduced in a throwaway copy (0.49s). Matches the recorded Mac `exit 1 context deadline exceeded`. |
| Open test failures | None on `b648399` (W12 aggregate gates green). journey09 cause found and fixed (W6). V25/Qrace180 were private-tree names and did not reproduce on the checkout. |
| Shared spool `usage()` scan | Accepted limitation: O(payload files) stat per snapshot (≈185 files observed); fail-closed on integrity error. Not redesigned. |
| Gates | Green on `b648399`: `make check` 647s, `make test` 619s, fuzz, analytics 97/97. Dominated by legacy `internal/syncer`; shrinks at legacy removal. |
| Deployment | Last recorded: Mac qualified-dirty `e69fb2ed…` (failing with deadline), BC-01 `b23712bc`. Current health unknown. |
| Cutover (contract §6) | Inventory/rebuild/fence/cutover/deletion not performed. |

### Authoritative decisions (operator, 2026-10-27)

1. Approved cuts: versioned Python qualification scaffolding and refresh layering;
   dependency-resume ACK bitmap (rely on idempotent content-addressed re-upload);
   diagnostic/fixture subcommands out of the operator CLI; later fold the optional-repair
   lane into the single work/quarantine model and trim enrichment budget/history.
2. Handoff restructured into this brief; ledger archived verbatim.
3. Proceed through Steps 0–3 without further checkpoints, then Steps 4–5.

Non-negotiable: privacy/exclusion/fail-closed checks and their tests, identities,
learned roots, original sources and existing quality gates are kept intact. No gate is
weakened. No live install, ingestion or R2 deletion happens as an incidental action.

### Plan

| Step | Content | Status |
| --- | --- | --- |
| 0 | Preserve dirty state; archive ledger; compact brief; commit reduced baseline. | Done `65759df` |
| 1 | Runtime reliability (merged `1706c00`; holder measurement deferred to Step 4 via `step_errors`): operation timeouts/transient claim errors retry per item, only durable local-store failures stop Run; claims released on restart; timeouts visible in status; don't hold the lone connection across slow work; regression tests. | Done (W1/W6/W9/W11) |
| 2 | Simplify (2A `c92bc61` done; 2B reduced scope `77276c4` done): analytics refresh through one reader path; diagnostic subcommands to test helpers/hidden; optional-lane fold; enrichment trim; break legacy import edges (`launch`, `snapshot`, `hook`). | Done in reduced scope (`c92bc61`, `77276c4`, `a3bde3e`); optional-lane fold rejected as load-bearing; legacy edges move to Step 5 |
| 3 | Green baseline on one commit: rerun journey09/V25 on the checkout, `make check`, `make test`, `make fuzz`, analytics tests. | Done `b648399` (W12) |
| 4 | Installed acceptance: Mac 24–48h autonomous run, status/coverage/restart/restore/report; then BC-01; actual R2. | Pending (needs host actions) |
| 5 | Cutover per contract §6; delete reviewed retired R2 key set; remove legacy v3 code. | Pending |

### Baseline commit content (Step 0)

Kept from the dirty worktree: Pi extension `--no-session` early return plus Node
runtime test (`internal/capturehooks/pi.go`, `pi_runtime_test.go`, `testdata/pi-runtime.mjs`,
`nodejs_24` in `flake.nix`, CI `setup-node`); fixture coverage-metadata check before side
effects and final-coverage-failure regression; unsafe-dir `Chmod` test premise fix;
optional-repair test early-return; comment/help wording; documentation status edits.
Dropped (preserved on branch): dependency-resume bitmap and its tests; all uncommitted
`tools/` changes. Focused check on that tree: `go vet` on changed packages PASS;
`go test -race -count=1 ./internal/capturehooks/ ./internal/sessioncapture/ ./internal/cli/`
PASS (9.8s / 111.6s / 37.7s). Full gates not yet run.

### Ownership

- Parent (coordinator): brief, integration, commits to `main`, acceptance.
- Workers: Sonnet subagents in separate git worktrees under `/Users/mm/dev/memory-wt/`,
  all based on `65759df`, disjoint owned paths:
  - W1 `c6fff8a7-7de5-45f` — Step 1 runtime reliability; `step1-runtime`; owns
    `internal/sessioncapture/runtime*.go`, `queue.go` (pool only), error/claim paths in
    `optional_repair.go`/`enrichment_worker.go`, sessioncapture tests, CLI status files.
  - W2 `0bc0157c-b61f-4f0` — Step 2A analytics simplification; `step2-simplify`; owns
    `tools/analytics/**`.
  - W3 `4934fa6e-92a8-406` — read-only full-gate verification of `65759df`;
    `step3-verify`; no tracked edits.
  - Status: all three startup-confirmed by observed tool activity. W2 first commit
    `27a66d0` (local export via the same bounded `run()` as ssh; adds `--max-bytes`)
    parent-reviewed: direction accepted.
- Corrections:
  - C1 → W1 (sent): treat an op-deadline `SQLITE_INTERRUPT` as transient only once the
    op deadline has passed, including in the delivery/optional-repair failure paths, with a test;
    use the PRAGMA table_info column helper rather than error-string matching; keep a single
    error-tree walker; still owed: identify and fix the long connection holder, and release claims
    on Run start. Acknowledgement and uptake: pending.
- Capability: background start, status/wait, steering and resume are available.

### Verification and acceptance

- **W1 `1706c00` (accepted, parent-reviewed diff).** Transient step errors (op
  deadline, SQLITE_BUSY/LOCKED, INTERRUPT only after op expiry) skip the step instead of
  stopping Run; any fatal leaf stays fatal; failed steps release source/session/repair/
  enrichment claims; `InitRuntime` resets claims/working states; stderr
  `capture-v1: worker=<name> step skipped, will retry: …` and `runtime_destination`
  `step_errors/last_step_error/last_step_error_at`, which `capture-v1 status` JSON shows
  as `Runtime.StepErrors` etc. 8 new tests in `runtime_step_test.go`. Worker checks:
  vet, `make lint` 0 issues, `internal/cli` race ok; `internal/sessioncapture` race
  fails only on load-sensitive `TestRuntimeNetworkRetryWaitsForNewestRawACK` (fails on
  base too). Gap: requirement 2 (long holder of the single connection) not found
  by code reading and not measured. Most likely queueing among about 10 workers with
  `synchronous=FULL`. Measure via `step_errors` in Step 4.
- **W2 `27a66d0` → `c92bc61` (accepted).** Preserved tools/ diff triaged: no genuine
  contract defect found; nothing re-adopted (telemetry, bounded summary, refresh
  process/worker layering, baseline suppliers and versioned qualification tests dropped).
  Bounded summary/paging deferred to a measured slice. Analytics 97/97 and JS 6/6 pass.
- **W3, baseline `65759df` under load 8–33:** analytics 97/97 pass; fuzz 9/9 pass;
  `make check` stopped at `cover` on the ACK test above (coverage/CRAP/bench-smoke/
  actionlint not reached); `make test` run 2: capture-acceptance 12+6+6 pass, Go 30/31
  packages pass, `internal/cli` `TestRuntimeEnrichmentEnabledRunPublishesAndStatusDoesNotTakeLease`
  failed (5s deadline) and passed alone ×3. Journey 9 (= `test_periodic_capture_
  quarantine_and_restart_without_another_hook`) failed once with the coordinator
  exiting 1 and passed ×3. All failures: ≤5s wall-clock test deadlines or coordinator
  exit under load; none deterministic. "V25" and Qrace180 not reproduced by name.
  Logs: `/tmp/w3-*.log`. Required: a quiet-machine gate run (Step 3).

### Step 2B decision (parent, from read-only plan `c3586223-a644-4f1`)

The strict optional-lane fold is **not done**: the optional delivery lane, the extra
optional-delivery/repair workers and the v2 `withheld` wire type are load-bearing
(a stalled or forbidden artifact must not block transcript delivery; missing provenance
must stay visible and redacted; `TestOptionalNetworkStallDoesNotLockRawAcceptanceOrClaims`).
Also kept: `optional_repair_gap` + quota replay (sole record that no repair row could be
admitted), the `metadata_budgets` byte ledger (contract byte limits), enrichment
admission gap/intent, all authority/exclusion code. Reduced scope: drop append-only
`optional_repair_history` and `enrichment_history` (duplicate row state), with explicit
`DROP … IF EXISTS` of retired objects on init.

- W4 `e139aa4f-108c-4df` — coordinator-exit stress A/B (BASE `65759df` vs NEW
  `c92bc61`), worktrees `step3-verify`/`step2b-stress`, uncommitted diagnostic edit
  only. Correction C2 (sent): load reached 82; keep 6 `yes`, flag iterations with load >30.
- W5 `fec722e2-7dfe-4d1` — history-table removal; worktree `step2b-optional` (base
  `91630ce`); full suites wait until W4's load processes are gone.
- C1 → W1: acknowledged and taken up; verified in `1706c00` (single walker, PRAGMA
  helper, INTERRUPT handled only after op expiry — observed error was plain
  DeadlineExceeded, so INTERRUPT handling is defensive).

- **W5 `77276c4` (accepted, parent-reviewed diff).** Retired `optional_repair_history`
  and `enrichment_history`: inserts, budget entries and cap trigger removed; init drops
  the retired objects and the ledger is recomputed
  (`TestOpenDropsRetiredHistoryObjectsAndRecomputesBudget`). Behaviour change accepted:
  enrichment metadata-budget exhaustion is now row-level `retry` + `last_error` with
  bounded attempts → quarantine, not immediate `quarantined/quota`; raw delivery still
  proceeds. Deleted two history-only cap tests. Non-test −43, test −29 LOC. Worker
  checks on a quiet machine: `make lint` 0, race `sessioncapture` 92.8s ok and `cli`
  42.9s ok, capture-acceptance ok, analytics 97/97. Enrichment contract wording updated.
- **W4 stress A/B (completed).** 25+25 alternating CoordinatorJourney iterations at load
  16–31 (an earlier 20+20 at load ~100 was discarded): BASE 1 failure, NEW 1 failure.
  Both failures were the coordinator exiting 1 with `capture coordinator unavailable;
  replay/resume require stopped coordinator`. That message comes from
  `runCaptureWritable` (`internal/cli/capture_runtime.go`), which discards the
  `sessioncapture.Open` error. **So the journey flake is a separate defect in the Open path, which
  `1706c00` does not reach** (no `step skipped` lines were seen). Rate ≈1/125 coordinator
  test runs. Leading hypothesis (unproven): `initializeDB` issues `PRAGMA journal_mode=WAL`
  before `PRAGMA busy_timeout=5000`, so a concurrent read-only `work-status` reader
  during a coordinator restart gets an immediate SQLITE_BUSY. A flock race is also possible.

- **W6 `ef727a9`/`555ea6c` + parent `56bd2f9` (accepted).** Root cause of the journey
  coordinator exit was reproduced on the base code by `TestOpenWaitsForConcurrentReaderLock`:
  `Open` failed immediately with `database is locked (5) (SQLITE_BUSY)` while a read-only
  inspector held a SHARED lock, because `busy_timeout` was set after `journal_mode=WAL`.
  Fix: `busy_timeout(5000)` in the DSN of the writer (with `synchronous(FULL)`) and of all
  three read-only readers (`runtime_view.go`, `ownership.go`, `enrichment_status.go`), so it
  applies per connection before any locking statement. `runCaptureWritable` now wraps the
  `Open` error (`capture coordinator unavailable: %w`). Parent correction C3: a typed
  `ErrQueueOpen` sentinel replaces string matching (`TestOpenReportsQueueOpenSentinel`).
  Evidence: 25/25 CoordinatorJourney iterations under 6 `yes` processes (load 9–23), with all
  175 coordinator stderr files empty. That is weak statistical power against a ≈1/125 base rate,
  so it shows no recurrence rather than proof. Race `sessioncapture` 101s ok, `cli` 42s/37s ok,
  `make lint` 0, capture-acceptance ok. One load-sensitive failure
  (`TestVerifiedOptionalRepairRestartAutonomouslyAttachesLatest`) passed when rerun alone 3×.

- **W7 Step 3 gates on `b94bf24` (verifier; load ≈9–13 including the installed service).**
  Sub-gates each pass: fmt, lint 0, fix, tidy, deadcode, vuln (no vulns), bench-smoke,
  actionlint, `crap` (cover 87.0%, 0/10 over CRAP 30), capture-acceptance, analytics 97/97,
  `.mjs` 8/8, fuzz 9/9. Aggregate `make check` (cover step) and `make test` both failed on
  only `TestRuntimeNetworkRetryWaitsForNewestRawACK` (`runtime_fatal_test.go:253`,
  5s `priorDeadline` for the withheld raw ACK after a 1s optional operation expiry).
  Alone: 5 of 6 passes, 6.4–10.8s. **Parent disposition:** this is not accepted as noise. It
  has failed in every full-suite run (W3, W7 ×2), and the deadline may not be relaxed.
  W8 must determine what consumes the window (product retry/backoff latency or test
  scheduling) and fix the cause. W8 `d9d4126e-1116-4ef` dispatched (worktree
  `step3-ack` at `98fb4a9`).

- **W8 `d9d4126e-1116-4ef` (diagnosis accepted; no commit).** Temporary timestamps showed
  that the withheld-ACK window is consumed by product retry backoff, not by `busy_timeout`,
  `1706c00`, lane mutexes or test scheduling. Ordinary optional transport failure is
  withheld ≈65ms after operation expiry. With the `context.DeadlineExceeded` cause,
  `isolateOptionalFailure` rejects any deadline in the tree, so the job takes the
  `runtimeDeliveryFailure` retry/backoff path (1s, 2s, 4s…) and is withheld only on a
  later round. Pinning jitter was insufficient (2/20 failures). Isolating every optional
  deadline broke `TestOptionalIsolationRejectsFatalErrorTrees`, which correctly rejects a
  bare deadline on a live context. **Parent design for W9:** isolate an optional
  `SourceDependencyError` whose leaves are deadline-only *only when the operation
  context itself has expired*. This is the existing "genuine operation expiry" case
  that the latch-saving `WithoutCancel` path was written for. Live-context deadlines,
  cancellation, durability/fatal trees and destination failures stay non-isolated.

- **W9 `c3fd555`/`78d3f15` (accepted, parent-reviewed diff).** `isolateOptionalFailure`
  now isolates a deadline only via `genuineOptionalExpiry`: an optional
  `SourceDependencyError`, deadline-only leaves (existing `leavesAre`), and an expired
  operation context. Exclusion, fatal/durability, cancellation and destination checks
  still run first. `TestOptionalIsolationRejectsFatalErrorTrees` is unchanged, and the new
  `TestOptionalIsolationAcceptsGenuineOperationExpiry` covers the positive and negative
  cases for each stage. Withholding persists the latch and supersedes with a latest-state
  raw view, and never keeps or ACKs the old optional job, which matches the enrichment
  contract's genuine-expiry clause. Evidence (load ≈9–11):
  `TestRuntimeNetworkRetryWaitsForNewestRawACK` passed `-race -count=20` 0 failures ×2;
  `KeepsRawAlive` passed count=10 ×2; full `sessioncapture` passed `-race -shuffle=on` ×4;
  `cli` ok; vet clean; lint 0; capture-acceptance ok.

- **W10 aggregate gates on `551dbb2` (verifier; load ≈8–11).** `make check` exit 0 (cover
  87.1%, CRAP 0/10 over 30; nearest `runCaptureFixture` 29.40), analytics 97/97, fuzz all
  pass. `TestRuntimeNetworkRetryWaitsForNewestRawACK` did not recur. `make test` failed
  once, on `TestCaptureIsolationFixtureProcess` (`internal/cli/capture_isolation_fixture_test.go:22`,
  15s per-stage deadline; the crash-stage helper was killed at 15.08s). Alone 3/3 pass, but
  each takes ≈5–6s for a local fixture. **Parent disposition:** the deadline will not be
  widened. W11 measures where the crash stage spends its time (it waits for the
  withheld/`unavailable` raw view while the repair call blocks, with
  `OperationTimeout` 10s and one delivery worker) and fixes the cause.

- **W11 `66ee491` (accepted, parent-reviewed diff).** Product race: `beginOptionalDelivery`
  inserted the `optional_repairs` row as `pending` and only then set `delivering`. The repair
  lane could claim it in between with an unconditional `SET state='working'`, which duplicated
  the upload and added a >=1s `retryDelay` (6/40 helper runs). Fix: delivery inserts
  `delivering` in one statement, and the repair claim is a guarded
  `WHERE state IN ('pending','retry')` compare-and-set. No deadline, fixture or W9 guard
  changed. Evidence (load 7–9): `^TestCaptureIsolationFixture` race ×10 pass, `cli` race
  shuffle ×3 ok (≈31s), `sessioncapture` ok 82s, vet, lint 0, capture-acceptance ok.
  **Gaps:** no deterministic regression test for the SELECT→UPDATE race window, and W10's
  15s kill is not fully explained beyond this 1s race. The next aggregate `make test` is
  the check.

- **Step 2 CLI cleanup `a3bde3e` (parent, right-sized).** Caller audit: the capture-acceptance
  journeys (`tools/analytics/test_capture_journey.py`) run `fixture --backend sdk-fixture` and
  `isolation-fixture` through the real binary. Moving them into test helpers would need a
  second test binary, which adds complexity, so they stay as unlisted harness commands.
  The `*-status`/`*-replay`/`current-heads`/`publication-status` commands are the contract's
  quarantine/replay and analytics consumer surface, so they are kept. The usage text now lists
  the real operator set (it had omitted the `optional-*` commands) and leaves the fixtures
  out. Breaking the legacy import edges (`launch`, `snapshot`, `hook`) moves to the Step 5
  legacy removal. `cli` race shuffle ok 33s, lint 0.

- **W12 aggregate gates on `b648399` (verifier; load ≈7–15 including the installed service).
  Step 3 CLOSED.** `make check` exit 0 (647s; cover 87.0%, CRAP 0/10 over 30, nearest
  `runCaptureFixture` 29.40), `make test` exit 0 (619s; capture-acceptance 12+6+6 and
  `go test -race -shuffle=on ./...` all packages), analytics 97/97, `.mjs` 8/8, fuzz 9/9 with
  no crashers. Exit codes were taken with `pipefail`. Caveats: one green run on one load
  profile. `TestCaptureIsolationFixtureProcess` elapsed time is not logged without `-v`.
  Not run: live R2, installed-path, BC-01 and cutover acceptance (Step 4–5).

- **Step 4 pre-install finding (parent, read-only, 2026-10-28).** Installed Mac service:
  binary `b23712bc` (`vcs.modified=true`), launchd `KeepAlive`, `poll_seconds` 10,
  `operation_seconds` 60, 2 capture / 2 delivery workers. stderr has 759 lines, all
  `context deadline exceeded` (the Run-fatal bug fixed in `1706c00`/`ef727a9`). The current
  process (up 3h+) is not crash-looping; it runs at ≈295% CPU. `sample` (stacks only) shows
  `lstat`/`stat`/`getdirentries`/`read`, under `captureNext → capture →
  validateUnchangedManifest → usage()` (the full scan of 17,195 payload files) plus a full
  re-read/hash of the source. Spool counts: 19,629 runtime sources, 20,104 jobs (14,272
  pending, 4,789 published, 15 quarantined). In 60s about 1,300 sources went `captured` →
  `pending` while the job count stayed at 20,104. **Defect (present on `main`):** every
  discovery sweep calls `Hint` for every source, and its UPSERT (`runtime_store.go:103`)
  always does `version=version+1` and sets `captured`→`pending`. So all unchanged sources are
  re-read and re-hashed every sweep, and a capture in progress is reset to `pending` when the
  version moves under it. Installing `main` unchanged would not fix this, so Step 4 is
  blocked on it. W13 fixes it. The per-capture `usage()` scan stays (a fail-closed integrity
  sweep) and is re-measured after the churn fix.

- **W13 `4a1cfec` (accepted, parent-reviewed diff).** Discovery attaches a stat
  fingerprint (dev, ino, size, mtime_ns, ctime_ns) only to untransformed sources (from
  `classifyInfo`/`classifyExternal`). `Request.Fingerprint` is `json:"-"`, so no persisted
  or publication bytes change. The new `runtime_sources.fingerprint` column (additive)
  stores a digest of the fingerprint plus the request with `ObservedAtNS` zeroed. The
  `Hint` UPSERT is a no-op only when the row is present and the non-empty fingerprint
  matches. Transformed, SQLite, hook, inbox, native, manual and enrichment hints always
  bump. `RowsAffected==0` + no row still returns `ErrQuota` (cap preserved). Policy:
  discovery's `allowed()` still runs on every source every sweep, and delivery rechecks
  separately, so skipping only drops the redundant re-read/hash. Per-source `observed_at`
  now means "last observed change"; sweep freshness stays in the discovery tables.
  Synthetic 2,000-file measurement: per-sweep version bumps 2000 → 0 and row writes
  2000 → 0; discover 267–300ms → 139ms. Evidence: vet clean, lint 0, race shuffle
  `sessioncapture` 92.8s / `capturesource` / `capturedaemon` / `cli` 39.3s ok, and
  capture-acceptance ok. **Gaps:** no end-to-end SQLite-WAL source test (covered by
  "transform ⇒ no fingerprint"). Coarse-timestamp filesystems could miss a change. Each
  source re-captures once after upgrade.

- **W14 aggregate gates on `fcc8b03` (verifier; load ≈7–10).** `make test` exit 0, analytics
  97/97, `.mjs` ok, fuzz 9/9. Aggregate `make check` stopped in `cover` on
  `TestCaptureIsolationFixtureReturningStages`: in coverage mode the `-race -cover` crash
  binary was compiled *inside* the 15s stage deadline and took 13.6s under parallel load,
  leaving 1.4s for the fixture. Every sub-gate after that passed when rerun (crap/cover 87.0%,
  0/10 over CRAP 30, bench-smoke, actionlint, capture-acceptance). **Parent fix `0504ed1`
  (test harness only):** the coverage build now runs before the stage deadline starts, and the
  15s limit bounds only the fixture run (unchanged). With the `make cover` flags
  (`-covermode=atomic -coverpkg=./... -args -test.gocoverdir`), `^TestCaptureIsolationFixture`
  passed ×3, build ≈0.7s outside the deadline, and e2e coverage files were still written.
- **Parent process incident (2026-10-28, repaired).** A `git stash`/`stash pop` wrapper
  around a fast-forward ran with nothing to stash, so `pop` applied an unrelated older
  `improve/capture` stash and left 13 conflicted tracked files on `main`. The parent restored
  exactly those files to `HEAD`. The stash entry is retained (`stash@{0}`), no commit was
  affected, and untracked research files were untouched. Rule from now on: no stash wrappers;
  commit brief edits before merging.

- **Step 4 Mac install (parent, 2026-10-28 03:58–04:12 AEDT; operator-authorized).**
  The installed service had restarted 582 times under launchd (`last exit code = 1`).
  Pre-swap counts were recorded. The service was stopped via `launchctl bootout` (stopped in
  1s; the clean shutdown checkpointed the WAL). Backup to
  `~/.local/state/agent-sessions-capture/backups/step4-20261028/` (0700): `queue.sqlite`
  byte-identical, `quick_check ok`, old binary `agent-sessions.bin.b23712bc` (sha256
  `e69fb2ed…`). Only `agent-sessions.bin` was swapped atomically; the plist, launcher
  wrapper, credentials and config are unchanged. New binary: `c8abfd1`, `vcs.modified=false`,
  sha256 `91a736f0…`, built from a detached clean worktree. Rollback: bootout, restore both
  files from the backup, bootstrap. First 5 minutes: 0 step errors, no new stderr lines,
  one-time fingerprint pass ≈300 sources/min.
- **Stale destination pause (found during Step 4).** `runtime_destination.paused=1,
  reason='destination'` had been set since 2026-10-26 07:10 AEDT, so nothing was delivered
  for ≈45h (published stayed at 4,789). R2 itself was healthy: the analytics refresher
  reported 0 endpoint/target errors, and a read-only `current-heads` through the service's
  own wrapper exited 0 with a complete listing. The documented operator `capture-v1 resume`
  was run with the coordinator stopped (exit 0, paused=0) and the service restarted. In the
  following 4 minutes published rose 4,902 → 5,435 (≈175/min), pending fell 14,119 → 13,586,
  the destination stayed unpaused, and step_errors stayed 0. **Suspected defect:**
  `captureDestinationBackend.get` (`internal/cli/capture_backend.go`) joins `ErrDestination`
  onto any unbound `ReadArchive` failure except cancel/deadline/durability. A generic
  transport failure (`publication destination operation failed` from
  `publicationr2.safeError`) therefore pauses the destination indefinitely, contrary to
  contract §3 (transient transport → backoff; only auth/config → pause). The existing
  mapping test covers only `bound: true`. W15 fixes this.

- **W15 `53f8a8f`, accepted (destination pause classification).** The unbound
  archive read in `captureDestinationBackend` is split into `open`/`bind`/`get`. `bind`
  pauses only on `ErrDestinationPaused` (401/403), `ErrNotFound` (missing binding),
  `publication.ErrInvalidMetadata` (a new marker for an archive document that was read in
  full but is oversized, malformed or unsupported) and archive identity mismatch (with
  `ErrIntegrity`). Transport, 5xx, throttling and mid-stream read failures now follow the
  same per-job backoff → bounded quarantine as the bound path (contract §3). The archive
  gate before namespace writes is unchanged: a failed read never binds. Tests:
  `TestCaptureDestinationUnboundArchiveReadClassification` and
  `TestTransientUnboundArchiveReadRetriesWithoutPause`; existing tests unmodified. Worker
  gates: vet, lint 0, `-race` on cli/sessioncapture/publicationr2/capturebackend/publication,
  and capture-acceptance all passed. Parent review: the `invalidMetadata` wrap is also
  reached by manifest reads, but nothing else matches the marker, so behaviour there is
  unchanged. The live spool's 15 quarantined delivery jobs (4 `timeout`, 11 `transient`,
  2026-10-24/25) predate this change and came from the bound path; they can be replayed.
  Not yet installed on the Mac.

- **W16 + parent gates on `856cc08`: all green.** W16's first `make check` exited 2 only
  because the gate worktree had no `.analytics-venv`, so the Makefile's `ANALYTICS_PYTHON`
  fell back to system `python3`, which lacks `duckdb`. The parent then reran the full
  `make check ANALYTICS_PYTHON=/Users/mm/dev/memory/.analytics-venv/bin/python` once, as the
  only gate running: exit 0 in 628s, coverage 87.1%, 0/10 functions over CRAP 30. W16 also
  passed `make test` (482s), analytics 97/97, `.mjs` 8/8 and fuzz 9/9. Rule for gate
  worktrees from now on: always pass `ANALYTICS_PYTHON` explicitly.
- **Mac install of `856cc08` (parent, 2026-10-28 04:58 AEDT).** Used the same procedure:
  bootout (stopped in 1s), backup in `backups/step4b-20261028/` (`queue.sqlite` byte-identical,
  `quick_check ok`, previous binary `agent-sessions.bin.c8abfd1`), atomic swap of
  `agent-sessions.bin` only, then bootstrap. The binary is `vcs.modified=false`, sha256
  `e88853c5…`. In the first 6 minutes published went 12,896 → 13,578 with no pause, 0
  step_errors and no new stderr lines.
- **`sqlite-backup` clean-closed WAL failure (found after install, predates it).** One
  `sqlite-backup` artifact source was quarantined: kind=capture, reason `transient`, 8
  attempts. Its last successful capture was 2026-10-24. The file's header is in WAL mode
  (bytes 18/19 = 2/2) but no `-wal`, `-shm` or `-journal` sidecar exists, which is the normal
  state after the last connection closes cleanly. Hypothesis: the read-only authorized VFS
  cannot open or create the absent WAL, the resulting error is unclassified, so the failure
  repeats on every attempt and ends in quarantine. Any captured WAL-mode database whose app
  has closed would hit the same failure. W17 is reproducing and fixing it with synthetic
  fixtures.
- **Residual re-hint churn on transform sources (measured, not yet addressed).** The W13
  fingerprint only applies when `transform==""`. Over 90s, the 7 `sqlite-backup` rows gained
  1 version bump and the 71 `scrubbed-config` rows gained 13. This is small next to the
  pre-W13 churn (19.6k rows every sweep), so it is recorded for measurement after the backlog
  drains, not fixed speculatively.

- **W17 `79eef7d`, accepted (`sqlite-backup` clean-closed WAL).** Reproduced with a synthetic
  fixture: a WAL-mode DB closed cleanly (header 2/2, no sidecars) made `BackupSQLiteAuthorized`
  fail with `unable to open database file (14)` (`SQLITE_CANTOPEN`). The failure is
  unclassified, so it counts as `transient` and ends in quarantine. Fix: `backupSQLitePinned`
  adds `immutable=1` only when no `main.db-wal` is pinned. The pinned main file is then the
  complete committed state. The WAL-present URI is unchanged, because `immutable` would drop
  WAL rows. Guards kept: journal rejection, `authorizeSQLitePair`, pins, `checkSQLitePair`
  before backup, after backup with a rehash, and at the end (including `checkSQLiteAbsence` /
  "WAL appeared"), and the read-only VFS. Tests: `TestAuthorizedSQLiteCleanClosedWALBacksUp`
  checks rows, an unmodified source and no sidecars created.
  `TestDerivedSQLiteAuthorizedCleanClosedWALCaptures` goes capture → deliver → export → row
  count. Both passed ×20. Existing tests unmodified; lint 0; `-race` on
  adapters/sessioncapture/capturesource and capture-acceptance passed. Classification of
  opaque SQLite format errors is unchanged; this remains a follow-up risk (there is no
  deterministic sentinel). Not yet installed.

- **W18 gates on `5c8e9cd`: all green** (`make check` 625s, cover 87.0%, 0/10 functions over
  CRAP 30; `make test` 456s; analytics 97/97; `.mjs` 8/8; fuzz 9/9). **Mac install of `5c8e9cd`**
  (parent, 2026-10-28 05:45 AEDT): same procedure, backup in `backups/step4c-20261028/`
  (byte-identical, `quick_check ok`, previous binary `.856cc08`), sha256 `c0bfe4b8…`. Capture
  quarantine 16 (the clean-closed WAL `sqlite-backup` source) was replayed while the
  coordinator was stopped. It captured on the first attempt (version 267 = captured 267).
- **Delivery cost grows with session size (found during Step 4; a product defect).** Every
  publication re-handles the session's *entire* object set:
  1. `uploadDependencies` → `PutFile` (an `IfNoneMatch` PUT) for each selected object;
  2. for an object that already exists, `reconcileImmutable` does a GET and a full re-hash;
  3. `Publish` → `verify` streams every object again.
  So each publication costs O(session objects), and a session's lifetime cost grows
  quadratically. Live evidence (metadata only):
  - none of the 16,551 published jobs has more than 99 sources;
  - none of the 792 pending jobs with 100 or more sources has ever published;
  - all 16 active delivery quarantines sit exactly one sequence past their remote head (remote
    = seq−1, a different publication id, so not a conflict), and most have every payload
    present locally;
  - quarantine 18 (`timeout`, new binary) is a 78-source session with a 27MB main revision.
  Each quarantined head blocks its whole session (1,017 later jobs blocked), and attempts that
  run to the 60s budget tie up both delivery workers, so throughput fell from ≈170 to ≈30
  publications per minute. W19 fixes this.
- **Shutdown cancellation counts as a failed attempt.** Quarantine 17 was written at the same
  second as the parent's `launchctl bootout`. `runtimeDeliveryFailure` (and the capture-side
  failure path) records through a `WithoutCancel` context, and `classify` has no case for
  `context.Canceled`, so a coordinator shutdown spends a `transient` attempt and can
  quarantine. Separately, `Replay` resets a quarantined delivery job to `pending` without
  checking its state, so replaying quarantine 5 (whose job has since been `superseded`) would
  revive a stale sequence. W20 fixes both.

- **W20 `ae8e20d`, accepted (shutdown cancellation and replay guard).** The new
  `shutdownCancellation(opCtx, err)` is true only when the op context ended by cancellation
  of its parent (not its own deadline) and every leaf is `context.Canceled`. Delivery
  restores the attempt count read before the attempt (`state='retry'`, `next_retry=0`,
  diagnostic `last_error='shutdown'`, guarded against `published`/`superseded`), writes no
  quarantine row and still returns the cancellation so Run stops. Capture skips the failure
  bookkeeping; the deferred `releaseSourceClaim` (`WithoutCancel` + bounded release timeout)
  returns the claim. Op-deadline expiry still spends an attempt as `timeout`. Mixed or fatal
  causes still take the old path and stop Run. `Replay` revives work only while it is still
  `quarantined`; otherwise it deactivates the row (kept and inspectable) and returns
  `ErrReplayResolved` (contract §3: "neither success nor silent disposal"). Tests in
  `runtime_cancel_test.go` (passed with `-race -count=20`); existing tests unmodified
  (`TestRuntimeFatalOptionalBoundaries/*/pure` motivated the non-empty `last_error`). Worker
  gates: lint 0, `-race` on sessioncapture/cli, capture-acceptance. Parent review confirmed
  the claim release and the published/superseded guard. Not yet installed.

- **W19 `d7f9364`, accepted (per-publication delivery cost proportional to the delta).**
  `publication.TrustedObjects` / `trustedFrom` trust an object only when its full `Object`
  descriptor (key, encoding, both digests, both sizes) equals a selected object of the remote
  current's manifest. That manifest must pass `parseCurrent`, a checksum-checked `resolve` and
  `advances`. Any failure returns nil, which means the full path. `uploadDependencies` skips
  only `PutFile` for trusted objects; every `recheck`/`recheckScopes` call and fault point
  stays per object. `Publish` uses `verifyExcept` with trust recomputed from the prior it
  actually read, so if the current moves between reads the worst case is a failed attempt, not
  an unverified publish. `Read`/export still verify the full closure. Invariant (code
  comments): an object referenced by a published manifest stays present until the reviewed §6
  cutover deletion (§4 "Begin without automatic GC", §6 step 6). Cutover deletion must honour
  this. Measured on Local: publication 12 went from 12 PUT / 12 opens to 1 / 1, with constant
  metadata operations (3 Current + 3 manifest opens). Tests: `TestDeliveryCostIsProportionalToDelta`,
  `TestTrustedObjectsFallbacks`, `TestReadStillVerifiesTrustedObjects`; existing tests
  unmodified. Worker gates: lint 0, `-race` on sessioncapture/publication/publicationr2/cli,
  capture-acceptance. Rebased onto W20 and the combined focused tests passed. Residual: local
  spool `q.verify` and the exact-retry ("already published") full `verify` are still
  O(session); not measured on R2. Not yet installed.

- **W21 gates on `362ac12`: all green** (`make check` 629s, cover 87.1%, 0/10 functions over
  CRAP 30; `make test` 487s; analytics 97/97; `.mjs` 8/8; fuzz 9/9). **Mac install of `362ac12`**
  (parent, 2026-10-28 06:39 AEDT): same procedure, backup in `backups/step4d-20261028/`
  (byte-identical, `quick_check ok`, previous binary `.5c8e9cd`), sha256 `675bfd2a…`. With the
  coordinator stopped, every active delivery quarantine was replayed: 17 revived (exit 0), and
  quarantine 5 returned `ErrReplayResolved` ("delivery work is superseded"), as designed. Active
  quarantines went 18 → 0.
- **Remaining large-session cost: per-object policy rechecks are O(K²) (found after install).**
  After 10 minutes on `362ac12`, still no publication with 100 or more sources, and 5 jobs were
  in `timeout` retry. A CPU sample (`sample`, read-only) put delivery time in `uploadOne` →
  `recheck`/`recheckScopes` → `checkObserved` → `capturepolicy.Resolve`. `uploadOne` rechecks the
  whole session's evidence twice per selected object. Runtime `CheckPolicy`
  (`runtimeFilesystemPolicy`) resolves the path and builds `capturepolicy.New` per config ×
  protected state. So a publication costs ≈4K² expensive checks (≈90k at K=150). W19 skipped
  only the `PutFile` call for trusted objects; their rechecks still run. Contract §3: "Recheck
  exclusions before delivering pending work". W22 keeps the per-upload recheck for every
  object whose bytes are uploaded and drops it for trusted (not uploaded) objects; the full
  rechecks before uploads and before the root commit stay.

- **W22 `3cee11b`, accepted (delivery policy rechecks linear in session size).** `uploadOne`
  no longer rechecks around trusted objects (no bytes sent). For each uploaded object it runs
  one full recheck (scopes + selected evidence) after the `upload-before` fault point and
  immediately before `PutFile`, replacing two back-to-back pairs. Full rechecks remain in
  `verifyDependencies` (before the first upload), after the upload loop, before the manifest
  `PutFile` and at the root `CompareAndSwap` (contract §3). No caching of `CheckPolicy`. The
  fault points still fire for trusted objects (existing tests count them). Measured
  `CheckPolicy` calls per publication: K=10 1,270 → 320; K=50 26,350 → 1,600; K=150 229,050 →
  4,800 (32×K). Tests: `TestDeliveryPolicyChecksAreLinearInSessionSize` (≤40×K) and
  `TestDeliveryExclusionBlocksWithTrustedObjects` (exclusion between captures, at
  `upload-before` on an uploaded object, and at `manifest-before` on a trusted source all
  block publication). Existing tests unmodified; worker gates lint 0, `-race` on
  sessioncapture/publication/cli, capture-acceptance. Residuals: per-call cost of
  `runtimeFilesystemPolicy` (`Resolve` + `capturepolicy.New` per config × state) is unchanged
  and unmeasured against the live 60s budget; first publications and full-path fallbacks
  (D=K) remain O(K²). Not yet installed.

- **W24 gates on `1ad50ea`: all green** (`make check` 613s, cover 87.1%, 0/10 functions over
  CRAP 30; `make test` 451s; analytics 97/97; `.mjs` 8/8; fuzz pass), each run under
  `caffeinate -i -s` on AC power with no sleep events. W23's earlier run was invalid, not a
  failure: the Mac entered clamshell sleep on battery at 07:09:37 during `internal/syncer`.
  The transient delivery retries seen afterwards line up with DarkWake windows (07:19, 07:37,
  08:41) and cleared once the Mac was fully awake. **Mac install of `1ad50ea`** (parent, 09:40
  AEDT): backup in `backups/step4e-20261028/` (byte-identical, `quick_check ok`, previous binary
  `.362ac12`), sha256 `3a5be6d6…`. With the coordinator stopped, quarantines 20 and 21 were
  replayed (exit 0), leaving 0 active. Within 10 minutes, sessions with 100 or more sources were
  publishing (1 → 5).
- **Remaining defect: near-first publications are O(D×K) (found after install).** Quarantine 22
  (`timeout`, 8 attempts) is seq 124 of a session last published at seq 13. Superseding folded
  seqs 14–123 into it, so it must upload D=110 of K=123 objects (28.6 MB encoded) in one 60s
  attempt. A new CPU sample is still dominated by `capturepolicy.Resolve`, `checkObserved`,
  `capturepolicy.New`, `ReadOwnership`, `adapters.All`, `excludes` and `isWithin` under
  `uploadOne`, `recheckScopes` and `verifyDependencies`. Each uploaded object still triggers a
  full-session pass (scopes + selected evidence), and every source check reloads the policy
  inputs: the exclusion registry, a `capturepolicy.New` for each config × protected state, the
  coordinate inventory and the adapters. 254 pending jobs have K=200–378, while the largest K
  ever published is 151. No code reads per-job upload progress, so retries restart from zero.
  Decision: keep the privacy window unchanged (a full-session recheck immediately before every
  `PutFile`). First cut the per-check constant by reading policy inputs once per pass, fresh for
  every pass and never shared between passes. Measure D=K at 378 and 1000 before deciding
  whether resumable upload progress is also needed.

- **W25 measured, no fix (stopped per brief).** A synthetic runtime delivery `CheckPolicy` call
  takes 2.6 ms with 40 exclusion records (registry `Load` + `Excludes` ≈ 78%, `capturepolicy.New`
  ×6 ≈ 5%, `runtimeFilesystemPolicy` ≈ 10%). The live registry has 0 records, so the live split
  is dominated by `Resolve`, `New` and privacy-owner derivation, as the CPU sample showed. Either
  way, a per-pass snapshot cuts only the constant: (D+4) full passes × 2K calls stays O(D×K)
  (projected D=K: K=123 ≈ 81 s, K=378 ≈ 750 s against the 60 s budget).
- **Decision (parent, 2026-10-28): per-upload recheck covers the uploaded object's own evidence;
  full-session passes stay at the batch boundaries.** Contract basis: §3 "Recheck exclusions
  before delivering pending work", and the enrichment contract "delivery still checks every
  selected original identity, including immediately before CAS". Neither requires a
  whole-session pass before each object. Full-session passes (scopes + every selected identity)
  remain before the first upload (`verifyDependencies`), after the upload loop, before the
  manifest `PutFile` and at the root `CompareAndSwap`. Immediately before each `PutFile`, the
  object's own evidence gets scopes + full check, which catches path, credential and
  claimed-session exclusions for the bytes being sent. Accepted residual: an exclusion that
  lands mid-batch and matches only a *different* source's identity stops further uploads at
  the post-loop pass rather than at the next object. It still blocks publication, and an
  exclusion that lands mid-batch already leaves earlier uploads and earlier publications in R2
  (privacy recovery, not delivery, removes those). Cost becomes O(K) per publication.

- **W26 `5bb81d9`, accepted (own-evidence per-upload recheck).** Implements the decision above.
  `uploadOne` takes the evidence index; immediately before each `PutFile` it runs
  `recheckScopes` + `check` on that object's own evidence only. A missing evidence slot fails
  closed with `ErrIntegrity`; this is unreachable today because `verifyDependencies` already
  rejects `len(es) != len(m.Sources)`. The full-session passes (`verifyDependencies`, post-loop,
  manifest `PutFile`, root `CompareAndSwap`) are unchanged. `CheckPolicy` calls for one
  publication with D=K: K=10 770 → 320; K=50 13,850 → 1,600; K=150 116,550 → 4,800; K=378
  724,626 → 12,096 (32×K). New tests: `TestFirstPublicationPolicyChecksAreLinear` (≤40×K),
  `TestUploadExclusionOfOwnPathPreventsPut`, `TestUploadExclusionOfLaterSourceBlocksPublication`
  (the later excluded object is caught by its own check and never uploaded) and
  `TestUploadSessionExclusionMidBatchBlocksPublication`; each asserts the excluded key is absent
  and nothing became current. Existing tests unmodified; worker gates: lint 0, `-race` on
  sessioncapture/publication/cli, capture-acceptance. Projected at 2.6 ms/call (upper bound):
  K=123 ≈10 s, K=378 ≈31 s, K=1000 ≈83 s (over the 60 s budget; no live session that large is
  pending, max K=378). Not yet installed.

- **W27 gates on `2e6b3ec`, then parent fix `34a586f`: green.** W27: `make test`, analytics
  97/97, `.mjs` 8/8 and fuzz all passed, but `make check` failed twice (first run on battery at
  load 12–40, parent rerun at load ≈10). Both times `internal/syncer` hit the default 10m
  `go test` timeout under `make cover` (600.9s, 600.7s); `TestSimulatedSessions` alone passed
  3/3. Cause: W26's `TestFirstPublicationPolicyChecksAreLinear` at K=378. Its fixture makes K
  sequential captures, each writing a K-sized manifest, so setup is O(K²): 83s under race, and
  `sessioncapture` under cover went 173s → 407s, which competes with `syncer` for CPU. `34a586f`
  limits that test to K=10/50/150 (13.5s; same counts, 32×K; bound and assertions unchanged; the
  40×K bound already fails the old code at K=10). Parent `make check` on `34a586f`: exit 0 in
  800s, cover 87.2%, 0/10 functions over CRAP 30, AC power, no sleep. Gates 2–4 were not rerun:
  the production code is identical to `2e6b3ec`. **Risk:** `internal/syncer` (legacy) took 597.4s
  against the 600s package timeout under cover. The timeout is not widened. Retiring legacy v3
  (Step 5) removes this package; until then, a cover failure in `syncer` near 600s needs a load
  and timing check before it is treated as a regression.

- **Mac install of `88451ad`** (parent, 2026-10-28 11:26 AEDT; production code identical to
  `34a586f`): backup in `backups/step4f-20261028/` (previous binary `.1ad50ea`), sha256
  `3177862d…`. With the coordinator stopped, quarantine 22 was replayed (exit 0), leaving 0 active.
  **Backup finding:** unlike earlier installs, `queue.sqlite-wal`/`-shm` were still present
  after `bootout`, most likely because a short-lived reader (the `publication-refresh` service
  spawns `agent-sessions.bin` children) held the spool open, so close did not checkpoint. The
  plain `cp` of `queue.sqlite` matched the pre-stop counts exactly, and the parent also took an
  online SQLite backup-API copy (`queue.online.sqlite`, `quick_check ok`) as the stronger
  rollback point. **Procedure change for future installs:** after stopping, if sidecars exist,
  back up through the SQLite backup API (read-only source), not `cp` of the main file alone.
  **Result:** within 12 minutes, 0 retries, 0 quarantines, no pause; the largest published K
  went 151 → 219, with 30 publications at K≥200 and 183 at K≥150. 224 pending jobs with K≥200
  remain (max 378), draining at about 7–9 publications per minute. No retry-cost problem was
  observed, so resumable upload progress is not pursued.

- **Load episode after the `88451ad` install, and W28 `8ec8282`, accepted.** From about 11:50
  AEDT, unrelated host work (other agents' `syncer.test` runs and a `turbo`/`bun` build) pushed
  load to 30–79 with heavy disk I/O. Delivery steps then ran past the 60s operation deadline
  (stderr gained 8 `step skipped, will retry: context deadline exceeded` lines, and
  `step_errors` rose). There was no pause and no quarantine, and publishing continued at a
  reduced rate. One job was recorded `integrity-error` while its siblings recorded `timeout`.
  Cause: in `Queue.verify`, the decode `io.Copy` reads through `contextReader`, but any copy
  error was returned as `ErrIntegrity: decode` with the cause dropped. `classify` tests
  `ErrIntegrity` before `DeadlineExceeded`, so a timeout was labelled integrity, and a
  shutdown during decode spent an attempt (it defeated W20's pure-cancellation check). Fix:
  when `ctx.Err() != nil` and the copy error is that context error, `verify` returns it
  unwrapped; every other decode failure still returns `ErrIntegrity`. Tests in
  `verify_ctx_test.go`: a context end during decode is not integrity (cancel and deadline;
  deadline classifies `timeout`); corrupt payloads with a live context stay integrity
  (encoded hash, decoded hash/size, corrupt zstd); end to end, a shutdown during dependency
  decode restores attempts with `last_error='shutdown'` and no quarantine. The first and third
  fail without the fix. Existing tests unmodified; worker gates: lint 0, `-race` on
  sessioncapture/publication/cli, capture-acceptance. Audit residual, not fixed:
  `enrichment_result.go:49` can still label a DB deadline as integrity. Operational note: an
  unindexed read-only scan of the 1.4 GB spool timed out under that I/O load; monitoring uses
  small, indexed queries only.

- **W29 gates on `59dfc99`: green after one diagnosed rerun.** `make check` exit 0 (cover 87.2%,
  0/10 functions over CRAP 30; `syncer` cover 536.6s); analytics 97/97; `.mjs` 8/8; fuzz pass; AC
  power, no sleep. W29's `make test` failed once on
  `TestRuntimeRemoteInspectionAndReplayNeverRequireCredentials` (`internal/cli`,
  `capture_configured_test.go:103`, its fixed 5s wait for capture + pause). It took 11.84s
  against a normal 0.6–1.5s, with load 11–16. Parent diagnosis: with W29's shuffle seed
  (1793151606356515000) it passed 5/5, and 25/25 while the `syncer`/`snapshot`/`sessioncapture`
  race suites ran alongside. So it is not order-dependent and was not reproduced under
  load. W28 changed only the `verify` decode path, which this test does not reach. Parent
  rerun of `make test`: exit 0 (`syncer` 593.2s, `sessioncapture` 349s, `cli` 138s). The
  wait is not widened. **Risks:** that 5s wait can starve under heavy host load; `syncer`
  (legacy) is within 7s of the 600s package timeout under `make test` too. Both are recorded,
  not fixed; Step 5 removes `syncer`.

- **Mac install of `463ddf3` (W28), and steady-state measurements** (parent, 2026-10-28
  13:31 AEDT). Before the stop, the backlog had fully drained (0 pending, 0 retry, 0 active
  quarantines). No WAL sidecars were present after `bootout`. The spool was backed up through
  the SQLite backup API to `backups/step4g-20261028/` (7.1s, `quick_check ok`, counts identical
  to the pre-stop read); the previous binary is kept as `.88451ad`. New sha256 `7de38051…`.
  Measured after the install (read-only):
  - **Delivery:** idle. In 2 minutes, 29 sources were re-observed, 2 captured and 2 jobs created.
    The stderr log is unchanged since 12:09:48; all 17 lines added after 11:50 are
    load-episode `context deadline exceeded` step skips.
  - **Discovery and freshness:** `poll_seconds` 10, three scopes (claude, codex, pi), each
    `bounded` with 1,365 entries examined per batch. A bounded batch takes about 50s and a
    complete sweep about 7.5 minutes at load ≈5 (generation 2596 started 13:48:20 and completed
    13:56:09). Generation 2595 carried `sweep_error='timeout'` from a batch that ran past 60s,
    and the last complete sweep before that was 12:29:34. So under host load, freshness falls
    back to at least one ~8 minute sweep after load drops, and a timed-out batch leaves the
    generation visibly incomplete, as the contract requires. This was not fixed; it is recorded
    for the autonomous run.
  - **Idle CPU:** about 180–250%. It is dominated by discovery (`discoverLoop` →
    `capturesource.Discover` → full runtime policy per walked entry: `runtimeExclusions`,
    `runtimePrivacyOwner`/`ReadOwnership`, `capturepolicy.Resolve`), then the capture,
    native-inbox and enrichment loops. This is the dominant steady-state cost. A candidate
    follow-up is a policy-input snapshot per discovery batch, which needs the same freshness
    argument as W25. It is measured, not yet designed.
  - **Enrichment:** 1,516 `attached`, 591 `quarantined` with `last_error='quota'` (459 written
    by the pre-capture-v1 `c9749dac+dirty` build, 132 by `c8abfd1` at 8 attempts). These are
    durable and visible, not lost. They need an operator review against the metadata budget
    before any replay.
  - **Discovery issues:** 3,160 active `protected_source_requires_scrub`, 225
    `executable_source_requires_review`, 87 `policy_denied`, 25 `adapter_omission`, 24
    `unsupported_compressed_source`, 3 `launch_snapshot_integration_required`. These are
    explicit coverage gaps for the BC-01/R2 acceptance inventory.
  - **Disk:** 24G free (95%); install backups total about 10G across `step4`–`step4g`.

- **24–48h autonomous run started** (parent, 2026-10-28 13:57 AEDT, installed `463ddf3`, AC
  power). The read-only monitor `/tmp/step4-diag/autorun.py` runs under `caffeinate -i -s`
  (300s interval, 48h). It writes metadata only to `/tmp/step4-diag/autorun.jsonl`: power, load,
  service CPU and uptime, launchd runs and last exit, job and source state counts, pause,
  `step_errors`, active quarantines by class, retry classes, discovery generation and
  `completed_at`, stderr line count, and free disk. Baseline at 14:02: 19,514 published, 0
  pending, 0 retry, 0 active quarantines, no pause, discovery generation 2596 complete at
  13:56:09, stderr 784 lines, 24.2G free. The installed binary is not changed during the run.
  **Acceptance criteria for the run:** no unplanned restarts (`runs = 1`), no destination pause,
  every new delivery or capture quarantine diagnosed to a cause, discovery `completed_at` keeps
  advancing, and stderr growth is explained.

- **Backups pruned (operator-approved, 2026-10-28 15:04 AEDT).** `step4g-20261028` was confirmed
  as the rollback point (`quick_check ok`, counts match the pre-stop read, `.88451ad` binary).
  The original pre-Step-4 dirty binary (`b23712bc`, sha `e69fb2ed…`) could not be rebuilt, so it
  was copied into `step4g` as `agent-sessions.bin.pre-step4-original` (verified with `cmp`).
  Then `step4`–`step4f` and the superseded `/tmp/step4-build` binaries were deleted. Free disk
  went from 24G to 50G.
- **Enrichment metadata budget is full; the 591 `quota` quarantines are a symptom (found
  2026-10-28).** `metadata_budgets.enrichment` was at 134,216,914 of 134,217,728 bytes (814 bytes
  left). By the trigger expressions, `enrichment_results` uses 127.2 MB (1,516 rows,
  about 84 KB each), `enrichment_work` 4.9 MB and `optional_repairs` 2.2 MB. Every result is
  live: each is referenced by its session's latest job evidence, and `checkEvidenceAssets`
  reloads it to recheck asset policy at delivery. So deleting results would turn delivery into
  `ErrIntegrity`. Deleting quarantine rows would free under 1 MB and fix nothing, because every
  new enrichment would hit the same quota. Cause: almost all of the 127 MB is the inline
  `Assets` list, and there are only **8 distinct asset lists** (834 distinct assets, about 340 KB)
  repeated across 1,516 results in 378 sessions. Also 3,358 jobs carry
  `enrichment_gap='quota'`. Transcript capture and delivery are unaffected; they are visible
  gaps, as the contract requires. Fix (W30): store asset lists once, content-addressed, and
  compact existing rows in place without changing result ids. Then replay the quota
  quarantines.

### Next action

Step 4 is installed and delivering, with the backlog drained (`463ddf3`). Next:
1. The 24–48h autonomous run on the Mac (AC power, lid open or `caffeinate`). Monitor
   read-only with small indexed queries: pause, `step_errors`, active quarantines,
   retry classes, discovery `completed_at`, and stderr growth.
2. Operator decisions, recorded rather than taken: prune install backups `step4`–`step4d`
   once `step4g` is confirmed as the rollback point; review the 591 enrichment `quota`
   quarantines.
3. Then BC-01 acceptance, R2 acceptance, and the Step 5 cutover per contract §6 (this
   removes the legacy `syncer` package that is within 7s of its 600s test timeout).
