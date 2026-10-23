# Historical session-capture implementation ledger

> **Archived 2026-10-24 (AEDT). Not current implementation or continuation guidance.**
> This preserves the former cumulative handoff, including intermediate plans,
> incomplete APIs, failed checks and subsequently repaired defects. Statements such
> as “current”, “pending”, “not implemented” and “next action” below apply to their
> historical checkpoint, not today's checkout. Do not restart those tasks or treat
> old commands as rollout authorization. Use the [current handoff](session-capture-handoff.md),
> [authoritative contract](session-capture-contract.md) and
> [final offline evidence](session-capture-offline-evidence.md) instead.
> Original ledger text follows unchanged apart from this title and archival notice.

**Updated:** 2026-10-23. **Current checkpoint:** first offline writer/shared-reader/
analytics slice implemented; its `make test` and `make check` passed after runtime
and coverage repairs. Phase-3 concurrent capture/coordinator work is now in progress
and needs fresh full gates. Native/live/two-machine release acceptance remains outstanding.
No production deployment, remote ingestion, or deletion. See the progress ledger and
next commands.

This handoff records where work stopped, the decisions already made, concrete
implementation guidance, acceptance gates, and restart commands. The authoritative
requirements remain [session-capture-contract.md](session-capture-contract.md). If an
implementation detail below needs adjustment, keep that contract and this handoff in
sync; do not silently substitute legacy v3 behavior.

## 1. Start here in a fresh session

From this same checkout:

```sh
cd /Users/mm/dev/memory
git status --short
git diff --check
nix develop
```

Then, inside the development shell, start a **new** Pi session:

```sh
pi --thinking medium @docs/session-capture-handoff.md \
  'Read AGENTS.md and docs/session-capture-contract.md. Implement the replacement described in the handoff, starting with the versioned contract, writer-generated fixtures, and one-session durable capture -> publication -> shared reader -> actual analytics vertical slice. Use medium-effort subagents with disjoint ownership. Preserve existing worktree changes. Continue through the implementation and acceptance phases, updating the handoff at checkpoints. Do not resume the retired recovery patch stack, weaken quality gates, or perform production deployment/R2 deletion before the documented two-machine acceptance and cutover gates.'
```

Equivalent single invocation from the repository root:

```sh
nix develop --command pi --thinking medium @docs/session-capture-handoff.md \
  'Read AGENTS.md and docs/session-capture-contract.md. Implement this handoff from the first unfinished phase using medium-effort subagents. Preserve existing changes and privacy policy. Complete and test the writer/shared-reader/analytics journey before production cutover or R2 deletion. Update this handoff with actual progress and remaining work.'
```

Use one invocation, not both. No specific model/provider is required; use the configured
working model. These commands start implementation, not an unattended production reset.
No attachment, prompt, or old test result authorizes skipping deployment safety gates.

### Read order

1. [Repository guidance](../AGENTS.md).
2. [Agreed contract](session-capture-contract.md).
3. This handoff, especially phases and failure invariants.
4. [Documentation map](README.md), then relevant source modules.
5. [Research/rationale](research/session-capture-contract-review.md) only as needed.

Do not restart broad architecture research or revisit whether to preserve the retired
archive. Resolve the listed concrete implementation parameters, record them, and build.

## 2. Exact current state

### Checkout and preservation

- Repository: `/Users/mm/dev/memory`, branch `main`, base commit **`9078a3e`**.
- The cleanup and this handoff are **uncommitted**. Preserve this working tree; a fresh
  clone of `main` will not contain these additions until they are committed.
- Preexisting user changes include `.gitignore`, intentional deletion of `CLAUDE.md`,
  and untracked `docs/research/` and `tools/analytics/`. Untracked does not mean disposable.
  Analytics files were already user work before the targeted safety edits.
- New guidance: `AGENTS.md`, rewritten `README.md`, `docs/README.md`,
  `docs/session-capture-contract.md`, `docs/research/README.md`, and this handoff.
- Existing documentation now labels legacy/historical material. Source edits in the
  Go collector were comments only, including truthful hashing/debounce descriptions.
- Do not run `git reset --hard`, `git clean`, broad staging, or a cleanup that restores
  `CLAUDE.md`. Review scoped diffs before committing. Do not commit private artifacts.
- If a feature branch is wanted, inspect branches and create one while retaining this
  worktree, for example `git switch -c feat/session-capture-vnext` if that name is free.
  Do not switch to an old repair branch as the implementation base.

Other worktrees exist for old recovery fixes and past release/review tasks. They were
not changed or removed. In particular, an old `memory-wt/HANDOFF-option-a.md` is **not**
the continuation plan. Its cold-recovery/pruned-piece stack is historical.

### Implemented versus not implemented

Implemented during cleanup:

- Canonical direction, documentation routing, historical status labels, and corrected
  misleading source/documentation claims.
- A narrow guard in `tools/analytics/r2_metadata.py`: known `/records/session/.../commits/`
  inventories, including mixed old/new inventories, fail after LIST and before GETs or
  local writes. Unsupported record/payload schemas fail explicitly. Rejection preserves
  existing analytics DB contents, refresh timestamps, caches, and research artifacts.
- Regression tests for this guard and safe diagnostics.

Implemented in the replacement checkpoint (not deployed):

- `internal/publication`: versioned `session-publication/v1`, complete full-object
  manifests/current roots, guarded local CAS, authoritative pinned read/export,
  bounded streaming verification, and explicit local durability fencing.
- `internal/sessioncapture`: authoritative SQLite WAL/FULL queue plus private fsynced
  compressed payloads, retained rewrite epochs, proven append, stable persisted jobs,
  quota/backpressure, ordered delivery, policy rechecks and durable progress/errors.
- `internal/publicationr2`: injected SDK transport and offline native conditional-PUT
  protocol tests only; no credential discovery or production CLI wiring. Actual R2
  endpoint semantics remain **unverified**.
- `agent-sessions capture-v1`: explicit experimental local-only commands and synthetic
  actual-writer fixture. Normal hooks/install/sync remain legacy and unchanged.
- `tools/analytics/publications.py`: separate archive-bound actual importer, normalized
  event index/raw coordinates and content-free report. It uses the Go export, not a
  second storage resolver. Existing research DBs and legacy dashboard are preserved.
- `make capture-acceptance` and CI: actual writer -> durable queue -> local publication
  -> shared reader -> importer -> event index -> report; required by test/check.

**Not implemented/accepted:** autonomous coordinator/discovery/retry, service/hooks,
quarantine inspect/replay, full source/artifact integration and deletion/rename semantics,
remote policy/root recovery, complete dashboard replacement, measured resource/freshness
budgets, full passing release gates, native/live R2 acceptance, two-machine rebuild,
release, cutover, or reset. See [implementation scope](session-capture-implementation.md),
[wire/reader v1](session-publication-v1.md), and [analytics v1](session-analytics-v1.md).
Global source capture I/O is still serialized for peak quota accounting; independent
session delivery is implemented, but fair independent capture is not yet accepted.
Retry timestamps are persisted advisory state; `Deliver` is explicit, not a daemon.

The guard is not a new-format reader or universal future-contract detector. An actually
empty legacy store still permits an empty refresh. Historical local reports remain
available without refreshing or rebuilding them.

### Prior cleanup validation (not the new implementation checkpoint)

- `PYTHONDONTWRITEBYTECODE=1 .analytics-venv/bin/python -m unittest discover -s tools/analytics -v`
  — **31 tests passed**.
- `go test ./internal/syncer -run '^TestHashContent' -count=1` — passed; this focused run
  was not a full race/native/live/release gate.
- Go formatting, `git diff --check`, and 92 local documentation-link targets passed.
- Independent cleanup review found no high/medium issue.
- Handoff sanity review also passed focused race/shuffle local/fake-S3 store tests and
  all nine metadata-importer tests, with no live R2 requests. This does not test CAS
  replacement or the new reader, which do not exist yet.
- No full `make check`, complete Go suite, fuzz campaign, native-agent acceptance, or
  live R2 acceptance was run for the cleanup.
- The verification skill is disabled by the operator's skill toggle. It could not be
  read/updated; do not bypass the block through shell/Git/another agent. Its audit gap
  is recorded in [verification/README.md](verification/README.md).

