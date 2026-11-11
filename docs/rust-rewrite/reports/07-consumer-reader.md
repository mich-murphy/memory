# Consumer and reader review for the Rust rewrite

Reviewer role: consumer and reader. Read-only. Repo at main = 57d8400, working tree has uncommitted edits to `docs/session-capture-handoff.md` (+880 lines), `docs/README.md`, `docs/production-gap-register-20261102.md`. I read the working-tree handoff.
Labels used: **measured** (I read the code or ran a count), **doc says** (taken from a doc, not verified), **inferred**, **guess**. Paths are relative to `/Users/mm/dev/memory`.

## Findings first

1. **The reader boundary is mostly honoured for storage, and broken for the wire.** Python never lists R2, never selects `current.json`, never decodes zstd (measured, `publications.py:1-7`, only imports `duckdb`, `events`). But Python carries a second, hand-mirrored implementation of the Go export validator (`publications.py:38-193`) and of the heads wire (`publication_refresh.py:344-376`). GAP-08 was "fixed" in 7bd6b65 by re-mirroring the Go bounds, which keeps the duplication and adds parity tests (`test_publication_properties.py:396-475`). The Rust rewrite should delete the mirror, not port it.
2. **There is no restore in the candidate path.** `capture-v1` has `export` only. The journey tests call their export helper `restore` (`test_capture_journey.py:714`). The only other Go caller of the reader is the writer's own rebase (`internal/sessioncapture/rebase.go:150`). Legacy restore is a separate algorithm (`internal/syncer/restore.go:20`). "One reader for restore and analytics" is therefore a design intent, not a demonstrated fact.
3. **The refresher regression is an interface-cost problem, and the root cause is not yet diagnosed.** The handoff says nothing has been changed and names suspects (`session-capture-handoff.md:65-75`). My reading of the code gives five structural contributors, all inferred, listed in section 3.4.
4. **The interface is page-and-spawn, whole-session, lossy-error.** One OS process per 256 heads, one process per export, whole-session export of every retained epoch, stderr discarded, and every failure except exit 4 collapsed to `process_failed`. A Rust rewrite that keeps this shape will keep the regression.
5. **Normalization is deterministic framing plus heuristic classification, bundled in one Python function.** Split them. Framing (offsets, depth, UTF-8, ids) belongs with the reader in Rust. Classification (`is_human`, `origin`, mirrors, duplicates) belongs in SQL views so it can change without re-import.
6. **Golden fixtures exist only as a generator, not as files.** `capture-v1 fixture` builds one synthetic Claude session at test time. No Codex or Pi writer fixture. The seeded property tests (A1-A8) are the best seed for cross-language conformance.

## 1. The Python consumer

### 1.1 Size (measured, `wc -l` and `grep -c "def test_"`)

| Group | Files | LOC |
| --- | --- | --- |
| v2 publication consumer | `publications.py` 576, `publication_refresh.py` 541, `publication_dashboard.py` 270, `projection_access.py` 53, `bench_publication_import.py` 62 | 1,502 |
| Shared by both modes | `events.py` 402, `serve.py` 229, `query.py` 39 | 670 |
| Legacy research pipeline | `r2_metadata.py` 191, `bodies.py` 318, `analyze.py` 359, plus `classify.mjs` 223, `views.sql` 31, `report.sql` 50 | 868 Python (+304 JS/SQL) |
| Frontend | `site/app.js` 401, `index.html` 36, `style.css` 5 | 442 |
| v2 tests | 8 files | 3,696 |
| Shared and legacy tests | `test_events.py` 259; `test_r2_metadata.py`, `test_bodies.py`, `test_dashboard.py` 505 | 764 |

Python in `tools/analytics` is 3,040 non-test lines plus 4,460 test lines. All Python under `tools/` is 8,308 lines. Test functions (`def test_`) in analytics: 152 Python plus 8 in `test_classify.mjs`. Other tools: `capture-delivery` 12, `candidate-live-r2` 6. Doc says the analytics suite is 122 tests, no skips, with the binary (`session-capture-handoff.md:249`). The difference from my 152 is that I count all files including legacy.
Dependencies (`requirements.txt`) are `duckdb==1.5.6` and `boto3==1.43.114`. **boto3 is used only by the legacy modules** (`r2_metadata.py:17`, `bodies.py:26`). The v2 path needs DuckDB only.

### 1.2 Legacy inspection versus v2 mode

