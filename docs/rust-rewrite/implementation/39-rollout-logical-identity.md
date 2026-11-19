# 39. One logical identity for a Codex rollout, verified by content (2026-11-19)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 38](38-rollout-identity.md) and replaces part of it and all of
[brief 37](37-export-decompress.md). Evidence goes to `evidence/30-rollout-logical-identity/`.
Text that states a plan stays as written; later changes are marked.

The acceptance run `20261117-a28bfa4` keeps running while this brief is worked on. Nothing here is
installed, sent to R2 or run against the run's state root.

## Why

Brief 38 stopped the failures by giving a `.jsonl.zst` rollout its own source id. That gives one
conversation two identities. Because the daemon records no deletions (slice 3), a recompressed or
resumed session then exports twice and the importer counts its events twice (it removes duplicates
only within one transcript); recompression uploads the whole file again, and a resume (new inode)
replays from byte 0. Go uploaded nothing in either case.

**Operator decision, 2026-11-19: follow DDIA.** The record is the logical JSONL event log; `.zst`
is a physical encoding of the same bytes, like a compacted storage segment, so it must not change
identity (DDIA ch. 4, encoding; ch. 11, stable identifiers for idempotence and duplicate
suppression). A new inode is not new content: verify the content (the end-to-end argument, "trust,
but verify") with the existing Adopt digest instead of replaying. Decoding is one more transform
before hashing, as DESIGN 6.2 already keys objects by transformed bytes. Conflicting copies are
reported, not hidden.

This replaces DESIGN D46 (decompress at export) with decompress at capture, and the "own source"
part of D47 with one logical id. D47's "an append source may change path" stays.

## How Codex handles rollouts (`codex-rs/rollout/src/compression.rs`, read 2026-11-18)

- Recompression: only files untouched for 7 days, under Codex's writer lock. It encodes to a temp
  file at level 3 with the pledged source size, verifies the temp decodes, publishes the `.zst`
  with no-clobber, rechecks that the plain file is unchanged, then removes the plain file.
- Resume (`materialize_rollout_for_append`): decodes the `.zst` to a temp, publishes it as the
  plain `.jsonl` (hard link, no-clobber), removes the `.zst`, then appends.
- Both present: one logical rollout with one physical path; **plain wins**, and a `.zst` beside a
  plain file is skipped (`should_skip_compressed_sibling`).
- Codex never appends to a `.zst`.

## Checkout

`main` is `6302a39`. This brief is on `docs/brief-39`. Two increments, sequential, stacked:
VERIFY on `rust/identity-verify` (worktree `../memory-wt/b39-verify`, from `docs/brief-39`), then
ZST on `rust/zst-capture` (worktree `../memory-wt/b39-zst`, from VERIFY's accepted tip). Crate
paths are under `rust/crates/`.

## Facts checked on `6302a39`

| Fact | Where |
|---|---|
| Capture reads local bytes only through `FileSystem::stat` and `FileSystem::read(handle, range)`; `RealFs` is the one real adapter, opened by handle from `register(path)` | `memory/src/environment.rs:20-46`; `memory/src/realfs.rs:51-175` |
| Capture maps every read error to `Unavailable` (retried, not visible) | `memory/src/realfs.rs:28-32`; `memory/src/capture.rs` |
| `memory::capture::Source` carries handle, id, kind, class, the manifest path and privacy facts; `to_source` builds it from `Located` | `memory/src/capture.rs:73-86`; `memory/src/resolve.rs:182-215` |
| `profile_of`: `CodexRollout` is `Transcript`, `CodexCompressed` is `OtherKnown` | `memory/src/resolve.rs:84-112` |
| Planner: an `Indexed` basis whose observed `FileId` differs is `Replay{IdentityChanged}`; an `Unindexed` basis is `Adopt` (hash `[0, captured_to)` locally, compare with the stored whole digest), then `Append` or `Unchanged` with a stat refresh, or `Replay{AdoptMismatch}` | `memory-core/src/planner.rs:194-262` |
| Capture chooses the basis from (index, remote): `(Some, _)` Indexed, `(None, Some)` Unindexed, `(None, None)` Fresh; Adopt reads the prefix in `step`-sized reads through `read_adopted` | `memory/src/capture.rs:1127-1166`, `:1070-1091` |
| `codex_rollout` (brief 38) gives a `.zst` the id `rollouts:<name>.jsonl.zst`; the walk keeps the lexicographically first path per (root, owner, id) and counts the rest as `duplicate` | `memory-core/src/locator.rs:732-754`; `memory/src/resolve.rs:398-440`, `:545-595` |
| `memory export` decompresses `CodexCompressed` epochs (brief 37); before brief 37 (`b435f73`) it streamed stored bytes and mapped `CodexCompressed` to `artifact` | `memory-reader/src/export.rs`; `memory/src/export.rs`; `git diff b435f73 6302a39` |
| No withhold reason, replay reason or source kind name appears in `rust/schemas/*.json` | grep, 2026-11-19 |
| Peak-memory real-binary test with a `LIMIT` for capture and audit | `memory/tests/s3_store/peak_memory.rs` |
| This Mac: 28 plain rollouts, 0 `.zst`, none over 50 MB | 2026-11-19 |

## Decisions

Decided by the parent on 2026-11-19 under the operator's direction (DESIGN D48); each is
reversible before cutover. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Identity change | A source whose observed `FileId` differs from its index entry is verified by content, not replayed: capture plans it on the `Unindexed` basis against the remote position (Adopt). Equal digest of `[0, captured_to)`: the epoch continues (`Unchanged`, or `Append` of only the new bytes) and the stat refresh records the new id. Different digest: a new epoch, as today. With no remote position: `Fresh`. Applies to every append source (a same-prefix rename-over of any agent's file benefits) |
| Rollout id | `rollouts:<name>.jsonl` for all four spellings (plain and `.zst`, `sessions/…` and `archived_sessions/`), one owner (Go's and Codex's rule; brief 38's `.zst` id is reverted) |
| Manifest kind and path | A `.zst` spelling is captured as kind `CodexRollout`, profile `Transcript`, with the logical path (the physical path without `.zst`). `CodexCompressed` stays a locator kind only; no manifest written by capture holds it |
| Encoding | A fact of the physical file, carried from the walk to `RealFs` at `register`, never stored. For a `.zst` handle, `stat` returns the decoded length as `size` (with the physical `FileId` and mtime) and `read` returns decoded bytes. Capture, audit, `check` and hints see only logical bytes |
| Decoder | Streaming zstd, `WindowLogMax(23)` (8 MiB; Codex level 3 needs at most 2^21); one or more whole frames, no trailing bytes, no truncated frame. At most one decoder alive in a `RealFs`; a `read` at or after its position continues it, an earlier start restarts from 0 |
| Decoded length | Found by one streaming decode, cached in memory per handle keyed by (`FileId`, physical size, physical mtime); any change of the key decodes again. Not persisted |
| Undecodable | A new closed reason `undecodable` (`WithholdReason::Undecodable`): the source is withheld, nothing is uploaded, raw bytes never. `RealFs` reports it as a distinct error and the `FileSystem` seam gains a way to classify it (default: not undecodable). `memory check` prints it as `withheld … reason=undecodable` |
| Two files, one id | Plain wins over `.zst` (Codex's rule); among equal encodings the lexicographically first root-relative path wins; the rest are counted as `duplicate` (brief 38 count and check field kept). Same rule in `enumerate`, `of` and `note` |
| Path change | Kept from brief 38 (D47): `Extend` and `Epoch` may change an append source's path; kind and class may not |
| Export | Brief 37 is reverted: `memory export` streams stored bytes again and `CodexCompressed` maps to `artifact` again (as on `b435f73`). No `.zst` epoch exists in storage: the acceptance prefix is deleted at teardown and production holds no data |
| Not changed | Stored formats and schemas, Claude and Pi locators, `restore`, check verdict rules, log lines other than the check line |

## Increments

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | VERIFY | `rust/identity-verify` | worker (Sonnet, medium) |
| 2 | ZST | `rust/zst-capture` | worker (Sonnet, medium), after VERIFY is accepted |

### 1. VERIFY

Owns `memory/src/capture.rs` (basis choice only), `memory-core/src/planner.rs` (doc only, unless
the rule is better expressed there; then report), and tests in `memory/tests/capture.rs`,
`memory-core/tests/core_planner.rs` and a new `memory/tests/s3_store/identity_verify.rs`.

- Proof (each a named test):
  - Capture rig: a captured source replaced by a new `FileId` with the same bytes is `Unchanged`
    with the new id recorded and nothing read again on the next round; with the same prefix and
    more bytes it is one `Extend` from `captured_to`; with a different prefix it is an `Epoch`; a
    shorter file is an `Epoch` (`Truncated`).
  - Daemon against the fake store: a Claude transcript atomically replaced (write temp, rename)
    by its old bytes plus new lines lands one `Extend`; the object PUTs after the replace carry only
    the new bytes (count them); `memory check` matches; the export has one epoch.
- Teeth: identity change replays again (`Replay{IdentityChanged}`); the stat refresh after an
  equal digest dropped (the next round reads again); Adopt digest compared over the wrong range.

### 2. ZST

Owns `memory/src/realfs.rs` (and a new module for the decoder if wanted), `memory/src/resolve.rs`,
`memory/src/environment.rs` (the classification hook), `memory/src/capture.rs` (mapping the
undecodable error only), `memory/src/check.rs` (the reason), `memory-core/src/locator.rs`
(`codex_rollout`, `export_role`), `memory-core/src/domain.rs` (the reason), the brief-37 files to
revert (`memory-reader/src/export.rs`, `memory/src/export.rs`, `memory-reader/tests/export.rs`,
`memory-core/tests/core_export.rs`, the zst tests in `memory/tests/s3_store/export.rs`), and tests
in `memory-core/tests/{core_locator,core_f2_gaps}.rs`, `memory/tests/resolve.rs`,
`memory/tests/s3_store/{rollout_identity,consumer_gate,owners,peak_memory}.rs`.

- Proof (each a named test):
  - Locator: all four spellings share one id and one owner; kinds stay `CodexRollout` and
    `CodexCompressed`.
  - Walk: plain and `.zst` together yield the plain file and `duplicate=1`; `sessions/` and
    `archived_sessions/` plain copies yield the first path; a lone `.zst` yields a source of kind
    `CodexRollout` with the logical path; `of` and `note` agree.
  - `RealFs`: a `.zst` handle's `stat` size is the decoded length; ranged reads (sequential and
    restarting) equal slices of the decoded bytes; two concatenated frames decode; trailing bytes,
    a truncated frame, garbage and a 2^24 window are `undecodable`; a 2^23 window is accepted; the
    cached length is recomputed after the file is replaced.
  - Daemon scenarios (rewrite `s3_store/rollout_identity.rs`); every round `failed=0 parked=0`;
    then the real `memory check` (`mismatch=0 uncaptured=0`) and the real `memory export`:
    - archived then appended: one source, path `archived_sessions/…`, body equals the file.
    - recompressed (plain captured, then replaced by its `.zst`): **no object PUT** after the
      replacement; one live `main` source with the plain path and the JSONL body.
    - `.zst` first (fresh capture decodes), then resumed as a longer plain file: one epoch, one
      `Extend`, object PUT bytes after the resume cover only the new bytes; body equals the file.
    - recompressed then resumed: still one epoch.
    - plain and `.zst` at once: one source, `duplicate=1`.
    - `sessions/` and `archived_sessions/` copies at once: one source, `duplicate=1`.
    - undecodable `.zst`: withheld `undecodable`, no object PUT, check prints the reason.
  - Consumer gate: brief 37's `.jsonl.zst` session stays in the fixture and now arrives as a Codex
    `main` source with the plain path and its events, exported with no decompression in the reader.
  - Peak: a 100 MB JSONL written as `.jsonl.zst` keeps capture and audit under the existing
    `LIMIT` of `peak_memory.rs`.
  - Export: the reader and CLI are byte-identical to `b435f73` apart from what briefs 32 to 36
    needed; `CodexCompressed` is `artifact` again.
- Teeth: `.zst` id keeps its suffix; the raw `.zst` bytes read (no decoding); window cap raised to
  2^27; trailing bytes accepted; precedence reversed (`.zst` over plain); manifest path keeps
  `.zst`; undecodable mapped to `Unavailable` (silent retry) instead of withheld; length cache not
  keyed by physical size and mtime.

## Rules for the workers

The rules of [brief 28](28-production-prerequisites.md#rules-for-workers) apply, with those of
briefs 29 to 38. No new dependency and no feature change (`zstd` is already a dependency of
`memory`). Never inspect, signal or read the state of the running acceptance daemon or anything
under `~/.local/state/memory-acceptance*`; never read `.env`; never contact R2; never use
`git stash`. Cargo only through
`nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && ...'`, one cargo job
at a time. Commit after each working step.

## Acceptance (parent)

Per increment: whole diff read; the teeth above. On the tip: fmt, clippy, rustdoc, nextest,
doctests, schemas, `cargo deny`, lints, `scripts/gates.sh` once with `MEMORY_ANALYTICS_PYTHON`
set; docs (DESIGN sections 5, 6.2, 7.2, 7.3 memory bound, D46 and D47 marked superseded, D48;
`docs/hooks.md`; handoff); push (secret check of the range), hosted gates, merge.

## Ledger

- 2026-11-19 06:50Z: brief written on `6302a39`.
- 2026-11-19 07:02Z: VERIFY accepted locally at `f162fbe` (worker 4 commits). The rule is one
  filter in `capture::append`; the planner's `IdentityChanged` guard stays as its contract.
  Teeth 3 of 3; workspace nextest 777 passed.
- 2026-11-19 07:45Z: ZST accepted locally at `61260d5` (worker 9 commits, parent 1). Deviation
  accepted: `memory check` reports an undecodable source as `withheld … reason=undecodable`
  (exit 0). The tooth "length cache keyed by `FileId` only" first survived; the parent added an
  in-place rewrite test. Teeth 8 of 8. Full gates on `61260d5`: 14 sub-gates 0, seed
  2026967693, coverage 96.37 %; 100 MB `.zst` peak 22.2 MB capture, 20.8 MB audit. Record:
  [evidence 30](evidence/30-rollout-logical-identity/). Next: push, hosted gates, merge.
