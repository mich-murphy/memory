# Session capture: challenge the contract before repairing the implementation

Status: supporting research and rationale. The authoritative agreed direction and acceptance criteria are now in [the session capture contract](../session-capture-contract.md); follow that document for implementation, not earlier patch plans. The replacement is not implemented. The operator accepts deleting the existing R2 archive and rebuilding from local sources on this Mac and BC-01; legacy archive compatibility is not a design requirement. No deletion or deployment has been performed.
Date: 2026-10-23.

## Executive recommendation

Treat this as a session-capture and log-shipping system, not an indefinitely replayable archive of every collector observation. Make the session (and explicitly associated artifacts) the logical publication and failure-isolation unit. Retain previous successful progress while pending failures remain durable and visible.

Use one local coordinator, a durable spool/outbox, per-source progress, bounded independent delivery, idempotent publication, and explicit retry/quarantine policies. Go, SQLite, and R2 are sufficient; the precedents below do not justify adding Kafka, Debezium, or a lakehouse platform.

Define the logical contract independently of the physical layout. A complete compressed session snapshot is the simplicity baseline. Differential chunks are an optimization to justify with measurements, not a reason to require replay of all previous collector generations.

Do not require historical `Restore` compatibility or recovery of retired v3 representations in the replacement. The operator explicitly prefers a clean rebuild over preserving the current R2 archive. Deleting it may lose remote-only data no longer present in either machine's local sources; disclose that limitation rather than assuming all previous history can be recreated.

## 1. The underlying modeling error

The current machine checkpoint conflates at least four different facts (and the end-to-end system has a fifth, separately tracked consumer-compatibility concern):

1. Whether the local projection agrees with published remote state.
2. Whether the newest source bytes have been captured.
3. Whether optional provenance/enrichment completed.
4. Whether every historical physical representation remains available.
5. Whether the reader/analytics importer understands the published contract and has processed the current session revision. This is not presently represented by the collector's checkpoint; it must not be conflated with capture success.

A single run-wide clean/dirty flag cannot accurately represent these facts. One failure invalidates too much work, and the resulting repair path blocks unrelated sessions.

Relevant current paths:

- `internal/syncer/checkpoint.go`: run-wide checkpoint matching, dirtying, sealing, and record enumeration.
- `internal/syncer/recovery.go`: machine-wide recovery before new capture.
- `internal/syncer/syncer.go`: checkpoint sealing conditional on zero summary errors.
- `internal/cli/commands.go`: one lock spans recovery, launches, discovery, and publication.
- `internal/hook/hook.go`: debounce markers and detached processes, rather than a general durable retry scheduler.

The observed pruning failure also exposes a distinction between logical data and physical representations: a historical piece can be obsolete as a representation while its conversation bytes remain present in a later complete representation. The current contract and implementation disagree about when that substitution is permitted.

## 2. Which history is actually required?

These are separate requirements:

| History | Proposed treatment |
| --- | --- |
| Messages, tool calls, and other conversation content within a session | Preserve the content promised by the product. This is the useful session history. |
| Unique conversation content removed by source compaction, truncation, or replacement | Explicit policy required. Latest file alone may not preserve it. Conservatively retain affected source epochs until decided. |
| Every intermediate observation made by the collector | Not a default product requirement. Drop the promise in a new contract unless a consumer needs observation-time travel. |
| Every previous chunk layout or compressed representation | Implementation detail, not a retention requirement. Reclaim only after it is no longer required by any retained root or in-flight operation. |
| Unrelated sessions' history | Not a prerequisite for capturing or reading this session. |

A full current transcript commonly contains earlier conversation turns; this does not require retaining every prefix captured on the way to that transcript. However, polling mutable files cannot promise to capture every event that existed between polls. Nor can the latest file automatically be treated as a full historical conversation after a destructive rewrite.