- **Legacy, do not port.** `r2_metadata.py`, `bodies.py`, `analyze.py`, `classify.mjs`, `views.sql`, `report.sql`, `test_r2_metadata.py`, `test_bodies.py`, `test_dashboard.py`, and `events.build()` (`events.py:315-383`). The README itself says they target retired schema-3 manifests and "must not be used as a replacement ingestion path" (`tools/analytics/README.md`, "Legacy research pipeline"). They own the existing research evidence (reports, classifications), which the operator wants preserved, so they stay as frozen files, not rewritten.
- **v2, port or replace.** `publications.py`, `publication_refresh.py`, `publication_dashboard.py`, `projection_access.py`, the v2 parts of `serve.py`/`query.py`, and the shared `iter_events`/`normalize_record`/`pair_codex_mirrors` in `events.py`.
- `serve.py` multiplexes both with `--source legacy|publications` (`serve.py:192`). `publication_dashboard.py` imports `publication_refresh` only to reuse `summary()` for health (`publication_dashboard.py:17-18,92`). That coupling forces the dashboard to carry the refresher's SQLite schema.

### 1.3 What Python takes from R2 or from the Go binary

**From R2 directly.** Nothing in v2 (measured). The legacy modules LIST and GET v3 manifests via boto3.
**From the Go binary.** Only three commands, all via `subprocess`, argv list, no shell, credentials never in argv (`publication_refresh.py:97-135`, `publications.py:540-551`).

| Command | Flags Python passes | Output Python consumes | Exit codes Python distinguishes |
| --- | --- | --- | --- |
| `capture-v1 current-heads` | `--config PATH --after-session CURSOR --limit 256` | one JSON object on stdout (<= 2 MiB) | 0 ok; 4 only matters for export; any other nonzero is `process_failed` |
| `capture-v1 publication-status` (queue discovery, default mode) | same flags | same wire, `session-publication-heads/v1`, no `failures` | same |
| `capture-v1 export` | `--config PATH --archive A --machine M --agent G --session S --output DIR --max-bytes N` | nothing on stdout (discarded). The result is `DIR/metadata.json` plus `DIR/bodies/NNNN.bin` | 0 ok; **4 = `export_too_large`** (permanent); other nonzero = `process_failed`; Python 300 s timeout = `process_timeout` |

Optional `ssh_host` wraps the same command in `ssh -oBatchMode=yes` and copies the export back with `scp -r` (`publication_refresh.py:137-160`). The doc says SSH is not the production path (README, "Optional ssh_host").
`publications.export_session()` (`publications.py:540`) is a second wrapper that supports `--store` for local offline tests, with its own hard-coded 300 s timeout and `reader_export_failed`/`reader_export_timeout` codes. Two wrappers, two timeout policies.

### 1.4 JSON shapes

**Heads page** (`session-publication-heads/v1`, Go `internal/publication/heads.go:23-40`):
```
{contract, archive_id, heads:[{identity:{archive_id,machine_id,agent,session_id}, sequence:uint, publication_id}],
 failures?:[{identity, reason:"current-unreadable"}], next_cursor:string, complete:bool}
```
Page size 1..256 counts heads and failures together. The cursor is opaque base64url of `{v:1,archive,machine,agent,after}`, advancing agents in order claude, codex, pi (`heads.go:91-123,174-185`). `after` is the last validated S3 common prefix, not an S3 token. Empty pages can advance agents (`heads.go:125-127`).
**Export envelope** (`session-reader/v2`, `types.go:350-364`):
```
{contract, manifest_sha256, manifest:{contract:"session-publication/v2", identity, publication_id, sequence,
  observed_at_ns, captured_at_ns, published_at_ns, coverage:{discovery,enrichment,missing[]},
  sources:[{source_id, epoch_id, role, path, state, revision, observed_at_ns, captured_at_ns,
            object:{key,encoding:"zstd",sha256,encoded_sha256,size,encoded_size}, optional?, withheld?, associations[]}]},
 bodies:[{source_id, epoch_id, path:"bodies/%04d.bin", sha256, size}]}
```
Nil slices may be `null`. Withheld sources have empty `source_id`, `path`, `associations` and no `object` (`selection.go:178-187`).

### 1.5 Pagination, sweep and timeouts (measured)

- A sweep starts at an empty cursor, follows `next_cursor` until `complete`, capped at 1024 pages (`publication_refresh.py:276,290`). Repeated cursors are an error (`:303-305`).
- One `run()` per page and one per export. `run()` polls every 25 ms, walks the output directory each time to enforce a byte cap and entry cap of 65,536 (`:76-114`).
- Process timeout 300 s default, bounds 1..3600 s (`:508,515`). Interval default 60 s.
- Per-head target flow (`_target`, `:446-508`) is: record target, verify existing projection (open DuckDB, hash every owned body), else export, `_load` validate, import under exclusive flock, update SQLite status.

### 1.6 DuckDB schema (measured, `publications.py:196-239`, `events.py:25-50`)

