# Adversarial review: dependability and unjustified complexity

**Reviewed source:** `3ea6752599ca5ea59d1c26d08969e92043b3e04b`, branch
`delivery/fresh-archive-qualification`, 2026-10-29.
**Scope:** existing candidate implementation and its production entrypoints against
[the replacement contract](session-capture-contract.md), not just the latest diff.
Five domain auditors and a sixth independent synthesis challenger used
**`openai/gpt-6.1-sol`, medium thinking**. Parent independently inspected findings and
reran the strongest synthetic reproductions. The challenger upheld R1–R9 with the
proof limits below; the parent accepted the bounded-input R10 from source/probe review.
Production code was not changed. Findings are not claims about observed incidents on
installed hosts.

## Judgment

**The direction is sound; the implementation is not yet dependable enough to accept.**
Keep independent session capture, durable SQLite/spool ownership, complete manifests,
guarded publication and one authoritative reader. A rewrite would add risk, not solve
these defects.

The most damaging remaining complexity is duplicated interpretation:

- two analytics stores erase failure evidence before replacement success is durable;
- the launcher contradicts the coordinator's destination-independence boundary;
- optional workers reinterpret shutdown differently from capture/delivery;
- alias policy rediscovers ownership using a legacy whole-project scan;
- delivery performs the same full immutable-payload verification twice.

These are not cosmetic issues. They create false health, lost failure evidence,
quarantined healthy work, unstable source identities and wasted delivery budgets.
**Delete redundant work and interpretations, not privacy checks or durability checks.**

Priority here is delivery priority: **P1** blocks dependable acceptance, **P2** is a
concrete correctness/resource repair, **P3** is a lower-frequency bounded-input hole.
No P0, production disclosure, or remote data-loss incident was established.

## Findings

### R1 — P1: the installed launcher makes destination credentials a local-capture startup dependency

**Code:** `tools/capture-delivery/launch.py:68–69`; contrast
`internal/cli/capture_backend.go:16–32,47–49`.

The credential-scoped wrapper eagerly opens/parses the credential file for
`capture-v1 run` before executing the coordinator. A missing, unreadable or malformed
file prevents **all** coordinator startup and local byte capture. Hooks can still
accept hints, but a pathname hint is not captured evidence. The core deliberately
constructs the R2 destination lazily so authentication/network failure can pause
publication without stopping local capture; the prescribed installed entrypoint
undoes that guarantee.

**Evidence:** a synthetic executable placeholder was validated but never executed.
With the same missing credential file, actual launcher argument construction accepts
`hook` and raises `FileNotFoundError` for `run`, before `execve`. Parent reproduced it.
No actual installed service was started or stopped. This is a startup/restart failure,
not a claim that removing the file stops an already-running, credentialed coordinator.

**Smallest correction:** let coordinator startup/local capture proceed when explicit
destination credentials are unavailable, while retaining strict credential-file
validation, content-free diagnostics and destination pause. Do not fall back to ambient
credentials. Define the repair/restart/resume behavior explicitly: credentials copied
into a process environment do not magically update when the file changes. This does
not require a new credential framework.

**Regression:** exercise the actual wrapper into the coordinator with missing,
malformed and later-repaired synthetic credentials; prove durable local acceptance,
visible destination pause and eventual delivery. Hooks-only credential tests are insufficient.

### R2 — P1: replacing an importer failure can erase the last durable failure

**Code:** `tools/analytics/publications.py:404–414`, especially `411–413`;
report consequence at `440–447`.

On a failed newer import, the handler autocommits `DELETE FROM failures`, then inserts
the replacement in a separate statement. An interruption or replacement-write failure
between them destroys the outstanding error. The previous successful publication
survives, but can now be reported as non-stale with no failure.

**Evidence:** actual synthetic DuckDB import/report: import recent publication `p1`,
fail `p2`, then fail `p3` and inject interruption immediately before the replacement
failure INSERT, after the real DELETE. Reopened report:
`failures=0`, `publication=p1`, `stale=false`. Parent independently reproduced.
This is a real committed persistence boundary with injected interruption, not a
SIGKILL/power-loss campaign. Independent refresh status may still expose its own error;
that does not repair the direct importer/report contract.

**Smallest correction:** one atomic failure-watermark UPSERT or a short transaction
covering replacement. Preserve monotonic failed target/attempt semantics and keep the
old failure if replacement cannot commit. Do not merge the two consumer databases.

**Regression:** interrupt failure replacement, reopen, assert at least the prior
failure remains and the old success is visibly stale. Also test older retries against
a newer outstanding failure.

### R3 — P1: retry registration can turn the dashboard green before anything is repaired