### Operational diagnosis: evidence, not a live status assertion

Earlier October 23 inspection found deployed **v0.5.0 / `60a9c6a`**, a dirty checkpoint,
last recorded completion October 21 at 19:38 AEDT with an R2 HEAD 403, repeated slow
recovery holding the global lock, and hundreds of pending launches. Prior investigation
identified 819 pruned historical-only pieces for one stream; local metadata independently
corroborated the count. Those findings motivated replacement. PIDs, backlog counts,
credentials, and deployment health must be rechecked before any operational action.

No process was stopped, source data changed, BC-01 inventory verified, release installed,
or R2 object deleted by this design/cleanup work.

## 3. Decisions already made

- Greenfield session-local capture, not an archive migration/compatibility project.
- Preserve useful captured conversation evidence, not every collector prefix, fold,
  compressed layout, or intermediate observation indefinitely.
- Baseline: retain latest complete captured contents per source epoch. Proven append
  advances an epoch; unproven rewrite/truncation retains the old evidence as a distinct
  epoch. Overlaps are explicit; semantic event deduplication is downstream.
- Full compressed epoch objects, reused sealed epochs, complete manifests, guarded
  current roots. Add chunking only if measurements require it, under the same contract.
- Durable local acceptance and remote publication are separate progress coordinates.
  Previous published success survives a failed update; an error does not reset the machine.
- Autonomous retry/discovery, bounded resources, fair session scheduling, explicit
  quarantine/replay, and destination-wide pause for systemic failures.
- **One authoritative reader** for restore and analytics. No independent Python and Go
  implementations of current selection/reconstruction.
- **Actual consumer acceptance**: writer -> queue -> publication -> reader -> metadata/
  body import -> events -> reports. Writer-generated fixtures, not disconnected mocks.
- Operator accepts deleting old R2 data and rebuilding from available local sources on
  this Mac and BC-01. Legacy prefix reconstruction and old-schema compatibility are not
  prerequisites. Current local sources may not contain remote-only old history.
- Preserve identities, local/remote opt-outs, learned roots, original sources, and local
  research. Reset is not revocation of privacy policy.
- Validate in isolation, rebuild/reconcile both machines, fence old writers, then delete
  only the reviewed retired object set. New roots must not depend on retired blobs.

## 4. Implementation map

| Area | Existing code to inspect/reuse | Replacement responsibility |
| --- | --- | --- |
| Identity/config | `internal/config`, `internal/cli/machine.go` | Preserve machine IDs; add explicit new archive binding and validated settings. |
| Discovery/agent formats | `internal/adapters` | Eligible sources, aliases, custom roots, safe SQLite exports, source/artifact ownership. |
| Privacy/filesystem | `internal/capturepolicy`, `internal/exclusion`, `internal/scrub`, `internal/fsutil` | Policy before capture and delivery; private durable storage and safe path handling. |
| Current state | `internal/state` | Existing WAL/`synchronous(NORMAL)` upload cache is not authoritative accepted-byte storage. |
| Current orchestration | `internal/cli/commands.go`, `internal/syncer` | Replace recovery-before-capture/global run flag with durable independent work. |
| Object transport | `internal/store` | Add guarded current-root replacement and conflict tokens; existing `Store` has no replace-CAS or ETag/version field. |
| Hooks/install | `internal/hook`, `internal/install` | Lightweight hints plus supervised autonomous coordinator on Darwin/Linux. |
| Launch/provenance | `internal/launch`, `internal/snapshot`, `internal/gitinfo` | Preserve useful provenance without blocking safe transcript capture on enrichment failure. |
| Reader | Current `internal/syncer/restore.go` and wire validation for reference | New authoritative current-publication resolver; no old graph replay requirement. |
| Analytics | `tools/analytics/{r2_metadata,bodies,events,analyze,serve}.py`, `views.sql`, `report.sql` | Consume resolved publications; archive/source/epoch identities, coherent metadata/bodies, coverage/freshness. |
| Acceptance | `cmd/agent-sessions`, `internal/testenv`, `.github/workflows/ci.yml`, `Makefile` | New real writer/reader/importer journey in release gates. Existing v3 tests are not that journey. |

Reuse tested primitives rather than copying the entire `Syncer`. Package boundaries such
as a new capture/work-store package and a reader package are implementation choices;
these are not existing APIs. Do not assume `internal/launch` is a daemon supervisor.
Current sync staging is explicitly disposable: upload cleanup and `clearStaging` delete
it and rely on recapture. Never relabel that directory as the durable spool without
replacing its ownership/reclamation semantics.

## 5. Phase 1: freeze a small wire/reader interface and executable fixture

**Checkpoint:** the offline slice below is implemented; exact recorded choices are in
[wire/reader v1](session-publication-v1.md). The guidance here remains the plan/acceptance
criteria, not a claim that every release requirement has passed.

**First deliverable:** versioned format documentation plus a tiny actual writer fixture
that a shared reader and Python importer can consume. Do not spend this phase repairing
old remote records or designing a general migration service.

Recommended shape, with names explicitly provisional until recorded in the spec:

```text
<new-prefix>/archive.json
<new-prefix>/machines/<machine>/sessions/<session-id>/current.json
<new-prefix>/machines/<machine>/sessions/<session-id>/manifests/<digest>.json
<new-prefix>/machines/<machine>/sessions/<session-id>/objects/<encoded-digest>.zst
```

Choose a new prefix/contract identifier, not a reinterpretation of `v3/`. Record archive
instance identity, supported version/capabilities, and binding rules. Machine and agent/
root/session identities must be unambiguous and safely encoded; hostname is not identity.

A complete manifest needs:

- Contract/archive identity, machine/agent/session identity, stable publication identity,
  and source-ordering coordinates. Record source-observed, capture, and publication times
  with explicit units; wall clock does not elect the winner.
- For every retained source epoch/artifact: stable source and epoch IDs, role, provenance/
  associations, selected immutable object, encoding, encoded/decoded sizes and checksums,
  and captured boundary/revision. Distinguish local absence from intentional remote deletion.
- Complete dependency closure for that captured view. No predecessor manifest is required
  to read it. Distinguish a completely readable captured view from partial discovery or
  stale/enrichment coverage; never claim unobserved content is current.
- Launch/snapshot references with explicit missing/partial states. Unrelated sessions or
  an unavailable global catalog are not content dependencies.

Use a stable publication ID/body persisted before network delivery so retries do not
create new logical observations. Avoid self-referential hashes in a manifest's identity.
Reader output and API version must be independently explicit, not inferred from a bucket
prefix or outer envelope version.

Implemented consumer boundary: the Go publication library and experimental local
`capture-v1 export` command write a new private directory with bounded streaming body
files and `metadata.json` committed last. Python invokes it without a shell and consumes
`session-reader/v1`, pinned to one publication. See the wire specification for exact
fields and limits. There are no base64 bodies or second Python storage resolver.

Write fixtures through the writer, then require exact reader bytes and actual analytics
rows. Introduce a deterministic fixture generator or test entry point in the build so the
compatibility check cannot be omitted from CI/local release gates.

## 6. Phase 2: durable local capture and publication vertical slice

### Local ownership and crash rules

Choose and document the authoritative store once. A reasonable starting implementation
is a dedicated SQLite work DB for authoritative queue/source metadata, with payloads in
private fsynced spool files. It is **not disposable upload-cache state**. Alternatively,
rebuildable immutable spool envelopes may be authoritative, but their reconstruction must
be implemented/tested; do not claim both approaches without building either.

For the SQLite/payload approach:

1. Safely stage a bounded source observation, compute content identities, and establish
   its source/epoch relationship. Detect unsafe concurrent mutation and retry or report
   ambiguity; file size/mtime alone does not prove append continuity.
2. Write payload to a private temporary spool path, flush/close with checked errors,
   atomically rename, and sync required file/directory boundaries for the stated failure
   model before acknowledging durable ownership.
3. In one short durable DB transaction record the capture/job identity, payload reference,
   epoch/revision, and local progress. Never advance progress before a durable payload
   exists. Specify SQLite WAL/synchronous settings and power-loss assumptions.
4. A crash before DB commit leaves an orphan payload, not acknowledged source progress;
   reconcile/reclaim cautiously. A committed job referencing a missing/corrupt payload is
   an explicit integrity error, not silently completed or reconstructed from a newer path.