- `publication_binding(version=2, archive_id)`, `sessions(session_key PK, archive_id, machine_id, agent, session_id, publication_id, sequence, manifest_sha256, observed/captured/published/imported/normalized_at_ns BIGINT, coverage JSON, normalization)`.
- `sources(session_key, trace_id, ..., source_id, epoch_id, source_role, revision, state, original_path, path, sha256, size, ..., associations JSON, partial_tail, source_key_hash, optional, withheld_reason)`.
- `events` (25 columns: `event_id, trace_id, line, offset, length, ordinal, native_id, parent_id, raw_type, event_type, timestamp, role, origin, text, tool_name, tool_call_id, tool_args JSON, is_error, model, is_human, duplicate, duplicate_of, is_mirror, byte_offset, byte_length`) plus 8 coordinate columns. `issues(trace_id, line, offset, length, kind, record_type)` plus coordinates. `failures(session_key PK, sequence, failed_at_ns, reason, attempts)`.
- Separate SQLite status DB, schema v4: `binding, expected_endpoints, endpoints, targets, head_failures` (`publication_refresh.py:164-222`).
- Local content-addressed body store `<db>.bodies/<sha256>`; flock sidecars `.projection.lock` (shared for readers, exclusive for import) and `.refresh.lock` (single refresher).
- Identities computed in Python: `session_key = sha256(json.dumps([archive,machine,agent,session], separators=(',',':')))` (`publications.py:280`), `trace_id = _key([session, source_key_hash, epoch_id])` (`:392`). **`json.dumps` defaults to `ensure_ascii=True`**, so non-ASCII identities hash as `\uXXXX` escapes. Any Rust port that wants to read an existing projection must reproduce that byte-for-byte. The refresher's SQLite `identity` key uses a different serialization, `json.dumps(identity, sort_keys=True)` with default `", "` separators (`:366`). Two private serializations, neither specified in a doc (inferred, from the code).

### 1.7 Normalization (`events.py`)

- `normalize_record(record, agent)` (`:72-185`) returns one row per content block. Claude, Pi and Codex each have their own record-type table (`:93-137`), then a common block loop handling `tool_use/toolCall`, `tool_result`, `thinking`, `encrypted_content` (`:139-178`).
- `origin` map user/assistant/tool/system, then `is_human` = origin human AND kind message AND not matching the `SYNTHETIC` regex (`:41-49,170-175`). Human-but-synthetic becomes `origin='system'`.
- Subagent user messages are forced to `is_human=False, origin='agent'` **in the importer**, not in `events.py` (`publications.py:~396`), and again in legacy `build()` (`events.py:~358`). Same rule in two callers.
- `iter_events` (`:188-239`) frames lines, keeps exact byte offsets, treats bad JSON as `malformed_json`, bad UTF-8 or lone surrogates as `malformed_record`, and records native-ID duplicates keyed `(native_id, ordinal, event_type, suffix)`. For Claude records lacking `uuid`, the key adds a hash of text/args/call id so message-id chunks do not collapse (`:226-232`).
- **Recursion isolation.** commit 0812c1c added `RecursionError` to both except clauses (`:201,214`) so a deeply nested record is quarantined as an issue and framing survives. Python's limit is about 1000 frames. `serde_json` defaults to 128. A record nested 129..~1000 deep is parsed by Python and rejected by a naive Rust port (inferred from library defaults, not run).
- **Codex mirror pairing** (`pair_codex_mirrors`, `:242-271`) is SQL, not Python. It aggregates whole-line text for `response_item.message` and `event_msg.user_message`, ranks occurrences per trace and text, joins nth-to-nth, and flags the `event_msg` side `is_mirror, duplicate, not is_human`, with `duplicate_of` = the response_item event. It never crosses a trace (epoch). It is called once per freshly normalized scope with the replaced trace ids (`publications.py:448`).
- Artifacts (`role='artifact'`) are never parsed. They get one `artifact_not_normalized` issue (`publications.py:~375`). A nonempty last line without `\n` yields `incomplete_tail`.

### 1.8 Dashboard (measured)

`serve.py` is a loopback `ThreadingHTTPServer` (Host and cross-site checks, fixed asset allowlist). `publication_dashboard.PublicationDashboard` serves `/api/report|sessions|trace|event|issues` from DuckDB read-only under a shared flock, with `SET threads=2, memory_limit='256MB'` (`publication_dashboard.py:~60`). It uses `report(summary=True)`: exact SQL totals, at most 50 detail rows. Nanoseconds, sequence, revision and size become decimal strings for JavaScript (`:~35`). Raw access slices byte coordinates from the owned body store with a 1 MiB line cap and 1 GiB verification cap. GET never imports or contacts the reader. Refresh health comes from the SQLite status DB (`:~66-110`); missing status is explicitly "partial", not green.

## 2. Is the one-reader boundary honoured?