Proposed default: **retain the latest complete captured contents of each source epoch belonging to a session; do not retain every collector intermediate version indefinitely.** A proven append advances the existing epoch; an unproven rewrite/truncation starts another and retains the previous epoch's captured evidence. Source deletion records absence rather than automatically deleting archived conversation content. Epochs can overlap; they are not disjoint semantic event segments. Epoch retention is not permission to merge conflicting versions into an invented conversation. Preserve provenance and raw source identity; define semantic reconciliation separately if needed.

## 3. Proposed contract and invariants

1. **Session-local availability.** Reading or publishing session A does not require validating session B or obsolete versions of A. Shared blobs are allowed only if A's root independently identifies its dependencies.
2. **Explicit capture boundary.** A path queued for later discovery is not captured data. Local acceptance occurs only when the intended bytes and their identity are durably owned by the spool. Remote completion occurs only when a readable complete publication is committed.
3. **Separate progress coordinates.** Track durable local capture and remote committed progress independently, per source/epoch. For append sources these may be byte offsets; for rewriteable sources use snapshot/revision identities. A useful remote watermark must not claim an unrepresented gap is complete.
4. **At-least-once processing, idempotent publication.** Replays may repeat work but cannot duplicate logical content or regress current state. Do not claim end-to-end exactly-once capture from arbitrary files.
5. **Prior success remains success.** An unsuccessful new capture leaves the previous committed session readable. Retry status does not invalidate it.
6. **Failures remain explicit.** Pending, retrying, quarantined, and administratively paused work is inspectable and replayable; it is not mislabeled committed or discarded after a retry limit.
7. **Independent scheduling.** An ordinary error for A does not stop B. Destination-wide outages can stop remote delivery, but local capture continues within an explicit resource budget.
8. **No network under long metadata transactions.** Use short SQLite transactions for claiming/updating work. One coordinator ownership lock is acceptable; hooks must not compete for a lock held throughout remote recovery.
9. **Current read is bounded by current representation.** It does not require replaying all prior plans, commits, or collector runs.
10. **Privacy is a safety barrier.** Durable exclusions and capture-policy checks still apply before reads/uploads, including during repair. Unknown exclusion state and unsafe config scrubbing must not be bypassed in the name of progress.

SQLite cannot simultaneously be described as disposable and be the only authority for accepted unpublished bytes. Either make the pending store explicitly authoritative with appropriate durability, or make durable spool records authoritative and rebuild its SQLite index. Filesystem payloads and SQLite rows also need a documented crash-reconciliation protocol; they do not form one automatic transaction.

## 4. Minimal architecture

```text
hooks + periodic bounded discovery
          |
          v
per-source observation / capture
          |
          v
private durable spool + work records + local progress
          |
          v
one coordinator, bounded fair delivery workers
          |
          v
immutable payload / complete session manifest
          |
          v
conditional publication of session current root
          |
          v
remote acknowledgement + local reclamation
```

Hooks enqueue lightweight discovery hints and return. The coordinator performs autonomous retry and periodic discovery; another agent lifecycle event is not required to make progress. Coalesce hints, not unique already-accepted bytes. A newer captured append may supersede an older pending capture only when it demonstrably contains those bytes; a rewrite cannot simply replace a queued path/payload and erase the previous epoch. Keep append ordering within a source; permit independent sessions to progress concurrently. Bound work per session so a large backlog cannot monopolize the worker.

Raw capture should not depend on semantic JSON normalization. Malformed transcript records can remain byte-exact raw evidence; downstream parsing can quarantine them independently. Recognized sensitive configuration still requires successful policy/scrubbing. Incomplete active-file tails should wait or remain explicitly partial, not be declared corrupt solely because a write is unfinished.

An illustrative work lifecycle:

```text
discovered -> durably_spooled -> ready -> in_flight -> committed
                                  ^         |
                                  |         +-> retry_wait
                                  |         +-> quarantined
                                  +-------------+ (explicit replay after repair)
```

An expired in-flight claim returns to runnable work. Destination pause is separate from an individual item's invalidity. Remote acknowledgement loss leads to reconciliation of that publication's stable identity, not machine-wide replay.

## 5. Error handling is a policy, not catch-and-continue

