# Session capture: current delivery handoff

This is the sole current deployment/qualification/next-action brief. Requirements live
in the [capture contract](session-capture-contract.md); the accepted cleanup direction
and evidence live in the [holistic review](holistic-review-20261029.md).
The previous checkpoint and detailed October 28 ledger are preserved verbatim in the
[October 29 archive](session-capture-handoff-archive-20261029.md). Earlier histories:
[October 27 archive](session-capture-handoff-archive-20261027.md) and
[progress history](session-capture-progress-history.md). Historical instructions are not
current rollout authority.

## Current session checkpoint

**2026-10-29: six-phase implementation in progress.** The operator approved completing
all six phases, with parent-owned quality, independent review and final verification.
The replacement is **not release-accepted, production-ready or cut over**. Defaults
remain legacy. No host change, remote deletion or original-source deletion has occurred
in this delivery session. Existing containment remains until its controlled re-enable
boundary is resolved; offline green alone does not lift it.

### Checkout and preservation

- Integration checkout `/Users/mm/dev/memory`, branch
  `delivery/queue-and-resource-simplification`; local `gh stack`: `main` →
  `delivery/holistic-baseline` → `delivery/correctness` → current branch. Local `main`
  already contains 88 commits beyond `origin/main`; this session has not pushed them.
- Starting implementation `4c9aca5c5cb178316e9eed32626dbf56f7dbaac9`; adopted the review
  report and handoff edits as `06c6da4`. No preexisting implementation changes in this checkout.
- Preserve frozen untracked `docs/research/README.md`, `agent-navigation-context-graphs/`
  and `agent-navigation-handover.md`; `CLAUDE.md` stays absent. Preserve stashes and
  unrelated `asset-checks`, `log-failed-sync`, `pruned-pieces` worktrees.
- External repair `/Users/mm/dev/memory-wt/capture-source-absence-20261028` has released
  source ownership per the machines handover. Its tracked diff and untracked
  `source_absence_test.go` were copied into a new task worktree. The original remains
  unchanged, uncommitted and not release-qualified.
- Prior dirty candidate/research evidence is preserved on
  `preserve/pre-simplification-20261027` and in the private historical trees identified
  by the archived handoff. Do not resume the retired recovery patch stack.

### Installed state and authority

| Area | Evidence / current limit |
| --- | --- |
| Mac capture | Fresh `launchctl print-disabled` on October 29: disabled. Last recorded installed binary `efbb99e`, sha `d5592fca…`; binary/process not freshly inspected here. |
| Mac consumers | Fresh launchd query: `com.agent-sessions.publication-refresh` and `publication-dashboard` also disabled. Do not repeat the older claim that refresh is running. |
| BC-01 | Parent resolved the apparent access blocker: SSH alias is lowercase `bc-01`; uppercase bypassed its Host stanza. Fresh read-only query reports capture `UnitFileState=disabled`, `ActiveState=inactive`, `SubState=dead`, `ExecMainStatus=0`. Last installed source record remains dirty `b23712bc`, not freshly qualified. |
| Re-enable | A controlled release action after combined gates and explicit containment reconciliation. No worker has host authority. |
| Cutover | Inventory, operating acceptance, final switch and reviewed retired-key deletion remain incomplete. No legacy code removal before accepted replacement. |

The machines handover's newest top section withdraws the custom `bc-heavy` tool; it was
removed deliberately. Do not reinstall it or follow historical admission instructions.
Parent serializes expensive gates directly in pinned Nix, on AC with sleep prevented.

### Fresh-state rebuild boundary (material reconciliation)

The machines handover now records subsequent **operator-authorized generated-data
cleanup, October 28 21:37–21:45 UTC**. Mac/BC experimental spools (including unpublished
backlog), Mac analytics and capture backups were deleted. The former `step4g/step4h`
rollback points and quarantines are historical, NOT available continuation inputs.
Original agent sources, identities/privacy configuration, credentials and installed
software remain; no cloud archive objects were deleted. Older legacy archives/state
outside the experimental root were explicitly not deleted. Native hooks may recreate
small intake stubs; that is not an intact queue or bulk capture.

