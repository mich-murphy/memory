# Holistic architecture and production-readiness review

**Reviewed:** 2026-10-29, `main` at `4c9aca5c5cb178316e9eed32626dbf56f7dbaac9`.
**Scope:** architecture, abstractions, APIs, contracts, data structures, maintainability,
resource behavior, tests and operational readiness. Implementation was not changed.
Five focused reviewers ran `openai/gpt-6-sol` with medium thinking; the parent reviewed
findings against source, challenged overclaims, and ran the checks below. A sixth read-only
Sol/medium reviewer checked the synthesis; its endpoint-rebinding correction is incorporated in R3.

## Executive judgment

**Keep the direction. Do not rewrite the project or introduce another framework.**
The session-local spool → complete publication → authoritative reader → analytics
architecture is appropriate. The main problem is not the choice of Go, SQLite, R2 or
DuckDB. It is the number of places that independently interpret work state, identity,
policy and health, plus the temporary coexistence of two capture architectures.

The candidate is substantially implemented and has meaningful end-to-end tests. It is
**not production-ready**: installed acceptance remains blocked, two analytics health
errors are reproducible, and representative resource/freshness/retention budgets remain
unqualified. Passing offline tests does not settle these points.

The highest-value work is:

1. Repair and qualify the known source-absence failure boundary before lifting containment.
2. Correct analytics scope/disappearance accounting; retain prior successes while exposing gaps.
3. Consolidate delivery transitions and strengthen the private source/evidence boundary.
4. Reuse immutable inventory declarations, not stale authorization decisions.
5. Measure and bound due-work selection and analytics serving/import work.
6. Complete installed acceptance and cutover, then remove the legacy architecture.

**A smaller codebase should result from fewer competing models, not fewer safety checks.**
Do not collapse independent progress facts, delete optional isolation, add legacy migration,
introduce chunk history, or build a generic job framework to achieve a line-count target.

## 1. Product need and design rationale

The product is dependable capture and analysis of Claude Code, Codex and Pi evidence
from this Mac and BC-01. Upload success is only one intermediate fact. The useful result
is readable evidence that reaches a correctly identified event index and report.

The original v3 design (`docs/archive-design.md`) promised reconstruction of every
committed file generation and intermediate representation, with machine-wide recovery
and clean/dirty checkpoints. Its complexity was partly a consequence of that promise.
The replacement deliberately changes the promise:

- Sessions are independent publication/read/failure units.
- Retain the latest complete captured contents of each source epoch. Proven append
  advances it; an unproven rewrite preserves the previous epoch rather than discarding evidence.
- Raw safe capture does not wait for semantic normalization or optional enrichment.
- SQLite owns accepted pending metadata; synced private payloads own captured bytes.
- Delivery is at-least-once and idempotent, with guarded complete manifests.
- One Go reader selects and verifies publications; Python consumes pinned exports.
- Source absence is not archive deletion; exclusion is not merely a best-effort filter.

This is a materially simpler contract. The current candidate generally implements it.
Restoring v3 recovery promises would undo the simplification rather than finish the product.

## 2. Architecture worth keeping

| Boundary | Responsibility | Review judgment |
| --- | --- | --- |
| Adapters, `capturesource`, hooks | Discover bounded source work and native context | Keep discovery separate from byte ownership. Hooks are hints, not durable capture. |
| `capturecoordinates`, `capturepolicy`, exclusions, safe-file handling | Identity, declared aliases and authorization | Load-bearing. Physical path, logical path and restrictive identities are not interchangeable. |
| `sessioncapture` | Durable local acceptance, ordering, retries, optional repair/enrichment | Correct home for local authority; its state/API boundaries need the most cleanup. |
| `publication` | Complete manifests, ordering/CAS, pinned read/export | Strong boundary; do not move selection/reconstruction into Python. |
| `capturebackend`, `publicationr2` | Explicit destination construction and transport semantics | Small, useful boundaries. Keep lazy credentials and typed transport failure semantics. |
| Analytics importer/projection/refresh/dashboard | Normalize, preserve prior success, report coverage/freshness | Correct separation from storage resolution; health scope and bounded work need improvement. |

Examples of good data structures already in use:

- `publication.advances` indexes sources by effective source identity and epoch, avoiding
  nested matching and preserving withheld identities (`internal/publication/publication.go:379–395`).