**Code:** `tools/analytics/publication_refresh.py:442–454`, especially `445–448`;
`tools/analytics/publication_dashboard.py:108–122`.

`_target` clears and commits `error=NULL` when registering an expected target,
**before** projection verification or reader/import success. A previously imported
same-coordinate target still has matching imported fields. If retry is interrupted,
those fields plus the recent previous completed-sweep timestamp make health green.

**Evidence:** one synthetic endpoint/target succeeds, then exporter failure at the same
publication makes actual dashboard health unhealthy. Interrupt retry at entry to
`_projection_coordinate`, close/reopen status, call actual `refresh_health`:

| Observation | Before retry | After interrupted retry |
| --- | --- | --- |
| `healthy` | false | **true** |
| `target_errors` | 1 | **0** |
| `stale` | false | false |
| `last_completed_sweep_ns` | prior completion | unchanged |

Parent independently reproduced. Transport/import, including the initial successful
import, use the existing mocked scheduling fixture; SQLite persistence and dashboard
health are real. The false-green window
lasts until another result or expiry of the old sweep's freshness budget (default300s).

**Smallest correction:** do not clear the previous error during expectation
registration. Existing successful validation/import updates already provide the right
place to clear it. Keep endpoint-binding and imported-fingerprint checks intact.

**Regression:** same-coordinate failure → retry interruption → reopen → health remains
unhealthy; successful verified retry clears the error without inventing import freshness.

### R4 — P2: orderly shutdown consumes poison budgets in optional workers

**Code:** `internal/sessioncapture/enrichment_worker.go:41–84`;
`internal/sessioncapture/optional_repair.go:167–188,230–249`.
Compare the existing shutdown handling in `runtime_store.go:259–263` and
`runtime_delivery.go:80–83`.

Enrichment/optional repair increment attempts on claim, then persist pure caller
cancellation as a failure using an uncancelled save context. At the attempt limit they
quarantine healthy work. Deferred release does not undo the terminal transition.
Ordinary service upgrades/restarts can therefore spend retry budgets, add irrelevant
backoff and eventually require manual replay. Raw capture/delivery already avoid this.

**Evidence:** actual `enrichmentNext` and `optionalRepairNext` synthetic callback/backend
cancellation at `MaxAttempts=1` both persisted `state=quarantined, attempts=1`.
Enrichment returned nil; optional repair returned cancellation. The intended regression
assertions failed. Parent reran with race/shuffle and reproduced both. The one-attempt
probe isolates the terminal boundary; the default limit is8, not1.

**Smallest correction:** reuse the existing pure-shutdown interpretation while the
original operation context is available; refund the claim attempt and release work
without failure backoff. Preserve saved results/verified attachment state. Do not
suppress genuine operation deadlines or joined durability/integrity errors.

**Regression:** terminal-attempt cancellation followed by reopen/retry without another
hook; negative cases for operation expiry and cancellation joined with a fatal error.

### R5 — P2: creating a sibling directory changes a Pi transcript's identity and role

**Code:** `internal/adapters/pi.go:71–73,158–175`;
`internal/capturesource/classify.go:93,108–117`.

A supported flat noncanonical Pi root is classified using the mutable existence of
`<root>/sessions`. With that directory absent, a timestamp/UUID JSONL is a main
session under `dirs/<rootTag>/`. Merely creating the sibling directory changes the
same file to `roots/<rootTag>/`, an empty adapter session key and a resource/artifact.
SourceID and SessionID change although pathname, content and configuration do not.
Future appends stop advancing the original conversation. Prior captured bytes are
not deleted. Alias-mode structural classification also disagrees with this heuristic.

**Evidence:** actual Config Save/Load → runtime composition → production classification
and policy using a synthetic flat custom root. Before/after sibling mkdir changed
SourceID, SessionID and role main→artifact. Parent reproduced with race/shuffle.
This was classification/policy evidence, not a complete import journey.

**Smallest correction:** one stable structural root/layout interpretation, or a stable
explicit inventory flavor where needed. At minimum, a recognized flat Pi transcript
must not be demoted by an unrelated directory. Keep child-layout/privacy validation.

**Regression:** mkdir/removal and ordinary/alias classification preserve exact identity
and role; previously queued work still passes the correct ownership policy.

### R6 — P2: one malformed Unicode record blocks all healthy siblings in a session update

**Code:** `tools/analytics/events.py:195–205`;
`tools/analytics/publications.py:205–207,401–415`.

