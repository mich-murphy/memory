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
| `BenchmarkFold` | The sweep that folds an idle session's segments into one object, once per session. |
| `BenchmarkBackfill` | The first sync on a machine. |
| `BenchmarkR2Put` | The R2 client alone (hashing, signing, sending) against a local server that discards the body. |

The syncer benchmarks use an in-memory bucket, so they time the collector, not the network. The `rtt=30ms` variants add a round trip to every request, which shows what R2's latency costs and how well uploads overlap. Besides time and memory they report `requests/op` and `sent-B/op`, the requests and bytes a run sends: both are what R2 bills and what a slow network waits on.

## Running and comparing

```sh
make bench                                  # every benchmark, 10 runs each, into bench.txt
make bench BENCH=HookTurn BENCHCOUNT=20     # one benchmark
go tool -modfile=tools/go.mod benchstat old.txt new.txt
```

Compare a change by running `make bench BENCHOUT=old.txt` before it and `make bench BENCHOUT=new.txt` after, on the same machine with nothing else running. benchstat marks a difference as significant (p < 0.05); `~` means the runs can't tell the two apart. Ten runs is the least that gives benchstat a useful confidence interval. CI runs each benchmark once (`make bench-smoke`) so a broken one fails the build; CI machines are too noisy to compare timings on.

## The golden test

`TestBucketMatchesGolden` (`internal/syncer/golden_test.go`) runs a small home through a backfill, idle and busy sweeps, a hook sync, a launch record, a rewrite and a fold, and compares every request and the final bucket with `internal/syncer/testdata/golden.txt`: keys, metadata, sizes, the SHA-256 of each object and of the bytes inside each zstd object, and the full text of manifests and snapshot sets. A change that alters what reaches the bucket fails it. When the change is meant to, regenerate the file with `go test ./internal/syncer -run Golden -update` and explain the diff in the pull request; a change to keys, fields or formats also needs `docs/storage-layout.md` updated.