- `map[Object]bool` uses the entire immutable descriptor for trusted-object reuse, not
  merely a filename or timestamp (`publication.go:410–447`). Reads still verify bodies.
- Content-addressed enrichment asset lists remove repeated large lists without removing
  per-result integrity checks (`internal/sessioncapture/enrichment_assets.go`). This is a
  measured data-model improvement, not speculative caching.
- SQL transactions, stable publication IDs, guarded claims and complete manifests are
  simpler authorities than a new message broker, distributed coordinator or event-sourced history.

### Scale of the checkout

Tracked Go files contain **33,277 non-test lines and 46,207 `_test.go` lines**. These are
physical lines including comments/blanks; non-test totals include fixtures/test helpers.
They are orientation, not quality scores or promised deletions.

| Package | Non-test Go lines | Test Go lines |
| --- | ---: | ---: |
| `sessioncapture` | 6,857 | 8,901 |
| `cli` | 3,462 | 4,041 |
| `capturesource` | 1,665 | 1,456 |
| `publication` | 1,570 | 1,857 |
| `publicationr2` + `capturebackend` | 637 | 2,279 |
| Legacy `syncer` | 4,825 | 11,056 |
| `state` + `store` | 2,127 | 3,198 |

Legacy retirement is a large eventual reduction, but not every shared dependency or
security assertion in those areas can be deleted with it. Nor does retirement simplify
all 6,857 lines of candidate queue/runtime logic automatically.

## 3. Prioritized findings

Priority means recommended delivery order, not CVSS severity. **P1** is a release or
operational-correctness concern; **P2** is a focused maintainability/resource improvement;
**P3** is a small cleanup or conditional hardening. Static risks are not presented as
observed production failures.

### R1 — P1: error disposition still depends too much on where an error escapes

**Known release blocker, not newly discovered:** the handoff records both capture
services disabled for source-absence containment, with a repair outside `main`.
`runtimeFatalCause` treats any `os.PathError`/`os.LinkError` as fatal
(`internal/sessioncapture/runtime_fatal.go:16–29`), while the worker treats errors that
escape item bookkeeping as run-level errors except a narrow transient set
(`runtime.go:224–245`). A missing authorized original source and a broken durable store
must not become indistinguishable merely because both use filesystem errors.

**Action:** review the existing source-absence repair, rather than write a second one.
Classify absence at the authorized source boundary. Missing exclusions/policy, spool
failures, durability errors and mixed fatal error trees must remain fail-closed.

**Acceptance:** disappearance after discovery does not stop healthy B; no captured bytes
or watermark are fabricated; missing policy/spool still stops safely; mixed errors retain
fatal causes. Run the unchanged gates on the combined source, then the authorized installed
sequence. This review neither examined nor accepted the external repair and did not lift containment.

### R2 — P1: a completed remote sweep can hide loss of a known current root

`publication_refresh.sweep` collects `seen`, processes heads/failures, then marks the
endpoint complete without reconciling previously known targets that were never seen
(`tools/analytics/publication_refresh.py:237–282`). The prior imported target remains
error-free. Thus a completed enumeration can refresh endpoint health while a known
publication is no longer discoverable.

**Reproduced:** using the existing synthetic refresh fixture, import A+B on two endpoints,
then return an empty completed remote-current inventory for one endpoint. Result:
`expected_targets=4`, `endpoint_errors=0`, `target_errors=0`, `unimported=0`,
`incomplete_endpoints=0`.

**Minimal fix:** after a *complete remote-current* sweep only, mark existing unseen
coordinates `current_missing`, using bounded database iteration and the existing `seen`
set. Include reported unreadable heads in `seen` so their specific failure survives.
Keep prior projection rows and successful coordinates. A never-published orphan namespace
is not itself a missing publication; skipping such a namespace is reasonable.

**Acceptance:** A disappears, B advances, A remains readable from the prior local projection
but coverage is red; partial enumeration does not imply absence; restored A clears the
error. Do not apply remote-root disappearance semantics indiscriminately to queue discovery.

### R3 — P1: active endpoint scope and target scope disagree

`bind_endpoints` retains removed endpoints as inactive history, but both CLI and dashboard
health aggregate all `targets`. Only `head_failures` are filtered to active endpoints
(`publication_refresh.py:182–225`; `publication_dashboard.py:86–122`). This can retain an
irrelevant error or inflate coverage after reconfiguration.