Python accepts an escaped lone surrogate such as `"content":"\ud800"` during JSON
parsing. Batch serialization then raises `UnicodeEncodeError` on UTF-8 encoding,
outside the per-record quarantine boundary. The entire session update rolls back,
including valid following records. Every retry hits the same poison record even
though JSONL framing gives exact coordinates for isolated normalization failure.

**Evidence:** synthetic verified export with that line plus a normal valid user line:
`import_failed`, previous `p1` retained, zero new issue rows. Parent reproduced using
the real importer. Raw bytes remain in the owned content cache; this is a normalization
isolation failure, not deletion of raw evidence.

**Smallest correction:** validate representability of normalized string fields at the
record boundary and emit a safe issue with original byte coordinates. Continue healthy
siblings. Do not silently replace characters, change raw bytes, or catch storage/DB
errors as malformed records. Changing JSON escaping alone is not a sufficient fix.

**Regression:** lone high/low surrogates in content/IDs/tool fields, valid paired
surrogates, valid sibling events, exact issue offsets, partial normalization and idempotence.

### R7 — P2: delivery hashes and decompresses every selected local payload twice

**Code:** `internal/sessioncapture/deliver.go:368` calls `verifyDependencies`;
`deliverOne` repeats it at `147`; verification implementation at `18–65,249–279`.

Every ordinary attempt follows both calls. Each full verification reads the encoded
file once for its hash and again for decompression/raw hashing. The result is **four
complete encoded-file reads and two decompressions per selected payload before upload**,
including retained sealed epochs. The intervening optional-delivery marker does not
change immutable payload bytes. Remote trusted-object reuse does not avoid this cost.

**Evidence:** parent and worker independently traced direct production control flow.
This is a static work-count proof, not a measured latency/timeout improvement. Actual
large artifacts make it worth removing; no production timeout is attributed to it here.

**Smallest correction:** retain one complete immutable-byte verification pass per
attempt. Separate any still-required fresh policy/latch checks from byte verification.
Keep pre-upload, manifest and CAS privacy checks, corruption rejection and dependency
validation. Do not add a persistent verification cache or an ACK bitmap.

**Regression:** instrument a multi-object attempt's verification count, retain all
privacy/corruption negatives, and measure fixed-fixture I/O/CPU before and after.

### R8 — P2: alias policy reintroduces an unbounded legacy whole-project ownership scan

**Code:** `internal/cli/capture_runtime_alias_policy.go:126`;
`capture_runtime_claimed_policy.go:54`; `internal/adapters/classify_logical.go:25`;
`internal/adapters/claude.go:109–112,144–170`.

For Claude UUID-addressed external artifacts, the candidate correctly derives a
bounded durable owner, then alias exclusion independently calls the legacy classifier.
That reaches `sessionSlugs`/`Glob`, listing every project and its entries without
context/budget checks. A fresh adapter per check defeats cross-source caching. An
unrelated project tree is again on the critical path of a session's policy check.
The legacy resolver also chooses the first matching slug instead of the durable owner.

**Evidence:** direct reachable scan chain; synthetic old-root/duplicate-UUID case
reproduced disagreement in the alias helper and discovery policy. **The actual
full-identity capture/delivery callback denied the opted-out artifact via its separate
claimed-session guard. No byte-publication privacy bypass was demonstrated.** Parent
reproduced that distinction. Runtime scan cost was not benchmarked.

**Smallest correction:** carry the independently validated owner through declared root
translation; translate only the proven root prefix, retaining the full session tuple.
Use preserved exclusion identity and declarations, not a fresh filesystem ownership
search. Keep unknown/ambiguous ownership fail-closed; do not weaken legacy safety code.

**Regression:** external artifacts under aliases, duplicate UUIDs and absent old roots;
assert exact opt-out equivalence and zero legacy project enumeration.

### R9 — P2: capture readiness blocks the controls needed when capture is broken

**Code:** `internal/cli/capture_runtime.go:103–106`;
`internal/capturedaemon/config.go:202–213`;
`internal/capturehooks/plan.go:202–224`.

Full runtime validation runs before service stop/uninstall, hook uninstall and local
work-status dispatch. If the effective policy directory disappears, all these commands
fail with `effective exclusions unavailable`. Hook removal also requires the old
executable to remain present. Capture must fail closed in this situation; stopping or
inspecting its service must not require capture to be ready.

**Evidence:** synthetic actual CLI calls for service stop/uninstall, hooks uninstall and
work-status all fail before dispatch after removing only the synthetic exclusions
directory. Parent reproduced with race/shuffle. **Direct OS supervisor commands remain
a workaround; this is not a claim that the process is impossible to stop.** No real
supervisor was invoked. The additional missing-old-executable rejection is supported
by source inspection, not a dynamic probe in this review.

