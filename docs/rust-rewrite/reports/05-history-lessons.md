# Project history review: what went wrong and what the Rust rewrite must encode

Reviewer role: project history. Read-only. No repository file was edited, no network or host was touched.

Labels used throughout. **[M]** measured by me this session (git, gh, wc, file reads). **[D]** the document says so, I did not re-measure. **[I]** inferred. **[G]** guess.

Paths are relative to `/Users/mm/dev/memory`. `handoff` means `docs/session-capture-handoff.md`. `gap` means `docs/production-gap-register-20261102.md`. `adv` means `docs/adversarial-review-20261029.md`. `holi` means `docs/holistic-review-20261029.md`. `arch` means `docs/architecture-review-20261030.md`. `a27`, `a29`, `a30` mean the three dated handoff archives.

## Findings first

1. **The same quadratic cost was found and "fixed" three times, then rebuilt by the rewrite.** PR #2 found transcripts re-uploaded T/2 times (2026-10-11). The v3 fold/chunk design fixed it. The contract rewrite chose full-epoch snapshots as the baseline and GAP-03 measured E·k/2 again (5.5x at k=10, 50.5x at k=100). Epoch retention then produced quadratic manifests (Mac 578 MiB versus about 8 MiB latest-only). Growth was never a design input. It was a post-hoc finding each time. [M git; D PR #2, gap GAP-03, handoff L1777-1810]
2. **Defects that mattered were liveness and efficiency defects, not data-correctness defects.** The gap register says the correctness core held under much stronger tests. Everything that stopped live operation was a classification, bound, or cost problem. [D gap Verdict]
3. **Five live activation attempts each found a new production defect that 62k lines of tests had not.** Crash loop on a deleted-source hint, an optional-repair livelock, an enrichment lifetime-cap spin, then two idle-CPU profiles. [D handoff L1910-2060]
4. **The documentation became a cost center.** 19,713 lines under `docs/` [M wc]. The handoff is 2,227 lines and four archives hold another 10,206 [M wc]. The holistic review already complained that a "compact" handoff had 1,007 lines. [D holi R9]
5. **The strongest evidence in the repo is executable and adversarial**: RED/GREEN overlays, mutant pairs, process-exit crash matrix, model-based histories. The weakest was prose ledgers, admission markers and "parent accepted" chains.
6. **Most lessons are already known to the authors as text.** The rewrite should convert them to types, schemas, lints and test harnesses. Section 6 ranks 15.

## 1. Dated timeline of phases

Commit-per-day counts from `git log` [M]: Oct 13 = 65, Oct 14 = 45, Oct 15 = 35, Oct 19 = 12, Oct 20-27 = about 15 on main, Oct 28 = 80, Oct 29 = 36, Nov 2 = 36, Nov 5 = 25. Total 430. The Oct 20-27 trough is not idle time. The candidate was being built in a dirty worktree (see phase 4). [I]