**Reproduced:** after one target fails on an endpoint and that endpoint is removed,
status reports one active endpoint but four expected targets, one target error and one
unimported target; the remaining endpoint has only two successful targets.

**Minimal fix for removal:** use the same active-scope join for target counts,
expected-target unions and error counts in both consumers. Retain inactive history explicitly.
A small shared status-summary query/function is preferable to two evolving definitions of health.

**Related binding gap:** repointing a same-named endpoint changes its fingerprint and resets
endpoint completion, but retains its targets. An active-name join alone does not distinguish
old-binding targets from the new destination. Associate active target expectations with the
current endpoint binding, or explicitly retain this as an unresolved case; do not call the
removal-only fix complete reconfiguration support.

**Acceptance:** remove/re-add and repoint a same-named endpoint; history remains inspectable
without counting omitted or old-binding targets as current coverage. Keep the independent
status-store boundary; this does not require cross-database qualification on every dashboard GET.

### R4 — P2: explicit and autonomous delivery duplicate state transitions

`Deliver` uses `deliverAccepted`/`recordFailure`/`acknowledge`
(`internal/sessioncapture/deliver.go:68–147,253–268,421–428`). Runtime delivery uses
`deliverDueWork`/`runtimeDeliveryFailure` and its own acknowledgement update
(`runtime_delivery.go:26–119`). The former can persist `integrity-error`/`policy-blocked`;
the latter uses retry/quarantine, destination pause and explicit shutdown accounting.

The scheduling distinction is legitimate: an operator can request an immediate attempt
while the daemon respects due time. Two definitions of what an attempt *means* are not
an equally useful abstraction. Fixes can land in one path without reaching the other.

**Action:** converge attempt-result/ACK persistence and guarded transitions, keeping
immediate versus due-only selection explicit. Do not build a generic workflow engine or
force capture, optional repair and enrichment into one undifferentiated queue.

**Acceptance:** paired tests through both APIs for cancellation, timeout, policy denial,
integrity failure, destination pause, lost ACK and superseded jobs. Preserve and document
any intentional differences in replay behavior. Success should remove transition branches
and duplicate SQL, not merely distribute them among more helper files.

### R5 — P2: private evidence is joined to sources by array position

Capture/selection/delivery pass a `Manifest` and parallel `[]evidence`. Delivery checks
length, then applies `es[j]` to `m.Sources[j]`
(`deliver.go:270–299,303–351`; `enrichment_selection.go:193–202`).
`encodeAcceptedView` validates/marshals the manifest and separately marshals evidence,
without a general pair-identity check (`capture.go:504–524`). Withheld rehydration has
strong commitment checks; that does not make every positional association self-validating.

This is a **maintenance and defense-in-depth risk**, not a demonstrated current privacy
bypass. A same-length permutation can look structurally valid while joining the wrong
policy evidence to an object. Every mutating loop must remember the hidden join invariant.

**Smallest improvement:** validate source/evidence correspondence at accepted-job
encode/decode boundaries, including session/source identity and the applicable retained
or withheld commitment. Add same-length swapped-evidence regressions.

If this substantially simplifies a touched slice, use an ephemeral paired value such as
`{Source, Evidence}` internally and split once at the existing persistence/wire boundary.
Do **not** persist a third authoritative representation or replace ordered wire arrays
with maps. Adopt the paired form only if the actual diff removes repeated index handling;
a type wrapper with more conversion code is not a win.

### R6 — P2: immutable inventory compilation is mixed into fresh policy checks

Queue options and source options compile coordinate inventories independently
(`internal/cli/capture_runtime_sources.go:12–55`). Alias policy helpers compile again
(`capture_runtime_alias_policy.go:22–53`), and `Config.AllowsSource` can compile during
per-path checks (`internal/capturedaemon/config.go:215–225`). Compilation validates and
sorts declarations (`internal/capturecoordinates/coordinates.go:69–99`). These declarations
come from the loaded config; repeated compilation is not fresh filesystem authorization.

**Action:** construct one runtime inventory from the loaded config and pass it to discovery,
queue and policy callbacks. Keep this composition small; the CLI should assemble it rather
than independently implement domain decisions in more closures.