**Smallest correction:** separate safe teardown/inspection validation from activation
readiness. Validate the minimal trusted configuration, paths and installed identity
needed for those operations; retain full fail-closed validation for start/capture.
Do not replace this with permissive generic config parsing or unsafe file deletion.

**Regression:** policy loss and missing old executable still permit trusted teardown;
unsafe paths/foreign managed files remain protected; activation still rejects broken policy.

### R10 — P3: an accepted manifest can exceed the export metadata envelope

**Code:** `internal/publication/export.go:85–101`;
manifest acceptance in `internal/publication/publication.go:162–209`.

Export duplicates selected source/epoch IDs into its body table, but applies the same
8MiB metadata limit to the larger export. A writer-valid readable manifest can therefore
be impossible to export. The check occurs after body fetch/decompression/write/sync.

**Evidence:** worker actual Local publisher/reader probe with a deliberately extreme
source ID: manifest **4,196,238 bytes**, Publish and Read succeed, Export fails its
metadata bound. **No representative discovery-generated workload was demonstrated**;
this is an accepted-input contract hole, not an observed operating blocker. Parent
inspected the size/control-flow mismatch; did not rerun this edge probe.

**Smallest correction:** align the bounded writer/export/consumer envelope, or reject
unexportable metadata before durable acceptance; preflight deterministic export metadata
before body I/O. Do not truncate identities or remove bounds.

**Regression:** below/at/above boundary, multiple selected/withheld entries, no body GET
or destination creation for an inevitable metadata-size rejection.

## Integration limitations, not unconditional new defects

Two more synthetic probes reproduced existing conservative restrictions. They matter
for acceptance, but are not permission to relax path/permission safety:

- **Declared source aliases do not compose with native hook management.**
  `capture_runtime_hooks.go:20–24` passes logical roots to a planner that rejects all
  symlink components (`capturehooks/plan.go:89–91`). A config-valid declared root alias
  can therefore fail hook planning. Treat this as an explicit integration gap for
  relocated-root installations, not a claim that arbitrary symlinks should be followed.
  Reuse validated physical coordinates if support is added; preserve managed-file trust.
- **Service installation rejects an existing readable supervisor directory.**
  `capturedaemon/service.go:111–114` uses the secret/config `WritePrivate` primitive;
  `config.go:296–298` rejects mode0755 parents even if owned and non-writable by others.
  Probe failed before the fake runner. This is conservative documented behavior and
  an installation limitation, not a security bypass. Any change should separate trusted
  supervisor-file placement from secret-directory policy, never chmod shared directories
  or relax the secret writer globally.

## Complexity disposition

| Keep | Remove or narrow | Defer until evidence/acceptance |
| --- | --- | --- |
| Durable queue/spool authority, source epochs, complete manifests and CAS | R7 duplicate immutable-byte pass | Chunking, GC and new retention algorithms |
| Independent raw capture versus optional enrichment/repair | R8 second ownership interpretation | New schedulers/frameworks/verification caches |
| Fresh privacy checks before delivery and CAS | R4 inconsistent shutdown interpretation | Dashboard global aggregation/native-ID memory changes until measured |
| Authoritative Go reader and pinned Python exports | R1 launcher/core readiness disagreement; R9 teardown coupling | Legacy architecture removal until controlled cutover |
| Explicit independent capture/publish/import/freshness facts | R2/R3 premature failure erasure | Cross-epoch deduplication or retired-schema migration |

The alias scan has a source-proven unbounded work mechanism. Other resource concerns
(native-ID maps proportional to source events, source-list SQL aggregating global events,
whole-export verification and retained payload accounting) still need representative
measurements. No speculative concurrency, universal line cap or second reader is justified.

After acceptance, retire the legacy execution lane coherently—old hook/launch/sync,
recovery and operator defaults—not shared security packages by name. Keeping both
production architectures forever would be expensive; deleting the old safety coverage
before its replacement is accepted would be worse.

### Rejected overclaim: missing previously immutable remote objects

A worker deleted a published synthetic object, then showed that trusted-descriptor reuse
can publish a newer unreadable closure. That deliberately violates the explicit immutable
published-object persistence assumption. No supported writer operation was found to
break it; no automatic GC exists and reviewed retirement excludes fresh keys. The reader
still verifies bodies. This is a conditional storage-fault diagnostic, **not** an accepted
normal-operation defect or justification for unconditional remote rereads. Parent challenged
the premise and the worker explicitly withdrew the stronger finding.

## Verification and limits

All evidence is tied to the reviewed SHA with **no production/test source diff**. Only
this review and the parent-owned handoff were changed. Frozen untracked research remains
untouched. Subagents wrote only synthetic probes/reports under `/tmp/memory-adversarial-*`.