**Honoured (measured).** Python does not resolve current, list the bucket, check R2 object hashes, decode zstd, or choose among manifests. `publications.py:1-7` and the README say so, and `test_publications.py:1` labels itself "not a substitute for the actual Go writer journey".

**Not honoured, with citations.**

| Where Python re-implements Go | Evidence | Drift risk |
| --- | --- | --- |
| Export envelope validator, field sets, bounds, `DisallowUnknownFields`, order, withheld rules, coverage rule for withheld | `publications.py:38-193` ("mirrored exactly", `:38`) | Any new Go field breaks Python until mirrored. Already happened once (GAP-08, `production-gap-register-20261102.md:236-252`) |
| Effective source identity `sha256(source_id)` and the selected/withheld split | `publications.py:77-80,~120-170` vs `selection.go:151-159` | Silent divergence changes `trace_id` and orphans projections |
| Heads wire validation (agent allow-list, 256 limit, unique identities) | `publication_refresh.py:344-376` vs `heads.go:71,128-131` | Same |
| Head regression and equal-sequence conflict rules | `publication_refresh.py:446-460`, `_load` checks `:482-486` vs Go `advances()` (`publication.go:395`) | Ordering semantics defined twice |
| `current_missing` absence inference from a complete sweep | `publication_refresh.py:326-336` | Policy lives only in Python. Reasonable, but undocumented in Go's contract |
| Export budget constants | `DEFAULT_MAX_BYTES=1024**3` (`:29`), `EXIT_EXPORT_BUDGET=4` (`:31`), manual comment "Matches Go's DefaultExportMaxBytes" | Hand-synced. Python also caps `--max-bytes` at 1 GiB (`:516`), while Go allows 1 TiB (`export.go:14`). The two over-cap Codex SQLite sessions (3,520 MiB decoded, `session-capture-handoff.md:1779`) cannot be imported by this CLI even if the operator wants it |
| Manifest digest | Go exports the digest of the **stored bytes**, but `metadata.json` carries a re-marshaled `Manifest` (`export.go:63`). Python cannot recompute or verify the digest and stores it as an unverifiable string | Provenance claim is trust, not proof |

**Doc status of GAP-08 (measured).** The gap register describes 16 MiB, `sequence=0` accepted, unknown fields accepted, and a local fingerprint. The current code has fixed all four (7bd6b65, cb54fd7). The register entry is stale relative to the code. The fix pattern (mirror and test) is the thing to avoid repeating.

## 3. The reader and export contract as implemented

### 3.1 Language-neutral interface (what Rust must implement or improve)

**R1. Resolve.** Input `(archive_id, machine_id, agent, session_id)`. Compute hex-encoded base key `capture-v1/archives/<hex>/machines/<hex>/agents/<hex>/sessions/<hex>` (`types.go:282-287`). Verify the bucket-global `capture-v1/archive.json` equals the archive (`checkArchive`, `publication.go:147`). GET `current.json` plus an opaque token. Parse strictly (`parseCurrent`, `:263-278`): contract `session-publication/v2`, identity equal to the request, sequence in (0, MaxInt64], `manifest_sha256` a 64-char lowercase hex digest, `manifest_key` equals base key + `/manifests/<sha>.json`.
**R2. Pin.** GET manifest bytes, require `sha256(bytes) == manifest_sha256`, decode with unknown fields rejected and no trailing data, run `ValidateManifest` (<= 8 MiB metadata, <= 1024 sources, unique effective (source_hash, epoch) pairs, withheld rules), check identity, publication id and sequence equal `current.json` (`resolve`, `:280-299`). **All later work uses this pinned manifest.** Current may advance during export without affecting it.
**R3. Select.** A source is selected iff `withheld == nil` (`selection.go:151`). Withheld sources have metadata only and cause no object access.
**R4. Budget.** Sum selected `object.size` against `--max-bytes` (default 1 GiB, max 1 TiB) before any body I/O or destination creation (`export.go:43-57`). Refusal is `ErrExportBudget`, exit 4, and is permanent for that limit. The sum is **cumulative across all retained epochs**.
**R5. Stream and verify.** For each selected source, stream `objects/<sha256-of-encoded>.zst` through a zstd decoder (single worker, 64 MiB window and memory cap), read at most declared encoded size + 1, produce at most declared decoded size + 1, verify decoded size, encoded size, encoded SHA-256 and decoded SHA-256, and consume trailing bytes so extra frames cannot hide (`streamObject`, `publication.go:319-351`). Write to `bodies/<NNNN>.bin` (NNNN is the manifest source index, so withheld sources leave gaps) with 0600, fsync.
**R6. Complete atomically.** Write `.metadata.tmp`, fsync, rename to `metadata.json`, fsync directory and parent (`export.go:120-139`). Absent `metadata.json` means incomplete. Destination must not exist. Errors may leave a partial directory the caller must clean.
**R7. Report.** Today the CLI prints nothing on success and a free-text error on failure. Exit codes `0 ok, 1 failed, 2 usage, 3 locked, 4 export budget` (`internal/cli/cli.go:36-43`).
**Heads (R8).** Scoped LIST by delimiter under `.../agents/<agent>/sessions/` (never bucket-wide), `limit` common prefixes per call, `StartAfter` = last validated prefix, response capped at 2 MiB, one retry attempt (`publicationr2/heads.go:38-75`). Then up to 8 concurrent `current.json` reads, **each with a 5 s child timeout**, producing a head, a skip (not found), or a `current-unreadable` failure for any other error (`heads.go:196-237`). Only caller cancellation and `ErrDestinationPaused` abort the page. A head reads only `current.json`, never the manifest or objects (`heads.go:42-57`).

