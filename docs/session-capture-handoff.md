# Implementation handoff: dependable session capture

**Updated: 2026-10-24 (AEDT). Current state:** autonomous delivery continues after
reviewed checkpoint `c9749da` (after `b68a412`). Its process-journey synchronization failure is fixed and repeated
100 times successfully; actual scratch-native and isolated real-R2 journeys also pass.
The SSH-free authoritative reader and durable per-session discovery-failure isolation
are implemented and independently reviewed. Final unchanged serial gates all pass:
`make check` 803s, `make test` 451s, nine 30-second fuzz targets 283s, 95 analytics
tests 29.551s, 86.9% merged coverage and CRAP thresholds. The
[delivery ledger](session-capture-delivery.md) records exact scopes and logs.
It is **not release-accepted or finally cut over**. Candidate binaries/configs and
supervisors are now installed and active on **both machines**; independent production
source backfill has started. The real shared binding was initialized on Mac and verified
using BC's own preserved credentials. Fresh identity/policy/root inventories remain
private. Legacy PATH writers are now fenced on both hosts and managed hooks replaced;
old settings/binaries were privately preserved. Publication refresher/dashboard run on
staging 8766; old dashboard 8765 is unchanged. Original sources and old remote keys remain untouched. See the delivery ledger's current operating
state; earlier inventory-only/no-install statements below are historical boundaries.

This document is current continuation guidance. Requirements remain the
[session-capture contract](session-capture-contract.md) and
[independent enrichment contract](session-enrichment-contract.md). The
[offline evidence](session-capture-offline-evidence.md) records final checks and scoped
resource observations. The former cumulative handoff is preserved in the
[historical implementation ledger](session-capture-progress-history.md): its old
“not implemented” and “next action” statements are **not outstanding tasks**.

## 1. Start here

Read [AGENTS.md](../AGENTS.md), the two contracts above, this handoff and the
[documentation map](README.md). Inspect `git status --short` and `git diff --check`;
use the pinned `nix develop` environment for development/validation.

### Checkout and preservation

- Repository `/Users/mm/dev/memory`, implementation base `9078a3e`. The operator has
  authorized a scoped candidate checkpoint; see the checkpoint ledger below and Git
  history for the resulting revision. A clone of the base alone does not contain it.
- Preserve all preexisting changes, intentional `CLAUDE.md` deletion, untracked source
  and documentation, `.gitignore`, analytics and frozen research. Untracked is not disposable.
- Preserve machine/archive identities, effective exclusions and opt-outs, learned
  roots, original sources, credentials and accepted pending bytes. Do not print private
  prompts, transcripts or credentials in diagnostics or plans.
- Do not reset/clean/stage unrelated files, restore `CLAUDE.md`, modify other worktrees,
  install or run real services, enable live opt-ins, or clean remote keys as incidental
  documentation/test actions. Old cold-recovery/pruned-piece worktrees are not the plan.
- Verification skill access was explicitly disabled by the operator during the earlier
  audit. Do not bypass that restriction. The gap remains recorded in
  [verification/README.md](verification/README.md).

## 2. Current implementation and boundaries