Therefore phase 5 requires intentional fresh-state inventory/rebuild, not a blind
binary swap/replay. Read-only planning confirmed an empty Queue starts at sequence 1
and cannot reconstruct prior source epochs or unpublished evidence; publishing against
existing R2 heads is not safe continuation. Production `capture-v1/archive.json` is a
global bucket binding: changing archive ID alone is not independent isolation. Existing
configuration supports a separate bucket. Parent recommended a fresh bucket/archive
and requested that release-boundary decision; no same-bucket namespace framework or
old-binding overwrite is authorized by this plan.
Do not recreate deleted generated data incidentally or treat local source availability
as proof every deleted accepted byte can be recovered. Preserve the containment plist
(failure-only KeepAlive, throttle 300s), build exact gated source, and back up any new
SQLite state via its backup API. Historical performance findings remain useful test
inputs, not current spool statistics. Parent informed the operator of this boundary.

## Scope and acceptance plan

| Phase | Scope | Exit condition | State |
| --- | --- | --- | --- |
| 1 | Reconcile baseline, ownership, criteria and tools | Each finding has owner/check; preservation and authority clear | Baseline reconciled; resource budgets still to qualify |
| 2 | Source absence, analytics scope/disappearance, current-state docs | Defects have before/after regressions; fail-closed boundaries retained | Implemented and independently reviewed; combined gates next |
| 3 | Typed queue state, inventory reuse, evidence correspondence, shared delivery outcomes, malformed work | Fewer interpretations/invalid states; invariant tests unchanged or stronger | Integrated with independent correction approval; combined gates next |
| 4 | Representative measurements; bounded report/import/scheduling improvements where justified | Stated resource/freshness/retention envelope demonstrated | Synthetic measurements justify three narrow changes; representative operating envelope remains open |
| 5 | Combined full gates, installed Mac/BC-01/native/R2 acceptance and autonomous observation | Actual installed end-to-end path, privacy negatives, restart/retry and sustained operation | Blocked on implementation/gates/containment reconciliation |
| 6 | Controlled cutover, reviewed remote retirement, legacy code removal | One supported architecture; preserved data/policy; post-cutover checks | Dependent on phase 5 |

Non-negotiable: preserve source epochs, durable pending ownership, privacy exclusions,
learned roots/identity, optional-artifact isolation and one authoritative reader.
No gate weakening, generic workflow framework, second reader, speculative GC/chunking,
upload-ACK bitmap or legacy migration prerequisite. A simplification must actually
remove duplicated interpretation/control flow, not merely add helper layers.

## Ownership and active workers

Parent owns this brief, integration checkout, commits/stack, expensive gates, all host
operations and requirements acceptance. Sol/medium workers use isolated worktrees;
background status, steering and resume are available. They may run scoped offline tests,
not concurrent full suites or host operations.

| Worker | ID | Worktree under `/Users/mm/dev/memory-wt/` | Owned domain |
| --- | --- | --- | --- |
| Source absence | `e3a0ec8e-06a8-41b` | `review-source-absence` | Copied repair + tests; sessioncapture source/fatal boundaries; capturedaemon service backoff |
| Analytics health | `db7d1d43-00b7-45c` | `review-analytics-health` | Refresh/dashboard health and coupled tests |
| Runtime inventory | `5b42a090-9295-42a` | `review-runtime-inventory` | CLI policy/source composition and coordinate config APIs; no sessioncapture/service edits |
| Status docs | `1f82dee1-a781-417` | `review-status-docs` | README and current reference headers; no handoff or historical evidence edits |

