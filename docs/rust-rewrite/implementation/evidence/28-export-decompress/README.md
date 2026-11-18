# Evidence 28: `memory export` decompresses recompressed Codex rollouts (brief 37)

Dates are UTC; the work ran on 2026-11-18 from 19:58Z to 20:40Z. Status: increment DECOMPRESS
accepted locally as `a7ffe82` (6 commits over `7a180c0`). Nothing was installed, sent to R2 or
run against the acceptance run's state root. The acceptance run `20261117-a28bfa4` kept running
on its own release binary throughout.

[Brief 37](../../37-export-decompress.md) sits on `7a180c0` (on top of `main` `b435f73`).
Operator decision, 2026-11-18: Codex session files are decompressed when exported; capture keeps
storing the on-disk bytes.

## Parent probe before the brief (capture finding, not fixed here)

In a throwaway worktree against the fake store (removed afterwards): a captured plain rollout
replaced by its `.jsonl.zst` makes every later round fail that source as `deterministic`,
alternating with `deferred`, for 8 rounds, never parked (`parked_entries=0`). Both files get the
same source id (`rollouts:<name without .zst>`), and the manifest's `same_identity`
(`memory-core/src/manifest/state.rs`) refuses a change of kind or path with
`ManifestError::SourceChanged`. Moving an unchanged rollout to `archived_sessions/` audits as
matched. This Mac had 0 `.jsonl.zst` files on 2026-11-18. A separate brief owns the fix.

## Commits

`git log --reverse 7a180c0..a7ffe82`. "Worker" is a delegated Sonnet session (medium), "parent"
the coordinating session.

| Commit | By | Change |
|---|---|---|
| `d81e06b` | worker | `export_role(CodexCompressed, _)` is `main`; the exception, its doc and the property's special case removed |
| `2f7c092` | worker | `memory-reader/src/export.rs`: bodies first, `metadata.json` rendered after from what was written; streaming zstd decode of compressed epochs; `ExportError::Decompress` |
| `c5c9df9` | worker | reader tests: indexing lint |
| `142cf14` | worker | `memory export`: `export_decompress` (exit 1), report `bytes` is the exported total; real-binary tests |
| `9d3b44d` | worker | consumer gate: a second Codex session written as a level-3 `.jsonl.zst`; owner counts 5 -> 6 (also in `owners.rs`, which reuses the fixture) |
| `a7ffe82` | parent | two tests whose stored pieces decode but differ from the manifest (see teeth) |

### How it works

- `build_envelope` still renders the whole document up front and keeps every check it had
  (shared owner, overflow, times, source count, stored-byte budget, metadata size), and returns a
  plan. `write_export` streams the bodies, then patches `object.sha256`, `object.size` and the
  body entry's `sha256` and `size` of each compressed epoch by JSON pointer, re-checks the
  metadata size and publishes `metadata.json` last. Sources and bodies are pushed in one loop,
  so their indexes agree.
- A compressed epoch's stored pieces are fetched, `decode_piece`d and checked exactly as before
  (contiguity, stored epoch size and SHA-256). Each decoded piece is fed to
  `zstd::stream::raw::Decoder` with `WindowLogMax(27)` (Go's 128 MiB), reinitialised between
  frames; output goes out in 128 KiB chunks to the body file and a SHA-256. A frame left open at
  the end, trailing bytes or a decoder error is `export_decompress`.
- Every exported byte (plain or decompressed) counts against `--max-bytes` as it is written;
  exceeding it is `export_too_large` (exit 4) mid-stream. Any failure removes the destination.
- `key`, `encoding`, `encoded_sha256` and `encoded_size` are unchanged, so the descriptor still
  commits to the stored pieces. The path keeps `.zst`.

### Worker deviations accepted

- `metadata.json` is rendered up front and patched after the bodies rather than rendered from
  scratch; the up-front size check stays and a second one runs before publishing.
- `write_export` returns the exported byte total; `Envelope.bytes` stays the stored total used by
  the up-front budget check.
- No peak-memory test for compressed epochs. The decoder holds at most its window (2^27 bytes)
  plus one stored piece, one decoded piece and a 128 KiB chunk; measuring it is left to the short
  run on real data.

## Teeth (parent)

Script in the parent's scratchpad (`decompress_teeth.py`); each tooth replaced exact anchors, ran
the export, core-export, consumer-gate and owners tests and restored the files.

| Tooth | Caught by |
|---|---|
| body written raw (no decompression) | `a_multi_piece_compressed_epoch_exports_the_decompressed_jsonl_as_main`, `undecodable_…`, `decompressed_bytes_over_the_budget_…`, both real-binary tests, the consumer gate |
| object `sha256`/`size` left as stored | `a_multi_piece_compressed_epoch_…`, the consumer gate |
| budget not counted on decompressed bytes | `decompressed_bytes_over_the_budget_are_too_large_mid_stream` |
| trailing bytes / decoder errors accepted | `undecodable_compressed_streams_are_export_decompress_and_leave_nothing`, `the_binary_refuses_a_corrupt_compressed_stream_as_export_decompress` |
| window cap removed (2^31) | `undecodable_compressed_streams_…` |
| stored-piece checks skipped for compressed epochs | first run: **survived**; after `a7ffe82`: `a_stored_piece_that_decodes_but_differs_from_its_descriptor_is_corrupt`, `a_compressed_epoch_whose_stored_digest_differs_is_corrupt` |
| role left `artifact` | `export_role_equals_the_locator_role_for_every_short_path`, `sources_one_per_epoch_…`, `a_multi_piece_compressed_epoch_…`, the consumer gate |

The worker's `a_corrupt_stored_piece_of_a_compressed_epoch_is_still_corrupt` flips the first
stored byte, which breaks the zstd frame magic, so a weakened path that skipped the stored checks
still failed with `Corrupt`. The parent added a piece replaced by another valid encoding of the
same bytes (only the stored-digest check refuses it) and an epoch whose recorded SHA-256 is
wrong. 7 of 7 caught.

## Checks on `a7ffe82`

`nix develop .#rust --command sh -c 'cd rust && MEMORY_ANALYTICS_PYTHON=<repo>/.analytics-venv/bin/python
PROPERTY_CASES=500 bash scripts/gates.sh'`: all 14 sub-gates 0, seed 3529537878; 04-nextest 764
passed, 05-hegel 32, 06-proptest 34, 10-consumer 2; coverage 96.56 % (9,829 of 10,179 lines).
The worker ran `RUSTDOCFLAGS=-Dwarnings cargo doc --workspace --no-deps`: clean.

## Limits

- The capture finding above: a rollout recompressed after capture is not captured again until a
  separate brief changes source identity or the manifest rule.
- `memory check` and `memory restore` work on the stored (compressed) bytes, as before.
- Not run: 2,000 property cases, `cargo mutants` (operator decision), a peak-memory measurement of
  a compressed export.
