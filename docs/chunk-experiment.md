# Offline SQLite chunk-sizing experiment

This is a test-only, opt-in experiment on macOS/Linux, not a production CLI or a
production performance claim. It uses no collector configuration, state database,
credentials, hooks, network store or discovery. It reads **only explicitly supplied
snapshot files**, their stat information and the presence of their SQLite sidecars.
It never writes the snapshots or retains compressed bodies. No private database or
results are included in the repository.

## Prepare the inputs

Supply two standalone, **quiescent SQLite backup copies** of the same database:
reference first, then update. Produce consistent copies with SQLite's backup API or
`.backup` while authorised to access the source; do not copy a live database file
with `cp` (committed pages may still be in its WAL). Use private directories (0700)
and files (0600). Finish the backups, close all handles and stop all writers before
running. Keep the copies unchanged for the entire command. Never use production
paths or the collector's own state/configuration as experiment inputs.

The harness requires explicit absolute regular-file paths, a SQLite magic header,
a valid power-of-two page size (including SQLite's 65536-byte encoding), aligned
file length and a current in-header page count matching the file. It rejects
symlink files and any adjacent `-wal`, `-shm` or `-journal`, even empty ones. It
checks size, identity and modification time before and after each measurement.
These checks are **not** an integrity check, a backup mechanism or proof against a
concurrent/malicious writer. Validate consistency yourself when making the copies.

## Run

From the module root, with the repository's pinned Go toolchain:

```sh
go test ./internal/syncer -run '^$' \
  -bench '^BenchmarkDatabaseChunkSizing$' -benchtime=1x -count=5 \
  -args -database-chunk-reference=/consistent/copy/reference.sqlite \
        -database-chunk-update=/consistent/copy/update.sqlite
```

Both flags are required. With either absent the benchmark skips, so ordinary tests
and benchmark smoke do not need any data. Do not run this benchmark in parallel
with other tests/benchmarks when interpreting process CPU. The paths above are
placeholders, not discovered defaults. The harness does not log paths, content,
digests or underlying errors; failures use fixed numeric codes: 1=input/changed
snapshot, 2=CPU accounting, 3=measurement/descriptor, 4=read-handle close. Shells,
wrappers and CI may themselves echo command arguments; keep those logs private.

Scenario indices:

| Index | Pieces |
| --- | --- |
| 0 | One whole-file piece, streaming |
| 1 | Fixed 1 MiB pieces, final short piece allowed |
| 2 | Fixed 4 MiB pieces, final short piece allowed |

All modes call `internal/raw.CompressStream`, the actual collector streaming
compression settings, with a fresh encoder for every piece. Even duplicate and
previously present pieces are compressed independently; there is no compression
result cache, no precompressed reference shared between scenarios or repetitions.
Every iteration measures reference and update afresh, with fresh dedup maps. Only
piece descriptors and encoded digest/length maps grow with chunk count; encoder
memory is bounded independently of database size, including whole mode. Compressed
output goes straight into a counting digest sink, not RAM buffers or disk files.

## Read the numbers

For each scenario the benchmark reports only numeric metrics:

- `cold-raw-B`, `update-raw-B`: uncompressed snapshot sizes.
- `cold-blob-unique-B`, `cold-unique-blobs`: bytes/count of distinct encoded blobs
  for the reference with an initially empty bucket model.
- `update-newblob-B`, `update-new-blobs`: additional bytes/count for the update,
  deduplicated both within the update and against **any position** in the reference.
  Repeated pieces still count separately in the piece graph. This models only the
  supplied two-generation history, not arbitrary pre-existing bucket contents.
- `cold-pieces`, `update-pieces`: ordered piece counts, including repeats.
- `cold-synthetic-plan-commit-B`, `update-synthetic-plan-commit-B`: **standard
  synthetic-identity plan+commit bytes**. These are twice the actual canonical
  FILE publication body length from `canonicalPublication`, validated against the
  measured piece graph, with FILE dependencies omitted as in production. Update
  uses sequence 2 and the reference descriptor's parent identity. Machine, agent,
  root, key, source and observation are fixed synthetic values. These are **not
  actual full archive metadata**: manifests, snapshots, historical version lists,
  checkpoints, object keys, HTTP headers and R2 request overhead are not included.
- `cold-cpu-ns/op`, `update-cpu-ns/op`: process user+system CPU for the individual
  scan/hash/compress/dedup/canonical-descriptor measurement. Input validation is
  outside these intervals. CPU includes background Go runtime work in the process,
  and can be noisy. Numbers are averaged across the benchmark's iterations.

Go's usual `ns/op` is wall time for the **pair**, including local input checks;
it is not the CPU metric. Neither value measures R2 end-to-end time: there are no
PUTs, HEADs, latency, retries, staging-file I/O or backup/export costs. File-system
cache, runtime GC, encoder startup and scenario order can affect timings. Whole
mode intentionally always streams, unlike the collector's in-memory path for
small non-chunked inputs; fixed modes deliberately ignore the production 8 MiB
chunking threshold to isolate boundaries. This is not CDC and does not justify a
production chunk-size change without separately controlled benchmarks/benchstat
and end-to-end validation. No timing results or claims are recorded here.

## Deterministic checks

```sh
go test ./internal/syncer -run '^TestChunk(Measurement|Descriptor|Header|Snapshot)' -count=1
go test ./internal/syncer -run '^$' -bench '^BenchmarkDatabaseChunkSizing$' -benchtime=1x
```

The tests use only small synthetic bytes. They check repeated/relocated/new/absent
blobs, identical updates, whole versus final-short-piece coverage, short reads,
canonical descriptor byte accounting and omitted-but-derived repeated FILE
dependencies, SQLite header/page validation and sidecar/mutation rejection. The
second command must skip without both flags. Tests never access a host database.
