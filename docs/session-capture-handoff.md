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
| Autonomous reliability | **Fix merged `1706c00` (W1); end-to-end confirmation pending.** Original cause: `runtimeWorker` (`internal/sessioncapture/runtime.go`) treats any error returned from a work step as Run-fatal; ~10 workers share one SQLite connection (`SetMaxOpenConns(1)`), so a >60s wait for it returns `context deadline exceeded` and kills Run. Reproduced in a throwaway copy (0.49s). Matches the recorded Mac `exit 1 context deadline exceeded`. |
| Open test failures (historical, private-tree) | journey09, V25 attachment/timeline ×3, Qrace180. Must be re-run on a repository commit; may be private-tree artifacts. |
| Shared spool `usage()` scan | Accepted limitation: O(payload files) stat per snapshot (≈185 files observed); fail-closed on integrity error. Not redesigned. |
| Gates | `make check` ≈620s, dominated by legacy `internal/syncer`; shrinks at legacy removal. |
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
| 1 | Runtime reliability (merged `1706c00`; holder measurement deferred to Step 4 via `step_errors`): operation timeouts/transient claim errors retry per item, only durable local-store failures stop Run; claims released on restart; timeouts visible in status; don't hold the lone connection across slow work; regression tests. | Pending |
| 2 | Simplify (2A `c92bc61` done; 2B reduced scope `77276c4` done): analytics refresh through one reader path; diagnostic subcommands to test helpers/hidden; optional-lane fold; enrichment trim; break legacy import edges (`launch`, `snapshot`, `hook`). | Pending |
| 3 | Green baseline on one commit: rerun journey09/V25 on the checkout, `make check`, `make test`, `make fuzz`, analytics tests. | Pending |
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

### Next action

Parent: Step 4. Prepare installed Mac acceptance with a binary built from `main`. Follow the
documented controlled-acceptance install procedure; do not improvise host changes. Explain the
installed pre-fix service's ≈300% CPU with the fixed binary's `status`/`step_errors`.
