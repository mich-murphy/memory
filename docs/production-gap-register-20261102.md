# Production gap register — November 2

> **Historical Go evidence, not current instructions.** The dated source/deployment
> statements and links below describe their original checkpoints. Current ownership,
> authority, verification and next actions are in the
> [active handoff](session-capture-handoff.md#current-session-checkpoint).
> [DESIGN.md section 0](../DESIGN.md#0-start-here-in-a-fresh-session) governs Rust work;
> do not resume the old gap stack or infer current host health from this register.

**Source:** `main` = `2deac62` (PR31 merged). **Status:** offline source analysis and
experiments only. No production code was changed, nothing was deployed, and no live R2,
BC-01, installed host or original session was touched. This register records confirmed
gaps, the behaviour verified as sound, and test-quality findings, so later work can
resolve each item. The [handoff](session-capture-handoff.md#production-readiness-analysis--november-2)
holds ownership, authority and the next action. The [capture contract](session-capture-contract.md)
remains the requirement.

**Resolution (added 2026-11-03; the analysis below is unchanged dated evidence):** every
gap GAP-01…GAP-11 has resolving commits on the unpublished branch `gap/integration`
(`2deac62..1150db5`). The canonical `make check` `p-int-canon6` passed on that tree. The
per-gap commit table, the evidence and the items not run or not proven are in the
[handoff](session-capture-handoff.md#gap-resolution-delivery--november-2). Nothing has
been pushed, deployed or run against live R2.

**Update (2026-11-05):** the gap work merged as stack #39 (`d975961`), followed by the
step-3 fixes (#46 `f94670a`) and the live-operation fixes #47–#53 (`main` = `57d8400`).
That source ran on the Mac and BC-01 until 2026-11-09, when its capture daemons were
stopped and its R2 prefixes deleted by operator instruction. For operating status, use the
handoff's [resume-here checkpoint](session-capture-handoff.md#november-6-2057z-nov-5-resume-here).

Private evidence root (not in the repository):
`~/.local/state/machines-validation-pilot/memory-analysis-20261102/`. It contains the
worker reports (`reports/`), test overlays (`overlays/`), frozen snapshots and every job's
argv, log, result and resource counters (`jobs/`). Every job ran in the shared admitted
Docker worker (pinned Nix Go 1.27.3, read-only source and dependencies, network none,
shared count-6 semaphore). Each job's source post-verify passed.

## Verdict

**Not production-ready for rapid workloads.** The data-correctness core holds up under
much stronger tests than the existing suite: the epoch model, crash durability at every
boundary, conditional publication, privacy exclusion and idempotent import. The confirmed
gaps are in **liveness and efficiency**:

- an ordinary network outage of about two minutes permanently quarantines sessions;
- a fixed 30 s transport timeout makes large objects unpublishable;
- every append capture is delivered and stored as a full object, so bytes grow
  quadratically;
- bounded inventories and caps fill up permanently;
- consumers fail permanently on sessions whose retained epochs are large.

Each needs a design decision plus implementation, not only a test.

How to read the evidence columns:

- **RED** means an experiment asserting the contract-required behaviour failed on the
  unmodified baseline at its intended assertion, while its positive control passed.
- **Static** means the gap is established from code reading only.
- Compile or fixture failures were never counted as evidence.

## 1. Confirmed gaps

| ID | Gap | Severity | Evidence |
|---|---|---|---|
| GAP-01 | Transient delivery failures quarantine after 8 attempts | Critical | RED `e-job-v1` (E1) |
| GAP-02 | Fixed per-operation timeouts make large objects unpublishable | High | RED `e-job-v2` (E6) + static |
| GAP-03 | Every append capture is a full object; no coalescing or reclamation | Critical (efficiency) | RED `e-job-v1` (E3), measured |
| GAP-04 | Inventory cap counts absent/published rows and blocks new sessions | High | RED `e-job-v1` (E4) |
| GAP-05 | 1024-source manifest cap overflows as a transient error and quarantines | High | RED `e-job-v2` (E5) |
| GAP-06 | A rebuilt local spool never converges with an existing remote | Medium-High | RED `e-job-v1` (E2) |
| GAP-07 | The consumer export cap applies to cumulative retained epochs | High | RED `a-run2` (A7) |
| GAP-08 | The Python consumer validates more loosely than the Go reader | Medium | RED `a-run2` (A8) |
| GAP-09 | Free-space reserve arithmetic is untested | Medium | 8 surviving mutants, `f-mut-quota-v2` |
| GAP-10 | A crash before payload sync leaves an unreclaimed temp file | Low | `c-c1-job1` characterization |
| GAP-11 | Transformed sources are re-hinted and re-prepared on every sweep | Medium (CPU) | Static, unmeasured |

### GAP-01 — Transient delivery failures quarantine after 8 attempts

- **Behaviour:**
  - `persistDeliveryFailure` sets `state='quarantined'` once `attempts >= MaxAttempts`
    for every class except `FailureDestination` (`internal/sessioncapture/runtime_delivery.go:144`).
  - The default is 8 (`runtime.go:96`), configurable 1–100. Backoff before attempt 8
    totals about 64–127 s, so an ordinary uplink flap of about two minutes quarantines
    every session that tried to deliver.
  - The CLI supplies no custom classifier, so unclassified transport errors and
    `ErrConflict` fall through to `FailureTransient` (`runtime.go:101-121`).
  - A quarantined head makes `deliverDueLaneWork` return early, so newer jobs in that
    session never deliver (`runtime_delivery.go:~75`).
  - Recovery requires a manual `replay`.
- **Contract:** §3 says temporary transport or throttling failures "keep pending; bounded
  exponential backoff with jitter".
- **Evidence (E1):**
  - A plain transient error quarantined the job at tick 8.
  - After the backend healed, the job stayed quarantined, and the newer sequence 2 stayed
    `pending` behind it.
  - Controls passed: one failure followed by healing publishes, and session B is isolated.
- **Fix direction:**
  - Never quarantine Transient, Timeout or Throttled failures. Keep backoff capped
    (currently 5 minutes) indefinitely, and expose lag and attempts in status.
  - Reserve quarantine for deterministic failures (policy, integrity, malformed
    metadata, or a typed over-limit class).
  - Give `ErrConflict` its own class (see GAP-06).
  - Let a newer subsuming job supersede a quarantined head where the contract permits
    (see GAP-03).
  - `runtime_store.go:270` applies the same attempt rule to source capture. Review it
    with the same classification.
- **Acceptance:** the E1 test unchanged: still `retry` after 12 transient attempts,
  publishes after healing without replay, newer job not blocked, session B unaffected.

### GAP-02 — Fixed per-operation timeouts make large objects unpublishable

- **Behaviour:**
  - `capturebackend/backend.go:133` constructs the R2 backend with a 30 s per-operation
    timeout. `http.Client{Timeout: 30s}` (`:137`) bounds the whole request, body included.
  - The runtime wraps each step in `OperationTimeout`, 60 s by default (`runtime.go:82`).
  - The largest uploadable object is therefore about 30 s × the uplink rate, for example
    about 75 MB at 2.5 MB/s. The decoded and encoded admission bound is 1 GiB.
  - Objects above the transport ceiling time out on every attempt, and GAP-01 then
    quarantines them.
- **Evidence (E6, queue level only):**
  - An object needing about 1 s under a 200 ms operation budget ended quarantined with
    attempts=8 and `last_error=timeout`.
  - The 10 s control published.
  - The transport-level 30 s limit is established statically, not executed.
- **Fix direction:**
  - Replace total-duration deadlines on object bodies with progress- or idle-based
    deadlines, or a budget scaled by size from a documented minimum throughput.
  - Consider multipart upload above a measured threshold.
  - Keep a total deadline on metadata and CAS operations.
  - Treat timeout as retryable (GAP-01).
- **Acceptance:** E6 with an injectable clock, plus a throttled SDK-peer upload of an
  object that takes longer than the old fixed bound.

### GAP-03 — Every append capture is a full object; no coalescing or reclamation

- **Behaviour:**
  - `acceptSnapshot` always calls `acceptView(..., supersede=false)`
    (`internal/sessioncapture/capture.go:336`). Supersession is used only by enrichment
    or optional detachment.
  - Delivery always takes the oldest unpublished job.
  - Each accepted append is a complete compressed copy of the whole epoch. All payloads
    and intermediate publications are retained, with no GC by design (`queue.go:6-9`,
    `quota.go:10-12`).
- **Contract:** §3 says "coalesce discovery hints and proven-subsumed append captures,
  not unique accepted bytes". §4 says "begin without automatic GC".
- **Measured (E3):**
  - 50 captured appends with deferred delivery produced **50 object PUTs and 50 CAS
    calls**.
  - Spool bytes are exactly `chunk·k(k+1)/2`: 5.5× the final source at k=10, 25.5× at
    k=50 and 50.5× at k=100 (64 KiB incompressible appends).
- **Model:**
  - A transcript of final encoded size E, captured k times, costs about `E·k/2` in local
    spool, uploaded bytes, read-back verification and stored R2 bytes, against an ideal
    of E.
  - Example: E = 6 MB, k = 500 gives about 1.5 GB. The spool limit (1 GiB in the service
    example) then stops capture, by design without eviction.
- **Fix direction (needs a design decision):**
  1. Supersede undelivered, proven-subsumed jobs of the same session at acceptance. Each
     new view is built from the latest manifest, and `advances` permits skipping
     sequences. This cuts deliveries and remote storage under delivery lag but does not
     reclaim the spool.
  2. Set a per-session capture cadence: debounce, or capture on quiescence and shutdown,
     so k tracks meaningful checkpoints rather than hook frequency.
  3. Add local payload reclamation for objects referenced by no live job and no retained
     publication. Remote retention and GC need their own policy under contract §4 and §6;
     do not slip either in incidentally.
  4. Consider chunked or append-delta objects only after measuring representative k and
     E. The contract makes chunking an optional, measured optimization.
- **Prerequisite measurement:** the distribution of captures per session and final size
  in actual Claude, Codex and Pi use. This has not been measured.
- **Acceptance:** E3 asserts at most about 2 object PUTs for 50 deferred appends and a
  spool bound after drain. The model history test (§3) must still pass, so no unique
  accepted bytes are lost.

### GAP-04 — Inventory cap counts absent/published rows and blocks new sessions

- **Behaviour:** the retained source inventory (`MaxRuntimeSources = 4096` by default,
  `runtime_store.go:69`, configurable up to 65,536) counts absent, fully published rows.
  They are never expired, so once full, `Hint` returns `ErrQuota` for every new session.
- **Evidence (E4):** with the cap at 8, eight absent and published rows caused a ninth
  new session to be refused with `ErrQuota`.
- **Fix direction:** compact or expire rows that are absent, have no pending, quarantined
  or claimed work, and whose last capture is published. Keep refusal when the cap is full
  of live work. Expose inventory occupancy.
- **Check before choosing the default:** actual source counts on this Mac and BC-01. These
  require separate inventory authority and were not inspected.
- **Acceptance:** E4, plus a negative case where a cap full of live rows still refuses.

### GAP-05 — 1024-source manifest cap overflows as a transient error and quarantines

- **Behaviour:**
  - `len(m.Sources) > publication.MaxSources` (1024, `internal/publication/types.go:17`)
    fails with a plain error at `capture.go:532`, not `ErrQuota`.
  - The runtime classifies it as transient and quarantines the source.
  - Sealed epochs are never dropped, so a rewrite-heavy session reaches this cap
    permanently. The enrichment worker also reserves part of the budget.
- **Evidence (E5):** capture 1025 failed with "source limit exceeded" and was classified
  `transient`, then quarantined. This run takes about 172 s under `-race`.
- **Fix direction:** return a typed error and record visible coverage
  (`coverage.missing: source-limit`). Then choose an explicit policy: a rollover session
  identity, compaction of sealed epochs, or a documented hard limit with an alert.
  Dropping prior epochs silently would violate contract §1.
- **Acceptance:** E5 asserts the typed visible outcome, with no transient quarantine.

### GAP-06 — A rebuilt local spool never converges with an existing remote

- **Behaviour:**
  - The local sequence is `latest local job + 1` (`capture.go:300`).
  - `deliverOne` returns `ErrConflict` when `remote.Sequence >= m.Sequence`
    (`deliver.go:169`).
  - `advances` forbids omitting prior epochs.
  - No code adopts a verified remote current, so after local queue loss (or a foreign
    writer on the same identity) every publication conflicts, is classed transient, and
    quarantines.
- **Evidence (E2):** both variants ended with seq1 quarantined and seq2 pending, and the
  remote stayed at sequence 3.
- **Scope:** not a steady-state path. It matters for disaster recovery, reinstall and
  machine-identity mistakes. Contract §6 preserves identities, which makes this rarer,
  not impossible.
- **Fix direction:** classify conflicts distinctly. When there is no local history for an
  identity, read and verify the remote current and rebase above it, retaining its epochs
  as sealed or trusted. Never overwrite.
- **Acceptance:** E2 converges without replay. The remote's prior epochs remain readable.

### GAP-07 — The consumer export cap applies to cumulative retained epochs

- **Behaviour:** refresh passes `max_bytes=256 MiB` (`tools/analytics/publication_refresh.py:90`)
  and Go's default is 1 GiB (`internal/publication/export.go:13`). Both cap the
  **cumulative** decoded size of all retained epochs, so a session with a small latest
  epoch but large history can never be imported.
- **Evidence (A7):**
  - Actual writer epochs of 31,790, 31,790 and 317 bytes were exported against a
    49,152-byte cap.
  - The sweep reported `process_failed` with `unimported=1` and no session row.
  - Go export succeeds at exactly 63,897 bytes and fails at 63,896.
  - The failure is visible but permanent, with a generic code.
  - The 1 GiB control imported.
- **Fix direction (policy decision):** choose one of a per-epoch bound, streaming import
  without a cumulative cap, or explicit partial import with a typed coverage gap
  (`too-large`). Never a green refresh lacking the session.
- **Acceptance:** `test_A7_strict_latest_small_epoch_session_importable_under_cap` in the
  overlay module (§3).

### GAP-08 — The Python consumer validates more loosely than the Go reader

Contract §4 requires one authoritative reader. Python must not interpret archive history,
but it does re-validate the export. Characterized in A8:

- It accepts `sequence=0`. Go requires `(0, MaxInt64]`.
- It accepts metadata up to 16 MiB (`publications.py:54-55`). Go caps it at 8 MiB.
- It accepts unknown fields at the top level and in the manifest, coverage, source,
  object and body entries.
- An unknown identity field raises a raw DuckDB `BinderException` rather than a typed
  import failure.
- `manifest_sha256` stored in DuckDB is a local re-serialization fingerprint and matches
  no Go digest, so it cannot be tied back to `current.json`.

**Fix direction:** mirror the Go bounds exactly. Decide whether unknown fields in a known
contract are rejected or ignored, and test that decision. Map every validation failure to
a typed import error. Store the remote manifest digest from the reader's export.

### GAP-09 — Free-space reserve arithmetic is untested

- **Behaviour:** `reserve` (`internal/sessioncapture/quota.go:56-67`) subtracts
  `MinFreeBytes` and outstanding writes from statfs free space. Production uses a
  256 MiB service reserve.
- **Evidence:** mutation run `f-mut-quota-v2` (gremlins, coefficient 100, 524 s) produced
  30 killed, 9 lived, 2 timed out and 1 not covered. Eight survivors are in
  `quota.go:61`, which no test reaches because `availableBytes` reads real statfs. The
  ninth, `quota.go:51` `>` to `>=`, differs only for a zero-byte write at exactly full
  quota.
- **Fix direction:** add an internal free-space seam (not a public test-only option)
  and exact boundary tests: free == reserve, free − reserve − writing == n, and
  outstanding writes. Then rerun mutation on `quota.go` and `runtime.go`. The projected
  `runtime.go` run is about 19 minutes at one worker.

### GAP-10 — A crash before payload sync leaves an unreclaimed temp file

A real process exit at `payload-before-sync` leaves one non-`.zst` temp file in
`payloads/`. It is counted in `usage()`, so accounting stays consistent, but it is never
reclaimed after reopen and drain. **Fix:** at `Open`, under the process lock, remove
temporaries matching the writer's temp pattern, and release their accounted bytes.

### GAP-11 — Transformed sources are re-hinted and re-prepared on every sweep

`capturesource/classify.go:132` sets `Fingerprint` only when `transform==""`. SQLite
backups, scrubbed configs and zstd sources therefore re-hint with a version bump on every
sweep, then re-prepare their transformed bytes. This likely contributes to the unresolved
settled-CPU budget. **Next measurement:** the C3 harness with transformed sources,
counting pending rows, preparation calls and CPU per sweep. Any shortcut must still
detect WAL or SHM changes and policy freshness; contract and handoff prohibit a naïve
stat-only cache.

### Static-only conformance items (not executed)

From the private conformance audit (`reports/contract-conformance.md`; see §5). Verify
these before prioritising them:

- No admission-time R2 key-length check. Long identities fail at delivery and then
  quarantine under GAP-01 (`publicationr2/backend.go:60` limits keys to 1024 bytes).
- `Status()` loads every job ever created, and `usage()` stats every payload file on each
  snapshot. Both cost O(history).
- No SQLite `user_version` and no CHECK constraint on `jobs.state`. Persisted JSON blobs
  are decoded without `DisallowUnknownFields`.
- Each publish re-verifies the full local closure and rechecks policy several times. An
  ambiguous-retry publish downloads every selected object. This is deliberate safety;
  measure it before changing anything.
- Every manifest carries `discovery-not-enumerated`, so analytics cannot distinguish
  "complete" from "never enumerated".

## 2. Verified sound (stronger evidence than the existing suite)

| Area | Experiment | Result |
|---|---|---|
| Epoch semantics: append, partial tail, rewrite, same-size rewrite, truncation, restart, faults, lost ACK | Seeded model-based history (`p-run200` 200 histories non-race, `p-race50` 50 under race/shuffle), contract model independent of the implementation | 0 violations. Mutant control (prefix check removed) failed at I1. The shrinker reduced the failure to 4 ops |
| Real process-exit crashes | `c-c1-job1`: `os.Exit(137)` at all 14 boundaries × {sequence 1, append sequence 2}, source deleted in committed cases | 28/28 + control GREEN. Reopen never failed, no torn job, bytes exact, no sequence regression |
| Rapid hooks | `c-c2-job1`: 16 goroutines × 1000 hints on 8 sources with appends. `c-c2-job2`: inbox bound | No lost wakeup, 8 rows, attempts 0. Inbox capped at 1024 with typed refusals |
| Settled plain discovery | `c-c3-job1`, N = 200, 1000, 4000 | No version bumps or captures. Sweep ≈ 95 ms discovery + 270 ms hint at N=4000. ctime catches a same-size rewrite with mtime restored |
| Privacy | `p-race50` canary: scrubbed config, raw protected config, malformed config, initial exclusion, opt-out after capture, opt-out at `upload-before` | No canary in any key, current body, or raw or zstd-decoded object (five encodings). Sibling sessions published exactly |
| Consumer properties | `a-run4`, 100 seeds | Idempotence, order invariance, epoch non-collision, partial tail flagged, append monotonicity, 14 corruption variants atomic |
| Fuzz | `f-fuzz-*` | No crasher, but **inconclusive**: workers stalled after about 6–12 s of each 60 s campaign (cause unidentified) |

Model weak spots to strengthen before adoption:

- The lost-ACK-then-republish path never produced a repeated CAS sequence.
- Only 9 of 200 histories hit a `job-committed` fault.
- The policy was constant-allow and the clock fixed.
- Power loss and missing fsync cannot be tested with process exits.

## 3. Test quality

### Weak oracles proven with mutant pairs

Method:

- Mutant + old test passes, showing the oracle is weak.
- Mutant + new test fails at the intended assertion.
- Baseline + new test passes under race/shuffle at `-count=3`.

Jobs: `t-a2-mut-old`, `t-b2-mut-new`, `t-c2-base-new`.

| Item | Existing test | Surviving mutant | Verdict |
|---|---|---|---|
| W1 | `TestRuntimeRetryBoundsAndSafeClasses` (`runtime_test.go:538`) checks only `1s ≤ d ≤ 5m` | Constant backoff | **Confirmed.** The new exact table fails it: `retryDelay(8)=1s`, want `2m8s` |
| W4 | `TestEpochsAndRetainedPublication` checks counts and states only | An append retains the stale object | **Confirmed.** The new byte-exact epoch export fails it |
| W5 | `publicationr2` `TestConcurrentReplacement`: two writers, result not tied to body | Later conflicts report success | **Confirmed.** A 16-writer test tied to the winning body fails it (15 "winners") |
| W7 | `capturesource/discover_test.go:290` uses `t.Skip` when no transform is emitted | Transform emission disabled | **Confirmed.** The old test skips; the new test fails |
| W2 | `TestSameSessionCaptureOrdering` | No-op session lock | **Refuted** for this mutant: the old test fails on a jobs unique constraint |
| W10 | `TestDeliveryCostIsProportionalToDelta` | Re-read all objects | **Refuted** for this mutant: the old open-count assertion kills it |

Not yet examined: W3 (crash test only on publication 1 with one source; now partly
covered by C1 and the model), W6 (sleep-induced concurrency peak in
`publication/heads_test.go`), W8 (wall-clock sleeps), W9 (constant pin in
`encoded_size_test.go`), and W11 (Python idempotence checked only by count and timestamp;
now covered by A1).

### Structural gaps

- No fuzz targets exist in the candidate packages (`sessioncapture`, `publication`,
  `publicationr2`, `capturebackend`, `capturesource`, `capturehooks`, `capturedaemon`,
  `exclusion`).
- `scrub.FuzzEquivalentSecretCopies` is declared but not in `Makefile` `FUZZ_TARGETS`.
- No property or model-based tests exist on the candidate path. The legacy
  `syncer/simulate_test.go` pattern was never ported.
- Crash tests return errors and then close gracefully. No real process exit was tested
  before C1.
- `make mutation`'s `--timeout-coefficient=3` is unusable for `internal/sessioncapture`.
  Gremlins derives its timeout from a 0.65 s coverage pass, so 40 of 41 quota mutants
  timed out (`f-mut-quota-v1`). Coefficient 100 gave valid results. Mutation remains an
  opt-in diagnostic; this is a tooling configuration finding, not a weaker gate.
- Gremlins `--exclude-files` only matched in the `(^|/)file\.go$` form.
- Unresolved: fuzz workers stall after about 9 s. Before trusting a campaign, rerun with
  `-fuzzminimizetime=0` on the medium profile and take a SIGQUIT goroutine dump during
  the stall.

### Ready-to-adopt test code (private overlays; review before committing)

| Test | Overlay path | Adoption target | Budget |
|---|---|---|---|
| Model-based history + privacy canary | `overlays/p-model-v2/internal/sessioncapture/zz_p_{model,privacy}_test.go` | `model_history_test.go`, `privacy_canary_test.go` | 200 histories ≈ 42 s non-race; 50 race ≈ 35 s |
| Process-exit crash matrix | `overlays/c-c1-v1/internal/sessioncapture/zz_c1_crash_test.go` | `crash_process_test.go` (add `nonZst==0` once GAP-10 is fixed) | ≈ 13 s race |
| Hint burst / inbox bound | `overlays/c-c2-v2/internal/sessioncapture/zz_c2_burst_test.go` | `runtime_hint_burst_test.go` (trim to about 2000 hints) | 78 s at 16k hints race |
| Settled discovery cost | `overlays/c-c3-v1/internal/capturesource/zz_c3_settled_test.go` | Benchmark or measurement, not a gate | 89 s |
| Gap acceptance tests E1–E6 | `overlays/e-v2/internal/sessioncapture/zz_e_gaps_test.go` | Lands with each fix | E5 172 s race |
| Stronger W1/W4/W5/W7 oracles | `overlays/t-new/...` (`zz_t_w{1,4,5,7}_test.go`) | Replace or extend the weak tests | seconds |
| Consumer properties A1–A8 | `overlays/a-v1/tools/analytics/test_zz_a_properties.py` | `test_publication_properties.py` | 100 seeds ≈ 110 s; default 40 |
| Fuzz targets (current, manifest, quotaWriter, editJSON) | `overlays/f-*` (final `f-fuzz-v6`) | `fuzz_test.go` per package, plus `FUZZ_TARGETS` | 60 s campaigns once the stall is resolved |

Note: the W2 overlay detects lock waiting by polling `runtime.Stack` for a frame name.
Prefer a fault-stage barrier before adopting it.

## 4. Recommended resolution order

1. **GAP-01 and GAP-02** (liveness under ordinary outages and large objects). These are
   small classification and deadline changes with ready acceptance tests. They are the
   highest risk for unattended operation.
2. **GAP-04, GAP-05, GAP-07** (permanent caps). Each needs a short policy decision, then
   a typed visible state plus a test.
3. **GAP-03** (coalescing). Measure the actual captures per session and session sizes
   first, then implement subsumed-job supersession and capture cadence. Local reclamation
   and any remote retention need their own reviewed policy.
4. **Test infrastructure:** adopt the model history, process-crash and privacy tests. Add
   `FuzzEquivalentSecretCopies` to `FUZZ_TARGETS`. Fix the fuzz stall before relying on
   campaigns. Add the GAP-09 seam and tests, and run tier-1 mutation (`quota.go`,
   `runtime.go`, `capture.go` epoch branch, `deliver.go`, `publication.go`) with a usable
   timeout coefficient.
5. **GAP-06, GAP-08, GAP-10, GAP-11** and the static items.
6. Then the separately authorized operating sequence in the handoff. These offline
   results do not establish installed, R2, two-host or budget acceptance.

## 5. Method and limits

- **Phase 1:** three read-only analysts produced a history catalog of 33 problem classes,
  a contract and schema conformance audit, and a test-oracle audit (`reports/`). The
  parent re-read the cited code for every claim promoted here.
- **Phase 2:** six workers ran experiments as private overlays on frozen snapshot
  `baseline-2deac62` (manifest `1f63f5e3…`). Deliberate production mutants were declared
  and kept separate. The parent inspected every result, argv, overlay binding and decisive
  log line, rather than accepting worker summaries.
- **Analytics exit status:** the analytics jobs pipe output through `tail`, so their
  container exit is 0 even when unittest reports `FAILED`. The A7 and A8 RED results come
  from the unittest lines in `a-run2`, not from its exit code.
- **Not run:** canonical `make check`, full package suites, native Darwin, hosted CI,
  timed fuzz beyond the stalled campaigns, the C4 SQLite live-writer backup, and the
  transformed-source sweep cost. No live R2, installed host or two-host check was run.
- **Not covered by the history catalog:** defects mentioned only inside the 6,620-line
  October 27 archive may be missing, because it was sampled rather than read in full.