| Area | Implemented candidate | Important boundary |
| --- | --- | --- |
| Wire and reader | `internal/publication`: **session-publication/v2**, complete immutable manifest/object closure, guarded current reference, pinned streaming read/export, metadata committed last | No history replay or silent v1 reinterpretation; `capture-v1` command/prefix is retained independently of wire version |
| Durable capture | `internal/sessioncapture`: SQLite WAL / synchronous FULL authority, private fsynced payloads, stable jobs, append proof and retained rewrite epochs, crash reconciliation, exact encoded quota | Accepted bytes are not a disposable cache; no automatic GC or silent eviction |
| Scheduling | Concurrent per-session capture; autonomous bounded discovery/delivery/retry; destination pause; quarantine/replay; independent durable path/native/wake inboxes; live read-only status | Byte capture, observation, publication, enrichment and consumer freshness remain separate; hooks alone do not own bytes |
| Discovery and ownership | `internal/capturesource`, `capturecoordinates`: bounded default/learned inventory, shared classification, explicit logical/root/leaf aliases and bounded external-artifact ownership | No implicit inode/symlink merging, global UUID ownership scan or undeclared parent/sibling authority; unresolved artifacts remain pending/partial |
| Source privacy | Runtime ownership/claimed-identity checks, all restrictive coordinate spellings, source identity sampled before policy callbacks and descriptor-relative no-follow opening | No callback-created read authority; missing originals retain exclusions; known opt-outs do not blanket-pause healthy unrelated sessions |
| Derived sources | Bounded zstd, protected-config scrubbing, authorized coherent readonly SQLite/WAL backup | No raw fallback, source writes, immutable-VFS WAL omission or whole-source scratch copies; present WAL needs independent authority; rollback journals explicitly rejected |
| Native and enrichment | `internal/hook`, `launch`, `snapshot`: transient allowlisted native context, independent durable native evidence/enrichment, self-contained deterministic bundles, effective injected policy, selected-byte closure | Context is not inferred from daemon environment; no prompt capture; missing native/CWD remains partial; enrichment defaults off |
| Optional progress | Explicit optional-artifact selection/withholding, separate raw/optional delivery lanes, durable repair and verified-attachment-pending restart | New same-session main is readable and locally ACKed before optional network gates release; required bodies cannot be demoted; session exclusion remains scope-fatal |
| Backend construction | `capturebackend` + `publicationr2`: exclusive explicit Local/R2 config, named static credential refs, constrained endpoint, actual SDK signing/conditional requests, lazy remote runtime binding | No default credential chain, SDK retries or remote prerequisite for local startup; isolated real R2 passes and both production binding/auth checks pass; complete operating acceptance remains |
| Lifecycle and hooks | `capturedaemon`, `capturehooks`, CLI: explicit private config, opt-in owned hooks, nonmutating plans and deliberate apply, Darwin/Linux service definitions with injected runners | Both supervisors now installed/started; legacy hooks/settings preserved pending controlled fence/new hook install; installed trust/restart/freshness acceptance remains |
| Analytics and callers | `publications.py`: reader/v2 import; report/v2; explicit dashboard/query `--source publications`, owned verified selected-body cache, source/epoch coordinates | Legacy defaults/research preserved; no Python storage resolver, GET-time import, original-path fallback, withheld-body serving or silent old DB reset |
| Acceptance | Mandatory `make capture-acceptance`, included in test/check and separate CI consumer job; **eleven actual-process journeys** | Offline actual SDK peer is not R2; synthetic native commands are not actual agent/trust evidence |

Source/file maps and concrete contracts:
[wire/reader v2](session-publication-v1.md), [analytics v2](session-analytics-v1.md),
[phase-3 behavior/bounds](session-capture-phase3.md),
[backend configuration](session-capture-backends.md). Versioned filenames retain link
compatibility, not retired wire compatibility. The original one-session slice and
legacy `internal/syncer` are not substitutes for the implemented runtime.

### Resource and operating limits

Implemented bounds include 1-GiB default encoded spool, separate four × 1-GiB
prepared-file slots, conservative free-space reserve, bounded decoders/config/snapshot
content and metadata, discovery entry/FD/inventory caps, repair/history/metadata caps,
operation budgets and terminal gaps. These are **not a combined RSS or production
capacity proof**. No automatic deletion/GC is claimed.

Scoped serial diagnostics cover tiny Local/SDK fixtures and a 134,218,178-byte synthetic
source: exact independent restore after source/spool paths move aside; 1-MiB quota
refuses with zero jobs. See [offline evidence](session-capture-offline-evidence.md) for
raw metrics, allocated-size scope and limitations. Representative combined RSS/disk
amplification/request cost and actual discovery freshness remain release evidence.

## 3. Acceptance/progress ledger

| Slice | Current state | Evidence or remaining work |
| --- | --- | --- |
| Wire, durable capture, authoritative reader/export | Implemented offline | Complete selected closure, exact bytes/roles/epochs, restart/faults/CAS/lost-ACK/privacy/quota tests |
| Coordinator, discovery, aliases, service/hook candidates | Implemented offline | Autonomous restart/retry, coverage generations, declared-root moves, bounded ownership, disabled/unresolved policy persistence and nonce-guarded reconciliation |
| Native evidence, self-contained enrichment, optional isolation | Implemented offline | Same-session main progress before optional gate release; durable repair/crash/restart/revocation; bundle closure and restrictive provenance |
| Actual backend factory and consumer callers | Implemented opt-in | Actual SDK peer factory → Queue → reader → Python; existing dashboard process uses only owned projection after source/store/spool/export removal |
| Full unchanged development gates | **Dated PASS; fresh acceptance FAIL** | Final serial checks below remain historical evidence; checkpoint rerun failed one of eleven journeys |
| Actual native agents, real conditional R2, representative resource/freshness | **Not accepted** | Separate deliberately scoped operational acceptance |
| Both-machine preserved inventory/policy, installed paths/restart | **In progress** | Fresh effective inventories, exact-revision binaries, production binding/auth and both active supervisors pass; installed native/restart/coverage acceptance remains |
| Controlled rebuild/fencing/cutover/deletion | **Not performed** | Follow original contract; no archive reset or deletion authorization follows from offline passes |