Reuse declarations only. Filesystem resolution, exclusions, asset policy and pre-upload/
pre-CAS checks must retain their freshness boundaries. Existing per-call path deduplication
and `NewSet` reuse are good precedents, not permission to cache policy across sweeps.

Related API cleanup: `root:`/`adapter-session:` association strings feed ownership logic
(`capturesource/classify.go:148–163`; `sessioncapture/ownership.go:51–100`). Distinguish
validated internal ownership claims from descriptive publication associations at the queue
boundary. Keep validation of forged/duplicate/conflicting claims; do not trust hook metadata.

**Acceptance:** alias moves, overlapping roots, old-root opt-outs, symlink changes and forged
ownership retain existing behavior; a counter proves one compilation per runtime construction.
Measure before claiming a throughput improvement. This is not a reason to merge all path types.

### R7 — P2: due-work selection traverses retained history instead of only runnable work

`claimSource` fetches pages of up to 64 source rows and filters states in Go;
`claimSession` pages retained sessions and performs a job-head query per candidate
(`internal/sessioncapture/runtime_store.go:182–225,315–365,400–422`). Workers revisit
selection on a timer of at most 100 ms (`runtime.go:224–245`). The queries are bounded per
turn, but finding a small amount of runnable work can depend on large inactive inventory.

**Action:** measure SQL calls, idle CPU and due latency with a mostly inactive representative
inventory. If material, let indexed SQL select runnable work in bounded pages while preserving
rotation/fairness, same-session ordering and claim guards. Prefer a due-work predicate/index
over an additional in-memory queue requiring its own recovery/reconciliation model.

Separately, `usage()` scans all retained payload metadata before snapshots
(`capture.go:40–70,102–105`). This is documented and protects accounting/integrity.
Do not replace it with an unexplained cache; measure it separately and change it only with
a tested accounting/rebuild invariant. Neither cost was newly measured against live data here.

### R8 — P2: analytics APIs materialize more data than an overview needs

`publications.report` loads all sessions, failures and sources into Python lists on every
call (`tools/analytics/publications.py:375–410`). The HTTP report invokes it per request
(`publication_dashboard.py:142–149`). A database memory setting does not bound the Python
objects or serialized response. Paginated exploration elsewhere does not bound this route.

The importer also executes an insert per event/issue (`publications.py:187–190,318–347`),
and unchanged refresh sweeps hash every owned selected body
(`publication_refresh.py:321–385`). These are straightforward implementations, but their
costs need explicit production budgets.

**Action:** separate a bounded dashboard summary from detailed/export reporting; calculate
counts in SQL and expose details through bounded pages. Preserve exhaustive offline reports
where needed. Batch normalized rows in bounded groups inside the existing per-session
transaction only if a benchmark shows benefit. Do not add an incremental event-reconciliation
engine merely to avoid replacing one session projection.

**Acceptance:** exact totals and failure visibility, bounded response/RSS, stable pagination,
rollback and idempotence on writer-generated data. Measure sweep bytes and duration before
changing body verification. Large JSONL lines (`events.py:187–205`) also need a measured
normalization budget: keep raw evidence and expose partial normalization if over budget.
These are source-proven growth properties, not claims that current live memory is exhausted.

### R9 — P1 operational clarity / P2 cleanup: dated state has multiple contradictory owners

The beginning of `README.md` records containment, then lines 32–55 describe superseded
private failures as current and say no full-development pass exists. `docs/README.md`
contains old pickup guidance; the contract header describes older installed state and
unknown health. These conflict with the handoff's latest qualifying gates and disabled
services. `docs/install.md` also describes candidate lifecycle as not installed despite
recorded experimental installations.

**Action:** keep requirements in the contract, interfaces in implementation references,
dated evidence in dated records, and current deployment/gates/next action in one compact
handoff section. Other documents should link there instead of copying operational status.
Archive chronology rather than repeatedly appending it to the current brief.

The handoff calls itself compact but had 1,007 lines before this review. Human accessibility
is harmed by that navigation burden even when every historical note is accurate. Do not
rewrite frozen evidence; remove duplicated *current guidance*, not history.

**Acceptance:** a new maintainer can answer what is built, installed, enabled, qualified and
next without reconciling several incompatible narratives. Clearly distinguish binary install,
legacy hook install, experimental activation and final cutover.

### R10 — P3: two small internal representations create needless invalid states

