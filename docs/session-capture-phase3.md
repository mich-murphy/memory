# Phase 3: independent capture and autonomous work

**2026-10-24 — offline implementation candidate; fresh full development gates pass.**
This follows the [capture contract](session-capture-contract.md), not the retired
recovery design. See [final offline evidence](session-capture-offline-evidence.md) for
unchanged check/test/fuzz, eleven process journeys and measured synthetic-resource
scope. This is not release acceptance. Nothing here records production installation, real-source
ingestion, two-machine inventory, live R2 acceptance, cutover, or deletion.

Current integrated status: native immutable evidence/inbox, independent enrichment,
raw/optional delivery and durable repair, declared aliases, authorized all-components
no-follow source reads and readonly WAL-aware SQLite capture are implemented and
independently reviewed offline. Explicit Local/R2 backend construction and
`--source publications` dashboard/query callers are wired. Wire/reader/report are v2;
`capture-v1` command prefix/namespace is unchanged. Defaults remain unactivated.
No new confirmed code/wiring blocker was found within the latest independent review's
spot-check scope; that is not exhaustive correctness or release acceptance.

## Implemented capture and scheduling

`internal/sessioncapture` supports concurrent sessions with per-session ordering.
Source reads, compression, and network calls do not hold a metadata transaction or
machine-wide capture-I/O lock. Context-aware session/hash-install waits do not create
one goroutine per waiter. A blocked session cannot claim every capture worker.

SQLite WAL with `synchronous=FULL` remains authoritative for accepted work. Private
encoded payloads are synced before acceptance; crash orphans, sealed epochs and
intermediate accepted publications remain retained. No automatic GC or eviction is
implemented. Failed capture never advances an accepted publication. Source absence
is a local fact, not remote deletion.

`CaptureIfChanged` double-hashes a stable observation and compares provenance; it
does not trust size/mtime. Unchanged raw sources can be checked at full encoded quota
without creating a new compressed copy. No-op observations advance observation work
but retain the actual source byte-capture timestamp. Ordinary `Capture` continues to
create an explicit publication.

`Run` owns separate capture and delivery scheduling with versioned source claims,
per-session delivery order, cooperative operation contexts, persisted retries and
jitter, destination pause, durable quarantine and replay. Accepted publications are
resumed after restart without another hook or their original source. Explicit
`Deliver` remains an immediate operator retry; `DeliverDue` honors scheduling.
Quarantine retains source/session coordinates, attempts, safe classifications,
contract and diagnostic software version. Replaying does not erase historical entries.

Caller cancellation is graceful only if every error-tree leaf is that cancellation;
joined database, integrity or durability failures are not hidden by shutdown.

## Concrete resource bounds

These are implemented candidate limits, **not measured production resource/freshness
acceptance**:

| Resource | Candidate bound/default |
| --- | --- |
| Discovery timer | 30 seconds |
| Capture / delivery workers | 4 / 4 |
| Cooperative operation context | 60 seconds |
| Retry backoff | Jittered, clamped to 1 second–5 minutes |
| Failed attempts before quarantine | 8 |
| Encoded spool | 1 GiB by service example; no eviction |
| Source body | 1 GiB raw/encoded cap |
| Prepared derived working space | 4 nonwaiting slots × 1 GiB = **4 GiB**, separate from encoded quota |
| Config input to scrubber | 5 MiB |
| zstd decoder window/memory limit | 64 MiB per decoder; not a whole-process RSS claim |
| Service filesystem reserve | 256 MiB; low-level fixtures may explicitly use zero |
| Discovery directory entries | At most 4096 per call, fairly divided among configured roots |
| Discovery directory descriptors | At most 256; depth/descriptor gaps are explicit |
| Configured roots | At most 128 |
| Retained source work records | 4096, with same-source coalescing at capacity |
| Retained discovery issues / scopes | 4096 each, including resolved history |
| Path-hint inbox | 1024 hints, each at most 64 KiB |
| Status page | 1–256 records; independent list cursors |