### 3.2 Provenance and error reporting as implemented

- Provenance in the export: `identity`, `publication_id`, `sequence`, `manifest_sha256`, per-source `path` (original path, provenance only), `role`, `state`, `revision`, `epoch_id`, `associations`, three timestamps.
- Partial reporting is only through `coverage` (strings, not an enum; producers use complete/partial/unknown, `session-publication-v1.md`) and withheld sources.
- Failure reporting from the reader to the consumer is by **exit code and discarded stderr**. The refresher's `run()` sends stderr to `/dev/null` by design (`publication_refresh.py:97`), so the typed vocabulary is `process_failed`, `process_timeout`, `export_too_large`, `process_output_limit`, `process_start_failed`.

### 3.3 Size cap and what the numbers mean

- Brief says 768 MiB. That is the **deployed** `--max-bytes 805306368` in the refresher plist (doc says, `session-capture-handoff.md:2124`). The code default is 1 GiB (`export.go:13`, `publication_refresh.py:29`), raised from 256 MiB for one 615 MB object (comment `publication_refresh.py:26-28`).
- Two targets, one per host, are `export_too_large`. Cause is not size of the latest epoch but cumulative retained epochs of `thread_history_1.sqlite` (Mac 6 epochs, 3,520 MiB decoded; handoff `:1779`) after every Codex run rewrites the DB (doc says, `:1871-1875`).
- Doc says the check reads only the manifest, so failing sweeps download nothing (`:1781`). Confirmed by `export.go:33-39`: `pinned` then `checkExportBudget`, then body I/O.
- The cap bounds a number the consumer does not need (cumulative history) and not the number it does (latest epoch, or bytes not yet present locally). GAP-07 says the same (`production-gap-register-20261102.md:236`).

### 3.4 The refresher regression and what in the interface caused it

Observed (doc says, `session-capture-handoff.md:65-75,1876`): sweep time about 19 min at 18:38Z, about 2 h at 20:45Z. About 57k expected targets over two endpoints. BC-01 endpoint `process_failed` since 20:45:17Z. 66 `current-unreadable` head failures from 19:58Z, all previously imported Pi and Codex resources. A manual first page took 22 s for 232 heads and 6 failures. Refresher CPU about 0.47 cores at 60 s interval (more than the coordinator). Root cause **not established**.
Structural contributors I can identify in the code (all **inferred**, none measured by me):

1. **One OS process per 256 heads.** Each `current-heads` run builds the config, backend and S3 client, TLS handshakes, reads `archive.json`, does a scoped LIST and then up to 256 CURRENT GETs. 57k targets is about 225 spawns per sweep. The measured 22 s per page is mostly connection setup plus bounded-concurrency GETs, so there is no connection reuse across pages.
2. **The 5 s per-head timeout has the same outcome as corruption.** `readHeadBatch` maps any non-`ErrNotFound` error, including `context deadline exceeded` and throttling, to `current-unreadable` (`heads.go:226-233`). Python records it as a durable `head_failures` row, retried only on the next full sweep (`publication_refresh.py:306-318`). Throttling therefore looks like 66 broken sessions. The reason vocabulary has one value.
3. **Worst-case page time approaches the process timeout.** 256 heads / 8 workers x 5 s = 160 s, plus LIST and startup, against `timeout=300`. Under throttling, whole-page timeouts become `process_timeout` on the endpoint, which is the `process_failed`/enumeration error class. Python cannot tell a slow page from a dead one because stderr is discarded.
4. **Every sweep re-reads every CURRENT.** There is no change feed, ETag set or "heads since" call, so the full lexical walk repeats each cycle (`heads.go:125-127` says callers restart empty). The README states why: a persistent lexical watermark would miss earlier-sorted sessions (`README.md`, "Each sweep starts enumeration at the beginning").
5. **Per-target local verification is O(bytes) and O(DuckDB opens).** For each unchanged target `_projection_coordinate_locked` opens a DuckDB connection, runs `SHOW TABLES` and two `PRAGMA table_info`, queries, then streams SHA-256 over every owned body (`publication_refresh.py:389-443`). Doc says a native sample shows DuckDB planning/reads, not a hang (`session-capture-handoff.md:1876`). That fits this per-target open-and-hash path.
6. **Whole-session export and import.** Any change re-exports every retained epoch (cumulative), Python copies and hashes each body again (`publications.py:284-305`), deletes and re-inserts the session's events (`:~370`). A session with 206 epochs (`~/.claude.json`, doc says, handoff line 91) makes cost quadratic over time.

