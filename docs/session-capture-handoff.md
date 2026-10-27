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

### Next action

Parent: dispatch W6 to fix the Open-path coordinator exit (surface the error, set the
busy timeout before any locking statement, reproduce deterministically, then repeat
W4's loop); afterwards run the Step 3 full gates on a quiet machine.