Encoded quota includes retained payloads, orphans, live encoded temporary bytes and
unwritten reservations. Duplicate temporary copies count until successfully removed.
A short directory-metadata fence distinguishes internal rename/removal races from
external missing evidence; the latter fails closed.

Prepared working slots are deliberately conservative. While a slot is active,
free-space admission reserves its full 1 GiB plus **all remaining encoded spool
headroom**, outstanding encoded reservations, and the configured free-space reserve.
This may reject a tiny derived capture on a nearly full disk. Prepared crash/failed-
cleanup directories retain their entire logical slot charge on later capture/reopen;
no automatic cleanup is claimed. SQLite metadata, filesystem overhead and unrelated
external disk writers are outside this reservation guarantee.

The retained metadata caps require intervention at saturation; they are not proof
that either real machine's complete inventory fits. Discovery callbacks and filesystem
operations remain cooperative; context cancellation does not make arbitrary kernel I/O
interruptible. Measure real inventory size, memory, disk amplification and freshness
before selecting production limits.

## Discovery, ownership and safe derived sources

`internal/capturesource` performs metadata-only bounded discovery over explicit
canonical/default and learned roots. Root-qualified identities use the full SHA256 of
the clean absolute root, not a short legacy tag. Hook classification and periodic
classification share admission and identity rules; longest matching configured roots
own overlapping paths. A resumable in-process cursor is not durable captured progress.

The opt-in derived path uses queue-owned transformations, never a caller-provided
prepared pathname:

- Main/subagent `.jsonl.zst` sources are decoded with bounded streaming and retain
  original path, ownership and transform associations.
- Raw, unchanged and transformed source reads bind pre-authorization file identity
  through all-components no-follow descriptors; authorization is not followed by an
  unrestricted pathname reopen.
- SQLite sources use an authorized readonly VFS and pinned, bounded incremental backup
  that includes committed WAL data without source SHM/writes or whole-source scratch
  copies. Present DB/WAL origins remain private policy evidence through acceptance,
  unchanged checks, missing-original delivery and pre-CAS. Rollback journals fail with
  `ErrSQLiteRollbackJournal`; no raw fallback is allowed. This is a verified stable
  observation, not a producer-atomic snapshot of arbitrary mutable files.
- Recognized JSON/TOML configuration requires successful scrubbing; malformed,
  unsupported or credential-alias inputs never fall back to raw capture.
- Canonical `~/.claude.json` has one exact standalone observation in enabled default
  inventory. It is a scrubbed resource source, **not permission to traverse Home**.

Discovery policy distinguishes directory metadata from final aggregate artifacts.
One opted-out session must not suppress discovery of another session's transcript.
Conversely, an agent-wide SQLite/config artifact is conservatively withheld if a
same-root opt-out could be included. Privacy ownership is derived from configured
roots and bounded layout rules, not trusted from request identities/associations.
Missing accepted transcript paths retain their exclusion identity before delivery.
Credential/config/state boundaries and effective opt-outs are rechecked through the
immediate pre-CAS boundary. Missing/unreadable policy fails closed.

Claude external artifact ownership now uses a bounded durable UUID/main-layout index,
not a global transcript scan. Conflicts and saturated conflicting owners remain
ambiguous. Native launch evidence and closed harness/memory bundles now have an opt-in
integration and actual-process journeys. Declared-root alias moves and optional
failure/crash/restart/repair/revocation isolation are implemented and tested offline;
actual alias inventory coverage and operating-resource acceptance remain outstanding. See the
[independent enrichment refinement](session-enrichment-contract.md) and exact
[handoff ledger](session-capture-handoff.md); no partial candidate replaces installed
legacy capture merely because primary transcripts work.

## Durable coverage, status and consumers

A durable pathname hint is observation metadata, not owned transcript bytes. Its
independent inbox lock does not wait for the queue lifetime flock; accepted handoff
removes it only after the authoritative work record commits. Invalid externally
created inbox entries are retained and counted, never silently acknowledged.

