# 37. `memory export` decompresses recompressed Codex rollouts (2026-11-18)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 36](36-install.md). Evidence goes to `evidence/28-export-decompress/`. Text that states a
plan stays as written; later changes are marked.

The acceptance run `20261117-a28bfa4` keeps running while this brief is worked on. Nothing here is
installed, sent to R2 or run against the run's state root.

## Goal

Codex recompresses rollouts untouched for 7 days to `rollout-*.jsonl.zst` (`docs/hooks.md`,
Codex). Rust captures the on-disk bytes, so such a source (`CodexCompressed`) holds a zstd file,
and `memory export` marks it `artifact`, which the importer does not parse
(`artifact_not_normalized`). Go decompressed at capture. **Operator decision, 2026-11-18: Codex
session files are decompressed when exported.** Capture keeps storing the on-disk bytes.

**DECOMPRESS**: `memory export` writes the decompressed JSONL of every `CodexCompressed` epoch as
its body, with role `main`, and the envelope describes that body (`object.sha256`, `object.size`
and the body entry equal the decompressed bytes). The stored epoch is still verified first, piece
by piece, exactly as today; then the stream is decompressed with a bounded window, and any decode
failure, trailing data or budget overrun refuses the export and leaves nothing behind.

Not in this brief: the capture finding below; `memory check` and `memory restore` (both keep
working on the stored bytes); Claude or Pi files.

## Capture finding (parent probe, 2026-11-18 19:58Z; not fixed here)