| Condition | Required behavior |
| --- | --- |
| Timeout, throttling, temporary network failure | Keep pending; exponential backoff with jitter and a retry ceiling on frequency, not silent data deletion. |
| Authentication/configuration failure | Pause the affected destination and alert. Do not route every valid session into a poison-data DLQ. |
| Complete malformed semantic record | Preserve raw bytes and coordinates; quarantine normalization if framing permits. Raw safe transcript capture can continue. |
| Partial final line | Wait for completion; maintain a bounded retry/discovery path. |
| Source truncation, rotation, replacement, or non-append rewrite | Reconcile that source/epoch; do not blindly reuse its offset or reset the machine. |
| Missing dependency of current session A | Mark A degraded and schedule repair; retain its error. B can proceed. Do not publish A as complete with missing bytes. |
| Missing obsolete collector representation | Relevant only if retained history promises require it. It must not gate current ingestion. |
| Provenance/snapshot enrichment failure | Record missing/partial provenance separately; do not claim enrichment succeeded or block safe transcript capture unnecessarily. |
| Disk/spool capacity exhausted | Stop accepting/advancing affected capture, alert, and expose lag. No silent oldest-item eviction. Source retention limits remain a loss risk. |
| Unknown privacy/exclusion state | Fail closed for the affected capture scope; recover policy before accepting/uploading data. |

Quarantine preserves original data or its private durable reference, source/epoch, byte range or revision, error classification, attempts, timestamps, and software/schema version. It needs a list/inspect/replay workflow. It is not synonymous with successful remote delivery and is not a permanent trash can. Avoid putting transcript content in ordinary logs.

## 6. Simplify remote publication

### Baseline: complete snapshots

The recommended simplicity-first baseline is **one full compressed object per selected source epoch, with a complete session manifest**. Sealed epochs are reused; only the growing active epoch is uploaded again. Publish the manifest as a consistent unit through a guarded current reference. This avoids both delta reconstruction and repeated transmission of all sealed epochs.

A single complete session bundle is an even simpler alternative if sizes justify it. For very simple layouts a conditional complete-object replacement may suffice, but separate metadata must not introduce a torn multi-object update.

Advantages: easy crash/restore reasoning, no prefix reconstruction, natural rewrite support. Cost: repeated upload of a growing active epoch. Coalescing and less frequent publication can reduce cost, but freshness and data-loss exposure must remain explicit. Do not retain every superseded full object forever merely because the format permits it; that would recreate collector-version retention at a higher storage cost.

### Optional optimization: chunks under the same contract

Upload immutable session-local chunks and a **complete** manifest listing the current ordered representation. Restore follows that manifest, not predecessor manifests. An append reuses verified data; a rewrite creates a new source revision/epoch. Compaction changes representation, not conversation semantics.

The existing largest investigated transcript was about 79 MB, so full-snapshot bandwidth deserves measurement. That is a reason to benchmark chunking, not to reintroduce indefinite history replay. Compare request counts, bytes sent, CPU, local bytes read, freshness, and operational complexity using actual session-size/turn distributions.

### Publication protocol

1. Capture/stage an explicit source boundary.
2. Upload immutable data.
3. Upload a complete immutable manifest.
4. Conditionally replace the session root against its expected ETag; conditionally create when absent.
5. Reconcile ambiguous outcomes using the stable publication identity; acknowledge locally only after publication is established.

Cloudflare currently documents atomic conditional `PutObject` evaluation against committed state, including serialization of competing conditional writes. This supports a small guarded current root. It is not a multi-object transaction, does not establish conditional multipart-completion semantics, and does not solve stale-source ordering by itself. Test the actual SDK and backend. A stale job must not rebase itself over newer content merely because CAS permits a subsequent retry.

A catalog may be an eventually updated discovery index. A per-session root and a separate global index are not one atomic commit. Do not require global cross-session snapshot consistency unless a consumer needs it.

## 7. Recovery, retention, and garbage collection

Separate ordinary restart, source repair, catalog rebuild, integrity audit, and historical restore. They are different operations with different scopes and budgets.