### Independent review and completed fixes

Medium-effort agents used disjoint ownership and independent reviews. The
[historical ledger](session-capture-progress-history.md) retains exact failed findings,
repairs, focused checks and agent IDs. Do not reopen superseded implementation tasks:

- Optional publication isolation, fatal-cause propagation and bookkeeping joins are
  repaired. Durability/local-store failure cannot disappear into successful retry
  bookkeeping or be downgraded by object attribution. Pure caller cancellation alone
  is graceful; ordinary network retries remain autonomous.
- Selected raw/unchanged/transformed/snapshot reads bind actual descriptors to identity
  observed before authorization, with all canonical components no-follow.
- Dedicated readonly WAL VFS preserves exact uncheckpointed rows. Newly learned present
  WAL origin on unchanged bytes is durably retained with unchanged epoch/object and new
  revision; a later genuinely unchanged observation can no-op. Revocation still blocks
  after original paths disappear; healthy B continues. Typed rollback-journal regression
  is nonvacuous. Standalone and repaired integration independently pass.
- Root-qualified claimed opt-out identities are checked independently from healthy path
  ownership, including old/current/logical aliases and withheld-only retained evidence.
- Bounded Git metadata/filename scratch replaces unrelated repository-body/object copying;
  denied ignore rules prune only the affected subtree. Enumeration and cleanup are bounded
  and failures retained, not swallowed.
- Legacy `snapshot.BuildContext` forwards caller context without replacing legacy policy
  or selectors with empty new-runtime Options. Legacy `Build` remains its Background wrapper.
- Intentional-crash subprocess coverage now counts actual atomic/race-instrumented command
  execution and checked metadata/counter flush. It does not close the Queue or release
  optional work before the real exit, and cannot silently ignore flush failure.

Original-contract reviewer `5d6e429c-b629-419` gives a **conditional offline PASS within
its spot-check scope**, with no additional confirmed code/wiring blocker. This is not
exhaustive correctness proof or release acceptance.

### Latest full development evidence — 2026-10-24 AEDT

Pinned Nix, live flags unset, full gates serial after code settled:

- **`make check` PASS, 622.40s**: formatting/lint/fix/tidy/deadcode/vulnerabilities,
  all atomic race/shuffle tests, merged **86.8%, 13,059 / 15,051 statements**, unchanged
  package/total gates; max CRAP **29.40**, threshold **30**; benchmark smoke (syncer
  **113.092s**), actionlint and eleven actual-process journeys **12.727s**.
- **Fresh `make test` PASS, 460.36s**: eleven journeys **11.247s**, full CGO/race/shuffle;
  syncer **414.472s**, queue **117.885s**, snapshot **67.416s**.
- **All nine `make fuzz` targets PASS**, unchanged **30s** each, **285.31s** overall.
- **Complete analytics: 73 tests / 23.744s PASS**, no skips/warnings; JavaScript syntax
  and `git diff --check` pass. No classifier rerun or private transcript submission.
- Logs: `/tmp/memory-replacement-final.pIMS8w/{make-check-current,make-test-current,make-fuzz,analytics-current}.log`.
  Final merged profile: `current-merged.cover.out` in that directory.
- Original `cover.out` restored and SHA256 verified:
  `70386d5160553c62f5b9cef9c38af0f7273336aebd491000ab9259fd94f117b9`.

No assertion, timeout, race/subprocess accounting, eight simulation histories / forty
steps, real 64-piece fixture or coverage/CRAP gate was weakened. Prior failed gate
attempts remain historical evidence; they are not today's status.

### Current-state alignment follow-up — 2026-10-24 AEDT

A subsequent broad documentation/public-comment/help audit corrected stale current-state
claims and archived the former cumulative handoff without discarding its evidence.
Independent review found and verified closure of the last Local-only public-help claim;
`TestCaptureBackendRootUsage` now covers help/missing/unknown command paths.

Fresh pinned-Nix checks: full format/lint **PASS, zero issues**, whole CLI race/shuffle
**33.364s**, fresh actual candidate + all analytics **73 tests / 22.839s**, including
eleven process journeys, and JavaScript syntax **PASS**. Documentation links/anchors /
whitespace and `git diff --check` pass; original `cover.out` hash is unchanged.
No runtime algorithm/API/schema changed. Full repository check/test/fuzz were not
rerun for this wording/help/regression pass; their dated evidence above is preserved.
See [alignment audit](current-state-audit.md) for exact scope/logs and the blocked-skill
limitation. No new confirmed contradiction remains within independent review scope.