Additional owners: queue `9856b5b1-e75f-4ef` / `review-queue-simplification`; analytics
resources `a6a0477e-fccf-4e8` / `review-analytics-resources`. Source/docs/inventory owners
have released implementation ownership. Independent source review `3d4e62d0-1ff2-413`,
queue review `b6dfa436-69d2-414`, and analytics recheck `f5330d9e-b67f-468` are read-only.
No automatic acceptance from completion/commit; workers use their recorded integration
bases rather than assuming one shared baseline.

### Material corrections

- C1 analytics worker briefly edited the integration dashboard through a relative tool
  path. Parent detected it immediately; worker instructed to transfer only its own edit
  to the assigned worktree and reverse that accidental edit. Integration dashboard now
  clean; worker explicitly acknowledged correction and parent verified both worktree
  statuses. All workers reminded to use absolute tool paths and explicit bash cwd.
- C2 analytics upgrade: parent identified Python SQLite DDL transaction gap; worker
  instructed to wrap v3→v4 ALTERs and completion invalidation in an explicit transaction
  with interruption/rollback regression. Implemented in integrated analytics changes.
- C3 inventory: test-only wrappers removed from production in `c5d1319`; parent verified
  shared composition and test helpers. All initial implementation startups confirmed.
- C4 source review: actual CLI exclusion callback erased fatal store classification.
  Queue exported safe `ErrPolicyUnavailable` in `2134744` (integrated `e490b51`);
  production callback fix `a1c980a` → `c80a938` now covers the actual `Run` path and safe
  fatal diagnostic. Independent reviewer approved closure at `ee09321` (read-only recheck).
- C5/C6 analytics: `b2dad68` → `f57de35` binds upgraded expectations and forces new-reader
  qualification on repoint. Parent caught a further retry loophole: updated target binding
  could reuse old imported binding on the second failed attempt. `41d3baa` → `47ae92d`
  keys reuse on `imported_fingerprint`, with repeated-failure/restart regression.
  Independent reviewer `f5330d9e-b67f-468` approved all four analytics fixes at `47ae92d`;
  focused refresh26/dashboard13 passed in pinned Nix.
- C7/C8 queue review: late failures must not revive superseded/downgrade published jobs;
  quarantine only when guarded transition wins. Shared handling must preserve immediate
  transient retry behavior rather than applying autonomous MaxAttempts. Implemented below.
- C7–C9 correction `eaaf3ce` → `d74c6c1`: terminal guarded failure/quarantine, explicit
  transient semantics, ACK/supersede regressions, and private EpochID association.
  Historical unbound rows are readable after identity/commitment checks, not retroactively
  proven; new writes require epoch binding. Reviewer `271384d0-a2ef-429` approved scoped
  code/tests at `d74c6c1` without running gates.
- C10 batch review: Python reparsing JSON tool arguments changed their representation.
  `b9b07d1` → `08fb3af` stages JSON as VARCHAR then casts in SQL; duplicate keys/numeric
  lexemes retained, regression added. Reviewer `2af17bba-8f94-49a` approved scoped
  code/tests at `d74c6c1` without running gates.
- Tools aborted two workers mid-run; resumed same IDs/worktrees after status reconciliation,
  no duplicate implementation owners. Queue worker also resumed after tool abort.
- Review corrections already accepted: no unconditional poison-row isolation without
  trustworthy coordinates; verified attachment retry is not a new upload attempt;
  no blanket role immutability rule; no per-GET cross-database qualification or second
  fixture executable. Same-name endpoint rebinding needs more than active-name filtering.

## Verification and progress ledger

### Baseline evidence only

- Historical source `bf99752` (implementation identical to starting `4c9aca5`): unchanged
  `make check`, `make test`, `make fuzz` passed; exact dated evidence in archive.
- Holistic review on `4c9aca5`: pinned-Nix capture-acceptance 12+6+6 passed; analytics
  97 discovered/12 skipped (journeys separately run); ten candidate Go packages passed
  `-race -shuffle=on -count=1`. Two synthetic refresh-health defects reproduced.
  Commands/logs in review §7. These results do not qualify implementation changes.