In a throwaway worktree against the fake store (removed): a captured plain rollout replaced by
its `.jsonl.zst` makes every later round fail that source as `deterministic`, alternating with
`deferred`, for 8 rounds and never parked. The locator gives both files the same source id
(`rollouts:<name without .zst>`, Go's continuity rule), and the manifest refuses a change of kind
or path on an existing source (`ManifestError::SourceChanged`, `memory-core/src/manifest/state.rs`
`same_identity`). Moving an unchanged rollout to `archived_sessions/` is fine (audit matched).
This Mac had 0 `.jsonl.zst` files on 2026-11-18. A separate brief fixes it; it does not change
this brief, whose export sees only what capture stored.

## Checkout

`main` is `b435f73`. This brief is on `docs/brief-37`. The worker gets
`../memory-wt/b37-decompress` on `rust/export-decompress` from `docs/brief-37`. Crate paths are
under `rust/crates/`.

## Facts checked on `b435f73`

| Fact | Where |
|---|---|
| `build_envelope` renders all of `metadata.json` (sources, object descriptors, bodies) before any file exists; `write_export` then streams each epoch (fetch, `decode_piece`, write, SHA-256) and checks contiguity, `size` and `sha256` per epoch; `publish` writes metadata last; failure removes the directory | `memory-reader/src/export.rs:129-227`, `:243-339` |
| `export_role` maps `CodexCompressed` to `artifact` as a stated exception; the locator gives it role `Main`; `core_export.rs` pins the exception | `memory-core/src/locator.rs:885-922`; `memory-core/tests/core_export.rs:120-145` |
| The importer requires each body's `sha256` and `size` to equal its source's `object.sha256` and `object.size`, verifies the body file against them, and parses only non-`artifact` roles | `tools/analytics/publications.py:178-188`, `:286-303`, `:405-416` |
| The object descriptor is shape-checked and never fetched; `encoded_sha256`/`encoded_size` are a commitment to the stored pieces | `memory-reader/src/export.rs:1-13`, `:93-123` |
| Go decoded rollouts with one decoder thread, low-memory mode and at most 128 MiB decoder memory | `internal/syncer/prepare.go:148` |
| `zstd = "=0.14.3"` with default features off is already a dependency of `memory-reader` | `rust/Cargo.toml`; `memory-reader/src/lib.rs:79` |
| `memory export` maps library errors to closed codes; exit 1 for failures, 4 for too large | `memory/src/export.rs:76-95`, `:160-219` |

## Decisions

Each is reversible before cutover. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Which epochs | Every epoch of an entry whose kind is `CodexCompressed`. No sniffing of other kinds |
| Role | `export_role(CodexCompressed, _)` is `main`, as the locator role; the exception and its doc are removed and the core property no longer special-cases it |
| Path | The stored path, unchanged (with `.zst`) |
| Envelope order | Bodies are written first; `metadata.json` is rendered after, from the per-epoch `(size, sha256)` of what was written. `build_envelope` keeps every check it can do up front (shared owner, overflow, times, source count, stored-byte budget) and returns a plan; the metadata size check runs after rendering and before `publish` |
| Object descriptor | For a decompressed epoch, `sha256` and `size` are those of the exported body; `key`, `encoding`, `encoded_sha256`, `encoded_size` as today |
| Stream | Stored pieces are fetched, `decode_piece`d and checked exactly as today (contiguity, epoch `size` and `sha256` over the stored stream); each decoded piece is fed to a streaming zstd decoder whose output goes to the body file and a SHA-256. At most one stored piece, one decoded piece and the decoder's window alive |
| Bounds | Decoder window at most 2^27 bytes (`window_log_max(27)`, Go's 128 MiB); the decompressed bytes count against the export budget (`--max-bytes`, the same total as today, now of exported bytes) and exceeding it is `export_too_large` (exit 4) mid-stream |
| Validity | The stream must decode completely: one or more whole frames, no trailing bytes, no truncated frame. Otherwise `export_decompress` (new `ExportError::Decompress`, exit 1). An empty stored epoch (no pieces) exports an empty body |
| Failure | Any failure removes the destination, as today |
| Report | `memory: exported sources=N skipped_deleted=N bytes=N`, `bytes` the exported (decompressed) total |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | DECOMPRESS | `rust/export-decompress` | worker (Sonnet, medium) |

Owns `memory-reader/src/export.rs`, `memory-core/src/locator.rs` (`export_role` only),
`memory/src/export.rs` (code mapping and doc), and tests in `memory-reader/tests/export.rs`,
`memory-core/tests/core_export.rs`, `memory/tests/s3_store/export.rs` and
`memory/tests/s3_store/consumer_gate.rs`. Read-only everywhere else; Python unchanged.

- Proof (each a named test):
  - Reader: a `CodexCompressed` epoch stored as several pieces, whose bytes are two concatenated
    zstd frames of known JSONL, exports a body equal to the JSONL; the source has role `main`,
    `object.sha256`/`size` and the body entry equal the JSONL's; `encoded_*` unchanged; plain
    sources in the same owner are byte-identical to today's export.
  - Reader: corrupt zstd, trailing bytes after the last frame, a truncated frame, and a frame
    needing a window over 2^27 each give `Decompress` and leave no destination.
  - Reader: decompressed bytes over `max_bytes` (stored bytes under it) give `TooLarge` and leave no
    destination; an empty compressed epoch exports an empty body.
  - Reader: a corrupt stored piece of a compressed epoch is still `Corrupt` (stored checks first).
  - Core: `export_role` of every `CodexCompressed` path is `main`; the locator-role property holds
    with no exception.
  - Consumer gate: the fixture gains a second Codex session written as a fresh `.jsonl.zst`
    (multi-line rollout, zstd level 3); through capture, `memory export`, the unchanged importer
    and the report it counts as a Codex `main` source with its events, and no
    `artifact_not_normalized` issue for it.
  - Real binary: `memory export` of an owner with a `CodexCompressed` source exits 0, prints the
    decompressed byte count, and a corrupt stream exits 1 with `memory: export_decompress`.
- Teeth (the parent applies each; each must fail a named test): body written raw (no
  decompression) with role `main`; object `sha256`/`size` left as the stored epoch's; budget not
  counted on decompressed bytes; trailing bytes accepted; window cap removed; stored-piece checks
  skipped for compressed epochs; role left `artifact`.

## Rules for the worker

The rules of [brief 28](28-production-prerequisites.md#rules-for-workers) apply, with those of
briefs 29 to 36. No new dependency and no feature change. No change to stored formats, state
files, log lines, capture, `check` or `restore`. Never inspect, signal or read the state of the
running acceptance daemon or anything under `~/.local/state/memory-acceptance*`; never read
`.env`; never contact R2; never use `git stash`. Cargo only through
`nix develop /Users/mm/dev/memory#rust --command sh -c 'cd /Users/mm/dev/memory-wt/b37-decompress/rust && ...'`,
one cargo job at a time. Commit after each working step.

## Acceptance (parent)

Whole diff read; fmt, clippy, rustdoc, nextest, doctests, schemas, `cargo deny`, lints; the teeth
above; `scripts/gates.sh` once on the tip with `MEMORY_ANALYTICS_PYTHON` set; docs (DESIGN
decision log, handoff); push (secret check of the range), hosted gates, merge.

## Ledger

- 2026-11-18 20:05Z: brief written on `b435f73`.
- 2026-11-18 20:40Z: DECOMPRESS accepted locally at `a7ffe82` (worker 5 commits, parent 1).
  Deviation accepted: `metadata.json` is rendered up front and patched after the bodies (a second
  size check runs before publishing). The tooth "stored-piece checks skipped" first survived (the
  worker's corrupt-piece test broke the zstd magic, which fails either way); the parent added two
  tests whose stored pieces decode but differ from the manifest. Teeth 7 of 7. Full gates on
  `a7ffe82`: 14 sub-gates 0, seed 3529537878, coverage 96.56 %. Record:
  [evidence 28](evidence/28-export-decompress/). Next: push, hosted gates, merge.
