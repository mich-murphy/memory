# 32. `memory export` and the consumer gate (2026-11-17)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 31](31-streaming-and-run-findings.md). Evidence goes to `evidence/23-export/`. Text that
states a plan stays as written; later changes are marked.

The acceptance run `20261117-a28bfa4` keeps running while this brief is worked on. It uses its own
release binary under `~/.local/libexec/memory-acceptance/`, so nothing here changes it. Nothing in
this brief is installed, sent to R2 or run against the run's state root.

## Goal

DESIGN 17 slice 2b: "`memory export` to the `session-reader/v2` envelope. The existing Python
importer, event index and report run against the export unchanged", and its exit: "the consumer
gate is green". Answer 9 places this after the run and before cutover; the code is built and
tested offline now, and run on real data after the run ends.

1. **EXPORT**: a `memory-reader` export library and a `memory export` subcommand that write one
   session owner's folded state as a `session-reader/v2` directory (`metadata.json` plus
   `bodies/NNNN.bin`), streaming bodies piece by piece.
2. **GATE**: actual Rust writer output (the real `memory` binary against the loopback S3 fake) ->
   `memory export` -> the unchanged `tools/analytics/publications.py` importer -> event index ->
   `report`, for Claude, Codex and Pi, plus the privacy canary scan of the export (the A13.1 abort
   trigger), as a gate that cannot pass by skipping.

Not in this brief: lifting the empty-prefix refusal (`config_prefix_required`, a separate brief
before cutover); a heads/listing command or the Go refresher's CLI shape (cutover concern);
`unpark`; snapshot and config kinds (slice 3).

## Checkout

`main` is `988ab88`. This brief is on `docs/brief-32`. Each worker gets one worktree under
`../memory-wt/` on its own branch from `docs/brief-32`.

## Facts checked on `988ab88` before writing

A read-only planner scoped the work; the parent rechecked each fact below and corrected four of
the planner's claims (epoch numbering, role storage, timestamp type, the local venv). Paths are
under `rust/crates/` unless they start with `tools/`.

| Fact | Where |
|---|---|
| Importer `_load`: top keys exactly `contract`, `manifest_sha256`, `manifest`, `bodies`; `contract == "session-reader/v2"`; manifest contract `session-publication/v2` with exactly the `_MANIFEST` keys; unknown keys rejected everywhere | `tools/analytics/publications.py:39-101` |
| `sequence` and per-source `revision` must be ints in `1..=2^63-1`; the three `*_ns` ints `>= 0`, `bool` rejected | `publications.py:52-59`, `:107-112`, `:130-132` |
| Selected source: exact `_SOURCE`/`_OBJECT` keys; `object.encoding == "zstd"`; `sha256`/`encoded_sha256` 64 lowercase hex; `key` non-empty; pair `(sha256(source_id), epoch_id)` unique; `optional` only when `role == "artifact"` | `publications.py:138-176` |
| `object.key`, `encoding`, `encoded_sha256`, `encoded_size` are shape-checked and never used; `object.sha256`/`size` must equal the body's | `publications.py:159-170`, `:178-191`, `:385-399` |
| Bodies: exact `_BODY` keys, one per selected pair, safe relative path; copied with symlink refusal, size cap and sha256 check (`body_integrity`) | `publications.py:178-191`, `:284-305` |
| `len(sources) <= 1024`; `metadata.json` `<= 8 MiB` | `publications.py:39-40`, `:83-86`, `:128` |
| Role use: `artifact` is not parsed (one `artifact_not_normalized` issue); `subagent` user events become `origin='agent'`, not human; any other role is parsed by `events.iter_events`; Codex traces are mirror-paired | `publications.py:405-421`, `:377` |
| Idempotency: lower `sequence` is `stale_publication`; equal sequence with another id or digest is `publication_conflict`; same id and digest is `unchanged` | `publications.py:336-357` |
| Report staleness uses `captured_at_ns`; `0` counts as an unknown capture time | `publications.py:511-524` |
| Go writer values: `state` `active` for the current epoch, `sealed` for superseded ones; coverage `partial`/`partial` with `missing`; roles accepted `main`, `subagent`, `tool-result`, `transcript`, `artifact` | `internal/sessioncapture/capture.go:302-334`, `queue.go:320` |
| Rust manifest `seq` starts at 0 (first manifest is the checkpoint at seq 0) | `memory-core/src/manifest/build.rs:50-77` |
| Rust epoch numbers start at 0 (`next_epoch` is `map_or(0, …)`) and are consecutive | `memory/src/capture.rs:503-505`; `memory-core/src/manifest/schema.rs:597-605` |
| The manifest stores `kind`, `path`, `class`, `epochs`, `deleted_at` per source; **no role** | `memory-core/src/manifest/state.rs:10-21` |
| The locator's role: `Main`, `Superseded`, `Child`, `History`, `SessionIndex`, `Artifact`, `Asset`; one kind can carry two roles (Claude transcript Main or Superseded; Pi session Main or Child) | `memory-core/src/locator.rs:294-309`, `:659-686`, `:796-860` |
| `Timestamp` is a `YYYY-MM-DDTHH:MM:SSZ` string; manifests carry `captured_at`, `current.json` `updated_at`; the only civil-date conversion is private in `hint.rs` | `memory-core/src/manifest/schema.rs:138-200`, `:690`, `:924`; `memory-core/src/hint.rs:428` |
| `read_owner` returns only `Folded{owner, state, position}`; `restore_epoch` builds a whole epoch in one `Vec` | `memory-reader/src/lib.rs:96-130`, `:185-227` |
| An epoch may have zero pieces (size 0); `Extend` may not | `memory-core/src/manifest/schema.rs:403-420`, `:484` |
| `restore`: owner text via `parse_owner_prefix`; config from the state root; `build_store` refuses an empty prefix; exits 0, 1, 2, 64, 66; one stderr line, nothing on stdout | `memory/src/restore.rs:28-42`, `:305-345`; `memory/src/assemble.rs:40-60` |
| `memory-reader` has `serde_json` and `sha2` as dev-dependencies only; both are workspace dependencies | `memory-reader/Cargo.toml`; `rust/Cargo.toml:17`, `:21` |
| The real-binary tests drive `memory daemon` against the loopback fake through `Rig` (Claude root only today) | `memory/tests/s3_store/daemon_restore.rs:45-100`; `memory/tests/s3_store/assemble.rs:32-61` |
| The Nix shells provide `python314` without duckdb; the Go consumer job uses a venv from `tools/analytics/requirements.txt` (`duckdb==1.5.6`); the Makefile uses `.analytics-venv` when present, and it exists here with duckdb 1.5.6 (ignored by git) | `flake.nix:93`, `:120`; `.github/workflows/ci.yml:54-68`; `Makefile:17`; `.gitignore:3` |
| `gates.sh` 04 runs nextest without the property tests; 08 is coverage over all tests | `rust/scripts/gates.sh:41-58` |