### Candidate checkpoint and operator direction — 2026-10-24 AEDT

The operator authorized committing the implemented candidate and its supporting
contracts/evidence as a reproducible checkpoint. Scope includes the existing shared
code/safety changes, candidate packages, analytics source/tests, build/CI wiring and
current/historical capture documentation. Unrelated navigation research, frozen local
research evidence, ignored analytics data/environments and generated artifacts remain
outside the checkpoint and are preserved locally. The commit records the offline
candidate, not deployment or release acceptance; dated gate results above are not
silently relabeled as fresh runs against the checkpoint.

Fresh checkpoint check: pinned Nix with both live flags unset,
`make capture-acceptance` **FAIL: 10/11 journeys passed, 11.808s**. The failure was
`CoordinatorJourney.test_native_launch_and_closed_snapshot_survive_without_originals_or_spool`
at `tools/analytics/test_capture_journey.py:537`: the first export's manifest differed
from the later export's manifest where equality was asserted. Cause is not established;
no assertion, timeout or runtime code was changed to hide it. This newer result means
the checkpoint is **not freshly green**, notwithstanding the earlier dated passes.
Diagnose publication/enrichment completion and fixture synchronization before claiming
current offline acceptance or proceeding to operating acceptance. `git diff --check`
passes. Full check/test/fuzz, full analytics and native/live/host gates were not rerun
for this checkpoint. A medium-effort read-only scope review found no confirmed private
dataset/credential or dependency blocker; this is not an exhaustive secret scan. It
also noted that legacy analytics uses external `zstd`, whose presence in a minimal
fresh environment is not established by the current Nix/CI declarations.

The operator reaffirmed a clean rebuild from available sources on this Mac and BC-01:
**do not add legacy-refresh guards, migration machinery or compatibility work as a
cutover prerequisite.** Legacy archive/analytics information need not be retained or
migrated. This does not authorize deleting original sources, privacy policy, identities
or unrelated local research evidence, or establish that remote-only history is available
locally. Retain existing safety coverage until the old paths are deliberately retired.

Capacity/retention implementation belongs in a measured end-to-end vertical slice once
the design is accepted. First validate the corrected writer/reader/consumer journey and
operating behavior; then measure representative amplification/freshness and implement
only justified capacity/retention changes with their own safety tests. Do not introduce
speculative chunk/history complexity or automatic deletion to make this checkpoint.

### Delivery ownership correction — 2026-10-24 AEDT

Checkpoint `b68a412` was incorrectly treated as a stopping point after a failed gate.
The operator clarified that the coordinator must continue through delivery: diagnose,
fix, independently review, run unchanged gates and proceed with the agreed acceptance
sequence. A failure or checkpoint is not a request for human intervention. This rule
is now recorded in `AGENTS.md`. Work continues; neither the fresh failure nor release
acceptance is claimed resolved by this process correction. The subsequent
[delivery ledger](session-capture-delivery.md) records the diagnosed synchronization
fix, repeated green checks, new actual native/R2 acceptance, two-machine inventory and
consumer-discovery integration. Use its latest results rather than treating the
checkpoint failure above as an unresolved diagnosis.

## 4. Current next action

Current-root A/B isolation review and final serial development gates are complete.
Bounded real-R2 current-root acceptance also passed (70/160 backend operations,
5.48s journey); it still uses the isolated test binding, not the production binding.
Checkpoint `c9749da` is committed and both installed supervisors are backfilling.
Continue **explicitly scoped operating acceptance**, not another architecture restart:
fence legacy PATH/workers, install new managed hooks and autonomous consumer, then verify installed native paths,
representative resource/freshness loads, both-machine source coverage, policy/identity
preservation, supervision/restart and exact authoritative restores through analytics.
Scratch-native and isolated R2 results above are already implemented evidence, not
outstanding implementation requests. The approval-gated BC SSH agent later refused
signatures; an existing authenticated Azure management channel was verified against the
same VM and identity without bypassing approval or copying keys. Autonomous consumer
refresh must use local Go R2 current-root discovery, not interactive SSH approval.
Do not invent a human inventory prerequisite for additional offline tests, or treat
absent external evidence as proof that implemented offline code is still missing.

Actual native/remote/host work requires deliberate targets/access/mutation scope; do not
perform it as an incidental audit. Existing acceptance scripts for legacy v3 are not
replacement endpoint acceptance. Verify current host state before acting; old PID,
backlog and v0.5.0 observations are dated evidence, not live status.