Discovery batches persist safe per-root/path issues, scope coverage and sweep
coordinates. An incremental healthy slice cannot clear an earlier root's failure.
Only a completed sweep without metadata/admission errors resolves unseen issues;
restart starts another generation and preserves interrupted gaps. Resolved history is
retained. `DiscoveryComplete` means a completed **observation sweep**, not complete
capture/enrichment. Aggregate coverage intentionally remains partial until the full
artifact contract is accepted. Rejected/truncated inbox counters describe bounded
inspection, not an unbounded exact scan.

`work-status` and `quarantine` use a read-only SQLite snapshot while the coordinator
holds its lifetime flock. `enrichment-status` independently pages exact work/result
basis and completeness without taking that lock. `optional-status` likewise supports
live read-only inspection. Replay/resume, enrichment replay, optional replay and
optional quota replay require the coordinator to be stopped.

The same authoritative **v2** publication reader serves restoration and Python.
Explicit optional withholding preserves redacted retained coordinates and partial
coverage without fetching a forbidden/unavailable body. Required sources cannot be
demoted. Old contracts/projection DBs are rejected, not silently reset. The
projection verifies and owns every selected body. Opaque artifact bodies are not sent
to the transcript parser or split into potentially huge binary lines; they remain
restorable evidence with an explicit `artifact_not_normalized` issue. Artifact-only
sessions do not fabricate conversation events or complete normalization.

## Opt-in CLI and lifecycle boundaries

The local experimental commands include `capture-v1 run`, `hint`, `hook`, `hooks`,
`work-status`, `quarantine`, `replay`, `resume`, `enrichment-status`,
`enrichment-replay`, `optional-status`, `optional-replay`, `optional-quota-replay`,
`policy-pauses`, and service planning/lifecycle. Configuration is strict
private `capture-runtime/v1` JSON with explicit archive, machine, source inventory,
spool and initialized policy. Existing `store` selects Local; an exclusive `backend`
specification can explicitly select Local or SDK R2 with named credential references.
Parsing, hints and inspection never resolve those references. Remote runtime startup
is independent of credentials/network: bounded delivery workers initialize and verify
archive binding before namespace access, with unavailable/auth destinations paused.
The factory and configured init/export are wired, but only nonnetwork synthetic SDK
peers have been exercised. No production credential lookup or R2 operation occurred.
Its actual configuration path is protected,
as are legacy collector configuration and all state locations.

`hook --agent AGENT --path PATH` classifies one source without body parsing. Bounded
stdin metadata can provide native transcript/session paths. A hook never opens the
writable queue or performs delivery. Pathless objects submit durable discovery wakes;
completed consistent sweeps acknowledge only their original nonce. Owned native
installation preserves unrelated/legacy hooks and uses recognized protected-config
backup names. When `enrichment=true`, native start/resume accepts a separate immutable
sanitized context record; it may precede the transcript file. Pi supplies SDK context,
not collector ancestry. Explicit pathname-only calls do not invent a native launch.

Disabled known-session hooks durably persist effective legacy exclusions. An unresolved
disabled identity creates an agent-scoped privacy barrier, not merely a failing hook
while a daemon continues capturing. `policy-pauses` can inspect it without opening the
queue. Clearing requires the exact nonce, `--apply`, and explicit acknowledgement
that every unresolved opt-out was reconciled; enabled hooks never clear it.

