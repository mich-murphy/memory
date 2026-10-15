# Benchmarks

The benchmarks run the collector the way it runs on a machine, against a generated home that looks like a real one, so a change is measured on the work it will actually do. A golden test pins what reaches the bucket, so a faster collector can't quietly capture less.

## What they model

`internal/testenv` writes a deterministic home: Claude Code, Codex and Pi sessions with log-normal sizes, subagents, tool results, file history, todos, config and an agent's SQLite database. Lines follow each agent's real format (the shapes in `docs/storage-layout.md` and the project's raw samples): prose, code that recurs across tool calls, and opaque base64 where agents put signatures and encrypted reasoning, so compression sees realistic input. Profiles:

| Profile | Sessions per agent | Median main transcript | Largest |
|---|---|---|---|
| `small` | 3 | 24 KiB | 160 KiB |
| `typical` | 40 | 256 KiB | 12 MiB |
| `many` | 400 | 24 KiB | 1 MiB |
| `large` | 1 | | 72 MiB, above the 64 MiB streaming threshold |
| `huge` | 1 | | 256 MiB |

| Benchmark | The moment it stands for |
|---|---|
| `BenchmarkHookTurn` | A hook's sync after one agent turn (24 KiB appended to the largest session). The most frequent run. |
| `BenchmarkHookTurnAfterSegments` | The same sync on a 12 MiB session that has gathered 1, 16 or 62 segments since its last fold (it folds at 64). |
| `BenchmarkSweepIdle` | A sweep at session start or end when nothing has changed. |
| `BenchmarkSweepAfterTurns` | A sweep after three sessions, one per agent, each took a turn. |
| `BenchmarkFold` | An unchanged 64-piece append chain: retain the first base and consolidate pieces 2..64. |
| `BenchmarkBackfill` | The first sync on a machine. |
| `BenchmarkR2Put` | The R2 client alone (hashing, signing, sending) against a local server that discards the body. |

The syncer benchmarks use an in-memory bucket, so they time the collector, not the network. A copied warmed SQLite cache is paired with its matching immutable bucket snapshot, including store identity and checkpoint; segmented fixtures preserve both at each turn count. Every sync error fails the benchmark, and busy sweeps, hook turns and folds assert that the intended work occurred rather than reporting a fast failed or unchanged run. The `rtt=30ms` variants add a round trip to every request, which shows what R2's latency costs and how well uploads overlap. Besides time and memory they report `requests/op` and `sent-B/op`, the requests and bytes a run sends: both are what R2 bills and what a slow network waits on.

## Running and comparing

```sh
make bench                                  # every benchmark, 10 runs each, into bench.txt
make bench BENCH=HookTurn BENCHCOUNT=20     # one benchmark
go tool -modfile=tools/go.mod benchstat old.txt new.txt
```

Compare a change by running `make bench BENCHOUT=old.txt` before it and `make bench BENCHOUT=new.txt` after, on the same machine with nothing else running. benchstat marks a difference as significant (p < 0.05); `~` means the runs can't tell the two apart. Ten runs is the least that gives benchstat a useful confidence interval. CI runs each benchmark once (`make bench-smoke`) so a broken one fails the build; CI machines are too noisy to compare timings on.

## v3 archive resource invariants

The append-only protocol deliberately adds plan/commit publication and durable metadata history. Backfill and changed captures do more metadata work than v2; retained history and database generations are not discarded to improve timings. Cache-loss recovery reads paginated historical metadata and validates dependency identities and lengths, without downloading transcript bodies.

Acceptance tests supplement timing samples with deterministic bounds:

- `TestWarmIdleRequestsDoNotGrowWithFilesOrHistory`: at most eight warm-idle requests with either 1 or 30 files and 20 prior changed generations; no immutable-record requests.
- `TestChangedStreamDoesNotHeadItsEntireHistory`: at most one historical descriptor HEAD for a changed stream with 30 generations.
- `TestWarmHookDoesNotDownloadArchivedTranscriptBodies`: zero archived transcript-body reads for a warm unchanged capture.
- `TestLargePlainAndCompressedCaptureHasBoundedHeap`: 256 MiB decoded plain and zstd inputs, actual whole-content SHA and streamed restore checks, with sampled capture heap growth at most 96 MiB. The highly compressible fixture is a streaming regression bound, not a universal bound on all metadata or concurrent machine workloads.

Historical descriptor bodies are replayed one at a time from SQL cursors. Stream lists and per-session launch/snapshot/version metadata still grow with cardinality; see [storage-layout.md](storage-layout.md) for per-record limits and archive growth. Request bounds assume clean matching checkpoints and normal single-writer operation, not an external corruption audit.

## v3 completion comparison (2026-10-14)

Ten samples of each of 24 benchmarks completed on Darwin arm64 / Apple M2. Artifacts: `/tmp/memory-archive-before.txt`, `/tmp/memory-archive-after.txt`, and `/tmp/memory-archive-benchstat.txt`. The final after run passed, with 240 result rows; it includes the final privacy and canonical-codec fixes.

**This is not a general speedup claim or a strict apples-to-apples warmed-cache comparison.** The older fixture copied SQLite without the corresponding remote archive and did not consistently check sync errors. The v3 fixture now pairs both snapshots and asserts real work. Old warmed/segmented results cannot establish equivalent successful work or equivalent dependency validation; failed/no-work v3 results from that obsolete fixture were discarded. The original before run also had concurrent-worker noise. Backfill retains the same generated source workload, but now publishes a materially stronger, larger archive, so its results measure a durability/resource tradeoff rather than identical output.

Selected final medians (allocation figures elsewhere in benchstat are cumulative, not peak heap):

| Workload | Final time/op | Requests/op | Interpretation |
|---|---:|---:|---|
| Backfill, typical | 1.404 s | 4711 | Same source workload; 22.7% slower than before, with immutable plans/commits and retained history. |
| Backfill, many | 35.07 s | 41056 | Same source workload; 134.3% slower. Metadata/publication overhead is significant. |
| Idle sweep, typical | 141.6 ms | 6.5 | Full discovery/privacy/export work; not just the direct-capture request-bound test above. |
| Hook turn, typical, zero RTT | 18.81 ms | 35 | Verified one new appended segment. |
| Hook turn, typical, 30 ms RTT | 1.170 s | 35 | Durable publication and dependency checks expose network latency costs. |
| Hook after 62 segments | 72.95 ms | 156 | Verification is bounded by piece count, but many checks remain. |
| Fold | 65.80 ms | 41 | Verified consolidation; earlier immutable pieces remain. |

Apparent large/huge backfill improvements are not claimed because the before samples were noisy. The unchanged R2 PUT primitive remains a more comparable control: medians changed from 152.0 to 147.1 µs (1 KiB), 196.8 to 186.0 µs (64 KiB), 4.146 to 4.834 ms (4 MiB), and 214.7 to 221.6 ms (80 MiB multipart). These mixed shifts, with essentially unchanged allocation counts, are not credited to archive optimisation. Deterministic request/integrity tests above establish the bounded warm-path properties independently of timing noise.

## Production-fix comparison (2026-10-15)

Ten-sample `make bench` runs completed on the same Apple M2 / Darwin arm64 / Go 1.27.2:
`3a59dd8` before, `a95976a` Go sources after. The after run had no competing builds,
tests, native-agent runs, live R2 or private sizing experiments. Raw results and benchstat:
[before](benchmarks/production-2026-10-15/before.txt),
[after](benchmarks/production-2026-10-15/after.txt),
[comparison](benchmarks/production-2026-10-15/compare.txt).

Selected medians (in-memory backend, zero added RTT unless stated):

| Operation | Before | After | Requests before → after |
| --- | ---: | ---: | ---: |
| Small backfill | 220.3 ms | 243.6 ms | 397 → 318 |
| Many-session backfill | 44.94 s | 36.16 s | 41,056 → 28,566 |
| Large backfill | 3.447 s | 1.169 s | 215 → 195 |
| Huge backfill | 9.407 s | 4.107 s | 215 → 195 |
| Small idle sweep | 29.75 ms | 16.66 ms | approximately 6 → 6 |
| Small session turn | 22.80 ms | 9.53 ms | 32 → 23 |
| Turn after 16 pieces | 52.88 ms | 29.32 ms | 64 → 39 |
| Turn after 62 pieces | 159.86 ms | 75.96 ms | 156 → 85 |

This is **not an across-the-board CPU/allocation improvement**. Small cold backfill
regresses 10.6%. Cumulative allocations rise: small backfill 15.45 → 43.21 MiB,
many-session backfill 1.529 → 2.769 GiB, huge backfill 333.9 → 411.6 MiB. These are
allocated bytes over an operation, not peak resident heap. Bounded metadata parsing,
local staged-body verification and confirmation bookkeeping add work. Baseline timing
variance is substantial (for example many backfill ±51%, huge backfill ±49%); prefer
request-count evidence and the full confidence intervals over universal speed claims.

Two exclusions are essential:

- `Fold` changed from obsolete idle folding to a real 64-piece retained-base fold;
  its before/after timings, bytes and allocations are **not equivalent workloads**.
- The `typical` profile uses real temporary project paths. Their length changes line
  generation thresholds and the shared RNG's subsequent consumption: observed generated
  sizes were approximately 96 MB before and 118 MB after. All `typical` rows are retained
  as observations, not used here for causal speedup claims. Decoupling fixture generation
  from temporary path spelling is future benchmark work; the other selected profiles use
  fixed logical cwd strings for their principal transcripts.

Do not interpret benchstat's mixed/unpaired geomean as an overall speedup. The new
snapshot-fanout diagnostic reports 190 requests and 114.1 ms at 1 ms injected RTT
(6.955 ms at zero RTT); it has no row in the original full-suite baseline. The separate
isolated fanout investigation measured 2,725 requests before caching. Fake RTT is not R2.
Actual bounded R2 chunk-publication evidence is in [live-chunks.md](live-chunks.md).

## The golden test

`TestBucketMatchesGolden` (`internal/syncer/golden_test.go`) runs a small home through a backfill, idle and busy sweeps, a hook sync, a launch record, a rewrite and a later idle sweep that must not time-fold, and compares every request and the final bucket with `internal/syncer/testdata/golden.txt`: keys, metadata, sizes, the SHA-256 of each object and of the bytes inside each zstd object, and the full text of manifests and snapshot sets. A change that alters what reaches the bucket fails it. When the change is meant to, regenerate the file with `go test ./internal/syncer -run Golden -update` and explain the diff in the pull request; a change to keys, fields or formats also needs `docs/storage-layout.md` updated.

Actual 64-piece folding is covered by `TestAppendBoundaryFoldRetainsBaseAndEveryHistoricalVersion`,
`TestContinuousGrowthFoldsAcknowledgedParentBeforeAppending` and the independent CLI journey,
rather than the golden workload's obsolete idle-fold step.

## Pieces/delta fixture change

`BenchmarkFold` now starts with 64 real pieces (base plus 63 appends), not an idle
two-piece file. Its timed operation retains the base and consolidates the tail. Old
idle-fold timings are not an equivalent-work baseline. The segmented fixture also saves
63 turns for this benchmark; the existing hook-turn cases remain 1, 16 and 62 turns.
No performance comparison was run in the implementation worktree; the parent owns the
serial before/after comparison. Unchanged chains below 64 pieces never fold on age alone.