Then follow the original controlled sequence: preserve policy/identities/roots/evidence,
validate isolated new binding, rebuild/reconcile/catch up both machines, fence old
writers/hooks, validate automatic capture/retry/restore/analytics, cut over, and delete
only the reviewed retired key set. Permission to replace R2 is not proof of local source
completeness or permission to delete originals, research or unrelated objects.

### If code changes again

Use the unchanged [quality-gate commands](quality-gates.md), run focused changed-behavior
tests, then final full gates serially. No live opt-ins, dependency install or host change
is implicit. For example, inside pinned Nix with all live-test opt-ins unset:

```sh
unset AGENT_SESSIONS_TEST_R2 AGENT_SESSIONS_LIVE AGENT_SESSIONS_CAPTURE_NATIVE CANDIDATE_R2_LIVE_ACCEPTANCE
make test
# Preserve the preexisting cover.out even on failure; retain new coverage separately.
gate_dir=$(mktemp -d /tmp/memory-replacement-check.XXXXXX)
cp cover.out "$gate_dir/original-cover.out"
(
  trap 'if [ -f cover.out ]; then cp cover.out "$gate_dir/generated-cover.out"; fi; cp "$gate_dir/original-cover.out" cover.out' EXIT
  make check COVER_DIR="$gate_dir/coverage"
)
make fuzz FUZZTIME=30s
```

Do not run all full gates concurrently, raise timeouts, suppress missing coverage or
represent an extended diagnostic as a passing unchanged gate. Update this current ledger
with actual new checks; preserve the historical ledger and frozen research separately.

### Focused measured inventory quota correction — 2026-10-24 AEDT

Reported production metadata reaches exactly 4096 runtime sources and discovery
issues. Code diagnosis confirms a retained-record admission ceiling, not destination
pause: new identities receive quota backpressure while admitted identities can still
coalesce/capture/publish; resolved issue history also consumes capacity. No production
source/credential inspection or host operation was performed for this correction.

Candidate config now optionally accepts `max_inventory_records` (zero/omitted =
4096, positive maximum 65536), forwarded through the single runtime queue-options
constructor to source, issue and scope admission. Batch limits remain 4096 and detail
pages remain at most 256. Existing read-only status uses full SQL aggregates and
requires no migration or count-path change. Suggested 32768 is an explicit operating
choice, not evidence of representative capacity acceptance. See the phase-3 quota
section. Active hosts remain unchanged by this work.

New tests cover 4097th source/issue admission and truthful paginated status, configured
ceiling rejection, durable inbox backpressure across same-cap restart, raised-cap
resumption without deletion, retained accepted publication identity, existing/new
source capture and publication drain, config loader roundtrips/bounds and CLI wiring.
Pinned Nix checks: focused race/shuffle queue/config/CLI including all
`TestRuntimeDiscovery*`: PASS (10.495s / 1.519s / 1.567s); complete affected
`internal/sessioncapture` and `internal/capturedaemon` race/shuffle: PASS
(92.622s / 1.739s); `make lint`: PASS, zero issues;
`golangci-lint fmt --diff` and `git diff --check`: PASS. An initial new test wrongly
expected one bounded delivery turn to drain three publications; corrected it to three
turns without changing runtime behavior or assertions. Initial lint findings were
fixed rather than suppressed. No full repository/native/live/analytics/fuzz gates
were run in that focused implementation turn.

Parent continuation: independent medium-effort scoped review **PASS**, no confirmed
blocker; bounds/defaults, atomic admission, non-mutating rejection, same-cap durable
backpressure/higher-cap restart, complete SQL counts and unchanged coverage gates
checked. This was static review, not another test execution. **413** source/build/test
inputs remain frozen. First fresh unchanged serial run (`gates-inventory-capacity`):
**make check PASS 628s; make test FAIL** in existing one-second healthy-session,
five-second startup/fatal-injection and 15-second isolation budgets. No deadline,
assertion or gate parallelism changed. Under concurrent initial backfill, measured
capture used 321–332% CPU plus refresher near one core; contention is supported, not
confirmed as sole cause. Same failing seeds / three focused repetitions pass both
before (queue 20.236s / CLI 16.508s) and after (19.850s / 16.693s) the explicit owned
production scheduling experiment: only `Nice=10` in capture/refresher units and live
owned process priorities, no restart or queue/projection change. A measured 60-second
window still made capture/refresh progress; this is not proof the scheduling change
fixes full-load gates or the high capture CPU. The unchanged retry under
`gates-inventory-fairness` was **deliberately stopped**, not passed: subsequent actual
exception diagnosis produced a necessary Python monitor correction and changed the
frozen inputs. Only the owned gate process tree was stopped; live capture/consumer
were untouched. Original `cover.out` restored exactly after both interrupted/failed
runs. New frozen full gates remain required. Prior c9749da results do not validate this later tree.
Neither operating config has yet been raised. Next: finish all serial gates, commit /
rebuild exact revision, controlled both-host upgrade with pending work preserved, then
installed native -> authoritative restore/import/index/report and final freshness /
classified catch-up acceptance. Do not reset the queue to clear the measured bound.