## Decisions the parent made where the design is silent

DESIGN names only the target envelope. Each decision is reversible before cutover. A worker that
finds one wrong reports it as a question and does not decide.

| Topic | Decision | Reason |
|---|---|---|
| Unit | One session owner per call: `memory export <owner> --out DIR [--max-bytes N]`. A `shared/...` owner is refused (`owner_shared`, exit 64): the envelope needs a `session_id` | `publications.py:102-106`; shared owners are slice 3 |
| `manifest_sha256`, `publication_id` | Both the hex of `Folded.position.manifest`, the digest of the exact stored head manifest bytes (verified against its key by `read_owner`) | Deterministic: re-export of the same head is `unchanged`; a new head is a new id |
| `sequence` | `position.seq + 1` (checked) | Importer needs `>= 1`; seq starts at 0 |
| Times | `captured_at_ns` and `observed_at_ns` (manifest and every source) = the head manifest's `captured_at`; `published_at_ns` = `current.json` `updated_at`; converted by a new public `Timestamp::unix_ns()` in memory-core (checked arithmetic) | No per-epoch time is stored; `0` would read as unknown |
| `coverage` | `{"discovery": "complete", "enrichment": "partial", "missing": ["snapshot-kinds-deferred"]}` | Append sources are enumerated; snapshot and config kinds are withheld until slice 3 |
| Sources | One envelope source per retained epoch of every live (not deleted) source, ordered by source id then epoch. Deleted sources are skipped and counted | DESIGN 6.4 keeps every append epoch; same skip as `restore` |
| `source_id`, `path` | The Rust `SourceId` string; the stored root-relative path (provenance only) | Stable trace ids |
| `epoch_id`, `revision`, `state` | `epoch_id` = decimal `n`; `revision` = `n + 1`; `state` = `active` for the source's last epoch, `sealed` for earlier ones | Epochs start at 0; Go's state words |
| `role` | A pure memory-core function `export_role(kind, path)`: locator `Main` -> `main`, `Superseded` -> `transcript`, `Child` -> `subagent`, everything else -> `artifact`, with one kind exception: `CodexCompressed` -> `artifact` (amended 2026-11-17 22:56Z). Pinned by a property: for every path `locate` maps to a session source, `export_role(kind, path)` equals the mapped locator role, except for that kind | Role is not stored; the locator is the independent oracle; artifacts (tool results, debug, aux) are not parsed, so they raise no bogus issues. The Rust writer stores a `.jsonl.zst` rollout's on-disk bytes unchanged (its only zstd call compresses pieces, `memory/src/environment.rs:161`), whereas Go decompressed them before upload (`internal/syncer/prepare.go:147`). Parsed as `main`, they would turn into `malformed_json` issues |
| `optional`, `associations`, `withheld` | `false`, `[]`, never emitted | Rust records no withheld sources in R2 (DESIGN 6.3) |
| `object` for an epoch | `sha256`/`size` = the epoch's; `encoding` `"zstd"`; one piece: that piece's `object`, `encoded_sha256`, `encoded_len`; several pieces: the first piece's `object`, `encoded_size` = checked sum, `encoded_sha256` = sha256 of the pieces' `encoded_sha256` hex strings concatenated in order; zero pieces: key `"objects/none"`, `encoded_size` 0, `encoded_sha256` = sha256 of the empty string. Documented in the module as a commitment that is never fetched | Shape-checked only by the importer; a new field is impossible (unknown keys rejected) |
| Bodies | `bodies/NNNN.bin` with `NNNN` the zero-padded index in `sources`, written piece by piece (fetch, `decode_piece`, write, incremental sha256), length and digest checked at the end; at most one stored and one decoded piece alive | 128 MB target (A10.4); export must not call `restore_epoch` |
| Destination | Must not exist (checked first; a symlink is refused); created 0700, `bodies/` 0700, files 0600 `create_new`, each fsynced; `.metadata.tmp` written, fsynced, renamed to `metadata.json`, directory and parent fsynced; on failure remove what the run created | Go `export.go` contract; absent `metadata.json` means incomplete |
| Limits | Before creating anything: exported bytes (sum of epoch sizes, checked) `<= --max-bytes` (default 1 GiB, accepted `1..=1 TiB`) else `export_too_large` **exit 4**; more than 1024 sources or metadata over 8 MiB is `export_too_many_sources` / `export_metadata_too_large`, exit 1 | Exit 4 is what the Python side already treats as too large (`publication_refresh.py`); fail closed, never drop epochs |
| Exit codes and output | 0 exported, 1 failed, 2 config or token, 4 too large, 64 usage/owner text/shared owner, 66 owner absent (nothing created). Success: one stderr line `memory: exported sources=N skipped_deleted=D bytes=B`; failure: one line `memory: <code>`. Nothing on stdout; no key, path, endpoint or owner text in any line | Same discipline as `restore` |
| Consumer gate location | A Rust integration test module in the `s3_store` test binary (names start `consumer_gate_`) runs the real daemon over Claude, Codex and Pi fixture roots, then `memory export`, then the Python half `tools/analytics/test_rust_export_journey.py` (unchanged importer, called through `publications.import_export` and `report`). Python comes from `MEMORY_ANALYTICS_PYTHON`, default `<repo>/.analytics-venv/bin/python` | The fake is a Rust helper; the importer stays untouched |
| Cannot skip | With `MEMORY_REQUIRE_CONSUMER=1` a missing Python or duckdb fails the test. Without it the test prints `consumer gate skipped` and returns. `gates.sh` 04 excludes `consumer_gate_`; a new sub-gate `14-consumer` runs it with `MEMORY_REQUIRE_CONSUMER=1`. CI: a new job in `rust.yml` that builds the venv from `requirements.txt` and runs `14-consumer` | A quiet pass is never counted as green |
| Canary | The gate's fixture plants canary strings only where the writer must withhold them (credential paths, protected basenames, unknown kinds, symlink chains, hook env) beside the real transcripts; the test scans `metadata.json` and every body of every export for each canary, including one split across a piece boundary | A13.1 abort trigger; for transcripts the transform is the identity, so the canary proves admission, not scrubbing |