- `Queue.sessions` normally contains mutexes, but `runtimeLocal()` inserts a different
  type under a reserved string and type-asserts/panics (`queue.go:106–118,220–230`;
  `runtime.go:56–75`). `derivedReservations()` similarly stores special state in `installs`
  (`derived.go:35–47`). Use typed queue fields with explicit/lazy initialization. Audit
  read-only `&Queue{}` facades. Do not add a dependency-injection container.
- A malformed persisted enrichment spec calls `finishEnrichment` without advancing
  `attempts` (`enrichment_worker.go:32–35,69–78`), so it cannot reach the retry ceiling.
  Normal admission serializes validated specs, so this is defensive corruption handling,
  not an observed normal-path outage. Choose explicit fail-closed or safely isolated
  quarantine behavior; do not endlessly label an undecodable authoritative row retryable.

Do not combine this with speculative lock-map eviction. Removing a mutex while it is held
or awaited can break serialization; typed singleton fields are the clear low-risk reduction.

## 4. Capacity and retention are acceptance decisions, not incidental cleanup

The queue explicitly retains payloads, intermediate jobs and crash orphans; acknowledgement
marks a job published but does not reclaim its bytes (`queue.go:1–24`; `deliver.go:421–428`).
Finite quotas therefore bound consumption by eventually refusing new acceptance, not by
providing indefinite unattended retention. Full snapshots and growing retained epochs also
meet the manifest's 1,024-source cap and metadata limit.

This is intentional conservative behavior, not permission to delete data or bolt on GC.
Production acceptance must state the supported capture/offline horizon, source-size and
session-shape envelope, spool headroom, discovery/delivery/import freshness and recovery
procedure when a limit is reached. Measure these in the actual validated vertical slice.

If that operating envelope is insufficient, design local reclamation separately using
pending/retained roots, sealed epochs, optional-repair assets and in-flight work. Keep remote
retention/deletion separate. Do not silently discard accepted evidence, change the wire
contract, add chunking or resurrect an upload-ACK bitmap in the name of optimization.

## 5. Deliberate non-recommendations

Several initially plausible cuts did not survive review:

- **Do not fold raw and optional work into one blocking lane.** Withheld artifacts,
  verified-before-attachment checkpoints and independent repair preserve raw progress.
- **Do not call every malformed private repair row a harmless poison message.** Its
  identity/evidence may be authoritative. Isolation requires an independently validated
  coordinate; local corruption may correctly stop the coordinator.
- **Do not count attachment retries as failed uploads automatically.** The durable
  verified checkpoint intentionally avoids re-upload; transient local failures already retry.
- **Do not impose blanket metadata equality between source revisions.** Append proof is
  caller-owned, and paths/selection/provenance can change. Specify any epoch-stable role
  rule before adding it; no role-transition defect was established here.
- **Do not verify the entire projection on every dashboard GET.** Refresh health is
  intentionally independent and declares `archive_coverage_claim=false`. Clarify the UI
  label even when green; do not restore removed qualification scaffolding.
- **Do not create a second fixture executable solely to remove hidden acceptance commands.**
  The handoff explicitly accepted keeping them to exercise the real binary without a
  second harness. Their mere presence is not a demonstrated production defect.
- **Do not weaken tests or replace writer-generated journeys with handwritten importer fixtures.**
  Retire legacy-only tests with legacy code, not ahead of replacement safety coverage.
- **Do not introduce enums, interfaces, small files or maps everywhere by rule.** Keep ordered
  collections where order matters; use maps for genuine identity lookup and typed fields for
  genuinely heterogeneous state. Require a simpler resulting control flow, not just more types.

## 6. Focused delivery sequence

| Order | Work | Done when |
| --- | --- | --- |
| 1 | Existing source-absence repair review; analytics R2/R3 fixes; documentation status cleanup | Error scope is explicit; both reproduced health cases fail before/pass after; current operational guidance has one owner. |
| 2 | Small internal simplifications: R10 typed fields; R6 compiled inventory; R5 correspondence validation | Existing privacy/identity checks unchanged; no new authoritative model; focused invariant tests pass. |
| 3 | R4 delivery transition convergence | Immediate/due scheduling differ deliberately but share result persistence; paired API tests demonstrate equivalent safety. |
| 4 | Representative resource slice, then only justified R7/R8 optimizations | Due latency, report size/RSS, import throughput, sweep I/O and retention horizon fit stated budgets. |
| 5 | Qualifying full gates and installed Mac/BC-01/R2 acceptance | Fresh native captures, restart/retry, exclusion negatives, restore and analytics work on both installed paths; required autonomous observation completed. |
| 6 | Controlled cutover and retirement | Inventory reconciled, old writers fenced, deletion set reviewed, new reads rechecked; default operator path is the new one. |

