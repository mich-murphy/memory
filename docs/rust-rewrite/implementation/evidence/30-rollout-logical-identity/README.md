# Evidence 30: one logical identity for a Codex rollout, verified by content (brief 39)

Dates are UTC; the work ran on 2026-11-19 from 06:37Z to 07:45Z. Status: increments VERIFY
(`f162fbe`, 4 commits over `9324564`) and ZST (`61260d5`, 10 commits over `f162fbe`) accepted
locally. Nothing was installed, sent to R2 or run against the acceptance run's state root. The
acceptance run `20261117-a28bfa4` kept running on its own release binary throughout.

[Brief 39](../../39-rollout-logical-identity.md) sits on `9324564` (on top of `main` `6302a39`).
Operator decision, 2026-11-19: follow DDIA. A rollout's identity is its logical JSONL; `.jsonl.zst`
is a physical encoding, decoded at capture; a new inode is verified by content. This supersedes
DESIGN D46 (decompress at export, brief 37) and the "own source" part of D47 (brief 38).

## Commits

"Worker" is a delegated Sonnet session (medium), "parent" the coordinating session.

| Commit | By | Change |
|---|---|---|
| `b304e91` | worker | VERIFY: `capture.rs` `append` drops an index entry whose `FileId` differs from the observed one, so the planner takes the `Unindexed` basis (Adopt) against the remote position |
| `06c3122` | worker | daemon test: an atomically replaced Claude transcript with the same prefix lands one `Extend` |
| `ec41d7c` | worker | brief 38's resumed-after-recompression scenario now continues one epoch |
| `f162fbe` | worker | capture property: same bytes under a new `FileId` continue the epoch |
| `93f5d44` | worker | ZST: brief 37 reverted (export streams stored bytes, `CodexCompressed` is `artifact`) |
| `0406561` | worker | locator: all four rollout spellings share `rollouts:<name>.jsonl` |
| `867ddcf` | worker | `RealFs`: `Encoding::{Plain, Zstd}`, `register_encoded`, decoded `stat`/`read`, `RealFsError::Undecodable`, `FileSystem::is_undecodable`; new `zstd_stream.rs` |
| `1eeace1` | worker | walk: a `.zst` is captured as `CodexRollout`, profile `Transcript`, logical path, zstd handle; plain wins over `.zst`, then the first path |
| `dc40b17`, `5f2a415` | worker | capture: undecodable is `Withheld(Undecodable)`; test |
| `5a4e12b` | worker | `memory check`: undecodable is `withheld … reason=undecodable`; daemon scenarios rewritten |
| `8cdd416`, `3e35970` | worker | consumer gate on the logical path; peak case; restore comparison decodes |
| `61260d5` | parent | test: the decoded-length cache is recomputed after an in-place rewrite (see teeth) |

### How it works

- **Content verification (VERIFY).** The only production change is one filter in
  `capture::append`: an index entry is used only while the observed `FileId` equals the indexed
  one. Otherwise the planner sees no index and adopts the remote position: it hashes
  `[0, captured_to)` and compares it with the stored whole digest. Equal: `Unchanged` or `Append`
  of only the new bytes, and the adopt index update records the new id. Different:
  `Replay{AdoptMismatch}` (new epoch). A remote position larger than the file: `Truncated`. The
  audit runs only with an index entry, so it never compares against the old basis. The planner's
  `Indexed` `IdentityChanged` guard stays as the pure planner's contract; capture no longer
  reaches it. This applies to every append source (an atomic rewrite of any transcript with the
  same prefix no longer replays).
- **Decoding (ZST).** `zstd_stream::Stream` reads 64 KiB of input and produces 128 KiB of output
  at a time, `WindowLogMax(23)`, whole frames only (reinit between frames), trailing bytes or a
  truncated frame undecodable. `RealFs` keeps at most one live decoder: a `read` at or after its
  position continues it, an earlier start or another handle restarts from 0. The decoded length
  is found by one streaming decode and cached per handle keyed by physical (`FileId`, size,
  mtime); undecodable is cached the same way. The physical-file open checks (NOFOLLOW, regular
  file, the `FileId` of the last stat) are unchanged, and a zstd read also refuses a changed
  size or mtime.
- **Identity.** Locator kinds stay `CodexRollout` and `CodexCompressed`; `to_source` turns a
  `CodexCompressed` into a capture `Source` of kind `CodexRollout` with the logical path, so no
  manifest written by capture holds `CodexCompressed`.

### Worker deviations accepted

- `memory check` maps an undecodable `stat` to `withheld … reason=undecodable` (exit 0, withheld
  is not a failure), beyond the check line the brief named. Without it the check showed
  `unsettled=1 unreadable=1` with no reason.
- After an identity change the adopted entry has never been audited, so the next round audits
  once (existing rule) before rounds read nothing.
- A non-regular `.zst` (symlink, FIFO) keeps the existing `Changed`/`Io` errors.

## Tests

- VERIFY, `capture.rs`: `identity_change_same_bytes_is_unchanged_and_records_the_new_id`,
  `identity_change_same_prefix_more_bytes_is_one_extend_from_captured_to`,
  `identity_change_different_prefix_is_an_epoch`, `identity_change_shorter_file_is_an_epoch`;
  `s3_store::identity_verify::atomic_replace_with_same_prefix_extends_one_epoch` (object PUT
  bytes after the replace decode to exactly the new lines).
