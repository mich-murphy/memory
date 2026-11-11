# Test suite review for the Rust rewrite

Scope: read-only. Paths are relative to /Users/mm/dev/memory. Labels: **measured** (I read the code or ran it), **doc says** (taken from a repo document, not re-run), **inferred**, **guess**.
Principles applied: test-behavior-not-implementation (the weak-oracle catalogue and the "carry or drop" calls), subtract-before-you-add (what the rewrite should not port), prove-it-works (what the consumer gate does and does not prove).

## Findings first

1. The suite is large (314 `_test.go` files, 62,321 lines, 1,193 `Test*` funcs, 15 `Benchmark*`, 14 fuzz targets; measured by grep and wc) but its value is concentrated in about 10 artifacts. Most of the rest is either legacy-path coverage or tests that pin Go internals.
2. The model-history test is a real seeded property test with an independent contract-derived oracle, not a scripted scenario list. It costs 25.7 s for 40 histories (measured, one run). It has not found a production bug in the repo record I can see. It was validated only by mutation controls. Its shrinker is crude (greedy op deletion).
3. The legacy `internal/syncer/simulate_test.go` found four real data-loss or corruption bugs in one commit (ccc4aed, measured from the commit message). That is the best evidence in the repo that simulation testing pays. It is tied to the legacy v3 archive format. Its structure, not its code, is the spec to port.
4. The process-exit crash matrix covers every boundary the contract names (spool, job, upload, manifest, current, local-ack). It enumerates 17 stages by hand. Production has 28 distinct `fault("...")` call sites (measured by grep), so 11 are never crash-tested, and nothing fails when a new hook is added.
5. Mutation testing (gremlins) found weak oracles and one pair of minor real findings (first run, f5c98a4) but zero production defects on the candidate path. It cost hours of wall time, repeated tool failures and about 1,240 lines of mutant-killing tests that mostly pin Go internals. My verdict is that it earned its cost once, early, on legacy code, and was poor value afterwards. Details in section 5.
6. The consumer gate (`make capture-acceptance`) is genuinely strong for its scope. It runs the real built binary to write, export and restore, then imports into DuckDB and asserts literal counts. It does not touch R2, a second machine, installed hooks, power loss, or large sessions, and the Python side reads the Go reader's export, not the bucket.
7. For Rust: carry the model-history design, the crash matrix design, the privacy canary (with its scanner-control), the Python property tests, the writer-to-importer gate, and the fuzz properties that assert round-trip or accept-implies-valid. Drop implementation-pinning tests, mutation-kill tests as a category, Go-coverage-driven branch tests, and the sleep-based tests. Use proptest for local pure invariants and keep the model test as a custom seeded harness over a deterministic simulation. cargo-mutants is worth running on three small crates as a periodic diagnostic, not a gate.

## 1. Inventory of techniques

Sources: docs/quality-gates.md lines 79-90 (read fully), Makefile lines 18-119, and the files cited.