| Phase | Dates | What it was | Decision that ended it |
|---|---|---|---|
| 0. v1/v2 collector | Oct 10-12 | Python-like collector into R2, data contract v2, ported to Go (PR #1, #3). PR #2 (Oct 11) "Upload only appended bytes" because a T-turn session sent about T/2 times its final size. [D PR #2] | Port to Go with quality gates, CRAP gate, mutation. |
| 1. v3 archive | Oct 13-19 | Simplify (PR #9 drop periodic timer, PR #10 cut six jobs, about 1,000 lines), simulation and mutation tests (PR #13), secret fixes (PR #14), v3 append-only plans/commits, retained generations, machine-wide recovery (PR #15), manifests by reference (PR #17), fold fixes (PR #18). | Oct 19-23. The archive was stuck: dirty checkpoint, R2 HEAD 403, recovery holding the global lock, 819 pruned historical pieces for one stream. [D progress-history L152-162] A 1,323-piece, 260 MB hand prune (PR #19) made cold recovery impossible without a "pruned-piece" patch. |
| 2. Chunking experiment | Oct 15 | SQLite fixed 1 MiB chunk publication, offline chunk-sizing harness, live R2 chunk acceptance (22 pieces, 21 unique). [D live-chunks.md, chunk-experiment.md] | Contract: chunking is "optional, measured". Docs relabelled as "historical v3 probe, not target". [D chunk-experiment.md L3] |
| 3. Cold-recovery patch stack | Oct 22-23 | PR #23 "verify cold-recovery records concurrently", plus a pruned-piece recovery change. PR #23's own measurements did not reproduce the suspected sixfold regression (896 s vs 831 s). [D PR #23] | Contract review 2026-10-23 and operator: retire the stack. PR #23 closed as "contract-retired". [D research/session-capture-contract-review.md; handoff L125-130] |
| 4. Contract rewrite + candidate capture-v1 | Oct 23-27 | Contract written (`docs/session-capture-contract.md`). Candidate built: `b68a412` (278 files, +44,161), `c9749da`, `b23712b` all Oct 24. [M git show --shortstat] Roughly 77 dirty paths held un-committed scaffolding, ACK bitmap, Python qualification layers, private "admission" harnesses. [D a29 L88-124, a27 L125-153] | Oct 27: operator approved cuts and a reduced baseline `65759df` (preserved on `preserve/pre-simplification-20261027`). [D a29 L88-124] |
| 5. Reliability and installs | Oct 27-30 | Installs #1-#8 on Mac. Run-fatal step errors, SQLITE_BUSY, repair claim race, O(DxK) near-first publication, quarantine 22/23, battery sleep. W-series fixes. Containment of both hosts (Oct 28-29). Adversarial review (R1-R10), holistic review, architecture review. PR #30 integrated 139 commits and 458 paths on Oct 30. [D a29, a30, handoff L105-125] | Both hosts disabled. Source merged but "not proved working live". |
| 6. Gap register and delivery stack | Nov 2-4 | Offline production-readiness analysis found GAP-01..11 with RED experiments. Fixed on `gap/integration`, restacked as #34-#38 (liveness, admission, coalesce, consumer, docs). [D gap, handoff L431-L1336] | Stack merged `d975961` Nov 4-5 after CI test-time margin fixes. |
| 7. Live rollout | Nov 4-6 | Operating steps 1-4. Five Mac activation attempts, BC-01 activation, PRs #41-#53. Merged main `57d8400`. [D handoff L13-60] | Not ended. Status: running on both hosts, not accepted. Refresher degraded. Epoch retention and BC-01 Codex trust open. |

Decision log between phases:
- Phase 1 to 4: "Greenfield session-local capture, not an archive migration/compatibility project." [D progress-history L164-189]
- Phase 4 to 5: operator "approved cuts" and "proceed through steps 0-3 without checkpoints". [D a29 L88-100]
- Phase 5 to 6: containment, then the gap analysis was explicitly "offline source analysis and experiments only". [D gap header]
- Phase 6 to 7: authority steps 1-4 allowed, step 5 (defaults, retirement, deletion, reset) excluded. [D handoff L26-40]

## 2. Catalogue of failures and defects

Format. **ID title.** What broke. Root cause. Found by. Fix. Lesson (structural). Citation.

### 2.1 Durability and crash

- **D1 Orphan segment after a lost answer (v3).** A segment that landed unrecorded sorted ahead of its replacement, so the reader rebuilt a truncated file. Root: upload and local record were not one atomic fact. Found by seeded simulation (PR #13). Fix: a `strays` table written before send. Lesson: ack loss is a first-class state in the model. Cite PR #13 body. [D]
- **D2 Wrong bytes stored under a content hash.** Lost reply to a fold upload, then a rewrite, then a copy stored different bytes under the hash. Fix: conditional copy on source sha256, HEAD plus `CopySourceIfMatch`. Lesson: content-addressed writes verify content at write. PR #14. [D]
- **D3 Machine id race.** Concurrent first runs each minted an id, an empty file minted one per run. Fix: write temp then link. Lesson: identity creation is a single atomic constructor. PR #13. [D]
- **D4 World-readable state DBs and stranded staged copies.** PR #13. [D] Lesson: file creation APIs take a mode type; no bare create.
- **D5 Crash before payload sync leaves an unreclaimed temp file (GAP-10).** Found by real process-exit crash matrix C1 (`os.Exit(137)` at 14 boundaries x 2 sequences, 28/28 green otherwise). Fix: open-time sweep. Lesson: crash points are injected into production code and driven by a harness that really kills the process. gap GAP-10, section 2. [D]
- **D6 Importer failure replacement erased the last durable failure.** `DELETE FROM failures` then a separate INSERT, interruption between them produced `failures=0, stale=false`. Found by adversarial probe with injected interruption. Fix: atomic UPSERT, keep old failure if replacement cannot commit. Lesson: health facts are monotonic writes, never clear-then-set. adv R2. [D, reproduced by their parent]
- **D7 `immutable=1` SQLite copy ignores `-wal`.** A disposable replay produced a stale ref and a false "payload integrity failure". Found live, Nov 5 attempt 3. Lesson: the SQLite open mode is a type (`ReadOnlyConsistent`) and `immutable` is not constructible when a WAL exists. handoff L1946-1960 region (attempt 3). [D]
- **D8 SQLite WAL backup under a live writer (C4/P12).** No torn snapshots found. Concurrent-writer failures became typed `ErrSQLiteSourceChanged`, attributed only when a recheck proves the source moved. Lesson: "source changed" is distinct from "backup failed" in the error type. handoff L1336-1420 region. [D]
- **D9 Crash test repaired away loss of committed work (T1 in arch).** `TestRestartAndLostAck` allowed recapture when status was empty, which also covered the `job-committed` fault where the job was already durable. The original source masked the loss. Lesson: crash tests delete the original after the committed boundary. arch T1. [D]

### 2.2 Concurrency, locking and lanes

- **C1 Raw/optional lane switching across a scheduling boundary.** `runtime_store.go:396-403` checks the lane, then a helper reselects, and `runtime_delivery.go:39-64` rereads the head after locking without rechecking the lane. A concurrent transition could spend raw budget on optional work. Found by deterministic barriers (arch A1). Fix `881b04e`. Lesson: lane is carried by the claim value, not re-derived. [D]
- **C2 Repair-claim race with delivery.** `66ee491` "claim optional repair rows atomically so delivery is never raced". Found in the W-series. [M commit subject]
- **C3 Open-path SQLITE_BUSY.** `ef727a9` busy timeout per connection before locking statements. [M commit subject]
- **C4 Raw delivery and optional-repair livelock (live).** After backlog drain the Mac stayed at about 2.6 cores with about 25 superseded jobs per minute, repairs at 14-15 attempts. Root: `beginOptionalDelivery` set already-repaired rows to `delivering`; concurrent repair superseded the job; `finish()` set every `delivering` row to `retry`; the latch re-withheld them. Found live (attempt 2), diagnosed by code read then RED test. Fix `632d596`, PR #48. Lesson: transitions are a single state machine with a property test that "progress is eventually made" under interleaving. handoff L1989-2040. [D]
- **C5 `Run` exited on transient step errors.** `1706c00`. Fix: only durable-store failures stop Run. Lesson: error scope is a type (`Fatal`, `Item`). [M subject; D a29 L100-110]
- **C6 Two definitions of "an attempt".** `Deliver` and runtime delivery each persisted results and ACKs separately (holi R4). Merged in `ee09321`. Lesson: one transition module, two schedulers. [D]
- **C7 Heterogeneous queue singleton.** `Queue.sessions` normally held mutexes but a reserved string held another type and was type-asserted, with a panic path (holi R10). Lesson: typed fields, no `Any` map. [D]
- **C8 Hint burst failure.** First read as a production lost wakeup, then root-caused as a test defect. Lesson: a failing concurrency test first gets a deterministic barrier before a product diagnosis. handoff L1560 region. [D]

### 2.3 Retries, quarantine and liveness

- **L1 Transient failures quarantined after 8 attempts (GAP-01, Critical).** A roughly two-minute uplink flap quarantined every delivering session and blocked newer jobs in the same session. Cite `runtime_delivery.go:144`, `runtime.go:96`. RED E1. Fix: never quarantine Transient, Timeout or Throttled. [D gap GAP-01]
- **L2 30 s total request timeout made large objects unpublishable (GAP-02).** `capturebackend/backend.go:133` plus an operation timeout. Object size ceiling about 75 MB at 2.5 MB/s. Fix: body budget 30 s + size / 1 MiB/s, idle timeout, floor 60 s, cap about 15 min. Marked conservative and unmeasured. [D gap GAP-02, handoff L490-520]
- **L3 Inventory cap counted absent published rows (GAP-04).** BC-01 inventory 29,082, 89% of the installed 32,768. The default of 4,096 was unusable. [D handoff L520-545, gap GAP-04]
- **L4 1024-source cap overflowed as a transient error (GAP-05).** A rewrite-heavy session reaches it permanently. Fix: typed `source-limit`, visible coverage. Parent EXPLAIN found the new state missing from the partial index. [D gap GAP-05, handoff L540-560]
- **L5 Rebuilt local spool never converges with an existing remote (GAP-06).** `capture.go:300`, `deliver.go:169`. Disaster-recovery path. [D]
- **L6 Orderly shutdown consumed poison budgets in optional workers.** Cancellation persisted as failure; at limit it quarantined healthy work. Fix `d374948`; shutdown spends no attempt `ae8e20d`. adv R4. [D, M subject]
- **L7 Source absence treated as run-fatal.** `runtime_fatal.go:16-29` treated any `PathError` as fatal. Live attempt 1: coordinator exited rc 1 about 20 s after start. A hint whose source was deleted was never removed, so every restart crash-looped. Mac inbox 337 hints (5 absent), BC-01 499 (89 absent). Fix PR #47 `0721bdf`. First flagged as a blocker in Oct 28-29, repair sat uncommitted in a worktree for days. [D holi R1, a29 L26-40, handoff L2041-2060]
- **L8 Enrichment "bounded count" was a lifetime cap.** 4,096 `enrichment_work` rows all terminal `attached`, nothing removed them. Every native-evidence acceptance failed `ErrQuota` and retried 32 files every 100 ms (about 22% of samples). 1,409 jobs with `enrichment_gap='quota'`. Fix: PR #49 stops the spin; retirement left as an operator decision. handoff L1946-1975. [D]
- **L9 Quarantine 23 and O(DxK) delivery.** 8 timeouts. One Deliver ran `CheckEnrichmentAsset` A x (5 x R_sel + U) times, for seq 40 that was 19,425 calls at about 1.11 ms. The cause was equal asset bytes keyed per `Source.SourceID`, so each subagent attached its own copy (open sessions held up to 17 copies). Quarantine 22: D=110 of K=123 objects (28.6 MB) in one 60 s attempt. Fixes W40/W41, `3cee11b`, `5bb81d9`. [D a29 L564-592, L863-892]
- **L10 Launcher made destination credentials a local-capture startup dependency.** The wrapper eagerly parsed the credential file before `execve`. Missing file blocked all capture. adv R1. Fix `2ecdf10`. [D]
- **L11 Readiness checks blocked teardown.** With exclusions dir missing, service stop, hook uninstall and work-status all failed. adv R9. [D]
- **L12 Battery sleep broke the autonomous run.** Mac clamshell sleep at 18:14 stopped the `efbb99e` run and invalidated gate runs (W23). Gates required AC power and `caffeinate`. a29 L85, L556. [D]
- **L13 BC-01 Codex hook trust stale.** Operator action needed, agent must not write `trusted_hash`. handoff L16-25, L80-90. [D]

### 2.4 Quadratic growth and efficiency

- **Q1 v1/v2 full re-upload (PR #2).** T/2 times the final size. [D]
- **Q2 Manifests copied history (PR #17).** Manifests were 6.4 of 8.0 GB of the bucket, one Pi session 4 GB. Fix: reference file streams. [D]
- **Q3 Fold rewrote nearly everything (PR #18).** 160x rewrite factor on one session, 12% of the bucket duplicate fold copies. Fix: keep large earlier pieces, recompress the tail, about 5x. Also 112 of 170 snapshot sets incomplete because the walk budget was shared. [D]
- **Q4 Full object per append (GAP-03, Critical efficiency).** 50 appends produced 50 PUTs and 50 CAS; spool bytes exactly chunk x k(k+1)/2. Measured workload amplification 4.5-10x (hook per turn) to 228-1294x (every append). Fix: subsumed-job supersession, quiescence debounce Q=30 s, M=300 s, local reclamation. [D gap GAP-03, handoff L494-500, L1014-1120]
- **Q5 Delivery hashed and decompressed every payload twice (adv R7).** Four encoded reads and two decompressions per payload per attempt. [D, static]
- **Q6 Unchanged transformed sources re-hinted and re-prepared every sweep (GAP-11).** `classify.go:132` sets `Fingerprint` only when `transform==""`. Prototype: 3 unchanged calls cost 1.6 ms raw but 4-6.7 ms for zstd, scrubbed config, SQLite backup. [D gap GAP-11, arch Efficiency]
- **Q7 Admission COUNT was quadratic.** About 50 s at 30k sources, found as the installed discovery timeout. Fix `HintBatch`. Remaining 25% of walk CPU was rebuilding exclusion registry and policy per callback. Fix `acba80f`, 27.9 s to 13.4 s. handoff L1420-1440 region. [D]
- **Q8 Idle CPU stayed 0.29-2.3 cores after drain.** Profiles named: per-file policy Stat/Lstat 39%, enrichment spin 22%, `ReadOwnership` opening a SQLite connection per artifact 28%, `classifyExternal` re-running `allowed` 14%, `Resolve` Lstat 14%, inbox ancestor fsync every 100 ms tick about 4.5%. Fixed by PRs #49-#53. Final: Mac 0.13 cores over 3 h, BC-01 0.06. [D PR #51, #53, handoff L13-25] Note Mac QoS triples CPU, so numbers depend on supervisor class.
- **Q9 Epoch retention is quadratic (open).** `~/.claude.json` 206 epochs in 8 days. `thread_history_1.sqlite` 6 epochs, 1,232 MiB encoded, about 205 MiB per Codex run. Old job rows keep full manifests, so queue manifests are 578 MiB against about 8 MiB. Two targets are `export_too_large` against 768 MiB. handoff L1802-1810, L63-75. [D]
- **Q10 Retained-history scans.** `Status()` loads every job. `usage()` stats every payload file per snapshot. Due-work selection cost scales with pending non-head revisions (0.7, 1.5, 13 ms at 1, 10, 100 revisions). Measured alternatives were rejected after trade-offs (42.5 to 745 us regression on sparse cases). gap static items, arch Efficiency. [D]
- **Q11 Test cost is quadratic too.** `TestSourceLimitIsTypedAndNotQuarantinedAsTransient` did 1030 real captures at O(sources) each, 4m48s-7m50s on macOS CI, and sessioncapture went from 150 s to 356-495 s. `make crap` hit the unchanged 600 s timeout on every stack layer. Fix: per-Queue limit knob. handoff L1370-1420. [D]
- **Q12 Analytics report materialised all sessions, failures, sources per HTTP request (holi R8).** Fix `1db3475` bounded summary. [D]

### 2.5 Privacy and exclusion

- **P1 Secrets slipped redaction (PR #14).** Backup copies (`settings.json.agent-sessions-backup-*`) uploaded unredacted env; `.env` files; command lines with `--api-key`; MCP args; Codex otel headers. [D]
- **P2 Secret-bearing Pi migration backup uploaded.** Found in the two-machine archive audit. production-readiness.md L16-22. [D]
- **P3 Secret filter over-blocked.** It withheld source files over Python like `key=lambda r: r[0],`. 3 skill scripts never uploaded (PR #18). Lesson: scrubber has a corpus of benign false positives. [D]
- **P4 Alias policy re-ran a legacy whole-project ownership scan.** Unbounded `Glob` over every project, and chose the first matching slug instead of the durable owner. No bypass was shown. adv R8. [D]
- **P5 One Codex exclusion denies every Codex shared resource.** By design `exclusion.Registry.Excludes` treats an empty session key under an excluded root as excluded. 7,909 active `policy_denied` issues, no Codex resource captured since. A suspected leak triggered precautionary containment at 17:46Z and was disproved. handoff L1777-1800. [D] This is a design question (shared multi-session stores).
- **P6 Positional join of evidence to sources.** `es[j]` applied to `m.Sources[j]`. A same-length permutation looks valid. holi R5. [D]
- **P7 Per-upload full-session privacy recheck was the O(DxK) driver.** Decision W25: recheck the uploaded object's own evidence per upload, full-session passes at batch boundaries only. This weakened an earlier, stricter window on purpose. a29 L585-592. [D]

### 2.6 Identity

- **I1 Pi sibling directory changed identity and role.** `internal/adapters/pi.go:71-73` classified by whether `<root>/sessions` exists. Creating it changed SourceID and SessionID and role main to artifact. adv R5. Fix `b0b4f9c`. [D]
- **I2 `/var` versus `/private/var`.** A test home path yielded an extra identity (attempt 4). Root-caused and fixed in the test. handoff L1910-1925. [D]
- **I3 Source-id keyed enrichment copies.** See L9. [D]
- **I4 No admission-time R2 key-length check.** `publicationr2/backend.go:60` limits keys to 1024 bytes. Long identities fail at delivery and (before GAP-01) quarantined. gap static items. [D, static]
- **I5 Repointed endpoint kept old-binding targets.** holi R3, fix `47ae92d`, `f57de35`. [D]

### 2.7 Consumer mismatch (Go versus Python)

- **G1 Python validated more loosely than the Go reader (GAP-08).** Accepts `sequence=0`, 16 MiB metadata (Go 8 MiB), unknown fields, raw DuckDB `BinderException`. `manifest_sha256` stored is a local fingerprint matching no Go digest. gap GAP-08. [D]
- **G2 Export cap counted cumulative retained epochs (GAP-07).** A session with a tiny latest epoch and large history could never import. Parent chose a cumulative 1 GiB cap with typed `export_too_large` over per-epoch. Still bites at 768 MiB flag on two targets. gap GAP-07, handoff L520-540, L63-75. [D]
- **G3 Codex mirror pairing omitted in the candidate importer.** `event_msg.user_message` and `response_item.message` both counted as human turns. arch A2. [D]
- **G4 One lone surrogate or deep nesting poisoned a whole session import.** `UnicodeEncodeError` and `RecursionError` outside per-record isolation. adv R6, arch A3, fix `abc7ee8`. [D]
- **G5 Export envelope exceeded the metadata limit for an accepted manifest.** 4.2 MB manifest published but could not be exported. adv R10, fix `e5393fc`. [D]
- **G6 Dashboard could be falsely green.** Retry registration cleared the error before projection success (adv R3). A completed remote sweep hid loss of a known root (holi R2). Target scope disagreed with active endpoint scope (holi R3). Fixes `3112872`, `47ae92d`. [D]
- **G7 Refresher regression now.** Since 18:38Z Nov 5, BC-01 endpoint `process_failed`, 66 `current-unreadable` heads (46 Mac, 20 BC-01), Mac sweep slowed from about 19 min to about 2 h, `run()` discards stderr so the failing page is unknown. handoff L26-40. [D, open]
- **G8 Reader digest compatibility.** Worker C's change would have caused false `publication_conflict` on legacy projections and generic `invalid_metadata` on a pre-field reader. Caught by parent review, fixed `1638b31`. [D handoff L520-535]

### 2.8 Test oracles

- **T1 W1** backoff test checked only `1s <= d <= 5m`. Constant-backoff mutant survived. New exact table failed `retryDelay(8)=1s`, want `2m8s`. gap section 3. [D]
- **T2 W4** epoch test checked counts and states only. Stale-object mutant survived. [D]
- **T3 W5** two-writer CAS test not tied to the winning body. 16-writer test found 15 "winners" under the mutant. [D]
- **T4 W7** `t.Skip` when no transform was emitted. Mutant (transform disabled) passed. [D]
- **T5 Supervisor tests** compared call count to the production-generated plan (arch T2). [D]
- **T6 Model-history weak spots.** The lost-ACK-then-republish path never produced a repeated CAS (found to be impossible by design). Only 9 of 200 histories hit `job-committed`. Constant-allow policy and fixed clock. gap section 2. [D]
- **T7 Fuzz stalled** after 6-12 s of each 60 s campaign, cause unidentified, so fuzz results were "inconclusive". `scrub.FuzzEquivalentSecretCopies` was not in `FUZZ_TARGETS`. [D gap section 3]
- **T8 Mutation tool unusable.** `--timeout-coefficient=3` gave 40 of 41 quota mutants timing out; coefficient 100 gave valid results. [D]
- **T9 Analytics jobs piped through `tail`** so container exit was 0 on unittest FAILED. [D gap section 5]
- **T10 Rejected oracle thinning.** CM worker's `508c3de` cut oracle seeds 12 to 6 to save CI time and was rejected. [D handoff L1500 region]

### 2.9 Process and rollout

- **X1 Candidate lived uncommitted.** The Oct 24 checkpoint was 278 files, +44k lines. The pre-simplification worktree had 77 dirty paths. Source-absence repair sat uncommitted in a worktree while installs proceeded. a29 L26-40, L88-124. [D/M]
- **X2 Delivery-stop coordination failure.** An agent treated its own chosen work unit as completion, grouped actionable offline work under "blocked", and stopped. The archive calls this a coordination failure, not an external block. a27 L223-262. This is why `AGENTS.md` now has a "Delivery ownership" section. [D]
- **X3 Handoff drift.** README, contract header, install.md, and handoff gave contradictory state. holi R9. [D]
- **X4 Over-gated diagnostics.** The "Local B" actual-run admission ended `CONSUMED / STOP_UNKNOWN / no retry`; the recorder dropped 66 events; the BC primitive-seal qualification advanced by 131,072 bytes. a27 L125-153. [D] This is review theatre around a diagnostic.
- **X5 Hosted CI infrastructure.** 5 macOS run attempts cancelled during a GitHub incident. handoff L1946. [D]
- **X6 Test margins.** macOS syncer took 406-600 s against a 600 s timeout, including a legacy suite that exists only to be retired. [D handoff L1400-1420, a29 L84]
- **X7 Validation semaphore.** A shared host semaphore was retired Nov 5. Private Docker harness is the only Go/Python runner. [D handoff L45-60]

Count: 9 durability, 8 concurrency, 13 liveness, 12 quadratic, 7 privacy, 5 identity, 8 consumer, 10 test oracle, 7 process. 79 items. [M count of IDs above]

## 3. Design decisions that were reversed

| Reversal | From, to | Why | Cite |
|---|---|---|---|
| Sweep cadence | Periodic timer (v1) to hook-only sweeps (PR #9) to coordinator with periodic bounded discovery, hooks as hints | A hook-only model cannot retry. Contract: "Work retries without requiring another agent event". | PR #9, contract section 3 [D] |
| Upload form | Full re-upload to delta append + fold (PR #2) to chunking (Oct 15) to full epoch snapshot (contract) to coalescing and debounce (GAP-03) | Each form fixed the previous one's cost and exposed another. The baseline is again the first one with a cadence bolted on. | PR #2, #18, contract s4, gap GAP-03 [D] |
| Progress model | Machine-wide clean/dirty checkpoint and recovery to per-source progress and per-session failure units | "A single run-wide clean/dirty flag cannot accurately represent these facts." | research/session-capture-contract-review.md s1 [D] |
| History promise | Retain every committed generation to latest complete epoch | Complexity of v3 was "partly a consequence of that promise". | holi s1 [D] |
| Dependency-resume ACK bitmap | Built, then cut | Idempotent content-addressed re-upload suffices. | a29 L88-100 [D] |
| Python qualification scaffolding and refresh layering | Built, then cut | One reader path. | a29 L88-100, `c92bc61` [D/M] |
| Optional-lane fold into one work model | Proposed, then rejected as load-bearing | Withheld artifacts and independent repair preserve raw progress. | a29 L101-110, holi s5 [D] |
| Optional-repair and enrichment history tables | Retired | `77276c4`. | [M] |
| Per-upload full-session privacy recheck | Narrowed to the uploaded object's own evidence | O(DxK) cost. Privacy window at batch boundaries retained. | a29 L585-592 [D] |
| Export cap | Strict per-epoch (A7 test) to cumulative cap plus typed error | Parent decision to match measured max 615 MB. Per-epoch deferred. | handoff L520-535 [D] |
| Selector SQL | New indexes tested, all rejected | Workload-specific trade-offs, no regression-free option. | arch Implementation continuation [D] |
| Separate destination | Fresh bucket preflight, superseded by existing-bucket binding reset | Provisioning blocker, then operator direction. | a30 L1200-1260 [D] |
| Cold-recovery concurrency (PR #23) | Closed | Did not address root cause and did not reproduce speedup. | PR #23 [D] |

### Over-built

- **v3 archive machinery**: plans, commits, tombstones, cold recovery, fold lineage. The ceiling was a promise of "every committed file generation" that no consumer needed. [D holi s1]
- **Chunking** ahead of measurement: SQLite chunk harness, live R2 acceptance, descriptor accounting. Never made a storage decision. [D chunk-experiment.md]
- **Migration thinking**: reading old `records/manifest/`, schema 4/5 dual readers (PR #17-18). The contract now bans migration as a prerequisite. [D]
- **Private acceptance harnesses** with admission files, nonces, CONSUMED markers (a27). [D]
- **Qualification scaffolding in Python**, ACK bitmap, generic scheduling frameworks (the reviews explicitly say not to build them).
- **Test mass.** 62k test lines versus 36k non-test [brief, not re-measured]. Three layers of legacy tests are retained only until cutover (syncer 11,056 test lines [D holi]).
- **Docs** (section 4).

### Under-built

- **Failure taxonomy.** One `Transient` class caught `ErrConflict`, source-limit, timeouts. [D gap GAP-01, GAP-05, GAP-06]
- **Retention and reclamation.** "Begin without automatic GC" was right for safety but there was no bounded design. Spool, epochs, enrichment rows, manifests all hit lifetime caps. [D holi s4, handoff L1802]
- **Workload data.** The first workload measurement ("D") was run on Nov 2, after the design was built. Upload throughput was never measured. [D handoff L494-515]
- **Observability of the consumer.** Refresher `run()` discards stderr (handoff L26-40), `Status()` O(history), and "complete" versus "never enumerated" were indistinguishable (`discovery-not-enumerated`). [D gap static items]
- **Budgets.** CPU, freshness, offline horizon were "step-4 agreements" after the build. [D handoff L63-70]
- **Property and model tests on the candidate path** and any fuzz targets in the candidate packages. [D gap section 3]

## 4. Process lessons

Candid assessment. Labelled [I] unless cited.

1. **Documentation volume.** 19,713 lines in `docs/` [M]. About 46k doc lines were added over the history, versus about 110k Go lines added [M git numstat]. The handoff and its archives (2,227 + 10,206 lines [M]) were the working memory of agent sessions. They contain real evidence, but a new session must read a ledger to learn that, for example, a "current" state was superseded two entries up. The holistic review already named this the P1 operational-clarity risk. [D holi R9]
2. **Handoff ledgers as control surface.** A "parent accepts worker" chain with snapshot names (`sl-1`, `cm-ab2`, `p-int-canon6`) and hashes is traceable but unreadable. The valuable part is the RED/GREEN job and mutant result. The decorative part is the prose around it. [I]
3. **Executable proof beat review prose.** Every important defect was found by something that ran: simulation (PR #13), process-exit matrix, barrier tests, RED overlays, live CPU profile, `EXPLAIN QUERY PLAN`. Reviewer-only findings (adversarial R1-R10) were upgraded only after a reproduction. The adversarial review itself disclaims: "A failing intended regression is evidence of a defect." [D adv Verification]
4. **Review theatre.** Hash-pinned "admission" and "sole verifier, AC, no sleep" gates, reviewers asked to confirm a prior reviewer, and a diagnostic that ended `STOP_UNKNOWN` with a retained prefix are cost without correctness. They produced very little that later code used. [I from a27 L125-153]
5. **Agent-driven delivery works when the parent tests the worker.** Parent caught: FailureQuota conflated permanent limits and spool-full; enrichment and optional repair still quarantined on outage; claim query missed the new state in a partial index; legacy-hash false conflict. [D handoff L520-560] None of these were found by the worker's own tests. The pattern is that the parent re-derives the invariant, not just reruns the gate. [I]
6. **Delivery-stop failure.** Agents stopped at self-selected milestones. The cure was prose in `AGENTS.md`. [D a27 L223-262; AGENTS.md "Delivery ownership"] A better cure is a task definition with an exit predicate.
7. **Wall-clock gates.** `make crap` has a 600 s package timeout. Test additions broke it three times (stack layers, macOS syncer, sessioncapture 503 s versus 500). The fix pattern, per-Queue knobs and no skipped captures, is good. The cost was days. [D handoff L1370-1420, a29 L774]
8. **Live is the real test.** Five Mac activation attempts, each contained, each found something (crash loop, livelock, spin, discovery CPU twice). Containment commands were rehearsed and worked. That part was well engineered. [D handoff L1910-2060]
9. **Time went where.** I estimate from commit counts and ledger length that the largest cost buckets were: v3 patches before the rewrite (Oct 13-23), the Oct 24-29 candidate stabilisation (W-series, 116 commits in two days [M]), the gap-register stack, and CI margins. Only the last two produced behaviors the live runs needed. [G]

## 5. Open decisions and risks the rewrite must settle up front

These are listed as open by the handoff and need an operator decision or measured design. [D handoff L63-100]

1. **Epoch retention for in-place-rewritten artifacts.** Rule today: every unproven rewrite starts a new full epoch and the latest manifest keeps all sealed epochs (contract section 1). Evidence: 206 epochs of `~/.claude.json` in 8 days, 587 MiB per Codex run in the shared SQLite store, manifests 578 MiB versus 8 MiB latest-only, two targets `export_too_large`. Needs: a per-artifact-class retention policy (config-like, SQLite store, transcript) in the manifest schema, and a statement about whether old epochs are evidence. Do not defer to GC later.
2. **Shared multi-session stores (Codex SQLite, `history.jsonl`).** They contain rows for many sessions. Capturing whole means one session's opt-out must exclude the whole store (today's behaviour, 7,909 denials). Capturing nothing loses Codex thread data. A row-level or per-session export is the third option. Needs an operator choice.
3. **Enrichment retention.** `MaxEnrichmentWork` 4,096 is a lifetime cap. Retiring attached rows deletes local spool evidence and changes the enrichment contract. Budget live: 29.9 MB of 128 MiB so only the row count binds. Needs a measured design plus approval. [D handoff L1946-1975]
4. **Refresher cost.** About 0.47 cores at `--interval 60`, sweeps of about 57k targets, a 300 s per-process timeout, per-head 5 s timeouts, and the current regression (G7). The rewrite should decide push versus pull freshness. A poll of every head scales with total sessions rather than changed sessions. [I]
5. **Discovery cadence and change detection.** BC-01 discovery generation takes about 5 minutes, which is the wait for a hookless session. Transformed sources re-prepare on each sweep (GAP-11). A cheap fingerprint that detects WAL/SHM change and policy freshness is unsolved. [D handoff L63-70, gap GAP-11]
6. **Export and reader caps.** 768 MiB refresher flag versus the 1 GiB Go default, cumulative versus per-epoch. [D gap GAP-07, handoff L520-535]
7. **Numeric budgets.** Coordinator CPU, refresher CPU, freshness, spool horizon and largest record are all unagreed. [D handoff L56-62]
8. **Hook trust.** Codex hook trust is an operator action, hooks cannot be self-approved, so a stale trust looks like a quiet capture gap. [D handoff L24-30, L80-90]
9. **Cutover and deletion.** Step 5 (defaults, legacy retirement, key deletion) is excluded. Legacy v3 code, 4,825 non-test and 11,056 test lines, remains and blocks CI margins. [D holi s2, handoff L26-40]

## 6. Top 15 lessons for the Rust rewrite, ranked

Each is phrased as a rule enforced by type, lint, test or architecture. Rank is by cost of the Go incidents it would have prevented, weighted by live impact. [I]

1. **Failures are a closed enum whose retry arm cannot quarantine.** `enum Failure { Transient, Throttled, Timeout, Destination, Deterministic(Reason), Limit(Kind) }`. Only `Deterministic` and `Limit` may call `quarantine()` (the function is not defined for other variants). Backoff is a pure function with a table test on exact values. Spool-full and source-limit are separate `Limit` kinds. Go evidence: GAP-01 `runtime_delivery.go:144`, `FailureQuota` conflation, optional work reproduced the same defect (`e4949da`).
2. **Cancellation is not a failure and cannot spend budget.** `Outcome::Cancelled` has no `attempts` field. Attempt budget is spent by consuming a token only in the failure arm. Go evidence: adv R4, `ae8e20d`, `d374948`.
3. **Every collection has a typed capacity and a typed retire path.** A `Bounded<T, Retire>` type refuses construction without a retire policy (or an explicit `NeverRetire` with an operator-visible alarm). No `count(*)` over retained tables on a hot path (lint banning raw `SELECT COUNT` in the scheduler module). Go evidence: GAP-04 (89% of 32,768), enrichment lifetime cap of 4,096, 1024-source cap, epoch growth.
4. **Hot-loop cost is an asserted counter, not a wall-clock hope.** Instrument SQL statements, `stat` calls, policy resolutions and byte reads; each loop has a test at N = 1k, 10k, 30k asserting O(delta) or O(runnable) counts. An idle-tick test asserts zero allocations and zero filesystem calls beyond a stated bound. Go evidence: Q4-Q8, W10 `TestDeliveryCostIsProportionalToDelta`, enrichment spin, `ReadOwnership` per artifact.
5. **Identity is a newtype derived only from validated structural coordinates.** `SourceId::from(Authorized<Coordinates>)`, canonical-path newtype (resolves `/var` to `/private/var`), no existence-of-sibling heuristics. Property test: creating or removing unrelated siblings cannot change an id. Go evidence: adv R5 Pi sibling directory, I2, W40 per-source enrichment copies.
6. **Privacy is a capability token.** Reading or uploading source bytes requires `Authorized<Path>`, produced only by one `policy::check(snapshot)`. The snapshot is built once per discovery pass, consumed within it, and never cached across passes. Pre-upload and pre-CAS take the token for that object only. Unknown policy state returns `Err`, and there is no default-allow constructor. Go evidence: adv R8, W25 decision, DT per-call policy snapshot, `exclusion.Registry`.
7. **One delivery state machine, lane carried in the claim.** `Claim<Raw>` and `Claim<Optional>` are distinct types, a transition function is exhaustive over `(State, Event)`, and the `jobs.state` column has a SQL `CHECK` plus `user_version`. A loom or interleaving property test asserts eventual progress with repair running. Go evidence: C1, C4 livelock, C6 two definitions of attempt, gap static items on `user_version`.
8. **One schema for the wire contract, generated for every consumer.** Bounds, field sets and digest definitions live in one schema crate. Python (or any consumer) imports generated validators, or never re-validates. Unknown fields policy is in the schema. CI runs a writer-generated corpus through every consumer. Go evidence: GAP-08, G3 pairing, G5 envelope, G8 digest.
9. **Health is derived from facts in one function, and facts are monotonic.** No "clear then set". Green requires positive evidence of the current target (a completed enumeration is not evidence about a missing root). Failure rows use UPSERT in one transaction. The dashboard and CLI call the same `health()`. Go evidence: adv R2, R3, holi R2, R3, G7.
10. **Crash points are in the product code, and a harness kills the real process.** A `fail_point!("payload-before-sync")` set at all 14 boundaries, driven by a harness that `exit(137)`s a child and reopens. A model-based test must assert coverage of each fault (for example, at least 5% of histories hit each fault point) or fail. Go evidence: C1 28/28 green only after being added, T6 "9 of 200 hit job-committed", D9 masking.
11. **The coordinator must start and be stoppable without a destination or readiness.** `Destination` is `Option`. Teardown, status and stop commands take `MinimalConfig`, never `ReadyConfig`. A test starts the real binary with the credential file missing and the policy dir deleted. Go evidence: adv R1, R9.
12. **Retention, coalescing and cadence belong in the v1 schema.** The manifest declares per-artifact-class epoch policy and sealed-epoch compaction. The capture state machine includes `supersede_subsumed` and a quiescence debounce. A checked-in aggregate workload corpus (sizes, captures-per-session) drives the default values, not opinion. Go evidence: GAP-03, epoch growth, handoff L1802, D workload (max sources 379, max decoded 615 MB).
13. **Evidence is stored with its source, never in parallel arrays.** `struct Selected { source, evidence }` with private fields. Wire encoding splits at the boundary, validated on decode. Go evidence: holi R5 `es[j]` applied to `m.Sources[j]`.
14. **Tests have exact oracles and no skip.** A lint denies `#[ignore]` outside a named `live` feature. Each state-machine module runs `cargo-mutants` in CI with a fixed timeout multiplier. Expected values are literal tables (backoff, epoch bodies, argv for service commands), not recomputed with production code. Heavy tests take size knobs on the type under test rather than seeding 1030 real captures. Go evidence: W1, W4, W5, W7, T2 supervisor, 1030-capture test, fuzz stall.
15. **There is one status document, and everything else links.** CI fails a doc if it contains a "current state" claim without a commit hash, and a generated status file (from `capture status`) is the only live-state source. A handoff is capped by a line-count lint. Go evidence: holi R9, 19,713 doc lines, 2,227-line handoff, X3.

Considered and left out: generic job framework, ACK bitmap, chunking, migration code. The Go reviews each rejected these. [D holi s5, adv Complexity disposition]

## 7. Open questions and unverified items

### Open questions for the operator

1. Is the legal retention for in-place-rewritten artifacts "latest only", "latest plus N", or "every distinct epoch forever"? Config-like files and SQLite stores likely want different answers.
2. Should the rewrite capture shared multi-session stores whole, per-row export, or not at all? What is the privacy outcome when one session opts out?
3. Is a freshness target (hook to report) defined in minutes, and for which agent? BC-01 hookless sessions currently wait about 5 minutes.
4. Does anything consume captured data that needs intermediate prefixes? The contract says no. Confirm for the rewrite.
5. Does the rewrite keep SQLite as the authoritative pending store, or move to a log-structured spool with rebuildable index? The contract allows either if proven.
6. Is the Python/DuckDB consumer to remain a separate runtime, or should the reader emit the analytics event index directly? This removes the Go-Python drift class (G1-G5).
7. Should enrichment remain a separate lane in the rewrite, given it consumed most of the late complexity (L8, L9, C4)?
8. Are two-host acceptance budgets to be set before or after the Rust writer exists? The Go project set them after.

### Things I could not verify

- I did not read the 6,620-line a27 archive in full. I sampled its headings and the delivery-stop and last-operation sections. The gap register itself says the same limitation applies to its catalog. [M/D]
- I did not read the code. File and line citations (`runtime_delivery.go:144`, `capturebackend/backend.go:133`, and others) are quoted from the review documents. Line numbers may have moved since main `57d8400`. [D]
- I did not read PR review comments in full. I saw only comment counts (PR #15: 1, PR #17: 5, PR #30: 2). [M]
- Handoff line numbers in the form `L1910-2060` come from my sed offsets. They are approximate to within a few lines. [M]
- I did not verify that the final fix for each defect is on main. I relied on commit subjects and PR descriptions. [D/M]
- Commit counts per day exclude work in unmerged worktrees (`memory-wt/`). The size of that work is unknown. [I]
- The claim that Oct 20-27 saw the candidate built in a dirty worktree is inferred from the 77-dirty-path statement and the `b68a412` shortstat. [I]
- The Rust-rule list is my own synthesis. None of the documents ask for these specific mechanisms. [I]
- I did not run `go test` for any package.