- Fresh readiness checks: AC power, no existing Go gate processes at initial inspection;
  `gh stack` available. Open GitHub PRs 23/26/27/28 are unrelated and were not modified.

### Current implementation

- Source repair worker `3029551` → integration `1d7bad9`: parent reviewed narrow source
  marking/service guards. Worker full race/shuffle sessioncapture104.310s/capturedaemon1.529s,
  lint0; two mutations killed. Independent reviewer `3d4e62d0-1ff2-413` found C4, so final
  C4 now closed by real callback regression and independent recheck; combined gates pending.
- Docs worker `be84448` → `c892719`: current-status duplication removed from 11 references,
  net reduction; parent inspected contract/install/README; link paths and diff check pass.
  Parent archived exact old handoff and compacted current brief (`a1360f1`).
- Analytics `dac9642` → `3112872`: completed remote sweep missing-state and binding-aware
  summary/atomic upgrade; worker refresh24 (one journey skip), dashboard12 pass. Independent
  reviewer `da8bed5b-2d37-4e7` required C5/C6; corrections integrated as above and under recheck.
- Inventory `c5d1319` → `8682dc3`: immutable shared composition, fresh live checks;
  worker race/shuffle cli/capturedaemon/capturecoordinates and lint pass. Parent requested
  stronger immutable-snapshot test and C4 production path now integrated; focused four-package
  race/shuffle and lint passed. Independent review found fresh exclusions still enforced.
- Queue worker `9856b5b1-e75f-4ef`, `review-queue-simplification`: typed singleton state,
  evidence correspondence, delivery-result convergence, malformed work; now also owns
  measured sparse scheduler fix. Parent actual-method overlay on `e490b51` with20k inactive
  rows+1due: source/session each need313 claim calls (36.8ms/266.6ms SQL), with up to100ms
  worker wait per empty turn. This justifies due selection before page truncation.
  `10f6463/9fb6885` → `a827c14/32de1e9` select due work before page truncation;
  one-call regression failed against old query. The same20k probe now needs one call
  (~0.31ms source/~0.86ms session); focused head-blocking/fairness tests pass.
  Log `/tmp/memory-sparse-scheduler-before.log`; no live performance claim.
- Resource worker `a6a0477e-fccf-4e8`, `review-analytics-resources` base3112872: bounded
  dashboard `965cf69` → `1db3475`; exact SQL totals/50 details, exhaustive offline report
  retained. Synthetic 20,001 sessions/5,000 failures: 22.59MB/0.412s/188.8MiB RSS →
  38.7KB/0.065s/59.8MiB. Row cap is not a universal byte cap.
  Batch `32851c3` → `bda8512`: 1,000 rows/1MiB staged event/issue batches in existing
  transaction (one giant record remains line-sized). Synthetic identical event/issue
  digests on final JSON-preserving version: 1k2.417s→0.054s, 10k18.727s→0.182s. Reproducer:
  `tools/analytics/bench_publication_import.py`. Focused publications25/dashboard13 and
  actual Go-writer journey passed; C10 correction integrated. Largest real record,
  fresh sweep I/O, discovery/freshness, offline spool and retention horizon unqualified.

No combined full/native/live or release acceptance claimed.

## Next action

1. Independent source, analytics, queue and resource reviews are closed narrowly;
   corrected invariants and failing-before sparse scheduling regressions are integrated.
2. Run unchanged full pinned-Nix gates centrally on the frozen combined source; diagnose
   failures rather than weakening gates. Record exact source, commands and logs here.
3. Resolve fresh bucket/archive choice and explicit containment boundary
   before installed rebuild; do not recreate deleted state as incidental testing.
4. Qualify representative budgets and actual installed Mac/BC-01/R2/native journeys,
   then sustained autonomous observation. Only then cut over and retire legacy/old keys.
5. Continue through available work; phases 5/6 cannot be marked complete from commits,
   offline tests or historical installs.
