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
| Autonomous reliability | **Root cause identified, fix pending.** `runtimeWorker` (`internal/sessioncapture/runtime.go`) treats any error returned from a work step as Run-fatal; ~10 workers share one SQLite connection (`SetMaxOpenConns(1)`), so a >60s wait for it returns `context deadline exceeded` and kills Run. Reproduced in a throwaway copy (0.49s). Matches the recorded Mac `exit 1 context deadline exceeded`. |
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
| 0 | Preserve dirty state; archive ledger; compact brief; commit reduced baseline. | In progress |
| 1 | Runtime reliability: operation timeouts/transient claim errors retry per item, only durable local-store failures stop Run; claims released on restart; timeouts visible in status; don't hold the lone connection across slow work; regression tests. | Pending |
| 2 | Simplify: analytics refresh through one reader path; diagnostic subcommands to test helpers/hidden; optional-lane fold; enrichment trim; break legacy import edges (`launch`, `snapshot`, `hook`). | Pending |
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
- Workers: Sonnet subagents in separate git worktrees with disjoint owned paths; listed
  here when dispatched with ID, scope and status.
- Capability: background start, status/wait, steering and resume are available.

### Next action

Parent: commit the Step 0 baseline, then dispatch Step 1 (runtime reliability) and the
independent Step 2 analytics/CLI simplification in separate worktrees.