Actual consumer retry recovery is recorded in the delivery ledger: 2,579 targets,
0 errors / 0 head failures, both saved original publication coordinates now imported;
no normalizer modification in that first window. The subsequent actual probe captured
four `FileNotFoundError` exceptions in `export -> disk_bytes -> lstat`, before import:
Go's temporary export metadata entry was atomically renamed between enumeration and
inspection. Deterministic separate-process regression RED before the narrow correction;
focused 19 PASS (4.716s), independent static review PASS, full analytics 97 PASS (33.073s)
in pinned Nix/owned runtime/current test binary. Two Python files changed, not deployed;
no deadline/budget/symlink/completed-export validation weakened. Original refresher
entrypoint restored/running, preserving the separately approved Nice=10 scheduling
field. Earlier 95-test installed pass (39.079s) predates this correction.
BC's unchanged installed-Pi plan now passes after separately authorized owned parent
mode tightening 0775 -> 0700, preserving its inode and all original source contents.
25 private pure helper tests pass, including bounded numeric quota schema review;
new binary/config hashes must be freshly reviewed after the upgrade, not assumed.
No native/model run yet. Old v3 metadata-only inventory completed (81,896 objects /
2,823,793,063 bytes / 82 LIST pages); no body GET or deletion. No final cutover or
retirement acceptance is implied.

### Settled monitored candidate — fresh validation in progress

Bounded quota and export-monitor corrections have independent scoped PASS reviews.
An additional measured pure-string `mayHaveBackupSuffix` prefilter refinement avoids
ordinary-name regexp false positives while the **unchanged regexp remains the sole
stripping authority**. No policy construction/identity cache, path-resolution, callback,
filesystem-check, scheduling or deadline change. Actual-callback synthetic medians
improved approximately 9–16%; exact `New`/`Resolve`/Lstat/exclusion-Stat counts match.
This does not resolve/accept production throughput. A proposed policy-construction
cache was explicitly rejected because it would miss protected-location repoints;
optional-repair success spin was also disproved (one worked / nine idle turns).

Actual post-write pinned-Nix: full capturepolicy race/shuffle PASS 1.947s; focused CLI
policy/enrichment/alias/privacy/claimed plus new source/credential/protected-state/
config repoint, live exclusion and cycle regressions PASS 6.791s; unchanged 30s
`FuzzBaseName` PASS 31.364s (42,498 executions, plateau noted); targeted lint zero issues,
format/diff checks PASS. Independent static review PASS, no confirmed blocker.

**414** settled source/build/test hashes remain unchanged. The first
`gates-inventory-monitor-policy` run **failed make check**: the existing returning
isolation journey's coverage-instrumented command **build** exceeded its unchanged
15-second context. Other gates were not run. Original `cover.out` was restored exactly.
Both isolation journeys subsequently passed ten ordinary race/shuffle repetitions
(72.949s); that invocation did **not** exercise the coverage-only build branch. Three
further repetitions with the actual coverage environment/instrumentation and same
failed shuffle seed **PASS 30.587s**, including that build branch. No source, deadline,
assertion or gate-parallelism change. The full unchanged serial retry
`gates-inventory-monitor-policy-retry` then recorded **make check PASS 630s; make test
FAIL** in the existing five-second enrichment-attachment and optional-fatal boundary
budgets. Fuzz/analytics/JS were not run after that failure; all 414 hashes still matched
and original coverage was restored exactly. Nice=10 is not established as sufficient
resource control or as a full-gate fix.