5. Persist claim/attempt/next-retry status. Expired in-flight work returns to runnable work.
   A DB/index rebuild and loss of authoritative queue metadata are different failure cases;
   document guarantees rather than deleting the pending store as a recovery tactic.
6. Mark remote completion only after complete publication is established. Reclaim payloads
   only when no accepted pending work/retained local evidence still requires them.

Audit filesystem sync helpers rather than assuming atomic visibility means durable
acceptance: current local `Create` does not sync the destination directory, and
`fsutil.WriteAtomic` ignores directory-sync errors. Handle required sync errors explicitly
in the new durability boundary. Use identity-aware capture-policy resolution, not a
simplified two-path check that can overlook intermediate symlink identities.

Use full objects first; cryptographic prefix checking can reuse existing tested code.
Do not restore the inaccurate assumption that saved hash state currently avoids full
prefix hashing. A path hint is not a payload; queue coalescing must not erase captured
pre-rewrite epochs. A source offset must never advance past an unrepresented gap.

### Remote publication and concurrency

1. Upload immutable objects, then the complete immutable manifest.
2. Conditionally create or replace the small current root against its expected state.
   The R2 docs explicitly describe atomic conditional `PutObject`; test the actual SDK
   and endpoint. Do not infer conditional multipart completion or multi-object transactions.
3. Keep ordering/serialization per session. A stale job must not blindly retry CAS over
   a newer source state. Stable attempt/publication identity handles lost acknowledgements.
4. Resolve ambiguous writes by read-back/guarded retry of that publication, not machine-wide
   history enumeration. A negative read alone does not prove an in-flight PUT cannot commit.
5. Update local published progress and release work only after confirmation. Previous
   roots remain readable through failures; partial uploads remain unselected orphans.
6. The local fake/filesystem store must implement the same conditional conflict semantics,
   not a racy HEAD-then-unconditional-PUT approximation. Existing absent-only `Create`
   is not replace-CAS; R2 `CopySourceIfMatch` guards the copy source, not the current root.
   Add typed precondition/conflict results and opaque version/ETag tokens explicitly.
7. Full-object uploads and reader verification must stream with bounded memory. A full
   snapshot is not permission to load a large database/transcript into RAM; bound parsed
   metadata and validate encoded/decoded lengths and checksums while streaming.

Serialize publication within a session while allowing independent sessions bounded
concurrency. No SQLite transaction spans network operations. One machine coordinator
ownership lock is fine; hooks must not queue behind a historical network scan.

## 7. Phase 3: coordinator, source semantics, and operations

Implement a supervised user service on macOS and Linux, with an explicit lifecycle and
autonomous periodic discovery/retry. Integrate install/uninstall/dry-run, service restart,
hook/extension reload, and diagnostic reporting. Do not assume detached per-hook syncs
or stale marker expiry provide scheduling guarantees.

Required semantics:

- Distinct default and learned custom roots; retain source identity across supported
  aliases/renames. Handle truncation, same-size rewrite, regrowth, partial tails, and
  replacements. Unknown rewrite starts a retained epoch, not machine-wide reset.
- Source-specific safe SQLite backups and associated artifact mapping. Define agent-wide
  resources separately from a session if appropriate; unrelated shared-resource errors
  cannot become an implicit global barrier.
- Partial discovery does not imply deletion. Raw malformed JSONL need not stop safe raw
  capture; incomplete final writes wait or remain explicitly partial. Sensitive malformed
  configuration remains withheld until scrubbing/policy is safe.
- Fair work budgets, bounded workers, byte-based spool quota/reserved free space, and
  separately bounded remote operations. At capacity stop accepting/advancing capture,
  alert, and report unread-source retention risk; never silently evict accepted bytes.
- Transport/throttle failures retry with capped backoff and jitter. Invalid credentials
  pause a destination and alert; they are not poison data. Poison work is durably
  quarantined with inspect/replay. Retry count exhaustion does not delete evidence.
- Source/content/publish/enrichment/import errors and watermarks remain separate. Expose
  counts and oldest age, paused reason, current stage/elapsed time, last successful
  publication, retry time, coverage gaps, and safe diagnostics without transcript dumps.
- Policy applies at capture and before delivery, including opt-outs added after queueing.
  Do not upload accepted old jobs merely because they were accepted before exclusion.

## 8. Phase 4: analytics is a real consumer, not an independent archive interpreter

Replace the metadata/body import path together. Do not just broaden the old inventory
filter or accept payload schema 4/5. Retired manifests copied fields that newer manifests
reference indirectly, and the replacement is deliberately a different contract.

- Consume the authoritative reader's versioned resolved output. Pin metadata/body work
  to the same archive and publication identity, including when new capture races import.
- Store archive instance, machine, agent, session, source, epoch, and selected publication.
  Invalidate/rebuild caches on archive reset; do not reuse old 'complete' flags.
- Logical trace/source identity includes epoch. Event identity must not collapse repeated
  identical content or create duplicates on idempotent reimport. Use native IDs where
  reliable and retain raw coordinates; overlapping epochs are not automatically merged.
- Preserve raw malformed events for trace access independently of semantic normalization.
  Model/timestamp/role/provenance fields must reflect explicit supported semantics.
- Import successful sessions independently while retaining per-session failures. An
  unsupported archive contract is an explicit global import failure, not an empty success.
  Distinguish an actually empty archive, partial discovery, unsupported data, and stale data.
- Reports expose captured/published versus imported/normalized/served freshness and coverage.
  Broken A does not hide healthy B or relabel A's old success as current.
- Preserve existing local research, reviewed findings, feedback, and citations. Explicitly
  retain or retire old-archive references; never silently attach them to rebuilt publications.
- Integrate the writer-generated fixture journey into both `Makefile` and CI. A command
  that only runs legacy Python unit tests is not the new compatibility gate.

No classifier rerun or external submission of private transcripts is required for storage/
consumer acceptance. Do not add such a dependency to this work.

## 9. Acceptance and performance: required before rollout

Add failure-injection tests around every durable boundary and the real consumer:

| Scenario | Required result |
| --- | --- |
| Crash before/after payload and job commit | No acknowledged bytes lost silently; explicit orphan/integrity reconciliation. |
| Crash after upload/root publication before local ack | Retry is idempotent and does not regress current state. |
| Lost PUT acknowledgement, stale CAS, concurrent first create | Correct single selected publication; conflict/outcome reconciled without global replay. |
| Broken A, healthy B, restart without another hook | B captures/restores/imports; A retains visible retry/quarantine state. |
| Failed update | Prior publication remains readable and is labeled with its true age. |
| Append, rewrite, rotation, truncation, compaction | Correct epoch/content policy and exact-byte restoration. |
| Unsupported format or missing current dependency | Explicit error/coverage gap, never successful-empty refresh. |
| Repeated import and overlapping epochs | Stable rows/trace identities; no silent collision, invented merge, or duplication. |
| Full spool, outage, revoked access, new opt-out | Honest backpressure/pause, no silent eviction or forbidden upload. |
| Expired old intermediate representations | Current read and analytics need only current retained roots/dependencies. |

Measure real session/turn size distributions, append traffic, request counts, bytes sent,
local bytes read/written, CPU/heap, queue age, recovery cost, and concurrent fairness. Prior
inspection found one approximately 79 MB transcript and a roughly 615 MB SQLite export;
full-object bandwidth must be measured, not assumed cheap. If chunking is justified, keep
it below the same manifest/reader contract; do not revive the old fold/replay protocol.

Existing historical benchmark fixtures are not sufficient proof of the new failure path.
Set concrete resource/freshness budgets and test deterministic bounds, not only timings.

### Commands available today

Run these serially; do not overlap full tests, benchmarks, or live operations. Disable
live opt-ins in ordinary regression runs:

```sh
# Focused baseline; this does not test the replacement.
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
  CGO_ENABLED=1 go test -race -shuffle=on ./internal/syncer -run '^TestHashContent' -count=1

# Existing offline Python regression suite, when this local venv exists.
PYTHONDONTWRITEBYTECODE=1 .analytics-venv/bin/python \
  -m unittest discover -s tools/analytics -v

# Full existing gates after meaningful implementation slices, not as proof the new
# consumer journey exists. Add and run that journey as part of implementation.
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE make test
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE make check
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE make fuzz FUZZTIME=30s

git diff --check
```