| Technique | Where | What it actually asserts | Oracle type |
|---|---|---|---|
| Seeded model-based history | internal/sessioncapture/model_history_test.go, model_optional_test.go | Exported epochs (state, revision, size, sha256, literal bytes) equal a contract model; current sequence never regresses; no writes after exclusion; liveness after backoff; second Deliver writes nothing; spool equals roots | Independent model (identities, literal bytes, counts) |
| Real process-exit crash matrix | internal/sessioncapture/crash_process_test.go | After `os.Exit(137)` at each stage: reopen succeeds, job count and max sequence equal expectation, exported bytes equal literal `"first\n"` or `"first\nsecond\n"`, sequence not regressed, no non-`.zst` temp left, usage equals physical bytes | Literal bytes plus derived expectation |
| Privacy canary | internal/sessioncapture/privacy_canary_test.go | A synthetic canary in six encodings (raw, JSON-escaped, ASCII-\u, URL, hex, base64) never appears in any backend key, CAS body, raw or zstd-decoded object; a positive sibling session publishes exactly; a scanner-control proves the scanner can see a leak | Absence, guarded by a positive control |
| Simulation (legacy) | internal/syncer/simulate_test.go | After each sync, every tracked file rebuilds byte-for-byte from the bucket via manifest and without; replaced bytes kept as a version; no secret in any object; second sync uploads 0; no remote delete | Independent (file bytes on disk) |
| Fuzzing | 14 targets in Makefile lines 18-23 | Mostly round-trip, determinism, accept-implies-valid, no input mutation | Mixed, see section 6 |
| End to end, shell | cmd/agent-sessions/testdata/script/*.txtar (4 scripts, 71 lines total) | `stdout 'uploaded 2 files'`, then `uploaded 0 files`; hook prints nothing; bucket object appears. All four test the legacy `sync` and `hook` path, none test `capture-v1` | Output text, object presence |
| End to end, Python | tools/analytics/test_capture_journey.py (838 lines), test_publication_refresh_journey.py, tools/capture-delivery/test_launch_journey.py | Real built binary writes and exports; DuckDB import yields literal role counts, issue counts, epoch counts, idempotent event ids | Literal values plus input literals |
| Seeded Python properties | tools/analytics/test_publication_properties.py (591 lines) | Model computed from raw body bytes with stdlib json only: idempotence, order invariance, epoch non-collision, partial tail flagged, append monotonicity, 14 corruption variants atomic, strictness parity with Go | Independent model |
| Golden literal | internal/cli/... `liveCodexIdentity` hashes (cmd/agent-sessions/live_test.go:957-977), `TestStatFingerprintV1` (internal/capturesource/fingerprint_linux_test.go:26) | Exact hash or fingerprint strings. The Codex hashes come from an independent pinned Rust normalizer (comment at live_test.go:958) | Literal, one independent |
| Mock-fault backends | publicationr2 tests use an in-process S3 protocol stub (`syntheticBackend`, mutation_boundary_test.go:31) and gofakes3; real R2 only with `AGENT_SESSIONS_TEST_R2=1` (doc says) | Status classes, idle-guard, budgets, lost-ACK behaviour | Behaviour of the SDK boundary |
| Step-count hang guards | internal/sessioncapture/hang_mutants_test.go:12-52 | A `hangBudget` context cancels after N `ctx.Err`/fault calls so a non-progressing loop fails fast instead of timing out | Termination, deterministic |
| Mutation testing | gremlins via `make mutation` | See section 5 | |
| Coverage and CRAP gates | `.testcoverage.yml`, `make crap` (quality-gates.md:83-84) | 75% total, 60% per package, 85% for raw/state/syncer; CRAP over 30 fails | Coverage proxy |
| Benchmarks | 15 `Benchmark*`, `make bench-smoke` runs each once | Only that they run | Invocation only |

Counts (measured): 81 `t.Parallel()` calls, 33 `time.Sleep` calls, 31 `t.Skip` calls in tests.

## 2. The model in model_history_test.go

### What it is (measured, file read in full except the helper tail)

It is a real property test. A seeded generator (`generateModelHistory`, lines 361-474) emits a `modelHistory` (agents, per-session source counts, denied flags, rewriter flags, and up to 110 ops). A runner (`modelRun`, lines 527-1180) applies each op to the real `Queue` and to a small model, checks invariants after every op, then does a fault-free drain with final checks. It is not a scripted scenario list.

Operations (lines 219-229, 413-466): Append, AppendPartial, Rewrite, SameSizeRewrite, Truncate, TruncateZero, Delete, Capture, CaptureIfChanged, Deliver, DeliverDue, Clock (advance injected clock), Restart (graceful close and reopen), Fault (arm a stage, nth hit), LostAck (put or cas), Deny (mid-history opt-out).

The model (`modelSource.apply`, lines 485-510, about 20 lines): the first capture opens epoch 1. Equal bytes bump the revision unless `CaptureIfChanged`. A byte-prefix extension replaces the active epoch content and bumps the revision. Anything else seals the active epoch and opens a new one. This is the contract's epoch rule, small enough to be a true independent oracle.

Invariants (identified by id in the code):
- I1/I2 (`checkRemote`, `compareSession`, lines 978-1050): per non-excluded session, the exported manifest has exactly the model's epochs per source, with state sealed or active, revision, size, SHA-256, and the exported body bytes equal the model's content. Epoch ids are unique.
- I2 also removes original sources on odd seeds (line 1130ff) so accepted bytes must not depend on the source surviving.
- I3 (`modelBackend.CompareAndSwap`, lines 145-175): per current key, sequence never decreases, and a repeat of a sequence must carry an identical body.
- I4 (`checkIdempotent`, lines 1053-1080): a second Deliver writes zero objects and leaves current unchanged.
- PRIVACY (`checkInvariants`, lines 679-694): after Deny, zero further backend writes under that session's prefix.
- LIVENESS (`dueDrain`, lines 1083-1108): once the injected clock passes the 5 minute retry cap, `DeliverDue` alone publishes everything pending.
- Spool and count oracle (`checkSpoolAndCounts`, line 1309, added in 44a0fac): roots always in spool; with no fault the spool equals roots; CAS writes never exceed jobs plus republishes.

Generation: workload-shaped, not uniform. Lognormal append sizes with a Pareto tail (lines 254-270), line lengths with a rare long line, a burst/idle clock mixture up to four hours, per-agent source counts (Claude subagent files in dozens), up to 16 sessions, rewrite probabilities scaled up from measured 0.01-5% so epochs occur in small runs, and a 3% "storm" session. Determinism is via `rand.NewPCG(seed, ...)`. Replay is `-model-seed=<seed> -histories=1`.

Shrinking (`shrinkModelHistory`, lines 1168-1183): greedy single-op deletion until a fixed point, with a budget of 200 reruns, accepting a candidate only if it fails with the same invariant id (not the same message). It cannot shrink sizes, seeds, or stage choices, and ops are not independent (deleting an Append changes what a later Capture sees), so it works only because ops have no preconditions. Doc says it reduced a mutant failure to 4 ops (production-gap-register-20261102.md:307).

Cost (measured): `go test -count=1 -run 'TestModelHistories$|TestModelGeneratorShape' ./internal/sessioncapture` passed in 25.65 s test time, 31.4 s wall, 40 histories, 2,244 ops. Doc says 200 histories is about 42 s non-race and 34-46 s for the default under race in a loaded run (gap register line 340ff; handoff "TestModelHistories 46.2 s").

Coverage guard (lines 1216-1230): the default run fails if it never exercises lost-ACK republish, job-committed fault, backoff gating, due delivery, policy refusal, multi-epoch, restart, fault fired, lost-ack fired. This is a good anti-vacuity device. My run printed `RepeatedCAS:0 AlreadyApplied:3 CaptureFaultAccepted:10 LostAcks:36`.

### What bugs it found

Measured from git: I found no commit whose message says the model found a production defect. The model landed in 64482d9 (cherry-pick of f612a58). Follow-up commits 44a0fac, 169ca96, bf3c21d extend its oracle. 44a0fac says "RED by mutation" (a mutant with the unlink disabled), meaning validation was by injected mutant, not by a found bug.
Doc says: "Mutant control (prefix check removed) failed at I1. The shrinker reduced the failure to 4 ops" and "0 violations" over 200 histories (production-gap-register-20261102.md:307). Doc also lists its own weak spots (lines 312-316): the lost-ACK-then-republish path never produced a repeated CAS (my run agrees, `RepeatedCAS:0`), only 9 of 200 histories hit a `job-committed` fault (my 40-history run hit 10), constant-allow policy and fixed clock in the first version, and process exits cannot test power loss.
Candid read: the model is a regression net and a specification, not a demonstrated bug finder. Its strongest evidence is that a deliberately broken prefix check is caught and shrunk. The model_optional history test (`model_optional_test.go`, 12 histories, seed 20261104) covers held optional artifacts, which the main generator does not (handoff says so).

### Limits (measured)
- "Restart" is a graceful `Close`/`Open` (`step`, lines 794-802), not a crash. Faults are error returns from the in-process hook, so SQLite and fsync durability are not exercised. The crash matrix covers that separately.
- The backend is `publication.Local`, so no S3 semantics (eventual consistency, ETag conditional write races).
- Single process, single writer per session. No concurrent captures or concurrent machines for one session.

## 3. Crash injection

Mechanism (measured, crash_process_test.go): the test binary re-executes itself (`os.Args[0] -test.run=^TestCrashProcessChild$`) with env vars naming a stage and phase. The child installs `Options.Fault` that calls `os.Exit(137)` when the stage name and the current phase match (lines 66-72). The parent records the phase from a file the child writes before each step, so the expectation is derived from where the child actually died.

Child workload: cap1 (capture "first\n"), del1 (deliver), cap2 (rewrite file to "first\nsecond\n" and capture), del2. Stages are the hand-written list `crashStages` (lines 38-43), 17 entries, each run for sequence 1 and 2 (reclaim stages only for 2) plus a no-crash control. That is 31 subtests.

Checked on reopen (`crashCase`, lines 132-232):
- `Open` succeeds. No unreclaimed temp file in `payloads/` and `usage()` equals physical bytes (`assertSpoolReclaimed`).
- Reclaim stages leave exactly one payload.
- Every job row has a publication id (no torn row); job count and max sequence equal `expectAfterCrash`.
- Pre-commit stages: recapture while source exists. Committed stages: the source is deleted before drain, so recovery cannot fall back on the original. This resolves finding T1 of docs/architecture-review-20261030.md:80 (same pattern at queue_test.go:126-139).
- After `Deliver`: current sequence did not regress, equals the final expected, exactly one epoch, exported body bytes equal the literal string, every job `published`, spool reclaimed again.

Boundary coverage versus the contract list (docs/session-capture-contract.md:150, "spool, job, upload, manifest, current-reference, and local-ack"):

| Contract boundary | Stages exercised |
|---|---|
| spool | payload-before-sync, payload-synced, payload-accept-before |
| job | job-before-commit, job-committed |
| upload | upload-before, upload-after |
| manifest | manifest-before, manifest-upload-before, manifest-upload-after |
| current reference | current-before, current-after, current-published |
| local ack | ack-before-commit, ack-committed |
| extra | reclaim-before-unlink, reclaim-after-unlink |

All six named boundaries are covered (measured). Gaps (measured by grep of `fault("...")` in non-test code): 11 production fault points are not in the matrix, namely `prepared-before-sync`, `prepared-before-cleanup`, `payload-before-cleanup`, `enrichment-admission-attempt`, `enrichment-result-stored`, `optional-repair-verified`, `runtime-hint-commit`, `runtime-init-statement`, `runtime-sweep-before`, `delivery-lane-selected`, `delivery-worker-lane-selected`. Other limits: one session and one source only; only sequences 1 and 2; local filesystem backend; `os.Exit` keeps the page cache, so lost fsync and torn writes are untestable (the gap register says so, line 316); the crash is always in the writer, never in the backend mid-request.
Design flaw to avoid in Rust: the stage list is a hand array. The check "crash stage not reached by child" (line 164) catches a stale name but nothing catches a new, unlisted hook. Generate the list from a single registry that production code also uses.

## 4. internal/syncer/simulate_test.go (legacy)

What it models (measured, lines 1-560): a `sim` with a seeded RNG plays 40 steps (`simSteps`). Each step performs 1-3 `change()` operations drawn from 16 kinds (append turn, half-written line, complete the half line, odd bytes, rewrite, truncate, delete, new session, recompress to zstd, touch, add a secret, break a config, append a huge line), advances a fake clock, then `syncSomehow` chooses among a normal hook sync, a sweep, a sync with N failing requests, a sync with lost answers, and a `kill` that snapshots the SQLite state mid-sync and restores it (simulating process death). A `faultyStore` injects failures per request kind (`faultyStore.fault`, line 708).
After each sync that completes: archive audit (`testenv.Archive.Audit`), every tracked file rebuilt both through the manifest and without one equals the file's bytes (`expected` removes an in-flight partial last line), any replaced earlier bytes still exist as a version, tombstones for deleted files, no secret in any decoded object, no remote delete, and a second sync uploads 0.
Runs 8 seeds by default, each in a child process (lines 45-110); `-simulations=200` plays more.

Evidence of value: commit ccc4aed (2026-10-14) "Fix data loss and corruption found by simulated session histories" lists four findings: a lost-answer stray segment truncating a file on manifest-less read, a retried copy overwriting a blob, machine-id races, and world-readable state files. Measured from the commit message.

Reusable as a spec? The oracle logic is, the code is not. The reusable parts are the operation vocabulary (half-written line then completion, recompress, same-size rewrite, delete, truncate), the fault vocabulary (fail N, lose the answer after the request landed, kill mid-sync with only already-flushed state surviving) and the check set (rebuild equals the file, replaced bytes retained, no secret, no remote delete, idempotent re-sync). The legacy `kill` models only "state at the moment of the Nth request", which is weaker than a filesystem-level crash model. The model_history test already ports most of this to the candidate path (the gap register at line 372 notes it had not been ported before then; it now has). Not ported: recompressed sources, config files with secrets mid-history (privacy canary covers a static case), and mid-sync kill with state restore.
The legacy format (v3 chunked archive, `testenv.Archive` record chains) is retired by the contract, so do not port `testenv` (1,327 lines, legacy-archive oracles).

## 5. Mutation testing history

Evidence (doc says unless noted; commits measured):
- First campaign (f5c98a4, merged in 8baabbb): "gremlins left 255 of 1,134 mutants alive". After triage most were killed by behaviour tests. Two findings: staging temp copies never removed after a killed run (a real leak, fixed) and a doc/code mismatch (64 segments vs 64 pieces). No data-loss bug.
- Tooling cost: `--timeout-coefficient=3` was unusable for `internal/sessioncapture` because gremlins derives the timeout from a 0.65 s coverage pass while the package takes tens of seconds (quality-gates.md:86; gap register line 360). 40 of 41 quota mutants timed out. Three consecutive quota mutation runs hit 1800 s or 3600 s deadlines without a result (handoff "T mutation" entries). The per-package full run is about 20 minutes on 4 cores (quality-gates.md:85).
- GAP-09 (gap register line 259): run `f-mut-quota-v2`, 524 s, 30 killed, 9 lived, 2 timed out, 1 not covered. Survivors exposed untested free-space arithmetic that was unreachable because `availableBytes` read real statfs. This is a real test-design finding (it forced an injectable seam), not a production bug.
- Scoped runs on the candidate path: `publication.go` 145 mutants, 124 killed, 11 lived (m-pub1), reduced to 1 equivalent after new tests; `publicationr2` whole package 137 killed, 22 lived (86.16% efficacy); `reclaim.go` 31 mutants (20 killed, 6 lived, 4 timeout) then 26 killed, 0 lived; `coalesce.go` 25 mutants (23 killed, 2 lived) then 25/25; `cadence.go` 16 mutants, 13 killed, 3 lived; rebase 12 killed, 3 lived, 1 unmapped; quota.go 50 of 51 killed after seam. The "M final" entry (handoff line 1274) states "completed, no real defects".
- Weak-oracle proofs by mutant pairs (gap register lines 324-342): W1 constant backoff, W4 stale object retained, W5 conflicting writers all report success, W7 skipped test hid disabled transform emission were confirmed weak. Two others (W2, W10) were refuted because the old test already failed.
- Test output of the campaign: new tests `internal/publication/mutation_boundary_test.go` (193 lines), `internal/publicationr2/mutation_boundary_test.go` (389 plus 58), `internal/sessioncapture/deliver_mutation_test.go` (168), `runtime_store_claim_test.go` (+87), `hang_mutants_test.go` in two packages (345). About 1,240 lines (measured via `git show --stat`). Commit e65c142 says each of 19 hang-type mutants previously "ended only by the package test timeout".

Verdict (my judgment): mutation testing earned its cost twice. First, as a one-off audit on the legacy codebase, where it found two small real defects and a large block of untested behaviour. Second, as a way to prove a specific weak oracle weak (W1, W4, W5, W7) and to expose the unreachable-statfs gap (GAP-09). It did not earn its cost as a continuing practice on the candidate path. Zero production defects were found across roughly a dozen scoped runs. The tooling fought the suite (whole-package rerun per mutant, no race or shuffle, hang mutants, block-buffered logs). And the mutant-killing tests it produced are often the weakest oracles in the suite: they assert an error class or a boundary constant, for example `deliver_mutation_test.go:104-110` only checks `errors.Is(err, ErrIntegrity)`, and `hang_mutants_test.go` exists purely to terminate error-check flips quickly. The principle "a surviving mutant needs a test of the behaviour it changes, not a test written to kill it" (quality-gates.md:85) was stated but the commits titled "mutation hardening" mostly did the opposite. Confidence: moderate. I did not triage each survivor.

## 6. Weak oracle catalogue

Method: I read about 30 test functions across 14 packages plus the Python suites, then scanned for tests with at most one assertion (115 hits, noisy). The test-behavior check is "would this pass if the subject did nothing". Items below are measured from the cited lines.

| # | Test | Pattern | Why |
|---|---|---|---|
| 1 | internal/sessioncapture/runtime_test.go:524-549 `TestRuntimeRetryBoundsAndSafeClasses` | Weak assertion plus constant pin | Backoff checked only as `1s <= d <= 5m`, so a constant 1 s backoff passes (confirmed as W1 by mutant). Line 546 pins defaults 64/64/8/30s |
| 2 | internal/sessioncapture/encoded_size_test.go:25-27 | Constant pin | `limit != 1<<30` restates `MaxEncodedObjectBytes`. Editing the constant fails the test, a defect does not |
| 3 | internal/sessioncapture/runtime_hint_stmts_test.go:23-26,40 `TestTxStmtsCachesAndFallsBack` | Implementation pin | Asserts `len(c.stmts) == 2` on an internal cache map. Any other caching scheme fails; a broken cache with two entries passes |
| 4 | internal/cli/capture_test.go:114-128 `TestCaptureRequiresExplicitLocalScope` | Absence only | Only `code != 0`. Passes if every `capture-v1` invocation fails for any reason, even if the scope check were removed |
| 5 | internal/cli/capture_runtime_test.go:139-146 | Plumbing pin | Sets `MaxInventoryRecords` then asserts the same value comes back from `runtimeQueueOptions`. Fixture asserts fixture |
| 6 | internal/sessioncapture/quota_linux_test.go:12-19 `TestFilesystemBlockSize` | Self-referential | Feeds a value and expects the identical value back for every input. A function that returns its argument passes, a mutant-kill pin |
| 7 | internal/doctor/hooks_test.go:109-117 `TestCodexDisabledFeatures` | Self-referential oracle | Expected value is `strings.Contains(setting, "false")`, a mini reimplementation of the parser. "malformed toml" has no "false" so expects false, never checks why |
| 8 | internal/scrub/text_test.go:167-185 `FuzzText` | Weak assertion | Checks only no input mutation, typed error, determinism. A `Text` that always returns nil passes. The detection behaviour lives in neighbouring table tests |
| 9 | internal/publication/fuzz_test.go:13-50 `FuzzParseCurrent` | Weak assertion | Every assertion is guarded by `err == nil`. A parser that rejects all input passes, and no seed asserts acceptance |
| 10 | internal/sessioncapture/fuzz_test.go:17-70 `FuzzQuotaWriter` | Self-referential | The oracle rebuilds the quota decision table (`switch` at lines 40-55) that `quotaWriter.Write` implements. A shared misreading passes. Still has value as a cross-check of accounting side effects |
| 11 | internal/sessioncapture/deliver_mutation_test.go:104-110 | Weak assertion | Only `errors.Is(err, ErrIntegrity)`. No check that nothing was written or the job state is unchanged |
| 12 | internal/publicationr2/backend_test.go:314-327 `TestConstructor` | Absence only | Only invalid options are rejected; no positive case in the body, so "always error" passes this test (other tests cover the success path) |
| 13 | tools/analytics/test_capture_journey.py:76-85 | Fixture asserts fixture (partial) | `summary['coverage'] == manifest['coverage']`, `manifest['identity'] == expected['identity']`: both sides come from the same Go writer run. The literal counts at lines 66-69 are the strong part |
| 14 | .../benchmarks: `make bench-smoke` (Makefile:99) | Invocation only | Runs each benchmark once so "a broken one fails CI". Asserts nothing about performance or correctness |
| 15 | cmd/agent-sessions/testdata/script/sync.txtar | Output text | `stdout 'uploaded 2 files'`: pins a message string of the legacy path. Legacy-only |
| 16 | internal/sessioncapture/runtime_audit_test.go, many "hang"/cancel tests, 31 `t.Skip` sites | Skip or termination only | W7 (discover_test.go:290) proved a `t.Skip` hid a disabled feature (gap register line 338) |

Findings T1 and T2 of docs/architecture-review-20261030.md (lines 80-105): both are now fixed in the tree (measured). T1: queue_test.go:126-139 removes the source before reopen for committed boundaries. T2: internal/capturedaemon/lifecycle_test.go:55-90 compares against test-owned `expectedServiceCommands`, with a zero-call dry-run check. The architecture review's own verdict (line 120-123) is that the sample "is not broadly tautological", and my sample agrees. Weak oracles cluster in (a) mutation-kill tests, (b) option plumbing tests, (c) fuzz targets with accept-guarded assertions.

### The 10 best tests (with the reason)

1. internal/sessioncapture/model_history_test.go `TestModelHistories` (line 1185). Independent contract model, literal bytes, anti-vacuity coverage guard, seed replay.
2. internal/sessioncapture/crash_process_test.go `TestCrashProcess` (line 292). Real process death, expectation derived from the recorded phase, source removed so recovery cannot cheat.
3. internal/sessioncapture/privacy_canary_test.go `TestPrivacyCanary` (line 142). Six encodings, raw and decoded, a positive sibling and a scanner-control that proves the scanner is not blind (lines 233-252).
4. tools/analytics/test_capture_journey.py `test_actual_capture_publication_reader_event_index_report` (line 47). Real binary, writer to export to DuckDB to report, then restores from the store after deleting sources and spool.
5. tools/analytics/test_publication_properties.py (Properties, line 166). Oracle from raw body bytes with stdlib `json` only, never importer output; seed replay by `PUB_SEED`.
6. internal/syncer/simulate_test.go `TestSimulatedSessions` (line 45). Four real bugs found (ccc4aed). Legacy format but the strongest bug-finding record.
7. tools/analytics/test_events.py `test_all_agents_tools_errors_humans_and_offsets` (line 107). Hand-written records, literal per-line byte offsets re-read from the raw bytes (`raw[offset:offset+length]`).
8. internal/capturedaemon/lifecycle_test.go `TestServiceStopStartRestartPreserveStateAndPlan` (line 55). Exact ordered argv per platform and action, dry-run executes nothing, plan and config preserved.
9. internal/publication/fuzz_test.go `FuzzDecodeValidateManifest` (line 52). Accept implies re-encode, re-decode, re-validate, cmp-equal and byte-stable: a real round-trip property that fails on a wrong encoder or validator.
10. cmd/agent-sessions/live_test.go:957 `TestLiveCodexIdentitiesMatchIndependentRust`. Golden hashes produced by an independent pinned Rust normalizer, not by this code. Rare and valuable (also directly relevant to the rewrite).
Close runners-up: internal/capturesource/fingerprint_linux_test.go:26 (exact `stat-v1:...` golden with extreme values), internal/sessioncapture/cadence_test.go:9 (table with exact boundaries), internal/publicationr2/mutation_boundary_test.go:86 (status 399/400/499/500 boundary table).

## 7. The end-to-end consumer gate

What `make capture-acceptance` runs (Makefile:~66-74, measured): builds a `CGO_ENABLED=0` binary, then runs `tools/analytics/test_*journey.py`, then `tools/candidate-live-r2/test_*.py`, then `tools/capture-delivery/test_*.py`, all with `CAPTURE_V1_BINARY` set. `test_capture_journey.py` carries `skipUnless(CAPTURE_V1_BINARY)`, so only the make target makes it mandatory (its docstring says so, lines 1-5).

Fixture generation: `agent-sessions capture-v1 fixture --output DIR` (internal/cli/capture_fixture.go:20-85). The inputs are handwritten literals in Go (main1, main2, child, a rewrite with a bad JSON line and a partial tail, lines 60-66). The binary then runs the real `sessioncapture.Open`, `Capture` x4 and `Deliver` against a local backend (or an SDK-factory backend), runs `publication.ExportSession`, and writes `expectations.json` from the same literals (lines 87-93). So the bytes and the manifest are writer-generated, while the expected retained bodies are input-derived. That is a sound oracle design: the expectation is the input, not the output. The fixture is a production subcommand compiled into the shipped binary, which a Rust rewrite should avoid.

What it proves (measured from the test code):
- The actual writer's manifest, objects and export validate under the Python importer's own validation, sequence equals 4, 3 sources, 3 retained bodies matching input hashes, a rewrite retains the earlier epoch.
- Import is idempotent (event ids equal across two imports) and the report's role counts, issue kinds (incomplete_tail 1, malformed_json 2) and epoch counts equal literals.
- Restore works with sources and spool deleted (lines 87-101), and re-import of the restored export gives identical event ids.
- Further journeys (lines 206-838): optional-artifact failure then crash then repair and privacy revoke, a broken session A does not block B, unsupported archive yields an error not an empty success, source move preserves epochs, configured cadence debounces in the real binary, quarantine and restart without another hook, derived sources restore and normalize.
- Credential handling of the launcher: missing or malformed credentials pause delivery before transport and ambient credentials are never used (test_launch_journey.py, lines 1-60).

What it does not prove:
- Python reads the Go reader's export directory (`metadata.json` plus bodies), not the bucket. The Python side re-validates metadata, but it does not independently walk the object store or verify the current-pointer chain.
- No real R2, no S3 conditional-write race, no multi-machine publication of the same archive. R2 is tested separately only with `AGENT_SESSIONS_TEST_R2=1` and `tools/candidate-live-r2` (doc says; I did not read it).
- No installed service, launchd or systemd, hooks in a real Claude Code or Codex process, or two-host runs (doc says these are the controlled acceptance step).
- Tiny data: three sources of a few hundred bytes. Large sessions, the 1 GiB bound and the multi-MB line tail are covered only in Go unit tests or the Python `WriterBacked` class with synthetic caps (test_publication_properties.py:478).
- Power loss and fsync are not covered at any layer.
- Normalization correctness is hand-asserted for one Claude shape and one Pi shape; no corpus of real transcripts (privacy forbids committing them, so this stays an open gap).
- The Python tests exit code caveat: gap register line 396 notes the analytics job pipeline masked failures through `tail`. The Makefile target itself uses `&&` and does not pipe, so the gate's status is honest (measured, Makefile lines 66-74).

## 8. Recommendations for the Rust rewrite

Principle driver: test behaviour, not implementation. Subtract before you add: do not port tests that pin Go internals.

### Carry (and why)
1. **Model-based history test as a custom seeded harness.** Keep the shape in model_history_test.go: generator, tiny contract model, invariant checks after each op, drain, coverage guard. Port the distributions (lines 254-345) as data. Add the missing shrinker quality (below).
2. **Crash matrix with real process death**, with a stage registry that production code owns so the test enumerates it. Extend to multi-source, N sequences, and a fault-injecting filesystem for torn and lost-fsync writes (the gap the Go suite names and cannot test).
3. **Privacy canary** with encodings, raw plus decoded scan, positive sibling and scanner-control. Port the idea verbatim and make the "scan every backend write" hook part of the fake backend, as `modelBackend` does (model_history_test.go:196-217).
4. **The Python consumer gate.** Keep it as the cross-language contract test with literal expectations. It is the only test that proves the reader contract for a non-Rust consumer. Move fixture generation out of the production binary into a test-only binary or xtask.
5. **The Python property tests (A1-A8)** are language-independent. Keep them unchanged; they pin the consumer's strictness parity with the reader (unknown fields, sequence bounds, metadata caps).
6. **Round-trip and accept-implies-valid fuzz properties** (`FuzzDecodeValidateManifest`, `FuzzCompressRoundTrip`, `FuzzParseObjectRoundTrip`, `FuzzJoinSplit`, `FuzzEditJSON`) as proptest or cargo-fuzz targets, plus a seed that asserts acceptance (fixing weakness 9).
7. **Exact-argv and exact-boundary tables**: lifecycle commands, retry delay table (the W1 replacement asserts `retryDelay(8)=2m8s`), status class boundaries, cadence bounds.
8. **Step-count termination budget** (`hangBudget`) instead of wall-clock timeouts, as a test utility for the retry and delivery loops.

### Drop
- Mutation-kill tests as a category (`*_mutation_test.go`, `hang_mutants_test.go`). Rewrite behaviour where it matters; do not port the pins.
- Tests that read private fields (`len(c.stmts)`, `q.used`, `q.writing`), constant pins (`encoded_size_test.go:25`), plumbing tests (capture_runtime_test.go:139), and tests that pin SQL or index plans (`TestRunnableClaimQueriesUsePartialIndexes`, enrichment_index_test.go) unless the Rust design keeps SQLite with the same plans. Measure performance with benchmarks instead.
- Everything legacy: `internal/syncer` (30 files), `internal/testenv`, `internal/state`, `internal/store`, `internal/snapshot`, the four testscript files, and `Fuzz*` for retired legacy packages, unless the contract keeps that behaviour. Doc says legacy defaults remain until cutover, so they stay in Go until then.
- Coverage-percentage and CRAP gates as drivers of test authorship. quality-gates.md:82 already warns about this. Use cargo-llvm-cov only as a report.
- Sleep-based tests (33 `time.Sleep` sites). Use the fake clock.
- `bench-smoke` as a correctness claim.

### Where proptest or model-based testing earns its place (specific invariants)
- **Epoch rule** (contract section 1): for any byte sequence of captures, epochs equal the prefix-extension model, `epoch_ids` are unique, sealed epochs never change. Pure function, ideal for proptest with `Vec<Vec<u8>>` strategies and structural shrinking, which the Go shrinker cannot do. Add partial tails and same-size rewrite as explicit strategy variants.
- **Manifest and current-pointer codecs**: decode then validate then encode is stable, strict unknown-field rejection, sequence in `(0, i64::MAX]`, digest lowercase hex of length 64 (the same properties as FuzzParseCurrent and FuzzDecodeValidateManifest). Plus differential property: Rust reader accepts exactly the set the Python importer accepts for generated metadata (A8 probes become generated cases).
- **Quota and accounting**: for any sequence of write, short write, error and release, `used` equals the sum of live payload sizes, never negative, `writing <= used`, and the classification order (integrity, then encoded size, then quota) holds (FuzzQuotaWriter's table).
- **Retry and backoff**: delay is monotone non-decreasing in attempt, bounded, jitter-bounded, and the due selector never returns a job before its `next_retry`.
- **Redaction**: for generated config documents (JSON and TOML) with secrets in random positions and encodings, output contains no secret and still parses, and non-secret keys survive byte-for-byte (the property in `FuzzEquivalentSecretCopies`).
- **Model-based (stateful)** for the Queue end to end, as in model_history_test.go. Use a custom seeded harness over a deterministic simulation rather than a proptest state machine, because the history includes injected time and faults, which are easier to keep replayable as `seed -> history` (the doc's `-model-seed` replay works well). Use proptest shrinking of the `Vec<Op>` plus per-op shrink of sizes. Keep the coverage guard so a run that never reaches lost-ACK republish fails.
- Not worth proptest: filesystem discovery, SQLite schema tests, CLI parsing, S3 client wiring.

### cargo-mutants
Worth it as a periodic, non-gating diagnostic, run per crate with `--in-diff` against main, on three crates only: the epoch and manifest model crate (pure logic), the quota and retry/backoff module, and the redaction/scrub crate. Reasons, with Go evidence: mutation found real value only where logic was small, pure and had arithmetic boundaries (quota.go GAP-09, publication.go validation, cadence.go). It wasted effort where the unit was a whole queue with a slow package suite (sessioncapture) and on error-check flips. Rust's advantages (per-mutant incremental builds in nextest, no whole-package serialisation) shrink the cost, but only if the suite stays fast. Rule: a surviving mutant is evidence for a missing behaviour test, never a test target. I did not measure cargo-mutants; this is inferred.

### Deterministic simulation
Design implied by the Go tests (which already inject `Now`, `Fault`, `CheckPolicy`, and a backend):
- **Clock**: a `Clock` trait injected everywhere; the simulation advances it with the burst/idle mixture (model_history_test.go:335-345). Backoff and cadence tests become pure arithmetic. No `sleep` in tests. The Go `hangBudget` pattern becomes a step budget in the executor.
- **Filesystem**: a `Fs` trait with a fake that tracks durable versus page-cache state, so "crash" means "drop unsynced writes, optionally tear the last write". This closes the biggest gap the Go suite names (power loss). Real-filesystem tests stay for permission modes and symlinks only.
- **Backend**: a fake `Backend` with injected failure kinds already present in Go: fail N, lose the ack after the write landed (`modelBackend.lose`, lines 96-107), plus S3 conditional write semantics (If-Match, If-None-Match). It records every put and CAS for the privacy scan and for the "second Deliver writes nothing" check.
- **Process model**: one single-threaded simulated runtime per history; crash equals dropping the runtime and reopening from the fake fs state. Add a small number of real-process crash tests for SQLite or WAL behaviour, as the Go matrix does, because the fake cannot prove SQLite itself.
- **Replay**: `SEED=<n> HISTORIES=1`, failing history printed as JSON, as the Go test does.
Honest scope: the Go suite needs this layered approach because its SQLite and its fsync usage cannot be faked. If the Rust design keeps SQLite, keep the real-process crash matrix for it.

### Fixtures to port verbatim as golden tests
- The fixture inputs in internal/cli/capture_fixture.go:60-66 (main1, main2, child, rewrite with `not-json` and a partial tail) and the expectations they imply (sequence 4, 3 sources, 3 retained bodies, role counts, issue counts). Treat as the writer-reader golden.
- cmd/agent-sessions/live_test.go:957-977 Codex hook identity hashes (independently produced) if the Rust port retains that identity scheme.
- internal/capturesource/fingerprint_{linux,darwin}_test.go golden `stat-v1:...` strings with extreme values.
- Seed lists of `FuzzParseCurrent`, `FuzzDecodeValidateManifest` (valid, empty, withheld, overflow sequence `1<<63`, lone surrogate `\ud800`, 2000-element source array, BOM, 5000 nested brackets).
- Canary variants (privacy_canary_test.go:24-32) and the cadence table (cadence_test.go:11-26).
- tools/analytics/test_publication_properties.py A8 probe tables (unknown-field, sequence, size bounds).
- internal/capturedaemon/lifecycle_test.go `expectedServiceCommands` argv tables per platform if the service plan format stays.

### Existing test files that can serve as executable specs
- internal/sessioncapture/model_history_test.go (model plus distributions), crash_process_test.go, privacy_canary_test.go, model_optional_test.go (optional artifact repair semantics).
- tools/analytics/test_capture_journey.py, test_publication_properties.py, test_publication_refresh_journey.py, test_events.py (normalization semantics and offsets), tools/capture-delivery/test_launch.py (credential handling).
- internal/sessioncapture/rebase_test.go and coalesce_test.go for the rebase and supersession semantics (contract-derived exact assertions, per handoff), cadence_test.go, queue_test.go `TestRestartAndLostAck`.
- internal/publication/*_test.go and mutation_boundary_test.go for the exact manifest and descriptor bounds (the bounds are worth keeping; the structure is mutation-driven).
- internal/syncer/simulate_test.go only as a vocabulary of file-mutation and fault operations.

## 9. Open questions for the operator
1. Should Python remain a permanent independent consumer with its own validation (the A8 strictness-parity model), or should the rewrite expose the reader as a library the importer calls? The first keeps a true cross-implementation check, the second removes a drift class but also removes the independent oracle.
2. Is power-loss durability a stated requirement? Without it the Rust fake-filesystem work is optional. The contract names crash and restart (line 150) but I found no explicit fsync or torn-write requirement.
3. Will the fixture command stay in the production binary? It is a test hook shipped in the product.
4. Is a corpus of redacted real transcripts acceptable for normalization tests, given the privacy rules? The suite today has only synthetic records.
5. Should the legacy suites be retired at cutover (62k lines includes about half legacy-path code, inferred from the directory counts) or ported in part?

## Things I could not verify
- The `TestModelHistories` and crash matrix numbers other than the 40-history run (25.65 s) come from docs, not my runs. I did not run the race build, the full suite, `make fuzz`, the Python gate, or mutation.
- I did not run or read `tools/candidate-live-r2`, `internal/publicationr2/candidate_live_acceptance_test.go`, or the legacy `recovery_test.go`/`sync_test.go` bodies in depth.
- Whether any production bug was found by the model test is based on commit messages and docs, which I searched but could not exhaustively audit; a bug fixed in a commit that did not mention the model would be invisible to me.
- Number of tests that would pass if the code did nothing is not measured. My catalogue is a sample of about 30 functions, so it supports the patterns, not a rate. The scan for "at most one assertion" (115 hits) is a lead list, not a finding.
- The 255-of-1,134 gremlins result and all per-run mutation counts are from commit messages and docs.
- The `go test` fuzz stall "workers stalled after about 6-12 s" (gap register line 313) was stated as unresolved there; I did not check whether the later `-fuzzminimizetime` fix resolved it beyond the quality-gates.md:90 description.