What the interface lacked, in one line each: a streaming multi-session call, per-head typed failure reasons, a way to say "give me only bodies I do not have", exit-code and structured-error taxonomy, and a head record that includes `manifest_sha256` so unchanged sessions can be skipped by digest compare without re-hashing local bytes.

## 4. Interface options for the Rust rewrite

### 4.1 Tradeoffs (evidence column cites the current pain)

| Option | Strengths | Weaknesses | Evidence from current pain |
| --- | --- | --- | --- |
| **A. Subprocess + JSON/files (status quo shape)** | Credentials stay in the Go/Rust process only. Crash and memory isolation. Journey tests use the real binary (`test_capture_journey.py:25-33`). Works under launchd/systemd wrappers (`launch.py`). | Spawn per page and per export. Lossy errors. Double disk write and re-hash. Python polls a directory to enforce a byte cap. | Sweep slowdown (3.4 items 1, 3, 5). `process_failed` is all the handoff has for the BC-01 endpoint failure. `run()` discards stderr |
| **B. PyO3 library in the Python process** | No spawn. Can yield bodies as streams. Typed exceptions. | Python process now holds R2 credentials and the zstd/S3 stack. A panic or abort takes the dashboard or refresher down. Wheel build per platform (macOS arm64 and Linux amd64) inside the pinned Nix env. GIL contention with DuckDB work. Test seam moves from "real binary" to "real extension". | The doc explicitly limits credentials to the actual Go reader (`README.md`, refresher section) and `launch.py` exists to hand them over safely. PyO3 reverses that. No evidence of a spawn-latency-only problem that PyO3 alone fixes |
| **C. Rust writes Parquet or Arrow, DuckDB reads directly** | Removes the Python importer, the Python copy, and the per-event JSONL staging (`publications.py:242-277`). Parquet files named by `manifest_sha256` are idempotent and atomic (write temp, rename). Fast on 600 MB sessions. DuckDB `read_parquet` or views replace the `events` tables. | Moves normalization policy into Rust unless split. Replacing a session's rows becomes file replacement plus a view, losing the single-transaction replace (`publications.py:373-381`) unless a manifest file selects the live set. Schema is now a Rust-to-DuckDB contract | `iter_events` throughput and the per-session transaction are the heavy path (README "Expect several GB"). Bench exists (`bench_publication_import.py`) but I did not run it |
| **D. Local HTTP or gRPC reader service** | Long-lived process amortizes the S3 client and TLS. Natural place for a CURRENT cache and a change feed. Streaming and typed errors come free. | A third resident service per host (ports, auth, lifecycle, two supervisors). The coordinator already is a resident Go process, so a second one is redundant. Credential handoff problem repeats. SSH-style cross-host use was already rejected as an autonomous dependency (`README.md`, ssh note). | Pain 1 and 2 (spawn, no reuse) are addressed. Pain 3 to 6 are addressed by API shape, not transport |

### 4.2 Recommendation

**Keep the process boundary. Replace the JSON-per-page protocol with a streaming, typed, delta-aware CLI, and make Rust own all wire validation. Do not choose PyO3 or a daemon.**
Concretely, driven by the code cited above:

