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

## PR publication and merge request — October 30

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
- Parent owns acceptance and this brief. The new PR caretaker owns branch/commit/push,
  review/CI supervision and eventual merge; startup/ID and the full PR URL will be
  recorded after dispatch. Parent may update this section before the final PR head is
  frozen; caretaker must coordinate that commit rather than race shared-file edits.
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