- Restart reclaims incomplete work and resumes pending publications.
- A's repair inspects A and required dependencies.
- Catalog rebuild enumerates current roots and is resumable; it does not validate every retired generation before accepting new work.
- Audit reports corruption independently; it does not automatically stop all capture.
- Historical restore is supported only for explicitly retained roots and their dependencies.

Retention decides which roots remain promised. Reachability decides which physical objects remain required. Do not use object age or 'not referenced by the latest generation' alone when another retained root still needs the object. Compaction publishes replacement data before retiring the old representation.

Start with no automatic remote deletion. Add GC only with explicit roots for current sessions, retained epochs/snapshots, readers, and in-flight publications, plus coordination that prevents a writer from referencing an object already selected for deletion. A grace period alone is not a complete protocol. Keep GC out of ordinary capture and test publication/GC races.

## 8. Replacement boundaries

The earlier cold-recovery concurrency and pruned-piece patches belong to the retired repair direction. They are not implementation milestones, prerequisites, or compatibility requirements for this replacement. Their commits and prior investigation preserve the historical evidence; do not resume that stack as the plan.

Checksums, streaming, scrubbing, adapters, immutable payload identity, source discovery, tested backend behavior, and useful failure logging remain reusable. Observability must additionally cover queue state, publication stages, consumer coverage, and freshness. Preserve useful components where they satisfy the new contract; replace the orchestration rather than accelerating the old global recovery path.

## 9. Clean rebuild and acceptance

The operator accepts destroying existing R2 archive contents. Preservation of retired schemas, old plans/commits, collector-generation time travel, and pruned-piece reconstruction are not replacement requirements. This removes the need for a legacy migration engine; it does not remove the need for controlled cutover.

The rebuild dataset is the eligible source data actually available on this Mac and BC-01. Inventory each machine before claiming completeness. Previously deleted local transcripts, removed source epochs, and historical provenance may exist only in the old bucket and will not be recreated merely by backfilling current sources. A report of that boundary is sufficient; exhaustive old-history restoration is not a release prerequisite.

Recommended rollout:

1. Define the new versioned writer/reader/importer contract and its acceptance fixtures together.
2. Validate a vertical slice in an isolated namespace: capture, durable queue, restart/retry, publication, restore, analytics import, and dashboard freshness. Include a real source sample for each supported agent.
3. Verify inventories and capture policy on both machines. Preserve stable machine identities, effective opt-outs/exclusions (including remote-only exclusions), learned nondefault roots from local state, and original source directories. Default directory discovery alone may omit roots previously learned through hooks. An archive reset must not silently re-enable excluded sessions; recheck policy before delivery of already queued work.
4. Stop or replace old workers and hook launch paths on both machines before production cutover. Verify no old process can resume writing the retired namespace. Do not remove lock files to simulate stopping writers.
5. Establish a fresh archive instance identity and state binding. Rebuild each machine from its local sources through the normal durable queue, with bounded concurrency and resumable progress. Preserve original local sources and avoid indiscriminately deleting local pending data/state.
6. Rebuild analytics and verify both machines independently: expected eligible sessions reconcile, bodies restore, repeat import is idempotent, incoming updates become visible, and failures are explicit rather than silently absent. Do not delete existing local research/classification artifacts under the authorization to reset R2; explicitly preserve or retire their old citations and never silently attach them to new publications.
7. Remove retired R2 keys using a reviewed exact destination/prefix inventory. A staging namespace allows validation before deletion; it is not a commitment to retain legacy data. New publications must depend only on new-namespace objects, not shared old blobs selected for deletion. Do not delete newly validated publications or unrelated bucket objects along with the retired archive. Verify the resulting inventory.

A wipe-before-rebuild would also be compatible with the operator's stated tolerance, but staging then deleting gives a safer cutover without requiring legacy compatibility or a permanent backup. Existing production remains degraded until an authorized replacement is actually deployed; this proposal is not an operational repair.

The first implementation should prove one session through durable capture, restart, idempotent publication, rewrite, restore, and analytics import. Then prove that an intentionally broken A cannot block B. Only then add chunk optimization or automate ongoing garbage collection.