If the Python environment is absent, use the existing pinned requirements after reviewing
them (dependency installation requires network; it is not R2 ingestion):

```sh
uv venv --python python3 .analytics-venv
uv pip install --python .analytics-venv/bin/python -r tools/analytics/requirements.txt
```

Do not raise timeouts, remove race coverage, weaken assertions, change thresholds, or call
an extended-time diagnostic run a passing release gate. Earlier v3 syncer gates exhausted
the ten-minute package budget, including on main; the suspected sixfold candidate slowdown
was not established. Profile/remove unnecessary test work without weakening behavior if
that blocker recurs. Record exact commands, revisions, runtime and pass/fail status.

Native sandbox and live-R2 journeys are separate explicit phases, using isolated homes/
owned key namespaces and cleanup verification. Existing legacy live tests need replacement
contract coverage; merely rerunning them is not sufficient. Do not enable their opt-ins
as an incidental smoke test.

## 10. Phase 5: inventory, two-machine rebuild, release/cutover, retirement

No destructive command is provided here because the exact namespace, destination, new
version, and verified deletion manifest do not yet exist. Implement/verify those first;
do not paste a whole-bucket deletion command to 'start clean'.

1. **Inventory sources and policy on both machines.** Verify actual access/paths; BC-01's
   current source coverage has not been checked in this work. Preserve machine IDs,
   config overrides, effective local/remote exclusions, learned roots, original sources,
   and pending unique evidence. Export roots/policy before discarding old local projections:
   learned roots can exist in both SQLite `roots` and legacy remote root records, not only
   default directories. Remote policy/root extraction must not depend on reconstructing
   old content histories.
2. **State the rebuild scope/loss boundary.** Eligible local sessions/artifacts are the
   rebuild dataset. Remote-only old versions/provenance may be lost under the operator's
   authorization; an honest report is enough, not an exhaustive legacy recovery project.
3. **Validate the full candidate in a fresh isolated namespace.** Fresh archive identity
   and state binding, reused distinct machine IDs, restored policy/roots. All dependencies
   belong to new data; no reference to a retired shared blob is permitted.
4. **Rebuild/catch up both machines.** Use the normal durable queue with resumable work,
   not a separate untested upload bypass. Reconcile every eligible source to publication/
   import or an explicitly classified gap. Verify bodies/checksums, identities, provenance,
   repeat-import idempotency, report freshness, and independent session progress.
5. **Release and fence old writers.** Follow current immutable-release verification and
   installation mechanics where still applicable, with the new service integration. Use
   traceable source and assets. Inspect current CI availability rather than assuming old
   billing restrictions still hold. Stop/replace hooks, detached jobs, and any services on
   both machines; open Pi processes may need reload/new sessions and Codex trust may need
   operator approval. Verify no stale binary can write the retired root.
6. **Prove ordinary operation after cutover.** Automatic hook capture, timer discovery,
   service restart, retries without another hook, reader restore, and analytics updates
   work on both machines. Queue drains within budget; degraded work remains explicit.
7. **Retire the old archive.** Review exact destination and retired keys/prefixes, including
   old shared objects, excluding new roots/data, preserved policy, and unrelated objects.
   Delete only that set, verify resulting inventory, and recheck new reads/imports. A
   temporary staging period is not a requirement to retain the legacy archive permanently.

Update README, contract, diagnostics, install and security docs to describe actual shipped
behavior only once implemented. Remove legacy runtime modules/obsolete fixture wiring
when their callers are replaced; retain relevant safety coverage and labeled historical
evidence. Do not leave two live competing publication authorities.

## 11. Parameters to settle immediately, not reasons to reopen the architecture

Record values and tests in phase 1:

- Exact new prefix/archive descriptor/manifest/reader-interface versions.
- Source/artifact ownership, source epoch identity, and precise ordering/conflict rules.
- Chosen authoritative pending-store model, sync settings, local crash/power-loss scope,
  and orphan/payload reclamation protocol.
- Publication/discovery freshness, worker fairness, request budgets/backoff, spool bytes,
  reserve free space, offline retention assumptions, and operational alerts.
- Explicit provenance completeness and analytics coverage statuses.
- Epoch retention/deletion policy; begin conservatively with no automatic remote GC.
- Go reader/Python interface and reproducible CI fixture entry point.

Use the agreed baseline rather than inventing a generic workflow platform, distributed
consensus service, universal semantic event log, or a historical migration engine.

## 12. Progress ledger for the next session

Update this ledger and the exact next command before ending another session:

| Slice | Status at handoff | Completion evidence required |
| --- | --- | --- |
| Design/cleanup and legacy importer guard | Done, uncommitted | Existing scoped tests above; not replacement acceptance. |
| Wire/reader interface + writer-generated fixture | Implemented, offline focused checks pass | `session-publication/v1` / `session-reader/v1`, `capture-v1`; actual fixture gate below. |
| Durable one-session publication/read/import slice | Implemented offline candidate | Exact bytes/roles/epochs/report; restart, faults, lost ack, quota, exclusion and broken-A/healthy-B tests. Not autonomous or production-accepted. |
| Coordinator, epochs, service/hooks, status/replay | Offline candidate implemented; artifact/lifecycle integration continues | Concurrent exact quota, durable autonomous scheduling/inboxes, scoped discovery coverage, pause/quarantine/replay, safe derived sources and explicit native/service candidates exist. Independent reviews and process journeys are below; fresh full gates and complete artifact/native acceptance remain required. |
| Analytics/report replacement + CI gate | Separate new consumer/report + gate implemented | Writer-generated pinned input, epoch identities, coverage/freshness and idempotence pass. Legacy dashboard/caller replacement remains. |
| Full/native/live acceptance and budgets | Earlier development gates pass; phase-3 rerun pending | Earlier `make test` / `make check`: 89.0% merged coverage, CRAP below 30. New phase-3 code needs fresh full gates. Native/live, actual R2, budgets and two-machine acceptance not run. |
| Mac + BC-01 inventory/rebuild/cutover | Not started | Reconciled sources, policy, automatic capture, restore/import on both. |
| Reviewed retired archive deletion | Not started | Exact approved inventory, deletion result, new data still readable. |

### Initial offline-slice evidence (2026-10-23, before runtime repairs)

Uncommitted on base `9078a3e`. The failures below are retained historical evidence;
the runtime/quality follow-up records the final passing development gates.

Medium-effort implementation subagents had disjoint publication, work-store, analytics
and SDK transport ownership; independent medium-effort read-only reviews covered
storage/durability and CLI/analytics. Review findings were fixed with regressions:
historical policy-identity loss on append, pre-CAS policy changes, unavailable local
policy treated as empty, sync failures hidden by readback, accepted invalid timestamps,
and old-success reimports erasing newer consumer failures. Parent also rejected lossy
UTF-8 wire identities, bounded ordering to signed int64, and kept unknown publication
commit time as zero rather than fabricating it from capture time.

Commands used the pinned Nix environment and unset both live opt-ins:

- `make capture-acceptance`: **3 tests pass**, actual writer/read/import/report; exact
  retained bodies and source roles, overlapping epochs, malformed/partial tail,
  repeat import, source/spool-independent restore, unsupported contract, and broken A
  with healthy B captured/delivered in new processes after source deletion.
- `CGO_ENABLED=1 go test -race -shuffle=on -cover ./internal/publication
  ./internal/publicationr2 ./internal/sessioncapture ./internal/cli -count=1`: passed
  after final transport immutability follow-up; coverage respectively **78.9%, 93.1%,
  81.0%, 79.5%**, package times **5.041s, 2.352s, 8.484s, 10.617s**. Focused coverage
  is not the full merged gate. SDK immutable writes now validate content-addressed
  keys before networking and use conditional create with exact streamed collision/
  lost-ack reconciliation, never unconditional overwrite.
- `make fmt-check lint fix-check tidy-check deadcode actionlint`: passed, lint zero.
- `git diff --check` and **57 local documentation-link targets**: passed. Preexisting
  worktree changes and the intentional `CLAUDE.md` deletion remain preserved.
