# 16. Property 4 driven by the real capture step

Status: implemented and passing (2026-11-09). File: `rust/crates/memory/tests/capture_property.rs`.

## What it is

DESIGN.md 15 property 4 as a state machine in Hegel (hegeltest =0.52.0 stateful API) and
proptest-state-machine. Each reconcile runs the REAL `memory::capture::capture`, then
`memory::commit::commit` against a deterministic fake store. It replaces the stand-in planner
of `commit_protocol.rs`.

**The filesystem is synthetic** (in-memory map behind the `FileSystem` seam) and **the piece
encoding is the identity** (no zstd). No R2, network, credentials, real agent dirs or daemon.

Steps: append, rewrite, truncate, remove source, reconcile, crash before put / after piece /
after manifest, lost reply (AlreadyOurs), 5xx (landed or not), restart (drops locals and base,
recovers through `recover_base`), delete-index (drops append indexes and snapshot caches),
offline/online, revoke/restore token, foreign writer, settle. `Update`s are applied to locals
only after a commit lands (at once when there is no commit).

## Model and oracle independence

- `Reference` holds file bytes and per-source history. Only steps update it; it never reads the
  driver, capture, commit or the store.
- The oracle (`read_chain`, `reassemble`, `observe`) reads store contents only: walks
  `current.json` to a checkpoint, checks manifest hashes, folds with memory-core, verifies each
  descriptor against the stored encoding, reassembles epochs, and checks every epoch is a prefix
  of bytes the source once held. `check` also requires the head never goes backwards and a
  committed manifest is never replaced.
- Liveness (after settle: online, token restored, transient faults cleared, 5 capture+commit
  rounds, enough for two deletion observations): latest epoch equals source bytes where the
  source exists; a removed source that was committed is recorded deleted. Waived once a foreign
  writer was injected (it parks the owner at an unpredictable attempt).

## Guard and controls

`Shapes` counts multi-epoch, restart, crash-after-manifest, lost-reply (AlreadyOurs) and
delete-index; both engines assert `require_all` after the run. `guard_has_negative_controls`
fails the guard for each single missing shape, for all-zero and for empty input;
`guard_shapes_arise_from_a_scripted_run_with_the_real_capture` shows all five arise.
`oracle_detects_tampered_store_contents` has 9 tamper cases plus a positive control (object removed,
encoding differs, current.json seq, 1a history, head backwards, manifest replaced, and three
liveness failures). `removed_source_needs_two_observations_then_is_deleted` is directed.

## Finding in capture.rs (not a test bug)

After a commit that landed but whose reply was lost with verification unreadable
(`LandingUnknown`/`BaseUnchanged`), locals are stale while the remote is ahead. `capture()` then
plans an `Extend` from the stale index and `commit` rejects it with `Invalid(Overlap)`. Minimal
script: append, reconcile, append, lost-reply+offline, settle. The driver works around it by
dropping append indexes and snapshot caches on those failures, so the planner takes the `Adopt`
path. Requested change elsewhere (owner of capture.rs): either document that contract in
capture.rs, or make `append()` treat an index ahead of or behind `remote` as needing adopt.

## Limitations

Two sources, append class only (snapshot class and privacy withholds are covered by
tests/capture.rs). Piece length 4. Liveness is not checked after a foreign writer. Foreign
parking is exempt from liveness, not asserted. Hegel/proptest ran 2000 cases each.

## Parent correction 1 (2026-11-09): stale index fixed in capture(), workaround removed

The worker's `Invalid(Overlap)` finding is a `capture.rs` defect. DESIGN 9.1 and 9.3 treat the
remote committed position as authoritative and the index as a cache that may be deleted. The
parent changed `capture::append` to ignore a local index whose `position` differs from the
remote's latest committed `EpochPosition`, so the planner adopts or replays. The NeedWindows
branch uses the same filtered index. The driver workaround in `tests/capture_property.rs`
(dropping every index and snapshot cache on `LandingUnknown`/`BaseUnchanged`) was removed, so
property 4 now exercises the fix. Mutation check: with the filter reverted, both
`hegel_property4_capture_state_machine` and `proptest_property4_capture_state_machine` fail
(`commit: Invalid(Overlap)`, cargo exit 101). With the filter restored, fmt 0, clippy 0 and
`cargo test -p memory` at 2,000 cases exit 0 (capture_property 6/6). The `capture_property.rs`
hash above is superseded.