## Increments

| # | Increment | Branch | Owner | Wave |
|---|---|---|---|---|
| 1 | EXPORT | `rust/export` | worker | 1 |
| 2 | GATE | `rust/consumer-gate` | worker | 2 (from the accepted EXPORT branch) |

### EXPORT

Owns `memory-core/src/manifest/schema.rs` (only `Timestamp::unix_ns` and its tests), a new
`memory-core/src/export_role.rs` (or a function in `locator.rs`) with its property test,
`memory-reader/src/lib.rs` (a `read_owner_head` returning the folded state plus head
`captured_at` and `current.json` `updated_at`; `read_owner` unchanged for its callers), a new
`memory-reader/src/export.rs`, `memory-reader/Cargo.toml` (`serde_json`, `sha2` moved to normal
dependencies, nothing new), new `memory/src/export.rs`, `memory/src/main.rs` (one arm),
`memory/src/lib.rs` (one module line), and tests under `memory-reader/tests/` and
`memory/tests/s3_store/export.rs`.

- Proof: one test per row of the decision table; bodies byte-equal to `restore_epoch` for 0, 1,
  3 and 70 pieces; a 70-piece epoch exported with a fetch that counts live buffers (at most one
  stored buffer alive); every corruption of `decode_piece` and a gap leave no `metadata.json` and
  no directory; limits refuse before any file exists (1025 epochs, `u64::MAX` sizes, 8 MiB
  metadata); `Timestamp::unix_ns` against an independent oracle (a table of known instants plus a
  property over the day count from `hint.rs`'s civil conversion); `export_role` property against
  `locate`; the binary's exit-code table and its refusals (existing destination unchanged,
  absent owner creates nothing, shared owner, bad `--max-bytes`); no input text in any output.