- `make fuzz FUZZTIME=30s`: **passed all 9 existing fuzz targets**, each at its unchanged
  30-second budget, serially after focused tests. This is not new native/live evidence.
- `PYTHONDONTWRITEBYTECODE=1 .analytics-venv/bin/python -m unittest discover
  -s tools/analytics -v`: **49 discovered, 46 passed, 3 skipped** only because this
  generic discovery does not provide a candidate binary. The mandatory fixture gate
  separately ran and passed all 3 writer journeys; it cannot skip them.
- `make test`: **failed**, legacy `internal/syncer` timed out at **600.649s** with
  shuffle seed `1792737262050729000`, during
  `TestAVersionAfterALostWholeUploadKeepsOnlyTheRightBytes/foldLanded`. Other packages
  passed. No timeout, assertion, race setting, or coverage gate was relaxed.
- `make check COVER_DIR=<isolated-temp>/coverage`: **failed**, again at the unchanged
  ten-minute syncer limit (**601.250s**, seed `1792738498445425000`, simulated-session
  children active). Format/lint/fix/tidy/deadcode and vuln passed first. Full merged
  coverage/CRAP and later check prerequisites did **not** complete. Existing `cover.out`
  was preserved/restored and scratch coverage removed; no research artifacts touched.

### Runtime/quality follow-up (2026-10-23, still uncommitted)

Medium-effort subagents investigated production runtime, fixture costs, and the
subprocess/package-timeout harness with disjoint ownership. Serial profiles established
cumulative SQLite/WAL, descriptor, and listing amplification rather than a demonstrated
hang in the last-named timeout test. The first repair still left `make check` timing
out; further bounded metadata/tip batching was required. Independent review caught and
regressed an initial SQL sorter/body-budget defect. See
[gate-runtime diagnosis](quality-gate-runtime.md) for the chronology, implementations,
measurements, and unchanged invariants.

Implemented maintenance: coherent current-chain selection and covering index,
metadata-only bounded LIST-page reads, batched scan markers, applied flags in replay
cursors, bounded fresh tombstone tip pages, exact-wire decoder fast path, and bounded
local LIST heap/prefix pruning. No old recovery architecture or new retired-schema
support was introduced. Manifest validation was separated into coherent checks;
meaningful durable-store and CLI failure/progress tests resolve downstream CRAP
blockers without changing thresholds or hiding functions.

Final checks, using pinned Nix and both live opt-ins unset:

- `make test`: **PASS**, **409s** wall; syncer **384.263s**, full race/shuffle suite and
  **3/3** actual writer/reader/import/report journeys.
- `make check COVER_DIR=<isolated-temp>/coverage`: **PASS**, **621s** wall including
  static checks and all benchmark smoke cases; atomic race/coverage syncer tests
  **465.828s**, within the unchanged **600s per-package** timeout.
- Merged coverage **89.0%** (**7,184 / 8,071 statements**), passing unchanged package
  **60%** and total **75%** thresholds; full CRAP scan passes unchanged **30**, highest
  **29.36**. All benchmarks smoke-pass; syncer benchmark package **105.147s**.
- Format/lint/fix/tidy/deadcode/vulnerability/actionlint pass; **zero lint issues** and
  **no known vulnerabilities**. Focused race/shuffle regressions also pass.
- `git diff --check` and **42 local documentation-link targets** pass after the
  runtime/handoff update.
- Existing `cover.out` restored byte-for-byte; final merged coverage retained only in
  temporary diagnostics. No research evidence was changed. All eight simulations and
  forty steps, real 64-piece fixtures, assertions, timeouts, and subprocess coverage
  accounting remain intact. No new fuzz/native/live claim is implied by these checks.

This unblocked development, not production release. Those maintenance changes alone
did not implement independent capture/quota reservation or autonomous scheduling.
The subsequent phase-3 work below now implements that candidate; phase 3 as a whole
is still not release-accepted.

No native hooks, actual R2, two-machine access/inventory, install, live ingestion,
production deployment, original-source deletion, remote cleanup or R2 deletion was performed.
Fixture deletion tests delete only temporary synthetic test sources/objects.

### Phase-3 continuation (2026-10-23, uncommitted candidate)

Medium-effort agents used disjoint quota/capture, coordinator, source discovery,
derived preparation, service/native hooks and CLI ownership. Independent audits
fixed concrete defects rather than relaxing tests. See the
[phase-3 notes](session-capture-phase3.md) for resource bounds and scope.

Implemented:

- Concurrent per-session capture, exact encoded reservations including temporary
  duplicates/orphans, context-aware session/hash waits, and exact unchanged-source
  checking at full quota. Filesystem metadata fencing avoids internal rename races
  while external missing evidence fails closed.
- Autonomous bounded source/delivery scheduling, versioned claims, persisted retry,
  jitter/budgets, destination pause, quarantine/replay, actual byte watermarks,
  diagnostic software versions, read-only live inspection, and restart delivery.
- Independent durable pathname inbox plus typed discovery-batch coverage. Sweep
  generations preserve interrupted/incremental gaps; invalid metadata/admission
  prevents false resolution. Invalid inbox entries remain retained and counted.
- Metadata-only source discovery with full-root-qualified identities, longest-root
  ownership, global 4096-entry / 256-directory-FD limits, and shared single-path
  hook classification. No environment-derived test roots or semantic capture gate.
- Queue-owned zstd decoding, coherent pinned WAL-aware SQLite backup, successful
  protected-config scrubbing, and exact standalone default `~/.claude.json` export.
  Original paths and explicit transform associations are preserved. Four prepared
  1-GiB slots are a **separate 4-GiB working bound**; they are not encoded quota or
  a 256-MiB memory claim. Parent integrated `checkWorkingReserveLocked` into encoded
  `reserve` so preparations and writes share conservative free-space reservations.
- Bounded durable Claude external-artifact ownership metadata and source classification;
  duplicate UUID ownership remains ambiguous. Saturation cannot leave an old unique
  owner valid after a rejected conflicting observation. Raw transcript work does
  not depend on successful enrichment ownership.
- Strict private local-only runtime configuration, service plans/lifecycle with
  injected runners, native metadata-hook command and opt-in owned hook planning.
  Actual native agent trust/installed-path acceptance is not implied.
- Opaque artifacts remain verified/owned restorable bodies with explicit
  `artifact_not_normalized`, not transcript-parser input or invented events.

Independent findings fixed with regressions include quota/temporary scan races,
uncancellable hash waits, malformed-head global worker failure, unchanged observations
faking capture freshness, stale absence erasing new work, false discovery-gap clearing,
shutdown hiding joined failures, root-wide directory opt-out suppressing healthy B,
and SQLite aggregate privacy bypass. Parent additionally preserved pure callback
cancellation rather than permanently classifying it as an exclusion.

Intermediate checks (not final full gates):

- Quota audit race/shuffle repetitions and whole package passed; runtime/source/CLI
  audits report zero scoped lint issues and package-CWD CRAP below unchanged 30.
- Latest combined focused race/shuffle/coverage run: CLI **81.0% / 11.196s**, daemon
  **85.1% / 1.475s**, native-hook package **90.3% / 2.527s**, discovery **87.4% /
  2.387s**, capture **84.3% / 24.608s**. Its following lint found two new CLI cognitive
  backstops; parent restructured command routing and native decoder boundaries and
  must rerun. Log `/tmp/memory-phase3-integrated.log`.
- Initial `make capture-acceptance`: **5/5 actual-process journeys PASS**, **4.652s**, including
  periodic healthy-B capture while A quarantines, restart/replay without another
  event/source, zstd subagent, scrubbed settings/global config, coherent WAL evidence,
  deletion of only synthetic source/spool, independent restore and actual analytics
  import/event index/report. `/tmp/memory-phase3-derived-acceptance.log`.
- Python projection tests **16/16 PASS**, including binary artifact ownership without
  transcript parsing. `/tmp/memory-phase3-acceptance.log`.

The sixth owned-native-command/wake/indexed-artifact journey exposed two real bugs:
filesystem component overflow from full hex session identities, and an unrecognized
native-installer backup suffix permitting raw protected-config classification.
Both were fixed with regressions; an independent physical/logical key confinement
review passed. No wire identity was shortened. Long local keys use disjoint hashed
physical paths; R2's logical 1024-byte limit is unchanged. Backups use the existing
recognized suffix; retired unsafe candidate backup names are denied, not renamed or
silently captured raw.

