# Implementation handoff: dependable session capture

**Updated: 2026-10-24 (AEDT). Current state:** the opt-in offline replacement candidate
is implemented and independently reviewed within recorded scopes. Full unchanged
development gates passed in the dated evidence below, but a fresh checkpoint run has
one failing process journey; see the checkpoint ledger before claiming current green
status. It is **not release-accepted, deployed or cut over**.
Default installed collector and analytics paths remain legacy. No real-source inventory,
production ingestion/R2 operation, install, archive reset or original-source deletion
was performed by this implementation work.

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
| Backend construction | `capturebackend` + `publicationr2`: exclusive explicit Local/R2 config, named static credential refs, constrained endpoint, actual SDK signing/conditional requests, lazy remote runtime binding | No default credential chain, SDK retries or remote availability prerequisite for local raw startup; real R2 endpoint acceptance is outstanding |
| Lifecycle and hooks | `capturedaemon`, `capturehooks`, CLI: explicit private config, opt-in owned hooks, nonmutating plans and deliberate apply, Darwin/Linux service definitions with injected runners | Legacy hooks/settings preserved; no actual install, trust/reload or supervisor acceptance claimed |
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
| Both-machine preserved inventory/policy, installed paths/restart | **Not performed** | This Mac + BC-01 access/coverage and effective privacy must be validated |
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

## 4. Current next action

First diagnose the fresh checkpoint acceptance failure recorded above and rerun the
unchanged gate. Then prepare and execute **explicitly scoped operating acceptance**,
not another architecture restart: actual Claude/Codex/Pi fields/trust/reload/disabled-hook behavior; isolated real
conditional R2 failure/readback; representative resource and freshness loads; preserved
identities/exclusions/learned-root inventory on both machines; installed paths/restart.
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
is implicit. For example, inside pinned Nix with both live variables unset:

```sh
unset AGENT_SESSIONS_TEST_R2 AGENT_SESSIONS_LIVE
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