- Teeth: `sequence = seq`; `revision = n`; `state` always `active`; `object.size` = encoded size;
  multi-piece commitment in reverse order; budget with wrapping `+`; destination created before
  the limits; an existing destination allowed; `metadata.json` renamed before bodies are synced;
  final epoch digest check removed; `Child` mapped to `main`; a deleted source exported; exit 1
  instead of 4 for too large.

### GATE

Owns `memory/tests/s3_store/consumer_gate.rs` (plus its `mod` line and any `Rig` addition for
Codex and Pi roots in `assemble.rs`), `tools/analytics/test_rust_export_journey.py` (new; skips
unless `RUST_EXPORT_JOURNEY` names an expectations file the Rust test wrote),
`rust/scripts/gates.sh` (sub-gate 14 and the 04 exclusion), `.github/workflows/rust.yml` (one
job), `docs/quality-gates.md` (the new sub-gate). Must not touch `publications.py`, `events.py`
or any existing analytics test.

- Proof, per agent, all on the real binary: capture by the daemon; `memory restore` and the
  exported bodies agree byte for byte; import returns `imported` with the expected source,
  event and issue counts; a second import of the same export is `unchanged`; after an append and
  a new commit the new export has a higher `sequence`, a new `publication_id`, and imports; a
  truncate-and-rewrite gives two epochs (`sealed` and `active`) that both import; a Claude
  subagent has no human events; a tool-results artifact gives `artifact_not_normalized` and no
  events; Codex mirror pairing runs; `report` returns the expected counts and no failures; a
  flipped body byte gives `body_integrity` and keeps the old projection; the canary scan is
  clean. A non-ASCII session id and path import, and the Python `session_key` equals one computed
  independently in the test.
- Teeth (applied by the parent to EXPORT's code on the GATE branch, each must fail the gate):
  `encoding` `"zstd-3"`; `role` `transcript` for artifacts; `optional` true for a transcript;
  a body's `sha256` off by one byte; a source dropped from `bodies`; `manifest_sha256` taken from
  a re-serialization; the admission of one canary-bearing protected basename disabled.

## Rules for workers

The rules of [brief 28](28-production-prerequisites.md#rules-for-workers) apply unchanged, with
those of briefs 29 to 31: an existing test is removed only when the report names the test that
now carries the same assertion; no new dependency (moving a workspace dependency from dev to
normal in `memory-reader` is allowed and must be reported); no change to what `status.json`,
`index.json`, a manifest or an object stores; never inspect, signal or read the state of the
running acceptance daemon or anything under `~/.local/state/memory-acceptance*`; never read
`.env`. Real-binary tests are timing-sensitive: a worker stresses a new one 30 times alone (no
other cargo job) and reports the failures.

## Acceptance (parent)

As in brief 31: whole diff read; the seven checks (fmt, clippy, rustdoc, nextest, doctests,
schemas, `cargo deny`); the four mutation predicates each still occur once; the teeth above run by
the parent on the intended assertion; `scripts/gates.sh` once on the combined tip, including
`14-consumer` against `.analytics-venv`; the Go `make capture-acceptance` untouched. Then push the
stack (secret check of the range first), hosted gates including the new consumer job, merge.

After this brief: end the acceptance run (two clean days), teardown, prefix deleted and listed
empty; the short streamed-binary run on real data, which also exports every captured owner and
imports it (the slice 2b exit on real data); then lifting the empty-prefix refusal and cutover
(DESIGN 18).

## Known limits carried

Per-source times are the head's capture time; a multi-piece epoch's `object` descriptor is a
commitment, not a fetchable object; the Go refresher's `capture-v1` CLI and heads pages have no
Rust equivalent yet; deleted files are never recorded as deleted (slice 3); `memory check` holds
whole sources.

## Progress ledger

- 2026-11-17 22:50Z: brief written on `988ab88`; planner claims rechecked, four corrected.
- 2026-11-17 22:56Z: EXPORT worker started on `rust/export`; first commit `71964dc`
  (`Timestamp::unix_ns`, `export_role`, oracle tests) reviewed by the parent. Correction sent:
  `CodexCompressed` -> `artifact` (its stored bytes are compressed); role row amended. Uptake
  pending. Finding for later: Codex rollouts that Codex has recompressed reach the importer
  unparsed, unlike in Go; decompressing at export or capture is a decision for after this brief.