The current versioned reader/publication candidate is **v2**, with explicit optional
artifact withholding and redacted public coordinates. Required evidence cannot be
demoted; selected dependency closure and source-epoch regression checks remain.
The separate Python consumer requires v2 and refuses old projection DBs read-only,
even with reset. See the [wire reference](session-publication-v1.md) and
[independent enrichment refinement](session-enrichment-contract.md).

After that integration, **6/6 actual-process journeys PASS**, **7.015s**:
`/tmp/memory-wire2-baseline-acceptance.log`. Exact installed synthetic Claude/Codex
commands, pathless wake before the 30-second timer, indexed external artifact bytes,
shared reader, import/report and uninstall preservation are now exercised. These six
journeys run with enrichment **off**, not proof of the new enrichment lane.
Projection unit discovery: **58 total, 52 pass, 6 actual-binary skips** without the
candidate variable; all **21 projection tests pass**.

Full `make test`, isolated `make check`, full analytics with actual binary, and fuzz
remain to be rerun. Earlier 89.0%/CRAP/full-gate results are historical, not
fresh phase-3 acceptance. The existing `cover.out` has not been intentionally changed
by focused profiles; preserve/restore it for full merged coverage.

No real-source scans, installs, real launchctl/systemctl, live ingestion, production
R2 access, remote cleanup, source deletion outside synthetic fixtures, deployment,
cutover or archive reset occurred.

### Enrichment integration underway (not acceptance)

Disjoint medium-effort owners added a queue-native enrichment lane, native launch
observations and context/policy-aware self-contained snapshot bundles. Native stdin
is allowlisted and bounded, argv/environment are sanitized again at durable storage,
missing transcripts may still have metadata accepted, and Pi uses SDK-observed
context rather than the collector's argv. Disabled known-session hooks persist legacy
exclusions. Unresolved disabled identity creates a durable agent-scoped policy barrier;
enabled hooks never clear it. Parent wired its effective policy check; operator
status/nonce-guarded clearing still needs completion.

Snapshot owner reports focused race/shuffle/lint/scoped CRAP passes, **81.1%** merged
focused coverage, original 64-MiB selected-byte gates preserved, and additional
2-MiB evidence / 8-MiB metadata / 128-MiB TAR limits. Git uses a private authorized
configuration-free copy; independent review of selection/scratch-space costs is
ongoing. Parent added explicit `enrichment` opt-in and bounded extra
`enrichment_roots` for trusted selectors only, not source discovery. Candidate Home,
source root and native CWD are scoped; arbitrary host managed/ancestor bytes must not
enter a synthetic fixture merely because global default paths exist.

Queue core compiles and has intermediate race/shuffle/lint passes, but is **unfinished**:
status/replay, normalized evidence/aggregate metadata budgets, stable no-op/basis
handling, no optional I/O under raw acceptance, and comprehensive isolation/crash/
quota tests are being finalized. A new independent audit specifically checks remote
optional upload/verification failures cannot poison a same-session raw publication.
Do not equate independent materialization alone with that stronger property.

Marking owned artifacts optional exposed existing
`TestRuntimeIndexedExternalPrivacySurvivesOwnerDeletion`: a whole-session opt-out
must still block that scope even if its only captured source is optional. Fixed with
`ErrSessionExcluded`, private-evidence scope rechecks, and the independently derived
CLI exclusion callback. The original assertion is preserved. Unresolved-agent policy
barriers are also scope-fatal, not permission to publish withheld-only scope metadata.
Known SSH/cloud/credential directories and extensionless private keys are now denied
before snapshot imports/custom-root reads; alias regressions cover resolved identities.

Latest parent integration: **7/7 actual-process journeys PASS**, **6.813s**, including
native launch plus a self-contained project snapshot, token/prompt exclusion, deletion
of synthetic originals/Home/spool, independent authoritative restore, exact main bytes,
idempotent importer and actual partial-normalization report.
`/tmp/memory-enrichment-cli-integration-4.log`. Focused parent CLI/daemon/source/policy
race/shuffle passed (**81.9 / 85.9 / 86.9 / 98.4%**), lint zero. A subsequent genuine
runtime-enabled lifecycle/status test raised CLI coverage to **84.4% / 14.872s**;
CLI scoped CRAP now passes (maximum **25.48**), lint zero:
`/tmp/memory-enrichment-cli-final.log`. Explicit operator-only nonce-guarded policy
barrier inspection/clear and enrichment status/replay routes exist. No enabled hook
or destination resume silently clears effective exclusions.

Independent audit `19e80569-d04f-472` identified additional **real blockers**, now
assigned for implementation, not labelled human gates:

1. Network delivery still held the same-session raw mutation lock; optional stalls
   could block raw acceptance despite independent snapshot materialization.
2. Remote optional upload/verification/fence failures lacked source attribution and
   could quarantine the composite head, blocking later safe main publication.
3. The private Git view copied unrelated repository bodies/full object storage and
   used default temporary storage outside queue reservation. Declared metadata-only
   selection and queue-owned working scratch must replace that broad clone.

The first remote-repair checkpoint passed focused tests
(`/tmp/memory-dependency-gates.log`: publication **85.4% / 8.647s**, queue
**81.6% / 37.522s**, lint zero, scoped CRAP at/below 30), including native poison-page
rotation and history-quota terminal fallback. However, independent review
`0838e3ed-d128-42f` is **FAIL**: the delivery-only lock still prevents newer same-session
main **publication** before an optional stall releases; object-attributed cancellation /
durability could be downgraded; repair completion precedes crash-safe reattachment;
repair records/history evade metadata budgets. Those four defects now have an
implemented repair checkpoint: raw/optional delivery lanes and bounded optional
worker, durable in-flight selection suppression, supersession-before-CAS/ACK guards,
error-tree guards, `verified_attachment_pending` autonomous restart, and explicit
repair caps (4,096 rows / 32,768 aggregate histories / 128 histories per repair,
all byte-charged). Quota admission uses retained-job `OptionalRepairGap`, not fatal Run
or a false composite ACK. Replay admits retained work before clearing that gap.
Strengthened channel tests require authoritative newest-main restoration plus safe-job
published ACK **before** releasing optional Put/Open/object-fence channels. Checked
prepared-slot cleanup now propagates failure and retains orphan charge across restart.
Queue/publication final pre-alias checks **71.963s / 81.6%** and **7.809s / 85.4%**,
lint zero, scoped CRAP pass (`/tmp/memory-completion-*`). Fresh independent review
`6d529c8b-708d-47c` is pending; these focused successes do not replace it.

The disjoint snapshot repair is implemented: authorized Git index/ref/ignore metadata
and filename placeholders replace broad repository/object copying; every scratch path
requires the queue-owned private `WorkingDirectory`, without system-temp fallback.
Unknown package dirty state is explicit partial/`null`, not fabricated clean. Focused
race/shuffle snapshot **41.709s**, adapters **4.065s**, lint zero; owned-function CRAP
below 30 (`/tmp/snapshot-final2.log`, `/tmp/snapshot-owned-final.cover`). Independent
review `c9633944-5650-4fb` then found ancestor-symlink authorization races, unrelated
ignore denial erasing healthy selection, unbounded WalkDir enumeration and swallowed
queue cleanup. These now have actual fixes/regressions: descriptor-relative
`safefile.OpenResolved` across every canonical component with pre-authorization file
identity; affected-subtree pruning with explicit partial coverage; 128-entry bounded
walk batches; and queue cleanup-error joining/orphan retention. Snapshot **46.521s**,
adapters **5.183s**, safefile **1.285s**, lint zero, owned CRAP max **26.89**:
`/tmp/snapshot-review-final2.log`. Whole adapters CRAP needs full coverage, not this
focused profile. Fresh independent alias/snapshot review is pending.

Additional explicit integration is now implemented, with offline evidence only:

- `internal/capturebackend` constructs the actual SDK backend from a strict exclusive
  `backend` configuration. Store-only local configs remain valid. Credentials are
  named environment references, resolved only for explicit remote operations; no
  default AWS chain or legacy private config reads. Nonnetwork injected HTTPS peers
  cover conditional headers, opaque ETags, lost ACK, auth, exact writer/reader bytes.
  Backend coverage **100.0%**, daemon **87.6%**, lint zero, scoped CRAP **26.00 / 23.14**.
