# Session capture: implementation direction

**Status:** authoritative replacement requirements, not implementation or release
evidence. For current deployment, qualification and next action, see the
[current-session checkpoint](session-capture-handoff.md#current-session-checkpoint);
for historical checks see [dated offline evidence](session-capture-offline-evidence.md).
Existing v3 code, wire specifications and rollout records describe legacy behavior,
not requirements to reproduce it. Final legacy/default cutover requires the controlled
acceptance sequence below.

## Objective

Capture Claude Code, Codex, and Pi session evidence dependably on this Mac and BC-01,
publish independent readable sessions to R2, and make those publications available to
analytics. Successful upload alone is not successful end-to-end capture.

The operator accepts destroying the existing R2 archive and rebuilding from available
local sources. A legacy migration engine, retired-schema compatibility, reconstruction
of pruned pieces, and restoration of every collector intermediate version are **not**
replacement requirements. Do not resume the old cold-recovery patch stack as the plan.

This permission is scoped to the archive replacement. It does not authorize deleting
original sources, local research/classification evidence, credentials, identities, or
privacy opt-outs. The permission itself is not evidence of a reset or deployment. Subsequent controlled
experimental installations are recorded in the handoff; documenting this direction does
not authorize additional rollout, reset or deletion.

## 1. Logical data contract

- A session and its explicitly associated artifacts are the publication/read unit.
  Session A must not require inspecting session B or the machine's complete archive
  history to capture, repair, or read A.
- Preserve captured conversation evidence, not every collector observation or chunk
  layout. A growing transcript need not retain every intermediate prefix forever.
- The baseline is the latest complete captured contents of each retained **source
  epoch**. A proven append advances an epoch; an unproven rewrite/truncation begins
  another without silently discarding the previous captured evidence. Epochs may
  overlap; do not invent a deduplicated conversation or conflate their identities.
- Source deletion records local absence; it is not an implicit instruction to delete
  captured conversation content. Remote deletion/retention needs its own policy.
- Preserve raw safe source bytes and provenance. Semantic parsing/normalization is
  downstream and must not gate safe raw transcript capture. Protected configuration
  still requires successful exclusion/scrubbing checks.
- Queuing a pathname is not capturing bytes. Polling cannot preserve changes that
  disappear before capture, and staging cannot guarantee an atomic source snapshot
  without producer cooperation. State capture boundaries and limitations honestly.
- Simplifying storage must not silently drop subagents, tool results, custom roots,
  launches, harness snapshots, database exports, or other currently captured artifacts.
  Define their ownership and completeness explicitly in the wire contract.

## 2. Progress and durability

Track independent facts rather than a run-wide clean/dirty flag:

| Fact | Meaning |
| --- | --- |
| Observed | Discovery found source work; bytes may not yet be owned. |
| Captured locally | Bytes and identity are durably owned by the spool/work store. |
| Published | A complete readable session publication is committed remotely. |
| Enriched | Declared provenance/snapshot work succeeded, or is explicitly partial. |
| Imported/normalized/served | Each consumer processed a stated publication; freshness and errors are visible. |

Use per-source/epoch progress and stable publication identities. At-least-once work
plus idempotent publication is the delivery model; do not promise exactly-once capture
from arbitrary mutable files. An unsuccessful update leaves the previous successful
publication readable and its progress intact.

The durable pending store is authoritative for accepted unpublished bytes. It cannot
also be an unexplained disposable cache. If SQLite is an index over spool records,
prove index rebuilding; if it is authoritative, document and test durability settings.
Specify crash reconciliation between filesystem payloads and database work records.

## 3. Scheduling and error isolation

```text
hooks + periodic bounded discovery
    -> per-source capture
    -> durable spool/work records + local progress
    -> coordinator with bounded fair delivery workers
    -> complete session publication
    -> remote acknowledgement + local reclamation
```

Hooks enqueue lightweight discovery hints. Work retries without requiring another
agent event. One local coordinator may own scheduling, but unrelated sessions must
not wait behind a machine-wide historical scan. Keep SQLite transactions short;
do not hold metadata transactions across remote calls or retry sleeps.

Coalesce discovery hints and proven-subsumed append captures, not unique accepted
bytes. Preserve source ordering and prevent stale work from replacing newer content.
Set byte-based spool limits, per-session fairness, operation budgets, and visible lag.

| Failure | Disposition |
| --- | --- |
| Temporary transport/throttling | Keep pending; bounded exponential backoff with jitter. |
| Destination authentication/configuration | Pause that destination and alert; do not poison every session. |
| Invalid semantic record | Preserve raw evidence; quarantine normalization where framing permits. |
| Incomplete active tail | Wait or expose partial state, not arbitrary byte skipping. |
| Rewrite/rotation/truncation | Reconcile that source/epoch, not the machine archive. |
| Missing current dependency for A | Mark A degraded, retain repair work, allow B to proceed. |
| Enrichment failure | Expose missing/partial provenance independently of safe transcript capture. |
| Spool full | Stop accepting/advancing affected capture, alert; no silent oldest-item eviction. |
| Unknown exclusion state/unsafe scrub | Fail closed for affected capture scope; recover policy first. |

Quarantine is durable, inspectable, and replayable, with source coordinates, reason,
attempts, and software/contract version. It is neither success nor silent disposal.
Recheck exclusions before delivering pending work, not only when reading sources.

## 4. Storage and the authoritative reader

The simplicity baseline is full compressed source-epoch objects, reused sealed epochs,
and a complete immutable session manifest behind a guarded current reference. Each
manifest directly describes its selected contents. Reading it must not replay every
predecessor plan, commit, or representation.

Upload dependencies before publishing the current reference. Reconcile ambiguous
outcomes by stable publication identity. Use and test backend conditional publication;
CAS alone does not establish source ordering or an atomic multi-object transaction.
A discovery catalog can be rebuildable/eventually updated; global cross-session
snapshot consistency is not a requirement.

**One authoritative storage reader/resolver** must serve restoration and analytics.
Define a language-neutral consumer interface; do not independently recreate current
selection or reconstruction algorithms in Python and Go. Metadata and bodies must
refer to the same pinned publication. Unknown contracts are explicit errors, never
successful empty discovery.

Chunking is optional physical optimization. Measure full-snapshot bytes, request
counts, local I/O, CPU, freshness, and restore cost before adding it. It must not change
the logical contract or reintroduce historical replay. Begin without automatic GC.
Future GC must honor current/retained roots, readers, and in-flight work, separately
from normal capture.

## 5. End-to-end acceptance

The required release journey is:

```text
source -> durable queue -> R2 publication -> authoritative reader
       -> analytics metadata/bodies -> event index -> report
```

Generate deterministic fixtures using the actual writer and consume them with the
actual reader and importer. Handwritten legacy importer fixtures and a collector
exit code cannot establish compatibility. Tests must assert exact coverage, identities,
body hashes, roles, and declared provenance, including partial/error cases.

Required scenarios:

- Crash/restart at spool, job, upload, manifest, current-reference, and local-ack boundaries.
- Lost acknowledgement, duplicate delivery, stale jobs, conditional conflicts, and
  recovery without another hook.
- Broken A with errors retained while B captures, publishes, restores, and imports.
- Previous successful publication remains readable after a failed update.
- Append, partial tail, rename, truncation, same-size rewrite/regrowth, and compaction.
- Source/epoch identity survives analytics; overlapping epochs do not collide or
  silently lose events. Repeated import is idempotent.
- Unsupported contract and missing dependencies produce visible failures/coverage
  gaps, not green refresh timestamps hiding absent sessions.
- Spool exhaustion, destination outage, privacy recovery, and exclusion before delivery.
- Current reads and analytics work without retired intermediate representations.
- Catalog/index rebuild works from current roots and durable pending work.
- Sandboxed/native hooks, actual R2 semantics, and both machines' installed paths are
  verified separately from offline tests. Do not weaken existing quality gates.

The mandatory offline consumer gate now exercises the actual candidate writer,
authoritative reader, importer, event index and report, including an injected actual
SDK peer. It is not merely a legacy suite or a disconnected hand-written fixture.
Those passing tests do not establish actual R2/native/installed-path acceptance;
adding this document alone would not have added such a gate.

## 6. Controlled two-machine rebuild

1. Inventory eligible sources on this Mac and BC-01, including learned nondefault
   roots. Report skipped, excluded, unreadable, and unavailable content separately.
2. Preserve distinct machine identities, effective local/remote exclusions and opt-outs,
   learned roots, credentials, original sources, and local research evidence. An archive
   reset must not silently re-enable excluded sessions.
3. Prove the new writer/reader/importer journey in an isolated namespace with a fresh
   archive identity/state binding. New roots must not depend on old shared blobs.
4. Rebuild and reconcile both machines' available sources, then catch up ongoing work.
   No claim is made that local sources can recreate remote-only deleted history.
5. Stop/replace old workers and hook launch paths on both machines before final
   cutover. Verify fresh automatic capture, restart/retry, restoration, and analytics.
   Do not delete lock files as a substitute for stopping/fencing writers.
6. Delete only the reviewed retired destination/key set after acceptance. Exclude new
   data, preserved policy, and unrelated objects; verify inventory and new reads after
   deletion. Temporary staging is not a legacy retention or migration requirement.

## 7. Implementation sequence and remaining parameters

1. Specify the versioned publication/reader interface and writer-generated fixtures.
2. Build one-session durable capture/publication/restore/import vertical slice.
3. Add bounded coordinator, autonomous discovery/retry, status/quarantine/replay, and
   hook/install integration. Prove failure isolation and privacy boundaries.
4. Complete analytics projections/reporting and two-machine acceptance, then cut over
   and retire the old archive.

The [wire/reader v2 reference](session-publication-v1.md),
[enrichment contract](session-enrichment-contract.md) and
[phase-3 implementation](session-capture-phase3.md) now define candidate wire fields,
source/artifact ownership, bounds and configured defaults. These are not silently
supplied by legacy v3 rules. Representative freshness/capacity/offline-retention
budgets and actual inventory coverage still require operating acceptance. Current
retention is conservative with no automatic GC; any deletion/retention-window change
needs its own policy and validation, not incidental spool cleanup.

For rationale and primary sources, see the
[design review](research/session-capture-contract-review.md). For legacy behavior and
historical evidence, use the explicitly labeled [documentation index](README.md).