Acceptance tests must cover:

- Crash before/after spool persistence, job persistence, data upload, manifest upload, root publication, and local acknowledgement.
- Acknowledgement loss, duplicate delivery, stale jobs, conditional-write conflict, and power-loss durability assumptions.
- A poison job or missing A dependency while B captures and restores successfully.
- Restart with prior errors intact, no new hooks, and successful autonomous retry.
- Append, partial line, rename, inode reuse, truncation, same-size rewrite, regrowth, and source compaction.
- Local queue saturation and expired source retention; no silent checkpoint advancement or eviction.
- Current restore after old intermediate manifests/representations are expired under the new policy.
- Rebuild from current roots and spool, without needing all historical publication records.
- Privacy/exclusion recovery and failed sensitive-config scrubbing.
- Publication/GC races before any automatic deletion is enabled.
- Writer-generated fixtures consumed by the actual reader, metadata/body importer, event index, and report. Assert exact session/source/epoch coverage and body hashes, and reject unsupported formats without a false successful-empty refresh.

Decisions before implementation: preserve latest source only or unique pre-rewrite conversation epochs; which associated artifacts remain promised (subagents, tool results, launches, harness snapshots, databases, and agent-wide files); publication freshness target; offline/spool budget; optional historical-retention window; and deletion/opt-out semantics for already archived bytes. Simplifying archive machinery must not silently narrow the capture dataset.

## 10. Analytics is part of the publication contract

The following is the **pre-cleanup diagnosis**, not a claim that the former silent-omission path is still unguarded. The cleanup added `guard_inventory` to `tools/analytics/r2_metadata.py`: known session-commit inventories, including mixed ones, now fail explicitly after LIST and before GETs or local writes. Existing database contents, timestamps, caches, and research artifacts survive that rejection. This is a narrow safety guard, not a new reader or general future-contract detector.

The underlying consumer mismatch remains more than a schema-number check:

- `refresh` in `tools/analytics/r2_metadata.py` selects only `/records/manifest/` commits. Newer collector publications use `/records/session/`; before the inventory guard, normal discovery silently omitted them.
- `decode_record` accepts only kind `manifest` with payload schema 3. Merely broadening discovery cannot make bodies or projections compatible.
- A truly empty store still permits an empty legacy refresh and table replacement. A successful legacy refresh is not proof of current-contract archive coverage.
- Current collector session payload schemas 4/5 still use a schema-3 publication envelope and the `v3/` namespace (`internal/syncer/publication.go:29–37`). These version dimensions must not be confused.
- Current transcript entries are stream references; their manifest dependency anchors membership, not necessarily the latest body. Later file updates need not republish that old-format session manifest (`docs/storage-layout.md:189–199`).
- `tools/analytics/views.sql` expects copied generation metadata, and `tools/analytics/bodies.py` requires the retired manifest format and a direct file commit. Accepting a newer schema number alone cannot resolve current bodies correctly.
- Launch projection expects inline `launches`, while schema 5 uses `launch_records` references (`views.sql:12`; `internal/syncer/validation.go:100–110`).
- Retained epochs also require new identities: `tools/analytics/events.py` currently identifies a body by machine/agent/file key, without epoch, and rejects duplicate file identities.

Existing importer/body tests generate retired schema-3 fixtures independently of the current collector. Local read-only inspection found an October 16 analytics cache containing only legacy current manifests. This is evidence of inadequate consumer acceptance, not a current remote inventory.

For the clean rebuild, implement **one authoritative storage reader/resolver** and make restore and analytics consume its resolved representation. It can expose a language-neutral interface to Python; the concrete interface is a design choice. Do not maintain separate handwritten Python and Go algorithms for deciding current state or reconstructing storage internals.

The consumer contract must include:

- Archive instance, contract version, machine/session/source/epoch, and publication identity.
- Metadata and bodies pinned to the same complete publication.
- Launch/provenance associations or explicit missing/partial status.
- Distinction between captured/published, imported, normalized, and served progress.
- Per-session errors and independent progress; unsupported archive contracts must fail explicitly rather than become an empty successful dataset.
- Import idempotency and invalidation after archive-instance reset.
- Epoch overlap represented faithfully, not duplicate-identity failure or silent event loss.