A separate deterministic scheduling defect is now corrected and independently
reviewed: `deliveryLaneNext` previously reported `did=true` when nonwaiting delivery
found a busy lane and performed no attempt. This bypassed the existing worker idle
poll. The work-aware helper now reports false before an attempt and true once an
attempt is reached; old error-only callers retain their API. Scheduling claims still
release before I/O, raw/optional lanes remain independent, and every privacy/upload/
publication check and deadline is unchanged. Synthetic RED: both busy lanes reported
10/10 false progress turns with zero attempts/puts/publications. GREEN: 0/10, with
unchanged zero-side-effect/state assertions and a virtual-time observation of the
existing 100ms idle poll. Already-paused scheduler behavior was idle before the fix;
only the helper's idle cases were additionally tested, not misreported as old defects.
Queue race/shuffle checks PASS 121.155s, 26.207s and 19.445s (including the failed seed
and existing fatal/independence/fairness/restart cases); matching CLI enrichment tests
PASS 4.697s; lint zero issues, format/diff PASS; independent focused static review PASS.
Actual coverage-only isolation branch with this source also PASS 10.902s. These are
not production CPU or large-publication timeout acceptance.

The new **415**-input unchanged serial run `gates-inventory-monitor-policy-progress`
ended **make check PASS 624s; make test FAIL**. The sole reported failure was
`TestRuntimeFatalNetworkRetryKeepsRawAlive`, seed `1792819926578271000`, at its
`wrong raw view: <nil>` assertion (1.06s test duration). **This is not a timeout**:
the assertion combines newest-raw identity and optional-withheld state, so diagnosis
must first distinguish actual stale raw publication from a healthy concurrent repair
or an insufficient fault fixture. Do not remove the assertion, weaken its race checks,
or infer the cause from duration. Syncer PASS 406.907s; fuzz/analytics/JS not run after
failure. All 415 source/build/test hashes match; original coverage restored exactly.
Private synthetic diagnostics then proved an insufficient test ACK predicate: a
legitimate prior withheld raw publication at seq3 satisfied `published sequence >
optional seq2`, while newest raw seq4 was still pending. After releasing raw delivery,
seq4 ACKed and the newest-source/withheld assertions passed. This controlled execution
demonstrates a stale read, not rollback; the exact historical uninstrumented branch
remains unknown. The approved **test-only** correction now awaits an ACK at or after
`newest.Sequence`, a stronger condition. A controlled regression shares that exact
wait and the original assertions: old predicate RED 1.39s; final source race/shuffle
with the failed seed and existing network/fatal cases, count=3, GREEN 37.665s; original
network case count=20 PASS 35.303s before cleanup-only lint correction; final lint zero,
format/diff PASS. All original operation/wait budgets, runtime concurrency, source/
withheld/liveness/retry assertions remain. Independent focused static review PASS.
Only `runtime_fatal_test.go` changed after the 415-input run; coverage remains restored.

The unchanged **415**-input serial run `gates-inventory-monitor-policy-progress-ack`
ended **ALL PASS**: `make check` **708s**, `make test` **453s**, all nine 30-second
fuzz targets **287s**, **97 analytics tests 33.454s**, and JavaScript/whitespace checks.
Coverage **87.0%**, package/total thresholds PASS, no reported CRAP>30. All 415 hashes
still match and original `cover.out` SHA
`70386d5160553c62f5b9cef9c38af0f7273336aebd491000ab9259fd94f117b9`
is restored exactly. Earlier failures/interruption remain recorded, not replaced by
focused results. New-tree Nix packaging, scratch-native/live-R2 and installed operating
acceptance have not yet run. Four production correction groups and the test correction
are **not installed yet**. Latest read-only Mac status still has 4096 sources/issues,
completed sweep0, pending sources855/publications499, 14 quarantined jobs and 30 inbox
backpressure hints; none was cleared. Next: commit reviewed source, exact-revision
Darwin/Linux builds and package validation, then controlled32768/monitor upgrades;
verify actual retry recovery without queue/projection reset; review fresh BC
native spec hashes then run the authorized isolated installed-producer journey;
complete classified catch-up, quarantine recovery, restore/index/report/restart/
freshness/resource acceptance and final dashboard/old-key cutover. Original source
and old remote bytes remain undeleted.

### Actual offline producer-cost evidence — not an implemented fix

A sandboxed private copy of accepted spool data reproduced delivery cost using the
actual delivery core and actual read-only privacy callbacks, an in-memory backend,
network denial and an unchanged **60-second** operation deadline. No original source
bodies or credentials were opened; no active queue, source, R2 or repository data was
changed. Only whitelisted counts, public function names and timings were reported.

- One 16-source publication (14.2 MB decoded, 748 retained enrichment assets) completed
  in **30.878s**: 36 full asset-check passes, 26,928 asset callbacks and 82,528 filesystem
  policy calls. `checkAssets` consumed 30.037s, including 26.886s filesystem policy;
  payload integrity verification consumed 0.021s. Nested times are not additive.
