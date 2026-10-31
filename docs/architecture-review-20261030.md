# Architecture, test quality and production-readiness review

**Source baseline:** `b0f949c6a8de76b4a8cd3f0bdf48ae29d44b1026`.
**Status:** source review, focused repairs and offline qualification **complete and
parent-accepted**. Changes are in the working tree, not committed or deployed by this task.
Production/installed operating acceptance remains separate.
The [handoff](session-capture-handoff.md#implementation-continuation--october-30) owns execution,
source-bound checks and operating authority. Pre-existing documentation reconciliation
is preserved. No installed services, original sessions, archives or identities are
part of these source-review experiments.

## Architectural judgment

**Keep the architecture; make focused corrections, not a rewrite.** Go, SQLite,
complete compressed source epochs, conditional R2 publication and a DuckDB consumer
are suitable choices for this product. Their adequacy at the intended operating scale
still needs measured limits and current-source installed acceptance.

| Boundary / data structure | Assessment |
| --- | --- |
| Discovery and hooks → durable capture | Hooks correctly carry hints, not ownership of bytes. Explicit bounded discovery and visible partial coverage are maintainable foundations. |
| SQLite work metadata + private synced spool | Appropriate authoritative pending store. Short transactions, process fencing and independent session progress are preferable to adding another queue/broker. Retention/accounting cost remains material. |
| Complete session manifest, ordered source/epoch entries | Direct reads and preserved rewrite epochs fit the evidence contract. Ordered arrays are useful; boundary identity/evidence validation is essential. Do not add a second persisted model just to remove index operations. |
| Publisher/backend boundary | Small transport interface and conditional current references are appropriate. Keep durability-fence behavior explicit in backend wrappers and preserve stale-job ordering beyond CAS alone. |
| One authoritative reader → language-neutral export | Strong separation: Python must not independently interpret archive history/current selection. Exact identity, body hash and pinned publication remain load-bearing. |
| Analytics normalization → transactionally replaced projection | Correct place for semantic interpretation. Raw preservation and framed-record isolation must not be confused with successful normalization. Shared normalization semantics should not drift between consumers. |
| Runtime work lanes | Raw capture/delivery must remain independent of optional/enrichment network work. Lane identity must survive selection/locking transitions, not just initial scheduling. |
| Operator lifecycle APIs | Nonactivating inspection/teardown correctly differs from readiness to start. Exact command contracts deserve independent tests, not assertions against the generated plan itself. |

Earlier duplicate delivery-result persistence, private evidence joins, inventory compilation,
heterogeneous queue singleton storage, broad dashboard detail, and analytics scope repairs
are already integrated. Do not present the October 29 findings as newly open without
checking current code. Legacy coexistence remains a maintenance cost, but deleting its
working safety coverage before controlled cutover is not an acceptable simplification.

## Findings selected for executable proof

### A1 — Delivery selection can change lanes across a scheduling boundary

Baseline `internal/sessioncapture/runtime_store.go:396–403` checks the requested worker
lane, then calls a helper that independently selects it again. In
`runtime_delivery.go:39–64`, that helper chooses a mutex from one head, then rereads the
head after locking without checking its lane. The immediate delivery path already checks
this invariant in `deliver.go:123–128`.

A concurrent head transition can therefore spend a raw worker's budget on optional
work, or operate under a mutex that does not correspond to the selected work. Both windows
were reproduced against instrumented baseline source using deterministic barriers; this
is not evidence of an observed installed failure.

**Required proof:** deterministic barriers for both windows, no wrong-lane backend
attempt or attempt-budget increment, and subsequent successful delivery through the
correct lane. Preserve the public immediate/due scheduling distinctions.

### A2 — Candidate publication import omits Codex mirror pairing

`events.iter_events` produces both `event_msg.user_message` and
`response_item.message`. The body-index builder pairs those representations by
trace/text/occurrence; the publication importer currently consumes the events without
that operation. The dashboard then counts both as human turns.

**Required proof:** repeated identical genuine prompts with paired and unpaired records,
multiple blocks, exact offsets/duplicate targets, repeat-import idempotence, and no
pairing across source epochs. Share semantic normalization, not a second storage reader
or a legacy migration path. Scope work to the imported traces rather than rescanning the
whole projection for every session.

### A3 — Excessive nesting escapes record-level normalization isolation

The JSON/normalization exception boundaries in `events.py:195–220` do not include
`RecursionError`. A newline-framed deeply nested record can abort a whole session import,
preventing healthy following records from advancing. Raw preservation alone does not
satisfy the consumer's framing/isolation contract.

**Required proof:** actual nested bytes exercising decoder and content-normalization
recursion; healthy following sibling retained; exact issue offsets/lengths, unchanged
owned raw hash and idempotence. Catch recursion only at these boundaries; do not swallow
I/O, database errors, `MemoryError`, or arbitrary exceptions as malformed source records.

### T1 — Crash recovery test can repair away loss of committed work

In `TestRestartAndLostAck`, the baseline permits recapture whenever reopened status is
empty and capture returned an error. That also covers the `job-committed` lost-ack fault,
where the job was already durable. The original remains available, masking loss.

**Required cleanup/proof:** distinguish precommit from committed boundaries; remove the
source before committed-case reopen; require exact retained publication/source/epoch
identity and restored literal input bytes. A targeted postcommit-loss mutation must pass
the old weak assertion and fail the replacement assertion.

### T2 — Supervisor tests prove invocation, not the requested action

`TestServiceStopStartRestartPreserveStateAndPlan` checks additional calls, while
`TestServicePlansEscapeAndApply` compares call count to the production-generated plan.
A wrong stop target or missing Linux `--now` can satisfy those assertions.

**Required cleanup/proof:** test-owned exact ordered platform/action argv, including
Darwin domain/path, Linux reload/enable/disable/restart ordering, and zero dry-run calls.
Keep foreign-plan, activation-drift, preservation and error-redaction negatives. Wrong
stop dispatch and omitted `--now` mutations should distinguish old from stronger oracles.

The sampled suite is **not broadly tautological**. Existing actual writer/reader journeys,
literal restored-byte comparisons, durable reopen tests, privacy negatives and exact
normalization-coordinate assertions are valuable. A roundtrip with an independent input
oracle is not tautological merely because it uses both writer and reader.

## Efficiency: measured decisions, not speculative removal of checks

The review identifies four mechanisms worth measuring:

1. **Unchanged transformed sources:** no cheap discovery fingerprint; materialization
   precedes unchanged checking; prior payload verification and stable-observation hashes
   remain body-scale work. Separate zstd, configuration and SQLite behavior before choosing
   a shortcut. WAL changes and privacy freshness prohibit a naïve stat-only cache.
2. **Retained spool accounting:** `usage()` scans/stats retained payload entries before
   new snapshots. Per-capture work grows with object count, not just byte quota. A counter
   replacement needs a tested accounting/reconciliation invariant; retention is not GC authority.
3. **Due selector:** `LIMIT 64` bounds returned rows, not necessarily SQL work over pending
   revisions. The forced `(next_retry,session,sequence)` index and ordering by session ID
   need a pinned query-plan/scaling probe before changing indexes.
4. **Discovery freshness:** bounded traversal advances once per polling turn absent hints.
   Tree size, entry allowance, root count and cadence jointly determine unhinted latency.
   Increasing burst size can trade freshness against background CPU and isolation.

Two small private prototypes have **passed their correctness checks** in separate admitted
Linux workers, with compilation outside measured payloads. Source manifest:
`51a60eb402240ede64019f728930160add13b5ef3ff5dad5b904ba4c631892f9`.
Parent inspected the prototype source and recorded scalar results.

| Three unchanged calls, about 116–124KiB normalized bytes | Wall ms | CPU ms | Preparation/sync markers |
| --- | ---: | ---: | ---: |
| Raw | 1.614 | 1.648 | 0 |
| zstd | 4.022 | 2.834 | 3 |
| Scrubbed configuration | 6.663 | 5.855 | 3 |
| Closed SQLite backup | 6.675 | 3.952 | 3 |

Exact body/SQL assertions and unchanged publication/epoch/revision/object checks passed;
no new payload-sync or job-commit events occurred. The transformed cohorts still perform
preparation for every unchanged call. These are tiny warm-cache mechanism samples, not
steady-state collector measurements or a statistically established timing ranking. The
SQLite byte oracle uses the same driver's backup implementation through a separate call,
supplemented by independent literal SQL-row assertions.

The selector experiment created **888 actual captures**: eight sessions at 1, 10 and 100
pending revisions each. Sixteen selection queries at cursor zero took **0.729, 1.473 and
13.090ms** respectively; an exhausted cursor returning zero rows took **0.565, 1.343 and
12.557ms**. Expected candidates and quarantine/future-retry/claimed-session controls passed.
The diagnostic plan used the due index, a correlated head subquery, and a temporary ordering
B-tree. The probe's copied SQL was checked byte-identical to production before execution.

**Decision:** due selection measurably scales with pending non-head revisions in this
fixture, even when returning nothing; it deserves a query-shape comparison at realistic
queue distributions. The absolute cost here is sub-millisecond per query. This does not
justify adopting a new index/queue model without testing inactive-history, fairness and
write-cost tradeoffs. Likewise, transform preparation work is proven, but it has not been
shown to explain installed settled CPU. Do not remove integrity/privacy checks or introduce
stat-only caching. No performance production change is accepted from these probes.

Evidence: private `memory-review-20261030/performance/scalar-results.json` and
`jobs/perf-{compile,A,B}-v1/`, including hashes, exact commands and final resource counters.
All three workers exited 0, with no OOM/max events/swap. Admission used the existing shared
VM semaphore; the two runtime payloads each retained their 180-second bound.

Primary-source check: SQLite documents [`INDEXED BY`](https://sqlite.org/lang_indexedby.html)
as a requirement, not a tuning hint; [query-plan output](https://sqlite.org/eqp.html) can
show temporary ordering trees but is not a stable test API. Inspect it as diagnostic
context, not a brittle product assertion. Additional indexes have write/retention costs.

### Implementation continuation: retain the selector

A later same-day source/offline continuation compared existing-index alternatives using
8,104 actual synthetic captures in v1 and 9,704 in v2. Each case/cursor/variant used six
alternating-order rounds of sixteen queries, with an independent ordered-row oracle,
exact production-SQL identity, and future/quarantine/claim controls. Reported medians are
of per-round mean query costs, not latency percentiles. These measurements are SQL-only,
not complete `claimSession` or runtime acceptance.

- Session-ordered head lookup improved the deep-revision case but slowed an inactive-prefix
  case from 42.5 to 745.4µs/query. An unforced distinct-session shape scanned retained history
  under the actual planner and regressed further. Both were rejected.
- The corrected due-index aggregation avoided those severe sparse regressions. Cursor-zero
  medians changed from **635.8 to 423.0µs** for eight heads ×100 revisions, but from **432.3
  to 572.2µs** for 256 heads; small/sparse cases generally cost another 8–11µs. It adds a
  grouping B-tree before the ordering B-tree, with no benefit from grouping one-row heads.
- Grouping all pending heads was faster for deep revisions but changed the all-future case
  from 27.6 to 199.3µs and also slowed many-head selection.

**Parent decision: retain the existing SQL and indexes.** Representative distributions and
runtime budgets do not yet justify selecting one of these workload-specific tradeoffs.
The bounded comparison is complete; no query rewrite, new head cache or additional index
was adopted. Any later adoption still needs actual claim/delivery integration proof.

Evidence: `memory-implementation-20261030/jobs/selector-{v1,v2}/`, with frozen source
bindings, exact commands, plans, samples and resource counters. Both passed their exact
oracles and exited 0 without skips or memory-max/OOM/swap events; nonzero I/O pressure is
retained. The [current handoff](session-capture-handoff.md#implementation-continuation--october-30)
records ownership and acceptance separately from the initial sealed review.

### Implementation continuation: encoded-size admission

The size-boundary prototype established a real acceptance defect: with adequate aggregate
spool capacity, the old writer accepted a 1,073,741,824-byte incompressible source as a
**1,073,766,413-byte encoded object**, which the unchanged R2 transport then rejected.
The scaled reproduction and intended-assertion RED established the same failure before
implementation; compilation failures were not counted as RED.

The released fix uses one shared **1GiB encoded-object limit** at capture admission and
R2 transport. The streaming quota writer checks the encoder's actual cumulative output
before reserving or writing the next block. `ErrEncodedSize` remains distinguishable while
wrapping `ErrQuota`; decoded limits, reader validation, sync/install/commit ordering and
accepted payload retention are unchanged. No public test-only option or new queue state
was introduced. The five changed paths and exact hashes are recorded in
`memory-implementation-20261030/envelope/production-release-v1.sha256`.

The actual production-source full-boundary probe rejected the same incompressible source
before local acceptance, preserving previous job status, source bytes, readable remote
publication and reopened progress. Both baseline and candidate also captured, published
through the actual SDK-backed synthetic disk peer, and authoritatively restored a
compressible source exactly at the decoded 1GiB bound (**114,691 encoded bytes**). This
was offline, not actual R2 qualification or a representative throughput/RSS budget.

The full baseline and candidate were each run once, non-race, with separate 600s compile
and runtime bounds; both exited 0 with no memory-max/OOM/swap events. Scaled proofs and
ordinary package tests retain race/shuffle. Unit tests assert exact-cap acceptance,
cap+1 rejection before reservation/write, coherent aggregate accounting, specific versus
aggregate quota errors, invalid counters and partial-write behavior. Parent and an
independent read-only reviewer found no source blocker; final aggregate qualification is
recorded in the current handoff, not inferred from these prototype results.

## Remaining production-readiness requirements

Passing development gates is necessary but insufficient. Current-source acceptance still
requires the separately authorized Mac → BC-01 → combined journey:

- installed native producer hooks and automatic new-session capture;
- actual conditional R2 writes/readback through the authoritative reader, importer, event
  index and report, with exact identity/epoch/body coverage;
- restart, destination outage/retry without another hook, source absence and effective
  exclusions, while retaining the previous successful publication;
- reconciled two-host inventory, ongoing freshness and explicit partial/quarantine handling;
- representative CPU/RSS/I/O, request cost, source/record-size limits, offline spool horizon,
  metadata growth and retention budgets;
- source-aligned artifacts and controlled final defaults/legacy retirement only after acceptance.

The initial decoded/encoded envelope mismatch is now reproduced and closed for **new
capture admission**, as described above. Previously accepted oversized objects are not
rewritten, invalidated or repaired by the guard; their existing transport failure remains
visible. This is not permission for a migration, cleanup or archive reset, and does not
qualify representative source/record-size or operating resource budgets.

## Initial review evidence and acceptance

Six distinct workers used **`openai/gpt-6.1-sol`, medium effort**, verified by tool status:
four scoped reviewers/implementation owners, one admitted validation owner and one
independent reviewer. Parent read actual code/diffs/prototypes/results, corrected scope
and assumptions, and retains final acceptance. No worker completion alone was acceptance.

### Focused proofs: complete

| Slice | Before / deliberately broken | Corrected result |
| --- | --- | --- |
| Lane selection | Each old-source race window fails with both idle-lane and wrong-lane-attempt assertions; baseline overlay adds only matching barrier hooks | Candidate lane/progress/fault/ACK tests pass race/shuffle at count3; complete sessioncapture and capturedaemon packages pass |
| Committed capture oracle | Targeted lost-ACK job deletion passes old test, fails stronger test at committed-job continuity assertion | All strengthened restart boundary cases pass, with source removed and literal bytes restored |
| Supervisor oracles | Wrong stop dispatch and missing `--now` each pass old tests, fail independent exact argv assertions | Final tests remove the subsumed self-derived/count-only checks; both mutation pairs reproved and package passes |
| Codex mirrors | Baseline importer has zero mirror marks where exact two expected | Forty tests across `test_events` and `test_publications` pass, including scoped pairing, epochs, offsets, repeat import and rollback |
| Framed recursion | Baseline iterator raises real `RecursionError`; baseline importer reports `import_failed` | Healthy sibling retained, two content-free coordinate issues, no model/dedupe contamination, exact raw ownership/hash and idempotence |

The initial recursion fixture **failed** before reaching the system under test: twice
`sys.getrecursionlimit()` did not exhaust Python3.14's C decoder stack. It was not counted
as RED evidence. An admitted bounded subprocess probe established actual decoder failure
at depth 60,000 (120,031 bytes), and successful decoding followed by normalization recursion
failure at depth 512. Corrected tests assert both premises without changing recursion/stack
limits or skipping platforms. Their native/other-architecture premises remain unqualified.
The [CPython stack-protection reference](https://github.com/python/cpython/blob/main/InternalDocs/stack_protection.md)
explains the Python3.14 C-stack/Python-recursion-counter distinction. Failed v1 evidence is
preserved separately from the corrected v2 result.

Parent also caught and corrected an initial test's wrong source-state literal (`present`
versus `active`) and the lane refactor's changed pause/decode ordering. Independent review
approved the corrected lane source and consumer/test invariants, and requested the now-added
one-shot fresh-scope precondition on the shared mirror helper. Existing identical-publication
projections return `unchanged`: an upgrade alone does not re-normalize their prior rows.
No automatic projection rebuild or migration was added.

Private evidence root:
`~/.local/state/machines-validation-pilot/memory-review-20261030/`.
Important records: `LEDGER.md`; `jobs/test-oracles-v1/out/proof-results.json`;
`jobs/latest-go-v2/`; `jobs/lane-combined-v1/`;
`jobs/analytics-depth-probe-v1/`; `jobs/analytics-focused-v{1,2}/`;
`performance/scalar-results.json`. Each contains exact commands/status/source bindings;
expected mutation failures are not product-gate passes. Private Git snapshot metadata is
synthetic and explicitly labeled; original HEAD and dirty source identity are recorded.

### Aggregate qualification: passed; parent acceptance complete for offline scope

The final source snapshot is bound by manifest
`ce9fe805086a5b8f6ec5b84c173491bb16beedcd831163f7805bacf07c6cfc49`.
Parent compared 610 code/test/tool/build files against it with zero drift, and separately
recorded the ten changed code/test file hashes in `parent-source-review.json`.
Subsequent documentation-only evidence updates do not change this code qualification.

| Final gate | Actual result |
| --- | --- |
| Canonical `make check` aggregate, not a prerequisite union | **Exit 0**, worker 665.794s; format, lint, fix/tidy, deadcode, vulnerability scan, full race/shuffle coverage, CRAP, benchmark smoke, actionlint and actual consumer journeys |
| Coverage | **87.5% (13,973/15,976)**; unchanged package/total thresholds pass |
| CRAP | Maximum **29.40**, unchanged ceiling 30 |
| Mandatory consumer/launcher stages | Actual writer/reader/import/index/report journeys 12/12; R2 launcher safety 6/6; delivery launcher 12 discovered with **one explicit SDK-peer-only skip** |
| Full analytics, freshly built current-source binary supplied | **122 tests, zero skips**, 29.230s, exit 0 |
| Parent final diff/documentation checks | `git diff --check` passes; relative documentation links/anchors resolve |

Inner commands, run only inside the admitted pinned-Nix Linux worker (not bare host):

```sh
make check COVER_DIR=/work/tmp/canonical-cover \
  ANALYTICS_PYTHON=/analytics-env/venv/bin/python \
  TOOL=/recipe/reference/offline-tool-adapter.sh

CGO_ENABLED=0 go build -o /work/tmp/agent-sessions-current ./cmd/agent-sessions
CAPTURE_V1_BINARY=/work/tmp/agent-sessions-current PYTHONDONTWRITEBYTECODE=1 \
  /analytics-env/venv/bin/python -m unittest discover \
  -s tools/analytics -p 'test_*.py' -v
```

The adapter preserves pinned tools/flags and supplies the sealed offline vulnerability
DB; it does not skip any prerequisite. Source, dependencies and runtime were read-only;
both temporary paths used executable VM-native EXT4; actual validation was network-none
under the shared worker-side semaphore. Go's 600s/package/race/shuffle and all original
coverage/CRAP/fixture/history gates were retained. Canonical worker peak 5,546,336,256 bytes;
full analytics peak 2,521,755,648 bytes; both max/OOM events and swap zero. Earlier Medium
cache seeding reached its 4GiB cap without OOM; those pressure events remain in evidence.

An existing HTTPError403 cleanup `ResourceWarning` appeared in the dashboard journey;
it was not suppressed and these results are not claimed warning-free. No separate explicit
SDK-peer launcher rerun is implied by the skipped test. The actual SDK-backed publication
consumer journey did run; it is a different test.

Final logs/results are `jobs/make-check-final-v1/` and
`jobs/analytics-full-final-v1/`. Parent inspected the actual exit markers, coverage/CRAP,
counts and source checks rather than accepting only the worker's completion statement.
The parent independently verified all **10,739 sealed evidence files (78,675,454 bytes)**,
with zero hash mismatches. `evidence.sha256` digest:
`819d57cab989a10fd72866eaefbb068f3f785e1232806a266969be006d7abf7b`.
No review-owned containers remain; private synthetic output volumes/evidence are preserved.
The architecture and corrective changes are accepted **only for this offline/source scope**.

No new native Darwin, hosted CI, timed fuzz campaign, actual R2, installed native-agent,
two-host inventory, activation, release, reset/deletion or legacy cutover gate was run.
This review samples the core capture/publication/consumer/operator boundaries; it is not
a formal line-by-line security audit of every legacy adapter and historical test.

## Implementation continuation: final acceptance

Four verified **`openai/gpt-6.1-sol`, medium-effort** workers handled the envelope,
selector comparisons, sole admitted validation execution, and independent read-only
review. Parent accepted the minimal envelope fix and rejected the measured selector
tradeoffs; worker completion alone was not acceptance. All five released implementation
hashes match the reviewed source. The new clean `production-v1` manifest is
`d79c4ea8ad43055c76c8876425c570e4cdffc22b59d7645226fd54317ea5288d`.
Parent independently compared 614 non-documentation files with zero drift; private
prototype tests are absent from the clean qualification snapshot.

| Continuation gate | Actual result |
| --- | --- |
| Affected packages | `sessioncapture`, `publication`, `publicationr2`: race/shuffle, count1, 600s/package; exit 0 |
| Pinned formatting/scoped lint | Exit 0, zero lint issues |
| Literal canonical `make check` | **Exit 0**, 779.624s within its 1800s worker bound |
| Coverage / CRAP | **87.5% (13,979/15,980)** / maximum **29.40**; original thresholds unchanged |
| Mandatory consumer/launcher stages | 12 + 6 + 12 tests discovered; existing explicit SDK-peer launcher skip retained |
| Full analytics with freshly built actual-source binary | **122 tests, no skips**, 28.369s, exit 0 |

The canonical and analytics commands and provider-only offline adapter shown above were
reused without weakening prerequisites. Source/runtime/dependencies remained read-only,
network-none, with native EXT4 temporary storage and the shared worker-side semaphore.
The analytics worker waited for shared capacity rather than bypassing admission. Canonical
peak was 5,265,522,688 bytes; analytics peak was 1,130,504,192 bytes; no memory-max/OOM/swap
events. Nonzero system pressure and the existing HTTPError403 cleanup warning are retained.

Evidence is separate from the initial sealed review:
`memory-implementation-20261030/jobs/production-{focused,fmt-lint,make-check,analytics-full}-v1/`,
plus the source-bound envelope and selector results described above. One setup-only
assertion failure in preparation of the stronger candidate error-type probe was retained:
no payload ran. Corrected scaled v2 proves actual `Capture` returns both `ErrEncodedSize`
and its `ErrQuota` classification; the already-passing full-size candidate was not repeated.

**Accepted only for source/offline scope.** Changes remain uncommitted. Prior work and all
original/live state are preserved. No new CI, native/other-architecture, timed fuzz,
installed-host, actual-R2 or two-host qualification was run. Existing oversized accepted
objects and operating workload budgets are not repaired or qualified by this new-admission
fix. No activation, release, reset/deletion or legacy cutover occurred.