1. **One call per sweep.** `reader heads --config C --format ndjson` emits one line per head or per failure and a final `{"complete":true,...}` trailer. The reader holds the S3 client for the whole sweep and owns pagination. Motivation: 3.4 items 1 and 4.
2. **Typed failure reasons** on heads and on every nonzero exit: at minimum `not_found`, `timeout`, `throttled`, `auth`, `invalid_metadata`, `integrity`, `unsupported_contract`, `budget`, each with a transient/permanent bit. Emit a structured one-line JSON diagnostic on stderr, content-free, which the refresher may log. Motivation: `heads.go:226-233`, `publication_refresh.py:97,116-117`.
3. **Head includes `manifest_sha256`.** `current.json` already has it (`types.go:342-349`), and `parseCurrent` already reads it. Consumers skip unchanged sessions by digest compare and drop the per-sweep body re-hash (3.4 item 5), keeping a cheaper local integrity check on a slower cadence.
4. **Split manifest from bodies.** `reader pin` writes `metadata.json` only. `reader fetch --store DIR` writes any missing `<sha256>` body directly into the consumer's content-addressed store. Python then skips the copy at `publications.py:284-305`, and re-imports only epochs whose body hash is new. Motivation: whole-session cumulative export (3.3, 3.4 item 6) and GAP-07.
5. **Per-epoch budget, not cumulative.** Bound the bytes that will actually be fetched plus a separate per-object cap. Motivation: GAP-07 and the two permanent `export_too_large` targets.
6. **Emit a verified typed envelope and delete the Python mirror.** Rust parses and validates once and writes `metadata.json` carrying a schema-version field. Python only checks the version and trusts the rest (boundary discipline). Also write the raw manifest bytes beside it (`manifest.json`) so `manifest_sha256` is verifiable by the consumer. Motivation: section 2.
7. **Drop the `queue` discovery mode and the `ssh_host` transport** unless the operator wants them. Production uses `remote-current` locally (doc says, `README.md`). Operator decision, see open questions.
8. **Later, optional (option C for rows):** if Rust also frames records, emit Arrow IPC or Parquet of structural rows per body hash. Only after items 1-6 land and the sweep cost is measured again.

Applies principles read this session. **Subtract before you add** removed the legacy pipeline and the two discovery transports from the port scope. **Boundary discipline** moved all export validation to the Rust side of the boundary. **Make operations idempotent** shaped items 3, 4 and the content-addressed fetch (re-running converges, no re-hash needed). **Minimize reader load** and **type-system discipline** shaped the typed failure enum and the single versioned envelope in place of two hand-synced validators.

## 5. Semantic normalization placement

**What exists.** Three layers are mixed inside `normalize_record` and `iter_events`.

| Layer | Examples | Deterministic | Where it should live |
| --- | --- | --- | --- |
| Framing | line split, byte offset/length, `malformed_json`, depth, lone surrogates, `incomplete_tail`, event id `trace:line:ordinal` | Yes | **Rust**, beside the reader. Needs a conformance fixture for depth, NaN, big ints (below) |
| Structural extraction | `native_id`, `parent_id`, `raw_type` (`codex: typ.payload.type`), content-block split, `tool_name/call_id/args`, `is_error`, `model` carry-forward | Yes, per agent schema | **Rust**, as a typed record per agent. Per-agent schemas change when agents change, so version them |
| Classification | `origin`, `is_human`, `SYNTHETIC` regex, subagent override, native-ID `duplicate`, Codex `is_mirror` pairing | Heuristic policy that the research already says "is heuristic and tested, not perfect" (`README.md`, "Interpretation matters") | **SQL views over the structural rows.** DuckDB RE2 supports `(?i)` and `\b`; `pair_codex_mirrors` is already SQL; the duplicate key is expressible as a window `row_number()` (inferred, not run). Changing policy then needs no re-import |

**Why not leave it all in Python.** It is the only reason Python must stay on the import path. It also owns the unspecified identity hash (1.6). **Why not move all to Rust.** The classification is analysis policy that the operator will iterate, and `events.py` is shared with frozen legacy research output.

### Drift history (measured from docs and git)

- **Legacy consumer mismatch.** Analytics was built for schema-3 manifests while the collector moved to schemas 4/5 and, later, v2. Silent omission of `/records/session/` commits, `views.sql` expecting inline `launches`, `events.py` rejecting duplicate file identity across epochs (`docs/research/session-capture-contract-review.md:214-228`). Fixed by `guard_inventory` plus a new v2 consumer, not by reconciling the old one.
- **GAP-08.** 16 MiB vs 8 MiB, `sequence=0`, unknown fields, local manifest hash (`production-gap-register-20261102.md:236-252`). Fixed by re-mirroring (7bd6b65, cb54fd7).
- **GAP-07.** Python 256 MiB cap vs Go 1 GiB default, same cumulative semantics (`:216-234`, fixed by 176e91b, the typed exit code and 1 GiB).
- **Residual.** The Python 1 GiB ceiling vs Go 1 TiB, duplicated `exit 4`, agent allow-list, page size 256, and the two private identity serializations.

### Conformance hazards a Rust port must pin with fixtures (inferred from Python and serde defaults, not run)

- Python `json.loads` accepts `NaN`, `Infinity`, big integers, and escaped lone surrogates (caught later as `malformed_record`). serde_json rejects the first two by default.
- Python recursion ceiling about 1000 vs serde_json 128.
- Duplicate keys: Python keeps the last. serde_json `Value` keeps the last. Streaming parsers differ.
- `tool_args` is re-serialized with `json.dumps(args, ensure_ascii=False)` (`events.py:~163`), so number lexemes change (for example `1E5` becomes `100000.0`) and key order follows parse order. Rust needs the `preserve_order` feature and a decision on float formatting.
- Identity hashes need `ensure_ascii=True` escaping (1.6).