- ZST, `realfs.rs`: `zstd_stat_size_is_decoded_length_and_ranged_reads_equal_decoded_slices`,
  `zstd_two_concatenated_frames_decode`,
  `zstd_trailing_bytes_truncated_frame_garbage_and_big_window_are_undecodable`,
  `zstd_window_2_pow_23_is_accepted`, `zstd_cached_length_is_recomputed_after_replacement`,
  `zstd_cached_length_is_recomputed_after_an_in_place_rewrite` (parent).
- `resolve.rs`: `plain_and_zst_yield_the_plain_file_and_one_duplicate` (the `.zst` path sorts
  first, so only the encoding rule keeps the plain file), `zst_copies_in_sessions_and_archive_yield_the_first_path_logically`,
  `a_lone_zst_is_a_codex_rollout_source_with_the_logical_path_and_transcript_profile`.
- `capture.rs`: `undecodable_stat_or_read_is_withheld_with_no_upload`.
- Locator: Codex rows of `core_locator`, `core_f2_gaps::row7c_codex_all_four_spellings_share_one_id_and_one_owner`.
- `s3_store::rollout_identity`, each with `failed=0 parked=0` every round, the real `memory check`
  and the real `memory export`: `archived_then_appended_lands_under_the_archived_path`,
  `recompressed_rollout_puts_nothing_and_stays_one_plain_path_source` (no object PUT),
  `zst_first_then_resumed_plain_extends_one_epoch_with_only_new_bytes`,
  `recompressed_then_resumed_is_still_one_epoch` (every byte uploaded once),
  `plain_and_zst_at_once_land_one_source_with_one_duplicate`,
  `sessions_and_archived_copies_at_once_land_once`,
  `undecodable_zst_is_withheld_with_no_put_and_check_says_why`.
- Consumer gate: brief 37's `.jsonl.zst` session arrives as a Codex `main` source with the plain
  path and 6 events; the reader decompresses nothing.
- Peak: `peak_memory::one_100_mb_zst_rollout_stays_under_128_mb` (worker, release build: capture
  22.2 MB, audit 20.8 MB); it also passed in the gate run below.

## Outcome per Codex action

| Action | Uploaded | Live sources after |
|---|---|---|
| Archive (rename) | nothing; new bytes only if it grows | one |
| Recompress | nothing (verified by digest) | one, plain path |
| Resume, then new turns | only the new bytes | one |
| Plain and `.zst` at once | nothing extra; `duplicate=1` | one |
| Undecodable `.zst` | nothing; withheld `undecodable` | none for that file |

## Teeth (parent)

Scripts in the parent's scratchpads (`verify_teeth.py`, `zst_teeth.py`); each tooth replaced exact
anchors, ran the focused tests and restored the files.

| Tooth | Caught by |
|---|---|
| identity change replays again (filter removed) | the four `identity_change_*` except the two epoch cases, `identity_verify`, `capture_property` (scripted run, hegel, proptest), `recompressed_then_resumed…` predecessor |
| adopt stat refresh dropped | `identity_change_same_bytes_is_unchanged_and_records_the_new_id` |
| adopt digest over the wrong range | `identity_change_same_*`, `identity_verify`, `capture_property` |
| `.zst` id keeps its suffix | `row7c_…`, `matrix_rows_…`, `proptest_property7_…`, `plain_and_zst_yield_…`, four `rollout_identity` scenarios |
| raw `.zst` bytes read (no decoding) | four `rollout_identity` scenarios, the consumer gate |
| window cap 2^27 | `zstd_trailing_bytes_…_big_window_are_undecodable` |
| trailing bytes accepted | the same, `zstd_two_concatenated_frames_decode` |
| precedence reversed (`.zst` over plain) | `plain_and_zst_yield_…`, `plain_and_zst_at_once_…` |
| manifest path keeps `.zst` | `a_lone_zst_…`, `zst_copies_…`, the consumer gate |
| undecodable mapped to `Unavailable` | `undecodable_stat_or_read_is_withheld_with_no_upload` |
| length cache keyed by `FileId` only | first run: **survived** (the worker's test replaces by rename, which changes the inode); after `61260d5`: `zstd_cached_length_is_recomputed_after_an_in_place_rewrite` |

3 of 3 (VERIFY) and 8 of 8 (ZST) caught. A tooth run left an untracked
`core_locator.proptest-regressions`; it was deleted, not committed.

## Checks

- VERIFY tip `f162fbe` (parent): `cargo fmt --check`, clippy `-Dwarnings`, workspace nextest
  without properties and the consumer gate: 777 passed.
- ZST tip `61260d5`: `nix develop .#rust --command sh -c 'cd rust &&
  MEMORY_ANALYTICS_PYTHON=<repo>/.analytics-venv/bin/python PROPERTY_CASES=500 bash
  scripts/gates.sh'`: all 14 sub-gates 0, seed 2026967693; 04-nextest 779 passed, 05-hegel 32,
  06-proptest 34, 10-consumer 2; coverage 96.37 % (9,999 of 10,376 lines); `07-schema` 0 (no
  schema change). The worker ran `RUSTDOCFLAGS=-Dwarnings cargo doc --workspace --no-deps`: clean.

## Limits

- Deletions are still not recorded (slice 3), but no Codex action now depends on them.
- Daily audits of a cold `.zst` decode it; an identity change hashes the whole logical prefix
  once.
- Not run: 2,000 property cases, `cargo mutants` (operator decision), a real recompressed rollout
  (this Mac had none on 2026-11-19).