- Parent CLI uses the factory for explicit config-backed init/export and runtime
  delivery. Remote startup is lazy: absent credentials/remote availability cannot
  prevent local raw capture. Archive binding is checked before namespace reads or
  uploads; auth becomes destination pause, not optional poison. Local startup retains
  archive-mismatch-before-queue behavior. Focused CLI race/shuffle **2.783s** proves
  missing-credential startup still captures and records a destination pause.
- Actual SDK-factory fixture → writer → authoritative reader → importer/event index /
  report **PASS** after the fixture deletes only its synthetic originals and spool.
  `/tmp/memory-sdk-consumer-process.log`: configured-reader helper tests plus the new
  actual SDK journey, **3 tests / 1.245s**. No TCP/DNS or real credential lookup occurs.
- Existing dashboard/query entry points now have explicit `--source publications`
  mode, dedicated projection default, read-only source/epoch navigation, verified
  selected-body raw offsets, and explicit unavailable legacy classification/review.
  Existing legacy defaults/history remain untouched. New actual-process dashboard
  journey removes the entire synthetic source/store/spool/export tree first, then
  serves only owned projection evidence. Exact events/epochs/Unicode/malformed lines,
  security headers and cross-origin denial pass with eight adapter/server tests:
  `/tmp/memory-dashboard-caller-process.log`, **9 tests / 5.664s**.

A fresh combined run now passes **10/10 actual-process journeys, 11.140s**:
`/tmp/memory-ten-process-journeys.log`. This includes SDK factory, existing dashboard,
and a declared-alias root move with an absent logical native pathname, available
physical opening, stable source/append epoch, retained rewrite epoch, exact body hashes,
source/spool-independent restore and actual idempotent event import. Complete analytics
discovery with the actual candidate passes **72 tests / 22.605s**, no skips:
`/tmp/memory-current-analytics-discovery.log`. A synthetic SQLite readback fixture
connection warning was subsequently fixed with explicit closure.

Declared aliases now have a pure immutable compiler (98.3%, lint/CRAP pass), source
shared discovery/classify/native integration (**81.9% / 2.607s**), strict typed runtime
configuration and shared all-identity policy callback, legacy-root opt-out checks even
when that old physical root is absent, and queue-owned coordinate/provenance validation.
The logical root namespace and adapter source/session identities remain unchanged;
actual read paths are private evidence and publication paths are canonical logical
coordinates. Pi declared-layout classification no longer depends on whether its old
custom root exists. Exact leaf routes never authorize parent enumeration.
`Options.Coordinates` is authority; supplied `LogicalPath` must match and supplied
`Identities` are restrictive privacy evidence only. Limits: 128 supplied identities /
64 KiB, 4,096 bytes per path; derived/retained policy evidence 32,768 identities / 2 MiB.
Source sends only observed/logical/original-hint provenance; queue derives all declared
spellings itself. Ownership stores a logical main path so two physical aliases do not
create false conflict. Final alias queue race/shuffle **69.576s / 82.3%**, publication
**8.409s / 85.4%**, lint/CRAP pass (`/tmp/memory-alias-q-*`).

Parent also wired live read-only `optional-status`, stopped `optional-replay` and
exact agent/session `optional-quota-replay`. A genuine unknown-quota-replay success
was found by a new CLI assertion and fixed with a nonzero matching-view guard; final
integrated CLI checks are pending. A separate actual-process optional failure/crash /
restart/repair/revocation fixture is active work, not yet accepted. It temporarily
made the CLI uncompilable while its new helper files were being authored; do not
mistake that intermediate command for the final package result.

Fresh independent reviews have now returned additional concrete blockers:

- `95ffa0fd-744f-47f`: snapshot's descriptor-bound read repair is present, but primary
  raw/unchanged/transformed queue source reads still use symlink-following opens after
  authorization. A parent/leaf redirect followed by restoration can capture forbidden
  bytes under safe retained identities. Owner `d68f2ea6-85da-4be` is binding all queue
  inputs to pre-authorization identity and all-components no-follow descriptors;
  SQLite WAL coherence and sidecars must not be weakened or replaced by a raw fallback.
- `6d529c8b-708d-47c`: the reviewed delivery independence, crash-safe attachment,
  quota/fallback, cleanup and CLI backend implementations pass code/test review, but
  `runtimeDeliveryFailure` / `saveOptionalRepairFailure` discard genuine fatal causes
  after successful bookkeeping. Owner `b02239f3-6fae-4d7` is preserving durability /
  local-store failures through `Run`, including joined cancellation, without making
  ordinary autonomous network retries fatal.

Both findings are code work, not human escalation or merely absent live evidence.
Further concrete checkpoints:

- The three-stage actual-process isolation fixture is implemented and routed as
  `capture-v1 isolation-fixture --output ROOT --stage crash|repair|revoke`, synthetic
  owned roots only. It proves newest-main reader bytes and local published ACK while
  optional repair remains blocked, intentionally exits without queue close/releasing
  the channel, reclaims durable repair after a new process starts with originals
  removed, preserves artifact object/time, then withholds the revoked optional body
  while publishing newer healthy main. Python writer → reader → importer → report
  and retained-cache assertions pass **1 test / 1.194s**:
  `/tmp/memory-isolation-process-consumer-final.log`. This is an eleventh journey,
  **not yet a fresh combined eleven-test run**.
- Raw/unchanged/zstd/config capture now uses pre-callback file identity plus
  `safefile.OpenResolved` and has 12 synchronized parent/leaf redirect regressions,
  including restore-before-acceptance and actual safe-body export. Queue race/shuffle
  excluding separately owned `TestRuntimeFatal*` passes **69.710s / 82.6%**, lint zero,
  scoped CRAP max **27.15**: `/tmp/memory-authorized-owned.log`,
  `/tmp/memory-authorized-owned.cover`, `/tmp/memory-authorized-crap.log`.
  **SQLite is explicitly not fixed by that checkpoint.** Its old pathname/VFS input
  remains a blocker. A negative pinned readonly-FS/immutable-VFS capability regression
  proves backup can return success while silently omitting uncheckpointed WAL rows;
  an exclusive-locking experiment exposed incorrect upstream readonly flags and a
  nil-sync close panic. Dedicated readonly WAL snapshot VFS work is active; do not
  integrate the negative prototype as a functioning reader or drop WAL/sidecar gates.
- Runtime fatal-cause propagation is implemented with targeted Run tests but final
  control/race checks remain active. The ordinary-retry control must require raw ACK
  before releasing optional upload, not race an immediate successful reattachment.
- A further read-only privacy audit found that healthy path A can be claimed under a
  known opted-out root-qualified session B. Path-owner checks alone do not deny the
  identity claim. A restrictive-only registry/declared-coordinate check is active
  work. It must not globally match UUID suffixes, require classifications as raw read
  authority, or turn directory metadata into an opted-out aggregate.
- Parent CLI race/shuffle passes **26.419s / 81.0%** and scoped lint zero after genuine
  helper extraction/formatting. Scoped CRAP still has two uncovered alias-enrichment
  helpers (**43.05 / 35.36**), being addressed with moved-root/authority regressions,
  not suppressions. `/tmp/memory-cli-current-final.log` is therefore **not a passing
  complete CLI gate**.

### Latest integrated offline checkpoint and full-gate progress (2026-10-24 AEDT)

The code repairs above are now implemented and independently re-reviewed:

- Dedicated authorized readonly SQLite VFS/API preserves exact uncheckpointed WAL
  rows without source SHM/writes or whole-source scratch copies. It authorizes and
  observes DB/WAL identities before opening source bodies, binds the original
  pre-callback main identity, and verifies the complete bounded pair before/after
  backup. Queue retains all **present** input origins across preparation, acceptance,
  missing-original delivery and pre-CAS. Absent WAL does not grant sibling body
  authority. Existing rollback journals are explicitly unsupported via
  `ErrSQLiteRollbackJournal`, not silently ignored. Legacy collector database exports
  are unchanged. The obsolete unused candidate pathname-opening entry point was
  removed only after this replacement was implemented, tested and reviewed; shared
  output/page/dimension bounds remain mandatory.