Pinned environment: Go1.27.3, Python3.14.8; existing analytics environment DuckDB1.5.6.
Live/native opt-ins were unset. A failing intended regression is evidence of a defect,
not a passing product gate; probes that assert defective outcomes exit0 deliberately.

### Parent checks

1. Independent source/probe inspection and analytics reproductions:

   ```sh
   nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
     -u AGENT_SESSIONS_CAPTURE_NATIVE sh -c \
     '.analytics-venv/bin/python /tmp/memory-adversarial-analytics/probe.py &&
      .analytics-venv/bin/python /tmp/memory-adversarial-analytics/health_probe.py'
   ```

   Exit0; reproduced R2/R3/R6. Log:
   `/tmp/memory-adversarial-analytics/parent-recheck.log`.

2. Combined synthetic Go regression overlay (adds virtual tests, no checkout edits):

   ```sh
   nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
     -u AGENT_SESSIONS_CAPTURE_NATIVE go test -race -shuffle=on \
     -overlay=/tmp/memory-adversarial-parent/overlay.json \
     ./internal/sessioncapture ./internal/cli \
     -run '^Test(AdversarialShutdownProbe|AdversarialPiIdentityMutation|AdversarialAliasExternalOptOutMismatch|ArchitectureProbe.*)$' \
     -count=1 -v
   ```

   **Exit1, expected defect assertions:** shutdown (both lanes), Pi identity and alias
   helper/discovery disagreement. Lifecycle restriction probes passed by asserting the
   failures. No race report. Package times0.980s/0.879s; log
   `/tmp/memory-adversarial-parent/go-probes.log`.

3. Actual launcher missing-file probe plus unchanged focused analytics tests:

   ```sh
   nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
     -u AGENT_SESSIONS_CAPTURE_NATIVE PYTHONDONTWRITEBYTECODE=1 sh -c \
     '.analytics-venv/bin/python /tmp/memory-adversarial-architecture/launcher_probe.py &&
      PYTHONPATH=tools/analytics .analytics-venv/bin/python -m unittest \
      test_publications test_publication_refresh test_publication_dashboard'
   ```

   Launcher defect reproduced; **64 existing tests passed,11.806s**. These green tests
   coexist with the new failing probes and therefore do not cover the reported defects.
   Log `/tmp/memory-adversarial-parent/python-focused.log`.

### Worker checks

- Queue: one focused overlay invocation, expected failure in both shutdown subtests,
  package0.428s. Report/probe/log: `/tmp/memory-adversarial-queue/`.
- Discovery: one focused CLI overlay invocation, expected identity/helper regression
  failures, package0.536s. `/tmp/memory-adversarial-discovery/`.
- Publication: two synthetic Local probes,0.670s; focused existing publication/R2/backend
  subsets passed1.232s/0.403s/0.370s. No race/shuffle for that subset. Exact test selection
  and the withdrawn object-deletion premise are in `/tmp/memory-adversarial-publication/report.md`.
- Analytics: two short synthetic Python invocations, exit0, then parent rechecks above.
  `/tmp/memory-adversarial-analytics/`.
- Architecture: synthetic race-enabled Go lifecycle probes passed by asserting rejection,
  CLI1.602s, plus launcher probe. `/tmp/memory-adversarial-architecture/`.
- Independent challenge: source/probe review only, no new tests. Upheld R1–R9 with
  explicit limits on startup, mocked initial import, injected interruption, downstream
  inference and privacy impact. `/tmp/memory-adversarial-challenge/report.md`.

**Not run:** `make check`, full `make test`, fuzz/coverage/lint, full Python discovery,
actual native agents, installed-path journeys, live R2, Linux/BC-01, hosted CI, capacity
benchmarks or OS-kill crash campaigns. No credentials, private transcripts, installed
queues, host/cloud state or frozen research contents were inspected. No installation,
deployment, cleanup, archive mutation or implementation repair was performed.

## Recommended delivery order

1. Close R1–R3 with failing-before/passing-after entrypoint and persistence regressions.
2. Fix R4–R6 without changing privacy, deadline or raw-evidence guarantees.
3. Remove R7/R8 redundant work/ownership interpretation; decouple R9 controls.
4. Close the bounded metadata hole and explicitly qualify applicable installation gaps.
5. Rerun unchanged full gates on the integrated fixes, then the outstanding installed
   two-host/restart/privacy/coverage/resource acceptance. Only then retire legacy paths.

The current handoff already withholds release acceptance. This review strengthens that
judgment; it neither authorizes rollout nor substitutes for operating qualification.