Enrichment is separately opt-in. Offline independence/repair acceptance is implemented
and reviewed; representative operating-resource acceptance remains outstanding.
Additional `enrichment_roots` authorize only trusted snapshot selector inputs, not
ordinary discovery or raw capture. Known Home/source-root/native-CWD scopes are bounded;
host managed/ancestor files outside that inventory are denied before body reads.
Snapshot selected-content limits remain 64 MiB, with additional 2-MiB evidence,
8-MiB metadata and 128-MiB serialized TAR limits. Queue-owned private scratch and
minimal authorized Git metadata/filename-placeholder selection replace broad repository
body/object copying; no system-temp fallback is used. Descriptor-bound authorization,
bounded walk batches, subtree-local denial and checked cleanup/orphan charge are
implemented and reviewed. Unknown package dirty state remains partial/null, not clean.
These bounds and synthetic tests are not combined production RSS/disk acceptance.

Service `plan`, `install`, `start`, `stop`, `restart`, and `uninstall` use explicit
candidate executable/configuration paths. Without `--apply`, no supervisor or service
filesystem changes occur. Fake-home tests inject runners; they do not call this Mac's
launchctl or a real systemd. Failed stop prevents unit deletion; uninstall never
removes source, spool, policy or configuration. Actual native lifecycle remains a
separate acceptance gate.

## Offline isolation and repair

Raw and optional delivery lanes allow newer same-session main bytes and published ACK
to advance while optional Put/Open/fence work is blocked. Suppression is durable and
supersession is checked before CAS/ACK; blocked composites are not falsely acknowledged.
Repair uses retained original basis and capture times, durable
`verified_attachment_pending` restart/reattachment and policy/integrity rechecks.
Repair metadata caps are 4096 rows, 32,768 aggregate histories and 128 histories per
repair, all byte-charged. Admission failure exposes `OptionalRepairGap`; replay admits
retained work before clearing it. No accepted evidence is silently evicted.
Destination authentication is not optional poison; genuine local-store/durability
causes, including joined cancellation, remain fatal after bookkeeping rather than
being downgraded into ordinary retry. Known opted-out claimed identities and retained
private evidence are restrictive checks even for withheld-only views.

## Checks and remaining acceptance

Fresh `make check` **622.40s**, `make test` **460.36s**, all nine unchanged **30s** fuzz
targets (**285.31s** total), and analytics **73 tests / 23.744s** pass. Exact final
coverage/CRAP and eleven-journey evidence are in
[final offline evidence](session-capture-offline-evidence.md).

### Historical intermediate checks (superseded by the integrated result)

The intermediate results below preserve chronology, not pending implementation work.

Focused independent reviews have regressed quota/install races, false capture
freshness, malformed-job global failure, stale absence completion, discovery sweep
resolution, cancellation masking, directory-wide exclusion suppression and SQLite
aggregate privacy bypass. Exact commands/profiles belong in the
[handoff ledger](session-capture-handoff.md).

An intermediate `make capture-acceptance` passes **seven actual-process journeys**
(**6.813s**, `/tmp/memory-enrichment-cli-integration-4.log`), including periodic B
capture while A quarantines, restart delivery without another hook/source, replay,
compressed subagent capture, protected global/settings/backup scrubbing, WAL backup,
exact installed Claude/Codex synthetic commands and pathless wake, indexed artifacts,
native launch plus closed project snapshot, original-source/Home/spool-independent
restore and actual idempotent import/event index/report.
### Current combined offline acceptance

Final combined acceptance has **eleven actual-process journeys**, including actual
SDK factory/protocol peer → writer → reader → importer after synthetic source/spool
deletion, the existing server entry point consuming only its owned projection after
the source/store/spool/export tree is removed, declared-root moves and three-process
optional failure/crash/restart/repair/revocation. Full check/test/fuzz and all 73
analytics tests now pass; the intermediate seven-journey result above is chronology,
not a list of currently pending repairs. SDK peers use injected in-memory HTTPS transports,
not actual conditional-R2 endpoints.

Operating-resource/freshness acceptance, sandboxed/actual native agents, conditional
R2, and both machines' preserved policy/inventory/installed-path acceptance remain
required. Caller/dashboard defaults are deliberately not activated; activation belongs
to controlled cutover, not proof of candidate caller wiring. No production cutover or
reviewed retired-key deletion is authorized by these offline tests.