- Independent `a222a6c1-d2ab-47c` passed standalone VFS review, found/fixed a vacuous
  journal regression, then reproduced a genuine `CaptureIfChanged` provenance loss.
  The fix supplies original source identities separately from prepared byte authority:
  new present-WAL aliases force durable acceptance with unchanged epoch/object;
  subsequent true no-op remains allowed. Both exact regressions are imported,
  strengthened with restart/missing-origin/revocation/healthy-B checks, and independently
  pass **1.889s**. Owner focused source/derived/unchanged checks pass **8.357s**, lint
  zero. The corrected typed-journal regression independently passes **1.445s**.
- Fatal runtime cause preservation is implemented, including actual Run initial /
  retained repair upload/fence paths, live/joined durability, pure cancellation and
  ordinary retry controls. Pure canceled enrichment-result DB reads no longer acquire
  false integrity errors; genuine typed/joined failures remain integrity-fatal. Full
  queue **76.102s / 83.2%**, lint zero, scoped CRAP max **27.15**; final focused causes
  and bookkeeping joins **18.203s / count=3**. `/tmp/runtime-fatal-full-final.log`,
  `/tmp/runtime-fatal-last-focused.log`, `/tmp/runtime-fatal-crap.log`.
- Known opted-out claimed identities are now restrictive runtime checks independent of
  healthy path ownership. Configured logical/old/current alias root-qualified
  namespaces, root-only correspondence and preserved path aliases are honored without
  global UUID matching or blanket directory denial. Actual raw/manual/capture/delivery /
  pre-CAS and withheld-only retained-private-evidence tests pass **4.508s**, lint zero.
  Bare queues without the configured callback were not expanded.
- Genuine moved-root enrichment closure/authority tests pass **1.973s**. A shared
  aggregate snapshot can conservatively remain withheld for a different opted-out
  session in the same root; unrelated roots do not collide by session-key suffix.
  Combined CLI race/shuffle **29.505s / 83.1%**, lint zero, scoped CRAP max **29.40**:
  `/tmp/memory-cli-integrated-current.log`.
- Fresh actual candidate analytics discovery passes **73 tests / 22.655s**, no skips
  or warnings, including all eleven process journeys; JavaScript syntax passes:
  `/tmp/memory-current-all-consumers.log`.
- Original-contract medium independent audit `5d6e429c-b629-419`: conditional offline
  PASS in its stated spot-check scope; no additional confirmed code/wiring blocker.
  This is not proof of release acceptance or exhaustive correctness.

Fresh unchanged development gates run **serially**, with live flags unset:

1. `make test` **PASS, 457.56s**. Eleven actual-process journeys **12.561s**;
   full race/shuffle queue **112.901s**, syncer **413.275s**, CLI **58.665s**.
   `/tmp/memory-replacement-final.pIMS8w/make-test.log`.
2. Initial `make check` exposed two real legacy context-forwarding lint findings.
   Added `snapshot.BuildContext`, retained `Build` as the exact legacy Background
   wrapper, forwarded both syncer callers, and tested cancellation plus selected-byte /
   ID equivalence without new Options/global-policy substitution. Full snapshot
   **41.957s**, focused snapshot/syncer **2.955s / 3.933s**, lint zero; independent
   context regressions **1.394s**. No timeout was raised.
3. Subsequent check required promoting already-pinned `modernc.org/libc v1.77.1`
   from indirect to direct (no version change), and removing unreachable obsolete
   candidate source-opening/Git convenience wrappers. Legacy export implementations
   and underlying Git safety, page/dimension/reserve checks remain intact.
4. Latest full `make check` passes formatting/lint/fix/tidy/deadcode/vulnerability
   checks and **all full atomic race/shuffle tests**. Syncer **435.775s**, queue
   **110.907s**. Merged **86.5%, 13,014 / 15,041 statements**; unchanged package / total
   coverage thresholds PASS. It then fails CRAP only for `isolationCrash` **240** and
   `isolationBackend.boundary` **42**, both measured **0%** despite their actual
   mandatory helper subprocess executions. This is an unaccounted intentional-exit
   subprocess profile, not permission to exclude it or lower CRAP. Owner
   `6f6045db-b277-41d` is fixing metadata/counter flush and child profile accounting.
   `/tmp/memory-replacement-final.pIMS8w/make-check-gates-final.log`, retained
   `/tmp/memory-replacement-final.pIMS8w/merged.cover.out`.

5. **Final superseding result: all unchanged development gates PASS.** Owner
   `6f6045db-b277-41d` fixed actual intentional-exit subprocess accounting. The Go test
   helper cannot provide runtime metadata before M.Run teardown, so the explicit
   coverage mode uses an actual atomic/race-instrumented command for the crash stage;
   other children receive the gate directory flag. Checked metadata/counter flush
   precedes the real exit, without Queue Close/channel release. Missing/non-directory
   output fails the fixture. Both offending functions now have real merged coverage:
   `isolationCrash` CRAP **28.48**, boundary **6.40**; no exclusions/threshold changes.
6. Fresh full **`make check` PASS, 622.40s**: formatting/lint/fix/tidy/deadcode /
   vulnerabilities, full atomic race/shuffle, coverage **86.8%, 13,059 / 15,051**,
   unchanged thresholds, max CRAP **29.40**, benchmark smoke (syncer **113.092s**),
   actionlint and all **11 process journeys / 12.727s**. Atomic syncer **433.512s**,
   queue **112.897s**, CLI **74.599s**. Fresh **`make test` PASS, 460.36s**, includes
   eleven journeys **11.247s**, full race/shuffle syncer **414.472s**, queue **117.885s**.
   All **nine `make fuzz` targets PASS at unchanged 30s**, **285.31s** total. Fresh
   analytics **73 tests / 23.744s**, no skips/warnings. Exact logs:
   `/tmp/memory-replacement-final.pIMS8w/{make-check-current,make-test-current,make-fuzz,analytics-current}.log`.
7. Added serial synthetic resource diagnostics: same actual binary Local / no-network
   SDK four-step fixtures and a **134,218,178-byte** synthetic source. Large acceptance /
   delivery / export maximum RSS **42,909,696 / 52,543,488 / 29,933,568 bytes**; wall
   **0.29 / 0.58 / 0.20s**. Independent export still hashes exactly after source/spool
   paths move aside; 1-MiB quota refuses with zero accepted jobs. These are single-source
   observations, **not** whole-service/real-inventory/resource acceptance. See
   [final offline evidence](session-capture-offline-evidence.md) for scope, full metrics,
   retained diagnostics and limitations.

Original `cover.out` is restored byte for byte after every check (SHA256
`70386d5160553c62f5b9cef9c38af0f7273336aebd491000ab9259fd94f117b9`). Generated
current merged coverage is retained separately. No deployment, remote operation,
original-source deletion or commit occurred.

### Next action / exact commands

**Offline candidate implementation and unchanged development gates are clean; not
release-accepted.** Do not reimplement the earlier pending launch/snapshot/alias /
caller repairs or resume the retired recovery stack. The bounded independent
original-contract review reports no new confirmed code/wiring blocker within its
spot-check scope, not exhaustive proof.

Next prepare/execute controlled operating-resource/freshness and native/live acceptance
with explicit approved scope: actual Claude/Codex/Pi field/trust/hook behavior, isolated
real conditional R2 failures/readback, representative loads, preserved identities /
policies/inventories on this Mac and BC-01, installed paths and restart. Native/remote /
host changes require the deliberate acceptance action, not an incidental test command.
Then follow fencing, rebuild/catch-up, validation, cutover and reviewed deletion in the
original contract. Permission to replace R2 does not prove source completeness. Do not
activate defaults, reset archives or delete originals/retired keys from these results.

If code changes subsequently, rerun serially with live flags unset; preserve original
`cover.out` with a trap and retain generated coverage separately as done above:

```sh
env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE nix develop --command make test
# make check with isolated COVER_DIR and original-cover restoration trap
# make fuzz at unchanged FUZZTIME=30s
```

The small synthetic resource measurements are already recorded, but representative
combined RSS/disk amplification/request cost and actual freshness are still missing
release evidence. Do not turn absent external evidence into invented prerequisites
for further offline testing, or turn passing offline tests into deployment approval.