## 6. Golden fixtures

**What exists (measured).**

- `capture-v1 fixture --output DIR [--backend sdk-fixture]` is an unlisted Go command (`internal/cli/capture.go:60-62`, `capture_fixture.go`). It runs the **real writer**: opens a queue, captures four steps (main append, a subagent child, a rewrite with a malformed line and truncated tail), delivers to a Local or injected-SDK store, exports, and writes `expectations.json` (identity, retained bodies, roles). Fixed clock `1_700_000_000`, archive `fixture-archive-v1`, machine `fixture-machine`, Claude only.
- `capture-v1 isolation-fixture --stage crash|repair|revoke` for optional-artifact withholding.
- `make capture-acceptance` (`Makefile:67-74`) builds the binary and runs the `*journey.py` tests, `candidate-live-r2` and `capture-delivery` tests. The Python journey asserts coverage, ids, hashes, roles and exact events from the real export (`test_capture_journey.py:47-110`).
- Seeded properties A1-A8 (`test_publication_properties.py`): the oracle is computed from raw body bytes with stdlib `json` only (`:73-99`), then compared with DuckDB dumps for idempotence, order invariance, epoch non-collision, partial tail, append monotonicity, corruption atomicity, Go-bound parity and cap math. Seeds replay with `PUB_SEED=n`.
- `tools/analytics/testdata/` is an empty untracked directory (measured, `ls`). There are **no committed golden export directories**.

**Suitability as cross-language conformance tests.**

| Property | Verdict |
| --- | --- |
| Produced by the actual writer, not hand-written | Yes. This is the strongest feature |
| Deterministic bytes | Partly. Absolute source paths are provenance, so `manifest_sha256` differs across roots (`capture_fixture.go` comment). Needs a fixed root or a path-redacted comparison |
| Committed and reviewable | No. Generated on demand, so a Rust reader and the Go reader cannot be compared offline unless both generate |
| Agent coverage | Claude only. No Codex mirrors, no Pi `toolResult`, no `encrypted_content` through the real writer. Codex, Pi cases live in hand-built `test_events.py` and `test_publications.py` |
| Normalization edge cases (depth, NaN, lone surrogates, huge ints) | Present as Python tests (`test_events.py`), not as byte fixtures |
| Reader failure cases (corrupt object, missing object, unsupported archive) | Yes in the journey (`test_capture_journey.py:266-320`) |

**Recommendation.** Commit a small `conformance/` set, generated once by the Go writer and then frozen. Include per case `export/` (metadata plus bodies), `expected_rows.jsonl` (events/issues/sources) produced by the current Python importer, and `expected_errors`. Cover Claude, Codex (with mirrors), Pi, withheld artifact, partial tail, depth 129 and 1000, `NaN`, lone surrogate, a 2-epoch rewrite, and non-ASCII identities. The Rust reader and Rust framing then run against the same files. Keep the live writer-to-reader journey as the second gate.

## Open questions for the operator

1. Should the Rust reader own record framing and structural extraction (so Python leaves the import path), or stay out of record parsing and expose only verified bytes? This decides option C versus a leaner A.
2. Is the cumulative retained-epoch cap policy acceptable to replace with per-epoch fetch (GAP-07 "policy decision")? And should two Codex SQLite stores be imported at all (handoff item 4)?
3. May the `queue` discovery mode and `ssh_host` transport be dropped from the rewrite?
4. Must the existing `.analytics/publications.duckdb` and refresh SQLite v4 status survive the rewrite, or is a fresh projection acceptable? The identity hash and body-store layout are the compatibility surface.
5. Is `current-unreadable` allowed to split into timeout, throttled, corrupt, so the dashboard can distinguish them?
6. Is a candidate `restore` command (write bytes back to a source path) in scope for the rewrite, or is `export` the entire restore story?

## Things I could not verify

- The cause of the refresher regression. I did not run a sweep, touch R2 or BC-01, or profile. Section 3.4 is code reading only.
- Throughput claims about Rust, Parquet, or SQL-based classification. No measurement. DuckDB RE2 support for the `SYNTHETIC` pattern and a window-based duplicate rule are inferred, not run.
- The claimed per-sweep call counts (about 225 spawns) are arithmetic from the doc's 57k targets and 256 page size, not an observation.
- serde_json and Python parse differences are from library defaults I know, not from running both.
- Whether other Python code outside `tools/` consumes `capture-v1` output (I searched `tools/` and `internal/cli` only).
- The handoff content is the uncommitted working tree version. I did not diff it against HEAD beyond the stat.
- Line numbers marked `~` are approximate for lines I read in a stream.