- A 45-source publication (49.4 MB, the same 748 assets) **timed out offline at 60.002s**:
  `checkAssets` 55.809s, filesystem policy 51.810s, 41,117 asset callbacks and 26 sink
  puts completed. Local privacy-check work can therefore exhaust the delivery budget;
  this does not establish the cause of every active timeout.
- The largest decoded and a previously inspected 59-source publication, both without
  enrichment assets, passed offline in 13.310s and 11.588s. Eleven quarantined jobs
  were observed in that read-only snapshot; none was cleared or replayed live.

A **private overlay only** combined protected-location construction freshly inside
**each** filesystem-policy invocation, without retaining policy objects across calls
or removing asset callbacks/authorization boundaries. Matched 16-source replay
improved to 23.101s with identical completed check counts. Independent scoped plan
review approved that limited freshness model, not identical incidental within-call
race observations. The worst publication **still timed out at 60.001s**; this is not a
sufficient production fix and has not been added to the source tree. An additional
pure-string noise-suffix prototype had no meaningful actual-delivery improvement and
was not adopted. Deeper timings locate remaining cost in fresh `Resolve` and
`isWithin`, not filename parsing or payload verification.

Per-invocation metadata reuse with final revalidation was reviewed only as a possible
experiment; its non-atomic/ABA limitations prevent claiming preservation of every
old intermediate observation. It is **not implemented or approved for release**.
A separately reviewed design permits durable acknowledgement/resumption of completed
immutable dependency PUTs, not policy or existence caching. A **private overlay only**
now compiles and its focused synthetic sessioncapture/CLI tests PASS 20.415s, with the
same selection under race PASS 31.961s. It stores bounded per-job dependency receipts,
uses validated logical R2 destination identity plus a freshly validated complete
archive descriptor, retains every new-write/start/final/manifest/CAS guard and full
remote body verification (also already-current), and conditionally clears all matching
receipt bits only after authoritative positive missing-dependency verification.
No generation marker is required by the scoped design review; corruption/cancellation/
authentication failures are not missing evidence. Independent implementation review is
PASS for receipt authority/durability, guards, full verification, missing/reset
classification, destination binding and overlay integrity. Abrupt-process durability,
protected-root repoint fixtures and actual resource behavior remain unproven. This
does **not** establish actual 45-source/748-asset completion, full gates,
release approval or any production/schema change. All five original overlay targets
still match; original tests are retained; runtime delivery/store fixes are not masked.
Keep all race assertions, privacy boundaries, retry limits and meaningful deadlines.

### Private controlled-upgrade and installed-Pi preparation — no new execution

The quota upgrade helper now starts the existing unit rather than regenerating it,
preserving Nice=10 and unknown fields. Original unit **and parent** device/inode/mode/
UID/GID/mtime/ctime, SHA and bytes are compared before and after start; atime is excluded.
Same-byte replacement/metadata mutation and partial binary/config replacement failures
have explicit regressions. All 24 helper and three artifact tests PASS. No apply occurred.

Mac Pi preparation reached 65 pure tests, but independent full private-helper review
**BLOCKED current8**. It identified a false startup premise: the actual pinned installed
shell wrapper invokes **absolute `/usr/bin/python3`**, not PATH-resolved Python. Its
launcher's env-Python shebang is irrelevant to that invocation. Pins matched, so this
was a semantic modeling error, not file drift. Base/venv prefix metadata and the 1,906
base-file/eight-alias comparison remain valid software evidence, but do **not** prove
the actual callback's interpreter closure. The two-tree/353,705,879-byte current8
closure is therefore insufficient for installed proof. Do not change the installed
wrapper or claim that changing PATH repairs it; establish the real shim/developer/
interpreter/import/loader route instead.

Review also found wildcard loopback grants to every port, not only the synthetic SSE
endpoint, and a software ACL validation gap outside POSIX-writable ancestor exceptions.
Both require private-helper corrections and fresh review: exact preselected numeric
port/negative unrelated-loopback probe, and bounded software ACL validation across the
complete code closure. No unsafe native execution occurred; interlocks stayed closed.
Private source/config/policy/inbox/session guards remain unchanged. Root/current-UID
writer-authority evidence applies to the inspected exception edges, not an unproved
all-ACL closure. Mach/sysctl/process-info requirements, inherited loader run-path
context and complete identity/content-bound effective-policy review remain separate
acceptance items. All future review flags stay false. Actual wrapper/SDK/extension,
software permissions and runtime configs are unchanged; no help/model/probe/native
capture ran. BC/Mac proofs still need post-upgrade fresh hashes/config review and
explicit execution authorization. Production capture remains historical/default4096.
