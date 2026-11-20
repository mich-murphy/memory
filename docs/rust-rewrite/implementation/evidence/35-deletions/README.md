# Evidence 35: deleted sources are recorded (brief 43)

All dates are UTC, 2026-11-20, from 08:12Z to about 09:20Z. Status: increment DELETE accepted
locally at `9a71407` (5 worker commits and 1 parent commit over `02ae35f`). Nothing was installed,
sent to R2 or run against the production state root; the production daemon (`1c5618b` build) kept
running throughout.

[Brief 43](../../43-deletions.md) sits on `02ae35f` (on top of `main` `4d3bcba`). Operator request,
2026-11-20: "let's fix this" (deleted files were never recorded as deleted, open since brief 31).

## Root cause (on `4d3bcba`)

Capture already had the two-miss rule (`absence_plan`, then `Change::Deleted`), but a deleted file
never reached it: `reconcile_once` built each owner's list from the walk, which yields only files
that exist; `admit` would have blocked a missing path as `path_unresolvable`; and an owner whose
every file was gone was never visited (`index.json` had no owner list).

## Commits

| Commit | By | Change |
|---|---|---|
| `b9c3ef7` | worker | `capture::absent_into`: `stat`-only probe of candidates (no read, no admission); a miss counts, the second consecutive miss with a live remote entry commits `Change::Deleted`; a seam error changes nothing; a present file resets the count. `Roots::namespace()`; `Sources::walk_complete()` and `Sources::locate_absent()` (resolves the root by the id's namespace prefix and `root.join(entry.path)`, or the `.zst` spelling; a missing root, unknown namespace or bad path is `Probe::Skip`). The daemon probes, only inside `reconcile_once` after a complete walk, stored live append entries that were not walked and are not parked; the deletions join the owner's commit |
| `1d7281e` | worker | `IndexBody.owners` (`#[serde(default)]`, a sorted list because `Owner` is not ordered): every walked owner is kept; owners in the set but not walked are visited with an empty list; an owner leaves the set once its stored state has no live append entry |
| `07413e3` | worker | `round` gains `deleted` (deletions in commits that landed) and `absence_skipped`; real-binary test of two `memory reconcile` runs |
| `8c6adad` | worker | Export keeps deleted sources, every epoch `sealed`; `Envelope.deleted`; stderr `memory: exported sources=<n> deleted=<n> bytes=<n>`; consumer-gate case importing such an envelope |
| `6b72c19` | worker | Module docs |
| `9a71407` | parent | `a_missing_root_deletes_nothing_on_the_real_binary`: the configured root moved away, three `memory reconcile` runs give `deleted=0 absence_skipped=1`, the entry stays live |

### Worker deviations accepted

- `owners` is a `Vec<Owner>` sorted by its debug form (`Owner` is not `Ord`); a `HashSet` in memory.
- New trait methods `walk_complete` and `locate_absent` and the `Probe` enum are the seam for the
  guards; `run_owner` reads a private `absence` flag set only during a full round (clippy refused an
  eighth parameter).
- An owner leaves the set only when its recovered base is `Known` or `Absent`.
- Test files outside the ownership list changed only for renamed text or new round keys
  (`tests/log.rs`, `s3_store/export.rs`, `s3_store/rollout_identity.rs`); the real-binary deletion
  test is in `s3_store/check.rs`; the reader fixture's deleted source is now exported `sealed`.
- The consumer-gate case runs the unchanged importer through an inline script. The importer
  accepted a source with no `active` epoch, so the brief's stop condition did not apply.
- Restore is unchanged: it skips deleted sources (`skipped_deleted`).

## Teeth (parent)

Script `delete_teeth.py` in the parent's scratchpad; each tooth replaced exact anchors, ran the
deletion, absence, owner-set, export and round-line tests of both crates with the consumer gate,
and restored.

| Tooth | Caught by |
|---|---|
| one miss deletes | 10 tests, among them `a_deleted_file_is_recorded_on_the_second_full_round_only` |
| truncated or busy walk guard removed | `a_truncated_or_busy_walk_deletes_nothing` |
| missing-root guard removed (`resolve.rs`) | first survived (the daemon tests use a test resolver); caught by the parent test `a_missing_root_deletes_nothing_on_the_real_binary` |
| a stat error treated as absence | `a_stat_error_is_not_an_absence` |
| parked sources probed | `a_parked_source_is_never_a_candidate` |
| walked ids probed | `a_missing_root_deletes_nothing` |
| owner set never filled | 4 tests, among them `an_owner_whose_every_file_is_gone_is_recorded_deleted_then_leaves_the_set` |
| export drops deleted sources | the reader export test, the consumer-gate case |
| export marks a deleted source `active` | the reader export test, the consumer-gate case |
| `deleted` not counted in the round | 6 tests |

10 of 10 caught.

## Checks

- Worker: fmt, clippy `-Dwarnings`, `cargo doc`, `cargo deny check` ok; workspace nextest with the
  consumer gate (without `hegel_`/`proptest_`): 824 passed; `binary(capture_property)` at 500
  cases: 10 passed.
- Parent, `9a71407`: `nix develop .#rust --command sh -c 'cd rust &&
  MEMORY_ANALYTICS_PYTHON=<repo>/.analytics-venv/bin/python PROPERTY_CASES=500 bash
  scripts/gates.sh'`: all 14 sub-gates 0, seed 4141271690; 04-nextest 822 passed, 05-hegel 32,
  06-proptest 34, 10-consumer 3; coverage 95.86 % (10,686 of 11,147 lines).

## Hosted gates, merge, install

- Stack #206 (PRs #203 to #205): Rust gates green on all three branches; CI failed only in the
  two `govulncheck` steps. Merged as `94230d2`.
- 2026-11-20 09:43:44Z: release build of `94230d2` (sha256 prefix `c79c522f18ae46ce`) put in
  place at `~/.local/libexec/memory/memory` by an atomic rename; the `1c5618b` binary is kept as
  `~/.local/state/memory-cutover/backup/memory-1c5618b`. `launchctl kickstart -k`: `runs = 6`.
- Start round (09:47:38Z): landed 1, failed 0, parked 0, `deleted=0 absence_skipped=0`; no WARN
  or ERROR line. `index.json` now lists 1,078 owners (it loaded as `loaded`: the old index had no
  `owners` field).
- `memory status` against production, exit 0: `state=loaded index=loaded rounds=80`; sources
  `total=6052 current=6052 behind=0 changed=0 unindexed=0`; failures `offline=4`, all other
  classes 0; audit `matched=6053 mismatched=0 ineligible=21`; nothing parked.

## Limits

- Owners deleted before this build, or while `index.json` was lost, are not found (the store has
  no LIST); the same limit as `memory check`. On this Mac `memory check` showed `remote_only=0`
  before the change, so nothing is pending.
- Snapshot-class deletions are slice 3. Bytes are never purged (DESIGN 19).
- Not run: 2,000 property cases, `cargo mutants` (operator decision).