Production acceptance is **source → queue → R2 publication → reader → analytics metadata/bodies → event index → report**, not merely a collector exit code or passing isolated tests. Generate fixtures from the writer and run the real importer against them. Rebuilding R2 without rebuilding and validating these consumers would reproduce a broken system with cleaner storage.

## 11. Evidence and applicability

These are established patterns, not one universal standard. The proposed architecture is a synthesis, not a vendor-certified design.

1. [Reis and Housley, Fundamentals of Data Engineering, chapter 7](https://www.oreilly.com/library/view/fundamentals-of-data/9781098108298/ch07.html): the legitimate public preview identifies reliability/durability, ordering/multiple delivery, replay, and DLQs. It does not expose enough chapter text to substantiate every detailed recommendation here.
2. [Filebeat: how it works](https://www.elastic.co/docs/reference/beats/filebeat/how-filebeat-works): persistent per-file state, acknowledgement tracking, and at-least-once replay. Direct precedent for independent progress rather than a machine-wide dirty bit.
3. [Filebeat internal queue](https://www.elastic.co/docs/reference/beats/filebeat/configuring-internal-queue): persistent pending delivery and capacity policies. Verify sync/write-ahead boundaries rather than equating disk-backed with zero-loss.
4. [Fluent Bit Tail](https://docs.fluentbit.io/manual/4.1/data-pipeline/inputs/tail): SQLite file-offset tracking and rotation behavior. File tailing is a closer model than database CDC for this source.
5. [Fluent Bit retries](https://docs.fluentbit.io/manual/4.1/administration/scheduling-and-retries): retry classifications and capped backoff with jitter. Do not copy discard-after-limit policies for valuable sessions.
6. [Vector buffering](https://vector.dev/docs/architecture/buffering-model/) and [acknowledgements](https://vector.dev/docs/architecture/end-to-end-acknowledgements/): explicit durable-buffer and acknowledgement boundaries. Its [file source](https://vector.dev/docs/reference/configuration/sources/file/) is labeled best effort; this is not an unconditional guarantee for mutable files.
7. [AWS transactional outbox](https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/transactional-outbox.html): atomically record local work/progress, then deliver idempotently. We do not control source-agent transactions, so cannot atomically couple an agent's write to our outbox; the pattern applies after local capture.
8. [Kafka Connect error handling, KIP-298](https://cwiki.apache.org/confluence/display/KAFKA/KIP-298%3A+Error+Handling+in+Connect) and [Debezium JDBC](https://debezium.io/documentation/reference/stable/connectors/jdbc.html): poison-record context, retry handling, and idempotent writes. Architectural examples, not a claim that local transcript files possess database CDC guarantees.
9. [Iceberg reliability](https://iceberg.apache.org/docs/latest/reliability/): complete consistent snapshots and atomic current-reference replacement. Borrow the principle, not a global lakehouse catalog.
10. [Iceberg maintenance](https://iceberg.apache.org/docs/latest/maintenance/) and [restic forget/prune](https://restic.readthedocs.io/en/stable/060_forget.html): retention is distinct from physical reachability/cleanup; orphan cleanup can endanger in-progress writes.
11. [Kafka 4.1 topic configuration](https://kafka.apache.org/41/configuration/topic-configs/): compacted current-state semantics are distinct from indefinitely retaining every historical value. Compaction cannot discard a delta's still-required predecessors.
12. [R2 consistency](https://developers.cloudflare.com/r2/reference/consistency/): direct strong consistency, atomic conditional writes, and why a negative read does not settle a previously ambiguous in-flight PUT. [S3 compatibility](https://developers.cloudflare.com/r2/api/s3/api/) must be checked per operation.
13. [SQLite WAL](https://sqlite.org/wal.html): SQLite durability depends on configuration and failure model; WAL alone is not a power-loss guarantee.