After accepted cutover, remove v3-only sync/recovery, CLI commands, legacy storage layouts
and obsolete default analytics refresh paths as a coordinated deletion. Trace imports before
removing `state`, `store`, `hook`, `launch`, `snapshot`, `raw` or adapters: some behavior is
shared. Preserve identities, learned roots, exclusions, original sources and local research.
Do not make migration compatibility a prerequisite.

A useful acceptance criterion for each simplification PR: it removes a second interpretation,
state transition or repeated join; a reader can follow the happy path and failure boundary
more directly; correctness tests remain at least as strong. No percentage LOC-reduction
promise is warranted before those diffs exist.

## 7. Verification, provenance and limitations

All fresh checks below used the pinned Nix environment on the reviewed implementation;
only review documentation changed in the tracked checkout. Live opt-ins were unset.

1. **PASS:**
   ```sh
   nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
     PYTHONDONTWRITEBYTECODE=1 make capture-acceptance \
     ANALYTICS_PYTHON=/Users/mm/dev/memory/.analytics-venv/bin/python
   ```
   Actual process journeys **12/12**, candidate live-launcher safety tests **6/6**,
   delivery-launcher safety tests **6/6**. Journeys took 30.048s.
   Log: `/tmp/memory-review-capture-acceptance.log`.
2. **PASS, with skips explicitly recorded:**
   ```sh
   nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
     PYTHONDONTWRITEBYTECODE=1 /Users/mm/dev/memory/.analytics-venv/bin/python \
     -m unittest discover -s tools/analytics -p 'test_*.py'
   ```
   **97 tests discovered, 12 skipped**, 15.283s. The skipped process journeys require
   `CAPTURE_V1_BINARY`; they ran separately in check 1, not in this invocation.
   Log: `/tmp/memory-review-analytics.log`.
3. **PASS:**
   ```sh
   nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
     CGO_ENABLED=1 go test -race -shuffle=on -count=1 \
     ./internal/publication ./internal/publicationr2 ./internal/capturebackend \
     ./internal/capturesource ./internal/capturepolicy ./internal/capturecoordinates \
     ./internal/capturedaemon ./internal/capturehooks ./internal/sessioncapture ./internal/cli
   ```
   All ten packages passed; `sessioncapture` 99.079s, `cli` 36.645s.
   Log: `/tmp/memory-review-go.log`.
4. **Two defects reproduced**, not acceptance tests: `/tmp/memory-review-probes.py`
   reused `test_publication_refresh.RefreshTests` with mocked reader transport/import.
   First, set both endpoints to `discovery='remote-current'`, run a successful sweep,
   empty `heads['Mac']`, sweep again. Second, fail `('Mac','a')`, sweep, remove Mac
   from endpoints, sweep again. Outputs are recorded in R2/R3. Run with:
   ```sh
   nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
     PYTHONDONTWRITEBYTECODE=1 /Users/mm/dev/memory/.analytics-venv/bin/python \
     /tmp/memory-review-probes.py
   ```
   This demonstrates status logic, not actual R2 deletion behavior. Temporary scripts/logs
   are supplementary; the scenarios above are sufficient to recreate them.

**Not run:** complete `make check`, full `make test`, timed fuzz campaigns, new full coverage/
CRAP, native installed acceptance, live R2, BC-01 inspection, release/install/cutover/deletion,
representative live resource measurements. Historical qualifying gates on `bf99752` are
recorded in the handoff, not rerun or upgraded to release evidence by this review.

Review coverage spans the capture/publication/consumer journey and legacy/build/operator
boundaries. It is not a claim of a line-by-line formal security audit of every adapter,
scrubber, test and historical implementation. Existing external worktrees, private transcripts,
credentials and frozen untracked research were not changed or used as review output.

**Conclusion:** finish the existing architecture by reducing duplicated authority and making
its limits explicit. The right next step is a small, ordered set of corrective and simplifying
changes—not another redesign, and not a declaration that passing offline tests makes it ready.
