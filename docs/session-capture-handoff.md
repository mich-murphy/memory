# Session capture: current delivery handoff

This is the sole current deployment/qualification/next-action brief. Requirements live
in the [capture contract](session-capture-contract.md); the accepted cleanup direction
and rationale live in the [holistic review](holistic-review-20261029.md).
The previous handoff is preserved **verbatim** in the
[October 30 archive](session-capture-handoff-archive-20261030.md). Its pre-merge tasks,
worker assignments, installation tables and earlier “current” checkpoints are dated
history, not current execution instructions or renewed operating authority.

## Current session checkpoint

### October 30: source integration complete; live operation not accepted

**The PR/integration work is done. Memory has not been proved working live end to end
on the merged source.** This documentation reconciliation does not deploy or activate it.

| Area | Accepted state / remaining limit |
| --- | --- |
| Source integration | [PR30](https://github.com/mich-murphy/memory/pull/30) **merged** into `main` at `b0f949c6a8de76b4a8cd3f0bdf48ae29d44b1026`. Its tree equals qualified source `ef0f3d31aa76d9ba7c1370431006e1a3c2bb1f62`. All 139 integration commits / 458 changed paths landed, including the 88 previously local-main commits. |
| Local development qualification | Actual canonical `make check` aggregate **exit 0** on literal `ef0f3d3`, not only a staged prerequisite union. Coverage **87.5% (13,963/15,963)**; maximum CRAP **29.40**, unchanged limit 30. Full analytics **117 tests / no skips**. See [source-bound evidence](session-capture-offline-evidence.md). |
| Hosted source/platform qualification | All seven jobs passed for both [exact-head PR CI](https://github.com/mich-murphy/memory/actions/runs/41017288817) and [exact merged-main push CI](https://github.com/mich-murphy/memory/actions/runs/41018839999): Linux amd64/arm64, native macOS, static, actual consumer/full analytics, fuzz, workflow security. This is not installed-host or actual-R2 acceptance. |
| PR cleanup | #23 closed as contract-retired recovery work. #26/#27/#28 closed as superseded **after their exact useful maintenance updates landed through #30**. No open PRs remained at integration completion; none were found during this documentation reconciliation. Historical branches/worktrees remain preserved. |
| Release artifacts | Private `0.5.0-SNAPSHOT-74c00f4` was verified against **earlier `74c00f4`**, not the merged source. It is unpublished, not a current-source release artifact, and not installable through the normal immutable-release installer. No new tag or release was published. |
| Installed capture | Last recorded installed source on **both hosts: `b8cbed2`**. The October 29 containment review found **both collectors disabled/off**. Installed coordinates, supervisor state and consumer health were **not freshly inspected** during source integration or this documentation work. Do not describe that dated observation as a fresh health check. |
| Operating acceptance | **Incomplete and separately gated:** merged-source native hooks, automatic capture, actual R2 publication, reader/importer/index/report freshness, restart/outage/privacy behavior, two-host coverage and representative resource budgets. |

Final independently verified integration disposition:
https://github.com/mich-murphy/memory/pull/30#issuecomment-6516354560

### What landed and what did not

- Retained `881b04e` per-lane scheduling repair and `74c00f4` portable stat adapters,
  preserving privacy, ordering, exact fingerprint bytes and quota behavior.
- Integrated the missing CLI failed-sync logging/durable run-recording leaf as
  `7b55448`, with real failure/cancellation regressions; final fixture lint fix is
  `ef0f3d3`. Independent medium-effort source review found no blocking defect.
- Retained PR26→`063277a` (gopls 0.24 / x/mod 0.41 / x/text 0.42), PR27→`04aef71`
  (pinned zizmor-action 0.7.1), and PR28→`267bd3f` (nixpkgs `d3b13ad` / cacert 3.131.1).
  Qualification used the actual newly locked Nix closure/CA and separately scanned
  the real 24-module gopls tool graph; collector `make vuln` alone does not cover it.
- Committed the relevant previously dirty handoff and **62 Git-visible research files /
  8,162,474 bytes** unchanged as `1a317db`. Bounded publication review is not universal
  DLP certification. Ignored raw query data and source-bearing cache databases remain
  local/ignored; never force-add them. Preserve frozen evidence and recorded hashes.
- Retired concurrent/pruned cold recovery, nongate-passing preservation snapshots,
  archived cost probes and the old **legacy** Linux-native prototype remain preserved,
  not shipping prerequisites. That prototype does not prove the independent candidate's
  Linux native hook→queue→reader→analytics path; this remains a real operating gap.
- The seven-stage delivery history is integrated and preserved. No PR audit, source
  integration, CI babysitting or validator worker remains outstanding from that task.
  Do not restart those tasks from the archived ledger.

### Historical live evidence is real, but not current acceptance

The replacement was experimentally installed **before** the latest repairs. The
[archived installed-qualification record](session-capture-handoff-archive-20261030.md#existing-bucket-reset-and-installed-qualification--october-29)
records the controlled existing-bucket binding switch, earlier actual R2 journeys,
selected installed Mac/BC publications through reader/importer/index/report, and native
Mac scratch gates. Do not rewrite that history as “never installed”, “no live tests ever
passed”, or “no binding change ever occurred”. Those selected older-source journeys did
**not** establish full inventory, sustained operation or acceptance of today's source.

The later [October 29 observation review](session-capture-handoff-archive-20261030.md#operating-measurements-reviewed--october-29)
recorded partial coverage, high Mac CPU after backlog drain, an unexplained Mac exit/
restart, and no completed BC discovery sweep before containment. Both collectors were
then confirmed off. No fixed 24-hour wait is required; elapsed time alone is not acceptance.
The subsequent scheduling repair has deterministic RED/GREEN evidence and a completed
bounded synthetic comparison, but **settled/background CPU and production operating
budgets remain unresolved**. Do not infer that the fairness fix solved them or start
more speculative performance runs from this brief.

The archive also records previously authorized generated-state deletion and the later
fresh binding. Some old rollback points no longer exist. Never treat old inventories,
backup paths or local source availability as proof that all prior accepted/unpublished
bytes remain recoverable. No bulk old-object retirement or final legacy cutover is
recorded. Both old `b8cbed2` and the merged candidate use `session-reader/v2`: no protocol
migration, archive reset or rebind is a prerequisite for coordinating their software.

## Completed source review — October 30

The earlier operator request authorized source architecture/API/data-structure review,
production-readiness analysis, test-oracle cleanup, and evidence-led offline prototypes
and improvements. It does **not** activate the separately gated operating sequence below.

- **Base:** `/Users/mm/dev/memory`, `main`, `b0f949c6a8de76b4a8cd3f0bdf48ae29d44b1026`.
  Pre-existing changes are README, documentation index, production-readiness, delivery,
  this handoff, offline evidence and phase3, plus the untracked October 30 handoff archive.
  Preserve that reconciliation and all frozen research; no production/test changes existed
  at review start. No fresh PR/CI or installed-state inspection is claimed.
- **Acceptance:** rank concrete findings against current source; verify hypotheses with
  bounded synthetic regressions/prototypes before adopting changes; replace weak oracles
  without deleting safety coverage; independently review changed invariants and run relevant
  unchanged gates. Distinguish maintainability suggestions, measured mechanisms and actual
  production budgets. No rewrite, speculative GC/chunking, legacy migration or live access.
- **Ownership/capability:** parent owns this brief, integration, admission and acceptance.
  Background workers support status, conversation inspection and steering; three may run
  concurrently. Requested workers use `gpt-6.1-sol`, medium thinking. Initial assignments
  were read-only: core architecture `664d320f-7afc-48d`; test oracles
  `b7368fb2-b679-400`; performance/scheduling `152dd951-7250-4e9`; consumers/operator
  APIs `df44d707-0f1f-424`. Tool results confirm the actual model is
  `openai/gpt-6.1-sol`; startup/activity verified for the first three reviewers.
  Validation owner `101a6d61-0e02-4e0` prepares private admitted runtime/evidence only.
- **Selected work/ownership:** initial core, test and performance audits are complete.
  Test worker's completed ownership covered only `internal/sessioncapture/queue_test.go` and
  `internal/capturedaemon/{config,lifecycle}_test.go`: independent postcommit/no-source
  recovery and exact supervisor command oracles, with targeted mutation proofs.
  Core worker's completed ownership covered only `internal/sessioncapture/runtime_{delivery,store}.go` and new
  `runtime_delivery_lane_test.go`: deterministic wrong-lane selection race proof/repair.
  Performance worker owned private synthetic overlays only (unchanged transforms and
  due-selector scaling), not production optimization. Consumer review is complete;
  `df44d707-0f1f-424` owned only `tools/analytics/{events,publications,test_events,test_publications}.py`
  for framed recursion isolation and trace-scoped Codex mirror pairing with regressions.
  Parent verified the lane windows and both consumer control flows against current code;
  the subsequent RED/GREEN results below established the defects and repairs. Reviewer withdrew an apparent
  `head_failures` issue because returning valid heads correctly clear discovery failures.
- **Evidence/status:** `test-oracles-v1` admitted proof completed: all three deliberately
  broken variants passed old focused tests (exit 0), failed stronger tests at the intended
  assertion (exit 1), and unchanged behavior passed the strengthened restart/supervisor
  targets (race/shuffle, count1, 600s/package). Source manifest
  `0e0b6ebab93a603de3d515ff12d0c0231cc7b6019deb2ee263e26d25982d9d83`;
  parent inspected exact argv/results in `jobs/test-oracles-v1/out/proof-results.json`.
  At that intermediate checkpoint full acceptance was pending; final results follow below.
  Medium worker reached its 4GiB cap
  (196 max events, no OOM, swap zero); do not claim zero resource pressure.
  Private evidence root: `~/.local/state/machines-validation-pilot/memory-review-20261030/`.
  Parent corrected the new test's mistaken `present` source-state expectation to the
  implementation's `active` via owner steering; correction observed and covered by proof.
  No general rewrite is justified. Retain integrity checks, no GC, and one reader.
  At that checkpoint the decoded/encoded 1GiB envelope remained unproven; the implementation
  continuation below records its subsequent proof and repair.
- **Further evidence:** both bounded performance prototypes passed their exact body/work/
  selection controls (see [review findings](architecture-review-20261030.md)); no performance
  production change adopted. `lane-combined-v1` proved both old-source wrong-lane windows RED,
  candidate focused tests GREEN at count3, and full sessioncapture/capturedaemon race/shuffle
  packages GREEN. Parent's paused-order correction is implemented/tested. Independent reviewer
  `2ef4e21d-f5ff-467` approved final lane source hashes and analytics/test-oracle invariants,
  contingent on execution. Parent documented mirror helper's one-shot fresh-scope precondition
  and removed redundant count-only assertions now subsumed by exact supervisor argv checks.
- **Correction verified:** `analytics-focused-v1` remains a real failed attempt: both
  recursion tests failed their fixture premise before the system under test, and GREEN
  had 2/40 failures. The bounded actual-byte probe established decoder overflow at
  depth60,000/120,031 bytes and normalization overflow at depth512 after successful decode.
  Corrected v2 proves each baseline defect with the intended failure and passes all40
  focused tests. No skipped assertion, stack-limit change or broad catch. Parent read the
  actual results and corrected fixtures. Primary CPython research confirms the C-stack/
  Python-recursion-counter distinction. Existing unchanged projections are not automatically
  re-normalized; no operating refresh/migration was introduced.
- **Final acceptance: source/offline scope complete.** All implementation owners released
  paths and independent review is clear. Parent removed only redundant old count assertions,
  reproved both supervisor mutations and compared 610 code/test/tool/build files against
  `final-v1` with zero drift. Final snapshot manifest:
  `ce9fe805086a5b8f6ec5b84c173491bb16beedcd831163f7805bacf07c6cfc49`.
  Literal canonical `make check` **exit 0**: coverage **87.5% (13,973/15,976)**, maximum
  CRAP **29.40**, all unchanged gates pass. Consumer journeys 12/12, R2 launcher safety
  6/6, delivery launcher 12 discovered/one explicit SDK-peer-only skip. Separate complete
  actual-binary analytics discovery **122 tests/no skips, exit 0**. Existing dashboard
  HTTPError cleanup warning was retained. Parent inspected exact logs/results and source
  identities; details/commands/limits live in the [review report](architecture-review-20261030.md).
  Final Large workers had no memory max/OOM events or swap. No gates were weakened.
  Parent independently reverified all **10,739 sealed evidence files**; seal digest
  `819d57cab989a10fd72866eaefbb068f3f785e1232806a266969be006d7abf7b`.
  Validation owner completed; no review-owned containers remain.
- **Delivery boundary/next:** changes remain uncommitted in this working tree; pre-existing
  documentation and all original/live state are preserved. No source worker or corrective
  item remains outstanding in accepted scope. No new CI, native Darwin, timed fuzz, installed/
  actual-R2/two-host acceptance, deployment or activation was performed. Readiness remains
  blocked on the separately authorized operating sequence below, representative budgets,
  and then-unproven encoded-envelope qualification—not another unmeasured rewrite. The
  continuation below addresses the envelope in source, not operating acceptance. Documentation-
  only evidence updates do not change the code identity qualified at this checkpoint.

## Implementation continuation — October 30

The operator now requests delegated implementation with parent-owned acceptance, quality
and verification. Continue source/offline work; this is not activation or live-data authority.

- **Reconciled base:** same `main`/`b0f949c6`, unstaged prior review plus pre-existing docs;
  no staged changes. All ten previously accepted code/test hashes match the sealed review.
  All six previous worker IDs freshly report completed/released; prior evidence remains
  frozen. Do not redo accepted repairs or overwrite their diffs. No new PR/CI inspection
  is claimed for this source-only continuation.
- **Capabilities:** background workers, status/transcript inspection and steering are
  available; at most three concurrent agents. New workers request `gpt-6.1-sol`, medium
  effort. Parent owns this brief and integration; one validation owner executes admitted
  checks. No missing coordination capability is currently identified.
- **Scope/acceptance:** (1) prove and resolve the decoded/encoded capture/transport envelope
  mismatch without silently weakening limits or losing accepted work; (2) compare and, if
  justified, implement a maintainable due-selector optimization with exact eligibility,
  ordering, lane and sparse-history safety. Prototype before selecting changes; retain
  previous success/privacy/quotas/durability. No speculative GC, stat-only transform cache,
  second queue/reader, protocol migration, live source inventory, release or cutover.
- **Ownership:** envelope `174889d8-7aa9-414`, initially private prototype/proposal, possible
  capture-admission/new boundary tests/shared size definitions; excludes runtime scheduling.
  Selector `a0a0fcfc-db42-486`, initially private query comparison, possible `runtime_store.go`
  selector/index and new focused tests; preserve accepted lane changes. Validator
  `be41504d-0c3b-4f2` owns new private runner/snapshots/logs only. All three startups/model
  identities were verified from actual activity/acknowledgements. Validator completed
  setup/selector/envelope proofs, final source-bound validation and evidence sealing.
  All four workers are completed/released. Selector owner made no production edits.
  Envelope owner released five production
  paths; parent and independent reviewer `3226dd9e-2f8b-4a5` verified their exact hashes.
  Reviewer startup/model and completion were verified; no blocking source finding.
- **Evidence:** new root `~/.local/state/machines-validation-pilot/memory-implementation-20261030/`;
  prior `memory-review-20261030/` stays read-only/sealed. Initial state: no new checks run,
  no new production change accepted. Required final gates retain pinned Nix, shared worker
  admission, source/dependencies read-only, native EXT4 temp, network-none, race/shuffle and
  default timeouts/thresholds. Previous checks remain applicable only to unchanged source.
- **First measured decision:** selector v1 passed exact row/order/head/claim controls across
  8,104 actual synthetic captures and 576 samples (six alternating-order rounds × 16 queries).
  Parent inspected logs/bindings/raw samples/summary: `jobs/selector-v1/`, source manifest
  `baf133b8b9574cbdf3bcbfeff8f160569facd1df410411b9dc31f4ebe6b6922a`.
  Session-driven lookup improved eight-head/100-revision SQL median 887.8→67.4µs but worsened
  inactive-prefix 42.5→745.4µs; rejected for adoption. Another due-session shape scanned
  retained history under the measured planner and regressed badly; also rejected. Worker
  was redirected to due-index aggregation/head equality and future-dominant controls.
  These are SQL mechanism measurements, not end-to-end performance acceptance.
- **Selector disposition:** parent accepts retaining baseline SQL, not an optimization.
  v2 passed 9,704 actual-capture fixtures/540 samples, manifest
  `dab99ef0040fbb698ddbd0fa3ac27a27e8c10cfb9f5e579b6a736e0592bc2af7`.
  Due aggregation improved eight-head/100-revision cursor-zero median 635.8→423.0µs,
  but worsened 256-head median 432.3→572.2µs and small/sparse cases by roughly 8–11µs.
  The additional grouping B-tree explains a structural tradeoff; no representative
  runtime workload/budget establishes that adopting it helps. Stop further variants.
  `jobs/selector-v2/` preserves the result; no production SQL, index or new queue state
  changed. Actual claimSession integration proof remains required if later adopted.
- **Envelope implemented and source/offline accepted:** shared encoded bound in
  `internal/publication/types.go`, unchanged R2 bound alias in `internal/publicationr2/backend.go`,
  `ErrEncodedSize` wrapping `ErrQuota` in `internal/sessioncapture/queue.go`, streaming guard
  in `internal/sessioncapture/quota.go`, and new `internal/sessioncapture/encoded_size_test.go`.
  Both 1GiB bounds remain; no reader change, public test-only option, migration or deletion.
  `envelope/production-release-v1.sha256` binds the five released files. Parent corrected
  the private unit's impossible aggregate usage seed before shipping: current units include
  the writer's existing bytes plus unrelated spool usage. Relocations into existing files
  and the specific error message are recorded separately from frozen private v1 inputs.
- **Actual envelope proof:** scaled baseline captured 65,536 decoded bytes as 65,550 encoded,
  then failed delivery; the same baseline failed the intended preacceptance assertion.
  Candidate passed preservation/reopen/exact-decoded controls, and unscaled cheap units
  passed. Full-size baseline then accepted **1,073,741,824 → 1,073,766,413 bytes** and failed
  transport; actual production source rejected before acceptance with prior source, job
  status and readable remote publication preserved. Both full-size runs published/read
  back the exact-1GiB compressible control (**114,691 encoded bytes**) through the actual
  SDK-backed synthetic disk peer and authoritative reader. No actual R2 service was used.
  Evidence: `jobs/envelope-{scaled-*,unscaled-unit,full-baseline,full-production}-v1/`.
  Full production proof manifest `f905be7303a9a81a45752ca412996f3a6d04a9ed363c62e7102047fc67c94ac4`
  adds only a private probe to the clean production source. No full-size repeat is needed.
- **Validation state:** setup/selector workers exited 0, source pre/post hashes verified,
  no max/OOM/swap events; selector peak 1,407,352,832 bytes. Nonzero I/O pressure is retained.
  Scratch inspection found 96,264,335,360 bytes free on native executable EXT4. Selector
  package timeout 600s, enclosing bound 1200s, actual v1 wall 50.978s. v2 wall 48.74s,
  peak 1,642,741,760 bytes, likewise no max/OOM/swap; I/O pressure retained. Envelope
  stage 1 authorizes four serial Medium race/shuffle jobs with 600s bounds: scaled
  baseline reproduction, intended-assertion RED, candidate GREEN, unscaled cheap units.
  These four jobs passed their expected outcomes (the intentional RED is exit 1, not a
  suite pass). Full baseline/production runs exited 0 with peak 3,639,156,736 / 2,531,053,568
  bytes, no max/OOM/swap, source pre/post checks passed; independent compile/runtime bounds
  remained 600s each (1250s enclosing worker). Full probes are non-race; cheap/scaled and
  ordinary source/package gates retain race/shuffle. Focused three-package race/shuffle
  checks and pinned formatting/scoped lint passed on clean manifest
  `d79c4ea8ad43055c76c8876425c570e4cdffc22b59d7645226fd54317ea5288d`, with no private probes.
  Parent independently compared 614 non-documentation files: zero drift. The stronger
  actual-Capture assertion for both error sentinels arrived after the full candidate had
  already completed; no full-size repeat occurred. A separate scaled final-source v2
  proof passed both assertions. Its first preparation attempt assumed a standalone constant
  instead of the released const-block member: no payload ran, and the failed setup remains
  preserved separately. Parent inspected corrected v2 source/binding/logs. Previous sealed
  evidence was independently reverified unchanged (10,739 files / 78,675,454 bytes).
- **Canonical gate passed:** literal `make check` exit 0 on clean production-v1; coverage
  **87.5% (13,979/15,980)**, maximum CRAP **29.40**, unchanged limit 30. Consumer suites
  12 + 6 + 12 discovered, with the existing explicit SDK-peer launcher skip retained.
  Parent inspected binding/log/result/resource records; actual wall 779.624s within 1800s,
  peak 5,265,522,688 bytes, no memory-max/OOM/swap events. Full analytics subsequently
  acquired the shared lease without bypass: freshly built actual-source binary, **122 tests,
  no skips, exit 0**, 28.369s test time; overall 280.512s includes admission waiting. Peak
  1,130,504,192 bytes, no memory-max/OOM/swap. The existing HTTPError403 cleanup
  ResourceWarning is retained, not a warning-free claim. Parent inspected final bindings,
  build argv, logs, exact results and resource counters; no check threshold was changed.
- **Parent acceptance:** continuation source/offline scope is complete: new-admission
  envelope defect repaired and validated; selector comparison concluded without an unsafe
  optimization. Independent review is clear; no source correction is outstanding. Existing
  accepted oversized objects are not repaired or deleted. Changes remain uncommitted;
  pre-existing edits, original/live data and prior sealed evidence are preserved. No new
  CI, native Darwin/other-architecture, timed fuzz, installed-host, actual-R2, deployment
  or two-host qualification is claimed. Operating acceptance and representative budgets
  remain separate requirements, not evidence supplied by these synthetic tests.
- **Evidence sealed and independently reverified:** new root
  `~/.local/state/machines-validation-pilot/memory-implementation-20261030/`,
  **16,615 files / 96,482,309 bytes**, zero hash mismatches. `evidence.sha256` digest:
  `32df65a2be32ff172e2d9f42389d2d103b6398075a76d3be0d23c48653fe81b4`.
  Parent acceptance is recorded in `parent-implementation-review.json`; exact execution
  results are in `FINAL-RESULTS.json` and `LEDGER.md`. Parent confirmed all 14 exact owned
  container IDs removed. Evidence is frozen; use a new root for later work. Final
  `git diff --check` and 60 relative documentation links/anchors pass.
- **Next:** no source/offline continuation item remains outstanding. The separately
  authorized operating sequence below remains the production-readiness boundary.

## PR publication and merge request — October 31

The operator now authorizes publishing this reviewed change bundle as a PR, delegated
babysitting with `gpt-6.1-sol` at medium effort, and merging when ready. This extends
source delivery authority only; all operating exclusions below remain in force.

- Parent reconciled `main` at `b0f949c6`, 20 modified and four untracked task files,
  no staged changes, and no open PRs. The prior implementation/review workers completed
  and released ownership. The related pre-existing documentation reconciliation and
  verbatim archive are included unchanged alongside the accepted review/implementation
  work; unrelated history, research, worktrees and sealed evidence must be preserved.
- Use one PR on a new single-layer `gh stack` branch rooted at current `main`, with
  logical commits for source fixes/tests and documentation. Preserve the old merged
  stack; do not force-push `main` or clean up historical worktrees/branches.
- Parent owns acceptance and this brief. Caretaker **`9781c418-91df-46f`**, confirmed
  running `openai/gpt-6.1-sol` at medium effort, owns branch/commit/push, review/CI
  supervision and eventual merge. Startup and code identity were independently checked.
  **PR: https://github.com/mich-murphy/memory/pull/31**
  Branch `review/capture-hardening-20261030` contains all 24 accepted paths in four
  logical source/documentation commits; this final brief update is documentation-only.
  The prior merged stack is preserved. Initial stack submission encountered a collaborator
  permission error; command-scoped use of the existing authorized owner account resolved
  it without changing global authentication or exposing credentials.
- This checkpoint records publication and the parent-approved final scope, **not a claim
  that CI or merge has completed**. The linked PR and its final disposition comment are
  authoritative for exact final-head checks, review resolution, merge SHA/tree comparison
  and merged-main CI. This avoids inventing a future merge result in its own pre-merge
  commit. Parent has released this brief update to the caretaker for the final commit;
  no additional code change is authorized without review and appropriate local checks.
- Acceptance: committed code must match qualified clean manifest `d79c4ea8…` unless a
  reviewed fix invalidates and reruns affected local gates. Inspect review feedback and
  all hosted jobs on the exact final head, not merely required-check labels. Use exactly
  one `gh run watch <run-id> --interval 60 --exit-status` at a time; no `gh pr checks` or
  polling loops. Respect rate-limit reset and all repository protection/approval rules.
  Merge only a ready, conflict-free, accepted head, then verify merged-tree identity and
  the merged-main CI run. Post full URLs immediately for any genuine human gate.
- New source-bound PR evidence belongs in a fresh private root, not either sealed review
  root. No new live/R2/native installed-host actions, refresh, activation, reset or release
  are authorized. Current source/offline gates already pass; do not rerun unchanged
  expensive validators merely to package commits. If a fix is needed, follow the pinned
  Nix/shared-worker policy and retain all timeouts, security assertions and quality gates.

## Production-readiness analysis — November 2

Operator request: Sonnet-worker exploratory analysis of goals/current state; offline
experiments across every historical problem class; verify structures, contracts and
schemas give correct, efficient session loading into R2; document gaps in enough detail
to resolve them; identify tautological tests and needed property/mutation testing.
This is source/offline authority only; it does not grant the operating sequence below.

- **Base:** `main` = `origin/main` = `2deac62` (PR31 merged), clean at task start.
- **Capabilities:** background workers with status/transcript/steering; at most three
  concurrent. Workers request `sonnet`, medium thinking. Parent owns this brief,
  synthesis, admission and acceptance. Tests/builds run only through the shared
  worker-side Docker admission in a new private root
  `~/.local/state/machines-validation-pilot/memory-analysis-20261102/`; prior roots stay sealed.
- **Phase 1 (read-only, reports only to the private root `reports/`):** history/failure
  catalog; architecture/contract/schema conformance; test-oracle/property/mutation audit.
- **Phase 2:** parent-selected bounded experiments as private snapshot overlays, never
  checkout edits; RED/GREEN premises checked before claims. Results feed one repository
  gap register. No production change is accepted without its own review and gates.
- **Exclusions:** no live R2/BC-01/original-source inventory, install, activation,
  refresh, release, reset/deletion, protocol migration or speculative rewrite/GC/chunking.
- **Workers (all completed/released, `claude-5.5-sonnet`, medium):** phase 1 history
  `bafa72af-7588-478`, conformance `66bdff21-eddb-45f`, oracles `eb5e4b1b-e68d-4e5`;
  phase 2 gaps `83f8b1e5-891d-4c7` (e-), oracle mutants `654804bb-3441-492` (t-),
  model/privacy `68de17a5-3fd2-469` (p-), crash/hints/discovery `b5f2b9de-3518-4fa` (c-),
  analytics `10ae7096-bf2d-4d6` (a-), fuzz/mutation `47f9fcc3-73b8-431` (f-).
  Corrections: p- held all three container slots → steered to one at a time
  (acknowledged; later jobs complied). f- reported 60s fuzz campaigns that stalled
  after about 6–12s, and an invalid mutation run (40/41 timed out) → steered to report
  effective time and use a valid coefficient (uptake verified in `f-mut-quota-v2` and its report).
- **Parent verification:** every promoted claim was rechecked against the cited code
  and against each job's argv, overlay binding (added versus declared-mutant paths),
  exit status, decisive log lines and source post-verify. Analytics jobs exit 0 through a
  `tail` pipe, so their RED results were read from the unittest lines.
- **Result:** [production gap register](production-gap-register-20261102.md):
  11 gaps (GAP-01 to GAP-07 RED on baseline), verified-sound areas, proven weak oracles
  (W1/W4/W5/W7 confirmed, W2/W10 refuted), ready-to-adopt test overlays and a resolution
  order. **No production or test code in this repository changed;** the only repository
  changes are this brief, the register and the documentation index link. Not run: `make check`,
  full suites, native Darwin, CI, live R2/installed/two-host checks.
- **Next:** with source-change authority, resolve GAP-01/GAP-02 first, adopting the
  E1/E6 acceptance tests from the private overlays, then the cap policies
  (GAP-04/05/07), then measure captures-per-session before GAP-03. Evidence is sealed
  (`evidence.sha256` in the private root); use a new root for later work.

## Gap resolution delivery — November 2

Operator request: drive Sonnet workers to resolve every register gap with tests;
strengthen property/mutation tests so they represent realistic session workloads; derive
settings from experiments on actual local and BC-01 session history. Parent owns this
brief, verification against code standards and original intent, integration and acceptance.

- **Authority:** source changes on new `gap/*` branches (worktrees under
  `/Users/mm/dev/memory-wt/gap-*`, base `2deac62`), published later as one `gh stack`.
  The operator's instruction authorizes **read-only, aggregate-only** reads of local
  (`~/.claude/projects`, `~/.codex/{sessions,archived_sessions}`, `~/.pi/agent/sessions`)
  and BC-01 session history plus read-only aggregate queries of installed queue
  databases. No transcript content, paths, project names or session IDs leave the reading
  process. Still excluded: live R2, installs, hooks/services, activation, refresh, release,
  reset/deletion, writes on BC-01.
- **Capabilities:** background workers with status/transcript/steering, at most three
  concurrent, `sonnet` at medium thinking. All builds/tests run through private root
  `~/.local/state/machines-validation-pilot/memory-delivery-20261102/` (snapshots taken
  from a worktree; local real-data jobs mount the session roots read-only with the network
  off, and their scratch volumes are purged afterwards). Smoke `setup-smoke-v1` passed.
- **Plan:** wave 1: data/workload measurement (D), liveness GAP-01/02/05 (L1),
  consumer GAP-07/08 (C). Wave 2: GAP-04/06/09/10/11 (L2) and test infrastructure
  (model/property/crash/fuzz/mutation, workload-shaped from D). Wave 3: GAP-03
  coalescing/cadence chosen from D's replay experiments. The parent runs canonical
  `make check` on the integrated stack.
- **Workers / corrections / acceptance:** recorded below as they occur.
- **D (data, `345c244b-a8cd-4c4`, completed):** private `reports/d-workload.md`,
  `data/d-summary.json` (aggregate-only; parent scanned for paths/IDs). Parent-verified
  against the installed Mac queue: jobs 20,308 published / 5 superseded, 19,413 runtime
  sources, `runtime_quarantine` 0 rows. **Correction:** D's "installed discovery never
  completed" holds only for the first two operating snapshots; Mac post-repair shows
  `CompletedSweep=8`, `DiscoveryComplete=false`, no error; BC-01 pre-final-repair shows 0
  sweeps, no error. Key inputs: amplification under today's full-object model is
  4.5–10× (hook per turn) up to 228–1294× (every append); max sources/session 379;
  max cumulative decoded 615 MB (one Codex artifact); BC-01 inventory 29,082 (89% of
  installed 32,768; default 4,096 unusable); no upload throughput was ever measured.
  Relayed to L1 (GAP-01/02/05) and C (GAP-07/08).
- **L1 (`975c9d90-0f8a-496`, `gap/liveness`):** RED proven (`l1-red-job` exit 1: E1, E5,
  E6 fail at intended assertions; controls pass). Acknowledged D's data: GAP-02 default
  body budget 30 s + size/1 MiB/s (floor 60 s, cap ≈15 min, idle timeout), marked
  conservative/unmeasured; transient/timeout get no attempt limit; 1024-source cap stays
  as a typed, visible, non-transient limit.
- **C (`97e003af-250e-49d`, `gap/consumer`):** RED proven (`c-red1`: A7 strict + three A8
  probes fail; A1–A6 pass). **Parent decision:** keep a cumulative total cap (default
  raised to 1 GiB, which clears the measured 615 MB maximum) with typed
  `export_too_large`, instead of strict A7. Conditions: named coverage gap in status and
  report, sibling isolation, import once the cap allows, boundary at cap and cap−1,
  honest test name, documented cumulative semantics, no silent reinterpretation of the
  installed `805306368` flag; partial latest-epoch import deferred until a real session
  approaches 1 GiB. Acknowledged; uptake verified in `dc2c68d`/`1638b31`.
  **Parent review correction:** legacy local-hash projections would have hit a false
  `publication_conflict`, and a pre-field reader would have failed as generic
  `invalid_metadata`. Fixed in `1638b31` (legacy-hash re-import adopts the remote digest;
  `reader_digest_missing`; coordinated-deployment docs). **Worker completed.** Evidence:
  `c-check3` (snapshot `c-11` = `1638b31`, no drift) fmt/lint/vet/race for publication, cli,
  cmd exit 0; `c-green5` full analytics 151 tests OK; Python hand-mutation 17/17 killed
  (`c-mut2`, before `1638b31`); scoped gremlins on `internal/publication` 88%, survivors in
  untouched `export.go:129-136`. **Parent: accepted for integration**, pending the
  integrated `make check` and the rebase onto the liveness/test-infra stack.
- **T (`4cc57302-d1f6-4b1`, `gap/test-infra`):** GAP-09 seam, GAP-10 open-time sweep,
  property/crash/privacy/fuzz/mutation tests. First check `t-gap9-10-a` failed lint only
  (`Open` gocognit 24 > 20); tests passed. Fixed (`t-gap9-10-b` exit 0); commits `8d924b1`
  (GAP-09/10), `81020ec` (crash matrix), `f612a58` (model history + privacy canary,
  `t-model-a` 40 histories 36 s race, 0 violations). Finding: a literal repeated CAS is
  impossible by design (lost CAS ACK is reconciled by read-back), so the floor asserts the
  already-applied path instead.
- **Shared files:** L1 and T both edit `quota.go`/`queue.go` (L1: `ErrSpoolFull` returns +
  `Status.CapturedAtNS`; T: free-space seam + Open sweep). Parent 3-way merge is clean.
  Integration order: `gap/liveness` → `gap/test-infra` (rebased) → `gap/consumer`. Both
  notified; T keeps `errors.Is(ErrQuota)` asserts until the rebase.
- **L1 correction:** `FailureQuota` conflated permanent limits with spool-full
  backpressure; contract §3 forbids quarantining on spool full. Steered to split them
  (`ErrSpoolFull` wraps `ErrQuota`, no budget), keep lost-ACK conflict reconciliation, and
  measure status-query cost at ~10k pending. Acknowledged; uptake verified in `cc96583`
  (GAP-01 commit; `l1-g1e-job` fmt/lint/vet/race exit 0; status over 10k pending 21.5 ms).
  **Second L1 correction:** enrichment (`finishEnrichment`) and optional repair
  (`saveOptionalRepairFailure`) still quarantined every class after `MaxAttempts`, which
  is the same outage defect. Steered to apply the deterministic-only rule there, with tests.
  Acknowledged; verified in `e4949da` (GAP-05 typed `source-limit` + GAP-01 optional-work
  follow-up; `l1-g5d-job` fmt/lint/vet/race exit 0).
  **Third L1 correction (parent EXPLAIN QUERY PLAN, 30k rows, 99.9% captured):** GAP-05
  added `source-limit` to the claimSource exclusion list but not to the partial index
  `runtime_runnable_sources`. The claim query then falls back from the index to a rowid
  range scan over settled rows, and `CREATE INDEX IF NOT EXISTS` would keep the stale index
  on installed queues. Steered to drop it and create a renamed v2 index with an identical
  predicate, plus a query-plan test and an old-DB reopen test. Verified in `c5e1108`:
  `DROP INDEX IF EXISTS runtime_runnable_sources` plus `runtime_runnable_sources_v2`, built
  from one shared `runnableSourcePredicate` const that `claimSourceSQL` also uses, with a
  query-plan test (`l1-g5j-job` exit 0). L2 adds partial indexes to the same InitRuntime
  list, so the parent resolves that at rebase.
- **L2 GAP-04 review:** live-only admission accepted. Steered away from evicting
  settled rows by id at the 4096 default: on the measured 19k/29k inventories that
  evicts and re-captures thousands of present sources each sweep. Required: a default
  settled retention above the measured inventories (justified by bytes per row),
  absent-first then least-recently-observed eviction, a visible eviction counter,
  exact per-sweep re-hint counts, and no eviction of claimed or quarantine-referenced rows.
  Uptake reported (`l2-green-g4e` exit 0). Settled retention defaults to 65,536, based on
  the installed Mac queue's ~3.8 KB per row including indexes (worst case ~250 MB). Eviction
  removes absent rows first, then the least recently observed, and runs once per discovery
  batch (48.6 ms to evict 1 of 65,537 rows; no-op check 3.75 ms). It skips claimed and
  quarantine-referenced rows, and `InventoryEvictions` appears in status. Not yet committed;
  the parent reviews the commit.
- **L1 GAP-02 review (in-tree diff):** accepted the size-aware `BodyBudget` (30 s +
  size/1 MiB/s, 60 s–15 min, 30 s idle; throughput unmeasured, configurable via
  `min_throughput_kib`), removal of `http.Client.Timeout` with the transport bounds kept,
  and the flat metadata/CAS budget. **Fourth L1 correction:** the delivery step budget was
  a flat ~16 min, but one step uploads every untrusted object in the manifest.
  Multi-large-object manifests would be cut off on every attempt, and on each retry the
  412 read-back costs a full download, so the job never publishes. Steered to make the
  step budget OperationTimeout + Σ Total(size) and to add a RED/GREEN 3-object test.
  Acknowledged by L1; uptake pending.
- **L2 GAP-04 committed `c7bcf85`** (`l2-check-g4f` fmt/lint/vet/race exit 0). The parent
  accepted the code. Follow-up requested: docs and comments said settled retention uses
  the configured cap, but the code uses the fixed 65,536 ceiling. Add `SettledCap` to
  status. Mutation run pending.
- **L2 discovery-timeout lead (unmeasured):** the installed policy callback rebuilds
  exclusion/policy state (`exclusion.Load`, `capturepolicy.NewSet`, `Resolve`) on every
  `allowed()` call, at least twice per entry. D's 1.4–2.4 s sweep used a no-op policy, so it
  did not exercise this. L2 is measuring it with the real CLI policy; a cached per-sweep
  snapshot must keep mid-sweep exclusions safe.
- **L2 discovery measurements:** `l2-m2` with the real CLI policy and an empty exclusion
  registry ran 0.69 s per 1365-entry call (~0.5 ms/entry), so the policy cost alone did not
  reproduce the timeout. Revised hypothesis: per-row autocommit admission under
  `synchronous=FULL` (tmpfs probes hide the fsync cost). The fix in progress is chunked
  `HintBatch` transactions. Parent required: on-disk timing, a check of whether the installed
  timeout snapshots predate fingerprint skip `4a1cfec`, a cause for `DiscoveryComplete=false`
  with 8 completed sweeps (suspected EntriesPerRoot truncation), and batch tests for
  per-request isolation, ownership in the same transaction, exact cap admission, and crash
  loss limited to uncommitted chunks.
- **L1 GAP-02 uptake:** `TransferTimeout` removed; a `publication.TransferBudgeter` optional
  interface; the per-attempt context is OperationTimeout + Σ budgets over selected objects
  (`TestManifestOfSlowObjectsPublishesInOneStep`). `l1-g2e-job` is running. The docs formula
  and RED proof are still pending.
- **L1 turn budget exhausted (first run):** committed `cc96583`, `e4949da` and `c5e1108`. GAP-02 is
  in the worktree but uncommitted. `l1-g2e-job` exit 1 in sessioncapture:
  `TestManifestOfSlowObjectsPublishesInOneStep` (suspected test setup: the head is not
  a 3-object manifest) and `TestShutdownDuringDependencyDecodeRestoresAttempts` (0 reads;
  possibly the per-attempt context wrapper). Resumed with orders to diagnose without
  weakening the shutdown test, then RED on the flat allowance, the docs formula
  `OperationTimeout + Σ 2·Total(size)`, the full check, the commit and scoped mutation.
- **L1 resume diagnosis (parent reviewed):** `TestShutdownDuringDependencyDecodeRestoresAttempts`
  observes cancellation through `tripCtx.Err()`. A `context.WithTimeout` child of a custom
  context only follows the parent's `Done`, not its `Err()`, so the trip never fired. L1
  wraps the attempt only when the backend reports a transfer budget and left the test
  unchanged. Accepted, because production cancellation still propagates through `Done`.
  Residual: under a budgeted (R2) backend the delivery step context has no timeout
  outside `attempt`; those DB reads are bounded by `busy_timeout(5000)`. `l1-g2f-job` is running.
- **L2 discovery root cause (measured, `l2-fsync2` exit 0):** the dominant cost is the
  per-new-row live-cap `COUNT(*)` in `Hint`, which is quadratic over a cold sweep:
  702 µs/row at 10k and 1685 µs/row at 30k (50.5 s for 30k, close to the 60 s budget).
  Counting slots once per 256-row chunk gives 77 and 69 µs/row (2.1 s at 30k). Batching
  fsyncs alone gave about 1.3×. These are Linux-VM figures; APFS is an estimate. L2 also
  corrected the parent's steering: ownership was never recorded in the upsert's
  transaction (it uses a separate transaction after admission), and `HintBatch` keeps
  that order. Accepted. `DiscoveryComplete=false` with 8 completed sweeps is, by code
  analysis, a snapshot taken during the next sweep (`prepareDiscoverySweep` resets
  `complete`); this is not measured. Pending: the batch semantics tests, `SettledCap`, and
  whether a root larger than `EntriesPerRoot` completes through cursor resumption.
- **L2 committed `cc54259`:** `HintBatch`/`admitChunk` (live count once per 256-row chunk;
  isolation, exact cap admission, crash at `runtime-hint-commit`, ownership after admission,
  write-free settled sweep, each tested) plus `SettledCap` and the docs fix
  (`l2-check-disc2` fmt/lint/vet/race exit 0). Remaining: GAP-11, GAP-06, the key-length
  check, scoped mutation, and the EntriesPerRoot cursor-completion test.
- **L1 GAP-02 committed `3ba5fc3`** (`l1-g2g-job` exit 0 across publication, publicationr2,
  capturebackend, cli, sessioncapture and cmd/agent-sessions). The parent reviewed and accepted
  it: the `publication.TransferBudgeter` optional interface, `2·Total(size)` per object (upload
  plus read-back), the lazily bound CLI destination forwarding `Spec.TransferBudget`, local
  backends adding nothing, and the docs formula. Minor drift risk: `Spec.TransferBudget` and
  `Backend.TransferBudget` each encode the factor of 2. Still pending: RED on the flat
  allowance and scoped mutation.
- **L2 GAP-11 committed `b821e31`** (`l2-check-g11b` exit 0). Accepted in direction. A
  follow-up was requested for three things: owned external SQLite artifacts
  (`ownership.go`) still fingerprint only when untransformed; a golden-digest guard that
  forces a `transformPolicyVersion` bump when scrub rules change; and a queue-level test
  with zero re-hints when unchanged and exactly one after a `-wal` change.
- **L2 GAP-11 follow-up `d499066`** (`l2-check-g11c` exit 0 for capturesource, scrub and
  sessioncapture): the owned-external fingerprint, `scrub.RulesDigest()` with a golden guard,
  and the queue-level settled/WAL re-hint test are accepted. Parent note: the claim that a new
  `rulesFor` case is caught is not true. The test checks only the listed names and a count,
  so a single-table fix or a reworded comment was requested. Scrub logic outside the rule
  tables still needs a manual version bump. L2 is now on GAP-06.
- **L1 GAP-02 RED:** `l1-red2-job` exit 0 exposed a test defect: the test bypassed the step
  context. It was fixed in `ff9ce7b` and now runs through `stepContext`. `l1-red3-job` with
  the flat-allowance shim exited 1 for the expected reason (`state=retry attempts=1
  last=timeout uploads=2`). The GREEN rerun (`l1-g2h-job`) and publicationr2 mutation
  (`l1-mut-r2-job`) are running.
- **T scoped mutation:** `t-mut-dry` exit 0 found 51 runnable quota.go mutants. A per-mutant
  driver (`t-mut-drv`, focused regex `Quota|Reserve|Sweep|Temporar|Spool`, unbuffered log) is
  running. It is reported as scoped mutation, not full-package gremlins.
- **L1 publicationr2 mutation** (`l1-mut-r2-job` exit 0, whole package): 137 killed, 22
  lived, 1 not covered, efficacy 86.16%. In `budget.go`, `150:9` lived (EOF disarm negation)
  and `54:71` (`TransferBudget` factor 2) is not covered in-package. Triage of the remaining
  lived mutants and the sessioncapture allowance mutation is still pending. The GREEN rerun
  `l1-g2h-job` exited 75 (slot refusal, no payload ran) and must be rerun.
- **L1 second run completed:** commits on `gap/liveness` are `cc96583`, `e4949da`, `c5e1108`,
  `3ba5fc3` and `ff9ce7b`. RED is valid (`l1-red3-job` exit 1, timeout reason). Still open:
  `ff9ce7b` (test-only) has not had a full check; the factor 2 in `TransferBudget` isn't
  tested directly and is duplicated in `capturebackend.Spec`; `budget.go:150:9` lived; and
  the sessioncapture mutation pass is outstanding. Resumed with orders to add the direct and
  cross-package budget tests, kill or justify 150:9, run one full check on HEAD, and run
  scoped per-mutant sessioncapture mutation of the GAP-01/02/05 functions. The 20
  pre-existing publicationr2 survivors are only to be listed, not triaged. GAP-02 defaults
  (1 MiB/s, 30 s base, 60 s–15 min, 30 s idle) remain unmeasured.
- **L2 `c7d79bd` (GAP-11 fixup), accepted:** `scrub.ruleTable` is the single table that both
  `rulesFor` and `RulesDigest` read, iterated in sorted order. The golden was regenerated
  because the digest algorithm changed; the rules did not. The comment states that
  `scrubDoc`/`IsSecretName` changes still need a manual bump of `transformPolicyVersion`.
- **L2 GAP-06 committed `26ba65c`** (`l2-check-g6c` exit 0 for sessioncapture, scrub and
  capturesource). Accepted by the parent. Remote-only sources pass through `recheckScopes`
  (claimed-session and publication-path exclusions) before any upload or CAS and fail closed
  with `ErrPolicy`+`ErrSessionExcluded`. There are 11 rebase tests. The steady state is one
  step per capture, measured at current=5 and opens=6/8/8 per delivery: every later delivery
  repeats Current+Read+merge, which is a bounded follow-up. Requested: phase3 docs, including
  which checks a remote-only source gets, and exact cost assertions. Scoped mutation
  `l2-mut-rebase` is running.
- **T final:** scoped quota.go mutation (`t-mut-drv`, focused regex, baseline 38.6 s, 193 s
  per-mutant timeout) hit its 3600 s deadline after 50 of 51 mutants. Of those 50, all were
  killed (21 by timeout) and 0 lived. The earlier survivors (`quota.go` MinFreeBytes
  arithmetic and `>`/`>=`) are now killed through the `freeBytes` seam. Mutant `26:28` never
  ran. `t-prop-count3` exit 0 (race, shuffle, -count=3, 204 s; TestModelHistories 34–36 s).
  Not done: the W2 barrier, the SIGQUIT dump, tier-1 mutation of publication/deliver, and the
  crash-matrix RED run on base.
- **Integration branch `gap/integration`** (worktree `memory-wt/gap-integration`), base
  `ff9ce7b` (L1), then T's 9 commits (applied cleanly), then L2's 6 commits through `26ba65c`.
  Parent conflict resolutions:
  - `runtime_store.go` index list keeps L1's `DROP INDEX` plus `runtime_runnable_sources_v2`
    and adds L2's live/settled partial indexes.
  - The Hint upsert takes L2's live-only count and L1's `source-limit` re-pend; this carried
    into L2's `upsertHintTail` const.
  - The W7 test was deleted, as L2 intended.
  Parent commit `669593f`: `rebase.go` keeps `ErrQuota` deliberately, because delivery-side
  `FailureSourceLimit` doesn't spend the quarantine budget and would retry indefinitely; the
  `FailureConflict` comment was updated. Snapshot `p-int-1`; focused check `p-int-check1` is
  running.
- **`p-int-check1` exit 0** (snapshot `p-int-1` = `669593f`, medium profile): fmt, lint
  (0 issues), vet and race/shuffle tests for sessioncapture (460.8 s), capturesource, scrub,
  publication, publicationr2, capturebackend, capturehooks, cli and cmd/agent-sessions. This
  is not the canonical `make check`, which runs once the final stack is integrated.
- **Later worker commits to cherry-pick onto integration:**
  - L1 `4d9b836`: TransferBudget factor table, idle-guard-disarmed test, and
    `Spec.TransferBudget` parity with R2 (`l1-g2j-job` exit 0).
  - L2 `c2dee0e`: phase3 rebase section; exact steady-state cost assertions
    (5/6/1/1, 5/8/2/1, 5/8/2/1); tests for the lived rebase mutants (`l2-check-g6d` exit 0).
    Rebase mutation: 33 killed, 5 lived, 2 not viable. The metadata-size boundary `76:12`
    is still untested; L2 says it is too expensive to test and gave no equivalence argument.
- **L1 scoped sessioncapture mutation** (`l1-mut-sc-job` exit 0; regex in
  `reports/l1-mutation.md`, baseline 39.3 s, 196 s per mutant): 12 killed, 3 lived, 1
  unmapped. No mutant lived in `spendsQuarantineBudget`, `classify`, `stepContext`,
  `transferAllowance` or the delivery allowance block. Lived: `runtime.go:168:43` (jitter
  `rand.Read` check, a real gap), `169:77` (equivalent: shifts the jitter edge by 2 ns),
  `321:41` (pre-existing `discoverLoop`). L1 was resumed to add the jitter test and rerun
  the mutation on `runtime.go` 160–175 and `budget.go` (to confirm `150:9` and `54:71`).
- **L2 `93b7462` (admission key length), accepted:** `ErrKeyLength` wraps `ErrQuota`, with
  an exact 432/433 boundary on both Hint and HintBatch. Requested follow-up: a cli parity
  test against `capturebackend.ValidateIdentity` (the 1024 rule is restated) and a
  document noting the R2 limit applies to every backend kind.
- **L2 final (`b0bf8eb`), accepted.** `l2-check-fin` exit 0 (sessioncapture, cli). It adds:
  - a cli parity test pinning admission key length to `capturebackend.ValidateIdentity`
    (sessions 431–434);
  - a document noting the R2 limit applies to every backend kind;
  - `TestDiscoveryResumesBoundedRootToCompletion`: 12 files, EntriesPerRoot=4, every
    intermediate batch reports `bounded` and Examined=4, the last batch is complete and all
    12 files are seen; no code change was needed;
  - an inclusive `MaxMetadata` rebase boundary test.
  `93b7462` and `b0bf8eb` are cherry-picked onto integration (`71ea144`, `eb40801`). L2 was
  resumed to rerun rebase mutation (five lived mutants have tests that are not yet confirmed)
  and to run first scoped mutation on GAP-04, GAP-11 and key length.
  Open, not proven: the installed discovery timeout is not confirmed against the installed
  binary, and the Mac cost is extrapolated from a Linux VM.
  **Deferred follow-up (recorded, not in scope):** after a rebase, every later delivery
  repeats Current+Read+merge (asserted at 5/6–8/1–2/1 calls). Folding the merged manifest
  into the local base would need local payloads for remote-only sources, which do not exist.
- **L1 `ed7e449` (GAP-01):** `TestDefaultRetryJitterIsAppliedWithinBounds`, `l1-g2k-job` exit
  0. Mutation reruns (`l1-mut-sc2-job`, then `budget.go`) are in progress.
- **L2 rebase mutation rerun** (`l2-mut-rb2` exit 0, baseline 4.3 s, timeout 60 s): all five
  previously lived positions (63:14, 76:12, 102:22, 105:23, 179:16) are now killed; 8 killed,
  0 lived. Scoped mutation on GAP-04, GAP-11 and key length is in progress (`l2-mut-store`).
  `ed7e449` is cherry-picked onto integration (`ad6969c`).
- **L1 closed (accepted).** `ed7e449` (`l1-g2k-job` exit 0) is on integration as `ad6969c`.
  Mutation reruns: `l1-mut-sc2-job` exit 0 (`runtime.go` 160–175: 10 killed, 1 lived
  `169:77`, which is equivalent because it shifts the jitter edge by 2 ns, and 1 unmapped);
  `l1-mut-r2b-job` exit 0 (`budget.go`: 22 killed, 0 lived). The targeted mutants
  `runtime.go:168:43`, `budget.go:150:9` and `budget.go:54:71` are now killed.
  Still recorded: `runtime.go:321:41` (pre-existing `discoverLoop`), and 21 pre-existing
  publicationr2 survivors in `backend.go`/`heads.go` (`reports/l1-mutation.md`).
  External blocker: the GAP-02 throughput defaults (1 MiB/s, 30 s base, 60 s–15 min, 30 s
  idle) can only be measured against live R2, which is excluded by authority. They stay
  conservative and unmeasured. With GAP-06, `conflict` now covers only irreconcilable
  divergence, so quarantine after `MaxAttempts` is the intended outcome, not an interim one.
- **G3 D1 interim review (uncommitted):**
  - `coalesce.go` `supersedeSubsumed`/`subsumes` runs inside the acceptance transaction. It
    considers only pending/retry jobs with no enrichment or optional-repair gap, and never
    proves withheld or optional sources.
  - Two existing tests were changed, and the parent accepts both edits:
    `TestEpochsAndRetainedPublication` now expects earlier jobs `superseded` and the final job
    `published`, with the root still holding all 3 epochs; the liveness subtest "newer job is
    not blocked" now expects job 1 `superseded` and job 2 `published`.
  - Steered to: add a non-subsumable sibling that preserves the original head-of-line intent;
    add the required in-flight supersede race test; comment the unmarshal skip; and before
    any reclamation code, verify the case of a job superseded locally whose CAS landed.
- **G3 D1 review fixes (uncommitted), accepted so far:**
  - There is a non-subsumable sibling liveness subtest (job 1 has an enrichment gap).
  - The race test asserts exact outcomes. At `current-before` the stale CAS lands at remote
    seq 1; job 1 stays `superseded`, and the final current is job 2's `PublicationID`.
  - `TestRebuiltSpoolRebasesAboveRemoteCurrent` now expects job 1 `superseded` and job 2
    published at seq 4 instead of 5. The remote-ahead intent is kept, along with the sealed
    remote and active local epoch assertions.
  - Perf fix: `pendingJobsIndexSQL` is now created in `initializeDB`, shared with
    `InitRuntime`. On first Open of a large legacy queue, `TestSourceLimit` went from 19 s to
    80 s; the cause was that supersession could not use the index. Integration note: this
    one-time `CREATE INDEX` cost on queues with about 30k jobs, and `rebase_test.go`
    overlapping with L2's `c2dee0e`, both need checking at cherry-pick.
- **L2 `runtime_store.go` scoped mutation** (`l2-mut-store`, baseline 6.1 s, timeout 60 s):
  - Equivalence arguments for `131:15` (`MaxMetadata` can't be reached under the 64 KiB
    hint cap), `151:21` (`slots==0`, same `ErrQuota`) and `325:171` (`LIMIT 0`).
  - `377:95` is killed by the new `TestClaimSourceRetryEqualToNowIsDue`.
  - The 16 timeouts are error-path flips in claim/delivery code outside GAP-04.
  - Key-length mutation `l2-mut-kl` is running.
- **L2 closed (accepted).** `a031a87` adds `TestClaimSourceRetryEqualToNowIsDue`
  (`l2-check-final2` exit 0 over sessioncapture, capturesource, scrub and cli). It is on
  integration as `f504c4f`. Scoped mutation (`reports/l2-mutation.md`, 60 s per-mutant floor):
  - `rebase.go`: 33 killed, 5 lived, 2 not viable; the rerun killed all 5 (8/8).
  - `fingerprint_transform.go`: 4 killed, 0 lived. `keylen.go`: 5 killed, 0 lived.
  - `scrub/digest.go`: no mutable operators (`l2-mut-dg` exit 1 is a tooling result, not
    RED).
  - `runtime_store.go`: the GAP-04 lived mutants are 1 killed and 3 argued equivalent
    (`131:15`, `151:21`, `325:171`); the parent accepts all three arguments. About 40 lived
    and 16 timeouts in claim/delivery/migration code from line 400 onward are pre-existing
    and outside GAP-04, so they go to the mutation-hardening worker.
  - `ownership.go` was not mutated (the change adds an `else` branch and no operators).
- **M (`0b03e7f1-9547-44d`, `gap/mutation` from `f504c4f`) dispatched for mutation
  hardening:** scoped per-mutant runs on `publication.go`/manifest validation, `deliver.go`,
  the 21 publicationr2 survivors and `runtime_store.go` claim code (line 400 onward).
  M must kill each lived mutant with an exact, workload-shaped test or argue equivalence.
  Production changes need parent approval. M must not touch G3's files.
- **Integration snapshot `p-int-2`** (`f504c4f`, adding L1 `ed7e449` and L2 `93b7462`,
  `b0bf8eb`, `a031a87`): focused check `p-int-check2` is running.
- **`p-int-check2` exit 0** (`f504c4f`, medium profile; same 9 packages; sessioncapture
  500.2 s).
- **G3 D1 `6cf2b41` accepted** (RED `g3-red2-job` exit 1, GREEN `g3-grn1-job` exit 0, check
  `g3-chk4-job` exit 0). The parent verified that all 8 changed files are byte-identical to
  the checked snapshot `g3-s5`. It is on integration as `1380f1d` (clean). The D2
  reclamation design is accepted: refs kept by triggers in the same transaction, pins for
  in-flight work, and for superseded-but-landed jobs no local payload need remains after
  N+1 (grep-verified by G3).
  **Risk:** sessioncapture race wall time is trending toward the 600 s package timeout
  (461 → 500 → 533 s). G3 is held to ≤500 s on its branch, and must fix the cause of any
  regression in production code rather than relax the timeout or shrink tests.
- **M `1b3c5e2` accepted** (test-only; `m-chk2` exit 0). Before these tests, the scoped
  `publication.go` mutation (`m-pub1` exit 0) gave 145 mutants: 124 killed, 11 lived, 9 not
  viable, 1 unmapped. The new tests pin exact behaviour at these boundaries: metadata limits
  (exactly `MaxMetadata` vs +1), the object descriptor `MaxInt64`/negative sizes, the
  `advances` sequence and ID rule, and Read resolve checks (with exact error strings). The
  commit is on integration as `8fae9ce`. Confirmation rerun `m-pub2` is running.
- **M `publication.go` confirmation** (`m-pub2` exit 0): lived count went from 11 to 1. The
  remaining `494:15` is argued equivalent in `reports/m-mutation.md`. The `deliver.go`
  baseline `m-del1` is running.
- **G3 D2 interim review (uncommitted `reclaim.go`):**
  - Accepted: the candidate commits in the same transaction as the supersede or ack; the root
    check and unlink happen under the install mutex; ENOENT counts as success; Open drains
    candidates before accounting; `staleAttempt`; `ReclaimPending`/`ReclaimFailed` in status.
  - Steered on five points:
    1. Reclaim errors after a durable accept or ack must not be reported as the capture or
       delivery failing.
    2. `payloadKept` uses `instr` scans over enrichment and optional-repair bodies. The
       installed Mac queue has 4096 rows (9.2 MB) and 1534 repairs. Use indexed refs, or
       measure the cost; also measure the `initReclaim` backfill at about 30k jobs.
    3. Optional-detachment candidates must not destroy unique, never-published bytes without
       a contract justification.
    4. Exact drain oracle: spool file set == root-referenced objects, which detects both
       over-deletion and leaks.
    5. Exact `cmp` in `TestSealedEpochPayloadsAreRetained`.
- **G3 D2 `g3-chk5-job` exit 1. This is a D2 safety defect, so D2 is not committed:**
  - Over-deletion: `TestRawAcceptanceNeverChecksOptionalPolicyAndFullWorkAdmission` fails
    with `publication object not found`, meaning a payload a live view still references was
    reclaimed.
  - Livelock: `TestOptionalRepairQuotaTerminalReserveKeepsRawPublishing/bytes` hung for
    9m18s. Delivery repeatedly goes `terminalOptionalQuota` → `supersedeOptional` →
    `acceptView`/`retireJobs` and never converges.
  - The sessioncapture and cli packages hit the 600 s timeout, and the cli isolation fixtures
    were killed at 15 s.
  G3 was steered to write a root cause for each first, then fix the cause in production
  code with a test that fails on the current tree, with no timeout changes. D2 is held until
  the exact drain oracle passes.
- **G3 D2 diagnosis:**
  - (B) livelock root cause accepted: G3's own De Morgan lint rewrite in `retireJobs` mixed
    `&&`/`||`, so selection supersession stopped superseding every undelivered job, and each
    terminal-quota supersede added a new pending job that delivery picked up forever. Fixed by
    a `retirable()` helper. `TestSelectionSupersessionSupersedesEveryUndeliveredJob` fails on
    the old tree; `g3-dg3-job` exit 0 (the 9-minute hang now takes 1.9 s).
  - (A) over-deletion: probable cause, not yet proven. Selection supersession created
    candidates for objects the new view holds only as `Withheld` descriptors, and the root
    check scanned only public manifests. Selection supersession now creates no candidates
    (unique local bytes kept). Still required: a RED on `g3-s11`, roots counting evidence
    `Original` objects, full check-v and the exact drain oracle, which names the retained
    selection-superseded class.
  - Housekeeping: a stray empty `zz_diag_test.go` is staged in G3's index; G3 was told to
    unstage it before committing.
- **M `deliver.go`:** `m-del1` hit the 1800 s deadline (exit 124, tooling), so M split the run
  by line range. `m-del2` (lines 248–340) exit 0: 7 killed, 5 lived. `m-del1`'s partial log
  shows `135:17` lived, plus error-path timeouts. `m-del3` (341–432) is running.
- **G3 D2 `g3-chk6-job` exit 1, two further defects found and diagnosed:**
  - (C) Performance: `TestSourceLimit` took 351.9 s (base 19 s, after D1 80 s), because
    `recordPayloadRefs` inserted one ref per source on every acceptance (about 1M
    statements at 1024 sources). Fix: record only the freshly installed object.
  - (D) `staleAttempt` hid an injected failure in the existing
    `TestDeliveryFailureAfterSupersedeCannotReviveJob/immediate`. Fix: only an `ErrIntegrity`
    failure of a superseded job counts as stale.
  - Also in progress: post-commit reclaim made best-effort, enrichment/repair holders indexed
    by triggers (no body scans), exact `cmp` in the sealed test. `g3-chk8-job` is running
    (`chk7` was a compile failure, not evidence). (A) still needs its RED proof.
- **M `deliver.go`:** `m-del3` (lines 341–432) exit 0: 21 killed, 3 timeouts, 0 lived. Lived
  total for the file: `135:17`, `274:42`, `289:11`, `296:43`, `314:42`, `338:8`. M is writing
  tests for them.
- **M `f95ab0c` accepted** (test-only `deliver_mutation_test.go`; `m-chk3` exit 0,
  sessioncapture 487.2 s). It uses a multi-source claude-shaped fixture: a rewritten main
  transcript with a sealed epoch, two subagents and an optional artifact. Tests pin:
  - the per-source policy recheck in check/upload when the scope pass succeeds;
  - the evidence-count mismatch giving `ErrIntegrity`;
  - the optional-unavailable latch, per repair state;
  - a missing evidence slot failing closed;
  - cancellation being reported when the job is superseded at cancel.
  On integration as `d2d7d5e`. Confirmation reruns (`m-del4`, `m-del5`) are running.
- **G3 D2 `g3-chk8-job` exit 1, further root causes:**
  - (E) G3's scripted edit had deleted the original superseded→`errSelectionSuperseded`
    block in `attemptManifest`. It is restored verbatim.
  - (F) A stale job's payload reclaimed mid-attempt surfaces as `fs.ErrNotExist`; this is
    now treated as stale only for a superseded job.
  - `TestSourceLimit` is still 276 s (base 19 s); G3 is profiling it (`g3-sl2-job`).
  D2 is still held.
- **G3 timing correction (measured):** under `-race`, `TestSourceLimit` takes 258 s on the
  pre-GAP-03 snapshot (`g3-sl2-job`) and 299 s on `g3-s18` (`g3-sl3-job`). The earlier
  19 s/80 s figures were non-race. D1+D2 add about 40 s under race, from the per-capture
  `retireJobs` scan over 1024-source manifests; G3 is reducing it. `g3-s18` passes the
  (E)/(F) regressions. Full check `g3-chk9-job` is running. Still open before the D2 commit:
  the (A) RED proof, evidence-`Original` roots, docs.
- **M `deliver.go` confirmed:** `m-del4`/`m-del5` exit 0, lived 6 → 0. Error-path loop
  timeouts are listed in `reports/m-mutation.md`. M has moved to the publicationr2
  `backend.go`/`heads.go` survivors (`m-r2a`).
- **G3 D2 `e3ee9db` accepted** (check `g3-chk11-job` exit 0, sessioncapture 489.5 s; targeted
  `g3-t1-job` and `g3-dg3-job` exit 0). The parent verified that all 12 files are
  byte-identical to the checked snapshot `g3-s21` and that no diagnostic file is staged.
  - Root cause of the (A) over-deletion: the `retireJobs` precedence bug made selection
    supersession create candidates for objects held only as `Withheld`.
  - Selection supersession now creates no candidates; those optional artifacts and raw
    companions are a named, retained class of unique local bytes.
  - `retiredObjects` skips Optional sources as defence in depth.
  - Enrichment and repair holders are indexed `payload_refs` rows maintained by triggers,
    so there are no body scans.
  - Post-commit reclaim is best-effort: a failure leaves the candidate with `last_error`,
    visible as `ReclaimPending`/`ReclaimFailed`, retried on the next pass or Open, and it is
    tested exactly.
  - `staleAttempt` covers only `ErrIntegrity`/`ErrNotExist` failures of superseded jobs.
  - The fault points `payload-accept-before` and `reclaim-before`/`after-unlink` are in the
    crash matrix.
  - Under race, `TestSourceLimit` went from 258 s to about 277 s.
  On integration as `3c4ef7b`. Open in later G3 deliverables: measure the `initReclaim`
  backfill at about 30k jobs, confirm there is no delete/un-repair path that bypasses the
  holder triggers, and add the exact spool oracle to the model-history property.
  Snapshot `p-int-3`; combined check `p-int-check3` is running.
- **`p-int-check3` (`3c4ef7b`, snapshot `p-int-3`) exit 0 across all 9 packages.**
  sessioncapture took 523.8 s, which is over the 500 s target and leaves 76 s before the
  fixed 600 s timeout. The trend is 461 → 500 → 523.8 s. The timeout is not relaxed. The
  verbose per-test measurement `p-int-v3` is running to attribute the time.
- **G3 resumed** for D2 follow-ups: (A) RED on `g3-s11`, scoped mutation of
  `reclaim.go`/`coalesce.go`, `initReclaim` backfill at about 30k jobs, and an audit of
  delete/un-repair paths. D3–D5 follow.
- **BLOCKER: the sessioncapture race time is at the 600 s timeout on integration.** `p-int-v3`
  (`-v`, snapshot `p-int-3`, 3 containers running) timed out at 600 s. Slowest tests:
  `TestSourceLimitIsTypedAndNotQuarantinedAsTransient` 305 s, which was already 258 s under
  race before GAP-03 because it does 1030 rewrite captures that grow to 1024 sources;
  `TestModelHistories` 45 s; `TestCrashProcess` 21.8 s. The package has no `t.Parallel()`.
  Worker PF (`gap/perf` at `f17f37b`, prefix `pf-`) was dispatched to profile first, then
  prefer production per-capture fixes (real sessions reach 379 sources). Test-structure
  changes are allowed only for the pre-existing quadratic cost, with identical assertions and
  a written justification. Target ≤420 s under contention. The timeout stays fixed.
- **G3 follow-ups accepted:**
  - `dece4d0` is a guard test only, not a cause. `g3-rc1`/`g3-rb1` show that (A) was the
    same precedence livelock: all payloads were present and no current pointer was ever
    published, so nothing was over-deleted.
  - `0443bc6` closes a real hole: `ReplayOptionalRepair` re-pended a repaired row after
    its holder was released, so a payload could be reclaimed while a replay needed it.
    Fixed by a `payload_rehold_repair` trigger. RED `g3-rd1-job` exit 1, GREEN
    `g3-rd2-job` exit 0.
  - Grep found no DELETE paths on `optional_repairs` or `enrichment_results`.
  On integration as `80874b5` and `f17f37b`.
- **G3 D2 follow-ups (in progress):**
  - `initReclaim` backfill measured (`g3-bf1-job`, exit 0, non-race): a synthetic queue of
    30,000 jobs (12–41 sources per job, every 3000th job with 1024 sources, 254,600 refs)
    takes 2.80 s in one transaction, once. A second Open takes 215 µs. That is about 4.7% of
    the 60 s operation budget for a one-time cost, so no chunking was added. Accepted.
  - Scoped mutation:
    - `reclaim.go` (`g3-mutr-job`): 31 mutants, 20 killed, 6 lived, 4 timeout.
    - `coalesce.go` (`g3-mutc-job`): 25 mutants, 23 killed, 2 lived.
    - Tests for the lived mutants are being written; reruns `g3-mutr2`/`g3-mutc2` are running.
- **PF race profile (`pf-prof2`, `TestSourceLimit` alone, race):** the test took 275.4 s.
  Cumulative time:
  - `Capture` 99.7 s;
  - `acceptView` 55.6 s;
  - `latestPolicyChecked` 41.1 s (`latest()` 22.1 s);
  - `encodeAcceptedView` 27.7 s (JSON Marshal 25.6 s);
  - `retireJobs` 19.4 s;
  - `checkObserved` 19.0 s;
  - `ValidateManifest` 12.8 s.
  Per-capture manifest decode and encode dominate.
- **M `3404cbe` accepted** (test-only `publicationr2/mutation_boundary_test.go`; `m-chk5`
  exit 0, publicationr2 67.1 s). It pins:
  - the key-length boundary 1023/1024/1025, using a sweep across kinds;
  - the 4xx class boundary (399/400/499/500);
  - Open/Current declared-length checks, and closing the body of an invalid response;
  - redaction of stream-read failures;
  - `validToken` printable/length edges;
  - the CAS body-limit boundary;
  - PutFile size edges;
  - idle-guard stall detection for upload and reconcile read-back;
  - list status/size bounds and namespace-listing advancement.
  The two stall tests assert an upper wall-clock bound of 4 s against a 200 ms idle guard.
  This is accepted as detection-latency evidence, not a relaxed timeout. On integration as
  `9399ead`. Scoped confirmation reruns of `backend.go`/`heads.go` (`m-r2d`) are running.
- **G3 scoped mutation round 2 (`g3-s25`):** `reclaim.go` 31 mutants: 26 killed, 0 lived,
  4 timeout (`err != nil` → `== nil` after Query/Exec hangs; counted separately, not as
  kills), 1 not viable. `coalesce.go`: 25/25 killed. Tests for the round-1 lived mutants
  (cross-session shared payload, rooted candidate, multi-object candidates, per-candidate
  error, legacy backfill, multi-job retire) are awaiting `g3-chk12-job` before commit.
- **G3 `fcfad75` accepted** (test-only; `g3-chk12-job` exit 0). It kills the round-1 survivors:
  - a payload shared across sessions survives one session retiring it, and B still delivers;
  - a rooted candidate is kept and its row dropped;
  - every object becomes a candidate, and every failed candidate keeps its own error;
  - the legacy `payload_refs` backfill works;
  - multi-job retire only touches plain states.
  Final scoped results: `reclaim.go` 26 killed, 0 lived, 4 timeout, 1 not viable;
  `coalesce.go` 25/25 killed. sessioncapture took 503.2 s on G3's branch, 3 s over the 500 s
  target (the tree varies 489–503 s between runs). This feeds the PF blocker. Slowest:
  `TestSourceLimit` 270.6 s, `TestModelHistories` 34.8 s, `TestCrashProcess` 14.3 s.
- **M `d537a16` accepted** (test-only; `m-chk6` exit 0). It pins `New` with a positive
  timeout, read-back close failure as an error, and `start-after` being sent only with a
  cursor. `m-r2e`/`m-r2f`/`m-r2g` reruns are pending.
- On integration as the cherry-picks of `fcfad75` and `d537a16`.
- **G3 D3 (cadence) in progress:**
  - Design: `dirty_since`/`debounce_until` columns, due time = min(now+Q, dirty_since+M),
    `ErrCadence` validation, debounce flush at Run start, and Status
    `DebouncedSources`/`DebounceLagNS`.
  - Config: `quiet_seconds`/`max_delay_seconds`. Defaults stay 0 until the D4 report.
  - Targeted `g3-d3b-job` exit 0; full check `chk13` pending.
- **PF profile done; first pass ran out of turns with no fix, so the blocker is still open.**
  Race profile of `TestSourceLimit` (`pf-prof2`, 265.8 s of samples):
  - `acceptView` 55.6 s, which includes:
    - `encodeAcceptedView` 27.7 s (about 4 manifest marshals per accept, plus
      `ValidateManifest`);
    - `retireJobs` 19.4 s (`undeliveredJobs` unmarshals every undelivered manifest,
      12.2 s).
  - `latestPolicyChecked` 41.1 s, which includes:
    - `latest()` decode 22.1 s;
    - per-source `checkObserved` 19.0 s.
  - Status from the test's final check: 12.0 s.
  Non-race the test takes 29.1 s; race instrumentation inflates the JSON reflection.
  PF was resumed at integration `95acce8` to implement, in order:
  1. a lazy manifest parse in `undeliveredJobs`;
  2. a single encode reused for the `MaxMetadata` check and export, with an
     accept-path boundary test;
  3. optionally, Status column selection;
  4. optionally, an invariant policy hoist. The policy is still evaluated for every source.
  Each fix gets scoped mutation. Target ≤420 s.
- **G3 D3 `chk13` exit 1:** a lint finding, and `TestRunnableSourceIndexMatchesClaimQuery`
  lacked a bind argument after `claimSourceSQL` gained a `debounce_until` placeholder.
  Changing that existing test from 2 to 3 bind args is accepted: the plan assertion
  (`runtime_runnable_sources_v2`) is unchanged. `g3-d3c-job` exit 0; `chk14` is running. In
  that run sessioncapture took 509 s; PF's fixes should restore the margin.
- **G3 D3 `c2ec0a3` accepted** (`g3-chk14-job` exit 0; the parent verified all 9 files are
  byte-identical to `g3-s31`). What it does:
  - Hints set `dirty_since` (keeping the first unconsumed observation) and
    `debounce_until = min(now+Q, dirty_since+M)`.
  - The claim query filters on `debounce_until<=now`.
  - Completion clears `dirty_since` only when the captured version is the claimed version;
    otherwise it is reset to now.
  - Run validates the settings (`ErrCadence`: non-negative, Q ≤ 1 h, M ≤ 24 h, M ≥ Q when
    Q > 0) and flushes the debounce at start, because observations are durable.
  - Status reports `DebouncedSources`/`DebounceLagNS`, and config adds
    `quiet_seconds`/`max_delay_seconds`. Defaults stay 0 (capture immediately) until D4.
  - Tests: exact Q/M release on a deterministic clock (hints at 0/4/8/11 s; released at
    12 s by M), lag of exactly 11 s, and immediate claim with zero Q.
  On integration as `f17d420`. sessioncapture took 525.6 s on G3's branch, which falls
  under the PF blocker.
- **PF fix 2 `2ac2951` (pending its full check):** `undeliveredJobs` fetches and parses the
  manifest only for plain jobs during coalescing (the SQL `CASE` matches the Go `plain`
  predicate). `TestRetireJobsKeepsUnparsablePlainJob` checks exact state counts. Focused
  run `pf-f3` exit 0.
- **M:** all 21 publicationr2 survivors are killed (`m-r2c`..`m-r2j` exit 0, 4 hang-type
  timeouts recorded). M is now on `runtime_store.go` lines 400 onward (`m-rs1`/`m-rs2`).
- **PF result: production fixes alone can't clear the blocker.** Under race, `TestSourceLimit`
  went from 275.4 s to 269.3 s with fix 2 (`pf-t1`) and to 263.4 s with fix 1 as well
  (`pf-fx1`, draft). That is about 12 s, or 4%. The remaining cost is spread across
  per-capture `latest()` decode, per-source policy checks, which can't be cached, and JSON
  reflection under tsan.
- **Parent decisions:**
  - **Fix 1 API:** fix 1 becomes `publication.MarshalValidManifest`, which marshals once
    and validates and returns those exact bytes. That replaces the draft's
    caller-trusted `ValidateManifestEncoded`. A table test checks equivalence with
    `ValidateManifest`/`json.Marshal` at the `envelopeFixture` −1/0/+1 boundary.
  - **Accept-path boundary:** PF's finding that the export envelope always binds first on
    the accept path is accepted, so there is no unreachable accept-path boundary test.
  - **Parallel tests:** option 3a is authorised. A parallel top-level test runs only
    alongside other parallel tests, after the serial ones finish. So `TestSourceLimit` is
    marked parallel together with other audited CPU-heavy, self-contained tests. Excluded:
    tests with package globals or hooks, env/cwd/umask changes, wall-clock or latency
    assertions, or crash subprocesses. Assertions stay unchanged.
  - **Acceptance:** sessioncapture ≤420 s on check-v, two independent green runs, and the
    full check.
- **G3 D4 (pending review):** `reports/g3-cadence.md` gives defaults Q=30 s and M=300 s, wired
  as daemon config with nil meaning default and 0 meaning disabled. `RuntimeOptions` zero
  stays immediate. Check `chk15` is running.
- **G3 D4 report reviewed (`reports/g3-cadence.md`, SCOPED simulation):**
  - Replay of D's 128 anonymised timelines (78 Mac, 50 BC-01; claude, codex and pi). Sweep
    Q ∈ {0…300} s, M ∈ {60…1800} s.
  - Stated rule: pick the smallest Q that cuts pooled p99 captures per session by at least
    70%, then the smallest M that bounds worst-case lag to about 5 minutes. That gives
    Q=30 s, M=300 s: p99 captures 509 → 142 (−72%), uploaded bytes −77%, PUTs −82%, lag
    p50 61 s, p99 277 s, max 301 s.
  - This agrees with D's independent recommendation of a quiescence debounce of at least
    30 s.
  - Limitations are stated: downsampled traces, append-only, one source per session, and
    no throughput data. The defaults rule is accepted on that basis.
  - Wiring: daemon config `*int`, where nil means default and 0 disables. `RuntimeOptions`
    zero stays immediate.
  - `chk15` exit 1: two cli tests assumed prompt capture with the new 30 s daemon default.
    The cli test-config helper now sets 0/0 explicitly. This change to an existing test is
    accepted: those tests assert capture behaviour, not cadence, and
    `TestCadenceDefaultsAndOverrides` pins the default. `g3-d4b-job` exit 0; `chk16` is
    running.
- **PF fix 1 revised** to `MarshalValidManifest` with an equivalence table test. `pf-fx2`:
  fmt, lint and vet clean, focused tests pass. Scoped mutation `pf-mut2` is running.
- **Harness incident, 2026-11-03 about 02:08: the Docker VM disk filled up** (162 MB free
  of 125 GB). 229 preserved scratch volumes from this delivery held 79.8 GB.
  - Invalid runs: `m-chk7` and `g3-chk17-job` both show ENOSPC and are not evidence.
    `pf-v1` (its `TestSourceLimit` stopped at 992 sources on a non-limit error) ended in
    the same minute and is treated as invalid.
  - Clean-up: the parent removed only idle volumes and stopped containers owned by this
    delivery (`machines.validation.owner=memory-delivery-20261102`, no running mounts).
    Volumes of other deliveries were not touched. 76.7 GB is now free.
  - Fix: `launch.py` now purges each job's scratch volume at job end (backup
    `launch.py.bak-20261103`). Evidence stays in `jobs/<job>/out`.
- **Accepted and on integration:**
  - G3 D4 `6d78c2f` → `db71c21` (`g3-chk16-job` exit 0; files byte-identical to `g3-s35`).
    `TestCadenceDefaultsAndOverrides` pins 30/300, disabled 0/0 and partial overrides.
  - G3 D5 `bddc3f2` → `9c7d866`: the model-history drain oracle. Roots are always a subset
    of the spool; spool equals roots exactly when no fault fired and no ack was lost; CAS
    writes ≤ jobs + republishes. GREEN `g3-m1-job` exit 0. RED by mutation `g3-m2-job`
    exit 1 (`RECLAIM`, with the unlink disabled).
  - G3 D5 `04039ec` → `85dbc5f`: PUT/CAS count property test, 12 seeds × 30 steps, exact
    1/1/1 per pending delivery and 0 otherwise. `g3-p1-job` exit 0.
  - PF `2ac2951` → `76a8ab5`. The cherry-pick conflicted only because both sides
    appended a test at the end of `coalesce_test.go`; the parent kept both.
  - PF `e99993a` → `f25e6b3`: `MarshalValidManifest` with an equivalence table test.
    `pf-mut3` killed 5 of 5 scoped mutants.
- **PF parallel option 3a:** the audit found 5 eligible tests (`TestSourceLimit`,
  `TestModelHistories`, the two inventory ceiling tests, `TestRestartAndLostAck`).
  `worker.sh` sets `GOMAXPROCS=1` for ad-hoc jobs, while the canonical gate
  (`canonical-check.sh`) exports `GOMAXPROCS=2`. PF will measure baseline `pf-base2` and
  parallel `pf-par1`/`pf-par2` under `GOMAXPROCS=2` on `f25e6b3`, then the full check.
  Commit only if every run is green and the parallel tree is ≤420 s.
- **`pf-base2` measured** (`f25e6b3`, no parallel, `GOMAXPROCS=2`, medium): sessioncapture
  ok in 515.7 s. That is over 420 s, so 3a is needed. CHECK_RC=1 came only from the
  `sqlclosecheck` lint in G3's model oracle. Slowest: `TestSourceLimit` 255.1 s,
  `TestModelHistories` 37.1 s, `TestCrashProcess` 15.7 s. `pf-par1` (3a) is running. PF
  was told to move 3a onto `37e11fa` before `pf-par2` and the full check.
- **G3 `e8a6310` accepted** (test-only lint fix: the oracle's root query is extracted to
  `rootFiles` and closed with defer; `g3-chk17b-job` exit 0, sessioncapture 534.8 s). On
  integration as `37e11fa`. G3's cadence.go scoped mutation (`g3-mutd2`): 16 mutants, 13
  killed, 3 lived. `36` (`>`→`>=`) is argued equivalent. The two default-constant mutants
  get `TestDefaultCadenceValues`; rerun `g3-mutd3` is running.
- **M:** `m-chk7` is invalid (ENOSPC). It is being rerun as `m-chk8` for the
  `runtime_store_claim_test.go` survivors (`479:28`, `569:8`, `620:37`).
- **`pf-par1`** (`f25e6b3` + 3a on 5 tests, `GOMAXPROCS=2`): sessioncapture ok in 483.2 s,
  against 515.7 s for the baseline. That is short of 420 s, because parallel tests resume
  only after all serial tests finish: the serial part is about 210 s, plus
  `TestSourceLimit` at 272 s. 3a is not committed.
  - The GAP-03 production slowdown is already recovered: `TestSourceLimit` is 255.1 s on
    `pf-base2`, against 258 s on the pre-GAP-03 base. The rest of the growth is new tests.
  - Parent decision: a broader parallel audit (option 1) on `7372f16`. Every candidate test
    is audited per test for globals, env/cwd, process-wide state, subprocesses and
    wall-clock assertions. Optionally, `TestSourceLimit`'s final Status read can be replaced
    by a direct query asserting the same publication-id fact.
  - Acceptance: `pf-par3`/`pf-par4` ≤420 s and lint clean, plus the full `pf-full2`.
    Option 3b (database seeding) stays ruled out.
- **Accepted and on integration:**
  - G3 `5b39ebd` → `094a4b8`: `TestDefaultCadenceValues`. cadence.go scoped mutation
    `g3-mutd3`: 15 killed, 1 lived, which is equivalent (`36` `>`→`>=`).
  - M `747b843` → `7372f16` (test-only `runtime_store_claim_test.go`; `m-chk8` exit 0,
    sessioncapture 465.5 s). It pins:
    - `sourceCapturedAt` per-source selection;
    - the `dueIdentity` boundary at ±1 ns and for a quarantined job;
    - the deferred claim release when clearing the claim fails.
- **PF broad parallel set (75 tests, `pf-par3` 428.3 s, lint clean) is not accepted.** The
  parent traced each test through its helpers and found that PF's screen missed helper calls
  and `time.After` real-time deadlines. Nine tests would flake or weaken an assertion under
  parallel CPU contention, so they go back to serial:
  - `TestObjectLongerThanOperationBudgetPublishes`: throttled real-time backend via
    `runThrottledDelivery`;
  - `TestOptionalNetworkStall…`: 2 s;
  - `TestOptionalRepairQuotaTerminalReserve…`: a 30 ms window that passes without checking
    anything under load;
  - `TestVerifiedOptionalRepairRestart…`: 3 s;
  - `TestRuntimeDiscoveryExpiredCallback…`: 2 s;
  - `TestRuntimeDiscoveryHintRestartAndNonceSweepFence`: 3 s;
  - both `testRuntimeNetworkRetryRawACK` users: 5 s;
  - `TestRuntimeFatalOptionalBoundaries`: 5 s.
  `TestRuntimeDeliveryProgressIdleScheduling` stays parallel because it runs on synctest's
  virtual time. PF must redo the audit through helpers and may add eligible tests under
  0.5 s. Acceptance is unchanged: `pf-par5`/`pf-par6` ≤420 s and lint clean, plus
  `pf-full3`. If the honest eligible set can't reach 420 s, PF reports and the parent
  decides; the eligibility rules are not stretched. The step-3 query, which asserts the
  identity, sequence and `PublicationID` of `last.Sequence`, is accepted as its own commit.
  `pf-par4` is superseded.
- **Integration HEAD `1dc9fe2` (snapshot `p-int-4`, 716 files)** now holds every accepted
  commit:
  - PF `f3b7ce6` → `9e0e8b7` (the step-3 direct query) and `e2f0403` → `7ad1703`
    (71 `t.Parallel()` lines, 0 other lines). Both are byte-identical to the green snapshot
    `pf-p5`. Evidence: `pf-par5` 377.6 s and `pf-par6` 408.2 s, both lint clean;
    `pf-full3` exit 0 across all 9 packages (sessioncapture 384.5 s). The ≤420 s target
    is met with the 600 s timeout unchanged; the baseline was 515.7 s.
  - C `be3b1b3` → `ca489a7`, `dc2c68d` → `802519b`, `1638b31` → `1dc9fe2` (GAP-07/08).
    One mechanical conflict in `ValidateManifest`: PF's `MarshalValidManifest` form
    `(data, err)` was combined with C's fixed-width digest-placeholder argument to
    `exportMetadata`. No other stale call sites.
  - `p-int-canon4` is **invalid as evidence**. It was a harness invocation error: it was
    launched without `--coverage-output`, so `go tool covdata` could not write `cover.out`
    into the read-only `/source`, and `cover` exited 2 (Makefile:75). Every Go package had
    already passed (sessioncapture 403.1 s, syncer 272.7 s). It is rerun as
    `p-int-canon5` with the flags of the previous green canonical run
    (`--profile large --deadline 1800 --seed-cache --coverage-output`).
  - `p-int-canon5` **exit 2 (real regression, not harness)**. Every gate before
    `capture-acceptance` passed, including cover/crap (sessioncapture 354.1 s). Two
    `test_capture_journey.py` journeys failed with `coordinator acceptance deadline
    exceeded` (the 45 s `until()`). Cause: G3 D4's daemon defaults Q=30 s and M=300 s
    apply to the real binary because the journey configs omit `quiet_seconds` and
    `max_delay_seconds`; with `debounce_until = now+Q`, each chained capture waits at
    least 30 s. The `candidate-live-r2` and `capture-delivery` suites did not run because
    make stopped first.
  - G3's session had expired, so the fix went to a fresh worker J (`999ab41c-86fb-4da`,
    prefix `j-`, progress `reports/j-progress.md`) on `gap/journey` (from `1dc9fe2`).
  - **Accepted:** J `08de567` → integration `1150db5` (4 test files, no Go, byte-identical to
    snapshot `j-1`). Explicit `quiet_seconds=0, max_delay_seconds=0` in all four
    `capture-runtime/v1` configs under `tools/`; assertions and deadlines unchanged. Only the
    `CoordinatorJourney` config is proven necessary; the other three are precautionary.
    New `test_configured_cadence_debounces_hinted_source_in_actual_binary` (Q=2 s, M=4 s):
    not-before-Q lower bound, then capture within the existing `until()`. RED `j-one-red`
    (runtime forced to Q=M=0: captured before Q, `ONE_RC=1`), GREEN `j-one1`.
    `j-ca1` `make capture-acceptance` `CA_RC=0`: analytics 13 OK, candidate-live-r2 6 OK,
    capture-delivery 12 OK (1 skip guarded by `CAPTURE_V1_REPAIR_BINARY`).
  - **Canonical `make check` green:** `p-int-canon6` on snapshot `p-int-5` (HEAD `1150db5`,
    `--profile large --deadline 1800 --seed-cache --coverage-output`),
    `CANONICAL_MAKE_CHECK_EXIT=0`, `SOURCE_POST_VERIFY_EXIT=0`, 17 min 8 s wall. It covers
    fmt, lint, fix, tidy, deadcode, vuln ("No vulnerabilities found"), cover (package 60% and
    total 75% thresholds PASS), crap (0/10 above CRAP 30), bench-smoke, actionlint and
    capture-acceptance (analytics 13 OK, candidate-live-r2 6 OK, capture-delivery 12 OK with
    1 guarded skip). sessioncapture 372.7 s under coverage+race, within the unchanged 600 s.
- **Gap closure on `gap/integration` (`2deac62..1150db5`, 51 commits, 121 files):**

  | Gap | Resolving commits (integration) | State |
  |---|---|---|
  | GAP-01 | `cc96583`, `e4949da`, `ad6969c` | Fixed, tested |
  | GAP-02 | `3ba5fc3`, `ff9ce7b`, `11482fa` | Fixed and tested; throughput defaults unmeasured (live R2 excluded) |
  | GAP-03 | `1380f1d`, `3c4ef7b`, `80874b5`, `f17f37b`, `8c63190`, `f17d420`, `db71c21`, `9c7d866`, `85dbc5f`, `37e11fa`, `094a4b8`, `1150db5` | Fixed, tested; cadence Q=30 s/M=300 s from Mac+BC-01 replay |
  | GAP-04 | `ad7b274`, `c17b69e`, `71ea144`, `eb40801`, `f504c4f` | Fixed, tested |
  | GAP-05 | `e4949da`, `c5e1108`, `669593f` | Fixed, tested |
  | GAP-06 | `97842b0`, `8fc0e80`, `669593f` | Fixed, tested; repeated-rebase cost deferred |
  | GAP-07 | `802519b` | Fixed, tested |
  | GAP-08 | `ca489a7`, `1dc9fe2` | Fixed, tested |
  | GAP-09 | `8617382` | Seam and tests added |
  | GAP-10 | `8617382`, `2c7bf7d` | Fixed, crash matrix asserts reclaim |
  | GAP-11 | `085e85f`, `73a41c0`, `aa179a9` | Fixed; transformed re-hint cost not reproducible on current local roots |

  Test-quality work in the same range: model-history and privacy canary (`64482d9`), hint
  burst/inbox bounds (`8e8458d`), strengthened W4/W5/W7 oracles (`9ff7c6c`), fuzz targets
  (`ed554df`, `3774512`, `ce7d913`), SCOPED mutation hardening (`8fae9ce`, `d2d7d5e`,
  `9399ead`, `95acce8`, `7372f16`), perf (`76a8ab5`, `f25e6b3`) and the audited
  parallel set (`9e0e8b7`, `7ad1703`).
- **PF final report (completed, no new commits).** It confirms `f3b7ce6`/`e2f0403` as
  already integrated (`9e0e8b7`/`7ad1703`). PF ran its gates on the working tree before
  splitting it into two commits. The parent's byte comparison against `pf-p5` and the
  canonical `p-int-canon6` cover the split commits. Recorded margin risk: on the same tree
  `pf-par5` took 377.6 s and `pf-par6` 408.2 s, so the ≤420 s target has only about 12 s
  of headroom under contention. `TestSourceLimit` (226–256 s) remains the floor of the
  package time. The 600 s timeout is unchanged.
- **Not run or not proven (recorded, not claimed):** live R2 and installed/BC-01 writes
  (excluded by authority); GAP-02 upload throughput defaults; native Darwin and CI; C4 WAL
  backup and P16 oversized-object repair; the installed discovery timeout; a live-traffic
  validation of the cadence replay (simulated, append-only); 19 hang-type timeout mutants
  with no fast test; the redundant `publication.go:494` guard (equivalent mutant, removal
  is a production change); model-history generator produces no held optional/repair
  objects (unit tests only).
- **Superseded next action:** publication is now authorized; see "Production delivery —
  November 4" below. The defaults and deadlines stay
    unchanged. Prompt-capture journeys set explicit 0/0 cadence, with justification. A new
    end-to-end journey proves the binary honours configured cadence: it asserts a
    not-before-Q lower bound and capture within the existing deadline, and is RED on a
    cadence-ignoring tree. The whole `capture-acceptance` target must pass, then the
    canonical check is rerun on integration.
- **M final (completed, no real defects).** All SCOPED survivors were killed except the
  `publication.go:494:15` `>`→`>=` mutant. It is argued equivalent because
  `ValidateManifest` rejects first, which makes the guard redundant; removing it is a
  production change and stays out of scope. Recorded: 19 hang-type timeout mutants
  (deliver.go, publicationr2, runtime_store.go) with no fast test, and coverage left
  unrerun (deliver.go 99-127; runtime_store.go 400-520 except 479). Details are in
  `reports/m-mutation.md`.
- **G3 final (D1–D5 complete).** The open items are recorded rather than blocking:
  - the model-history generator produces no held optional or repair objects, which unit
    tests cover instead;
  - the cadence replay models append-only sessions and is simulated, not live;
  - the optional BC-01 aggregate read was not done.
- **PF corrected parallel set** (`pf-p5`, on `7372f16`): 71 parallel top-level tests, with
  the 9 timing-sensitive tests moved back to serial, plus the step-3 query.
  - The parent's own transitive helper trace agrees: the only flagged hits are
    `TestModelHistories` (`time.Since` feeds `t.Logf` only) and
    `TestRuntimeDeliveryProgressIdleScheduling` (`Sleep` in a synctest bubble). The diff
    has 13 non-`t.Parallel()` lines, all of them the step-3 query.
  - `pf-par5`: sessioncapture ok in 377.6 s (baseline 515.7 s), CHECK_RC=0, lint clean.
    Slowest: `TestSourceLimit` 255.8 s, `TestModelHistories` 46.2 s, `TestCrashProcess`
    15.0 s.
  - Pending before commit: flake guard `pf-par6` and full check `pf-full3`.
- **M finished all targets.** `runtime_store.go` from line 400: lived 3 → 0 (`m-rs3`..`m-rs6`
  exit 0), with 6 hang-type timeouts recorded. M's final report is pending.
- **G3 (`7cf92667-4fb8-4dc`, `gap/coalesce` from `669593f`) dispatched for GAP-03:**
  supersession of proven-subsumed jobs, local payload reclamation (not remote GC),
  quiescence/max-delay cadence, a replay simulator over D's timelines to choose Q/M, model
  history extension and scoped mutation.
- **L2 GAP-06 review (uncommitted `rebase.go`):** the parent accepted the direction: rebase above
  a verified remote current, retain every remote epoch and seal superseded active ones, raise a
  typed conflict on an equal-revision commitment mismatch, CAS on the original token, and
  converge after a lost ACK through `covered`. `l2-red-g6` exit 1 for the expected reason.
  The commit is held for three required changes. First, remote-only selected sources were not
  rechecked against current exclusions, so a rebase could republish an excluded source; this
  must fail closed with `ErrPolicy`. Second, after a rebase every later delivery repeats the
  Current+Read+merge; this needs a 3-capture convergence test with per-delivery backend call
  counts, plus either a safe fix or a documented, measured follow-up. Third, conflict, covered,
  concurrent-CAS, lost-ACK and source-limit tests with exact assertions, then scoped mutation.
- **T mutation (third attempt):** `t-mut-quota3` was killed at its 3600 s deadline (container
  exit 143, no result.json, empty log). The parent diagnosed two causes: the `cut` pipe
  block-buffered and dropped per-mutant output, and coefficient 100 lets one hanging mutant
  run for ~100× the package baseline. Steered to unbuffered output, a `--dry-run` mutant
  inventory, and, if needed, a targeted per-mutant driver with a focused `-run` regex and
  a measured timeout, reported as scoped mutation.
- **T mutation:** `t-mut-quota` and `t-mut-quota2` hit exit 124 at the 1800 s deadline
  (gremlins reruns the whole package suite per mutant; no result). These are tooling
  limits, not RED. `t-mut-quota3` is running with a 3600 s deadline.
- **T progress:** commits through `4f580f7` (hint burst/inbox, W4/W5/W7 oracles each
  killing its mutant and passing base at `-count=3`, fuzz targets + `FUZZ_TARGETS`,
  bounded fuzz minimization + mutation coefficient variable). Fuzz-stall cause per T:
  the engine minimizes a newly interesting input for up to the default 60 s with one
  worker and reports no execs meanwhile; toggling `-fuzzminimizetime` flips the
  behaviour. The SIGQUIT dump was not captured, so this rests on that A/B, not a dump.
- **L2 (`833310fa-5a28-49c`, `gap/discovery` from `cc96583`):** GAP-04 (inventory cap
  must not refuse new sessions because of settled history; BC-01 rows are 99.9%
  `captured`, so absent-row expiry alone is insufficient), discovery completion/timeout
  root cause, GAP-11 transformed-source fingerprints covering every input, GAP-06
  remote-ahead convergence, optional admission-time key-length check. File rules keep
  it off L1's in-flight files. Started (progress file, first job `l2-m1`).
- **Pending:** L2 (GAP-04/06/11 plus discovery truncation/completion) after L1 lands,
  stacked on `gap/liveness`; GAP-03 after L1/L2.

## Production delivery — November 4

Operator request (2026-11-04):
1. Publish the gap work as a `gh stack`, review it, run CI (including native macOS) and
   merge it.
2. Run operating acceptance now, which is steps 1–4 of the plan below: Mac first, then
   BC-01, then combined operation.
3. Resolve these items now:
   - C4/P12 SQLite WAL backup under a live writer;
   - the installed discovery timeout (root cause, then fix);
   - the 19 hang-type mutants (root cause, then fix);
   - removal of the redundant `publication.go:494` guard;
   - optional and repair objects in the model-history generator;
   - an investigation of the repeated-rebase cost.

   The test-time margin is deferred to future work.

- **Authority:** publishing branches and PRs, CI, and merging to `main` once CI is green.
  Operating-plan steps 1–4 are now authorized: installed-state and R2/BC-01
  inventory, coordinated build from merged source, activation and acceptance on Mac then
  BC-01. Step 5 (final defaults, legacy retirement, reset or deletion of keys) stays
  **excluded**, as does release tag publication. The preservation rules below still apply.
- **Sequence:** step 3 source work is merged before activation, so live acceptance runs on
  one source state. The read-only installed-state reconciliation (plan step 1) starts at
  once.
- **Stack (step 1):** a parent-built stack from `delivery-stack`
  (`/Users/mm/dev/memory-wt/delivery-stack`). Its final code tree is byte-identical to the
  green `1150db5` (`p-int-canon6`). J's `08de567` is split by file into layers 3 and 4: in
  layer 3 the cadence defaults arrive with the journey-config fix, so no layer is red by
  construction.

  | Layer | Branch | Content |
  |---|---|---|
  | 1 | `delivery/1-liveness` (`ce7d913`) | GAP-01/02/05/09/10, test infrastructure |
  | 2 | `delivery/2-admission` (`f504c4f`) | GAP-04/06/11 |
  | 3 | `delivery/3-coalesce` (`ab5b5aa`) | GAP-03, mutation hardening, perf, parallel tests |
  | 4 | `delivery/4-consumer` (`c38f77f`) | GAP-07/08 consumer |
  | 5 | `delivery/5-docs` | gap register, README index, this brief |

## Next action: separately authorized operating acceptance

The source-publication/merge request and this documentation request do **not** grant
activation, deployment, original-source/R2/BC-01 SSH inventory, host/runtime changes,
analytics refresh, release publication, archive reset/rebind/deletion or legacy retirement.
The operating plan and authorization boundary are posted at:
https://github.com/mich-murphy/memory/pull/30#issuecomment-6515658801

Once compatible operating authority and supported execution arrangements are in place,
the **assistant owns execution and follow-through**, not a list of manual operator chores:

1. **Reconcile current state without activation.** Read the contract and this brief;
   inspect actual installed binaries, hooks/wrappers, supervisors and consumer coordinates
   on Mac then BC-01. Inventory eligible sources and pending/accepted work under the
   permitted access scope. Distinguish excluded, unreadable, absent and unavailable
   history. Preserve current identities, effective local/remote opt-outs, custom roots,
   credentials and archive binding. Do not rely on the dated `b8cbed2`/off observation.
2. **Prepare a source-aligned coordinated delivery.** Build/verify required artifacts
   from the merged source, not the old `74c00f4` snapshot. Coordinate capture, the shared reader,
   credential launchers/static wrappers and Mac analytics source/runtime. The prepared
   Linux analytics venv is **not Darwin ABI-compatible**. BC remote-current analytics
   uses a Mac-local reader bound to BC identity/archive, not interactive SSH as a reader.
   Dormant staging must not call commands that start capture or refresh consumers.
3. **Accept Mac first, then BC-01.** Under explicit activation authority verify actual
   native Claude/Codex/Pi hooks and automatic new-session capture through durable work,
   actual conditional R2 publication/readback, the authoritative reader, importer, event
   index and report. Retain exact identity/body/epoch coverage and visible partial/errors.
   Candidate Linux-native behavior must be demonstrated, not borrowed from the legacy
   prototype. Exercise restart, destination outage/retry without another hook, source
   absence and effective privacy exclusions; preserve the previous readable publication.
4. **Accept combined operation.** Reconcile both inventories, fresh-reader consumer
   sweeps, two-host independence and ongoing automatic capture. Measure representative
   CPU/RSS/I/O, request cost, settled work, freshness, largest records and offline spool/
   retention horizon in the actual vertical slice. Agree and meet operating budgets;
   do not substitute a timer, HTTP 200, upload success, green CI or old report for health.
   Diagnose/fix ordinary failures and rerun the unchanged relevant gates before acceptance.
5. **Only after acceptance, with separately reviewed scope:** consider final defaults/
   legacy retirement and deletion of reviewed retired keys. Recheck writer fences and
   inventory drift; exclude every new/current/in-flight/policy key and the already replaced
   global binding. Never blindly replay an old inventory, reset or deletion plan.

Until then, the accurate status is **merged and development-qualified, not operationally
accepted**. A new artifact, commit, green CI run or source merge does not cross this gate.

## Preservation, execution and evidence rules

- Preserve accepted unpublished payloads, pending spool and queue DB/WAL/SHM, original
  sessions/research, effective privacy, identities/roots/binding, advanced analytics/body
  caches and live locks. Use supported SQLite backups when authorized; never restore a
  stale database over newer accepted state, delete a lock to bypass a writer, or recreate
  deleted generated state incidentally. Preserve unrelated branches/worktrees/stashes;
  do not restore intentionally deleted `CLAUDE.md`.
- `service install --apply` **starts capture**; `capture-v1 run` and
  `publication_refresh --once` are activation/mutation, not dormant staging.
- Follow [quality gates](quality-gates.md) and read `~/.config/agents/validation.md`
  before expensive work. Use pinned Nix and the shared Docker worker-side admission;
  source/dependencies/runtime and every module-cache alias/prepared tool must be read-only,
  actual validation network-none, both temporary variables on executable VM-native EXT4.
  Preserve native Go 600s/package, race/shuffle, fixtures/history counts and safety gates.
  Current policy supplies no local native-Darwin admission: do not invent one, use bare
  host validators, reinstall `bc-heavy`/Pueue or re-enable capture for validation.
- Parent owns the sole brief, integration, scheduling and final evidence acceptance.
  Delegated workers use **medium effort**, with explicit scope/ownership. One CI watcher
  at a time: `gh run watch <run-id> --interval 60 --exit-status`; no polling loops or
  `gh pr checks`. Post full URLs immediately when human approval is actually required.
- This documentation-only reconciliation archived the prior handoff body verbatim,
  retained the earlier `74c00f4`/October 24 evidence body unchanged, checked `git diff --check`
  (including the new archive), and resolved all 86 relative documentation links/anchors
  in the edited current pages. No Go/Python test suites, builds, new CI runs or
  native/live/installed gates were rerun for these documentation edits.
- Current integration evidence: private
  `~/.local/state/machines-validation-pilot/memory-pr-integration-20261030/`, especially
  `parent/FINAL-ACCEPTANCE.json`, `parent/final-evidence.sha256`, and
  `ci-runtime/{LEDGER.md,local-evidence.sha256,ci-evidence.sha256}`. Parent verified
  817 local evidence files and a separate 19-file CI seal. Failed/noncanonical attempts
  remain failures/history, not green results. Earlier release/performance evidence and
  full progress ledgers remain linked from the [October 30 archive](session-capture-handoff-archive-20261030.md).
